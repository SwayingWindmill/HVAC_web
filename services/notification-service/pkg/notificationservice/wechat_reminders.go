package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// WeChat reminders for the mini program (#664, docs/specs/miniprogram-wechat.md). WeChat lets the
// platform send a person one subscription message for each time they allowed one in the mini
// program; the platform keeps how many are left, per person and kind of reminder.

type ReminderKind string

const (
	// ReminderAlarm is a reminder of a new urgent or important Alarm on a Site the person can see.
	ReminderAlarm ReminderKind = "ALARM"
	// ReminderDaily is one morning's summary of yesterday's energy at the person's chosen Site.
	ReminderDaily ReminderKind = "DAILY"
)

// ReminderRequest is one reminder the person allowed; a daily summary names its Site.
type ReminderRequest struct {
	Kind   ReminderKind `json:"kind"`
	SiteID string       `json:"siteId,omitempty"`
}

func (request ReminderRequest) valid() bool {
	switch request.Kind {
	case ReminderAlarm:
		return request.SiteID == ""
	case ReminderDaily:
		return alarmmodel.IsUUIDv7(request.SiteID)
	}
	return false
}

// WeChatPerson is a signed-in person inside the Tenant they were signed in to.
type WeChatPerson struct {
	TenantID      string
	SubjectIssuer string
	Subject       string
}

func (person WeChatPerson) valid() bool {
	return strings.TrimSpace(person.TenantID) != "" && strings.TrimSpace(person.SubjectIssuer) != "" && strings.TrimSpace(person.Subject) != ""
}

// WeChatReminders is how many more reminders of each kind the platform may send, and the Site
// the daily summary is for.
type WeChatReminders struct {
	Alarm       int    `json:"alarm"`
	Daily       int    `json:"daily"`
	DailySiteID string `json:"dailySiteId,omitempty"`
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
SELECT COALESCE(sum(remaining) FILTER (WHERE kind = 'ALARM'), 0), COALESCE(sum(remaining) FILTER (WHERE kind = 'DAILY'), 0),
  COALESCE((SELECT site_id::text FROM notification_runtime.wechat_daily_subscription WHERE subject_issuer = $1 AND subject = $2), '')
FROM notification_runtime.wechat_reminder_allowance
WHERE subject_issuer = $1 AND subject = $2
`, person.SubjectIssuer, person.Subject).Scan(&reminders.Alarm, &reminders.Daily, &reminders.DailySiteID)
	if err != nil {
		return WeChatReminders{}, fmt.Errorf("read WeChat reminders: %w", err)
	}
	return reminders, tx.Commit(ctx)
}

// AddWeChatReminder records one more reminder the person allowed; a daily summary also moves to
// the Site it names.
func (store *PostgresStore) AddWeChatReminder(ctx context.Context, person WeChatPerson, request ReminderRequest, now time.Time) (WeChatReminders, error) {
	if !person.valid() || !request.valid() {
		return WeChatReminders{}, errors.New("a signed-in person and a reminder kind (with its Site for a daily summary) are required")
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
`, person.TenantID, person.SubjectIssuer, person.Subject, string(request.Kind), now.UTC()); err != nil {
		return WeChatReminders{}, fmt.Errorf("add WeChat reminder: %w", err)
	}
	if request.Kind == ReminderDaily {
		if _, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_daily_subscription (tenant_id, subject_issuer, subject, site_id, updated_at)
VALUES ($1::uuid, $2, $3, $4::uuid, $5)
ON CONFLICT (tenant_id, subject_issuer, subject) DO UPDATE SET site_id = EXCLUDED.site_id, updated_at = EXCLUDED.updated_at
`, person.TenantID, person.SubjectIssuer, person.Subject, request.SiteID, now.UTC()); err != nil {
			return WeChatReminders{}, fmt.Errorf("choose the daily summary's Site: %w", err)
		}
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
