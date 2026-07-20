package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLegacyProxySendsRestrictedDelegationAndNormalizesResponse(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	identity, signer := legacyTestIdentity(t, now)
	session := legacyTestSession(now)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		for _, header := range []string{"Cookie", "Authorization", "X-CSRF-Token", "X-Principal", "X-Organization-ID"} {
			if request.Header.Get(header) != "" {
				t.Fatalf("Legacy request forwarded %s", header)
			}
		}
		claims, err := identitycontext.VerifyDelegation(&signer.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatal(err)
		}
		if err := identitycontext.ValidateDelegation(claims, now, identity.config.ExecutingWorkloadSPIFFE, "legacy-hvac-backend", "legacy:platform-status:read", "organization:org-01"); err != nil {
			t.Fatal(err)
		}
		if claims.SessionID != sessionevent.AuditAggregateID(session.ID) || len(claims.Roles) != 0 {
			t.Fatalf("Legacy delegation leaked Session or expanded roles: %#v", claims)
		}
		body := `{"code":200,"message":"success","data":{"status":"UP","uptime":12,"timestamp":"2026-07-19T12:00:00.000Z","version":"1.0.0","memory":{"rss":999}},"traceId":"legacy-internal","timestamp":"2026-07-19T12:00:00.000Z"}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	controller := newLegacyController(&LegacyConfig{BaseURL: "https://legacy.invalid", Audience: "legacy-hvac-backend", HTTPClient: client})
	decision := ownershipregistry.Decision{RouteKey: "GET /api/v1/platform/status", PathTemplate: "/api/v1/platform/status", SelectedOwner: ownershipregistry.OwnerLegacy, RegistryRevision: 3, RouteRevision: 2, CompatibilityMode: "legacy-read"}

	response, failure := controller.callPlatformStatus(context.Background(), identity, session, decision, "request-01", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	if failure != nil {
		t.Fatalf("Legacy status failed: %#v", failure)
	}
	if calls.Load() != 1 || response.Implementation != "legacy" || response.Service != "platform-status" || response.RoutePolicyRevision != 3 || response.RouteRevision != 2 {
		t.Fatalf("unexpected normalized response: %#v", response)
	}
}

func TestLegacyTimeoutOpensCircuit(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	identity, _ := legacyTestIdentity(t, now)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	controller := newLegacyController(&LegacyConfig{
		BaseURL:          "https://legacy.invalid",
		HTTPClient:       client,
		Timeout:          10 * time.Millisecond,
		FailureThreshold: 2,
		OpenDuration:     time.Minute,
	})
	decision := ownershipregistry.Decision{SelectedOwner: ownershipregistry.OwnerLegacy, RegistryRevision: 1, RouteRevision: 1, CompatibilityMode: "legacy-read"}
	for index := 0; index < 2; index++ {
		_, failure := controller.callPlatformStatus(context.Background(), identity, legacyTestSession(now), decision, "request", "")
		if failure == nil || failure.code != "LEGACY_TIMEOUT" || failure.status != http.StatusGatewayTimeout {
			t.Fatalf("timeout %d failure = %#v", index, failure)
		}
	}
	_, failure := controller.callPlatformStatus(context.Background(), identity, legacyTestSession(now), decision, "request", "")
	if failure == nil || failure.code != "LEGACY_CIRCUIT_OPEN" || failure.status != http.StatusServiceUnavailable {
		t.Fatalf("circuit failure = %#v", failure)
	}
	if calls.Load() != 2 {
		t.Fatalf("open circuit made %d upstream calls", calls.Load())
	}
}

func legacyTestIdentity(t *testing.T, now time.Time) (*identityController, *ecdsa.PrivateKey) {
	t.Helper()
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &identityController{
		config: IdentityConfig{
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
			PolicyRevision:          "policy-v1",
			DelegationSigner:        signer,
			DelegationTTL:           30 * time.Second,
		},
		now: func() time.Time { return now },
	}, signer
}

func legacyTestSession(now time.Time) bffSession {
	return bffSession{Session: sessionstore.Session{
		ID:                   "opaque-session-cookie-value",
		Principal:            identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		ActingOrganizationID: "org-01",
		ExpiresAt:            now.Add(time.Hour),
	}}
}
