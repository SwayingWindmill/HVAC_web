package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestAccountingRecalcRoutesMatchPublicContract(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	valid := []struct {
		method, path, kind, internalPath string
		write                            bool
	}{
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/recalculate", "recalculate", "/internal/v1/registry/sites/" + siteID + "/accounting/recalculate", true},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/recalculate-jobs", "recalculate-jobs", "/internal/v1/registry/sites/" + siteID + "/accounting/recalculate-jobs", false},
	}
	for _, tc := range valid {
		route, ok := matchPublicAccountingRecalcRoute(tc.method, tc.path)
		if !ok || route.siteID != siteID || route.kind != tc.kind || route.internalPath != tc.internalPath || route.write != tc.write {
			t.Fatalf("%s %s resolved as %#v ok=%t", tc.method, tc.path, route, ok)
		}
	}

	invalid := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/recalculate"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/recalculate-jobs"},
		{http.MethodDelete, "/api/v1/sites/" + siteID + "/accounting/recalculate"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/accounting/recalculate-jobs/"},
		{http.MethodPost, "/api/v1/sites/" + siteID + "/accounting/recalculate/extra"},
	}
	for _, tc := range invalid {
		if _, ok := matchPublicAccountingRecalcRoute(tc.method, tc.path); ok {
			t.Fatalf("%s %s unexpectedly matched accounting recalc route", tc.method, tc.path)
		}
	}
}

