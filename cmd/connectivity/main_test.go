package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgetunnel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

func TestCredentialServerRejectsNonLoopbackTunnelAddressFailClosed(t *testing.T) {
	t.Setenv("CONNECTIVITY_TUNNEL_LOOPBACK_ADDR", "0.0.0.0:18800")
	_, _, err := credentialServer(nil)
	if !errors.Is(err, edgetunnel.ErrNonLoopbackAddress) {
		t.Fatalf("expected ErrNonLoopbackAddress for 0.0.0.0:18800, got %v", err)
	}
}

func TestCredentialServerRejectsNonLoopbackSSHTunnelAddressFailClosed(t *testing.T) {
	t.Setenv("CONNECTIVITY_TUNNEL_SSH_LOOPBACK_ADDR", "0.0.0.0:18822")
	_, _, err := credentialServer(nil)
	if !errors.Is(err, edgetunnel.ErrNonLoopbackAddress) {
		t.Fatalf("expected ErrNonLoopbackAddress for 0.0.0.0:18822, got %v", err)
	}
}

func TestUplinkBatchConfigFromEnv(t *testing.T) {
	defaults := uplinkConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatalf("default uplinkConfig invalid: %v", err)
	}
	if defaults.BatchMaxMessages != adapter.DefaultBatchMaxMessages ||
		defaults.BatchFlushWindow != adapter.DefaultBatchFlushWindow ||
		defaults.AutoBackfillLagThreshold != adapter.DefaultAutoBackfillLagThreshold ||
		defaults.WorkerCount != adapter.DefaultWorkerCount {
		t.Fatalf("defaults=%#v", defaults)
	}

	t.Setenv("CONNECTIVITY_BATCH_MAX_MESSAGES", "512")
	t.Setenv("CONNECTIVITY_BATCH_WINDOW", "250ms")
	t.Setenv("CONNECTIVITY_AUTO_BACKFILL_LAG", "2m")
	t.Setenv("CONNECTIVITY_UPLINK_WORKERS", "16")
	valid := uplinkConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid bounded uplinkConfig failed: %v", err)
	}
	if valid.BatchMaxMessages != 512 || valid.BatchFlushWindow != 250*time.Millisecond || valid.AutoBackfillLagThreshold != 2*time.Minute || valid.WorkerCount != 16 {
		t.Fatalf("valid=%#v", valid)
	}

	t.Setenv("CONNECTIVITY_BATCH_WINDOW", "0s")
	t.Setenv("CONNECTIVITY_AUTO_BACKFILL_LAG", "0s")
	zeroOverrides := uplinkConfig()
	if err := zeroOverrides.Validate(); err != nil {
		t.Fatalf("zero duration overrides failed Validate: %v", err)
	}
	if zeroOverrides.BatchFlushWindow != 0 || zeroOverrides.AutoBackfillLagThreshold != 0 {
		t.Fatalf("zeroOverrides=%#v", zeroOverrides)
	}

	for _, invalid := range []struct {
		name string
		key  string
		val  string
	}{
		{"zero batch max", "CONNECTIVITY_BATCH_MAX_MESSAGES", "0"},
		{"above max batch", "CONNECTIVITY_BATCH_MAX_MESSAGES", "513"},
		{"non-numeric batch max", "CONNECTIVITY_BATCH_MAX_MESSAGES", "abc"},
		{"negative flush window", "CONNECTIVITY_BATCH_WINDOW", "-1ms"},
		{"above max flush window", "CONNECTIVITY_BATCH_WINDOW", "1001ms"},
		{"invalid flush window", "CONNECTIVITY_BATCH_WINDOW", "not-a-duration"},
		{"negative auto backfill lag", "CONNECTIVITY_AUTO_BACKFILL_LAG", "-1s"},
		{"invalid auto backfill lag", "CONNECTIVITY_AUTO_BACKFILL_LAG", "not-a-duration"},
		{"zero uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "0"},
		{"above max uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "1025"},
		{"non-numeric uplink workers", "CONNECTIVITY_UPLINK_WORKERS", "abc"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Setenv(invalid.key, invalid.val)
			if err := uplinkConfig().Validate(); err == nil {
				t.Fatalf("%s (%s=%q) unexpectedly passed Validate", invalid.name, invalid.key, invalid.val)
			}
		})
	}
}

