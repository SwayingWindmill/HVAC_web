package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

type transferGatewayFixture struct {
	*commandGatewayFixture
	siteB                    string
	tenantB                  string
	siteB1                   string
	siteB2                   string
	siteOutside              string
	transferID               string
	targetDeviceID           string
	resetID                  string
	recorder                 *memoryAuditRecorder
	coreCalls                atomic.Int32
	connectivityCalls        atomic.Int32
	lastCoreDelegation       string
	lastCoreTargetDelegation string
	lastCoreBody             string
	lastConnMethod           string
	lastConnPath             string
	lastConnBody             string
}

func newTransferGatewayFixture(
	t *testing.T,
	iamAllowTenantB bool,
	iamTenantBSites []string,
	connHandler func(req *http.Request) (*http.Response, error),
	coreHandler func(req *http.Request) (*http.Response, error),
) *transferGatewayFixture {
	t.Helper()
	base := newCommandGatewayFixture(t)
	now := base.now
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion": 1,
		"registryRevision": 95,
		"routes": [
			{"method":"POST","path":"/api/v1/sites/{siteId}/devices/{deviceId}/transfer","owner":"platform-core-service","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","device","principal"]},
			{"method":"POST","path":"/api/v1/gateways/{gatewayId}/factory-reset","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","device","principal"]},
			{"method":"GET","path":"/api/v1/gateways/{gatewayId}/factory-resets","owner":"connectivity","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["tenant","site","device","principal"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })
	recorder := attachTestAuditHandler(t, base.handler, base.gatewaySigner, now)

	fix := &transferGatewayFixture{
		commandGatewayFixture: base,
		siteB:                 "018f3d00-1000-7000-8000-0000000000b1",
		tenantB:               "018f3d00-0000-7000-8000-000000000002",
		siteB1:                "018f3d00-1000-7000-8000-000000000b01",
		siteB2:                "018f3d00-1000-7000-8000-000000000b02",
		siteOutside:           "018f3d00-1000-7000-8000-000000000999",
		transferID:            "0195f300-5000-7000-8000-000000000101",
		targetDeviceID:        "0195f300-3000-7000-8000-000000000202",
		resetID:               "0195f300-6000-7000-8000-000000000303",
		recorder:              recorder,
	}

	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		base.iamCalls.Add(1)
		parent, verifyErr := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if verifyErr != nil {
			return nil, verifyErr
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			t.Fatal("invalid Registry decision request")
		}
		switch input.TenantID {
		case base.tenantID:
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID, fix.siteB},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: base.tenantID, AllowedSiteIDs: []string{base.siteID, fix.siteB},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		case fix.tenantB:
			if !iamAllowTenantB {
				return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
					Decision: registryauth.Decision{
						Allowed: false, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
						TenantID: fix.tenantB, Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
						ReasonCode: registryauth.ReasonDenyMembershipRequired, DecidedAt: now.Format(time.RFC3339Nano),
					},
				}), nil
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fix.tenantB, AllowedSiteIDs: append([]string(nil), iamTenantBSites...),
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "target-tenant-" + randomURLToken(12),
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fix.tenantB, AllowedSiteIDs: append([]string(nil), iamTenantBSites...),
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		default:
			return telemetryJSONResponse(http.StatusForbidden, map[string]string{"code": "FORBIDDEN"}), nil
		}
	})}

	base.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL:      "https://core-registry.example.test",
		CoreTimeout:      2 * time.Second,
		MaxResponseBytes: 1 << 20,
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.coreCalls.Add(1)
			fix.lastCoreDelegation = request.Header.Get("X-Delegation-Grant")
			fix.lastCoreTargetDelegation = request.Header.Get("X-Target-Delegation-Grant")
			raw, _ := io.ReadAll(request.Body)
			fix.lastCoreBody = string(raw)
			if coreHandler != nil {
				return coreHandler(request)
			}
			return jsonHTTPResponse(http.StatusCreated, `{
				"transferId": "`+fix.transferID+`",
				"transferScope": "CROSS_SITE",
				"sourceTenantId": "`+base.tenantID+`",
				"sourceSiteId": "`+base.siteID+`",
				"sourceDeviceId": "`+base.deviceID+`",
				"sourceDeviceRevision": 2,
				"targetTenantId": "`+base.tenantID+`",
				"targetSiteId": "`+fix.siteB+`",
				"targetDeviceId": "`+fix.targetDeviceID+`",
				"wipeRequired": true,
				"reason": "move to site B",
				"transferredBy": "`+base.principalID+`",
				"transferredAt": "2026-07-26T15:00:00.000Z",
				"sourceDevice": {
					"id": "`+base.deviceID+`",
					"tenantId": "`+base.tenantID+`",
					"siteId": "`+base.siteID+`",
					"code": "gw-01",
					"displayName": "Gateway 01",
					"deviceType": "GATEWAY",
					"connectivityMode": "GATEWAY_PROXIED",
					"status": "RETIRED",
					"revision": 2,
					"createdAt": "2026-07-26T14:00:00.000Z",
					"updatedAt": "2026-07-26T15:00:00.000Z"
				},
				"targetDevice": {
					"id": "`+fix.targetDeviceID+`",
					"tenantId": "`+base.tenantID+`",
					"siteId": "`+fix.siteB+`",
					"code": "gw-01",
					"displayName": "Gateway 01",
					"deviceType": "GATEWAY",
					"connectivityMode": "GATEWAY_PROXIED",
					"status": "ACTIVE",
					"revision": 1,
					"createdAt": "2026-07-26T15:00:00.000Z",
					"updatedAt": "2026-07-26T15:00:00.000Z"
				},
				"replayed": false
			}`), nil
		})},
	})

	base.handler.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.connectivityCalls.Add(1)
			fix.lastConnMethod = request.Method
			fix.lastConnPath = request.URL.Path
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				fix.lastConnBody = string(raw)
			} else {
				fix.lastConnBody = ""
			}
			if connHandler != nil {
				return connHandler(request)
			}
			return jsonHTTPResponse(http.StatusOK, `{"gatewayId":"`+base.deviceID+`","transferId":"`+fix.transferID+`","credentialsRevoked":true,"factoryReset":{"id":"`+fix.resetID+`","status":"DISPATCHED"}}`), nil
		})},
	}

	return fix
}

