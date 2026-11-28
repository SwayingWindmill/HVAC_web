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

	"github.com/quanlaihe/hvac-web/libs/energyseries"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/libs/servicehost"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/mv-service/internal/mv"
)

const (
	defaultShutdownTimeout        = 10 * time.Second
	defaultBackgroundDrainTimeout = 10 * time.Second
	defaultWorkerInterval         = time.Minute
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := runService(logger); err != nil {
		logger.Error("mv_service_failed", "error_code", "MV_SERVICE_FAILED", "error", err)
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
		Service:      "mv-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics
	shutdownTelemetry := servicehost.NewTelemetryShutdown(telemetry, servicehost.DefaultTelemetryShutdownPeriod)
	// Defer telemetry shutdown after pool closers are registered above so LIFO flushes telemetry first and closes pools last.
	defer shutdownTelemetry()

	dsn, err := servicehost.RequiredEnv("MV_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := mv.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, store.Close)

	chURL, err := servicehost.RequiredEnv("MV_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	chUser, err := servicehost.RequiredEnv("MV_CLICKHOUSE_READER_USERNAME")
	if err != nil {
		return err
	}
	hours, err := mv.NewClickHouseHours(chURL, chUser, os.Getenv("MV_CLICKHOUSE_READER_PASSWORD"), nil)
	if err != nil {
		return err
	}
	closers = append(closers, hours.CloseIdleConnections)

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: store.Ping},
		observability.Dependency{Name: "clickhouse", Required: true, Check: hours.Ping},
	)

	tlsCert, err := servicehost.RequiredEnv("MV_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := servicehost.RequiredEnv("MV_TLS_KEY")
	if err != nil {
		return err
	}
	tlsCA, err := servicehost.RequiredEnv("MV_TLS_CA")
	if err != nil {
		return err
	}
	iamURL, err := servicehost.RequiredEnv("MV_IAM_URL")
	if err != nil {
		return err
	}
	queryURL, err := servicehost.RequiredEnv("MV_QUERY_URL")
	if err != nil {
		return err
	}
	// Each verified hour is priced from the Telemetry Query Service; M&V presents its own workload
	// identity to IAM and to the query service (ADR 0017, ADR 0023).
	energy, err := energyseries.NewWorkload(energyseries.WorkloadConfig{
		CertificatePath: tlsCert, PrivateKeyPath: tlsKey, CAPath: tlsCA,
		IAMURL: iamURL, QueryURL: queryURL,
	})
	if err != nil {
		return err
	}
	service, err := mv.NewService(store, hours, store, mv.NewQueryRates(energy), time.Now, schedulerjob.UUIDv7)
	if err != nil {
		return err
	}
	// Only the Gateway may call M&V, over mutual TLS with a context it signed (#612).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: tlsCert, PrivateKeyPath: tlsKey},
		ClientCAPath:     tlsCA,
	})
	if err != nil {
		return err
	}
	delegationCert, err := servicehost.RequiredEnv("MV_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := mv.NewHTTPHandler(service, mv.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    servicehost.EnvOrDefault("MV_GATEWAY_SPIFFE", mv.DefaultGatewaySPIFFEID),
	})
	if err != nil {
		return err
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		return err
	}
	workerID := servicehost.EnvOrDefault("MV_WORKER_ID", servicehost.DefaultWorkerID("mv-service"))

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	servicehost.RunSupervised(workerCtx, &bgWG, logger, metrics, "mv.worker", servicehost.DefaultPanicRestartDelay, func() {
		runMVWorkerLoop(workerCtx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return mv.RunScheduledEvaluationsWithLogger(runCtx, queue, service, workerID, 8, logger)
		})
	})

	server := &http.Server{
		Addr:              servicehost.EnvOrDefault("MV_HTTP_ADDR", ":19096"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Implementing a measure fits fourteen days of hours read from ClickHouse.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              servicehost.EnvOrDefault("MV_DIAGNOSTICS_ADDR", ":19101"),
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	httpWG, runErr := servicehost.ServeAndWait(sigCtx, stopSignals, logger, metrics, "mv", server, diagnostics, true)

	if shutdownErr := servicehost.ShutdownRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "mv.shutdown", httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("mv_server_shutdown_failed", "error_code", "MV_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("mv_service_stopped")
	return runErr
}

func runMVWorkerLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, runOnce func(context.Context) error) {
	if runOnce == nil || ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runOnce(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("mv_scheduled_evaluations_failed", "error_code", "MV_SCHEDULED_EVALUATIONS_FAILED", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
