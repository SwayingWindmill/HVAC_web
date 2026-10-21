package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The WeChat reminder worker (#664, docs/specs/miniprogram-wechat.md). Alarm reminders and daily
// summaries become deliveries, one per person: a reminder is taken from the person's allowance
// before it is sent, given back when sending fails, and never sent after the delivery's deadline
// or a second time when WeChat may already have it.

const (
	// One send and three retries, 1, 4 and 9 minutes apart.
	reminderAttempts = 4
	reminderLease    = 2 * time.Minute
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

// RecipientDecider is IAM: which of these people may get this kind of reminder of the Site now
// (an Alarm reminder names its Alarm).
type RecipientDecider interface {
	AllowedRecipients(ctx context.Context, kind ReminderKind, tenantID, siteID, alarmID string, people []WeChatPerson) ([]WeChatPerson, error)
}

// SiteFacts are what a reminder says about the Site: its name, and its time zone for times and days.
type SiteFacts struct {
	Name     string
	Timezone string
}

// SiteDirectory is the Registry's Site.
type SiteDirectory interface {
	Site(ctx context.Context, tenantID, siteID string) (SiteFacts, error)
}

// DailyEnergy is the Site's electricity and cooling for the Site day starting at day, nil when
// not read.
type DailyEnergy interface {
	DayEnergy(ctx context.Context, tenantID, siteID, timezone string, day time.Time) (electricityKWh, coolingKWh *float64, err error)
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

type WeChatReminderWorker struct {
	Scheduler  *Scheduler
	Recipients RecipientDecider
	Sites      SiteDirectory
	Energy     DailyEnergy
	Sender     WeChatSender
	// The templates in 微信公众平台 → 订阅消息 → 我的模板: 监控告警通知 and 昨日能效数据查询提醒.
	AlarmTemplateID string
	DailyTemplateID string
	Logger          *slog.Logger

	// nextDailyPlan spaces out working out the day's summaries: once a minute is enough.
	nextDailyPlan time.Time
	// sites keeps each Site's name and time zone for an hour, so planning reads the Registry
	// seldom, not every minute.
	sites map[string]cachedSite
}

type cachedSite struct {
	facts    SiteFacts
	location *time.Location
	until    time.Time
}

const siteFactsKept = time.Hour

// siteAt is the Site with its time zone loaded, read from the Registry at most once an hour.
func (worker *WeChatReminderWorker) siteAt(ctx context.Context, tenantID, siteID string, now time.Time) (SiteFacts, *time.Location, error) {
	key := tenantID + "/" + siteID
	if cached, ok := worker.sites[key]; ok && now.Before(cached.until) {
		return cached.facts, cached.location, nil
	}
	facts, err := worker.Sites.Site(ctx, tenantID, siteID)
	if err != nil {
		return SiteFacts{}, nil, err
	}
	location, err := time.LoadLocation(facts.Timezone)
	if err != nil {
		return SiteFacts{}, nil, fmt.Errorf("the Site's time zone %q: %w", facts.Timezone, err)
	}
	if worker.sites == nil {
		worker.sites = map[string]cachedSite{}
	}
	worker.sites[key] = cachedSite{facts: facts, location: location, until: now.Add(siteFactsKept)}
	return facts, location, nil
}

func (worker *WeChatReminderWorker) log() *slog.Logger {
	if worker.Logger == nil {
		return slog.Default()
	}
	return worker.Logger
}

// RunOnce does one piece of due work: who gets a pending Alarm reminder, whose daily summaries
// are due, or one person's send. False when nothing is due.
func (worker *WeChatReminderWorker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if worked, err := worker.expandAlarm(ctx, now); worked || err != nil {
		return worked, err
	}
	if !now.Before(worker.nextDailyPlan) {
		worker.nextDailyPlan = now.Add(time.Minute)
		if worked, err := worker.planDaily(ctx, now); worked || err != nil {
			return worked, err
		}
	}
	return worker.deliverOne(ctx, now)
}

// askRecipients asks IAM about the people a page at a time.
func (worker *WeChatReminderWorker) askRecipients(ctx context.Context, kind ReminderKind, tenantID, siteID, alarmID string, people []WeChatPerson) ([]WeChatPerson, error) {
	var allowed []WeChatPerson
	for start := 0; start < len(people); start += reminderHoldersPerQuestion {
		page, err := worker.Recipients.AllowedRecipients(ctx, kind, tenantID, siteID, alarmID, people[start:min(start+reminderHoldersPerQuestion, len(people))])
		if err != nil {
			return nil, fmt.Errorf("ask IAM who may get a %s reminder: %w", kind, err)
		}
		allowed = append(allowed, page...)
	}
	return allowed, nil
}

// addDeliveries notes one delivery per person in this state (PENDING to send), each at most once.
func addDeliveries(ctx context.Context, tx pgx.Tx, kind ReminderKind, reference, siteID, state string, people []WeChatPerson, expiresAt, now time.Time) error {
	for _, person := range people {
		if _, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.wechat_reminder_delivery
  (tenant_id, kind, reference, subject_issuer, subject, site_id, state, attempts, next_attempt_at, expires_at, created_at, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $9, 0, $7, $8, $7, $7)
ON CONFLICT DO NOTHING`, person.TenantID, string(kind), reference, person.SubjectIssuer, person.Subject, siteID, now.UTC(), expiresAt.UTC(), state); err != nil {
			return err
		}
	}
	return nil
}

type claimedDelivery struct {
	person    WeChatPerson
	kind      ReminderKind
	reference string
	siteID    string
	attempts  int
	// leaseUntil fences the claim: a send that outlives it changes nothing.
	leaseUntil time.Time
}

func (worker *WeChatReminderWorker) deliverOne(ctx context.Context, now time.Time) (bool, error) {
	claim, worked, err := worker.claimDelivery(ctx, now)
	if err != nil || claim == nil {
		return worked, err
	}
	stage := "message"
	message, err := worker.message(ctx, *claim, now)
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
		worker.log().Warn("wechat_reminder_outcome_unknown", "error_code", "WECHAT_REMINDER_OUTCOME_UNKNOWN", "kind", string(claim.kind), "reference", claim.reference)
		return true, worker.settle(ctx, *claim, now, "FAILED", false)
	default:
		worker.log().Warn("wechat_reminder_not_sent", "error_code", "WECHAT_REMINDER_NOT_SENT", "kind", string(claim.kind), "reference", claim.reference, "stage", stage, "attempt", claim.attempts)
		return true, worker.retry(ctx, *claim, now)
	}
}

func (worker *WeChatReminderWorker) message(ctx context.Context, claim claimedDelivery, now time.Time) (WeChatMessage, error) {
	if claim.kind == ReminderDaily {
		return worker.dailyMessage(ctx, claim, now)
	}
	return worker.alarmMessage(ctx, claim, now)
}

// claimDelivery takes one due delivery and the person's allowance for it; no claim when the
// delivery was settled without sending (worked tells whether anything was due).
func (worker *WeChatReminderWorker) claimDelivery(ctx context.Context, now time.Time) (*claimedDelivery, bool, error) {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	var claim claimedDelivery
	var state string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, kind, reference, subject_issuer, subject, site_id::text, state, attempts, expires_at
FROM notification_runtime.wechat_reminder_delivery
WHERE (state = 'PENDING' AND next_attempt_at <= $1) OR (state = 'SENDING' AND lease_until <= $1)
ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(
		&claim.person.TenantID, &claim.kind, &claim.reference, &claim.person.SubjectIssuer, &claim.person.Subject, &claim.siteID, &state, &claim.attempts, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim WeChat reminder delivery: %w", err)
	}
	finish := func(state string) (*claimedDelivery, bool, error) {
		if err := setDeliveryState(ctx, tx, claim, state, now, now, nil); err != nil {
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
	taken, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = remaining - 1, updated_at = $5
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = $4 AND remaining > 0`,
		claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, string(claim.kind), now.UTC())
	if err != nil {
		return nil, true, err
	}
	if taken.RowsAffected() == 0 {
		return finish("NO_REMINDER_LEFT")
	}
	claim.attempts++
	claim.leaseUntil = now.Add(reminderLease).UTC()
	if err := setDeliveryState(ctx, tx, claim, "SENDING", now, now, &claim.leaseUntil); err != nil {
		return nil, true, err
	}
	return &claim, true, tx.Commit(ctx)
}

// settle records how a send ended; when WeChat says the person has none left, or has no WeChat,
// their count is wrong and is cleared.
func (worker *WeChatReminderWorker) settle(ctx context.Context, claim claimedDelivery, now time.Time, state string, clearAllowance bool) error {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if held, err := settleClaim(ctx, tx, claim, state, now, now); err != nil || !held {
		return err
	}
	if clearAllowance {
		if _, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = 0, updated_at = $5
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = $4`,
			claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, string(claim.kind), now.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// retry gives the reminder back and tries again later, or gives up after the last attempt.
func (worker *WeChatReminderWorker) retry(ctx context.Context, claim claimedDelivery, now time.Time) error {
	tx, err := worker.Scheduler.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state, next := "PENDING", now.Add(time.Duration(claim.attempts*claim.attempts)*time.Minute)
	if claim.attempts >= reminderAttempts {
		state, next = "FAILED", now
	}
	if held, err := settleClaim(ctx, tx, claim, state, next, now); err != nil || !held {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_allowance SET remaining = remaining + 1, updated_at = $5
WHERE tenant_id = $1::uuid AND subject_issuer = $2 AND subject = $3 AND kind = $4`,
		claim.person.TenantID, claim.person.SubjectIssuer, claim.person.Subject, string(claim.kind), now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// settleClaim ends a send this worker still holds; false when its lease ran out and another
// worker settled the delivery meanwhile.
func settleClaim(ctx context.Context, tx pgx.Tx, claim claimedDelivery, state string, next, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_delivery
SET state = $6, next_attempt_at = $7, lease_until = NULL, updated_at = $8
WHERE tenant_id = $1::uuid AND kind = $2 AND reference = $3 AND subject_issuer = $4 AND subject = $5 AND state = 'SENDING' AND lease_until = $9`,
		claim.person.TenantID, string(claim.kind), claim.reference, claim.person.SubjectIssuer, claim.person.Subject, state, next.UTC(), now.UTC(), claim.leaseUntil)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func setDeliveryState(ctx context.Context, tx pgx.Tx, claim claimedDelivery, state string, next, now time.Time, leaseUntil *time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE notification_runtime.wechat_reminder_delivery
SET state = $6, attempts = $7, next_attempt_at = $8, lease_until = $9, updated_at = $10
WHERE tenant_id = $1::uuid AND kind = $2 AND reference = $3 AND subject_issuer = $4 AND subject = $5`,
		claim.person.TenantID, string(claim.kind), claim.reference, claim.person.SubjectIssuer, claim.person.Subject, state, claim.attempts, next.UTC(), leaseUntil, now.UTC())
	return err
}

// clip keeps a value within WeChat's limit for the keyword, marking what was cut.
func clip(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "…"
}
