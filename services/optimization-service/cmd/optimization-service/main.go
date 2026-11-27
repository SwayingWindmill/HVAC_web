package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/energyseries"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

const (
	defaultShutdownTimeout         = 10 * time.Second
	defaultBackgroundDrainTimeout  = 12 * time.Second
	defaultPanicRestartDelay       = time.Second
	defaultFinalizeJobTimeout      = 5 * time.Second
	defaultDispatchTimeout         = 20 * time.Second
	defaultDispatchShutdownGrace   = 5 * time.Second
	defaultJobTimeoutFallback      = 5 * time.Minute
	defaultTelemetryShutdownPeriod = 5 * time.Second
)

var errOperatorCanceled = errors.New("optimization job canceled by operator")

type optimizationJobQueue interface {
	Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error)
	Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error)
	Renew(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error)
	Complete(context.Context, schedulerjob.Job, any, time.Time) error
	Fail(context.Context, schedulerjob.Job, string, error, bool, time.Time) error
}

type optimizationInputLoader interface {
	optimization.SiteCycleLock
	LoadOptimizationRequest(context.Context, string, string, optimization.SchedulerOptimizationReference) (optimization.Request, error)
}

type optimizationRunner interface {
	Optimize(context.Context, optimization.Request) (optimization.Recommendation, error)
}

type optimizationPreparer interface {
	Prepare(context.Context, optimization.PreparationRequest) (optimization.PreparedOptimization, error)
}

type autoDispatcher interface {
	TryDispatch(context.Context, optimization.Recommendation, time.Time) (optimization.AutoDispatchOutcome, error)
}

type monitorPassRunner interface {
	RunMonitorPass(context.Context, time.Time) (optimization.MonitorPassSummary, error)
}

type stalePublicationReconciler interface {
	Reconcile(context.Context, time.Time, int) (int, error)
}

