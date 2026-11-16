package gateway

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

func TestPublicIntelligenceRoutesHaveSingleDomainOwners(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, test := range []struct {
		method string
		path   string
		owner  string
		siteID string
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/load", ownershipregistry.OwnerForecast, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/forecast/pv", ownershipregistry.OwnerForecast, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/fdd/findings", ownershipregistry.OwnerFDD, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/fdd/rules/low-delta-t", ownershipregistry.OwnerFDD, siteID},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/fdd/evaluate/low-delta-t", ownershipregistry.OwnerFDD, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/recommendations/latest", ownershipregistry.OwnerOptimization, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/preparations/latest", ownershipregistry.OwnerOptimization, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/shadow-evaluations/latest", ownershipregistry.OwnerOptimization, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/staging-recommendations/latest", ownershipregistry.OwnerOptimization, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/auto-control-lease", ownershipregistry.OwnerCore, siteID},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/optimization/auto-control-lease", ownershipregistry.OwnerCore, siteID},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/optimization/auto-control-lease/revoke", ownershipregistry.OwnerCore, siteID},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/optimization/execution-monitors", ownershipregistry.OwnerCore, siteID},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/optimization/auto-commands", ownershipregistry.OwnerCommand, siteID},
		{http.MethodPost, "/api/v1/optimization/runs", ownershipregistry.OwnerOptimization, ""},
	} {
		route, ok := matchPublicIntelligenceRoute(test.method, test.path)
		if !ok || route.owner != test.owner || route.siteID != test.siteID {
			t.Fatalf("route %s %s resolved as %#v ok=%t", test.method, test.path, route, ok)
		}
	}
}

func TestPublicFDDLinkRouteIsPatchOnly(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	findingID := "01990000-7000-7000-8000-000000000001"
	path := "/api/v1/sites/" + siteID + "/fdd/findings/" + findingID + "/links"
	route, ok := matchPublicIntelligenceRoute(http.MethodPatch, path)
	if !ok || route.owner != ownershipregistry.OwnerFDD || route.siteID != siteID || route.findingID != findingID || route.target != "fdd-link" {
		t.Fatalf("FDD link route resolved as %#v ok=%t", route, ok)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if _, ok := matchPublicIntelligenceRoute(method, path); ok {
			t.Fatalf("%s unexpectedly matched FDD link route", method)
		}
	}
}

func TestWorkOrderOriginatesFromExactAlarm(t *testing.T) {
	alarmID := "01990000-7100-7000-8000-000000000001"
	workOrder := workordermodel.WorkOrder{SourceReferences: []workordermodel.SourceReference{{
		Domain: workordermodel.SourceAlarm, ResourceID: alarmID, Relationship: workordermodel.RelationshipOrigin,
	}}}
	if !workOrderOriginatesFromAlarm(workOrder, alarmID) {
		t.Fatal("matching ALARM origin was not recognized")
	}
	for _, reference := range []workordermodel.SourceReference{
		{Domain: workordermodel.SourceAlarm, ResourceID: "01990000-7100-7000-8000-000000000002", Relationship: workordermodel.RelationshipOrigin},
		{Domain: workordermodel.SourceAlarm, ResourceID: alarmID, Relationship: workordermodel.RelationshipRelated},
		{Domain: workordermodel.SourceAsset, ResourceID: alarmID, Relationship: workordermodel.RelationshipOrigin},
	} {
		workOrder.SourceReferences = []workordermodel.SourceReference{reference}
		if workOrderOriginatesFromAlarm(workOrder, alarmID) {
			t.Fatalf("non-matching Work Order provenance was accepted: %#v", reference)
		}
	}
}

func TestIntelligenceRoutesRejectWrongMethods(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, path := range []string{
		"/api/v1/sites/" + siteID + "/forecast/load",
		"/api/v1/sites/" + siteID + "/forecast/pv",
		"/api/v1/sites/" + siteID + "/fdd/findings",
		"/api/v1/sites/" + siteID + "/optimization/shadow-evaluations/latest",
		"/api/v1/sites/" + siteID + "/optimization/staging-recommendations/latest",
	} {
		if _, ok := matchPublicIntelligenceRoute(http.MethodPost, path); ok {
			t.Fatalf("POST unexpectedly matched read-only Intelligence route %s", path)
		}
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodGet, "/api/v1/sites/"+siteID+"/fdd/evaluate/low-delta-t"); ok {
		t.Fatal("GET unexpectedly matched FDD evaluation route")
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodGet, publicOptimizationRunsPath); ok {
		t.Fatal("GET unexpectedly matched Optimization create route")
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodDelete, "/api/v1/sites/"+siteID+"/optimization/auto-control-lease"); ok {
		t.Fatal("DELETE unexpectedly matched auto-control-lease route")
	}
	if _, ok := matchPublicIntelligenceRoute(http.MethodGet, "/api/v1/sites/"+siteID+"/optimization/auto-commands"); ok {
		t.Fatal("GET unexpectedly matched auto-commands route")
	}
}

