package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type assetModelETagFixture struct {
	*commandGatewayFixture
	miniSessionID string
	denyIAM       atomic.Bool
	otherSiteOnly atomic.Bool
	assetName     atomic.Value
	coreCalls     atomic.Int32
}

func newAssetModelETagFixture(t *testing.T) *assetModelETagFixture {
	t.Helper()
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

	fix := &assetModelETagFixture{
		commandGatewayFixture: base,
		miniSessionID:         "asset-model-mini-bearer-token",
	}
	fix.assetName.Store("Chiller A")

	csrfCiphertext, err := base.handler.identity.encryptBytes([]byte("mini-asset-model-csrf"))
	if err != nil {
		t.Fatalf("encrypt mini csrf: %v", err)
	}
	if _, err := base.store.CreateSession(context.Background(), sessionstore.Session{
		Client:              sessionstore.ClientMiniProgram,
		ID:                  fix.miniSessionID,
		Principal:           identitycontext.UserPrincipal{Subject: "mini-operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID:            base.tenantID,
		CSRFTokenCiphertext: csrfCiphertext,
		ExpiresAt:           now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action:            "SESSION_CREATED",
		Result:            "SUCCEEDED",
		PolicyRevision:    "identity-policy-1",
		CorrelationID:     "asset-model-etag-mini",
		TraceID:           strings.Repeat("a", 32),
		Traceparent:       "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now,
	}); err != nil {
		t.Fatalf("create mini program session: %v", err)
	}

	base.handler.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		base.iamCalls.Add(1)
		parent, err := identitycontext.VerifyDelegation(&base.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
		}
		var input registryauth.DecisionRequest
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
		}
		allowedSites := []string{base.siteID}
		if fix.otherSiteOnly.Load() {
			allowedSites = []string{"018f3e00-2000-7000-8000-000000000099"}
		}
		allowed := !fix.denyIAM.Load()
		reason := registryauth.ReasonAllowSiteRole
		if !allowed {
			reason = registryauth.ReasonDenyActionNotGranted
		}
		claims := registryauth.GrantClaims{
			Issuer:         "spiffe://hvac.local/iam-service",
			Presenter:      "spiffe://hvac.local/platform-gateway",
			Audience:       "platform-core-service",
			PrincipalID:    base.principalID,
			SubjectIssuer:  parent.SubjectIssuer,
			Subject:        parent.Subject,
			TenantID:       base.tenantID,
			AllowedSiteIDs: allowedSites,
			Actions:        []registryauth.Action{input.Action},
			PolicyRevision: "registry-read:1/iam:1",
			DecisionReason: reason,
			SessionID:      parent.SessionID,
			ParentTokenID:  parent.TokenID,
			IssuedAt:       now.Unix(),
			ExpiresAt:      now.Add(30 * time.Second).Unix(),
			TokenID:        randomURLToken(16),
		}
		decision := registryauth.Decision{
			Allowed:        allowed,
			PrincipalID:    base.principalID,
			SubjectIssuer:  parent.SubjectIssuer,
			Subject:        parent.Subject,
			TenantID:       base.tenantID,
			AllowedSiteIDs: allowedSites,
			Actions:        []registryauth.Action{input.Action},
			PolicyRevision: "registry-read:1/iam:1",
			ReasonCode:     reason,
			DecidedAt:      now.Format(time.RFC3339Nano),
		}
		return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{
			Decision:        decision,
			DelegationGrant: commandUnsignedRegistryGrant(claims),
		}), nil
	})}

	base.handler.registry = newRegistryController(&RegistryConfig{
		CoreBaseURL: "https://registry.example.test",
		CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			fix.coreCalls.Add(1)
			ts := now.UTC().Format("2006-01-02T15:04:05.000Z")
			name, _ := fix.assetName.Load().(string)
			model := platformapi.SiteAssetModel{
				SchemaVersion: 2,
				TenantID:      base.tenantID,
				SiteID:        base.siteID,
				Spaces:        []platformapi.Space{},
				Assets: []platformapi.Asset{{
					ID:          base.assetID,
					TenantID:    base.tenantID,
					SiteID:      base.siteID,
					Code:        "chiller-a",
					DisplayName: name,
					AssetType:   "CHILLER",
					Status:      "ACTIVE",
					Revision:    1,
					CreatedAt:   ts,
					UpdatedAt:   ts,
				}},
				Devices:         []platformapi.Device{},
				Sensors:         []platformapi.Sensor{},
				TelemetryPoints: []platformapi.TelemetryPoint{},
				Relationships:   []platformapi.AssetRelationship{},
				Counts: platformapi.AssetModelCounts{
					Assets: 1,
				},
			}
			return telemetryJSONResponse(http.StatusOK, model), nil
		})},
	})

	return fix
}

