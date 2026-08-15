package alarmservice

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func TestMemoryStoreAppliesAndReplaysIdempotentMutation(t *testing.T) {
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	mutation := validMutation(alarmmodel.OperationAcknowledge, 1, "alarm-idempotency-1", "2026-07-31T12:01:00Z")
	first, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || !second.Replayed || first.Alarm.Version != 2 || second.Alarm.Version != first.Alarm.Version || len(second.Alarm.Transitions) != 2 {
		t.Fatalf("unexpected idempotent results: first=%#v second=%#v", first, second)
	}
}

func TestMemoryStoreRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	mutation := validMutation(alarmmodel.OperationAcknowledge, 1, "alarm-idempotency-2", "2026-07-31T12:01:00Z")
	if _, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation); err != nil {
		t.Fatal(err)
	}
	mutation.Reason = "different reason"
	if _, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestMemoryStoreRejectsStaleExpectedVersionWithoutWriting(t *testing.T) {
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm()})
	if err != nil {
		t.Fatal(err)
	}
	mutation := validMutation(alarmmodel.OperationClose, 9, "alarm-idempotency-3", "2026-07-31T12:01:00Z")
	if _, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation); !errors.Is(err, alarmmodel.ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	current, err := store.Get(context.Background(), testTenantID, testSiteID, testAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || len(current.Transitions) != 1 {
		t.Fatalf("stale mutation changed projection: %#v", current)
	}
}

func validMutation(operation alarmmodel.Operation, expectedVersion uint64, idempotencyKey, occurredAt string) Mutation {
	return Mutation{
		Operation:       operation,
		ExpectedVersion: expectedVersion,
		Reason:          "operator supplied reason",
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:operator-1",
		PolicyRevision:  "alarm-policy-9",
		CorrelationID:   idempotencyKey,
		IdempotencyKey:  idempotencyKey,
		OccurredAt:      occurredAt,
	}
}
