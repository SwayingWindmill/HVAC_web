package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
	"github.com/quanlaihe/hvac-web/modules/registry/pkg/coreservice"
)

func credentialServer(store *connectivity.Store) (*http.Server, *tunnel.Hub, error) {
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
	tunnelHub := tunnel.NewHub(tunnel.WithDefaultGateway(envOr("CONNECTIVITY_TUNNEL_GATEWAY_ID", "")))
	service := connectivity.NewCredentialService(store, authority)
	service.SetRevokeHook(tunnelHub.CloseGateway)
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if _, err := tunnelHub.StartLoopbackListener(loopbackAddr); err != nil {
			tunnelHub.Close()
			return nil, nil, err
		}
	}
	tunnelHandler := tunnel.NewUpgradeHandler(tunnelHub, connectivity.ParseGatewayPeerCert, service.VerifyActiveCredential)
	return &http.Server{
		Addr:    envOr("CONNECTIVITY_CREDENTIAL_ADDR", ":8448"),
		Handler: connectivity.NewCredentialHandlerWithTunnel(service, authorize, tunnelHandler),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{serverIdentity},
			ClientCAs:    roots,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			NextProtos:   []string{"http/1.1"},
		},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}, tunnelHub, nil
}
