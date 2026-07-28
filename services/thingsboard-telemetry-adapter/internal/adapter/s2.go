package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	observationPath                  = "/internal/v1/telemetry/thingsboard/observations:accept"
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

type TelemetryRuntimeClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewTelemetryRuntimeClient(baseURL string, httpClient *http.Client) (*TelemetryRuntimeClient, error) {
	if err := validateProviderURL(baseURL, false); err != nil {
		return nil, fmt.Errorf("telemetry runtime base URL: %w", err)
	}
	if httpClient == nil {
		return nil, errors.New("telemetry runtime HTTP client is required")
	}
	return &TelemetryRuntimeClient{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), httpClient: httpClient}, nil
}

func NewMTLSHTTPClient(config TelemetryRuntimeConfig) (*http.Client, error) {
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+observationPath, bytes.NewReader(body))
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
