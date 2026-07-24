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

	sourceAuthenticator, err := telemetry.ParseSourceAuthenticatorJSON(requiredEnv("TELEMETRY_SOURCE_BINDINGS_JSON"))
	if err != nil {
		logger.Error("telemetry_source_bindings_invalid", "error_code", "TELEMETRY_SOURCE_BINDINGS_INVALID")
		os.Exit(1)
	}

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

	realtimeService, realtimeContext, realtimeCancel, err := loadRealtimeService(store, certificate)
	if err != nil {
		logger.Error("telemetry_realtime_configuration_invalid", "error_code", "TELEMETRY_REALTIME_CONFIGURATION_INVALID")
		os.Exit(1)
	}
	if realtimeCancel != nil {
		defer realtimeCancel()
		go runRealtimeRelay(realtimeContext, realtimeService, logger)
	}

	server := &http.Server{
		Addr: envOr("TELEMETRY_SERVICE_ADDR", "127.0.0.1:18446"),
		Handler: telemetry.NewHandler(telemetry.ServerConfig{
			Store: store, Authorizer: authorizer,
			AllowedGatewaySPIFFE: envOr("TELEMETRY_ALLOWED_GATEWAY_SPIFFE", "spiffe://hvac.local/platform-gateway"),
			RuntimeAudience:      envOr("TELEMETRY_GRANT_AUDIENCE", "telemetry-runtime-service"),
			ObservationAcceptor:  store, CoverageReporter: store, SourceAuthenticator: sourceAuthenticator,
			Realtime:                realtimeService,
			AllowedCentrifugoSPIFFE: envOr("TELEMETRY_ALLOWED_CENTRIFUGO_SPIFFE", "spiffe://hvac.local/centrifugo"),
			CentrifugoProxySecret:   strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_PROXY_SECRET")),
			AllowedIAMSPIFFE:        envOr("TELEMETRY_ALLOWED_IAM_SPIFFE", "spiffe://hvac.local/iam-service"),
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

func loadRealtimeService(store *telemetry.PostgresStore, certificate tls.Certificate) (*telemetry.RealtimeService, context.Context, context.CancelFunc, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_REALTIME_ENABLED")), "true") {
		return nil, context.Background(), nil, nil
	}
	centrifugoCAs, err := loadCertPool(requiredEnv("TELEMETRY_CENTRIFUGO_CA"))
	if err != nil {
		return nil, nil, nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      centrifugoCAs,
			Certificates: []tls.Certificate{certificate},
			ServerName:   strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_SERVER_NAME")),
		},
		DisableCompression: true,
		ForceAttemptHTTP2:  false,
	}
	centrifugo, err := telemetry.NewCentrifugoTransport(telemetry.CentrifugoTransportConfig{
		BaseURL:    requiredEnv("TELEMETRY_CENTRIFUGO_API_URL"),
		APIKey:     requiredEnv("TELEMETRY_CENTRIFUGO_API_KEY"),
		HTTPClient: telemetry.NewBoundedCentrifugoHTTPClient(transport),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	service, err := telemetry.NewRealtimeService(telemetry.RealtimeConfig{
		Repository:             store,
		Transport:              centrifugo,
		PublicEndpoint:         requiredEnv("TELEMETRY_REALTIME_ENDPOINT"),
		CapabilityHMACKey:      []byte(requiredEnv("TELEMETRY_REALTIME_CAPABILITY_HMAC_KEY")),
		ConnectionTokenHMACKey: []byte(requiredEnv("TELEMETRY_CENTRIFUGO_TOKEN_HMAC_KEY")),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if len(strings.TrimSpace(os.Getenv("TELEMETRY_CENTRIFUGO_PROXY_SECRET"))) < 32 {
		return nil, nil, nil, errors.New("Centrifugo subscribe proxy secret must be at least 32 bytes")
	}
	relayContext, relayCancel := context.WithCancel(context.Background())
	return service, relayContext, relayCancel, nil
}

func runRealtimeRelay(ctx context.Context, service *telemetry.RealtimeService, logger *slog.Logger) {
	if service == nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastFailureLog := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			published, err := service.RelayOnce(ctx, 64)
			if err != nil {
				if time.Since(lastFailureLog) >= time.Second {
					logger.Warn("telemetry_realtime_relay_failed", "error_code", "TELEMETRY_REALTIME_RELAY_FAILED")
					lastFailureLog = time.Now()
				}
				continue
			}
			if published > 0 {
				logger.Info("telemetry_realtime_relay_batch_published", "publication_count", published)
			}
		}
	}
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
