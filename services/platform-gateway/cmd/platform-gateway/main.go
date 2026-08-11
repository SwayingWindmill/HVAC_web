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
	"net/url"
	"os"
	"os/signal"
	"strings"
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
	identity, workloadCertificate, closeIdentity, err := loadIdentityConfig(runContext)
	if err != nil {
		logger.Error("gateway_identity_config_invalid", "error_code", "IDENTITY_CONFIG_INVALID")
		os.Exit(1)
	}
	defer closeIdentity()
	telemetryConfig, err := loadTelemetryConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_telemetry_config_invalid", "error_code", "TELEMETRY_CONFIG_INVALID")
		os.Exit(1)
	}
	commandConfig, err := loadCommandConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_command_config_invalid", "error_code", "COMMAND_CONFIG_INVALID")
		os.Exit(1)
	}
	alarmConfig, err := loadAlarmConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_alarm_config_invalid", "error_code", "ALARM_CONFIG_INVALID")
		os.Exit(1)
	}
	workOrderConfig, err := loadWorkOrderConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_work_order_config_invalid", "error_code", "WORK_ORDER_CONFIG_INVALID")
		os.Exit(1)
	}
	analyticsConfig, err := loadAnalyticsConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_analytics_config_invalid", "error_code", "ANALYTICS_CONFIG_INVALID")
		os.Exit(1)
	}
	operationsConfig, err := loadOperationsConfig(workloadCertificate)
	if err != nil {
		logger.Error("gateway_operations_config_invalid", "error_code", "OPERATIONS_CONFIG_INVALID")
		os.Exit(1)
	}
	serverTLSConfig, serverTLSEnabled, err := loadGatewayServerTLSConfig()
	if err != nil {
		logger.Error("gateway_server_tls_config_invalid", "error_code", "GATEWAY_SERVER_TLS_CONFIG_INVALID")
		os.Exit(1)
	}

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
		Registry:      routing.registry,
		Telemetry:     telemetryConfig,
		Command:       commandConfig,
		Alarm:         alarmConfig,
		WorkOrder:     workOrderConfig,
		Analytics:     analyticsConfig,
		Operations:    operationsConfig,
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
		TLSConfig:         serverTLSConfig,
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
	logger.Info("gateway_started", "service", "platform-gateway", "address", address, "version", version, "commit", commit, "identity_enabled", identity != nil, "tls_enabled", serverTLSEnabled)
	serve := server.ListenAndServe
	if serverTLSEnabled {
		serve = func() error { return server.ListenAndServeTLS("", "") }
	}
	if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway_stopped_unexpectedly", "error_code", "GATEWAY_SERVE_FAILED")
		os.Exit(1)
	}
	logger.Info("gateway_stopped", "service", "platform-gateway")
}

