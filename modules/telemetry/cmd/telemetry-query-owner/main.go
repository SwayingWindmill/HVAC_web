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

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/cube"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/history"
	"github.com/quanlaihe/hvac-web/modules/telemetry/internal/query"
)

func main() {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-query-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	certificate, err := tls.LoadX509KeyPair(requiredEnv("QUERY_TLS_CERT"), requiredEnv("QUERY_TLS_KEY"))
	if err != nil {
		logger.Error("query_tls_identity_load_failed", "error_code", "QUERY_TLS_IDENTITY_LOAD_FAILED")
		os.Exit(1)
	}
	clientCAs, err := loadCertPool(requiredEnv("QUERY_CLIENT_CA"))
	if err != nil {
		logger.Error("query_client_ca_invalid", "error_code", "QUERY_CLIENT_CA_INVALID")
		os.Exit(1)
	}
	delegationPublicKey, err := loadCertificatePublicKey(requiredEnv("QUERY_GATEWAY_DELEGATION_CERT"))
	if err != nil {
		logger.Error("query_gateway_delegation_certificate_invalid", "error_code", "QUERY_GATEWAY_DELEGATION_CERTIFICATE_INVALID")
		os.Exit(1)
	}
	tokenFactory, err := cube.NewHMACTokenFactory([]byte(requiredEnv("QUERY_CUBE_API_SECRET")), time.Now)
	if err != nil {
		logger.Error("query_cube_token_factory_invalid", "error_code", "QUERY_CUBE_TOKEN_FACTORY_INVALID")
		os.Exit(1)
	}
	cubeHTTPClient, err := newCubeHTTPClient(strings.TrimSpace(os.Getenv("QUERY_CUBE_CA")))
	if err != nil {
		logger.Error("query_cube_ca_invalid", "error_code", "QUERY_CUBE_CA_INVALID")
		os.Exit(1)
	}
	cubeClient, err := cube.NewClient(cube.Config{
		BaseURL: requiredEnv("QUERY_CUBE_ENDPOINT"), DatasetRevision: requiredEnv("QUERY_DATASET_REVISION"),
		TokenFactory: tokenFactory, HTTPClient: cubeHTTPClient,
	})
	if err != nil {
		logger.Error("query_cube_client_invalid", "error_code", "QUERY_CUBE_CLIENT_INVALID")
		os.Exit(1)
	}
	historyHTTPClient, err := newCubeHTTPClient(strings.TrimSpace(os.Getenv("QUERY_HISTORY_CLICKHOUSE_CA")))
	if err != nil {
		logger.Error("query_history_clickhouse_ca_invalid", "error_code", "QUERY_HISTORY_CLICKHOUSE_CA_INVALID")
		os.Exit(1)
	}
	historyClient, err := history.NewClient(history.Config{
		BaseURL: requiredEnv("QUERY_HISTORY_CLICKHOUSE_ENDPOINT"), Database: envOr("QUERY_HISTORY_CLICKHOUSE_DATABASE", "telemetry_history"),
		Table: envOr("QUERY_HISTORY_CLICKHOUSE_TABLE", "observations"), Username: envOr("QUERY_HISTORY_CLICKHOUSE_USERNAME", "telemetry_query_history_reader"),
		Password: os.Getenv("QUERY_HISTORY_CLICKHOUSE_PASSWORD"), HTTPClient: historyHTTPClient,
	})
	if err != nil {
		logger.Error("query_history_clickhouse_client_invalid", "error_code", "QUERY_HISTORY_CLICKHOUSE_CLIENT_INVALID")
		os.Exit(1)
	}

	server := &http.Server{
		Addr: envOr("QUERY_SERVICE_ADDR", "127.0.0.1:18447"),
		Handler: query.NewHandler(query.ServerConfig{
			Engine:                 cubeClient,
			HistoryEngine:          historyClient,
			DelegationPublicKey:    delegationPublicKey,
			DelegationIssuerSPIFFE: envOr("QUERY_DELEGATION_ISSUER_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AllowedPresenterSPIFFE: envOr("QUERY_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			AdditionalAllowedPresenterSPIFFEs: []string{
				envOr("QUERY_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
			},
			Audience:      envOr("QUERY_AUDIENCE", "telemetry-query-service"),
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
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	diagnostics := &http.Server{
		Addr: envOr("QUERY_DIAGNOSTICS_ADDR", "127.0.0.1:19088"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("query_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
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
	logger.Info("query_started", "service", "telemetry-query-service", "address", server.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("query_stopped_unexpectedly", "error_code", "QUERY_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("query_stopped", "service", "telemetry-query-service")
}

func newCubeHTTPClient(caPath string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath != "" {
		roots, err := loadCertPool(caPath)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = roots
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}, nil
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
		return nil, errors.New("delegation certificate is invalid")
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
