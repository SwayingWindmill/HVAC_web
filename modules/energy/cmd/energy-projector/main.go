package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	clickhouseclient "github.com/quanlaihe/hvac-web/modules/energy/internal/clickhouse"
	"github.com/quanlaihe/hvac-web/modules/energy/internal/energy"
	"github.com/quanlaihe/hvac-web/modules/energy/pkg/analyticsprojector"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "analytics-read-model-projector", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 512, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = shutdownTelemetry(ctx)
	}()

	httpClient, err := newClickHouseHTTPClient(strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_CA")))
	if err != nil {
		logger.Error("analytics_clickhouse_ca_invalid", "error_code", "ANALYTICS_CLICKHOUSE_CA_INVALID")
		return 1
	}
	baseURL, err := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL")
	if err != nil {
		logEnvError(logger, "analytics_clickhouse_configuration_invalid", "ANALYTICS_CLICKHOUSE_CONFIGURATION_INVALID", err)
		return 1
	}
	reader, err := clickhouseclient.NewReader(clickhouseclient.ReaderConfig{
		BaseURL:           baseURL,
		SourceDatabase:    envOr("ANALYTICS_SOURCE_DATABASE", "telemetry_history"),
		AnalyticsDatabase: envOr("ANALYTICS_DATABASE", "analytics"),
		AnalyticsTable:    envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:          strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_READER_USERNAME")),
		Password:          os.Getenv("ANALYTICS_CLICKHOUSE_READER_PASSWORD"),
		HTTPClient:        httpClient,
	})
	if err != nil {
		logger.Error("analytics_clickhouse_reader_invalid", "error_code", "ANALYTICS_CLICKHOUSE_READER_INVALID")
		return 1
	}
	writer, err := clickhouseclient.NewWriter(clickhouseclient.WriterConfig{
		BaseURL:    baseURL,
		Database:   envOr("ANALYTICS_DATABASE", "analytics"),
		Table:      envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:   strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_WRITER_USERNAME")),
		Password:   os.Getenv("ANALYTICS_CLICKHOUSE_WRITER_PASSWORD"),
		HTTPClient: httpClient,
	})
	if err != nil {
		logger.Error("analytics_clickhouse_writer_invalid", "error_code", "ANALYTICS_CLICKHOUSE_WRITER_INVALID")
		return 1
	}
	internalCA, err := requiredEnv("ANALYTICS_INTERNAL_CA")
	if err != nil {
		logEnvError(logger, "analytics_internal_client_invalid", "ANALYTICS_INTERNAL_CLIENT_INVALID", err)
		return 1
	}
	internalCert, err := requiredEnv("ANALYTICS_INTERNAL_TLS_CERT")
	if err != nil {
		logEnvError(logger, "analytics_internal_client_invalid", "ANALYTICS_INTERNAL_CLIENT_INVALID", err)
		return 1
	}
	internalKey, err := requiredEnv("ANALYTICS_INTERNAL_TLS_KEY")
	if err != nil {
		logEnvError(logger, "analytics_internal_client_invalid", "ANALYTICS_INTERNAL_CLIENT_INVALID", err)
		return 1
	}
	internalClient, err := newInternalHTTPClient(internalCA, internalCert, internalKey)
	if err != nil {
		logger.Error("analytics_internal_client_invalid", "error_code", "ANALYTICS_INTERNAL_CLIENT_INVALID")
		return 1
	}
	coreRegistryURL, err := requiredEnv("ANALYTICS_CORE_REGISTRY_URL")
	if err != nil {
		logEnvError(logger, "analytics_core_registry_url_invalid", "ANALYTICS_CORE_REGISTRY_URL_INVALID", err)
		return 1
	}
	iamURL, err := requiredEnv("ANALYTICS_IAM_URL")
	if err != nil {
		logEnvError(logger, "analytics_iam_url_invalid", "ANALYTICS_IAM_URL_INVALID", err)
		return 1
	}
	registryConfig := analyticsprojector.RegistryClientConfig{
		BaseURL: coreRegistryURL, IAMURL: iamURL, HTTPClient: internalClient,
	}
	bindingResolver, err := analyticsprojector.NewBindingResolver(registryConfig)
	if err != nil {
		logger.Error("analytics_core_binding_resolver_invalid", "error_code", "ANALYTICS_CORE_BINDING_RESOLVER_INVALID")
		return 1
	}
	submeterSources, err := analyticsprojector.NewTotalSubmeterSource(registryConfig)
	if err != nil {
		logger.Error("analytics_core_submeter_source_invalid", "error_code", "ANALYTICS_CORE_SUBMETER_SOURCE_INVALID")
		return 1
	}
	totalSubmeters, err := analyticsprojector.NewTotalSubmeterProjector(analyticsprojector.TotalSubmeterProjectorConfig{
		Reader: reader, Writer: writer, Sources: submeterSources,
	})
	if err != nil {
		logger.Error("analytics_submeter_projector_invalid", "error_code", "ANALYTICS_SUBMETER_PROJECTOR_INVALID")
		return 1
	}
	batchSize, err := integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096)
	if err != nil {
		logEnvError(logger, "analytics_projector_configuration_invalid", "ANALYTICS_PROJECTOR_CONFIGURATION_INVALID", err)
		return 1
	}
	projector, err := energy.NewProjector(energy.ProjectorConfig{
		CounterSource: reader, BindingResolver: bindingResolver, FactSink: writer, RebuildEvents: writer,
		BatchSize: batchSize,
	})
	if err != nil {
		logger.Error("analytics_projector_configuration_invalid", "error_code", "ANALYTICS_PROJECTOR_CONFIGURATION_INVALID")
		return 1
	}
	pollInterval, err := durationEnv("ANALYTICS_PROJECTOR_POLL_INTERVAL", 500*time.Millisecond, 25*time.Millisecond, time.Minute)
	if err != nil {
		logEnvError(logger, "analytics_projector_configuration_invalid", "ANALYTICS_PROJECTOR_CONFIGURATION_INVALID", err)
		return 1
	}
	submeterInterval, err := durationEnv("ANALYTICS_TOTAL_SUBMETER_INTERVAL", 5*time.Minute, 10*time.Second, time.Hour)
	if err != nil {
		logEnvError(logger, "analytics_projector_configuration_invalid", "ANALYTICS_PROJECTOR_CONFIGURATION_INVALID", err)
		return 1
	}

	diagnostics := &http.Server{
		Addr:              envOr("ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR", "127.0.0.1:19089"),
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "energy_projector.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("analytics_projector_diagnostics_failed", "error_code", "ANALYTICS_PROJECTOR_DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger.Info("analytics_read_model_projector_started", "poll_interval", pollInterval.String())
	submeterDone := make(chan struct{})
	go func() {
		defer close(submeterDone)
		defer observability.Recover(logger, telemetry.Metrics, "energy_projector.submeter_sync_loop")
		submeterTicker := time.NewTicker(submeterInterval)
		defer submeterTicker.Stop()
		for {
			syncSubmetersOnce(ctx, totalSubmeters, telemetry, logger)
			select {
			case <-ctx.Done():
				return
			case <-submeterTicker.C:
			}
		}
	}()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			shutdownEnergyProjector(energyProjectorShutdownDeps{
				markNotReady:      telemetry.MarkNotReady,
				submeterDone:      submeterDone,
				shutdownDiag:      diagnostics.Shutdown,
				diagnosticsDone:   diagnosticsDone,
				shutdownTelemetry: shutdownTelemetry,
			})
			logger.Info("analytics_read_model_projector_stopped")
			return 0
		case <-ticker.C:
			projectionContext, span := telemetry.Tracer.Start(ctx, "analytics.energy_interval.project", observability.SpanKindInternal, map[string]any{
				"db.system":       "clickhouse",
				"analytics.model": "energy_interval_facts",
			})
			projectContext, projectCancel := context.WithTimeout(projectionContext, 20*time.Second)
			projected, projectErr := projector.ProjectOnce(projectContext)
			projectCancel()
			if projectErr != nil {
				telemetry.MarkNotReady()
				span.SetStatus("error", "projection failed")
				span.End()
				_ = telemetry.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "energy_interval_facts"}, 1)
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("analytics_energy_projection_failed", "error_code", "ANALYTICS_ENERGY_PROJECTION_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			telemetry.MarkReady()
			span.SetStatus("ok", "")
			span.End()
			if projected > 0 {
				_ = telemetry.Metrics.AddCounter("hvac_analytics_energy_intervals_projected_total", "Projected additive energy intervals.", map[string]string{"model": "energy_interval_facts"}, float64(projected))
				logger.Debug("analytics_energy_intervals_projected", "interval_count", projected)
			}
		}
	}
}

