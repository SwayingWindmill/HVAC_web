package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/registry/internal/core"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := observability.NewJSONLogger(os.Stdout, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service: "platform-core-service", OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), QueueSize: 1024, ExportTimeout: 500 * time.Millisecond,
	})
	var telemetryShutdownOnce sync.Once
	shutdownTelemetry := func(ctx context.Context) error {
		var shutdownErr error
		telemetryShutdownOnce.Do(func() {
			shutdownErr = telemetry.Shutdown(ctx)
		})
		return shutdownErr
	}
	var store *core.PostgresStore
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = shutdownTelemetry(ctx)
		cancel()
		if store != nil {
			store.Close()
		}
	}()

	tlsCertPath, err := requiredEnv("CORE_TLS_CERT")
	if err != nil {
		logEnvError(logger, "core_tls_identity_load_failed", "CORE_TLS_IDENTITY_LOAD_FAILED", err)
		return 1
	}
	tlsKeyPath, err := requiredEnv("CORE_TLS_KEY")
	if err != nil {
		logEnvError(logger, "core_tls_identity_load_failed", "CORE_TLS_IDENTITY_LOAD_FAILED", err)
		return 1
	}
	certificate, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		logger.Error("core_tls_identity_load_failed", "error_code", "CORE_TLS_IDENTITY_LOAD_FAILED")
		return 1
	}
	clientCAPath, err := requiredEnv("CORE_CLIENT_CA")
	if err != nil {
		logEnvError(logger, "core_client_ca_invalid", "CORE_CLIENT_CA_INVALID", err)
		return 1
	}
	clientCAs, err := loadCertPool(clientCAPath)
	if err != nil {
		logger.Error("core_client_ca_invalid", "error_code", "CORE_CLIENT_CA_INVALID")
		return 1
	}
	iamCAPath, err := requiredEnv("CORE_IAM_CA")
	if err != nil {
		logEnvError(logger, "core_iam_ca_invalid", "CORE_IAM_CA_INVALID", err)
		return 1
	}
	iamCAs, err := loadCertPool(iamCAPath)
	if err != nil {
		logger.Error("core_iam_ca_invalid", "error_code", "CORE_IAM_CA_INVALID")
		return 1
	}
	iamGrantCertPath, err := requiredEnv("CORE_IAM_GRANT_CERT")
	if err != nil {
		logEnvError(logger, "core_iam_grant_certificate_invalid", "CORE_IAM_GRANT_CERTIFICATE_INVALID", err)
		return 1
	}
	iamGrantPublicKey, err := loadCertificatePublicKey(iamGrantCertPath)
	if err != nil {
		logger.Error("core_iam_grant_certificate_invalid", "error_code", "CORE_IAM_GRANT_CERTIFICATE_INVALID")
		return 1
	}
	cursorKeyEncoded, err := requiredEnv("CORE_CURSOR_HMAC_KEY")
	if err != nil {
		logEnvError(logger, "core_cursor_key_invalid", "CORE_CURSOR_KEY_INVALID", err)
		return 1
	}
	cursorKey, err := base64.RawURLEncoding.DecodeString(cursorKeyEncoded)
	if err != nil {
		logger.Error("core_cursor_key_invalid", "error_code", "CORE_CURSOR_KEY_INVALID")
		return 1
	}
	cursorCodec, err := core.NewCursorCodec(cursorKey)
	if err != nil {
		logger.Error("core_cursor_key_invalid", "error_code", "CORE_CURSOR_KEY_INVALID")
		return 1
	}
	databaseURL, err := requiredEnv("CORE_DATABASE_URL")
	if err != nil {
		logEnvError(logger, "core_registry_store_open_failed", "CORE_REGISTRY_STORE_OPEN_FAILED", err)
		return 1
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	store, err = core.OpenPostgresStore(openContext, databaseURL)
	cancelOpen()
	if err != nil {
		logger.Error("core_registry_store_open_failed", "error_code", "CORE_REGISTRY_STORE_OPEN_FAILED")
		return 1
	}

	iamClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: newRegistryHTTPTransport(&tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      iamCAs,
			Certificates: []tls.Certificate{certificate},
		}),
	}
	iamEndpoint, err := requiredEnv("CORE_IAM_ENDPOINT")
	if err != nil {
		logEnvError(logger, "core_iam_status_client_invalid", "CORE_IAM_STATUS_CLIENT_INVALID", err)
		return 1
	}
	grantStatus, err := core.NewHTTPGrantStatusProvider(iamEndpoint, iamClient)
	if err != nil {
		logger.Error("core_iam_status_client_invalid", "error_code", "CORE_IAM_STATUS_CLIENT_INVALID")
		return 1
	}

	relayCtx, cancelRelay := context.WithCancel(context.Background())
	defer cancelRelay()
	relay, relayErr := startOptionalOutboxRelay(relayCtx, store, certificate, iamCAs, logger)
	if relayErr != nil {
		logger.Error("core_outbox_relay_config_invalid", "error", relayErr.Error())
		return 1
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
				envOr("CORE_COMMAND_SERVICE_SPIFFE", "spiffe://hvac.local/command-service"),
			},
			AllowedCommandServiceSPIFFE: envOr("CORE_COMMAND_SERVICE_SPIFFE", "spiffe://hvac.local/command-service"),
			Audience:                    envOr("CORE_AUDIENCE", "platform-core-service"),
			GrantStatus:                 grantStatus,
			Logger:                      logger,
			Observability:               telemetry,
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
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		defer observability.Recover(logger, telemetry.Metrics, "registry_owner.diagnostics")
		if err := diagnostics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("core_diagnostics_failed", "error_code", "DIAGNOSTICS_SERVE_FAILED")
		}
	}()

	relayDone := make(chan struct{})
	if relay != nil {
		go func() {
			defer close(relayDone)
			defer observability.Recover(logger, telemetry.Metrics, "registry_owner.outbox_relay")
			relay.Run(relayCtx)
		}()
	} else {
		close(relayDone)
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		defer observability.Recover(logger, telemetry.Metrics, "registry_owner.shutdown")
		<-signalCtx.Done()
		telemetry.MarkNotReady()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		cancelRelay()
		select {
		case <-relayDone:
		case <-ctx.Done():
		}
		_ = diagnostics.Shutdown(ctx)
		select {
		case <-diagnosticsDone:
		case <-ctx.Done():
		}
		_ = shutdownTelemetry(ctx)
	}()

	telemetry.MarkReady()
	logger.Info("core_started", "service", "platform-core-service", "address", server.Addr)
	exitCode := 0
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("core_stopped_unexpectedly", "error_code", "CORE_SERVE_FAILED")
		exitCode = 1
	}
	stopSignals()
	<-shutdownDone
	if exitCode == 0 {
		logger.Info("core_stopped", "service", "platform-core-service")
	}
	return exitCode
}

