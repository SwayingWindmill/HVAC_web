package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// WeChat reminders of Alarms (#664, docs/specs/miniprogram-wechat.md). A new urgent or important
// Alarm is reminded once to everyone in the Tenant who allowed an alarm reminder and, as IAM says,
// can read the Site's Alarms now. A reminder is taken from the person's allowance before it is
// sent and given back when sending fails; nobody is reminded once the Alarm is 30 minutes old.

const (
	alarmReminderWindow = 30 * time.Minute
	// One send and three retries, 1, 4 and 9 minutes apart.
	alarmReminderAttempts = 4
	alarmReminderLease    = 2 * time.Minute
	// reminderHoldersPerQuestion is how many people one question to IAM names (IAM's limit).
	reminderHoldersPerQuestion = 200
)

var (
	// ErrWeChatRefused is WeChat refusing because the person has no reminder of this kind left.
	ErrWeChatRefused = errors.New("WeChat refuses: no reminder left")
	// ErrWeChatNotBound is a person without a WeChat bound.
	ErrWeChatNotBound = errors.New("the person has no WeChat bound")
	// ErrWeChatOutcomeUnknown is a message that may have reached the person: never sent again.
	ErrWeChatOutcomeUnknown = errors.New("WeChat may have sent the message")
)

// AlarmRecipientDecider is IAM: which of these people can read the Site's Alarms now.
type AlarmRecipientDecider interface {
	AllowedRecipients(ctx context.Context, tenantID, siteID, alarmID string, people []WeChatPerson) ([]WeChatPerson, error)
}

// SiteFacts are what a reminder says about the Site: its name, and its time zone for the time.
type SiteFacts struct {
	Name     string
	Timezone string
}

// SiteDirectory is the Registry's Site.
type SiteDirectory interface {
	Site(ctx context.Context, tenantID, siteID string) (SiteFacts, error)
}

// WeChatMessage is a subscription message: the template, the mini program page it opens and each
// keyword's value.
type WeChatMessage struct {
	TemplateID string            `json:"templateId"`
	Page       string            `json:"page"`
	Data       map[string]string `json:"data"`
}

// WeChatSender is identity-service sending to a person; ErrWeChatRefused, ErrWeChatNotBound and
// ErrWeChatOutcomeUnknown are its answers, any other error a message that was not sent.
type WeChatSender interface {
	SendWeChatMessage(ctx context.Context, person WeChatPerson, message WeChatMessage) error
}

type WeChatAlarmReminderWorker struct {
	Scheduler  *Scheduler
	Recipients AlarmRecipientDecider
	Sites      SiteDirectory
	Sender     WeChatSender
	// TemplateID is 监控告警通知 in 微信公众平台 → 订阅消息 → 我的模板.
	TemplateID string
	Logger     *slog.Logger
}

func (worker *WeChatAlarmReminderWorker) log() *slog.Logger {
	if worker.Logger == nil {
		return slog.Default()
	}
	return worker.Logger
}

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

// RunOnce works out who gets one pending Alarm reminder, or sends one person's; false when
// nothing is due.
func (worker *WeChatAlarmReminderWorker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if worked, err := worker.expandOne(ctx, now); worked || err != nil {
		return worked, err
	}
	return worker.deliverOne(ctx, now)
}

