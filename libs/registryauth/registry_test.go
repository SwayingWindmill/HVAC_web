package registryauth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	ownerA            = "018f1e00-0000-7000-8000-000000000001"
	ownerB            = "018f1e00-0000-7000-8000-000000000002"
	ownerASite1       = "018f1e00-1000-7000-8000-000000000001"
	ownerASite2       = "018f1e00-1000-7000-8000-000000000002"
	fixtureSession    = "fixture-session"
	fixtureParent     = "fixture-parent"
	fixtureIdentifier = "fixture-identifier"
)

func TestRegistryGrantIsBoundToCorePolicyRevocationAndScope(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := registryauth.SignGrant(signer, validClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := registryauth.VerifyGrant(&signer.PublicKey, grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryauth.ValidateGrant(claims, validValidation(now)); err != nil {
		t.Fatalf("valid registry grant rejected: %v", err)
	}
	if !registryauth.ScopeAllows(claims, ownerA, ownerASite1) {
		t.Fatal("allowed owner Site was rejected")
	}
	if registryauth.ScopeAllows(claims, ownerA, ownerASite2) {
		t.Fatal("explicitly denied sibling Site was allowed")
	}
	if registryauth.ScopeAllows(claims, ownerB, "") {
		t.Fatal("ungranted Organization was allowed")
	}
}

func TestRegistryGrantFailsClosedForBoundaryAndFreshnessChanges(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		mutate     func(*registryauth.GrantClaims)
		validation func(*registryauth.GrantValidation)
	}{
		{name: "wrong issuer", mutate: func(claims *registryauth.GrantClaims) { claims.Issuer = "spiffe://hvac.local/other-iam" }},
		{name: "wrong presenter", mutate: func(claims *registryauth.GrantClaims) { claims.Presenter = "spiffe://hvac.local/other-gateway" }},
		{name: "wrong audience", mutate: func(claims *registryauth.GrantClaims) { claims.Audience = "audit-ledger-service" }},
		{name: "transitive", mutate: func(claims *registryauth.GrantClaims) { claims.Transitive = true }},
		{name: "expired", mutate: func(claims *registryauth.GrantClaims) { claims.ExpiresAt = now.Add(-time.Second).Unix() }},
		{name: "too long lived", mutate: func(claims *registryauth.GrantClaims) { claims.ExpiresAt = claims.IssuedAt + 31 }},
		{name: "oversized token id", mutate: func(claims *registryauth.GrantClaims) {
			claims.TokenID = strings.Repeat("x", registryauth.MaximumGrantTokenIDSize+1)
		}},
		{name: "deny reason", mutate: func(claims *registryauth.GrantClaims) { claims.DecisionReason = registryauth.ReasonDenyExplicit }},
		{name: "wrong action", validation: func(validation *registryauth.GrantValidation) { validation.Action = registryauth.ActionDeviceRead }},
		{name: "stale policy", validation: func(validation *registryauth.GrantValidation) { validation.CurrentPolicyRevision = "s1-policy-v2" }},
		{name: "revoked", validation: func(validation *registryauth.GrantValidation) {
			validation.IsRevoked = func(string) (bool, error) { return true, nil }
		}},
		{name: "revocation unavailable", validation: func(validation *registryauth.GrantValidation) {
			validation.IsRevoked = func(string) (bool, error) { return false, errRevocationUnavailable{} }
		}},
		{name: "revocation omitted", validation: func(validation *registryauth.GrantValidation) { validation.IsRevoked = nil }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			claims := validClaims(now)
			if testCase.mutate != nil {
				testCase.mutate(&claims)
			}
			validation := validValidation(now)
			if testCase.validation != nil {
				testCase.validation(&validation)
			}
			if err := registryauth.ValidateGrant(claims, validation); err == nil {
				t.Fatalf("invalid registry grant was accepted: %#v", claims)
			}
		})
	}
}

