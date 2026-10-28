package iam_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const (
	postgresFixtureIssuer        = "https://identity.example.test/oidc"
	postgresTenantAID            = "018f1d00-0000-7000-8000-000000000001"
	postgresTenantBID            = "018f1d00-0000-7000-8000-000000000002"
	postgresOwnerAPrincipalID    = "018f1e00-2000-7000-8000-000000000001"
	postgresDelegatedPrincipalID = "018f1e00-2000-7000-8000-000000000002"
	postgresOwnerASite1ID        = "018f1e00-1000-7000-8000-000000000001"
	postgresSite1DeviceID        = "018f1e00-4000-7000-8000-000000000001"
	// The S1 runner seeds Owner A as site-operator on Site 1 with this binding.
	postgresOwnerASiteOperatorBindingID = "018f1e00-2200-7000-8000-000000000011"
)

func openPostgresAuthorizationStore(t *testing.T, ctx context.Context) *iam.PostgresAuthorizationStore {
	t.Helper()
	store, err := iam.OpenPostgresAuthorizationStore(ctx, requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func assignmentSummary(assignments []iam.RoleAssignment) string {
	summary := make([]string, len(assignments))
	for index, assignment := range assignments {
		summary[index] = assignment.RoleKey + "@" + assignment.SiteID
	}
	return strings.Join(summary, ",")
}

func TestPostgresAuthorizationStoreLoadsImmutableIdentityAndRoleAssignments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)

	owner, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", TenantID: postgresTenantAID})
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Found || owner.Principal.ID != postgresOwnerAPrincipalID || owner.PolicyRevision != "registry-read:1/iam:1" {
		t.Fatalf("unexpected Owner A facts: %#v", owner)
	}
	if len(owner.Memberships) != 1 || owner.Memberships[0].TenantID != postgresTenantAID || len(owner.TenantSiteIDs) != 2 {
		t.Fatalf("unexpected Owner A memberships or Sites: %#v", owner)
	}
	if got := assignmentSummary(owner.Assignments); got != "registry-reader@,site-operator@"+postgresOwnerASite1ID {
		t.Fatalf("unexpected Owner A assignments: %s", got)
	}

	delegated, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "delegated", TenantID: postgresTenantAID})
	if err != nil {
		t.Fatal(err)
	}
	if !delegated.Found || delegated.Principal.ID != postgresDelegatedPrincipalID || assignmentSummary(delegated.Assignments) != "registry-reader@"+postgresOwnerASite1ID {
		t.Fatalf("unexpected delegated facts: %#v", delegated)
	}

	wrongIssuer, err := store.LookupRegistryAuthorization(ctx, iam.AuthorizationLookup{SubjectIssuer: "https://other-issuer.example.test/oidc", Subject: "owner-a", TenantID: postgresTenantAID})
	if err != nil {
		t.Fatal(err)
	}
	if wrongIssuer.Found || wrongIssuer.Principal.ID != "" || wrongIssuer.PolicyRevision != "registry-read:1/iam:1" {
		t.Fatalf("issuer was not part of the immutable identity key: %#v", wrongIssuer)
	}
}

func TestPostgresRegistryGrantStatusIsPolicyCurrentAndTenantScoped(t *testing.T) {
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
INSERT INTO iam.registry_grant_revocations (token_id, tenant_id, revoked_at, expires_at, reason_code)
VALUES ($1, $2::uuid, now(), now() + interval '5 minutes', 'TEST_REVOCATION')
ON CONFLICT (token_id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id,
    revoked_at = EXCLUDED.revoked_at,
    expires_at = EXCLUDED.expires_at,
    reason_code = EXCLUDED.reason_code
`, identifier, postgresTenantAID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM iam.registry_grant_revocations WHERE token_id = $1`, identifier); err != nil {
			t.Error(err)
		}
	}()

	store := openPostgresAuthorizationStore(t, ctx)
	status, err := store.LookupRegistryGrantStatus(ctx, postgresTenantAID, identifier)
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentPolicyRevision != "registry-read:1/iam:1" || !status.Revoked {
		t.Fatalf("status = %#v", status)
	}
	other, err := store.LookupRegistryGrantStatus(ctx, postgresTenantBID, identifier)
	if err != nil {
		t.Fatal(err)
	}
	if other.CurrentPolicyRevision != "registry-read:1/iam:1" || other.Revoked {
		t.Fatalf("cross-Tenant status = %#v", other)
	}
}

