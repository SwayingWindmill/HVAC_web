package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel"
)

func TestHistoryGrantBindsConfiguredPresenter(t *testing.T) {
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	handler := &handler{
		identity: &identityController{
			config: IdentityConfig{
				ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
				DelegationSigner:        signer,
				DelegationTTL:           5 * time.Minute,
			},
			now: func() time.Time { return now },
		},
		analytics: &analyticsController{queryAudience: "telemetry-query-service"},
	}
	caller := telemetryCaller{
		principal: identitycontext.UserPrincipal{
			Subject: "user-1",
			Issuer:  "https://identity.example.test",
		},
		tenantID:  "01990000-3000-7000-8000-000000000001",
		contextID: "session-1",
		expiresAt: now.Add(10 * time.Minute),
	}
	authorization := telemetryAuthorization{
		principalID:    "01990000-3100-7000-8000-000000000001",
		policyRevision: "telemetry-policy-v1",
	}
	presenter := "spiffe://hvac.local/fdd-service"
	grant, failure := handler.signHistoryQueryGrant(
		caller,
		authorization,
		caller.tenantID,
		telemetryhistorymodel.DeviceHistoryAction,
		func() (string, error) { return "history-scope-digest", nil },
		presenter,
	)
	if failure != nil {
		t.Fatalf("sign history grant: %#v", failure)
	}
	claims, err := identitycontext.VerifyDelegation(signer.Public(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "spiffe://hvac.local/platform-gateway" || claims.ExecutingService != presenter || claims.Audience != "telemetry-query-service" {
		t.Fatalf("claims=%#v", claims)
	}
}
