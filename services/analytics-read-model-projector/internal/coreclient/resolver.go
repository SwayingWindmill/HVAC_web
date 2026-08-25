package coreclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

const maximumResponseBodySize = int64(1 << 20)

type Config struct {
	BaseURL    string
	Grant      string
	GrantFile  string
	HTTPClient *http.Client
}

type Resolver struct {
	endpoint   *url.URL
	grant      string
	grantFile  string
	httpClient *http.Client
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
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("Core Registry resolver base URL must be an HTTP(S) origin")
	}
	if strings.TrimSpace(config.Grant) == "" && strings.TrimSpace(config.GrantFile) == "" {
		return nil, errors.New("Core Registry resolver grant or grant file is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Resolver{endpoint: endpoint, grant: strings.TrimSpace(config.Grant), grantFile: strings.TrimSpace(config.GrantFile), httpClient: config.HTTPClient}, nil
}

func (resolver *Resolver) Resolve(ctx context.Context, input energy.BindingResolveInput) (energy.BindingResolution, error) {
	if resolver == nil || resolver.endpoint == nil || resolver.httpClient == nil {
		return energy.BindingResolution{}, errors.New("Core Registry resolver is closed")
	}
	grant, err := resolver.readGrant()
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
	response, err := resolver.httpClient.Do(request)
	if err != nil {
		return energy.BindingResolution{}, fmt.Errorf("resolve Core meter binding: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBodySize+1))
	if err != nil {
		return energy.BindingResolution{}, fmt.Errorf("read Core meter binding resolution: %w", err)
	}
	if int64(len(payload)) > maximumResponseBodySize {
		return energy.BindingResolution{}, errors.New("Core meter binding resolution response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(payload))
		if len(message) > 512 {
			message = message[:512]
		}
		return energy.BindingResolution{}, fmt.Errorf("Core meter binding resolution returned %d: %s", response.StatusCode, message)
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

func (resolver *Resolver) readGrant() (string, error) {
	if resolver.grantFile != "" {
		content, err := os.ReadFile(resolver.grantFile)
		if err != nil {
			return "", fmt.Errorf("read Core Registry grant file: %w", err)
		}
		grant := strings.TrimSpace(string(content))
		if grant == "" {
			return "", errors.New("Core Registry grant file is empty")
		}
		return grant, nil
	}
	return resolver.grant, nil
}

var _ energy.BindingResolver = (*Resolver)(nil)
