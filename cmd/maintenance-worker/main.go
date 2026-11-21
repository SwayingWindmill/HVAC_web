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
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/maintenance/pkg/maintenance"
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
		Service: "maintenance-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
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

	config, err := loadWorkerSettings()
	if err != nil {
		logger.Error("maintenance_configuration_invalid", "error_code", "MAINTENANCE_CONFIGURATION_INVALID", "error", err.Error())
		return 1
	}

	store, err := openStore(sigCtx)
	if err != nil {
		logger.Error("maintenance_configuration_invalid", "error_code", "MAINTENANCE_CONFIGURATION_INVALID", "error", err.Error())
		return 1
	}
	var closeStoreOnce sync.Once
	closeStore := func() {
		closeStoreOnce.Do(store.Close)
	}
	closers = append(closers, closeStore)
	if err := store.VerifyProductSchema(sigCtx); err != nil {
		logger.Error("maintenance_schema_compatibility_invalid", "error_code", "SCHEMA_COMPATIBILITY_INVALID", "cause", err.Error())
		return 1
	}
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: store.Ping})

	hostname, _ := os.Hostname()
	workerID := strings.TrimSpace(os.Getenv("MAINTENANCE_WORKER_ID"))
	if workerID == "" {
		workerID = "maintenance-service:" + hostname
	}
	worker := &maintenance.Worker{
		Store:         store,
		WorkerID:      workerID,
		Batch:         config.batch,
		LeaseDuration: config.leaseDuration,
		Logger:        logger,
	}
	scanInterval := config.scanInterval
	auditInterval := config.auditInterval
	coldArchiveDir := strings.TrimSpace(os.Getenv("MAINTENANCE_COLD_ARCHIVE_DIR"))

	diagnostics := &http.Server{
		Addr: envOr("MAINTENANCE_DIAGNOSTICS_ADDR", ":19093"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	diagDone := make(chan struct{})
	diagErr := make(chan error, 1)
	go func() {
		defer close(diagDone)
		defer observability.Recover(logger, telemetry.Metrics, "maintenance_worker.diagnostics", func(any) {
			select {
			case diagErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("maintenance_diagnostics_failed", "error_code", "MAINTENANCE_DIAGNOSTICS_SERVE_FAILED")
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
		defer observability.Recover(logger, telemetry.Metrics, "maintenance_worker.loop")
		runWorkerLoop(workerCtx, worker, store, coldArchiveDir, scanInterval, auditInterval, telemetry, logger)
	}()

	telemetry.MarkReady()
	logger.Info("maintenance_service_started", "scan_interval", scanInterval.String(), "audit_interval", auditInterval.String(), "worker_id", workerID, "cold_archive_audit_enabled", coldArchiveDir != "")

	exitCode := 0
	select {
	case <-sigCtx.Done():
	case <-diagErr:
		exitCode = 1
	}
	stopSignals()

	shutdownMaintenanceWorker(logger, telemetry, diagnostics, diagDone, cancelWorker, workerDone, shutdownTelemetry, closeStore, 10*time.Second)
	logger.Info("maintenance_service_stopped")
	return exitCode
}

func shutdownMaintenanceWorker(
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
				logger.Warn("maintenance_worker_shutdown_timeout", "error_code", "MAINTENANCE_SHUTDOWN_TIMEOUT")
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

func openStore(ctx context.Context) (*maintenance.Store, error) {
	return maintenance.OpenStore(ctx, os.Getenv("MAINTENANCE_POSTGRES_DSN"))
}

type lifecycleAuditor interface {
	AuditLifecycleGovernance(ctx context.Context, now time.Time) (maintenance.LifecycleAuditSummary, error)
}

func runWorkerLoop(ctx context.Context, worker *maintenance.Worker, store *maintenance.Store, coldArchiveDir string, interval, auditInterval time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	runCycle(ctx, worker, telemetry, logger)
	runLifecycleAudit(ctx, store, coldArchiveDir, telemetry, logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	auditTicker := time.NewTicker(auditInterval)
	defer auditTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle(ctx, worker, telemetry, logger)
		case <-auditTicker.C:
			runLifecycleAudit(ctx, store, coldArchiveDir, telemetry, logger)
		}
	}
}

func runLifecycleAudit(ctx context.Context, store lifecycleAuditor, coldArchiveDir string, telemetry *observability.Runtime, logger *slog.Logger) {
	archiveDir := strings.TrimSpace(coldArchiveDir)
	if store == nil && archiveDir == "" {
		return
	}
	var metrics *observability.Registry
	if telemetry != nil {
		metrics = telemetry.Metrics
	}
	auditCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := time.Now().UTC()

	if store != nil {
		summary, err := store.AuditLifecycleGovernance(auditCtx, now)
		if err != nil {
			if metrics != nil {
				_ = metrics.AddCounter("maintenance_lifecycle_audit_error_total", "Maintenance lifecycle governance audit errors.", nil, 1)
			}
			if logger != nil {
				logger.Warn("maintenance_lifecycle_audit_failed", "error_code", "MAINTENANCE_LIFECYCLE_AUDIT_FAILED")
			}
		} else {
			if metrics != nil {
				_ = metrics.AddCounter("maintenance_lifecycle_audit_total", "Maintenance lifecycle governance audits.", nil, 1)
				if summary.FindingsCount > 0 {
					_ = metrics.AddCounter("maintenance_lifecycle_audit_findings_total", "Maintenance lifecycle governance findings recorded.", nil, float64(summary.FindingsCount))
				}
			}
			if (summary.FindingsCount > 0 || summary.EventsResolved > 0) && logger != nil {
				logger.Info(
					"maintenance_lifecycle_audit_completed",
					"policies_checked", summary.PoliciesChecked,
					"active_legal_holds", summary.ActiveLegalHolds,
					"findings", summary.FindingsCount,
					"resolved", summary.EventsResolved,
				)
			}
		}
	}

	if archiveDir != "" {
		runColdArchiveAudit(auditCtx, archiveDir, now, metrics, logger)
	}
}

func runColdArchiveAudit(ctx context.Context, archiveDir string, now time.Time, metrics *observability.Registry, logger *slog.Logger) {
	report, archiveErr := maintenance.VerifyColdArchiveDirectory(ctx, archiveDir, now)
	if archiveErr != nil {
		if metrics != nil {
			_ = metrics.AddCounter("maintenance_cold_archive_audit_error_total", "Maintenance cold archive integrity audit errors.", nil, 1)
		}
		if logger != nil {
			logger.Warn("maintenance_cold_archive_audit_failed", "error_code", "MAINTENANCE_COLD_ARCHIVE_AUDIT_FAILED")
		}
		return
	}
	if metrics != nil {
		_ = metrics.AddCounter("maintenance_cold_archive_audit_total", "Maintenance cold archive integrity audits.", nil, 1)
		if !report.Healthy() {
			_ = metrics.AddCounter("maintenance_cold_archive_audit_findings_total", "Maintenance cold archive integrity findings detected.", nil, float64(len(report.Findings)))
		}
	}
	if !report.Healthy() && logger != nil {
		logger.Warn(
			"maintenance_cold_archive_integrity_findings",
			"error_code", "MAINTENANCE_COLD_ARCHIVE_INTEGRITY_FINDINGS",
			"manifests_checked", report.ManifestsChecked,
			"verified_count", report.VerifiedCount,
			"temp_files_scanned", report.TempFilesScanned,
			"stale_temp_count", report.StaleTempCount,
			"corrupt_count", report.CorruptCount,
			"missing_count", report.MissingCount,
			"findings", len(report.Findings),
		)
	} else if report.ManifestsChecked > 0 && logger != nil {
		logger.Info(
			"maintenance_cold_archive_audit_completed",
			"manifests_checked", report.ManifestsChecked,
			"verified_count", report.VerifiedCount,
			"temp_files_scanned", report.TempFilesScanned,
		)
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

type workerSettings struct {
	batch         int
	leaseDuration time.Duration
	scanInterval  time.Duration
	auditInterval time.Duration
}

func loadWorkerSettings() (workerSettings, error) {
	batch, err := integerEnv("MAINTENANCE_CLAIM_BATCH", 10, 1, 100)
	if err != nil {
		return workerSettings{}, err
	}
	leaseDuration, err := durationEnv("MAINTENANCE_LEASE_DURATION", 30*time.Second, 5*time.Second, 10*time.Minute)
	if err != nil {
		return workerSettings{}, err
	}
	scanInterval, err := durationEnv("MAINTENANCE_SCAN_INTERVAL", 2*time.Second, time.Second, time.Minute)
	if err != nil {
		return workerSettings{}, err
	}
	auditInterval, err := durationEnv("MAINTENANCE_LIFECYCLE_AUDIT_INTERVAL", time.Minute, 5*time.Second, 24*time.Hour)
	if err != nil {
		return workerSettings{}, err
	}
	return workerSettings{
		batch:         batch,
		leaseDuration: leaseDuration,
		scanInterval:  scanInterval,
		auditInterval: auditInterval,
	}, nil
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("invalid duration environment variable: " + name)
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
		return 0, errors.New("invalid integer environment variable: " + name)
	}
	return parsed, nil
}
