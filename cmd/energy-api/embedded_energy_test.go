package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestAttachEmbeddedTelemetryExposesOwnerMetricsOnDiagnostics(t *testing.T) {
	parent := observability.NewRuntime(observability.RuntimeConfig{Service: "platform-gateway", QueueSize: 16})
	cmdRuntime := observability.NewRuntime(observability.RuntimeConfig{Service: "energy-api-command", QueueSize: 16})
	alarmRuntime := observability.NewRuntime(observability.RuntimeConfig{Service: "energy-api-alarm", QueueSize: 16})

	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "verified"}, 5)
	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "timed_out"}, 1)
	_ = alarmRuntime.Metrics.AddCounter("hvac_alarm_http_requests_total", "Alarm HTTP requests.", map[string]string{"service": "alarm-service", "method": "GET", "route": "alarms.list", "status_class": "2xx"}, 2)

	attachEmbeddedTelemetry(parent, &embeddedEnergyServices{
		telemetry: []*observability.Runtime{cmdRuntime, alarmRuntime},
	})

	rec := httptest.NewRecorder()
	parent.DiagnosticsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostics /metrics status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hvac_command_verifications_total{outcome="verified"} 5`,
		`hvac_command_verifications_total{outcome="timed_out"} 1`,
		`hvac_alarm_http_requests_total{method="GET",route="alarms.list",service="alarm-service",status_class="2xx"} 2`,
		`s0_telemetry_dropped_spans{service="platform-gateway"} 0`,
		`s0_telemetry_dropped_spans{service="energy-api-command"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagnostics /metrics missing %q:\n%s", want, body)
		}
	}
	if count := strings.Count(body, "# TYPE hvac_command_verifications_total counter"); count != 1 {
		t.Fatalf("expected deduplicated # TYPE line, got %d:\n%s", count, body)
	}
}

func TestEmbeddedCommandControlFailureNotifierPublishesIntoAlarmStore(t *testing.T) {
	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := &embeddedAlarmStoreRef{}
	ref.setStore(store)

	notifier := buildEmbeddedCommandControlFailureNotifier(ref, nil, nil)
	tenantID := "0195b5c0-0000-7000-8000-000000000010"
	siteID := "0195b5c0-0000-7000-8000-000000000011"
	deviceID := "0195b5c0-0000-7000-8000-000000000012"
	pointID := "0195b5c0-0000-7000-8000-000000000013"
	commandID := "0195b5c0-0000-7000-8000-000000000014"

	notifier(context.Background(), commandservice.ControlFailureEvent{
		Kind:       commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
		TenantID:   tenantID,
		SiteID:     siteID,
		DeviceID:   deviceID,
		PointID:    pointID,
		CommandID:  commandID,
		Capability: commandmodel.CapabilityStart,
		ReasonCode: commandmodel.ReasonPulseDeliveredPowerUnconfirmed,
		Detail:     "chiller power remained below threshold after pulse",
		OccurredAt: time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC),
	})

	list, err := store.List(context.Background(), tenantID, siteID, alarmservice.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 operational alarm published, got %d", len(list.Items))
	}
	alarm := list.Items[0]
	if alarm.SourceType != alarmmodel.SourceExternal ||
		alarm.AlarmType != "COMMAND_POWER_UNCONFIRMED" ||
		alarm.CurrentSeverity != alarmmodel.SeverityMajor ||
		!strings.Contains(alarm.Summary, commandID) {
		t.Fatalf("unexpected operational alarm: %#v", alarm)
	}

	emergCmdID := "0195b5c0-0000-7000-8000-000000000015"
	notifier(context.Background(), commandservice.ControlFailureEvent{
		Kind:       alarmservice.ControlEventEmergencyDORelease,
		TenantID:   tenantID,
		SiteID:     siteID,
		DeviceID:   deviceID,
		PointID:    pointID,
		CommandID:  emergCmdID,
		Capability: commandmodel.CapabilityStop,
		ReasonCode: alarmservice.ControlEventEmergencyDORelease,
		Detail:     "emergency shutdown all DO outputs",
		OccurredAt: time.Date(2026, 11, 16, 4, 5, 0, 0, time.UTC),
		ActorType:  "PRINCIPAL",
		ActorID:    "0195b5c0-0000-7000-8000-000000000099",
	})
	listEmerg, err := store.List(context.Background(), tenantID, siteID, alarmservice.Filter{Limit: 10})
	if err != nil || len(listEmerg.Items) != 2 {
		t.Fatalf("expected 2 operational alarms after EMERGENCY_DO_RELEASE, items=%d err=%v", len(listEmerg.Items), err)
	}
	var emergAlarm *alarmmodel.Alarm
	for i := range listEmerg.Items {
		if strings.Contains(listEmerg.Items[i].Summary, emergCmdID) {
			emergAlarm = &listEmerg.Items[i]
			break
		}
	}
	if emergAlarm == nil || emergAlarm.CurrentSeverity != alarmmodel.SeverityCritical || emergAlarm.AlarmType != "EMERGENCY_DO_RELEASE" ||
		!strings.Contains(emergAlarm.Summary, "0195b5c0-0000-7000-8000-000000000099") {
		t.Fatalf("expected CRITICAL EMERGENCY_DO_RELEASE alarm with actor ID, got %#v", emergAlarm)
	}
}

