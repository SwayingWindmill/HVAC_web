package notificationservice

import (
	"testing"
	"time"
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
	// One a day, and only to those IAM lets read the Site's energy.
	fixture.run(siteEightOh1.Add(2 * time.Hour))
	if len(fixture.sentTo(duty.Subject)) != 1 || fixture.leftDaily(duty) != 1 {
		t.Fatalf("summaries: %d, left %d", len(fixture.sentTo(duty.Subject)), fixture.leftDaily(duty))
	}
	if len(fixture.sentTo(outsider.Subject)) != 0 || fixture.leftDaily(outsider) != 2 {
		t.Fatal("a summary went to someone who cannot read the Site's energy")
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