func (fix *assetModelETagFixture) getAssetModel(authMode string, ifNoneMatch ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+fix.siteID+"/asset-model", nil)
	switch authMode {
	case "cookie":
		fix.authenticate(req, false)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+fix.miniSessionID)
	}
	for _, headerVal := range ifNoneMatch {
		req.Header.Add("If-None-Match", headerVal)
	}
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)
	return rec
}

func TestAssetModelETagReturnsQuotedHashAndCacheHeadersOn200(t *testing.T) {
	fix := newAssetModelETagFixture(t)
	etagPattern := regexp.MustCompile(`^"[0-9a-f]{64}"$`)

	for _, mode := range []string{"cookie", "bearer"} {
		rec := fix.getAssetModel(mode)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want 200 body=%s", mode, rec.Code, rec.Body.String())
		}
		etag := rec.Header().Get("ETag")
		if !etagPattern.MatchString(etag) {
			t.Fatalf("%s ETag=%q want quoted 64-hex digest", mode, etag)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
			t.Fatalf("%s Cache-Control=%q want private, no-cache", mode, got)
		}
		if got := rec.Header().Get("Vary"); got != "Cookie, Authorization" {
			t.Fatalf("%s Vary=%q want Cookie, Authorization", mode, got)
		}
		var model platformapi.SiteAssetModel
		if err := json.NewDecoder(rec.Body).Decode(&model); err != nil {
			t.Fatalf("%s decode asset-model: %v", mode, err)
		}
		if model.SiteID != fix.siteID || len(model.Assets) != 1 || model.Assets[0].DisplayName != "Chiller A" {
			t.Fatalf("%s unexpected asset-model %#v", mode, model)
		}
	}
}

func TestAssetModelETagReturns304WithEmptyBodyWhenIfNoneMatchMatches(t *testing.T) {
	fix := newAssetModelETagFixture(t)
	first := fix.getAssetModel("bearer")
	if first.Code != http.StatusOK {
		t.Fatalf("initial status=%d want 200 body=%s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")

	matchCases := []struct {
		name   string
		header string
	}{
		{name: "exact strong tag", header: etag},
		{name: "weak prefix", header: "W/" + etag},
		{name: "comma separated list", header: `"deadbeef", W/` + etag},
		{name: "wildcard", header: "*"},
	}
	for _, tc := range matchCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := fix.getAssetModel("bearer", tc.header)
			if rec.Code != http.StatusNotModified {
				t.Fatalf("status=%d want 304 body=%s", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("expected empty 304 body, got %q", rec.Body.String())
			}
			if got := rec.Header().Get("ETag"); got != etag {
				t.Fatalf("ETag=%q want %q", got, etag)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
				t.Fatalf("Cache-Control=%q want private, no-cache", got)
			}
			if got := rec.Header().Get("Vary"); got != "Cookie, Authorization" {
				t.Fatalf("Vary=%q want Cookie, Authorization", got)
			}
		})
	}

	missCases := []string{`"0000000000000000"`, `w/` + etag, etag + "trailing"}
	for _, header := range missCases {
		rec := fix.getAssetModel("bearer", header)
		if rec.Code != http.StatusOK {
			t.Fatalf("If-None-Match=%q status=%d want 200", header, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("If-None-Match=%q expected full JSON body on 200", header)
		}
	}
}