// A client tells "this Site has no result yet" from "this deployment does not serve the route" by
// the code alone, so an owner router's plain-text 404 must not read as a missing result (#A5).
func TestIntelligenceNotFoundSeparatesMissingRouteFromMissingResult(t *testing.T) {
	h := &handler{intelligence: &intelligenceController{}}
	for _, testCase := range []struct {
		name string
		raw  []byte
		code string
	}{
		{"owner router 404", []byte("404 page not found\n"), "ROUTE_NOT_FOUND"},
		{"empty 404", nil, "ROUTE_NOT_FOUND"},
		{"owner JSON not-found", []byte(`{"error":"optimization_not_found"}`), "RESOURCE_NOT_FOUND"},
		{"owner problem not-found", []byte(`{"status":404,"code":"REPORT_SNAPSHOT_NOT_FOUND"}`), "RESOURCE_NOT_FOUND"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			h.writeIntelligenceResult(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sites/x/forecast/load", nil), testCase.raw, http.StatusNotFound, nil)
			assertProblemCode(t, recorder, http.StatusNotFound, testCase.code)
		})
	}
}

func TestIntelligenceReadWithoutConfiguredOwnerIsAMissingRoute(t *testing.T) {
	h := &handler{intelligence: &intelligenceController{}}
	siteID := "01990000-5000-7000-8000-000000000001"
	for _, testCase := range []struct {
		name  string
		serve func(http.ResponseWriter, *http.Request, bffSession, publicIntelligenceRoute)
		route publicIntelligenceRoute
	}{
		{"forecast", h.serveForecast, publicIntelligenceRoute{siteID: siteID, target: "load"}},
		{"fdd findings", h.serveFDDRead, publicIntelligenceRoute{siteID: siteID, target: "fdd-findings"}},
		{"fdd link", h.serveFDDLink, publicIntelligenceRoute{siteID: siteID, findingID: siteID}},
		{"optimization latest", func(w http.ResponseWriter, r *http.Request, s bffSession, route publicIntelligenceRoute) {
			h.serveLatestOptimizationRead(w, r, s, route, "recommendations")
		}, publicIntelligenceRoute{siteID: siteID}},
		{"optimization preparation", func(w http.ResponseWriter, r *http.Request, s bffSession, route publicIntelligenceRoute) {
			h.serveLatestOptimizationRead(w, r, s, route, "preparations")
		}, publicIntelligenceRoute{siteID: siteID}},
		{"optimization shadow", func(w http.ResponseWriter, r *http.Request, s bffSession, route publicIntelligenceRoute) {
			h.serveLatestOptimizationRead(w, r, s, route, "shadow-evaluations")
		}, publicIntelligenceRoute{siteID: siteID}},
		{"optimization staging", func(w http.ResponseWriter, r *http.Request, s bffSession, route publicIntelligenceRoute) {
			h.serveLatestOptimizationRead(w, r, s, route, "staging-recommendations")
		}, publicIntelligenceRoute{siteID: siteID}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			testCase.serve(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+siteID, nil), bffSession{}, testCase.route)
			assertProblemCode(t, recorder, http.StatusNotFound, "ROUTE_NOT_FOUND")
		})
	}
}

func assertProblemCode(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil || problem.Code != code {
		t.Fatalf("problem code = %q (%v), want %q: %s", problem.Code, err, code, recorder.Body.String())
	}
}

