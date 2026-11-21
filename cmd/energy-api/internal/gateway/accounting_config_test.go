package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

func TestAccountingConfigAndCounterAuditRoutesMatchPublicContract(t *testing.T) {
	siteID := "018f3e00-2000-7000-8000-000000000002"
	versionID := "018f3e00-9000-7000-8000-000000000001"

	configCases := []struct {
		method       string
		path         string
		wantMatch    bool
		wantWrite    bool
		wantInternal string
	}{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/holidays", true, false, "/internal/v1/registry/sites/" + siteID + "/accounting/holidays"},
		{http.MethodPut, "/api/v1/sites/" + siteID + "/accounting/holidays/2026-10-01", true, true, "/internal/v1/registry/sites/" + siteID + "/accounting/holidays/2026-10-01"},
		{http.MethodDelete, "/api/v1/sites/" + siteID + "/accounting/holidays/2026-10-01", true, true, "/internal/v1/registry/sites/" + siteID + "/accounting/holidays/2026-10-01"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/meter-ratios", true, false, "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/meter-ratios", true, true, "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/meter-ratios/" + versionID + "/retire", true, true, "/internal/v1/registry/sites/" + siteID + "/accounting/meter-ratios/" + versionID + "/retire"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/holidays", false, false, ""},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/holidays/2026-10-01", false, false, ""},
	}
	for _, tc := range configCases {
		route, ok := matchPublicAccountingConfigRoute(tc.method, tc.path)
		if ok != tc.wantMatch {
			t.Fatalf("matchPublicAccountingConfigRoute(%s, %s) = %v, want %v", tc.method, tc.path, ok, tc.wantMatch)
		}
		if ok && (route.write != tc.wantWrite || route.internalPath != tc.wantInternal) {
			t.Fatalf("matchPublicAccountingConfigRoute(%s, %s) = %+v", tc.method, tc.path, route)
		}
	}

	auditRoute, ok := matchPublicCounterAuditRoute(http.MethodGet, "/api/v1/sites/"+siteID+"/accounting/counter-audit")
	if !ok || auditRoute.siteID != siteID {
		t.Fatalf("matchPublicCounterAuditRoute(GET) = %+v, ok=%v", auditRoute, ok)
	}
	if _, ok := matchPublicCounterAuditRoute(http.MethodPost, "/api/v1/sites/"+siteID+"/accounting/counter-audit"); ok {
		t.Fatal("POST counter-audit must not match GET route")
	}
}

