package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

const maximumThingsBoardResponseBytes = 2 << 20

type config struct {
	IntegrationInstanceID string         `json:"integrationInstanceId"`
	PollInterval          string         `json:"pollInterval"`
	Sources               []sourceConfig `json:"sources"`
}

type sourceConfig struct {
	Name       string        `json:"name"`
	ExternalID string        `json:"externalId"`
	Points     []pointConfig `json:"points"`
}

type pointConfig struct {
	SourceKey    string  `json:"sourceKey"`
	TelemetryKey string  `json:"telemetryKey"`
	Unit         string  `json:"unit,omitempty"`
	Scale        float64 `json:"scale"`
	Offset       float64 `json:"offset,omitempty"`
}

type latestRow struct {
	SourceName string
	SourceKey  string
	Timestamp  int64
	Value      float64
}

type pointBinding struct {
	Source       sourceConfig
	Point        pointConfig
	LastAccepted int64
}

type thingsBoardClient struct {
	baseURL    *url.URL
	username   string
	password   string
	token      string
	httpClient *http.Client
}

type thingsBoardLoginResponse struct {
	Token string `json:"token"`
}

type thingsBoardTimeseriesValue struct {
	Timestamp int64           `json:"ts"`
	Value     json.RawMessage `json:"value"`
}

type reconciler struct {
	integrationID string
	pollInterval  time.Duration
	thingsBoard   *thingsBoardClient
	runtime       *adapter.TelemetryRuntimeClient
	sources       []sourceConfig
	bindings      map[string]*pointBinding
	logger        *slog.Logger
}

func main() {
	configPath := flag.String("config", strings.TrimSpace(os.Getenv("THINGSBOARD_RECONCILER_CONFIG")), "path to the ThingsBoard reconciliation JSON config")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if strings.TrimSpace(*configPath) == "" {
		logger.Error("thingsboard_reconciler_config_missing")
		os.Exit(1)
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		logger.Error("thingsboard_reconciler_config_invalid", "error", err.Error())
		os.Exit(1)
	}

	thingsBoard, err := newThingsBoardClient(
		strings.TrimSpace(os.Getenv("THINGSBOARD_BASE_URL")),
		strings.TrimSpace(os.Getenv("THINGSBOARD_USERNAME")),
		os.Getenv("THINGSBOARD_PASSWORD"),
	)
	if err != nil {
		logger.Error("thingsboard_client_invalid", "error", err.Error())
		os.Exit(1)
	}

	runtimeConfig, err := telemetryRuntimeConfigFromEnv()
	if err != nil {
		logger.Error("telemetry_runtime_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	runtime, err := adapter.NewTelemetryRuntimeClient(runtimeConfig)
	if err != nil {
		logger.Error("telemetry_runtime_client_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := thingsBoard.login(ctx); err != nil {
		logger.Error("thingsboard_login_failed", "error", err.Error())
		os.Exit(1)
	}

	reconciler := newReconciler(cfg, thingsBoard, runtime, logger)
	logger.Info("thingsboard_reconciler_started", "integration_instance_id", cfg.IntegrationInstanceID, "source_count", len(cfg.Sources), "poll_interval", reconciler.pollInterval.String())
	if err := reconciler.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("thingsboard_reconciler_stopped", "error", err.Error())
		os.Exit(1)
	}
}

func newThingsBoardClient(rawBaseURL, username, password string) (*thingsBoardClient, error) {
	parsed, err := url.Parse(strings.TrimRight(rawBaseURL, "/"))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("THINGSBOARD_BASE_URL must be an HTTP(S) origin")
	}
	if username == "" || password == "" {
		return nil, errors.New("ThingsBoard username and password are required")
	}
	return &thingsBoardClient{
		baseURL:    parsed,
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (client *thingsBoardClient) login(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{"username": client.username, "password": client.password})
	if err != nil {
		return fmt.Errorf("encode ThingsBoard login request: %w", err)
	}
	endpoint := *client.baseURL
	endpoint.Path = "/api/auth/login"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ThingsBoard login request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return errors.New("ThingsBoard login request failed")
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumThingsBoardResponseBytes)
	if err != nil {
		return fmt.Errorf("read ThingsBoard login response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ThingsBoard login returned %d", response.StatusCode)
	}
	var login thingsBoardLoginResponse
	if err := json.Unmarshal(responseBody, &login); err != nil || strings.TrimSpace(login.Token) == "" {
		return errors.New("ThingsBoard login response has no token")
	}
	client.token = strings.TrimSpace(login.Token)
	return nil
}

func (client *thingsBoardClient) latest(ctx context.Context, source sourceConfig) ([]latestRow, error) {
	rows, status, err := client.latestWithToken(ctx, source)
	if status != http.StatusUnauthorized {
		return rows, err
	}
	client.token = ""
	if err := client.login(ctx); err != nil {
		return nil, err
	}
	rows, _, err = client.latestWithToken(ctx, source)
	return rows, err
}

func (client *thingsBoardClient) latestWithToken(ctx context.Context, source sourceConfig) ([]latestRow, int, error) {
	endpoint := *client.baseURL
	endpoint.Path = "/api/plugins/telemetry/DEVICE/" + url.PathEscape(source.ExternalID) + "/values/timeseries"
	query := endpoint.Query()
	keys := make([]string, 0, len(source.Points))
	for _, point := range source.Points {
		keys = append(keys, point.SourceKey)
	}
	query.Set("keys", strings.Join(keys, ","))
	query.Set("useStrictDataTypes", "true")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create ThingsBoard telemetry request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, 0, errors.New("ThingsBoard telemetry request failed")
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumThingsBoardResponseBytes)
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read ThingsBoard telemetry response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, fmt.Errorf("ThingsBoard telemetry returned %d", response.StatusCode)
	}

	var payload map[string][]thingsBoardTimeseriesValue
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, response.StatusCode, errors.New("ThingsBoard telemetry response is invalid")
	}
	rows := make([]latestRow, 0, len(source.Points))
	for _, point := range source.Points {
		values := payload[point.SourceKey]
		if len(values) == 0 {
			continue
		}
		value, err := numericThingsBoardValue(values[0].Value)
		if err != nil {
			return nil, response.StatusCode, fmt.Errorf("ThingsBoard source %s key %s is not numeric", source.Name, point.SourceKey)
		}
		rows = append(rows, latestRow{SourceName: source.Name, SourceKey: point.SourceKey, Timestamp: values[0].Timestamp, Value: value})
	}
	return rows, response.StatusCode, nil
}

