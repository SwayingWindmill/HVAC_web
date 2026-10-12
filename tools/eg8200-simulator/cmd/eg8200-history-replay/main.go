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
	ReplayDatasetID string          `json:"replayDatasetId"`
	DeviceID        string          `json:"deviceId"`
	TelemetryKey    string          `json:"telemetryKey"`
	Value           json.RawMessage `json:"value"`
	ValueType       string          `json:"valueType"`
	Unit            *string         `json:"unit"`
	WireQuality     uint8           `json:"wireQuality"`
	SampledAt       time.Time       `json:"sampledAt"`
	Offset          int64           `json:"offset"`
}

type replayAdmitter func(context.Context, replayObservationRequest) error

// replayPlan is what one replay produces: the dataset, its window, the Plant step, the points kept
// (all when nil, keyed "DEVICE/telemetry.key") and the commands applied as the replay reaches them.
type replayPlan struct {
	DatasetID string
	From      time.Time
	Duration  time.Duration
	Step      time.Duration
	Points    map[string]bool
	Commands  []scheduledCommand
}

// scheduledCommand is an operator command applied to the Plant at a moment of the replay.
type scheduledCommand struct {
	At      time.Time
	Command simulator.Command
}

type scheduledCommandFile struct {
	At       time.Time          `json:"at"`
	DeviceID string             `json:"deviceId"`
	Method   string             `json:"method"`
	Params   map[string]float64 `json:"params"`
}

type replayClient struct {
	endpoint   string
	httpClient *http.Client
	// retryWait is how long to wait before sending an observation again after a retryable refusal.
	retryWait func(attempt int) time.Duration
}

// maximumReplayAttempts bounds how long one observation waits out rate limiting before the replay stops.
const maximumReplayAttempts = 40

// backoff waits a little longer after each refusal, up to five seconds.
func backoff(attempt int) time.Duration {
	return min(5*time.Second, time.Duration(attempt)*250*time.Millisecond)
}

