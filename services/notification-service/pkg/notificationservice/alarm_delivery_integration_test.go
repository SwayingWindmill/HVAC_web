package notificationservice

import (
	"context"
	"errors"
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
