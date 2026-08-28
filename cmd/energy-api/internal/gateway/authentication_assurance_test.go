package gateway

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

func TestValidAuthenticationAssuranceRejectsDowngradeAndStaleStepUp(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	freshMFA := oidcClaims{
		ACR:      authenticationACRMFA,
		AMR:      []string{"pwd", "otp"},
		AuthTime: now.Add(-2 * time.Minute).Unix(),
	}
	if !validAuthenticationAssurance(freshMFA, authenticationACRMFA, now) {
		t.Fatal("expected fresh MFA assurance to satisfy a high-assurance request")
	}
	basic := oidcClaims{
		ACR:      authenticationACRBasic,
		AMR:      []string{"pwd"},
		AuthTime: now.Unix(),
	}
	if validAuthenticationAssurance(basic, authenticationACRMFA, now) {
		t.Fatal("expected basic assurance to be rejected for a high-assurance request")
	}
	if validAuthenticationAssurance(basic, "urn:hvac:loa:unknown", now) {
		t.Fatal("expected an unknown requested assurance level to fail closed")
	}
	stale := freshMFA
	stale.AuthTime = now.Add(-11 * time.Minute).Unix()
	if validAuthenticationAssurance(stale, authenticationACRMFA, now) {
		t.Fatal("expected stale MFA assurance to be rejected for step-up")
	}
}

func TestHighRiskCommandApprovalRequiresRecentMFA(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	basic := bffSession{Session: sessionstore.Session{
		AuthenticationACR:  authenticationACRBasic,
		AuthenticationAMR:  []string{"pwd"},
		AuthenticationTime: now,
	}}
	if !commandApprovalRequiresStepUp(commandView{Risk: commandmodel.RiskHigh}, basic, now) {
		t.Fatal("expected HIGH-risk approval to require step-up from a basic session")
	}
	if commandApprovalRequiresStepUp(commandView{Risk: commandmodel.RiskMedium}, basic, now) {
		t.Fatal("expected MEDIUM-risk approval not to require step-up")
	}
	recentMFA := bffSession{Session: sessionstore.Session{
		AuthenticationACR:  authenticationACRMFA,
		AuthenticationAMR:  []string{"pwd", "otp"},
		AuthenticationTime: now.Add(-5 * time.Minute),
	}}
	if commandApprovalRequiresStepUp(commandView{Risk: commandmodel.RiskCritical}, recentMFA, now) {
		t.Fatal("expected recent MFA assurance to satisfy CRITICAL-risk approval step-up")
	}
}
