package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
)

func TestGatewayReplaceDeviceHardwareAuthCSRFConflictsAndAudit(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	recorder := attachTestAuditHandler(t, fix.handler, fix.gatewaySigner, fix.now)

	var coreStatus int
	var coreRespBody string
	var sawInternalPath string

	fix.handler.registry.coreHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		fix.coreCalls.Add(1)
		sawInternalPath = req.URL.Path
		return &http.Response{
			StatusCode: coreStatus,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(coreRespBody)),
		}, nil
	})}

	routePath := "/api/v1/sites/" + fix.siteID + "/devices/" + fix.deviceID + "/replace-hardware"
	validPayload := `{
		"newExternalId":"SN-METER-2026-888",
		"newProtocolAddress":{"modbusUnit":3,"secretNote":"should-not-leak-in-audit-values"},
		"meta":{"expectedRevision":2,"idempotencyKey":"gw-hw-replace-01","reason":"更换故障总表并保持历史数据连续"}
	}`

	// 1. Happy path -> 201 Created, forwards Core response, records rich redacted domain audit.
	coreStatus = http.StatusCreated
	coreRespBody = `{
		"replacementId":"019a1111-0000-7000-8000-000000000001",
		"tenantId":"` + fix.tenantID + `",
		"siteId":"` + fix.siteID + `",
		"deviceId":"` + fix.deviceID + `",
		"revision":3,
		"previousExternalId":"SN-METER-OLD-111",
		"newExternalId":"SN-METER-2026-888",
		"previousGatewayDeviceId":"018f3e00-4000-7000-8000-000000000100",
		"newGatewayDeviceId":"018f3e00-4000-7000-8000-000000000100",
		"previousProtocolAddress":{"modbusUnit":1},
		"newProtocolAddress":{"modbusUnit":3},
		"reason":"更换故障总表并保持历史数据连续",
		"replacedBy":"operator-1",
		"replacedAt":"2026-11-18T10:00:00.000Z",
		"device":{
			"id":"` + fix.deviceID + `",
			"tenantId":"` + fix.tenantID + `",
			"siteId":"` + fix.siteID + `",
			"code":"hvac-meter",
			"displayName":"1号冷站总电表",
			"deviceType":"POWER_METER",
			"status":"ACTIVE",
			"revision":3,
			"externalId":"SN-METER-2026-888",
			"createdAt":"2026-09-28T07:15:32.297Z",
			"updatedAt":"2026-11-18T10:00:00.000Z"
		},
		"replayed":false
	}`

	req := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(validPayload))
	req.RemoteAddr = "10.30.40.50:51234"
	req.Header.Set("Content-Type", "application/json")
	fix.authenticate(req, true)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("success status=%d want=201 body=%s", rec.Code, rec.Body.String())
	}
	if sawInternalPath != "/internal/v1/registry/sites/"+fix.siteID+"/devices/"+fix.deviceID+"/replace-hardware" {
		t.Fatalf("unexpected internal core path: %s", sawInternalPath)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode gateway response: %v", err)
	}
	if decoded["deviceId"] != fix.deviceID || decoded["newExternalId"] != "SN-METER-2026-888" {
		t.Fatalf("unexpected gateway payload: %#v", decoded)
	}

	events, payloads := recorder.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	ev := events[0]
	if ev.Action != "REGISTRY_DEVICE_HARDWARE_REPLACED" ||
		ev.Outcome != auditOutcomeSucceeded ||
		ev.TargetID != fix.deviceID ||
		ev.TargetLabel != "1号冷站总电表" ||
		ev.ClientIP != "10.30.40.50" ||
		ev.ClientKind != auditserver.ClientKindBrowser {
		t.Fatalf("unexpected audit event metadata: %#v", ev)
	}
	for _, needle := range []string{
		`"previousExternalId":"SN-METER-OLD-111"`,
		`"newExternalId":"SN-METER-2026-888"`,
		`"reason":"更换故障总表并保持历史数据连续"`,
	} {
		if !bytes.Contains(ev.ChangeSummary, []byte(needle)) {
			t.Fatalf("audit summary missing %s: %s", needle, ev.ChangeSummary)
		}
	}
	if bytes.Contains(payloads[0], []byte("should-not-leak-in-audit-values")) {
		t.Fatalf("protocol address values leaked into audit summary: %s", payloads[0])
	}

	// 2. Stale revision conflict -> 409 REGISTRY_REVISION_CONFLICT.
	coreStatus = http.StatusConflict
	coreRespBody = `{"code":"REGISTRY_REVISION_CONFLICT","title":"Revision conflict","detail":"stale"}`
	conflictReq := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(validPayload))
	conflictReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(conflictReq, true)
	conflictRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(conflictRec, conflictReq)
	assertRegistryProblem(t, conflictRec, http.StatusConflict, "REGISTRY_REVISION_CONFLICT")

	// 3. Invalid payload / 400 REGISTRY_MUTATION_INVALID forwarded cleanly.
	coreStatus = http.StatusBadRequest
	coreRespBody = `{"code":"REGISTRY_MUTATION_INVALID","title":"Invalid","detail":"bad external id"}`
	badReq := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(`{"newExternalId":"","meta":{"expectedRevision":1,"idempotencyKey":"gw-hw-bad-01","reason":"bad"}}`))
	badReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(badReq, true)
	badRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(badRec, badReq)
	assertRegistryProblem(t, badRec, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID")

	// 4. Missing Origin -> 403 ORIGIN_NOT_ALLOWED; Missing CSRF -> 403 CSRF_TOKEN_INVALID before hitting IAM or Core.
	beforeCore := fix.coreCalls.Load()
	noOriginReq := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(validPayload))
	noOriginReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(noOriginReq, false)
	noOriginRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noOriginRec, noOriginReq)
	assertRegistryProblem(t, noOriginRec, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")

	noCSRFReq := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(validPayload))
	noCSRFReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(noCSRFReq, false)
	noCSRFReq.Header.Set("Origin", "https://web.example.test")
	noCSRFRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noCSRFRec, noCSRFReq)
	assertRegistryProblem(t, noCSRFRec, http.StatusForbidden, "CSRF_TOKEN_INVALID")
	if fix.coreCalls.Load() != beforeCore {
		t.Fatal("CSRF/Origin rejection reached Core")
	}

	// 5. Cross-site nondiscovery -> 404 RESOURCE_NOT_FOUND before reaching Core.
	otherSiteID := "018f3e00-1000-7000-8000-000000000099"
	crossSiteReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+otherSiteID+"/devices/"+fix.deviceID+"/replace-hardware", strings.NewReader(validPayload))
	crossSiteReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(crossSiteReq, true)
	crossSiteRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(crossSiteRec, crossSiteReq)
	assertRegistryProblem(t, crossSiteRec, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	if fix.coreCalls.Load() != beforeCore {
		t.Fatal("cross-site request reached Core")
	}

	// 6. Read-only viewer lacking device.write -> 403 AUTHORIZATION_DENIED + denied audit.
	fix.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, _ := identitycontext.VerifyDelegation(&fix.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
			Decision: registryauth.Decision{
				Allowed:        false,
				PrincipalID:    fix.principalID,
				SubjectIssuer:  parent.SubjectIssuer,
				Subject:        parent.Subject,
				TenantID:       fix.tenantID,
				AllowedSiteIDs: []string{fix.siteID},
				Actions:        nil,
				PolicyRevision: "registry-write:1/iam:1",
				ReasonCode:     registryauth.ReasonDenyActionNotGranted,
				DecidedAt:      fix.now.Format(time.RFC3339Nano),
			},
		}), nil
	})}
	viewerReq := httptest.NewRequest(http.MethodPost, routePath, strings.NewReader(validPayload))
	viewerReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(viewerReq, true)
	viewerRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(viewerRec, viewerReq)
	assertRegistryProblem(t, viewerRec, http.StatusForbidden, "AUTHORIZATION_DENIED")
	if fix.coreCalls.Load() != beforeCore {
		t.Fatal("read-only viewer reached Core")
	}
}
