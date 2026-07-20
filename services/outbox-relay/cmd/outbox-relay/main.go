package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/outbox-relay/internal/relay"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
		Owner:  envOr("OUTBOX_RELAY_OWNER", "outbox-relay-01"),
		Logger: logger,
	})
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
