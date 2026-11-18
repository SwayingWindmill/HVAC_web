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
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/scheduler/pkg/scheduler"
)

func main() {
	os.Exit(run())
}

func run() int {
	var closers []func()
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "scheduler", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func() {
		telemetryShutdownOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = telemetry.Shutdown(shutdownCtx)
		})
	}
	defer shutdownTelemetry()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	scanInterval, err := durationEnv("SCHEDULER_SCAN_INTERVAL", 2*time.Second, time.Second, time.Minute)
	if err != nil {
		logger.Error("scheduler_configuration_invalid", "error_code", "SCHEDULER_CONFIGURATION_INVALID", "variable", "SCHEDULER_SCAN_INTERVAL")
		return 1
	}
	scanBatch, err := integerEnv("SCHEDULER_SCAN_BATCH", 100, 1, 1000)
	if err != nil {
		logger.Error("scheduler_configuration_invalid", "error_code", "SCHEDULER_CONFIGURATION_INVALID", "variable", "SCHEDULER_SCAN_BATCH")
		return 1
	}
	fireTolerance := 2 * scanInterval
	if fireTolerance < time.Minute {
		fireTolerance = time.Minute
	}

	store, err := openStore(sigCtx)
	if err != nil {
		logger.Error("scheduler_configuration_invalid", "error_code", "SCHEDULER_CONFIGURATION_INVALID")
		return 1
	}
	var closeStoreOnce sync.Once
	closeStore := func() {
		closeStoreOnce.Do(store.Close)
	}
	closers = append(closers, closeStore)
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})

	diagnostics := &http.Server{
		Addr: envOr("SCHEDULER_DIAGNOSTICS_ADDR", ":19092"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	diagDone := make(chan struct{})
	diagErr := make(chan error, 1)
	go func() {
		defer close(diagDone)
		defer observability.Recover(logger, telemetry.Metrics, "scheduler.diagnostics", func(any) {
			select {
			case diagErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("scheduler_diagnostics_failed", "error_code", "SCHEDULER_DIAGNOSTICS_SERVE_FAILED")
			select {
			case diagErr <- err:
			default:
			}
		}
	}()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		defer observability.Recover(logger, telemetry.Metrics, "scheduler.loop")
		runWorkerLoop(workerCtx, store, scanInterval, scanBatch, fireTolerance, telemetry, logger)
	}()

	telemetry.MarkReady()
	logger.Info("scheduler_started", "scan_interval", scanInterval.String(), "scan_batch", scanBatch)

	exitCode := 0
	select {
	case <-sigCtx.Done():
	case <-diagErr:
		exitCode = 1
	}
	stopSignals()

	shutdownScheduler(logger, telemetry, diagnostics, diagDone, cancelWorker, workerDone, shutdownTelemetry, closeStore, 10*time.Second)
	logger.Info("scheduler_stopped")
	return exitCode
}

func shutdownScheduler(
	logger *slog.Logger,
	telemetry *observability.Runtime,
	diagnostics *http.Server,
	diagDone <-chan struct{},
	cancelWorker context.CancelFunc,
	workerDone <-chan struct{},
	shutdownTelemetry func(),
	closeStore func(),
	drainTimeout time.Duration,
) {
	if drainTimeout <= 0 {
		drainTimeout = 10 * time.Second
	}
	if telemetry != nil {
		telemetry.MarkNotReady()
	}
	if diagnostics != nil {
		httpCtx, cancelHTTP := context.WithTimeout(context.Background(), drainTimeout)
		if err := diagnostics.Shutdown(httpCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = diagnostics.Close()
		}
		if diagDone != nil {
			select {
			case <-diagDone:
			case <-httpCtx.Done():
			}
		}
		cancelHTTP()
	} else if diagDone != nil {
		timer := time.NewTimer(drainTimeout)
		select {
		case <-diagDone:
		case <-timer.C:
		}
		timer.Stop()
	}
	if cancelWorker != nil {
		cancelWorker()
	}
	if workerDone != nil {
		timer := time.NewTimer(drainTimeout)
		select {
		case <-workerDone:
		case <-timer.C:
			if logger != nil {
				logger.Warn("scheduler_shutdown_timeout", "error_code", "SCHEDULER_SHUTDOWN_TIMEOUT")
			}
		}
		timer.Stop()
	}
	if shutdownTelemetry != nil {
		shutdownTelemetry()
	}
	if closeStore != nil {
		closeStore()
	}
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
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
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

func runWorkerLoop(ctx context.Context, store *scheduler.Store, interval time.Duration, batch int, fireTolerance time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
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

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("invalid duration environment variable: %s", name)
	}
	return parsed, nil
}

func integerEnv(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("invalid integer environment variable: %s", name)
	}
	return parsed, nil
}
