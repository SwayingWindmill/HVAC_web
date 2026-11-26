package connectivity

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
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

type fakeWaveformStore struct {
	*fakeDiagnosticsStore
	tenantID string
	siteID   string
	rules    []WaveformRuleRecord
	captures []WaveformCaptureRecord
	metrics  GatewayHostMetricsSummary
	audits   []string
}

func (f *fakeWaveformStore) CheckGatewayScopeForDiagnostics(_ context.Context, _ registryauth.GrantClaims, gatewayID string) (string, string, error) {
	return f.tenantID, f.siteID, f.scopeErr
}

func (f *fakeWaveformStore) ListGatewayWaveformRules(_ context.Context, _ registryauth.GrantClaims, _ string) ([]WaveformRuleRecord, error) {
	return f.rules, nil
}

func (f *fakeWaveformStore) ListGatewayWaveformRulesForGateway(_ context.Context, _ string) ([]WaveformRuleRecord, error) {
	return f.rules, nil
}

func (f *fakeWaveformStore) CreateGatewayWaveformRule(_ context.Context, _ registryauth.GrantClaims, gatewayID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error) {
	cfg, err := validateUpsertWaveformRuleInput(input)
	if err != nil {
		return WaveformRuleRecord{}, err
	}
	rec := WaveformRuleRecord{
		ID:                 "0194f000-9100-7000-8000-000000000001",
		TenantID:           f.tenantID,
		SiteID:             f.siteID,
		GatewayID:          gatewayID,
		RuleName:           cfg.RuleName,
		Enabled:            cfg.Enabled,
		DeviceCode:         cfg.DeviceCode,
		PointCode:          cfg.PointCode,
		TriggerType:        cfg.TriggerType,
		ThresholdValue:     cfg.ThresholdValue,
		DeltaWindowSeconds: cfg.DeltaWindowSeconds,
		PreSeconds:         cfg.PreSeconds,
		PostSeconds:        cfg.PostSeconds,
		SampleRateHz:       cfg.SampleRateHz,
		PointCodes:         cfg.PointCodes,
		CooldownSeconds:    cfg.CooldownSeconds,
	}
	f.rules = append(f.rules, rec)
	return rec, nil
}

func (f *fakeWaveformStore) UpdateGatewayWaveformRule(_ context.Context, _ registryauth.GrantClaims, _ string, ruleID string, input UpsertWaveformRuleInput) (WaveformRuleRecord, error) {
	for i := range f.rules {
		if f.rules[i].ID == ruleID {
			f.rules[i].RuleName = input.RuleName
			return f.rules[i], nil
		}
	}
	return WaveformRuleRecord{}, ErrWaveformNotFound
}

func (f *fakeWaveformStore) DeleteGatewayWaveformRule(_ context.Context, _ registryauth.GrantClaims, _ string, ruleID string) error {
	for i := range f.rules {
		if f.rules[i].ID == ruleID {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			return nil
		}
	}
	return ErrWaveformNotFound
}

func (f *fakeWaveformStore) ListGatewayWaveformCaptures(_ context.Context, _ registryauth.GrantClaims, _ string, _ int) ([]WaveformCaptureRecord, error) {
	return f.captures, nil
}

func (f *fakeWaveformStore) GetGatewayWaveformCapture(_ context.Context, _ registryauth.GrantClaims, _ string, captureID string) (WaveformCaptureRecord, error) {
	for _, c := range f.captures {
		if c.CaptureID == captureID {
			return c, nil
		}
	}
	return WaveformCaptureRecord{}, ErrWaveformNotFound
}

