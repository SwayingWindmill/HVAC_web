package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
)

type routingRuntime struct {
	manager  *ownershipregistry.Manager
	audit    ownershipregistry.AuditSink
	legacy   *gateway.LegacyConfig
	registry *gateway.RegistryConfig
	close    func()
	watch    func(context.Context)
}

func loadRoutingRuntime(ctx context.Context, logger *slog.Logger, identityEnabled bool) (routingRuntime, error) {
	registryPath := envOr("ROUTE_OWNERSHIP_REGISTRY", "contracts/ownership/route-ownership.v1.json")
	initialBytes, err := os.ReadFile(registryPath)
	if err != nil {
		return routingRuntime{}, fmt.Errorf("read Route Ownership Registry: %w", err)
	}
	snapshot, err := ownershipregistry.Parse(initialBytes)
	if err != nil {
		return routingRuntime{}, fmt.Errorf("validate Route Ownership Registry: %w", err)
	}

	var audit ownershipregistry.AuditSink
	closeAudit := func() {}
	if dsn := os.Getenv("GATEWAY_DATABASE_URL"); dsn != "" {
		postgresAudit, err := ownershipregistry.OpenPostgresAudit(ctx, dsn)
		if err != nil {
			return routingRuntime{}, errors.New("durable route audit store is unavailable")
		}
		audit = postgresAudit
		closeAudit = postgresAudit.Close
	} else if os.Getenv("S0_ALLOW_MEMORY_ROUTE_AUDIT") == "true" {
		audit = ownershipregistry.NewMemoryAuditSink()
	} else {
		return routingRuntime{}, errors.New("GATEWAY_DATABASE_URL is required unless S0_ALLOW_MEMORY_ROUTE_AUDIT=true")
	}

	manager := ownershipregistry.NewManager(snapshot, audit, time.Now)
	legacy, err := loadLegacyConfig(snapshot, identityEnabled)
	if err != nil {
		closeAudit()
		return routingRuntime{}, err
	}
	registry, err := loadRegistryConfig(snapshot, identityEnabled)
	if err != nil {
		closeAudit()
		return routingRuntime{}, err
	}

	watch := func(watchContext context.Context) {
		last := append([]byte(nil), initialBytes...)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchContext.Done():
				return
			case <-ticker.C:
				candidate, err := os.ReadFile(registryPath)
				if err != nil || bytes.Equal(candidate, last) {
					continue
				}
				if err := manager.Reload(watchContext, candidate, ownershipregistry.PolicyChangeContext{
					ExecutingService:  "platform-gateway",
					ExecutingSPIFFEID: envOr("GATEWAY_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway"),
				}); err != nil {
					logger.Warn("route_registry_reload_rejected", "error_code", "ROUTE_REGISTRY_RELOAD_REJECTED")
					last = append(last[:0], candidate...)
					continue
				}
				last = append(last[:0], candidate...)
				logger.Info("route_registry_reloaded", "revision", manager.Current().RegistryRevision())
			}
		}
	}
	return routingRuntime{manager: manager, audit: audit, legacy: legacy, registry: registry, close: closeAudit, watch: watch}, nil
}

func loadLegacyConfig(snapshot *ownershipregistry.Snapshot, identityEnabled bool) (*gateway.LegacyConfig, error) {
	if !identityEnabled {
		return nil, nil
	}
	legacyURL := os.Getenv("LEGACY_URL")
	legacyCAPath := os.Getenv("LEGACY_SERVER_CA")
	clientCertPath := os.Getenv("IAM_CLIENT_CERT")
	clientKeyPath := os.Getenv("IAM_CLIENT_KEY")
	configured := legacyURL != "" && legacyCAPath != "" && clientCertPath != "" && clientKeyPath != ""
	if !configured {
		if !snapshot.ContainsOwner(ownershipregistry.OwnerLegacy) || os.Getenv("S0_ALLOW_NO_LEGACY") == "true" {
			return nil, nil
		}
		return nil, errors.New("LEGACY_URL, LEGACY_SERVER_CA and Gateway workload certificate are required for Legacy-owned routes")
	}
	if err := validatePrivateServiceURL(legacyURL, "LEGACY_URL"); err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, err
	}
	roots, err := loadCertPool(legacyCAPath, "Legacy server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.LegacyConfig{
		BaseURL:          legacyURL,
		Audience:         envOr("LEGACY_AUDIENCE", "legacy-hvac-backend"),
		HTTPClient:       nonRedirectingClient(workloadTransport(roots, &certificate, envOr("LEGACY_SERVER_NAME", "localhost"))),
		Timeout:          durationEnv("LEGACY_TIMEOUT", 750*time.Millisecond),
		FailureThreshold: intEnv("LEGACY_CIRCUIT_FAILURES", 2),
		OpenDuration:     durationEnv("LEGACY_CIRCUIT_OPEN", 5*time.Second),
	}, nil
}

func loadRegistryConfig(snapshot *ownershipregistry.Snapshot, identityEnabled bool) (*gateway.RegistryConfig, error) {
	if !identityEnabled {
		return nil, nil
	}
	coreURL := os.Getenv("CORE_URL")
	coreCAPath := os.Getenv("CORE_SERVER_CA")
	clientCertPath := os.Getenv("IAM_CLIENT_CERT")
	clientKeyPath := os.Getenv("IAM_CLIENT_KEY")
	configured := coreURL != "" && coreCAPath != "" && clientCertPath != "" && clientKeyPath != ""
	if !configured {
		if !snapshot.ContainsOwner(ownershipregistry.OwnerCore) || os.Getenv("S1_ALLOW_NO_CORE") == "true" {
			return nil, nil
		}
		return nil, errors.New("CORE_URL, CORE_SERVER_CA and Gateway workload certificate are required for Core Registry routes")
	}
	if err := validatePrivateServiceURL(coreURL, "CORE_URL"); err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, err
	}
	roots, err := loadCertPool(coreCAPath, "Core server CA")
	if err != nil {
		return nil, err
	}
	return &gateway.RegistryConfig{
		CoreBaseURL:         coreURL,
		CoreHTTPClient:      nonRedirectingClient(workloadTransport(roots, &certificate, envOr("CORE_SERVER_NAME", "localhost"))),
		CoreTimeout:         durationEnv("CORE_REGISTRY_TIMEOUT", 750*time.Millisecond),
		ShadowTimeout:       durationEnv("REGISTRY_SHADOW_TIMEOUT", 500*time.Millisecond),
		MaxResponseBytes:    int64(intEnv("REGISTRY_MAX_RESPONSE_BYTES", 2<<20)),
		MaxShadowConcurrent: intEnv("REGISTRY_MAX_SHADOW_CONCURRENT", 32),
	}, nil
}

func nonRedirectingClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validatePrivateServiceURL(value, name string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an absolute HTTPS origin without credentials, path, query or fragment", name)
	}
	return nil
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func intEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	var result int
	if _, err := fmt.Sscanf(value, "%d", &result); err != nil || result <= 0 {
		return fallback
	}
	return result
}
