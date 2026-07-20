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
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func main() {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "iam-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	address := envOr("IAM_SERVICE_ADDR", "127.0.0.1:18444")
	certificate, err := tls.LoadX509KeyPair(requiredEnv("IAM_TLS_CERT"), requiredEnv("IAM_TLS_KEY"))
	if err != nil {
		logger.Error("iam_tls_identity_load_failed", "error", err)
		os.Exit(1)
	}
	caPEM, err := os.ReadFile(requiredEnv("IAM_CLIENT_CA"))
	if err != nil {
		logger.Error("iam_client_ca_load_failed", "error", err)
		os.Exit(1)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		logger.Error("iam_client_ca_invalid")
		os.Exit(1)
	}
	server := &http.Server{
		Addr: address,
		Handler: iam.NewHandler(iam.Config{
			AllowedWorkloadSPIFFE: envOr("IAM_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			Audience:              envOr("IAM_AUDIENCE", "iam-service"),
			Logger:                logger,
			Observability:         telemetry,
		}),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
			ClientCAs:    clientCAs,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("IAM_DIAGNOSTICS_ADDR", "127.0.0.1:19083"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("iam_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	logger.Info("iam_started", "service", "iam-service", "address", address)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("iam_stopped_unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("iam_stopped", "service", "iam-service")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}