func TestAccountingConfigGatewayRBACCSRFDeleteAndStatusForwarding(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	allowBillingManage := true
	var lastCoreMethod, lastCorePath, lastCoreBody string
	nextCoreStatus := http.StatusOK
	nextCoreBody := `{"items":[]}`

	fix.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fix.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&fix.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("verify gateway IAM delegation: %v", err)
		}
		var reqBody registryauth.DecisionRequest
		if err := json.NewDecoder(request.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode IAM request: %v", err)
		}
		if reqBody.Action == registryauth.ActionBillingManage && !allowBillingManage {
			return jsonTestResponse(http.StatusForbidden, map[string]any{"code": "DENY_ACTION_NOT_GRANTED"}), nil
		}
		if reqBody.Action != registryauth.ActionSiteRead && reqBody.Action != registryauth.ActionBillingManage {
			return jsonTestResponse(http.StatusForbidden, map[string]any{"code": "DENY_ACTION_NOT_GRANTED"}), nil
		}
		claims := registryauth.GrantClaims{
			Issuer:         "spiffe://hvac.local/iam-service",
			Presenter:      "spiffe://hvac.local/platform-gateway",
			Audience:       "platform-core-service",
			PrincipalID:    fix.principalID,
			SubjectIssuer:  parent.SubjectIssuer,
			Subject:        parent.Subject,
			TenantID:       fix.tenantID,
			AllowedSiteIDs: []string{fix.siteID},
			Actions:        []registryauth.Action{reqBody.Action},
			PolicyRevision: "registry-write:1/iam:1",
			DecisionReason: registryauth.ReasonAllowSiteRole,
			SessionID:      parent.SessionID,
			ParentTokenID:  parent.TokenID,
			IssuedAt:       fix.now.Unix(),
			ExpiresAt:      fix.now.Add(30 * time.Second).Unix(),
			TokenID:        randomURLToken(16),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
			Decision: registryauth.Decision{
				Allowed:        true,
				PrincipalID:    fix.principalID,
				SubjectIssuer:  parent.SubjectIssuer,
				Subject:        parent.Subject,
				TenantID:       fix.tenantID,
				AllowedSiteIDs: []string{fix.siteID},
				Actions:        []registryauth.Action{reqBody.Action},
				PolicyRevision: "registry-write:1/iam:1",
				ReasonCode:     registryauth.ReasonAllowSiteRole,
				DecidedAt:      fix.now.Format(time.RFC3339Nano),
			},
			DelegationGrant: commandUnsignedRegistryGrant(claims),
		}), nil
	})}

	fix.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL: "https://core.example.test",
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.coreCalls.Add(1)
			lastCoreMethod = request.Method
			lastCorePath = request.URL.Path
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				lastCoreBody = string(raw)
			} else {
				lastCoreBody = ""
			}
			return &http.Response{
				StatusCode: nextCoreStatus,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(nextCoreBody)),
			}, nil
		})},
		CoreTimeout: time.Second,
	})

	holidaysPath := "/api/v1/sites/" + fix.siteID + "/accounting/holidays"
	holidayDatePath := holidaysPath + "/2026-10-01"
	ratiosPath := "/api/v1/sites/" + fix.siteID + "/accounting/meter-ratios"
	versionID := "018f3e00-9000-7000-8000-000000000001"
	retirePath := ratiosPath + "/" + versionID + "/retire"

	// Missing Origin/CSRF rejected before Core/IAM calls.
	noCSRFReq := httptest.NewRequest(http.MethodPut, holidayDatePath, strings.NewReader(`{"dayType":"HOLIDAY","label":"国庆节"}`))
	noCSRFReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(noCSRFReq, false)
	noCSRFRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noCSRFRec, noCSRFReq)
	assertRegistryProblem(t, noCSRFRec, http.StatusForbidden, "CSRF_INVALID")
	if fix.coreCalls.Load() != 0 {
		t.Fatalf("CSRF failure reached Core %d times", fix.coreCalls.Load())
	}

	// Hidden site returns 404 without calling Core.
	otherSiteReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/018f1d00-0000-7000-8000-000000000099/accounting/holidays", nil)
	fix.authenticate(otherSiteReq, true)
	otherSiteRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(otherSiteRec, otherSiteReq)
	assertRegistryProblem(t, otherSiteRec, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// Invalid calendar date returns 400 without calling Core.
	badDateReq := httptest.NewRequest(http.MethodPut, holidaysPath+"/2026-02-30", strings.NewReader(`{"dayType":"HOLIDAY","label":"无效日"}`))
	badDateReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(badDateReq, true)
	badDateRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(badDateRec, badDateReq)
	assertRegistryProblem(t, badDateRec, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID")

	// GET holidays forwards cleanly.
	getReq := httptest.NewRequest(http.MethodGet, holidaysPath+"?year=2026", nil)
	fix.authenticate(getReq, true)
	getRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || lastCoreMethod != http.MethodGet || lastCorePath != "/internal/v1/registry/sites/"+fix.siteID+"/accounting/holidays" {
		t.Fatalf("GET holidays failed: status=%d method=%s path=%s body=%s", getRec.Code, lastCoreMethod, lastCorePath, getRec.Body.String())
	}

	// DELETE holiday with empty body succeeds and does not fail empty JSON guard.
	nextCoreStatus = http.StatusOK
	nextCoreBody = `{"deleted":true,"siteId":"` + fix.siteID + `","holidayDate":"2026-10-01"}`
	delReq := httptest.NewRequest(http.MethodDelete, holidayDatePath, nil)
	fix.authenticate(delReq, true)
	delRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK || lastCoreMethod != http.MethodDelete || lastCoreBody != "" {
		t.Fatalf("DELETE holiday failed: status=%d method=%s upstreamBody=%q resp=%s", delRec.Code, lastCoreMethod, lastCoreBody, delRec.Body.String())
	}

	// POST meter-ratios forwards 201 Created cleanly.
	nextCoreStatus = http.StatusCreated
	nextCoreBody = `{"id":"` + versionID + `","tenantId":"` + fix.tenantID + `","siteId":"` + fix.siteID + `","ctRatio":100,"ptRatio":1,"meterMultiplier":1,"ratioApplicationMode":"PLATFORM_APPLIED","readingUnit":"kWh","effectiveFrom":"2026-10-01T00:00:00Z","effectiveTo":null,"status":"RELEASED","revision":1}`
	createReq := httptest.NewRequest(http.MethodPost, ratiosPath, strings.NewReader(`{"pointId":"018f3e00-4000-7000-8000-000000000004","ctRatio":100,"ptRatio":1,"meterMultiplier":1,"ratioApplicationMode":"PLATFORM_APPLIED","readingUnit":"kWh","effectiveFrom":"2026-10-01T00:00:00Z"}`))
	createReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(createReq, true)
	createRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST meter-ratios status=%d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}

	// POST meter-ratios/{versionId}/retire forwards 409 REGISTRY_REVISION_CONFLICT cleanly.
	nextCoreStatus = http.StatusConflict
	nextCoreBody = `{"type":"about:blank","title":"Conflict","status":409,"code":"REGISTRY_REVISION_CONFLICT","detail":"stale revision","retryable":false}`
	retireReq := httptest.NewRequest(http.MethodPost, retirePath, strings.NewReader(`{"expectedRevision":1}`))
	retireReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(retireReq, true)
	retireRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(retireRec, retireReq)
	assertRegistryProblem(t, retireRec, http.StatusConflict, "REGISTRY_REVISION_CONFLICT")

	// Denied billing.manage stops before Core.
	allowBillingManage = false
	beforeCore := fix.coreCalls.Load()
	forbidReq := httptest.NewRequest(http.MethodPost, ratiosPath, strings.NewReader(`{"ctRatio":1}`))
	forbidReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(forbidReq, true)
	forbidRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(forbidRec, forbidReq)
	assertRegistryProblem(t, forbidRec, http.StatusForbidden, "BILLING_MANAGE_FORBIDDEN")
	if fix.coreCalls.Load() != beforeCore {
		t.Fatalf("forbidden request reached Core")
	}
}

