package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/platform-core-service/internal/core"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "platform-core-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	certificate, err := tls.LoadX509KeyPair(requiredEnv("CORE_TLS_CERT"), requiredEnv("CORE_TLS_KEY"))
	if err != nil {
		logger.Error("core_tls_identity_load_failed", "error_code", "CORE_TLS_IDENTITY_LOAD_FAILED")
		os.Exit(1)
	}
	clientCAs, err := loadCertPool(requiredEnv("CORE_CLIENT_CA"))
	if err != nil {
		logger.Error("core_client_ca_invalid", "error_code", "CORE_CLIENT_CA_INVALID")
		os.Exit(1)
	}
	iamCAs, err := loadCertPool(requiredEnv("CORE_IAM_CA"))
	if err != nil {
		logger.Error("core_iam_ca_invalid", "error_code", "CORE_IAM_CA_INVALID")
		os.Exit(1)
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(requiredEnv("CORE_IAM_GRANT_CERT"))
	if err != nil {
		logger.Error("core_iam_grant_certificate_invalid", "error_code", "CORE_IAM_GRANT_CERTIFICATE_INVALID")
		os.Exit(1)
	}
	cursorKey, err := base64.RawURLEncoding.DecodeString(requiredEnv("CORE_CURSOR_HMAC_KEY"))
	if err != nil {
		logger.Error("core_cursor_key_invalid", "error_code", "CORE_CURSOR_KEY_INVALID")
		os.Exit(1)
	}
	cursorCodec, err := core.NewCursorCodec(cursorKey)
	if err != nil {
		logger.Error("core_cursor_key_invalid", "error_code", "CORE_CURSOR_KEY_INVALID")
		os.Exit(1)
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err := core.OpenPostgresStore(openContext, requiredEnv("CORE_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		logger.Error("core_registry_store_open_failed", "error_code", "CORE_REGISTRY_STORE_OPEN_FAILED")
		os.Exit(1)
	}
	defer store.Close()

	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      iamCAs,
			Certificates: []tls.Certificate{certificate},
		}},
	}
	grantStatus, err := core.NewHTTPGrantStatusProvider(requiredEnv("CORE_IAM_ENDPOINT"), iamClient)
	if err != nil {
		logger.Error("core_iam_status_client_invalid", "error_code", "CORE_IAM_STATUS_CLIENT_INVALID")
		os.Exit(1)
	}

	server := &http.Server{
		Addr: envOr("CORE_SERVICE_ADDR", "127.0.0.1:18445"),
		Handler: core.NewHandler(core.ServerConfig{
			Store:                  store,
			Writer:                 store,
			CursorCodec:            cursorCodec,
			GrantPublicKey:         iamGrantPublicKey,
			GrantIssuer:            envOr("CORE_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
			AllowedPresenterSPIFFE: envOr("CORE_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("CORE_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
				envOr("CORE_ANALYTICS_PROJECTOR_SPIFFE", "spiffe://hvac.local/analytics-read-model-projector"),
			},
			Audience:      envOr("CORE_AUDIENCE", "platform-core-service"),
			GrantStatus:   grantStatus,
			Logger:        logger,
			Observability: telemetry,
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
		Addr: envOr("CORE_DIAGNOSTICS_ADDR", "127.0.0.1:19084"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("core_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("core_started", "service", "platform-core-service", "address", server.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("core_stopped_unexpectedly", "error_code", "CORE_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("core_stopped", "service", "platform-core-service")
}

func loadCertPool(path string) (*x509.CertPool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, errors.New("certificate pool is empty")
	}
	return pool, nil
}

func loadCertificatePublicKey(path string) (any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("IAM grant certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate.PublicKey, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}
