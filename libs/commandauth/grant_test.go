package commandauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestGrantScopeRiskAndRevisionValidation(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	claims := GrantClaims{
		Issuer: "iam-service", Presenter: "platform-gateway", Audience: "command-service",
		GrantID: "grant-1", Purpose: commandmodel.AuthorizationCommandSubmit,
		PrincipalID: "principal-1", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskMedium,
		CapabilityRevision: "capability:set-temperature-setpoint:v1", PolicyRevision: "policy-7",
		EmergencyRevocationRevision: 3, IssuedAt: now.Unix(), ExpiresAt: now.Add(20 * time.Second).Unix(), TokenID: "token-1",
	}
	token, err := SignGrant(privateKey, claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyGrant(&privateKey.PublicKey, token)
	if err != nil {
		t.Fatal(err)
	}
	validation := Validation{
		Now: now, Issuer: "iam-service", Presenter: "platform-gateway", Audience: "command-service",
		Purpose:     commandmodel.AuthorizationCommandSubmit,
		PrincipalID: "principal-1", TenantID: "org-1", SiteID: "site-1", DeviceID: "device-1",
		Capability:         commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: "capability:set-temperature-setpoint:v1", Risk: commandmodel.RiskMedium,
		UseChecker: func(GrantClaims) (UseStatus, error) {
			return UseStatus{CurrentPolicyRevision: "policy-7", CurrentRevocationRevision: 3}, nil
		},
	}
	if err := ValidateGrant(verified, validation); err != nil {
		t.Fatal(err)
	}
	validation.Risk = commandmodel.RiskHigh
	if err := ValidateGrant(verified, validation); err == nil {
		t.Fatal("risk ceiling violation unexpectedly accepted")
	}
	validation.Risk = commandmodel.RiskMedium
	validation.DeviceID = "device-2"
	if err := ValidateGrant(verified, validation); err == nil {
		t.Fatal("scope drift unexpectedly accepted")
	}
	validation.DeviceID = "device-1"
	validation.Purpose = commandmodel.AuthorizationCommandApprove
	if err := ValidateGrant(verified, validation); err == nil {
		t.Fatal("authorization purpose drift unexpectedly accepted")
	}
}
