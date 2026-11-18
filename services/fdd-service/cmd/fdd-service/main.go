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

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
	"github.com/quanlaihe/hvac-web/services/fdd-service/internal/fdd"
)

const (
	defaultShutdownTimeout         = 10 * time.Second
	defaultBackgroundDrainTimeout  = 10 * time.Second
	defaultPanicRestartDelay       = time.Second
	defaultWorkerInterval          = 5 * time.Second
	defaultTelemetryShutdownPeriod = 5 * time.Second
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("fdd_service_failed", "error_code", "FDD_SERVICE_FAILED", "error", err)
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
		Service:      "fdd-service",
		OTLPEndpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
	})
	metrics := telemetry.Metrics

	dsn, err := requiredEnv("FDD_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := fdd.OpenPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()

	chURL, err := requiredEnv("FDD_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	chUser, err := requiredEnv("FDD_CLICKHOUSE_READER_USERNAME")
	if err != nil {
		return err
	}
	evidence, err := fdd.NewClickHouseEvidence(chURL, chUser, os.Getenv("FDD_CLICKHOUSE_READER_PASSWORD"), nil)
	if err != nil {
		return err
	}
	defer evidence.CloseIdleConnections()

	// Defer telemetry shutdown after DB/ClickHouse defers so LIFO teardown flushes telemetry first and closes pools last.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultTelemetryShutdownPeriod)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	telemetry.SetDependencies(
		observability.Dependency{Name: "postgres", Required: true, Check: store.Ping},
		observability.Dependency{Name: "clickhouse", Required: true, Check: evidence.Ping},
	)

	service, err := fdd.NewService(store, store.TwinLoader(), evidence, time.Now)
	if err != nil {
		return err
	}

	tlsCert, err := requiredEnv("FDD_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := requiredEnv("FDD_TLS_KEY")
	if err != nil {
		return err
	}
	clientCA, err := requiredEnv("FDD_CLIENT_CA")
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
	delegationCert, err := requiredEnv("FDD_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := fdd.NewHTTPHandler(service, fdd.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("FDD_GATEWAY_SPIFFE", fdd.DefaultGatewaySPIFFEID),
		ReaderSPIFFE:     envOrDefault("FDD_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
	})
	if err != nil {
		return err
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		return err
	}
	workerID := envOrDefault("FDD_WORKER_ID", "fdd-service:"+hostname())

	var wg sync.WaitGroup
	runSupervised(ctx, &wg, logger, metrics, "fdd.worker", defaultPanicRestartDelay, func() {
		runFDDWorkerLoop(ctx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return fdd.RunScheduledEvaluationsWithLogger(runCtx, queue, service, workerID, 8, logger)
		})
	})

	server := &http.Server{
		Addr:              envOrDefault("FDD_HTTP_ADDR", ":19094"),
		Handler:           handler,
		TLSConfig:         serverTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// The container health check has no client certificate, so it asks a plain port that answers nothing else.
	diagnostics := &http.Server{
		Addr:              envOrDefault("FDD_DIAGNOSTICS_ADDR", ":19098"),
		Handler:           fdd.NewDiagnosticsHandler(telemetry),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	telemetry.MarkReady()

	serveErr := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "fdd.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("fdd_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr)
		if serveErrVal := server.ListenAndServeTLS("", ""); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", serveErrVal)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "fdd.diagnostics_server", func(any) {
			select {
			case serveErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if serveErrVal := diagnostics.ListenAndServe(); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("diagnostics server: %w", serveErrVal)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("fdd_service_shutting_down")
	case err := <-serveErr:
		runErr = err
		stop()
	}

	telemetry.MarkNotReady()
	shutdownDone, shutdownResult := startServerShutdown(defaultShutdownTimeout, logger, metrics, "fdd.shutdown", server, diagnostics)
	<-shutdownDone
	if shutdownErr := shutdownResult(); shutdownErr != nil {
		logger.Warn("fdd_server_shutdown_failed", "error_code", "FDD_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	waitBackground(&wg, defaultBackgroundDrainTimeout, logger)
	logger.Info("fdd_service_stopped")
	return runErr
}

func runFDDWorkerLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, runOnce func(context.Context) error) {
	if runOnce == nil || ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runOnce(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("fdd_scheduled_evaluations_failed", "error_code", "FDD_SCHEDULED_EVALUATIONS_FAILED", "error", err)
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
			logger.Warn("fdd_background_wait_timeout", "error_code", "FDD_SHUTDOWN_DRAIN_TIMEOUT", "timeout", timeout.String())
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