func TestGatewayRegistryDeviceTransferRejectsForgedTargetDelegationGrantHeader(t *testing.T) {
	fix := newTransferGatewayFixture(t, true, nil, nil, nil)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/transfer",
		strings.NewReader(`{"targetSiteId":"`+fix.siteB+`","meta":{"expectedRevision":1,"idempotencyKey":"transfer-forged-1","reason":"test"}}`),
	)
	fix.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Target-Delegation-Grant", "forged-target-grant")

	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"FORGED_IDENTITY_HEADER"`) {
		t.Fatalf("expected 400 FORGED_IDENTITY_HEADER, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fix.iamCalls.Load() != 0 || fix.coreCalls.Load() != 0 || fix.connectivityCalls.Load() != 0 {
		t.Fatalf("expected zero upstream calls on forged header, got iam=%d core=%d conn=%d", fix.iamCalls.Load(), fix.coreCalls.Load(), fix.connectivityCalls.Load())
	}
}

func TestGatewayRegistryDeviceTransferCrossSiteEnrichesConnectivityAndEmitsAudit(t *testing.T) {
	fix := newTransferGatewayFixture(t, true, nil, nil, nil)

	// 1. Authorized cross-site transfer enriches response with synchronous :transfer-decommission and emits audit.
	payload := `{"targetSiteId":"` + fix.siteB + `","wipeEdgeCache":true,"meta":{"expectedRevision":1,"idempotencyKey":"transfer-cross-site-1","reason":"relocate gateway"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/transfer", strings.NewReader(payload))
	fix.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("cross-site transfer status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fix.coreCalls.Load() != 1 || fix.lastCoreDelegation == "" || fix.lastCoreTargetDelegation != "" {
		t.Fatalf("unexpected Core headers: calls=%d deleg=%q targetDeleg=%q", fix.coreCalls.Load(), fix.lastCoreDelegation, fix.lastCoreTargetDelegation)
	}
	if fix.connectivityCalls.Load() != 1 || fix.lastConnMethod != http.MethodPost || fix.lastConnPath != "/internal/v1/gateways/"+fix.deviceID+":transfer-decommission" {
		t.Fatalf("unexpected Connectivity call: calls=%d %s %s", fix.connectivityCalls.Load(), fix.lastConnMethod, fix.lastConnPath)
	}
	var connReq map[string]any
	if err := json.Unmarshal([]byte(fix.lastConnBody), &connReq); err != nil || connReq["tenantId"] != fix.tenantID || connReq["transferId"] != fix.transferID || connReq["wipeRequired"] != true || connReq["actorId"] != fix.principalID {
		t.Fatalf("unexpected Connectivity decommission body: %s (%v)", fix.lastConnBody, err)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["oldCredentialsRevoked"] != true || resp["factoryResetId"] != fix.resetID || resp["factoryResetStatus"] != "DISPATCHED" {
		t.Fatalf("expected Connectivity enrichment in response, got %s", rec.Body.String())
	}

	events, _ := fix.recorder.snapshot()
	if len(events) != 1 || events[0].Category != auditserver.AggregateTypeRegistryMutation || events[0].Action != "REGISTRY_DEVICE_TRANSFERRED" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
	if !bytes.Contains(events[0].ChangeSummary, []byte(`"factoryResetStatus":"DISPATCHED"`)) || !bytes.Contains(events[0].ChangeSummary, []byte(`"oldCredentialsRevoked":true`)) {
		t.Fatalf("missing reset details in audit ChangeSummary: %s", events[0].ChangeSummary)
	}

	// 2. Destination site outside principal's AllowedSiteIDs is rejected with 403 REGISTRY_TRANSFER_FORBIDDEN before Core.
	deniedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/transfer",
		strings.NewReader(`{"targetSiteId":"`+fix.siteOutside+`","meta":{"expectedRevision":1,"idempotencyKey":"transfer-cross-site-denied","reason":"unauthorized site"}}`),
	)
	fix.authenticate(deniedReq, true)
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden || !strings.Contains(deniedRec.Body.String(), `"REGISTRY_TRANSFER_FORBIDDEN"`) {
		t.Fatalf("expected 403 REGISTRY_TRANSFER_FORBIDDEN, got status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}
	if fix.coreCalls.Load() != 1 {
		t.Fatalf("Core must not be called when destination site is out of scope, coreCalls=%d", fix.coreCalls.Load())
	}
}

func TestGatewayRegistryDeviceTransferCrossTenantDualIAMAndDenial(t *testing.T) {
	// 1. Authorized cross-tenant transfer sends BOTH source and target delegation grants to Core.
	fix := newTransferGatewayFixture(t, true, []string{testSiteB1ForFixture()}, nil, nil)
	fix.siteB1 = testSiteB1ForFixture()

	reqBody := `{"targetTenantId":"` + fix.tenantB + `","targetSiteId":"` + fix.siteB1 + `","meta":{"expectedRevision":1,"idempotencyKey":"transfer-cross-tenant-1","reason":"customer handoff"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/transfer", strings.NewReader(reqBody))
	fix.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("cross-tenant transfer status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fix.iamCalls.Load() != 2 || fix.coreCalls.Load() != 1 || fix.lastCoreDelegation == "" || fix.lastCoreTargetDelegation == "" || fix.lastCoreDelegation == fix.lastCoreTargetDelegation {
		t.Fatalf("expected 2 IAM calls and distinct X-Delegation-Grant / X-Target-Delegation-Grant headers, got iam=%d core=%d same=%v",
			fix.iamCalls.Load(), fix.coreCalls.Load(), fix.lastCoreDelegation == fix.lastCoreTargetDelegation)
	}

	// 2. Denied destination tenant permission returns 403 REGISTRY_TRANSFER_FORBIDDEN without calling Core.
	deniedTenantFix := newTransferGatewayFixture(t, false, nil, nil, nil)
	deniedReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+deniedTenantFix.siteID+"/devices/"+deniedTenantFix.deviceID+"/transfer", strings.NewReader(reqBody))
	deniedTenantFix.authenticate(deniedReq, true)
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedRec := httptest.NewRecorder()
	deniedTenantFix.handler.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden || !strings.Contains(deniedRec.Body.String(), `"REGISTRY_TRANSFER_FORBIDDEN"`) || deniedTenantFix.coreCalls.Load() != 0 {
		t.Fatalf("denied tenant expected 403 REGISTRY_TRANSFER_FORBIDDEN with 0 Core calls, got status=%d core=%d body=%s",
			deniedRec.Code, deniedTenantFix.coreCalls.Load(), deniedRec.Body.String())
	}

	// 3. Tenant B allowed on siteB2 ONLY (not requested siteB1) returns 403 REGISTRY_TRANSFER_FORBIDDEN without calling Core.
	wrongSiteFix := newTransferGatewayFixture(t, true, []string{"018f3d00-1000-7000-8000-000000000b02"}, nil, nil)
	wrongSiteReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+wrongSiteFix.siteID+"/devices/"+wrongSiteFix.deviceID+"/transfer", strings.NewReader(reqBody))
	wrongSiteFix.authenticate(wrongSiteReq, true)
	wrongSiteReq.Header.Set("Content-Type", "application/json")
	wrongSiteRec := httptest.NewRecorder()
	wrongSiteFix.handler.ServeHTTP(wrongSiteRec, wrongSiteReq)
	if wrongSiteRec.Code != http.StatusForbidden || !strings.Contains(wrongSiteRec.Body.String(), `"REGISTRY_TRANSFER_FORBIDDEN"`) || wrongSiteFix.coreCalls.Load() != 0 {
		t.Fatalf("wrong target site expected 403 REGISTRY_TRANSFER_FORBIDDEN with 0 Core calls, got status=%d core=%d body=%s",
			wrongSiteRec.Code, wrongSiteFix.coreCalls.Load(), wrongSiteRec.Body.String())
	}
}

func testSiteB1ForFixture() string {
	return "018f3d00-1000-7000-8000-000000000b01"
}

func TestGatewayRegistryDeviceTransferConnectivityOutageStillReturnsCore201(t *testing.T) {
	fix := newTransferGatewayFixture(t, true, nil, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connectivity temporarily unreachable")
	}, nil)

	payload := `{"targetSiteId":"` + fix.siteB + `","meta":{"expectedRevision":1,"idempotencyKey":"transfer-conn-down-1","reason":"relocate while conn down"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/transfer", strings.NewReader(payload))
	fix.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected committed Core 201 even if Connectivity call fails, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp["transferId"] != fix.transferID {
		t.Fatalf("unexpected fallback body=%s err=%v", rec.Body.String(), err)
	}
	if _, hasRevoked := resp["oldCredentialsRevoked"]; hasRevoked {
		t.Fatalf("expected no oldCredentialsRevoked when Connectivity failed, got %s", rec.Body.String())
	}
}

func TestGatewayRegistryDeviceTransferFactoryResetProxyAndAudit(t *testing.T) {
	const gwID = "018f3d00-2000-7000-8000-000000000001"
	fix := newTransferGatewayFixture(t, true, nil, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return jsonHTTPResponse(http.StatusOK, `{"id":"0195f300-6000-7000-8000-000000000404","tenantId":"`+testTenantIDForGatewayTest()+`","gatewayId":"`+gwID+`","wipeCredentials":true,"wipeConfigShadow":true,"wipeOfflineBuffer":true,"reason":"operator reset","status":"DISPATCHED","issuedBy":"`+testPrincipalIDForGatewayTest()+`","wipedArtifacts":[],"createdAt":"2026-07-26T15:00:00Z","updatedAt":"2026-07-26T15:00:00Z"}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"items":[{"id":"0195f300-6000-7000-8000-000000000404","status":"DISPATCHED"}]}`), nil
	}, nil)

	// 1. POST without CSRF fails 403 before touching IAM or Connectivity.
	noCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+gwID+"/factory-reset", strings.NewReader(`{"reason":"operator reset"}`))
	fix.authenticate(noCSRF, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, noCSRF)
	if rec.Code != http.StatusForbidden || fix.iamCalls.Load() != 0 || fix.connectivityCalls.Load() != 0 {
		t.Fatalf("missing CSRF got status=%d iam=%d conn=%d", rec.Code, fix.iamCalls.Load(), fix.connectivityCalls.Load())
	}

	// 2. POST with CSRF proxies to Connectivity and emits GATEWAY_FACTORY_RESET_ISSUED audit.
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/"+gwID+"/factory-reset", strings.NewReader(`{"reason":"operator reset"}`))
	fix.authenticate(postReq, true)
	postReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, postReq)
	if rec.Code != http.StatusOK || fix.lastConnMethod != http.MethodPost || fix.lastConnPath != "/internal/v1/gateways/"+gwID+"/factory-reset" || fix.lastConnBody != `{"reason":"operator reset"}` {
		t.Fatalf("POST factory-reset got status=%d %s %s body=%q resp=%s", rec.Code, fix.lastConnMethod, fix.lastConnPath, fix.lastConnBody, rec.Body.String())
	}

	events, _ := fix.recorder.snapshot()
	if len(events) != 1 || events[0].Category != auditserver.AggregateTypeGatewayCredential || events[0].Action != "GATEWAY_FACTORY_RESET_ISSUED" {
		t.Fatalf("unexpected factory-reset audit events: %#v", events)
	}

	// 3. GET /factory-resets proxies to Connectivity cleanly.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+gwID+"/factory-resets", nil)
	fix.authenticate(getReq, false)
	rec = httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, getReq)
	if rec.Code != http.StatusOK || fix.lastConnMethod != http.MethodGet || fix.lastConnPath != "/internal/v1/gateways/"+gwID+"/factory-resets" || !strings.Contains(rec.Body.String(), "DISPATCHED") {
		t.Fatalf("GET factory-resets got status=%d %s %s resp=%s", rec.Code, fix.lastConnMethod, fix.lastConnPath, rec.Body.String())
	}

	// 4. safeLogPath templates transfer and factory-reset paths.
	if got := safeLogPath("/api/v1/sites/" + fix.siteID + "/devices/" + fix.deviceID + "/transfer"); got != "/api/v1/sites/{siteId}/devices/{deviceId}/transfer" {
		t.Fatalf("safeLogPath transfer=%q", got)
	}
	if got := safeLogPath("/api/v1/gateways/" + gwID + "/factory-reset"); got != "/api/v1/gateways/{gatewayId}/factory-reset" {
		t.Fatalf("safeLogPath factory-reset=%q", got)
	}
}

func testTenantIDForGatewayTest() string {
	return "018f3d00-0000-7000-8000-000000000001"
}

func testPrincipalIDForGatewayTest() string {
	return "018f3d00-9000-7000-8000-000000000001"
}
