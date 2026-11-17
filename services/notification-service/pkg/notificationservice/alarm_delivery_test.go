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
		"rule no trigger":  `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":" ","suppressPrefixes":["data-quality/"]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"rule no suppress": `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":"device-untrusted/","scopeSameDevice":true,"suppressPrefixes":[]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"rule blank item":  `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":"device-untrusted/","suppressPrefixes":[""]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"rule missing /":   `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":"device-untrusted","scopeSameDevice":true,"suppressPrefixes":["data-quality/"]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
	} {
		if _, err := ParseSiteRouting([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDecideRootCauseRulesRespectSameDeviceScope(t *testing.T) {
	routing, err := ParseSiteRouting([]byte(`{
  "schemaVersion": 1, "siteId": "` + deliveryTestSiteID + `", "siteName": "松雅轩", "timezone": "Asia/Shanghai",
  "holdMinutes": 3, "maxAgeHours": 12, "rateLimitPerHour": 2,
  "rootCauseAlarmType": "reports-interrupted",
  "suppressWhenRootCause": ["readings-not-updated/"],
  "rootCauseRules": [
    {"whenActivePrefix": "device-untrusted/", "scopeSameDevice": true, "suppressPrefixes": ["data-quality/", "reading-gap/", "readings-not-updated/", "readings-flat/"]},
    {"whenActivePrefix": "site-maintenance/", "suppressPrefixes": ["physics/"]}
  ],
  "routes": [
    {"name": "all", "minSeverity": "WARNING", "steps": [{"afterMinutes": 0, "recipients": ["duty"], "channels": ["smtp"]}]}
  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	at := deliveryBase.Add(10 * time.Minute)
	facts := DeliveryFacts{ActiveAlarmTypes: []string{"device-untrusted/temperature-uid20"}}

	for _, suppressedType := range []string{
		"data-quality/suspect/temperature-uid20/t16",
		"data-quality/flat-running/temperature-uid20/t1",
		"reading-gap/temperature-uid20/t16",
		"readings-not-updated/temperature-uid20",
		"readings-flat/temperature-uid20/t15",
	} {
		decision := DecideDelivery(activeAlarm(suppressedType, alarmmodel.SeverityMajor), routing, facts, at)
		if len(decision.Messages) != 0 || decision.Held != "ROOT_CAUSE" {
			t.Fatalf("%s should be held by device-untrusted/temperature-uid20: %+v", suppressedType, decision)
		}
	}

	for _, allowedType := range []string{
		"device-untrusted/temperature-uid20",
		"reading-gap/meter-uid5/power_total",
		"readings-not-updated/meter-uid5",
		"readings-flat/outdoor/ambient_temperature",
		"readings-not-updated/temperature-uid20-other",
		"physics/chiller-a-chw-direction",
	} {
		decision := DecideDelivery(activeAlarm(allowedType, alarmmodel.SeverityMajor), routing, facts, at)
		if len(decision.Messages) != 1 || decision.Held != "" {
			t.Fatalf("%s should NOT be suppressed by device-untrusted/temperature-uid20: %+v", allowedType, decision)
		}
	}

	siteWide := DecideDelivery(activeAlarm("physics/chiller-a-chw-direction", alarmmodel.SeverityMajor), routing, DeliveryFacts{ActiveAlarmTypes: []string{"site-maintenance/all"}}, at)
	if len(siteWide.Messages) != 0 || siteWide.Held != "ROOT_CAUSE" {
		t.Fatalf("site-wide rule without scopeSameDevice should suppress matching prefix: %+v", siteWide)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestHTTPDeliverySenderReuseTimeoutAndNoRedirect(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "alarm-send-token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	firstDefault := deliveryHTTPClient(nil)
	secondDefault := deliveryHTTPClient(nil)
	if firstDefault != secondDefault {
		t.Fatal("default delivery HTTP client must be reused across requests")
	}
	if firstDefault.Timeout != defaultDeliverySendTimeout {
		t.Fatalf("default client timeout = %v, want %v", firstDefault.Timeout, defaultDeliverySendTimeout)
	}
	if err := firstDefault.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("default CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}

	custom := &http.Client{}
	effective := deliveryHTTPClient(custom)
	if custom.Timeout != 0 || custom.CheckRedirect != nil {
		t.Fatal("deliveryHTTPClient mutated caller client")
	}
	if effective.Timeout != defaultDeliverySendTimeout {
		t.Fatalf("effective custom timeout = %v, want %v", effective.Timeout, defaultDeliverySendTimeout)
	}
	if err := effective.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("effective CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}

	var seenDeadline time.Time
	var hasDeadline bool
	inspectSender := HTTPDeliverySender{
		URL:       "http://adapter.example.test/send",
		TokenFile: tokenFile,
		Client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				seenDeadline, hasDeadline = req.Context().Deadline()
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       http.NoBody,
					Header:     make(http.Header),
				}, nil
			}),
		},
	}
	message := OutboundMessage{Channel: ChannelSMTP, To: "duty@example.test", Subject: "s", Text: "t", Key: "k"}
	before := time.Now()
	if err := inspectSender.SendAlarmMessage(context.Background(), message); err != nil {
		t.Fatalf("inspect SendAlarmMessage failed: %v", err)
	}
	if !hasDeadline || time.Until(seenDeadline) <= 0 || seenDeadline.Sub(before) > defaultDeliverySendTimeout+2*time.Second {
		t.Fatalf("expected bounded send deadline near %v, got hasDeadline=%v deadline=%v", defaultDeliverySendTimeout, hasDeadline, seenDeadline)
	}

	redirectTargetHits := 0
	redirectServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/target" {
			redirectTargetHits++
			writer.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(writer, request, "/target", http.StatusFound)
	}))
	defer redirectServer.Close()

	redirectSender := HTTPDeliverySender{
		URL:       redirectServer.URL + "/send",
		TokenFile: tokenFile,
		Client:    &http.Client{},
	}
	err := redirectSender.SendAlarmMessage(context.Background(), message)
	if err == nil || errors.Is(err, ErrDeliveryRejected) || redirectTargetHits != 0 {
		t.Fatalf("expected redirect refusal without following target: err=%v targetHits=%d", err, redirectTargetHits)
	}
}

