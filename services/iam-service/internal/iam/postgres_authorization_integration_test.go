package iam_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/analyticsmodel"
	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/libs/workorderauth"
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
	if len(owner.SiteBindings) != 1 || owner.SiteBindings[0].ActingOrganizationID != postgresOwnerAOrganizationID ||
		owner.SiteBindings[0].OwningOrganizationID != postgresOwnerAOrganizationID || owner.SiteBindings[0].SiteID != postgresOwnerASite1ID {
		t.Fatalf("unexpected Owner A Analytics SiteBinding: %#v", owner.SiteBindings)
	}
	analyticsGranted := false
	for _, action := range owner.SiteBindings[0].Actions {
		if string(action) == analyticsmodel.EnergySeriesAction {
			analyticsGranted = true
		}
	}
	if !analyticsGranted {
		t.Fatalf("Owner A Analytics action missing: %#v", owner.SiteBindings[0].Actions)
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

func TestPostgresTelemetryAuthorizationLoadsExactDeviceAndKeyFacts(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	facts, err := store.LookupTelemetryAuthorization(ctx, iam.TelemetryAuthorizationLookup{
		SubjectIssuer:        postgresFixtureIssuer,
		Subject:              "delegated",
		ActingOrganizationID: postgresActingOrganizationID,
		Targets:              []telemetryauth.Target{{DeviceID: "018f1e00-4000-7000-8000-000000000001", Keys: []string{"fan.speed", "zone.temperature"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.PolicyRevision != "telemetry-access:2" || len(facts.RoleBindings) != 1 || len(facts.SiteBindings) != 1 || len(facts.ExplicitDenies) != 0 || len(facts.Devices) != 1 || len(facts.ScopeBindings) != 1 || len(facts.KeyBindings) != 2 {
		t.Fatalf("Telemetry facts = %#v", facts)
	}
}

func TestPostgresPrincipalTelemetryCapabilityLookupDoesNotEnumerateDevicesOrKeys(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	facts, err := store.LookupPrincipalTelemetryCapabilities(ctx, iam.PrincipalCapabilityLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "delegated", ActingOrganizationID: postgresActingOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.PolicyRevision != "telemetry-access:2" || len(facts.RoleBindings) != 1 || len(facts.SiteBindings) != 1 || len(facts.ScopeBindings) != 1 {
		t.Fatalf("principal Telemetry capability facts = %#v", facts)
	}
	if len(facts.Devices) != 0 || len(facts.KeyBindings) != 0 {
		t.Fatalf("capability lookup enumerated Device/key facts: %#v", facts)
	}
}

func TestPostgresAlarmAuthorizationLoadsExactSiteFactsAndPersistsAudit(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	facts, err := store.LookupAlarmAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", ActingOrganizationID: postgresOwnerAOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.Principal.ID != postgresOwnerAPrincipalID || facts.PolicyRevision != "alarm-access:1" || len(facts.Permissions) != 2 {
		t.Fatalf("Alarm facts = %#v", facts)
	}
	for _, permission := range facts.Permissions {
		if permission.OrganizationID != postgresOwnerAOrganizationID || permission.SiteID != postgresOwnerASite1ID || permission.Effect != iam.BindingEffectAllow || permission.Status != iam.FactStatusActive {
			t.Fatalf("unexpected Alarm permission: %#v", permission)
		}
	}

	other, err := store.LookupAlarmAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", ActingOrganizationID: postgresActingOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !other.Found || other.PolicyRevision != "alarm-access:unconfigured" || len(other.Permissions) != 0 {
		t.Fatalf("cross-Organization Alarm facts were not fail-closed: %#v", other)
	}

	requestID := "alarm-postgres-decision-1"
	if err := store.RecordAlarmDecision(ctx, iam.AlarmDecisionAudit{
		PrincipalID: postgresOwnerAPrincipalID, ActingOrganizationID: postgresOwnerAOrganizationID,
		SiteID: postgresOwnerASite1ID, Action: alarmauth.ActionList, Allowed: true,
		PolicyRevision: "alarm-access:1", ReasonCode: alarmauth.ReasonAllowExactScope,
		RequestID: requestID, TraceID: "trace-alarm-postgres-1", OccurredAt: "2026-08-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer admin.Exec(context.Background(), `DELETE FROM iam.alarm_authorization_decisions WHERE request_id = $1`, requestID)
	var allowed bool
	var policyRevision, reasonCode string
	if err := admin.QueryRow(ctx, `SELECT allowed, policy_revision, reason_code FROM iam.alarm_authorization_decisions WHERE request_id = $1`, requestID).Scan(&allowed, &policyRevision, &reasonCode); err != nil {
		t.Fatal(err)
	}
	if !allowed || policyRevision != "alarm-access:1" || reasonCode != string(alarmauth.ReasonAllowExactScope) {
		t.Fatalf("durable Alarm decision = allowed=%v policy=%q reason=%q", allowed, policyRevision, reasonCode)
	}
}

func TestPostgresWorkOrderAuthorizationLoadsExactSiteFactsAndPersistsAudit(t *testing.T) {
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := iam.OpenPostgresAuthorizationStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	facts, err := store.LookupWorkOrderAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", ActingOrganizationID: postgresOwnerAOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Found || facts.Principal.ID != postgresOwnerAPrincipalID || facts.PolicyRevision != "work-order-access:1" || len(facts.Permissions) != 11 || len(facts.Targets) != 2 {
		t.Fatalf("Work Order facts = %#v", facts)
	}
	expectedActions := map[workorderauth.Action]bool{
		workorderauth.ActionList: false, workorderauth.ActionRead: false, workorderauth.ActionCreate: false, workorderauth.ActionAssign: false,
		workorderauth.ActionPlan: false, workorderauth.ActionStart: false, workorderauth.ActionBlock: false, workorderauth.ActionResume: false,
		workorderauth.ActionComplete: false, workorderauth.ActionCancel: false, workorderauth.ActionReopen: false,
	}
	for _, permission := range facts.Permissions {
		if permission.OrganizationID != postgresOwnerAOrganizationID || permission.SiteID != postgresOwnerASite1ID || permission.Effect != iam.BindingEffectAllow || permission.Status != iam.FactStatusActive {
			t.Fatalf("unexpected Work Order permission: %#v", permission)
		}
		if seen, ok := expectedActions[permission.Action]; !ok || seen {
			t.Fatalf("unexpected or duplicate Work Order action: %q", permission.Action)
		}
		expectedActions[permission.Action] = true
	}
	for action, seen := range expectedActions {
		if !seen {
			t.Fatalf("missing Work Order permission action: %q", action)
		}
	}

	other, err := store.LookupWorkOrderAuthorization(ctx, iam.AuthorizationLookup{
		SubjectIssuer: postgresFixtureIssuer, Subject: "owner-a", ActingOrganizationID: postgresActingOrganizationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !other.Found || other.PolicyRevision != "work-order-access:unconfigured" || len(other.Permissions) != 0 {
		t.Fatalf("cross-Organization Work Order facts were not fail-closed: %#v", other)
	}

	requestID := "work-order-postgres-decision-1"
	if err := store.RecordWorkOrderDecision(ctx, iam.WorkOrderDecisionAudit{
		PrincipalID: postgresOwnerAPrincipalID, ActingOrganizationID: postgresOwnerAOrganizationID,
		SiteID: postgresOwnerASite1ID, WorkOrderID: "01910000-1000-7000-8000-000000000001",
		Action: workorderauth.ActionRead, Allowed: true,
		PolicyRevision: "work-order-access:1", ReasonCode: workorderauth.ReasonAllowExactScope,
		RequestID: requestID, TraceID: "trace-work-order-postgres-1", OccurredAt: "2026-08-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	defer admin.Exec(context.Background(), `DELETE FROM iam.work_order_authorization_decisions WHERE request_id = $1`, requestID)
	var allowed bool
	var policyRevision, reasonCode, workOrderID string
	if err := admin.QueryRow(ctx, `SELECT allowed, policy_revision, reason_code, work_order_id::text FROM iam.work_order_authorization_decisions WHERE request_id = $1`, requestID).Scan(&allowed, &policyRevision, &reasonCode, &workOrderID); err != nil {
		t.Fatal(err)
	}
	if !allowed || policyRevision != "work-order-access:1" || reasonCode != string(workorderauth.ReasonAllowExactScope) || workOrderID != "01910000-1000-7000-8000-000000000001" {
		t.Fatalf("durable Work Order decision = allowed=%v policy=%q reason=%q workOrder=%q", allowed, policyRevision, reasonCode, workOrderID)
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
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM core_registry.equipment`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime accessed Core Equipment data: %v", err)
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
