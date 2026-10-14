package iam

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestIAMCommandDecisionIssuesExactPurposeBoundGrant(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	facts := commandFacts(now, RoleAssignment{TenantID: S1FixtureTenantAID, SiteID: "site-1", RoleKey: "operator", Capabilities: []string{"command.submit.medium"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	h := NewHandler(Config{
		Now: func() time.Time { return now }, CommandAuthorizationStore: newStaticCommandAuthorizationStore("command-policy-7", 3, commandDeviceSites, []AuthorizationFacts{facts}),
		CommandGrantSigner: signer, CommandGrantIssuer: "spiffe://hvac.local/iam-service",
		CommandGrantAudience: "command-service", NewCommandGrantID: func() string { return "grant-command-1" },
	}).(*handler)

	input := commandauth.DecisionRequest{
		TenantID: S1FixtureTenantAID, SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetChilledWaterTemperatureSetpoint,
		CapabilityRevision: "capability:set-chilled-water-temperature-setpoint:v1", Purpose: commandmodel.AuthorizationCommandSubmit,
	}
	body, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", CommandDecisionPath, bytes.NewReader(body))
	status := h.handleCommandDecision(recorder, request, identitycontext.DelegationClaims{
		SubjectIssuer: "https://issuer.example.test", Subject: "user-1", TenantID: S1FixtureTenantAID,
	}, "spiffe://hvac.local/platform-gateway")
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, recorder.Body.String())
	}
	var output commandauth.DecisionResponse
	if json.NewDecoder(recorder.Body).Decode(&output) != nil {
		t.Fatal("decode command decision")
	}
	if !output.Decision.Allowed || output.Decision.MaximumRisk != commandmodel.RiskMedium || output.DelegationGrant == "" {
		t.Fatalf("unexpected decision %#v", output)
	}
	claims, err := commandauth.VerifyGrant(&signer.PublicKey, output.DelegationGrant)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Purpose != commandmodel.AuthorizationCommandSubmit || claims.PrincipalID != "principal-1" || claims.DeviceID != "device-1" || claims.MaximumRisk != commandmodel.RiskMedium || claims.EmergencyRevocationRevision != 3 {
		t.Fatalf("unexpected command grant %#v", claims)
	}
}

var commandDeviceSites = map[string]string{"device-1": "site-1", "device-2": "site-2"}

func decideCommand(t *testing.T, now time.Time, facts AuthorizationFacts, deviceID, siteID string, purpose commandmodel.AuthorizationPurpose) commandauth.Decision {
	t.Helper()
	decision, err := evaluateCommandAuthorization(t.Context(), newStaticCommandAuthorizationStore("command-policy-1", 0, commandDeviceSites, []AuthorizationFacts{facts}), now,
		"https://issuer.example.test", "user-1", commandauth.DecisionRequest{
			TenantID: S1FixtureTenantAID, SiteID: siteID, DeviceID: deviceID,
			Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", Purpose: purpose,
		})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestIAMCommandDecisionDoesNotReuseSubmitPermissionForApproval(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	facts := commandFacts(now, RoleAssignment{TenantID: S1FixtureTenantAID, RoleKey: "operator", Capabilities: []string{"command.submit.high"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	if decision := decideCommand(t, now, facts, "device-1", "site-1", commandmodel.AuthorizationCommandApprove); decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyScope {
		t.Fatalf("submit permission expanded into approval %#v", decision)
	}
}

func TestIAMCommandDecisionTakesTheHighestCeilingOnTheDevicesSite(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	facts := commandFacts(now,
		RoleAssignment{TenantID: S1FixtureTenantAID, RoleKey: "operator", Capabilities: []string{"command.submit.medium"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
		RoleAssignment{TenantID: S1FixtureTenantAID, SiteID: "site-1", RoleKey: "site-manager", Capabilities: []string{"command.submit.high"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)},
	)
	if decision := decideCommand(t, now, facts, "device-1", "site-1", commandmodel.AuthorizationCommandSubmit); !decision.Allowed || decision.MaximumRisk != commandmodel.RiskHigh {
		t.Fatalf("site-1 decision %#v", decision)
	}
	if decision := decideCommand(t, now, facts, "device-2", "site-2", commandmodel.AuthorizationCommandSubmit); !decision.Allowed || decision.MaximumRisk != commandmodel.RiskMedium {
		t.Fatalf("site-2 decision %#v", decision)
	}
	// A Device is commanded only on its own Site, whatever Site the request names.
	if decision := decideCommand(t, now, facts, "device-2", "site-1", commandmodel.AuthorizationCommandSubmit); decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyScope {
		t.Fatalf("mismatched Site decision %#v", decision)
	}
}

func TestIAMCommandExplicitDenyOverridesAllow(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	facts := commandFacts(now, RoleAssignment{TenantID: S1FixtureTenantAID, RoleKey: "site-manager", Capabilities: []string{"command.submit.high"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	facts.ExplicitDenies = []ExplicitDeny{{TenantID: S1FixtureTenantAID, SiteID: "site-1", Capability: "command.submit.high", ValidFrom: now.Add(-time.Hour)}}
	if decision := decideCommand(t, now, facts, "device-1", "site-1", commandmodel.AuthorizationCommandSubmit); decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny lost %#v", decision)
	}
}

func commandFacts(now time.Time, assignments ...RoleAssignment) AuthorizationFacts {
	return AuthorizationFacts{
		Principal:   PrincipalRecord{ID: "principal-1", SubjectIssuer: "https://issuer.example.test", Subject: "user-1", Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: S1FixtureTenantAID, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Assignments: assignments,
	}
}

func TestIAMCommandDenyRemovesOnlyItsOwnCeiling(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	facts := commandFacts(now, RoleAssignment{TenantID: S1FixtureTenantAID, RoleKey: "custom", Capabilities: []string{"command.submit.critical", "command.submit.medium"}, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)})
	facts.ExplicitDenies = []ExplicitDeny{{TenantID: S1FixtureTenantAID, Capability: "command.submit.critical", ValidFrom: now.Add(-time.Hour)}}
	if decision := decideCommand(t, now, facts, "device-1", "site-1", commandmodel.AuthorizationCommandSubmit); !decision.Allowed || decision.MaximumRisk != commandmodel.RiskMedium {
		t.Fatalf("decision %#v", decision)
	}
}
