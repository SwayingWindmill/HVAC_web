package gateway

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/services/identity-service/pkg/identityadmin"
)

type memoryAuditRecorder struct {
	mu      sync.Mutex
	events  []auditserver.DomainEvent
	payload [][]byte
}

func (rec *memoryAuditRecorder) GetRecord(_ context.Context, _, _ string) (auditserver.Record, error) {
	return auditserver.Record{}, auditserver.ErrRecordNotFound
}

func (rec *memoryAuditRecorder) ConsumeDomain(_ context.Context, payload []byte, _ auditserver.MessageMetadata) (bool, error) {
	var event auditserver.DomainEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return false, err
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.events = append(rec.events, event)
	rec.payload = append(rec.payload, append([]byte(nil), payload...))
	return true, nil
}

func (rec *memoryAuditRecorder) SearchRecords(_ context.Context, _ string, _ auditserver.SearchFilter) ([]auditserver.SearchRecord, error) {
	return nil, nil
}

func (rec *memoryAuditRecorder) snapshot() ([]auditserver.DomainEvent, [][]byte) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	events := append([]auditserver.DomainEvent(nil), rec.events...)
	payloads := make([][]byte, len(rec.payload))
	for i, raw := range rec.payload {
		payloads[i] = append([]byte(nil), raw...)
	}
	return events, payloads
}

func attachTestAuditHandler(t *testing.T, h *handler, signer crypto.Signer, now time.Time) *memoryAuditRecorder {
	t.Helper()
	uri, err := url.Parse("spiffe://hvac.local/platform-gateway")
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "platform-gateway"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &memoryAuditRecorder{}
	auditHandler := auditserver.NewHandler(auditserver.Config{
		Store:                 recorder,
		Searcher:              recorder,
		DomainWriter:          recorder,
		AllowedWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
		Now:                   func() time.Time { return now },
	})
	h.identity.config.AuditURL = "https://audit.example.test"
	h.identity.config.AuditAudience = "audit-ledger-service"
	h.identity.config.AuditHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.TLS = &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{cert},
				VerifiedChains:   [][]*x509.Certificate{{cert}},
			}
			rr := httptest.NewRecorder()
			auditHandler.ServeHTTP(rr, clone)
			return rr.Result(), nil
		}),
	}
	return recorder
}

type stubUserDirectory struct {
	changeErr error
}

func (s *stubUserDirectory) CreateUser(context.Context, identityadmin.NewUser) (identityadmin.User, error) {
	return identityadmin.User{}, nil
}
func (s *stubUserDirectory) UsersByID(context.Context, []string) ([]identityadmin.User, error) {
	return nil, nil
}
func (s *stubUserDirectory) SetPassword(context.Context, string, string, string) error { return nil }
func (s *stubUserDirectory) SetEnabled(context.Context, string, string, bool) error    { return nil }
func (s *stubUserDirectory) UpdateProfile(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubUserDirectory) ChangePassword(context.Context, string, string, string) error {
	return s.changeErr
}

