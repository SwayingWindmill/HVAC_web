package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestGatewayRegistryCrossSiteFiltersAndTemplateRead(t *testing.T) {
	base := newCommandGatewayFixture(t)
	snapshotRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatalf("read route-ownership.v1.json: %v", err)
	}
	snapshot, err := ownershipregistry.Parse(snapshotRaw)
	if err != nil {
		t.Fatalf("parse route-ownership.v1.json: %v", err)
	}
	now := base.now
	base.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	revID := "018f3e00-7000-7000-8000-000000000001"
	tplID := "018f3e00-7100-7000-8000-000000000001"
	assignID := "018f3e00-7200-7000-8000-000000000001"
	spaceID := "018f3e00-2000-7000-8000-000000000001"

	var mu sync.Mutex
	var lastAction registryauth.Action
	var lastCorePath string
	var lastCoreQuery string

	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
		}
		mu.Lock()
		lastAction = input.Action
		mu.Unlock()
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

	base.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL: "https://registry.example.test",
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			lastCorePath = request.URL.Path
			lastCoreQuery = request.URL.RawQuery
			mu.Unlock()

			switch request.URL.Path {
			case "/internal/v1/registry/assets":
				return telemetryJSONResponse(http.StatusOK, map[string]any{
					"items": []map[string]any{{
						"id": base.assetID, "tenantId": base.tenantID, "siteId": base.siteID,
						"code": "ch-1", "displayName": "Chiller 1", "assetType": "CHILLER", "status": "ACTIVE",
						"tags":     map[string]string{"plant": "north", "critical": "true"},
						"revision": 1, "createdAt": "2026-11-17T02:00:00.000Z", "updatedAt": "2026-11-17T02:00:00.000Z",
					}},
					"nextCursor": nil, "hasMore": false,
				}), nil
			case "/internal/v1/registry/devices":
				return telemetryJSONResponse(http.StatusOK, map[string]any{
					"items": []map[string]any{{
						"id": "018f3e00-4000-7000-8000-000000000001", "tenantId": base.tenantID, "siteId": base.siteID,
						"code": "vfd-1", "displayName": "VFD 1", "deviceType": "VFD", "status": "ACTIVE",
						"tags":     map[string]string{"vendor": "schneider"},
						"revision": 1, "createdAt": "2026-11-17T02:00:00.000Z", "updatedAt": "2026-11-17T02:00:00.000Z",
					}},
					"nextCursor": nil, "hasMore": false,
				}), nil
			case "/internal/v1/registry/templates":
				return telemetryJSONResponse(http.StatusOK, map[string]any{
					"items": []map[string]any{{
						"id": tplID, "tenantId": base.tenantID, "templateKey": "chiller.v1", "templateKind": "DEVICE",
						"latestRevisionId": revID, "latestRevisionNumber": 1,
						"releasedAt": "2026-11-17T02:00:00.000Z", "createdAt": "2026-11-17T02:00:00.000Z",
					}},
					"nextCursor": nil, "hasMore": false,
				}), nil
			case "/internal/v1/registry/templates/revisions/" + revID:
				return telemetryJSONResponse(http.StatusOK, map[string]any{
					"id": revID, "tenantId": base.tenantID, "templateId": tplID, "templateKey": "chiller.v1", "templateKind": "DEVICE",
					"revisionNumber": 1, "status": "RELEASED",
					"payload":           map[string]any{"deviceType": "CHILLER"},
					"releaseReferences": map[string]string{"doc": "v1"},
					"releasedAt":        "2026-11-17T02:00:00.000Z",
				}), nil
			case "/internal/v1/registry/sites/" + base.siteID + "/template-assignments":
				return telemetryJSONResponse(http.StatusOK, map[string]any{
					"items": []map[string]any{{
						"id": assignID, "tenantId": base.tenantID, "siteId": base.siteID,
						"targetType": "DEVICE", "targetId": "018f3e00-4000-7000-8000-000000000001",
						"templateRevisionId": revID, "validFrom": "2026-11-17T02:00:00.000Z",
						"revision": 1,
					}},
					"nextCursor": nil, "hasMore": false,
				}), nil
			default:
				return jsonTestResponse(http.StatusNotFound, map[string]any{"code": "NOT_FOUND"}), nil
			}
		})},
	})

	serve := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		base.authenticate(req, false)
		rec := httptest.NewRecorder()
		base.handler.ServeHTTP(rec, req)
		return rec
	}

	// 1. Cross-site Assets search with multi-dimensional filters & repeatable tags.
	rec := serve("/api/v1/assets?siteId=" + base.siteID + "&status=ACTIVE&assetType=CHILLER&spaceId=" + spaceID +
		"&templateKey=chiller.v1&templateRevisionId=" + revID + "&tag=plant:north&tag=critical&limit=25")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"plant":"north"`) {
		t.Fatalf("GET /api/v1/assets failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	parsedQ, _ := url.ParseQuery(lastCoreQuery)
	if lastAction != registryauth.ActionAssetList || lastCorePath != "/internal/v1/registry/assets" ||
		parsedQ.Get("siteId") != base.siteID || parsedQ.Get("assetType") != "CHILLER" ||
		len(parsedQ["tag"]) != 2 {
		t.Fatalf("unexpected forwarded assets call: action=%s path=%s query=%s", lastAction, lastCorePath, lastCoreQuery)
	}

	// 2. Cross-site Devices search.
	devRec := serve("/api/v1/devices?deviceType=VFD&tag=vendor:schneider")
	if devRec.Code != http.StatusOK || lastAction != registryauth.ActionDeviceList || lastCorePath != "/internal/v1/registry/devices" {
		t.Fatalf("GET /api/v1/devices failed: status=%d action=%s path=%s body=%s", devRec.Code, lastAction, lastCorePath, devRec.Body.String())
	}

	// 3. Reject siteId on site-scoped route & malformed tags at gateway.
	badSiteScope := serve("/api/v1/sites/" + base.siteID + "/assets?siteId=" + base.siteID)
	if badSiteScope.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting siteId on site-scoped route, got %d (%s)", badSiteScope.Code, badSiteScope.Body.String())
	}
	badTag := serve("/api/v1/assets?tag=BadKey:1")
	if badTag.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting invalid tag key, got %d (%s)", badTag.Code, badTag.Body.String())
	}

	// 4. Template read routes & exact IAM actions.
	tplRec := serve("/api/v1/templates?templateKind=DEVICE")
	if tplRec.Code != http.StatusOK || lastAction != registryauth.ActionDeviceList || !strings.Contains(tplRec.Body.String(), `"templateKey":"chiller.v1"`) {
		t.Fatalf("GET /api/v1/templates failed: status=%d action=%s body=%s", tplRec.Code, lastAction, tplRec.Body.String())
	}

	revRec := serve("/api/v1/templates/revisions/" + revID)
	if revRec.Code != http.StatusOK || lastAction != registryauth.ActionDeviceList || !strings.Contains(revRec.Body.String(), `"status":"RELEASED"`) {
		t.Fatalf("GET /api/v1/templates/revisions/{id} failed: status=%d action=%s body=%s", revRec.Code, lastAction, revRec.Body.String())
	}

	assignRec := serve("/api/v1/sites/" + base.siteID + "/template-assignments?targetType=DEVICE&targetId=018f3e00-4000-7000-8000-000000000001")
	if assignRec.Code != http.StatusOK || lastAction != registryauth.ActionAssetModelRead || !strings.Contains(assignRec.Body.String(), revID) {
		t.Fatalf("GET /api/v1/sites/{siteId}/template-assignments failed: status=%d action=%s body=%s", assignRec.Code, lastAction, assignRec.Body.String())
	}

	// 4b. Template read fallback succeeds for tenant-scoped ActionTemplateManage principals lacking device.list / asset-model.read.
	prevIAM := base.handler.identity.config.IAMHTTPClient
	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
		}
		if input.Action != registryauth.ActionTemplateManage {
			return jsonTestResponse(http.StatusForbidden, map[string]any{"code": "AUTHORIZATION_DENIED"}), nil
		}
		mu.Lock()
		lastAction = input.Action
		mu.Unlock()
		claims := registryauth.GrantClaims{
			Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
			PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, Actions: []registryauth.Action{registryauth.ActionTemplateManage},
			PolicyRevision: "tpl-admin:1/iam:1", DecisionReason: registryauth.ReasonAllowTenantRole,
			SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
			IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
		}
		decision := registryauth.Decision{
			Allowed: true, PrincipalID: base.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
			TenantID: base.tenantID, Actions: []registryauth.Action{registryauth.ActionTemplateManage},
			PolicyRevision: "tpl-admin:1/iam:1", ReasonCode: registryauth.ReasonAllowTenantRole, DecidedAt: now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
	})}
	for _, tplPath := range []string{
		"/api/v1/templates",
		"/api/v1/templates/revisions/" + revID,
		"/api/v1/sites/" + base.siteID + "/template-assignments",
	} {
		recFallback := serve(tplPath)
		if recFallback.Code != http.StatusOK || lastAction != registryauth.ActionTemplateManage {
			t.Fatalf("ActionTemplateManage fallback failed for %s: status=%d action=%s body=%s", tplPath, recFallback.Code, lastAction, recFallback.Body.String())
		}
	}
	base.handler.identity.config.IAMHTTPClient = prevIAM

	// 5. Reject malformed tags returned in Asset payload.
	route, _, matched := matchPublicRegistryRoute(http.MethodGet, "/api/v1/assets")
	if !matched {
		t.Fatal("matchPublicRegistryRoute did not match /api/v1/assets")
	}
	if _, err := canonicalRegistrySuccessForRoute(route, []byte(`{
		"items":[{"id":"`+base.assetID+`","tenantId":"`+base.tenantID+`","siteId":"`+base.siteID+`","code":"c1","displayName":"C1","assetType":"AHU","status":"ACTIVE","tags":{"UpperBad":"x"},"revision":1,"createdAt":"2026-11-17T02:00:00.000Z","updatedAt":"2026-11-17T02:00:00.000Z"}],
		"nextCursor":null,"hasMore":false
	}`)); err == nil {
		t.Fatal("expected canonicalRegistrySuccessForRoute to reject invalid tag key")
	}

	_ = platformapi.ListAssetsPath
}
