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

func parseReadingGapFirstOccurredAt(attributes map[string]string) (time.Time, error) {
	raw := strings.TrimSpace(attributes["firstOccurredAt"])
	if raw == "" {
		return time.Time{}, errors.New("a reading-gap Alarm event needs firstOccurredAt")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse reading-gap firstOccurredAt: %w", err)
	}
	return parsed.UTC(), nil
}

// recordAlarmReminder notes a new urgent or important Alarm to remind people of, or a reading-gap
// Alarm that just escalated to important or urgent after 24 hours unacknowledged (#800).
func recordAlarmReminder(ctx context.Context, tx pgx.Tx, event AlarmEvent, recordedAt time.Time) error {
	if event.CurrentSeverity != alarmmodel.SeverityCritical && event.CurrentSeverity != alarmmodel.SeverityMajor {
		return nil
	}
	switch event.Action {
	case AlarmCreated:
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
	case AlarmSeverityChanged:
		if !alarmmodel.IsReadingGapType(event.Attributes["alarmType"]) {
			return nil
		}
		title := strings.TrimSpace(event.Attributes["title"])
		if title == "" {
			return errors.New("an Alarm to remind people of needs its title")
		}
		firstOccurredAt, err := parseReadingGapFirstOccurredAt(event.Attributes)
		if err != nil {
			return err
		}
		// Keep occurred_at at firstOccurredAt so WeChat time2 shows when the reading gap started,
		// while expires_at gives 30 minutes from the escalation instant to deliver the reminder.
		_, err = tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_alarm_reminder
  (tenant_id, alarm_id, site_id, source_event_id, title, severity, occurred_at, expires_at, state, next_attempt_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'PENDING', $9, $9, $9)
ON CONFLICT (tenant_id, alarm_id) DO UPDATE
  SET source_event_id = EXCLUDED.source_event_id,
      title = EXCLUDED.title,
      severity = EXCLUDED.severity,
      occurred_at = LEAST(notification_runtime.wechat_alarm_reminder.occurred_at, EXCLUDED.occurred_at),
      expires_at = EXCLUDED.expires_at,
      state = 'PENDING',
      next_attempt_at = EXCLUDED.next_attempt_at,
      updated_at = EXCLUDED.updated_at
  WHERE notification_runtime.wechat_alarm_reminder.severity <> EXCLUDED.severity`,
			event.TenantID, event.AlarmID, event.SiteID, event.SourceEventID, title, string(event.CurrentSeverity),
			firstOccurredAt, event.OccurredAt.UTC().Add(alarmReminderWindow), recordedAt.UTC())
		return err
	default:
		return nil
	}
}

// recordOpenReadingGap maintains Notification's bounded copy of open reading-gap Alarms so the
// 08:00 Site-local daily summary can list them without reading another owner's database (#800).
func recordOpenReadingGap(ctx context.Context, tx pgx.Tx, event AlarmEvent, recordedAt time.Time) error {
	alarmType := strings.TrimSpace(event.Attributes["alarmType"])
	if !alarmmodel.IsReadingGapType(alarmType) {
		return nil
	}
	condition := string(alarmmodel.ConditionActive)
	if event.Action == AlarmCleared || event.Condition == alarmmodel.ConditionCleared {
		condition = string(alarmmodel.ConditionCleared)
	} else if event.Action != AlarmCreated && event.Action != AlarmSeverityChanged {
		return nil
	}
	title := strings.TrimSpace(event.Attributes["title"])
	if title == "" {
		return errors.New("a reading-gap Alarm needs its title")
	}
	firstOccurredAt, err := parseReadingGapFirstOccurredAt(event.Attributes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO notification_runtime.open_reading_gap_alarm
  (tenant_id, alarm_id, site_id, alarm_type, title, current_severity, condition, first_occurred_at, last_event_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (tenant_id, alarm_id) DO UPDATE
  SET title = EXCLUDED.title,
      current_severity = EXCLUDED.current_severity,
      condition = EXCLUDED.condition,
      first_occurred_at = LEAST(notification_runtime.open_reading_gap_alarm.first_occurred_at, EXCLUDED.first_occurred_at),
      last_event_at = EXCLUDED.last_event_at,
      updated_at = EXCLUDED.updated_at
  WHERE notification_runtime.open_reading_gap_alarm.last_event_at < EXCLUDED.last_event_at
     OR (notification_runtime.open_reading_gap_alarm.last_event_at = EXCLUDED.last_event_at AND EXCLUDED.condition = 'CLEARED')`,
		event.TenantID, event.AlarmID, event.SiteID, alarmType, title, string(event.CurrentSeverity),
		condition, firstOccurredAt, event.OccurredAt.UTC(), recordedAt.UTC())
	return err
}

// expandAlarm works out who gets one pending Alarm reminder.
func (worker *WeChatReminderWorker) expandAlarm(ctx context.Context, now time.Time) (bool, error) {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var tenantID, alarmID, siteID, severity string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, alarm_id::text, site_id::text, severity, expires_at FROM notification_runtime.wechat_alarm_reminder
WHERE state = 'PENDING' AND next_attempt_at <= $1
ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(&tenantID, &alarmID, &siteID, &severity, &expiresAt)
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
	deliveryReference := alarmID
	var hadInitialDelivery bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM notification_runtime.wechat_reminder_delivery
WHERE tenant_id = $1::uuid AND kind = 'ALARM' AND reference = $2)`, tenantID, alarmID).Scan(&hadInitialDelivery); err != nil {
		return true, err
	}
	if hadInitialDelivery {
		deliveryReference = alarmID + ":" + severity
	}
	if err := addDeliveries(ctx, tx, ReminderAlarm, deliveryReference, siteID, "PENDING", allowed, expiresAt, now); err != nil {
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
func (worker *WeChatReminderWorker) alarmMessage(ctx context.Context, claim claimedDelivery, now time.Time) (WeChatMessage, error) {
	alarmID, _, _ := strings.Cut(claim.reference, ":")
	var title, severity string
	var occurred time.Time
	if err := worker.Scheduler.pool.QueryRow(ctx, `SELECT title, severity, occurred_at FROM notification_runtime.wechat_alarm_reminder
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, claim.person.TenantID, alarmID).Scan(&title, &severity, &occurred); err != nil {
		return WeChatMessage{}, fmt.Errorf("read the Alarm to remind of: %w", err)
	}
	site, location, err := worker.siteAt(ctx, claim.person.TenantID, claim.siteID, now)
	if err != nil {
		return WeChatMessage{}, err
	}
	return WeChatMessage{
		TemplateID: worker.AlarmTemplateID,
		Page:       "pages/alarm/index?id=" + alarmID,
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
