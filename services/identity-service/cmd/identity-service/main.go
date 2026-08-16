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
	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "identity-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 128, ExportTimeout: 500 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	server, err := identity.NewServer(ctx, identity.Config{
		Issuer:                os.Getenv("IDENTITY_ISSUER"),
		ClientID:              os.Getenv("IDENTITY_CLIENT_ID"),
		RedirectURI:           os.Getenv("IDENTITY_REDIRECT_URI"),
		PostLogoutRedirectURI: os.Getenv("IDENTITY_POST_LOGOUT_REDIRECT_URI"),
		DatabaseURL:           os.Getenv("IDENTITY_DATABASE_URL"),
		SigningKeyFile:        os.Getenv("IDENTITY_SIGNING_KEY_FILE"),
	})
	cancel()
	if err != nil {
		component := "configuration"
		switch {
		case strings.Contains(err.Error(), "identity database"):
			component = "database"
		case strings.Contains(err.Error(), "signing key"):
			component = "signing_key"
		}
		logger.Error("identity_configuration_invalid", "error_code", "IDENTITY_CONFIG_INVALID", "component", component, "error", err.Error())
		os.Exit(1)
	}
	defer server.Close()

	httpServer := &http.Server{
		Addr: envOr("IDENTITY_ADDR", ":19095"), Handler: server.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("IDENTITY_DIAGNOSTICS_ADDR", ":19085"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("identity_diagnostics_failed", "error_code", "IDENTITY_DIAGNOSTICS_FAILED")
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("identity_started", "service", "identity-service", "issuer", os.Getenv("IDENTITY_ISSUER"))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("identity_stopped_unexpectedly", "error_code", "IDENTITY_SERVE_FAILED", "error", err.Error())
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