func TestUplinkSharedSubscriptionAndOverflowConfigFromEnv(t *testing.T) {
	defaults := uplinkConfig()
	if err := defaults.Validate(); err != nil {
		t.Fatalf("default uplinkConfig invalid: %v", err)
	}
	if defaults.MQTT.SharedSubscriptionGroup != "connectivity" {
		t.Fatalf("default SharedSubscriptionGroup = %q, want connectivity", defaults.MQTT.SharedSubscriptionGroup)
	}
	if defaults.OverflowMaxMessages != 0 {
		t.Fatalf("default OverflowMaxMessages = %d, want 0 (defaulting in NewRuntime)", defaults.OverflowMaxMessages)
	}

	for _, disabled := range []string{"", "off", "OFF", "none", "NONE"} {
		t.Setenv("CONNECTIVITY_MQTT_SHARE_GROUP", disabled)
		cfg := uplinkConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("share group %q invalid: %v", disabled, err)
		}
		if cfg.MQTT.SharedSubscriptionGroup != "" {
			t.Fatalf("share group %q -> %q, want empty", disabled, cfg.MQTT.SharedSubscriptionGroup)
		}
	}

	t.Setenv("CONNECTIVITY_MQTT_SHARE_GROUP", "connectivity-pool_1")
	t.Setenv("CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "8192")
	t.Setenv("CONNECTIVITY_OVERFLOW_MAX_BYTES", "65536")
	t.Setenv("CONNECTIVITY_OVERFLOW_DIR", "/tmp/hvac-overflow-test")
	custom := uplinkConfig()
	if err := custom.Validate(); err != nil {
		t.Fatalf("custom config invalid: %v", err)
	}
	if custom.MQTT.SharedSubscriptionGroup != "connectivity-pool_1" || custom.OverflowMaxMessages != 8192 || custom.OverflowMaxBytes != 65536 || custom.OverflowDir != "/tmp/hvac-overflow-test" {
		t.Fatalf("custom=%#v", custom)
	}

	for _, invalid := range []struct {
		name string
		key  string
		val  string
	}{
		{"invalid share group slash", "CONNECTIVITY_MQTT_SHARE_GROUP", "bad/group"},
		{"invalid share group plus", "CONNECTIVITY_MQTT_SHARE_GROUP", "bad+group"},
		{"zero overflow max", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "0"},
		{"overflow max above limit", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "1048577"},
		{"non-numeric overflow max", "CONNECTIVITY_OVERFLOW_MAX_MESSAGES", "abc"},
		{"zero overflow max bytes", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "0"},
		{"overflow max bytes above limit", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "1073741825"},
		{"non-numeric overflow max bytes", "CONNECTIVITY_OVERFLOW_MAX_BYTES", "abc"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			t.Setenv(invalid.key, invalid.val)
			if err := uplinkConfig().Validate(); err == nil {
				t.Fatalf("%s (%s=%q) unexpectedly passed Validate", invalid.name, invalid.key, invalid.val)
			}
		})
	}
}

func TestUplinkDefaultMQTTClientIDUsesHostnameAndRespectsExplicitOverride(t *testing.T) {
	for _, blank := range []string{"", "   "} {
		t.Setenv("CONNECTIVITY_MQTT_CLIENT_ID", blank)
		cfg := uplinkConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("blank CONNECTIVITY_MQTT_CLIENT_ID (%q) invalid: %v", blank, err)
		}
		host, hostErr := os.Hostname()
		cleanHost := sanitizeMQTTClientID(host)
		if hostErr == nil && cleanHost != "" {
			wantPrefix := "connectivity-"
			if !strings.HasPrefix(cfg.MQTT.ClientID, wantPrefix) {
				t.Fatalf("ClientID=%q, want prefix %q", cfg.MQTT.ClientID, wantPrefix)
			}
		} else if cfg.MQTT.ClientID != "connectivity" {
			t.Fatalf("ClientID=%q, want fallback connectivity", cfg.MQTT.ClientID)
		}
		if len(cfg.MQTT.ClientID) > maxDefaultMQTTClientIDLen {
			t.Fatalf("ClientID length=%d exceeds %d", len(cfg.MQTT.ClientID), maxDefaultMQTTClientIDLen)
		}
	}

	t.Setenv("CONNECTIVITY_MQTT_CLIENT_ID", "connectivity-replica-01")
	override := uplinkConfig()
	if override.MQTT.ClientID != "connectivity-replica-01" {
		t.Fatalf("explicit override ClientID=%q, want connectivity-replica-01", override.MQTT.ClientID)
	}

	if got := sanitizeMQTTClientID("pod.01/edge@node_a-1"); got != "pod01edgenode_a-1" {
		t.Fatalf("sanitizeMQTTClientID=%q, want pod01edgenode_a-1", got)
	}
	if got := defaultMQTTClientIDForHost("connectivity", "..."); got != "connectivity" {
		t.Fatalf("invalid-only hostname fallback=%q, want connectivity", got)
	}
	if got := defaultMQTTClientIDForHost("connectivity", strings.Repeat("x", 120)); len(got) != maxDefaultMQTTClientIDLen || !strings.HasPrefix(got, "connectivity-") {
		t.Fatalf("long hostname ClientID=(%q, len=%d), want prefix connectivity- and len %d", got, len(got), maxDefaultMQTTClientIDLen)
	}
}