func (f *fakeWaveformStore) CreateManualWaveformCapture(_ context.Context, _ registryauth.GrantClaims, gatewayID string, input TriggerWaveformCaptureInput) (WaveformCaptureRecord, error) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	rec := WaveformCaptureRecord{
		CaptureID:     "0194f000-9200-7000-8000-000000000001",
		TenantID:      f.tenantID,
		SiteID:        f.siteID,
		GatewayID:     gatewayID,
		TriggerSource: "MANUAL",
		TriggerReason: input.TriggerReason,
		Status:        "PENDING",
		TriggeredAt:   now,
		WindowStartAt: now.Add(-30 * time.Second),
		WindowEndAt:   now.Add(30 * time.Second),
		SampleRateHz:  1,
		RequestedBy:   "operator-1",
	}
	f.captures = append(f.captures, rec)
	return rec, nil
}

func (f *fakeWaveformStore) GetGatewayHostMetrics(_ context.Context, _ registryauth.GrantClaims, _ string, _ int) (GatewayHostMetricsSummary, error) {
	return f.metrics, nil
}

func (f *fakeWaveformStore) RecordDiagnosticsAudit(_ context.Context, _, _, _, action, _ string, _, _, _, redacted int, status, _ string) error {
	f.audits = append(f.audits, action+":"+status)
	return nil
}

