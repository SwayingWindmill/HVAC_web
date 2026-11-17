package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/concentrator"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
	"github.com/quanlaihe/hvac-web/modules/registry/pkg/coreservice"
)

func credentialServer(store *connectivity.Store) (*http.Server, *tunnel.Hub, error) {
	return credentialServerWithRuntime(store, nil, nil)
}

func credentialServerWithRuntime(store *connectivity.Store, concentratorService *concentrator.Service, coverageReporter adapter.CoverageReporter, tunnelOptions ...tunnel.Option) (*http.Server, *tunnel.Hub, error) {
	loopbackAddr := strings.TrimSpace(envOr("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "127.0.0.1:18800"))
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if err := tunnel.ValidateLoopbackAddr(loopbackAddr); err != nil {
			return nil, nil, err
		}
	}
	serverIdentity, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_SERVER_CERT", "/run/hvac/gateway-ca/connectivity.crt"), envOr("CONNECTIVITY_SERVER_KEY", "/run/hvac/gateway-ca/connectivity.key"))
	if err != nil {
		return nil, nil, err
	}
	cert, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_TLS_CERT", "/run/hvac/pki/mqtt-telemetry-adapter/tls.crt"), envOr("CONNECTIVITY_TLS_KEY", "/run/hvac/pki/mqtt-telemetry-adapter/tls.key"))
	if err != nil {
		return nil, nil, err
	}
	rootPEM, err := os.ReadFile(envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return nil, nil, errors.New("invalid workload CA")
	}
	caPEM, err := os.ReadFile(envOr("GATEWAY_CA_CERT", "/run/hvac/gateway-ca/ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(envOr("GATEWAY_CA_KEY", "/run/hvac/gateway-ca/ca.key"))
	if err != nil {
		return nil, nil, err
	}
	authority, err := connectivity.NewCertificateAuthority(caPEM, keyPEM, envOr("GATEWAY_BROKER_URL", "tls://localhost:8883"))
	if err != nil {
		return nil, nil, err
	}
	iamPEM, err := os.ReadFile(envOr("CONNECTIVITY_IAM_CERT", "/run/hvac/pki/iam/tls.crt"))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(iamPEM)
	if block == nil {
		return nil, nil, errors.New("invalid IAM signing certificate")
	}
	iam, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "iam-service"}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status, err := coreservice.NewHTTPGrantStatusProvider(envOr("CONNECTIVITY_IAM_URL", "https://iam-service:8444"), client)
	if err != nil {
		return nil, nil, err
	}
	authorize := func(r *http.Request, action registryauth.Action) (registryauth.GrantClaims, error) {
		claims, err := registryauth.VerifyGrant(iam.PublicKey, r.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return claims, connectivity.ErrCredentialForbidden
		}
		validation := registryauth.GrantValidation{Now: time.Now(), Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service", Action: action, CurrentPolicyRevision: claims.PolicyRevision, IsRevoked: func(string) (bool, error) { return false, nil }}
		if err = registryauth.ValidateGrant(claims, validation); err != nil {
			return claims, connectivity.ErrCredentialForbidden
		}
		current, err := status.Lookup(r.Context(), claims)
		if err != nil {
			return claims, err
		}
		validation.CurrentPolicyRevision = current.CurrentPolicyRevision
		validation.IsRevoked = func(string) (bool, error) { return current.Revoked, nil }
		if err = registryauth.ValidateGrant(claims, validation); err != nil {
			return claims, connectivity.ErrCredentialForbidden
		}
		return claims, nil
	}
	tunnelHubOpts := append([]tunnel.Option{
		tunnel.WithDefaultGateway(envOr("CONNECTIVITY_TUNNEL_GATEWAY_ID", "")),
		tunnel.WithAuditSink(store),
	}, tunnelOptions...)
	tunnelHub := tunnel.NewHub(tunnelHubOpts...)
	service := connectivity.NewCredentialService(store, authority)
	service.SetRevokeHook(func(gatewayID string) {
		tunnelHub.CloseGateway(gatewayID)
		reportGatewayRevokedCoverage(store, coverageReporter, gatewayID)
	})
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if _, err := tunnelHub.StartLoopbackListener(loopbackAddr); err != nil {
			tunnelHub.Close()
			return nil, nil, err
		}
	}
	tunnelHandler := tunnel.NewUpgradeHandler(tunnelHub, connectivity.ParseGatewayPeerCert, service.VerifyActiveCredential)
	var concentratorHandler http.Handler
	if concentratorService != nil {
		concentratorHandler = concentrator.NewHTTPHandler(concentratorService, authorize)
	}
	return &http.Server{
		Addr:    envOr("CONNECTIVITY_CREDENTIAL_ADDR", ":8448"),
		Handler: connectivity.NewCredentialHandlerWithExtensions(service, authorize, tunnelHandler, concentratorHandler),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{serverIdentity},
			ClientCAs:    roots,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			NextProtos:   []string{"http/1.1"},
		},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}, tunnelHub, nil
}

type gatewayChildLister interface {
	ListGatewayChildDeviceIDs(ctx context.Context, gatewayID string) ([]string, error)
}

func reportGatewayRevokedCoverage(store *connectivity.Store, reporter adapter.CoverageReporter, gatewayID string) {
	if store == nil {
		return
	}
	reportGatewayRevokedCoverageWithLister(store, reporter, gatewayID)
}

func reportGatewayRevokedCoverageWithLister(lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID string) {
	if lister == nil || reporter == nil {
		return
	}
	trimmedGatewayID := strings.ToLower(strings.TrimSpace(gatewayID))
	if trimmedGatewayID == "" {
		return
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deviceIDs, err := lister.ListGatewayChildDeviceIDs(reportCtx, trimmedGatewayID)
	if err != nil {
		return
	}
	if isUUIDv7(trimmedGatewayID) {
		seen := false
		for _, id := range deviceIDs {
			if strings.EqualFold(id, trimmedGatewayID) {
				seen = true
				break
			}
		}
		if !seen {
			deviceIDs = append(deviceIDs, trimmedGatewayID)
		}
	}
	if len(deviceIDs) == 0 {
		return
	}
	reason := "SOURCE_UNAVAILABLE"
	now := time.Now().UTC()
	observedAt := now.Format(time.RFC3339Nano)
	sourceRev := max(now.UnixNano(), 1)
	for start := 0; start < len(deviceIDs); start += adapter.MaximumCoverageBatchSize {
		end := min(start+adapter.MaximumCoverageBatchSize, len(deviceIDs))
		_ = reporter.ReportSourceCoverage(reportCtx, adapter.SourceCoverageReport{
			SourceID:       trimmedGatewayID,
			DeviceIDs:      deviceIDs[start:end],
			Available:      false,
			ReasonCode:     &reason,
			ObservedAt:     observedAt,
			SourceRevision: sourceRev,
		})
	}
}

func isUUIDv7(raw string) bool {
	if len(raw) != 36 || raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' || raw[14] != '7' {
		return false
	}
	if raw[19] != '8' && raw[19] != '9' && raw[19] != 'a' && raw[19] != 'b' {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := raw[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
