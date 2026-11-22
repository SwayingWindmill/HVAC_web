package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/limitpolicy"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type capturedOTAAudit struct {
	mu     sync.Mutex
	events []auditserver.DomainEvent
}

func (c *capturedOTAAudit) GetRecord(_ context.Context, _, _ string) (auditserver.Record, error) {
	return auditserver.Record{}, auditserver.ErrRecordNotFound
}

func (c *capturedOTAAudit) ConsumeDomain(_ context.Context, payload []byte, _ auditserver.MessageMetadata) (bool, error) {
	var event auditserver.DomainEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return true, nil
}

func (c *capturedOTAAudit) list() []auditserver.DomainEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]auditserver.DomainEvent(nil), c.events...)
}

type otaGatewayFixture struct {
	*commandGatewayFixture
	campaignID              string
	packageID               string
	upstreamCalls           atomic.Int32
	upstreamStatus          int
	upstreamBody            string
	upstreamErr             error
	lastUpstreamMethod      string
	lastUpstreamPath        string
	lastUpstreamRawQuery    string
	lastUpstreamContentType string
	lastUpstreamBody        []byte
	lastUpstreamTraceparent string
	lastAuthAction          registryauth.Action
	audits                  *capturedOTAAudit
}

func newOTAGatewayFixture(t *testing.T) *otaGatewayFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := base.now.UTC()
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 93,
		"routes": [
			{"method":"GET","path":"/api/v1/ota/packages","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","principal"]},
			{"method":"POST","path":"/api/v1/ota/packages","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","principal"]},
			{"method":"GET","path":"/api/v1/ota/campaigns","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","principal"]},
			{"method":"POST","path":"/api/v1/ota/campaigns","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","principal"]},
			{"method":"GET","path":"/api/v1/ota/campaigns/{campaignId}","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/ota/campaigns/{campaignId}/pause","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/ota/campaigns/{campaignId}/resume","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]},
			{"method":"POST","path":"/api/v1/ota/campaigns/{campaignId}/rollback","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	capturedAudit := &capturedOTAAudit{}
	spiffeURI, _ := url.Parse(base.handler.identity.config.ExecutingWorkloadSPIFFE)
	auditCertTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(202),
		Subject:      pkix.Name{CommonName: "platform-gateway"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		URIs:         []*url.URL{spiffeURI},
	}
	auditCertDER, err := x509.CreateCertificate(rand.Reader, auditCertTmpl, auditCertTmpl, base.gatewaySigner.Public(), base.gatewaySigner)
	if err != nil {
		t.Fatal(err)
	}
	auditCert, err := x509.ParseCertificate(auditCertDER)
	if err != nil {
		t.Fatal(err)
	}
	auditHandler := auditserver.NewHandler(auditserver.Config{
		Store:                 capturedAudit,
		DomainWriter:          capturedAudit,
		AllowedWorkloadSPIFFE: base.handler.identity.config.ExecutingWorkloadSPIFFE,
		Now:                   func() time.Time { return base.now.UTC() },
	})
	base.handler.identity.config.AuditURL = "https://audit.example.test"
	base.handler.identity.config.AuditAudience = "audit-ledger-service"
	base.handler.identity.config.AuditHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.TLS = &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{auditCert},
				VerifiedChains:   [][]*x509.Certificate{{auditCert}},
			}
			rr := httptest.NewRecorder()
			auditHandler.ServeHTTP(rr, clone)
			return rr.Result(), nil
		}),
	}

	fix := &otaGatewayFixture{
		commandGatewayFixture: base,
		packageID:             "0194f040-2000-7000-8000-000000000001",
		campaignID:            "0194f040-3000-7000-8000-000000000001",
		upstreamStatus:        http.StatusOK,
		upstreamBody:          `{"items":[]}`,
		audits:                capturedAudit,
	}
	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		base.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return nil, err
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			t.Fatal("invalid Registry decision request")
		}
		fix.lastAuthAction = input.Action
		claims := registryauth.GrantClaims{
			Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
			PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID},
			Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
			DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
			IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
		}
		decision := registryauth.Decision{
			Allowed: true, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID},
			Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
			ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
	})}
	base.handler.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.upstreamCalls.Add(1)
			fix.lastUpstreamMethod = request.Method
			fix.lastUpstreamPath = request.URL.Path
			fix.lastUpstreamRawQuery = request.URL.RawQuery
			fix.lastUpstreamContentType = request.Header.Get("Content-Type")
			fix.lastUpstreamTraceparent = request.Header.Get("traceparent")
			if request.Header.Get("X-Delegation-Grant") == "" {
				t.Fatal("missing X-Delegation-Grant")
			}
			fix.lastUpstreamBody = nil
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				fix.lastUpstreamBody = raw
			}
			if fix.upstreamErr != nil {
				return nil, fix.upstreamErr
			}
			return &http.Response{
				StatusCode: fix.upstreamStatus,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(fix.upstreamBody)),
			}, nil
		})},
	}
	return fix
}