func TestDecideRateLimitDeduplicatesTargetsAndRespectsHoldWindow(t *testing.T) {
	routing := testRouting(t)
	routing.RateLimitPerHour = 2
	routing.Routes[0].Steps[0] = EscalationStep{
		AfterMinutes: 0,
		Recipients:   []string{"duty", "duty"},
		Channels:     []DeliveryChannel{ChannelSMTP, ChannelSMTP},
	}
	routing.Routes[0].Steps[1] = EscalationStep{
		AfterMinutes: 15,
		Recipients:   []string{"duty"},
		Channels:     []DeliveryChannel{ChannelSMTP},
	}

	alarm := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	fireAt := deliveryBase.Add(3 * time.Minute)
	first := DecideDelivery(alarm, routing, DeliveryFacts{}, fireAt)
	if len(first.Messages) != 1 || first.Messages[0].Suppressed {
		t.Fatalf("expected single deduplicated non-suppressed message, got %+v", first.Messages)
	}

	secondAlarm := activeAlarm("physics/pump-pressure", alarmmodel.SeverityMajor)
	secondAlarm.AlarmID = "0190f000-0050-7000-8000-0000000000a2"
	second := DecideDelivery(secondAlarm, routing, DeliveryFacts{SentLastHour: map[string]int{"duty": 1}}, fireAt)
	if len(second.Messages) != 1 || second.Messages[0].Suppressed {
		t.Fatalf("deduplicated step should consume only 1 quota slot, got %+v", second.Messages)
	}

	thirdAlarm := activeAlarm("physics/chiller-trip", alarmmodel.SeverityMajor)
	thirdAlarm.AlarmID = "0190f000-0050-7000-8000-0000000000a3"
	third := DecideDelivery(thirdAlarm, routing, DeliveryFacts{SentLastHour: map[string]int{"duty": 2}}, fireAt)
	if len(third.Messages) != 1 || !third.Messages[0].Suppressed || third.Messages[0].Reason != "RATE_LIMITED" {
		t.Fatalf("expected RATE_LIMITED after quota reached, got %+v", third.Messages)
	}

	// Direct struct callers with zero MaxAgeHours or negative HoldMinutes must not drop fresh Alarms.
	directRouting := SiteRouting{
		SiteID:           deliveryTestSiteID,
		SiteName:         "松雅轩",
		Timezone:         "Asia/Shanghai",
		HoldMinutes:      -5,
		RateLimitPerHour: 1,
		QuietHours:       &QuietHours{Start: "bad", End: "07:00", BypassSeverity: alarmmodel.SeverityCritical},
		Routes: []AlarmRoute{
			{Name: "all", MinSeverity: alarmmodel.SeverityWarning, Steps: []EscalationStep{{AfterMinutes: 0, Recipients: []string{"duty"}, Channels: []DeliveryChannel{ChannelSMTP}}}},
		},
	}
	if got := DecideDelivery(alarm, directRouting, DeliveryFacts{}, deliveryBase.Add(time.Minute)); len(got.Messages) != 1 || got.Held != "" {
		t.Fatalf("expected direct routing defaults to deliver fresh Alarm safely, got %+v", got)
	}
}

