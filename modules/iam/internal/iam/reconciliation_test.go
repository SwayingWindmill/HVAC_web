package iam

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPrepareReconciliationRequestProducesStableHashForEquivalentOrdering(t *testing.T) {
	validFrom := time.Date(2026, 7, 22, 3, 0, 0, 0, time.FixedZone("offset", 8*60*60))
	base := ReconciliationRequest{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SourceSystem:  " identity ",
		SourceKey:     " user-123 ",
		SourceVersion: 3,
		Principal: ReconciledPrincipal{
			ID:            "018f1e00-2000-7000-8000-000000000099",
			SubjectIssuer: " https://identity.example.test/oidc ",
			Subject:       " user-123 ",
			DisplayName:   " Example User ",
			Email:         " user@example.test ",
			Status:        PrincipalStatusActive,
		},
		Memberships: []ReconciledMembership{
			{TenantID: "018f1d00-0000-7000-8000-000000000001", Status: FactStatusActive, ValidFrom: validFrom},
		},
		RoleBindings: []ReconciledRoleBinding{
			{
				TenantID:  "018f1d00-0000-7000-8000-000000000001",
				RoleKey:   " registry-reader ",
				Actions:   []registryauth.Action{registryauth.ActionSiteRead, registryauth.ActionAssetRead, registryauth.ActionSiteRead},
				Effect:    BindingEffectAllow,
				ValidFrom: validFrom,
			},
		},
	}

	first, firstHash, err := prepareReconciliationRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	if base.Memberships[0].TenantID != base.TenantID || len(base.RoleBindings[0].Actions) != 3 {
		t.Fatalf("request normalization mutated caller-owned slices: %#v", base)
	}
	base.RoleBindings[0].Actions[0], base.RoleBindings[0].Actions[1] = base.RoleBindings[0].Actions[1], base.RoleBindings[0].Actions[0]
	second, secondHash, err := prepareReconciliationRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent desired state produced different hashes: %s != %s", firstHash, secondHash)
	}
	if first.SourceSystem != "identity" || first.Principal.SubjectIssuer != "https://identity.example.test/oidc" {
		t.Fatalf("input was not normalized: %#v", first)
	}
	if len(first.RoleBindings[0].Actions) != 2 || len(second.RoleBindings[0].Actions) != 2 {
		t.Fatalf("duplicate actions were not removed: %#v %#v", first.RoleBindings, second.RoleBindings)
	}
	if first.Memberships[0].ValidFrom.Location() != time.UTC {
		t.Fatalf("timestamps were not normalized to UTC: %v", first.Memberships[0].ValidFrom)
	}
}

func TestPrepareReconciliationRequestRejectsMutableIdentityAsIdentifier(t *testing.T) {
	_, _, err := prepareReconciliationRequest(ReconciliationRequest{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SourceSystem:  "identity",
		SourceKey:     "user@example.test",
		SourceVersion: 1,
		Principal: ReconciledPrincipal{
			ID:            "018f1e00-2000-7000-8000-000000000099",
			SubjectIssuer: "https://identity.example.test/oidc",
			Subject:       "",
			DisplayName:   "Example User",
			Email:         "user@example.test",
			Status:        PrincipalStatusActive,
		},
	})
	if err == nil {
		t.Fatal("reconciliation accepted a principal without immutable subject")
	}
}

func TestPrepareReconciliationRequestRejectsDuplicateExplicitDeny(t *testing.T) {
	validFrom := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	deny := ReconciledExplicitDeny{
		TenantID:   "018f1d00-0000-7000-8000-000000000001",
		SiteID:     "018f1e00-1000-7000-8000-000000000001",
		Action:     registryauth.ActionSiteRead,
		ReasonCode: "blocked",
		ValidFrom:  validFrom,
	}
	_, _, err := prepareReconciliationRequest(ReconciliationRequest{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SourceSystem:  "identity",
		SourceKey:     "user-123",
		SourceVersion: 1,
		Principal: ReconciledPrincipal{
			ID:            "018f1e00-2000-7000-8000-000000000099",
			SubjectIssuer: "https://identity.example.test/oidc",
			Subject:       "user-123",
			DisplayName:   "Example User",
			Email:         "user@example.test",
			Status:        PrincipalStatusActive,
		},
		ExplicitDenies: []ReconciledExplicitDeny{deny, deny},
	})
	if err == nil {
		t.Fatal("duplicate explicit deny was accepted")
	}
}

func TestPrepareReconciliationRequestNormalizesNilAndEmptyCollections(t *testing.T) {
	request := ReconciliationRequest{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SourceSystem:  "identity",
		SourceKey:     "user-123",
		SourceVersion: 1,
		Principal: ReconciledPrincipal{
			ID:            "018f1e00-2000-7000-8000-000000000099",
			SubjectIssuer: "https://identity.example.test/oidc",
			Subject:       "user-123",
			DisplayName:   "Example User",
			Email:         "user@example.test",
			Status:        PrincipalStatusActive,
		},
	}
	_, nilHash, err := prepareReconciliationRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Memberships = []ReconciledMembership{}
	request.RoleBindings = []ReconciledRoleBinding{}
	request.SiteBindings = []ReconciledSiteBinding{}
	request.ExplicitDenies = []ReconciledExplicitDeny{}
	_, emptyHash, err := prepareReconciliationRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if nilHash != emptyHash {
		t.Fatalf("nil and empty desired-state collections produced different hashes: %s != %s", nilHash, emptyHash)
	}
}

func TestNewUUIDv7ProducesValidIdentifier(t *testing.T) {
	value, err := newUUIDv7(time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !isUUIDv7(value) {
		t.Fatalf("generated identifier is not UUIDv7: %s", value)
	}
}