func TestRegistryGrantRejectsOversizedEncoding(t *testing.T) {
	oversized := strings.Repeat("a", registryauth.MaximumEncodedGrantSize+1)
	if _, err := registryauth.VerifyGrant(nil, oversized); err == nil {
		t.Fatal("oversized registry grant was accepted")
	}
}

func TestGrantStatusRequestRequiresUUIDv7AndBoundedTokenIdentifier(t *testing.T) {
	valid := registryauth.GrantStatusRequest{ActingOrganizationID: ownerA, TokenID: fixtureIdentifier}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid status request was rejected: %v", err)
	}
	for name, request := range map[string]registryauth.GrantStatusRequest{
		"missing organization": {TokenID: fixtureIdentifier},
		"invalid organization": {ActingOrganizationID: "not-an-organization", TokenID: fixtureIdentifier},
		"missing identifier":   {ActingOrganizationID: ownerA},
		"oversized identifier": {ActingOrganizationID: ownerA, TokenID: strings.Repeat("x", registryauth.MaximumGrantTokenIDSize+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil {
				t.Fatal("invalid status request was accepted")
			}
		})
	}
}

func TestDecisionRequestRequiresConcreteActionAndActingOrganization(t *testing.T) {
	cases := []registryauth.DecisionRequest{
		{},
		{ActingOrganizationID: "not-an-organization", Action: registryauth.ActionSiteRead},
		{ActingOrganizationID: "550e8400-e29b-41d4-a716-446655440000", Action: registryauth.ActionSiteRead},
		{ActingOrganizationID: "018f1e00-0000-7000-0000-000000000001", Action: registryauth.ActionSiteRead},
		{ActingOrganizationID: ownerA},
		{ActingOrganizationID: ownerA, Action: registryauth.ActionRegistryRead},
		{ActingOrganizationID: ownerA, Action: registryauth.Action("registry.delete")},
	}
	for _, request := range cases {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid request was accepted: %#v", request)
		}
	}
	if err := (registryauth.DecisionRequest{ActingOrganizationID: ownerA, Action: registryauth.ActionSiteRead}).Validate(); err != nil {
		t.Fatalf("valid request was rejected: %v", err)
	}
}

type errRevocationUnavailable struct{}

func (errRevocationUnavailable) Error() string { return "revocation unavailable" }

func validClaims(now time.Time) registryauth.GrantClaims {
	return registryauth.GrantClaims{
		Issuer:                 "spiffe://hvac.local/iam-service",
		Presenter:              "spiffe://hvac.local/platform-gateway",
		Audience:               "platform-core-service",
		PrincipalID:            "018f1e00-2000-7000-8000-000000000001",
		SubjectIssuer:          "https://issuer.example.test",
		Subject:                "fixture-user",
		ActingOrganizationID:   ownerA,
		AllowedOrganizationIDs: []string{ownerA},
		DeniedSiteIDs:          []string{ownerASite2},
		Actions:                []registryauth.Action{registryauth.ActionSiteRead},
		PolicyRevision:         "s1-policy-v1",
		DecisionReason:         registryauth.ReasonAllowOrganizationRole,
		SessionID:              fixtureSession,
		ParentTokenID:          fixtureParent,
		IssuedAt:               now.Unix(),
		ExpiresAt:              now.Add(30 * time.Second).Unix(),
		TokenID:                fixtureIdentifier,
		Transitive:             false,
	}
}

func validValidation(now time.Time) registryauth.GrantValidation {
	return registryauth.GrantValidation{
		Now:                   now,
		Issuer:                "spiffe://hvac.local/iam-service",
		Presenter:             "spiffe://hvac.local/platform-gateway",
		Audience:              "platform-core-service",
		Action:                registryauth.ActionSiteRead,
		CurrentPolicyRevision: "s1-policy-v1",
		IsRevoked:             func(string) (bool, error) { return false, nil },
	}
}
