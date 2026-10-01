package iam

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type fixedCapabilityResolver struct {
	authorization identitycontext.EffectiveAuthorization
}

func (resolver fixedCapabilityResolver) ResolvePrincipalCapabilities(context.Context, PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	return resolver.authorization, nil
}

func TestManagementCapabilitiesComeFromRoleTemplateGrantAndExplicitDenyWins(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	facts := AuthorizationFacts{
		Principal:   PrincipalRecord{ID: "principal-1", SubjectIssuer: "issuer", Subject: "subject", Status: FactStatusActive},
		Memberships: []TenantMembership{{TenantID: "tenant-a", Status: FactStatusActive}},
		RoleBindings: []RoleBinding{{
			TenantID:     "tenant-a",
			RoleKey:      "arbitrary-role-name",
			Capabilities: []identitycontext.Capability{identitycontext.CapabilityIAMAdmin},
			Effect:       BindingEffectAllow,
			Status:       FactStatusActive,
		}},
	}
	store := newStaticAuthorizationStore("policy:1", []AuthorizationFacts{facts})
	resolver := withManagementCapabilities(fixedCapabilityResolver{authorization: identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       "policy:1",
		Capabilities:         []identitycontext.Capability{},
	}}, store, func() time.Time { return now })

	authorization, err := resolver.ResolvePrincipalCapabilities(context.Background(), PrincipalCapabilityLookup{SubjectIssuer: "issuer", Subject: "subject", TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.Has(identitycontext.CapabilityIAMAdmin) {
		t.Fatal("Role Template capability was not granted")
	}

	facts.ExplicitDenies = []ExplicitDeny{{
		TenantID:     "tenant-a",
		Capabilities: []identitycontext.Capability{identitycontext.CapabilityIAMAdmin},
		Status:       FactStatusActive,
	}}
	store = newStaticAuthorizationStore("policy:2", []AuthorizationFacts{facts})
	resolver = withManagementCapabilities(fixedCapabilityResolver{authorization: identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       "policy:2",
		Capabilities:         []identitycontext.Capability{},
	}}, store, func() time.Time { return now })
	authorization, err = resolver.ResolvePrincipalCapabilities(context.Background(), PrincipalCapabilityLookup{SubjectIssuer: "issuer", Subject: "subject", TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Has(identitycontext.CapabilityIAMAdmin) {
		t.Fatal("explicit deny did not override the Role Template capability")
	}
}
