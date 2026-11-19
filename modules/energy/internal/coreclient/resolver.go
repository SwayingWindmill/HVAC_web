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
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
)

const (
	maximumResponseBodySize = int64(1 << 20)
	workloadDecisionPath    = "/internal/v1/registry/workload-decision"
	grantRefreshMargin      = 10 * time.Second
	defaultMeterBindingTTL  = 30 * time.Second
)

type Config struct {
	BaseURL    string
	IAMURL     string
	HTTPClient *http.Client
	Now        func() time.Time
	CacheTTL   time.Duration
}

// Resolver resolves meter bindings from Core Registry as the projector's own
// Workload Principal, caching per-Site active COUNTER bindings and short-lived IAM grants (ADR 0017).
type Resolver struct {
	endpoint    *url.URL
	iamEndpoint *url.URL
	httpClient  *http.Client
	now         func() time.Time
	cacheTTL    time.Duration

	mu              sync.Mutex
	grants          map[grantKey]workloadGrant
	siteCache       map[siteCacheKey]*siteBindingCacheEntry
	missRefreshedAt map[sitePointCacheKey]time.Time
}

// grantKey is one IAM grant: a Tenant and the Registry action it authorizes.
type grantKey struct {
	tenantID string
	action   registryauth.Action
}

type workloadGrant struct {
	token     string
	expiresAt time.Time
}

type siteCacheKey struct {
	tenantID string
	siteID   string
}

type pointCacheKey struct {
	deviceID string
	pointID  string
}

type sitePointCacheKey struct {
	tenantID string
	siteID   string
	deviceID string
	pointID  string
}

type siteBindingCacheEntry struct {
	fetchedAt time.Time
	byPoint   map[pointCacheKey][]energy.BindingResolution
}

type bindingItem struct {
	Status            string                `json:"status,omitempty"`
	TenantID          string                `json:"tenantId"`
	SiteID            string                `json:"siteId"`
	MeterID           string                `json:"meterId"`
	MeterBindingID    string                `json:"meterBindingId"`
	TopologyVersionID string                `json:"topologyVersionId"`
	BindingVersion    int64                 `json:"bindingVersion"`
	BindingRevision   int64                 `json:"revision"`
	EnergyTypeID      string                `json:"energyTypeId"`
	EnergyType        string                `json:"energyType"`
	MeterRole         string                `json:"meterRole"`
	Direction         string                `json:"direction"`
	AccountingScope   string                `json:"accountingScope"`
	LoadDeviceID      string                `json:"loadDeviceId"`
	DeviceID          string                `json:"deviceId"`
	PointID           string                `json:"pointId"`
	PointType         string                `json:"pointType"`
	ReadingUnit       string                `json:"readingUnit,omitempty"`
	RatioVersions     []energy.RatioVersion `json:"ratioVersions,omitempty"`
	EffectiveFrom     time.Time             `json:"effectiveFrom"`
	EffectiveTo       *time.Time            `json:"effectiveTo"`
}

type siteMeterBindingsResponse struct {
	SiteID string        `json:"siteId"`
	Items  []bindingItem `json:"items"`
}

func NewResolver(config Config) (*Resolver, error) {
	endpoint, err := parseOrigin(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("core registry resolver base URL: %w", err)
	}
	iamEndpoint, err := parseOrigin(config.IAMURL)
	if err != nil {
		return nil, fmt.Errorf("core registry resolver IAM URL: %w", err)
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
	cacheTTL := config.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultMeterBindingTTL
	}
	return &Resolver{
		endpoint:        endpoint,
		iamEndpoint:     iamEndpoint,
		httpClient:      config.HTTPClient,
		now:             config.Now,
		cacheTTL:        cacheTTL,
		grants:          map[grantKey]workloadGrant{},
		siteCache:       map[siteCacheKey]*siteBindingCacheEntry{},
		missRefreshedAt: map[sitePointCacheKey]time.Time{},
	}, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("must be an HTTP(S) origin")
	}
	return endpoint, nil
}

func (resolver *Resolver) Resolve(ctx context.Context, input energy.BindingResolveInput) (energy.BindingResolution, error) {
	key := siteCacheKey{tenantID: input.TenantID, siteID: input.SiteID}
	point := pointCacheKey{deviceID: input.DeviceID, pointID: input.PointID}
	now := resolver.now().UTC()

	resolver.mu.Lock()
	entry := resolver.siteCache[key]
	resolver.mu.Unlock()

	if entry == nil || !now.Before(entry.fetchedAt.Add(resolver.cacheTTL)) {
		fetched, err := resolver.fetchSiteBindings(ctx, input.TenantID, input.SiteID, now)
		if err != nil {
			return energy.BindingResolution{}, err
		}
		entry = fetched
		resolver.mu.Lock()
		resolver.siteCache[key] = entry
		for missKey, last := range resolver.missRefreshedAt {
			if !now.Before(last.Add(resolver.cacheTTL)) {
				delete(resolver.missRefreshedAt, missKey)
			}
		}
		resolver.mu.Unlock()
	}

	resolution := energy.MatchMeterBindings(entry.byPoint[point], input.SampledAt)
	if resolution.Status == energy.BindingNoMatch && now.After(entry.fetchedAt) {
		missKey := sitePointCacheKey{
			tenantID: input.TenantID,
			siteID:   input.SiteID,
			deviceID: input.DeviceID,
			pointID:  input.PointID,
		}
		resolver.mu.Lock()
		lastMiss, seen := resolver.missRefreshedAt[missKey]
		canRefresh := !seen || !now.Before(lastMiss.Add(resolver.cacheTTL))
		resolver.mu.Unlock()
		if canRefresh {
			fetched, err := resolver.fetchSiteBindings(ctx, input.TenantID, input.SiteID, now)
			if err != nil {
				return energy.BindingResolution{}, err
			}
			resolver.mu.Lock()
			resolver.siteCache[key] = fetched
			resolver.missRefreshedAt[missKey] = now
			resolver.mu.Unlock()
			resolution = energy.MatchMeterBindings(fetched.byPoint[point], input.SampledAt)
		}
	}
	return resolution, nil
}

