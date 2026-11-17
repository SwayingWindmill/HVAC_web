package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	"github.com/quanlaihe/hvac-web/libs/energyseries"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

const (
	defaultShutdownTimeout    = 10 * time.Second
	defaultPanicRestartDelay  = time.Second
	defaultFinalizeJobTimeout = 5 * time.Second
)

type monitorPassRunner interface {
	RunMonitorPass(context.Context, time.Time) (optimization.MonitorPassSummary, error)
}

type stalePublicationReconciler interface {
	Reconcile(context.Context, time.Time, int) (int, error)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	metrics := observability.NewRegistry()

	pool, err := pgxpool.New(ctx, requiredEnv("OPTIMIZATION_POSTGRES_DSN"))
	if err != nil {
		return err
	}
	defer pool.Close()

	publication, err := optimization.NewPostgresStore(pool)
	if err != nil {
		return err
	}
	evaluations, err := optimization.NewClickHouseSink(
		requiredEnv("OPTIMIZATION_CLICKHOUSE_HTTP_URL"),
		envOrDefault("OPTIMIZATION_CLICKHOUSE_USERNAME", "optimization_service_writer"),
		os.Getenv("OPTIMIZATION_CLICKHOUSE_PASSWORD"), nil,
	)
	if err != nil {
		return err
	}
	// The priced day comes from the Telemetry Query Service; the optimization service presents its
	// own workload identity to IAM and to the query service (ADR 0017, ADR 0023).
	energy, err := energyseries.NewWorkload(energyseries.WorkloadConfig{
		CertificatePath: requiredEnv("OPTIMIZATION_TLS_CERT"), PrivateKeyPath: requiredEnv("OPTIMIZATION_TLS_KEY"), CAPath: requiredEnv("OPTIMIZATION_TLS_CA"),
		IAMURL: requiredEnv("OPTIMIZATION_IAM_URL"), QueryURL: requiredEnv("OPTIMIZATION_QUERY_URL"),
	})
	if err != nil {
		return err
	}
	inputs, err := optimization.NewClickHouseInputReader(optimization.InputReaderConfig{
		BaseURL:  requiredEnv("OPTIMIZATION_CLICKHOUSE_HTTP_URL"),
		Username: envOrDefault("OPTIMIZATION_CLICKHOUSE_READER_USERNAME", "optimization_service_reader"),
		Password: os.Getenv("OPTIMIZATION_CLICKHOUSE_READER_PASSWORD"),
		Energy:   energy,
	})
	if err != nil {
		return err
	}
	telemetryHealthClient, err := optimization.NewWorkloadTelemetryHealthClient(optimization.TelemetryHealthClientConfig{
		CertificatePath: requiredEnv("OPTIMIZATION_TLS_CERT"),
		PrivateKeyPath:  requiredEnv("OPTIMIZATION_TLS_KEY"),
		CAPath:          requiredEnv("OPTIMIZATION_TLS_CA"),
		TelemetryURL:    envOrDefault("OPTIMIZATION_TELEMETRY_URL", "https://telemetry-runtime-service:8446"),
	})
	if err != nil {
		return err
	}
	preparer, err := optimization.NewPreparer(publication, inputs, time.Now)
	if err != nil {
		return err
	}
	preparer.WithPointHealth(telemetryHealthClient)
	service, err := optimization.NewDefaultService(publication, evaluations, time.Now)
	if err != nil {
		return err
	}
	service.WithShadowReader(inputs)
	service.WithStaging(&optimization.StagingEvaluator{
		History:  inputs,
		Snapshot: inputs,
		Catalog:  publication,
		Policy:   optimization.DefaultStagingPolicy(),
	})
	// The Chiller COP Model runs as a shadow solver: it records what it would recommend beside
	// every published run and never publishes. OPTIMIZATION_SHADOW_SOLVER=OFF turns it off.
	switch shadow := envOrDefault("OPTIMIZATION_SHADOW_SOLVER", optimization.ChillerCOPSolverName); shadow {
	case optimization.ChillerCOPSolverName:
		service.WithShadow(optimization.COPModelSolver{History: inputs, Staging: inputs, StagingCatalog: publication}, 30*time.Second, log.Printf)
	case "OFF":
	default:
		return fmt.Errorf("OPTIMIZATION_SHADOW_SOLVER must be %s or OFF, not %q", optimization.ChillerCOPSolverName, shadow)
	}
	// Only the Gateway may call Optimization, over mutual TLS with a context it signed (#612).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: requiredEnv("OPTIMIZATION_TLS_CERT"), PrivateKeyPath: requiredEnv("OPTIMIZATION_TLS_KEY")},
		ClientCAPath:     requiredEnv("OPTIMIZATION_TLS_CA"),
	})
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(requiredEnv("OPTIMIZATION_GATEWAY_DELEGATION_CERT"))
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
		CertificatePath: requiredEnv("OPTIMIZATION_TLS_CERT"),
		PrivateKeyPath:  requiredEnv("OPTIMIZATION_TLS_KEY"),
		CAPath:          requiredEnv("OPTIMIZATION_TLS_CA"),
		IAMURL:          requiredEnv("OPTIMIZATION_IAM_URL"),
		GatewayURL:      envOrDefault("OPTIMIZATION_GATEWAY_URL", "https://platform-gateway:8452"),
	})
	if err != nil {
		return err
	}
	workerID := envOrDefault("OPTIMIZATION_WORKER_ID", defaultWorkerID())
	monitorLeaseDuration := durationEnv("OPTIMIZATION_MONITOR_LEASE_DURATION", optimization.DefaultExecutionMonitorClaimLease)
	autoController, err := optimization.NewAutoController(publication, inputs, commandClient, time.Now, log.Printf)
	if err != nil {
		return err
	}
	autoController.WithPointHealth(telemetryHealthClient).WithWorkerID(workerID).WithLeaseDuration(monitorLeaseDuration)

	claimInterval := durationEnv("OPTIMIZATION_CLAIM_INTERVAL", 5*time.Second)
	leaseDuration := durationEnv("OPTIMIZATION_LEASE_DURATION", 2*time.Minute)
	leaseRenew := durationEnv("OPTIMIZATION_LEASE_RENEW", 30*time.Second)
	claimBatch := integerEnv("OPTIMIZATION_CLAIM_BATCH", 8)

	var wg sync.WaitGroup
	runSupervised(ctx, &wg, logger, metrics, "optimization.worker", defaultPanicRestartDelay, func() {
		runOptimizationWorker(ctx, queue, publication, service, preparer, autoController, workerID, claimInterval, claimBatch, leaseDuration, leaseRenew, logger, metrics)
	})
	runSupervised(ctx, &wg, logger, metrics, "optimization.execution_monitor", defaultPanicRestartDelay, func() {
		runMonitorLoop(ctx, optimization.DefaultMonitorTickInterval, autoController)
	})
	runSupervised(ctx, &wg, logger, metrics, "optimization.reconcile", defaultPanicRestartDelay, func() {
		runReconcileLoop(ctx, 30*time.Second, service)
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
		Addr: envOrDefault("OPTIMIZATION_DIAGNOSTICS_ADDR", ":19100"), Handler: optimization.HealthHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}

	serveErr := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "optimization.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		log.Printf("optimization-service listening on %s", server.Addr)
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Printf("optimization-service shutting down")
	case err := <-serveErr:
		runErr = err
		stop()
	}

	if shutdownErr := shutdownServers(defaultShutdownTimeout, server, diagnostics); shutdownErr != nil {
		log.Printf("optimization-service server shutdown error: %v", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	waitBackground(&wg, defaultShutdownTimeout)
	log.Printf("optimization-service stopped")
	return runErr
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

func runMonitorLoop(ctx context.Context, interval time.Duration, runner monitorPassRunner) {
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
			if monitorErr != nil && ctx.Err() == nil {
				log.Printf("optimization execution monitor pass failed: %v", monitorErr)
			}
		}
	}
}

