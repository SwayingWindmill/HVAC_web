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
	"github.com/quanlaihe/hvac-web/services/mv-service/internal/mv"
)

const (
	defaultShutdownTimeout         = 10 * time.Second
	defaultBackgroundDrainTimeout  = 10 * time.Second
	defaultPanicRestartDelay       = time.Second
	defaultWorkerInterval          = time.Minute
	defaultTelemetryShutdownPeriod = 5 * time.Second
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

	dsn, err := requiredEnv("MV_DATABASE_URL")
	if err != nil {
		return err
	}
	store, err := mv.OpenPostgres(sigCtx, dsn)
	if err != nil {
		return err
	}
	closers = append(closers, store.Close)

	chURL, err := requiredEnv("MV_CLICKHOUSE_HTTP_URL")
	if err != nil {
		return err
	}
	chUser, err := requiredEnv("MV_CLICKHOUSE_READER_USERNAME")
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

	tlsCert, err := requiredEnv("MV_TLS_CERT")
	if err != nil {
		return err
	}
	tlsKey, err := requiredEnv("MV_TLS_KEY")
	if err != nil {
		return err
	}
	tlsCA, err := requiredEnv("MV_TLS_CA")
	if err != nil {
		return err
	}
	iamURL, err := requiredEnv("MV_IAM_URL")
	if err != nil {
		return err
	}
	queryURL, err := requiredEnv("MV_QUERY_URL")
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
	delegationCert, err := requiredEnv("MV_GATEWAY_DELEGATION_CERT")
	if err != nil {
		return err
	}
	gatewayKey, err := identitycontext.LoadCertificatePublicKey(delegationCert)
	if err != nil {
		return err
	}
	handler, err := mv.NewHTTPHandler(service, mv.AuthConfig{
		GatewayPublicKey: gatewayKey,
		GatewaySPIFFE:    envOrDefault("MV_GATEWAY_SPIFFE", mv.DefaultGatewaySPIFFEID),
	})
	if err != nil {
		return err
	}
	queue, err := store.EvaluationQueue()
	if err != nil {
		return err
	}
	workerID := envOrDefault("MV_WORKER_ID", "mv-service:"+hostname())

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var bgWG sync.WaitGroup
	runSupervised(workerCtx, &bgWG, logger, metrics, "mv.worker", defaultPanicRestartDelay, func() {
		runMVWorkerLoop(workerCtx, defaultWorkerInterval, logger, func(runCtx context.Context) error {
			return mv.RunScheduledEvaluationsWithLogger(runCtx, queue, service, workerID, 8, logger)
		})
	})

	server := &http.Server{
		Addr:              envOrDefault("MV_HTTP_ADDR", ":19096"),
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
		Addr:              envOrDefault("MV_DIAGNOSTICS_ADDR", ":19101"),
		Handler:           mv.NewDiagnosticsHandler(telemetry),
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
		defer observability.Recover(logger, metrics, "mv.diagnostics_server", func(any) {
			select {
			case serveErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if serveErrVal := diagnostics.ListenAndServe(); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("diagnostics server: %w", serveErrVal)
		}
	}()
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, metrics, "mv.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		logger.Info("mv_service_started", "address", server.Addr, "diagnostics_address", diagnostics.Addr)
		if serveErrVal := server.ListenAndServeTLS("", ""); serveErrVal != nil && !errors.Is(serveErrVal, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server: %w", serveErrVal)
		}
	}()

	var runErr error
	select {
	case <-sigCtx.Done():
		logger.Info("mv_service_shutting_down")
	case err := <-serveErr:
		runErr = err
		stopSignals()
	}

	if shutdownErr := shutdownServiceRuntime(logger, telemetry, defaultShutdownTimeout, defaultBackgroundDrainTimeout, "mv.shutdown", &httpWG, cancelWorkers, &bgWG, shutdownTelemetry, server, diagnostics); shutdownErr != nil {
		logger.Warn("mv_server_shutdown_failed", "error_code", "MV_SHUTDOWN_FAILED", "error", shutdownErr)
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	logger.Info("mv_service_stopped")
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
			logger.Warn("mv_background_wait_timeout", "error_code", "MV_SHUTDOWN_DRAIN_TIMEOUT", "timeout", timeout.String())
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
