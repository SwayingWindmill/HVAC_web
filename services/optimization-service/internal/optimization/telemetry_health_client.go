package optimization

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

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

const InternalOptimizationPointHealthPath = "/internal/v1/telemetry/optimization/point-health:check"

type TelemetryLivePoint struct {
	DeviceID         string    `json:"deviceId"`
	TelemetryKey     string    `json:"telemetryKey"`
	NumericValue     *float64  `json:"numericValue,omitempty"`
	BoolValue        *bool     `json:"boolValue,omitempty"`
	TextValue        *string   `json:"textValue,omitempty"`
	Unit             string    `json:"unit,omitempty"`
	Quality          string    `json:"quality,omitempty"`
	Freshness        string    `json:"freshness,omitempty"`
	DevicePresence   string    `json:"devicePresence,omitempty"`
	BusinessRevision int64     `json:"businessRevision,omitempty"`
	PointRevision    int64     `json:"pointRevision,omitempty"`
	SampledAt        time.Time `json:"sampledAt,omitempty"`
	ReceivedAt       time.Time `json:"receivedAt,omitempty"`
}

func (p *TelemetryLivePoint) UnmarshalJSON(data []byte) error {
	type wireLivePoint struct {
		DeviceID         string   `json:"deviceId"`
		TelemetryKey     string   `json:"telemetryKey"`
		NumericValue     *float64 `json:"numericValue,omitempty"`
		BoolValue        *bool    `json:"boolValue,omitempty"`
		TextValue        *string  `json:"textValue,omitempty"`
		Unit             string   `json:"unit,omitempty"`
		Quality          string   `json:"quality,omitempty"`
		Freshness        string   `json:"freshness,omitempty"`
		DevicePresence   string   `json:"devicePresence,omitempty"`
		BusinessRevision int64    `json:"businessRevision,omitempty"`
		PointRevision    int64    `json:"pointRevision,omitempty"`
		SampledAt        *string  `json:"sampledAt,omitempty"`
		ReceivedAt       *string  `json:"receivedAt,omitempty"`
	}
	var raw wireLivePoint
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	sampledAt, err := parseOptionalTelemetryTime(raw.SampledAt)
	if err != nil {
		return fmt.Errorf("parse livePoint sampledAt: %w", err)
	}
	receivedAt, err := parseOptionalTelemetryTime(raw.ReceivedAt)
	if err != nil {
		return fmt.Errorf("parse livePoint receivedAt: %w", err)
	}
	*p = TelemetryLivePoint{
		DeviceID:         raw.DeviceID,
		TelemetryKey:     raw.TelemetryKey,
		NumericValue:     raw.NumericValue,
		BoolValue:        raw.BoolValue,
		TextValue:        raw.TextValue,
		Unit:             raw.Unit,
		Quality:          raw.Quality,
		Freshness:        raw.Freshness,
		DevicePresence:   raw.DevicePresence,
		BusinessRevision: raw.BusinessRevision,
		PointRevision:    raw.PointRevision,
		SampledAt:        sampledAt,
		ReceivedAt:       receivedAt,
	}
	return nil
}

func parseOptionalTelemetryTime(raw *string) (time.Time, error) {
	if raw == nil {
		return time.Time{}, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

type OptimizationPointHealthSnapshot struct {
	Healthy      bool                 `json:"healthy"`
	ReasonCode   string               `json:"reasonCode,omitempty"`
	TelemetryKey string               `json:"telemetryKey,omitempty"`
	BlockReason  string               `json:"blockReason,omitempty"`
	Detail       string               `json:"detail,omitempty"`
	LivePoints   []TelemetryLivePoint `json:"livePoints,omitempty"`
}

type TelemetryPointHealthChecker interface {
	CheckTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) error
	InspectTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) (OptimizationPointHealthSnapshot, error)
}

type TelemetryHealthClientConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	CAPath          string
	TelemetryURL    string
	ServerName      string
	Timeout         time.Duration
	HTTPClient      *http.Client
}

type WorkloadTelemetryHealthClient struct {
	baseURL    string
	httpClient *http.Client
}

type optimizationPointHealthRequest struct {
	TenantID      string   `json:"tenantId"`
	SiteID        string   `json:"siteId"`
	DeviceIDs     []string `json:"deviceIds,omitempty"`
	TelemetryKeys []string `json:"telemetryKeys"`
}

