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
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/services/audit-ledger-service/internal/audit"
)

func main() {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "audit-ledger-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := audit.OpenStore(ctx, required("AUDIT_CONSUMER_DATABASE_URL"), required("AUDIT_QUERY_DATABASE_URL"))
	if err != nil {
		logger.Error("audit_store_open_failed", "error_code", "AUDIT_DATABASE_UNAVAILABLE")
		os.Exit(1)
	}
	defer store.Close()

	consumer := audit.NewConsumer(store, audit.ConsumerConfig{
		Brokers:       splitCSV(required("CONTROL_BACKBONE_BROKERS")),
		Topic:         envOr("AUDIT_TOPIC", sessionevent.ControlTopic),
		GroupID:       envOr("AUDIT_CONSUMER_GROUP", "audit-ledger-session-v1"),
		Logger:        logger,
		Observability: telemetry,
	})
	defer consumer.Close()
	consumerErrors := make(chan error, 1)
	go func() { consumerErrors <- consumer.Run(ctx) }()

	clientCAPEM, err := os.ReadFile(required("AUDIT_CLIENT_CA"))
	if err != nil {
		logger.Error("audit_client_ca_read_failed", "error_code", "AUDIT_TLS_CONFIG_INVALID")
		os.Exit(1)
	}
	clientPool := x509.NewCertPool()
	if !clientPool.AppendCertsFromPEM(clientCAPEM) {
		logger.Error("audit_client_ca_invalid", "error_code", "AUDIT_TLS_CONFIG_INVALID")
		os.Exit(1)
	}
	server := &http.Server{
		Addr: envOr("AUDIT_SERVICE_ADDR", "127.0.0.1:18445"),
		Handler: audit.NewHandler(audit.ServerConfig{
			Store:                 store,
			AllowedWorkloadSPIFFE: envOr("AUDIT_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			Audience:              envOr("AUDIT_AUDIENCE", "audit-ledger-service"),
			Logger:                logger,
			Observability:         telemetry,
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			ClientCAs:  clientPool,
			ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("AUDIT_DIAGNOSTICS_ADDR", "127.0.0.1:19082"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("audit_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("audit_ledger_started", "service", "audit-ledger-service", "address", server.Addr)
		err := server.ListenAndServeTLS(required("AUDIT_TLS_CERT"), required("AUDIT_TLS_KEY"))
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	telemetry.MarkReady()
	select {
	case <-ctx.Done():
	case err := <-consumerErrors:
		if err != nil {
			logger.Error("audit_consumer_stopped", "error_code", "AUDIT_CONSUMER_FAILED")
		}
		cancel()
	case err := <-serverErrors:
		if err != nil {
			logger.Error("audit_server_stopped", "error_code", "AUDIT_SERVER_FAILED")
		}
		cancel()
	}
	telemetry.MarkNotReady()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownContext)
	_ = diagnostics.Shutdown(shutdownContext)
	_ = telemetry.Shutdown(shutdownContext)
	logger.Info("audit_ledger_stopped", "service", "audit-ledger-service")
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