func (resolver *Resolver) fetchSiteBindings(ctx context.Context, tenantID, siteID string, fetchedAt time.Time) (*siteBindingCacheEntry, error) {
	grant, err := resolver.grantFor(ctx, tenantID, registryauth.ActionMeterBindingResolve)
	if err != nil {
		return nil, err
	}
	endpoint := *resolver.endpoint
	endpoint.Path = "/internal/v1/registry/sites/" + url.PathEscape(siteID) + "/meter-bindings"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Core site meter bindings request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Delegation-Grant", grant)
	payload, err := resolver.exchange(request, "Core site meter bindings read")
	if err != nil {
		return nil, err
	}
	var decoded siteMeterBindingsResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode Core site meter bindings: %w", err)
	}
	if decoded.SiteID != siteID {
		return nil, errors.New("core site meter bindings response site mismatch")
	}
	byPoint := make(map[pointCacheKey][]energy.BindingResolution, len(decoded.Items))
	for _, item := range decoded.Items {
		if item.BindingVersion < 0 || item.BindingRevision < 0 || item.DeviceID == "" || item.PointID == "" || item.EffectiveFrom.IsZero() {
			return nil, errors.New("core site meter binding entry is invalid")
		}
		pk := pointCacheKey{deviceID: item.DeviceID, pointID: item.PointID}
		byPoint[pk] = append(byPoint[pk], energy.BindingResolution{
			TenantID:          item.TenantID,
			SiteID:            item.SiteID,
			MeterID:           item.MeterID,
			MeterBindingID:    item.MeterBindingID,
			TopologyVersionID: item.TopologyVersionID,
			BindingVersion:    uint64(item.BindingVersion),
			BindingRevision:   uint64(item.BindingRevision),
			EnergyTypeID:      item.EnergyTypeID,
			EnergyType:        item.EnergyType,
			MeterRole:         item.MeterRole,
			Direction:         item.Direction,
			AccountingScope:   item.AccountingScope,
			LoadDeviceID:      item.LoadDeviceID,
			DeviceID:          item.DeviceID,
			PointID:           item.PointID,
			PointType:         item.PointType,
			ReadingUnit:       item.ReadingUnit,
			RatioVersions:     normalizeRatioVersions(item.RatioVersions),
			EffectiveFrom:     item.EffectiveFrom.UTC(),
			EffectiveTo:       utcPtr(item.EffectiveTo),
		})
	}
	return &siteBindingCacheEntry{
		fetchedAt: fetchedAt,
		byPoint:   byPoint,
	}, nil
}

func normalizeRatioVersions(versions []energy.RatioVersion) []energy.RatioVersion {
	if len(versions) == 0 {
		return nil
	}
	out := make([]energy.RatioVersion, len(versions))
	for i, version := range versions {
		version.EffectiveFrom = version.EffectiveFrom.UTC()
		version.EffectiveTo = utcPtr(version.EffectiveTo)
		out[i] = version
	}
	return out
}

func utcPtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func (resolver *Resolver) grantFor(ctx context.Context, tenantID string, action registryauth.Action) (string, error) {
	key := grantKey{tenantID: tenantID, action: action}
	resolver.mu.Lock()
	held, ok := resolver.grants[key]
	resolver.mu.Unlock()
	if ok && resolver.now().Add(grantRefreshMargin).Before(held.expiresAt) {
		return held.token, nil
	}
	body, err := json.Marshal(registryauth.DecisionRequest{TenantID: tenantID, Action: action})
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
		return "", fmt.Errorf("IAM denied Registry %s for tenant %s: %s", action, tenantID, response.Decision.ReasonCode)
	}
	expiresAt, err := time.Parse(time.RFC3339, response.DelegationGrantExpiresAt)
	if err != nil {
		return "", fmt.Errorf("decode Registry workload grant expiry: %w", err)
	}
	resolver.mu.Lock()
	resolver.grants[key] = workloadGrant{token: response.DelegationGrant, expiresAt: expiresAt}
	resolver.mu.Unlock()
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
