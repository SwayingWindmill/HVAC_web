package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	address := os.Getenv("PLATFORM_GATEWAY_ADDR")
	if address == "" {
		address = ":8080"
	}

	handler := gateway.NewHandler(gateway.Config{
		Logger: logger,
		Build: platformapi.BuildInfo{
			Service: "platform-gateway",
			Version: version,
			Commit:  commit,
			BuiltAt: builtAt,
		},
	})

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdownSignal
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("gateway_shutdown_failed", "error", err)
		}
	}()

	logger.Info("gateway_started", "service", "platform-gateway", "address", address, "version", version, "commit", commit)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway_stopped_unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway_stopped", "service", "platform-gateway")
}