type fakeQuarantinePruner struct {
	called   chan time.Duration
	deleted  int64
	pruneErr error
}

func (f *fakeQuarantinePruner) PruneExpiredQuarantine(_ context.Context, retention time.Duration) (int64, error) {
	if f.called != nil {
		select {
		case f.called <- retention:
		default:
		}
	}
	return f.deleted, f.pruneErr
}

func TestDiagnosticsServerRoutesAndShutdownReadiness(t *testing.T) {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "mqtt-telemetry-adapter"})
	defer func() { _ = telemetry.Shutdown(context.Background()) }()
	telemetry.MarkReady()

	var runtimeReady atomic.Bool
	runtimeReady.Store(true)
	var shuttingDown atomic.Bool
	srv := diagnosticsServerWithReady(":0", runtimeReady.Load, telemetry, &shuttingDown)

	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		srv.Handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := get(http.MethodPost, "/health/live"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health/live status = %d, want 405", rec.Code)
	}
	for _, path := range []string{"/health/live", "/health/ready", "/health/telemetry/ready", "/metrics"} {
		if rec := get(http.MethodGet, path); rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
	}
	diagRec := get(http.MethodGet, "/diagnostics")
	if diagRec.Code != http.StatusOK {
		t.Fatalf("GET /diagnostics status = %d, want 200", diagRec.Code)
	}
	var diagPayload map[string]any
	if err := json.Unmarshal(diagRec.Body.Bytes(), &diagPayload); err != nil {
		t.Fatalf("decode /diagnostics JSON: %v", err)
	}
	if diagPayload["service"] != "mqtt-telemetry-adapter" || diagPayload["ready"] != true {
		t.Fatalf("unexpected /diagnostics payload: %+v", diagPayload)
	}

	shuttingDown.Store(true)
	if rec := get(http.MethodGet, "/health/live"); rec.Code != http.StatusOK {
		t.Fatalf("GET /health/live during shutdown = %d, want 200", rec.Code)
	}
	for _, path := range []string{"/health/ready", "/health/telemetry/ready"} {
		if rec := get(http.MethodGet, path); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s during shutdown = %d, want 503", path, rec.Code)
		}
	}
}

func TestStartQuarantineRetentionLoopPrunesOnStartupAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pruner := &fakeQuarantinePruner{
		called:  make(chan time.Duration, 1),
		deleted: 3,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	done := startQuarantineRetentionLoop(ctx, pruner, logger, nil, time.Hour, 0)

	select {
	case retention := <-pruner.called:
		if retention <= 0 {
			t.Fatalf("retention = %s, want positive default", retention)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected startup prune execution")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not exit on cancel")
	}
}

