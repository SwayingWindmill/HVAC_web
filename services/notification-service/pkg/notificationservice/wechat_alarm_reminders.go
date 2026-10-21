package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// WeChat reminders of Alarms (#664, docs/specs/miniprogram-wechat.md). A new urgent or important
// Alarm is reminded once to everyone in the Tenant who allowed an alarm reminder and, as IAM says,
// can read the Site's Alarms when it begins; nobody is reminded once the Alarm is 30 minutes old.

const alarmReminderWindow = 30 * time.Minute

// recordAlarmReminder notes a new urgent or important Alarm to remind people of, in the
// transaction that records its event.
func recordAlarmReminder(ctx context.Context, tx pgx.Tx, event AlarmEvent, recordedAt time.Time) error {
	if event.Action != AlarmCreated || (event.CurrentSeverity != alarmmodel.SeverityCritical && event.CurrentSeverity != alarmmodel.SeverityMajor) {
		return nil
	}
	title := strings.TrimSpace(event.Attributes["title"])
	if title == "" {
		return errors.New("an Alarm to remind people of needs its title")
	}
	_, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_alarm_reminder
  (tenant_id, alarm_id, site_id, source_event_id, title, severity, occurred_at, expires_at, state, next_attempt_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'PENDING', $9, $9, $9)
ON CONFLICT (tenant_id, alarm_id) DO NOTHING`,
		event.TenantID, event.AlarmID, event.SiteID, event.SourceEventID, title, string(event.CurrentSeverity),
		event.OccurredAt.UTC(), event.OccurredAt.UTC().Add(alarmReminderWindow), recordedAt.UTC())
	return err
}

// expandAlarm works out who gets one pending Alarm reminder.
func (worker *WeChatReminderWorker) expandAlarm(ctx context.Context, now time.Time) (bool, error) {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var tenantID, alarmID, siteID string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, alarm_id::text, site_id::text, expires_at FROM notification_runtime.wechat_alarm_reminder
WHERE state = 'PENDING' AND next_attempt_at <= $1
ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(&tenantID, &alarmID, &siteID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim Alarm reminder: %w", err)
	}
	setState := func(state string, next time.Time) error {
		_, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_alarm_reminder SET state = $3, next_attempt_at = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, tenantID, alarmID, state, next.UTC(), now.UTC())
		return err
	}
	if !now.Before(expiresAt) {
		if err := setState("EXPIRED", now); err != nil {
			return true, err
		}
		return true, tx.Commit(ctx)
	}
	holders, err := reminderHolders(ctx, tx, tenantID, ReminderAlarm)
	if err != nil {
		return true, err
	}
	allowed, err := worker.askRecipients(ctx, ReminderAlarm, tenantID, siteID, alarmID, holders)
	if err != nil {
		// IAM is unavailable: ask again in a minute, while the Alarm is still news.
		if stateErr := setState("PENDING", now.Add(time.Minute)); stateErr != nil {
			return true, stateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		return true, err
	}
	if err := addDeliveries(ctx, tx, ReminderAlarm, alarmID, siteID, allowed, expiresAt, now); err != nil {
		return true, err
	}
	if err := setState("EXPANDED", now); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

// reminderHolders are the people in the Tenant with a reminder of this kind left.
func reminderHolders(ctx context.Context, tx pgx.Tx, tenantID string, kind ReminderKind) ([]WeChatPerson, error) {
	rows, err := tx.Query(ctx, `SELECT subject_issuer, subject FROM notification_runtime.wechat_reminder_allowance
WHERE tenant_id = $1::uuid AND kind = $2 AND remaining > 0 ORDER BY subject_issuer, subject`, tenantID, string(kind))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (WeChatPerson, error) {
		person := WeChatPerson{TenantID: tenantID}
		return person, row.Scan(&person.SubjectIssuer, &person.Subject)
	})
}

var severityLabels = map[string]string{"CRITICAL": "紧急", "MAJOR": "重要"}

// alarmMessage fills 监控告警通知: 告警标题, 告警等级, 监控设备, 告警地点 and 告警时间 in the Site's own time.
func (worker *WeChatReminderWorker) alarmMessage(ctx context.Context, claim claimedDelivery) (WeChatMessage, error) {
	var title, severity string
	var occurred time.Time
	if err := worker.Scheduler.pool.QueryRow(ctx, `SELECT title, severity, occurred_at FROM notification_runtime.wechat_alarm_reminder
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, claim.person.TenantID, claim.reference).Scan(&title, &severity, &occurred); err != nil {
		return WeChatMessage{}, fmt.Errorf("read the Alarm to remind of: %w", err)
	}
	site, location, err := worker.site(ctx, claim)
	if err != nil {
		return WeChatMessage{}, err
	}
	return WeChatMessage{
		TemplateID: worker.AlarmTemplateID,
		Page:       "pages/alarm/index?id=" + claim.reference,
		Data: map[string]string{
			"thing10":       clip(title, 20),
			"short_thing15": severityLabels[severity],
			// Alarms carry no device yet; the detail page says the same.
			"thing9": "未关联设备",
			"thing4": clip(site.Name, 20),
			"time2":  occurred.In(location).Format("2006年1月2日 15:04"),
		},
	}, nil
}

// site is the delivery's Site with its time zone loaded.
func (worker *WeChatReminderWorker) site(ctx context.Context, claim claimedDelivery) (SiteFacts, *time.Location, error) {
	site, err := worker.Sites.Site(ctx, claim.person.TenantID, claim.siteID)
	if err != nil {
		return SiteFacts{}, nil, err
	}
	location, err := time.LoadLocation(site.Timezone)
	if err != nil {
		return SiteFacts{}, nil, fmt.Errorf("the Site's time zone %q: %w", site.Timezone, err)
	}
	return site, location, nil
}
