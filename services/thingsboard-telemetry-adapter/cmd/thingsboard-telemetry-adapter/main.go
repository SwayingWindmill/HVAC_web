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

	"github.com/quanlaihe/hvac-web/services/thingsboard-telemetry-adapter/internal/adapter"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configPath := flag.String("config", strings.TrimSpace(os.Getenv("THINGSBOARD_TELEMETRY_ADAPTER_CONFIG")), "path to the adapter JSON config")
	diagnosticsAddress := flag.String("diagnostics-addr", envOr("THINGSBOARD_TELEMETRY_ADAPTER_DIAGNOSTICS_ADDR", ":19093"), "health server listen address")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		logger.Error("thingsboard_telemetry_adapter_config_required")
		os.Exit(2)
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_config_open_failed", "error", err.Error())
		os.Exit(1)
	}
	config, err := adapter.DecodeConfig(configFile)
	_ = configFile.Close()
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_config_invalid", "error", err.Error())
		os.Exit(1)
	}

	thingsBoard, err := adapter.NewThingsBoardClient(
		config.ThingsBoard.BaseURL,
		adapter.FileTokenProvider{Path: config.ThingsBoard.JWTFile},
		nil,
	)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_provider_invalid", "error", err.Error())
		os.Exit(1)
	}
	telemetryHTTPClient, err := adapter.NewMTLSHTTPClient(config.TelemetryRuntime)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_mtls_invalid", "error", err.Error())
		os.Exit(1)
	}
	telemetryRuntime, err := adapter.NewTelemetryRuntimeClient(config.TelemetryRuntime.BaseURL, telemetryHTTPClient)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_runtime_client_invalid", "error", err.Error())
		os.Exit(1)
	}
	checkpoints, err := adapter.OpenCheckpointStore(config.CheckpointFile)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_checkpoint_invalid", "error", err.Error())
		os.Exit(1)
	}
	pipeline, err := adapter.NewPipeline(config, thingsBoard, telemetryRuntime, checkpoints, time.Now)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_pipeline_invalid", "error", err.Error())
		os.Exit(1)
	}
	runtime, err := adapter.NewRuntime(pipeline, config.PollDuration(), logger)
	if err != nil {
		logger.Error("thingsboard_telemetry_adapter_runtime_invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	diagnostics := diagnosticsServer(*diagnosticsAddress, runtime)
	errCh := make(chan error, 2)
	go func() {
		logger.Info("thingsboard_telemetry_adapter_diagnostics_started", "address", *diagnosticsAddress)
		if serveErr := diagnostics.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	go func() {
		logger.Info("thingsboard_telemetry_adapter_started", "integration_instance_id", config.IntegrationInstanceID, "device_count", len(config.Devices))
		errCh <- runtime.Run(ctx)
	}()

	select {
	case <-ctx.Done():
	case runErr := <-errCh:
		if runErr != nil {
			logger.Error("thingsboard_telemetry_adapter_failed", "error", runErr.Error())
		}
		cancel()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	logger.Info("thingsboard_telemetry_adapter_stopped")
}

func diagnosticsServer(address string, runtime *adapter.Runtime) *http.Server {
	mux := http.NewServeMux()
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
