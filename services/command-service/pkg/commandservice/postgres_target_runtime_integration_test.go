package commandservice

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestPostgresConnectorEvidenceIsDurableIdempotentAndFenceBound(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	store := NewPostgresStore(opened.pool, fixedPostgresClock(), nil)

	otherRequest := postgresCommandRequest("target-runtime-evidence-other", 23)
	otherRequest.SiteID = "018f3e00-1000-7000-8000-000000000002"
	otherRequest.DeviceID = "018f3e00-3000-7000-8000-000000000002"
	otherRequest.Authorization.SiteID = otherRequest.SiteID
	otherRequest.Authorization.DeviceID = otherRequest.DeviceID
	other, err := store.Submit(ctx, otherRequest)
	if err != nil {
		t.Fatalf("submit other command: %v", err)
	}
	submitted, err := store.Submit(ctx, postgresCommandRequest("target-runtime-evidence", 24))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	envelope, err := store.ClaimDispatchForCohort(ctx, commandTenantA, commandSiteA, commandDeviceA, commandmodel.CapabilitySetTemperatureSetpoint, "dispatcher-a", 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if envelope.CommandID != submitted.Intent.ID {
		t.Fatalf("claimed command=%s submitted=%s", envelope.CommandID, submitted.Intent.ID)
	}
	var otherStatus string
	var otherLeaseOwner *string
	if err := admin.QueryRow(ctx, `
SELECT i.status, o.lease_owner
FROM command_runtime.command_intents i
JOIN command_runtime.command_dispatch_outbox o ON o.command_id = i.command_id
WHERE i.command_id = $1::uuid
`, other.Intent.ID).Scan(&otherStatus, &otherLeaseOwner); err != nil {
		t.Fatal(err)
	}
	if otherStatus != string(commandmodel.IntentQueued) || otherLeaseOwner != nil {
		t.Fatalf("non-cohort command was perturbed: status=%s leaseOwner=%v", otherStatus, otherLeaseOwner)
	}

	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: envelope.AttemptID, CommandID: envelope.CommandID,
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		ExternalDeviceID: "eg8200-device-1", ExecutionFence: envelope.ExecutionFence,
		PayloadHash: envelope.PayloadHash, MappingRevision: "mapping:setpoint:v1", BindingRevision: "binding:device:v1",
		ProviderEndpoint: "hvac/v1/tenant/site/gateway/command", ProviderMethod: "setTemperatureSetpoint",
		RequestSHA256: strings.Repeat("a", 64),
		PreparedAt:    time.Date(2026, 7, 26, 11, 0, 1, 0, time.UTC),
	}
	mismatchedPrepared := prepared
	mismatchedPrepared.CommandID = other.Intent.ID
	if err := store.PrepareConnectorEvidence(ctx, mismatchedPrepared); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected Attempt/Command mismatch to fail closed, got %v", err)
	}
	var mismatchedCount int
	if err := admin.QueryRow(ctx, `
SELECT count(*)
FROM command_runtime.connector_evidence
WHERE attempt_id = $1::uuid AND execution_fence = $2
`, envelope.AttemptID, envelope.ExecutionFence).Scan(&mismatchedCount); err != nil {
		t.Fatal(err)
	}
	if mismatchedCount != 0 {
		t.Fatalf("mismatched connector evidence rows=%d", mismatchedCount)
	}
	if err := store.PrepareConnectorEvidence(ctx, prepared); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := store.PrepareConnectorEvidence(ctx, prepared); err != nil {
		t.Fatalf("idempotent prepare: %v", err)
	}

	completed := commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared,
		RequestWritten:            true, ConnectorPhase: commandmodel.ConnectorRequestCommitted,
		FailureCode: "PROVIDER_RESPONSE_UNAVAILABLE",
		CompletedAt: time.Date(2026, 7, 26, 11, 0, 2, 0, time.UTC),
	}
	if err := store.CompleteConnectorEvidence(ctx, completed); err != nil {
		t.Fatalf("complete request committed: %v", err)
	}
	if err := store.CompleteConnectorEvidence(ctx, completed); err != nil {
		t.Fatalf("idempotent complete: %v", err)
	}

	mismatch := completed
	mismatch.FailureCode = "DIFFERENT_RESULT"
	if err := store.CompleteConnectorEvidence(ctx, mismatch); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected mismatched duplicate to be rejected, got %v", err)
	}

	var count int
	if err := admin.QueryRow(ctx, `
SELECT count(*)
FROM command_runtime.connector_evidence
WHERE attempt_id = $1::uuid AND execution_fence = $2
`, envelope.AttemptID, envelope.ExecutionFence).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("connector evidence rows=%d", count)
	}
}

func TestPostgresCommandGrantUseSurvivesReplicaAndRestartBoundaries(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)
	firstStore, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	claims := commandauth.GrantClaims{
		TokenID:  strings.Join([]string{"grant", "use", "one"}, "-"),
		GrantID:  strings.Join([]string{"grant", "id", "one"}, "-"),
		TenantID: commandTenantA, PolicyRevision: "command-policy-v2", EmergencyRevocationRevision: 7,
	}
	status, err := firstStore.ConsumeCommandGrant(ctx, claims, "command-policy-v2", 7)
	if err != nil {
		firstStore.Close()
		t.Fatalf("first consume: %v", err)
	}
	if status.Replayed || status.CurrentPolicyRevision != "command-policy-v2" || status.CurrentRevocationRevision != 7 {
		firstStore.Close()
		t.Fatalf("first status=%#v", status)
	}
	firstStore.Close()

	secondStore, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	status, err = secondStore.ConsumeCommandGrant(ctx, claims, "command-policy-v2", 7)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if !status.Replayed {
		t.Fatalf("expected replay after store restart, got %#v", status)
	}
}
