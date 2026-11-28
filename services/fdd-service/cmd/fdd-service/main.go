package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/servicehost"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/fdd-service/internal/fdd"
)

const (
	defaultShutdownTimeout            = 10 * time.Second
	defaultBackgroundDrainTimeout     = 10 * time.Second
	defaultWorkerInterval             = 5 * time.Second
	defaultMeterImbalanceInterval     = time.Hour
	defaultMeterImbalanceSweepTimeout = 2 * time.Minute
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := runService(logger); err != nil {
		logger.Error("fdd_service_failed", "error_code", "FDD_SERVICE_FAILED", "error", err)
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
		Service:      "fdd-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics
	shutdownTelemetry := servicehost.NewTelemetryShutdown(telemetry, servicehost.DefaultTelemetryShutdownPeriod)
	// Defer telemetry shutdown after pool closers are registered above so LIFO flushes telemetry first and closes pools last.
	defer shutdownTelemetry()

	dsn, err := servicehost.RequiredEnv("FDD_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := fdd.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, store.Close)

	chURL, err := servicehost.RequiredEnv("FDD_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	chUser, err := servicehost.RequiredEnv("FDD_CLICKHOUSE_READER_USERNAME")
	if err != nil {
		return err
	}
	evidence, err := fdd.NewClickHouseEvidence(chURL, chUser, os.Getenv("FDD_CLICKHOUSE_READER_PASSWORD"), nil)
	if err != nil {
		return err
	}
	closers = append(closers, evidence.CloseIdleConnections)

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: store.Ping},
		observability.Dependency{Name: "clickhouse", Required: true, Check: evidence.Ping},
	)

	service, err := fdd.NewService(store, store.TwinLoader(), evidence, time.Now)
	if err != nil {
		return err
	}

	tlsCert, err := servicehost.RequiredEnv("FDD_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := servicehost.RequiredEnv("FDD_TLS_KEY")
	if err != nil {
		return err
	}
	clientCA, err := servicehost.RequiredEnv("FDD_CLIENT_CA")
	if err != nil {
		return err
	}
	// Only the Gateway, and the Operations Agent for a Site's findings, may call FDD, each over mutual
	// TLS with a context the Gateway signed (#595).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: tlsCert, PrivateKeyPath: tlsKey},
		ClientCAPath:     clientCA,
	})
	if err != nil {
		return err
	}
	delegationCert, err := servicehost.RequiredEnv("FDD_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := fdd.NewHTTPHandler(service, fdd.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    servicehost.EnvOrDefault("FDD_GATEWAY_SPIFFE", fdd.DefaultGatewaySPIFFEID),
		ReaderSPIFFE:     servicehost.EnvOrDefault("FDD_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
	})
	if err != nil {
		return err
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		return err
	}
	workerID := servicehost.EnvOrDefault("FDD_WORKER_ID", servicehost.DefaultWorkerID("fdd-service"))
	meterImbalanceInterval, err := servicehost.DurationEnv("FDD_METER_IMBALANCE_INTERVAL", defaultMeterImbalanceInterval)
	if err != nil {
		return err
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	servicehost.RunSupervised(workerCtx, &bgWG, logger, metrics, "fdd.worker", servicehost.DefaultPanicRestartDelay, func() {
		runFDDWorkerLoop(workerCtx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return fdd.RunScheduledEvaluationsWithLogger(runCtx, queue, service, workerID, 8, logger)
		})
	})
	servicehost.RunSupervised(workerCtx, &bgWG, logger, metrics, "fdd.meter_imbalance", servicehost.DefaultPanicRestartDelay, func() {
		runPeriodicFDDLoop(workerCtx, meterImbalanceInterval, logger, "fdd_meter_imbalance_sweep_failed", "FDD_METER_IMBALANCE_SWEEP_FAILED", func(runCtx context.Context) error {
			sweepCtx, sweepCancel := context.WithTimeout(runCtx, defaultMeterImbalanceSweepTimeout)
			defer sweepCancel()
			return fdd.RunMeterImbalanceSweep(sweepCtx, evidence, service, time.Now().UTC(), logger)
		})
	})

	server := &http.Server{
		Addr:              servicehost.EnvOrDefault("FDD_HTTP_ADDR", ":19094"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              servicehost.EnvOrDefault("FDD_DIAGNOSTICS_ADDR", ":19098"),
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	httpWG, runErr := servicehost.ServeAndWait(sigCtx, stopSignals, logger, metrics, "fdd", server, diagnostics, true)

	if shutdownErr := servicehost.ShutdownRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "fdd.shutdown", httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("fdd_server_shutdown_failed", "error_code", "FDD_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("fdd_service_stopped")
	return runErr
}

func runFDDWorkerLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, runOnce func(context.Context) error) {
	runPeriodicFDDLoop(ctx, interval, logger, "fdd_scheduled_evaluations_failed", "FDD_SCHEDULED_EVALUATIONS_FAILED", runOnce)
}

func runPeriodicFDDLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, eventName, errorCode string, runOnce func(context.Context) error) {
	if runOnce == nil || ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runOnce(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn(eventName, "error_code", errorCode, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
