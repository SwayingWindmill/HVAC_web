package notificationservice

import (
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// The daily WeChat summary (#664, docs/specs/miniprogram-wechat.md): after 8 in the morning, Site
// time, everyone who allowed one and can read the Site's energy gets yesterday's electricity,
// cooling and efficiency, once a day; nobody gets one after noon.

// Shanghai is UTC+8: 00:00 UTC is 08:00 at the Site.
var (
	siteSevenThirty = time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC)
	siteEightOh1    = time.Date(2026, 10, 22, 0, 1, 0, 0, time.UTC)
	siteNoonThirty  = time.Date(2026, 10, 22, 4, 30, 0, 0, time.UTC)
)

func TestPostgresDailySummaryGoesOnceADayAfterEightToWhoeverCanReadTheEnergy(t *testing.T) {
	fixture := newReminderFixture(t)
	duty, outsider := fixture.dailyPerson("duty", 2, alarmReminderSiteID), fixture.dailyPerson("outsider", 2, alarmReminderSiteID)
	fixture.decider.allowedDaily[duty.Subject] = true

	fixture.run(siteSevenThirty)
	if len(fixture.sentTo(duty.Subject)) != 0 {
		t.Fatal("a summary went out before 8 at the Site")
	}
	fixture.run(siteEightOh1)
	messages := fixture.sentTo(duty.Subject)
	if len(messages) != 1 {
		t.Fatalf("duty got %d summaries", len(messages))
	}
	want := map[string]string{
		"thing1": "松雅轩", "character_string2": "7402kWh", "character_string3": "1691kWh", "character_string4": "4.38", "character_string5": "-",
	}
	if messages[0].TemplateID != "daily-template" || messages[0].Page != "pages/overview/index" || len(messages[0].Data) != len(want) {
		t.Fatalf("message = %#v", messages[0])
	}
	for keyword, value := range want {
		if messages[0].Data[keyword] != value {
			t.Fatalf("%s = %q, want %q", keyword, messages[0].Data[keyword], value)
		}
	}
	// Review (#664): planning every minute reads the Site from the Registry at most once an hour.
	readSoFar := fixture.sites.asked
	fixture.run(siteEightOh1.Add(4 * time.Minute))
	fixture.run(siteEightOh1.Add(20 * time.Minute))
	if fixture.sites.asked != readSoFar {
		t.Fatalf("the Registry was asked %d more times within the hour", fixture.sites.asked-readSoFar)
	}
	// One a day, and only to those IAM lets read the Site's energy.
	fixture.run(siteEightOh1.Add(2 * time.Hour))
	if len(fixture.sentTo(duty.Subject)) != 1 || fixture.leftDaily(duty) != 1 {
		t.Fatalf("summaries: %d, left %d", len(fixture.sentTo(duty.Subject)), fixture.leftDaily(duty))
	}
	if len(fixture.sentTo(outsider.Subject)) != 0 || fixture.leftDaily(outsider) != 2 {
		t.Fatal("a summary went to someone who cannot read the Site's energy")
	}
	// Review (#664): someone IAM refused is not asked about again the same day.
	fixture.run(siteEightOh1.Add(3 * time.Hour))
	asked := 0
	for _, question := range fixture.decider.asked {
		for _, subject := range question {
			if subject == outsider.Subject {
				asked++
			}
		}
	}
	if asked != 1 {
		t.Fatalf("IAM was asked about the outsider %d times", asked)
	}
	// The next day brings the next summary.
	fixture.run(siteEightOh1.Add(24 * time.Hour))
	if len(fixture.sentTo(duty.Subject)) != 2 || fixture.leftDaily(duty) != 0 {
		t.Fatal("the next day's summary did not go out")
	}
}

func TestPostgresDailySummaryWritesADashForEnergyNotRead(t *testing.T) {
	fixture := newReminderFixture(t)
	duty := fixture.dailyPerson("duty", 1, alarmReminderSiteID)
	fixture.decider.allowedDaily[duty.Subject] = true
	fixture.worker.Energy = fakeEnergy{}
	fixture.run(siteEightOh1)
	messages := fixture.sentTo(duty.Subject)
	if len(messages) != 1 || messages[0].Data["character_string2"] != "-" || messages[0].Data["character_string3"] != "-" || messages[0].Data["character_string4"] != "-" {
		t.Fatalf("message = %#v", messages)
	}
}

func TestPostgresNoDailySummaryAfterNoon(t *testing.T) {
	fixture := newReminderFixture(t)
	duty := fixture.dailyPerson("duty", 1, alarmReminderSiteID)
	fixture.decider.allowedDaily[duty.Subject] = true
	fixture.run(siteNoonThirty)
	if len(fixture.sentTo(duty.Subject)) != 0 || fixture.leftDaily(duty) != 1 {
		t.Fatal("a summary went out after noon at the Site")
	}
}