func TestExtractClientIPTrustBoundaryAndProxyHeaders(t *testing.T) {
	directPublic := httptest.NewRequest(http.MethodPost, "/", nil)
	directPublic.RemoteAddr = "203.0.113.50:4444"
	directPublic.Header.Set("X-Forwarded-For", "198.51.100.99")
	directPublic.Header.Set("X-Real-IP", "198.51.100.88")
	if got := extractClientIP(directPublic); got != "203.0.113.50" {
		t.Fatalf("public RemoteAddr trusted spoofed headers: got %q", got)
	}

	proxied := httptest.NewRequest(http.MethodPost, "/", nil)
	proxied.RemoteAddr = "172.18.0.2:50000"
	proxied.Header.Set("X-Forwarded-For", "127.0.0.1, 203.0.113.77")
	if got := extractClientIP(proxied); got != "203.0.113.77" {
		t.Fatalf("trusted proxy XFF failed: got %q", got)
	}

	// Poisoned left entry before nginx-appended rightmost client IP still resolves rightmost valid IP.
	poisonedLeft := httptest.NewRequest(http.MethodPost, "/", nil)
	poisonedLeft.RemoteAddr = "172.18.0.2:50000"
	poisonedLeft.Header.Set("X-Forwarded-For", "garbage, 203.0.113.77")
	poisonedLeft.Header.Set("X-Real-IP", "198.51.100.10")
	if got := extractClientIP(poisonedLeft); got != "203.0.113.77" {
		t.Fatalf("expected rightmost valid IP behind nginx, got %q", got)
	}

	// Unparseable rightmost XFF never falls back to left entry or unstripped X-Real-IP.
	malformedXFF := httptest.NewRequest(http.MethodPost, "/", nil)
	malformedXFF.RemoteAddr = "127.0.0.1:41000"
	malformedXFF.Header.Set("X-Forwarded-For", "198.51.100.1, not-an-ip")
	malformedXFF.Header.Set("X-Real-IP", "198.51.100.10")
	if got := extractClientIP(malformedXFF); got != "127.0.0.1" {
		t.Fatalf("malformed XFF must fall back to RemoteAddr, got %q", got)
	}

	// Nginx does not set or strip X-Real-IP, so it must never be trusted on its own.
	realIPOnly := httptest.NewRequest(http.MethodPost, "/", nil)
	realIPOnly.RemoteAddr = "127.0.0.1:41000"
	realIPOnly.Header.Set("X-Real-IP", "198.51.100.10")
	if got := extractClientIP(realIPOnly); got != "127.0.0.1" {
		t.Fatalf("unstripped X-Real-IP must not be trusted: got %q", got)
	}

	multiHeader := httptest.NewRequest(http.MethodPost, "/", nil)
	multiHeader.RemoteAddr = "172.18.0.2:50000"
	multiHeader.Header.Add("X-Forwarded-For", "198.51.100.1")
	multiHeader.Header.Add("X-Forwarded-For", "203.0.113.88")
	if got := extractClientIP(multiHeader); got != "203.0.113.88" {
		t.Fatalf("multi-header XFF must prefer rightmost hop: got %q", got)
	}

	directLAN := httptest.NewRequest(http.MethodPost, "/", nil)
	directLAN.RemoteAddr = "[::1]:51000"
	if got := extractClientIP(directLAN); got != "::1" {
		t.Fatalf("loopback IPv6 parse failed: got %q", got)
	}

	emptyReq := httptest.NewRequest(http.MethodPost, "/", nil)
	emptyReq.RemoteAddr = ""
	if got := extractClientIP(emptyReq); got != "" {
		t.Fatalf("empty RemoteAddr got %q", got)
	}
}

func TestEncodeRedactedChangeSummaryProtectsSecrets(t *testing.T) {
	raw := encodeRedactedChangeSummary(map[string]any{
		"credentialKind":   "CERTIFICATE",
		"passwordChanged":  true,
		"password":         "SuperSecret123!",
		"otp":              "654321",
		"passwordHash":     "sha256:secret-hash",
		"verificationCode": "123456",
		"enrollmentCode":   "ENROLL-999",
		"nested": map[string]any{
			"token":           "bearer-xyz",
			"afterMode":       "MANUAL_OVERRIDE",
			"passwordChanged": "fake-secret-value",
		},
		"tags": map[string]string{
			"password": "tag-secret-pw",
			"vendor":   "trane",
		},
		"items": []any{
			map[string]any{"secretKey": "slice-secret", "code": "CH-01"},
		},
		"rows": []map[string]any{
			{"hash": "row-hash-secret", "name": "row-1"},
		},
	})
	for _, leaked := range []string{
		"SuperSecret123!", "654321", "secret-hash", "123456",
		"ENROLL-999", "bearer-xyz", "fake-secret-value",
		"tag-secret-pw", "slice-secret", "row-hash-secret",
	} {
		if bytes.Contains(raw, []byte(leaked)) {
			t.Fatalf("secret %q leaked in redacted change summary: %s", leaked, raw)
		}
	}
	for _, wanted := range []string{
		`"passwordChanged":true`,
		`"credentialKind":"CERTIFICATE"`,
		`"afterMode":"MANUAL_OVERRIDE"`,
		`"vendor":"trane"`,
		`"code":"CH-01"`,
		`"name":"row-1"`,
	} {
		if !bytes.Contains(raw, []byte(wanted)) {
			t.Fatalf("expected %s in change summary: %s", wanted, raw)
		}
	}
}