type stagingSweepRunner interface {
	EvaluateAndPersistAllSiteStaging(context.Context, time.Time) (optimization.StagingSweepSummary, error)
}

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := runService(logger); err != nil {
		logger.Error("optimization_service_failed", "error_code", "OPTIMIZATION_SERVICE_FAILED", "error", err)
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
		Service:      "optimization-service",
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

	dsn, err := requiredEnv("OPTIMIZATION_POSTGRES_DSN")
	if err != nil {
		return err
	}
	publication, err := optimization.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, publication.Close)

	clickhouseURL, err := requiredEnv("OPTIMIZATION_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	evaluations, err := optimization.NewClickHouseSink(
		clickhouseURL,
		envOrDefault("OPTIMIZATION_CLICKHOUSE_USERNAME", "optimization_service_writer"),
		os.Getenv("OPTIMIZATION_CLICKHOUSE_PASSWORD"), nil,
	)
	if err != nil {
		return err
	}
	closers = append(closers, evaluations.CloseIdleConnections)
	tlsCert, err := requiredEnv("OPTIMIZATION_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := requiredEnv("OPTIMIZATION_TLS_KEY")
	if err != nil {
		return err
	}
	tlsCA, err := requiredEnv("OPTIMIZATION_TLS_CA")
	if err != nil {
		return err
	}
	iamURL, err := requiredEnv("OPTIMIZATION_IAM_URL")
	if err != nil {
		return err
	}
	queryURL, err := requiredEnv("OPTIMIZATION_QUERY_URL")
	if err != nil {
		return err
	}
	// The priced day comes from the Telemetry Query Service; the optimization service presents its
	// own workload identity to IAM and to the query service (ADR 0017, ADR 0023).
	energy, err := energyseries.NewWorkload(energyseries.WorkloadConfig{
		CertificatePath: tlsCert, PrivateKeyPath: tlsKey, CAPath: tlsCA,
		IAMURL: iamURL, QueryURL: queryURL,
	})
	if err != nil {
		return err
	}
	telemetryLiveClient, telemetryHealthClient, err := newTelemetryClients(tlsCert, tlsKey, tlsCA)
	if err != nil {
		return err
	}
	inputs, err := optimization.NewClickHouseInputReader(optimization.InputReaderConfig{
		BaseURL:          clickhouseURL,
		Username:         envOrDefault("OPTIMIZATION_CLICKHOUSE_READER_USERNAME", "optimization_service_reader"),
		Password:         os.Getenv("OPTIMIZATION_CLICKHOUSE_READER_PASSWORD"),
		Energy:           energy,
		LiveObservations: telemetryLiveClient,
	})
	if err != nil {
		return err
	}
	closers = append(closers, inputs.CloseIdleConnections)

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: publication.Ping},
		observability.Dependency{Name: "clickhouse", Required: true, Check: inputs.Ping},
	)
	preparer, err := optimization.NewPreparer(publication, inputs, time.Now)
	if err != nil {
		return err
	}
	preparer.WithPointHealth(telemetryHealthClient)
	allowedSolverHosts := optimization.ParseAllowedSolverHosts(os.Getenv("OPTIMIZATION_ALLOWED_SOLVER_HOSTS"))
	solverRouter := optimization.NewRoutingSolverAdapter(optimization.RoutingSolverConfig{
		Resolver:     publication,
		Baseline:     optimization.HVACRecommendationSolver{},
		COPModel:     &optimization.COPModelSolver{History: inputs, Staging: inputs, StagingCatalog: publication},
		AllowedHosts: allowedSolverHosts,
		Timeout:      intelligencemodel.DefaultInferenceCallTimeout,
		Breaker:      intelligencemodel.NewCircuitBreaker(intelligencemodel.CircuitBreakerConfig{}),
		Bulkhead:     intelligencemodel.NewBulkhead(intelligencemodel.DefaultBulkheadMaxConcurrent),
		Logf: func(format string, _ ...any) {
			logger.Warn("optimization_solver_router_notice", "event", strings.SplitN(format, ":", 2)[0])
		},
	})
	service, err := optimization.NewService(solverRouter, publication, evaluations, time.Now)
	if err != nil {
		return err
	}
	service.WithShadowReader(inputs)
	service.WithAllowedSolverHosts(allowedSolverHosts)
	service.WithStaging(&optimization.StagingEvaluator{
		History:  inputs,
		Snapshot: inputs,
		Catalog:  publication,
		Policy:   optimization.DefaultStagingPolicy(),
	})
	service.WithStagingSiteLister(optimization.NewCompositeStagingSiteLister(inputs, publication))
	// The Chiller COP Model runs as a shadow solver: it records what it would recommend beside
	// every published run and never publishes. OPTIMIZATION_SHADOW_SOLVER=OFF turns it off.
	switch shadow := envOrDefault("OPTIMIZATION_SHADOW_SOLVER", optimization.ChillerCOPSolverName); shadow {
	case optimization.ChillerCOPSolverName:
		service.WithShadow(optimization.COPModelSolver{History: inputs, Staging: inputs, StagingCatalog: publication}, 30*time.Second, func(format string, _ ...any) {
			logger.Warn("optimization_shadow_solver_notice", "event", strings.SplitN(format, ":", 2)[0])
		})
	case "OFF":
	default:
		return fmt.Errorf("OPTIMIZATION_SHADOW_SOLVER must be %s or OFF, not %q", optimization.ChillerCOPSolverName, shadow)
	}
	// Only the Gateway may call Optimization, over mutual TLS with a context it signed (#612).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: tlsCert, PrivateKeyPath: tlsKey},
		ClientCAPath:     tlsCA,
	})
	if err != nil {
		return err
	}
	delegationCertPath, err := requiredEnv("OPTIMIZATION_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCertPath)
	if err != nil {
		return err
	}
	handler, err := optimization.NewHTTPHandler(service, preparer, optimization.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("OPTIMIZATION_GATEWAY_SPIFFE", optimization.DefaultGatewaySPIFFEID),
	})
	if err != nil {
		return err
	}

	queue, err := publication.RunQueue()
	if err != nil {
		return err
	}
	commandClient, err := optimization.NewWorkloadCommandClient(optimization.WorkloadCommandClientConfig{
		CertificatePath: tlsCert,
		PrivateKeyPath:  tlsKey,
		CAPath:          tlsCA,
		IAMURL:          iamURL,
		GatewayURL:      envOrDefault("OPTIMIZATION_GATEWAY_URL", "https://platform-gateway:8452"),
	})
	if err != nil {
		return err
	}
	workerID := envOrDefault("OPTIMIZATION_WORKER_ID", defaultWorkerID())
	monitorLeaseDuration, err := durationEnv("OPTIMIZATION_MONITOR_LEASE_DURATION", optimization.DefaultExecutionMonitorClaimLease)
	if err != nil {
		return err
	}
	autoController, err := optimization.NewAutoController(publication, inputs, commandClient, time.Now, func(format string, _ ...any) {
		logger.Warn("optimization_auto_control_notice", "event", strings.SplitN(format, ":", 2)[0])
	})
	if err != nil {
		return err
	}
	var controlEvents optimization.ControlEventPublisher
	if alarmURL := strings.TrimSpace(envOrDefault("OPTIMIZATION_ALARM_URL", "https://alarm-service:8448")); alarmURL != "" &&
		!strings.EqualFold(alarmURL, "OFF") && !strings.EqualFold(alarmURL, "DISABLED") {
		publisher, pubErr := optimization.NewHTTPControlEventPublisher(optimization.ControlEventClientConfig{
			CertificatePath: tlsCert,
			PrivateKeyPath:  tlsKey,
			CAPath:          tlsCA,
			AlarmURL:        alarmURL,
			ServerName:      strings.TrimSpace(os.Getenv("OPTIMIZATION_ALARM_SERVER_NAME")),
			Timeout:         3 * time.Second,
		})
		if pubErr != nil {
			logger.Warn("optimization_control_alarm_publisher_disabled", "error_code", "OPTIMIZATION_ALARM_CLIENT_DISABLED", "error", pubErr)
		} else {
			controlEvents = publisher
		}
	}
	autoController.WithPointHealth(telemetryHealthClient).WithControlEvents(controlEvents).WithWorkerID(workerID).WithLeaseDuration(monitorLeaseDuration)

	claimInterval, err := durationEnv("OPTIMIZATION_CLAIM_INTERVAL", 5*time.Second)
	if err != nil {
		return err
	}
	leaseDuration, err := durationEnv("OPTIMIZATION_LEASE_DURATION", 2*time.Minute)
	if err != nil {
		return err
	}
	leaseRenew, err := durationEnv("OPTIMIZATION_LEASE_RENEW", 30*time.Second)
	if err != nil {
		return err
	}
	claimBatch, err := integerEnv("OPTIMIZATION_CLAIM_BATCH", 8)
	if err != nil {
		return err
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	runSupervised(workerCtx, &bgWG, logger, metrics, "optimization.worker", defaultPanicRestartDelay, func() {
		runOptimizationWorker(workerCtx, queue, publication, service, preparer, autoController, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew, logger, metrics)
	})
	runSupervised(workerCtx, &bgWG, logger, metrics, "optimization.execution_monitor", defaultPanicRestartDelay, func() {
		runMonitorLoop(workerCtx, optimization.DefaultMonitorTickInterval, autoController, logger)
	})
	runSupervised(workerCtx, &bgWG, logger, metrics, "optimization.reconcile", defaultPanicRestartDelay, func() {
		runReconcileLoop(workerCtx, 30*time.Second, service, logger)
	})
	runSupervised(workerCtx, &bgWG, logger, metrics, "optimization.staging_evaluation", defaultPanicRestartDelay, func() {
		runStagingEvaluationLoop(workerCtx, optimization.DefaultStagingSweepInterval, service, logger)
	})

	server := &http.Server{
		Addr:              envOrDefault("OPTIMIZATION_HTTP_ADDR", ":19093"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr: envOrDefault("OPTIMIZATION_DIAGNOSTICS_ADDR", ":19100"), Handler: optimization.NewDiagnosticsHandler(telemetry),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}

	telemetry.MarkReady()

	serveErr := make(chan error, 2)
	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, metrics, "optimization.diagnostics_server", func(any) {
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
		defer observability.Recover(logger, metrics, "optimization.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("optimization_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr)
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", err)
		}
	}()

	var runErr error
	select {
	case <-sigCtx.Done():
		logger.Info("optimization_service_shutting_down")
	case err := <-serveErr:
		runErr = err
		stopSignals()
	}

	if shutdownErr := shutdownServiceRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "optimization.shutdown", &httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("optimization_server_shutdown_failed", "error_code", "OPTIMIZATION_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("optimization_service_stopped")
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

func runMonitorLoop(ctx context.Context, interval time.Duration, runner monitorPassRunner, logger *slog.Logger) {
	if runner == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			monitorCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			_, monitorErr := runner.RunMonitorPass(monitorCtx, time.Now().UTC())
			cancel()
			if monitorErr != nil && ctx.Err() == nil && logger != nil {
				logger.Warn("optimization_monitor_pass_failed", "error_code", "OPTIMIZATION_MONITOR_PASS_FAILED", "error", monitorErr)
			}
		}
	}
}

