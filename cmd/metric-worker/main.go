package main

import (
	"context"
	"encoding/json"
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
	"github.com/quanlaihe/hvac-web/modules/metric/pkg/metric"
)

type request struct {
	Operation   string    `json:"operation"`
	TenantID    string    `json:"tenantId"`
	SiteID      string    `json:"siteId"`
	BindingID   string    `json:"bindingId"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	Reason      string    `json:"reason"`
	StaleBefore time.Time `json:"staleBefore"`
	Limit       int       `json:"limit"`
}

type metricJobPayload struct {
	BindingID                string    `json:"bindingId"`
	PeriodStart              time.Time `json:"periodStart"`
	PeriodEnd                time.Time `json:"periodEnd"`
	FinalizationDelaySeconds int64     `json:"finalizationDelaySeconds"`
}

type runtime struct {
	store          *metric.PostgresStore
	series         *metric.ClickHouseStore
	engine         *metric.Engine
	lifecycle      *metric.LifecycleExecutor
	latest         *metric.RedisLatestStore
	backfillSource metric.BackfillObservationSource
}

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
		Service: "metric-worker", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
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

	runtime, err := openRuntime(sigCtx)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	var closeRuntimeOnce sync.Once
	closeRuntime := func() {
		closeRuntimeOnce.Do(runtime.Close)
	}
	closers = append(closers, closeRuntime)
	if err := runtime.VerifyProductSchema(sigCtx); err != nil {
		logger.Error("metric_worker_schema_compatibility_invalid", "error_code", "SCHEMA_COMPATIBILITY_INVALID", "cause", err.Error())
		return 1
	}
	telemetry.SetDependencies(observability.Dependency{Name: "metric-runtime", Required: true, Check: runtime.Ping})

	if strings.EqualFold(strings.TrimSpace(os.Getenv("METRIC_WORKER_MODE")), "oneshot") {
		if err := runOneShot(sigCtx, runtime, os.Stdin, os.Stdout); err != nil {
			logger.Error("metric_oneshot_failed", "error_code", "METRIC_ONESHOT_FAILED")
			return 1
		}
		return 0
	}

	workerID := strings.TrimSpace(os.Getenv("METRIC_WORKER_ID"))
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = "metric-worker:" + hostname
	}
	claimInterval, err := durationEnv("METRIC_JOB_CLAIM_INTERVAL", 2*time.Second, time.Second, time.Minute)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	claimBatch, err := integerEnv("METRIC_JOB_CLAIM_BATCH", 20, 1, 100)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	leaseDuration, err := durationEnv("METRIC_JOB_LEASE_DURATION", 60*time.Second, 10*time.Second, 30*time.Minute)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	leaseRenew, err := durationEnv("METRIC_JOB_LEASE_RENEW_INTERVAL", leaseDuration/3, time.Second, leaseDuration/2)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	finalizationDelay, err := durationEnv("METRIC_WORKER_FINALIZATION_DELAY", 5*time.Minute, 0, 24*time.Hour)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	backfillScanInterval, err := durationEnv("METRIC_BACKFILL_SCAN_INTERVAL", 30*time.Second, 0, 24*time.Hour)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	dirtyRecalcInterval, err := durationEnv("METRIC_DIRTY_RECALC_INTERVAL", 5*time.Second, 0, 24*time.Hour)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	reconcileInterval, err := reconcileIntervalEnv()
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}
	reconcileStaleAge, err := durationEnv("METRIC_RECONCILE_STALE_AGE", 5*time.Minute, time.Second, 24*time.Hour)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		return 1
	}

	var detector backfillDetector
	if backfillScanInterval > 0 {
		builtDetector, detErr := metric.NewBackfillRecalcDetector(runtime.store, runtime.backfillSource, metric.BackfillRecalcConfig{
			FinalizationDelay: finalizationDelay,
			WatermarkStore:    runtime.store,
			WatermarkScope:    envOr("METRIC_BACKFILL_WATERMARK_SCOPE", metric.DefaultBackfillWatermarkScope),
		})
		if detErr != nil {
			logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
			return 1
		}
		detector = builtDetector
	}

	var recalcRunner dirtyRecalcRunner
	if dirtyRecalcInterval > 0 {
		builtRecalc, rwErr := metric.NewRecalcWorker(metric.RecalcWorkerConfig{
			Store:      runtime.store,
			Executor:   runtime.engine,
			Reconciler: runtime.engine,
		})
		if rwErr != nil {
			logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
			return 1
		}
		recalcRunner = builtRecalc
	}

	diagnostics := &http.Server{
		Addr: envOr("METRIC_DIAGNOSTICS_ADDR", ":19090"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	diagDone := make(chan struct{})
	diagErr := make(chan error, 1)
	go func() {
		defer close(diagDone)
		defer observability.Recover(logger, telemetry.Metrics, "metric_worker.diagnostics", func(any) {
			select {
			case diagErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metric_diagnostics_failed", "error_code", "METRIC_DIAGNOSTICS_SERVE_FAILED")
			select {
			case diagErr <- err:
			default:
			}
		}
	}()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	var bgWG sync.WaitGroup
	if detector != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			defer observability.Recover(logger, telemetry.Metrics, "metric_worker.backfill_detector")
			runBackfillDetectorLoop(workerCtx, detector, backfillScanInterval, telemetry, logger)
		}()
	}
	if recalcRunner != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			defer observability.Recover(logger, telemetry.Metrics, "metric_worker.dirty_recalc")
			runDirtyRecalcLoop(workerCtx, recalcRunner, dirtyRecalcInterval, telemetry, logger)
		}()
	}
	if reconcileInterval > 0 {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			defer observability.Recover(logger, telemetry.Metrics, "metric_worker.publication_reconcile")
			runPublicationReconcileLoop(workerCtx, runtime.store, runtime.engine, reconcileInterval, reconcileStaleAge, telemetry, logger)
		}()
	}
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		defer observability.Recover(logger, telemetry.Metrics, "metric_worker.loop")
		runWorker(workerCtx, runtime, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew, finalizationDelay, telemetry, logger)
	}()

	telemetry.MarkReady()
	logger.Info(
		"metric_worker_started",
		"worker_id", workerID,
		"claim_interval", claimInterval.String(),
		"claim_batch", claimBatch,
		"lease_duration", leaseDuration.String(),
		"finalization_delay", finalizationDelay.String(),
		"backfill_scan_interval", backfillScanInterval.String(),
		"dirty_recalc_interval", dirtyRecalcInterval.String(),
		"reconcile_interval", reconcileInterval.String(),
	)

	exitCode := 0
	select {
	case <-sigCtx.Done():
	case <-diagErr:
		exitCode = 1
	}
	stopSignals()

	shutdownMetricWorker(logger, telemetry, diagnostics, diagDone, cancelWorker, &bgWG, shutdownTelemetry, closeRuntime, 10*time.Second)
	logger.Info("metric_worker_stopped")
	return exitCode
}

func shutdownMetricWorker(
	logger *slog.Logger,
	telemetry *observability.Runtime,
	diagnostics *http.Server,
	diagDone <-chan struct{},
	cancelWorker context.CancelFunc,
	bgWG *sync.WaitGroup,
	shutdownTelemetry func(),
	closeRuntime func(),
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
	if bgWG != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			bgWG.Wait()
		}()
		timer := time.NewTimer(drainTimeout)
		select {
		case <-done:
		case <-timer.C:
			if logger != nil {
				logger.Warn("metric_worker_shutdown_timeout", "error_code", "METRIC_WORKER_SHUTDOWN_TIMEOUT")
			}
		}
		timer.Stop()
	}
	if shutdownTelemetry != nil {
		shutdownTelemetry()
	}
	if closeRuntime != nil {
		closeRuntime()
	}
}

func openRuntime(ctx context.Context) (*runtime, error) {
	dsn, err := requiredEnv("METRIC_POSTGRES_DSN")
	if err != nil {
		return nil, err
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse metric PostgreSQL DSN: %w", err)
	}
	if config.ConnConfig.User != "metric_engine_runtime" {
		return nil, errors.New("metric PostgreSQL identity must be metric_engine_runtime")
	}
	config.MaxConns = 8
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := metric.NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	clickhouseURL, err := requiredEnv("METRIC_CLICKHOUSE_URL")
	if err != nil {
		store.Close()
		return nil, err
	}
	clickhouseUser := os.Getenv("METRIC_CLICKHOUSE_USER")
	clickhousePassword := os.Getenv("METRIC_CLICKHOUSE_PASSWORD")
	series, err := metric.NewClickHouseStore(clickhouseURL, clickhouseUser, clickhousePassword, nil)
	if err != nil {
		store.Close()
		return nil, err
	}
	backfillSource, err := metric.NewClickHouseBackfillSource(clickhouseURL, clickhouseUser, clickhousePassword, nil)
	if err != nil {
		store.Close()
		return nil, err
	}
	redisURL, err := requiredEnv("METRIC_REDIS_URL")
	if err != nil {
		store.Close()
		return nil, err
	}
	latest, err := metric.NewRedisLatestStore(redisURL, 7*24*time.Hour)
	if err != nil {
		store.Close()
		return nil, err
	}
	engine, err := metric.New(store, series, latest)
	if err != nil {
		_ = latest.Close()
		store.Close()
		return nil, err
	}
	lifecycle, err := metric.NewLifecycleExecutor(store, series)
	if err != nil {
		_ = latest.Close()
		store.Close()
		return nil, err
	}
	return &runtime{store: store, series: series, engine: engine, lifecycle: lifecycle, latest: latest, backfillSource: backfillSource}, nil
}

func (runtime *runtime) Ping(ctx context.Context) error {
	if err := runtime.store.Ping(ctx); err != nil {
		return err
	}
	if err := runtime.series.Ping(ctx); err != nil {
		return err
	}
	return runtime.latest.Ping(ctx)
}

func (runtime *runtime) VerifyProductSchema(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	return runtime.store.VerifyProductSchema(ctx)
}

func (runtime *runtime) Close() {
	if runtime == nil {
		return
	}
	if runtime.latest != nil {
		_ = runtime.latest.Close()
	}
	if runtime.store != nil {
		runtime.store.Close()
	}
}

type backfillDetector interface {
	InitializeWatermark(context.Context) error
	DetectAndEnqueue(context.Context, time.Time) (int, error)
}

type dirtyRecalcRunner interface {
	ProcessOnce(context.Context) (metric.RecalcBatchStats, error)
}

func runDirtyRecalcLoop(
	ctx context.Context,
	runner dirtyRecalcRunner,
	pollInterval time.Duration,
	telemetry *observability.Runtime,
	logger *slog.Logger,
) {
	if runner == nil || pollInterval <= 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	defer observability.Recover(logger, runtimeMetrics(telemetry), "metric_worker.dirty_recalc")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDirtyRecalcOnce(ctx, runner, telemetry, logger)
		}
	}
}

func runDirtyRecalcOnce(ctx context.Context, runner dirtyRecalcRunner, telemetry *observability.Runtime, logger *slog.Logger) {
	passCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	stats, err := runner.ProcessOnce(passCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if telemetry != nil && telemetry.Metrics != nil {
			_ = telemetry.Metrics.AddCounter("hvac_metric_dirty_recalc_passes_total", "Dirty window recalculation passes.", map[string]string{"outcome": "error"}, 1)
		}
		logger.Warn("metric_dirty_recalc_failed", "error_code", "METRIC_DIRTY_RECALC_FAILED", "claimed", stats.Claimed, "failed", stats.Failed)
		return
	}
	if telemetry != nil && telemetry.Metrics != nil {
		_ = telemetry.Metrics.AddCounter("hvac_metric_dirty_recalc_passes_total", "Dirty window recalculation passes.", map[string]string{"outcome": "success"}, 1)
		if stats.ExecutedRuns > 0 {
			_ = telemetry.Metrics.AddCounter("hvac_metric_dirty_recalc_runs_total", "Metric executions triggered by dirty window coalescing.", nil, float64(stats.ExecutedRuns))
		}
	}
	if stats.Claimed > 0 {
		logger.Info("metric_dirty_recalc_completed", "claimed", stats.Claimed, "coalesced_spans", stats.CoalescedSpan, "executed_runs", stats.ExecutedRuns, "succeeded", stats.Succeeded)
	}
}

func runPublicationReconcileLoop(
	ctx context.Context,
	lister metric.PublicationScopeLister,
	reconciler metric.ScopeReconciler,
	interval time.Duration,
	staleAge time.Duration,
	telemetry *observability.Runtime,
	logger *slog.Logger,
) {
	if lister == nil || reconciler == nil || interval <= 0 {
		return
	}
	if staleAge <= 0 {
		staleAge = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	defer observability.Recover(logger, runtimeMetrics(telemetry), "metric_worker.publication_reconcile")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcilePublicationsOnce(ctx, lister, reconciler, staleAge, telemetry, logger)
		}
	}
}

func reconcilePublicationsOnce(
	ctx context.Context,
	lister metric.PublicationScopeLister,
	reconciler metric.ScopeReconciler,
	staleAge time.Duration,
	telemetry *observability.Runtime,
	logger *slog.Logger,
) {
	passCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	repaired, err := metric.ReconcileStalePublications(passCtx, lister, reconciler, time.Now().UTC().Add(-staleAge), 100)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if telemetry != nil && telemetry.Metrics != nil {
			_ = telemetry.Metrics.AddCounter("hvac_metric_publication_reconcile_runs_total", "Periodic metric publication reconciliations.", map[string]string{"outcome": "error"}, 1)
		}
		logger.Warn("metric_publication_reconcile_failed", "error_code", "METRIC_PUBLICATION_RECONCILE_FAILED", "repaired", repaired)
		return
	}
	if telemetry != nil && telemetry.Metrics != nil {
		_ = telemetry.Metrics.AddCounter("hvac_metric_publication_reconcile_runs_total", "Periodic metric publication reconciliations.", map[string]string{"outcome": "success"}, 1)
		if repaired > 0 {
			_ = telemetry.Metrics.AddCounter("hvac_metric_publication_repaired_total", "Stale PERSISTING metric publications repaired by periodic reconciliation.", nil, float64(repaired))
		}
	}
	if repaired > 0 {
		logger.Info("metric_publication_reconcile_completed", "repaired", repaired)
	}
}

func runBackfillDetectorLoop(
	ctx context.Context,
	detector backfillDetector,
	scanInterval time.Duration,
	telemetry *observability.Runtime,
	logger *slog.Logger,
) {
	if detector == nil || scanInterval <= 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	defer observability.Recover(logger, runtimeMetrics(telemetry), "metric_worker.backfill_detector")

	initCtx, initCancel := context.WithTimeout(ctx, 10*time.Second)
	initErr := detector.InitializeWatermark(initCtx)
	initCancel()
	if initErr != nil && ctx.Err() == nil {
		recordBackfillScanMetric(telemetry, "error", 0)
		logger.Warn("metric_backfill_watermark_init_failed", "error_code", "METRIC_BACKFILL_WATERMARK_INIT_FAILED")
	}

	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanBackfillOnce(ctx, detector, telemetry, logger)
		}
	}
}

func scanBackfillOnce(ctx context.Context, detector backfillDetector, telemetry *observability.Runtime, logger *slog.Logger) {
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	enqueued, err := detector.DetectAndEnqueue(scanCtx, time.Now().UTC())
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		recordBackfillScanMetric(telemetry, "error", 0)
		logger.Warn("metric_backfill_scan_failed", "error_code", "METRIC_BACKFILL_SCAN_FAILED")
		return
	}
	recordBackfillScanMetric(telemetry, "success", enqueued)
	if enqueued > 0 {
		logger.Info("metric_backfill_recalc_enqueued", "enqueued", enqueued)
	}
}

func recordBackfillScanMetric(telemetry *observability.Runtime, outcome string, enqueued int) {
	if telemetry == nil || telemetry.Metrics == nil {
		return
	}
	_ = telemetry.Metrics.AddCounter("hvac_metric_backfill_scans_total", "Metric backfill recalc detector scans.", map[string]string{"outcome": outcome}, 1)
	if enqueued > 0 {
		_ = telemetry.Metrics.AddCounter("hvac_metric_backfill_recalc_enqueued_total", "METRIC_RECALC jobs enqueued by automatic backfill detection.", nil, float64(enqueued))
	}
}

func runWorker(ctx context.Context, runtime *runtime, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew, finalizationDelay time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	backfillSemaphore := make(chan struct{}, 1)
	claimAndRun(ctx, runtime, workerID, claimBatch, leaseDuration, leaseRenew, finalizationDelay, backfillSemaphore, telemetry, logger)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimAndRun(ctx, runtime, workerID, claimBatch, leaseDuration, leaseRenew, finalizationDelay, backfillSemaphore, telemetry, logger)
		}
	}
}

func claimAndRun(ctx context.Context, runtime *runtime, workerID string, claimBatch int, leaseDuration, leaseRenew, finalizationDelay time.Duration, backfillSemaphore chan struct{}, telemetry *observability.Runtime, logger *slog.Logger) {
	claimContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := runtime.store.ClaimMetricJobs(claimContext, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		recordMetricWorkerResult(telemetry, "claim_error")
		logger.Warn("metric_job_claim_failed", "error_code", "METRIC_JOB_CLAIM_FAILED")
		return
	}
	if len(jobs) == 0 {
		return
	}
	var wait sync.WaitGroup
	for _, claimed := range jobs {
		job := claimed
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer observability.Recover(logger, runtimeMetrics(telemetry), "metric_worker.job")
			if job.JobType == "METRIC_BACKFILL" {
				select {
				case backfillSemaphore <- struct{}{}:
					defer func() { <-backfillSemaphore }()
				case <-ctx.Done():
					return
				}
			}
			executeMetricJob(ctx, runtime, job, leaseDuration, leaseRenew, finalizationDelay, telemetry, logger)
		}()
	}
	wait.Wait()
}

func executeMetricJob(ctx context.Context, runtime *runtime, job metric.SchedulerJob, leaseDuration, leaseRenew, finalizationDelay time.Duration, telemetry *observability.Runtime, logger *slog.Logger) {
	if err := metric.ValidateMetricSchedulerJob(job); err != nil {
		recordMetricWorkerResult(telemetry, "invalid_job")
		logger.Warn("metric_job_invalid", "job_id", job.JobID, "error_code", "METRIC_JOB_INVALID")
		return
	}
	started, err := runtime.store.StartMetricJob(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		recordMetricWorkerResult(telemetry, "start_error")
		logger.Warn("metric_job_start_failed", "job_id", job.JobID, "error_code", "METRIC_JOB_START_FAILED")
		return
	}
	if !started {
		recordMetricWorkerResult(telemetry, "cancelled_before_start")
		return
	}
	logger.Info("metric_job_started",
		"job_id", job.JobID, "job_type", job.JobType, "schedule_id", job.ScheduleID, "trigger_type", job.TriggerType,
		"attempt_no", job.AttemptNo, "worker_id", job.WorkerID, "tenant_id", job.TenantID, "site_id", job.SiteID,
		"scheduled_for", job.ScheduledFor, "trace_id", job.TraceID,
	)

	jobContext, cancelJob := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancelJob()
	leaseStop := make(chan struct{})
	leaseDone := make(chan struct{})
	cancelRequested := make(chan struct{}, 1)
	go func() {
		defer close(leaseDone)
		defer observability.Recover(logger, runtimeMetrics(telemetry), "metric_worker.lease_renewer")
		ticker := time.NewTicker(leaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-leaseStop:
				return
			case <-jobContext.Done():
				return
			case <-ticker.C:
				renewContext, renewCancel := context.WithTimeout(context.Background(), 5*time.Second)
				requested, renewErr := runtime.store.RenewMetricJobLease(renewContext, job, leaseDuration, time.Now().UTC())
				renewCancel()
				if renewErr != nil {
					recordMetricWorkerResult(telemetry, "lease_error")
					cancelJob()
					return
				}
				if requested {
					select {
					case cancelRequested <- struct{}{}:
					default:
					}
					cancelJob()
					return
				}
			}
		}
	}()

	output, errorCode, retryable, executionErr := executeMetricBusiness(jobContext, runtime, job, finalizationDelay)
	close(leaseStop)
	<-leaseDone

	select {
	case <-cancelRequested:
		cancelContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = runtime.store.CancelRunningMetricJob(cancelContext, job, time.Now().UTC())
		cancel()
		if err != nil {
			logger.Warn("metric_job_cancel_finalize_failed", "job_id", job.JobID, "error_code", "METRIC_JOB_CANCEL_FINALIZE_FAILED")
		}
		recordMetricWorkerResult(telemetry, "cancelled")
		_ = telemetry.Metrics.AddCounter("job_cancelled_total", "Cancelled durable Jobs.", map[string]string{"job_type": job.JobType}, 1)
		return
	default:
	}

	if executionErr != nil {
		if errors.Is(executionErr, context.DeadlineExceeded) {
			errorCode, retryable = "TIMEOUT", true
		}
		failureContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = runtime.store.FailMetricJob(failureContext, job, errorCode, executionErr, retryable, time.Now().UTC())
		cancel()
		if err != nil {
			logger.Warn("metric_job_failure_finalize_failed", "job_id", job.JobID, "error_code", "METRIC_JOB_FAILURE_FINALIZE_FAILED")
		}
		recordMetricWorkerResult(telemetry, "execute_error")
		switch {
		case retryable && job.AttemptNo < job.MaxAttempts:
			_ = telemetry.Metrics.AddCounter("job_retry_total", "Durable Job retries.", map[string]string{"job_type": job.JobType}, 1)
		case job.AttemptNo >= job.MaxAttempts:
			_ = telemetry.Metrics.AddCounter("job_dead_total", "Durable Jobs with exhausted attempts.", map[string]string{"job_type": job.JobType}, 1)
		default:
			_ = telemetry.Metrics.AddCounter("job_failed_total", "Failed durable Jobs.", map[string]string{"job_type": job.JobType}, 1)
		}
		return
	}

	completeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = runtime.store.CompleteMetricJob(completeContext, job, output, time.Now().UTC())
	cancel()
	if err != nil {
		recordMetricWorkerResult(telemetry, "complete_error")
		logger.Warn("metric_job_complete_failed", "job_id", job.JobID, "error_code", "METRIC_JOB_COMPLETE_FAILED")
		return
	}
	recordMetricWorkerResult(telemetry, "succeeded")
	_ = telemetry.Metrics.AddCounter("job_succeeded_total", "Succeeded durable Jobs.", map[string]string{"job_type": job.JobType}, 1)
	logger.Info("metric_job_succeeded", "job_id", job.JobID, "job_type", job.JobType, "attempt_no", job.AttemptNo, "trace_id", job.TraceID)
}

func executeMetricBusiness(ctx context.Context, runtime *runtime, job metric.SchedulerJob, defaultFinalizationDelay time.Duration) (map[string]any, string, bool, error) {
	if job.JobType == "DATA_RETENTION_SCAN" || job.JobType == "DATA_ARCHIVE" {
		var payload metric.LifecyclePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, "INVALID_PAYLOAD", false, err
		}
		outcome, err := runtime.lifecycle.Execute(ctx, job, payload, time.Now().UTC())
		if err != nil {
			if errors.Is(err, metric.ErrArchiveEvidenceRequired) {
				return nil, "ARCHIVE_EVIDENCE_REQUIRED", true, err
			}
			return nil, "LIFECYCLE_EXECUTION_FAILED", true, err
		}
		return map[string]any{
			"status": outcome.Status, "resourceKey": outcome.ResourceKey,
			"deletionRequestId": outcome.DeletionRequestID, "tombstoneId": outcome.TombstoneID,
		}, "", false, nil
	}

	publication, found, err := runtime.store.LoadJobPublication(ctx, job.TenantID, job.SiteID, job.JobID)
	if err != nil {
		return nil, "DEPENDENCY_UNAVAILABLE", true, err
	}
	if found && publication.Status == "PERSISTING" {
		if _, err = runtime.engine.ReconcileScope(ctx, job.TenantID, job.SiteID, time.Now().UTC(), 100); err != nil {
			return nil, "PUBLICATION_RECONCILE_FAILED", true, err
		}
		publication, found, err = runtime.store.LoadJobPublication(ctx, job.TenantID, job.SiteID, job.JobID)
		if err != nil {
			return nil, "DEPENDENCY_UNAVAILABLE", true, err
		}
	}
	if found {
		switch publication.Status {
		case "PERSISTED":
			return map[string]any{"runId": publication.RunID, "resultId": publication.ResultID, "revision": publication.Revision, "deduplicated": true}, "", false, nil
		case "PERSISTING":
			return nil, "PUBLICATION_RECONCILE_PENDING", true, errors.New("metric result publication remains PERSISTING")
		case "FAILED":
			return nil, "PUBLICATION_FAILED", false, errors.New("metric result publication is terminal FAILED")
		}
	}

	var payload metricJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, "INVALID_PAYLOAD", false, err
	}
	payload.BindingID = strings.TrimSpace(payload.BindingID)
	if payload.BindingID == "" {
		return nil, "INVALID_PAYLOAD", false, errors.New("metric job bindingId is required")
	}

	period := metric.Period{Start: payload.PeriodStart.UTC(), End: payload.PeriodEnd.UTC()}
	var reason string
	switch job.JobType {
	case "METRIC_WINDOW_CALC":
		schedule, err := runtime.store.LoadSchedule(ctx, job.TenantID, job.SiteID, payload.BindingID, job.ScheduledFor)
		if err != nil {
			return nil, "DEPENDENCY_UNAVAILABLE", true, err
		}
		finalizationDelay := defaultFinalizationDelay
		if payload.FinalizationDelaySeconds < 0 || payload.FinalizationDelaySeconds > int64((24*time.Hour)/time.Second) {
			return nil, "INVALID_PAYLOAD", false, errors.New("metric finalizationDelaySeconds is invalid")
		}
		if payload.FinalizationDelaySeconds > 0 {
			finalizationDelay = time.Duration(payload.FinalizationDelaySeconds) * time.Second
		}
		period, err = metric.CompletedPeriod(schedule, job.ScheduledFor, finalizationDelay)
		if err != nil {
			return nil, "INVALID_METRIC_WINDOW", false, err
		}
		reconcileBefore := time.Now().UTC().Add(-5 * time.Minute)
		if _, err = runtime.engine.ReconcileScope(ctx, job.TenantID, job.SiteID, reconcileBefore, 100); err != nil {
			return nil, "DEPENDENCY_UNAVAILABLE", true, err
		}
		exists, err := runtime.store.HasActiveScheduledRun(ctx, job.TenantID, job.SiteID, payload.BindingID, period)
		if err != nil {
			return nil, "DEPENDENCY_UNAVAILABLE", true, err
		}
		if exists {
			return map[string]any{"bindingId": payload.BindingID, "periodStart": period.Start, "periodEnd": period.End, "deduplicated": true}, "", false, nil
		}
		reason = "SCHEDULED"
	case "METRIC_RECALC":
		reason = "LATE_DATA"
		if !period.End.After(period.Start) {
			return nil, "INVALID_PAYLOAD", false, errors.New("metric recalculation period is invalid")
		}
	case "METRIC_BACKFILL":
		reason = "BACKFILL"
		if !period.End.After(period.Start) {
			return nil, "INVALID_PAYLOAD", false, errors.New("metric backfill period is invalid")
		}
	default:
		return nil, "UNSUPPORTED_JOB_TYPE", false, errors.New("unsupported metric job type")
	}

	result, err := runtime.engine.Execute(ctx, metric.RunRequest{
		TenantID: job.TenantID, SiteID: job.SiteID, BindingID: payload.BindingID, SchedulerJobID: job.JobID,
		PeriodStart: period.Start, PeriodEnd: period.End, Reason: reason,
	})
	if err != nil {
		return nil, "METRIC_EXECUTION_FAILED", true, err
	}
	return map[string]any{
		"bindingId":   payload.BindingID,
		"runId":       result.RunID,
		"resultId":    result.ResultID,
		"periodStart": period.Start,
		"periodEnd":   period.End,
	}, "", false, nil
}

func runtimeMetrics(telemetry *observability.Runtime) *observability.Registry {
	if telemetry == nil {
		return nil
	}
	return telemetry.Metrics
}

func recordMetricWorkerResult(telemetry *observability.Runtime, result string) {
	if telemetry == nil {
		return
	}
	_ = telemetry.Metrics.AddCounter("hvac_metric_worker_runs_total", "Metric worker Job outcomes.", map[string]string{"result": result}, 1)
}

func runOneShot(ctx context.Context, runtime *runtime, input *os.File, output *os.File) error {
	var request request
	if err := json.NewDecoder(input).Decode(&request); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(request.Operation)) {
	case "", "execute":
		result, err := runtime.engine.Execute(ctx, metric.RunRequest{
			TenantID: request.TenantID, SiteID: request.SiteID, BindingID: request.BindingID,
			PeriodStart: request.PeriodStart, PeriodEnd: request.PeriodEnd, Reason: request.Reason,
		})
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "reconcile":
		staleBefore := request.StaleBefore
		if staleBefore.IsZero() {
			staleBefore = time.Now().UTC().Add(-5 * time.Minute)
		}
		repaired, err := runtime.engine.Reconcile(ctx, staleBefore, request.Limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]any{"repaired": repaired, "staleBefore": staleBefore})
	default:
		return errors.New("unsupported metric operation")
	}
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New(name + " is required")
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseDurationValue(raw string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("duration value is invalid")
	}
	return parsed, nil
}

func reconcileIntervalEnv() (time.Duration, error) {
	if strings.TrimSpace(os.Getenv("RECONCILE_INTERVAL")) != "" {
		return durationEnv("RECONCILE_INTERVAL", time.Minute, 0, 24*time.Hour)
	}
	return durationEnv("METRIC_RECONCILE_INTERVAL", time.Minute, 0, 24*time.Hour)
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	parsed, err := parseDurationValue(os.Getenv(name), fallback, minimum, maximum)
	if err != nil {
		return 0, errors.New(name + " is invalid")
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
		return 0, errors.New(name + " is invalid")
	}
	return parsed, nil
}