func main() {
	plantConfigPath := flag.String("plant-config", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_PLANT_CONFIG")), "path to the canonical Virtual Central Plant config")
	deviceIDsPath := flag.String("device-ids", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_DEVICE_IDS")), "path to the JSON object mapping each simulator device to its platform Device id")
	telemetryURL := flag.String("telemetry-url", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TELEMETRY_URL")), "HTTPS origin for Telemetry Runtime")
	datasetID := flag.String("dataset-id", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_DATASET_ID")), "stable replay dataset UUIDv7")
	fromValue := flag.String("from", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_FROM")), "historical replay start time in RFC3339")
	durationValue := flag.String("duration", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_DURATION")), "finite replay duration")
	stepValue := flag.String("step", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_STEP")), "Plant step, the sample interval when empty")
	pointsValue := flag.String("points", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_POINTS")), "comma-separated DEVICE/telemetry.key points to replay, all when empty")
	commandsPath := flag.String("commands", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_COMMANDS")), "JSON file of commands to apply during the replay")
	scenarioPath := flag.String("scenario", strings.TrimSpace(os.Getenv("EG8200_SCENARIO")), "the Site's daily scenario, the plant config's when empty")
	certFile := flag.String("tls-cert", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TLS_CERT")), "replay workload TLS certificate")
	keyFile := flag.String("tls-key", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_TLS_KEY")), "replay workload TLS private key")
	caFile := flag.String("server-ca", strings.TrimSpace(os.Getenv("EG8200_HISTORY_REPLAY_SERVER_CA")), "Telemetry Runtime server CA")
	serverName := flag.String("server-name", envOr("EG8200_HISTORY_REPLAY_SERVER_NAME", "telemetry-runtime-service"), "Telemetry Runtime TLS server name")
	flag.Parse()

	if strings.TrimSpace(*plantConfigPath) == "" || strings.TrimSpace(*deviceIDsPath) == "" {
		log.Fatal("historical replay requires plant-config and device-ids")
	}
	if !uuidV7Pattern.MatchString(strings.TrimSpace(*datasetID)) {
		log.Fatal("historical replay dataset-id must be UUIDv7")
	}
	from, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*fromValue))
	if err != nil {
		log.Fatal("historical replay from must be RFC3339")
	}
	duration, err := time.ParseDuration(strings.TrimSpace(*durationValue))
	if err != nil || duration <= 0 {
		log.Fatal("historical replay duration must be positive")
	}
	plantConfig, deviceIDs, err := loadReplayConfigs(*plantConfigPath, *deviceIDsPath)
	if err != nil {
		log.Fatal(err)
	}
	// A local Site replays the same weather and load day its live simulator runs.
	if path := strings.TrimSpace(*scenarioPath); path != "" {
		scenarioFile, err := os.Open(path)
		if err != nil {
			log.Fatal(err)
		}
		scenario, err := simulator.DecodeScenarioJSON(scenarioFile)
		_ = scenarioFile.Close()
		if err != nil {
			log.Fatal(err)
		}
		plantConfig.Scenario = scenario
	}
	plan := replayPlan{DatasetID: strings.ToLower(strings.TrimSpace(*datasetID)), From: from.UTC(), Duration: duration}
	if value := strings.TrimSpace(*stepValue); value != "" {
		if plan.Step, err = time.ParseDuration(value); err != nil || plan.Step <= 0 {
			log.Fatal("historical replay step must be a positive duration")
		}
	}
	if value := strings.TrimSpace(*pointsValue); value != "" {
		plan.Points = map[string]bool{}
		for _, point := range strings.Split(value, ",") {
			plan.Points[strings.TrimSpace(point)] = true
		}
	}
	if path := strings.TrimSpace(*commandsPath); path != "" {
		if plan.Commands, err = loadScheduledCommands(path); err != nil {
			log.Fatal(err)
		}
	}
	client, err := newReplayClient(*telemetryURL, *certFile, *keyFile, *caFile, *serverName)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	count, err := runReplay(ctx, plantConfig, deviceIDs, plan, client.Admit)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("historical replay admitted %d observations", count)
}

func loadReplayConfigs(plantPath, deviceIDsPath string) (simulator.Config, map[string]string, error) {
	plantFile, err := os.Open(plantPath)
	if err != nil {
		return simulator.Config{}, nil, fmt.Errorf("open replay plant config: %w", err)
	}
	plantConfig, decodeErr := simulator.DecodeConfig(plantFile)
	closeErr := plantFile.Close()
	if decodeErr != nil {
		return simulator.Config{}, nil, decodeErr
	}
	if closeErr != nil {
		return simulator.Config{}, nil, closeErr
	}
	raw, err := os.ReadFile(deviceIDsPath)
	if err != nil {
		return simulator.Config{}, nil, fmt.Errorf("read replay Device ids: %w", err)
	}
	var deviceIDs map[string]string
	if err := json.Unmarshal(raw, &deviceIDs); err != nil {
		return simulator.Config{}, nil, fmt.Errorf("decode replay Device ids: %w", err)
	}
	for _, deviceID := range plantConfig.ReportingDeviceIDs() {
		if !uuidV7Pattern.MatchString(deviceIDs[deviceID]) {
			return simulator.Config{}, nil, fmt.Errorf("replay Device id for %s is missing or not UUIDv7", deviceID)
		}
	}
	return plantConfig, deviceIDs, nil
}

func loadScheduledCommands(path string) ([]scheduledCommand, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay commands: %w", err)
	}
	var entries []scheduledCommandFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode replay commands: %w", err)
	}
	commands := make([]scheduledCommand, 0, len(entries))
	for _, entry := range entries {
		if entry.At.IsZero() || strings.TrimSpace(entry.DeviceID) == "" || strings.TrimSpace(entry.Method) == "" {
			return nil, errors.New("a replay command needs at, deviceId and method")
		}
		commands = append(commands, scheduledCommand{At: entry.At.UTC(), Command: simulator.Command{DeviceID: entry.DeviceID, Method: entry.Method, Params: entry.Params}})
	}
	return commands, nil
}

func runReplay(ctx context.Context, config simulator.Config, deviceIDs map[string]string, plan replayPlan, admit replayAdmitter) (int, error) {
	datasetID, from, duration := plan.DatasetID, plan.From, plan.Duration
	if admit == nil || !uuidV7Pattern.MatchString(datasetID) || from.IsZero() || duration <= 0 || plan.Step < 0 {
		return 0, errors.New("historical replay runner configuration is invalid")
	}
	commands := slices.Clone(plan.Commands)
	slices.SortStableFunc(commands, func(left, right scheduledCommand) int { return left.At.Compare(right.At) })
	pointByReference := make(map[string]simulator.PointConfig, len(config.Points))
	for _, point := range config.Points {
		pointByReference[point.DeviceID+"\x00"+point.TelemetryKey] = point
	}
	for reference := range plan.Points {
		device, key, _ := strings.Cut(reference, "/")
		if _, ok := pointByReference[device+"\x00"+key]; !ok {
			return 0, fmt.Errorf("historical replay point %s is not in the plant config", reference)
		}
	}
	plant := simulator.NewPlant(config.Plant, config.Scenario, from.UTC())
	scheduler, err := simulator.NewMeasurementScheduler(config)
	if err != nil {
		return 0, err
	}
	offsets := make(map[string]int64, len(deviceIDs))
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
			if plan.Points != nil && !plan.Points[measurement.DeviceID+"/"+measurement.TelemetryKey] {
				continue
			}
			point, ok := pointByReference[measurement.DeviceID+"\x00"+measurement.TelemetryKey]
			if !ok {
				return fmt.Errorf("historical replay point metadata is missing for %s/%s", measurement.DeviceID, measurement.TelemetryKey)
			}
			deviceID := strings.TrimSpace(deviceIDs[measurement.DeviceID])
			if deviceID == "" {
				return fmt.Errorf("historical replay Device identity is missing for %s", measurement.DeviceID)
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
				ReplayDatasetID: datasetID,
				DeviceID:        deviceID,
				TelemetryKey:    strings.TrimSpace(point.PointCode),
				Value:           value,
				ValueType:       strings.TrimSpace(point.ValueType),
				Unit:            unit,
				WireQuality:     wireQuality,
				SampledAt:       measurement.ObservedAt.UTC(),
				Offset:          offsets[deviceID],
			}
			if err := admit(ctx, request); err != nil {
				return err
			}
			offsets[deviceID]++
			admitted++
		}
		return nil
	}

	if err := emit(plant.Snapshot()); err != nil {
		return admitted, err
	}
	elapsed := time.Duration(0)
	interval := cmp.Or(plan.Step, config.Interval())
	for elapsed < duration {
		for len(commands) > 0 && !commands[0].At.After(from.Add(elapsed)) {
			if result := plant.ApplyCommand(commands[0].Command); !result.Success {
				return admitted, fmt.Errorf("replay command %s on %s at %s was refused: %s", commands[0].Command.Method, commands[0].Command.DeviceID, commands[0].At.Format(time.RFC3339), result.Code)
			}
			commands = commands[1:]
		}
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
		retryWait: backoff,
		endpoint:  strings.TrimRight(base.String(), "/") + historicalReplayObservationPath,
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
	for attempt := 1; ; attempt++ {
		status, response, err := client.send(ctx, body)
		if err != nil {
			return err
		}
		if status == http.StatusOK {
			return nil
		}
		// Rate limiting and a briefly unavailable store are retryable; anything else is a bad observation.
		if (status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable) || attempt == maximumReplayAttempts {
			return fmt.Errorf("Historical Replay admission returned %d: %s", status, response)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(client.retryWait(attempt)):
		}
	}
}

func (client *replayClient) send(ctx context.Context, body []byte) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("create Historical Replay request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("admit Historical Replay observation: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumReplayResponseBytes+1))
	if err != nil {
		return 0, "", fmt.Errorf("read Historical Replay response: %w", err)
	}
	if int64(len(payload)) > maximumReplayResponseBytes {
		return 0, "", errors.New("Historical Replay response exceeds limit")
	}
	return response.StatusCode, strings.TrimSpace(string(payload)), nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
