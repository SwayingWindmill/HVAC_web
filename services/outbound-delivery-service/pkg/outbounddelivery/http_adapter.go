package outbounddelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

var errRedirectRejected = errors.New("delivery redirect rejected")

type AddressResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

type CredentialMaterial struct {
	Authorization string
}

type CredentialResolver interface {
	Resolve(ctx context.Context, tenantID, credentialRef string) (CredentialMaterial, error)
}

type HTTPAdapterOptions struct {
	Resolver    AddressResolver
	DialContext DialContextFunc
	Credentials CredentialResolver
}

type HTTPAdapter struct {
	resolver    AddressResolver
	dialContext DialContextFunc
	credentials CredentialResolver

	mu         sync.Mutex
	semaphores map[string]chan struct{}
}

func NewHTTPAdapter(options HTTPAdapterOptions) *HTTPAdapter {
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialContext := options.DialContext
	if dialContext == nil {
		dialer := &net.Dialer{}
		dialContext = dialer.DialContext
	}
	return &HTTPAdapter{
		resolver: resolver, dialContext: dialContext, credentials: options.Credentials,
		semaphores: map[string]chan struct{}{},
	}
}

func (adapter *HTTPAdapter) Deliver(ctx context.Context, delivery ClaimedDelivery) AdapterResult {
	definition := delivery.Integration
	if err := definition.Validate(); err != nil || !definition.Enabled {
		return AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "INTEGRATION_NOT_DELIVERABLE"}
	}
	if int64(len(delivery.Intent.Payload)) > definition.MaxRequestBytes {
		return AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "REQUEST_BODY_LIMIT_EXCEEDED"}
	}

	release, err := adapter.acquire(ctx, definition)
	if err != nil {
		return AdapterResult{Outcome: OutcomeNotSent, Retryable: true, ErrorCode: "CONCURRENCY_WAIT_CANCELLED"}
	}
	defer release()

	parsed, approved, err := adapter.resolveDestination(ctx, definition)
	if err != nil {
		return AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "DESTINATION_POLICY_REJECTED"}
	}

	credential := CredentialMaterial{}
	if definition.CredentialRef != "" {
		if adapter.credentials == nil {
			return AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "CREDENTIAL_RESOLVER_UNAVAILABLE"}
		}
		credential, err = adapter.credentials.Resolve(ctx, definition.TenantID, definition.CredentialRef)
		if err != nil {
			return AdapterResult{Outcome: OutcomeNotSent, Retryable: true, ErrorCode: "CREDENTIAL_RESOLUTION_FAILED"}
		}
	}

	requestCtx, cancel := context.WithTimeout(ctx, definition.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, parsed.String(), bytes.NewReader(delivery.Intent.Payload))
	if err != nil {
		return AdapterResult{Outcome: OutcomeNotSent, ErrorCode: "REQUEST_BUILD_FAILED"}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", delivery.Intent.IdempotencyKey)
	if credential.Authorization != "" {
		request.Header.Set("Authorization", credential.Authorization)
	}

	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           adapter.validatedDialContext(parsed.Hostname(), approved),
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   definition.Timeout,
		ResponseHeaderTimeout: definition.Timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   definition.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errRedirectRejected
		},
	}

	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errRedirectRejected) {
			return AdapterResult{Outcome: OutcomeMaybeSent, ErrorCode: "REDIRECT_REJECTED"}
		}
		var beforeSend *preSendError
		if errors.As(err, &beforeSend) {
			return AdapterResult{Outcome: OutcomeNotSent, Retryable: true, ErrorCode: "CONNECT_FAILED_BEFORE_SEND"}
		}
		return AdapterResult{Outcome: OutcomeMaybeSent, ErrorCode: "TRANSPORT_OUTCOME_UNKNOWN"}
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, definition.MaxResponseBytes+1))
	if err != nil {
		return AdapterResult{Outcome: OutcomeMaybeSent, HTTPStatus: response.StatusCode, ErrorCode: "RESPONSE_READ_OUTCOME_UNKNOWN"}
	}
	providerRequestID := boundedHeader(response.Header.Get("X-Request-Id"))
	if int64(len(body)) > definition.MaxResponseBytes {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return AdapterResult{Outcome: OutcomeAcceptedNotConfirmed, HTTPStatus: response.StatusCode, ProviderRequestID: providerRequestID, ErrorCode: "RESPONSE_BODY_LIMIT_EXCEEDED"}
		}
		return AdapterResult{Outcome: OutcomeMaybeSent, HTTPStatus: response.StatusCode, ProviderRequestID: providerRequestID, ErrorCode: "RESPONSE_BODY_LIMIT_EXCEEDED"}
	}
	responseDigest := PayloadDigest(body)

	result := AdapterResult{HTTPStatus: response.StatusCode, ProviderRequestID: providerRequestID, ResponseDigest: responseDigest}
	switch {
	case response.StatusCode == http.StatusAccepted:
		result.Outcome = OutcomeAcceptedNotConfirmed
		result.ErrorCode = "PROVIDER_ACCEPTED_NOT_CONFIRMED"
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.Outcome = OutcomeDelivered
	case response.StatusCode >= 300 && response.StatusCode < 400:
		result.Outcome = OutcomeFailed
		result.ErrorCode = "REDIRECT_RESPONSE_REJECTED"
	case response.StatusCode >= 400 && response.StatusCode < 500:
		result.Outcome = OutcomeFailed
		result.ErrorCode = "HTTP_CLIENT_REJECTED"
	default:
		result.Outcome = OutcomeMaybeSent
		result.ErrorCode = "HTTP_SERVER_OUTCOME_UNKNOWN"
	}
	return result
}

