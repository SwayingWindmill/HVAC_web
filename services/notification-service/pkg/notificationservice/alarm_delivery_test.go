package notificationservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

const deliveryTestSiteID = "01a14000-1000-7000-8000-0000000000d1"

// testRouting is 松雅轩-like: hold 3 minutes, quiet 23:00–07:00 except CRITICAL, two steps 15
// minutes apart, gateway offline as the root cause of stale and data-quality Alarms.
func testRouting(t *testing.T) SiteRouting {
	t.Helper()
	routing, err := ParseSiteRouting([]byte(`{
  "schemaVersion": 1, "siteId": "` + deliveryTestSiteID + `", "siteName": "松雅轩", "timezone": "Asia/Shanghai",
  "holdMinutes": 3, "maxAgeHours": 12, "rateLimitPerHour": 2,
  "quietHours": {"start": "23:00", "end": "07:00", "bypassSeverity": "CRITICAL"},
  "rootCauseAlarmType": "reports-interrupted",
  "suppressWhenRootCause": ["readings-not-updated/", "readings-flat/", "data-quality/", "rules-unevaluable"],
  "routes": [
    {"name": "urgent", "minSeverity": "MAJOR", "notifyAck": true, "notifyRecovery": true, "steps": [
      {"afterMinutes": 0, "recipients": ["duty"], "channels": ["wecom_app", "smtp"]},
      {"afterMinutes": 15, "recipients": ["lead"], "channels": ["wecom_app", "wecom_bot"]}]},
    {"name": "info", "minSeverity": "WARNING", "steps": [{"afterMinutes": 0, "recipients": ["duty"], "channels": ["smtp"]}]}
  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	return routing
}

// Beijing 14:26 on 11-01, the gateway offline of the incident.
var deliveryBase = time.Date(2026, 11, 1, 6, 26, 0, 0, time.UTC)

func activeAlarm(alarmType string, severity alarmmodel.Severity) DeliveryAlarm {
	return DeliveryAlarm{TenantID: alarmReminderTenantID, AlarmID: "0190f000-0050-7000-8000-0000000000a1", SiteID: deliveryTestSiteID,
		AlarmType: alarmType, Title: "网关上报中断", Summary: "松雅轩 15 分钟没有收到任何读数。", Severity: severity, Version: 1, BeganAt: deliveryBase}
}

func keys(alarmID string, messages []PlannedMessage) []string {
	var out []string
	for _, message := range messages {
		key := DedupKey(alarmID, message)
		if message.Suppressed {
			key += "!" + message.Reason
		}
		out = append(out, key)
	}
	return out
}

func queuedFrom(messages []PlannedMessage, at time.Time) []QueuedMessage {
	var queued []QueuedMessage
	for _, message := range messages {
		queued = append(queued, QueuedMessage{Kind: message.Kind, Step: message.Step, Channel: message.Channel, Recipient: message.Recipient, QueuedAt: at, Reached: !message.Suppressed})
	}
	return queued
}

func TestDecideHoldsThenFiresThenEscalatesUntilAcknowledged(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)

	held := DecideDelivery(alarm, routing, DeliveryFacts{}, deliveryBase.Add(time.Minute))
	if len(held.Messages) != 0 || held.Held != "HOLD" || !held.NextDueAt.Equal(deliveryBase.Add(3*time.Minute)) {
		t.Fatalf("during the hold: %+v", held)
	}
	fireAt := deliveryBase.Add(3 * time.Minute)
	fire := DecideDelivery(alarm, routing, DeliveryFacts{}, fireAt)
	if got := strings.Join(keys(alarm.AlarmID, fire.Messages), " "); got != alarm.AlarmID+":FIRE:0:wecom_app:duty "+alarm.AlarmID+":FIRE:0:smtp:duty" {
		t.Fatalf("first messages: %s", got)
	}
	if !fire.NextDueAt.Equal(fireAt.Add(15 * time.Minute)) {
		t.Fatalf("escalation due at %v", fire.NextDueAt)
	}
	facts := DeliveryFacts{Queued: queuedFrom(fire.Messages, fireAt)}
	if again := DecideDelivery(alarm, routing, facts, fireAt.Add(time.Minute)); len(again.Messages) != 0 || !again.NextDueAt.Equal(fireAt.Add(15*time.Minute)) {
		t.Fatalf("nothing new before the escalation: %+v", again)
	}
	escalate := DecideDelivery(alarm, routing, facts, fireAt.Add(15*time.Minute))
	if got := strings.Join(keys(alarm.AlarmID, escalate.Messages), " "); got != alarm.AlarmID+":ESCALATE:1:wecom_bot:group "+alarm.AlarmID+":ESCALATE:1:wecom_app:lead" {
		t.Fatalf("escalation: %s", got)
	}
	if escalate.NextDueAt != nil {
		t.Fatalf("nothing after the last step: %v", escalate.NextDueAt)
	}
	facts.Queued = append(facts.Queued, queuedFrom(escalate.Messages, fireAt.Add(15*time.Minute))...)

	acknowledged := alarm
	ackAt := fireAt.Add(20 * time.Minute)
	acknowledged.AcknowledgedAt, acknowledged.AcknowledgedBy = &ackAt, "王工"
	ack := DecideDelivery(acknowledged, routing, facts, ackAt)
	if len(ack.Messages) != 4 || ack.Messages[0].Kind != DeliveryAck || ack.NextDueAt != nil {
		t.Fatalf("everyone told hears of the acknowledgement: %+v", ack)
	}
	facts.Queued = append(facts.Queued, queuedFrom(ack.Messages, ackAt)...)
	cleared := acknowledged
	clearAt := ackAt.Add(10 * time.Minute)
	cleared.ClearedAt = &clearAt
	recovery := DecideDelivery(cleared, routing, facts, clearAt)
	if len(recovery.Messages) != 4 || recovery.Messages[0].Kind != DeliveryRecovery {
		t.Fatalf("everyone told hears of the recovery: %+v", recovery)
	}
}

func TestDecideAcknowledgedInTimeStopsTheEscalation(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)
	fireAt := deliveryBase.Add(3 * time.Minute)
	fire := DecideDelivery(alarm, routing, DeliveryFacts{}, fireAt)
	ackAt := fireAt.Add(5 * time.Minute)
	alarm.AcknowledgedAt = &ackAt
	facts := DeliveryFacts{Queued: queuedFrom(fire.Messages, fireAt)}
	facts.Queued = append(facts.Queued, queuedFrom(DecideDelivery(alarm, routing, facts, ackAt).Messages, ackAt)...)
	later := DecideDelivery(alarm, routing, facts, fireAt.Add(30*time.Minute))
	if len(later.Messages) != 0 || later.NextDueAt != nil {
		t.Fatalf("no escalation after an acknowledgement: %+v", later)
	}
}

func TestDecideAlarmThatClearsDuringTheHoldTellsNobody(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)
	clearAt := deliveryBase.Add(2 * time.Minute)
	alarm.ClearedAt = &clearAt
	if decision := DecideDelivery(alarm, routing, DeliveryFacts{}, clearAt); len(decision.Messages) != 0 || decision.NextDueAt != nil {
		t.Fatalf("a blip tells nobody: %+v", decision)
	}
}

func TestDecideGatewayOfflineHoldsBackItsSymptoms(t *testing.T) {
	routing := testRouting(t)
	stale := activeAlarm("readings-not-updated/chiller-a", alarmmodel.SeverityMajor)
	at := deliveryBase.Add(10 * time.Minute)
	held := DecideDelivery(stale, routing, DeliveryFacts{RootCauseActive: true}, at)
	if len(held.Messages) != 0 || held.Held != "ROOT_CAUSE" || !held.NextDueAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("a symptom waits for its root cause: %+v", held)
	}
	if free := DecideDelivery(stale, routing, DeliveryFacts{}, at); len(free.Messages) != 2 {
		t.Fatalf("without the root cause the symptom is told: %+v", free)
	}
	physics := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	if other := DecideDelivery(physics, routing, DeliveryFacts{RootCauseActive: true}, at); len(other.Messages) != 2 {
		t.Fatalf("an Alarm that is no symptom is told: %+v", other)
	}
}

func TestDecideQuietHoursDeferAllButCritical(t *testing.T) {
	routing := testRouting(t)
	// 01:00 Beijing.
	night := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)
	alarm.BeganAt = night.Add(-10 * time.Minute)
	deferred := DecideDelivery(alarm, routing, DeliveryFacts{}, night)
	if want := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC); len(deferred.Messages) != 0 || deferred.Held != "QUIET_HOURS" || !deferred.NextDueAt.Equal(want) {
		t.Fatalf("deferred until 07:00 Beijing: %+v", deferred)
	}
	alarm.Severity = alarmmodel.SeverityCritical
	if urgent := DecideDelivery(alarm, routing, DeliveryFacts{}, night); len(urgent.Messages) != 2 {
		t.Fatalf("CRITICAL is told at night: %+v", urgent)
	}
	// 23:30 Beijing: quiet hours end the next morning.
	evening := time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC)
	if until := routing.quietUntil(evening); until == nil || !until.Equal(time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("quiet until %v", until)
	}
	if until := routing.quietUntil(deliveryBase); until != nil {
		t.Fatalf("14:26 is not quiet: %v", until)
	}
}

func TestDecideRateLimitSuppressesButNeverCritical(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	at := deliveryBase.Add(5 * time.Minute)
	busy := DeliveryFacts{SentLastHour: map[string]int{"duty": 2}}
	decision := DecideDelivery(alarm, routing, busy, at)
	if len(decision.Messages) != 2 || !decision.Messages[0].Suppressed || decision.Messages[0].Reason != "RATE_LIMITED" {
		t.Fatalf("over the limit: %+v", decision)
	}
	alarm.Severity = alarmmodel.SeverityCritical
	if critical := DecideDelivery(alarm, routing, busy, at); critical.Messages[0].Suppressed {
		t.Fatalf("CRITICAL is never capped: %+v", critical)
	}
}

func TestDecideOldAlarmsAndUnroutedAlarmsTellNobody(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)
	if old := DecideDelivery(alarm, routing, DeliveryFacts{}, deliveryBase.Add(13*time.Hour)); len(old.Messages) != 0 || old.Held != "TOO_OLD" {
		t.Fatalf("an Alarm first seen after 12 hours: %+v", old)
	}
	info := activeAlarm("reports-interrupted", alarmmodel.SeverityInfo)
	if none := DecideDelivery(info, routing, DeliveryFacts{}, deliveryBase.Add(time.Hour)); len(none.Messages) != 0 || none.Held != "NO_ROUTE" {
		t.Fatalf("INFO matches no route: %+v", none)
	}
	warning := activeAlarm("rules-unevaluable", alarmmodel.SeverityWarning)
	fire := DecideDelivery(warning, routing, DeliveryFacts{}, deliveryBase.Add(time.Hour))
	if len(fire.Messages) != 1 || fire.Messages[0].Channel != ChannelSMTP {
		t.Fatalf("WARNING goes by mail only: %+v", fire)
	}
	acked := warning
	at := deliveryBase.Add(2 * time.Hour)
	acked.AcknowledgedAt = &at
	if quiet := DecideDelivery(acked, routing, DeliveryFacts{Queued: queuedFrom(fire.Messages, at)}, at); len(quiet.Messages) != 0 {
		t.Fatalf("the info route sends no acknowledgement notice: %+v", quiet)
	}
}

func TestRenderDeliveryMessageInTheSiteTime(t *testing.T) {
	routing := testRouting(t)
	alarm := activeAlarm("reports-interrupted", alarmmodel.SeverityMajor)
	subject, text := RenderDeliveryMessage(alarm, routing, DeliveryFire, 0, "https://console.example.test/")
	if subject != "【告警·重要】松雅轩：网关上报中断" {
		t.Fatalf("subject %q", subject)
	}
	for _, want := range []string{"开始：2026-11-01 14:26（Asia/Shanghai）", "15 分钟内无人确认，将通知下一级。", "确认告警：https://console.example.test/operations/alarms?site=" + alarm.SiteID + "&view=all&inspect=" + alarm.AlarmID} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q lacks %q", text, want)
		}
	}
	clearAt := deliveryBase.Add(47 * time.Minute)
	alarm.ClearedAt = &clearAt
	subject, text = RenderDeliveryMessage(alarm, routing, DeliveryRecovery, 0, "")
	if subject != "【已恢复】松雅轩：网关上报中断" || !strings.Contains(text, "恢复：2026-11-01 15:13，持续 47 分钟") || strings.Contains(text, "inspect") {
		t.Fatalf("recovery %q %q", subject, text)
	}
}

func TestRoutingFilesAreCheckedAgainstTheRecipients(t *testing.T) {
	dir := t.TempDir()
	routing, _ := json.Marshal(testRouting(t))
	if err := os.WriteFile(filepath.Join(dir, "songyaxuan.alarm-routing.json"), routing, 0o600); err != nil {
		t.Fatal(err)
	}
	recipientsPath := filepath.Join(dir, "recipients.json")
	write := func(body string) {
		if err := os.WriteFile(recipientsPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"recipients": {"duty": {"name": "值班", "email": "duty@example.test", "wecomUserId": "duty"}, "lead": {"name": "主管", "wecomUserId": "lead"}}}`)
	recipients, err := LoadRecipientDirectory(recipientsPath)
	if err != nil {
		t.Fatal(err)
	}
	table, err := LoadAlarmRouting(dir, recipients)
	if err != nil || table.Len() != 1 {
		t.Fatalf("table %d: %v", table.Len(), err)
	}
	if _, ok := table.Site(strings.ToUpper(deliveryTestSiteID)); !ok {
		t.Fatal("the Site is found whatever its case")
	}
	write(`{"recipients": {"duty": {"name": "值班", "wecomUserId": "duty"}, "lead": {"name": "主管", "wecomUserId": "lead"}}}`)
	recipients, _ = LoadRecipientDirectory(recipientsPath)
	if _, err := LoadAlarmRouting(dir, recipients); err == nil || !strings.Contains(err.Error(), "no contact for smtp") {
		t.Fatalf("mail to someone without an address: %v", err)
	}
	write(`{"recipients": {"duty": {"name": "值班", "email": "duty@example.test", "phone": "1"}}}`)
	if _, err := LoadRecipientDirectory(recipientsPath); err == nil || strings.Contains(err.Error(), "duty@") {
		t.Fatalf("unknown fields are refused without echoing contacts: %v", err)
	}
	for name, body := range map[string]string{
		"first step later": `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":5,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"steps not rising": `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]},{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"mail to nobody":   `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"channels":["smtp"]}]}]}`,
		"bad zone":         `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Beijing","routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"unknown channel":  `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["sms"]}]}]}`,
		"no root cause":    `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","suppressWhenRootCause":["data-quality/"],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
	} {
		if _, err := ParseSiteRouting([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHTTPDeliverySenderContract(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "alarm-send-token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got OutboundMessage
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/send" || request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("Content-Type") != "application/json" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			writer.WriteHeader(http.StatusTeapot)
			return
		}
		writer.WriteHeader(status)
	}))
	defer server.Close()
	sender := HTTPDeliverySender{URL: server.URL + "/send", TokenFile: tokenFile}
	message := OutboundMessage{Channel: ChannelSMTP, To: "duty@example.test", Subject: "s", Text: "t", DryRun: true, Key: "k"}
	if err := sender.SendAlarmMessage(context.Background(), message); err != nil || got != message {
		t.Fatalf("sent %+v: %v", got, err)
	}
	status = http.StatusBadRequest
	if err := sender.SendAlarmMessage(context.Background(), message); !errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("400 is final: %v", err)
	}
	status = http.StatusBadGateway
	if err := sender.SendAlarmMessage(context.Background(), message); err == nil || errors.Is(err, ErrDeliveryRejected) {
		t.Fatalf("502 is retried: %v", err)
	}
	if err := (HTTPDeliverySender{URL: server.URL + "/send", TokenFile: filepath.Join(dir, "missing")}).SendAlarmMessage(context.Background(), message); err == nil {
		t.Fatal("no token, no send")
	}
	if mode, err := ParseDeliveryMode(""); err != nil || mode != DeliveryOff {
		t.Fatalf("default mode %q %v", mode, err)
	}
	if _, err := ParseDeliveryMode("on"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}
