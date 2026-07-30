package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	clickhouseclient "github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/clickhouse"
	"github.com/quanlaihe/hvac-web/services/analytics-read-model-projector/internal/energy"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "analytics-read-model-projector", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 512, ExportTimeout: 500 * time.Millisecond,
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx)
	}()

	httpClient, err := newClickHouseHTTPClient(strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_CA")))
	if err != nil {
		logger.Error("analytics_clickhouse_ca_invalid", "error_code", "ANALYTICS_CLICKHOUSE_CA_INVALID")
		os.Exit(1)
	}
	baseURL := requiredEnv("ANALYTICS_CLICKHOUSE_HTTP_URL")
	reader, err := clickhouseclient.NewReader(clickhouseclient.ReaderConfig{
		BaseURL:           baseURL,
		SourceDatabase:    envOr("ANALYTICS_SOURCE_DATABASE", "telemetry_history"),
		SourceTable:       envOr("ANALYTICS_SOURCE_TABLE", "observations"),
		AnalyticsDatabase: envOr("ANALYTICS_DATABASE", "analytics"),
		AnalyticsTable:    envOr("ANALYTICS_ENERGY_TABLE", "energy_interval_facts"),
		Username:          strings.TrimSpace(os.Getenv("ANALYTICS_CLICKHOUSE_READER_USERNAME")),
		Password:          os.Getenv("ANALYTICS_CLICKHOUSE_READER_PASSWORD"),
		HTTPClient:        httpClient,
	})
	if err != nil {
		logger.Error("analytics_clickhouse_reader_invalid", "error_code", "ANALYTICS_CLICKHOUSE_READER_INVALID")
		os.Exit(1)
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
		os.Exit(1)
	}
	projector, err := energy.NewProjector(energy.ProjectorConfig{
		Source:    reader,
		Sink:      writer,
		BatchSize: integerEnv("ANALYTICS_PROJECTOR_BATCH_SIZE", 256, 1, 4096),
	})
	if err != nil {
		logger.Error("analytics_projector_configuration_invalid", "error_code", "ANALYTICS_PROJECTOR_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	pollInterval := durationEnv("ANALYTICS_PROJECTOR_POLL_INTERVAL", 500*time.Millisecond, 25*time.Millisecond, time.Minute)

	diagnostics := &http.Server{
		Addr:              envOr("ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR", "127.0.0.1:19089"),
		Handler:           telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("analytics_projector_diagnostics_failed", "error_code", "ANALYTICS_PROJECTOR_DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger.Info("analytics_read_model_projector_started", "poll_interval", pollInterval.String())

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			telemetry.MarkNotReady()
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = diagnostics.Shutdown(shutdownContext)
			shutdownCancel()
			logger.Info("analytics_read_model_projector_stopped")
			return
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
				_ = telemetry.Metrics.AddCounter("hvac_analytics_energy_intervals_projected_total", "Projected additive energy intervals.", map[string]string{"energy_type": energy.EnergyTypeElectricity}, float64(projected))
				logger.Info("analytics_energy_intervals_projected", "interval_count", projected)
			}
		}
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
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}

func integerEnv(name string, fallback, minimum, maximum int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
	}
	return parsed
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		_, _ = os.Stderr.WriteString(name + " is invalid\n")
		os.Exit(1)
	}
	return parsed
}