func TestGatewayUnifiedDomainAuditAcrossControlScheduleCredentialsAndIAM(t *testing.T) {
	fix := newCommandGatewayFixture(t)
	recorder := attachTestAuditHandler(t, fix.handler, fix.gatewaySigner, fix.now)

	// 1. Remote command submit + reconcile + cancel.
	submitReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(
		`{"assetId":"`+fix.assetID+`","commandPointId":"`+fix.commandPointID+`","parameters":{"setpointC":23.5}}`,
	))
	submitReq.RemoteAddr = "172.18.0.5:41000"
	submitReq.Header.Set("X-Forwarded-For", "198.51.100.25")
	submitReq.Header.Set("Content-Type", "application/json")
	submitReq.Header.Set("Idempotency-Key", "audit-cmd-submit-1")
	fix.authenticate(submitReq, true)
	submitRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(submitRec, submitReq)
	if submitRec.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", submitRec.Code, submitRec.Body.String())
	}
	var submitted commandView
	if err := json.NewDecoder(submitRec.Body).Decode(&submitted); err != nil {
		t.Fatal(err)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+submitted.CommandID+"/cancel", strings.NewReader(`{"reason":"操作员撤销"}`))
	cancelReq.RemoteAddr = "10.10.1.9:9001"
	cancelReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(cancelReq, true)
	cancelRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	// 2. Control mode + asset control lock + denied control exit.
	fix.control.IAM.DenyScheduleManage = true
	deniedCtrl := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fix.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"现场检修"}`))
	deniedCtrl.RemoteAddr = "10.10.1.10:9002"
	deniedCtrl.Header.Set("Content-Type", "application/json")
	fix.authenticate(deniedCtrl, true)
	deniedCtrlRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(deniedCtrlRec, deniedCtrl)
	if deniedCtrlRec.Code != http.StatusForbidden {
		t.Fatalf("denied control mode status=%d", deniedCtrlRec.Code)
	}
	fix.control.IAM.DenyScheduleManage = false

	okLock := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+fix.assetID+"/control-lock", strings.NewReader(`{"lockMode":"MAINTENANCE_LOCK","reason":"保养锁定"}`))
	okLock.RemoteAddr = "10.10.1.11:9003"
	okLock.Header.Set("Content-Type", "application/json")
	fix.authenticate(okLock, true)
	okLockRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(okLockRec, okLock)
	if okLockRec.Code != http.StatusOK {
		t.Fatalf("lock status=%d body=%s", okLockRec.Code, okLockRec.Body.String())
	}

	// 3. IAM own password change (success + rejected) with zero password leakage.
	dir := &stubUserDirectory{}
	fix.handler.identity.config.UserDirectory = dir
	pwReq := httptest.NewRequest(http.MethodPost, publicOwnPasswordPath, strings.NewReader(`{"currentPassword":"OldPassword1!","newPassword":"NewPassword2!"}`))
	pwReq.RemoteAddr = "10.10.1.12:9004"
	pwReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(pwReq, true)
	pwRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(pwRec, pwReq)
	if pwRec.Code != http.StatusNoContent {
		t.Fatalf("own password status=%d body=%s", pwRec.Code, pwRec.Body.String())
	}

	dir.changeErr = identityadmin.ErrInvalidCredentials
	pwFail := httptest.NewRequest(http.MethodPost, publicOwnPasswordPath, strings.NewReader(`{"currentPassword":"WrongPassword!","newPassword":"NewPassword2!"}`))
	pwFail.RemoteAddr = "10.10.1.12:9005"
	pwFail.Header.Set("Content-Type", "application/json")
	fix.authenticate(pwFail, true)
	pwFailRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(pwFailRec, pwFail)
	if pwFailRec.Code != http.StatusBadRequest {
		t.Fatalf("own password fail status=%d", pwFailRec.Code)
	}

	events, payloads := recorder.snapshot()
	if len(events) != 6 {
		t.Fatalf("expected 6 domain audit events, got %d: %#v", len(events), events)
	}
	if events[0].Category != auditserver.AggregateTypeRemoteCommand || events[0].Action != "COMMAND_SUBMITTED" || events[0].ClientIP != "198.51.100.25" || events[0].ClientKind != auditserver.ClientKindBrowser || events[0].TargetLabel != "温度设定指令" {
		t.Fatalf("unexpected command submit audit event: %#v", events[0])
	}
	if events[1].Action != "COMMAND_CANCELED" || events[1].Outcome != auditOutcomeSucceeded || events[1].TargetLabel != "取消控制指令（温度设定）" {
		t.Fatalf("unexpected command cancel audit event: %#v", events[1])
	}
	if events[2].Category != auditserver.AggregateTypeControlLock || events[2].Action != "SITE_CONTROL_MODE_UPDATED" || events[2].Outcome != auditOutcomeDenied || events[2].TargetLabel != "站点控制模式（本地手动）" {
		t.Fatalf("unexpected denied control mode audit event: %#v", events[2])
	}
	if events[3].Category != auditserver.AggregateTypeControlLock || events[3].Action != "ASSET_CONTROL_LOCK_UPDATED" || events[3].Outcome != auditOutcomeSucceeded || events[3].TargetLabel != "设备控制锁定（维保锁定）" {
		t.Fatalf("unexpected control lock audit event: %#v", events[3])
	}
	if events[4].Category != auditserver.AggregateTypeIAMAdmin || events[4].Action != "IAM_OWN_PASSWORD_CHANGED" || events[4].Outcome != auditOutcomeSucceeded || !bytes.Contains(events[4].ChangeSummary, []byte(`"passwordChanged":true`)) {
		t.Fatalf("unexpected own password success event: %#v (summary=%s)", events[4], events[4].ChangeSummary)
	}
	if events[5].Action != "IAM_OWN_PASSWORD_CHANGED" || events[5].Outcome != auditOutcomeRejected || !bytes.Contains(events[5].ChangeSummary, []byte(`"passwordChanged":false`)) {
		t.Fatalf("unexpected own password rejected event: %#v (summary=%s)", events[5], events[5].ChangeSummary)
	}
	for _, raw := range payloads {
		if bytes.Contains(raw, []byte("OldPassword1!")) || bytes.Contains(raw, []byte("NewPassword2!")) || bytes.Contains(raw, []byte("WrongPassword!")) {
			t.Fatalf("password leaked into domain audit payload: %s", raw)
		}
	}

	// 4. Control exit post-auth validation error + malformed-200 failure path.
	badMode := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fix.siteID+"/control-mode", strings.NewReader(`{"controlMode":"BOGUS"}`))
	badMode.RemoteAddr = "10.10.1.13:9006"
	badMode.Header.Set("Content-Type", "application/json")
	fix.authenticate(badMode, true)
	badModeRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(badModeRec, badMode)
	if badModeRec.Code != http.StatusBadRequest {
		t.Fatalf("bad control mode status=%d", badModeRec.Code)
	}

	origTransport := fix.handler.command.httpClient.Transport
	fix.handler.command.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"not":"valid-control-mode"}`)),
		}, nil
	})
	malformedMode := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fix.siteID+"/control-mode", strings.NewReader(`{"controlMode":"REMOTE_AUTO","reason":"恢复自动"}`))
	malformedMode.RemoteAddr = "10.10.1.14:9007"
	malformedMode.Header.Set("Content-Type", "application/json")
	fix.authenticate(malformedMode, true)
	malformedRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(malformedRec, malformedMode)
	if malformedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("malformed control mode status=%d", malformedRec.Code)
	}
	fix.handler.command.httpClient.Transport = origTransport

	events, _ = recorder.snapshot()
	if len(events) != 8 {
		t.Fatalf("expected 8 audit events after rejected/malformed control-exit, got %d", len(events))
	}
	if events[6].Outcome != auditOutcomeRejected || !bytes.Contains(events[6].ChangeSummary, []byte(`"reasonCode":"CONTROL_EXIT_REQUEST_INVALID"`)) {
		t.Fatalf("unexpected rejected control mode event: %#v", events[6])
	}
	if events[7].Outcome != auditOutcomeFailed || !bytes.Contains(events[7].ChangeSummary, []byte(`"reasonCode":"COMMAND_RESPONSE_INVALID"`)) {
		t.Fatalf("unexpected malformed control mode event: %#v", events[7])
	}
}