func numericThingsBoardValue(raw json.RawMessage) (float64, error) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, errors.New("value is not numeric")
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, errors.New("value is not numeric")
	}
	return number, nil
}

func telemetryRuntimeConfigFromEnv() (adapter.TelemetryRuntimeConfig, error) {
	values := map[string]string{
		"TELEMETRY_RUNTIME_BASE_URL":    strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_BASE_URL")),
		"TELEMETRY_RUNTIME_CA":          strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_CA")),
		"TELEMETRY_RUNTIME_CERT":        strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_CERT")),
		"TELEMETRY_RUNTIME_KEY":         strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_KEY")),
		"TELEMETRY_RUNTIME_SERVER_NAME": strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_SERVER_NAME")),
	}
	for name, value := range values {
		if value == "" {
			return adapter.TelemetryRuntimeConfig{}, fmt.Errorf("%s is required", name)
		}
	}
	return adapter.TelemetryRuntimeConfig{
		BaseURL:    values["TELEMETRY_RUNTIME_BASE_URL"],
		CAFile:     values["TELEMETRY_RUNTIME_CA"],
		CertFile:   values["TELEMETRY_RUNTIME_CERT"],
		KeyFile:    values["TELEMETRY_RUNTIME_KEY"],
		ServerName: values["TELEMETRY_RUNTIME_SERVER_NAME"],
	}, nil
}

func loadConfig(path string) (config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config{}, errors.New("config contains trailing JSON")
	}
	return cfg, validateConfig(cfg)
}

func validateConfig(cfg config) error {
	if !uuidV7(cfg.IntegrationInstanceID) {
		return errors.New("integrationInstanceId must be UUIDv7")
	}
	interval, err := time.ParseDuration(strings.TrimSpace(cfg.PollInterval))
	if err != nil || interval < time.Second || interval > time.Minute {
		return errors.New("pollInterval must be between 1s and 1m")
	}
	if len(cfg.Sources) == 0 {
		return errors.New("sources must not be empty")
	}
	seenSources := map[string]struct{}{}
	for _, source := range cfg.Sources {
		if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.ExternalID) == "" || len(source.Points) == 0 {
			return errors.New("source name, externalId and points are required")
		}
		if _, exists := seenSources[source.Name]; exists {
			return fmt.Errorf("source %s is duplicated", source.Name)
		}
		seenSources[source.Name] = struct{}{}
		seenPoints := map[string]struct{}{}
		for _, point := range source.Points {
			if strings.TrimSpace(point.SourceKey) == "" || strings.TrimSpace(point.TelemetryKey) == "" {
				return fmt.Errorf("source %s has an invalid point mapping", source.Name)
			}
			if point.Scale == 0 || math.IsNaN(point.Scale) || math.IsInf(point.Scale, 0) || math.IsNaN(point.Offset) || math.IsInf(point.Offset, 0) {
				return fmt.Errorf("source %s point %s has an invalid transform", source.Name, point.SourceKey)
			}
			if _, exists := seenPoints[point.SourceKey]; exists {
				return fmt.Errorf("source %s point %s is duplicated", source.Name, point.SourceKey)
			}
			seenPoints[point.SourceKey] = struct{}{}
		}
	}
	return nil
}