func runReconcileLoop(ctx context.Context, interval time.Duration, reconciler stalePublicationReconciler, logger *slog.Logger) {
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
				logger.Warn("optimization_reconcile_failed", "error_code", "OPTIMIZATION_RECONCILE_FAILED", "error", reconcileErr)
			}
		}
	}
}

func runStagingEvaluationLoop(ctx context.Context, interval time.Duration, runner stagingSweepRunner, logger *slog.Logger) {
	if runner == nil {
		return
	}
	if interval <= 0 {
		interval = optimization.DefaultStagingSweepInterval
	}
	runOnce := func() {
		if ctx.Err() != nil {
			return
		}
		sweepCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		summary, sweepErr := runner.EvaluateAndPersistAllSiteStaging(sweepCtx, time.Now().UTC())
		cancel()
		if sweepErr != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("optimization_staging_sweep_failed", "error_code", "OPTIMIZATION_STAGING_SWEEP_FAILED", "sites", summary.Sites, "persisted", summary.Persisted, "failed", summary.Failed, "error", sweepErr)
		}
	}
	runOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
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
			logger.Warn("optimization_background_shutdown_timeout", "error_code", "OPTIMIZATION_SHUTDOWN_TIMEOUT")
		}
	}
}

func newTelemetryClients(tlsCert, tlsKey, tlsCA string) (*optimization.WorkloadTelemetryLiveClient, *optimization.WorkloadTelemetryHealthClient, error) {
	telemetryURL := strings.TrimRight(envOrDefault("OPTIMIZATION_TELEMETRY_URL", "https://telemetry-runtime-service:8446"), "/")
	parsed, err := url.Parse(telemetryURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, nil, fmt.Errorf("invalid OPTIMIZATION_TELEMETRY_URL %q", telemetryURL)
	}
	httpClient, err := workloadtls.NewHTTPClient(workloadtls.ClientConfig{
		CertificateFiles: &workloadtls.CertificateFiles{
			CertificatePath: tlsCert,
			PrivateKeyPath:  tlsKey,
		},
		ServerCAPath: tlsCA,
		ServerName:   parsed.Hostname(),
		Timeout:      10 * time.Second,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create telemetry mTLS client: %w", err)
	}
	liveClient, err := optimization.NewWorkloadTelemetryLiveClient(optimization.TelemetryLiveClientConfig{
		TelemetryURL: telemetryURL,
		HTTPClient:   httpClient,
	})
	if err != nil {
		return nil, nil, err
	}
	healthClient, err := optimization.NewWorkloadTelemetryHealthClient(optimization.TelemetryHealthClientConfig{
		TelemetryURL: telemetryURL,
		HTTPClient:   httpClient,
	})
	if err != nil {
		return nil, nil, err
	}
	return liveClient, healthClient, nil
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

func runOptimizationWorker(ctx context.Context, queue optimizationJobQueue, store optimizationInputLoader, service optimizationRunner, preparer optimizationPreparer, autoController autoDispatcher, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	if ctx.Err() != nil {
		return
	}
	claimAndRunOptimizationJobs(ctx, queue, store, service, preparer, autoController, workerID, claimBatch, leaseDuration, leaseRenew, logger, metrics)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimAndRunOptimizationJobs(ctx, queue, store, service, preparer, autoController, workerID, claimBatch, leaseDuration, leaseRenew, logger, metrics)
		}
	}
}

