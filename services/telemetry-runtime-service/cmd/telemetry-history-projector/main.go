package main

import (
	"context"
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
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/internal/telemetry"
)

func main() {
	observabilityRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-history-projector", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 512, ExportTimeout: 500 * time.Millisecond,
	})
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = observabilityRuntime.Shutdown(shutdownContext)
	}()
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)

	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err := telemetry.OpenHistoryPostgresRepository(openContext, requiredEnv("TELEMETRY_HISTORY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_history_repository_open_failed", "error_code", "TELEMETRY_HISTORY_REPOSITORY_OPEN_FAILED")
		os.Exit(1)
	}
	defer repository.Close()

	sink, err := telemetry.NewClickHouseHistorySink(telemetry.ClickHouseHistoryConfig{
		BaseURL:  requiredEnv("TELEMETRY_CLICKHOUSE_HTTP_URL"),
		Database: envOr("TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table:    envOr("TELEMETRY_CLICKHOUSE_TABLE", "observations"),
		Username: strings.TrimSpace(os.Getenv("TELEMETRY_CLICKHOUSE_USERNAME")),
		Password: os.Getenv("TELEMETRY_CLICKHOUSE_PASSWORD"),
	})
	if err != nil {
		logger.Error("telemetry_clickhouse_configuration_invalid", "error_code", "TELEMETRY_CLICKHOUSE_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	pollInterval := durationEnv("TELEMETRY_HISTORY_POLL_INTERVAL", 250*time.Millisecond, 25*time.Millisecond, time.Minute)
	relay, err := telemetry.NewHistoryRelay(telemetry.HistoryRelayConfig{
		Repository:  repository,
		Sink:        sink,
		BatchSize:   integerEnv("TELEMETRY_HISTORY_BATCH_SIZE", 256, 1, 4096),
		LeaseFor:    durationEnv("TELEMETRY_HISTORY_LEASE_DURATION", 30*time.Second, time.Second, 10*time.Minute),
		RetryAfter:  durationEnv("TELEMETRY_HISTORY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour),
		MaxAttempts: integerEnv("TELEMETRY_HISTORY_MAX_ATTEMPTS", 12, 1, 100),
	})
	if err != nil {
		logger.Error("telemetry_history_relay_configuration_invalid", "error_code", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID")
		os.Exit(1)
	}

	diagnostics := &http.Server{
		Addr:              envOr("TELEMETRY_HISTORY_DIAGNOSTICS_ADDR", "127.0.0.1:19087"),
		Handler:           observabilityRuntime.DiagnosticsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetry_history_diagnostics_stopped_unexpectedly", "error_code", "TELEMETRY_HISTORY_DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	observabilityRuntime.MarkReady()
	logger.Info("telemetry_history_projector_started", "poll_interval", pollInterval.String())

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			observabilityRuntime.MarkNotReady()
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = diagnostics.Shutdown(shutdownContext)
			shutdownCancel()
			logger.Info("telemetry_history_projector_stopped")
			return
		case <-ticker.C:
			relayContext, relayCancel := context.WithTimeout(ctx, 15*time.Second)
			published, relayErr := relay.RelayOnce(relayContext)
			relayCancel()
			if relayErr != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_history_projection_failed", "error_code", "TELEMETRY_HISTORY_PROJECTION_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if published > 0 {
				logger.Info("telemetry_history_batch_projected", "observation_count", published)
			}
		}
	}
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