func TestBoundedAndSettleContextHelpers(t *testing.T) {
	ctx30, cancel30 := withDefaultTimeout(context.Background(), 30*time.Second)
	defer cancel30()
	deadline30, ok := ctx30.Deadline()
	if !ok || time.Until(deadline30) <= 20*time.Second {
		t.Fatalf("expected ~30s default deadline, got ok=%v until=%v", ok, time.Until(deadline30))
	}

	ctx20, cancel20 := withBoundedTimeout(ctx30, 20*time.Second)
	defer cancel20()
	deadline20, ok := ctx20.Deadline()
	if !ok || time.Until(deadline20) > 21*time.Second {
		t.Fatalf("expected clamped ~20s deadline, got ok=%v until=%v", ok, time.Until(deadline20))
	}

	expiredCtx, cancelExpired := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelExpired()
	<-expiredCtx.Done()
	settleCtx, cancelSettle := settleContext(expiredCtx, 5*time.Second)
	defer cancelSettle()
	if settleCtx.Err() != nil {
		t.Fatalf("expected live settle context after send timeout, got %v", settleCtx.Err())
	}
	if _, hasDeadline := settleCtx.Deadline(); !hasDeadline {
		t.Fatal("expected bounded deadline on settle context")
	}
}

func TestAlarmEventValidateSupportsSuppressedAndUnsuppressed(t *testing.T) {
	base := AlarmEvent{
		TenantID:              alarmReminderTenantID,
		SiteID:                deliveryTestSiteID,
		SourceEventID:         "0190f000-0050-7000-8000-0000000000b1",
		AlarmID:               "0190f000-0050-7000-8000-0000000000b2",
		IncidentCorrelationID: "0190f000-0050-7000-8000-0000000000b2",
		CurrentSeverity:       alarmmodel.SeverityMajor,
		PeakSeverity:          alarmmodel.SeverityMajor,
		OccurredAt:            deliveryBase,
	}
	for _, tc := range []struct {
		name      string
		action    AlarmAction
		condition alarmmodel.Condition
		wantErr   bool
	}{
		{name: "suppressed active", action: AlarmSuppressed, condition: alarmmodel.ConditionActive},
		{name: "suppressed cleared", action: AlarmSuppressed, condition: alarmmodel.ConditionCleared},
		{name: "unsuppressed active", action: AlarmUnsuppressed, condition: alarmmodel.ConditionActive},
		{name: "unsuppressed cleared", action: AlarmUnsuppressed, condition: alarmmodel.ConditionCleared},
		{name: "created cleared invalid", action: AlarmCreated, condition: alarmmodel.ConditionCleared, wantErr: true},
		{name: "severity changed cleared invalid", action: AlarmSeverityChanged, condition: alarmmodel.ConditionCleared, wantErr: true},
		{name: "cleared active invalid", action: AlarmCleared, condition: alarmmodel.ConditionActive, wantErr: true},
		{name: "unknown action invalid", action: AlarmAction("REOPENED"), condition: alarmmodel.ConditionActive, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := base
			ev.Action = tc.action
			ev.Condition = tc.condition
			err := ev.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestDecideMaintenanceSuppressionWindow(t *testing.T) {
	routing := testRouting(t)
	until := deliveryBase.Add(2 * time.Hour)
	alarm := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityCritical)
	alarm.SuppressedUntil = &until

	held := DecideDelivery(alarm, routing, DeliveryFacts{}, deliveryBase.Add(10*time.Minute))
	if len(held.Messages) != 0 || held.Held != "SUPPRESSED" || held.NextDueAt == nil || !held.NextDueAt.Equal(until) {
		t.Fatalf("expected SUPPRESSED hold until %v, got %+v", until, held)
	}

	resumed := DecideDelivery(alarm, routing, DeliveryFacts{}, until)
	if len(resumed.Messages) != 2 || resumed.Held != "" || resumed.Messages[0].Kind != DeliveryFire {
		t.Fatalf("expected FIRE once suppression window elapsed, got %+v", resumed)
	}

	// Step 0 already fired, then maintenance window holds step 1 escalation until window expires.
	step0At := deliveryBase.Add(3 * time.Minute)
	fire := DecideDelivery(activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor), routing, DeliveryFacts{}, step0At)
	facts := DeliveryFacts{Queued: queuedFrom(fire.Messages, step0At)}
	escalateSuppressUntil := step0At.Add(30 * time.Minute)
	alarmForStep1 := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	alarmForStep1.SuppressedUntil = &escalateSuppressUntil
	heldEscalate := DecideDelivery(alarmForStep1, routing, facts, step0At.Add(15*time.Minute))
	if len(heldEscalate.Messages) != 0 || heldEscalate.Held != "SUPPRESSED" || !heldEscalate.NextDueAt.Equal(escalateSuppressUntil) {
		t.Fatalf("expected step 1 escalation held during suppression, got %+v", heldEscalate)
	}
	resumedEscalate := DecideDelivery(alarmForStep1, routing, facts, escalateSuppressUntil)
	if len(resumedEscalate.Messages) != 2 || resumedEscalate.Messages[0].Kind != DeliveryEscalate {
		t.Fatalf("expected step 1 escalation after suppression window, got %+v", resumedEscalate)
	}

	// Acknowledged while suppressed: if nobody was reached, returns empty; if reached, waits until SuppressedUntil.
	ackAt := step0At.Add(5 * time.Minute)
	ackedNeverReached := alarmForStep1
	ackedNeverReached.AcknowledgedAt = &ackAt
	if got := DecideDelivery(ackedNeverReached, routing, DeliveryFacts{}, ackAt); len(got.Messages) != 0 || got.Held != "" || got.NextDueAt != nil {
		t.Fatalf("unreached acknowledged alarm should not hold for ACK: %+v", got)
	}
	heldAck := DecideDelivery(ackedNeverReached, routing, facts, ackAt)
	if len(heldAck.Messages) != 0 || heldAck.Held != "SUPPRESSED" || !heldAck.NextDueAt.Equal(escalateSuppressUntil) {
		t.Fatalf("reached acknowledged alarm should wait out suppression: %+v", heldAck)
	}
	resumedAck := DecideDelivery(ackedNeverReached, routing, facts, escalateSuppressUntil)
	if len(resumedAck.Messages) != 2 || resumedAck.Messages[0].Kind != DeliveryAck {
		t.Fatalf("expected ACK after suppression ends, got %+v", resumedAck)
	}

	// Cleared while suppressed still sends RECOVERY to already-reached recipients.
	cleared := alarmForStep1
	clearAt := step0At.Add(10 * time.Minute)
	cleared.ClearedAt = &clearAt
	recovery := DecideDelivery(cleared, routing, facts, clearAt)
	if len(recovery.Messages) != 2 || recovery.Messages[0].Kind != DeliveryRecovery {
		t.Fatalf("expected immediate RECOVERY for already-reached recipients, got %+v", recovery)
	}

	// If suppression exceeds maxAgeHours before step 0 ever fires, TOO_OLD drops it on expiry.
	longUntil := deliveryBase.Add(14 * time.Hour)
	longAlarm := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	longAlarm.SuppressedUntil = &longUntil
	if old := DecideDelivery(longAlarm, routing, DeliveryFacts{}, longUntil); len(old.Messages) != 0 || old.Held != "TOO_OLD" {
		t.Fatalf("expected TOO_OLD after 14h suppression, got %+v", old)
	}
}

func TestDecideCooldownDeduplicatesNonCriticalFireOnly(t *testing.T) {
	routing := testRouting(t)
	routing.CooldownMinutes = 30
	routing.Routes[0].CooldownMinutes = 10 // major route overrides site default
	routing.RateLimitPerHour = 3

	at := deliveryBase.Add(5 * time.Minute)
	facts := DeliveryFacts{
		SentLastHour:           map[string]int{"duty": 1},
		LastSameTypeNotifiedAt: map[string]time.Time{"duty": at.Add(-9 * time.Minute)},
	}

	major := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityMajor)
	suppressed := DecideDelivery(major, routing, facts, at)
	if len(suppressed.Messages) != 2 {
		t.Fatalf("expected 2 planned messages, got %+v", suppressed.Messages)
	}
	for _, msg := range suppressed.Messages {
		if !msg.Suppressed || msg.Reason != "COOLDOWN" {
			t.Fatalf("expected COOLDOWN suppression, got %+v", msg)
		}
	}

	// Boundary: exactly 10 minutes elapsed -> cooldown expired -> delivers normally.
	facts.LastSameTypeNotifiedAt["duty"] = at.Add(-10 * time.Minute)
	expired := DecideDelivery(major, routing, facts, at)
	for _, msg := range expired.Messages {
		if msg.Suppressed {
			t.Fatalf("expected FIRE at exact cooldown boundary, got %+v", msg)
		}
	}

	// CRITICAL bypasses cooldown.
	facts.LastSameTypeNotifiedAt["duty"] = at.Add(-time.Minute)
	critical := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityCritical)
	critDecision := DecideDelivery(critical, routing, facts, at)
	for _, msg := range critDecision.Messages {
		if msg.Suppressed {
			t.Fatalf("CRITICAL must bypass cooldown, got %+v", msg)
		}
	}

	// Warning route inherits site default 30m cooldown.
	warning := activeAlarm("physics/cop-out-of-range", alarmmodel.SeverityWarning)
	facts.LastSameTypeNotifiedAt["duty"] = at.Add(-20 * time.Minute)
	warnDecision := DecideDelivery(warning, routing, facts, at)
	if len(warnDecision.Messages) != 1 || !warnDecision.Messages[0].Suppressed || warnDecision.Messages[0].Reason != "COOLDOWN" {
		t.Fatalf("expected warning route to inherit site 30m cooldown, got %+v", warnDecision)
	}

	// Escalation and recovery stay unaffected by cooldown.
	fireAt := deliveryBase.Add(3 * time.Minute)
	initialFire := DecideDelivery(major, routing, DeliveryFacts{}, fireAt)
	escalateFacts := DeliveryFacts{
		Queued:                 queuedFrom(initialFire.Messages, fireAt),
		LastSameTypeNotifiedAt: map[string]time.Time{"lead": fireAt.Add(14 * time.Minute), "group": fireAt.Add(14 * time.Minute), "duty": fireAt.Add(14 * time.Minute)},
	}
	escalate := DecideDelivery(major, routing, escalateFacts, fireAt.Add(15*time.Minute))
	if len(escalate.Messages) != 2 || escalate.Messages[0].Suppressed || escalate.Messages[1].Suppressed {
		t.Fatalf("escalation must not be suppressed by cooldown: %+v", escalate.Messages)
	}
	clearAt := fireAt.Add(20 * time.Minute)
	cleared := major
	cleared.ClearedAt = &clearAt
	recovery := DecideDelivery(cleared, routing, escalateFacts, clearAt)
	if len(recovery.Messages) != 2 || recovery.Messages[0].Suppressed || recovery.Messages[1].Suppressed {
		t.Fatalf("recovery must not be suppressed by cooldown: %+v", recovery.Messages)
	}
}