func TestWaveformDiagnosticsHTTPHandlersAndRedaction(t *testing.T) {
	gwID := "0194f000-2000-7000-8000-000000000001"
	tenantID := "0194f000-0000-7000-8000-000000000001"
	siteID := "0194f000-1000-7000-8000-000000000001"

	store := &fakeWaveformStore{
		fakeDiagnosticsStore: &fakeDiagnosticsStore{},
		tenantID:             tenantID,
		siteID:               siteID,
		metrics: GatewayHostMetricsSummary{
			GatewayID: gwID,
			Latest: &GatewayHostMetricsRecord{
				ID: "0194f000-9300-7000-8000-000000000001", GatewayID: gwID, CPUPercent: 14.5, MQTTTLSVersion: "TLSv1.3", MQTTRTTMs: 18.2,
			},
		},
	}
	bridge := NewDiagnosticsMQTTBridge()
	bridge.AttachPublisher(func(ctx context.Context, _ string, cmd edgecontrol.DiagnosticsDownlinkPayload) error {
		if cmd.Action == edgecontrol.DiagnosticsActionPullLogBundle {
			bridge.CompleteLogReply(ctx, gwID, edgecontrol.DiagnosticsLogReplyPayload{
				RequestID:     cmd.RequestID,
				Status:        "SUCCEEDED",
				Content:       "line1 ok\nAuthorization: Bearer topsecretjwt123456\nline3 done",
				ReturnedBytes: 60,
				RedactedCount: 0,
				CollectedAtMs: time.Now().UTC().UnixMilli(),
			})
		}
		return nil
	})
	authorize := func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: "0194f000-4000-7000-8000-000000000001"}, nil
	}
	tap := packettap.NewManager()
	diagHandler := NewDiagnosticsHandlerWithWaveform(store, tap, authorize, bridge)
	wrapped := NewCredentialHandler(nil, authorize, WithDiagnosticsHandler(diagHandler))

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}
	doReq := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}

	createBody, _ := json.Marshal(UpsertWaveformRuleInput{
		RuleName: "喘振电流突变", Enabled: true, DeviceCode: "chiller-01", PointCode: "current_a",
		TriggerType: edgecontrol.WaveformTriggerDeltaJump, ThresholdValue: 25.0,
		PreSeconds: 30, PostSeconds: 60, SampleRateHz: 1, PointCodes: []string{"chiller-01.current_a"}, CooldownSeconds: 120,
	})
	if rec := doReq(http.MethodPost, "/internal/v1/gateways/"+gwID+"/waveform-rules", createBody); rec.Code != http.StatusCreated {
		t.Fatalf("create rule status=%d body=%s", rec.Code, rec.Body.String())
	}

	if rec := doReq(http.MethodPost, "/internal/v1/gateways/"+gwID+"/waveform-captures:trigger", []byte(`{"preSeconds":15,"postSeconds":30,"sampleRateHz":1,"triggerReason":"排查冷水温度震荡"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("trigger capture status=%d body=%s", rec.Code, rec.Body.String())
	}

	logRec := doReq(http.MethodPost, "/internal/v1/gateways/"+gwID+"/diagnostics/logs:pull", []byte(`{"maxBytes":4096,"sinceMinutes":30}`))
	if logRec.Code != http.StatusOK {
		t.Fatalf("log pull status=%d body=%s", logRec.Code, logRec.Body.String())
	}
	var bundle GatewayLogBundleResult
	if err := json.Unmarshal(logRec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("unmarshal log bundle: %v", err)
	}
	if strings.Contains(bundle.Content, "topsecretjwt123456") || bundle.RedactedCount < 1 {
		t.Fatalf("expected server-side redaction fallback, got redacted=%d content=%q", bundle.RedactedCount, bundle.Content)
	}
	if len(store.audits) != 2 {
		t.Fatalf("expected 2 diagnostics audits recorded, got %v", store.audits)
	}

	mRec := doReq(http.MethodGet, "/internal/v1/gateways/"+gwID+"/diagnostics/host-metrics", nil)
	if mRec.Code != http.StatusOK || !strings.Contains(mRec.Body.String(), "TLSv1.3") {
		t.Fatalf("host metrics status=%d body=%s", mRec.Code, mRec.Body.String())
	}
}

type fakeGatewayPeerVerifier struct {
	gatewayID string
	err       error
}

func (f fakeGatewayPeerVerifier) VerifyActiveCredential(_ context.Context, _ *x509.Certificate) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.gatewayID, nil
}

func TestWaveformRulesSyncPushAndGatewayPullEndpoint(t *testing.T) {
	gwID := "0194f000-2000-7000-8000-000000000001"
	tenantID := "0194f000-0000-7000-8000-000000000001"
	siteID := "0194f000-1000-7000-8000-000000000001"

	store := &fakeWaveformStore{
		fakeDiagnosticsStore: &fakeDiagnosticsStore{},
		tenantID:             tenantID,
		siteID:               siteID,
	}
	var synced []edgecontrol.DiagnosticsDownlinkPayload
	bridge := NewDiagnosticsMQTTBridge()
	bridge.AttachPublisher(func(_ context.Context, targetGW string, cmd edgecontrol.DiagnosticsDownlinkPayload) error {
		if targetGW != gwID {
			t.Fatalf("unexpected gateway push target %q", targetGW)
		}
		if cmd.Action == edgecontrol.DiagnosticsActionSyncWaveformRules {
			synced = append(synced, cmd)
		}
		return nil
	})
	authorize := func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: "0194f000-4000-7000-8000-000000000001"}, nil
	}
	verifier := fakeGatewayPeerVerifier{gatewayID: gwID}
	tap := packettap.NewManager()
	diagHandler := NewDiagnosticsHandlerWithWaveformAndVerifier(store, tap, verifier, authorize, bridge)
	wrapped := NewCredentialHandler(nil, authorize, WithDiagnosticsHandler(diagHandler))

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}

	doOperator := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}

	// POST creates rule and pushes SYNC_WAVEFORM_RULES with 1 rule.
	createBody, _ := json.Marshal(UpsertWaveformRuleInput{
		RuleName: "出水温度高限", Enabled: true, DeviceCode: "chiller-01", PointCode: "chw_supply_temp",
		TriggerType: edgecontrol.WaveformTriggerThresholdHigh, ThresholdValue: 11.0,
		PreSeconds: 20, PostSeconds: 40, SampleRateHz: 1, PointCodes: []string{"chiller-01.chw_supply_temp"}, CooldownSeconds: 60,
	})
	if rec := doOperator(http.MethodPost, "/internal/v1/gateways/"+gwID+"/waveform-rules", createBody); rec.Code != http.StatusCreated {
		t.Fatalf("POST waveform-rules status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(synced) != 1 || len(synced[0].Rules) != 1 || synced[0].Rules[0].RuleName != "出水温度高限" {
		t.Fatalf("expected SYNC_WAVEFORM_RULES after POST, got %+v", synced)
	}
	ruleID := synced[0].Rules[0].RuleID

	// PUT updates rule and pushes updated rule set.
	updateBody, _ := json.Marshal(UpsertWaveformRuleInput{
		RuleName: "出水温度高限-更新", Enabled: true, DeviceCode: "chiller-01", PointCode: "chw_supply_temp",
		TriggerType: edgecontrol.WaveformTriggerThresholdHigh, ThresholdValue: 12.5,
		PreSeconds: 20, PostSeconds: 40, SampleRateHz: 1, PointCodes: []string{"chiller-01.chw_supply_temp"}, CooldownSeconds: 60,
	})
	if rec := doOperator(http.MethodPut, "/internal/v1/gateways/"+gwID+"/waveform-rules/"+ruleID, updateBody); rec.Code != http.StatusOK {
		t.Fatalf("PUT waveform-rules status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(synced) != 2 || len(synced[1].Rules) != 1 || synced[1].Rules[0].RuleName != "出水温度高限-更新" {
		t.Fatalf("expected SYNC_WAVEFORM_RULES after PUT, got %+v", synced)
	}

	// Build a valid gateway client cert + PoP signature for GET /gateway/v1/waveform-rules.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(11),
		Subject:      pkix.Name{CommonName: gwID},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(11),
		Subject:      pkix.Name{CommonName: gwID},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certHeader := url.QueryEscape(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))

	gwReq := httptest.NewRequest(http.MethodGet, "https://localhost/gateway/v1/waveform-rules", nil)
	gwReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	gwReq.Header.Set("X-Gateway-Client-Cert", certHeader)
	if err := edgecontrol.SignGatewayRequestPoP(gwReq, der, key, time.Now().UTC()); err != nil {
		t.Fatalf("SignGatewayRequestPoP: %v", err)
	}
	gwRec := httptest.NewRecorder()
	wrapped.ServeHTTP(gwRec, gwReq)
	if gwRec.Code != http.StatusOK {
		t.Fatalf("GET /gateway/v1/waveform-rules status=%d body=%s", gwRec.Code, gwRec.Body.String())
	}
	var pullResp struct {
		GatewayID string                           `json:"gatewayId"`
		Rules     []edgecontrol.WaveformRuleConfig `json:"rules"`
	}
	if err := json.Unmarshal(gwRec.Body.Bytes(), &pullResp); err != nil {
		t.Fatalf("decode gateway waveform-rules response: %v", err)
	}
	if pullResp.GatewayID != gwID || len(pullResp.Rules) != 1 || pullResp.Rules[0].RuleID != ruleID || pullResp.Rules[0].RuleName != "出水温度高限-更新" {
		t.Fatalf("unexpected gateway waveform-rules response: %+v", pullResp)
	}

	// Missing X-Gateway-Client-Cert is rejected with 400.
	noCertReq := httptest.NewRequest(http.MethodGet, "/gateway/v1/waveform-rules", nil)
	noCertReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	noCertRec := httptest.NewRecorder()
	wrapped.ServeHTTP(noCertRec, noCertReq)
	if noCertRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when peer cert missing, got %d", noCertRec.Code)
	}

	// DELETE clears rule and pushes empty SYNC_WAVEFORM_RULES.
	if rec := doOperator(http.MethodDelete, "/internal/v1/gateways/"+gwID+"/waveform-rules/"+ruleID, nil); rec.Code != http.StatusOK {
		t.Fatalf("DELETE waveform-rules status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(synced) != 3 || len(synced[2].Rules) != 0 {
		t.Fatalf("expected empty SYNC_WAVEFORM_RULES after DELETE, got %+v", synced)
	}
}
