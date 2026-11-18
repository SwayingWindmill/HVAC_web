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

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/forecast-service/internal/forecast"
)

const (
	defaultShutdownTimeout         = 10 * time.Second
	defaultBackgroundDrainTimeout  = 10 * time.Second
	defaultFinalizeJobTimeout      = 5 * time.Second
	defaultPanicRestartDelay       = time.Second
	defaultReconcileInterval       = 30 * time.Second
	defaultJobTimeoutFallback      = 5 * time.Minute
	defaultTelemetryShutdownPeriod = 5 * time.Second
)

var errOperatorCanceled = errors.New("forecast job canceled by operator")

type forecastJobQueue interface {
	Claim(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]schedulerjob.Job, error)
	Start(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Renew(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Complete(ctx context.Context, job schedulerjob.Job, output any, now time.Time) error
	Fail(ctx context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, now time.Time) error
}

type forecastInputLoader interface {
	LoadForecastRequest(ctx context.Context, tenantID, siteID string, reference forecast.SchedulerForecastReference) (forecast.Request, error)
}

type forecastRunner interface {
	Forecast(ctx context.Context, request forecast.Request) ([]forecast.Point, error)
}

type forecastPreparer interface {
	Prepare(ctx context.Context, request forecast.PreparationRequest) (forecast.PreparedForecast, error)
}

type forecastReconciler interface {
	Reconcile(ctx context.Context, olderThan time.Time, limit int) (int, error)
}

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := runService(logger); err != nil {
		logger.Error("forecast_service_failed", "error_code", "FORECAST_SERVICE_FAILED", "error", err)
		return 1
	}
	return 0
}

