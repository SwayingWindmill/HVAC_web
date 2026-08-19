package alarmservice

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func TestMemoryStoreConcurrentFirstCreateConvergesOnOneActiveIncident(t *testing.T) {
	store, err := NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	publication := validPublication("2026-07-31T12:00:00Z")
	const writers = 12
	results := make(chan alarmmodel.Alarm, writers)
	errorsCh := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			alarm, err := store.Publish(context.Background(), testTenantID, testSiteID, publication)
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
	var alarmID string
	for alarm := range results {
		if alarmID == "" {
			alarmID = alarm.AlarmID
		}
		if alarm.AlarmID != alarmID {
			t.Fatalf("same fingerprint created multiple active incidents: %s != %s", alarm.AlarmID, alarmID)
		}
	}
	listed, err := store.List(context.Background(), testTenantID, testSiteID, Filter{Condition: alarmmodel.ConditionActive, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].OccurrenceCount != writers {
		t.Fatalf("unexpected concurrent publication projection: %#v", listed.Items)
	}
}

func TestMemoryStoreClearThenRecurrenceCreatesNewIncident(t *testing.T) {
	store, _ := NewMemoryStore(nil)
	first, err := store.Publish(context.Background(), testTenantID, testSiteID, validPublication("2026-07-31T12:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := store.ClearActive(context.Background(), testTenantID, testSiteID, Recovery{
		Fingerprint: first.Fingerprint, IncidentCorrelationID: first.IncidentCorrelationID,
		OccurredAt: "2026-07-31T12:05:00Z", Reason: "clear predicate matched",
		RuleRevision: "alarm-policy-10", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "recovery-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Condition != alarmmodel.ConditionCleared {
		t.Fatalf("recovery did not clear incident: %#v", cleared)
	}
	publication := validPublication("2026-07-31T12:10:00Z")
	publication.CorrelationID = "recurrence-1"
	second, err := store.Publish(context.Background(), testTenantID, testSiteID, publication)
	if err != nil {
		t.Fatal(err)
	}
	if second.AlarmID == first.AlarmID || second.IncidentCorrelationID == first.IncidentCorrelationID || second.Fingerprint != first.Fingerprint {
		t.Fatalf("recurrence did not establish a new correlated incident: first=%#v second=%#v", first, second)
	}
	if _, err := store.ClearActive(context.Background(), testTenantID, testSiteID, Recovery{
		Fingerprint: first.Fingerprint, IncidentCorrelationID: first.IncidentCorrelationID,
		OccurredAt: "2026-07-31T12:11:00Z", Reason: "late replay of old clear predicate",
		RuleRevision: "alarm-policy-10", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "recovery-1-replay",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale recovery crossed Incident boundary: %v", err)
	}
	current, err := store.Get(context.Background(), testTenantID, testSiteID, second.AlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Condition != alarmmodel.ConditionActive || current.IncidentCorrelationID != second.IncidentCorrelationID {
		t.Fatalf("stale recovery changed recurrence: %#v", current)
	}
}

func TestMemoryStoreAppliesAndReplaysIdempotentMutation(t *testing.T) {
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm(t)})
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
	if first.Replayed || !second.Replayed || first.Alarm.Version != 2 || second.Alarm.Version != first.Alarm.Version || len(second.Alarm.Timeline) != 2 || second.Alarm.Condition != alarmmodel.ConditionActive {
		t.Fatalf("unexpected idempotent results: first=%#v second=%#v", first, second)
	}
}

func TestMemoryStoreRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm(t)})
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
	store, err := NewMemoryStore([]alarmmodel.Alarm{validHTTPAlarm(t)})
	if err != nil {
		t.Fatal(err)
	}
	assignee := "principal:operator-2"
	mutation := validMutation(alarmmodel.OperationAssign, 9, "alarm-idempotency-3", "2026-07-31T12:01:00Z")
	mutation.AssigneeID = &assignee
	if _, err := store.Apply(context.Background(), testTenantID, testSiteID, testAlarmID, mutation); !errors.Is(err, alarmmodel.ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	current, err := store.Get(context.Background(), testTenantID, testSiteID, testAlarmID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || len(current.Timeline) != 1 || current.Condition != alarmmodel.ConditionActive {
		t.Fatalf("stale mutation changed projection: %#v", current)
	}
}

func validPublication(occurredAt string) Publication {
	return Publication{
		AlarmType: "SUPPLY_TEMPERATURE_DRIFT", SourceType: alarmmodel.SourceSiteRule,
		SourceReference: "rule:central-plant-temperature-drift:v3", RuleRevision: "alarm-policy-10",
		Title: "Supply temperature drift", Summary: "Supply temperature is outside the governed operating band.",
		Severity: alarmmodel.SeverityMajor, OccurredAt: occurredAt,
		Evidence:  []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:publication", CapturedAt: occurredAt}},
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "publication-1",
	}
}

func validMutation(operation alarmmodel.Operation, expectedVersion uint64, idempotencyKey, occurredAt string) Mutation {
	return Mutation{
		Operation: operation, ExpectedVersion: expectedVersion, Reason: "operator supplied reason",
		ActorType: "PRINCIPAL", ActorID: "principal:operator-1", PolicyRevision: "alarm-policy-9",
		CorrelationID: idempotencyKey, IdempotencyKey: idempotencyKey, OccurredAt: occurredAt,
	}
}