func TestPostgresTelemetryAuthorizationLoadsOnlyTheRequestedDevices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)
	facts, err := store.LookupTelemetryAuthorization(ctx, iam.TelemetryAuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "delegated", TenantID: postgresTenantAID,
		Targets: []telemetryauth.Target{{DeviceID: postgresSite1DeviceID, Keys: []string{"fan.speed", "zone.temperature"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.PolicyRevision != "telemetry-access:2/iam:1" || len(facts.Assignments) != 1 || len(facts.Devices) != 1 || facts.Devices[0].SiteID != postgresOwnerASite1ID {
		t.Fatalf("Telemetry facts = %#v", facts)
	}
}

func TestPostgresAlarmAuthorizationReadsAssignmentsAndPersistsAudit(t *testing.T) {
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)
	facts, err := store.LookupAlarmAuthorization(ctx, iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", TenantID: postgresTenantAID})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.Principal.ID != postgresOwnerAPrincipalID || facts.PolicyRevision != "alarm-access:1/iam:1" || len(facts.Assignments) != 2 {
		t.Fatalf("Alarm facts = %#v", facts)
	}
	other, err := store.LookupAlarmAuthorization(ctx, iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", TenantID: postgresTenantBID})
	if err != nil {
		t.Fatal(err)
	}
	if !other.Found || other.PolicyRevision != "alarm-access:unconfigured" || len(other.Assignments) != 0 {
		t.Fatalf("cross-Tenant Alarm facts were not fail-closed: %#v", other)
	}

	requestID := "alarm-postgres-decision-1"
	if err := store.RecordAlarmDecision(ctx, iam.AlarmDecisionAudit{
		PrincipalID: postgresOwnerAPrincipalID, TenantID: postgresTenantAID,
		SiteID: postgresOwnerASite1ID, Action: alarmauth.ActionRead, Allowed: true,
		PolicyRevision: "alarm-access:1/iam:1", ReasonCode: alarmauth.ReasonAllowExactScope,
		RequestID: requestID, TraceID: "trace-alarm-postgres-1", OccurredAt: "2026-08-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM iam.alarm_authorization_decisions WHERE request_id = $1`, requestID); err != nil {
			t.Error(err)
		}
	}()
	var allowed bool
	var policyRevision, reasonCode string
	if err := admin.QueryRow(ctx, `SELECT allowed, policy_revision, reason_code FROM iam.alarm_authorization_decisions WHERE request_id = $1`, requestID).Scan(&allowed, &policyRevision, &reasonCode); err != nil {
		t.Fatal(err)
	}
	if !allowed || policyRevision != "alarm-access:1/iam:1" || reasonCode != string(alarmauth.ReasonAllowExactScope) {
		t.Fatalf("durable Alarm decision = allowed=%v policy=%q reason=%q", allowed, policyRevision, reasonCode)
	}
}

func TestPostgresWorkOrderAuthorizationReadsTargetsAndTheAssigneesAccess(t *testing.T) {
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)
	lookup := func(assigneeID string) iam.WorkOrderAuthorizationFacts {
		t.Helper()
		facts, err := store.LookupWorkOrderAuthorization(ctx, iam.WorkOrderAuthorizationLookup{
			AuthorizationLookup: iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", TenantID: postgresTenantAID},
			AssigneePrincipalID: assigneeID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	facts := lookup(postgresDelegatedPrincipalID)
	if !facts.Found || facts.Principal.ID != postgresOwnerAPrincipalID || facts.PolicyRevision != "work-order-access:1/iam:1" || len(facts.Assignments) != 2 || len(facts.Targets) != 1 {
		t.Fatalf("Work Order facts = %#v", facts)
	}
	if !facts.Assignee.Found || facts.Assignee.Principal.ID != postgresDelegatedPrincipalID || len(facts.Assignee.Memberships) != 1 || len(facts.Assignee.Assignments) != 1 {
		t.Fatalf("assignee facts = %#v", facts.Assignee)
	}
	if unknown := lookup("018f1e00-2000-7000-8000-000000000099"); unknown.Assignee.Found {
		t.Fatalf("an unknown assignee was found: %#v", unknown.Assignee)
	}

	requestID := "work-order-postgres-decision-1"
	if err := store.RecordWorkOrderDecision(ctx, iam.WorkOrderDecisionAudit{
		PrincipalID: postgresOwnerAPrincipalID, TenantID: postgresTenantAID,
		SiteID: postgresOwnerASite1ID, WorkOrderID: "01910000-1000-7000-8000-000000000001",
		Action: workorderauth.ActionRead, Allowed: true,
		PolicyRevision: "work-order-access:1/iam:1", ReasonCode: workorderauth.ReasonAllowExactScope,
		RequestID: requestID, TraceID: "trace-work-order-postgres-1", OccurredAt: "2026-08-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM iam.work_order_authorization_decisions WHERE request_id = $1`, requestID); err != nil {
			t.Error(err)
		}
	}()
	var allowed bool
	var policyRevision string
	if err := admin.QueryRow(ctx, `SELECT allowed, policy_revision FROM iam.work_order_authorization_decisions WHERE request_id = $1`, requestID).Scan(&allowed, &policyRevision); err != nil {
		t.Fatal(err)
	}
	if !allowed || policyRevision != "work-order-access:1/iam:1" {
		t.Fatalf("durable Work Order decision = allowed=%v policy=%q", allowed, policyRevision)
	}
}

func TestPostgresCommandAuthorizationResolvesTheDevicesSiteAndCurrentAssignments(t *testing.T) {
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)
	commands := store.CommandAuthorization("command-policy-7", 3)
	lookup := iam.CommandAuthorizationLookup{
		AuthorizationLookup: iam.AuthorizationLookup{SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", TenantID: postgresTenantAID},
		DeviceID:            postgresSite1DeviceID,
	}
	facts, err := commands.LookupCommandAuthorization(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.Principal.ID != postgresOwnerAPrincipalID || facts.DeviceSiteID != postgresOwnerASite1ID || len(facts.Assignments) != 2 || facts.PolicyRevision != "command-policy-7" || facts.EmergencyRevocationRevision != 3 {
		t.Fatalf("unexpected Command authority: %#v", facts)
	}
	crossTenant := lookup
	crossTenant.TenantID = postgresTenantBID
	if facts, err := commands.LookupCommandAuthorization(ctx, crossTenant); err != nil || facts.DeviceSiteID != "" || len(facts.Assignments) != 0 {
		t.Fatalf("Command facts leaked across Tenants: %#v err=%v", facts, err)
	}

	// Revoking the assignment takes effect on the next lookup; nothing is cached.
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	const setStatus = `UPDATE iam.role_bindings SET status = $2, revision = revision + 1, updated_at = now() WHERE id = $1::uuid`
	if _, err := admin.Exec(ctx, setStatus, postgresOwnerASiteOperatorBindingID, "REVOKED"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), setStatus, postgresOwnerASiteOperatorBindingID, "ACTIVE"); err != nil {
			t.Error(err)
		}
	}()
	facts, err = commands.LookupCommandAuthorization(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assignmentSummary(facts.Assignments), "site-operator") {
		t.Fatalf("site-operator assignment missing: %#v", facts.Assignments)
	}
	for _, assignment := range facts.Assignments {
		if assignment.RoleKey == "site-operator" && assignment.Status != iam.FactStatusRevoked {
			t.Fatalf("revoked assignment was cached: %#v", facts.Assignments)
		}
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
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM core_registry.assets`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime accessed Core Asset data: %v", err)
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
