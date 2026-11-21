package notificationservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// Alarm delivery on PostgreSQL (P0-4, docs/specs/alarm-delivery.md), replaying the 11-01 松雅轩
// gateway offline at 14:26 Beijing time that nobody was told of.

type fakeDeliverySender struct {
	mu       sync.Mutex
	sent     []OutboundMessage
	outcomes map[string][]error
}

func (fake *fakeDeliverySender) SendAlarmMessage(_ context.Context, message OutboundMessage) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	target := string(message.Channel) + ":" + message.To
	if queued := fake.outcomes[target]; len(queued) > 0 {
		fake.outcomes[target] = queued[1:]
		if queued[0] != nil {
			return queued[0]
		}
	}
	fake.sent = append(fake.sent, message)
	return nil
}

func (fake *fakeDeliverySender) take() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var out []string
	for _, message := range fake.sent {
		kind := strings.SplitN(message.Key, ":", 3)[1]
		out = append(out, kind+" "+string(message.Channel)+" "+message.To)
	}
	fake.sent = nil
	sort.Strings(out)
	return out
}

func (fake *fakeDeliverySender) takeOutbound() []OutboundMessage {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	out := append([]OutboundMessage(nil), fake.sent...)
	fake.sent = nil
	return out
}

func (fixture *deliveryFixture) runOutbound(at time.Time) []OutboundMessage {
	fixture.t.Helper()
	for range 100 {
		worked, err := fixture.dispatcher.RunOnce(context.Background(), at)
		if err != nil {
			fixture.t.Fatal(err)
		}
		if !worked {
			return fixture.sender.takeOutbound()
		}
	}
	fixture.t.Fatal("the dispatcher never ran out of work")
	return nil
}

type deliveryFixture struct {
	t          *testing.T
	reminders  *reminderFixture
	dispatcher *AlarmDeliveryDispatcher
	sender     *fakeDeliverySender
	siteID     string
	versions   map[string]int64
}

func newDeliveryFixture(t *testing.T, mode DeliveryMode) *deliveryFixture {
	t.Helper()
	reminders := newReminderFixture(t)
	fixture := &deliveryFixture{t: t, reminders: reminders, sender: &fakeDeliverySender{outcomes: map[string][]error{}}, siteID: reminders.uuid(), versions: map[string]int64{}}
	routing := testRouting(t)
	routing.SiteID = fixture.siteID
	recipients := RecipientDirectory{Recipients: map[string]Recipient{
		"duty": {Name: "值班", Email: "duty@example.test", WeComUserID: "duty-wecom"},
		"lead": {Name: "主管", WeComUserID: "lead-wecom"},
	}}
	if err := routing.checkRecipients(recipients); err != nil {
		t.Fatal(err)
	}
	fixture.dispatcher = &AlarmDeliveryDispatcher{
		Scheduler: reminders.worker.Scheduler, Routing: NewAlarmRoutingTable(routing), Recipients: recipients,
		Sender: fixture.sender, Mode: mode, ConsoleURL: "https://console.example.test",
	}
	return fixture
}

func (fixture *deliveryFixture) event(alarmID, alarmType string, action AlarmAction, severity alarmmodel.Severity, at time.Time) {
	fixture.t.Helper()
	fixture.versions[alarmID]++
	fixture.record(alarmID, alarmType, action, severity, at, fixture.versions[alarmID])
}

func (fixture *deliveryFixture) record(alarmID, alarmType string, action AlarmAction, severity alarmmodel.Severity, at time.Time, version int64) {
	fixture.t.Helper()
	fixture.recordWithAttrs(alarmID, alarmType, action, severity, at, version, nil)
}

func (fixture *deliveryFixture) recordWithAttrs(alarmID, alarmType string, action AlarmAction, severity alarmmodel.Severity, at time.Time, version int64, extra map[string]string) {
	fixture.t.Helper()
	condition := alarmmodel.ConditionActive
	if action == AlarmCleared {
		condition = alarmmodel.ConditionCleared
	}
	attrs := map[string]string{
		"title": "网关上报中断", "alarmType": alarmType, "summary": "松雅轩 15 分钟没有收到任何读数。",
		"acknowledgedBy": "王工", "firstOccurredAt": at.UTC().Format(time.RFC3339Nano),
	}
	for k, v := range extra {
		attrs[k] = v
	}
	event := AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: fixture.siteID, SourceEventID: fixture.reminders.uuid(), AlarmID: alarmID, IncidentCorrelationID: alarmID,
		AlarmVersion: version, Action: action, CurrentSeverity: severity, PeakSeverity: severity, Condition: condition, OccurredAt: at,
		Attributes: attrs,
	}
	if err := fixture.reminders.store.RecordAlarmEvent(context.Background(), event, at); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *deliveryFixture) run(at time.Time) []string {
	fixture.t.Helper()
	for range 100 {
		worked, err := fixture.dispatcher.RunOnce(context.Background(), at)
		if err != nil {
			fixture.t.Fatal(err)
		}
		if !worked {
			return fixture.sender.take()
		}
	}
	fixture.t.Fatal("the dispatcher never ran out of work")
	return nil
}

