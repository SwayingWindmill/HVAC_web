package optimization

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
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
	HealthAt        string   `json:"healthAt,omitempty"`
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
	baseURL, httpClient, err := resolveWorkloadTelemetryHTTPClient(
		config.TelemetryURL,
		config.CertificatePath,
		config.PrivateKeyPath,
		config.CAPath,
		config.ServerName,
		config.Timeout,
		config.HTTPClient,
		"telemetry live client",
	)
	if err != nil {
		return nil, err
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

	var result OptimizationLiveObservationsResult
	if err := postWorkloadTelemetryJSON(
		ctx,
		client.httpClient,
		client.baseURL+InternalOptimizationLiveObservationsPath,
		optimizationLiveObservationsRequest{
			TenantID:      tenant,
			SiteID:        site,
			TelemetryKeys: cleanKeys,
		},
		&result,
		maximumOptimizationInputResponseBytes,
		"telemetry live observations",
	); err != nil {
		return OptimizationLiveObservationsResult{}, err
	}
	if result.Observations == nil {
		result.Observations = map[string]OptimizationLiveObservation{}
	}
	return result, nil
}
