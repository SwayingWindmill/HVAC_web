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
	"strings"
	"time"
)

const (
	sourceObservationPath            = "/internal/v1/telemetry/sources/observations:accept"
	mqttGatewayEvidencePath          = "/internal/v1/telemetry/sources/mqtt/gateway-evidence:accept"
	mqttPresenceEvidencePath         = "/internal/v1/telemetry/sources/mqtt/presence-evidence:accept"
	mqttRuntimeEventPath             = "/internal/v1/telemetry/sources/mqtt/events:accept"
	maximumTelemetryRuntimeBodyBytes = int64(256 << 10)
)

type Observation struct {
	IntegrationInstanceID string         `json:"integrationInstanceId"`
	SourcePath            string         `json:"sourcePath"`
	ExternalEntityType    string         `json:"externalEntityType"`
	ExternalID            string         `json:"externalId"`
	TelemetryKey          string         `json:"telemetryKey"`
	Value                 any            `json:"value"`
	ValueType             string         `json:"valueType"`
	Unit                  *string        `json:"unit"`
	WireQuality           uint8          `json:"wireQuality"`
	SampledAt             string         `json:"sampledAt"`
	SourcePosition        SourcePosition `json:"sourcePosition"`
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

type GatewayEvidence struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	EvidenceType          string          `json:"evidenceType"`
	ObservedAt            string          `json:"observedAt"`
	Sequence              int64           `json:"sequence"`
	Payload               json.RawMessage `json:"payload"`
}

type PresenceEvidence struct {
	IntegrationInstanceID string `json:"integrationInstanceId"`
	ExternalEntityType    string `json:"externalEntityType"`
	ExternalID            string `json:"externalId"`
	SignalType            string `json:"signalType"`
	ObservedAt            string `json:"observedAt"`
	SourceEventID         string `json:"sourceEventId"`
}

type RuntimeEventEvidence struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	TenantID              string          `json:"tenantId"`
	SiteID                string          `json:"siteId"`
	GatewayID             string          `json:"gatewayId"`
	MessageID             string          `json:"messageId"`
	Sequence              int64           `json:"sequence"`
	EventType             string          `json:"eventType"`
	SourceType            string          `json:"sourceType"`
	SourceID              string          `json:"sourceId"`
	EventTime             string          `json:"eventTime"`
	Severity              string          `json:"severity"`
	Data                  json.RawMessage `json:"data"`
}

type PresenceEvidenceReceipt struct {
	DeviceID         string `json:"deviceId"`
	Accepted         bool   `json:"accepted"`
	Duplicate        bool   `json:"duplicate"`
	BusinessRevision int64  `json:"businessRevision"`
	StateChanged     bool   `json:"stateChanged"`
}

type RuntimeClient interface {
	AcceptObservation(context.Context, Observation) (ObservationReceipt, error)
	AcceptGatewayEvidence(context.Context, GatewayEvidence) error
	AcceptPresenceEvidence(context.Context, PresenceEvidence) (PresenceEvidenceReceipt, error)
	AcceptRuntimeEvent(context.Context, RuntimeEventEvidence) error
}

type TelemetryRuntimeClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewTelemetryRuntimeClient(config TelemetryRuntimeConfig) (*TelemetryRuntimeClient, error) {
	if err := validateHTTPSOrigin(config.BaseURL); err != nil {
		return nil, fmt.Errorf("telemetry runtime base URL: %w", err)
	}
	httpClient, err := newMTLSHTTPClient(config)
	if err != nil {
		return nil, err
	}
	return &TelemetryRuntimeClient{baseURL: strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"), httpClient: httpClient}, nil
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

func (client *TelemetryRuntimeClient) AcceptObservation(ctx context.Context, observation Observation) (ObservationReceipt, error) {
	body, err := json.Marshal(observation)
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("encode S2 observation: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+sourceObservationPath, bytes.NewReader(body))
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("create S2 observation request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ObservationReceipt{}, errors.New("S2 observation request failed")
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumTelemetryRuntimeBodyBytes)
	if err != nil {
		return ObservationReceipt{}, fmt.Errorf("read S2 observation response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ObservationReceipt{}, fmt.Errorf("S2 observation returned %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var receipt ObservationReceipt
	if err := decoder.Decode(&receipt); err != nil || ensureJSONEOF(decoder) != nil {
		return ObservationReceipt{}, errors.New("S2 observation receipt is invalid")
	}
	switch receipt.Status {
	case "ACCEPTED", "DUPLICATE", "OUT_OF_ORDER", "QUARANTINED", "REJECTED":
	default:
		return ObservationReceipt{}, errors.New("S2 observation receipt status is invalid")
	}
	return receipt, nil
}

func (client *TelemetryRuntimeClient) AcceptGatewayEvidence(ctx context.Context, evidence GatewayEvidence) error {
	return client.postEvidence(ctx, mqttGatewayEvidencePath, evidence, nil)
}

func (client *TelemetryRuntimeClient) AcceptPresenceEvidence(ctx context.Context, evidence PresenceEvidence) (PresenceEvidenceReceipt, error) {
	var receipt PresenceEvidenceReceipt
	if err := client.postEvidence(ctx, mqttPresenceEvidencePath, evidence, &receipt); err != nil {
		return PresenceEvidenceReceipt{}, err
	}
	return receipt, nil
}

func (client *TelemetryRuntimeClient) AcceptRuntimeEvent(ctx context.Context, evidence RuntimeEventEvidence) error {
	return client.postEvidence(ctx, mqttRuntimeEventPath, evidence, nil)
}

func (client *TelemetryRuntimeClient) postEvidence(ctx context.Context, path string, value any, destination any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode MQTT evidence: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create MQTT evidence request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return errors.New("MQTT evidence request failed")
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumTelemetryRuntimeBodyBytes)
	if err != nil {
		return fmt.Errorf("read MQTT evidence response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MQTT evidence returned %d", response.StatusCode)
	}
	if destination == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || ensureJSONEOF(decoder) != nil {
		return errors.New("MQTT evidence receipt is invalid")
	}
	return nil
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
