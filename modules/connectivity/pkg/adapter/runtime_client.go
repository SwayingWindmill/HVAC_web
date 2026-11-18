package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const maxTelemetryRetryAfter = 5 * time.Minute

var (
	ErrTelemetryRateLimited    = errors.New("telemetry runtime rate limited")
	ErrTelemetryRequestInvalid = errors.New("telemetry runtime request invalid")
)

type RateLimitedError struct {
	Label      string
	RetryAfter time.Duration
}

func (err *RateLimitedError) Error() string {
	if err == nil {
		return ErrTelemetryRateLimited.Error()
	}
	if err.RetryAfter > 0 {
		return fmt.Sprintf("%s returned 429 (retry after %s)", err.Label, err.RetryAfter)
	}
	return fmt.Sprintf("%s returned 429", err.Label)
}

func (err *RateLimitedError) Unwrap() error {
	return ErrTelemetryRateLimited
}

type RequestInvalidError struct {
	Label      string
	StatusCode int
}

func (err *RequestInvalidError) Error() string {
	if err == nil {
		return ErrTelemetryRequestInvalid.Error()
	}
	if err.StatusCode > 0 {
		return fmt.Sprintf("%s returned %d", err.Label, err.StatusCode)
	}
	return ErrTelemetryRequestInvalid.Error()
}

func (err *RequestInvalidError) Unwrap() error {
	return ErrTelemetryRequestInvalid
}

const (
	// MaxObservationBatchSize caps a single Telemetry runtime acceptBatch call.
	MaxObservationBatchSize = 512
	// MaxObservationBatchBytes keeps encoded acceptBatch requests under Telemetry's 4 MiB limit.
	MaxObservationBatchBytes         = 3 << 20
	sourceObservationBatchPath       = "/internal/v1/telemetry/sources/observations:acceptBatch"
	sourceCoveragePath               = "/internal/v1/telemetry/sources/coverage:report"
	mqttRuntimeEventPath             = "/internal/v1/telemetry/sources/mqtt/events:accept"
	maximumTelemetryRuntimeBodyBytes = int64(2 << 20)
)

// Observation is one point value with the Device and Point resolved from Registry; either
// is nil when the Gateway names something unregistered, and Telemetry quarantines it.
type Observation struct {
	SourceID           string          `json:"sourceId"`
	SourcePath         string          `json:"sourcePath"`
	ExternalEntityType string          `json:"externalEntityType"`
	ExternalID         string          `json:"externalId"`
	Device             *ResolvedDevice `json:"device"`
	Point              *ResolvedPoint  `json:"point"`
	TelemetryKey       string          `json:"telemetryKey"`
	Value              any             `json:"value"`
	ValueType          string          `json:"valueType"`
	Unit               *string         `json:"unit"`
	WireQuality        uint8           `json:"wireQuality"`
	SampledAt          string          `json:"sampledAt"`
	EdgeReplay         bool            `json:"edgeReplay,omitempty"`
	SourcePosition     SourcePosition  `json:"sourcePosition"`
	TraceID            string          `json:"traceId,omitempty"`
}

type ResolvedDevice struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
	DeviceID string `json:"deviceId"`
}

type ResolvedPoint struct {
	PointID                string   `json:"pointId"`
	SensorID               *string  `json:"sensorId"`
	PointType              string   `json:"pointType"`
	ValueType              string   `json:"valueType"`
	Unit                   *string  `json:"unit"`
	CounterDecreaseMode    *string  `json:"counterDecreaseMode"`
	CounterRolloverModulus *float64 `json:"counterRolloverModulus"`
	PointRevision          int64    `json:"pointRevision"`
}

type SourcePosition struct {
	Partition string `json:"partition"`
	Offset    int64  `json:"offset"`
	EventID   string `json:"eventId"`
}

type ObservationReceipt struct {
	ObservationID    string   `json:"observationId"`
	EvidenceID       string   `json:"evidenceId"`
	Status           string   `json:"status"`
	Quality          string   `json:"quality"`
	QualityReasons   []string `json:"qualityReasons"`
	QuarantineReason string   `json:"quarantineReason"`
	DeviceID         string   `json:"deviceId"`
	BusinessRevision int64    `json:"businessRevision"`
	StateChanged     bool     `json:"stateChanged"`
	PositionAdvanced bool     `json:"positionAdvanced"`
}

type RuntimeEventEvidence struct {
	TenantID          string          `json:"tenantId"`
	SiteID            string          `json:"siteId"`
	GatewayID         string          `json:"gatewayId"`
	MessageID         string          `json:"messageId"`
	Sequence          int64           `json:"sequence"`
	EventType         string          `json:"eventType"`
	SourceType        string          `json:"sourceType"`
	SourceID          string          `json:"sourceId"`
	EventTime         string          `json:"eventTime"`
	Severity          string          `json:"severity"`
	Data              json.RawMessage `json:"data"`
	Device            *ResolvedDevice `json:"device,omitempty"`
	CoverageDeviceIDs []string        `json:"coverageDeviceIds,omitempty"`
	TraceID           string          `json:"traceId,omitempty"`
}

type GatewayCoverageScope struct {
	GatewayID string   `json:"gatewayId"`
	DeviceIDs []string `json:"deviceIds"`
}

type SourceCoverageReport struct {
	SourceID       string                 `json:"sourceId"`
	DeviceIDs      []string               `json:"deviceIds"`
	GatewayScopes  []GatewayCoverageScope `json:"gatewayScopes,omitempty"`
	Available      bool                   `json:"available"`
	ReasonCode     *string                `json:"reasonCode,omitempty"`
	ObservedAt     string                 `json:"observedAt,omitempty"`
	SourceRevision int64                  `json:"sourceRevision,omitempty"`
}