func TestGatewayRegistryScheduleAndCredentialsAuditRespectReadsAndClientKind(t *testing.T) {
	regFix := newRegistryCSRFFixture(t)
	recorder := attachTestAuditHandler(t, regFix.handler, regFix.gatewaySigner, regFix.now)

	// Mini-program session writing an asset + denied write.
	miniToken := "mini-program-bearer-token"
	csrfCipher, err := regFix.handler.identity.encryptBytes([]byte("mini-csrf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := regFix.store.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientMiniProgram, ID: miniToken,
		Principal: identitycontext.UserPrincipal{Subject: "mini-op", DisplayName: "王值班", Issuer: "https://issuer.example.test", Roles: []string{"site-manager"}},
		TenantID:  regFix.tenantID, CSRFTokenCiphertext: csrfCipher, ExpiresAt: regFix.now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "mini-1",
		TraceID: strings.Repeat("c", 32), Traceparent: "00-" + strings.Repeat("c", 32) + "-" + strings.Repeat("d", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: regFix.now,
	}); err != nil {
		t.Fatal(err)
	}
	regFix.handler.registry.coreBaseURL = "https://registry.example.test"
	regFix.handler.registry.coreHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/operating-schedule") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"timezone":"Asia/Shanghai","slots":[{"dayOfWeek":1,"startMinute":480,"endMinute":1080}]}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"` + regFix.assetID + `","displayName":"2号离心机"}`)),
			}, nil
		}),
	}

	// Write registry asset with real Core Registry displayName + meta.reason from MINI_PROGRAM.
	assetReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+regFix.siteID+"/assets", strings.NewReader(
		`{"displayName":"2号离心机","code":"CH-02","meta":{"expectedRevision":0,"idempotencyKey":"reg-create-1","reason":"现场新增离心机"}}`,
	))
	assetReq.RemoteAddr = "10.20.30.5:4400"
	assetReq.Header.Set("Authorization", "Bearer "+miniToken)
	assetReq.Header.Set("Content-Type", "application/json")
	assetRec := httptest.NewRecorder()
	regFix.handler.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusCreated {
		t.Fatalf("registry asset status=%d body=%s", assetRec.Code, assetRec.Body.String())
	}

	// Read operating-schedule (must NOT emit audit) vs PUT operating-schedule (must emit meta.reason & schedule metadata).
	getSched := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+regFix.siteID+"/operating-schedule", nil)
	regFix.authenticate(getSched, false)
	getSchedRec := httptest.NewRecorder()
	regFix.handler.ServeHTTP(getSchedRec, getSched)
	if getSchedRec.Code != http.StatusOK {
		t.Fatalf("get schedule status=%d body=%s", getSchedRec.Code, getSchedRec.Body.String())
	}

	putSched := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+regFix.siteID+"/operating-schedule", strings.NewReader(
		`{"name":"夏季制冷计划","preCoolLeadMinutes":45,"coastDownLagMinutes":30,"slots":[{"dayOfWeek":1,"startMinute":480,"endMinute":1080}],"meta":{"expectedRevision":1,"idempotencyKey":"sched-put-1","reason":"迎峰度夏调整"}}`,
	))
	putSched.RemoteAddr = "10.20.30.6:4401"
	putSched.Header.Set("Content-Type", "application/json")
	regFix.authenticate(putSched, true)
	putSchedRec := httptest.NewRecorder()
	regFix.handler.ServeHTTP(putSchedRec, putSched)
	if putSchedRec.Code != http.StatusOK {
		t.Fatalf("put schedule status=%d body=%s", putSchedRec.Code, putSchedRec.Body.String())
	}

	// Gateway credentials: POST enrollment-code (redacts raw code!) + GET credentials (no-op).
	credFix := newGatewayCredentialsFixture(t)
	credRecorder := attachTestAuditHandler(t, credFix.handler, credFix.gatewaySigner, credFix.now)
	credFix.handler.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"items":[]}`))}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"enrollmentCode":"SECRET-JOIN-9999","expiresAt":"2026-07-26T16:00:00Z"}`)),
				}, nil
			}),
		},
	}

	getCred := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+credFix.gatewayID+"/credentials", nil)
	credFix.authenticate(getCred, false)
	getCredRec := httptest.NewRecorder()
	credFix.handler.ServeHTTP(getCredRec, getCred)
	if getCredRec.Code != http.StatusOK {
		t.Fatalf("get credentials status=%d", getCredRec.Code)
	}

	postCode := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+credFix.gatewayID+"/enrollment-code", nil)
	postCode.RemoteAddr = "10.20.30.8:4402"
	credFix.authenticate(postCode, true)
	postCodeRec := httptest.NewRecorder()
	credFix.handler.ServeHTTP(postCodeRec, postCode)
	if postCodeRec.Code != http.StatusOK {
		t.Fatalf("post enrollment-code status=%d body=%s", postCodeRec.Code, postCodeRec.Body.String())
	}

	regEvents, _ := recorder.snapshot()
	if len(regEvents) != 2 {
		t.Fatalf("expected 2 registry/schedule events (reads excluded), got %d: %#v", len(regEvents), regEvents)
	}
	if regEvents[0].Category != auditserver.AggregateTypeRegistryMutation || regEvents[0].ClientKind != auditserver.ClientKindMiniProgram || regEvents[0].ActorDisplay != "王值班" || regEvents[0].TargetLabel != "2号离心机" {
		t.Fatalf("unexpected mini-program registry event: %#v", regEvents[0])
	}
	if bytes.Contains(regEvents[0].ChangeSummary, []byte(`"meta"`)) || !bytes.Contains(regEvents[0].ChangeSummary, []byte(`"displayName"`)) || !bytes.Contains(regEvents[0].ChangeSummary, []byte(`"reason":"现场新增离心机"`)) {
		t.Fatalf("unexpected registry changeSummary: %s", regEvents[0].ChangeSummary)
	}
	if regEvents[1].Category != auditserver.AggregateTypeOperatingSchedule || regEvents[1].Action != "OPERATING_SCHEDULE_UPDATED" || regEvents[1].TargetLabel != "站点运行计划（夏季制冷计划）" {
		t.Fatalf("unexpected operating schedule event: %#v", regEvents[1])
	}
	for _, wanted := range []string{
		`"scheduleName":"夏季制冷计划"`,
		`"preCoolLeadMinutes":45`,
		`"coastDownLagMinutes":30`,
		`"entriesCount":1`,
		`"reason":"迎峰度夏调整"`,
	} {
		if !bytes.Contains(regEvents[1].ChangeSummary, []byte(wanted)) {
			t.Fatalf("expected %s in operating schedule summary: %s", wanted, regEvents[1].ChangeSummary)
		}
	}

	credEvents, credPayloads := credRecorder.snapshot()
	if len(credEvents) != 1 || credEvents[0].Category != auditserver.AggregateTypeGatewayCredential || credEvents[0].Action != "GATEWAY_ENROLLMENT_CODE_ISSUED" {
		t.Fatalf("expected 1 gateway credential event, got %#v", credEvents)
	}
	if bytes.Contains(credPayloads[0], []byte("SECRET-JOIN-9999")) {
		t.Fatalf("enrollment code leaked into audit payload: %s", credPayloads[0])
	}
}