func claimAndRunOptimizationJobs(ctx context.Context, queue optimizationJobQueue, store optimizationInputLoader, service optimizationRunner, preparer optimizationPreparer, autoController autoDispatcher, workerID string, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	if ctx.Err() != nil {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := queue.Claim(claimCtx, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("optimization_job_claim_failed", "error_code", "OPTIMIZATION_JOB_CLAIM_FAILED", "error", err)
		}
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if optimization.IsScheduledPreparation(job) {
			prepareScheduledOptimization(ctx, queue, preparer, job, leaseDuration, logger)
			continue
		}
		executeOptimizationJob(ctx, queue, store, service, autoController, job, leaseDuration, leaseRenew, logger, metrics)
	}
}

type preparationJobOutput struct {
	OptimizationRunID string `json:"optimizationRunId"`
	InputSnapshotID   string `json:"inputSnapshotId"`
}

type optimizationJobOutput struct {
	OptimizationRunID      string                                   `json:"optimizationRunId"`
	RecommendationID       string                                   `json:"recommendationId"`
	ApprovalState          intelligencemodel.RecommendationApproval `json:"approvalState"`
	AutoDispatched         bool                                     `json:"autoDispatched"`
	ShadowRecorded         bool                                     `json:"shadowRecorded,omitempty"`
	ExecutionMode          string                                   `json:"executionMode,omitempty"`
	AutoDispatchSkipReason string                                   `json:"autoDispatchSkipReason,omitempty"`
	CommandIntentID        string                                   `json:"commandIntentId,omitempty"`
	MonitorID              string                                   `json:"monitorId,omitempty"`
	DecisionID             string                                   `json:"decisionId,omitempty"`
	TraceID                string                                   `json:"traceId,omitempty"`
}

