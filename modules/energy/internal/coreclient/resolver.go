package coreclient

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

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

const (
	maximumResponseBodySize = int64(1 << 20)
	workloadDecisionPath    = "/internal/v1/registry/workload-decision"
	// grantRefreshMargin renews a grant while enough of it remains for the
	// Core call it is about to authorize.
	grantRefreshMargin = 10 * time.Second
)

type Config struct {
	BaseURL    string
	IAMURL     string
	HTTPClient *http.Client
	Now        func() time.Time
}

// Resolver resolves meter bindings from Core Registry as the projector's own
// Workload Principal, holding one short-lived IAM grant per Tenant (ADR 0017). The
// projector calls it sequentially, so the grant cache is not synchronized.
type Resolver struct {
	endpoint    *url.URL
	iamEndpoint *url.URL
	httpClient  *http.Client
	now         func() time.Time

	grants map[string]workloadGrant
}

type workloadGrant struct {
	token     string
	expiresAt time.Time
}

type resolveResponse struct {
	Status            string     `json:"status"`
	TenantID          string     `json:"tenantId"`
	SiteID            string     `json:"siteId"`
	MeterID           string     `json:"meterId"`
	MeterBindingID    string     `json:"meterBindingId"`
	TopologyVersionID string     `json:"topologyVersionId"`
	BindingVersion    int64      `json:"bindingVersion"`
	BindingRevision   int64      `json:"revision"`
	EnergyTypeID      string     `json:"energyTypeId"`
	EnergyType        string     `json:"energyType"`
	MeterRole         string     `json:"meterRole"`
	Direction         string     `json:"direction"`
	DeviceID          string     `json:"deviceId"`
	PointID           string     `json:"pointId"`
	PointType         string     `json:"pointType"`
	EffectiveFrom     time.Time  `json:"effectiveFrom"`
	EffectiveTo       *time.Time `json:"effectiveTo"`
}

func NewResolver(config Config) (*Resolver, error) {
	endpoint, err := parseOrigin(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("Core Registry resolver base URL: %w", err)
	}
	iamEndpoint, err := parseOrigin(config.IAMURL)
	if err != nil {
		return nil, fmt.Errorf("Core Registry resolver IAM URL: %w", err)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Resolver{endpoint: endpoint, iamEndpoint: iamEndpoint, httpClient: config.HTTPClient, now: config.Now, grants: map[string]workloadGrant{}}, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("must be an HTTP(S) origin")
	}
	return endpoint, nil
}

func (resolver *Resolver) Resolve(ctx context.Context, input energy.BindingResolveInput) (energy.BindingResolution, error) {
	grant, err := resolver.grantFor(ctx, input.TenantID)
	if err != nil {
		return energy.BindingResolution{}, err
	}
	endpoint := *resolver.endpoint
	endpoint.Path = "/internal/v1/registry/sites/" + url.PathEscape(input.SiteID) + "/meter-bindings/resolve"
	query := endpoint.Query()
	query.Set("deviceId", input.DeviceID)
	query.Set("pointId", input.PointID)
	query.Set("sampledAt", input.SampledAt.UTC().Format(time.RFC3339Nano))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return energy.BindingResolution{}, fmt.Errorf("create Core meter binding resolution request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	payload, err := resolver.exchange(request, "Core meter binding resolution")
	if err != nil {
		return energy.BindingResolution{}, err
	}
	var decoded resolveResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return energy.BindingResolution{}, fmt.Errorf("decode Core meter binding resolution: %w", err)
	}
	if decoded.BindingVersion < 0 || decoded.BindingRevision < 0 {
		return energy.BindingResolution{}, errors.New("Core meter binding resolution version is invalid")
	}
	return energy.BindingResolution{
		Status: energy.BindingResolutionStatus(decoded.Status), TenantID: decoded.TenantID, SiteID: decoded.SiteID,
		MeterID: decoded.MeterID, MeterBindingID: decoded.MeterBindingID, TopologyVersionID: decoded.TopologyVersionID,
		BindingVersion: uint64(decoded.BindingVersion), BindingRevision: uint64(decoded.BindingRevision), EnergyTypeID: decoded.EnergyTypeID, EnergyType: decoded.EnergyType,
		MeterRole: decoded.MeterRole, Direction: decoded.Direction, DeviceID: decoded.DeviceID, PointID: decoded.PointID,
		PointType: decoded.PointType, EffectiveFrom: decoded.EffectiveFrom, EffectiveTo: decoded.EffectiveTo,
	}, nil
}

func (resolver *Resolver) grantFor(ctx context.Context, tenantID string) (string, error) {
	if held, ok := resolver.grants[tenantID]; ok && resolver.now().Add(grantRefreshMargin).Before(held.expiresAt) {
		return held.token, nil
	}
	body, err := json.Marshal(registryauth.DecisionRequest{TenantID: tenantID, Action: registryauth.ActionMeterBindingResolve})
	if err != nil {
		return "", fmt.Errorf("encode Registry workload decision request: %w", err)
	}
	endpoint := *resolver.iamEndpoint
	endpoint.Path = workloadDecisionPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create Registry workload decision request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	payload, err := resolver.exchange(request, "Registry workload decision")
	if err != nil {
		return "", err
	}
	var response registryauth.DecisionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", fmt.Errorf("decode Registry workload decision: %w", err)
	}
	if !response.Decision.Allowed {
		return "", fmt.Errorf("IAM denied Registry meter binding resolution for tenant %s: %s", tenantID, response.Decision.ReasonCode)
	}
	expiresAt, err := time.Parse(time.RFC3339, response.DelegationGrantExpiresAt)
	if err != nil {
		return "", fmt.Errorf("decode Registry workload grant expiry: %w", err)
	}
	resolver.grants[tenantID] = workloadGrant{token: response.DelegationGrant, expiresAt: expiresAt}
	return response.DelegationGrant, nil
}

func (resolver *Resolver) exchange(request *http.Request, operation string) ([]byte, error) {
	response, err := resolver.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", operation, err)
	}
	if int64(len(payload)) > maximumResponseBodySize {
		return nil, fmt.Errorf("%s response exceeds 1 MiB", operation)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(payload))
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("%s returned %d: %s", operation, response.StatusCode, message)
	}
	return payload, nil
}

var _ energy.BindingResolver = (*Resolver)(nil)
