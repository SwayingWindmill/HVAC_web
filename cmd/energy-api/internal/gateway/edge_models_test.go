package gateway

import (
	"encoding/json"
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
)

func TestGatewayEdgeModelWorkspaceDeploySyncsShadowAndHandlesRBAC(t *testing.T) {
	fixture := newOperationsGatewayFixture(t, 30)
	controller := fixture.handler.(*handler)
	snapshot, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	controller.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return fixture.now })

	gwID := "018f3d00-2000-7000-8000-000000000001"
	artID := "019a0000-1000-7000-8000-000000000001"
	depID := "019a0000-2000-7000-8000-000000000002"

	var shadowPutCount atomic.Int32
	var lastShadowBody string

	controller.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, req.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return nil, err
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(req.Body).Decode(&input) != nil {
			t.Fatal("invalid IAM decision request")
		}
		grantClaims := registryauth.GrantClaims{
			Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
			PrincipalID: "018f3d00-0000-7000-8000-000000000099", SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
			Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry:1/iam:1",
			DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
			IssuedAt: fixture.now.Unix(), ExpiresAt: fixture.now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
		}
		dec := registryauth.Decision{
			Allowed: true, PrincipalID: "018f3d00-0000-7000-8000-000000000099", SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID}, Actions: []registryauth.Action{input.Action},
			PolicyRevision: "registry:1/iam:1", ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: fixture.now.Format(time.RFC3339Nano),
		}
		return jsonHTTPResponse(http.StatusOK, mustJSON(t, registryauth.DecisionResponse{Decision: dec, DelegationGrant: commandUnsignedRegistryGrant(grantClaims)})), nil
	})}

	controller.connectivity = &ConnectivityConfig{
		BaseURL: "https://connectivity.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			shadowPutCount.Add(1)
			b, _ := io.ReadAll(req.Body)
			lastShadowBody = string(b)
			return jsonHTTPResponse(http.StatusOK, `{"tenantId":"`+fixture.tenantID+`","gatewayId":"`+gwID+`","syncStatus":"PENDING_SYNC","desired":{"desiredRevision":9,"pointMappings":[],"samplingPolicies":[],"concentratorConfigs":{},"updatedBy":"`+"018f3d00-0000-7000-8000-000000000099"+`","reason":"deploy edge model bundle","createdAt":"2026-11-23T12:00:00Z","updatedAt":"2026-11-23T12:00:00Z"},"drift":[]}`), nil
		})},
	}

	controller.intelligence.ownerHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/edge-models"):
				return jsonHTTPResponse(http.StatusOK, `{"tenantId":"`+fixture.tenantID+`","siteId":"`+fixture.siteID+`","generatedAt":"2026-11-23T12:00:00Z","artifacts":[],"approvals":[],"deployments":[],"evaluations":[],"driftObservations":[],"rollbacks":[],"desiredBundles":[]}`), nil
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edge-models/deployments"):
				return jsonHTTPResponse(http.StatusOK, `{"deployment":{"id":"`+depID+`","artifactId":"`+artID+`","versionId":"`+artID+`","domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"1.0.0","approvalId":"`+artID+`","environment":"PRODUCTION","status":"ACTIVE","servingRef":"edge-gateway://`+gwID+`","gatewayId":"`+gwID+`","revision":1,"effectiveFrom":"2026-11-23T12:00:00Z","hyperparams":{"supplyTempSetpointMinC":6.5}},"gatewayId":"`+gwID+`","desiredBundles":[{"bundleId":"`+artID+`","domain":"EDGE_CONTROL","modelKey":"chiller-cop-opt","version":"1.0.0","rolloutMode":"ACTIVE","artifactSha256":"`+strings.Repeat("a", 64)+`","maxShadowDeviation":1.5,"hyperparams":{"supplyTempSetpointMinC":6.5}}]}`), nil
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edge-models/artifacts"):
				return jsonHTTPResponse(http.StatusUnprocessableEntity, `{"error":"edge_model_validation_failed","detail":"supplyTempSetpointMinC=1 out of [4,18]"}`), nil
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/rollback"):
				return jsonHTTPResponse(http.StatusOK, `{"rollback":{"id":"`+artID+`","deploymentId":"`+depID+`","fromVersionId":"`+artID+`","toVersionId":"`+artID+`","reason":"回退","actorType":"USER","actorId":"u1","status":"APPLIED","requestedAt":"2026-11-23T12:01:00Z","appliedAt":"2026-11-23T12:01:00Z"},"gatewayId":"`+gwID+`","desiredBundles":[]}`), nil
			default:
				return jsonHTTPResponse(http.StatusNotFound, `{"error":"edge_model_not_found"}`), nil
			}
		}),
	}

	// 1. GET workspace returns 200.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/edge-models", nil)
	fixture.authenticate(getReq, false)
	getRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET edge-models status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	// 2. 422 upstream validation failure surfaces cleanly as ProblemDetails 422.
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/edge-models/artifacts", strings.NewReader(`{"domain":"EDGE_CONTROL","modelKey":"m1","version":"1.0","hyperparams":{"supplyTempSetpointMinC":1}}`))
	fixture.authenticate(badReq, true)
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(badRec, badReq)
	assertProblemCode(t, badRec, http.StatusUnprocessableEntity, "EDGE_MODEL_VALIDATION_FAILED")

	// 3. Deploy forwards desiredBundles to connectivity config-shadow and reports SYNCED revision.
	depReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/edge-models/deployments", strings.NewReader(`{"artifactId":"`+artID+`","environment":"PRODUCTION","gatewayId":"`+gwID+`"}`))
	fixture.authenticate(depReq, true)
	depReq.Header.Set("Content-Type", "application/json")
	depRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(depRec, depReq)
	if depRec.Code != http.StatusOK || shadowPutCount.Load() != 1 || !strings.Contains(depRec.Body.String(), `"shadowSyncStatus":"SYNCED"`) || !strings.Contains(lastShadowBody, `"chiller-cop-opt"`) {
		t.Fatalf("deploy status=%d shadowCalls=%d body=%s lastShadow=%s", depRec.Code, shadowPutCount.Load(), depRec.Body.String(), lastShadowBody)
	}

	// 4. Rollback triggers second config-shadow sync clearing edgeModels.
	rbReq := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+fixture.siteID+"/edge-models/deployments/"+depID+"/rollback", strings.NewReader(`{"reason":"偏差超限回退"}`))
	fixture.authenticate(rbReq, true)
	rbReq.Header.Set("Content-Type", "application/json")
	rbRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rbRec, rbReq)
	if rbRec.Code != http.StatusOK || shadowPutCount.Load() != 2 || !strings.Contains(lastShadowBody, `"edgeModels":[]`) {
		t.Fatalf("rollback status=%d shadowCalls=%d body=%s lastShadow=%s", rbRec.Code, shadowPutCount.Load(), rbRec.Body.String(), lastShadowBody)
	}

	// 5. safeLogPath redacts IDs cleanly.
	if got := safeLogPath("/api/v1/sites/" + fixture.siteID + "/edge-models/deployments/" + depID + "/rollback"); got != "/api/v1/sites/{siteId}/edge-models/deployments/{deploymentId}/rollback" {
		t.Fatalf("safeLogPath=%q", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