func TestEmbeddedCoreOutboxRelayConnectivityCARequiredAndMTLSClientsConfigured(t *testing.T) {
	iamPool := x509.NewCertPool()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-gateway-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "gateway-ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CORE_OUTBOX_RELAY_ENABLED", "")
	t.Setenv("CORE_TELEMETRY_RUNTIME_URL", "https://telemetry-runtime-service:8446")
	t.Setenv("CORE_CONNECTIVITY_SERVICE_URL", "https://connectivity:8448")
	t.Setenv("CORE_COMMAND_SERVICE_URL", "https://command-service:8447")
	t.Setenv("CONNECTIVITY_SERVER_CA", filepath.Join(t.TempDir(), "missing.crt"))

	if _, _, err := buildEmbeddedCoreOutboxRelayConfig(tls.Certificate{}, iamPool, nil, nil); err == nil {
		t.Fatal("expected missing CONNECTIVITY_SERVER_CA to fail closed without falling back to iamPool")
	}

	t.Setenv("CONNECTIVITY_SERVER_CA", caPath)
	cfg, enabled, err := buildEmbeddedCoreOutboxRelayConfig(tls.Certificate{}, iamPool, nil, nil)
	if err != nil || !enabled {
		t.Fatalf("expected outbox relay config to succeed, enabled=%v err=%v", enabled, err)
	}
	for name, tc := range map[string]struct {
		client     *http.Client
		serverName string
		expectIAM  bool
	}{
		"telemetry":    {cfg.TelemetryHTTPClient, "telemetry-runtime-service", true},
		"connectivity": {cfg.ConnectivityHTTPClient, "connectivity", false},
		"command":      {cfg.CommandHTTPClient, "command-service", true},
	} {
		if tc.client == nil || tc.client.CheckRedirect == nil {
			t.Fatalf("%s client missing CheckRedirect guard", name)
		}
		if err := tc.client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
			t.Fatalf("%s CheckRedirect returned %v, want http.ErrUseLastResponse", name, err)
		}
		tr, ok := tc.client.Transport.(*http.Transport)
		if !ok || tr.TLSClientConfig == nil {
			t.Fatalf("%s missing TLSClientConfig", name)
		}
		if tr.TLSClientConfig.ServerName != tc.serverName {
			t.Fatalf("%s ServerName=%q, want %q", name, tr.TLSClientConfig.ServerName, tc.serverName)
		}
		if tc.expectIAM && tr.TLSClientConfig.RootCAs != iamPool {
			t.Fatalf("%s expected default iamPool roots", name)
		}
		if !tc.expectIAM && tr.TLSClientConfig.RootCAs == iamPool {
			t.Fatalf("connectivity client unexpectedly fell back to iamPool")
		}
	}
}