func TestGatewayRateLimitConfigFromEnv(t *testing.T) {
	mps, burst, err := gatewayRateLimitConfig()
	if err != nil {
		t.Fatalf("default gatewayRateLimitConfig failed: %v", err)
	}
	if mps != defaultGatewayRateLimitMPS || burst != defaultGatewayRateLimitBurst {
		t.Fatalf("defaults = (%v, %d), want (%v, %d)", mps, burst, defaultGatewayRateLimitMPS, defaultGatewayRateLimitBurst)
	}

	for _, disabled := range []string{"", "off", "NONE"} {
		t.Setenv("CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", disabled)
		gotMPS, gotBurst, gotErr := gatewayRateLimitConfig()
		if gotErr != nil || gotMPS != 0 || gotBurst != 0 {
			t.Fatalf("disabled %q => (%v, %d, %v), want (0, 0, nil)", disabled, gotMPS, gotBurst, gotErr)
		}
	}

	t.Setenv("CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "75.5")
	t.Setenv("CONNECTIVITY_GATEWAY_RATE_LIMIT_BURST", "150")
	mps, burst, err = gatewayRateLimitConfig()
	if err != nil || mps != 75.5 || burst != 150 {
		t.Fatalf("override => (%v, %d, %v), want (75.5, 150, nil)", mps, burst, err)
	}

	for _, bad := range []struct {
		key string
		val string
	}{
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "0"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "-5"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "NaN"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "Inf"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_MPS", "abc"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_BURST", "0"},
		{"CONNECTIVITY_GATEWAY_RATE_LIMIT_BURST", "abc"},
	} {
		t.Run(bad.key+"="+bad.val, func(t *testing.T) {
			t.Setenv(bad.key, bad.val)
			if _, _, badErr := gatewayRateLimitConfig(); badErr == nil {
				t.Fatalf("expected validation failure for %s=%q", bad.key, bad.val)
			}
		})
	}
}

type mainPresenceFakeIdentity struct {
	gw adapter.Gateway
}

func (f mainPresenceFakeIdentity) ResolveGateway(_ context.Context, gatewayID string) (adapter.Gateway, error) {
	if strings.EqualFold(gatewayID, f.gw.ID) {
		return f.gw, nil
	}
	return adapter.Gateway{}, adapter.ErrGatewayUnknown
}

func (f mainPresenceFakeIdentity) ResolveDevice(_ context.Context, _ adapter.Gateway, _ string) (string, error) {
	return f.gw.ID, nil
}

func (f mainPresenceFakeIdentity) ResolveBindings(_ context.Context, _ adapter.Gateway, _ []adapter.DevicePointQuery) (adapter.ResolvedBindings, error) {
	return adapter.ResolvedBindings{}, nil
}

func (f mainPresenceFakeIdentity) QuarantineUplink(_ context.Context, _ adapter.UplinkQuarantine) error {
	return nil
}

type mainPresenceFakeRuntime struct{}

func (mainPresenceFakeRuntime) AcceptObservationBatch(_ context.Context, observations []adapter.Observation) ([]adapter.ObservationReceipt, error) {
	receipts := make([]adapter.ObservationReceipt, len(observations))
	for i := range receipts {
		receipts[i] = adapter.ObservationReceipt{Status: "ACCEPTED"}
	}
	return receipts, nil
}

func (mainPresenceFakeRuntime) AcceptRuntimeEvent(_ context.Context, _ adapter.RuntimeEventEvidence) error {
	return nil
}

func (mainPresenceFakeRuntime) ReportSourceCoverage(_ context.Context, _ adapter.SourceCoverageReport) error {
	return nil
}

