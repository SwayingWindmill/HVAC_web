package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/maintenance-service/internal/maintenance"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "maintenance-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := openStore(ctx)
	if err != nil {
		logger.Error("maintenance_configuration_invalid", "error_code", "MAINTENANCE_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	defer store.Close()
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})

	hostname, _ := os.Hostname()
	workerID := strings.TrimSpace(os.Getenv("MAINTENANCE_WORKER_ID"))
	if workerID == "" {
		workerID = "maintenance-service:" + hostname
	}
	worker := &maintenance.Worker{
		Store: store,
		WorkerID: workerID,
		Batch: integerEnv("MAINTENANCE_CLAIM_BATCH", 10, 1, 100),
		LeaseDuration: durationEnv("MAINTENANCE_LEASE_DURATION", 30*time.Second, 5*time.Second, 10*time.Minute),
		Logger: logger,
	}
	scanInterval := durationEnv("MAINTENANCE_SCAN_INTERVAL", 2*time.Second, time.Second, time.Minute)

	diagnostics := &http.Server{
		Addr: envOr("MAINTENANCE_DIAGNOSTICS_ADDR", ":19093"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("maintenance_diagnostics_failed", "error_code", "MAINTENANCE_DIAGNOSTICS_SERVE_FAILED")
			cancel()
		}
	}()

	telemetry.MarkReady()
	logger.Info("maintenance_service_started", "scan_interval", scanInterval.String(), "worker_id", workerID)
	run(ctx, worker, scanInterval, telemetry, logger)
	telemetry.MarkNotReady()

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("maintenance_service_stopped")
}

func openStore(ctx context.Context) (*maintenance.Store, error) {
	return maintenance.OpenStore(ctx, os.Getenv("MAINTENANCE_POSTGRES_DSN"))
}

func run(ctx context.Context, worker *maintenance.Worker, interval time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	runCycle(ctx, worker, telemetry, logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle(ctx, worker, telemetry, logger)
		}
	}
}

func runCycle(ctx context.Context, worker *maintenance.Worker, telemetry *observability.Runtime, logger *slog.Logger) {
	count, err := worker.Cycle(ctx, time.Now().UTC())
	if err != nil {
		_ = telemetry.Metrics.AddCounter("maintenance_cycle_error_total", "Maintenance worker cycle errors.", nil, 1)
		logger.Warn("maintenance_cycle_failed", "error_code", "MAINTENANCE_CYCLE_FAILED")
		return
	}
	_ = telemetry.Metrics.AddCounter("maintenance_cycle_total", "Maintenance worker cycles.", nil, 1)
	if count > 0 {
		_ = telemetry.Metrics.AddCounter("maintenance_job_claimed_total", "Maintenance jobs claimed.", nil, float64(count))
		logger.Info("maintenance_cycle_completed", "jobs_claimed", count)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		panic("invalid duration environment variable: " + name)
	}
	return parsed
}

func integerEnv(name string, fallback, minimum, maximum int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		panic("invalid integer environment variable: " + name)
	}
	return parsed
}