func TestCounterAuditGatewayAuthScopeGrantAndQueryValidation(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	allowAnalytics := true
	var queryCalls atomic.Int32

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	deviceID := "018f3e00-3000-7000-8000-000000000003"
	anomaliesOnly := false
	expectedQuery, err := (telemetryhistorymodel.CounterAuditQuery{
		TenantID:      fix.tenantID,
		SiteID:        fix.siteID,
		From:          from,
		To:            to,
		DeviceID:      &deviceID,
		AnomaliesOnly: &anomaliesOnly,
		Limit:         50,
	}).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	expectedScope, err := expectedQuery.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}

	fix.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fix.iamCalls.Add(1)
		if request.URL.Path != analyticsDecisionPath {
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
		}
		parent, err := identitycontext.VerifyDelegation(&fix.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("verify parent grant: %v", err)
		}
		var reqBody analyticsmodel.AuthorizationDecisionRequest
		if err := json.NewDecoder(request.Body).Decode(&reqBody); err != nil {
			t.Fatal(err)
		}
		if reqBody.Action != analyticsmodel.EnergySeriesAction || reqBody.SiteID != fix.siteID {
			t.Fatalf("unexpected analytics auth request: %+v", reqBody)
		}
		reason := analyticsmodel.AuthorizationReasonAllowRole
		if !allowAnalytics {
			reason = analyticsmodel.AuthorizationReasonDenyAction
		}
		return telemetryJSONResponse(http.StatusOK, analyticsmodel.AuthorizationDecisionResponse{
			Decision: analyticsmodel.AuthorizationDecision{
				Allowed:        allowAnalytics,
				PrincipalID:    fix.principalID,
				SubjectIssuer:  parent.SubjectIssuer,
				Subject:        parent.Subject,
				TenantID:       fix.tenantID,
				SiteID:         fix.siteID,
				Action:         analyticsmodel.EnergySeriesAction,
				PolicyRevision: "analytics-policy-7",
				ReasonCode:     reason,
				DecidedAt:      fix.now.Format(time.RFC3339Nano),
			},
		}), nil
	})}

	fix.handler.analytics = newAnalyticsController(&AnalyticsConfig{
		QueryBaseURL:  "https://query.example.test",
		QueryAudience: "telemetry-query-service",
		Timeout:       time.Second,
		QueryHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			queryCalls.Add(1)
			if request.Method != http.MethodPost || request.URL.Path != "/internal/v1/telemetry/counter-audit" {
				t.Fatalf("unexpected query request %s %s", request.Method, request.URL.Path)
			}
			claims, err := identitycontext.VerifyDelegation(&fix.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
			if err != nil {
				t.Fatalf("verify downstream analytics grant: %v", err)
			}
			if claims.TenantID != fix.tenantID || len(claims.Scopes) != 1 || claims.Scopes[0] != expectedScope {
				t.Fatalf("unexpected grant claims: %+v, want scope=%s", claims, expectedScope)
			}
			var decoded telemetryhistorymodel.CounterAuditQuery
			if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.SiteID != fix.siteID || decoded.Limit != 50 {
				t.Fatalf("unexpected upstream query: %+v", decoded)
			}
			resp := telemetryhistorymodel.CounterAuditResponse{
				SiteID:    fix.siteID,
				From:      from,
				To:        to,
				Summary:   telemetryhistorymodel.CounterAuditSummary{TotalSamples: 3, AnomalyCount: 1, ResetCount: 1},
				Items:     []telemetryhistorymodel.CounterAuditItem{},
				Truncated: false,
			}
			encoded, _ := json.Marshal(resp)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(encoded)),
			}, nil
		})},
	})

	basePath := "/api/v1/sites/" + fix.siteID + "/accounting/counter-audit"

	// Validation rejections do not call upstream.
	for _, badURL := range []string{
		basePath + "?to=" + to.Format(time.RFC3339),
		basePath + "?from=" + from.Format(time.RFC3339) + "&to=" + from.Add(32*24*time.Hour).Format(time.RFC3339),
		basePath + "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339) + "&extra=1",
	} {
		req := httptest.NewRequest(http.MethodGet, badURL, nil)
		fix.authenticate(req, true)
		rec := httptest.NewRecorder()
		fix.handler.ServeHTTP(rec, req)
		assertRegistryProblem(t, rec, http.StatusBadRequest, "COUNTER_AUDIT_QUERY_INVALID")
	}

	// IAM deny returns 403 ANALYTICS_ACCESS_DENIED without touching query service.
	allowAnalytics = false
	deniedReq := httptest.NewRequest(http.MethodGet, basePath+"?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&deviceId="+deviceID+"&anomaliesOnly=false&limit=50", nil)
	fix.authenticate(deniedReq, true)
	deniedRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(deniedRec, deniedReq)
	assertRegistryProblem(t, deniedRec, http.StatusForbidden, "ANALYTICS_ACCESS_DENIED")
	if queryCalls.Load() != 0 {
		t.Fatalf("denied request reached query service %d times", queryCalls.Load())
	}

	// Authorized request returns 200 with Cache-Control: private, no-store.
	allowAnalytics = true
	okReq := httptest.NewRequest(http.MethodGet, basePath+"?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&deviceId="+deviceID+"&anomaliesOnly=false&limit=50", nil)
	fix.authenticate(okReq, true)
	okRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || queryCalls.Load() != 1 {
		t.Fatalf("counter audit failed: status=%d calls=%d body=%s", okRec.Code, queryCalls.Load(), okRec.Body.String())
	}
	if cache := okRec.Header().Get("Cache-Control"); cache != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", cache)
	}
}
