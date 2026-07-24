package telemetryauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

const (
	actingOrganizationID = "018f1e00-0000-7000-8000-000000000003"
	deviceID             = "018f1e00-4000-7000-8000-000000000001"
	siblingDeviceID      = "018f1e00-4000-7000-8000-000000000003"
)

func TestScopeDigestIsOrderIndependentButExact(t *testing.T) {
	left, err := telemetryauth.ScopeDigest(telemetryauth.ActionSubscribe, actingOrganizationID, []telemetryauth.Target{{DeviceID: deviceID, Keys: []string{"zone.temperature", "fan.speed"}}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := telemetryauth.ScopeDigest(telemetryauth.ActionSubscribe, actingOrganizationID, []telemetryauth.Target{{DeviceID: deviceID, Keys: []string{"fan.speed", "zone.temperature"}}})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("equivalent exact scopes produced different digests: %q != %q", left, right)
	}
	different, err := telemetryauth.ScopeDigest(telemetryauth.ActionSubscribe, actingOrganizationID, []telemetryauth.Target{{DeviceID: deviceID, Keys: []string{"zone.temperature"}}})
	if err != nil {
		t.Fatal(err)
	}
	if left == different {
		t.Fatal("different exact key scope produced the same digest")
	}
}

func TestRecoveryGrantValidationFailsClosedForEveryBoundFieldAndReplay(t *testing.T) {
	signer, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	targets := []telemetryauth.Target{{DeviceID: deviceID, Keys: []string{"zone.temperature"}}}
	digest, err := telemetryauth.ScopeDigest(telemetryauth.ActionRecoveryUse, actingOrganizationID, targets)
	if err != nil {
		t.Fatal(err)
	}
	claims := telemetryauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "telemetry-runtime-service",
		PrincipalID: "018f1e00-2000-7000-8000-000000000002", SubjectIssuer: "https://identity.example.test/oidc", Subject: "delegated",
		ActingOrganizationID: actingOrganizationID, ActorChain: []telemetryauth.Actor{{Service: "platform-gateway", SPIFFEID: "spiffe://hvac.local/platform-gateway"}},
		Action: telemetryauth.ActionRecoveryUse, ScopeDigest: digest, TargetCount: 1, KeyCount: 1, PolicyRevision: "telemetry-access:7",
		SessionID: "session-1", ParentTokenID: "parent-1", RequestID: "request-1", TraceID: "0123456789abcdef0123456789abcdef", Route: "/api/v1/telemetry/subscriptions:bootstrap",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(20 * time.Second).Unix(), TokenID: "grant-1",
	}
	token, err := telemetryauth.SignGrant(signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := telemetryauth.VerifyGrant(&signer.PublicKey, token)
	if err != nil {
		t.Fatal(err)
	}
	base := telemetryauth.GrantValidation{
		Now: now, Issuer: claims.Issuer, Presenter: claims.Presenter, Audience: claims.Audience,
		PrincipalID: claims.PrincipalID, SessionID: claims.SessionID, Action: claims.Action,
		ActingOrganizationID: actingOrganizationID, Targets: targets,
		UseChecker: func(telemetryauth.GrantClaims) (telemetryauth.GrantUseStatus, error) {
			return telemetryauth.GrantUseStatus{CurrentPolicyRevision: claims.PolicyRevision}, nil
		},
	}
	if err := telemetryauth.ValidateGrant(verified, base); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}

	tests := []struct {
		name       string
		validation telemetryauth.GrantValidation
		contains   string
	}{
		{name: "audience", validation: with(base, func(value *telemetryauth.GrantValidation) { value.Audience = "wrong" }), contains: "audience"},
		{name: "presenter", validation: with(base, func(value *telemetryauth.GrantValidation) { value.Presenter = "wrong" }), contains: "presenter"},
		{name: "cross-principal-cursor", validation: with(base, func(value *telemetryauth.GrantValidation) { value.PrincipalID = "018f1e00-2000-7000-8000-000000000003" }), contains: "principal"},
		{name: "session", validation: with(base, func(value *telemetryauth.GrantValidation) { value.SessionID = "session-2" }), contains: "session"},
		{name: "action", validation: with(base, func(value *telemetryauth.GrantValidation) { value.Action = telemetryauth.ActionSubscribe }), contains: "action"},
		{name: "expired", validation: with(base, func(value *telemetryauth.GrantValidation) { value.Now = now.Add(21 * time.Second) }), contains: "expired"},
		{name: "cross-device-key-cursor", validation: with(base, func(value *telemetryauth.GrantValidation) {
			value.Targets = []telemetryauth.Target{{DeviceID: siblingDeviceID, Keys: []string{"fan.speed"}}}
		}), contains: "scope"},
		{name: "policy", validation: with(base, func(value *telemetryauth.GrantValidation) {
			value.UseChecker = func(telemetryauth.GrantClaims) (telemetryauth.GrantUseStatus, error) {
				return telemetryauth.GrantUseStatus{CurrentPolicyRevision: "telemetry-access:8"}, nil
			}
		}), contains: "policy"},
		{name: "revoked", validation: with(base, func(value *telemetryauth.GrantValidation) {
			value.UseChecker = func(telemetryauth.GrantClaims) (telemetryauth.GrantUseStatus, error) {
				return telemetryauth.GrantUseStatus{CurrentPolicyRevision: claims.PolicyRevision, Revoked: true}, nil
			}
		}), contains: "revoked"},
		{name: "replayed", validation: with(base, func(value *telemetryauth.GrantValidation) {
			value.UseChecker = func(telemetryauth.GrantClaims) (telemetryauth.GrantUseStatus, error) {
				return telemetryauth.GrantUseStatus{CurrentPolicyRevision: claims.PolicyRevision, Replayed: true}, nil
			}
		}), contains: "replayed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := telemetryauth.ValidateGrant(verified, test.validation)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", err, test.contains)
			}
		})
	}
	if _, err := telemetryauth.VerifyGrant(&signer.PublicKey, "malformed"); err == nil {
		t.Fatal("malformed grant was accepted")
	}
}

func with(value telemetryauth.GrantValidation, mutate func(*telemetryauth.GrantValidation)) telemetryauth.GrantValidation {
	mutate(&value)
	return value
}
