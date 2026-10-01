package iam

import (
	"context"
	"testing"
	"time"
)

func TestTenantContextsListOnlyActiveMembershipsForPrincipal(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	store := newStaticAuthorizationStore("policy:1", []AuthorizationFacts{{
		Principal: PrincipalRecord{ID: "principal-1", SubjectIssuer: "issuer", Subject: "subject", Status: FactStatusActive},
		Memberships: []TenantMembership{
			{TenantID: "tenant-b", Status: FactStatusActive},
			{TenantID: "tenant-a", Status: FactStatusActive},
			{TenantID: "tenant-suspended", Status: FactStatusSuspended},
			{TenantID: "tenant-expired", Status: FactStatusActive, ValidTo: &expired},
		},
	}}).(*staticAuthorizationStore)

	contexts, err := store.ListTenantContexts(context.Background(), "issuer", "subject", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 || contexts[0].TenantID != "tenant-a" || contexts[1].TenantID != "tenant-b" {
		t.Fatalf("contexts = %#v, want tenant-a and tenant-b only", contexts)
	}

	missing, err := store.ListTenantContexts(context.Background(), "issuer", "other-subject", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("unauthorized principal contexts = %#v, want empty", missing)
	}
}