func newReconciler(cfg config, thingsBoard *thingsBoardClient, runtime *adapter.TelemetryRuntimeClient, logger *slog.Logger) *reconciler {
	interval, _ := time.ParseDuration(cfg.PollInterval)
	bindings := make(map[string]*pointBinding)
	for _, source := range cfg.Sources {
		for _, point := range source.Points {
			bindings[source.Name+"\x00"+point.SourceKey] = &pointBinding{Source: source, Point: point}
		}
	}
	return &reconciler{integrationID: cfg.IntegrationInstanceID, pollInterval: interval, thingsBoard: thingsBoard, runtime: runtime, sources: cfg.Sources, bindings: bindings, logger: logger}
}

func (r *reconciler) run(ctx context.Context) error {
	if err := r.poll(ctx); err != nil {
		r.logger.Warn("thingsboard_reconciliation_poll_failed", "error", err.Error())
	}
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.poll(ctx); err != nil {
				r.logger.Warn("thingsboard_reconciliation_poll_failed", "error", err.Error())
			}
		}
	}
}

func (r *reconciler) poll(ctx context.Context) error {
	for _, source := range r.sources {
		rows, err := r.thingsBoard.latest(ctx, source)
		if err != nil {
			return fmt.Errorf("read ThingsBoard source %s: %w", source.Name, err)
		}
		for _, row := range rows {
			binding := r.bindings[row.SourceName+"\x00"+row.SourceKey]
			if binding == nil || row.Timestamp <= binding.LastAccepted {
				continue
			}
			if err := r.accept(ctx, binding, row); err != nil {
				r.logger.Warn("thingsboard_observation_accept_failed", "source", row.SourceName, "key", row.SourceKey, "sampled_at_ms", row.Timestamp, "error", err.Error())
				continue
			}
			binding.LastAccepted = row.Timestamp
		}
	}
	return nil
}

func (r *reconciler) accept(ctx context.Context, binding *pointBinding, row latestRow) error {
	value := row.Value*binding.Point.Scale + binding.Point.Offset
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return errors.New("transformed value is not finite")
	}
	var unit *string
	if configuredUnit := strings.TrimSpace(binding.Point.Unit); configuredUnit != "" {
		unit = &configuredUnit
	}
	partition := "thingsboard:" + binding.Source.ExternalID + ":" + binding.Point.SourceKey
	receipt, err := r.runtime.AcceptObservation(ctx, adapter.Observation{
		IntegrationInstanceID: r.integrationID,
		SourcePath:            "RECONCILIATION",
		ExternalEntityType:    "DEVICE",
		ExternalID:            binding.Source.ExternalID,
		TelemetryKey:          binding.Point.TelemetryKey,
		Value:                 value,
		ValueType:             "NUMBER",
		Unit:                  unit,
		WireQuality:           0,
		SampledAt:             time.UnixMilli(row.Timestamp).UTC().Format(time.RFC3339Nano),
		SourcePosition: adapter.SourcePosition{
			Partition: partition,
			Offset:    row.Timestamp,
			EventID:   deterministicUUIDv7(row.Timestamp, partition, value),
		},
	})
	if err != nil {
		return err
	}
	switch receipt.Status {
	case "ACCEPTED", "DUPLICATE", "OUT_OF_ORDER":
		r.logger.Info("thingsboard_observation_reconciled", "source", row.SourceName, "key", row.SourceKey, "status", receipt.Status, "device_id", receipt.DeviceID, "business_revision", receipt.BusinessRevision)
		return nil
	case "QUARANTINED", "REJECTED":
		return fmt.Errorf("telemetry runtime returned %s (%s)", receipt.Status, receipt.QuarantineReason)
	default:
		return fmt.Errorf("telemetry runtime returned unexpected status %s", receipt.Status)
	}
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

func deterministicUUIDv7(timestampMS int64, partition string, value float64) string {
	if timestampMS < 0 {
		timestampMS = 0
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%.17g", partition, timestampMS, value)))
	var bytes [16]byte
	bytes[0] = byte(timestampMS >> 40)
	bytes[1] = byte(timestampMS >> 32)
	bytes[2] = byte(timestampMS >> 24)
	bytes[3] = byte(timestampMS >> 16)
	bytes[4] = byte(timestampMS >> 8)
	bytes[5] = byte(timestampMS)
	bytes[6] = 0x70 | (hash[0] & 0x0f)
	bytes[7] = hash[1]
	bytes[8] = 0x80 | (hash[2] & 0x3f)
	copy(bytes[9:], hash[3:10])
	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func uuidV7(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return len(value) == 36 && value[8] == '-' && value[13] == '-' && value[14] == '7' && value[18] == '-' && strings.ContainsRune("89ab", rune(value[19])) && value[23] == '-'
}
