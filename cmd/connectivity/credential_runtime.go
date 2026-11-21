package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/concentrator"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/mqttguard"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/tunnel"
	"github.com/quanlaihe/hvac-web/modules/registry/pkg/coreservice"
)

const onlineGatewayMetricHelp = "Number of gateways currently tracked as online by Connectivity."

type gatewayOnlineTracker struct {
	mu      sync.RWMutex
	online  map[string]bool
	metrics *observability.Registry
}

func newGatewayOnlineTracker() *gatewayOnlineTracker {
	return &gatewayOnlineTracker{online: make(map[string]bool)}
}

func newGatewayOnlineTrackerWithMetrics(metrics *observability.Registry) *gatewayOnlineTracker {
	tracker := &gatewayOnlineTracker{
		online:  make(map[string]bool),
		metrics: metrics,
	}
	tracker.publishCount(0)
	return tracker
}

func (t *gatewayOnlineTracker) SetOnline(gatewayID string, online bool) {
	if t == nil {
		return
	}
	id := strings.ToLower(strings.TrimSpace(gatewayID))
	if id == "" {
		return
	}
	t.mu.Lock()
	if online {
		t.online[id] = true
	} else {
		delete(t.online, id)
	}
	count := len(t.online)
	t.mu.Unlock()
	t.publishCount(count)
}

func (t *gatewayOnlineTracker) IsOnline(gatewayID string) bool {
	if t == nil {
		return false
	}
	id := strings.ToLower(strings.TrimSpace(gatewayID))
	if id == "" {
		return false
	}
	t.mu.RLock()
	online := t.online[id]
	t.mu.RUnlock()
	return online
}

func (t *gatewayOnlineTracker) Count() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	count := len(t.online)
	t.mu.RUnlock()
	return count
}

func (t *gatewayOnlineTracker) publishCount(count int) {
	if t == nil || t.metrics == nil {
		return
	}
	_ = t.metrics.SetGauge("hvac_mqtt_online_gateways", onlineGatewayMetricHelp, nil, float64(count))
}

func credentialServer(store *connectivity.Store) (*http.Server, *tunnel.Hub, error) {
	return credentialServerWithRuntime(store, nil, nil)
}

func credentialServerWithRuntime(store *connectivity.Store, concentratorService *concentrator.Service, coverageReporter adapter.CoverageReporter, tunnelOptions ...tunnel.Option) (*http.Server, *tunnel.Hub, error) {
	return credentialServerWithContext(context.Background(), slog.Default(), nil, nil, store, concentratorService, coverageReporter, tunnelOptions...)
}

func credentialServerWithContext(
	rootCtx context.Context,
	logger *slog.Logger,
	metrics *observability.Registry,
	presence *gatewayOnlineTracker,
	store *connectivity.Store,
	concentratorService *concentrator.Service,
	coverageReporter adapter.CoverageReporter,
	tunnelOptions ...tunnel.Option,
) (*http.Server, *tunnel.Hub, error) {
	return credentialServerWithDiagnostics(rootCtx, logger, metrics, presence, store, concentratorService, coverageReporter, nil, tunnelOptions...)
}

func credentialServerWithDiagnostics(
	rootCtx context.Context,
	logger *slog.Logger,
	metrics *observability.Registry,
	presence *gatewayOnlineTracker,
	store *connectivity.Store,
	concentratorService *concentrator.Service,
	coverageReporter adapter.CoverageReporter,
	tap *packettap.Manager,
	tunnelOptions ...tunnel.Option,
) (*http.Server, *tunnel.Hub, error) {
	srv, hub, _, err := credentialAndGuardServerWithDiagnostics(
		rootCtx, logger, metrics, presence, store, concentratorService, coverageReporter, tap, false, tunnelOptions...,
	)
	return srv, hub, err
}

func credentialAndGuardServerWithDiagnostics(
	rootCtx context.Context,
	logger *slog.Logger,
	metrics *observability.Registry,
	presence *gatewayOnlineTracker,
	store *connectivity.Store,
	concentratorService *concentrator.Service,
	coverageReporter adapter.CoverageReporter,
	tap *packettap.Manager,
	enableGuardFromEnv bool,
	tunnelOptions ...tunnel.Option,
) (*http.Server, *tunnel.Hub, *mqttguard.Server, error) {
	return credentialAndGuardServerWithSession(
		rootCtx, logger, metrics, presence, store, concentratorService, coverageReporter, tap, enableGuardFromEnv, nil, tunnelOptions...,
	)
}