func newRegistryHTTPTransport(tlsConfig *tls.Config) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.TLSClientConfig = tlsConfig
	return transport
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

func startOptionalOutboxRelay(ctx context.Context, store *core.PostgresStore, cert tls.Certificate, defaultRoots *x509.CertPool, logger *slog.Logger) (*core.OutboxRelay, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CORE_OUTBOX_RELAY_ENABLED")), "false") {
		return nil, nil
	}
	telemetryURL := envOr("CORE_TELEMETRY_RUNTIME_URL", strings.TrimSpace(os.Getenv("TELEMETRY_RUNTIME_URL")))
	connectivityURL := envOr("CORE_CONNECTIVITY_SERVICE_URL", strings.TrimSpace(os.Getenv("CONNECTIVITY_SERVICE_URL")))
	commandURL := envOr("CORE_COMMAND_SERVICE_URL", strings.TrimSpace(os.Getenv("COMMAND_SERVICE_URL")))
	if telemetryURL == "" && connectivityURL == "" && commandURL == "" {
		return nil, nil
	}
	buildClient := func(caEnv, defaultCA, serverName string, fallback *x509.CertPool) (*http.Client, error) {
		roots := fallback
		caPath := strings.TrimSpace(os.Getenv(caEnv))
		if caPath == "" {
			caPath = defaultCA
		}
		if caPath != "" {
			loaded, err := loadCertPool(caPath)
			if err != nil {
				return nil, err
			}
			roots = loaded
		}
		if roots == nil {
			return nil, fmt.Errorf("%s root CA is required", caEnv)
		}
		return &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: newRegistryHTTPTransport(&tls.Config{
				MinVersion:   tls.VersionTLS13,
				RootCAs:      roots,
				Certificates: []tls.Certificate{cert},
				ServerName:   serverName,
			}),
		}, nil
	}
	var telClient, connClient, cmdClient *http.Client
	if telemetryURL != "" {
		var err error
		telClient, err = buildClient("TELEMETRY_RUNTIME_SERVER_CA", "", envOr("TELEMETRY_RUNTIME_SERVER_NAME", "telemetry-runtime-service"), defaultRoots)
		if err != nil {
			return nil, err
		}
	}
	if connectivityURL != "" {
		var err error
		connClient, err = buildClient("CONNECTIVITY_SERVER_CA", "/run/hvac/gateway-ca/ca.crt", envOr("CONNECTIVITY_SERVICE_SERVER_NAME", "connectivity"), nil)
		if err != nil {
			return nil, err
		}
	}
	if commandURL != "" {
		var err error
		cmdClient, err = buildClient("COMMAND_SERVICE_SERVER_CA", "", envOr("COMMAND_SERVICE_SERVER_NAME", "command-service"), defaultRoots)
		if err != nil {
			return nil, err
		}
	}
	_ = ctx
	return core.NewOutboxRelay(store, core.OutboxRelayConfig{
		TelemetryBaseURL:       telemetryURL,
		TelemetryHTTPClient:    telClient,
		ConnectivityBaseURL:    connectivityURL,
		ConnectivityHTTPClient: connClient,
		CommandBaseURL:         commandURL,
		CommandHTTPClient:      cmdClient,
		Logger:                 logger,
	})
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
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
