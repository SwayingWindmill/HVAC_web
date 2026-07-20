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

	"github.com/quanlaihe/hvac-web/libs/oidctest"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	address := envOr("OIDC_FIXTURE_ADDR", "127.0.0.1:19090")
	issuer := envOr("OIDC_FIXTURE_ISSUER", "http://127.0.0.1:19090")
	provider, err := oidctest.New(oidctest.Config{
		Issuer:      issuer,
		ClientID:    envOr("OIDC_FIXTURE_CLIENT_ID", "hvac-web-s0"),
		RedirectURI: envOr("OIDC_FIXTURE_REDIRECT_URI", "https://127.0.0.1:5179/api/v1/auth/callback"),
	})
	if err != nil {
		logger.Error("oidc_fixture_config_invalid", "error", err)
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
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	logger.Info("oidc_fixture_started", "address", address, "issuer", issuer)
	var serveErr error
	if certificatePath, keyPath := os.Getenv("OIDC_FIXTURE_TLS_CERT"), os.Getenv("OIDC_FIXTURE_TLS_KEY"); certificatePath != "" && keyPath != "" {
		serveErr = server.ListenAndServeTLS(certificatePath, keyPath)
	} else {
		serveErr = server.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("oidc_fixture_stopped_unexpectedly", "error", serveErr)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
