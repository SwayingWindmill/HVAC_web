package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/scheduler/pkg/scheduler"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "scheduler", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := openStore(ctx)
	if err != nil {
		logger.Error("scheduler_configuration_invalid", "error_code", "SCHEDULER_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	defer store.Close()
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})

	scanInterval := durationEnv("SCHEDULER_SCAN_INTERVAL", 2*time.Second, time.Second, time.Minute)
	scanBatch := integerEnv("SCHEDULER_SCAN_BATCH", 100, 1, 1000)
	fireTolerance := 2 * scanInterval
	if fireTolerance < time.Minute {
		fireTolerance = time.Minute
	}

	diagnostics := &http.Server{
		Addr: envOr("SCHEDULER_DIAGNOSTICS_ADDR", ":19092"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("scheduler_diagnostics_failed", "error_code", "SCHEDULER_DIAGNOSTICS_SERVE_FAILED")
			cancel()
		}
	}()

	telemetry.MarkReady()
	logger.Info("scheduler_started", "scan_interval", scanInterval.String(), "scan_batch", scanBatch)
	run(ctx, store, scanInterval, scanBatch, fireTolerance, telemetry, logger)
	telemetry.MarkNotReady()

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("scheduler_stopped")
}

func openStore(ctx context.Context) (*scheduler.Store, error) {
	dsn := strings.TrimSpace(os.Getenv("SCHEDULER_POSTGRES_DSN"))
	if dsn == "" {
		return nil, errors.New("SCHEDULER_POSTGRES_DSN is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse scheduler PostgreSQL DSN: %w", err)
	}
	if config.ConnConfig.User != "scheduler_runtime" {
		return nil, errors.New("scheduler PostgreSQL identity must be scheduler_runtime")
	}
	config.MaxConns = 4
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := scheduler.NewStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func run(ctx context.Context, store *scheduler.Store, interval time.Duration, batch int, fireTolerance time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	runCycle(ctx, store, batch, fireTolerance, telemetry, logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle(ctx, store, batch, fireTolerance, telemetry, logger)
		}
	}
}

func runCycle(ctx context.Context, store *scheduler.Store, batch int, fireTolerance time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	cycleContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := store.Cycle(cycleContext, time.Now().UTC(), batch, fireTolerance)
	if err != nil {
		_ = telemetry.Metrics.AddCounter("scheduler_scan_error_total", "Scheduler scan errors.", nil, 1)
		logger.Warn("scheduler_scan_failed", "error_code", "SCHEDULER_SCAN_FAILED")
		return
	}
	_ = telemetry.Metrics.AddCounter("scheduler_scan_total", "Scheduler scans.", nil, 1)
	if result.Created > 0 {
		_ = telemetry.Metrics.AddCounter("job_created_total", "Durable jobs created by Scheduler.", map[string]string{"trigger_type": "SCHEDULE"}, float64(result.Created))
	}
	if result.Skipped > 0 {
		_ = telemetry.Metrics.AddCounter("job_skipped_total", "Jobs skipped by Scheduler policy.", nil, float64(result.Skipped))
	}
	if result.Recovered > 0 {
		_ = telemetry.Metrics.AddCounter("job_recovered_total", "Expired job leases recovered by Scheduler.", nil, float64(result.Recovered))
	}
	if result.Retries > 0 {
		_ = telemetry.Metrics.AddCounter("job_retry_total", "Retry-wait jobs promoted to ready.", nil, float64(result.Retries))
	}
	if result.Cancelled > 0 {
		_ = telemetry.Metrics.AddCounter("job_cancelled_total", "Jobs cancelled before execution by Scheduler coordination.", nil, float64(result.Cancelled))
	}
	if result.Promoted > 0 {
		_ = telemetry.Metrics.AddCounter("job_ready_total", "Pending Jobs promoted to READY by Scheduler coordination.", nil, float64(result.Promoted))
	}
	_ = telemetry.Metrics.SetGauge("scheduler_due_schedule_count", "Due schedules observed in the latest scan.", nil, float64(result.Schedules))
	statsContext, statsCancel := context.WithTimeout(ctx, 3*time.Second)
	stats, statsErr := store.QueueStats(statsContext, time.Now().UTC())
	statsCancel()
	if statsErr == nil {
		_ = telemetry.Metrics.SetGauge("job_ready_count", "Jobs currently ready to claim.", nil, float64(stats.Ready))
		_ = telemetry.Metrics.SetGauge("job_retry_wait_count", "Jobs currently waiting for retry.", nil, float64(stats.RetryWait))
		_ = telemetry.Metrics.SetGauge("job_running", "Jobs currently claimed or running.", nil, float64(stats.Running))
		_ = telemetry.Metrics.SetGauge("job_oldest_ready_age_seconds", "Age of the oldest ready Job.", nil, stats.OldestReadyAgeSeconds)
	}
	if result.Schedules > 0 || result.Created > 0 || result.Recovered > 0 || result.Retries > 0 || result.Cancelled > 0 || result.Promoted > 0 {
		logger.Info("scheduler_cycle_completed", "due_schedules", result.Schedules, "jobs_created", result.Created, "jobs_skipped", result.Skipped, "leases_recovered", result.Recovered, "retries_ready", result.Retries, "jobs_cancelled", result.Cancelled, "jobs_promoted", result.Promoted)
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
