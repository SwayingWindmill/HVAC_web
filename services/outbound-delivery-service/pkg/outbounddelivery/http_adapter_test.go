package outbounddelivery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticResolver struct {
	addresses []netip.Addr
}

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), nil
}

type staticCredentialResolver struct {
	material CredentialMaterial
}

func (resolver staticCredentialResolver) Resolve(context.Context, string, string) (CredentialMaterial, error) {
	return resolver.material, nil
}

func TestHTTPAdapterRejectsPrivateAndMetadataDestinationsBeforeDial(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1"} {
		t.Run(blocked, func(t *testing.T) {
			var dialed atomic.Bool
			adapter := NewHTTPAdapter(HTTPAdapterOptions{
				Resolver: staticResolver{addresses: []netip.Addr{netip.MustParseAddr(blocked)}},
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed.Store(true)
					return nil, nil
				},
			})
			result := adapter.Deliver(context.Background(), testClaim("http://delivery.example/hook"))
			if result.Outcome != OutcomeNotSent || result.ErrorCode != "DESTINATION_POLICY_REJECTED" {
				t.Fatalf("result = %#v", result)
			}
			if dialed.Load() {
				t.Fatal("blocked destination reached dial boundary")
			}
		})
	}
}

func TestHTTPAdapterPinsValidatedAddressAndInjectsCredentialAtSendBoundary(t *testing.T) {
	const credential = "Bearer CREDENTIAL_CANARY"
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		if request.Header.Get("Authorization") != credential {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Idempotency-Key") != "intent-key" {
			t.Fatalf("idempotency header = %q", request.Header.Get("Idempotency-Key"))
		}
		response.Header().Set("X-Request-Id", "provider-123")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	validatedIP := netip.MustParseAddr("93.184.216.34")
	baseDialer := &net.Dialer{}
	var dialAddress string
	adapter := NewHTTPAdapter(HTTPAdapterOptions{
		Resolver: staticResolver{addresses: []netip.Addr{validatedIP}},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialAddress = address
			return baseDialer.DialContext(ctx, network, serverURL.Host)
		},
		Credentials: staticCredentialResolver{material: CredentialMaterial{Authorization: credential}},
	})
	claim := testClaim("http://delivery.example:" + serverURL.Port() + "/hook")
	claim.Integration.CredentialRef = "credential-ref-1"
	result := adapter.Deliver(context.Background(), claim)
	if result.Outcome != OutcomeDelivered || result.ProviderRequestID != "provider-123" || result.HTTPStatus != http.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	if !strings.HasPrefix(dialAddress, validatedIP.String()+":") {
		t.Fatalf("dial address = %q, want validated IP", dialAddress)
	}
	if strings.Contains(result.ErrorCode+result.ProviderRequestID+result.ResponseDigest, "CREDENTIAL_CANARY") {
		t.Fatal("credential leaked into delivery evidence")
	}
	if requestCount.Load() != 1 {
		t.Fatalf("request count = %d", requestCount.Load())
	}
}

func TestHTTPAdapterRejectsRedirectAndTreatsAcceptedAsUnconfirmed(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			response.Header().Set("Location", "http://elsewhere.example/next")
			response.WriteHeader(http.StatusFound)
		}))
		defer server.Close()
		adapter, claim := localRoutedAdapter(t, server.URL)
		result := adapter.Deliver(context.Background(), claim)
		if result.Outcome != OutcomeMaybeSent || result.ErrorCode != "REDIRECT_REJECTED" {
			t.Fatalf("result = %#v", result)
		}
		if hits.Load() != 1 {
			t.Fatalf("redirect caused %d requests", hits.Load())
		}
	})

	t.Run("accepted", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()
		adapter, claim := localRoutedAdapter(t, server.URL)
		result := adapter.Deliver(context.Background(), claim)
		if result.Outcome != OutcomeAcceptedNotConfirmed {
			t.Fatalf("result = %#v", result)
		}
	})
}

func localRoutedAdapter(t *testing.T, serverURL string) (*HTTPAdapter, ClaimedDelivery) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	validatedIP := netip.MustParseAddr("93.184.216.34")
	baseDialer := &net.Dialer{}
	adapter := NewHTTPAdapter(HTTPAdapterOptions{
		Resolver: staticResolver{addresses: []netip.Addr{validatedIP}},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return baseDialer.DialContext(ctx, network, parsed.Host)
		},
	})
	return adapter, testClaim("http://delivery.example:" + parsed.Port() + "/hook")
}

func testClaim(destination string) ClaimedDelivery {
	return ClaimedDelivery{
		Intent: DeliveryIntent{
			TenantID:       "0190f000-0000-7000-8000-000000000001",
			Payload:        []byte(`{"event":"alarm"}`),
			IdempotencyKey: "intent-key",
		},
		Integration: IntegrationDefinition{
			ID:               "0190f000-0000-7000-8000-000000000010",
			TenantID:         "0190f000-0000-7000-8000-000000000001",
			Name:             "test webhook",
			Revision:         1,
			AdapterType:      AdapterRESTWebhook,
			DestinationURL:   destination,
			AllowedHosts:     []string{"delivery.example"},
			Enabled:          true,
			MaxRequestBytes:  MaxRequestBodyBytes,
			MaxResponseBytes: MaxResponseBodyBytes,
			Timeout:          2 * time.Second,
			MaxConcurrency:   1,
			MaxAttempts:      3,
			RetryDelay:       time.Second,
			CreatedAt:        time.Date(2026, 8, 19, 4, 0, 0, 0, time.UTC),
		},
	}
}