func TestGatewayAutoControlLeaseRequiresTOTPAndSupportsRevokeAndMonitors(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	now := fixture.handler.now().UTC()
	snapshot, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	var leaseMu sync.Mutex
	var armedState json.RawMessage
	fixture.handler.registry.coreHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/auto-control-lease"):
				leaseMu.Lock()
				defer leaseMu.Unlock()
				if len(armedState) > 0 {
					return jsonHTTPResponse(http.StatusOK, string(armedState)), nil
				}
				return jsonHTTPResponse(http.StatusOK, `{"tenantId":"`+fixture.tenantID+`","siteId":"`+fixture.siteID+`","confirmationMode":"MANUAL_CONFIRM","activeLease":null,"latestLease":null}`), nil
			case req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/auto-control-lease"):
				raw, _ := io.ReadAll(req.Body)
				leaseMu.Lock()
				defer leaseMu.Unlock()
				armedState = json.RawMessage(`{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","confirmationMode":"AUTO_WITH_LEASE","activeLease":{"id":"01990000-8800-7000-8000-000000000011","tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","status":"ACTIVE","armedBy":"` + fixture.principalID + `","armedAt":"2026-07-26T15:00:00.000Z","expiresAt":"2026-07-26T23:00:00.000Z","allowedCapabilities":["SET_CHILLED_WATER_TEMPERATURE_SETPOINT"],"maxSetpointStepC":1,"supplyTempMinC":6,"supplyTempMaxC":12,"zoneTempMaxC":26,"minRunningChillers":1,"maxCommandsPerHour":2,"revision":1,"createdAt":"2026-07-26T15:00:00.000Z","updatedAt":"2026-07-26T15:00:00.000Z"},"latestLease":{"id":"01990000-8800-7000-8000-000000000011","tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","status":"ACTIVE","armedBy":"` + fixture.principalID + `","armedAt":"2026-07-26T15:00:00.000Z","expiresAt":"2026-07-26T23:00:00.000Z","allowedCapabilities":["SET_CHILLED_WATER_TEMPERATURE_SETPOINT"],"maxSetpointStepC":1,"supplyTempMinC":6,"supplyTempMaxC":12,"zoneTempMaxC":26,"minRunningChillers":1,"maxCommandsPerHour":2,"revision":1,"createdAt":"2026-07-26T15:00:00.000Z","updatedAt":"2026-07-26T15:00:00.000Z"}}`)
				if !strings.Contains(string(raw), `"SET_CHILLED_WATER_TEMPERATURE_SETPOINT"`) {
					t.Fatalf("unexpected forwarded body: %s", string(raw))
				}
				return jsonHTTPResponse(http.StatusOK, string(armedState)), nil
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/auto-control-lease/revoke"):
				leaseMu.Lock()
				defer leaseMu.Unlock()
				revoked := `{"tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","confirmationMode":"MANUAL_CONFIRM","activeLease":null,"latestLease":{"id":"01990000-8800-7000-8000-000000000011","tenantId":"` + fixture.tenantID + `","siteId":"` + fixture.siteID + `","status":"REVOKED","armedBy":"` + fixture.principalID + `","armedAt":"2026-07-26T15:00:00.000Z","expiresAt":"2026-07-26T23:00:00.000Z","allowedCapabilities":["SET_CHILLED_WATER_TEMPERATURE_SETPOINT"],"maxSetpointStepC":1,"supplyTempMinC":6,"supplyTempMaxC":12,"zoneTempMaxC":26,"minRunningChillers":1,"maxCommandsPerHour":2,"tripReason":"巡检撤回","revision":2,"createdAt":"2026-07-26T15:00:00.000Z","updatedAt":"2026-07-26T15:10:00.000Z"}}`
				armedState = json.RawMessage(revoked)
				return jsonHTTPResponse(http.StatusOK, revoked), nil
			case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/optimization-execution-monitors"):
				return jsonHTTPResponse(http.StatusOK, `{"tenantId":"`+fixture.tenantID+`","siteId":"`+fixture.siteID+`","items":[{"id":"01990000-8800-7000-8000-000000000021","tenantId":"`+fixture.tenantID+`","siteId":"`+fixture.siteID+`","leaseId":"01990000-8800-7000-8000-000000000011","recommendationId":"01990000-8800-7000-8000-000000000022","commandIntentId":"01990000-8800-7000-8000-000000000023","deviceId":"`+fixture.deviceID+`","targetCapability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","zoneTempMaxC":26,"supplyTempMaxC":12,"startedAt":"2026-07-26T15:00:00.000Z","windowEndsAt":"2026-07-26T15:30:00.000Z","status":"OBSERVING","createdAt":"2026-07-26T15:00:00.000Z","updatedAt":"2026-07-26T15:00:00.000Z"}]}`), nil
			default:
				return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
			}
		}),
	}

	// Default lease is MANUAL_CONFIRM.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-control-lease", nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `"MANUAL_CONFIRM"`) {
		t.Fatalf("initial GET lease failed: %d %s", getRec.Code, getRec.Body.String())
	}

	// Arm without valid TOTP must fail with 428 STEP_UP_REQUIRED.
	noTotp := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-control-lease", strings.NewReader(`{"expiresInHours":8,"allowedCapabilities":["SET_CHILLED_WATER_TEMPERATURE_SETPOINT"],"supplyTempMinC":6,"supplyTempMaxC":12,"reason":"启用自动优化","verificationCode":""}`))
	fixture.authenticate(noTotp, true)
	noTotp.Header.Set("Content-Type", "application/json")
	noTotpRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(noTotpRec, noTotp)
	assertProblemCode(t, noTotpRec, http.StatusPreconditionRequired, "STEP_UP_REQUIRED")

	// Valid TOTP + schedule.manage arms ACTIVE lease.
	armReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-control-lease", strings.NewReader(`{"expiresInHours":8,"allowedCapabilities":["SET_CHILLED_WATER_TEMPERATURE_SETPOINT"],"supplyTempMinC":6,"supplyTempMaxC":12,"reason":"启用自动优化","verificationCode":"123456"}`))
	fixture.authenticate(armReq, true)
	armReq.Header.Set("Content-Type", "application/json")
	armRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(armRec, armReq)
	if armRec.Code != http.StatusOK || !strings.Contains(armRec.Body.String(), `"AUTO_WITH_LEASE"`) {
		t.Fatalf("PUT arm lease failed: %d %s", armRec.Code, armRec.Body.String())
	}

	// Revoke lease returns MANUAL_CONFIRM.
	revokeReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-control-lease/revoke", strings.NewReader(`{"reason":"巡检撤回"}`))
	fixture.authenticate(revokeReq, true)
	revokeReq.Header.Set("Content-Type", "application/json")
	revokeRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK || !strings.Contains(revokeRec.Body.String(), `"REVOKED"`) {
		t.Fatalf("POST revoke lease failed: %d %s", revokeRec.Code, revokeRec.Body.String())
	}

	// Execution monitors endpoint returns site monitors.
	monReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/optimization/execution-monitors", nil)
	fixture.authenticate(monReq, false)
	monRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(monRec, monReq)
	if monRec.Code != http.StatusOK || !strings.Contains(monRec.Body.String(), `"OBSERVING"`) {
		t.Fatalf("GET monitors failed: %d %s", monRec.Code, monRec.Body.String())
	}
}

