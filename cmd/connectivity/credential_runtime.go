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
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
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

type credentialRuntimeConfig struct {
	RootCtx             context.Context
	Logger              *slog.Logger
	Metrics             *observability.Registry
	Presence            *gatewayOnlineTracker
	Store               *connectivity.Store
	ConcentratorService *concentrator.Service
	CoverageReporter    adapter.CoverageReporter
	Tap                 *packettap.Manager
	EnableGuardFromEnv  bool
	MQTTSession         *mqttclient.SharedSession
	TunnelOptions       []tunnel.Option
	OTAStorage          *connectivity.OTAArtifactStorage
	OTASigner           *connectivity.OTASignerVerifier
	OTAPublicBaseURL    string
}

func credentialServer(store *connectivity.Store) (*http.Server, *tunnel.Hub, error) {
	srv, hub, _, _, err := newCredentialRuntime(credentialRuntimeConfig{Store: store})
	return srv, hub, err
}

func newCredentialRuntime(cfg credentialRuntimeConfig) (*http.Server, *tunnel.Hub, *mqttguard.Server, *crlRuntime, error) {
	rootCtx := cfg.RootCtx
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	metrics := cfg.Metrics
	presence := cfg.Presence
	store := cfg.Store
	concentratorService := cfg.ConcentratorService
	coverageReporter := cfg.CoverageReporter
	tap := cfg.Tap
	enableGuardFromEnv := cfg.EnableGuardFromEnv
	mqttSession := cfg.MQTTSession
	tunnelOptions := cfg.TunnelOptions
	loopbackAddr := strings.TrimSpace(envOr("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "127.0.0.1:18800"))
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if err := tunnel.ValidateLoopbackAddr(loopbackAddr); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	sshLoopbackAddr := strings.TrimSpace(envOr("CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR", ""))
	sshLoopbackEnabled := sshLoopbackAddr != "" && !strings.EqualFold(sshLoopbackAddr, "off")
	if sshLoopbackEnabled {
		if err := tunnel.ValidateLoopbackAddr(sshLoopbackAddr); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	serverIdentity, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_SERVER_CERT", "/run/hvac/gateway-ca/connectivity.crt"), envOr("CONNECTIVITY_SERVER_KEY", "/run/hvac/gateway-ca/connectivity.key"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cert, err := tls.LoadX509KeyPair(envOr("CONNECTIVITY_TLS_CERT", "/run/hvac/pki/mqtt-telemetry-adapter/tls.crt"), envOr("CONNECTIVITY_TLS_KEY", "/run/hvac/pki/mqtt-telemetry-adapter/tls.key"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	rootPEM, err := os.ReadFile(envOr("CONNECTIVITY_CA", "/run/hvac/pki/ca.crt"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return nil, nil, nil, nil, errors.New("invalid workload CA")
	}
	caPEM, err := os.ReadFile(envOr("GATEWAY_CA_CERT", "/run/hvac/gateway-ca/ca.crt"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	keyPEM, err := os.ReadFile(envOr("GATEWAY_CA_KEY", "/run/hvac/gateway-ca/ca.key"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	authority, err := connectivity.NewCertificateAuthority(caPEM, keyPEM, envOr("GATEWAY_BROKER_URL", "tls://localhost:8883"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	iamPEM, err := os.ReadFile(envOr("CONNECTIVITY_IAM_CERT", "/run/hvac/pki/iam/tls.crt"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	block, _ := pem.Decode(iamPEM)
	if block == nil {
		return nil, nil, nil, nil, errors.New("invalid IAM signing certificate")
	}
	iam, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, err
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
	if sshLoopbackEnabled {
		tunnelHubOpts = append(tunnelHubOpts, tunnel.WithAllowedTargetPorts(tunnel.AllowedTargetPort, tunnel.SSHTargetPort))
	}
	tunnelHub := tunnel.NewHub(tunnelHubOpts...)
	service := connectivity.NewCredentialService(store, authority)
	crlOutPath := strings.TrimSpace(os.Getenv("GATEWAY_CRL_OUTPUT_FILE"))
	var mqttGuard *mqttguard.Server
	if enableGuardFromEnv {
		if guardAddr := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_ADDR")); guardAddr != "" {
			if authority == nil {
				return nil, nil, nil, nil, errors.New("mqttguard requires gateway CA authority")
			}
			guardCert := serverIdentity
			if certPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_CERT")); certPath != "" {
				keyPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_KEY"))
				if keyPath == "" {
					return nil, nil, nil, nil, errors.New("CONNECTIVITY_MQTT_GUARD_KEY is required when CONNECTIVITY_MQTT_GUARD_CERT is set")
				}
				loaded, loadErr := tls.LoadX509KeyPair(certPath, keyPath)
				if loadErr != nil {
					return nil, nil, nil, nil, loadErr
				}
				guardCert = loaded
			}
			gatewayClientCAs := x509.NewCertPool()
			if !gatewayClientCAs.AppendCertsFromPEM(caPEM) {
				return nil, nil, nil, nil, errors.New("parse gateway CA for mqttguard")
			}
			upstreamRoots := x509.NewCertPool()
			if !upstreamRoots.AppendCertsFromPEM(rootPEM) {
				return nil, nil, nil, nil, errors.New("parse upstream CA for mqttguard")
			}
			if upstreamCAPath := strings.TrimSpace(os.Getenv("CONNECTIVITY_MQTT_GUARD_UPSTREAM_CA")); upstreamCAPath != "" {
				extraPEM, readErr := os.ReadFile(upstreamCAPath)
				if readErr != nil {
					return nil, nil, nil, nil, readErr
				}
				if !upstreamRoots.AppendCertsFromPEM(extraPEM) {
					return nil, nil, nil, nil, errors.New("parse CONNECTIVITY_MQTT_GUARD_UPSTREAM_CA")
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
				return nil, nil, nil, nil, guardErr
			}
		}
	}
	crlCtrl := attachCredentialCRLControl(rootCtx, logger, metrics, presence, store, authority, service, tunnelHub, mqttGuard, coverageReporter, mqttSession, crlOutPath)
	if mqttSession != nil {
		service.SetFactoryResetDispatchHook(func(gatewayID string, reset edgecontrol.FactoryResetPayload) error {
			return publishGatewayFactoryReset(rootCtx, mqttSession, logger, metrics, tap, gatewayID, reset)
		})
	}
	if loopbackAddr != "" && !strings.EqualFold(loopbackAddr, "off") {
		if _, err := tunnelHub.StartLoopbackListener(loopbackAddr); err != nil {
			crlCtrl.Close()
			tunnelHub.Close()
			return nil, nil, nil, nil, err
		}
	}
	if sshLoopbackEnabled {
		if _, err := tunnelHub.StartLoopbackListenerForPort(sshLoopbackAddr, tunnel.SSHTargetPort); err != nil {
			crlCtrl.Close()
			tunnelHub.Close()
			return nil, nil, nil, nil, err
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
	var otaHandler http.Handler
	if store != nil {
		var onGatewaySeen func(string)
		var isGatewayOnline func(string) bool
		if presence != nil {
			onGatewaySeen = func(gatewayID string) {
				presence.SetOnline(gatewayID, true)
			}
			isGatewayOnline = presence.IsOnline
		}
		configShadowHandler = connectivity.NewConfigShadowHandlerWithRuntimePresence(store, service, authorize, onDesiredUpdated, onGatewaySeen, isGatewayOnline)
		diagnosticsHandler = connectivity.NewDiagnosticsHandler(store, tap, authorize)

		otaStorage := cfg.OTAStorage
		if otaStorage == nil {
			var storageErr error
			otaStorage, storageErr = connectivity.NewOTAArtifactStorage(envOr("CONNECTIVITY_OTA_STORAGE_DIR", "/var/lib/hvac/ota-packages"))
			if storageErr != nil {
				crlCtrl.Close()
				tunnelHub.Close()
				if mqttGuard != nil {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = mqttGuard.Shutdown(shutdownCtx)
					cancel()
				}
				return nil, nil, nil, nil, storageErr
			}
		}
		otaSigner := cfg.OTASigner
		if otaSigner == nil {
			var signerErr error
			otaSigner, signerErr = connectivity.LoadOTASignerVerifierFromEnv()
			if signerErr != nil {
				crlCtrl.Close()
				tunnelHub.Close()
				if mqttGuard != nil {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = mqttGuard.Shutdown(shutdownCtx)
					cancel()
				}
				return nil, nil, nil, nil, signerErr
			}
		}
		otaPublicBaseURL := strings.TrimSpace(cfg.OTAPublicBaseURL)
		if otaPublicBaseURL == "" {
			otaPublicBaseURL = strings.TrimSpace(os.Getenv("CONNECTIVITY_OTA_PUBLIC_BASE_URL"))
		}
		var onOTADispatch func([]connectivity.OTAStageDispatch)
		if mqttSession != nil {
			onOTADispatch = func(dispatches []connectivity.OTAStageDispatch) {
				if len(dispatches) == 0 {
					return
				}
				observability.GoSafe(logger, metrics, "connectivity.ota_stage_publish", func() {
					publishGatewayOTAStages(rootCtx, mqttSession, logger, metrics, tap, otaPublicBaseURL, dispatches)
				})
			}
		}
		otaHandler = connectivity.NewOTAHandler(store, otaStorage, otaSigner, service, authorize, connectivity.OTAHandlerOptions{
			PublicBaseURL:   otaPublicBaseURL,
			OnDispatch:      onOTADispatch,
			OnGatewaySeen:   onGatewaySeen,
			IsGatewayOnline: isGatewayOnline,
		})
	}
	return &http.Server{
		Addr:    envOr("CONNECTIVITY_CREDENTIAL_ADDR", ":8448"),
		Handler: connectivity.NewCredentialHandlerWithOTA(service, authorize, tunnelHandler, concentratorHandler, configShadowHandler, diagnosticsHandler, otaHandler),
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
	}, tunnelHub, mqttGuard, crlCtrl, nil
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

func recordOTAPublishMetric(metrics *observability.Registry, outcome string) {
	if metrics == nil {
		return
	}
	_ = metrics.AddCounter(adapter.OTAReplicationMetricName, adapter.OTAReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func recordLifecyclePublishMetric(metrics *observability.Registry, outcome string) {
	if metrics == nil {
		return
	}
	_ = metrics.AddCounter(adapter.LifecycleReplicationMetricName, adapter.LifecycleReplicationMetricHelp, map[string]string{"outcome": outcome}, 1)
}

func publishGatewayFactoryReset(
	rootCtx context.Context,
	publisher shadowMQTTPublisher,
	logger *slog.Logger,
	metrics *observability.Registry,
	tap *packettap.Manager,
	gatewayID string,
	reset edgecontrol.FactoryResetPayload,
) error {
	if publisher == nil {
		return errors.New("MQTT factory-reset publisher is unavailable")
	}
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" {
		return errors.New("factory-reset gatewayId is required")
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	wireBytes, err := adapter.EncodeFactoryReset(gwID, reset, time.Now().UTC())
	if err != nil {
		recordLifecyclePublishMetric(metrics, "failed")
		if logger != nil {
			logger.Warn("mqtt_factory_reset_encode_failed", "gateway_id", gwID, "reset_id", reset.ResetID, "error", err.Error())
		}
		return err
	}
	topic := adapter.LifecycleDownlinkTopic(gwID)
	if tap != nil {
		tap.Record("DOWNLINK", "EVENT", gwID, topic, reset.ResetID, wireBytes)
	}
	ctx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer cancel()
	if _, pubErr := publisher.Publish(ctx, &paho.Publish{
		Topic:   topic,
		QoS:     1,
		Payload: wireBytes,
	}); pubErr != nil {
		recordLifecyclePublishMetric(metrics, "failed")
		if logger != nil {
			logger.Warn("mqtt_factory_reset_publish_failed", "gateway_id", gwID, "reset_id", reset.ResetID, "topic", topic, "error", pubErr.Error())
		}
		return pubErr
	}
	recordLifecyclePublishMetric(metrics, "success")
	return nil
}

func publishGatewayOTAStages(
	rootCtx context.Context,
	publisher shadowMQTTPublisher,
	logger *slog.Logger,
	metrics *observability.Registry,
	tap *packettap.Manager,
	publicBaseURL string,
	dispatches []connectivity.OTAStageDispatch,
) {
	if publisher == nil || len(dispatches) == 0 {
		return
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	for _, item := range dispatches {
		gwID := strings.TrimSpace(item.GatewayID)
		if gwID == "" {
			continue
		}
		stage := item.Stage
		if base := strings.TrimSpace(publicBaseURL); base != "" || strings.TrimSpace(stage.DownloadURL) == "" {
			stage.DownloadURL = connectivity.BuildOTAStageDownloadURL(base, stage.PackageID)
		}
		wireBytes, err := adapter.EncodeOTAStage(gwID, stage, time.Now().UTC())
		if err != nil {
			recordOTAPublishMetric(metrics, "failed")
			if logger != nil {
				logger.Warn("mqtt_ota_encode_failed", "gateway_id", gwID, "task_id", stage.TaskID, "error", err.Error())
			}
			continue
		}
		topic := adapter.OTADownlinkTopic(gwID)
		if tap != nil {
			tap.Record("DOWNLINK", "EVENT", gwID, topic, stage.TaskID, wireBytes)
		}
		ctx, cancel := context.WithTimeout(rootCtx, 10*time.Second)
		_, pubErr := publisher.Publish(ctx, &paho.Publish{
			Topic:   topic,
			QoS:     1,
			Payload: wireBytes,
		})
		cancel()
		if pubErr != nil {
			recordOTAPublishMetric(metrics, "failed")
			if logger != nil {
				logger.Warn("mqtt_ota_publish_failed", "gateway_id", gwID, "task_id", stage.TaskID, "topic", topic, "error", pubErr.Error())
			}
			continue
		}
		recordOTAPublishMetric(metrics, "success")
	}
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

type crlRuntime struct {
	refreshAndReload func()
	close            func()
}

func (r *crlRuntime) RefreshAndReload() {
	if r != nil && r.refreshAndReload != nil {
		r.refreshAndReload()
	}
}

func (r *crlRuntime) Close() {
	if r != nil && r.close != nil {
		r.close()
	}
}

func attachCredentialCRLControl(
	rootCtx context.Context,
	logger *slog.Logger,
	metrics *observability.Registry,
	presence *gatewayOnlineTracker,
	store *connectivity.Store,
	authority *connectivity.CertificateAuthority,
	service *connectivity.CredentialService,
	tunnelHub *tunnel.Hub,
	mqttGuard *mqttguard.Server,
	coverageReporter adapter.CoverageReporter,
	mqttSession *mqttclient.SharedSession,
	crlOutPath string,
) *crlRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	var crlMu sync.Mutex
	refreshCRL := func() bool {
		if crlOutPath == "" || authority == nil || store == nil {
			return false
		}
		crlMu.Lock()
		defer crlMu.Unlock()
		refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		crlPEM, crlErr := service.ExportCRL(refreshCtx)
		if crlErr != nil {
			if !errors.Is(crlErr, connectivity.ErrCRLSignUnsupported) {
				logger.Warn("gateway_crl_export_failed", "error", crlErr.Error())
			}
			return false
		}
		if writeErr := writeAtomicCRLFile(crlOutPath, crlPEM); writeErr != nil {
			logger.Warn("gateway_crl_write_failed", "path", crlOutPath, "error", writeErr.Error())
			return false
		}
		return true
	}
	crlSyncer := newBrokerCRLSyncer(mqttSession, logger, metrics)
	refreshAndReload := func() {
		if refreshCRL() {
			_ = crlSyncer.NotifyReload(rootCtx)
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
		var lister gatewayChildLister
		if store != nil {
			lister = store
		}
		reportGatewayRevokedCoverageWithLogger(logger, lister, coverageReporter, gatewayID)
		refreshCRL()
		_ = crlSyncer.NotifyGatewayRevoked(rootCtx, gatewayID)
	})
	service.SetCertificateRevokeHook(func(gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
		if !hasRemainingActive && presence != nil {
			presence.SetOnline(gatewayID, false)
		}
		var lister gatewayChildLister
		if store != nil {
			lister = store
		}
		applyCertificateRevokeHookWithLogger(logger, tunnelHub, lister, coverageReporter, gatewayID, fingerprintSHA256, hasRemainingActive)
		if mqttGuard != nil {
			mqttGuard.CloseGatewayByFingerprint(gatewayID, fingerprintSHA256)
		}
		refreshCRL()
		_ = crlSyncer.NotifyCertificateRevoked(rootCtx, gatewayID, fingerprintSHA256, hasRemainingActive)
	})
	var removeObserver func()
	if crlOutPath != "" && authority != nil && store != nil {
		observability.GoSafe(logger, metrics, "connectivity.crl_startup_refresh", refreshAndReload)
		if mqttSession != nil {
			var obsMu sync.Mutex
			wasConnected := mqttSession.State().Connected
			removeObserver = mqttSession.AddObserver(func(state mqttclient.SessionState) {
				obsMu.Lock()
				reconnected := !wasConnected && state.Connected && !state.Closed
				wasConnected = state.Connected && !state.Closed
				obsMu.Unlock()
				if reconnected {
					observability.GoSafe(logger, metrics, "connectivity.crl_reconnect_sync", refreshAndReload)
				}
			})
		}
	}
	return &crlRuntime{
		refreshAndReload: refreshAndReload,
		close:            removeObserver,
	}
}

type gatewayChildLister interface {
	ListGatewayChildDeviceIDs(ctx context.Context, gatewayID string) ([]string, error)
}

func applyCertificateRevokeHook(hub *tunnel.Hub, lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
	applyCertificateRevokeHookWithLogger(nil, hub, lister, reporter, gatewayID, fingerprintSHA256, hasRemainingActive)
}

func applyCertificateRevokeHookWithLogger(logger *slog.Logger, hub *tunnel.Hub, lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID, fingerprintSHA256 string, hasRemainingActive bool) {
	if hub != nil {
		hub.CloseGatewayByFingerprint(gatewayID, fingerprintSHA256)
	}
	if !hasRemainingActive {
		reportGatewayRevokedCoverageWithLogger(logger, lister, reporter, gatewayID)
	}
}

func reportGatewayRevokedCoverageWithLister(lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID string) {
	reportGatewayRevokedCoverageWithLogger(nil, lister, reporter, gatewayID)
}

func reportGatewayRevokedCoverageWithLogger(logger *slog.Logger, lister gatewayChildLister, reporter adapter.CoverageReporter, gatewayID string) {
	if lister == nil || reporter == nil {
		return
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var resolver adapter.GatewayChildrenResolver = lister
	if err := adapter.ReportGatewayUnavailableCoverage(reportCtx, resolver, reporter, gatewayID, "SOURCE_UNAVAILABLE", now, max(now.UnixNano(), 1)); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("gateway_revoked_coverage_report_failed", "gateway_id", gatewayID, "error", err.Error())
	}
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