func (worker *WeChatAlarmReminderWorker) expandOne(ctx context.Context, now time.Time) (bool, error) {
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
	rows, err := tx.Query(ctx, `SELECT subject_issuer, subject FROM notification_runtime.wechat_reminder_allowance
WHERE tenant_id = $1::uuid AND kind = 'ALARM' AND remaining > 0 ORDER BY subject_issuer, subject`, tenantID)
	if err != nil {
		return true, err
	}
	holders, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (WeChatPerson, error) {
		person := WeChatPerson{TenantID: tenantID}
		return person, row.Scan(&person.SubjectIssuer, &person.Subject)
	})
	if err != nil {
		return true, err
	}
	var allowed []WeChatPerson
	for start := 0; start < len(holders) && err == nil; start += reminderHoldersPerQuestion {
		var page []WeChatPerson
		page, err = worker.Recipients.AllowedRecipients(ctx, tenantID, siteID, alarmID, holders[start:min(start+reminderHoldersPerQuestion, len(holders))])
		allowed = append(allowed, page...)
	}
	if err != nil {
		// IAM is unavailable: ask again in a minute, while the Alarm is still news.
		if stateErr := setState("PENDING", now.Add(time.Minute)); stateErr != nil {
			return true, stateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		return true, fmt.Errorf("ask IAM who may get an Alarm reminder: %w", err)
	}
	for _, person := range allowed {
		if _, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_alarm_delivery
  (tenant_id, alarm_id, subject_issuer, subject, state, attempts, next_attempt_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, $4, 'PENDING', 0, $5, $5, $5)
ON CONFLICT DO NOTHING`, tenantID, alarmID, person.SubjectIssuer, person.Subject, now.UTC()); err != nil {
			return true, err
		}
	}
	if err := setState("EXPANDED", now); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

type claimedDelivery struct {
	person   WeChatPerson
	alarmID  string
	siteID   string
	title    string
	severity string
	occurred time.Time
	attempts int
	// leaseUntil fences the claim: a send that outlives it changes nothing.
	leaseUntil time.Time
}

func (worker *WeChatAlarmReminderWorker) deliverOne(ctx context.Context, now time.Time) (bool, error) {
	claim, worked, err := worker.claimDelivery(ctx, now)
	if err != nil || claim == nil {
		return worked, err
	}
	stage := "site"
	message, err := worker.message(ctx, *claim)
	if err == nil {
		stage = "send"
		err = worker.Sender.SendWeChatMessage(ctx, claim.person, message)
	}
	switch {
	case err == nil:
		return true, worker.settle(ctx, *claim, now, "SENT", false)
	case errors.Is(err, ErrWeChatRefused):
		return true, worker.settle(ctx, *claim, now, "REFUSED", true)
	case errors.Is(err, ErrWeChatNotBound):
		return true, worker.settle(ctx, *claim, now, "NOT_BOUND", true)
	case errors.Is(err, ErrWeChatOutcomeUnknown):
		worker.log().Warn("wechat_alarm_reminder_outcome_unknown", "error_code", "WECHAT_ALARM_REMINDER_OUTCOME_UNKNOWN", "alarm_id", claim.alarmID)
		return true, worker.settle(ctx, *claim, now, "FAILED", false)
	default:
		worker.log().Warn("wechat_alarm_reminder_not_sent", "error_code", "WECHAT_ALARM_REMINDER_NOT_SENT", "alarm_id", claim.alarmID, "stage", stage, "attempt", claim.attempts)
		return true, worker.retry(ctx, *claim, now)
	}
}

// claimDelivery takes one due reminder and the person's allowance for it; no claim when the
// reminder was settled without sending (worked tells whether anything was due).
func (worker *WeChatAlarmReminderWorker) claimDelivery(ctx context.Context, now time.Time) (*claimedDelivery, bool, error) {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	var claim claimedDelivery
	var state string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
SELECT d.tenant_id::text, d.alarm_id::text, d.subject_issuer, d.subject, d.state, d.attempts,
  r.site_id::text, r.title, r.severity, r.occurred_at, r.expires_at
FROM notification_runtime.wechat_alarm_delivery d
JOIN notification_runtime.wechat_alarm_reminder r ON r.tenant_id = d.tenant_id AND r.alarm_id = d.alarm_id
WHERE (d.state = 'PENDING' AND d.next_attempt_at <= $1) OR (d.state = 'SENDING' AND d.lease_until <= $1)
ORDER BY d.next_attempt_at LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, now.UTC()).Scan(
		&claim.person.TenantID, &claim.alarmID, &claim.person.SubjectIssuer, &claim.person.Subject, &state, &claim.attempts,
		&claim.siteID, &claim.title, &claim.severity, &claim.occurred, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim Alarm reminder delivery: %w", err)
	}
	finish := func(state string) (*claimedDelivery, bool, error) {
		if err := setDeliveryState(ctx, tx, claim, state, claim.attempts, now, now, nil); err != nil {
			return nil, true, err
		}
		return nil, true, tx.Commit(ctx)
	}
	switch {
	// A send cut off midway may have reached WeChat: never send it again.
	case state == "SENDING":
		return finish("FAILED")
	case !now.Before(expiresAt):
		return finish("EXPIRED")
	}
	taken, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = remaining - 1, updated_at = $4
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = 'ALARM' AND remaining > 0`,
		claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, now.UTC())
	if err != nil {
		return nil, true, err
	}
	if taken.RowsAffected() == 0 {
		return finish("NO_REMINDER_LEFT")
	}
	claim.attempts++
	claim.leaseUntil = now.Add(alarmReminderLease).UTC()
	if err := setDeliveryState(ctx, tx, claim, "SENDING", claim.attempts, now, now, &claim.leaseUntil); err != nil {
		return nil, true, err
	}
	return &claim, true, tx.Commit(ctx)
}

