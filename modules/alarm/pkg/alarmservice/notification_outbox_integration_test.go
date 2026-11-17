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
	_, err = store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, created.AlarmID, ack)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := alarmmodel.Fingerprint(postgresTestTenantID, postgresTestSiteID, publication.SourceType, publication.SourceReference, publication.AlarmType, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ClearActive(ctx, postgresTestTenantID, postgresTestSiteID, Recovery{
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
	// The new Alarm, its acknowledgement and its recovery go out; its severity change does not.
	want := []NotificationAction{NotificationCreated, NotificationAcknowledged, NotificationCleared}
	if len(actions) != len(want) || versions[0] != created.Version || versions[1] <= severityChanged.Version || versions[2] <= versions[1] {
		t.Fatalf("Alarm outbox emitted the wrong events: actions=%#v versions=%#v", actions, versions)
	}
	for index, action := range want {
		if actions[index] != action {
			t.Fatalf("Alarm outbox emitted the wrong events: actions=%#v versions=%#v", actions, versions)
		}
	}
	// Acknowledging again changes nothing and tells nobody.
	if _, err := store.Apply(ctx, postgresTestTenantID, postgresTestSiteID, created.AlarmID, ack); err == nil {
		tx, err := store.beginTenantTransaction(ctx, postgresTestTenantID, true)
		if err != nil {
			t.Fatal(err)
		}
		var acknowledgements int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM alarm_runtime.notification_outbox WHERE tenant_id=$1 AND alarm_id=$2 AND action='ACKNOWLEDGED'`, postgresTestTenantID, created.AlarmID).Scan(&acknowledgements); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(ctx)
		if acknowledgements != 1 {
			t.Fatalf("a repeated acknowledgement was sent again: %d", acknowledgements)
		}
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

func TestPostgresAlarmNotificationOutboxEmitsSuppressionEventsWithAttributes(t *testing.T) {
	databaseURL := os.Getenv("S4_ALARM_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S4_ALARM_TEST_DATABASE_URL is not configured")
	}
	suppressTenantID := "0190f000-0000-7000-8000-000000000099"
	ctx := identitycontext.WithTenantID(context.Background(), suppressTenantID)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	deviceID := "01910000-0000-7000-8000-000000000222"
	created, err := store.Publish(ctx, suppressTenantID, postgresTestSiteID, Publication{
		DeviceID:        &deviceID,
		SourceType:      alarmmodel.SourceDeviceRule,
		SourceReference: "device-rule-s16-suppress-test",
		AlarmType:       "CHILLER_LOCKOUT",
		Title:           "Chiller Lockout",
		Summary:         "Suppression outbox test",
		Severity:        alarmmodel.SeverityWarning,
		OccurredAt:      "2026-08-23T09:00:00Z",
		RuleRevision:    "alarm-policy-s16-suppress-1",
		ActorType:       "WORKLOAD",
		ActorID:         "alarm-evaluator",
		CorrelationID:   "s16-suppress-publish",
	})
	if err != nil {
		t.Fatal(err)
	}

	suppressExpires := "2026-08-23T11:00:00Z"
	suppressedResult, err := store.Apply(ctx, suppressTenantID, postgresTestSiteID, created.AlarmID, Mutation{
		Operation:       alarmmodel.OperationSuppress,
		ExpectedVersion: created.Version,
		OccurredAt:      "2026-08-23T09:01:00Z",
		Reason:          "Maintenance window",
		SuppressedUntil: &suppressExpires,
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:operator-1",
		PolicyRevision:  "alarm-policy-postgres-1",
		IdempotencyKey:  "s16-suppress-event-1",
		CorrelationID:   "s16-suppress-corr-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Apply(ctx, suppressTenantID, postgresTestSiteID, created.AlarmID, Mutation{
		Operation:       alarmmodel.OperationUnsuppress,
		ExpectedVersion: suppressedResult.Alarm.Version,
		OccurredAt:      "2026-08-23T09:02:00Z",
		Reason:          "Maintenance finished early",
		ActorType:       "PRINCIPAL",
		ActorID:         "principal:operator-1",
		PolicyRevision:  "alarm-policy-postgres-1",
		IdempotencyKey:  "s16-unsuppress-event-1",
		CorrelationID:   "s16-unsuppress-corr-1",
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := store.beginTenantTransaction(ctx, suppressTenantID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT source_event_id::text, action FROM alarm_runtime.notification_outbox WHERE tenant_id=$1 AND alarm_id=$2 ORDER BY alarm_version, action`, suppressTenantID, created.AlarmID)
	if err != nil {
		t.Fatal(err)
	}
	type rowItem struct {
		id     string
		action NotificationAction
	}
	var items []rowItem
	for rows.Next() {
		var item rowItem
		if err := rows.Scan(&item.id, &item.action); err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	rows.Close()

	want := []NotificationAction{NotificationCreated, NotificationSuppressed, NotificationUnsuppressed}
	if len(items) != len(want) {
		t.Fatalf("got %d outbox events %#v, want %v", len(items), items, want)
	}
	for i, wantAction := range want {
		if items[i].action != wantAction {
			t.Fatalf("outbox[%d].action = %q, want %q", i, items[i].action, wantAction)
		}
		event, err := loadNotificationOutboxEvent(ctx, tx, items[i].id, false)
		if err != nil {
			t.Fatal(err)
		}
		if event.Attributes["deviceId"] != deviceID {
			t.Fatalf("outbox[%d] deviceId = %q, want %q", i, event.Attributes["deviceId"], deviceID)
		}
		if wantAction == NotificationSuppressed && event.Attributes["suppressedUntil"] != suppressExpires {
			t.Fatalf("outbox[%d] suppressedUntil = %q, want %q", i, event.Attributes["suppressedUntil"], suppressExpires)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
