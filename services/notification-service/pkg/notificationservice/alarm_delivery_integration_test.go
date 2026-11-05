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
	condition := alarmmodel.ConditionActive
	if action == AlarmCleared {
		condition = alarmmodel.ConditionCleared
	}
	event := AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: fixture.siteID, SourceEventID: fixture.reminders.uuid(), AlarmID: alarmID, IncidentCorrelationID: alarmID,
		AlarmVersion: version, Action: action, CurrentSeverity: severity, PeakSeverity: severity, Condition: condition, OccurredAt: at,
		Attributes: map[string]string{"title": "网关上报中断", "alarmType": alarmType, "summary": "松雅轩 15 分钟没有收到任何读数。", "acknowledgedBy": "王工"},
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
