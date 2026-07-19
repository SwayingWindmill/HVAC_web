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

	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	address := envOr("PLATFORM_GATEWAY_ADDR", ":8080")
	identity, err := loadIdentityConfig()
	if err != nil {
		logger.Error("gateway_identity_config_invalid", "error", err)
		os.Exit(1)
	}

	handler := gateway.NewHandler(gateway.Config{
		Logger:   logger,
		Identity: identity,
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

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdownSignal
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("gateway_shutdown_failed", "error", err)
		}
	}()

	logger.Info("gateway_started", "service", "platform-gateway", "address", address, "version", version, "commit", commit, "identity_enabled", identity != nil)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway_stopped_unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway_stopped", "service", "platform-gateway")
}

func loadIdentityConfig() (*gateway.IdentityConfig, error) {
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		return nil, nil
	}
	required := map[string]string{}
	for _, name := range []string{"OIDC_CLIENT_ID", "OIDC_REDIRECT_URI", "PLATFORM_PUBLIC_ORIGIN", "IAM_URL", "IAM_CLIENT_CERT", "IAM_CLIENT_KEY", "IAM_SERVER_CA"} {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s is required when OIDC_ISSUER is configured", name)
		}
		required[name] = value
	}
	certificate, err := tls.LoadX509KeyPair(required["IAM_CLIENT_CERT"], required["IAM_CLIENT_KEY"])
	if err != nil {
		return nil, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("IAM client private key cannot sign delegation grants")
	}
	caPEM, err := os.ReadFile(required["IAM_SERVER_CA"])
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("IAM server CA is invalid")
	}
	oidcClient := &http.Client{Timeout: 5 * time.Second}
	if oidcCAPath := os.Getenv("OIDC_SERVER_CA"); oidcCAPath != "" {
		oidcCAPEM, err := os.ReadFile(oidcCAPath)
		if err != nil {
			return nil, err
		}
		oidcRoots := x509.NewCertPool()
		if !oidcRoots.AppendCertsFromPEM(oidcCAPEM) {
			return nil, errors.New("OIDC server CA is invalid")
		}
		oidcClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    oidcRoots,
			ServerName: envOr("OIDC_SERVER_NAME", "localhost"),
		}}
	}
	key, err := sessionEncryptionKey()
	if err != nil {
		return nil, err
	}
	return &gateway.IdentityConfig{
		OIDCIssuer:              issuer,
		OIDCClientID:            required["OIDC_CLIENT_ID"],
		OIDCRedirectURI:         required["OIDC_REDIRECT_URI"],
		PublicOrigin:            required["PLATFORM_PUBLIC_ORIGIN"],
		IAMURL:                  required["IAM_URL"],
		IAMAudience:             envOr("IAM_AUDIENCE", "iam-service"),
		ExecutingWorkloadSPIFFE: envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
		PolicyRevision:          envOr("IDENTITY_POLICY_REVISION", "policy-v1"),
		DelegationSigner:        signer,
		TokenEncryptionKey:      key,
		SessionTTL:              30 * time.Minute,
		StateTTL:                2 * time.Minute,
		DelegationTTL:           30 * time.Second,
		RevocationObjective:     time.Second,
		IAMHTTPClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS13,
				RootCAs:      roots,
				Certificates: []tls.Certificate{certificate},
				ServerName:   envOr("IAM_SERVER_NAME", "localhost"),
			}},
		},
		OIDCHTTPClient: oidcClient,
	}, nil
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
