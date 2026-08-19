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
	"github.com/quanlaihe/hvac-web/services/metric-engine-service/internal/metric"
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
	store     *metric.PostgresStore
	series    *metric.ClickHouseStore
	engine    *metric.Engine
	lifecycle *metric.LifecycleExecutor
	latest    *metric.RedisLatestStore
}

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "metric-worker", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runtime, err := openRuntime(ctx)
	if err != nil {
		logger.Error("metric_worker_configuration_invalid", "error_code", "METRIC_WORKER_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	defer runtime.Close()
	telemetry.SetReadinessCheck(runtime.Ping)

	if strings.EqualFold(strings.TrimSpace(os.Getenv("METRIC_WORKER_MODE")), "oneshot") {
		if err := runOneShot(ctx, runtime, os.Stdin, os.Stdout); err != nil {
			logger.Error("metric_oneshot_failed", "error_code", "METRIC_ONESHOT_FAILED")
			os.Exit(1)
		}
		return
	}

	workerID := requiredEnv("METRIC_WORKER_ID")
	claimInterval := durationEnv("METRIC_JOB_CLAIM_INTERVAL", 2*time.Second, time.Second, time.Minute)
	claimBatch := integerEnv("METRIC_JOB_CLAIM_BATCH", 20, 1, 100)
	leaseDuration := durationEnv("METRIC_JOB_LEASE_DURATION", 60*time.Second, 10*time.Second, 30*time.Minute)
	leaseRenew := durationEnv("METRIC_JOB_LEASE_RENEW_INTERVAL", leaseDuration/3, time.Second, leaseDuration/2)
	finalizationDelay := durationEnv("METRIC_WORKER_FINALIZATION_DELAY", 5*time.Minute, 0, 24*time.Hour)

	diagnostics := &http.Server{
		Addr: envOr("METRIC_DIAGNOSTICS_ADDR", ":19090"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metric_diagnostics_failed", "error_code", "METRIC_DIAGNOSTICS_SERVE_FAILED")
			cancel()
		}
	}()

	telemetry.MarkReady()
	logger.Info("metric_worker_started", "worker_id", workerID, "claim_interval", claimInterval.String(), "claim_batch", claimBatch, "lease_duration", leaseDuration.String(), "finalization_delay", finalizationDelay.String())
	runWorker(ctx, runtime, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew, finalizationDelay, telemetry, logger)
	telemetry.MarkNotReady()

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("metric_worker_stopped")
}

func openRuntime(ctx context.Context) (*runtime, error) {
	config, err := pgxpool.ParseConfig(requiredEnv("METRIC_POSTGRES_DSN"))
	if err != nil {
		return nil, fmt.Errorf("parse metric PostgreSQL DSN: %w", err)
	}
	if config.ConnConfig.User != "metric_engine_runtime" {
		return nil, errors.New("metric PostgreSQL identity must be metric_engine_runtime")
	}
	config.MaxConns = 8
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
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
	series, err := metric.NewClickHouseStore(requiredEnv("METRIC_CLICKHOUSE_URL"), os.Getenv("METRIC_CLICKHOUSE_USER"), os.Getenv("METRIC_CLICKHOUSE_PASSWORD"), nil)
	if err != nil {
		store.Close()
		return nil, err
	}
	redisDatabase := integerEnv("METRIC_REDIS_DB", 0, 0, 1024)
	latest, err := metric.NewRedisLatestStore(requiredEnv("METRIC_REDIS_ADDR"), os.Getenv("METRIC_REDIS_PASSWORD"), redisDatabase, 7*24*time.Hour)
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
	return &runtime{store: store, series: series, engine: engine, lifecycle: lifecycle, latest: latest}, nil
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
			return nil, "PUBLICATION_RECONCILE_PENDING", true, errors.New("Metric Result publication remains PERSISTING")
		case "FAILED":
			return nil, "PUBLICATION_FAILED", false, errors.New("Metric Result publication is terminal FAILED")
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
	reason := "BACKFILL"
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

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
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
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
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
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
	}
	return parsed
}