func TestGatewayOptimizationAutoCommandRequiresWorkloadIdentityAndGrant(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	now := fixture.handler.now().UTC()
	snapshot, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	grant, err := commandauth.SignGrant(fixture.iamSigner, commandauth.GrantClaims{
		Issuer:             "spiffe://hvac.local/iam-service",
		Presenter:          "spiffe://hvac.local/platform-gateway",
		Audience:           "command-service",
		GrantID:            "grant-auto-cmd-1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		PrincipalID:        fixture.principalID,
		TenantID:           fixture.tenantID,
		SiteID:             fixture.siteID,
		DeviceID:           fixture.deviceID,
		Capability:         commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		MaximumRisk:        commandmodel.RiskLow,
		CapabilityRevision: "capability:set-chilled-water-temperature-setpoint:v1",
		PolicyRevision:     "command-policy-1",
		IssuedAt:           now.Unix(),
		ExpiresAt:          now.Add(25 * time.Second).Unix(),
		TokenID:            "tok-auto-cmd-1",
		Transitive:         false,
		WorkloadSPIFFE:     defaultOptimizationWorkloadID,
		LeaseID:            "01990000-8800-7000-8000-000000000011",
		ArmedBy:            fixture.principalID,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"tenantId":"` + fixture.tenantID + `","deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","parameters":{"setpointC":8.0},"verificationPointKey":"chilled_water_supply_temp_sp","idempotencyKey":"auto-opt:01990000-8800-7000-8000-000000000031","currentState":{"evaluationAvailability":"AVAILABLE","presence":"ONLINE","readiness":"CURRENT","quality":"GOOD","businessRevision":1,"currentValue":7.0,"observedAt":"2026-07-26T14:58:00Z"},"commandGrant":"` + grant + `"}`

	// Reject reverse-proxied requests from nginx even with a valid payload.
	proxied := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(payload))
	proxied.Header.Set("Content-Type", "application/json")
	proxied.Header.Set("X-Forwarded-For", "203.0.113.10")
	proxiedRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(proxiedRec, proxied)
	assertProblemCode(t, proxiedRec, http.StatusForbidden, "OPTIMIZATION_WORKLOAD_FORBIDDEN")

	// Reject wrong mTLS workload identity.
	wrongTLS := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(payload))
	wrongTLS.Header.Set("Content-Type", "application/json")
	wrongTLS.TLS = verifiedWorkloadTLSState(t, "spiffe://hvac.local/report-service")
	wrongRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(wrongRec, wrongTLS)
	assertProblemCode(t, wrongRec, http.StatusForbidden, "OPTIMIZATION_WORKLOAD_FORBIDDEN")

	// Unverified or non-mTLS TLS state without VerifiedChains when TLS is present fails too.
	emptyTLS := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(payload))
	emptyTLS.Header.Set("Content-Type", "application/json")
	emptyTLS.TLS = &tls.ConnectionState{}
	emptyRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(emptyRec, emptyTLS)
	assertProblemCode(t, emptyRec, http.StatusForbidden, "OPTIMIZATION_WORKLOAD_FORBIDDEN")

	// Valid mTLS workload + valid IAM grant succeeds with 202 Accepted.
	fixture.commandCapability = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	fixture.handler.command.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPost && req.URL.Path == internalCommandsPath {
				if req.Header.Get("X-Command-Grant") != grant {
					t.Fatalf("missing X-Command-Grant header")
				}
				return jsonHTTPResponse(http.StatusAccepted, `{"schemaVersion":1,"commandId":"`+fixture.commandID+`","tenantId":"`+fixture.tenantID+`","siteId":"`+fixture.siteID+`","deviceId":"`+fixture.deviceID+`","pointId":"`+fixture.commandPointID+`","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","capabilityRevision":"capability:set-chilled-water-temperature-setpoint:v1","status":"QUEUED","risk":"LOW","approvalPolicy":"NONE","approvalCount":0,"requiredApprovalCount":0,"parameters":{"setpointC":8},"deviceCommandSequence":1,"version":1,"snapshotRevision":1,"transitions":[{"toStatus":"QUEUED","reason":"SUBMITTED","actorType":"WORKLOAD","occurredAt":"2026-07-26T15:00:00Z","version":1}],"createdAt":"2026-07-26T15:00:00Z","updatedAt":"2026-07-26T15:00:00Z"}`), nil
			}
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}),
	}
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(payload))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.TLS = verifiedWorkloadTLSState(t, defaultOptimizationWorkloadID)
	okRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusAccepted {
		t.Fatalf("valid auto-command status=%d want 202 body=%s", okRec.Code, okRec.Body.String())
	}

	// Rollback must use auto-rollback: prefix, and when valid it is allowed even when telemetry is stale/offline.
	badRollback := `{"tenantId":"` + fixture.tenantID + `","deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","parameters":{"setpointC":7.0},"verificationPointKey":"chilled_water_supply_temp_sp","idempotencyKey":"auto-opt:01990000-8800-7000-8000-000000000099","isRollback":true,"currentState":{"evaluationAvailability":"UNAVAILABLE","presence":"OFFLINE","readiness":"STALE","quality":"BAD","businessRevision":1,"currentValue":8.0,"observedAt":"2026-07-26T14:00:00Z"},"commandGrant":"` + grant + `"}`
	badRollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(badRollback))
	badRollbackReq.Header.Set("Content-Type", "application/json")
	badRollbackReq.TLS = verifiedWorkloadTLSState(t, defaultOptimizationWorkloadID)
	badRollbackRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(badRollbackRec, badRollbackReq)
	assertProblemCode(t, badRollbackRec, http.StatusBadRequest, "COMMAND_REQUEST_INVALID")

	rollbackPayload := `{"tenantId":"` + fixture.tenantID + `","deviceId":"` + fixture.deviceID + `","pointId":"` + fixture.commandPointID + `","capability":"SET_CHILLED_WATER_TEMPERATURE_SETPOINT","parameters":{"setpointC":7.0},"verificationPointKey":"chilled_water_supply_temp_sp","idempotencyKey":"auto-rollback:01990000-8800-7000-8000-000000000099","isRollback":true,"currentState":{"evaluationAvailability":"UNAVAILABLE","presence":"OFFLINE","readiness":"STALE","quality":"BAD","businessRevision":1,"currentValue":8.0,"observedAt":"2026-07-26T14:00:00Z"},"commandGrant":"` + grant + `"}`
	rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/optimization/auto-commands", strings.NewReader(rollbackPayload))
	rollbackReq.Header.Set("Content-Type", "application/json")
	rollbackReq.TLS = verifiedWorkloadTLSState(t, defaultOptimizationWorkloadID)
	rollbackRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rollbackRec, rollbackReq)
	if rollbackRec.Code != http.StatusAccepted {
		t.Fatalf("rollback with stale telemetry status=%d want 202 body=%s", rollbackRec.Code, rollbackRec.Body.String())
	}
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
