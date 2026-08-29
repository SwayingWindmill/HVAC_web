package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

const atv630TemplateKey = "schneider.atv630.cia402-modbus-tcp"

type edgeAcceptanceState struct {
	modbusReady atomic.Bool
	mu          sync.RWMutex
	last        simulator.Snapshot
}

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "eg8200-atv630-edge", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 256, ExportTimeout: 500 * time.Millisecond,
	})
	plantConfigPath := flag.String("plant-config", strings.TrimSpace(os.Getenv("EG8200_SIMULATOR_CONFIG")), "path to the central-plant Point/config contract")
	mqttConfigPath := flag.String("mqtt-config", strings.TrimSpace(os.Getenv("EG8200_MQTT_CONFIG")), "path to the Edge MQTT transport JSON config")
	modbusEndpoint := flag.String("modbus-endpoint", strings.TrimSpace(os.Getenv("ATV630_MODBUS_ENDPOINT")), "production ATV630 Modbus/TCP endpoint")
	templateRevisionFile := flag.String("template-revision-file", strings.TrimSpace(os.Getenv("ATV630_TEMPLATE_REVISION_FILE")), "file containing the released ATV630 Registry TemplateRevision ID")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("ATV630_EDGE_DIAGNOSTICS_ADDR", ":19097"), "ATV630 Edge diagnostics listen address")
	flag.Parse()
	if strings.TrimSpace(*plantConfigPath) == "" || strings.TrimSpace(*mqttConfigPath) == "" || strings.TrimSpace(*modbusEndpoint) == "" || strings.TrimSpace(*templateRevisionFile) == "" {
		logger.Error("atv630_edge_config_required")
		os.Exit(2)
	}
	templateRevisionBytes, err := os.ReadFile(*templateRevisionFile)
	if err != nil {
		logger.Error("atv630_edge_template_revision_read_failed", "error", err.Error())
		os.Exit(1)
	}
	templateRevisionID := strings.TrimSpace(string(templateRevisionBytes))
	if templateRevisionID == "" {
		logger.Error("atv630_edge_template_revision_invalid")
		os.Exit(1)
	}

	plantFile, err := os.Open(*plantConfigPath)
	if err != nil {
		logger.Error("atv630_edge_point_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	fullConfig, err := simulator.DecodeConfig(plantFile)
	_ = plantFile.Close()
	if err != nil {
		logger.Error("atv630_edge_point_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	edgeConfig, err := simulator.ATV630EdgeConfig(fullConfig)
	if err != nil {
		logger.Error("atv630_edge_point_binding_invalid", "error", err.Error())
		os.Exit(1)
	}
	mqttFile, err := os.Open(*mqttConfigPath)
	if err != nil {
		logger.Error("atv630_edge_mqtt_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	mqttConfig, err := simulator.DecodeMQTTGatewayConfig(mqttFile)
	_ = mqttFile.Close()
	if err != nil {
		logger.Error("atv630_edge_mqtt_config_invalid", "error", err.Error())
		os.Exit(1)
	}

	bridge, err := edgecontrol.NewModbusTCPBridge(edgecontrol.ModbusTCPBridgeConfig{
		Endpoint: *modbusEndpoint,
		Timeout:  750 * time.Millisecond,
		Retries:  1,
	})
	if err != nil {
		logger.Error("atv630_edge_modbus_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	defer func() { _ = bridge.Close() }()
	edgeRuntime, err := simulator.NewATV630EdgeControlRuntime(fullConfig, bridge, 1)
	if err != nil {
		logger.Error("atv630_edge_runtime_init_failed", "error", err.Error())
		os.Exit(1)
	}
	timedata, err := edgecontrol.OpenFileTimedata(filepath.Join(mqttConfig.QueueDirectory, "timedata-atv630"), edgecontrol.PriorityLow)
	if err != nil {
		logger.Error("atv630_edge_timedata_init_failed", "error", err.Error())
		os.Exit(1)
	}
	if err := edgeRuntime.AttachTimedata(timedata); err != nil {
		logger.Error("atv630_edge_timedata_attach_failed", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	publisher, err := simulator.NewMQTTPublisher(ctx, fullConfig, mqttConfig, edgeRuntime, telemetry.Metrics)
	if err != nil {
		logger.Error("atv630_edge_mqtt_publisher_invalid", "error", err.Error())
		os.Exit(1)
	}
	connectionContext, connectionCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := publisher.AwaitConnection(connectionContext); err != nil {
		logger.Warn("atv630_edge_mqtt_initial_connection_pending", "error", err.Error())
	}
	connectionCancel()

	sequenceStatePath := filepath.Join(mqttConfig.QueueDirectory, "atv630-measurement-sequences.v1.json")
	initialSequences, err := simulator.LoadMeasurementSequences(sequenceStatePath)
	if err != nil {
		logger.Error("atv630_edge_sequence_state_invalid", "error", err.Error())
		os.Exit(1)
	}
	scheduler, err := simulator.NewMeasurementSchedulerWithSequences(edgeConfig, initialSequences)
	if err != nil {
		logger.Error("atv630_edge_scheduler_invalid", "error", err.Error())
		os.Exit(1)
	}

	state := &edgeAcceptanceState{}
	diagnostics := edgeDiagnosticsServer(*diagnosticsAddress, templateRevisionID, edgeRuntime, publisher, state)
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("atv630_edge_diagnostics_failed", "error", err.Error())
			cancel()
		}
	}()

	interval := edgeConfig.Interval()
	logger.Info("atv630_edge_started", "modbus_endpoint", *modbusEndpoint, "template_key", atv630TemplateKey, "template_revision_id", templateRevisionID, "tick_interval", interval.String())
	runCycle := func() {
		cycleAt := time.Now().UTC()
		cycle := edgeRuntime.RunCycle(ctx, cycleAt)
		for _, poll := range cycle.PollResults {
			if poll.Error != nil {
				state.modbusReady.Store(false)
				logger.Warn("atv630_edge_modbus_poll_failed", "adapter_id", poll.AdapterID, "error", poll.Error.Error())
				return
			}
		}
		if cycle.Cycle.Halted {
			state.modbusReady.Store(false)
			logger.Warn("atv630_edge_cycle_halted", "error", errorText(cycle.Cycle.OutputError))
			return
		}
		state.modbusReady.Store(true)
		state.mu.Lock()
		state.last = cycle.TelemetrySnapshot
		state.mu.Unlock()

		measurements, err := scheduler.Observe(cycle.TelemetrySnapshot)
		if err != nil {
			logger.Error("atv630_edge_observe_failed", "error", err.Error())
			cancel()
			return
		}
		if len(measurements) == 0 {
			return
		}
		if err := simulator.SaveMeasurementSequences(sequenceStatePath, scheduler.Sequences()); err != nil {
			logger.Error("atv630_edge_sequence_state_persist_failed", "error", err.Error())
			cancel()
			return
		}
		publishContext, publishCancel := context.WithTimeout(ctx, 5*time.Second)
		err = publisher.PublishMeasurements(publishContext, measurements)
		publishCancel()
		if err != nil {
			logger.Warn("atv630_edge_mqtt_publish_failed", "error", err.Error())
		}
	}

	runCycle()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = publisher.Disconnect(shutdownContext)
			_ = diagnostics.Shutdown(shutdownContext)
			_ = telemetry.Shutdown(shutdownContext)
			shutdownCancel()
			return
		case <-ticker.C:
			runCycle()
		}
	}
}

func edgeDiagnosticsServer(address, templateRevisionID string, runtime *simulator.EdgeControlRuntime, publisher *simulator.MQTTPublisher, state *edgeAcceptanceState) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		if !publisher.Ready() || !state.modbusReady.Load() {
			http.Error(writer, "not ready", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /acceptance/state", func(writer http.ResponseWriter, _ *http.Request) {
		state.mu.RLock()
		snapshot := state.last
		state.mu.RUnlock()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"templateKey": atv630TemplateKey, "templateRevisionId": templateRevisionID,
			"modbusReady": state.modbusReady.Load(), "mqttReady": publisher.Ready(), "snapshot": snapshot,
		})
	})
	mux.HandleFunc("POST /acceptance/commands/{capability}", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			CommandID string             `json:"commandId"`
			Params    map[string]float64 `json:"params"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.CommandID) == "" {
			http.Error(writer, "invalid command payload", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		outcomeCh, err := runtime.SubmitCommand(simulator.EdgeCommandIntentRequest{
			CommandID: body.CommandID, DeviceID: "CHWP-01", CommandCode: request.PathValue("capability"), Params: body.Params,
			IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
		})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusConflict)
			return
		}
		select {
		case outcome := <-outcomeCh:
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(outcome)
		case <-request.Context().Done():
			http.Error(writer, "command request cancelled", http.StatusRequestTimeout)
		case <-time.After(25 * time.Second):
			http.Error(writer, "command outcome timed out", http.StatusGatewayTimeout)
		}
	})
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
