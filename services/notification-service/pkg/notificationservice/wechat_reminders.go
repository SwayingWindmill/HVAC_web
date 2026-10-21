package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WeChat reminders for the mini program (#664, docs/specs/miniprogram-wechat.md). WeChat lets the
// platform send a person one subscription message for each time they allowed one in the mini
// program; the platform keeps how many are left, per person and kind of reminder.

type ReminderKind string

// ReminderAlarm is a reminder of a new urgent or important Alarm on a Site the person can see.
const ReminderAlarm ReminderKind = "ALARM"

// WeChatPerson is a signed-in person inside the Tenant they were signed in to.
type WeChatPerson struct {
	TenantID      string
	SubjectIssuer string
	Subject       string
}

func (person WeChatPerson) valid() bool {
	return strings.TrimSpace(person.TenantID) != "" && strings.TrimSpace(person.SubjectIssuer) != "" && strings.TrimSpace(person.Subject) != ""
}

// WeChatReminders is how many more reminders of each kind the platform may send.
type WeChatReminders struct {
	Alarm int `json:"alarm"`
}

func (store *PostgresStore) WeChatReminders(ctx context.Context, person WeChatPerson) (WeChatReminders, error) {
	if !person.valid() {
		return WeChatReminders{}, errors.New("a signed-in person is required")
	}
	tx, err := store.beginTenant(ctx, person.TenantID)
	if err != nil {
		return WeChatReminders{}, err
	}
	defer tx.Rollback(ctx)
	var reminders WeChatReminders
	err = tx.QueryRow(ctx, `
SELECT COALESCE(sum(remaining) FILTER (WHERE kind = 'ALARM'), 0)
FROM notification_runtime.wechat_reminder_allowance
WHERE subject_issuer = $1 AND subject = $2
`, person.SubjectIssuer, person.Subject).Scan(&reminders.Alarm)
	if err != nil {
		return WeChatReminders{}, fmt.Errorf("read WeChat reminders: %w", err)
	}
	return reminders, tx.Commit(ctx)
}

// AddWeChatReminder records one more reminder of this kind the person allowed.
func (store *PostgresStore) AddWeChatReminder(ctx context.Context, person WeChatPerson, kind ReminderKind, now time.Time) (WeChatReminders, error) {
	if !person.valid() || kind != ReminderAlarm {
		return WeChatReminders{}, errors.New("a signed-in person and a reminder kind are required")
	}
	tx, err := store.beginTenant(ctx, person.TenantID)
	if err != nil {
		return WeChatReminders{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_reminder_allowance (tenant_id, subject_issuer, subject, kind, remaining, updated_at)
VALUES ($1::uuid, $2, $3, $4, 1, $5)
ON CONFLICT (tenant_id, subject_issuer, subject, kind) DO UPDATE SET remaining = wechat_reminder_allowance.remaining + 1, updated_at = EXCLUDED.updated_at
`, person.TenantID, person.SubjectIssuer, person.Subject, string(kind), now.UTC()); err != nil {
		return WeChatReminders{}, fmt.Errorf("add WeChat reminder: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WeChatReminders{}, err
	}
	return store.WeChatReminders(ctx, person)
}

// ClearWeChatReminders forgets the person's reminders, as when they remove WeChat.
func (store *PostgresStore) ClearWeChatReminders(ctx context.Context, person WeChatPerson, now time.Time) error {
	if !person.valid() {
		return errors.New("a signed-in person is required")
	}
	tx, err := store.beginTenant(ctx, person.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
UPDATE notification_runtime.wechat_reminder_allowance SET remaining = 0, updated_at = $3
WHERE subject_issuer = $1 AND subject = $2
`, person.SubjectIssuer, person.Subject, now.UTC()); err != nil {
		return fmt.Errorf("clear WeChat reminders: %w", err)
	}
	return tx.Commit(ctx)
}