// settle records how a send ended; when WeChat says the person has none left, or has no WeChat,
// their count is wrong and is cleared.
func (worker *WeChatAlarmReminderWorker) settle(ctx context.Context, claim claimedDelivery, now time.Time, state string, clearAllowance bool) error {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if held, err := settleClaim(ctx, tx, claim, state, now, now); err != nil || !held {
		return err
	}
	if clearAllowance {
		if _, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = 0, updated_at = $4
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = 'ALARM'`,
			claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, now.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// retry gives the reminder back and tries again later, or gives up after the last attempt.
func (worker *WeChatAlarmReminderWorker) retry(ctx context.Context, claim claimedDelivery, now time.Time) error {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, next := "PENDING", now.Add(time.Duration(claim.attempts*claim.attempts)*time.Minute)
	if claim.attempts >= alarmReminderAttempts {
		state, next = "FAILED", now
	}
	if held, err := settleClaim(ctx, tx, claim, state, next, now); err != nil || !held {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = remaining + 1, updated_at = $4
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = 'ALARM'`,
		claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// settleClaim ends a send this worker still holds; false when its lease ran out and another
// worker settled the delivery meanwhile.
func settleClaim(ctx context.Context, tx pgx.Tx, claim claimedDelivery, state string, next, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_alarm_delivery
SET state = $5, next_attempt_at = $6, lease_until = NULL, updated_at = $7
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid AND subject_issuer = $3 AND subject = $4 AND state = 'SENDING' AND lease_until = $8`,
		claim.person.TenantID, claim.alarmID, claim.person.SubjectIssuer, claim.person.Subject, state, next.UTC(), now.UTC(), claim.leaseUntil)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func setDeliveryState(ctx context.Context, tx pgx.Tx, claim claimedDelivery, state string, attempts int, next, now time.Time, leaseUntil *time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_alarm_delivery
SET state = $5, attempts = $6, next_attempt_at = $7, lease_until = $8, updated_at = $9
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid AND subject_issuer = $3 AND subject = $4`,
		claim.person.TenantID, claim.alarmID, claim.person.SubjectIssuer, claim.person.Subject, state, attempts, next.UTC(), leaseUntil, now.UTC())
	return err
}

var severityLabels = map[string]string{"CRITICAL": "紧急", "MAJOR": "重要"}

// message fills 监控告警通知: 告警标题, 告警等级, 监控设备, 告警地点 and 告警时间 in the Site's own time.
func (worker *WeChatAlarmReminderWorker) message(ctx context.Context, claim claimedDelivery) (WeChatMessage, error) {
	site, err := worker.Sites.Site(ctx, claim.person.TenantID, claim.siteID)
	if err != nil {
		return WeChatMessage{}, err
	}
	location, err := time.LoadLocation(site.Timezone)
	if err != nil {
		return WeChatMessage{}, fmt.Errorf("the Site's time zone %q: %w", site.Timezone, err)
	}
	return WeChatMessage{
		TemplateID: worker.TemplateID,
		Page:       "pages/alarm/index?id=" + claim.alarmID,
		Data: map[string]string{
			"thing10":       clip(claim.title, 20),
			"short_thing15": severityLabels[claim.severity],
			// Alarms carry no device yet; the detail page says the same.
			"thing9": "未关联设备",
			"thing4": clip(site.Name, 20),
			"time2":  claim.occurred.In(location).Format("2006年1月2日 15:04"),
		},
	}, nil
}

// clip keeps a value within WeChat's limit for the keyword, marking what was cut.
func clip(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "…"
}