func runReconcileLoop(ctx context.Context, interval time.Duration, reconciler stalePublicationReconciler) {
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
			if reconcileErr != nil && ctx.Err() == nil {
				log.Printf("optimization reconciliation failed: %v", reconcileErr)
			}
		}
	}
}

func shutdownServers(timeout time.Duration, servers ...*http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var firstErr error
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func waitBackground(wg *sync.WaitGroup, timeout time.Duration) {
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
		log.Printf("optimization-service timed out waiting for background workers to exit")
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func runOptimizationWorker(ctx context.Context, queue *schedulerjob.Queue, store *optimization.PostgresStore, service *optimization.Service, preparer *optimization.Preparer, autoController *optimization.AutoController, workerID string, claimInterval time.Duration, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
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

func claimAndRunOptimizationJobs(ctx context.Context, queue *schedulerjob.Queue, store *optimization.PostgresStore, service *optimization.Service, preparer *optimization.Preparer, autoController *optimization.AutoController, workerID string, claimBatch int, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	if ctx.Err() != nil {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	jobs, err := queue.Claim(claimCtx, workerID, claimBatch, leaseDuration, time.Now().UTC())
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("optimization job claim failed: %v", err)
		}
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if optimization.IsScheduledPreparation(job) {
			prepareScheduledOptimization(ctx, queue, preparer, job, leaseDuration)
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
	AutoDispatchSkipReason string                                   `json:"autoDispatchSkipReason,omitempty"`
	CommandIntentID        string                                   `json:"commandIntentId,omitempty"`
	MonitorID              string                                   `json:"monitorId,omitempty"`
}

// prepareScheduledOptimization turns a Site's schedule firing into a prepared run on its current
// state; the run's own job then executes like an operator's. A firing that cannot prepare fails
// without retry: the next hour's firing reads the Site again.
func prepareScheduledOptimization(ctx context.Context, queue *schedulerjob.Queue, preparer *optimization.Preparer, job schedulerjob.Job, leaseDuration time.Duration) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		log.Printf("optimization schedule firing %s start failed: %v", job.JobID, err)
		return
	}
	if !started {
		return
	}
	request, err := optimization.ScheduledPreparationRequest(job)
	if err != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
		_ = queue.Fail(finalizeCtx, job, "OPTIMIZATION_JOB_INVALID", err, false, time.Now().UTC())
		finalizeCancel()
		return
	}
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	prepared, err := preparer.Prepare(jobCtx, request)
	cancel()
	if err != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
		failErr := queue.Fail(finalizeCtx, job, optimization.PreparationFailureCode(err), err, false, time.Now().UTC())
		finalizeCancel()
		if failErr != nil {
			log.Printf("optimization schedule firing %s failure finalization failed: %v", job.JobID, failErr)
		}
		return
	}
	output := preparationJobOutput{
		OptimizationRunID: prepared.OptimizationRunID,
		InputSnapshotID:   prepared.InputSnapshotID,
	}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil {
		log.Printf("optimization schedule firing %s success finalization failed: %v", job.JobID, err)
	}
}

