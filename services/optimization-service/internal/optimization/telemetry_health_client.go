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

type TelemetryPointHealthChecker interface {
	CheckTelemetryPointHealth(ctx context.Context, tenantID, siteID string, deviceIDs, telemetryKeys []string) error
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
	Healthy      bool   `json:"healthy"`
	ReasonCode   string `json:"reasonCode,omitempty"`
	TelemetryKey string `json:"telemetryKey,omitempty"`
	BlockReason  string `json:"blockReason,omitempty"`
	Detail       string `json:"detail,omitempty"`
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
	if client == nil || client.httpClient == nil {
		return errors.New("telemetry health client is not configured")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return errors.New("telemetry health check scope is invalid")
	}
	cleanKeys := normalizeTelemetryStrings(telemetryKeys, false)
	if len(cleanKeys) == 0 {
		return nil
	}
	cleanDevices := normalizeTelemetryStrings(deviceIDs, true)

	payload, err := json.Marshal(optimizationPointHealthRequest{
		TenantID:      tenant,
		SiteID:        site,
		DeviceIDs:     cleanDevices,
		TelemetryKeys: cleanKeys,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+InternalOptimizationPointHealthPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, req.Header)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request telemetry point health: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read telemetry point health response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry point health returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result optimizationPointHealthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode telemetry point health response: %w", err)
	}
	if !result.Healthy {
		if detail := strings.TrimSpace(result.Detail); detail != "" {
			return unavailable(ReasonInputsInvalid, "%s", detail)
		}
		if key := strings.TrimSpace(result.TelemetryKey); key != "" {
			return unavailable(ReasonInputsInvalid, "point %s telemetry health (%s) suppresses optimization", key, result.BlockReason)
		}
		return unavailable(ReasonInputsInvalid, "telemetry point health check suppressed optimization")
	}
	return nil
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
