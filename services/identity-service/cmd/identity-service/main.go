package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "identity-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 128, ExportTimeout: 500 * time.Millisecond, Logger: logger,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	server, err := identity.NewServer(ctx, identity.Config{
		Issuer:                    os.Getenv("IDENTITY_ISSUER"),
		ClientID:                  os.Getenv("IDENTITY_CLIENT_ID"),
		RedirectURI:               os.Getenv("IDENTITY_REDIRECT_URI"),
		PostLogoutRedirectURI:     os.Getenv("IDENTITY_POST_LOGOUT_REDIRECT_URI"),
		DatabaseURL:               os.Getenv("IDENTITY_DATABASE_URL"),
		SigningKeyFile:            os.Getenv("IDENTITY_SIGNING_KEY_FILE"),
		MFAEncryptionKeyFile:      os.Getenv("IDENTITY_MFA_ENCRYPTION_KEY_FILE"),
		SchemaVersion:             envOr("PHASE1_PRODUCT_VERSION", "0.1.0"),
		WeChatAppID:               os.Getenv("IDENTITY_WECHAT_APP_ID"),
		WeChatSecretFile:          os.Getenv("IDENTITY_WECHAT_APPSECRET_FILE"),
		WeChatState:               envOr("IDENTITY_WECHAT_MINIPROGRAM_STATE", "formal"),
		GatewayDelegationCertFile: os.Getenv("IDENTITY_GATEWAY_DELEGATION_CERT"),
		GatewaySPIFFE:             envOr("IDENTITY_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		Logger:                    logger,
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
		return 1
	}
	defer server.Close()
	telemetry.SetDependencies(observability.Dependency{Name: "postgres", Required: true, Check: server.Ping})

	httpServer := &http.Server{
		Addr: envOr("IDENTITY_ADDR", ":19095"), Handler: server.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("IDENTITY_DIAGNOSTICS_ADDR", ":19085"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 2)
	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, telemetry.Metrics, "identity.diagnostics_server", func(any) {
			select {
			case serveErr <- errors.New("diagnostics server panicked"):
			default:
			}
		})
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("identity_diagnostics_failed", "error_code", "IDENTITY_DIAGNOSTICS_FAILED")
			select {
			case serveErr <- err:
			default:
			}
		}
	}()

	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		defer observability.Recover(logger, telemetry.Metrics, "identity.http_server", func(any) {
			select {
			case serveErr <- errors.New("http server panicked"):
			default:
			}
		})
		telemetry.MarkReady()
		logger.Info("identity_started", "service", "identity-service", "issuer", os.Getenv("IDENTITY_ISSUER"))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("identity_stopped_unexpectedly", "error_code", "IDENTITY_SERVE_FAILED", "error", err.Error())
			select {
			case serveErr <- err:
			default:
			}
		}
	}()

	return drainAndShutdown(sigCtx, serveErr, logger, telemetry, httpServer, diagnostics, &httpWG, 10*time.Second)
}

func drainAndShutdown(
	sigCtx context.Context,
	serveErr <-chan error,
	logger *slog.Logger,
	telemetry *observability.Runtime,
	httpServer *http.Server,
	diagnostics *http.Server,
	httpWG *sync.WaitGroup,
	shutdownTimeout time.Duration,
) int {
	exitCode := 0
	select {
	case <-sigCtx.Done():
		logger.Info("identity_shutting_down", "service", "identity-service")
	case <-serveErr:
		exitCode = 1
	}

	telemetry.MarkNotReady()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "identity.shutdown")
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelDrain()
		for _, srv := range []*http.Server{httpServer, diagnostics} {
			if srv == nil {
				continue
			}
			if err := srv.Shutdown(drainCtx); err != nil {
				logger.Warn("identity_http_shutdown_failed", "error_code", "IDENTITY_SHUTDOWN_FAILED", "cause", err.Error())
				_ = srv.Close()
			}
		}
		if httpWG != nil {
			httpWG.Wait()
		}
	}()
	<-shutdownDone

	obsCtx, cancelObs := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelObs()
	if err := telemetry.Shutdown(obsCtx); err != nil {
		logger.Warn("identity_telemetry_shutdown_failed", "error_code", "TELEMETRY_SHUTDOWN_FAILED", "cause", err.Error())
	}
	logger.Info("identity_stopped", "service", "identity-service")
	return exitCode
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