func (adapter *HTTPAdapter) acquire(ctx context.Context, definition IntegrationDefinition) (func(), error) {
	key := definition.ID + ":" + strconv.FormatUint(definition.Revision, 10)
	adapter.mu.Lock()
	semaphore := adapter.semaphores[key]
	if semaphore == nil {
		semaphore = make(chan struct{}, definition.MaxConcurrency)
		adapter.semaphores[key] = semaphore
	}
	adapter.mu.Unlock()

	select {
	case semaphore <- struct{}{}:
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (adapter *HTTPAdapter) resolveDestination(ctx context.Context, definition IntegrationDefinition) (*url.URL, []netip.Addr, error) {
	parsed, err := url.Parse(definition.DestinationURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, nil, errors.New("invalid destination URL")
	}
	if !hostAllowed(parsed.Hostname(), definition.AllowedHosts) {
		return nil, nil, errors.New("destination host is not allowed")
	}

	var addresses []netip.Addr
	if literal, parseErr := netip.ParseAddr(parsed.Hostname()); parseErr == nil {
		addresses = []netip.Addr{literal}
	} else {
		addresses, err = adapter.resolver.LookupNetIP(ctx, "ip", parsed.Hostname())
		if err != nil {
			return nil, nil, fmt.Errorf("resolve destination: %w", err)
		}
	}
	if len(addresses) == 0 {
		return nil, nil, errors.New("destination resolved to no addresses")
	}
	approved := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !isPublicDestination(address) {
			return nil, nil, fmt.Errorf("destination resolved to blocked address %s", address)
		}
		approved = append(approved, address)
	}
	return parsed, approved, nil
}

func (adapter *HTTPAdapter) validatedDialContext(expectedHost string, approved []netip.Addr) DialContextFunc {
	expectedHost = strings.ToLower(strings.TrimSuffix(expectedHost, "."))
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, &preSendError{err: err}
		}
		if strings.ToLower(strings.TrimSuffix(host, ".")) != expectedHost {
			return nil, &preSendError{err: errors.New("transport attempted an unvalidated destination host")}
		}
		var lastErr error
		for _, approvedAddress := range approved {
			connection, dialErr := adapter.dialContext(ctx, network, net.JoinHostPort(approvedAddress.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, &preSendError{err: lastErr}
	}
}

type preSendError struct {
	err error
}

func (err *preSendError) Error() string {
	if err.err == nil {
		return "delivery connection failed before request send"
	}
	return err.err.Error()
}

func (err *preSendError) Unwrap() error { return err.err }

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

func isPublicDestination(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	return !carrierGradeNAT.Contains(address)
}

func boundedHeader(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}
