package iam_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
	"github.com/quanlaihe/hvac-web/modules/iam/internal/iam"
)

const (
	postgresFixtureIssuer        = "https://identity.example.test/oidc"
	postgresTenantAID            = "018f1d00-0000-7000-8000-000000000001"
	postgresTenantBID            = "018f1d00-0000-7000-8000-000000000002"
	postgresOwnerAPrincipalID    = "018f1e00-2000-7000-8000-000000000001"
	postgresDelegatedPrincipalID = "018f1e00-2000-7000-8000-000000000002"
	postgresOwnerASite1ID        = "018f1e00-1000-7000-8000-000000000001"
	postgresSite1AssetID         = "018f1e00-3000-7000-8000-000000000001"
	postgresSite1DeviceID        = "018f1e00-4000-7000-8000-000000000001"
	// The S1 runner seeds Owner A as site-operator on Site 1 with this binding.
	postgresOwnerASiteOperatorBindingID  = "018f1e00-2200-7000-8000-000000000011"
	postgresOptimizationRecommendationID = "01990000-1960-7000-8000-000000000001"
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
		SiteID: postgresOwnerASite1ID, Action: alarmmodel.ActionRead, Allowed: true,
		PolicyRevision: "alarm-access:1/iam:1", ReasonCode: alarmmodel.ReasonAllowExactScope,
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
	if !allowed || policyRevision != "alarm-access:1/iam:1" || reasonCode != string(alarmmodel.ReasonAllowExactScope) {
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
		Action: workordermodel.ActionRead, Allowed: true,
		PolicyRevision: "work-order-access:1/iam:1", ReasonCode: workordermodel.ReasonAllowExactScope,
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
	for _, table := range []string{
		"core_registry.sites",
		"core_registry.assets",
		"core_registry.devices",
		"core_registry.device_bindings",
		"core_registry.site_control_states",
		"core_registry.asset_control_locks",
		"core_registry.site_auto_control_leases",
		"core_registry.optimization_execution_monitors",
	} {
		if _, err := pool.Exec(ctx, "SELECT count(*) FROM "+table); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
			t.Fatalf("IAM runtime accessed %s directly: %v", table, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE iam.principals SET display_name = display_name`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime mutated IAM facts: %v", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, view := range []string{
		"core_registry.sites_v1",
		"core_registry.iam_requested_devices_v1",
		"core_registry.site_control_states_v1",
		"core_registry.device_control_locks_v1",
		"core_registry.site_auto_control_leases_v1",
		"core_registry.optimization_execution_monitors_v1",
	} {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+view).Scan(&count); err != nil {
			t.Fatalf("IAM runtime failed to query %s without tenant scope: %v", view, err)
		}
		if count != 0 {
			t.Fatalf("%s returned %d rows without RLS tenant context", view, count)
		}
	}

	var configured string
	if err := tx.QueryRow(ctx, `SELECT set_config('app.tenant_id', $1, true)`, postgresTenantAID).Scan(&configured); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT set_config('app.requested_device_ids', $1, true)`, "{"+postgresSite1DeviceID+"}").Scan(&configured); err != nil {
		t.Fatal(err)
	}
	var siteCount, deviceCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM core_registry.sites_v1`).Scan(&siteCount); err != nil {
		t.Fatal(err)
	}
	if siteCount != 2 {
		t.Fatalf("sites_v1 count = %d, want 2", siteCount)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM core_registry.iam_requested_devices_v1`).Scan(&deviceCount); err != nil {
		t.Fatal(err)
	}
	if deviceCount != 1 {
		t.Fatalf("iam_requested_devices_v1 count = %d, want 1", deviceCount)
	}
}

func TestPostgresCommandWorkloadAuthorizationUsesContractViews(t *testing.T) {
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	const (
		leaseID        = "0199f100-1000-7000-8000-000000000001"
		monitorID      = "0199f100-2000-7000-8000-000000000001"
		commandIntent  = "0199f100-3000-7000-8000-000000000001"
		controlStateID = "0199f100-4000-7000-8000-000000000001"
		assetLockID    = "0199f100-5000-7000-8000-000000000001"
	)

	now := time.Now().UTC()
	if _, err := admin.Exec(ctx, `
INSERT INTO core_registry.site_auto_control_leases (
  id, tenant_id, site_id, status, armed_by, armed_at, expires_at,
  allowed_capabilities, max_setpoint_step_c, supply_temp_min_c, supply_temp_max_c,
  max_commands_per_hour, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, 'ACTIVE', $4::uuid, $5, $6,
  ARRAY['SET_CHILLED_WATER_TEMPERATURE_SETPOINT'], 1.5, 6.0, 12.0,
  3, 1, $5, $5
)
`, leaseID, postgresTenantAID, postgresOwnerASite1ID, postgresOwnerAPrincipalID, now.Add(-10*time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM core_registry.site_auto_control_leases WHERE id = $1::uuid`, leaseID)
	}()

	if _, err := admin.Exec(ctx, `
INSERT INTO core_registry.site_control_states (
  id, tenant_id, site_id, mode, reason, updated_by, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, 'MANUAL_OVERRIDE', 'test lock', $4::uuid, 1, $5, $5
)
`, controlStateID, postgresTenantAID, postgresOwnerASite1ID, postgresOwnerAPrincipalID, now); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM core_registry.site_control_states WHERE id = $1::uuid`, controlStateID)
	}()

	if _, err := admin.Exec(ctx, `
INSERT INTO core_registry.asset_control_locks (
  id, tenant_id, site_id, asset_id, lock_mode, reason, updated_by, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'MANUAL_LOCK', 'test asset lock', $5::uuid, 1, $6, $6
)
`, assetLockID, postgresTenantAID, postgresOwnerASite1ID, postgresSite1AssetID, postgresOwnerAPrincipalID, now); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM core_registry.asset_control_locks WHERE id = $1::uuid`, assetLockID)
	}()

	if _, err := admin.Exec(ctx, `
INSERT INTO core_registry.optimization_execution_monitors (
  id, tenant_id, site_id, lease_id, recommendation_id, command_intent_id, device_id,
  target_capability, baseline_supply_temp_c, applied_supply_temp_c, restore_supply_temp_c,
  zone_temp_max_c, supply_temp_max_c, started_at, window_ends_at, status, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid,
  'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', 7.5, 8.5, 7.5,
  26.0, 12.0, $8, $9, 'OBSERVING', $8, $8
)
`, monitorID, postgresTenantAID, postgresOwnerASite1ID, leaseID, postgresOptimizationRecommendationID, commandIntent, postgresSite1DeviceID, now.Add(-5*time.Minute), now.Add(25*time.Minute)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM core_registry.optimization_execution_monitors WHERE id = $1::uuid`, monitorID)
	}()

	store := openPostgresAuthorizationStore(t, ctx)
	workloadStore, ok := store.CommandAuthorization("command-policy-7", 3).(iam.CommandWorkloadAuthorizationStore)
	if !ok {
		t.Fatal("CommandAuthorization store does not implement CommandWorkloadAuthorizationStore")
	}

	lookup := iam.CommandWorkloadAuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer,
		Subject:       "owner-a",
		TenantID:      postgresTenantAID,
		SiteID:        postgresOwnerASite1ID,
		DeviceID:      postgresSite1DeviceID,
		WindowStart:   now.Add(-time.Hour),
		Now:           now,
	}
	facts, err := workloadStore.LookupCommandWorkloadAuthorization(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.PrincipalFound || facts.PrincipalID != postgresOwnerAPrincipalID || !facts.TenantMemberActive {
		t.Fatalf("unexpected workload principal facts: %#v", facts)
	}
	if facts.DeviceSiteID != postgresOwnerASite1ID || facts.SiteControlMode != "MANUAL_OVERRIDE" || !facts.HasAssetControlLock {
		t.Fatalf("unexpected site/lock facts: %#v", facts)
	}
	if facts.ActiveLease == nil || facts.ActiveLease.ID != leaseID || facts.ActiveLease.MaxCommandsPerHour != 3 || facts.RecentAutoCommandsLastHour != 1 {
		t.Fatalf("unexpected lease/monitor facts: %#v", facts)
	}

	if _, err := admin.Exec(ctx, `UPDATE core_registry.site_control_states SET mode = 'REMOTE_AUTO', revision = 2 WHERE id = $1::uuid`, controlStateID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE core_registry.asset_control_locks SET lock_mode = 'UNLOCKED', revision = 2 WHERE id = $1::uuid`, assetLockID); err != nil {
		t.Fatal(err)
	}
	unlockedFacts, err := workloadStore.LookupCommandWorkloadAuthorization(ctx, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if unlockedFacts.SiteControlMode != "REMOTE_AUTO" || unlockedFacts.HasAssetControlLock {
		t.Fatalf("expected REMOTE_AUTO and no asset lock, got %#v", unlockedFacts)
	}

	restoreSetpoint := 7.5
	rollbackLookup := lookup
	rollbackLookup.IsRollback = true
	rollbackLookup.RequestedSetpoint = &restoreSetpoint
	rollbackFacts, err := workloadStore.LookupCommandWorkloadAuthorization(ctx, rollbackLookup)
	if err != nil {
		t.Fatal(err)
	}
	if !rollbackFacts.MatchingObservingRollback || rollbackFacts.ActiveLease == nil || rollbackFacts.ActiveLease.ID != leaseID {
		t.Fatalf("expected matching observing rollback, got %#v", rollbackFacts)
	}

	mismatchSetpoint := 8.0
	rollbackLookup.RequestedSetpoint = &mismatchSetpoint
	mismatchFacts, err := workloadStore.LookupCommandWorkloadAuthorization(ctx, rollbackLookup)
	if err != nil {
		t.Fatal(err)
	}
	if mismatchFacts.MatchingObservingRollback || mismatchFacts.ActiveLease != nil {
		t.Fatalf("unexpected rollback match on mismatched setpoint: %#v", mismatchFacts)
	}

	crossTenant := lookup
	crossTenant.TenantID = postgresTenantBID
	crossFacts, err := workloadStore.LookupCommandWorkloadAuthorization(ctx, crossTenant)
	if err != nil {
		t.Fatal(err)
	}
	if crossFacts.DeviceSiteID != "" || crossFacts.ActiveLease != nil || crossFacts.HasAssetControlLock || crossFacts.RecentAutoCommandsLastHour != 0 {
		t.Fatalf("command workload facts leaked across tenants: %#v", crossFacts)
	}
}

func TestPostgresTenantPrincipalNamesReturnsOnlyActiveMembersOfTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := openPostgresAuthorizationStore(t, ctx)

	names, err := store.LookupTenantPrincipalNames(ctx, postgresTenantAID, []string{
		postgresOwnerAPrincipalID,
		postgresDelegatedPrincipalID,
		"not-a-uuid",
		"018f1e00-2000-7000-8000-999999999999",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if names[postgresOwnerAPrincipalID] == "" || names[postgresDelegatedPrincipalID] == "" || len(names) != 2 {
		t.Fatalf("unexpected Tenant A principal names: %#v", names)
	}
	otherTenant, err := store.LookupTenantPrincipalNames(ctx, postgresTenantBID, []string{postgresOwnerAPrincipalID}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(otherTenant) != 0 {
		t.Fatalf("Tenant A principal leaked to Tenant B: %#v", otherTenant)
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