type RuntimeClient interface {
	AcceptObservationBatch(context.Context, []Observation) ([]ObservationReceipt, error)
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
	ReportSourceCoverage(context.Context, SourceCoverageReport) error
}

type TelemetryRuntimeClient struct {
	baseURL    string
	httpClient *http.Client
	tracer     *observability.Tracer
}

func NewTelemetryRuntimeClient(config TelemetryRuntimeConfig) (*TelemetryRuntimeClient, error) {
	return NewTelemetryRuntimeClientWithTracer(config, nil)
}

func NewTelemetryRuntimeClientWithTracer(config TelemetryRuntimeConfig, tracer *observability.Tracer) (*TelemetryRuntimeClient, error) {
	if err := validateHTTPSOrigin(config.BaseURL); err != nil {
		return nil, fmt.Errorf("telemetry runtime base URL: %w", err)
	}
	httpClient, err := newMTLSHTTPClient(config)
	if err != nil {
		return nil, err
	}
	if tracer == nil {
		tracer = observability.NewTracer("mqtt-telemetry-adapter", nil)
	}
	return &TelemetryRuntimeClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"),
		httpClient: httpClient,
		tracer:     tracer,
	}, nil
}

func newMTLSHTTPClient(config TelemetryRuntimeConfig) (*http.Client, error) {
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, errors.New("load telemetry runtime client identity failed")
	}
	caContent, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("read telemetry runtime CA failed")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caContent) {
		return nil, errors.New("telemetry runtime CA is invalid")
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      rootCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   strings.TrimSpace(config.ServerName),
		},
		DisableCompression: true,
		ForceAttemptHTTP2:  false,
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

type observationBatchRequest struct {
	Observations []Observation `json:"observations"`
}

type observationBatchResponse struct {
	Receipts []ObservationReceipt `json:"receipts"`
}

func (client *TelemetryRuntimeClient) effectiveTracer() *observability.Tracer {
	if client != nil && client.tracer != nil {
		return client.tracer
	}
	return observability.NewTracer("mqtt-telemetry-adapter", nil)
}

func (client *TelemetryRuntimeClient) AcceptObservationBatch(ctx context.Context, observations []Observation) ([]ObservationReceipt, error) {
	if len(observations) == 0 || len(observations) > MaxObservationBatchSize {
		return nil, fmt.Errorf("S2 observation batch size %d must be between 1 and %d", len(observations), MaxObservationBatchSize)
	}
	if observability.TraceID(ctx) == "" {
		for _, observation := range observations {
			if traceID := normalizeTraceID(observation.TraceID); traceID != "" {
				ctx = observability.ContextWithRemoteParent(ctx, client.effectiveTracer(), "00-"+traceID+"-0000000000000001-01", "")
				break
			}
		}
	}
	responseBody, err := client.postJSON(ctx, sourceObservationBatchPath, "S2 observation batch", observationBatchRequest{Observations: observations})
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var batchResponse observationBatchResponse
	if err := decoder.Decode(&batchResponse); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, errors.New("S2 observation batch receipt is invalid")
	}
	if len(batchResponse.Receipts) != len(observations) {
		return nil, fmt.Errorf("S2 observation batch returned %d receipts for %d observations", len(batchResponse.Receipts), len(observations))
	}
	for _, receipt := range batchResponse.Receipts {
		if !validReceiptStatus(receipt.Status) {
			return nil, errors.New("S2 observation receipt status is invalid")
		}
	}
	return batchResponse.Receipts, nil
}

func validReceiptStatus(status string) bool {
	switch status {
	case "ACCEPTED", "DUPLICATE", "OUT_OF_ORDER", "QUARANTINED", "REJECTED":
		return true
	default:
		return false
	}
}

func (client *TelemetryRuntimeClient) AcceptRuntimeEvent(ctx context.Context, evidence RuntimeEventEvidence) error {
	if observability.TraceID(ctx) == "" {
		if traceID := normalizeTraceID(evidence.TraceID); traceID != "" {
			ctx = observability.ContextWithRemoteParent(ctx, client.effectiveTracer(), "00-"+traceID+"-0000000000000001-01", "")
		}
	}
	_, err := client.postJSON(ctx, mqttRuntimeEventPath, "MQTT evidence", evidence)
	return err
}

func (client *TelemetryRuntimeClient) ReportSourceCoverage(ctx context.Context, report SourceCoverageReport) error {
	_, err := client.postJSON(ctx, sourceCoveragePath, "MQTT coverage", report)
	return err
}

func (client *TelemetryRuntimeClient) postJSON(ctx context.Context, path, label string, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", label, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", label, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	observability.InjectHTTP(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s request failed", label)
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumTelemetryRuntimeBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", label, err)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, &RateLimitedError{
			Label:      label,
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		}
	}
	if response.StatusCode == http.StatusBadRequest {
		return nil, &RequestInvalidError{
			Label:      label,
			StatusCode: response.StatusCode,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %d", label, response.StatusCode)
	}
	return responseBody, nil
}

func parseRetryAfter(header string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil {
		if seconds <= 0 {
			return 0
		}
		if maxSeconds := int(maxTelemetryRetryAfter / time.Second); seconds >= maxSeconds {
			return maxTelemetryRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(trimmed); err == nil {
		delay := retryAt.Sub(now)
		if delay <= 0 {
			return 0
		}
		if delay > maxTelemetryRetryAfter {
			return maxTelemetryRetryAfter
		}
		return delay
	}
	return 0
}

func validateHTTPSOrigin(raw string) error {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("must be an HTTPS origin")
	}
	return nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, errors.New("response body exceeds limit")
	}
	return content, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing content")
	}
	return nil
}
