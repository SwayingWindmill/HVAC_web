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

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/gateway"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

type routingRuntime struct {
	manager  *ownershipregistry.Manager
	audit    ownershipregistry.AuditSink
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
	snapshot, err := parseActiveRouteRegistry(initialBytes)
	if err != nil {
		return routingRuntime{}, err
	}

	dsn := os.Getenv("GATEWAY_DATABASE_URL")
	if dsn == "" {
		return routingRuntime{}, errors.New("GATEWAY_DATABASE_URL is required")
	}
	postgresAudit, err := ownershipregistry.OpenPostgresAudit(ctx, dsn)
	if err != nil {
		return routingRuntime{}, errors.New("durable route audit store is unavailable")
	}
	if err := postgresAudit.VerifyProductSchema(ctx); err != nil {
		postgresAudit.Close()
		return routingRuntime{}, fmt.Errorf("durable route audit store schema incompatible: %w", err)
	}
	var audit ownershipregistry.AuditSink = postgresAudit
	closeAudit := postgresAudit.Close

	manager := ownershipregistry.NewManager(snapshot, audit, time.Now)
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
				if _, err := parseActiveRouteRegistry(candidate); err != nil {
					logger.Warn("route_registry_reload_rejected", "error_code", "ROUTE_REGISTRY_RELOAD_REJECTED")
					last = append(last[:0], candidate...)
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
	return routingRuntime{manager: manager, audit: audit, registry: registry, close: closeAudit, watch: watch}, nil
}

func parseActiveRouteRegistry(raw []byte) (*ownershipregistry.Snapshot, error) {
	snapshot, err := ownershipregistry.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("validate Route Ownership Registry: %w", err)
	}
	return snapshot, nil
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
		if !snapshot.ContainsOwner(ownershipregistry.OwnerCore) {
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
	roots, err := workloadtls.LoadCertPool(coreCAPath)
	if err != nil {
		return nil, err
	}
	return &gateway.RegistryConfig{
		CoreBaseURL:      coreURL,
		CoreHTTPClient:   nonRedirectingClient(workloadTransport(roots, &certificate, envOr("CORE_SERVER_NAME", "localhost"))),
		CoreTimeout:      durationEnv("CORE_REGISTRY_TIMEOUT", 750*time.Millisecond),
		MaxResponseBytes: int64(intEnv("REGISTRY_MAX_RESPONSE_BYTES", 2<<20)),
	}, nil
}

func nonRedirectingClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   10 * time.Second,
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
