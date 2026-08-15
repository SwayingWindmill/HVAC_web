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
	tenantA           = "018f1d00-0000-7000-8000-000000000001"
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
	if !registryauth.ScopeAllows(claims, ownerASite1) {
		t.Fatal("allowed Site was rejected")
	}
	if registryauth.ScopeAllows(claims, ownerASite2) {
		t.Fatal("explicitly denied sibling Site was allowed")
	}
	if registryauth.ScopeAllows(claims, "") {
		t.Fatal("empty Site scope was allowed")
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
	valid := registryauth.GrantStatusRequest{TenantID: tenantA, TokenID: fixtureIdentifier}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid status request was rejected: %v", err)
	}
	for name, request := range map[string]registryauth.GrantStatusRequest{
		"missing tenant":       {TokenID: fixtureIdentifier},
		"invalid tenant":       {TenantID: "not-a-tenant", TokenID: fixtureIdentifier},
		"missing identifier":   {TenantID: tenantA},
		"oversized identifier": {TenantID: tenantA, TokenID: strings.Repeat("x", registryauth.MaximumGrantTokenIDSize+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil {
				t.Fatal("invalid status request was accepted")
			}
		})
	}
}

func TestDecisionRequestRequiresConcreteActionAndTenant(t *testing.T) {
	cases := []registryauth.DecisionRequest{
		{},
		{TenantID: "not-a-tenant", Action: registryauth.ActionSiteRead},
		{TenantID: "550e8400-e29b-41d4-a716-446655440000", Action: registryauth.ActionSiteRead},
		{TenantID: "018f1d00-0000-7000-0000-000000000001", Action: registryauth.ActionSiteRead},
		{TenantID: tenantA},
		{TenantID: tenantA, Action: registryauth.ActionRegistryRead},
		{TenantID: tenantA, Action: registryauth.Action("registry.delete")},
		{TenantID: tenantA, Action: registryauth.ActionSiteRead, GrantPresenter: "not-a-spiffe-id"},
		{TenantID: tenantA, Action: registryauth.ActionSiteRead, GrantPresenter: "spiffe://" + strings.Repeat("x", 513)},
	}
	for _, request := range cases {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid request was accepted: %#v", request)
		}
	}
	for _, request := range []registryauth.DecisionRequest{
		{TenantID: tenantA, Action: registryauth.ActionSiteRead},
		{
			TenantID:       tenantA,
			Action:         registryauth.ActionSiteRead,
			GrantPresenter: "spiffe://hvac.local/operations-agent-service",
		},
	} {
		if err := request.Validate(); err != nil {
			t.Fatalf("valid request was rejected: %v", err)
		}
	}
	if err := (registryauth.DecisionRequest{TenantID: tenantA, Action: registryauth.ActionDeviceBindingList}).Validate(); err != nil {
		t.Fatalf("valid DeviceBinding request was rejected: %v", err)
	}
	if !registryauth.ActionDeviceBindingList.SiteScoped() {
		t.Fatal("DeviceBinding list action must remain Site scoped")
	}
}

type errRevocationUnavailable struct{}

func (errRevocationUnavailable) Error() string { return "revocation unavailable" }

func validClaims(now time.Time) registryauth.GrantClaims {
	return registryauth.GrantClaims{
		Issuer:         "spiffe://hvac.local/iam-service",
		Presenter:      "spiffe://hvac.local/platform-gateway",
		Audience:       "platform-core-service",
		PrincipalID:    "018f1e00-2000-7000-8000-000000000001",
		SubjectIssuer:  "https://issuer.example.test",
		Subject:        "fixture-user",
		TenantID:       tenantA,
		AllowedSiteIDs: []string{ownerASite1},
		DeniedSiteIDs:  []string{ownerASite2},
		Actions:        []registryauth.Action{registryauth.ActionSiteRead},
		PolicyRevision: "s1-policy-v1",
		DecisionReason: registryauth.ReasonAllowTenantRole,
		SessionID:      fixtureSession,
		ParentTokenID:  fixtureParent,
		IssuedAt:       now.Unix(),
		ExpiresAt:      now.Add(30 * time.Second).Unix(),
		TokenID:        fixtureIdentifier,
		Transitive:     false,
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
