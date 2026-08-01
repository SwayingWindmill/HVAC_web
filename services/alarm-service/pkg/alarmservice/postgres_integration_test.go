package alarmservice

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const (
	postgresTestOrganizationID = "01910000-0000-7000-8000-000000000001"
	postgresTestSiteID         = "01910000-0001-7000-8000-000000000001"
	postgresTestAlarmID        = "01910000-1000-7000-8000-000000000001"
)

func TestPostgresLifecycleWriteIsScopedAtomicAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	initial, err := store.Get(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status != alarmmodel.StatusOpen || initial.Version != 1 {
		t.Fatalf("unexpected initial Alarm: %#v", initial)
	}

	acknowledge := postgresMutation(alarmmodel.OperationAcknowledge, 1, "alarm-postgres-idempotency-1", "2026-07-31T10:00:00Z")
	first, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, acknowledge)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Alarm.Status != alarmmodel.StatusAcknowledged || first.Alarm.Version != 2 || len(first.Alarm.Transitions) != 2 {
		t.Fatalf("unexpected acknowledgement: %#v", first)
	}
	replay, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, acknowledge)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Alarm.Version != 2 || len(replay.Alarm.Transitions) != 2 {
		t.Fatalf("unexpected acknowledgement replay: %#v", replay)
	}
	conflictingPayload := acknowledge
	conflictingPayload.Reason = "different payload"
	if _, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, conflictingPayload); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	suppressionEnd := "2026-07-31T14:01:00Z"
	suppress := postgresMutation(alarmmodel.OperationSuppress, 2, "alarm-postgres-idempotency-2", "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &suppressionEnd
	suppressed, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, suppress)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Alarm.Status != alarmmodel.StatusSuppressed || suppressed.Alarm.SuppressedUntil == nil || *suppressed.Alarm.SuppressedUntil != suppressionEnd || suppressed.Alarm.Version != 3 {
		t.Fatalf("unexpected suppression: %#v", suppressed)
	}

	assignee := "principal:postgres-operator-2"
	assign := postgresMutation(alarmmodel.OperationAssign, 3, "alarm-postgres-idempotency-3", "2026-07-31T10:02:00Z")
	assign.AssigneeID = &assignee
	assigned, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, assign)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Alarm.Status != alarmmodel.StatusSuppressed || assigned.Alarm.AssigneeID == nil || *assigned.Alarm.AssigneeID != assignee || assigned.Alarm.Version != 4 {
		t.Fatalf("unexpected assignment while suppressed: %#v", assigned)
	}

	unsuppress := postgresMutation(alarmmodel.OperationUnsuppress, 4, "alarm-postgres-idempotency-4", "2026-07-31T10:03:00Z")
	unsuppressed, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, unsuppress)
	if err != nil {
		t.Fatal(err)
	}
	if unsuppressed.Alarm.Status != alarmmodel.StatusAcknowledged || unsuppressed.Alarm.SuppressedUntil != nil || unsuppressed.Alarm.AssigneeID == nil || *unsuppressed.Alarm.AssigneeID != assignee || unsuppressed.Alarm.Version != 5 {
		t.Fatalf("unexpected unsuppression projection: %#v", unsuppressed)
	}

	stale := postgresMutation(alarmmodel.OperationClose, 1, "alarm-postgres-idempotency-5", "2026-07-31T10:04:00Z")
	if _, err := store.Apply(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID, stale); !errors.Is(err, alarmmodel.ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	if _, err := store.Get(ctx, "01910000-0000-7000-8000-000000000099", postgresTestSiteID, postgresTestAlarmID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-Organization invisibility, got %v", err)
	}

	current, err := store.Get(ctx, postgresTestOrganizationID, postgresTestSiteID, postgresTestAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != alarmmodel.StatusAcknowledged || current.Version != 5 || len(current.Transitions) != 5 || current.SuppressedUntil != nil || current.AssigneeID == nil || *current.AssigneeID != assignee {
		t.Fatalf("failed mutations changed the aggregate or lifecycle did not converge: %#v", current)
	}
	for _, transition := range current.Transitions[1:] {
		if transition.ActorID == nil || transition.PolicyRevision == nil || transition.CorrelationID == nil {
			t.Fatalf("persisted lifecycle evidence is incomplete: %#v", transition)
		}
	}
}

func postgresMutation(operation alarmmodel.Operation, expectedVersion uint64, idempotencyKey, occurredAt string) Mutation {
	return Mutation{
		Operation:       operation,
		ExpectedVersion: expectedVersion,
		Reason:          "postgres integration lifecycle operation",
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:postgres-operator",
		PolicyRevision:  "alarm-policy-postgres-1",
		CorrelationID:   idempotencyKey,
		IdempotencyKey:  idempotencyKey,
		OccurredAt:      occurredAt,
	}
}