func TestParseSiteRoutingValidatesCooldownTopologyAndSongyaxuanConfig(t *testing.T) {
	for name, body := range map[string]string{
		"negative site cooldown":  `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","cooldownMinutes":-1,"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"huge site cooldown":      `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","cooldownMinutes":10081,"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"negative route cooldown": `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","cooldownMinutes":-2,"steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"huge route cooldown":     `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","routes":[{"name":"a","minSeverity":"MAJOR","cooldownMinutes":10081,"steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"blank whenDeviceId":      `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":"power-outage","whenDeviceId":" ","suppressPrefixes":["reading-gap/"]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
		"blank suppressDeviceId":  `{"schemaVersion":1,"siteId":"` + deliveryTestSiteID + `","siteName":"x","timezone":"Asia/Shanghai","rootCauseRules":[{"whenActivePrefix":"power-outage","whenDeviceId":"panel-1","suppressPrefixes":["reading-gap/"],"suppressDeviceIds":["chiller-a",""]}],"routes":[{"name":"a","minSeverity":"MAJOR","steps":[{"afterMinutes":0,"recipients":["duty"],"channels":["smtp"]}]}]}`,
	} {
		if _, err := ParseSiteRouting([]byte(body)); err == nil {
			t.Errorf("%s: unexpectedly accepted", name)
		}
	}

	recipients, err := LoadRecipientDirectory(filepath.Join("..", "..", "..", "..", "deploy", "platform", "phase1", "alarm-delivery.example", "recipients.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := LoadAlarmRouting(filepath.Join("..", "..", "..", "..", "deploy", "sites"), recipients)
	if err != nil {
		t.Fatal(err)
	}
	songyaxuan, ok := table.Site("01a14000-1000-7000-8000-000000000001")
	if !ok {
		t.Fatal("songyaxuan routing missing")
	}
	if len(songyaxuan.Routes) < 2 || songyaxuan.Routes[0].CooldownMinutes != 10 || songyaxuan.Routes[1].CooldownMinutes != 30 {
		t.Fatalf("unexpected songyaxuan cooldown config: %+v", songyaxuan.Routes)
	}
}

func TestDecideRootCauseParentDeviceAndGatewayTopology(t *testing.T) {
	routing, err := ParseSiteRouting([]byte(`{
  "schemaVersion": 1, "siteId": "` + deliveryTestSiteID + `", "siteName": "松雅轩", "timezone": "Asia/Shanghai",
  "holdMinutes": 3, "maxAgeHours": 12, "rateLimitPerHour": 5,
  "rootCauseRules": [
    {
      "whenActivePrefix": "power-outage",
      "whenDeviceId": "main-panel",
      "suppressPrefixes": ["reading-gap/", "power-outage"],
      "suppressDeviceIds": ["chiller-a", "pump-1"]
    },
    {
      "whenActivePrefix": "gateway-offline",
      "whenDeviceId": "gw-1",
      "suppressPrefixes": ["readings-not-updated/"]
    }
  ],
  "routes": [
    {"name": "all", "minSeverity": "WARNING", "steps": [{"afterMinutes": 0, "recipients": ["duty"], "channels": ["smtp"]}]}
  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	at := deliveryBase.Add(10 * time.Minute)
	facts := DeliveryFacts{
		ActiveAlarms: []ActiveAlarmFact{
			{AlarmType: "power-outage", DeviceID: "main-panel"},
			{AlarmType: "gateway-offline", DeviceID: "gw-2"},
		},
	}

	// Downstream devices in suppressDeviceIds are suppressed.
	chillerGap := activeAlarm("reading-gap/chiller-a/temp", alarmmodel.SeverityMajor)
	chillerGap.DeviceID = "chiller-a"
	if got := DecideDelivery(chillerGap, routing, facts, at); got.Held != "ROOT_CAUSE" || len(got.Messages) != 0 {
		t.Fatalf("expected chiller-a gap suppressed by main-panel power-outage, got %+v", got)
	}

	// UUID deviceId on downstream alarm still matches device code in suppressDeviceIds via alarmType.
	pumpGap := activeAlarm("reading-gap/pump-1/flow", alarmmodel.SeverityMajor)
	pumpGap.DeviceID = "01a14000-4000-7000-8000-000000000099"
	if got := DecideDelivery(pumpGap, routing, facts, at); got.Held != "ROOT_CAUSE" || len(got.Messages) != 0 {
		t.Fatalf("expected pump-1 gap with UUID deviceId suppressed, got %+v", got)
	}

	// Same alarmType on child device suppressed by parent device rule.
	childOutage := activeAlarm("power-outage", alarmmodel.SeverityMajor)
	childOutage.DeviceID = "chiller-a"
	if got := DecideDelivery(childOutage, routing, facts, at); got.Held != "ROOT_CAUSE" || len(got.Messages) != 0 {
		t.Fatalf("expected child device power-outage suppressed by main-panel, got %+v", got)
	}

	// Unlisted downstream device chiller-b is NOT suppressed by panel rule.
	otherGap := activeAlarm("reading-gap/chiller-b/temp", alarmmodel.SeverityMajor)
	otherGap.DeviceID = "chiller-b"
	if got := DecideDelivery(otherGap, routing, facts, at); got.Held != "" || len(got.Messages) != 1 {
		t.Fatalf("expected chiller-b gap NOT suppressed, got %+v", got)
	}

	// gateway-offline on gw-2 must NOT trigger rule scoped to whenDeviceId=gw-1.
	stale := activeAlarm("readings-not-updated/chiller-b", alarmmodel.SeverityWarning)
	stale.DeviceID = "chiller-b"
	if got := DecideDelivery(stale, routing, facts, at); got.Held != "" || len(got.Messages) != 1 {
		t.Fatalf("expected gw-2 not to match whenDeviceId=gw-1, got %+v", got)
	}

	// Adding gateway-offline on gw-1 suppresses site-wide when suppressDeviceIds is empty.
	facts.ActiveAlarms = append(facts.ActiveAlarms, ActiveAlarmFact{AlarmType: "gateway-offline", DeviceID: "gw-1"})
	if got := DecideDelivery(stale, routing, facts, at); got.Held != "ROOT_CAUSE" || len(got.Messages) != 0 {
		t.Fatalf("expected gw-1 site-wide rule to suppress readings-not-updated, got %+v", got)
	}
}