func runService(logger *slog.Logger) error {
	if logger == nil {
		logger = observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	}
	var closers []func()
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:      "forecast-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func() {
		telemetryShutdownOnce.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultTelemetryShutdownPeriod)
			defer cancel()
			_ = telemetry.Shutdown(shutdownCtx)
		})
	}
	// Defer telemetry shutdown after pool closers are registered above so LIFO flushes telemetry first and closes pools last.
	defer shutdownTelemetry()

	clickHouseURL, err := requiredEnv("FORECAST_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	sink, err := forecast.NewClickHouseSink(forecast.ClickHouseConfig{
		BaseURL:        clickHouseURL,
		Database:       envOrDefault("FORECAST_CLICKHOUSE_DATABASE", "analytics"),
		Table:          envOrDefault("FORECAST_CLICKHOUSE_TABLE", "forecast_series"),
		Username:       envOrDefault("FORECAST_CLICKHOUSE_USERNAME", "forecast_service_writer"),
		Password:       os.Getenv("FORECAST_CLICKHOUSE_PASSWORD"),
		ReaderUsername: envOrDefault("FORECAST_CLICKHOUSE_READER_USERNAME", "forecast_service_reader"),
		ReaderPassword: os.Getenv("FORECAST_CLICKHOUSE_READER_PASSWORD"),
	})
	if err != nil {
		return err
	}
	closers = append(closers, sink.CloseIdleConnections)

	dsn, err := requiredEnv("FORECAST_POSTGRES_DSN")
	if err != nil {
		return err
	}
	publication, err := forecast.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, publication.Close)

	service, err := forecast.NewService(sink, publication, time.Now)
	if err != nil {
		return err
	}
	history, err := forecast.NewClickHouseHistoryReader(forecast.HistoryConfig{
		BaseURL:  clickHouseURL,
		Username: envOrDefault("FORECAST_CLICKHOUSE_READER_USERNAME", "forecast_service_reader"),
		Password: os.Getenv("FORECAST_CLICKHOUSE_READER_PASSWORD"),
	})
	if err != nil {
		return err
	}
	closers = append(closers, history.CloseIdleConnections)

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: publication.Ping},
		observability.Dependency{Name: "clickhouse", Required: true, Check: history.Ping},
	)

	preparer, err := forecast.NewPreparer(publication, history, time.Now)
	if err != nil {
		return err
	}

	certPath, err := requiredEnv("FORECAST_TLS_CERT")
	if err != nil {
		return err
	}
	keyPath, err := requiredEnv("FORECAST_TLS_KEY")
	if err != nil {
		return err
	}
	caPath, err := requiredEnv("FORECAST_TLS_CA")
	if err != nil {
		return err
	}
	// Only the Gateway may call Forecast, over mutual TLS with a context it signed (#612).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: certPath, PrivateKeyPath: keyPath},
		ClientCAPath:     caPath,
	})
	if err != nil {
		return err
	}
	delegationCert, err := requiredEnv("FORECAST_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := forecast.NewHTTPHandler(service, forecast.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("FORECAST_GATEWAY_SPIFFE", forecast.DefaultGatewaySPIFFEID),
	})
	if err != nil {
		return err
	}

	queue, err := publication.RunQueue()
	if err != nil {
		return err
	}
	workerID := envOrDefault("FORECAST_WORKER_ID", defaultWorkerID())
	claimInterval, err := durationEnv("FORECAST_CLAIM_INTERVAL", 5*time.Second)
	if err != nil {
		return err
	}
	leaseDuration, err := durationEnv("FORECAST_LEASE_DURATION", 2*time.Minute)
	if err != nil {
		return err
	}
	leaseRenew, err := durationEnv("FORECAST_LEASE_RENEW", 30*time.Second)
	if err != nil {
		return err
	}
	claimBatch, err := integerEnv("FORECAST_CLAIM_BATCH", 8)
	if err != nil {
		return err
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	runSupervised(workerCtx, &bgWG, logger, metrics, "forecast.worker", defaultPanicRestartDelay, func() {
		runForecastWorker(workerCtx, queue, publication, service, preparer, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew, logger, metrics)
	})

	// Cross-store repair is part of the Forecast runtime itself. It only repairs
	// stale PERSISTING jobs after proving the expected result count in ClickHouse.
	runSupervised(workerCtx, &bgWG, logger, metrics, "forecast.reconcile", defaultPanicRestartDelay, func() {
		runReconcileLoop(workerCtx, defaultReconcileInterval, service, logger)
	})

	server := &http.Server{
		Addr:              envOrDefault("FORECAST_HTTP_ADDR", ":19092"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              envOrDefault("FORECAST_DIAGNOSTICS_ADDR", ":19099"),
		Handler:           forecast.NewDiagnosticsHandler(telemetry),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	serveErr := make(chan error, 2)
	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, metrics, "forecast.diagnostics_server", func(any) {
			select {
			case serveErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("diagnostics server: %w", err)
		}
	}()
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, metrics, "forecast.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("forecast_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr)
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", err)
		}
	}()

	var runErr error
	select {
	case <-sigCtx.Done():
		logger.Info("forecast_service_shutting_down")
	case err := <-serveErr:
		runErr = err
		stopSignals()
	}

	if shutdownErr := shutdownServiceRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "forecast.shutdown", &httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("forecast_server_shutdown_failed", "error_code", "FORECAST_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("forecast_service_stopped")
	return runErr
}

func shutdownServiceRuntime(
	logger *slog.Logger,
	telemetry *observability.Runtime,
	shutdownTimeout time.Duration,
	drainTimeout time.Duration,
	component string,
	httpWG *sync.WaitGroup,
	cancelWorkers context.CancelFunc,
	bgWG *sync.WaitGroup,
	shutdownTelemetry func(),
	servers ...*http.Server,
) error {
	var metrics *observability.Registry
	if telemetry != nil {
		telemetry.MarkNotReady()
		metrics = telemetry.Metrics
	}
	shutdownDone, shutdownResult := startServerShutdown(shutdownTimeout, logger, metrics, component, servers...)
	<-shutdownDone
	shutdownErr := shutdownResult()
	if httpWG != nil {
		waitBackground(httpWG, shutdownTimeout, logger)
	}
	if cancelWorkers != nil {
		cancelWorkers()
	}
	if bgWG != nil {
		waitBackground(bgWG, drainTimeout, logger)
	}
	if shutdownTelemetry != nil {
		shutdownTelemetry()
	}
	return shutdownErr
}