func TestSharedSessionConfigAndOutOfOrderLWTKeepsGatewayOnline(t *testing.T) {
	registry := observability.NewRegistry()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := buildSharedUplinkSessionConfig(adapter.MQTTConfig{
		BrokerURL:  "tls://mqtt-broker:8883",
		ClientID:   "connectivity-test",
		CAFile:     "/run/hvac/pki/ca.crt",
		CertFile:   "/run/hvac/pki/tls.crt",
		KeyFile:    "/run/hvac/pki/tls.key",
		ServerName: "mqtt-broker",
	}, logger, registry)
	if cfg.ClientID != "connectivity-test" || cfg.TLS == nil || cfg.KeepAlive != 30 || cfg.SessionExpiryInterval != 24*60*60 {
		t.Fatalf("unexpected shared session config: %+v", cfg)
	}
	cfg.OnConnectError(errors.New("dial timeout"))
	cfg.OnSubscribeError(errors.New("suback 0x87"))

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	for _, want := range []string{
		`hvac_mqtt_connections_total{outcome="failed"} 1`,
		`hvac_mqtt_subscriptions_total{outcome="failed"} 1`,
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("expected metric %q in output:\n%s", want, metricsBody)
		}
	}

	gwID := "0194f000-3000-7000-8000-000000000099"
	presence := newGatewayOnlineTrackerWithMetrics(registry)
	processor, err := adapter.NewProcessor(
		mainPresenceFakeIdentity{gw: adapter.Gateway{ID: gwID, TenantID: "0194f000-1000-7000-8000-000000000001", SiteID: "0194f000-2000-7000-8000-000000000001"}},
		mainPresenceFakeRuntime{},
		adapter.WithProcessorMetrics(registry),
		adapter.WithGatewayRateLimit(200, 400),
		adapter.WithGatewayPresenceHook(presence.SetOnline),
	)
	if err != nil {
		t.Fatalf("NewProcessor failed: %v", err)
	}

	connectPayload := `{"schemaVersion":"2.0","messageId":"0194f000-4000-7000-8000-000000000002","gatewayId":"` + gwID + `","timestamp":1730000002000,"sequence":1,"payload":{"eventType":"GATEWAY_CONNECTED","sourceType":"GATEWAY","sourceId":"` + gwID + `","eventTime":1730000002000,"severity":"INFO","data":{"sessionId":"0194f000-5000-7000-8000-000000000002"}}}`
	if _, err := processor.Process(context.Background(), "hvac/v1/"+gwID+"/up/event", []byte(connectPayload)); err != nil {
		t.Fatalf("connect event failed: %v", err)
	}
	if !presence.IsOnline(gwID) {
		t.Fatal("expected gateway online after GATEWAY_CONNECTED")
	}

	staleLWTPayload := `{"schemaVersion":"2.0","messageId":"0194f000-4000-7000-8000-000000000001","gatewayId":"` + gwID + `","timestamp":1730000001000,"sequence":99,"payload":{"eventType":"LWT","sourceType":"GATEWAY","sourceId":"` + gwID + `","eventTime":1730000001000,"severity":"WARNING","data":{"sessionId":"0194f000-5000-7000-8000-000000000001","reason":"broker_connection_lost"}}}`
	if _, err := processor.Process(context.Background(), "hvac/v1/"+gwID+"/up/event", []byte(staleLWTPayload)); err != nil {
		t.Fatalf("stale LWT event failed: %v", err)
	}
	if !presence.IsOnline(gwID) || presence.Count() != 1 {
		t.Fatal("delayed old-session LWT must not flip reconnected gateway offline")
	}
}

func TestOpenConcentratorAndSharedSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mqtt-telemetry-adapter"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal test key: %v", err)
	}
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tap := packettap.NewManager()
	mqttCfg := adapter.MQTTConfig{
		BrokerURL:  "tls://127.0.0.1:1",
		ClientID:   "connectivity-shared",
		CAFile:     certPath,
		CertFile:   certPath,
		KeyFile:    keyPath,
		ServerName: "localhost",
	}

	session, transport, err := openConcentratorAndSharedSession(context.Background(), mqttCfg, logger, observability.NewRegistry(), tap)
	if err != nil {
		t.Fatalf("openConcentratorAndSharedSession default failed: %v", err)
	}
	if sharedCfg := buildSharedUplinkSessionConfig(mqttCfg, logger, observability.NewRegistry()); sharedCfg.ReceiveMaximum != 256 {
		t.Fatalf("default ReceiveMaximum=%d, want 256", sharedCfg.ReceiveMaximum)
	}
	customMQTTCfg := mqttCfg
	customMQTTCfg.ReceiveMaximum = 64
	if customCfg := buildSharedUplinkSessionConfig(customMQTTCfg, logger, observability.NewRegistry()); customCfg.ReceiveMaximum != 64 {
		t.Fatalf("custom ReceiveMaximum=%d, want 64", customCfg.ReceiveMaximum)
	}
	if session == nil || transport == nil {
		t.Fatalf("expected shared session and transport, got session=%v transport=%v", session, transport)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = transport.Close(closeCtx)
	_ = session.Close(closeCtx)
	closeCancel()
}

