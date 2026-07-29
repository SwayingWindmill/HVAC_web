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

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/oidctest"
)

func main() {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "oidc-test-provider", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 128, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	address := envOr("OIDC_FIXTURE_ADDR", "127.0.0.1:19090")
	issuer := envOr("OIDC_FIXTURE_ISSUER", "http://127.0.0.1:19090")
	provider, err := oidctest.New(oidctest.Config{
		Issuer:                      issuer,
		ClientID:                    envOr("OIDC_FIXTURE_CLIENT_ID", "hvac-web-s0"),
		RedirectURI:                 envOr("OIDC_FIXTURE_REDIRECT_URI", "https://127.0.0.1:5179/api/v1/auth/callback"),
		DefaultActingOrganizationID: envOr("OIDC_FIXTURE_ACTING_ORGANIZATION_ID", "org-fixture-01"),
	})
	if err != nil {
		logger.Error("oidc_fixture_config_invalid", "error_code", "OIDC_FIXTURE_CONFIG_INVALID")
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              address,
		Handler:           provider,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("OIDC_FIXTURE_DIAGNOSTICS_ADDR", "127.0.0.1:19084"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("oidc_fixture_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("oidc_fixture_started", "service", "oidc-test-provider", "address", address, "issuer", issuer)
	var serveErr error
	if certificatePath, keyPath := os.Getenv("OIDC_FIXTURE_TLS_CERT"), os.Getenv("OIDC_FIXTURE_TLS_KEY"); certificatePath != "" && keyPath != "" {
		serveErr = server.ListenAndServeTLS(certificatePath, keyPath)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("oidc_fixture_stopped_unexpectedly", "error_code", "OIDC_FIXTURE_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("oidc_fixture_stopped", "service", "oidc-test-provider")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
