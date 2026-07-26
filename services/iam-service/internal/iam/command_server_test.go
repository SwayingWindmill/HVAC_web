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
	facts := commandFacts(now, []CommandPermission{{
		OrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit, MaximumRisk: commandmodel.RiskMedium,
		Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}})
	h := NewHandler(Config{
		Now: func() time.Time { return now }, CommandAuthorizationStore: newStaticCommandAuthorizationStore("command-policy-7", 3, []CommandAuthorizationFacts{facts}),
		CommandGrantSigner: signer, CommandGrantIssuer: "spiffe://hvac.local/iam-service",
		CommandGrantAudience: "command-service", NewCommandGrantID: func() string { return "grant-command-1" },
	}).(*handler)

	input := commandauth.DecisionRequest{
		ActingOrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1", Purpose: commandmodel.AuthorizationCommandSubmit,
	}
	body, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", CommandDecisionPath, bytes.NewReader(body))
	status := h.handleCommandDecision(recorder, request, identitycontext.DelegationClaims{
		SubjectIssuer: "https://issuer.example.test", Subject: "user-1", ActingOrganizationID: "org-1",
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

func TestIAMCommandDecisionDoesNotReuseSubmitPermissionForApproval(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	facts := commandFacts(now, []CommandPermission{{
		OrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit, MaximumRisk: commandmodel.RiskHigh,
		Effect: BindingEffectAllow, Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}})
	decision, err := evaluateCommandAuthorization(t.Context(), newStaticCommandAuthorizationStore("command-policy-1", 0, []CommandAuthorizationFacts{facts}), now,
		"https://issuer.example.test", "user-1", commandauth.DecisionRequest{
			ActingOrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
			Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", Purpose: commandmodel.AuthorizationCommandApprove,
		})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyScope {
		t.Fatalf("submit permission expanded into approval %#v", decision)
	}
}

func TestIAMCommandExplicitDenyOverridesAllow(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	base := CommandPermission{
		OrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit, MaximumRisk: commandmodel.RiskHigh,
		Status: FactStatusActive, ValidFrom: now.Add(-time.Hour),
	}
	allow := base
	allow.Effect = BindingEffectAllow
	deny := base
	deny.Effect = BindingEffectDeny
	facts := commandFacts(now, []CommandPermission{allow, deny})
	decision, err := evaluateCommandAuthorization(t.Context(), newStaticCommandAuthorizationStore("command-policy-1", 0, []CommandAuthorizationFacts{facts}), now,
		"https://issuer.example.test", "user-1", commandauth.DecisionRequest{
			ActingOrganizationID: "org-1", SiteID: "site-1", DeviceID: "device-1",
			Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
			CapabilityRevision: "capability:set-temperature-setpoint:v1", Purpose: commandmodel.AuthorizationCommandSubmit,
		})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.ReasonCode != commandauth.ReasonDenyExplicit {
		t.Fatalf("explicit deny lost %#v", decision)
	}
}

func commandFacts(now time.Time, permissions []CommandPermission) CommandAuthorizationFacts {
	return CommandAuthorizationFacts{
		Principal:   PrincipalRecord{ID: "principal-1", SubjectIssuer: "https://issuer.example.test", Subject: "user-1", Status: FactStatusActive},
		Memberships: []OrganizationMembership{{OrganizationID: "org-1", Status: FactStatusActive, ValidFrom: now.Add(-time.Hour)}},
		Permissions: permissions,
	}
}