func TestGatewayOTAProxyUploadCampaignsAuditAndLimits(t *testing.T) {
	fix := newOTAGatewayFixture(t)

	// 1. GET /api/v1/ota/packages forwards query and traceparent.
	const wantTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	pkgListReq := httptest.NewRequest(http.MethodGet, "/api/v1/ota/packages?component=EDGE_GATEWAY&limit=20", nil)
	fix.authenticate(pkgListReq, false)
	pkgListReq.Header.Set("traceparent", "00-"+wantTraceID+"-00f067aa0ba902b7-01")
	pkgListRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(pkgListRec, pkgListReq)
	if pkgListRec.Code != http.StatusOK || fix.lastUpstreamMethod != http.MethodGet ||
		fix.lastUpstreamPath != "/internal/v1/ota/packages" ||
		fix.lastUpstreamRawQuery != "component=EDGE_GATEWAY&limit=20" ||
		fix.lastAuthAction != registryauth.ActionDeviceRead ||
		pkgListRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET packages = %d method=%s path=%s query=%q", pkgListRec.Code, fix.lastUpstreamMethod, fix.lastUpstreamPath, fix.lastUpstreamRawQuery)
	}
	parsedTrace, ok := observability.ParseTraceparent(fix.lastUpstreamTraceparent, "")
	if !ok || parsedTrace.TraceID != wantTraceID {
		t.Fatalf("traceparent=%q ok=%v", fix.lastUpstreamTraceparent, ok)
	}

	// 2. POST /api/v1/ota/packages without CSRF rejected before upstream.
	beforeCalls := fix.upstreamCalls.Load()
	noCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/ota/packages", strings.NewReader("ignored"))
	fix.authenticate(noCSRFReq, false)
	noCSRFRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noCSRFRec, noCSRFReq)
	if noCSRFRec.Code != http.StatusForbidden || fix.upstreamCalls.Load() != beforeCalls {
		t.Fatalf("missing CSRF got %d upstream=%d", noCSRFRec.Code, fix.upstreamCalls.Load())
	}

	// 3. Non-multipart POST /api/v1/ota/packages returns 400 and logs rejected audit.
	badCTReq := httptest.NewRequest(http.MethodPost, "/api/v1/ota/packages", strings.NewReader(`{"version":"1.5.0"}`))
	fix.authenticate(badCTReq, true)
	badCTReq.Header.Set("Content-Type", "application/json")
	badCTRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(badCTRec, badCTReq)
	if badCTRec.Code != http.StatusBadRequest || !strings.Contains(badCTRec.Body.String(), "OTA_REQUEST_INVALID") {
		t.Fatalf("non-multipart upload = %d %s", badCTRec.Code, badCTRec.Body.String())
	}

	// 4. Multipart POST /api/v1/ota/packages streams multipart body + emits OTA_PACKAGE_UPLOADED audit.
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	_ = mw.WriteField("version", "1.5.0")
	fw, _ := mw.CreateFormFile("file", "edge.bin")
	_, _ = fw.Write([]byte("ota-stream-payload"))
	_ = mw.Close()
	wantMPBytes := append([]byte(nil), mpBuf.Bytes()...)

	fix.upstreamStatus = http.StatusCreated
	fix.upstreamBody = `{"id":"` + fix.packageID + `","version":"1.5.0","component":"EDGE_GATEWAY","targetArch":"armv7l"}`
	upReq := httptest.NewRequest(http.MethodPost, "/api/v1/ota/packages", bytes.NewReader(wantMPBytes))
	fix.authenticate(upReq, true)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusCreated || fix.lastUpstreamPath != "/internal/v1/ota/packages" ||
		!bytes.Equal(fix.lastUpstreamBody, wantMPBytes) || fix.lastAuthAction != registryauth.ActionDeviceWrite {
		t.Fatalf("multipart upload = %d path=%s ct=%q bytesMatch=%v body=%s", upRec.Code, fix.lastUpstreamPath, fix.lastUpstreamContentType, bytes.Equal(fix.lastUpstreamBody, wantMPBytes), upRec.Body.String())
	}

	// 5. Campaign routes: create, list, detail, pause, resume, rollback (including empty optional body).
	fix.upstreamStatus = http.StatusCreated
	fix.upstreamBody = `{"id":"` + fix.campaignID + `","status":"CANARY_RUNNING"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/ota/campaigns", strings.NewReader(`{"name":"Canary 1.5.0","packageId":"`+fix.packageID+`","canaryPercent":20,"failureRateThresholdPct":50,"reason":"rollout"}`))
	fix.authenticate(createReq, true)
	createRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated || fix.lastUpstreamPath != "/internal/v1/ota/campaigns" {
		t.Fatalf("POST campaigns = %d body=%s", createRec.Code, createRec.Body.String())
	}

	fix.upstreamStatus = http.StatusOK
	fix.upstreamBody = `{"id":"` + fix.campaignID + `","status":"PAUSED"}`
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/ota/campaigns?status=PAUSED", ""},
		{http.MethodGet, "/api/v1/ota/campaigns/" + fix.campaignID, ""},
		{http.MethodPost, "/api/v1/ota/campaigns/" + fix.campaignID + "/pause", `{"reason":"hold"}`},
		{http.MethodPost, "/api/v1/ota/campaigns/" + fix.campaignID + "/resume", ""},
		{http.MethodPost, "/api/v1/ota/campaigns/" + fix.campaignID + "/rollback", ""},
	} {
		var bodyReader io.Reader
		if tc.body != "" {
			bodyReader = strings.NewReader(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.path, bodyReader)
		fix.authenticate(req, tc.method == http.MethodPost)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	events := fix.audits.list()
	if len(events) < 6 {
		t.Fatalf("expected >= 6 OTA domain audit events recorded, got %d: %+v", len(events), events)
	}
	actions := map[string]bool{}
	for _, ev := range events {
		actions[ev.Action] = true
		if ev.Category != auditserver.AggregateTypeGatewayCredential {
			t.Fatalf("unexpected OTA audit category %q", ev.Category)
		}
	}
	for _, wantAction := range []string{
		"OTA_PACKAGE_UPLOADED",
		"OTA_CAMPAIGN_CREATED",
		"OTA_CAMPAIGN_PAUSED",
		"OTA_CAMPAIGN_RESUMED",
		"OTA_CAMPAIGN_ROLLED_BACK",
	} {
		if !actions[wantAction] {
			t.Fatalf("missing OTA domain audit action %q in %v", wantAction, actions)
		}
	}

	// 6. Rate limiter & safeLogPath normalization.
	fix.handler.rateLimiter = limitpolicy.NewLimiter(limitpolicy.NewMemoryCounter(1000), &limitpolicy.Policy{
		Version: 1,
		Limits: []limitpolicy.Limit{
			{Dimension: limitpolicy.DimensionRest, Window: time.Minute, Burst: 100},
			{Dimension: limitpolicy.DimensionCommandWrite, Window: time.Minute, Burst: 1, FailClosed: true},
		},
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ota/campaigns/"+fix.campaignID+"/pause", nil)
		req.RemoteAddr = "10.0.0.1:1002"
		fix.authenticate(req, true)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		if i == 0 && rec.Code != http.StatusOK {
			t.Fatalf("first pause expected 200, got %d", rec.Code)
		}
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second pause expected 429, got %d", rec.Code)
		}
	}

	for raw, want := range map[string]string{
		"/api/v1/ota/packages":                                  "/api/v1/ota/packages",
		"/api/v1/ota/campaigns":                                 "/api/v1/ota/campaigns",
		"/api/v1/ota/campaigns/" + fix.campaignID:               "/api/v1/ota/campaigns/{campaignId}",
		"/api/v1/ota/campaigns/" + fix.campaignID + "/pause":    "/api/v1/ota/campaigns/{campaignId}/pause",
		"/api/v1/ota/campaigns/" + fix.campaignID + "/resume":   "/api/v1/ota/campaigns/{campaignId}/resume",
		"/api/v1/ota/campaigns/" + fix.campaignID + "/rollback": "/api/v1/ota/campaigns/{campaignId}/rollback",
	} {
		if got := safeLogPath(raw); got != want {
			t.Fatalf("safeLogPath(%q) = %q, want %q", raw, got, want)
		}
	}
}