func runSupervised(ctx context.Context, wg *sync.WaitGroup, logger *slog.Logger, metrics *observability.Registry, component string, restartDelay time.Duration, fn func()) {
	if fn == nil {
		return
	}
	if restartDelay <= 0 {
		restartDelay = defaultPanicRestartDelay
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			panicked := false
			func() {
				defer observability.Recover(logger, metrics, component, func(any) {
					panicked = true
				})
				fn()
			}()
			if !panicked || ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(restartDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func runReconcileLoop(ctx context.Context, interval time.Duration, reconciler forecastReconciler, logger *slog.Logger) {
	if reconciler == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcileCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, reconcileErr := reconciler.Reconcile(reconcileCtx, time.Now().UTC().Add(-2*time.Minute), 100)
			cancel()
			if reconcileErr != nil && ctx.Err() == nil && logger != nil {
				logger.Warn("forecast_reconcile_failed", "error_code", "FORECAST_RECONCILE_FAILED", "error", reconcileErr)
			}
		}
	}
}

func startServerShutdown(timeout time.Duration, logger *slog.Logger, metrics *observability.Registry, component string, servers ...*http.Server) (<-chan struct{}, func() error) {
	shutdownDone := make(chan struct{})
	var (
		mu       sync.Mutex
		firstErr error
	)
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, metrics, component, func(r any) {
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("shutdown panicked: %v", r)
			}
			mu.Unlock()
		})
		if err := shutdownServers(timeout, servers...); err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}()
	return shutdownDone, func() error {
		mu.Lock()
		defer mu.Unlock()
		return firstErr
	}
}

func shutdownServers(timeout time.Duration, servers ...*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func(server *http.Server) {
			defer wg.Done()
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(srv)
	}
	wg.Wait()
	return firstErr
}

func waitBackground(wg *sync.WaitGroup, timeout time.Duration, logger *slog.Logger) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if logger != nil {
			logger.Warn("forecast_background_wait_timeout", "error_code", "FORECAST_SHUTDOWN_DRAIN_TIMEOUT", "timeout", timeout.String())
		}
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func runForecastWorker(ctx context.Context, queue forecastJobQueue, store forecastInputLoader, service forecastRunner, preparer forecastPreparer, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	claimAndRunForecastJobs(ctx, queue, store, service, preparer, workerID, claimBatch, leaseDuration, leaseRenew, logger, metrics)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimAndRunForecastJobs(ctx, queue, store, service, preparer, workerID, claimBatch, leaseDuration, leaseRenew, logger, metrics)
		}
	}
}

func claimAndRunForecastJobs(ctx context.Context, queue forecastJobQueue, store forecastInputLoader, service forecastRunner, preparer forecastPreparer, workerID string, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	if ctx.Err() != nil {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := queue.Claim(claimCtx, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("forecast_job_claim_failed", "error_code", "FORECAST_JOB_CLAIM_FAILED", "error", err)
		}
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if job.TriggerType == "SCHEDULE" {
			prepareScheduledForecast(ctx, queue, preparer, job, leaseDuration, logger)
			continue
		}
		executeForecastJob(ctx, queue, store, service, job, leaseDuration, leaseRenew, logger, metrics)
	}
}

