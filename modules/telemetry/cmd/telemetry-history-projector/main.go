package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
)

func main() {
	os.Exit(run())
}

func run() int {
	observabilityRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-history-projector", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 512, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	var (
		repository      *telemetry.HistoryPostgresRepository
		sink            telemetry.HistorySinkCloser
		diagnostics     *http.Server
		diagnosticsDone chan struct{}
		shutdownOnce    sync.Once
	)
	runShutdown := func() {
		shutdownOnce.Do(func() {
			var deps historyProjectorShutdownDeps
			if diagnostics != nil {
				deps.markNotReady = observabilityRuntime.MarkNotReady
				deps.shutdownDiag = diagnostics.Shutdown
				deps.diagnosticsDone = diagnosticsDone
			}
			deps.shutdownTelemetry = observabilityRuntime.Shutdown
			if sink != nil {
				deps.closeSink = func() { _ = sink.Close() }
			}
			if repository != nil {
				deps.closeRepository = repository.Close
			}
			shutdownHistoryProjector(deps)
		})
	}
	defer runShutdown()

	databaseURL, err := requiredEnv("TELEMETRY_HISTORY_DATABASE_URL")
	if err != nil {
		logEnvError(logger, "telemetry_history_repository_open_failed", "TELEMETRY_HISTORY_REPOSITORY_OPEN_FAILED", err)
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err = telemetry.OpenHistoryPostgresRepository(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_history_repository_open_failed", "error_code", "TELEMETRY_HISTORY_REPOSITORY_OPEN_FAILED")
		return 1
	}

	sink, err = telemetry.OpenClickHouseHistorySinkFromLookup(os.Getenv, "")
	if err != nil {
		logEnvError(logger, "telemetry_clickhouse_configuration_invalid", "TELEMETRY_CLICKHOUSE_CONFIGURATION_INVALID", err)
		return 1
	}
	pollInterval, err := durationEnv("TELEMETRY_HISTORY_POLL_INTERVAL", 5*time.Second, 25*time.Millisecond, time.Minute)
	if err != nil {
		logEnvError(logger, "telemetry_history_relay_configuration_invalid", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID", err)
		return 1
	}
	batchSize, err := integerEnv("TELEMETRY_HISTORY_BATCH_SIZE", 4096, 1, 32768)
	if err != nil {
		logEnvError(logger, "telemetry_history_relay_configuration_invalid", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID", err)
		return 1
	}
	leaseFor, err := durationEnv("TELEMETRY_HISTORY_LEASE_DURATION", 30*time.Second, time.Second, 10*time.Minute)
	if err != nil {
		logEnvError(logger, "telemetry_history_relay_configuration_invalid", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID", err)
		return 1
	}
	retryAfter, err := durationEnv("TELEMETRY_HISTORY_RETRY_DELAY", 5*time.Second, time.Second, time.Hour)
	if err != nil {
		logEnvError(logger, "telemetry_history_relay_configuration_invalid", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID", err)
		return 1
	}
	relay, err := telemetry.NewHistoryRelay(telemetry.HistoryRelayConfig{
		Repository: repository,
		Sink:       sink,
		BatchSize:  batchSize,
		LeaseFor:   leaseFor,
		RetryAfter: retryAfter,
	})
	if err != nil {
		logger.Error("telemetry_history_relay_configuration_invalid", "error_code", "TELEMETRY_HISTORY_RELAY_CONFIGURATION_INVALID")
		return 1
	}

	diagnostics = &http.Server{
		Addr:              envOr("TELEMETRY_HISTORY_DIAGNOSTICS_ADDR", "127.0.0.1:19087"),
		Handler:           observabilityRuntime.DiagnosticsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	diagnosticsDone = make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, observabilityRuntime.Metrics, "telemetry_history_projector.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetry_history_diagnostics_stopped_unexpectedly", "error_code", "TELEMETRY_HISTORY_DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	observabilityRuntime.MarkReady()
	logger.Info("telemetry_history_projector_started", "poll_interval", pollInterval.String())

	lastFailureLog := time.Time{}
	relay.Run(ctx, pollInterval, func(published int, relayErr error) {
		if relayErr != nil {
			if time.Since(lastFailureLog) >= time.Second {
				logger.Warn("telemetry_history_projection_failed", "error_code", "TELEMETRY_HISTORY_PROJECTION_FAILED")
				lastFailureLog = time.Now()
			}
			return
		}
		if published > 0 {
			logger.Debug("telemetry_history_batch_projected", "observation_count", published)
		}
	})
	runShutdown()
	logger.Info("telemetry_history_projector_stopped")
	return 0
}

type historyProjectorShutdownDeps struct {
	markNotReady      func()
	shutdownDiag      func(context.Context) error
	diagnosticsDone   <-chan struct{}
	shutdownTelemetry func(context.Context) error
	closeSink         func()
	closeRepository   func()
}

func shutdownHistoryProjector(deps historyProjectorShutdownDeps) {
	if deps.markNotReady != nil {
		deps.markNotReady()
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
	if deps.closeSink != nil {
		deps.closeSink()
	}
	if deps.closeRepository != nil {
		deps.closeRepository()
	}
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