func TestAccountingRecalcGatewayRBACCSRFAnd202Passthrough(t *testing.T) {
	fix := newRegistryCSRFFixture(t)
	allowedActions := map[registryauth.Action]bool{
		registryauth.ActionSiteRead:      true,
		registryauth.ActionBillingManage: true,
	}
	var iamRequestedActions []registryauth.Action
	var lastCorePath string
	var lastCoreQuery string
	var lastActorDisplay string

	fix.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fix.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&fix.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
		}
		iamRequestedActions = append(iamRequestedActions, input.Action)
		if !allowedActions[input.Action] {
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
			Actions:        []registryauth.Action{input.Action},
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
				Actions:        []registryauth.Action{input.Action},
				PolicyRevision: "registry-write:1/iam:1",
				ReasonCode:     registryauth.ReasonAllowSiteRole,
				DecidedAt:      fix.now.Format(time.RFC3339Nano),
			},
			DelegationGrant: commandUnsignedRegistryGrant(claims),
		}), nil
	})}

	fix.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL: "https://registry.example.test",
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.coreCalls.Add(1)
			lastCorePath = request.URL.Path
			lastCoreQuery = request.URL.RawQuery
			lastActorDisplay = request.Header.Get("X-Actor-Display-Name")
			if request.Method == http.MethodPost {
				return telemetryJSONResponse(http.StatusAccepted, map[string]any{
					"id":                 "01990000-9300-7000-8000-000000000001",
					"tenantId":           fix.tenantID,
					"siteId":             fix.siteID,
					"granularity":        "HOUR",
					"windowStart":        "2026-10-12T16:00:00.000Z",
					"windowEnd":          "2026-10-16T16:00:00.000Z",
					"status":             "PENDING",
					"availableAt":        "2026-10-16T16:00:00.000Z",
					"requestedBy":        fix.principalID,
					"requestedByDisplay": lastActorDisplay,
					"reason":             "补算",
					"source":             "MANUAL",
					"attemptCount":       0,
					"lastError":          "",
					"createdAt":          "2026-10-16T16:00:00.000Z",
					"updatedAt":          "2026-10-16T16:00:00.000Z",
					"finishedAt":         nil,
				}), nil
			}
			return telemetryJSONResponse(http.StatusOK, map[string]any{"items": []any{}}), nil
		})},
	})

	recalcPath := "/api/v1/sites/" + fix.siteID + "/accounting/recalculate"
	jobsPath := "/api/v1/sites/" + fix.siteID + "/accounting/recalculate-jobs"
	payload := `{"fromDate":"2026-10-13","toDate":"2026-10-16","granularity":"HOUR","reason":"补算"}`

	// 1. CSRF check blocks POST without Origin.
	noOriginReq := httptest.NewRequest(http.MethodPost, recalcPath, strings.NewReader(payload))
	noOriginReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(noOriginReq, false)
	noOriginRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(noOriginRec, noOriginReq)
	assertRegistryProblem(t, noOriginRec, http.StatusForbidden, "CSRF_INVALID")

	// 2. Hidden Site returns 404 without hitting Core.
	otherSiteReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/018f1e00-1000-7000-8000-000000000099/accounting/recalculate", strings.NewReader(payload))
	otherSiteReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(otherSiteReq, true)
	otherSiteRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(otherSiteRec, otherSiteReq)
	assertRegistryProblem(t, otherSiteRec, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	if fix.coreCalls.Load() != 0 {
		t.Fatalf("hidden site unexpectedly called Core %d times", fix.coreCalls.Load())
	}

	// 3. GET jobs works with site.read and forwards query params.
	getJobsReq := httptest.NewRequest(http.MethodGet, jobsPath+"?limit=15", nil)
	fix.authenticate(getJobsReq, true)
	getJobsRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(getJobsRec, getJobsReq)
	if getJobsRec.Code != http.StatusOK || lastCoreQuery != "limit=15" || !strings.HasSuffix(lastCorePath, "/accounting/recalculate-jobs") {
		t.Fatalf("GET jobs status=%d path=%q query=%q body=%s", getJobsRec.Code, lastCorePath, lastCoreQuery, getJobsRec.Body.String())
	}

	// 4. POST succeeds with billing.manage and returns 202 Accepted.
	postBillingReq := httptest.NewRequest(http.MethodPost, recalcPath, strings.NewReader(payload))
	postBillingReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(postBillingReq, true)
	postBillingRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(postBillingRec, postBillingReq)
	if postBillingRec.Code != http.StatusAccepted {
		t.Fatalf("billing.manage POST status=%d, want 202; body=%s", postBillingRec.Code, postBillingRec.Body.String())
	}

	// 5. POST falls back to savings.manage when billing.manage is denied.
	allowedActions[registryauth.ActionBillingManage] = false
	allowedActions[registryauth.ActionSavingsManage] = true
	iamRequestedActions = nil
	postSavingsReq := httptest.NewRequest(http.MethodPost, recalcPath, strings.NewReader(payload))
	postSavingsReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(postSavingsReq, true)
	postSavingsRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(postSavingsRec, postSavingsReq)
	if postSavingsRec.Code != http.StatusAccepted {
		t.Fatalf("savings.manage POST status=%d, want 202; body=%s", postSavingsRec.Code, postSavingsRec.Body.String())
	}
	if len(iamRequestedActions) != 3 || iamRequestedActions[0] != registryauth.ActionSiteRead ||
		iamRequestedActions[1] != registryauth.ActionBillingManage || iamRequestedActions[2] != registryauth.ActionSavingsManage {
		t.Fatalf("unexpected IAM action fallback sequence: %v", iamRequestedActions)
	}

	// 6. POST is denied with 403 ACCOUNTING_RECALC_FORBIDDEN when neither billing.manage nor savings.manage is held.
	allowedActions[registryauth.ActionSavingsManage] = false
	beforeCore := fix.coreCalls.Load()
	forbidReq := httptest.NewRequest(http.MethodPost, recalcPath, strings.NewReader(payload))
	forbidReq.Header.Set("Content-Type", "application/json")
	fix.authenticate(forbidReq, true)
	forbidRec := httptest.NewRecorder()
	fix.handler.ServeHTTP(forbidRec, forbidReq)
	assertRegistryProblem(t, forbidRec, http.StatusForbidden, "ACCOUNTING_RECALC_FORBIDDEN")
	if fix.coreCalls.Load() != beforeCore {
		t.Fatalf("forbidden POST unexpectedly called Core")
	}

	// 7. writeAccountingRecalcBackendResponse passes 202 and 400 cleanly.
	resp202 := &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"01990000-9300-7000-8000-000000000001"}`)),
	}
	out202 := httptest.NewRecorder()
	code, errCode := writeAccountingRecalcBackendResponse(out202, httptest.NewRequest(http.MethodPost, recalcPath, nil), resp202, 2<<20)
	if code != http.StatusAccepted || errCode != "" || out202.Code != http.StatusAccepted {
		t.Fatalf("202 passthrough got code=%d errCode=%q http=%d", code, errCode, out202.Code)
	}

	resp400 := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":"REGISTRY_MUTATION_INVALID","detail":"invalid date range"}`)),
	}
	out400 := httptest.NewRecorder()
	code400, errCode400 := writeAccountingRecalcBackendResponse(out400, httptest.NewRequest(http.MethodPost, recalcPath, nil), resp400, 2<<20)
	if code400 != http.StatusBadRequest || errCode400 != "REGISTRY_MUTATION_INVALID" || out400.Code != http.StatusBadRequest {
		t.Fatalf("400 passthrough got code=%d errCode=%q http=%d", code400, errCode400, out400.Code)
	}
}