func credentialAndGuardServerWithSession(
	rootCtx context.Context,
	logger *slog.Logger,
	metrics *observability.Registry,
	presence *gatewayOnlineTracker,
	store *connectivity.Store,
	concentratorService *concentrator.Service,
	coverageReporter adapter.CoverageReporter,
	tap *packettap.Manager,
	enableGuardFromEnv bool,
	mqttSession *mqttclient.SharedSession,
	tunnelOptions ...tunnel.Option,
) (*http.Server, *tunnel.Hub, *mqttguard.Server, error) {
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	loopbackAddr := strings.TrimSpace(envOr("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "127.0.0.1:18800"))
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if err := tunnel.ValidateLoopbackAddr(loopbackAddr); err != nil {
			return nil, nil, nil, err
		}
	}
	serverIdentity, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_SERVER_CERT", "/run/hvac/gateway-ca/connectivity.crt"), envOr("CONNECTIVITY_SERVER_KEY", "/run/hvac/gateway-ca/connectivity.key"))
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_TLS_CERT", "/run/hvac/pki/mqtt-telemetry-adapter/tls.crt"), envOr("CONNECTIVITY_TLS_KEY", "/run/hvac/pki/mqtt-telemetry-adapter/tls.key"))
	if err != nil {
		return nil, nil, nil, err
	}
	rootPEM, err := os.ReadFile(envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt"))
	if err != nil {
		return nil, nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return nil, nil, nil, errors.New("invalid workload CA")
	}
	caPEM, err := os.ReadFile(envOr("GATEWAY_CA_CERT", "/run/hvac/gateway-ca/ca.crt"))
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM, err := os.ReadFile(envOr("GATEWAY_CA_KEY", "/run/hvac/gateway-ca/ca.key"))
	if err != nil {
		return nil, nil, nil, err
	}
	authority, err := connectivity.NewCertificateAuthority(caPEM, keyPEM, envOr("GATEWAY_BROKER_URL", "tls://localhost:8883"))
	if err != nil {
		return nil, nil, nil, err
	}
	iamPEM, err := os.ReadFile(envOr("CONNECTIVITY_IAM_CERT", "/run/hvac/pki/iam/tls.crt"))
	if err != nil {
		return nil, nil, nil, err
	}
	block, _ := pem.Decode(iamPEM)
	if block == nil {
		return nil, nil, nil, errors.New("invalid IAM signing certificate")
	}
	iam, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	var iamTransport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		iamTransport = base.Clone()
	} else {
		iamTransport = &http.Transport{}
	}
	iamTransport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	iamTransport.MaxConnsPerHost = 16
	iamTransport.MaxIdleConns = 64
	iamTransport.MaxIdleConnsPerHost = 16
	iamTransport.IdleConnTimeout = 90 * time.Second
	iamTransport.ResponseHeaderTimeout = 10 * time.Second
	iamTransport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "iam-service"}
	client := &http.Client{Timeout: 3 * time.Second, Transport: iamTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status, err := coreservice.NewHTTPGrantStatusProvider(envOr("CONNECTIVITY_IAM_URL", "https://iam-service:8444"), client)
	if err != nil {
		return nil, nil, nil, err
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
	crlOutPath := strings.TrimSpace(os.Getenv("GATEWAY_CRL_OUTPUT_FILE"))
	var crlMu sync.Mutex
	refreshCRL := func() {
		if crlOutPath == "" || authority == nil || store == nil {
			return
		}
		crlMu.Lock()
		defer crlMu.Unlock()
		refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		crlPEM, crlErr := service.ExportCRL(refreshCtx)
		if crlErr != nil {
			if !errors.Is(crlErr, connectivity.ErrCRLSignUnsupported) {
				slog.Default().Warn("gateway_crl_export_failed", "error", crlErr.Error())
			}
			return
		}
		if writeErr := writeAtomicCRLFile(crlOutPath, crlPEM); writeErr != nil {
			slog.Default().Warn("gateway_crl_write_failed", "path", crlOutPath, "error", writeErr.Error())
		}
	}
	var mqttGuard *mqttguard.Server
	if enableGuardFromEnv {
		if guardAddr := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_ADDR")); guardAddr != "" {
			if authority == nil {
				return nil, nil, nil, errors.New("mqttguard requires gateway CA authority")
			}
			guardCert := serverIdentity
			if certPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_CERT")); certPath != "" {
				keyPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_KEY"))
				if keyPath == "" {
					return nil, nil, nil, errors.New("CONNECTIVITY_MQTT_GUARD_KEY is required when CONNECTIVITY_MQTT_GUARD_CERT is set")
				}
				loaded, loadErr := tls.LoadX509KeyPair(certPath, keyPath)
				if loadErr != nil {
					return nil, nil, nil, loadErr
				}
				guardCert = loaded
			}
			gatewayClientCAs := x509.NewCertPool()
			if !gatewayClientCAs.AppendCertsFromPEM(caPEM) {
				return nil, nil, nil, errors.New("parse gateway CA for mqttguard")
			}
			upstreamRoots := x509.NewCertPool()
			if !upstreamRoots.AppendCertsFromPEM(rootPEM) {
				return nil, nil, nil, errors.New("parse upstream CA for mqttguard")
			}
			if upstreamCAPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_UPSTREAM_CA")); upstreamCAPath != "" {
				extraPEM, readErr := os.ReadFile(upstreamCAPath)
				if readErr != nil {
					return nil, nil, nil, readErr
				}
				if !upstreamRoots.AppendCertsFromPEM(extraPEM) {
					return nil, nil, nil, errors.New("parse CONNECTIVITY_MQTT_GUARD_UPSTREAM_CA")
				}
			}
			var guardErr error
			mqttGuard, guardErr = mqttguard.New(mqttguard.Config{
				ListenAddr:         guardAddr,
				ServerCertificate:  guardCert,
				GatewayClientCAs:   gatewayClientCAs,
				UpstreamAddr:       envOr("CONNECTIVITY_MQTT_GUARD_UPSTREAM_ADDR", "mqtt-broker:8883"),
				UpstreamRootCAs:    upstreamRoots,
				UpstreamServerName: envOr("CONNECTIVITY_MQTT_SERVER_NAME", "mqtt-broker"),
				Verifier:           service,
				Issuer:             authority,
				Logger:             logger,
				Metrics:            metrics,
			})
			if guardErr != nil {
				return nil, nil, nil, guardErr
			}
		}
	}

	service.SetRevokeHook(func(gatewayID string) {
		if presence != nil {
			presence.SetOnline(gatewayID, false)
		}
		tunnelHub.CloseGateway(gatewayID)
		if mqttGuard != nil {
			mqttGuard.CloseGateway(gatewayID)
		}
		reportGatewayRevokedCoverage(store, coverageReporter, gatewayID)
		refreshCRL()
	})
	service.SetCertificateRevokeHook(func(gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
		if !hasRemainingActive && presence != nil {
			presence.SetOnline(gatewayID, false)
		}
		applyCertificateRevokeHook(tunnelHub, store, coverageReporter, gatewayID, fingerprintSHA256, hasRemainingActive)
		if mqttGuard != nil {
			mqttGuard.CloseGatewayByFingerprint(gatewayID, fingerprintSHA256)
		}
		refreshCRL()
	})
	if crlOutPath != "" && authority != nil && store != nil {
		observability.GoSafe(logger, metrics, "connectivity.crl_startup_refresh", refreshCRL)
	}
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if _, err := tunnelHub.StartLoopbackListener(loopbackAddr); err != nil {
			tunnelHub.Close()
			return nil, nil, nil, err
		}
	}
	tunnelHandler := tunnel.NewUpgradeHandler(tunnelHub, connectivity.ParseGatewayPeerCert, service.VerifyActiveCredential)
	var concentratorHandler http.Handler
	if concentratorService != nil {
		concentratorHandler = concentrator.NewHTTPHandler(concentratorService, authorize)
	}
	var onDesiredUpdated func(gatewayID string)
	if concentratorService != nil || (store != nil && mqttSession != nil) {
		onDesiredUpdated = func(gatewayID string) {
			if store != nil && mqttSession != nil {
				observability.GoSafe(logger, metrics, "connectivity.shadow_replication_publish", func() {
					publishGatewayDesiredShadow(rootCtx, store, mqttSession, logger, metrics, tap, gatewayID)
				})
			}
			if concentratorService != nil {
				if presence != nil && !presence.IsOnline(gatewayID) {
					return
				}
				observability.GoSafe(logger, metrics, "connectivity.concentrator_shadow_reconcile", func() {
					reconcileConcentratorShadowAsync(rootCtx, concentratorService, logger, gatewayID)
				})
			}
		}
	}
	var configShadowHandler http.Handler
	var diagnosticsHandler http.Handler
	if store != nil {
		var onGatewaySeen func(string)
		if presence != nil {
			onGatewaySeen = func(gatewayID string) {
				presence.SetOnline(gatewayID, true)
			}
		}
		configShadowHandler = connectivity.NewConfigShadowHandlerWithPresence(store, service, authorize, onDesiredUpdated, onGatewaySeen)
		diagnosticsHandler = connectivity.NewDiagnosticsHandler(store, tap, authorize)
	}
	return &http.Server{
		Addr:    envOr("CONNECTIVITY_CREDENTIAL_ADDR", ":8448"),
		Handler: connectivity.NewCredentialHandlerWithDiagnostics(service, authorize, tunnelHandler, concentratorHandler, configShadowHandler, diagnosticsHandler),
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
	}, tunnelHub, mqttGuard, nil
}

