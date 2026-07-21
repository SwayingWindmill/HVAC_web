package logtopoc

import (
	"context"
	"testing"
)

func TestEvaluationSelectsPartialSDKAdoptionFromMeasuredBehavior(t *testing.T) {
	report, err := Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision.Mode != "partial-sdk-adoption" {
		t.Fatalf("unexpected decision: %s", report.Decision.Mode)
	}
	for name, check := range map[string]Check{
		"pkce":             report.Checks.AuthorizationCodePKCE,
		"state":            report.Checks.StateValidation,
		"state-one-time":   report.Checks.StateOneTimeUse,
		"jwks-rotation":    report.Checks.JWKSRotation,
		"provider-outage":  report.Checks.ProviderOutageFailsClosed,
		"local-clear":      report.Checks.LocalCredentialClear,
		"postgres-adapter": report.Checks.PostgresStorageAdapterPossible,
	} {
		if !check.Passed {
			t.Fatalf("required SDK capability %s failed: %s", name, check.Evidence)
		}
	}
	for name, check := range map[string]Check{
		"nonce-sent":             report.Checks.NonceSent,
		"nonce-mismatch":         report.Checks.NonceMismatchRejected,
		"token-type":             report.Checks.TokenTypeEnforced,
		"storage-errors":         report.Checks.StorageWriteFailureObservable,
		"refresh-single-flight":  report.Checks.DistributedRefreshSingleFlight,
		"revoke-errors":          report.Checks.RevocationFailureObservable,
		"organization-signature": report.Checks.OrganizationClaimsVerified,
	} {
		if check.Passed {
			t.Fatalf("full-client rejection gate %s unexpectedly passed", name)
		}
	}
	if AllCriticalFullClientChecksPass(report.Checks) {
		t.Fatal("full client unexpectedly passed all critical gates")
	}
}

func TestDecisionListsPlatformAuthorizationAsRetained(t *testing.T) {
	report, err := Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, control := range report.Decision.RetainPlatformControls {
		if control == "HVAC OrganizationMembership, RoleBinding, SiteBinding, explicit deny and policy revision" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("decision did not retain platform-owned HVAC authorization")
	}
}
