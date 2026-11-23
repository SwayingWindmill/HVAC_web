package commandservice

import (
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestPostgresBatchControlOfflineQueueWakeAndTTLExpiry(t *testing.T) {
	store, _, now, cleanup := postgresDispatchFixture(t)
	defer cleanup()
	ctx := t.Context()

	deviceB := "018f3e00-3000-7000-8000-000000000002"
	pointB := "018f3e00-4000-7000-8000-000000000002"
	ttl := 180
	expiresAt := now.Add(time.Duration(ttl) * time.Second)

	// 1. Offline QUEUE_IF_OFFLINE setpoint command with snapshot_revision=0 persists, defers on offline
	// pre-send, stays unclaimable during backoff, and wakes immediately via WakeDeferredDispatches.
	offlineReq := postgresCommandRequest("pg-offline-wake-1", 23.5)
	offlineReq.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
	offlineReq.TTLSeconds = &ttl
	offlineReq.ExpiresAt = &expiresAt
	offlineReq.CurrentState = commandmodel.CurrentStateEvidence{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "OFFLINE",
		Readiness:              "CURRENT",
		Quality:                "GOOD",
		BusinessRevision:       0,
		ObservedAt:             *now,
	}
	offlineReq.Authorization.IssuedAt = now.Add(-5 * time.Second)
	offlineReq.Authorization.ExpiresAt = now.Add(25 * time.Second)

	createdOffline, err := store.Submit(ctx, offlineReq)
	if err != nil {
		t.Fatalf("Submit offline queue request: %v", err)
	}
	if createdOffline.Intent.Status != commandmodel.IntentQueued ||
		createdOffline.Intent.SnapshotRevision != 0 ||
		createdOffline.Intent.DeliveryMode != commandmodel.DeliveryModeQueueIfOffline {
		t.Fatalf("unexpected submitted offline intent: %+v", createdOffline.Intent)
	}

	batchID, err := newUUID(*now)
	if err != nil {
		t.Fatalf("allocate batch UUID: %v", err)
	}
	batchRec := CommandBatchRecord{
		ID:                     batchID,
		TenantID:               commandTenantA,
		SiteID:                 commandSiteA,
		IdempotencyKey:         "pg-batch-idem-1",
		RequestHash:            "hash-batch-1",
		Capability:             commandmodel.CapabilitySetTemperatureSetpoint,
		CommandSource:          commandmodel.SourceOperator,
		DeliveryMode:           commandmodel.DeliveryModeQueueIfOffline,
		TTLSeconds:             &ttl,
		ExpiresAt:              &expiresAt,
		TargetCount:            2,
		Status:                 "PENDING",
		InitiatedByPrincipalID: commandPrincipalA,
		TraceID:                "4bf92f3577b34da6a3ce929d0e0e4736",
		Reason:                 "pg batch integration test",
		CreatedAt:              *now,
		UpdatedAt:              *now,
	}
	batchView, replayed, err := store.CreateCommandBatch(ctx, batchRec)
	if err != nil || replayed || batchView.ID != batchID {
		t.Fatalf("CreateCommandBatch view=%+v replayed=%v err=%v", batchView, replayed, err)
	}
	if _, replayed2, err := store.CreateCommandBatch(ctx, batchRec); err != nil || !replayed2 {
		t.Fatalf("idempotent CreateCommandBatch replay=%v err=%v", replayed2, err)
	}
	conflictRec := batchRec
	conflictRec.RequestHash = "different-hash"
	if _, _, err := store.CreateCommandBatch(ctx, conflictRec); !errors.Is(err, ErrCommandBatchConflict) {
		t.Fatalf("CreateCommandBatch conflict err=%v, want ErrCommandBatchConflict", err)
	}

	cmdID := createdOffline.Intent.ID
	finalized, err := store.FinalizeCommandBatch(ctx, commandTenantA, batchID, []CommandBatchItemRecord{
		{
			BatchID: batchID, ItemIndex: 0, TenantID: commandTenantA, SiteID: commandSiteA,
			DeviceID: commandDeviceA, PointID: commandPointA, CommandID: &cmdID,
			Admitted: true, Status: string(createdOffline.Intent.Status), CreatedAt: *now, UpdatedAt: *now,
		},
		{
			BatchID: batchID, ItemIndex: 1, TenantID: commandTenantA, SiteID: commandSiteA,
			DeviceID: deviceB, PointID: pointB, Admitted: false,
			RejectionCode: "COMMAND_CAPABILITY_DENIED", RejectionReason: "no grant for device B",
			Status: "REJECTED", CreatedAt: *now, UpdatedAt: *now,
		},
	}, 1, 1, "PARTIAL")
	if err != nil || finalized.AcceptedCount != 1 || finalized.RejectedCount != 1 || len(finalized.Items) != 2 {
		t.Fatalf("FinalizeCommandBatch finalized=%+v err=%v", finalized, err)
	}
	listed, err := store.ListCommandBatches(ctx, commandTenantA, commandSiteA, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != batchID {
		t.Fatalf("ListCommandBatches=%+v err=%v", listed, err)
	}

	// Claim and defer via DISPATCH_SAFETY_DEVICE_OFFLINE.
	env1, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-offline", 10*time.Second)
	if err != nil || env1.CommandID != cmdID {
		t.Fatalf("initial ClaimDispatch env=%+v err=%v", env1, err)
	}
	if err := store.ResolveDispatch(ctx, env1, commandmodel.ConnectorResult{
		Phase:       commandmodel.ConnectorPreSendRejected,
		FailureCode: "DISPATCH_SAFETY_DEVICE_OFFLINE",
		EvidenceID:  "presend-offline-1",
	}); err != nil {
		t.Fatalf("ResolveDispatch offline defer: %v", err)
	}
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-offline", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("deferred claim during backoff returned %v, want ErrNoDispatchAvailable", err)
	}
	woken, err := store.WakeDeferredDispatches(ctx, commandTenantA, []string{commandDeviceA})
	if err != nil || woken != 1 {
		t.Fatalf("WakeDeferredDispatches woken=%d err=%v, want woken=1", woken, err)
	}
	env2, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-offline", 10*time.Second)
	if err != nil || env2.CommandID != cmdID {
		t.Fatalf("post-wake ClaimDispatch env=%+v err=%v", env2, err)
	}
	if err := store.ResolveDispatch(ctx, env2, commandmodel.ConnectorResult{
		Phase:       commandmodel.ConnectorPreSendRejected,
		FailureCode: "DISPATCH_SAFETY_DEVICE_SLEEPING",
		EvidenceID:  "presend-sleeping-2",
	}); err != nil {
		t.Fatalf("ResolveDispatch second offline defer: %v", err)
	}

	// 2. Submit AWAITING_APPROVAL medium-risk setpoint on device B with same TTL, then advance past TTL.
	awaitReq := postgresCommandRequest("pg-awaiting-ttl-1", 24.5) // delta 1.5C -> MEDIUM risk -> AWAITING_APPROVAL
	awaitReq.CurrentState.ObservedAt = *now
	awaitReq.DeviceID = deviceB
	awaitReq.PointID = pointB
	awaitReq.DeliveryMode = commandmodel.DeliveryModeQueueIfOffline
	awaitReq.TTLSeconds = &ttl
	awaitReq.ExpiresAt = &expiresAt
	awaitReq.Authorization.DeviceID = deviceB
	awaitReq.Authorization.GrantID = "018f3e00-9000-7000-8000-000000000002"
	awaitReq.Authorization.IssuedAt = now.Add(-5 * time.Second)
	awaitReq.Authorization.ExpiresAt = now.Add(25 * time.Second)
	createdAwait, err := store.Submit(ctx, awaitReq)
	if err != nil || createdAwait.Intent.Status != commandmodel.IntentAwaitingApproval {
		t.Fatalf("Submit awaiting approval intent=%+v err=%v", createdAwait.Intent, err)
	}

	*now = expiresAt.Add(time.Second)
	dueTenants, err := store.DueDispatchTenants(ctx, *now)
	if err != nil || len(dueTenants) != 1 || dueTenants[0] != commandTenantA {
		t.Fatalf("DueDispatchTenants at TTL expiry=%v err=%v, want [%s]", dueTenants, err, commandTenantA)
	}
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-ttl", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("ClaimDispatch after TTL expiry returned %v, want ErrNoDispatchAvailable", err)
	}

	gotOffline, err := store.Get(ctx, commandTenantA, cmdID)
	if err != nil || gotOffline.Status != commandmodel.IntentExpired {
		t.Fatalf("offline command status=%s err=%v, want EXPIRED", gotOffline.Status, err)
	}
	gotAwait, err := store.Get(ctx, commandTenantA, createdAwait.Intent.ID)
	if err != nil || gotAwait.Status != commandmodel.IntentExpired {
		t.Fatalf("awaiting command status=%s err=%v, want EXPIRED", gotAwait.Status, err)
	}
	refreshedBatch, err := store.GetCommandBatch(ctx, commandTenantA, commandSiteA, batchID)
	if err != nil || len(refreshedBatch.Items) != 2 || refreshedBatch.Items[0].Status != string(commandmodel.IntentExpired) {
		t.Fatalf("GetCommandBatch after TTL expiry=%+v err=%v", refreshedBatch, err)
	}
}
