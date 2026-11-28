package registryauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumGrantStatusResponse = 64 << 10

var ErrStatusFailed = errors.New("registry grant status unavailable")

type GrantStatusProvider interface {
	Lookup(context.Context, GrantClaims) (GrantStatus, error)
}

type StaticGrantStatusProvider struct {
	PolicyRevision  string
	RevokedTokenIDs map[string]struct{}
	Err             error
}

func (provider StaticGrantStatusProvider) Lookup(_ context.Context, claims GrantClaims) (GrantStatus, error) {
	if provider.Err != nil {
		return GrantStatus{}, provider.Err
	}
	_, revoked := provider.RevokedTokenIDs[claims.TokenID]
	return GrantStatus{CurrentPolicyRevision: provider.PolicyRevision, Revoked: revoked}, nil
}

type HTTPGrantStatusProvider struct {
	endpoint string
	client   *http.Client
}

// NewHTTPGrantStatusProvider builds a fail-closed, redirect-disabled HTTPS client
// querying IAM's registry grant status endpoint.
func NewHTTPGrantStatusProvider(endpoint string, client *http.Client) (*HTTPGrantStatusProvider, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("IAM grant status endpoint must be an HTTPS origin")
	}
	if client == nil {
		return nil, errors.New("IAM grant status HTTP client is required")
	}
	copyClient := *client
	copyClient.Jar = nil
	if copyClient.Timeout <= 0 || copyClient.Timeout > 5*time.Second {
		copyClient.Timeout = 5 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPGrantStatusProvider{endpoint: strings.TrimRight(endpoint, "/") + GrantStatusPath, client: &copyClient}, nil
}

func (provider *HTTPGrantStatusProvider) Lookup(ctx context.Context, claims GrantClaims) (GrantStatus, error) {
	if provider == nil || provider.client == nil {
		return GrantStatus{}, ErrStatusFailed
	}
	statusRequest := GrantStatusRequest{TenantID: claims.TenantID, TokenID: claims.TokenID}
	if err := statusRequest.Validate(); err != nil {
		return GrantStatus{}, ErrStatusFailed
	}
	payload, err := json.Marshal(statusRequest)
	if err != nil {
		return GrantStatus{}, fmt.Errorf("marshal IAM grant status request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, bytes.NewReader(payload))
	if err != nil {
		return GrantStatus{}, fmt.Errorf("create IAM grant status request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return GrantStatus{}, fmt.Errorf("query IAM grant status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		return GrantStatus{}, ErrStatusFailed
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximumGrantStatusResponse+1))
	if err != nil || len(content) > maximumGrantStatusResponse {
		return GrantStatus{}, ErrStatusFailed
	}
	var status GrantStatus
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil || strings.TrimSpace(status.CurrentPolicyRevision) == "" {
		return GrantStatus{}, ErrStatusFailed
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return GrantStatus{}, ErrStatusFailed
	}
	return status, nil
}

var _ GrantStatusProvider = (*HTTPGrantStatusProvider)(nil)
var _ GrantStatusProvider = StaticGrantStatusProvider{}
