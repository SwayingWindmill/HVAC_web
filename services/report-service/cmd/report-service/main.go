package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/report-service/internal/report"
)

const (
	defaultShutdownTimeout         = 10 * time.Second
	defaultBackgroundDrainTimeout  = 10 * time.Second
	defaultPanicRestartDelay       = time.Second
	defaultWorkerInterval          = time.Minute
	defaultTelemetryShutdownPeriod = 5 * time.Second
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("report_service_failed", "error_code", "REPORT_SERVICE_FAILED", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	if logger == nil {
		logger = observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:      "report-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics

	dsn, err := requiredEnv("REPORT_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := report.OpenPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()

	// Defer telemetry shutdown after DB defer so LIFO teardown flushes telemetry first and closes pool last.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultTelemetryShutdownPeriod)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: store.Ping},
	)

	tlsCert, err := requiredEnv("REPORT_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := requiredEnv("REPORT_TLS_KEY")
	if err != nil {
		return err
	}
	tlsCA, err := requiredEnv("REPORT_TLS_CA")
	if err != nil {
		return err
	}
	iamURL, err := requiredEnv("REPORT_IAM_URL")
	if err != nil {
		return err
	}
	queryURL, err := requiredEnv("REPORT_QUERY_URL")
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
	delegationCert, err := requiredEnv("REPORT_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := report.NewHTTPHandler(service, report.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("REPORT_GATEWAY_SPIFFE", report.DefaultGatewaySPIFFEID),
		Logger:           logger,
	})
	if err != nil {
		return err
	}
	queue, err := store.GenerationQueue()
	if err != nil {
		return err
	}
	workerID := envOrDefault("REPORT_WORKER_ID", "report-service:"+hostname())

	var wg sync.WaitGroup
	runSupervised(ctx, &wg, logger, metrics, "report.worker", defaultPanicRestartDelay, func() {
		runReportWorkerLoop(ctx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return report.RunScheduledGenerationsWithLogger(runCtx, queue, service, workerID, 4, logger)
		})
	})

	server := &http.Server{
		Addr:              envOrDefault("REPORT_HTTP_ADDR", ":19097"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              envOrDefault("REPORT_DIAGNOSTICS_ADDR", ":19102"),
		Handler:           report.NewDiagnosticsHandler(telemetry),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	serveErr := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "report.diagnostics_server", func(any) {
			select {
			case serveErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if serveErrVal := diagnostics.ListenAndServe(); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("diagnostics server: %w", serveErrVal)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "report.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("report_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr)
		if serveErrVal := server.ListenAndServeTLS("", ""); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", serveErrVal)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("report_service_shutting_down")
	case err := <-serveErr:
		runErr = err
		stop()
	}

	telemetry.MarkNotReady()
	shutdownDone, shutdownResult := startServerShutdown(defaultShutdownTimeout, logger, metrics, "report.shutdown", server, diagnostics)
	<-shutdownDone
	if shutdownErr := shutdownResult(); shutdownErr != nil {
		logger.Warn("report_server_shutdown_failed", "error_code", "REPORT_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	waitBackground(&wg, defaultBackgroundDrainTimeout, logger)
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
			logger.Warn("report_background_wait_timeout", "error_code", "REPORT_SHUTDOWN_DRAIN_TIMEOUT", "timeout", timeout.String())
		}
	}
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
