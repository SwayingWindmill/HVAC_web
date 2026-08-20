package alarmservice

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestPostgresAlarmNotificationOutboxEmitsOnlyBusinessStateChangesAndRelayIsFenced(t *testing.T) {
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
		AlarmType: "S16_NOTIFICATION_OUTBOX", SourceType: alarmmodel.SourceSiteRule,
		SourceReference: "alarm-policy:s16-notification-outbox", RuleRevision: "alarm-policy-s16-1",
		Title: "S16 notification outbox", Summary: "S16 durable source event fixture.",
		Severity: alarmmodel.SeverityMajor, OccurredAt: "2026-08-23T08:00:00Z",
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "s16-create",
	}
	created, err := store.Publish(ctx, postgresTestTenantID, postgresTestSiteID, publication)
	if err != nil {
		t.Fatal(err)
	}
	publication.OccurredAt = "2026-08-23T08:01:00Z"
	publication.CorrelationID = "s16-same-severity"
	if _, err := store.Publish(ctx, postgresTestTenantID, postgresTestSiteID, publication); err != nil {
		t.Fatal(err)
	}
	publication.Severity = alarmmodel.SeverityCritical
	publication.OccurredAt = "2026-08-23T08:02:00Z"
	publication.CorrelationID = "s16-severity-changed"
	severityChanged, err := store.Publish(ctx, postgresTestTenantID, postgresTestSiteID, publication)
	if err != nil {
		t.Fatal(err)
	}
	ack := postgresMutation(alarmmodel.OperationAcknowledge, severityChanged.Version, "s16-notification-ack", "2026-08-23T08:03:00Z")
	acked, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, created.AlarmID, ack)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := alarmmodel.Fingerprint(postgresTestTenantID, postgresTestSiteID, publication.SourceType, publication.SourceReference, publication.AlarmType, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := store.ClearActive(ctx, postgresTestTenantID, postgresTestSiteID, Recovery{
		Fingerprint: fingerprint, IncidentCorrelationID: created.IncidentCorrelationID, OccurredAt: "2026-08-23T08:04:00Z",
		Reason: "S16 recovery", RuleRevision: "alarm-policy-s16-1", ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "s16-clear",
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := store.beginTenantTransaction(ctx, postgresTestTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT action,alarm_version FROM alarm_runtime.notification_outbox WHERE tenant_id=$1 AND alarm_id=$2 ORDER BY alarm_version`, postgresTestTenantID, created.AlarmID)
	if err != nil {
		t.Fatal(err)
	}
	var actions []NotificationAction
	var versions []uint64
	for rows.Next() {
		var action NotificationAction
		var version uint64
		if err := rows.Scan(&action, &version); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		versions = append(versions, version)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 4 || actions[0] != NotificationCreated || actions[1] != NotificationSeverityChanged || actions[2] != NotificationAcknowledged || actions[3] != NotificationCleared {
		t.Fatalf("Alarm outbox emitted non-business or missing state events: actions=%#v versions=%#v", actions, versions)
	}
	if versions[0] != 1 || versions[1] != 3 || versions[2] != acked.Alarm.Version || versions[3] != cleared.Version {
		t.Fatalf("Alarm outbox is not bound to exact aggregate versions: %#v", versions)
	}

	relay, err := OpenNotificationRelay(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	claimAt := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	firstClaim, err := relay.Claim(context.Background(), "s16-relay-a", claimAt, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reclaimed, err := relay.Claim(context.Background(), "s16-relay-b", claimAt.Add(2*time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.SourceEventID != firstClaim.SourceEventID || reclaimed.LeaseFence <= firstClaim.LeaseFence {
		t.Fatalf("expired Alarm notification work was not reclaimed with a higher fence: first=%#v reclaimed=%#v", firstClaim, reclaimed)
	}
	if err := relay.Complete(context.Background(), firstClaim.SourceEventID, firstClaim.LeaseOwner, firstClaim.LeaseFence, claimAt.Add(3*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale Alarm notification relay fence completed work: %v", err)
	}
	if err := relay.Complete(context.Background(), reclaimed.SourceEventID, reclaimed.LeaseOwner, reclaimed.LeaseFence, claimAt.Add(3*time.Second)); err != nil {
		t.Fatalf("current Alarm notification relay owner could not complete work: %v", err)
	}
}