type energyProjectorShutdownDeps struct {
	markNotReady      func()
	submeterDone      <-chan struct{}
	shutdownDiag      func(context.Context) error
	diagnosticsDone   <-chan struct{}
	shutdownTelemetry func(context.Context) error
}

func shutdownEnergyProjector(deps energyProjectorShutdownDeps) {
	if deps.markNotReady != nil {
		deps.markNotReady()
	}
	if deps.submeterDone != nil {
		submeterCtx, submeterCancel := context.WithTimeout(context.Background(), 5*time.Second)
		select {
		case <-deps.submeterDone:
		case <-submeterCtx.Done():
		}
		submeterCancel()
	}
	if deps.shutdownDiag != nil || deps.diagnosticsDone != nil {
		diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if deps.shutdownDiag != nil {
			_ = deps.shutdownDiag(diagCtx)
		}
		if deps.diagnosticsDone != nil {
			select {
			case <-deps.diagnosticsDone:
			case <-diagCtx.Done():
			}
		}
		diagCancel()
	}
	if deps.shutdownTelemetry != nil {
		telemetryCtx, telemetryCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = deps.shutdownTelemetry(telemetryCtx)
		telemetryCancel()
	}
}

func syncSubmetersOnce(ctx context.Context, totalSubmeters *analyticsprojector.TotalSubmeterProjector, telemetry *observability.Runtime, logger *slog.Logger) {
	defer observability.Recover(logger, telemetry.Metrics, "energy_projector.submeter_sync")
	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	defer syncCancel()
	if syncErr := totalSubmeters.SyncOnce(syncCtx); syncErr != nil {
		_ = telemetry.Metrics.AddCounter("hvac_analytics_projection_failures_total", "Failed analytics read-model projection batches.", map[string]string{"model": "site_total_submeters"}, 1)
		logger.Warn("analytics_total_submeter_projection_failed", "error_code", "ANALYTICS_TOTAL_SUBMETER_PROJECTION_FAILED")
	}
}

func newClickHouseHTTPClient(caPath string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		content, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(content) {
			return nil, errors.New("ClickHouse CA pool is empty")
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: newClonedProjectorTransport(tlsConfig, false, 30*time.Second)}, nil
}

func newInternalHTTPClient(caPath, certPath, keyPath string) (*http.Client, error) {
	roots, err := loadCertPool(caPath)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     newClonedProjectorTransport(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}}, true, 15*time.Second),
	}, nil
}

func newClonedProjectorTransport(tlsConfig *tls.Config, disableCompression bool, responseHeaderTimeout time.Duration) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSClientConfig = tlsConfig
	transport.DisableCompression = disableCompression
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return transport
}

func loadCertPool(path string) (*x509.CertPool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, errors.New("internal CA pool is empty")
	}
	return pool, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func logEnvError(logger *slog.Logger, msg, code string, err error) {
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
	}
	if logger != nil {
		logger.Error(msg, "error_code", code)
	}
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func integerEnv(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}