func loadIdentityConfig(ctx context.Context) (*gateway.IdentityConfig, *tls.Certificate, func(), error) {
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return nil, nil, func() {}, nil
	}
	required := map[string]string{}
	for _, name := range []string{"OIDC_CLIENT_ID", "OIDC_REDIRECT_URI", "PLATFORM_PUBLIC_ORIGIN", "IAM_URL", "IAM_CLIENT_CERT", "IAM_CLIENT_KEY", "IAM_SERVER_CA"} {
		value := os.Getenv(name)
		if value == "" {
			return nil, nil, func() {}, fmt.Errorf("%s is required when OIDC_ISSUER is configured", name)
		}
		required[name] = value
	}
	certificate, err := tls.LoadX509KeyPair(required["IAM_CLIENT_CERT"], required["IAM_CLIENT_KEY"])
	if err != nil {
		return nil, nil, func() {}, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, func() {}, errors.New("Gateway workload private key cannot sign delegation grants")
	}
	iamRoots, err := loadCertPool(required["IAM_SERVER_CA"], "IAM server CA")
	if err != nil {
		return nil, nil, func() {}, err
	}
	oidcClient := &http.Client{Timeout: 5 * time.Second}
	if oidcCAPath := os.Getenv("OIDC_SERVER_CA"); oidcCAPath != "" {
		oidcRoots, err := loadCertPool(oidcCAPath, "OIDC server CA")
		if err != nil {
			return nil, nil, func() {}, err
		}
		oidcClient.Transport = workloadTransport(oidcRoots, nil, envOr("OIDC_SERVER_NAME", "localhost"))
	}

	var auditClient *http.Client
	auditURL := os.Getenv("AUDIT_URL")
	if auditURL == "" {
		if os.Getenv("S0_ALLOW_NO_AUDIT_LEDGER") != "true" {
			return nil, nil, func() {}, errors.New("AUDIT_URL is required unless S0_ALLOW_NO_AUDIT_LEDGER=true")
		}
	} else {
		auditCAPath := os.Getenv("AUDIT_SERVER_CA")
		if auditCAPath == "" {
			return nil, nil, func() {}, errors.New("AUDIT_SERVER_CA is required when AUDIT_URL is configured")
		}
		auditRoots, err := loadCertPool(auditCAPath, "Audit server CA")
		if err != nil {
			return nil, nil, func() {}, err
		}
		auditClient = &http.Client{Timeout: 5 * time.Second, Transport: workloadTransport(auditRoots, &certificate, envOr("AUDIT_SERVER_NAME", "localhost"))}
	}

	var store sessionstore.Store
	closeStore := func() {}
	if dsn := os.Getenv("GATEWAY_DATABASE_URL"); dsn != "" {
		postgresStore, err := sessionstore.OpenPostgres(ctx, dsn, sessionstore.PostgresConfig{})
		if err != nil {
			return nil, nil, func() {}, errors.New("durable Session store is unavailable")
		}
		store = postgresStore
		closeStore = postgresStore.Close
	} else if os.Getenv("S0_ALLOW_MEMORY_SESSION_STORE") == "true" {
		store = sessionstore.NewMemoryStore()
	} else {
		return nil, nil, func() {}, errors.New("GATEWAY_DATABASE_URL is required unless S0_ALLOW_MEMORY_SESSION_STORE=true")
	}

	key, err := sessionEncryptionKey()
	if err != nil {
		closeStore()
		return nil, nil, func() {}, err
	}
	return &gateway.IdentityConfig{
		OIDCIssuer:                  issuer,
		OIDCClientID:                required["OIDC_CLIENT_ID"],
		OIDCRedirectURI:             required["OIDC_REDIRECT_URI"],
		PublicOrigin:                required["PLATFORM_PUBLIC_ORIGIN"],
		DefaultActingOrganizationID: os.Getenv("OIDC_DEFAULT_ACTING_ORGANIZATION_ID"),
		IAMURL:                      required["IAM_URL"],
		IAMAudience:                 envOr("IAM_AUDIENCE", "iam-service"),
		AuditURL:                    auditURL,
		AuditAudience:               envOr("AUDIT_AUDIENCE", "audit-ledger-service"),
		ExecutingWorkloadSPIFFE:     envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		PolicyRevision:              envOr("IDENTITY_POLICY_REVISION", "policy-v1"),
		DelegationSigner:            signer,
		TokenEncryptionKey:          key,
		SessionStore:                store,
		SessionTTL:                  30 * time.Minute,
		StateTTL:                    2 * time.Minute,
		DelegationTTL:               30 * time.Second,
		RevocationObjective:         time.Second,
		IAMHTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: workloadTransport(iamRoots, &certificate, envOr("IAM_SERVER_NAME", "localhost")),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		AuditHTTPClient: auditClient,
		OIDCHTTPClient:  oidcClient,
	}, &certificate, closeStore, nil
}