func TestEmbeddedCommandHandlerExtractsTraceparentAndRecordsBoundedRoutes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	iamKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gatewayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const (
		gatewaySPIFFE = "spiffe://hvac.local/platform-gateway"
		wantTraceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	)
	gatewayURI, err := url.Parse(gatewaySPIFFE)
	if err != nil {
		t.Fatal(err)
	}

	store := commandservice.NewMemoryAuthorityStore(func() time.Time { return now })
	defer store.Close()

	commandHandler, err := commandservice.NewHTTPHandler(commandservice.HTTPConfig{
		Authority:                  store,
		CommandGrantPublicKey:      &iamKey.PublicKey,
		CommandGrantIssuer:         "spiffe://hvac.local/iam-service",
		GatewaySPIFFE:              gatewaySPIFFE,
		CommandGrantAudience:       "command-service",
		GatewayDelegationPublicKey: &gatewayKey.PublicKey,
		GatewayReadAudience:        "command-service",
		Now:                        func() time.Time { return now },
		CommandGrantUseChecker: func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
			return commandauth.UseStatus{
				CurrentPolicyRevision:     claims.PolicyRevision,
				CurrentRevocationRevision: claims.EmergencyRevocationRevision,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stubOK := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "energy-api-command", QueueSize: 16})
	defer func() { _ = telemetry.Shutdown(context.Background()) }()
	handler := newEmbeddedCommandHandler(gatewaySPIFFE, stubOK, commandHandler, stubOK, telemetry)

	profile, ok := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)
	if !ok {
		t.Fatal("missing setpoint capability profile")
	}
	grant, err := commandauth.SignGrant(iamKey, commandauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: gatewaySPIFFE, Audience: "command-service",
		GrantID: "grant-emb-1", Purpose: commandmodel.AuthorizationCommandSubmit,
		PrincipalID: "principal-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskLow,
		CapabilityRevision: profile.Revision, PolicyRevision: "command-policy-1", EmergencyRevocationRevision: 1,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(25 * time.Second).Unix(), TokenID: "grant-emb-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	currentValue := 22.5
	body, err := json.Marshal(map[string]any{
		"tenantId":             "tenant-1",
		"siteId":               "site-1",
		"deviceId":             "device-1",
		"pointId":              "point-1",
		"principalId":          "principal-1",
		"idempotencyKey":       "emb-trace-1",
		"capability":           commandmodel.CapabilitySetTemperatureSetpoint,
		"parameters":           map[string]any{commandmodel.ParameterSetpointC: 23.0},
		"verificationPointKey": "zone.temperature_setpoint",
		"currentState": map[string]any{
			"evaluationAvailability": "AVAILABLE",
			"presence":               "ONLINE",
			"readiness":              "CURRENT",
			"quality":                "GOOD",
			"businessRevision":       10,
			"currentValue":           currentValue,
			"observedAt":             now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, commandservice.InternalCommandsPath, bytes.NewReader(body))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{gatewayURI}}}}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Command-Grant", grant)
	req.Header.Set("traceparent", "00-"+wantTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST %s status=%d body=%s", commandservice.InternalCommandsPath, rec.Code, rec.Body.String())
	}

	var view commandservice.CommandView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.CommandID == "" {
		t.Fatalf("decode CommandView err=%v body=%s", err, rec.Body.String())
	}
	intent, err := store.Get(context.Background(), "tenant-1", view.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.TraceID != wantTraceID {
		t.Fatalf("intent.TraceID=%q, want %q", intent.TraceID, wantTraceID)
	}
	env, err := store.ClaimDispatch(context.Background(), "tenant-1", "dispatcher-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if env.TraceID != wantTraceID {
		t.Fatalf("DispatchEnvelope.TraceID=%q, want %q", env.TraceID, wantTraceID)
	}

	modeReq := httptest.NewRequest(http.MethodPut, commandservice.InternalOperatorPrefix+"/sites/site-1/control-mode", nil)
	modeReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{gatewayURI}}}}
	handler.ServeHTTP(httptest.NewRecorder(), modeReq)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, commandservice.InternalDispatchClaimPath, nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/unexpected/path", nil))

	metricsRec := httptest.NewRecorder()
	telemetry.DiagnosticsHandler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := metricsRec.Body.String()
	for _, wantMetric := range []string{
		`hvac_command_http_requests_total{method="POST",route="commands.create",service="command-service",status_class="2xx"} 1`,
		`hvac_command_http_requests_total{method="PUT",route="control.mode",service="command-service",status_class="2xx"} 1`,
		`hvac_command_http_requests_total{method="POST",route="dispatch.claim",service="command-service",status_class="2xx"} 1`,
		`hvac_command_http_requests_total{method="GET",route="unknown",service="command-service",status_class="2xx"} 1`,
	} {
		if !strings.Contains(metricsBody, wantMetric) {
			t.Fatalf("metrics missing %q:\n%s", wantMetric, metricsBody)
		}
	}
}
