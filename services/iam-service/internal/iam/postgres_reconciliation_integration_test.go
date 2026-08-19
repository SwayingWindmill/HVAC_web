package iam_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const reconciliationPrincipalID = "018f1e00-2000-7000-8000-000000000099"

func TestPostgresReconciliationIsIdempotentVersionedAndQuarantinesConflicts(t *testing.T) {
	reconcilerURL := requiredIAMPostgresEnv(t, "S1_IAM_RECONCILER_DATABASE_URL")
	adminURL := requiredIAMPostgresEnv(t, "S1_ADMIN_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := iam.OpenPostgresReconciliationStore(ctx, reconcilerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	validFrom := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	request := iam.ReconciliationRequest{
		TenantID:      "018f1d00-0000-7000-8000-000000000001",
		SourceSystem:  "identity",
		SourceKey:     "user-reconciliation-fixture",
		SourceVersion: 1,
		Principal: iam.ReconciledPrincipal{
			ID: reconciliationPrincipalID, SubjectIssuer: postgresFixtureIssuer,
			Subject: "new-subject-same-email", DisplayName: "Reconciled User",
			Email: "owner-a@example.test", Status: iam.PrincipalStatusActive,
		},
		Memberships: []iam.ReconciledMembership{{
			TenantID: postgresTenantAID, Status: iam.FactStatusActive, ValidFrom: validFrom,
		}},
		RoleBindings: []iam.ReconciledRoleBinding{{
			TenantID: postgresTenantAID, RoleKey: "registry-reader",
			Actions: []registryauth.Action{registryauth.ActionSiteRead}, Effect: iam.BindingEffectAllow, ValidFrom: validFrom,
		}},
	}

	applied, err := store.Reconcile(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != iam.ReconciliationApplied {
		t.Fatalf("expected APPLIED, got %#v", applied)
	}
	unchanged, err := store.Reconcile(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != iam.ReconciliationNoChange || unchanged.InputHash != applied.InputHash {
		t.Fatalf("expected idempotent NO_CHANGE, got %#v", unchanged)
	}

	request.Principal.DisplayName = "Conflicting Same Version"
	conflict, err := store.Reconcile(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Status != iam.ReconciliationQuarantined || conflict.ReasonCode != iam.ReasonSourceVersionConflict {
		t.Fatalf("expected same-version conflict quarantine, got %#v", conflict)
	}

	otherSource := request
	otherSource.SourceKey = "other-source-for-same-principal"
	otherSource.Principal.DisplayName = "Reconciled User"
	sourceConflict, err := store.Reconcile(ctx, otherSource)
	if err != nil {
		t.Fatal(err)
	}
	if sourceConflict.Status != iam.ReconciliationQuarantined || sourceConflict.ReasonCode != iam.ReasonSourcePrincipalConflict {
		t.Fatalf("expected source-to-Principal conflict quarantine, got %#v", sourceConflict)
	}

	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var subject, email, conflictingSourceKey string
	var eventCount, quarantineCount int
	if err := admin.QueryRow(ctx, `SELECT external_subject, email FROM iam.principals WHERE id = $1::uuid`, reconciliationPrincipalID).Scan(&subject, &email); err != nil {
		t.Fatal(err)
	}
	if subject != "new-subject-same-email" || email != "owner-a@example.test" {
		t.Fatalf("duplicate email changed immutable mapping: %q %q", subject, email)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM iam.reconciliation_events WHERE principal_id = $1::uuid`, reconciliationPrincipalID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM iam.reconciliation_quarantine WHERE requested_principal_id = $1::uuid AND quarantine_status = 'OPEN'`, reconciliationPrincipalID).Scan(&quarantineCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 4 || quarantineCount != 2 {
		t.Fatalf("unexpected reconciliation evidence: events=%d quarantine=%d", eventCount, quarantineCount)
	}
	if err := admin.QueryRow(ctx, `
SELECT current_source_key
FROM iam.reconciliation_quarantine
WHERE source_system = 'identity'
  AND source_key = 'other-source-for-same-principal'
  AND reason_code = 'SOURCE_PRINCIPAL_CONFLICT'
`).Scan(&conflictingSourceKey); err != nil {
		t.Fatal(err)
	}
	if conflictingSourceKey != "user-reconciliation-fixture" {
		t.Fatalf("conflicting reconciliation source was not retained: %q", conflictingSourceKey)
	}
}

func TestPostgresReconcilerRoleIsIsolatedFromCoreAndRuntimeLedger(t *testing.T) {
	reconcilerURL := requiredIAMPostgresEnv(t, "S1_IAM_RECONCILER_DATABASE_URL")
	runtimeURL := requiredIAMPostgresEnv(t, "S1_IAM_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if store, err := iam.OpenPostgresReconciliationStore(ctx, runtimeURL); err == nil {
		store.Close()
		t.Fatal("reconciliation store accepted IAM runtime identity")
	}
	reconciler, err := pgxpool.New(ctx, reconcilerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reconciler.Close()
	if _, err := reconciler.Exec(ctx, `SELECT count(*) FROM core_registry.organizations`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM reconciler accessed Core Schema: %v", err)
	}
	if _, err := reconciler.Exec(ctx, `UPDATE iam.principals SET external_subject = 'forged' WHERE id = $1::uuid`, reconciliationPrincipalID); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM reconciler changed immutable identity columns: %v", err)
	}
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Exec(ctx, `SELECT count(*) FROM iam.reconciliation_events`); err == nil || !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("IAM runtime accessed reconciliation ledger: %v", err)
	}
}
