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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/mqtt-telemetry-adapter/internal/adapter"
	"github.com/quanlaihe/hvac-web/services/mqtt-telemetry-adapter/internal/connectivity"
)

type moduleHealth struct {
	commandReady atomic.Bool
}

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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	connectivityTenantID := strings.TrimSpace(os.Getenv("CONNECTIVITY_TENANT_ID"))
	connectivityDatabaseURL := strings.TrimSpace(os.Getenv("CONNECTIVITY_DATABASE_URL"))
	connectivityStore, err := connectivity.Open(ctx, connectivityDatabaseURL, connectivityTenantID)
	if err != nil {
		logger.Error("iot_connectivity_store_unavailable", "error", err.Error())
		os.Exit(1)
	}
	defer connectivityStore.Close()
	integration, err := connectivityStore.LoadIntegration(ctx, config.IntegrationInstanceID)
	if err != nil {
		logger.Error("iot_integration_instance_unavailable", "integration_instance_id", config.IntegrationInstanceID)
		os.Exit(1)
	}
	if integration.TopicNamespace != "energy/v1" {
		logger.Error("iot_transport_profile_mismatch", "integration_instance_id", integration.ID)
		os.Exit(1)
	}
	config.MQTT.BrokerURL = integration.BrokerOrigin
	config.RuntimeGatewayIDs = []string{integration.GatewayExternalID}

	telemetryRuntime, err := adapter.NewTelemetryRuntimeClient(config.TelemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_client_invalid", "error", err.Error())
		os.Exit(1)
	}
	processor, err := adapter.NewProcessor(config.IntegrationInstanceID, connectivityStore, telemetryRuntime)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_processor_invalid", "error", err.Error())
		os.Exit(1)
	}
	runtime, err := adapter.NewRuntime(config, processor, logger, telemetry.Metrics)
	if err != nil {
		logger.Error("mqtt_telemetry_adapter_runtime_invalid", "error", err.Error())
		os.Exit(1)
	}

	health := &moduleHealth{}
	commandRuntime, commandErr := loadInProcessCommandRuntime(ctx, connectivityStore, integration)
	if commandErr != nil {
		logger.Error("iot_command_runtime_unavailable", "error_code", "COMMAND_MODULE_UNAVAILABLE", "error", commandErr.Error())
	} else if commandRuntime != nil {
		commandRuntime.SetReadySink(health.commandReady.Store)
	}
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime, health, telemetry)
	diagnosticsErr := make(chan error, 1)
	go func() {
		logger.Info("mqtt_telemetry_adapter_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			diagnosticsErr <- serveErr
		}
	}()
	go func() {
		logger.Info("mqtt_telemetry_adapter_started", "integration_instance_id", config.IntegrationInstanceID, "gateway_id", integration.GatewayExternalID, "topic_filters", config.MQTT.TopicFilters)
		if runErr := runtime.Run(ctx); runErr != nil && ctx.Err() == nil {
			// Telemetry ingress is its own fault domain. Command delivery remains
			// running; telemetry readiness becomes false until the process is repaired.
			logger.Error("mqtt_telemetry_module_stopped", "error_code", "TELEMETRY_MODULE_STOPPED", "error", runErr.Error())
		}
	}()
	if commandRuntime != nil {
		go commandRuntime.Run(ctx, logger)
	}
	go runCredentialExpiry(ctx, connectivityStore, logger)

	select {
	case <-ctx.Done():
	case diagnosticsRunErr := <-diagnosticsErr:
		logger.Error("mqtt_telemetry_adapter_diagnostics_failed", "error", diagnosticsRunErr.Error())
		cancel()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = commandRuntime.Close(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("mqtt_telemetry_adapter_stopped")
}

func runCredentialExpiry(ctx context.Context, store *connectivity.Store, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := store.ExpireDueCredentials(ctx); err != nil {
				logger.Error("iot_credential_expiry_failed", "error_code", "CREDENTIAL_EXPIRY_FAILED")
			}
		}
	}
}

func diagnosticsServer(address string, runtime *adapter.Runtime, health *moduleHealth, telemetry *observability.Runtime) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.Metrics.Handler())
	mux.HandleFunc("/health/live", getHealthHandler(func() bool { return true }))
	mux.HandleFunc("/health/ready", getHealthHandler(runtime.Ready))
	mux.HandleFunc("/health/telemetry/ready", getHealthHandler(runtime.Ready))
	mux.HandleFunc("/health/command/ready", getHealthHandler(func() bool { return health.commandReady.Load() }))
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