type jobFailureFinalizer interface {
	Fail(context.Context, schedulerjob.Job, string, error, bool, time.Time) error
}

func acquireSiteCycleLockForJob(
	ctx context.Context,
	queue jobFailureFinalizer,
	locker optimization.SiteCycleLock,
	job schedulerjob.Job,
	logger *slog.Logger,
) (func(), bool) {
	if locker == nil {
		return func() {}, true
	}
	release, acquired, err := locker.AcquireSiteCycleLock(ctx, job.TenantID, job.SiteID)
	if err != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "OPTIMIZATION_SITE_LOCK_FAILED", err, true, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_job_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return nil, false
	}
	if !acquired {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "OPTIMIZATION_SITE_CYCLE_ACTIVE", errors.New("another optimization cycle is active for site"), true, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_job_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return nil, false
	}
	return release, true
}

// prepareScheduledOptimization turns a Site's schedule firing into a prepared run on its current
// state; the run's own job then executes like an operator's. A firing that cannot prepare fails
// without retry: the next hour's firing reads the Site again.
func prepareScheduledOptimization(ctx context.Context, queue optimizationJobQueue, preparer optimizationPreparer, job schedulerjob.Job, leaseDuration time.Duration, logger *slog.Logger) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("optimization_schedule_start_failed", "error_code", "OPTIMIZATION_SCHEDULE_START_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}
	if !started {
		return
	}
	request, err := optimization.ScheduledPreparationRequest(job)
	if err != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "OPTIMIZATION_JOB_INVALID", err, false, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_schedule_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}
	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultJobTimeoutFallback
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	prepared, err := preparer.Prepare(jobCtx, request)
	cancel()
	if err != nil {
		retryable := ctx.Err() != nil
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, optimization.PreparationFailureCode(err), err, retryable, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_schedule_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}
	output := preparationJobOutput{
		OptimizationRunID: prepared.OptimizationRunID,
		InputSnapshotID:   prepared.InputSnapshotID,
	}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil && logger != nil {
		logger.Warn("optimization_schedule_complete_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}

func dispatchContextWithShutdownGrace(ctx context.Context, normalTimeout, shutdownGrace time.Duration) (context.Context, context.CancelFunc) {
	if ctx.Err() != nil {
		return context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	}
	dispatchCtx, cancelDispatch := context.WithTimeout(context.WithoutCancel(ctx), normalTimeout)
	var (
		mu         sync.Mutex
		graceTimer *time.Timer
		closed     bool
	)
	stopAfter := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		graceTimer = time.AfterFunc(shutdownGrace, cancelDispatch)
	})
	return dispatchCtx, func() {
		stopAfter()
		mu.Lock()
		closed = true
		if graceTimer != nil {
			graceTimer.Stop()
		}
		mu.Unlock()
		cancelDispatch()
	}
}

