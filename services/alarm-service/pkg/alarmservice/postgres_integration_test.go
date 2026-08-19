package alarmservice

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	postgresTestTenantID = "0190f000-0000-7000-8000-000000000001"
	postgresTestSiteID   = "01910000-0001-7000-8000-000000000001"
	postgresTestAlarmID  = "01910000-1000-7000-8000-000000000001"
)

func TestPostgresLifecycleWriteIsScopedAtomicAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	initial, err := store.Get(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Condition != alarmmodel.ConditionActive || initial.Version != 1 || initial.Acknowledgement != nil || initial.Suppression != nil {
		t.Fatalf("unexpected initial Alarm: %#v", initial)
	}

	acknowledge := postgresMutation(alarmmodel.OperationAcknowledge, 1, "alarm-postgres-idempotency-1", "2026-07-31T10:00:00Z")
	first, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, acknowledge)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Alarm.Condition != alarmmodel.ConditionActive || first.Alarm.Acknowledgement == nil || first.Alarm.Version != 2 || len(first.Alarm.Timeline) != 2 {
		t.Fatalf("unexpected acknowledgement: %#v", first)
	}
	replay, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, acknowledge)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Alarm.Version != 2 || len(replay.Alarm.Timeline) != 2 {
		t.Fatalf("unexpected acknowledgement replay: %#v", replay)
	}
	conflictingPayload := acknowledge
	conflictingPayload.Reason = "different payload"
	if _, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, conflictingPayload); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	suppressionEnd := "2026-07-31T14:01:00Z"
	suppress := postgresMutation(alarmmodel.OperationSuppress, 2, "alarm-postgres-idempotency-2", "2026-07-31T10:01:00Z")
	suppress.SuppressedUntil = &suppressionEnd
	suppressed, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, suppress)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Alarm.Condition != alarmmodel.ConditionActive || suppressed.Alarm.Suppression == nil || suppressed.Alarm.Suppression.ExpiresAt != suppressionEnd || suppressed.Alarm.Version != 3 {
		t.Fatalf("unexpected suppression: %#v", suppressed)
	}

	assignee := "principal:postgres-operator-2"
	assign := postgresMutation(alarmmodel.OperationAssign, 3, "alarm-postgres-idempotency-3", "2026-07-31T10:02:00Z")
	assign.AssigneeID = &assignee
	assigned, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, assign)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Alarm.Condition != alarmmodel.ConditionActive || assigned.Alarm.AssigneeID == nil || *assigned.Alarm.AssigneeID != assignee || assigned.Alarm.Version != 4 {
		t.Fatalf("unexpected assignment while suppressed: %#v", assigned)
	}

	unsuppress := postgresMutation(alarmmodel.OperationUnsuppress, 4, "alarm-postgres-idempotency-4", "2026-07-31T10:03:00Z")
	unsuppressed, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, unsuppress)
	if err != nil {
		t.Fatal(err)
	}
	if unsuppressed.Alarm.Condition != alarmmodel.ConditionActive || unsuppressed.Alarm.Suppression != nil || unsuppressed.Alarm.AssigneeID == nil || *unsuppressed.Alarm.AssigneeID != assignee || unsuppressed.Alarm.Version != 5 {
		t.Fatalf("unexpected unsuppression projection: %#v", unsuppressed)
	}

	stale := postgresMutation(alarmmodel.OperationAssign, 1, "alarm-postgres-idempotency-5", "2026-07-31T10:04:00Z")
	staleAssignee := "principal:stale"
	stale.AssigneeID = &staleAssignee
	if _, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID, stale); !errors.Is(err, alarmmodel.ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	wrongTenantID := "0190f000-0000-7000-8000-000000000099"
	wrongTenantContext := identitycontext.WithTenantID(context.Background(), wrongTenantID)
	if _, err := store.Get(wrongTenantContext, wrongTenantID, postgresTestSiteID, postgresTestAlarmID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-Tenant invisibility, got %v", err)
	}
	if _, err := store.Get(wrongTenantContext, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected Tenant binding mismatch to fail closed, got %v", err)
	}

	current, err := store.Get(ctx, postgresTestTenantID, postgresTestSiteID, postgresTestAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Condition != alarmmodel.ConditionActive || current.Acknowledgement == nil || current.Version != 5 || len(current.Timeline) != 5 || current.Suppression != nil || current.AssigneeID == nil || *current.AssigneeID != assignee {
		t.Fatalf("failed mutations changed the aggregate or lifecycle did not converge: %#v", current)
	}
}

func TestPostgresConcurrentFirstCreateAndRecurrence(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	ctx := identitycontext.WithTenantID(context.Background(), postgresTestTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publication := Publication{
		AlarmType: "S13_CONCURRENT_FIRST_CREATE", SourceType: alarmmodel.SourceSiteRule,
		SourceReference: "rule:s13-concurrent-first-create:v1", RuleRevision: "alarm-policy-s13-1",
		Title: "S13 concurrent first create", Summary: "PostgreSQL partial unique arbitration fixture.",
		Severity: alarmmodel.SeverityMinor, OccurredAt: "2026-08-19T08:00:00Z",
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "s13-concurrent-first-create",
	}
	fingerprint, err := alarmmodel.Fingerprint(postgresTestTenantID, postgresTestSiteID, publication.SourceType, publication.SourceReference, publication.AlarmType, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	results := make(chan alarmmodel.Alarm, writers)
	errorsCh := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			alarm, err := store.Publish(ctx, postgresTestTenantID, postgresTestSiteID, publication)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- alarm
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	var firstID, firstCorrelation string
	for alarm := range results {
		if firstID == "" {
			firstID, firstCorrelation = alarm.AlarmID, alarm.IncidentCorrelationID
		}
		if alarm.AlarmID != firstID {
			t.Fatalf("concurrent first create produced multiple active incidents: %s != %s", alarm.AlarmID, firstID)
		}
	}
	active, err := store.List(ctx, postgresTestTenantID, postgresTestSiteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	matching := 0
	for _, alarm := range active.Items {
		if alarm.Fingerprint == fingerprint {
			matching++
			if alarm.OccurrenceCount != writers {
				t.Fatalf("concurrent occurrences did not converge: %#v", alarm)
			}
		}
	}
	if matching != 1 {
		t.Fatalf("expected exactly one active fingerprint, got %d", matching)
	}
	if _, err := store.ClearActive(ctx, postgresTestTenantID, postgresTestSiteID, Recovery{
		Fingerprint: fingerprint, IncidentCorrelationID: firstCorrelation,
		OccurredAt: "2026-08-19T08:05:00Z", Reason: "clear predicate matched",
		RuleRevision: "alarm-policy-s13-1", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "s13-clear",
	}); err != nil {
		t.Fatal(err)
	}
	publication.OccurredAt = "2026-08-19T08:10:00Z"
	publication.CorrelationID = "s13-recurrence"
	recurrence, err := store.Publish(ctx, postgresTestTenantID, postgresTestSiteID, publication)
	if err != nil {
		t.Fatal(err)
	}
	if recurrence.AlarmID == firstID || recurrence.IncidentCorrelationID == firstCorrelation || recurrence.Fingerprint != fingerprint {
		t.Fatalf("recurrence did not create a new incident: %#v", recurrence)
	}
	if _, err := store.ClearActive(ctx, postgresTestTenantID, postgresTestSiteID, Recovery{
		Fingerprint: fingerprint, IncidentCorrelationID: firstCorrelation,
		OccurredAt: "2026-08-19T08:11:00Z", Reason: "late replay of old clear predicate",
		RuleRevision: "alarm-policy-s13-1", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "s13-clear-replay",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale recovery crossed Incident boundary: %v", err)
	}
	current, err := store.Get(ctx, postgresTestTenantID, postgresTestSiteID, recurrence.AlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Condition != alarmmodel.ConditionActive || current.IncidentCorrelationID != recurrence.IncidentCorrelationID {
		t.Fatalf("stale recovery changed recurrence: %#v", current)
	}
}

func postgresMutation(operation alarmmodel.Operation, expectedVersion uint64, idempotencyKey, occurredAt string) Mutation {
	return Mutation{
		Operation: operation, ExpectedVersion: expectedVersion, Reason: "postgres integration lifecycle operation",
		ActorType: "PRINCIPAL", ActorID: "principal:postgres-operator", PolicyRevision: "alarm-policy-postgres-1",
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: occurredAt,
	}
}