// #800: an open reading-gap Alarm appears in the 08:00 Site-local daily summary with its meter,
// local start time, and open day count, and disappears once recovered.
func TestPostgresDailySummaryIncludesOpenReadingGapsWithDaysAndClearsWhenRecovered(t *testing.T) {
	fixture := newReminderFixture(t)
	gapSiteID := fixture.uuid()
	duty := fixture.dailyPerson("duty", 2, gapSiteID)
	fixture.decider.allowedDaily[duty.Subject] = true
	fixture.worker.Energy = fakeEnergy{
		days: map[string][2]float64{
			"2026-10-21 Asia/Shanghai": {1691, 7402},
			"2026-10-22 Asia/Shanghai": {1720, 7510},
		},
	}

	gapAlarmID := fixture.uuid()
	secondGapID := fixture.uuid()
	startedAt := siteEightOh1.Add(-48*time.Hour + 10*time.Minute) // 2026-10-20 08:11 Asia/Shanghai -> rounds to 2 days at 10-22 08:01
	secondStartedAt := siteEightOh1.Add(-24 * time.Hour)          // 2026-10-21 08:01 Asia/Shanghai -> 1 day
	if err := fixture.store.RecordAlarmEvent(context.Background(), AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: gapSiteID, SourceEventID: fixture.uuid(),
		AlarmID: gapAlarmID, IncidentCorrelationID: gapAlarmID, Action: AlarmCreated,
		CurrentSeverity: alarmmodel.SeverityMajor, PeakSeverity: alarmmodel.SeverityMajor,
		Condition: alarmmodel.ConditionActive, OccurredAt: startedAt,
		Attributes: map[string]string{
			"alarmType":       "reading-gap/meter-uid2/active-energy",
			"title":           "D 机组电表：读数长时间没有更新",
			"firstOccurredAt": startedAt.UTC().Format(time.RFC3339Nano),
		},
	}, startedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordAlarmEvent(context.Background(), AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: gapSiteID, SourceEventID: fixture.uuid(),
		AlarmID: secondGapID, IncidentCorrelationID: secondGapID, Action: AlarmCreated,
		CurrentSeverity: alarmmodel.SeverityMajor, PeakSeverity: alarmmodel.SeverityMajor,
		Condition: alarmmodel.ConditionActive, OccurredAt: secondStartedAt,
		Attributes: map[string]string{
			"alarmType":       "reading-gap/meter-uid3/active-energy",
			"title":           "A 机组电表：读数长时间没有更新",
			"firstOccurredAt": secondStartedAt.UTC().Format(time.RFC3339Nano),
		},
	}, secondStartedAt); err != nil {
		t.Fatal(err)
	}

	fixture.run(siteEightOh1)
	messages := fixture.sentTo(duty.Subject)
	if len(messages) != 1 {
		t.Fatalf("expected 1 daily summary with open reading gaps, got %d", len(messages))
	}
	if got, want := messages[0].Data["thing1"], "松雅轩·D机组电表、A机组电表断2天"; got != want {
		t.Fatalf("thing1 = %q, want %q", got, want)
	}
	if got, want := messages[0].Data["character_string5"], "10/20-08:11-2d,10/21-08:01-1d"; got != want {
		t.Fatalf("character_string5 = %q, want %q", got, want)
	}

	recoveredAt := siteEightOh1.Add(2 * time.Hour)
	for _, clearedID := range []string{gapAlarmID, secondGapID} {
		if err := fixture.store.RecordAlarmEvent(context.Background(), AlarmEvent{
			TenantID: alarmReminderTenantID, SiteID: gapSiteID, SourceEventID: fixture.uuid(),
			AlarmID: clearedID, IncidentCorrelationID: clearedID, Action: AlarmCleared,
			CurrentSeverity: alarmmodel.SeverityMajor, PeakSeverity: alarmmodel.SeverityMajor,
			Condition: alarmmodel.ConditionCleared, OccurredAt: recoveredAt,
			Attributes: map[string]string{
				"alarmType":       "reading-gap/meter-uid2/active-energy",
				"title":           "D 机组电表：读数长时间没有更新",
				"firstOccurredAt": startedAt.UTC().Format(time.RFC3339Nano),
			},
		}, recoveredAt); err != nil {
			t.Fatal(err)
		}
	}
	// An out-of-order CREATED replay from before recovery must not resurrect a cleared gap.
	if err := fixture.store.RecordAlarmEvent(context.Background(), AlarmEvent{
		TenantID: alarmReminderTenantID, SiteID: gapSiteID, SourceEventID: fixture.uuid(),
		AlarmID: gapAlarmID, IncidentCorrelationID: gapAlarmID, Action: AlarmCreated,
		CurrentSeverity: alarmmodel.SeverityMajor, PeakSeverity: alarmmodel.SeverityMajor,
		Condition: alarmmodel.ConditionActive, OccurredAt: startedAt,
		Attributes: map[string]string{
			"alarmType":       "reading-gap/meter-uid2/active-energy",
			"title":           "D 机组电表：读数长时间没有更新",
			"firstOccurredAt": startedAt.UTC().Format(time.RFC3339Nano),
		},
	}, recoveredAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	fixture.run(siteEightOh1.Add(24 * time.Hour))
	messages = fixture.sentTo(duty.Subject)
	if len(messages) != 2 {
		t.Fatalf("expected second day's summary after recovery, got %d", len(messages))
	}
	if got, want := messages[1].Data["thing1"], "松雅轩"; got != want {
		t.Fatalf("recovered thing1 = %q, want %q", got, want)
	}
	if got, want := messages[1].Data["character_string5"], "-"; got != want {
		t.Fatalf("recovered character_string5 = %q, want %q", got, want)
	}
}
