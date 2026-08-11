package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/mqtt-telemetry-adapter/internal/adapter"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "mqtt-telemetry-adapter", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	configPath := flag.String("config", strings.TrimSpace(os.Getenv("MQTT_TELEMETRY_ADAPTER_CONFIG")), "path to the MQTT telemetry adapter JSON config")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("MQTT_TELEMETRY_ADAPTER_DIAGNOSTICS_ADDR", ":19094"), "health server listen address")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		logger.Error("mqtt_telemetry_adapter_config_required")
		os.Exit(2)
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	config, err := adapter.DecodeConfig(configFile)
	_ = configFile.Close()
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_config_invalid", "error", err.Error())
		os.Exit(1)
	}
	telemetryRuntime, err := adapter.NewTelemetryRuntimeClient(config.TelemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_client_invalid", "error", err.Error())
		os.Exit(1)
	}
	processor, err := adapter.NewProcessor(config.IntegrationInstanceID, config.GatewayScopes, telemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_processor_invalid", "error", err.Error())
		os.Exit(1)
	}
	runtime, err := adapter.NewRuntime(config, processor, logger, telemetry.Metrics)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime, telemetry)
	errCh := make(chan error, 2)
	go func() {
		logger.Info("mqtt_telemetry_adapter_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	go func() {
		logger.Info("mqtt_telemetry_adapter_started", "integration_instance_id", config.IntegrationInstanceID, "topic_filter", config.MQTT.TopicFilter)
		errCh <- runtime.Run(ctx)
	}()

	select {
	case <-ctx.Done():
	case runErr := <-errCh:
		if runErr != nil {
			logger.Error("mqtt_telemetry_adapter_failed", "error", runErr.Error())
		}
		cancel()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("mqtt_telemetry_adapter_stopped")
}

func diagnosticsServer(address string, runtime *adapter.Runtime, telemetry *observability.Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.Metrics.Handler())
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
		if !runtime.Ready() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("not ready\n"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	})
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
