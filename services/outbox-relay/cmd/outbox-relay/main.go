package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/outbox-relay/internal/relay"
)

func main() {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "outbox-relay", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := sessionstore.OpenOutbox(ctx, required("OUTBOX_DATABASE_URL"))
	if err != nil {
		logger.Error("outbox_store_open_failed", "error_code", "OUTBOX_DATABASE_UNAVAILABLE")
		os.Exit(1)
	}
	defer store.Close()
	publisher := relay.NewKafkaPublisher(splitCSV(required("CONTROL_BACKBONE_BROKERS")))
	defer publisher.Close()
	worker := relay.New(store, publisher, relay.Config{
		Owner: envOr("OUTBOX_RELAY_OWNER", "outbox-relay-01"), Logger: logger, Observability: telemetry,
	})
	diagnostics := &http.Server{
		Addr: envOr("OUTBOX_RELAY_DIAGNOSTICS_ADDR", "127.0.0.1:19081"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("outbox_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()
	defer func() {
		telemetry.MarkNotReady()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = diagnostics.Shutdown(shutdownContext)
		_ = telemetry.Shutdown(shutdownContext)
	}()
	telemetry.MarkReady()
	logger.Info("outbox_relay_started", "service", "outbox-relay")
	if err := worker.Run(ctx); err != nil {
		logger.Error("outbox_relay_stopped", "error_code", "OUTBOX_RELAY_FAILED")
		os.Exit(1)
	}
	logger.Info("outbox_relay_stopped", "service", "outbox-relay")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