func (fixture *deliveryFixture) states(alarmID string) string {
	fixture.t.Helper()
	rows, err := fixture.dispatcher.Scheduler.pool.Query(context.Background(), `SELECT kind || ' ' || channel || ' ' || recipient || ' ' || state || coalesce(' ' || reason, '')
FROM notification_runtime.alarm_delivery_attempt WHERE alarm_id = $1::uuid ORDER BY 1`, alarmID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			fixture.t.Fatal(err)
		}
		out = append(out, line)
	}
	return strings.Join(out, "; ")
}

func equal(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestPostgresGatewayOfflineIsToldEscalatedAcknowledgedAndRecovered(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	offline := fixture.reminders.uuid()
	stale := fixture.reminders.uuid()
	fixture.event(offline, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase)
	fixture.event(stale, "readings-not-updated/chiller-a", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(30*time.Second))

	equal(t, "during the hold", fixture.run(deliveryBase.Add(time.Minute)))
	equal(t, "when the hold ends, the gateway offline only", fixture.run(deliveryBase.Add(4*time.Minute)),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")
	// The dispatcher sees the Alarm again on every event: the same messages are not queued twice.
	fixture.record(offline, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1)
	equal(t, "seen again", fixture.run(deliveryBase.Add(5*time.Minute)))
	equal(t, "nobody acknowledged in 15 minutes", fixture.run(deliveryBase.Add(19*time.Minute)),
		"ESCALATE wecom_app lead-wecom", "ESCALATE wecom_bot ")

	fixture.event(offline, "reports-interrupted", AlarmAcknowledged, alarmmodel.SeverityMajor, deliveryBase.Add(25*time.Minute))
	equal(t, "everyone told hears it was acknowledged", fixture.run(deliveryBase.Add(25*time.Minute)),
		"ACK smtp duty@example.test", "ACK wecom_app duty-wecom", "ACK wecom_app lead-wecom", "ACK wecom_bot ")
	fixture.event(stale, "readings-not-updated/chiller-a", AlarmCleared, alarmmodel.SeverityMajor, deliveryBase.Add(46*time.Minute))
	fixture.event(offline, "reports-interrupted", AlarmCleared, alarmmodel.SeverityMajor, deliveryBase.Add(47*time.Minute))
	equal(t, "and that it recovered", fixture.run(deliveryBase.Add(47*time.Minute)),
		"RECOVERY smtp duty@example.test", "RECOVERY wecom_app duty-wecom", "RECOVERY wecom_app lead-wecom", "RECOVERY wecom_bot ")
	equal(t, "nothing later", fixture.run(deliveryBase.Add(3*time.Hour)))
	if states := fixture.states(stale); states != "" {
		t.Fatalf("the stale readings were the gateway's doing and told nobody: %s", states)
	}
	if states := fixture.states(offline); strings.Contains(states, " SENT") || strings.Count(states, "DRY_RUN") != 12 {
		t.Fatalf("dry-run sends nothing: %s", states)
	}
}

func TestPostgresAnAcknowledgementBeforeTheCreationStillStopsEverything(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	alarmID := fixture.reminders.uuid()
	// The outbox hands over out of order: the acknowledgement (version 2) first.
	fixture.record(alarmID, "reports-interrupted", AlarmAcknowledged, alarmmodel.SeverityMajor, deliveryBase.Add(2*time.Minute), 2)
	fixture.record(alarmID, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1)
	equal(t, "acknowledged before anyone was told", fixture.run(deliveryBase.Add(30*time.Minute)))
	var began time.Time
	var acknowledged *time.Time
	if err := fixture.dispatcher.Scheduler.pool.QueryRow(context.Background(), `SELECT began_at, acknowledged_at FROM notification_runtime.alarm_delivery WHERE alarm_id = $1::uuid`, alarmID).Scan(&began, &acknowledged); err != nil {
		t.Fatal(err)
	}
	if !began.Equal(deliveryBase) || acknowledged == nil {
		t.Fatalf("began %v acknowledged %v", began, acknowledged)
	}
}

func TestPostgresLiveSendsAreRetriedOrGivenUp(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryLive)
	down := errors.New("adapter down")
	fixture.sender.outcomes["wecom_app:duty-wecom"] = []error{down, down, down, down}
	fixture.sender.outcomes["smtp:duty@example.test"] = []error{down, nil}
	alarmID := fixture.reminders.uuid()
	fixture.event(alarmID, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase)
	equal(t, "both fail first", fixture.run(deliveryBase.Add(3*time.Minute)))
	equal(t, "mail goes through a minute later", fixture.run(deliveryBase.Add(4*time.Minute)), "FIRE smtp duty@example.test")
	for _, minutes := range []int{8, 13, 30} {
		fixture.run(deliveryBase.Add(time.Duration(minutes) * time.Minute))
	}
	if states := fixture.states(alarmID); !strings.Contains(states, "FIRE smtp duty SENT") || !strings.Contains(states, "FIRE wecom_app duty FAILED GAVE_UP") {
		t.Fatalf("states %s", states)
	}
	rejected := fixture.reminders.uuid()
	fixture.sender.outcomes["wecom_app:duty-wecom"] = []error{ErrDeliveryRejected}
	fixture.event(rejected, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase.Add(time.Hour))
	fixture.run(deliveryBase.Add(time.Hour + 3*time.Minute))
	if states := fixture.states(rejected); !strings.Contains(states, "FIRE wecom_app duty FAILED REJECTED") {
		t.Fatalf("a rejected message is not retried: %s", states)
	}
}

func TestPostgresDeliveryIsTenantScopedAndForced(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	alarmID := fixture.reminders.uuid()
	fixture.event(alarmID, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase)
	fixture.run(deliveryBase.Add(4 * time.Minute))
	ctx := context.Background()
	other, err := fixture.reminders.store.beginTenant(ctx, "0190f000-0050-7000-8000-0000000000ff")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(ctx)
	var seen int
	if err := other.QueryRow(ctx, `SELECT count(*) FROM notification_runtime.alarm_delivery WHERE alarm_id = $1::uuid`, alarmID).Scan(&seen); err != nil || seen != 0 {
		t.Fatalf("another Tenant sees %d: %v", seen, err)
	}
	if _, err := other.Exec(ctx, `SELECT 1 FROM notification_runtime.alarm_delivery_attempt LIMIT 1`); err == nil {
		t.Fatal("the runtime role reads messages")
	}
	var forced int
	if err := fixture.dispatcher.Scheduler.pool.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'notification_runtime' AND c.relname IN ('alarm_delivery','alarm_delivery_attempt') AND c.relrowsecurity AND c.relforcerowsecurity`).Scan(&forced); err != nil || forced != 2 {
		t.Fatalf("row security forced on %d tables: %v", forced, err)
	}
	if _, err := fixture.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery SET acknowledged_at = now() WHERE alarm_id = $1::uuid`, alarmID); err == nil {
		t.Fatal("the dispatcher may not acknowledge an Alarm")
	}
}

