package iam_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const (
	postgresFixtureIssuer        = "https://identity.example.test/oidc"
	postgresOwnerAOrganizationID = "018f1e00-0000-7000-8000-000000000001"
	postgresActingOrganizationID = "018f1e00-0000-7000-8000-000000000003"
	postgresOwnerAPrincipalID    = "018f1e00-2000-7000-8000-000000000001"
	postgresDelegatedPrincipalID = "018f1e00-2000-7000-8000-000000000002"
	postgresOwnerASite1ID        = "018f1e00-1000-7000-8000-000000000001"
)

func TestPostgresAuthorizationStoreLoadsImmutableIdentityAndScopedFacts(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	owner, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer:        postgresFixtureIssuer,
		Subject:              "owner-a",
		ActingOrganizationID: postgresOwnerAOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Found || owner.Principal.ID != postgresOwnerAPrincipalID || owner.PolicyRevision != "registry-read:1" {
		t.Fatalf("unexpected Owner A facts: %#v", owner)
	}
	if len(owner.Memberships) != 1 || owner.Memberships[0].OrganizationID != postgresOwnerAOrganizationID {
		t.Fatalf("unexpected Owner A memberships: %#v", owner.Memberships)
	}
	if len(owner.RoleBindings) != 1 || owner.RoleBindings[0].Effect != iam.BindingEffectAllow {
		t.Fatalf("unexpected Owner A role bindings: %#v", owner.RoleBindings)
	}

	delegated, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer:        postgresFixtureIssuer,
		Subject:              "delegated",
		ActingOrganizationID: postgresActingOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !delegated.Found || delegated.Principal.ID != postgresDelegatedPrincipalID || len(delegated.SiteBindings) != 1 {
		t.Fatalf("unexpected delegated facts: %#v", delegated)
	}
	binding := delegated.SiteBindings[0]
	if binding.OwningOrganizationID != postgresOwnerAOrganizationID || binding.SiteID != postgresOwnerASite1ID || binding.Effect != iam.BindingEffectAllow {
		t.Fatalf("unexpected cross-Organization SiteBinding: %#v", binding)
	}

	wrongIssuer, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer:        "https://other-issuer.example.test/oidc",
		Subject:              "owner-a",
		ActingOrganizationID: postgresOwnerAOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if wrongIssuer.Found || wrongIssuer.Principal.ID != "" || wrongIssuer.PolicyRevision != "registry-read:1" {
		t.Fatalf("issuer was not part of the immutable identity key: %#v", wrongIssuer)
	}
}

func TestPostgresRegistryGrantStatusIsPolicyCurrentAndOrganizationScoped(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	identifier := "integration-id-1"
	if _, err := admin.Exec(ctx, `
INSERT INTO iam.registry_grant_revocations (token_id, acting_organization_id, revoked_at, expires_at, reason_code)
VALUES ($1, $2::uuid, now(), now() + interval '5 minutes', 'TEST_REVOCATION')
ON CONFLICT (token_id) DO UPDATE
SET acting_organization_id = EXCLUDED.acting_organization_id,
    revoked_at = EXCLUDED.revoked_at,
    expires_at = EXCLUDED.expires_at,
    reason_code = EXCLUDED.reason_code
`, identifier, postgresActingOrganizationID); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DELETE FROM iam.registry_grant_revocations WHERE token_id = $1`, identifier)

	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status, err := store.LookupRegistryGrantStatus(ctx, postgresActingOrganizationID, identifier)
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentPolicyRevision != "registry-read:1" || !status.Revoked {
		t.Fatalf("status = %#v", status)
	}
	other, err := store.LookupRegistryGrantStatus(ctx, postgresOwnerAOrganizationID, identifier)
	if err != nil {
		t.Fatal(err)
	}
	if other.CurrentPolicyRevision != "registry-read:1" || other.Revoked {
		t.Fatalf("cross-Organization status = %#v", other)
	}
}

func TestPostgresAuthorizationRuntimeIsRLSBoundAndRoleChecked(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if store, err := iam.OpenPostgresAuthorizationStore(ctx, adminURL); err == nil {
		store.Close()
		t.Fatal("IAM store accepted a database identity other than s1_iam_runtime")
	}

	pool, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM core_registry.organizations`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime accessed Core Schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE iam.principals SET display_name = display_name`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime mutated IAM facts: %v", err)
	}
}

func requiredIAMPostgresEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Skipf("%s is not configured", name)
	}
	return value
}