func TestShutdownConnectivityRuntimeOrder(t *testing.T) {
	var order []string
	credentialsDone := make(chan struct{})
	runtimeDone := make(chan struct{})
	commandsDone := make(chan struct{})
	retentionDone := make(chan struct{})
	diagnosticsDone := make(chan struct{})

	shutdownConnectivityRuntime(connectivityShutdownDeps{
		markShuttingDown: func() { order = append(order, "mark_not_ready") },
		shutdownCredentials: func(context.Context) error {
			order = append(order, "credentials_shutdown")
			close(credentialsDone)
			return nil
		},
		credentialsDone: credentialsDone,
		closeTunnelHub:  func() { order = append(order, "tunnel_close") },
		cancelIngress: func() {
			order = append(order, "cancel_ingress")
			close(runtimeDone)
			close(commandsDone)
			close(retentionDone)
		},
		cancelDrainRoot:   func() { order = append(order, "cancel_drain_root") },
		runtimeDone:       runtimeDone,
		commandsDone:      commandsDone,
		waitCommands:      func(context.Context) error { order = append(order, "commands_wait"); return nil },
		retentionDone:     retentionDone,
		flushQuarantine:   func(context.Context) error { order = append(order, "flush_quarantine"); return nil },
		closeConcentrator: func(context.Context) error { order = append(order, "concentrator_close"); return nil },
		closeMQTTSession:  func(context.Context) error { order = append(order, "mqtt_close"); return nil },
		closeCommands:     func(context.Context) error { order = append(order, "commands_close"); return nil },
		shutdownDiagnostics: func(context.Context) error {
			order = append(order, "diagnostics_shutdown")
			close(diagnosticsDone)
			return nil
		},
		diagnosticsDone:   diagnosticsDone,
		shutdownTelemetry: func(context.Context) error { order = append(order, "telemetry_shutdown"); return nil },
		closeStore:        func() { order = append(order, "store_close") },
	})

	want := []string{
		"mark_not_ready",
		"credentials_shutdown",
		"tunnel_close",
		"cancel_ingress",
		"commands_wait",
		"flush_quarantine",
		"cancel_drain_root",
		"concentrator_close",
		"mqtt_close",
		"commands_close",
		"diagnostics_shutdown",
		"telemetry_shutdown",
		"store_close",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
}

func TestShutdownConnectivityRuntimeWaitsBackgroundBeforeStoreAndTransportClose(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}

	credentialsDone := make(chan struct{})
	runtimeDone := make(chan struct{})
	commandsDone := make(chan struct{})
	retentionDone := make(chan struct{})
	diagnosticsDone := make(chan struct{})

	ingressCtx, ingressCancel := context.WithCancel(context.Background())
	var bgWG sync.WaitGroup
	bgStarted := make(chan struct{})
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		defer observability.Recover(nil, nil, "connectivity.test_reconcile")
		close(bgStarted)
		<-ingressCtx.Done()
		time.Sleep(15 * time.Millisecond)
		record("reconcile_done")
	}()
	<-bgStarted

	shutdownConnectivityRuntime(connectivityShutdownDeps{
		markShuttingDown: func() { record("mark_not_ready") },
		shutdownCredentials: func(context.Context) error {
			if ingressCtx.Err() != nil {
				t.Error("ingressCtx canceled before credentials shutdown")
			}
			record("credentials_shutdown")
			close(credentialsDone)
			return nil
		},
		credentialsDone: credentialsDone,
		closeTunnelHub:  func() { record("tunnel_close") },
		cancelIngress: func() {
			record("cancel_ingress")
			ingressCancel()
			close(runtimeDone)
			close(commandsDone)
			close(retentionDone)
		},
		runtimeDone: runtimeDone,
		waitBackground: func(ctx context.Context) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				bgWG.Wait()
			}()
			awaitSignal(ctx, done)
			record("wait_background")
		},
		commandsDone:      commandsDone,
		retentionDone:     retentionDone,
		closeConcentrator: func(context.Context) error { record("concentrator_close"); return nil },
		closeMQTTSession:  func(context.Context) error { record("mqtt_close"); return nil },
		shutdownDiagnostics: func(context.Context) error {
			close(diagnosticsDone)
			return nil
		},
		diagnosticsDone:   diagnosticsDone,
		shutdownTelemetry: func(context.Context) error { record("telemetry_shutdown"); return nil },
		closeStore:        func() { record("store_close") },
	})

	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	want := "mark_not_ready,credentials_shutdown,tunnel_close,cancel_ingress,reconcile_done,wait_background,concentrator_close,mqtt_close,telemetry_shutdown,store_close"
	if got != want {
		t.Fatalf("shutdown order = %s, want %s", got, want)
	}
}