func TestPostgresSendingLeaseReclaimsOrGivesUpAfterAttemptsExhausted(t *testing.T) {
	ctx := context.Background()

	t.Run("reclaims expired or null lease under retry cap", func(t *testing.T) {
		fixture := newDeliveryFixture(t, DeliveryLive)
		alarmID := fixture.reminders.uuid()
		fixture.event(alarmID, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase)
		fixture.sender.outcomes["wecom_app:duty-wecom"] = []error{ErrDeliveryRejected}
		fixture.sender.outcomes["smtp:duty@example.test"] = []error{errors.New("simulated pre-crash error")}
		fixture.run(deliveryBase.Add(3 * time.Minute))

		smtpKey := alarmID + ":FIRE:0:smtp:duty"
		expiredAt := deliveryBase.Add(5 * time.Minute)
		if _, err := fixture.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'SENDING', attempts = 1, next_attempt_at = $2, lease_until = $2
WHERE tenant_id = $1::uuid AND dedup_key = $3`, alarmReminderTenantID, expiredAt.UTC(), smtpKey); err != nil {
			t.Fatal(err)
		}
		equal(t, "expired SENDING lease under retry cap is reclaimed and sent",
			fixture.run(deliveryBase.Add(6*time.Minute)), "FIRE smtp duty@example.test")
		if states := fixture.states(alarmID); !strings.Contains(states, "FIRE smtp duty SENT") {
			t.Fatalf("expected reclaimed attempt to be SENT, got %s", states)
		}

		// Also verify a SENDING row with NULL lease_until cannot stay stranded.
		nullLeaseAlarmID := fixture.reminders.uuid()
		fixture.event(nullLeaseAlarmID, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase.Add(time.Hour))
		fixture.sender.outcomes["wecom_app:duty-wecom"] = []error{ErrDeliveryRejected}
		fixture.sender.outcomes["smtp:duty@example.test"] = []error{errors.New("simulated pre-crash error")}
		fixture.run(deliveryBase.Add(time.Hour + 3*time.Minute))
		nullKey := nullLeaseAlarmID + ":FIRE:0:smtp:duty"
		if _, err := fixture.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'SENDING', attempts = 1, lease_until = NULL
WHERE tenant_id = $1::uuid AND dedup_key = $2`, alarmReminderTenantID, nullKey); err != nil {
			t.Fatal(err)
		}
		equal(t, "null SENDING lease is reclaimed and sent",
			fixture.run(deliveryBase.Add(time.Hour+4*time.Minute)), "FIRE smtp duty@example.test")
	})

	t.Run("gives up on expired lease when attempts already exhausted", func(t *testing.T) {
		fixture := newDeliveryFixture(t, DeliveryLive)
		alarmID := fixture.reminders.uuid()
		fixture.event(alarmID, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase)
		fixture.sender.outcomes["wecom_app:duty-wecom"] = []error{ErrDeliveryRejected}
		fixture.sender.outcomes["smtp:duty@example.test"] = []error{errors.New("simulated pre-crash error")}
		fixture.run(deliveryBase.Add(3 * time.Minute))

		smtpKey := alarmID + ":FIRE:0:smtp:duty"
		if _, err := fixture.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'SENDING', attempts = $3, next_attempt_at = $2, lease_until = $2
WHERE tenant_id = $1::uuid AND dedup_key = $4`, alarmReminderTenantID, deliveryBase.Add(5*time.Minute).UTC(), deliveryAttempts, smtpKey); err != nil {
			t.Fatal(err)
		}
		equal(t, "exhausted SENDING lease must not send past deliveryAttempts", fixture.run(deliveryBase.Add(6*time.Minute)))
		if states := fixture.states(alarmID); !strings.Contains(states, "FIRE smtp duty FAILED GAVE_UP") {
			t.Fatalf("expected exhausted SENDING lease to become FAILED GAVE_UP, got %s", states)
		}
	})

	t.Run("stale replay during hold preserves next_due_at", func(t *testing.T) {
		fixture := newDeliveryFixture(t, DeliveryDryRun)
		holdAlarmID := fixture.reminders.uuid()
		fixture.record(holdAlarmID, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1)
		equal(t, "held before hold window ends", fixture.run(deliveryBase.Add(time.Minute)))
		fixture.record(holdAlarmID, "reports-interrupted", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(2*time.Minute), 1)
		var nextDue time.Time
		if err := fixture.dispatcher.Scheduler.pool.QueryRow(ctx, `SELECT next_due_at FROM notification_runtime.alarm_delivery WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, alarmReminderTenantID, holdAlarmID).Scan(&nextDue); err != nil {
			t.Fatal(err)
		}
		if !nextDue.Equal(deliveryBase.Add(3 * time.Minute)) {
			t.Fatalf("stale replay overwrote hold next_due_at: got %v, want %v", nextDue, deliveryBase.Add(3*time.Minute))
		}
	})
}