type optimizationPointHealthResponse struct {
	Healthy      bool                 `json:"healthy"`
	ReasonCode   string               `json:"reasonCode,omitempty"`
	TelemetryKey string               `json:"telemetryKey,omitempty"`
	BlockReason  string               `json:"blockReason,omitempty"`
	Detail       string               `json:"detail,omitempty"`
	LivePoints   []TelemetryLivePoint `json:"livePoints,omitempty"`
}

func NewWorkloadTelemetryHealthClient(config TelemetryHealthClientConfig) (*WorkloadTelemetryHealthClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.TelemetryURL), "/")
	if baseURL == "" {
		return nil, errors.New("telemetry health client requires TelemetryURL")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid TelemetryURL %q", baseURL)
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		serverName := strings.TrimSpace(config.ServerName)
		if serverName == "" {
			serverName = parsed.Hostname()
		}
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		httpClient, err = workloadtls.NewHTTPClient(workloadtls.ClientConfig{
			CertificateFiles: &workloadtls.CertificateFiles{
				CertificatePath: config.CertificatePath,
				PrivateKeyPath:  config.PrivateKeyPath,
			},
			ServerCAPath: config.CAPath,
			ServerName:   serverName,
			Timeout:      timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("create telemetry health mTLS client: %w", err)
		}
	}
	return &WorkloadTelemetryHealthClient{
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

// CheckTelemetryPointHealth returns ReasonInputsInvalid when remote point health or probe offsets suppress optimization.
func (client *WorkloadTelemetryHealthClient) CheckTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) error {
	snapshot, err := client.InspectTelemetryPointHealth(ctx, tenantID, siteID, deviceIDs, telemetryKeys)
	if err != nil {
		return err
	}
	if !snapshot.Healthy {
		return unhealthyPointHealthError(snapshot)
	}
	return nil
}

// InspectTelemetryPointHealth queries telemetry-service for point health and real-time live point states.
func (client *WorkloadTelemetryHealthClient) InspectTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) (OptimizationPointHealthSnapshot, error) {
	if client == nil || client.httpClient == nil {
		return OptimizationPointHealthSnapshot{}, errors.New("telemetry health client is not configured")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return OptimizationPointHealthSnapshot{}, errors.New("telemetry health check scope is invalid")
	}
	cleanKeys := normalizeTelemetryStrings(telemetryKeys, false)
	if len(cleanKeys) == 0 {
		return OptimizationPointHealthSnapshot{Healthy: true}, nil
	}
	cleanDevices := normalizeTelemetryStrings(deviceIDs, true)

	payload, err := json.Marshal(optimizationPointHealthRequest{
		TenantID:      tenant,
		SiteID:        site,
		DeviceIDs:     cleanDevices,
		TelemetryKeys: cleanKeys,
	})
	if err != nil {
		return OptimizationPointHealthSnapshot{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+InternalOptimizationPointHealthPath, bytes.NewReader(payload))
	if err != nil {
		return OptimizationPointHealthSnapshot{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, req.Header)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return OptimizationPointHealthSnapshot{}, fmt.Errorf("request telemetry point health: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return OptimizationPointHealthSnapshot{}, fmt.Errorf("read telemetry point health response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return OptimizationPointHealthSnapshot{}, fmt.Errorf("telemetry point health returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result optimizationPointHealthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return OptimizationPointHealthSnapshot{}, fmt.Errorf("decode telemetry point health response: %w", err)
	}
	return OptimizationPointHealthSnapshot(result), nil
}

func unhealthyPointHealthError(snapshot OptimizationPointHealthSnapshot) error {
	if detail := strings.TrimSpace(snapshot.Detail); detail != "" {
		return unavailable(ReasonInputsInvalid, "%s", detail)
	}
	if key := strings.TrimSpace(snapshot.TelemetryKey); key != "" {
		return unavailable(ReasonInputsInvalid, "point %s telemetry health (%s) suppresses optimization", key, snapshot.BlockReason)
	}
	return unavailable(ReasonInputsInvalid, "telemetry point health check suppressed optimization")
}

func normalizeTelemetryStrings(values []string, uuidOnly bool) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			continue
		}
		if uuidOnly && !uuidPattern.MatchString(clean) {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