func TestExecuteCommandCreateEmitsSystemAuditOnSuccessAndFailure(t *testing.T) {
	fix := newCommandGatewayFixture(t)
	ctrl := fix.handler
	recorder := attachTestAuditHandler(t, ctrl, fix.gatewaySigner, fix.now)

	prepared := preparedCommand{
		tenantID:             fix.tenantID,
		siteID:               fix.siteID,
		deviceID:             fix.deviceID,
		pointID:              fix.commandPointID,
		principalID:          fix.principalID,
		idempotencyKey:       "auto-opt:01990000-8800-7000-8000-000000000299",
		capability:           commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		parameters:           commandmodel.CommandParameters{"setpointC": 8.0},
		verificationPointKey: "chilled_water_supply_temp_sp",
		priorityTier:         commandmodel.PriorityAIOptimization,
		commandSource:        commandmodel.SourceOptimization,
		preconditionMode:     commandmodel.PreconditionRequireCurrentState,
		grant:                "signed-grant",
	}

	ctrl.command.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusAccepted, `{"schemaVersion":1,"commandId":"`+fix.commandID+`","tenantId":"`+fix.tenantID+`","siteId":"`+fix.siteID+`","deviceId":"`+fix.deviceID+`","pointId":"`+fix.commandPointID+`","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","capabilityRevision":"capability:set-chilled-water-temperature-setpoint:v1","status":"QUEUED","risk":"LOW","approvalPolicy":"NONE","approvalCount":0,"requiredApprovalCount":0,"parameters":{"setpointC":8},"deviceCommandSequence":1,"version":1,"snapshotRevision":1,"transitions":[{"toStatus":"QUEUED","reason":"SUBMITTED","actorType":"WORKLOAD","occurredAt":"2026-07-26T15:00:00Z","version":1}],"createdAt":"2026-07-26T15:00:00Z","updatedAt":"2026-07-26T15:00:00Z"}`), nil
		}),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fix.siteID+"/optimization/auto-commands", nil)
	req.RemoteAddr = "172.18.0.9:55001"
	view, status, _, failure := ctrl.executeCommandCreate(context.Background(), req, prepared)
	if failure != nil || status != http.StatusAccepted || view.CommandID != fix.commandID {
		t.Fatalf("unexpected success path result: status=%d failure=%#v", status, failure)
	}

	ctrl.command.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusConflict, `{"code":"COMMAND_GUARDRAIL_BLOCKED","title":"Blocked","detail":"Guardrail active"}`), nil
		}),
	}
	_, _, _, failure = ctrl.executeCommandCreate(context.Background(), req, prepared)
	if failure == nil || failure.code != "COMMAND_GUARDRAIL_BLOCKED" {
		t.Fatalf("expected guardrail failure, got %#v", failure)
	}

	events, _ := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 SYSTEM command audit events, got %d: %#v", len(events), events)
	}
	if events[0].ClientKind != auditserver.ClientKindSystem || events[0].Outcome != auditOutcomeSucceeded || events[0].TargetID != fix.commandID || events[0].TargetLabel != "冷冻水温度设定指令" {
		t.Fatalf("unexpected auto-command success audit event: %#v", events[0])
	}
	if events[1].ClientKind != auditserver.ClientKindSystem || events[1].Outcome != auditOutcomeRejected || !bytes.Contains(events[1].ChangeSummary, []byte(`"reasonCode":"COMMAND_GUARDRAIL_BLOCKED"`)) {
		t.Fatalf("unexpected auto-command failure audit event: %#v", events[1])
	}
}