func TestPostgresMaintenanceSuppressionHoldsResumesAndHandlesOutOfOrder(t *testing.T) {
	ctx := context.Background()
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	alarmID := fixture.reminders.uuid()
	suppressUntil := deliveryBase.Add(2 * time.Hour)

	fixture.recordWithAttrs(alarmID, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{
		"deviceId": "chiller-a",
	})
	fixture.recordWithAttrs(alarmID, "physics/cop-out-of-range", AlarmSuppressed, alarmmodel.SeverityMajor, deliveryBase.Add(time.Minute), 2, map[string]string{
		"deviceId":        "chiller-a",
		"suppressedUntil": suppressUntil.UTC().Format(time.RFC3339Nano),
	})
	equal(t, "held during maintenance suppression window", fixture.run(deliveryBase.Add(5*time.Minute)))

	var storedDevice string
	var storedUntil *time.Time
	var storedNextDue *time.Time
	if err := fixture.dispatcher.Scheduler.pool.QueryRow(ctx, `SELECT COALESCE(device_id, ''), suppressed_until, next_due_at
FROM notification_runtime.alarm_delivery WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, alarmReminderTenantID, alarmID).Scan(&storedDevice, &storedUntil, &storedNextDue); err != nil {
		t.Fatal(err)
	}
	if storedDevice != "chiller-a" || storedUntil == nil || !storedUntil.Equal(suppressUntil) || storedNextDue == nil || !storedNextDue.Equal(suppressUntil) {
		t.Fatalf("unexpected suppressed state: device=%q until=%v nextDue=%v", storedDevice, storedUntil, storedNextDue)
	}

	// Scheduler role may read device_id and suppressed_until, but must NOT update them directly.
	if _, err := fixture.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery SET suppressed_until = NULL WHERE alarm_id = $1::uuid`, alarmID); err == nil {
		t.Fatal("scheduler role must not update suppressed_until")
	}

	// Unsuppress (v3), then replay stale SUPPRESSED (v2) out-of-order: stale suppression must not come back.
	unsuppressAt := deliveryBase.Add(30 * time.Minute)
	fixture.recordWithAttrs(alarmID, "physics/cop-out-of-range", AlarmUnsuppressed, alarmmodel.SeverityMajor, unsuppressAt, 3, nil)
	fixture.recordWithAttrs(alarmID, "physics/cop-out-of-range", AlarmSuppressed, alarmmodel.SeverityMajor, deliveryBase.Add(2*time.Minute), 2, map[string]string{
		"suppressedUntil": suppressUntil.UTC().Format(time.RFC3339Nano),
	})
	if err := fixture.dispatcher.Scheduler.pool.QueryRow(ctx, `SELECT suppressed_until FROM notification_runtime.alarm_delivery
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, alarmReminderTenantID, alarmID).Scan(&storedUntil); err != nil {
		t.Fatal(err)
	}
	if storedUntil != nil {
		t.Fatalf("stale SUPPRESSED event restored suppressed_until: %v", storedUntil)
	}
	equal(t, "unsuppressed alarm delivers immediately", fixture.run(unsuppressAt),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")
	fixture.event(alarmID, "physics/cop-out-of-range", AlarmCleared, alarmmodel.SeverityMajor, unsuppressAt.Add(5*time.Minute))
	equal(t, "unsuppressed alarm clears cleanly", fixture.run(unsuppressAt.Add(5*time.Minute)),
		"RECOVERY smtp duty@example.test", "RECOVERY wecom_app duty-wecom")

	// Second alarm created already suppressed until expiry resumes automatically when suppressedUntil elapses.
	expiredAlarmID := fixture.reminders.uuid()
	windowEnd := deliveryBase.Add(90 * time.Minute)
	fixture.recordWithAttrs(expiredAlarmID, "physics/pump-pressure", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{
		"deviceId":        "pump-1",
		"suppressedUntil": windowEnd.UTC().Format(time.RFC3339Nano),
	})
	equal(t, "suppressed at creation", fixture.run(deliveryBase.Add(10*time.Minute)))
	equal(t, "resumes after suppressedUntil elapses", fixture.run(windowEnd),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")
}

func TestPostgresRouteCooldownDeduplicatesSameTypeAndPreservesEscalationAndRecovery(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	routing := testRouting(t)
	routing.SiteID = fixture.siteID
	routing.RateLimitPerHour = 10
	routing.Routes[0].CooldownMinutes = 10
	fixture.dispatcher.Routing = NewAlarmRoutingTable(routing)

	alarmA := fixture.reminders.uuid()
	alarmB := fixture.reminders.uuid()
	alarmC := fixture.reminders.uuid()
	alarmCrit := fixture.reminders.uuid()

	fixture.event(alarmA, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase)
	equal(t, "first alarm fires", fixture.run(deliveryBase.Add(3*time.Minute)),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")

	// Second alarm of same type within 10m cooldown is recorded as SUPPRESSED COOLDOWN.
	fixture.event(alarmB, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(5*time.Minute))
	equal(t, "second alarm within cooldown is suppressed", fixture.run(deliveryBase.Add(8*time.Minute)))
	if states := fixture.states(alarmB); !strings.Contains(states, "FIRE smtp duty SUPPRESSED COOLDOWN") || !strings.Contains(states, "FIRE wecom_app duty SUPPRESSED COOLDOWN") {
		t.Fatalf("expected COOLDOWN attempts recorded for alarmB, got %s", states)
	}

	// CRITICAL alarm of same type during cooldown bypasses cooldown.
	fixture.event(alarmCrit, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase.Add(6*time.Minute))
	equal(t, "CRITICAL bypasses cooldown", fixture.run(deliveryBase.Add(9*time.Minute)),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")

	// Alarm A escalation and recovery stay unaffected by cooldown.
	equal(t, "alarm A escalates normally", fixture.run(deliveryBase.Add(18*time.Minute)),
		"ESCALATE wecom_app lead-wecom", "ESCALATE wecom_bot ")
	fixture.event(alarmA, "physics/cop-out-of-range", AlarmCleared, alarmmodel.SeverityMajor, deliveryBase.Add(19*time.Minute))
	equal(t, "alarm A recovery notifies all reached", fixture.run(deliveryBase.Add(19*time.Minute)),
		"RECOVERY smtp duty@example.test", "RECOVERY wecom_app duty-wecom", "RECOVERY wecom_app lead-wecom", "RECOVERY wecom_bot ")

	// Wait past 10m from alarmCrit (fired at +9m) -> alarm C at +20m fires normally.
	fixture.event(alarmC, "physics/cop-out-of-range", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(17*time.Minute))
	equal(t, "alarm after cooldown expires fires normally", fixture.run(deliveryBase.Add(20*time.Minute)),
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom")
}

func TestPostgresParentDeviceTopologyRootCauseSuppression(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	routing := testRouting(t)
	routing.SiteID = fixture.siteID
	routing.RateLimitPerHour = 10
	routing.RootCauseRules = []RootCauseRule{
		{
			WhenActivePrefix:  "power-outage",
			WhenDeviceID:      "main-panel",
			SuppressPrefixes:  []string{"reading-gap/"},
			SuppressDeviceIDs: []string{"chiller-a"},
		},
	}
	fixture.dispatcher.Routing = NewAlarmRoutingTable(routing)

	panelAlarm := fixture.reminders.uuid()
	chillerAAlarm := fixture.reminders.uuid()
	chillerBAlarm := fixture.reminders.uuid()

	fixture.recordWithAttrs(panelAlarm, "power-outage", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{"deviceId": "main-panel"})
	fixture.recordWithAttrs(chillerAAlarm, "reading-gap/chiller-a/temp", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(10*time.Second), 1, map[string]string{"deviceId": "chiller-a"})
	fixture.recordWithAttrs(chillerBAlarm, "reading-gap/chiller-b/temp", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(20*time.Second), 1, map[string]string{"deviceId": "chiller-b"})

	// Wait past hold: main-panel power-outage and unlisted chiller-b fire, while chiller-a is held as ROOT_CAUSE.
	got := fixture.run(deliveryBase.Add(4 * time.Minute))
	equal(t, "only panel and unlisted chiller-b notify", got,
		"FIRE smtp duty@example.test", "FIRE smtp duty@example.test",
		"FIRE wecom_app duty-wecom", "FIRE wecom_app duty-wecom")
	if states := fixture.states(chillerAAlarm); states != "" {
		t.Fatalf("expected chiller-a to have zero attempts while root cause active, got %s", states)
	}

	// Clearing main-panel root cause releases chiller-a on next run.
	fixture.recordWithAttrs(panelAlarm, "power-outage", AlarmCleared, alarmmodel.SeverityMajor, deliveryBase.Add(5*time.Minute), 2, map[string]string{"deviceId": "main-panel"})
	gotAfterClear := fixture.run(deliveryBase.Add(6 * time.Minute))
	equal(t, "panel recovery and released chiller-a fire", gotAfterClear,
		"FIRE smtp duty@example.test", "FIRE wecom_app duty-wecom",
		"RECOVERY smtp duty@example.test", "RECOVERY wecom_app duty-wecom")
}

func TestPostgresAlarmGroupingWindowBatchDeliveryMaxItemsAndCriticalImmediate(t *testing.T) {
	ctx := context.Background()
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	routing := testRouting(t)
	routing.SiteID = fixture.siteID
	routing.RateLimitPerHour = 20
	routing.Routes[0].Steps = routing.Routes[0].Steps[:1]
	routing.Routes[0].GroupWindowSeconds = 60
	routing.Routes[0].GroupMaxItems = 3
	routing.Routes[0].GroupBy = "severity"
	routing.Routes[1].GroupWindowSeconds = 60
	routing.Routes[1].GroupMaxItems = 3
	routing.Routes[1].GroupBy = "severity"
	fixture.dispatcher.Routing = NewAlarmRoutingTable(routing)

	// Four MAJOR alarms (exceeding GroupMaxItems=3 -> batch of 3 + batch of 1), one CRITICAL (immediate ungrouped), one WARNING (separate severity group).
	major1 := fixture.reminders.uuid()
	major2 := fixture.reminders.uuid()
	major3 := fixture.reminders.uuid()
	major4 := fixture.reminders.uuid()
	crit1 := fixture.reminders.uuid()
	warn1 := fixture.reminders.uuid()

	fixture.recordWithAttrs(major1, "physics/cop-low", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{"title": "冷机1 COP偏低", "deviceId": "chiller-1"})
	fixture.recordWithAttrs(major2, "physics/pump-diff", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(5*time.Second), 1, map[string]string{"title": "冷冻泵压差高", "deviceId": "pump-1"})
	fixture.recordWithAttrs(major3, "physics/tower-fan", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(10*time.Second), 1, map[string]string{"title": "冷却塔风机故障", "deviceId": "tower-1"})
	fixture.recordWithAttrs(major4, "physics/chw-temp", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(15*time.Second), 1, map[string]string{"title": "冷冻水供水温度高", "deviceId": "chiller-2"})
	fixture.recordWithAttrs(crit1, "physics/chiller-trip", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase.Add(20*time.Second), 1, map[string]string{"title": "主机紧急停机", "deviceId": "chiller-1"})
	fixture.recordWithAttrs(warn1, "rules-unevaluable", AlarmCreated, alarmmodel.SeverityWarning, deliveryBase.Add(25*time.Second), 1, map[string]string{"title": "规则暂无法评估"})

	sent := fixture.runOutbound(deliveryBase.Add(4 * time.Minute))
	// Expected sends:
	// - major batch of 3: 1 smtp + 1 wecom_app
	// - major single (4th item): 1 smtp + 1 wecom_app
	// - critical single (immediate ungrouped): 1 smtp + 1 wecom_app
	// - warning single: 1 smtp
	// Total = 7 outbound messages instead of 11.
	if len(sent) != 7 {
		t.Fatalf("expected 7 outbound messages, got %d: %+v", len(sent), sent)
	}
	groupedMajorCount := 0
	criticalCount := 0
	for _, msg := range sent {
		if msg.Subject == "[松雅轩] 3 条重要告警汇总" {
			groupedMajorCount++
			if !strings.Contains(msg.Text, "查看告警：https://console.example.test/operations/alarms?site="+fixture.siteID) {
				t.Fatalf("grouped digest missing console URL: %s", msg.Text)
			}
		}
		if msg.Subject == "【告警·紧急】松雅轩：主机紧急停机" {
			criticalCount++
		}
	}
	if groupedMajorCount != 2 || criticalCount != 2 {
		t.Fatalf("expected 2 grouped major digests and 2 immediate critical sends, got grouped=%d critical=%d messages=%+v", groupedMajorCount, criticalCount, sent)
	}
	for _, id := range []string{major1, major2, major3, major4, crit1} {
		if st := fixture.states(id); !strings.Contains(st, "FIRE smtp duty DRY_RUN") || !strings.Contains(st, "FIRE wecom_app duty DRY_RUN") {
			t.Fatalf("expected DRY_RUN on both channels for %s, got %s", id, st)
		}
	}

	// Verify lookahead window (next_attempt_at <= now + groupWindowSeconds):
	// Schedule 3 attempts: primary at T, siblingA at T+30s (inside 60s window), siblingB at T+90s (outside 60s window).
	fixture2 := newDeliveryFixture(t, DeliveryDryRun)
	routing2 := testRouting(t)
	routing2.SiteID = fixture2.siteID
	routing2.RateLimitPerHour = 20
	routing2.Routes[0].Steps = routing2.Routes[0].Steps[:1]
	routing2.Routes[0].GroupWindowSeconds = 60
	routing2.Routes[0].GroupMaxItems = 10
	routing2.Routes[0].GroupBy = "severity"
	fixture2.dispatcher.Routing = NewAlarmRoutingTable(routing2)

	winBase := deliveryBase.Add(2 * time.Hour)
	primID := fixture2.reminders.uuid()
	sibInsideID := fixture2.reminders.uuid()
	sibOutsideID := fixture2.reminders.uuid()
	fixture2.recordWithAttrs(primID, "physics/a", AlarmCreated, alarmmodel.SeverityMajor, winBase, 1, map[string]string{"title": "窗口主告警"})
	fixture2.recordWithAttrs(sibInsideID, "physics/b", AlarmCreated, alarmmodel.SeverityMajor, winBase.Add(5*time.Second), 1, map[string]string{"title": "窗口内兄弟告警"})
	fixture2.recordWithAttrs(sibOutsideID, "physics/c", AlarmCreated, alarmmodel.SeverityMajor, winBase.Add(10*time.Second), 1, map[string]string{"title": "窗口外告警"})

	planAt := winBase.Add(4 * time.Minute)
	for {
		worked, err := fixture2.dispatcher.planOne(ctx, planAt)
		if err != nil {
			t.Fatalf("planOne failed: %v", err)
		}
		if !worked {
			break
		}
	}
	if _, err := fixture2.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt SET next_attempt_at = $2 WHERE tenant_id = $1::uuid AND alarm_id = $3::uuid`,
		alarmReminderTenantID, planAt.Add(30*time.Second).UTC(), sibInsideID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture2.dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt SET next_attempt_at = $2 WHERE tenant_id = $1::uuid AND alarm_id = $3::uuid`,
		alarmReminderTenantID, planAt.Add(90*time.Second).UTC(), sibOutsideID); err != nil {
		t.Fatal(err)
	}

	windowSent := fixture2.runOutbound(planAt)
	if len(windowSent) != 2 {
		t.Fatalf("expected 2 grouped messages (smtp + wecom_app) at planAt, got %d: %+v", len(windowSent), windowSent)
	}
	for _, msg := range windowSent {
		if msg.Subject != "[松雅轩] 2 条重要告警汇总" || !strings.Contains(msg.Text, "窗口内兄弟告警") || strings.Contains(msg.Text, "窗口外告警") {
			t.Fatalf("unexpected lookahead window digest: %+v", msg)
		}
	}
	if st := fixture2.states(sibOutsideID); !strings.Contains(st, "PENDING") {
		t.Fatalf("expected outside-window alarm to remain PENDING, got %s", st)
	}
	laterSent := fixture2.runOutbound(planAt.Add(90 * time.Second))
	if len(laterSent) != 2 {
		t.Fatalf("expected outside-window alarm to fire separately at +90s, got %+v", laterSent)
	}
}

func TestPostgresAlarmGroupingCoordinatedRetryAndGiveUp(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryLive)
	routing := testRouting(t)
	routing.SiteID = fixture.siteID
	routing.RateLimitPerHour = 20
	routing.Routes[0].Steps = routing.Routes[0].Steps[:1]
	routing.Routes[0].GroupWindowSeconds = 60
	routing.Routes[0].GroupMaxItems = 10
	routing.Routes[0].GroupBy = "severity"
	routing.Routes[0].Steps[0].Channels = []DeliveryChannel{ChannelSMTP}
	fixture.dispatcher.Routing = NewAlarmRoutingTable(routing)

	down := errors.New("adapter temporarily down")
	fixture.sender.outcomes["smtp:duty@example.test"] = []error{down, nil}

	alarmA := fixture.reminders.uuid()
	alarmB := fixture.reminders.uuid()
	fixture.recordWithAttrs(alarmA, "physics/a", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{"title": "告警A"})
	fixture.recordWithAttrs(alarmB, "physics/b", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase.Add(5*time.Second), 1, map[string]string{"title": "告警B"})

	// First attempt fails (past 3m hold for both) -> both return to PENDING for retry at +1m.
	if sent := fixture.runOutbound(deliveryBase.Add(4 * time.Minute)); len(sent) != 0 {
		t.Fatalf("expected 0 sent on failure, got %+v", sent)
	}
	for _, id := range []string{alarmA, alarmB} {
		if st := fixture.states(id); !strings.Contains(st, "FIRE smtp duty PENDING") {
			t.Fatalf("expected coordinated retry PENDING for %s, got %s", id, st)
		}
	}

	// Retry 1m later succeeds -> both settle to SENT with one grouped send.
	retried := fixture.runOutbound(deliveryBase.Add(5 * time.Minute))
	if len(retried) != 1 || retried[0].Subject != "[松雅轩] 2 条重要告警汇总" {
		t.Fatalf("expected 1 grouped retry send, got %+v", retried)
	}
	for _, id := range []string{alarmA, alarmB} {
		if st := fixture.states(id); !strings.Contains(st, "FIRE smtp duty SENT") {
			t.Fatalf("expected SENT for %s, got %s", id, st)
		}
	}

	// Subsequent batch that exhausts all 4 attempts settles both as FAILED GAVE_UP.
	fixture.sender.outcomes["smtp:duty@example.test"] = []error{down, down, down, down}
	alarmC := fixture.reminders.uuid()
	alarmD := fixture.reminders.uuid()
	base2 := deliveryBase.Add(2 * time.Hour)
	fixture.recordWithAttrs(alarmC, "physics/c", AlarmCreated, alarmmodel.SeverityMajor, base2, 1, map[string]string{"title": "告警C"})
	fixture.recordWithAttrs(alarmD, "physics/d", AlarmCreated, alarmmodel.SeverityMajor, base2.Add(5*time.Second), 1, map[string]string{"title": "告警D"})
	for _, offset := range []time.Duration{4 * time.Minute, 5 * time.Minute, 9 * time.Minute, 18 * time.Minute} {
		if sent := fixture.runOutbound(base2.Add(offset)); len(sent) != 0 {
			t.Fatalf("expected 0 sent during failure exhaust at +%v, got %+v", offset, sent)
		}
	}
	for _, id := range []string{alarmC, alarmD} {
		if st := fixture.states(id); !strings.Contains(st, "FIRE smtp duty FAILED GAVE_UP") {
			t.Fatalf("expected coordinated FAILED GAVE_UP for %s, got %s", id, st)
		}
	}
}

func TestPostgresAlarmGroupingKeysAndReloaderHotSwap(t *testing.T) {
	fixture := newDeliveryFixture(t, DeliveryDryRun)
	dir := t.TempDir()
	recipientsPath := filepath.Join(dir, "recipients.json")
	routingDir := filepath.Join(dir, "sites")
	if err := os.MkdirAll(routingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientsPath, []byte(`{"recipients":{"duty":{"name":"值班","email":"duty-v1@example.test","wecomUserId":"duty-v1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	siteRouting := SiteRouting{
		SchemaVersion:    1,
		SiteID:           fixture.siteID,
		SiteName:         "松雅轩",
		Timezone:         "Asia/Shanghai",
		HoldMinutes:      3,
		MaxAgeHours:      12,
		RateLimitPerHour: 20,
		Routes: []AlarmRoute{
			{
				Name:               "all-grouped-by-site",
				MinSeverity:        alarmmodel.SeverityMinor,
				GroupWindowSeconds: 60,
				GroupMaxItems:      10,
				GroupBy:            "site",
				Steps:              []EscalationStep{{AfterMinutes: 0, Recipients: []string{"duty"}, Channels: []DeliveryChannel{ChannelSMTP}}},
			},
		},
	}
	initialJSON, _ := json.Marshal(siteRouting)
	routingFile := filepath.Join(routingDir, "site.alarm-routing.json")
	if err := os.WriteFile(routingFile, initialJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	reloader, err := NewRoutingReloader(recipientsPath, routingDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	reloader.logger.Store(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	fixture.dispatcher.Reloader = reloader

	// Under groupBy=site, MAJOR + MINOR merge together, while CRITICAL still stays ungrouped.
	majorID := fixture.reminders.uuid()
	minorID := fixture.reminders.uuid()
	critID := fixture.reminders.uuid()
	fixture.recordWithAttrs(majorID, "physics/a", AlarmCreated, alarmmodel.SeverityMajor, deliveryBase, 1, map[string]string{"title": "重要告警"})
	fixture.recordWithAttrs(minorID, "physics/b", AlarmCreated, alarmmodel.SeverityMinor, deliveryBase.Add(5*time.Second), 1, map[string]string{"title": "次要告警"})
	fixture.recordWithAttrs(critID, "physics/c", AlarmCreated, alarmmodel.SeverityCritical, deliveryBase.Add(10*time.Second), 1, map[string]string{"title": "紧急告警"})

	sent1 := fixture.runOutbound(deliveryBase.Add(4 * time.Minute))
	if len(sent1) != 2 {
		t.Fatalf("expected 1 grouped (MAJOR+MINOR) + 1 immediate CRITICAL = 2 messages, got %+v", sent1)
	}
	for _, msg := range sent1 {
		if msg.To != "duty-v1@example.test" {
			t.Fatalf("expected v1 recipient before hot reload, got %q", msg.To)
		}
	}

	// Hot-reload recipients email + switch groupBy to alarmType without recreating dispatcher.
	if err := os.WriteFile(recipientsPath, []byte(`{"recipients":{"duty":{"name":"值班","email":"duty-v2@example.test","wecomUserId":"duty-v2"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	siteRouting.Routes[0].GroupBy = "alarmType"
	updatedJSON, _ := json.Marshal(siteRouting)
	if err := os.WriteFile(routingFile, updatedJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _, err := reloader.ReloadNow(context.Background())
	if err != nil || !changed {
		t.Fatalf("ReloadNow failed: changed=%v err=%v", changed, err)
	}

	base2 := deliveryBase.Add(time.Hour)
	sameType1 := fixture.reminders.uuid()
	sameType2 := fixture.reminders.uuid()
	otherType := fixture.reminders.uuid()
	fixture.recordWithAttrs(sameType1, "physics/cop-low", AlarmCreated, alarmmodel.SeverityMajor, base2, 1, map[string]string{"title": "冷机1 COP低"})
	fixture.recordWithAttrs(sameType2, "physics/cop-low", AlarmCreated, alarmmodel.SeverityMajor, base2.Add(5*time.Second), 1, map[string]string{"title": "冷机2 COP低"})
	fixture.recordWithAttrs(otherType, "physics/pump-high", AlarmCreated, alarmmodel.SeverityMajor, base2.Add(10*time.Second), 1, map[string]string{"title": "水泵电流高"})

	sent2 := fixture.runOutbound(base2.Add(4 * time.Minute))
	if len(sent2) != 2 {
		t.Fatalf("expected 1 grouped alarmType digest + 1 separate alarmType = 2 messages, got %+v", sent2)
	}
	for _, msg := range sent2 {
		if msg.To != "duty-v2@example.test" {
			t.Fatalf("expected hot-reloaded v2 recipient, got %q", msg.To)
		}
	}
}