// prepareScheduledForecast turns a scheduled plan into a Forecast job: preparation freezes the
// latest metric history and enqueues the run, which this worker then claims like any other.
func prepareScheduledForecast(ctx context.Context, queue forecastJobQueue, preparer forecastPreparer, job schedulerjob.Job, leaseDuration time.Duration, logger *slog.Logger) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("scheduled_forecast_start_failed", "error_code", "FORECAST_SCHEDULE_START_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}
	if !started {
		return
	}
	request, err := forecast.ValidateScheduledForecastPlan(job)
	if err != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "FORECAST_PLAN_INVALID", err, false, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("scheduled_forecast_invalid_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}
	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultJobTimeoutFallback
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	prepared, err := preparer.Prepare(jobCtx, request)
	if err != nil {
		// Interrupted runs remain retryable; too little history is not retried since the next plan reads newer history.
		retryable := ctx.Err() != nil || !errors.Is(err, forecast.ErrPreparationUnavailable)
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "FORECAST_PREPARATION_FAILED", err, retryable, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("scheduled_forecast_fail_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}
	output := map[string]any{"forecastJobId": prepared.ForecastJobID, "forecastSnapshotId": prepared.ForecastSnapshotID}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil && logger != nil {
		logger.Warn("scheduled_forecast_complete_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}

func executeForecastJob(ctx context.Context, queue forecastJobQueue, store forecastInputLoader, service forecastRunner, job schedulerjob.Job, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("forecast_job_start_failed", "error_code", "FORECAST_JOB_START_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}
	if !started {
		return
	}
	reference, validationErr := forecast.ValidateForecastSchedulerJob(job)
	if validationErr != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "FORECAST_JOB_INVALID", validationErr, false, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("forecast_job_invalid_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultJobTimeoutFallback
	}
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()
	jobCtx, cancelJob := context.WithCancelCause(timeoutCtx)
	defer cancelJob(nil)

	request, loadErr := store.LoadForecastRequest(jobCtx, job.TenantID, job.SiteID, reference)
	if loadErr != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "FORECAST_INPUT_LOAD_FAILED", loadErr, true, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("forecast_job_load_fail_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}

	leaseStop := make(chan struct{})
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		defer observability.Recover(logger, metrics, "forecast.lease_renewer", func(any) {
			cancelJob(nil)
		})
		ticker := time.NewTicker(leaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-leaseStop:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(ctx, 5*time.Second)
				cancelRequested, renewErr := queue.Renew(renewCtx, job, leaseDuration, time.Now().UTC())
				renewCancel()
				if cancelRequested {
					cancelJob(errOperatorCanceled)
					return
				}
				if renewErr != nil {
					cancelJob(renewErr)
					return
				}
			}
		}
	}()

	points, executionErr := service.Forecast(jobCtx, request)
	close(leaseStop)
	<-leaseDone

	if executionErr != nil {
		// Strictly separate OS shutdown (SIGTERM -> ctx.Err() != nil -> retryable) from operator cancellation.
		operatorCanceled := errors.Is(context.Cause(jobCtx), errOperatorCanceled) && ctx.Err() == nil
		retryable := !operatorCanceled
		errorCode := "FORECAST_EXECUTION_FAILED"
		if operatorCanceled {
			errorCode = "FORECAST_CANCELED_BY_OPERATOR"
		} else if errors.Is(executionErr, context.DeadlineExceeded) && ctx.Err() == nil {
			errorCode = "TIMEOUT"
		}
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		err = queue.Fail(finalizeCtx, job, errorCode, executionErr, retryable, time.Now().UTC())
		finalizeCancel()
		if err != nil && logger != nil {
			logger.Warn("forecast_job_fail_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}

	quality := ""
	if len(points) > 0 {
		quality = points[0].Quality
	}
	output := map[string]any{"forecastSnapshotId": request.ForecastSnapshotID, "forecastJobId": request.ForecastJobID, "pointCount": len(points), "quality": quality}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil && logger != nil {
		logger.Warn("forecast_job_complete_finalize_failed", "error_code", "FORECAST_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func integerEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > 100 {
		return 0, fmt.Errorf("%s must be an integer in [1,100]", name)
	}
	return parsed, nil
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "forecast"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
