package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "eg8200-mqtt-publisher", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 256, ExportTimeout: 500 * time.Millisecond,
	})
	plantConfigPath := flag.String("plant-config", strings.TrimSpace(os.Getenv("EG8200_SIMULATOR_CONFIG")), "path to the EG8200 simulator JSON config")
	mqttConfigPath := flag.String("mqtt-config", strings.TrimSpace(os.Getenv("EG8200_MQTT_CONFIG")), "path to the EG8200 MQTT transport JSON config")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("EG8200_MQTT_DIAGNOSTICS_ADDR", ":19095"), "Edge MQTT diagnostics listen address")
	flag.Parse()
	if strings.TrimSpace(*plantConfigPath) == "" || strings.TrimSpace(*mqttConfigPath) == "" {
		logger.Error("eg8200_mqtt_config_required")
		os.Exit(2)
	}

	plantFile, err := os.Open(*plantConfigPath)
	if err != nil {
		logger.Error("eg8200_mqtt_plant_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	plantConfig, err := simulator.DecodeConfig(plantFile)
	_ = plantFile.Close()
	if err != nil {
		logger.Error("eg8200_mqtt_plant_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	mqttFile, err := os.Open(*mqttConfigPath)
	if err != nil {
		logger.Error("eg8200_mqtt_transport_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	mqttConfig, err := simulator.DecodeMQTTGatewayConfig(mqttFile)
	_ = mqttFile.Close()
	if err != nil {
		logger.Error("eg8200_mqtt_transport_config_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	plant := simulator.NewPlant(plantConfig.Plant, plantConfig.Scenario, time.Now().UTC())
	edgeRuntime, err := simulator.NewEdgeControlRuntime(plantConfig, plant)
	if err != nil {
		logger.Error("eg8200_edge_runtime_init_failed", "component", "edge_control_runtime")
		os.Exit(1)
	}
	timedata, err := edgecontrol.OpenFileTimedata(filepath.Join(mqttConfig.QueueDirectory, "timedata"), edgecontrol.PriorityLow)
	if err != nil {
		logger.Error("eg8200_edge_timedata_init_failed", "component", "edge_timedata")
		os.Exit(1)
	}
	if err := edgeRuntime.AttachTimedata(timedata); err != nil {
		logger.Error("eg8200_edge_timedata_attach_failed", "component", "edge_timedata")
		os.Exit(1)
	}
	manifest, err := edgeRuntime.Manifest("central-plant:v1", time.Now().UTC())
	if err != nil {
		logger.Error("eg8200_edge_manifest_failed", "component", "edge_control_runtime")
		os.Exit(1)
	}
	logger.Info("eg8200_edge_runtime_configured", "component_count", len(manifest.Components), "channel_count", len(manifest.Channels), "capability_profile_count", len(manifest.CapabilityProfiles))
	publisher, err := simulator.NewMQTTPublisher(ctx, plantConfig, mqttConfig, edgeRuntime, telemetry.Metrics)
	if err != nil {
		logger.Error("eg8200_mqtt_publisher_invalid", "error", err.Error())
		os.Exit(1)
	}
	diagnostics := edgeDiagnosticsServer(*diagnosticsAddress, publisher, telemetry)
	go func() {
		logger.Info("eg8200_mqtt_diagnostics_started", "address", diagnostics.Addr)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("eg8200_mqtt_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
			cancel()
		}
	}()
	connectionContext, connectionCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := publisher.AwaitConnection(connectionContext); err != nil {
		logger.Warn("eg8200_mqtt_initial_connection_pending", "error", err.Error())
	}
	connectionCancel()

	sequenceStatePath := filepath.Join(mqttConfig.QueueDirectory, "measurement-sequences.v1.json")
	initialSequences, err := simulator.LoadMeasurementSequences(sequenceStatePath)
	if err != nil {
		logger.Error("eg8200_mqtt_sequence_state_invalid", "error", err.Error())
		os.Exit(1)
	}
	scheduler, err := simulator.NewMeasurementSchedulerWithSequences(plantConfig, initialSequences)
	if err != nil {
		logger.Error("eg8200_mqtt_scheduler_invalid", "error", err.Error())
		os.Exit(1)
	}
	interval := plantConfig.Interval()
	logger.Info(
		"eg8200_mqtt_publisher_started",
		"gateway_id", plantConfig.GatewayID,
		"tenant_id", mqttConfig.TenantID,
		"site_id", mqttConfig.SiteID,
		"scheduler_interval", interval.String(),
		"point_count", len(plantConfig.Points),
	)

	publish := func(snapshot simulator.Snapshot) {
		measurements, observeErr := scheduler.Observe(snapshot)
		if observeErr != nil {
			logger.Error("eg8200_mqtt_observe_failed", "error", observeErr.Error())
			cancel()
			return
		}
		if len(measurements) == 0 {
			return
		}
		if stateErr := simulator.SaveMeasurementSequences(sequenceStatePath, scheduler.Sequences()); stateErr != nil {
			logger.Error("eg8200_mqtt_sequence_state_persist_failed", "error", stateErr.Error())
			cancel()
			return
		}
		publishContext, publishCancel := context.WithTimeout(ctx, 5*time.Second)
		publishErr := publisher.PublishMeasurements(publishContext, measurements)
		publishCancel()
		if publishErr != nil {
			logger.Warn("eg8200_mqtt_publish_failed", "point_count", len(measurements), "error", publishErr.Error())
			return
		}
		logger.Info("eg8200_mqtt_publish_queued", "point_count", len(measurements), "observed_at", snapshot.ObservedAt.UTC().Format(time.RFC3339Nano))
	}

	runEdgeCycleAndPublish := func() {
		snapshot := plant.Tick(interval)
		edgeCycle := edgeRuntime.RunCycle(ctx, snapshot.ObservedAt)
		for _, poll := range edgeCycle.PollResults {
			if poll.Error != nil {
				logger.Warn("eg8200_edge_simulator_adapter_poll_failed", "adapter_id", poll.AdapterID)
			}
		}
		for _, controller := range edgeCycle.Cycle.ControllerResults {
			if controller.Error != nil {
				logger.Warn("eg8200_edge_controller_failed", "controller_id", controller.ControllerID, "critical", controller.Critical)
			}
		}
		if edgeCycle.Cycle.OutputError != nil {
			logger.Warn("eg8200_edge_output_failed", "cycle", edgeCycle.Cycle.Image.Cycle())
		}
		if edgeCycle.TimedataError != nil {
			logger.Error("eg8200_edge_timedata_record_failed", "cycle", edgeCycle.Cycle.Image.Cycle())
		}
		publish(edgeCycle.TelemetrySnapshot)
	}

	runEdgeCycleAndPublish()
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
			logger.Info("eg8200_mqtt_publisher_stopped")
			return
		case <-ticker.C:
			runEdgeCycleAndPublish()
		}
	}
}

func edgeDiagnosticsServer(address string, publisher *simulator.MQTTPublisher, telemetry *observability.Runtime) *http.Server {
	mux := http.NewServeMux()
	metrics := telemetry.Metrics.Handler()
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := publisher.RefreshMetrics(); err != nil {
			_ = telemetry.Metrics.AddCounter("hvac_edge_mqtt_metric_refresh_failures_total", "Failures while measuring the local persistent MQTT queue.", nil, 1)
		}
		metrics.ServeHTTP(writer, request)
	})
	mux.HandleFunc("/health/live", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !publisher.Ready() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("not ready\n"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	return &http.Server{
		Addr: address, Handler: mux,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
