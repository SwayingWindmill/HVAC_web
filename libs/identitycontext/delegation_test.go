package identitycontext_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestDelegationGrantIsSignatureAndBoundaryChecked(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now)
	grant, err := identitycontext.SignDelegation(signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := identitycontext.VerifyDelegation(&signer.PublicKey, grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := identitycontext.ValidateDelegation(verified, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", "session:session-01"); err != nil {
		t.Fatalf("valid delegation rejected: %v", err)
	}

	otherSigner, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identitycontext.VerifyDelegation(&otherSigner.PublicKey, grant); err == nil {
		t.Fatal("delegation signed by another workload was accepted")
	}
}

func TestDelegationGrantCannotExpandOrForward(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*identitycontext.DelegationClaims)
	}{
		{name: "audience expansion", mutate: func(claims *identitycontext.DelegationClaims) { claims.Audience = "audit-service" }},
		{name: "action expansion", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Actions = []string{"principal:read", "session:revoke"}
		}},
		{name: "scope expansion", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.Scopes = []string{"session:session-01", "organization:*"}
		}},
		{name: "forwarded by IAM", mutate: func(claims *identitycontext.DelegationClaims) {
			claims.ExecutingService = "spiffe://hvac.local/iam-service"
		}},
		{name: "long lived", mutate: func(claims *identitycontext.DelegationClaims) { claims.ExpiresAt = claims.IssuedAt + 61 }},
		{name: "expired", mutate: func(claims *identitycontext.DelegationClaims) { claims.ExpiresAt = now.Add(-time.Second).Unix() }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			claims := validClaims(now)
			testCase.mutate(&claims)
			if err := identitycontext.ValidateDelegation(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", "session:session-01"); err == nil {
				t.Fatalf("expanded or forwarded delegation was accepted: %#v", claims)
			}
		})
	}
}

func TestDelegationAnyScopePreservesExactResourceProjection(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	claims := validClaims(now)
	claims.Scopes = []string{"organization:org-01", "site:site-01"}
	if err := identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", []string{"organization:org-01", "site:site-01"}); err != nil {
		t.Fatalf("multi-scope delegation rejected: %v", err)
	}
	if err := identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", []string{"site:site-01"}); err == nil {
		t.Fatal("delegation with an extra unapproved scope was accepted")
	}
	if err := identitycontext.ValidateDelegation(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", "site:site-01"); err == nil {
		t.Fatal("single-scope validator accepted a multi-scope delegation")
	}
	claims.Scopes = []string{"site:site-01", "site:site-01"}
	if err := identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "principal:read", []string{"site:site-01"}); err == nil {
		t.Fatal("duplicate delegation scope was accepted")
	}
}

func validClaims(now time.Time) identitycontext.DelegationClaims {
	return identitycontext.DelegationClaims{
		Issuer: "spiffe://hvac.local/platform-gateway", Subject: "fixture-user", SubjectIssuer: "https://issuer.example.test",
		DisplayName: "Fixture User", Email: "fixture@example.test", Roles: []string{"operator"},
		ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "iam-service", ActingOrganizationID: "org-01",
		Actions: []string{"principal:read"}, Scopes: []string{"session:session-01"}, PolicyRevision: "policy-v1",
		SessionID: "session-01", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: "grant-01",
	}
}
