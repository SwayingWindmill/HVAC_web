package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/internal/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	certificate, err := tls.LoadX509KeyPair(requiredEnv("TELEMETRY_TLS_CERT"), requiredEnv("TELEMETRY_TLS_KEY"))
	if err != nil {
		logger.Error("telemetry_tls_identity_load_failed", "error_code", "TELEMETRY_TLS_IDENTITY_LOAD_FAILED")
		os.Exit(1)
	}
	clientCAs, err := loadCertPool(requiredEnv("TELEMETRY_CLIENT_CA"))
	if err != nil {
		logger.Error("telemetry_client_ca_invalid", "error_code", "TELEMETRY_CLIENT_CA_INVALID")
		os.Exit(1)
	}
	iamCAs, err := loadCertPool(requiredEnv("TELEMETRY_IAM_CA"))
	if err != nil {
		logger.Error("telemetry_iam_ca_invalid", "error_code", "TELEMETRY_IAM_CA_INVALID")
		os.Exit(1)
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(requiredEnv("TELEMETRY_IAM_GRANT_CERT"))
	if err != nil {
		logger.Error("telemetry_iam_grant_certificate_invalid", "error_code", "TELEMETRY_IAM_GRANT_CERTIFICATE_INVALID")
		os.Exit(1)
	}

	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err := telemetry.OpenPostgresStore(openContext, requiredEnv("TELEMETRY_DATABASE_URL"))
	cancelOpen()
	if err != nil {
		logger.Error("telemetry_store_open_failed", "error_code", "TELEMETRY_STORE_OPEN_FAILED")
		os.Exit(1)
	}
	defer store.Close()

	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, RootCAs: iamCAs, Certificates: []tls.Certificate{certificate},
			},
			DisableCompression: true,
			ForceAttemptHTTP2:  false,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	authorizer, err := telemetry.NewHTTPGrantAuthorizer(
		requiredEnv("TELEMETRY_IAM_ENDPOINT"), iamClient, iamGrantPublicKey,
		envOr("TELEMETRY_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
		envOr("TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
	)
	if err != nil {
		logger.Error("telemetry_iam_authorizer_invalid", "error_code", "TELEMETRY_IAM_AUTHORIZER_INVALID")
		os.Exit(1)
	}

	server := &http.Server{
		Addr: envOr("TELEMETRY_SERVICE_ADDR", "127.0.0.1:18446"),
		Handler: telemetry.NewHandler(telemetry.ServerConfig{
			Store: store, Authorizer: authorizer,
			AllowedGatewaySPIFFE: envOr("TELEMETRY_ALLOWED_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		}),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
			ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
		},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(context)
	}()

	logger.Info("telemetry_runtime_started", "service", "telemetry-runtime-service", "address", server.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("telemetry_runtime_stopped_unexpectedly", "error_code", "TELEMETRY_RUNTIME_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("telemetry_runtime_stopped", "service", "telemetry-runtime-service")
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
