package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

const (
	historicalReplayObservationPath = "/internal/v1/telemetry/history-replay/observations:accept"
	maximumReplayResponseBytes      = int64(64 << 10)
)

var uuidV7Pattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type replayObservationRequest struct {
	IntegrationInstanceID string          `json:"integrationInstanceId"`
	ReplayDatasetID       string          `json:"replayDatasetId"`
	DeviceExternalID      string          `json:"deviceExternalId"`
	TelemetryKey          string          `json:"telemetryKey"`
	Value                 json.RawMessage `json:"value"`
	ValueType             string          `json:"valueType"`
	Unit                  *string         `json:"unit"`
	WireQuality           uint8           `json:"wireQuality"`
	SampledAt             time.Time       `json:"sampledAt"`
	Offset                int64           `json:"offset"`
}

type replayAdmitter func(context.Context, replayObservationRequest) error

type replayClient struct {
	endpoint   string
	httpClient *http.Client
}

func main() {
	plantConfigPath := flag.String("plant-config", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_PLANT_CONFIG")), "path to the canonical Virtual Central Plant config")
	mqttConfigPath := flag.String("mqtt-config", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_MQTT_CONFIG")), "path to the existing MQTT config used for Device external identity mapping")
	telemetryURL := flag.String("telemetry-url", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TELEMETRY_URL")), "HTTPS origin for Telemetry Runtime")
	integrationID := flag.String("integration-id", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_INTEGRATION_ID")), "current authorized integration instance UUIDv7")
	datasetID := flag.String("dataset-id", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_DATASET_ID")), "stable replay dataset UUIDv7")
	fromValue := flag.String("from", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_FROM")), "historical replay start time in RFC3339")
	durationValue := flag.String("duration", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_DURATION")), "finite replay duration")
	certFile := flag.String("tls-cert", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TLS_CERT")), "replay workload TLS certificate")
	keyFile := flag.String("tls-key", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TLS_KEY")), "replay workload TLS private key")
	caFile := flag.String("server-ca", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_SERVER_CA")), "Telemetry Runtime server CA")
	serverName := flag.String("server-name", envOr("EG8200_HISTORY_REPLAY_SERVER_NAME", "telemetry-runtime-service"), "Telemetry Runtime TLS server name")
	flag.Parse()

	if strings.TrimSpace(*plantConfigPath) == "" || strings.TrimSpace(*mqttConfigPath) == "" {
		log.Fatal("historical replay requires plant-config and mqtt-config")
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(*integrationID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(*datasetID)) {
		log.Fatal("historical replay integration-id and dataset-id must be UUIDv7")
	}
	from, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*fromValue))
	if err != nil {
		log.Fatal("historical replay from must be RFC3339")
	}
	duration, err := time.ParseDuration(strings.TrimSpace(*durationValue))
	if err != nil || duration <= 0 {
		log.Fatal("historical replay duration must be positive")
	}
	plantConfig, mqttConfig, err := loadReplayConfigs(*plantConfigPath, *mqttConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	client, err := newReplayClient(*telemetryURL, *certFile, *keyFile, *caFile, *serverName)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	count, err := runReplay(ctx, plantConfig, mqttConfig, strings.TrimSpace(*integrationID), strings.ToLower(strings.TrimSpace(*datasetID)), from.UTC(), duration, client.Admit)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("historical replay admitted %d observations", count)
}

func loadReplayConfigs(plantPath, mqttPath string) (simulator.Config, simulator.MQTTGatewayConfig, error) {
	plantFile, err := os.Open(plantPath)
	if err != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, fmt.Errorf("open replay plant config: %w", err)
	}
	plantConfig, decodeErr := simulator.DecodeConfig(plantFile)
	closeErr := plantFile.Close()
	if decodeErr != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, decodeErr
	}
	if closeErr != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, closeErr
	}
	mqttFile, err := os.Open(mqttPath)
	if err != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, fmt.Errorf("open replay MQTT config: %w", err)
	}
	mqttConfig, decodeErr := simulator.DecodeMQTTGatewayConfig(mqttFile)
	closeErr = mqttFile.Close()
	if decodeErr != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, decodeErr
	}
	if closeErr != nil {
		return simulator.Config{}, simulator.MQTTGatewayConfig{}, closeErr
	}
	return plantConfig, mqttConfig, nil
}

func runReplay(ctx context.Context, config simulator.Config, mqttConfig simulator.MQTTGatewayConfig, integrationID, datasetID string, from time.Time, duration time.Duration, admit replayAdmitter) (int, error) {
	if admit == nil || !uuidV7Pattern.MatchString(integrationID) || !uuidV7Pattern.MatchString(datasetID) || from.IsZero() || duration <= 0 {
		return 0, errors.New("historical replay runner configuration is invalid")
	}
	pointByReference := make(map[string]simulator.PointConfig, len(config.Points))
	for _, point := range config.Points {
		pointByReference[point.DeviceID+"\x00"+point.TelemetryKey] = point
	}
	plant := simulator.NewPlant(config.Plant, config.Scenario, from.UTC())
	scheduler, err := simulator.NewMeasurementScheduler(config)
	if err != nil {
		return 0, err
	}
	offsets := make(map[string]int64, len(mqttConfig.DeviceExternalIDByDeviceID))
	admitted := 0
	emit := func(snapshot simulator.Snapshot) error {
		measurements, err := scheduler.Observe(snapshot)
		if err != nil {
			return err
		}
		slices.SortFunc(measurements, func(left, right simulator.Measurement) int {
			if order := cmp.Compare(left.DeviceID, right.DeviceID); order != 0 {
				return order
			}
			return cmp.Compare(left.TelemetryKey, right.TelemetryKey)
		})
		for _, measurement := range measurements {
			point, ok := pointByReference[measurement.DeviceID+"\x00"+measurement.TelemetryKey]
			if !ok {
				return fmt.Errorf("historical replay point metadata is missing for %s/%s", measurement.DeviceID, measurement.TelemetryKey)
			}
			externalID := strings.TrimSpace(mqttConfig.DeviceExternalIDByDeviceID[measurement.DeviceID])
			if externalID == "" {
				return fmt.Errorf("historical replay Device external identity is missing for %s", measurement.DeviceID)
			}
			var unit *string
			if value := strings.TrimSpace(point.Unit); value != "" {
				unit = &value
			}
			wireQuality := uint8(0)
			if !strings.EqualFold(strings.TrimSpace(measurement.Quality), "GOOD") {
				wireQuality = 1
			}
			value, err := json.Marshal(measurement.Value)
			if err != nil {
				return fmt.Errorf("encode historical replay value for %s/%s: %w", measurement.DeviceID, measurement.TelemetryKey, err)
			}
			request := replayObservationRequest{
				IntegrationInstanceID: integrationID,
				ReplayDatasetID:       datasetID,
				DeviceExternalID:      externalID,
				TelemetryKey:          strings.TrimSpace(point.PointCode),
				Value:                 value,
				ValueType:             strings.TrimSpace(point.ValueType),
				Unit:                  unit,
				WireQuality:           wireQuality,
				SampledAt:             measurement.ObservedAt.UTC(),
				Offset:                offsets[externalID],
			}
			if err := admit(ctx, request); err != nil {
				return err
			}
			offsets[externalID]++
			admitted++
		}
		return nil
	}

	if err := emit(plant.Snapshot()); err != nil {
		return admitted, err
	}
	elapsed := time.Duration(0)
	interval := config.Interval()
	for elapsed < duration {
		step := min(interval, duration-elapsed)
		if err := emit(plant.Tick(step)); err != nil {
			return admitted, err
		}
		elapsed += step
	}
	return admitted, nil
}

func newReplayClient(rawURL, certFile, keyFile, caFile, serverName string) (*replayClient, error) {
	base, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("historical replay Telemetry URL must be an HTTPS origin")
	}
	certificate, err := tls.LoadX509KeyPair(strings.TrimSpace(certFile), strings.TrimSpace(keyFile))
	if err != nil {
		return nil, fmt.Errorf("load historical replay workload identity: %w", err)
	}
	pem, err := os.ReadFile(strings.TrimSpace(caFile))
	if err != nil {
		return nil, fmt.Errorf("read historical replay server CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("historical replay server CA is empty")
	}
	return &replayClient{
		endpoint: strings.TrimRight(base.String(), "/") + historicalReplayObservationPath,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: strings.TrimSpace(serverName),
			}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (client *replayClient) Admit(ctx context.Context, input replayObservationRequest) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode Historical Replay observation: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Historical Replay request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("admit Historical Replay observation: %w", err)
	}
	defer response.Body.Close()
	body, err = io.ReadAll(io.LimitReader(response.Body, maximumReplayResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Historical Replay response: %w", err)
	}
	if int64(len(body)) > maximumReplayResponseBytes {
		return errors.New("Historical Replay response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Historical Replay admission returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