func loadGatewayServerTLSConfig() (*tls.Config, bool, error) {
	certPath := strings.TrimSpace(os.Getenv("GATEWAY_SERVER_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("GATEWAY_SERVER_KEY"))
	clientCAPath := strings.TrimSpace(os.Getenv("GATEWAY_CLIENT_CA"))
	configured := certPath != "" || keyPath != "" || clientCAPath != ""
	if !configured {
		return nil, false, nil
	}
	if certPath == "" || keyPath == "" || clientCAPath == "" {
		return nil, false, errors.New("GATEWAY_SERVER_CERT, GATEWAY_SERVER_KEY and GATEWAY_CLIENT_CA must be configured together")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, false, err
	}
	clientCAs, err := loadCertPool(clientCAPath, "Gateway client CA")
	if err != nil {
		return nil, false, err
	}
	return gatewayServerTLSConfig(certificate, clientCAs), true, nil
}

func gatewayServerTLSConfig(certificate tls.Certificate, clientCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.VerifyClientCertIfGiven,
	}
}

func loadTelemetryConfig(certificate *tls.Certificate) (*gateway.TelemetryConfig, error) {
	runtimeURL := strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_URL"))
	if runtimeURL == "" {
		return nil, nil
	}
	if certificate == nil {
		return nil, errors.New("Telemetry Runtime requires the authenticated Gateway workload certificate")
	}
	caPath := os.Getenv("TELEMETRY_RUNTIME_SERVER_CA")
	if caPath == "" {
		return nil, errors.New("TELEMETRY_RUNTIME_SERVER_CA is required when TELEMETRY_RUNTIME_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Telemetry Runtime server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.TelemetryConfig{
		RuntimeBaseURL:    runtimeURL,
		RuntimeAudience:   envOr("TELEMETRY_RUNTIME_AUDIENCE", "telemetry-runtime-service"),
		Timeout:           2 * time.Second,
		MaxResponseBytes:  2 << 20,
		RuntimeHTTPClient: &http.Client{Transport: workloadTransport(roots, certificate, envOr("TELEMETRY_RUNTIME_SERVER_NAME", "localhost"))},
	}, nil
}

func loadCommandConfig(certificate *tls.Certificate) (*gateway.CommandConfig, error) {
	serviceURL := strings.TrimSpace(os.Getenv("COMMAND_SERVICE_URL"))
	if serviceURL == "" {
		return nil, nil
	}
	if certificate == nil {
		return nil, errors.New("Command Service requires the authenticated Gateway workload certificate")
	}
	caPath := os.Getenv("COMMAND_SERVICE_SERVER_CA")
	if caPath == "" {
		return nil, errors.New("COMMAND_SERVICE_SERVER_CA is required when COMMAND_SERVICE_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Command Service server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.CommandConfig{
		BackendBaseURL:    serviceURL,
		BackendAudience:   envOr("COMMAND_SERVICE_AUDIENCE", "command-service"),
		IAMGrantIssuer:    envOr("IAM_COMMAND_GRANT_ISSUER", "spiffe://hvac.local/iam-service"),
		TemperatureKey:    envOr("COMMAND_TEMPERATURE_KEY", "zone.temperature"),
		Timeout:           10 * time.Second,
		MaxResponseBytes:  256 << 10,
		BackendHTTPClient: &http.Client{Transport: workloadTransport(roots, certificate, envOr("COMMAND_SERVICE_SERVER_NAME", "localhost"))},
	}, nil
}

func loadAlarmConfig(certificate *tls.Certificate) (*gateway.AlarmConfig, error) {
	serviceURL := strings.TrimSpace(os.Getenv("ALARM_SERVICE_URL"))
	if serviceURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(serviceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ALARM_SERVICE_URL must be an HTTPS origin without user info, path, query or fragment")
	}
	if certificate == nil {
		return nil, errors.New("Alarm Service requires the authenticated Gateway workload certificate")
	}
	caPath := strings.TrimSpace(os.Getenv("ALARM_SERVICE_SERVER_CA"))
	if caPath == "" {
		return nil, errors.New("ALARM_SERVICE_SERVER_CA is required when ALARM_SERVICE_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Alarm Service server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.AlarmConfig{
		BackendBaseURL:   strings.TrimRight(parsed.String(), "/"),
		BackendAudience:  envOr("ALARM_SERVICE_AUDIENCE", "alarm-service"),
		Timeout:          5 * time.Second,
		MaxResponseBytes: 2 << 20,
		BackendHTTPClient: &http.Client{Transport: workloadTransport(
			roots,
			certificate,
			envOr("ALARM_SERVICE_SERVER_NAME", "localhost"),
		)},
	}, nil
}

func loadWorkOrderConfig(certificate *tls.Certificate) (*gateway.WorkOrderConfig, error) {
	serviceURL := strings.TrimSpace(os.Getenv("WORK_ORDER_SERVICE_URL"))
	if serviceURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(serviceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("WORK_ORDER_SERVICE_URL must be an HTTPS origin without user info, path, query or fragment")
	}
	if certificate == nil {
		return nil, errors.New("Work Order Service requires the authenticated Gateway workload certificate")
	}
	caPath := strings.TrimSpace(os.Getenv("WORK_ORDER_SERVICE_SERVER_CA"))
	if caPath == "" {
		return nil, errors.New("WORK_ORDER_SERVICE_SERVER_CA is required when WORK_ORDER_SERVICE_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Work Order Service server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.WorkOrderConfig{
		BackendBaseURL:   strings.TrimRight(parsed.String(), "/"),
		BackendAudience:  envOr("WORK_ORDER_SERVICE_AUDIENCE", "work-order-service"),
		Timeout:          5 * time.Second,
		MaxResponseBytes: 2 << 20,
		BackendHTTPClient: &http.Client{Transport: workloadTransport(
			roots, certificate, envOr("WORK_ORDER_SERVICE_SERVER_NAME", "localhost"),
		)},
	}, nil
}

func loadAnalyticsConfig(certificate *tls.Certificate) (*gateway.AnalyticsConfig, error) {
	queryURL := strings.TrimSpace(os.Getenv("TELEMETRY_QUERY_URL"))
	if queryURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(queryURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("TELEMETRY_QUERY_URL must be an HTTPS origin without user info, path, query or fragment")
	}
	queryURL = strings.TrimRight(parsed.String(), "/")
	if certificate == nil {
		return nil, errors.New("Telemetry Query Service requires the authenticated Gateway workload certificate")
	}
	caPath := strings.TrimSpace(os.Getenv("TELEMETRY_QUERY_SERVER_CA"))
	if caPath == "" {
		return nil, errors.New("TELEMETRY_QUERY_SERVER_CA is required when TELEMETRY_QUERY_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Telemetry Query server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.AnalyticsConfig{
		QueryBaseURL:     queryURL,
		QueryAudience:    envOr("TELEMETRY_QUERY_AUDIENCE", "telemetry-query-service"),
		Timeout:          8 * time.Second,
		MaxResponseBytes: 8 << 20,
		QueryHTTPClient:  &http.Client{Transport: workloadTransport(roots, certificate, envOr("TELEMETRY_QUERY_SERVER_NAME", "localhost"))},
	}, nil
}

func loadOperationsConfig(certificate *tls.Certificate) (*gateway.OperationsAgentConfig, error) {
	operationsURL := strings.TrimSpace(os.Getenv("OPERATIONS_AGENT_URL"))
	if operationsURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(operationsURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OPERATIONS_AGENT_URL must be an HTTPS origin without user info, path, query or fragment")
	}
	if certificate == nil {
		return nil, errors.New("Operations Agent requires the authenticated Gateway workload certificate")
	}
	caPath := strings.TrimSpace(os.Getenv("OPERATIONS_AGENT_SERVER_CA"))
	if caPath == "" {
		return nil, errors.New("OPERATIONS_AGENT_SERVER_CA is required when OPERATIONS_AGENT_URL is configured")
	}
	roots, err := loadCertPool(caPath, "Operations Agent server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.OperationsAgentConfig{
		BaseURL:          strings.TrimRight(parsed.String(), "/"),
		Audience:         envOr("OPERATIONS_AGENT_AUDIENCE", "operations-agent-service"),
		WorkloadSPIFFEID: envOr("OPERATIONS_AGENT_SPIFFE_ID", "spiffe://hvac.local/operations-agent-service"),
		Timeout:          8 * time.Second,
		MaxRequestBytes:  8 << 10,
		MaxResponseBytes: 1 << 20,
		HTTPClient: &http.Client{Transport: workloadTransport(
			roots,
			certificate,
			envOr("OPERATIONS_AGENT_SERVER_NAME", "localhost"),
		)},
	}, nil
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
