package iam_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const (
	adminTestPrincipalID = "018f1e00-2000-7000-8000-000000000071"
	viewerRoleTemplateID = "01a00260-0000-7000-8000-000000000001"
	adminRoleTemplateID  = "01a00260-0000-7000-8000-000000000004"
	// A Tenant of its own, so the revision bumps here leave the other tests' exact revisions alone.
	adminTestTenantID = "018f1d00-0000-7000-8000-000000000071"
	adminTestSiteID   = "018f1e00-1000-7000-8000-000000000071"
)

func TestPostgresUserAdministrationKeepsAdministratorsInControl(t *testing.T) {
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	storeURL := requiredIAMPostgresEnv(t, "S1_IAM_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A Tenant with one Site, and a Tenant-wide administrator to act as.
	setup, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close()
	if _, err := setup.Exec(ctx, `
INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
VALUES ($1::uuid, 'tenant-admin-it', 'Tenant Admin IT', 'Asia/Shanghai', 'CNY', 'CN', 'ACTIVE', 1, now(), now())
`, adminTestTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `INSERT INTO iam.authorization_revisions (tenant_id, revision, updated_at) VALUES ($1::uuid, 1, now())`, adminTestTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
INSERT INTO core_registry.sites (id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, 'admin-it-site', 'Admin IT Site', 'Asia/Shanghai', 'ACTIVE', 1, now(), now())
`, adminTestSiteID, adminTestTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at)
VALUES ($1::uuid, $2, 'admin-it', 'Admin IT', 'admin-it@example.test', 'ACTIVE', 1, now(), now());
`, adminTestPrincipalID, postgresFixtureIssuer); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
INSERT INTO iam.tenant_memberships (id, tenant_id, principal_id, status, valid_from, revision, created_at, updated_at)
VALUES ('018f1e00-2100-7000-8000-000000000071', $1::uuid, $2::uuid, 'ACTIVE', now() - interval '1 hour', 1, now(), now());
`, adminTestTenantID, adminTestPrincipalID); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
INSERT INTO iam.role_bindings (id, tenant_id, principal_id, role_template_id, status, valid_from, revision, created_at, updated_at)
VALUES ('018f1e00-2200-7000-8000-000000000071', $1::uuid, $2::uuid, $3::uuid, 'ACTIVE', now() - interval '1 hour', 1, now(), now());
`, adminTestTenantID, adminTestPrincipalID, adminRoleTemplateID); err != nil {
		t.Fatal(err)
	}

	store, err := iam.OpenPostgresAdminStore(ctx, storeURL, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	actor := iam.AdminActor{SubjectIssuer: postgresFixtureIssuer, Subject: "admin-it", TenantID: adminTestTenantID, CorrelationID: "it", TraceID: "it", OccurredAt: time.Now().UTC()}

	directory, err := store.ListMembers(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	roleKeys := []string{}
	for _, role := range directory.Roles {
		roleKeys = append(roleKeys, role.RoleKey)
		if (role.RoleKey == "admin") != role.TenantScoped {
			t.Fatalf("role %s tenantScoped=%v", role.RoleKey, role.TenantScoped)
		}
	}
	if len(roleKeys) != 4 || roleKeys[0] != "viewer" || roleKeys[3] != "admin" {
		t.Fatalf("built-in Roles = %v", roleKeys)
	}

	created, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "member.create", SubjectIssuer: postgresFixtureIssuer, Subject: "new-operator",
		DisplayName: "New Operator", Email: "new.operator@example.test",
		Assignments: []iam.AdminAssignment{{RoleTemplateID: viewerRoleTemplateID, SiteID: adminTestSiteID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory, err = store.ListMembers(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	var member *iam.AdminMember
	for index := range directory.Members {
		if directory.Members[index].PrincipalID == created.ResourceID {
			member = &directory.Members[index]
		}
	}
	if member == nil || member.MembershipStatus != "ACTIVE" || len(member.Assignments) != 1 || member.Assignments[0].RoleKey != "viewer" || member.Assignments[0].SiteID != adminTestSiteID {
		t.Fatalf("created member = %#v", member)
	}
	if _, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "member.create", SubjectIssuer: postgresFixtureIssuer, Subject: "new-operator",
		DisplayName: "New Operator", Email: "new.operator@example.test", Assignments: []iam.AdminAssignment{{RoleTemplateID: viewerRoleTemplateID}},
	}); !errors.Is(err, iam.ErrAdminRevisionConflict) {
		t.Fatalf("a second membership for the same identity = %v", err)
	}

	if _, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "role-binding.upsert", PrincipalID: created.ResourceID, RoleTemplateID: adminRoleTemplateID, SiteID: adminTestSiteID, Status: "ACTIVE",
	}); err == nil {
		t.Fatal("the admin Role was assigned to a single Site")
	}
	if _, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "role-template.upsert", RoleKey: "viewer", DisplayName: "Changed", Capabilities: []string{"registry.read"}, Status: "ACTIVE",
	}); err == nil {
		t.Fatal("a built-in Role was changed")
	}

	if _, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "membership.upsert", PrincipalID: adminTestPrincipalID, Status: "SUSPENDED", ExpectedRevision: 1,
	}); !errors.Is(err, iam.ErrAdminSelfLockout) {
		t.Fatalf("suspending oneself = %v", err)
	}
	if _, err := store.ApplyMutation(ctx, actor, iam.AdminMutationRequest{
		Operation: "role-binding.upsert", PrincipalID: adminTestPrincipalID, RoleTemplateID: adminRoleTemplateID, Status: "REVOKED", ExpectedRevision: 1,
	}); !errors.Is(err, iam.ErrAdminSelfLockout) {
		t.Fatalf("revoking one's own admin Role = %v", err)
	}
}
