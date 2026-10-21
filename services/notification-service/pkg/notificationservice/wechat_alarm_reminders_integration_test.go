package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// WeChat reminders of Alarms (#664, docs/specs/miniprogram-wechat.md): a new urgent or important
// Alarm is sent once to everyone in the Tenant who allowed an alarm reminder and can read the
// Site's Alarms now; nobody is reminded more than 30 minutes after the Alarm began.

const (
	alarmReminderTenantID = "0190f000-0050-7000-8000-000000000001"
	alarmReminderSiteID   = "01910000-0050-7000-8000-000000000001"
	alarmReminderIssuer   = "https://platform.example.test/identity"
)

// fakeRecipients allows the listed subjects, for Alarms and for daily summaries, and remembers
// whom it was asked about.
type fakeRecipients struct {
	mu           sync.Mutex
	allowed      map[string]bool
	allowedDaily map[string]bool
	asked        [][]string
}

func (fake *fakeRecipients) AllowedRecipients(_ context.Context, kind ReminderKind, tenantID, siteID, alarmID string, people []WeChatPerson) ([]WeChatPerson, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	allowedSubjects := fake.allowed
	if kind == ReminderDaily {
		allowedSubjects = fake.allowedDaily
	}
	var subjects []string
	var allowed []WeChatPerson
	for _, person := range people {
		subjects = append(subjects, person.Subject)
		if allowedSubjects[person.Subject] && person.TenantID == tenantID {
			allowed = append(allowed, person)
		}
	}
	fake.asked = append(fake.asked, subjects)
	return allowed, nil
}

type fakeSites struct{}

func (fakeSites) Site(_ context.Context, tenantID, siteID string) (SiteFacts, error) {
	return SiteFacts{Name: "松雅轩", Timezone: "Asia/Shanghai"}, nil
}

// fakeSender records each message, and answers each subject the way it is told to.
type fakeSender struct {
	mu       sync.Mutex
	sent     []sentReminder
	outcomes map[string][]error
	// during runs while a message is on its way, as another worker would.
	during func()
}

type sentReminder struct {
	subject string
	message WeChatMessage
}

var errWeChatDown = errors.New("WeChat is unreachable")