func TestAssetModelETagInvalidatesWhenModelChanges(t *testing.T) {
	fix := newAssetModelETagFixture(t)
	first := fix.getAssetModel("bearer")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200", first.Code)
	}
	etag1 := first.Header().Get("ETag")

	fix.assetName.Store("Chiller A v2")
	second := fix.getAssetModel("bearer", etag1)
	if second.Code != http.StatusOK {
		t.Fatalf("after mutation status=%d want 200", second.Code)
	}
	etag2 := second.Header().Get("ETag")
	if etag2 == "" || etag2 == etag1 {
		t.Fatalf("expected new ETag after mutation: old=%q new=%q", etag1, etag2)
	}
	var updated platformapi.SiteAssetModel
	if err := json.NewDecoder(second.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated asset-model: %v", err)
	}
	if len(updated.Assets) != 1 || updated.Assets[0].DisplayName != "Chiller A v2" {
		t.Fatalf("unexpected updated asset-model: %#v", updated)
	}

	third := fix.getAssetModel("bearer", etag2)
	if third.Code != http.StatusNotModified || third.Body.Len() != 0 {
		t.Fatalf("expected 304 for new ETag, got status=%d len=%d", third.Code, third.Body.Len())
	}
}

func TestAssetModelETagNeverReturns304ForUnauthenticatedOrUnauthorizedCallers(t *testing.T) {
	fix := newAssetModelETagFixture(t)
	baseline := fix.getAssetModel("bearer")
	if baseline.Code != http.StatusOK {
		t.Fatalf("baseline status=%d want 200", baseline.Code)
	}
	validETag := baseline.Header().Get("ETag")

	// 1. Unauthenticated caller sending exact ETag or wildcard must fail closed with 401.
	for _, header := range []string{validETag, "*"} {
		rec := fix.getAssetModel("none", header)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated If-None-Match=%q status=%d want 401", header, rec.Code)
		}
		if rec.Header().Get("ETag") != "" {
			t.Fatalf("unauthenticated response leaked ETag %q", rec.Header().Get("ETag"))
		}
	}

	// 2. IAM role denial must return 404 before issuing 304.
	fix.denyIAM.Store(true)
	callsBefore := fix.coreCalls.Load()
	denied := fix.getAssetModel("bearer", validETag)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("IAM denied status=%d want 404 body=%s", denied.Code, denied.Body.String())
	}
	if denied.Header().Get("ETag") != "" || fix.coreCalls.Load() != callsBefore {
		t.Fatalf("IAM denied must not call core or leak ETag: etag=%q coreCalls=%d->%d", denied.Header().Get("ETag"), callsBefore, fix.coreCalls.Load())
	}
	fix.denyIAM.Store(false)

	// 3. Site scope revocation (allowed only on a different site) must return 404 before calling Core Registry or issuing 304.
	fix.otherSiteOnly.Store(true)
	callsBefore = fix.coreCalls.Load()
	outOfScope := fix.getAssetModel("bearer", "*")
	if outOfScope.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope site status=%d want 404 body=%s", outOfScope.Code, outOfScope.Body.String())
	}
	if outOfScope.Header().Get("ETag") != "" || fix.coreCalls.Load() != callsBefore {
		t.Fatalf("out-of-scope site must not call core or leak ETag: etag=%q coreCalls=%d->%d", outOfScope.Header().Get("ETag"), callsBefore, fix.coreCalls.Load())
	}
	fix.otherSiteOnly.Store(false)

	// 4. Revoked Mini Program Bearer session must fail closed with 401 even with valid ETag.
	if _, err := fix.store.RevokeSession(context.Background(), fix.miniSessionID, sessionstore.MutationContext{
		Action:            "SESSION_REVOKED",
		Result:            "SUCCEEDED",
		PolicyRevision:    "identity-policy-1",
		CorrelationID:     "asset-model-revoke-test",
		TraceID:           strings.Repeat("a", 32),
		Traceparent:       "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        fix.now,
	}); err != nil {
		t.Fatalf("revoke mini session: %v", err)
	}
	revoked := fix.getAssetModel("bearer", validETag)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked bearer status=%d want 401 body=%s", revoked.Code, revoked.Body.String())
	}
	if revoked.Header().Get("ETag") != "" {
		t.Fatalf("revoked bearer leaked ETag %q", revoked.Header().Get("ETag"))
	}
}
