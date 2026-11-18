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

const InternalOptimizationLiveObservationsPath = "/internal/v1/telemetry/optimization/live-observations:read"

type OptimizationLiveObservation struct {
	ObservationID   string   `json:"observationId"`
	DeviceID        string   `json:"deviceId"`
	PointID         string   `json:"pointId"`
	TelemetryKey    string   `json:"telemetryKey"`
	PointRevision   uint64   `json:"pointRevision"`
	SampledAt       string   `json:"sampledAt"`
	ReceivedAt      string   `json:"receivedAt"`
	Value           float64  `json:"value"`
	Unit            string   `json:"unit"`
	Quality         string   `json:"quality"`
	QualityReasons  []string `json:"qualityReasons"`
	HealthStatus    string   `json:"healthStatus,omitempty"`
	HealthReasons   []string `json:"healthReasons,omitempty"`
	SourceEventID   string   `json:"sourceEventId"`
	SourcePartition string   `json:"sourcePartition"`
	SourceOffset    uint64   `json:"sourceOffset"`
}

type OptimizationLiveObservationsResult struct {
	Observations  map[string]OptimizationLiveObservation `json:"observations"`
	AmbiguousKeys []string                               `json:"ambiguousKeys,omitempty"`
}

// TelemetryLiveObservationReader reads hot site-level numeric observations from Telemetry Runtime.
type TelemetryLiveObservationReader interface {
	ReadLiveObservations(ctx context.Context, tenantID, siteID string, telemetryKeys []string) (OptimizationLiveObservationsResult, error)
}

type TelemetryLiveClientConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	CAPath          string
	TelemetryURL    string
	ServerName      string
	Timeout         time.Duration
	HTTPClient      *http.Client
}

type WorkloadTelemetryLiveClient struct {
	baseURL    string
	httpClient *http.Client
}

type optimizationLiveObservationsRequest struct {
	TenantID      string   `json:"tenantId"`
	SiteID        string   `json:"siteId"`
	TelemetryKeys []string `json:"telemetryKeys"`
}

func NewWorkloadTelemetryLiveClient(config TelemetryLiveClientConfig) (*WorkloadTelemetryLiveClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.TelemetryURL), "/")
	if baseURL == "" {
		return nil, errors.New("telemetry live client requires TelemetryURL")
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
			return nil, fmt.Errorf("create telemetry live mTLS client: %w", err)
		}
	}
	return &WorkloadTelemetryLiveClient{
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

func (client *WorkloadTelemetryLiveClient) ReadLiveObservations(ctx context.Context, tenantID, siteID string, telemetryKeys []string) (OptimizationLiveObservationsResult, error) {
	if client == nil || client.httpClient == nil {
		return OptimizationLiveObservationsResult{}, errors.New("telemetry live client is not configured")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(site) {
		return OptimizationLiveObservationsResult{}, errors.New("telemetry live observation scope is invalid")
	}
	cleanKeys := normalizeTelemetryStrings(telemetryKeys, false)
	if len(cleanKeys) == 0 {
		return OptimizationLiveObservationsResult{
			Observations: map[string]OptimizationLiveObservation{},
		}, nil
	}

	payload, err := json.Marshal(optimizationLiveObservationsRequest{
		TenantID:      tenant,
		SiteID:        site,
		TelemetryKeys: cleanKeys,
	})
	if err != nil {
		return OptimizationLiveObservationsResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+InternalOptimizationLiveObservationsPath, bytes.NewReader(payload))
	if err != nil {
		return OptimizationLiveObservationsResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	observability.InjectHTTP(ctx, req.Header)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return OptimizationLiveObservationsResult{}, fmt.Errorf("request telemetry live observations: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maximumOptimizationInputResponseBytes+1))
	if err != nil {
		return OptimizationLiveObservationsResult{}, fmt.Errorf("read telemetry live observations response: %w", err)
	}
	if int64(len(body)) > maximumOptimizationInputResponseBytes {
		return OptimizationLiveObservationsResult{}, fmt.Errorf("telemetry live observations response exceeds %d MiB", maximumOptimizationInputResponseBytes>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return OptimizationLiveObservationsResult{}, fmt.Errorf("telemetry live observations returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result OptimizationLiveObservationsResult
	if err := json.Unmarshal(body, &result); err != nil {
		return OptimizationLiveObservationsResult{}, fmt.Errorf("decode telemetry live observations response: %w", err)
	}
	if result.Observations == nil {
		result.Observations = map[string]OptimizationLiveObservation{}
	}
	return result, nil
}
