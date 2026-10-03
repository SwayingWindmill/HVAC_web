package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "mqtt-telemetry-adapter", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("MQTT_TELEMETRY_ADAPTER_DIAGNOSTICS_ADDR", ":19094"), "health server listen address")
	flag.Parse()
	config := uplinkConfig()
	if err := config.Validate(); err != nil {
		logger.Error("connectivity_uplink_config_invalid", "cause", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	connectivityDatabaseURL := strings.TrimSpace(os.Getenv("CONNECTIVITY_DATABASE_URL"))
	connectivityStore, err := connectivity.Open(ctx, connectivityDatabaseURL)
	if err != nil {
		logger.Error("iot_connectivity_store_unavailable", "error", err.Error())
		os.Exit(1)
	}
	defer connectivityStore.Close()
	telemetryRuntime, err := adapter.NewTelemetryRuntimeClient(config.TelemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_client_invalid", "error", err.Error())
		os.Exit(1)
	}
	processor, err := adapter.NewProcessor(connectivityStore, telemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_processor_invalid", "error", err.Error())
		os.Exit(1)
	}
	runtime, err := adapter.NewRuntime(config, processor, logger, telemetry.Metrics)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_invalid", "error", err.Error())
		os.Exit(1)
	}

	// Commands are part of Connectivity when enabled: if they cannot start, the process
	// fails instead of reporting healthy without a command channel.
	var commands *commandRuntime
	if strings.EqualFold(strings.TrimSpace(os.Getenv("COMMAND_RUNTIME_IN_PROCESS_ENABLED")), "true") {
		if commands, err = loadCommandRuntime(ctx, connectivityStore); err != nil {
			logger.Error("connectivity_command_runtime_unavailable", "error_code", "COMMAND_MODULE_UNAVAILABLE", "error", err.Error())
			os.Exit(1)
		}
	}
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime, telemetry)
	diagnosticsErr := make(chan error, 1)
	go func() {
		logger.Info("mqtt_telemetry_adapter_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			diagnosticsErr <- serveErr
		}
	}()
	go func() {
		logger.Info("connectivity_uplink_started", "topic_filters", adapter.UplinkTopicFilters)
		if runErr := runtime.Run(ctx); runErr != nil && ctx.Err() == nil {
			// Telemetry ingress is its own fault domain. Command delivery remains
			// running; telemetry readiness becomes false until the process is repaired.
			logger.Error("mqtt_telemetry_module_stopped", "error_code", "TELEMETRY_MODULE_STOPPED", "error", runErr.Error())
		}
	}()
	if commands != nil {
		go commands.Run(ctx, logger)
	}

	select {
	case <-ctx.Done():
	case diagnosticsRunErr := <-diagnosticsErr:
		logger.Error("mqtt_telemetry_adapter_diagnostics_failed", "error", diagnosticsRunErr.Error())
		cancel()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	if commands != nil {
		_ = commands.Close(shutdownContext)
	}
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("mqtt_telemetry_adapter_stopped")
}

// uplinkConfig reads the uplink settings; the defaults are the in-container paths of
// Connectivity's workload identity and the Phase 1 service addresses.
func uplinkConfig() adapter.Config {
	identityCert := envOr("CONNECTIVITY_TLS_CERT", "/run/hvac/pki/mqtt-telemetry-adapter/tls.crt")
	identityKey := envOr("CONNECTIVITY_TLS_KEY", "/run/hvac/pki/mqtt-telemetry-adapter/tls.key")
	ca := envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt")
	return adapter.Config{
		MQTT: adapter.MQTTConfig{
			BrokerURL: envOr("CONNECTIVITY_MQTT_URL", "tls://mqtt-broker:8883"),
			ClientID:  envOr("CONNECTIVITY_MQTT_CLIENT_ID", "connectivity"),
			CAFile:    ca, CertFile: identityCert, KeyFile: identityKey,
			ServerName: envOr("CONNECTIVITY_MQTT_SERVER_NAME", "mqtt-broker"),
		},
		TelemetryRuntime: adapter.TelemetryRuntimeConfig{
			BaseURL: envOr("CONNECTIVITY_TELEMETRY_URL", "https://telemetry-runtime-service:8446"),
			CAFile:  ca, CertFile: identityCert, KeyFile: identityKey,
			ServerName: envOr("CONNECTIVITY_TELEMETRY_SERVER_NAME", "telemetry-runtime-service"),
		},
		QueueCapacity: queueCapacity(),
	}
}

// queueCapacity bounds each Gateway's in-process backlog (CONNECTIVITY_QUEUE_CAPACITY,
// default 1024); beyond it the broker holds the Gateway's messages.
func queueCapacity() int {
	capacity, err := strconv.Atoi(envOr("CONNECTIVITY_QUEUE_CAPACITY", "1024"))
	if err != nil {
		return 0
	}
	return capacity
}

func diagnosticsServer(address string, runtime *adapter.Runtime, telemetry *observability.Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.Metrics.Handler())
	mux.HandleFunc("/health/live", getHealthHandler(func() bool { return true }))
	mux.HandleFunc("/health/ready", getHealthHandler(runtime.Ready))
	mux.HandleFunc("/health/telemetry/ready", getHealthHandler(runtime.Ready))
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func getHealthHandler(ready func() bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ready() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("not ready\n"))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready\n"))
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