func executeOptimizationJob(ctx context.Context, queue *schedulerjob.Queue, store *optimization.PostgresStore, service *optimization.Service, autoController *optimization.AutoController, job schedulerjob.Job, leaseDuration, leaseRenew time.Duration, logger *slog.Logger, metrics *observability.Registry) {
	started, err := queue.Start(ctx, job, leaseDuration, time.Now().UTC())
	if err != nil {
		log.Printf("optimization job %s start failed: %v", job.JobID, err)
		return
	}
	if !started {
		return
	}
	reference, validationErr := optimization.ValidateOptimizationSchedulerJob(job)
	if validationErr != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
		_ = queue.Fail(finalizeCtx, job, "OPTIMIZATION_JOB_INVALID", validationErr, false, time.Now().UTC())
		finalizeCancel()
		return
	}
	jobCtx, cancelJob := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancelJob()
	request, loadErr := store.LoadOptimizationRequest(jobCtx, job.TenantID, job.SiteID, reference)
	if loadErr != nil {
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
		_ = queue.Fail(finalizeCtx, job, "OPTIMIZATION_INPUT_LOAD_FAILED", loadErr, false, time.Now().UTC())
		finalizeCancel()
		return
	}
	leaseStop := make(chan struct{})
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		defer observability.Recover(logger, metrics, "optimization.lease_renewer", func(any) {
			cancelJob()
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
				if renewErr != nil || cancelRequested {
					cancelJob()
					return
				}
			}
		}
	}()
	recommendation, executionErr := service.Optimize(jobCtx, request)
	close(leaseStop)
	<-leaseDone
	if executionErr != nil {
		retryable := !errors.Is(executionErr, context.Canceled)
		errorCode := "OPTIMIZATION_EXECUTION_FAILED"
		if errors.Is(executionErr, context.DeadlineExceeded) {
			errorCode = "TIMEOUT"
		}
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
		err = queue.Fail(finalizeCtx, job, errorCode, executionErr, retryable, time.Now().UTC())
		finalizeCancel()
		if err != nil {
			log.Printf("optimization job %s failure finalization failed: %v", job.JobID, err)
		}
		return
	}
	dispatchCtx, dispatchCancel := context.WithTimeout(ctx, 20*time.Second)
	dispatchOutcome, dispatchErr := autoController.TryDispatch(dispatchCtx, recommendation, time.Now().UTC())
	dispatchCancel()
	if dispatchErr != nil {
		log.Printf("optimization job %s auto-control dispatch failed: %v", job.JobID, dispatchErr)
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
		AutoDispatchSkipReason: dispatchOutcome.SkipReason,
		CommandIntentID:        dispatchOutcome.CommandIntentID,
		MonitorID:              dispatchOutcome.MonitorID,
	}
	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), defaultFinalizeJobTimeout)
	err = queue.Complete(finalizeCtx, job, output, time.Now().UTC())
	finalizeCancel()
	if err != nil {
		log.Printf("optimization job %s success finalization failed: %v", job.JobID, err)
	}
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		log.Fatalf("%s must be a positive duration", name)
	}
	return parsed
}

func integerEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > 100 {
		log.Fatalf("%s must be an integer in [1,100]", name)
	}
	return parsed
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "optimization"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
