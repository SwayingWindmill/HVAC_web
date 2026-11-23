package main

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

func main() {
	os.Exit(run())
}

func run() int {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "iam-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	var closers []func()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shutdownTelemetry(ctx)
		cancel()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()

	address := envOr("IAM_SERVICE_ADDR", "127.0.0.1:18444")
	tlsCertPath, err := requiredEnv("IAM_TLS_CERT")
	if err != nil {
		logEnvError(logger, "iam_tls_identity_load_failed", "IAM_TLS_IDENTITY_LOAD_FAILED", err)
		return 1
	}
	tlsKeyPath, err := requiredEnv("IAM_TLS_KEY")
	if err != nil {
		logEnvError(logger, "iam_tls_identity_load_failed", "IAM_TLS_IDENTITY_LOAD_FAILED", err)
		return 1
	}
	certificate, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		logger.Error("iam_tls_identity_load_failed", "error", err)
		return 1
	}
	iamCertificate, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		logger.Error("iam_tls_certificate_parse_failed", "error_code", "IAM_TLS_CERTIFICATE_PARSE_FAILED")
		return 1
	}
	iamSPIFFEID, err := certificateSPIFFEID(iamCertificate)
	if err != nil {
		logger.Error("iam_tls_spiffe_identity_invalid", "error_code", "IAM_TLS_SPIFFE_IDENTITY_INVALID")
		return 1
	}
	registryGrantSigner, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		logger.Error("iam_registry_grant_signer_invalid", "error_code", "IAM_REGISTRY_GRANT_SIGNER_INVALID")
		return 1
	}
	clientCAPath, err := requiredEnv("IAM_CLIENT_CA")
	if err != nil {
		logEnvError(logger, "iam_client_ca_load_failed", "IAM_CLIENT_CA_LOAD_FAILED", err)
		return 1
	}
	caPEM, err := os.ReadFile(clientCAPath)
	if err != nil {
		logger.Error("iam_client_ca_load_failed", "error", err)
		return 1
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		logger.Error("iam_client_ca_invalid")
		return 1
	}

	policyRevision := envOr("IAM_POLICY_REVISION", "policy-unconfigured")
	authorizationStore := iam.NewDenyAllAuthorizationStore(policyRevision)
	var telemetryAuthorizationStore iam.TelemetryAuthorizationStore
	var commandAuthorizationStore iam.CommandAuthorizationStore
	var alarmAuthorizationStore iam.AlarmAuthorizationStore
	var alarmAuditSink iam.AlarmDecisionAuditSink
	var workOrderAuthorizationStore iam.WorkOrderAuthorizationStore
	var workOrderAuditSink iam.WorkOrderDecisionAuditSink
	var telemetryGrantStore iam.TelemetryGrantStore
	var adminStore iam.AdminStore
	var grantStatusStore iam.RegistryGrantStatusStore = iam.StaticRegistryGrantStatusStore{PolicyRevision: policyRevision}
	databaseURL := strings.TrimSpace(os.Getenv("IAM_DATABASE_URL"))
	fixtureEnabled := envEnabled("IAM_S1_AUTHORIZATION_FIXTURE")
	telemetryFixtureEnabled := envEnabled("IAM_S2_AUTHORIZATION_FIXTURE")
	if databaseURL != "" && (fixtureEnabled || telemetryFixtureEnabled) {
		logger.Error("iam_authorization_store_configuration_conflict", "error_code", "IAM_AUTHORIZATION_STORE_CONFIGURATION_CONFLICT")
		return 1
	}
	if databaseURL != "" {
		openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
		postgresStore, err := iam.OpenPostgresAuthorizationStore(openContext, databaseURL)
		cancelOpen()
		if err != nil {
			logger.Error("iam_authorization_store_open_failed", "error_code", "IAM_AUTHORIZATION_STORE_OPEN_FAILED")
			return 1
		}
		closers = append(closers, postgresStore.Close)
		authorizationStore = postgresStore
		revocationRevisionRaw, err := requiredEnv("COMMAND_EMERGENCY_REVOCATION_REVISION")
		if err != nil {
			logEnvError(logger, "iam_command_revocation_revision_invalid", "IAM_COMMAND_REVOCATION_REVISION_INVALID", err)
			return 1
		}
		commandRevocationRevision, err := strconv.ParseUint(revocationRevisionRaw, 10, 64)
		if err != nil {
			logger.Error("iam_command_revocation_revision_invalid")
			return 1
		}
		commandPolicyRevision, err := requiredEnv("COMMAND_POLICY_REVISION")
		if err != nil {
			logEnvError(logger, "iam_command_policy_revision_invalid", "IAM_COMMAND_POLICY_REVISION_INVALID", err)
			return 1
		}
		commandAuthorizationStore = postgresStore.CommandAuthorization(commandPolicyRevision, commandRevocationRevision)
		telemetryAuthorizationStore = postgresStore
		alarmAuthorizationStore = postgresStore
		alarmAuditSink = postgresStore
		workOrderAuthorizationStore = postgresStore
		workOrderAuditSink = postgresStore
		grantStatusStore = postgresStore
		policyRevision = "database-managed"
		logger.Info("iam_postgres_authorization_store_enabled")
	} else if fixtureEnabled {
		subjectIssuer, err := requiredEnv("IAM_EXTERNAL_SUBJECT_ISSUER")
		if err != nil {
			logEnvError(logger, "iam_external_subject_issuer_invalid", "IAM_EXTERNAL_SUBJECT_ISSUER_INVALID", err)
			return 1
		}
		authorizationStore = iam.NewS1FixtureAuthorizationStore(subjectIssuer)
		policyRevision = iam.S1FixturePolicyRevision
		grantStatusStore = iam.StaticRegistryGrantStatusStore{PolicyRevision: policyRevision}
		logger.Warn("iam_s1_authorization_fixture_enabled", "policy_revision", policyRevision)
	}
	if telemetryFixtureEnabled {
		subjectIssuer, err := requiredEnv("IAM_EXTERNAL_SUBJECT_ISSUER")
		if err != nil {
			logEnvError(logger, "iam_external_subject_issuer_invalid", "IAM_EXTERNAL_SUBJECT_ISSUER_INVALID", err)
			return 1
		}
		telemetryAuthorizationStore = iam.NewS2FixtureTelemetryAuthorizationStore(subjectIssuer)
		logger.Warn("iam_s2_authorization_fixture_enabled", "policy_revision", iam.S2FixturePolicyRevision)
	}

	adminDatabaseURL := strings.TrimSpace(os.Getenv("IAM_ADMIN_DATABASE_URL"))
	if adminDatabaseURL != "" {
		openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
		postgresAdminStore, err := iam.OpenPostgresAdminStore(openContext, adminDatabaseURL)
		cancelOpen()
		if err != nil {
			logger.Error("iam_admin_store_open_failed", "error_code", "IAM_ADMIN_STORE_OPEN_FAILED")
			return 1
		}
		closers = append(closers, postgresAdminStore.Close)
		adminStore = postgresAdminStore
		logger.Info("iam_postgres_admin_store_enabled")
	}

	telemetryGrantDatabaseURL := strings.TrimSpace(os.Getenv("IAM_TELEMETRY_GRANT_DATABASE_URL"))
	if telemetryGrantDatabaseURL != "" {
		openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
		postgresGrantStore, err := iam.OpenPostgresTelemetryGrantStore(openContext, telemetryGrantDatabaseURL)
		cancelOpen()
		if err != nil {
			logger.Error("iam_telemetry_grant_store_open_failed", "error_code", "IAM_TELEMETRY_GRANT_STORE_OPEN_FAILED")
			return 1
		}
		closers = append(closers, postgresGrantStore.Close)
		telemetryGrantStore = postgresGrantStore
		logger.Info("iam_telemetry_grant_store_enabled")
	}

	server := &http.Server{
		Addr: address,
		Handler: iam.NewHandler(iam.Config{
			AllowedWorkloadSPIFFE:  envOr("IAM_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			CommandAuthoritySPIFFE: envOr("IAM_COMMAND_AUTHORITY_SPIFFE", "spiffe://hvac.local/command-service"),
			CoreWorkloadSPIFFE:     envOr("IAM_CORE_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-core-service"),
			Audience:               envOr("IAM_AUDIENCE", "iam-service"),
			Logger:                 logger,
			Observability:          telemetry,
			AuthorizationStore:     authorizationStore,
			AdminStore:             adminStore,
			RegistryGrantSigner:    registryGrantSigner,
			RegistryGrantIssuer:    iamSPIFFEID,
			RegistryGrantAudience:  envOr("IAM_REGISTRY_GRANT_AUDIENCE", "platform-core-service"),
			AllowedRegistryGrantPresenters: []string{
				envOr("IAM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
				envOr("IAM_COMMAND_AUTHORITY_SPIFFE", "spiffe://hvac.local/command-service"),
			},
			RegistryGrantStatus:         grantStatusStore,
			TelemetryAuthorizationStore: telemetryAuthorizationStore,
			AlarmAuthorizationStore:     alarmAuthorizationStore,
			AlarmAuditSink:              alarmAuditSink,
			WorkOrderAuthorizationStore: workOrderAuthorizationStore,
			WorkOrderAuditSink:          workOrderAuditSink,
			TelemetryGrantSigner:        registryGrantSigner,
			TelemetryGrantIssuer:        iamSPIFFEID,
			TelemetryGrantAudience:      envOr("IAM_TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
			AllowedTelemetryGrantPresenters: []string{
				envOr("IAM_OPERATIONS_AGENT_SPIFFE", "spiffe://hvac.local/operations-agent-service"),
			},
			TelemetryRuntimeSPIFFE:    envOr("IAM_TELEMETRY_RUNTIME_SPIFFE", "spiffe://hvac.local/telemetry-runtime-service"),
			TelemetryGrantStore:       telemetryGrantStore,
			CommandAuthorizationStore: commandAuthorizationStore,
			CommandGrantSigner:        registryGrantSigner,
			CommandGrantIssuer:        iamSPIFFEID,
			CommandGrantAudience:      envOr("IAM_COMMAND_GRANT_AUDIENCE", "command-service"),
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
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "iam_owner.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("iam_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "iam_owner.shutdown")
		<-signalCtx.Done()
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = diagnostics.Shutdown(ctx)
		select {
		case <-diagnosticsDone:
		case <-ctx.Done():
		}
		_ = shutdownTelemetry(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("iam_started", "service", "iam-service", "address", address, "policy_revision", policyRevision)
	exitCode := 0
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("iam_stopped_unexpectedly", "error", err)
		exitCode = 1
	}
	stopSignals()
	<-shutdownDone
	if exitCode == 0 {
		logger.Info("iam_stopped", "service", "iam-service")
	}
	return exitCode
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

func logEnvError(logger *slog.Logger, msg, code string, err error) {
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
	}
	if logger != nil {
		logger.Error(msg, "error_code", code)
	}
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