func executeOptimizationJob(ctx context.Context, queue optimizationJobQueue, store optimizationInputLoader, service optimizationRunner, autoController autoDispatcher, job schedulerjob.Job, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		if ctx.Err() == nil && logger != nil {
			logger.Warn("optimization_job_start_failed", "error_code", "OPTIMIZATION_JOB_START_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}
	if !started {
		return
	}
	reference, validationErr := optimization.ValidateOptimizationSchedulerJob(job)
	if validationErr != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "OPTIMIZATION_JOB_INVALID", validationErr, false, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_job_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
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

	request, loadErr := store.LoadOptimizationRequest(jobCtx, job.TenantID, job.SiteID, reference)
	if loadErr != nil {
		retryable := ctx.Err() != nil
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, "OPTIMIZATION_INPUT_LOAD_FAILED", loadErr, retryable, time.Now().UTC())
		finalizeCancel()
		if failErr != nil && logger != nil {
			logger.Warn("optimization_job_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", failErr)
		}
		return
	}
	releaseSiteLock, locked := acquireSiteCycleLockForJob(jobCtx, queue, store, job, logger)
	if !locked {
		return
	}
	defer releaseSiteLock()

	leaseStop := make(chan struct{})
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		defer observability.Recover(logger, metrics, "optimization.lease_renewer", func(any) {
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
	recommendation, executionErr := service.Optimize(jobCtx, request)
	close(leaseStop)
	<-leaseDone
	if executionErr != nil {
		// Strictly decouple OS shutdown (SIGTERM -> ctx.Err() != nil -> retryable=true) from operator cancelRequested.
		operatorCanceled := errors.Is(context.Cause(jobCtx), errOperatorCanceled) && ctx.Err() == nil
		retryable := !operatorCanceled
		errorCode := "OPTIMIZATION_EXECUTION_FAILED"
		if operatorCanceled {
			errorCode = "OPTIMIZATION_CANCELED_BY_OPERATOR"
		} else if errors.Is(executionErr, context.DeadlineExceeded) && ctx.Err() == nil {
			errorCode = "TIMEOUT"
		}
		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
		err = queue.Fail(finalizeCtx, job, errorCode, executionErr, retryable, time.Now().UTC())
		finalizeCancel()
		if err != nil && logger != nil {
			logger.Warn("optimization_job_fail_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
		}
		return
	}
	dispatchCtx, dispatchCancel := dispatchContextWithShutdownGrace(ctx, defaultDispatchTimeout, defaultDispatchShutdownGrace)
	dispatchOutcome, dispatchErr := autoController.TryDispatch(dispatchCtx, recommendation, time.Now().UTC())
	dispatchCancel()
	if dispatchErr != nil && logger != nil {
		logger.Warn("optimization_auto_control_dispatch_failed", "error_code", "OPTIMIZATION_AUTO_DISPATCH_FAILED", "job_id", job.JobID, "error", dispatchErr)
	}
	approvalState := recommendation.Approval
	if dispatchOutcome.Dispatched {
		approvalState = intelligencemodel.RecommendationApproved
	}
	output := optimizationJobOutput{
		OptimizationRunID:      request.OptimizationRunID,
		RecommendationID:       recommendation.ID,
		ApprovalState:          approvalState,
		AutoDispatched:         dispatchOutcome.Dispatched,
		ShadowRecorded:         dispatchOutcome.ShadowRecorded,
		ExecutionMode:          dispatchOutcome.ExecutionMode,
		AutoDispatchSkipReason: dispatchOutcome.SkipReason,
		CommandIntentID:        dispatchOutcome.CommandIntentID,
		MonitorID:              dispatchOutcome.MonitorID,
		DecisionID:             dispatchOutcome.DecisionID,
		TraceID:                dispatchOutcome.TraceID,
	}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil && logger != nil {
		logger.Warn("optimization_job_complete_finalize_failed", "error_code", "OPTIMIZATION_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
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
		hostname = "optimization"
	}
	suffix := make([]byte, 4)
	if _, randErr := rand.Read(suffix); randErr != nil {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(hostname), os.Getpid())
	}
	return fmt.Sprintf("%s-%d-%s", strings.TrimSpace(hostname), os.Getpid(), hex.EncodeToString(suffix))
}
