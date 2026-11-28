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
	"github.com/quanlaihe/hvac-web/services/report-service/internal/report"
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
		logger.Error("report_service_failed", "error_code", "REPORT_SERVICE_FAILED", "error", err)
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
		Service:      "report-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics
	shutdownTelemetry := servicehost.NewTelemetryShutdown(telemetry, servicehost.DefaultTelemetryShutdownPeriod)
	// Defer telemetry shutdown after pool closers are registered above so LIFO flushes telemetry first and closes pools last.
	defer shutdownTelemetry()

	dsn, err := servicehost.RequiredEnv("REPORT_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := report.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, store.Close)

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: store.Ping},
	)

	tlsCert, err := servicehost.RequiredEnv("REPORT_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := servicehost.RequiredEnv("REPORT_TLS_KEY")
	if err != nil {
		return err
	}
	tlsCA, err := servicehost.RequiredEnv("REPORT_TLS_CA")
	if err != nil {
		return err
	}
	iamURL, err := servicehost.RequiredEnv("REPORT_IAM_URL")
	if err != nil {
		return err
	}
	queryURL, err := servicehost.RequiredEnv("REPORT_QUERY_URL")
	if err != nil {
		return err
	}
	// The Site's energy comes from the Telemetry Query Service; report-service presents its own
	// workload identity to IAM and to the query service (ADR 0017, ADR 0023).
	series, err := energyseries.NewWorkload(energyseries.WorkloadConfig{
		CertificatePath: tlsCert, PrivateKeyPath: tlsKey, CAPath: tlsCA,
		IAMURL: iamURL, QueryURL: queryURL,
	})
	if err != nil {
		return err
	}
	hours := report.NewQueryHours(series)
	service, err := report.NewService(store, hours, time.Now, schedulerjob.UUIDv7)
	if err != nil {
		return err
	}
	// Only the Gateway may call report-service, over mutual TLS with a context it signed (#613).
	serverTLS, err := workloadtls.NewServerTLSConfig(workloadtls.ServerConfig{
		CertificateFiles: workloadtls.CertificateFiles{CertificatePath: tlsCert, PrivateKeyPath: tlsKey},
		ClientCAPath:     tlsCA,
	})
	if err != nil {
		return err
	}
	delegationCert, err := servicehost.RequiredEnv("REPORT_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := report.NewHTTPHandler(service, report.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    servicehost.EnvOrDefault("REPORT_GATEWAY_SPIFFE", report.DefaultGatewaySPIFFEID),
		Logger:           logger,
	})
	if err != nil {
		return err
	}
	queue, err := store.GenerationQueue()
	if err != nil {
		return err
	}
	workerID := servicehost.EnvOrDefault("REPORT_WORKER_ID", servicehost.DefaultWorkerID("report-service"))

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	servicehost.RunSupervised(workerCtx, &bgWG, logger, metrics, "report.worker", servicehost.DefaultPanicRestartDelay, func() {
		runReportWorkerLoop(workerCtx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return report.RunScheduledGenerationsWithLogger(runCtx, queue, service, workerID, 4, logger)
		})
	})

	server := &http.Server{
		Addr:              servicehost.EnvOrDefault("REPORT_HTTP_ADDR", ":19097"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              servicehost.EnvOrDefault("REPORT_DIAGNOSTICS_ADDR", ":19102"),
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	httpWG, runErr := servicehost.ServeAndWait(sigCtx, stopSignals, logger, metrics, "report", server, diagnostics, true)

	if shutdownErr := servicehost.ShutdownRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "report.shutdown", httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("report_server_shutdown_failed", "error_code", "REPORT_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("report_service_stopped")
	return runErr
}

func runReportWorkerLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, runOnce func(context.Context) error) {
	if runOnce == nil || ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runOnce(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("report_generations_failed", "error_code", "REPORT_GENERATIONS_FAILED", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
