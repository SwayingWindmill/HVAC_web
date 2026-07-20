package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

func TestPublicLegacyTimeoutAndCircuitProblems(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := sessionstore.NewMemoryStore()
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion":1,"registryRevision":1,"routes":[{
			"method":"GET","path":"/api/v1/platform/status","owner":"legacy-hvac-backend","revision":1,
			"rollout":{"mode":"percentage","percentage":100,"fallbackOwner":"platform-gateway","cohortSalt":"platform-status-v1"},
			"compatibilityMode":"legacy-read","allowedScopeDimensions":["organization","principal"]
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var upstreamCalls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	h := NewHandler(Config{
		Build: platformapi.BuildInfo{Version: "test", Commit: "test", BuiltAt: now.Format(time.RFC3339)},
		Now:   func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer:              "https://issuer.example.test",
			OIDCClientID:            "client",
			OIDCRedirectURI:         "https://web.example.test/api/v1/auth/callback",
			PublicOrigin:            "https://web.example.test",
			IAMURL:                  "https://iam.example.test",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
			DelegationSigner:        signer,
			TokenEncryptionKey:      []byte("0123456789abcdef0123456789abcdef"),
			SessionStore:            store,
			PolicyRevision:          "policy-v1",
		},
		RouteManager: ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now }),
		RouteAudit:   ownershipregistry.NewMemoryAuditSink(),
		Legacy: &LegacyConfig{
			BaseURL:          "https://legacy.invalid",
			HTTPClient:       httpClient,
			Timeout:          10 * time.Millisecond,
			FailureThreshold: 2,
			OpenDuration:     time.Minute,
		},
	}).(*handler)
	csrfCiphertext, err := h.identity.encryptBytes([]byte("csrf-value"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID:                   "opaque-session-cookie-value",
		Principal:            identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", DisplayName: "Fixture User", Email: "fixture@example.test", Roles: []string{"operator"}},
		ActingOrganizationID: "org-01",
		CSRFTokenCiphertext:  csrfCiphertext,
		ExpiresAt:            now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "policy-v1",
		CorrelationID: "request-create", TraceID: strings.Repeat("a", 32),
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertPublicProblem := func(expectedStatus int, expectedCode string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, platformapi.GetPlatformStatusPath, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: created.ID})
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != expectedStatus {
			t.Fatalf("status=%d want=%d body=%s", recorder.Code, expectedStatus, recorder.Body.String())
		}
		var problem platformapi.ProblemDetails
		if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
			t.Fatal(err)
		}
		if problem.Code != expectedCode || problem.TraceID == "" || !problem.Retryable {
			t.Fatalf("unexpected public problem: %#v", problem)
		}
	}

	assertPublicProblem(http.StatusGatewayTimeout, "LEGACY_TIMEOUT")
	assertPublicProblem(http.StatusGatewayTimeout, "LEGACY_TIMEOUT")
	assertPublicProblem(http.StatusServiceUnavailable, "LEGACY_CIRCUIT_OPEN")
	if upstreamCalls.Load() != 2 {
		t.Fatalf("open circuit made %d upstream calls", upstreamCalls.Load())
	}
}
