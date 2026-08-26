package telemetry

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

func TestHTTPGrantAuthorizerVerifiesExactScopeBeforeSingleUseConsumption(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	targets := []telemetryauth.Target{{DeviceID: deviceA, Keys: []string{"zone.temperature"}}}
	grant, claims := signedGrant(t, privateKey, now, telemetryauth.ActionSnapshotRead, targets)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/internal/v1/telemetry/grants:consume" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		var input grantConsumeRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			t.Errorf("decode consume request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if input.DelegationGrant != grant || input.PrincipalID != claims.PrincipalID || input.SessionID != claims.SessionID || input.TenantID != claims.TenantID || input.Action != claims.Action {
			t.Errorf("consume request drifted: %#v", input)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(grantAcceptance{
			TokenID: claims.TokenID, PrincipalID: claims.PrincipalID, SessionID: claims.SessionID,
			TenantID: claims.TenantID, Action: claims.Action,
			ScopeDigest: claims.ScopeDigest, PolicyRevision: claims.PolicyRevision, ExpiresAt: claims.ExpiresAt,
		})
	}))
	defer server.Close()

	authorizer, err := NewHTTPGrantAuthorizer(server.URL, server.Client(), &privateKey.PublicKey, claims.Issuer, claims.Audience)
	if err != nil {
		t.Fatal(err)
	}
	authorizer.now = func() time.Time { return now }
	access, err := authorizer.Authorize(t.Context(), gatewaySPIFFE, grant, telemetryauth.ActionSnapshotRead, targets)
	if err != nil {
		t.Fatal(err)
	}
	if access.TokenID != claims.TokenID || access.PrincipalID != claims.PrincipalID || access.SessionID != claims.SessionID || access.TenantID != claims.TenantID || access.PolicyRevision != claims.PolicyRevision {
		t.Fatalf("access=%#v", access)
	}
	if calls.Load() != 1 {
		t.Fatalf("IAM calls=%d", calls.Load())
	}

	if _, err := authorizer.Authorize(t.Context(), "spiffe://hvac.local/other", grant, telemetryauth.ActionSnapshotRead, targets); !errors.Is(err, ErrGrantRejected) {
		t.Fatalf("wrong presenter err=%v", err)
	}
	altered := []telemetryauth.Target{{DeviceID: deviceA, Keys: []string{"zone.humidity"}}}
	if _, err := authorizer.Authorize(t.Context(), gatewaySPIFFE, grant, telemetryauth.ActionSnapshotRead, altered); !errors.Is(err, ErrGrantRejected) {
		t.Fatalf("altered scope err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid preflight reached IAM: calls=%d", calls.Load())
	}
}

func TestHTTPGrantAuthorizerFailsClosedWhenIAMOrAcceptanceIsInvalid(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	targets := []telemetryauth.Target{{DeviceID: deviceA}}
	grant, claims := signedGrant(t, privateKey, now, telemetryauth.ActionSnapshotRead, targets)

	tests := []struct {
		name   string
		handle http.HandlerFunc
	}{
		{
			name:   "IAM unavailable",
			handle: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) },
		},
		{
			name: "acceptance scope mismatch",
			handle: func(writer http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(writer).Encode(grantAcceptance{
					TokenID: claims.TokenID, PrincipalID: claims.PrincipalID, SessionID: claims.SessionID,
					TenantID: claims.TenantID, Action: claims.Action,
					ScopeDigest: "mismatched", PolicyRevision: claims.PolicyRevision, ExpiresAt: claims.ExpiresAt,
				})
			},
		},
		{
			name: "acceptance has trailing JSON",
			handle: func(writer http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(writer).Encode(grantAcceptance{
					TokenID: claims.TokenID, PrincipalID: claims.PrincipalID, SessionID: claims.SessionID,
					TenantID: claims.TenantID, Action: claims.Action,
					ScopeDigest: claims.ScopeDigest, PolicyRevision: claims.PolicyRevision, ExpiresAt: claims.ExpiresAt,
				})
				_, _ = writer.Write([]byte(`{}`))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handle)
			defer server.Close()
			authorizer, err := NewHTTPGrantAuthorizer(server.URL, server.Client(), &privateKey.PublicKey, claims.Issuer, claims.Audience)
			if err != nil {
				t.Fatal(err)
			}
			authorizer.now = func() time.Time { return now }
			if _, err := authorizer.Authorize(t.Context(), gatewaySPIFFE, grant, telemetryauth.ActionSnapshotRead, targets); !errors.Is(err, ErrAuthorizationUnavailable) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func signedGrant(t *testing.T, privateKey *rsa.PrivateKey, now time.Time, action telemetryauth.Action, targets []telemetryauth.Target) (string, telemetryauth.GrantClaims) {
	t.Helper()
	digest, err := telemetryauth.ScopeDigest(action, orgA, targets)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := telemetryauth.CanonicalTargets(targets)
	if err != nil {
		t.Fatal(err)
	}
	keyCount := 0
	for _, target := range canonical {
		keyCount += len(target.Keys)
	}
	claims := telemetryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: gatewaySPIFFE, Audience: "telemetry-runtime-service",
		PrincipalID: "018f2e00-2000-7000-8000-000000000001", SubjectIssuer: "https://identity.example", Subject: "subject-a",
		TenantID: orgA, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: gatewaySPIFFE}},
		Action: action, ScopeDigest: digest, TargetCount: len(canonical), KeyCount: keyCount,
		PolicyRevision: "telemetry-access:1", SessionID: "session-a", ParentTokenID: "parent-a",
		RequestID: "request-a", TraceID: "0123456789abcdef0123456789abcdef", Route: "/api/v1/devices/{deviceId}/observation-snapshot",
		IssuedAt: now.Add(-time.Second).Unix(), ExpiresAt: now.Add(20 * time.Second).Unix(), TokenID: "grant-a", Transitive: false,
	}
	grant, err := telemetryauth.SignGrant(privateKey, claims)
	if err != nil {
		t.Fatal(err)
	}
	claims.Version = telemetryauth.GrantVersion
	return grant, claims
}
