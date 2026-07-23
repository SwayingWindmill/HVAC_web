package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "platform-gateway", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	address := envOr("PLATFORM_GATEWAY_ADDR", ":8080")
	runContext, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	identity, closeIdentity, err := loadIdentityConfig(runContext)
	if err != nil {
		logger.Error("gateway_identity_config_invalid", "error_code", "IDENTITY_CONFIG_INVALID")
		os.Exit(1)
	}
	defer closeIdentity()

	routing, err := loadRoutingRuntime(runContext, logger, identity != nil)
	if err != nil {
		logger.Error("gateway_route_config_invalid", "error_code", "ROUTE_CONFIG_INVALID")
		os.Exit(1)
	}
	defer routing.close()
	go routing.watch(runContext)

	handler := gateway.NewHandler(gateway.Config{
		Logger:        logger,
		Identity:      identity,
		RouteManager:  routing.manager,
		RouteAudit:    routing.audit,
		Legacy:        routing.legacy,
		Registry:      routing.registry,
		Observability: telemetry,
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
	diagnostics := &http.Server{
		Addr: envOr("PLATFORM_GATEWAY_DIAGNOSTICS_ADDR", "127.0.0.1:19080"), Handler: telemetry.DiagnosticsHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("gateway_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdownSignal
		telemetry.MarkNotReady()
		cancelRun()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("gateway_shutdown_failed", "error_code", "GATEWAY_SHUTDOWN_FAILED")
		}
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("gateway_started", "service", "platform-gateway", "address", address, "version", version, "commit", commit, "identity_enabled", identity != nil)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway_stopped_unexpectedly", "error_code", "GATEWAY_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("gateway_stopped", "service", "platform-gateway")
}

func loadIdentityConfig(ctx context.Context) (*gateway.IdentityConfig, func(), error) {
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return nil, func() {}, nil
	}
	required := map[string]string{}
	for _, name := range []string{"OIDC_CLIENT_ID", "OIDC_REDIRECT_URI", "PLATFORM_PUBLIC_ORIGIN", "IAM_URL", "IAM_CLIENT_CERT", "IAM_CLIENT_KEY", "IAM_SERVER_CA"} {
		value := os.Getenv(name)
		if value == "" {
			return nil, func() {}, fmt.Errorf("%s is required when OIDC_ISSUER is configured", name)
		}
		required[name] = value
	}
	certificate, err := tls.LoadX509KeyPair(required["IAM_CLIENT_CERT"], required["IAM_CLIENT_KEY"])
	if err != nil {
		return nil, func() {}, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, func() {}, errors.New("Gateway workload private key cannot sign delegation grants")
	}
	iamRoots, err := loadCertPool(required["IAM_SERVER_CA"], "IAM server CA")
	if err != nil {
		return nil, func() {}, err
	}
	oidcClient := &http.Client{Timeout: 5 * time.Second}
	if oidcCAPath := os.Getenv("OIDC_SERVER_CA"); oidcCAPath != "" {
		oidcRoots, err := loadCertPool(oidcCAPath, "OIDC server CA")
		if err != nil {
			return nil, func() {}, err
		}
		oidcClient.Transport = workloadTransport(oidcRoots, nil, envOr("OIDC_SERVER_NAME", "localhost"))
	}

	var auditClient *http.Client
	auditURL := os.Getenv("AUDIT_URL")
	if auditURL == "" {
		if os.Getenv("S0_ALLOW_NO_AUDIT_LEDGER") != "true" {
			return nil, func() {}, errors.New("AUDIT_URL is required unless S0_ALLOW_NO_AUDIT_LEDGER=true")
		}
	} else {
		auditCAPath := os.Getenv("AUDIT_SERVER_CA")
		if auditCAPath == "" {
			return nil, func() {}, errors.New("AUDIT_SERVER_CA is required when AUDIT_URL is configured")
		}
		auditRoots, err := loadCertPool(auditCAPath, "Audit server CA")
		if err != nil {
			return nil, func() {}, err
		}
		auditClient = &http.Client{Timeout: 5 * time.Second, Transport: workloadTransport(auditRoots, &certificate, envOr("AUDIT_SERVER_NAME", "localhost"))}
	}

	var store sessionstore.Store
	closeStore := func() {}
	if dsn := os.Getenv("GATEWAY_DATABASE_URL"); dsn != "" {
		postgresStore, err := sessionstore.OpenPostgres(ctx, dsn, sessionstore.PostgresConfig{})
		if err != nil {
			return nil, func() {}, errors.New("durable Session store is unavailable")
		}
		store = postgresStore
		closeStore = postgresStore.Close
	} else if os.Getenv("S0_ALLOW_MEMORY_SESSION_STORE") == "true" {
		store = sessionstore.NewMemoryStore()
	} else {
		return nil, func() {}, errors.New("GATEWAY_DATABASE_URL is required unless S0_ALLOW_MEMORY_SESSION_STORE=true")
	}

	key, err := sessionEncryptionKey()
	if err != nil {
		closeStore()
		return nil, func() {}, err
	}
	return &gateway.IdentityConfig{
		OIDCIssuer:              issuer,
		OIDCClientID:            required["OIDC_CLIENT_ID"],
		OIDCRedirectURI:         required["OIDC_REDIRECT_URI"],
		PublicOrigin:            required["PLATFORM_PUBLIC_ORIGIN"],
		IAMURL:                  required["IAM_URL"],
		IAMAudience:             envOr("IAM_AUDIENCE", "iam-service"),
		AuditURL:                auditURL,
		AuditAudience:           envOr("AUDIT_AUDIENCE", "audit-ledger-service"),
		ExecutingWorkloadSPIFFE: envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		PolicyRevision:          envOr("IDENTITY_POLICY_REVISION", "policy-v1"),
		DelegationSigner:        signer,
		TokenEncryptionKey:      key,
		SessionStore:            store,
		SessionTTL:              30 * time.Minute,
		StateTTL:                2 * time.Minute,
		DelegationTTL:           30 * time.Second,
		RevocationObjective:     time.Second,
		IAMHTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: workloadTransport(iamRoots, &certificate, envOr("IAM_SERVER_NAME", "localhost")),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		AuditHTTPClient: auditClient,
		OIDCHTTPClient:  oidcClient,
	}, closeStore, nil
}

func workloadTransport(roots *x509.CertPool, certificate *tls.Certificate, serverName string) *http.Transport {
	config := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: serverName}
	if certificate != nil {
		config.Certificates = []tls.Certificate{*certificate}
	}
	return &http.Transport{TLSClientConfig: config}
}

func loadCertPool(path, label string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s is invalid", label)
	}
	return pool, nil
}

func sessionEncryptionKey() ([]byte, error) {
	if encoded := os.Getenv("SESSION_TOKEN_KEY"); encoded != "" {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, errors.New("SESSION_TOKEN_KEY must be base64url-encoded 32 bytes")
		}
		return key, nil
	}
	if os.Getenv("S0_ALLOW_EPHEMERAL_SESSION_KEY") != "true" {
		return nil, errors.New("SESSION_TOKEN_KEY is required unless S0_ALLOW_EPHEMERAL_SESSION_KEY=true")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
