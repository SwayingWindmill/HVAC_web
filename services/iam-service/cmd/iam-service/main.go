package main

import (
	"context"
	"crypto"
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
	iamCertificate, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		logger.Error("iam_tls_certificate_parse_failed", "error_code", "IAM_TLS_CERTIFICATE_PARSE_FAILED")
		os.Exit(1)
	}
	iamSPIFFEID, err := certificateSPIFFEID(iamCertificate)
	if err != nil {
		logger.Error("iam_tls_spiffe_identity_invalid", "error_code", "IAM_TLS_SPIFFE_IDENTITY_INVALID")
		os.Exit(1)
	}
	registryGrantSigner, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		logger.Error("iam_registry_grant_signer_invalid", "error_code", "IAM_REGISTRY_GRANT_SIGNER_INVALID")
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

	policyRevision := envOr("IAM_POLICY_REVISION", "policy-unconfigured")
	authorizationStore := iam.NewDenyAllAuthorizationStore(policyRevision)
	var telemetryAuthorizationStore iam.TelemetryAuthorizationStore
	var telemetryGrantStore iam.TelemetryGrantStore
	var grantStatusStore iam.RegistryGrantStatusStore = iam.StaticRegistryGrantStatusStore{PolicyRevision: policyRevision}
	databaseURL := strings.TrimSpace(os.Getenv("IAM_DATABASE_URL"))
	fixtureEnabled := envEnabled("IAM_S1_AUTHORIZATION_FIXTURE")
	telemetryFixtureEnabled := envEnabled("IAM_S2_AUTHORIZATION_FIXTURE")
	if databaseURL != "" && (fixtureEnabled || telemetryFixtureEnabled) {
		logger.Error("iam_authorization_store_configuration_conflict", "error_code", "IAM_AUTHORIZATION_STORE_CONFIGURATION_CONFLICT")
		os.Exit(1)
	}
	if databaseURL != "" {
		openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
		postgresStore, err := iam.OpenPostgresAuthorizationStore(openContext, databaseURL)
		cancelOpen()
		if err != nil {
			logger.Error("iam_authorization_store_open_failed", "error_code", "IAM_AUTHORIZATION_STORE_OPEN_FAILED")
			os.Exit(1)
		}
		defer postgresStore.Close()
		authorizationStore = postgresStore
		telemetryAuthorizationStore = postgresStore
		grantStatusStore = postgresStore
		policyRevision = "database-managed"
		logger.Info("iam_postgres_authorization_store_enabled")
	} else if fixtureEnabled {
		subjectIssuer := requiredEnv("IAM_EXTERNAL_SUBJECT_ISSUER")
		authorizationStore = iam.NewS1FixtureAuthorizationStore(subjectIssuer)
		policyRevision = iam.S1FixturePolicyRevision
		grantStatusStore = iam.StaticRegistryGrantStatusStore{PolicyRevision: policyRevision}
		logger.Warn("iam_s1_authorization_fixture_enabled", "policy_revision", policyRevision)
	}
	if telemetryFixtureEnabled {
		subjectIssuer := requiredEnv("IAM_EXTERNAL_SUBJECT_ISSUER")
		telemetryAuthorizationStore = iam.NewS2FixtureTelemetryAuthorizationStore(subjectIssuer)
		logger.Warn("iam_s2_authorization_fixture_enabled", "policy_revision", iam.S2FixturePolicyRevision)
	}

	telemetryGrantDatabaseURL := strings.TrimSpace(os.Getenv("IAM_TELEMETRY_GRANT_DATABASE_URL"))
	if telemetryGrantDatabaseURL != "" {
		openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
		postgresGrantStore, err := iam.OpenPostgresTelemetryGrantStore(openContext, telemetryGrantDatabaseURL)
		cancelOpen()
		if err != nil {
			logger.Error("iam_telemetry_grant_store_open_failed", "error_code", "IAM_TELEMETRY_GRANT_STORE_OPEN_FAILED")
			os.Exit(1)
		}
		defer postgresGrantStore.Close()
		telemetryGrantStore = postgresGrantStore
		logger.Info("iam_telemetry_grant_store_enabled")
	}

	server := &http.Server{
		Addr: address,
		Handler: iam.NewHandler(iam.Config{
			AllowedWorkloadSPIFFE:       envOr("IAM_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			CoreWorkloadSPIFFE:          envOr("IAM_CORE_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-core-service"),
			Audience:                    envOr("IAM_AUDIENCE", "iam-service"),
			Logger:                      logger,
			Observability:               telemetry,
			AuthorizationStore:          authorizationStore,
			RegistryGrantSigner:         registryGrantSigner,
			RegistryGrantIssuer:         iamSPIFFEID,
			RegistryGrantAudience:       envOr("IAM_REGISTRY_GRANT_AUDIENCE", "platform-core-service"),
			RegistryGrantStatus:         grantStatusStore,
			TelemetryAuthorizationStore: telemetryAuthorizationStore,
			TelemetryGrantSigner:        registryGrantSigner,
			TelemetryGrantIssuer:        iamSPIFFEID,
			TelemetryGrantAudience:      envOr("IAM_TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
			TelemetryRuntimeSPIFFE:      envOr("IAM_TELEMETRY_RUNTIME_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
			TelemetryGrantStore:         telemetryGrantStore,
			CommandGrantSigner:          registryGrantSigner,
			CommandGrantIssuer:          iamSPIFFEID,
			CommandGrantAudience:        envOr("IAM_COMMAND_GRANT_AUDIENCE", "command-service"),
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
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		_ = telemetry.Shutdown(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("iam_started", "service", "iam-service", "address", address, "policy_revision", policyRevision)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("iam_stopped_unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("iam_stopped", "service", "iam-service")
}

func certificateSPIFFEID(certificate *x509.Certificate) (string, error) {
	if certificate == nil || len(certificate.URIs) != 1 || certificate.URIs[0] == nil {
		return "", errors.New("IAM certificate must contain exactly one URI identity")
	}
	identity := certificate.URIs[0].String()
	if !strings.HasPrefix(identity, "spiffe://") {
		return "", errors.New("IAM certificate URI is not a SPIFFE identity")
	}
	return identity, nil
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
