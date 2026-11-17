package alarmservice

import (
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

func TestTransitionNotificationSpecsEmitsSuppressionTransitions(t *testing.T) {
	deviceID := "01910000-0000-7000-8000-000000000222"
	base := alarmmodel.Alarm{
		AlarmID:         "01910000-1000-7000-8000-000000000901",
		TenantID:        "01910000-0000-7000-8000-000000000001",
		SiteID:          "01910000-0000-7000-8000-000000000101",
		DeviceID:        &deviceID,
		AlarmType:       "CHILLER_FAULT",
		Title:           "Chiller Trip",
		Summary:         "Compressor fault",
		SourceType:      alarmmodel.SourceDeviceRule,
		SourceReference: "evt-1",
		RuleRevision:    "rev-1",
		FirstOccurredAt: "2026-11-17T08:00:00Z",
		UpdatedAt:       "2026-11-17T08:00:00Z",
		Condition:       alarmmodel.ConditionActive,
		Version:         1,
	}

	suppressed := base
	suppressed.Version = 2
	suppressed.UpdatedAt = "2026-11-17T08:01:00Z"
	suppressed.Suppression = &alarmmodel.Suppression{
		StartsAt:       "2026-11-17T08:01:00Z",
		ExpiresAt:      "2026-11-17T10:01:00Z",
		Reason:         "Maintenance window",
		ActorID:        "operator-1",
		PolicyRevision: "rev-1",
	}

	specs, err := transitionNotificationSpecs(base, suppressed)
	if err != nil {
		t.Fatalf("suppress transition error: %v", err)
	}
	if len(specs) != 1 || specs[0].action != NotificationSuppressed {
		t.Fatalf("got suppress specs %#v, want single SUPPRESSED", specs)
	}
	if !specs[0].occurredAt.Equal(time.Date(2026, 11, 17, 8, 1, 0, 0, time.UTC)) {
		t.Fatalf("suppress occurredAt = %v", specs[0].occurredAt)
	}

	attrs := buildNotificationAttributes(suppressed, NotificationSuppressed)
	if attrs["deviceId"] != deviceID {
		t.Fatalf("attributes deviceId = %q, want %q", attrs["deviceId"], deviceID)
	}
	if attrs["suppressedUntil"] != "2026-11-17T10:01:00Z" {
		t.Fatalf("attributes suppressedUntil = %q", attrs["suppressedUntil"])
	}

	resuppressed := suppressed
	resuppressed.Version = 3
	resuppressed.UpdatedAt = "2026-11-17T08:02:00Z"
	resuppressed.Suppression = &alarmmodel.Suppression{
		StartsAt:       "2026-11-17T08:02:00Z",
		ExpiresAt:      "2026-11-17T12:02:00Z",
		Reason:         "Extended maintenance",
		ActorID:        "operator-1",
		PolicyRevision: "rev-1",
	}
	specs, err = transitionNotificationSpecs(suppressed, resuppressed)
	if err != nil || len(specs) != 1 || specs[0].action != NotificationSuppressed {
		t.Fatalf("re-suppress got specs=%#v err=%v", specs, err)
	}

	unsuppressed := resuppressed
	unsuppressed.Version = 4
	unsuppressed.UpdatedAt = "2026-11-17T08:03:00Z"
	unsuppressed.Suppression = nil
	specs, err = transitionNotificationSpecs(resuppressed, unsuppressed)
	if err != nil || len(specs) != 1 || specs[0].action != NotificationUnsuppressed {
		t.Fatalf("unsuppress got specs=%#v err=%v", specs, err)
	}
	unsuppressAttrs := buildNotificationAttributes(unsuppressed, NotificationUnsuppressed)
	if unsuppressAttrs["deviceId"] != deviceID {
		t.Fatalf("unsuppress attributes deviceId = %q", unsuppressAttrs["deviceId"])
	}
	if _, ok := unsuppressAttrs["suppressedUntil"]; ok {
		t.Fatalf("unsuppress unexpectedly included suppressedUntil: %#v", unsuppressAttrs)
	}

	clearedAt := "2026-11-17T08:05:00Z"
	clearedFromSuppressed := suppressed
	clearedFromSuppressed.Version = 5
	clearedFromSuppressed.Condition = alarmmodel.ConditionCleared
	clearedFromSuppressed.Suppression = nil
	clearedFromSuppressed.ClearedAt = &clearedAt
	clearedFromSuppressed.UpdatedAt = clearedAt
	specs, err = transitionNotificationSpecs(suppressed, clearedFromSuppressed)
	if err != nil || len(specs) != 2 || specs[0].action != NotificationUnsuppressed || specs[1].action != NotificationCleared {
		t.Fatalf("clear while suppressed got specs=%#v err=%v", specs, err)
	}
}