type shadowDesiredPuller interface {
	PullGatewayDesiredConfig(ctx context.Context, gatewayID string, sinceRevision int64) (connectivity.GatewayShadowPullResponse, error)
}

type shadowMQTTPublisher interface {
	Publish(ctx context.Context, pub *paho.Publish) (*paho.PublishResponse, error)
}

func recordShadowPublishMetric(metrics *observability.Registry, outcome string) {
	if metrics == nil {
		return
	}
	_ = metrics.AddCounter(adapter.ShadowReplicationMetricName, adapter.ShadowReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func publishGatewayDesiredShadow(
	rootCtx context.Context,
	puller shadowDesiredPuller,
	publisher shadowMQTTPublisher,
	logger *slog.Logger,
	metrics *observability.Registry,
	tap *packettap.Manager,
	gatewayID string,
) {
	if puller == nil || publisher == nil {
		return
	}
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" {
		return
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer cancel()

	pullResp, err := puller.PullGatewayDesiredConfig(ctx, gwID, 0)
	if err != nil {
		recordShadowPublishMetric(metrics, "failed")
		if logger != nil {
			logger.Warn("mqtt_shadow_pull_failed", "gateway_id", gwID, "error", err.Error())
		}
		return
	}
	if pullResp.Desired == nil || pullResp.DesiredRevision < 1 {
		return
	}
	wireBytes, msgID, err := adapter.EncodeShadowChangeBatch(gwID, pullResp.DesiredRevision, pullResp.Desired, time.Now().UTC())
	if err != nil {
		recordShadowPublishMetric(metrics, "failed")
		if logger != nil {
			logger.Warn("mqtt_shadow_encode_failed", "gateway_id", gwID, "error", err.Error())
		}
		return
	}
	topic := adapter.ShadowDownlinkTopic(gwID)
	if tap != nil {
		tap.Record("DOWNLINK", "SHADOW", gwID, topic, msgID, wireBytes)
	}
	if _, err := publisher.Publish(ctx, &paho.Publish{
		Topic:   topic,
		QoS:     1,
		Payload: wireBytes,
	}); err != nil {
		recordShadowPublishMetric(metrics, "failed")
		if logger != nil {
			logger.Warn("mqtt_shadow_publish_failed", "gateway_id", gwID, "topic", topic, "error", err.Error())
		}
		return
	}
	recordShadowPublishMetric(metrics, "success")
}

type gatewayChildLister interface {
	ListGatewayChildDeviceIDs(ctx context.Context, gatewayID string) ([]string, error)
}

func applyCertificateRevokeHook(hub *tunnel.Hub, lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
	if hub != nil {
		hub.CloseGatewayByFingerprint(gatewayID, fingerprintSHA256)
	}
	if !hasRemainingActive {
		reportGatewayRevokedCoverageWithLister(lister, reporter, gatewayID)
	}
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

func writeAtomicCRLFile(targetPath string, content []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gateway-ca-*.crl.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}