func (fake *fakeSender) SendWeChatMessage(_ context.Context, person WeChatPerson, message WeChatMessage) error {
	if during := fake.during; during != nil {
		fake.during = nil
		during()
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if queued := fake.outcomes[person.Subject]; len(queued) > 0 {
		fake.outcomes[person.Subject] = queued[1:]
		if queued[0] != nil {
			return queued[0]
		}
	}
	fake.sent = append(fake.sent, sentReminder{subject: person.Subject, message: message})
	return nil
}

// fakeEnergy answers each day's electricity and cooling; a day it does not know has none.
type fakeEnergy struct {
	days map[string][2]float64
}

func (fake fakeEnergy) DayEnergy(_ context.Context, tenantID, siteID, timezone string, day time.Time) (*float64, *float64, error) {
	energy, ok := fake.days[day.Format("2006-01-02")+" "+timezone]
	if !ok {
		return nil, nil, nil
	}
	return &energy[0], &energy[1], nil
}

type reminderFixture struct {
	t       *testing.T
	store   *PostgresStore
	worker  *WeChatReminderWorker
	sender  *fakeSender
	decider *fakeRecipients
	stamp   string
	base    time.Time
}

func newReminderFixture(t *testing.T) *reminderFixture {
	t.Helper()
	store := openNotificationTestStore(t)
	t.Cleanup(store.Close)
	scheduler, err := OpenScheduler(context.Background(), os.Getenv("S16_NOTIFICATION_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scheduler.Close)
	fixture := &reminderFixture{
		t: t, store: store, sender: &fakeSender{outcomes: map[string][]error{}}, decider: &fakeRecipients{allowed: map[string]bool{}, allowedDaily: map[string]bool{}},
		stamp: strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", ""),
		base:  time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
	fixture.worker = &WeChatReminderWorker{
		Scheduler: scheduler, Recipients: fixture.decider, Sites: fakeSites{}, Sender: fixture.sender,
		Energy:          fakeEnergy{days: map[string][2]float64{"2026-10-21 Asia/Shanghai": {1691, 7402}}},
		AlarmTemplateID: "alarm-template", DailyTemplateID: "daily-template",
	}
	return fixture
}

// person is a subject unique to this run, with this many alarm reminders allowed.
func (fixture *reminderFixture) person(name string, allowed int) WeChatPerson {
	fixture.t.Helper()
	person := WeChatPerson{TenantID: alarmReminderTenantID, SubjectIssuer: alarmReminderIssuer, Subject: name + "-" + fixture.stamp}
	for range allowed {
		if _, err := fixture.store.AddWeChatReminder(context.Background(), person, ReminderRequest{Kind: ReminderAlarm}, fixture.base); err != nil {
			fixture.t.Fatal(err)
		}
	}
	return person
}

// dailyPerson is a subject unique to this run with this many daily summaries of the Site allowed.
func (fixture *reminderFixture) dailyPerson(name string, allowed int, siteID string) WeChatPerson {
	fixture.t.Helper()
	person := WeChatPerson{TenantID: alarmReminderTenantID, SubjectIssuer: alarmReminderIssuer, Subject: name + "-" + fixture.stamp}
	for range allowed {
		if _, err := fixture.store.AddWeChatReminder(context.Background(), person, ReminderRequest{Kind: ReminderDaily, SiteID: siteID}, fixture.base); err != nil {
			fixture.t.Fatal(err)
		}
	}
	return person
}

func (fixture *reminderFixture) leftDaily(person WeChatPerson) int {
	fixture.t.Helper()
	reminders, err := fixture.store.WeChatReminders(context.Background(), person)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return reminders.Daily
}

func (fixture *reminderFixture) uuid() string {
	fixture.t.Helper()
	id, err := uuidv7(time.Now())
	if err != nil {
		fixture.t.Fatal(err)
	}
	return id
}

// alarm records an Alarm event as the relay does, and answers the Alarm's ID.
func (fixture *reminderFixture) alarm(action AlarmAction, severity alarmmodel.Severity, title string, at time.Time) string {
	fixture.t.Helper()
	alarmID := fixture.uuid()
	fixture.event(alarmID, action, severity, title, at)
	return alarmID
}

func (fixture *reminderFixture) event(alarmID string, action AlarmAction, severity alarmmodel.Severity, title string, at time.Time) {
	fixture.t.Helper()
	condition := alarmmodel.ConditionActive
	if action == AlarmCleared {
		condition = alarmmodel.ConditionCleared
	}
	event := AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: alarmReminderSiteID, SourceEventID: fixture.uuid(), AlarmID: alarmID, IncidentCorrelationID: alarmID,
		Action: action, CurrentSeverity: severity, PeakSeverity: severity, Condition: condition, OccurredAt: at,
		Attributes: map[string]string{"title": title},
	}
	if _, err := fixture.store.ProcessAlarmEvent(context.Background(), event, at); err != nil {
		fixture.t.Fatal(err)
	}
}

// run lets the worker do everything due at this moment.
func (fixture *reminderFixture) run(at time.Time) {
	fixture.t.Helper()
	for range 50 {
		worked, err := fixture.worker.RunOnce(context.Background(), at)
		if err != nil {
			fixture.t.Fatal(err)
		}
		if !worked {
			return
		}
	}
	fixture.t.Fatal("the reminder worker never ran out of work")
}

func (fixture *reminderFixture) left(person WeChatPerson) int {
	fixture.t.Helper()
	reminders, err := fixture.store.WeChatReminders(context.Background(), person)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return reminders.Alarm
}

func (fixture *reminderFixture) sentTo(subject string) []WeChatMessage {
	fixture.sender.mu.Lock()
	defer fixture.sender.mu.Unlock()
	var messages []WeChatMessage
	for _, sent := range fixture.sender.sent {
		if sent.subject == subject {
			messages = append(messages, sent.message)
		}
	}
	return messages
}

func TestPostgresAlarmReminderGoesOnceToEveryoneAllowedWhoCanSeeTheSite(t *testing.T) {
	fixture := newReminderFixture(t)
	duty, moved, never := fixture.person("duty", 2), fixture.person("moved", 1), fixture.person("never", 0)
	fixture.decider.allowed[duty.Subject] = true
	alarmID := fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "冷冻水供水温度过高，已经连续超过设定值十五分钟以上", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))

	messages := fixture.sentTo(duty.Subject)
	if len(messages) != 1 {
		t.Fatalf("duty got %d reminders", len(messages))
	}
	want := WeChatMessage{TemplateID: "alarm-template", Page: "pages/alarm/index?id=" + alarmID, Data: map[string]string{
		// WeChat allows 20 characters in a thing and 5 in a short_thing.
		"thing10": "冷冻水供水温度过高，已经连续超过设定值…", "short_thing15": "重要", "thing9": "未关联设备", "thing4": "松雅轩", "time2": "2026年10月21日 16:00",
	}}
	if messages[0].TemplateID != want.TemplateID || messages[0].Page != want.Page || len(messages[0].Data) != len(want.Data) {
		t.Fatalf("message = %#v", messages[0])
	}
	for keyword, value := range want.Data {
		if messages[0].Data[keyword] != value {
			t.Fatalf("%s = %q, want %q", keyword, messages[0].Data[keyword], value)
		}
	}
	// Only people with a reminder left were asked about; IAM kept the one moved off the Site.
	if len(fixture.sentTo(moved.Subject)) != 0 || len(fixture.sentTo(never.Subject)) != 0 {
		t.Fatal("a reminder went to someone who cannot see the Site or allowed none")
	}
	if asked := fixture.decider.asked; len(asked) != 1 || len(asked[0]) != 2 {
		t.Fatalf("IAM was asked about %v", asked)
	}
	// One reminder was used; later runs and a replayed event send nothing more.
	if fixture.left(duty) != 1 || fixture.left(moved) != 1 {
		t.Fatalf("left: duty %d, moved %d", fixture.left(duty), fixture.left(moved))
	}
	fixture.event(alarmID, AlarmCreated, alarmmodel.SeverityMajor, "冷冻水供水温度过高", fixture.base)
	fixture.run(fixture.base.Add(2 * time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 1 {
		t.Fatal("the same Alarm was reminded twice")
	}
}

func TestPostgresOnlyNewUrgentAndImportantAlarmsAreReminded(t *testing.T) {
	fixture := newReminderFixture(t)
	duty := fixture.person("duty", 5)
	fixture.decider.allowed[duty.Subject] = true
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMinor, "冷冻水泵读数长时间没有更新", fixture.base)
	fixture.alarm(AlarmSeverityChanged, alarmmodel.SeverityMajor, "冷却水温度偏高", fixture.base)
	fixture.alarm(AlarmCleared, alarmmodel.SeverityCritical, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 0 || fixture.left(duty) != 5 {
		t.Fatal("a minor, changed or cleared Alarm was reminded")
	}
	fixture.alarm(AlarmCreated, alarmmodel.SeverityCritical, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	if messages := fixture.sentTo(duty.Subject); len(messages) != 1 || messages[0].Data["short_thing15"] != "紧急" {
		t.Fatalf("the urgent Alarm was not reminded: %#v", messages)
	}
}

func TestPostgresWeChatRefusingOrNoWeChatClearsTheReminders(t *testing.T) {
	fixture := newReminderFixture(t)
	refusing, unbound := fixture.person("refusing", 3), fixture.person("unbound", 2)
	fixture.decider.allowed[refusing.Subject], fixture.decider.allowed[unbound.Subject] = true, true
	fixture.sender.outcomes[refusing.Subject] = []error{ErrWeChatRefused}
	fixture.sender.outcomes[unbound.Subject] = []error{ErrWeChatNotBound}
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	// WeChat says the person has none left, or they have no WeChat: what the platform counted is wrong.
	if fixture.left(refusing) != 0 || fixture.left(unbound) != 0 {
		t.Fatalf("left: refusing %d, unbound %d", fixture.left(refusing), fixture.left(unbound))
	}
}

func TestPostgresAReminderThatCannotBeSentIsRetriedAndGivenUpAfterThirtyMinutes(t *testing.T) {
	fixture := newReminderFixture(t)
	duty, late := fixture.person("duty", 2), fixture.person("late", 2)
	fixture.decider.allowed[duty.Subject], fixture.decider.allowed[late.Subject] = true, true
	fixture.sender.outcomes[duty.Subject] = []error{errWeChatDown}
	fixture.sender.outcomes[late.Subject] = []error{errWeChatDown, errWeChatDown, errWeChatDown, errWeChatDown}
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	// A failed send gives the reminder back.
	if len(fixture.sentTo(duty.Subject)) != 0 || fixture.left(duty) != 2 {
		t.Fatal("a failed send used a reminder")
	}
	fixture.run(fixture.base.Add(10 * time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 1 || fixture.left(duty) != 1 {
		t.Fatal("the reminder was not sent on the next try")
	}
	// After 30 minutes the Alarm is old news: no more tries.
	fixture.run(fixture.base.Add(31 * time.Minute))
	fixture.sender.outcomes[late.Subject] = nil
	fixture.run(fixture.base.Add(40 * time.Minute))
	if len(fixture.sentTo(late.Subject)) != 0 || fixture.left(late) != 2 {
		t.Fatal("a reminder went out more than 30 minutes after the Alarm began")
	}
	// An Alarm already older than 30 minutes when its event arrives is not reminded at all.
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "冷冻水供水温度过高", fixture.base)
	fixture.run(fixture.base.Add(45 * time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 1 {
		t.Fatal("a stale Alarm was reminded")
	}
}

// Review (#664): a message identity-service could not get a clear answer for may already be on
// the person's phone, so it is never sent again and the reminder counts as used.
func TestPostgresAReminderWeChatMayHaveSentIsNotSentAgain(t *testing.T) {
	fixture := newReminderFixture(t)
	duty := fixture.person("duty", 2)
	fixture.decider.allowed[duty.Subject] = true
	fixture.sender.outcomes[duty.Subject] = []error{ErrWeChatOutcomeUnknown}
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	fixture.run(fixture.base.Add(20 * time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 0 || fixture.left(duty) != 1 {
		t.Fatalf("a message WeChat may have sent was sent again or given back: sent %d, left %d", len(fixture.sentTo(duty.Subject)), fixture.left(duty))
	}
}

// Review (#664): a send that outlives its lease has been settled by another worker; finishing it
// late must neither reopen it nor give the reminder back.
func TestPostgresASendThatOutlivesItsLeaseChangesNothing(t *testing.T) {
	fixture := newReminderFixture(t)
	duty := fixture.person("duty", 2)
	fixture.decider.allowed[duty.Subject] = true
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "网关上报中断", fixture.base)
	fixture.sender.outcomes[duty.Subject] = []error{errWeChatDown}
	// While the slow send is out, its lease runs out and another worker settles the delivery.
	fixture.sender.during = func() { fixture.run(fixture.base.Add(10 * time.Minute)) }
	fixture.run(fixture.base.Add(time.Minute))
	fixture.run(fixture.base.Add(20 * time.Minute))
	if len(fixture.sentTo(duty.Subject)) != 0 || fixture.left(duty) != 1 {
		t.Fatalf("the late send reopened the delivery: sent %d, left %d", len(fixture.sentTo(duty.Subject)), fixture.left(duty))
	}
}

// Review (#664): everyone holding a reminder is asked about, however many there are.
func TestPostgresEveryHolderIsConsideredBeyondOneQuestionToIAM(t *testing.T) {
	fixture := newReminderFixture(t)
	var last WeChatPerson
	for index := range 205 {
		last = fixture.person(fmt.Sprintf("holder-%03d", index), 1)
	}
	fixture.decider.allowed[last.Subject] = true
	fixture.alarm(AlarmCreated, alarmmodel.SeverityMajor, "网关上报中断", fixture.base)
	fixture.run(fixture.base.Add(time.Minute))
	if len(fixture.sentTo(last.Subject)) != 1 {
		t.Fatal("a holder past the first question to IAM was left out")
	}
}
