package notificationservice

import (
	"context"
	"strings"
	"testing"
	"time"
)

// WeChat reminders (#664, docs/specs/miniprogram-wechat.md): each time a person allows one in the
// mini program's WeChat dialog, the platform may send them one more. The count is per person and
// per kind of reminder, inside the Tenant the person was signed in to.

const (
	reminderTenantID      = "0190f000-0040-7000-8000-000000000001"
	reminderOtherTenantID = "0190f000-0041-7000-8000-000000000001"
)

func TestPostgresWeChatRemindersCountPerPersonAndTenant(t *testing.T) {
	store := openNotificationTestStore(t)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	subject := "duty-" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	person := WeChatPerson{TenantID: reminderTenantID, SubjectIssuer: "https://platform.example.test/identity", Subject: subject}

	count := func(person WeChatPerson) int {
		t.Helper()
		reminders, err := store.WeChatReminders(ctx, person)
		if err != nil {
			t.Fatal(err)
		}
		return reminders.Alarm
	}
	if count(person) != 0 {
		t.Fatal("a person who never allowed a reminder has some")
	}
	for range 2 {
		if _, err := store.AddWeChatReminder(ctx, person, ReminderAlarm, now); err != nil {
			t.Fatal(err)
		}
	}
	if count(person) != 2 {
		t.Fatalf("after allowing twice: %d", count(person))
	}
	// The same account signed in to another Tenant has its own count, and another person none.
	if count(WeChatPerson{TenantID: reminderOtherTenantID, SubjectIssuer: person.SubjectIssuer, Subject: subject}) != 0 ||
		count(WeChatPerson{TenantID: reminderTenantID, SubjectIssuer: person.SubjectIssuer, Subject: subject + "-other"}) != 0 {
		t.Fatal("reminders leaked to another Tenant or person")
	}
	// Removing WeChat clears them.
	if err := store.ClearWeChatReminders(ctx, person, now); err != nil {
		t.Fatal(err)
	}
	if count(person) != 0 {
		t.Fatalf("after clearing: %d", count(person))
	}
}
