package notificationservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

// Alarm delivery to people (P0-4, docs/specs/alarm-delivery.md). RecordAlarmEvent keeps what
// Notification knows of each Alarm; the dispatcher decides (DecideDelivery) which messages to
// queue for it, and sends queued messages one at a time through the alert adapter, which holds
// the mail and WeCom secrets.

// DeliveryMode is ALARM_DELIVERY_MODE.
type DeliveryMode string

const (
	// DeliveryOff runs no dispatcher: Alarms are still recorded, nothing is decided or sent.
	DeliveryOff DeliveryMode = "off"
	// DeliveryDryRun decides and queues as live would, and asks the adapter to check each message
	// without sending it (DRY_RUN).
	DeliveryDryRun DeliveryMode = "dry-run"
	DeliveryLive   DeliveryMode = "live"
)

func ParseDeliveryMode(value string) (DeliveryMode, error) {
	switch mode := DeliveryMode(strings.TrimSpace(value)); mode {
	case "", DeliveryOff:
		return DeliveryOff, nil
	case DeliveryDryRun, DeliveryLive:
		return mode, nil
	default:
		return "", fmt.Errorf("ALARM_DELIVERY_MODE must be off, dry-run or live, not %q", value)
	}
}

const (
	// One send and three retries, 1, 4 and 9 minutes apart, as for WeChat reminders.
	deliveryAttempts = 4
	deliveryLease    = 2 * time.Minute
	planLease        = time.Minute
)

var (
	// ErrDeliveryRejected is the adapter refusing the message for good (bad address, channel not
	// configured): it is not retried.
	ErrDeliveryRejected = errors.New("alert adapter rejected the message")
)

// OutboundMessage is one message as the adapter's POST /send takes it.
type OutboundMessage struct {
	Channel DeliveryChannel `json:"channel"`
	// To is the mail address (smtp), the WeCom user id (wecom_app) or empty (wecom_bot).
	To      string `json:"to,omitempty"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	DryRun  bool   `json:"dryRun"`
	// Key is the message's dedup key, for the adapter's log.
	Key string `json:"key"`
}

type DeliverySender interface {
	SendAlarmMessage(ctx context.Context, message OutboundMessage) error
}

// recordAlarmDelivery keeps the Alarm for delivery, in the transaction that records its event.
// The newest version wins for what the Alarm is; an acknowledgement or a clear, once seen, stays.
func recordAlarmDelivery(ctx context.Context, tx pgx.Tx, event AlarmEvent, recordedAt time.Time) error {
	alarmType := strings.TrimSpace(event.Attributes["alarmType"])
	title := strings.TrimSpace(event.Attributes["title"])
	if alarmType == "" {
		alarmType = "unknown"
	}
	if title == "" {
		title = alarmType
	}
	version := event.AlarmVersion
	if version < 1 {
		version = 1
	}
	var acknowledgedAt, clearedAt *time.Time
	var acknowledgedBy *string
	occurred := event.OccurredAt.UTC()
	began := occurred
	switch event.Action {
	case AlarmAcknowledged:
		acknowledgedAt = &occurred
		if by := strings.TrimSpace(event.Attributes["acknowledgedBy"]); by != "" {
			acknowledgedBy = &by
		}
	case AlarmCleared:
		clearedAt = &occurred
	}
	_, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.alarm_delivery AS d
  (tenant_id, alarm_id, site_id, alarm_type, title, summary, severity, alarm_version, began_at,
   acknowledged_at, acknowledged_by, cleared_at, next_due_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, $13)
ON CONFLICT (tenant_id, alarm_id) DO UPDATE SET
  alarm_type = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.alarm_type ELSE d.alarm_type END,
  title = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.title ELSE d.title END,
  summary = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.summary ELSE d.summary END,
  severity = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.severity ELSE d.severity END,
  alarm_version = GREATEST(d.alarm_version, EXCLUDED.alarm_version),
  began_at = LEAST(d.began_at, EXCLUDED.began_at),
  acknowledged_at = COALESCE(d.acknowledged_at, EXCLUDED.acknowledged_at),
  acknowledged_by = COALESCE(d.acknowledged_by, EXCLUDED.acknowledged_by),
  cleared_at = COALESCE(d.cleared_at, EXCLUDED.cleared_at),
  next_due_at = EXCLUDED.next_due_at,
  updated_at = EXCLUDED.updated_at`,
		event.TenantID, event.AlarmID, event.SiteID, clip(alarmType, 256), clip(title, 512), clip(event.Attributes["summary"], 2048),
		string(event.CurrentSeverity), version, began, acknowledgedAt, acknowledgedBy, clearedAt, recordedAt.UTC())
	return err
}

// AlarmDeliveryDispatcher decides and sends Alarm messages for every Tenant.
type AlarmDeliveryDispatcher struct {
	Scheduler  *Scheduler
	Routing    AlarmRoutingTable
	Recipients RecipientDirectory
	Sender     DeliverySender
	Mode       DeliveryMode
	// ConsoleURL is the web console's origin, for the link to the Alarm in each message.
	ConsoleURL string
	Logger     *slog.Logger
}

func (dispatcher *AlarmDeliveryDispatcher) log() *slog.Logger {
	if dispatcher.Logger == nil {
		return slog.Default()
	}
	return dispatcher.Logger
}

// RunOnce decides for one due Alarm or sends one queued message. False when nothing is due.
func (dispatcher *AlarmDeliveryDispatcher) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if dispatcher.Mode != DeliveryDryRun && dispatcher.Mode != DeliveryLive {
		return false, errors.New("alarm delivery is off")
	}
	if worked, err := dispatcher.planOne(ctx, now); worked || err != nil {
		return worked, err
	}
	return dispatcher.sendOne(ctx, now)
}

func (dispatcher *AlarmDeliveryDispatcher) planOne(ctx context.Context, now time.Time) (bool, error) {
	tx, err := dispatcher.Scheduler.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var alarm DeliveryAlarm
	var severity string
	var acknowledgedBy *string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, alarm_id::text, site_id::text, alarm_type, title, summary, severity, alarm_version, began_at,
       acknowledged_at, acknowledged_by, cleared_at
FROM notification_runtime.alarm_delivery
WHERE next_due_at <= $1 AND (lease_until IS NULL OR lease_until <= $1)
ORDER BY next_due_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(
		&alarm.TenantID, &alarm.AlarmID, &alarm.SiteID, &alarm.AlarmType, &alarm.Title, &alarm.Summary, &severity, &alarm.Version, &alarm.BeganAt,
		&alarm.AcknowledgedAt, &acknowledgedBy, &alarm.ClearedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim Alarm to deliver: %w", err)
	}
	alarm.Severity = alarmmodel.Severity(severity)
	if acknowledgedBy != nil {
		alarm.AcknowledgedBy = *acknowledgedBy
	}
	setNext := func(next *time.Time) error {
		var value any
		if next != nil {
			value = next.UTC()
		}
		_, err := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery SET next_due_at = $3, lease_until = NULL, updated_at = $4
WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`, alarm.TenantID, alarm.AlarmID, value, now.UTC())
		return err
	}
	routing, ok := dispatcher.Routing.Site(alarm.SiteID)
	if !ok {
		// A Site without a routing file tells nobody; it is looked at again when the Alarm changes.
		if err := setNext(nil); err != nil {
			return true, err
		}
		return true, tx.Commit(ctx)
	}
	facts, err := deliveryFacts(ctx, tx, alarm, routing, now)
	if err != nil {
		return true, err
	}
	decision := DecideDelivery(alarm, routing, facts, now)
	for _, message := range decision.Messages {
		state, reason := "PENDING", any(nil)
		if message.Suppressed {
			state, reason = "SUPPRESSED", message.Reason
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.alarm_delivery_attempt
  (tenant_id, alarm_id, dedup_key, site_id, kind, step, channel, recipient, state, reason, attempts, next_attempt_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, 0, $11, $11, $11)
ON CONFLICT (tenant_id, dedup_key) DO NOTHING`,
			alarm.TenantID, alarm.AlarmID, DedupKey(alarm.AlarmID, message), alarm.SiteID, string(message.Kind), message.Step,
			string(message.Channel), message.Recipient, state, reason, now.UTC()); err != nil {
			return true, err
		}
		if message.Suppressed {
			dispatcher.log().Warn("alarm_delivery_suppressed", "alarm_id", alarm.AlarmID, "kind", string(message.Kind), "channel", string(message.Channel), "recipient", message.Recipient, "reason", message.Reason)
		}
	}
	if decision.Held != "" {
		dispatcher.log().Info("alarm_delivery_held", "alarm_id", alarm.AlarmID, "alarm_type", alarm.AlarmType, "held", decision.Held)
	}
	if err := setNext(decision.NextDueAt); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func deliveryFacts(ctx context.Context, tx pgx.Tx, alarm DeliveryAlarm, routing SiteRouting, now time.Time) (DeliveryFacts, error) {
	var facts DeliveryFacts
	if routing.RootCauseAlarmType != "" && alarm.AlarmType != routing.RootCauseAlarmType {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_runtime.alarm_delivery
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND alarm_type = $3 AND cleared_at IS NULL AND alarm_id <> $4::uuid)`,
			alarm.TenantID, alarm.SiteID, routing.RootCauseAlarmType, alarm.AlarmID).Scan(&facts.RootCauseActive); err != nil {
			return facts, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT kind, step, channel, recipient, created_at, state NOT IN ('SUPPRESSED','FAILED')
FROM notification_runtime.alarm_delivery_attempt WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid ORDER BY created_at, dedup_key`, alarm.TenantID, alarm.AlarmID)
	if err != nil {
		return facts, err
	}
	facts.Queued, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (QueuedMessage, error) {
		var message QueuedMessage
		var kind, channel string
		err := row.Scan(&kind, &message.Step, &channel, &message.Recipient, &message.QueuedAt, &message.Reached)
		message.Kind, message.Channel = DeliveryKind(kind), DeliveryChannel(channel)
		return message, err
	})
	if err != nil {
		return facts, err
	}
	rows, err = tx.Query(ctx, `SELECT recipient, count(*) FROM notification_runtime.alarm_delivery_attempt
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND kind IN ('FIRE','ESCALATE') AND state IN ('PENDING','SENDING','SENT','DRY_RUN') AND created_at > $3
GROUP BY recipient`, alarm.TenantID, alarm.SiteID, now.Add(-time.Hour).UTC())
	if err != nil {
		return facts, err
	}
	facts.SentLastHour = map[string]int{}
	_, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
		var recipient string
		var count int
		err := row.Scan(&recipient, &count)
		facts.SentLastHour[recipient] = count
		return struct{}{}, err
	})
	return facts, err
}

type claimedAttempt struct {
	tenantID, alarmID, dedupKey string
	kind                        DeliveryKind
	step                        int
	channel                     DeliveryChannel
	recipient                   string
	attempts                    int
	leaseUntil                  time.Time
}

func (dispatcher *AlarmDeliveryDispatcher) sendOne(ctx context.Context, now time.Time) (bool, error) {
	claim, err := dispatcher.claimAttempt(ctx, now)
	if err != nil || claim == nil {
		return false, err
	}
	message, err := dispatcher.outbound(ctx, *claim)
	if err != nil {
		dispatcher.log().Error("alarm_delivery_message_failed", "error_code", "ALARM_DELIVERY_MESSAGE_FAILED", "alarm_id", claim.alarmID, "key", claim.dedupKey, "reason", err.Error())
		return true, dispatcher.settle(ctx, *claim, now, "FAILED", "NO_CONTACT", false)
	}
	message.DryRun = dispatcher.Mode != DeliveryLive
	err = dispatcher.Sender.SendAlarmMessage(ctx, message)
	switch {
	case err == nil && message.DryRun:
		dispatcher.log().Info("alarm_delivery_dry_run", "alarm_id", claim.alarmID, "kind", string(claim.kind), "step", claim.step, "channel", string(claim.channel), "recipient", claim.recipient)
		return true, dispatcher.settle(ctx, *claim, now, "DRY_RUN", "", false)
	case err == nil:
		dispatcher.log().Info("alarm_delivery_sent", "alarm_id", claim.alarmID, "kind", string(claim.kind), "step", claim.step, "channel", string(claim.channel), "recipient", claim.recipient)
		return true, dispatcher.settle(ctx, *claim, now, "SENT", "", false)
	case errors.Is(err, ErrDeliveryRejected):
		dispatcher.log().Error("alarm_delivery_rejected", "error_code", "ALARM_DELIVERY_REJECTED", "alarm_id", claim.alarmID, "key", claim.dedupKey)
		return true, dispatcher.settle(ctx, *claim, now, "FAILED", "REJECTED", false)
	default:
		dispatcher.log().Warn("alarm_delivery_send_failed", "error_code", "ALARM_DELIVERY_SEND_FAILED", "alarm_id", claim.alarmID, "key", claim.dedupKey, "attempt", claim.attempts)
		return true, dispatcher.settle(ctx, *claim, now, "PENDING", "", true)
	}
}

func (dispatcher *AlarmDeliveryDispatcher) claimAttempt(ctx context.Context, now time.Time) (*claimedAttempt, error) {
	tx, err := dispatcher.Scheduler.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	claim := claimedAttempt{}
	var kind, channel string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, alarm_id::text, dedup_key, kind, step, channel, recipient, attempts
FROM notification_runtime.alarm_delivery_attempt
WHERE next_attempt_at <= $1 AND (state = 'PENDING' OR (state = 'SENDING' AND lease_until <= $1))
ORDER BY next_attempt_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(&claim.tenantID, &claim.alarmID, &claim.dedupKey, &kind, &claim.step, &channel, &claim.recipient, &claim.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim Alarm message: %w", err)
	}
	claim.kind, claim.channel = DeliveryKind(kind), DeliveryChannel(channel)
	claim.attempts++
	claim.leaseUntil = now.Add(deliveryLease).UTC()
	if _, err := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt SET state = 'SENDING', attempts = $3, lease_until = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND dedup_key = $2`, claim.tenantID, claim.dedupKey, claim.attempts, claim.leaseUntil, now.UTC()); err != nil {
		return nil, err
	}
	return &claim, tx.Commit(ctx)
}

// settle records how a claimed message ended; a retry waits attempts² minutes, until the last.
func (dispatcher *AlarmDeliveryDispatcher) settle(ctx context.Context, claim claimedAttempt, now time.Time, state, reason string, retry bool) error {
	next := now
	if retry {
		if claim.attempts >= deliveryAttempts {
			state, reason = "FAILED", "GAVE_UP"
			dispatcher.log().Error("alarm_delivery_gave_up", "error_code", "ALARM_DELIVERY_GAVE_UP", "alarm_id", claim.alarmID, "key", claim.dedupKey)
		} else {
			next = now.Add(time.Duration(claim.attempts*claim.attempts) * time.Minute)
		}
	}
	var reasonValue any
	if reason != "" {
		reasonValue = reason
	}
	_, err := dispatcher.Scheduler.pool.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = $3, reason = $4, next_attempt_at = $5, lease_until = NULL, updated_at = $6
WHERE tenant_id = $1::uuid AND dedup_key = $2 AND state = 'SENDING' AND lease_until = $7`,
		claim.tenantID, claim.dedupKey, state, reasonValue, next.UTC(), now.UTC(), claim.leaseUntil)
	return err
}

// outbound renders the claimed message for its channel and recipient.
func (dispatcher *AlarmDeliveryDispatcher) outbound(ctx context.Context, claim claimedAttempt) (OutboundMessage, error) {
	var alarm DeliveryAlarm
	var severity string
	var acknowledgedBy *string
	if err := dispatcher.Scheduler.pool.QueryRow(ctx, `SELECT tenant_id::text, alarm_id::text, site_id::text, alarm_type, title, summary, severity, alarm_version, began_at,
       acknowledged_at, acknowledged_by, cleared_at FROM notification_runtime.alarm_delivery WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`,
		claim.tenantID, claim.alarmID).Scan(&alarm.TenantID, &alarm.AlarmID, &alarm.SiteID, &alarm.AlarmType, &alarm.Title, &alarm.Summary, &severity, &alarm.Version,
		&alarm.BeganAt, &alarm.AcknowledgedAt, &acknowledgedBy, &alarm.ClearedAt); err != nil {
		return OutboundMessage{}, fmt.Errorf("read the Alarm: %w", err)
	}
	alarm.Severity = alarmmodel.Severity(severity)
	if acknowledgedBy != nil {
		alarm.AcknowledgedBy = *acknowledgedBy
	}
	routing, ok := dispatcher.Routing.Site(alarm.SiteID)
	if !ok {
		return OutboundMessage{}, errors.New("the Site has no routing any more")
	}
	to := ""
	switch claim.channel {
	case ChannelSMTP:
		to = dispatcher.Recipients.Recipients[claim.recipient].Email
	case ChannelWeComApp:
		to = dispatcher.Recipients.Recipients[claim.recipient].WeComUserID
	}
	if claim.channel != ChannelWeComBot && to == "" {
		return OutboundMessage{}, fmt.Errorf("recipient %s has no contact for %s", claim.recipient, claim.channel)
	}
	subject, text := RenderDeliveryMessage(alarm, routing, claim.kind, claim.step, dispatcher.ConsoleURL)
	return OutboundMessage{Channel: claim.channel, To: to, Subject: subject, Text: text, Key: claim.dedupKey}, nil
}

var deliverySeverityLabels = map[alarmmodel.Severity]string{
	alarmmodel.SeverityCritical: "紧急", alarmmodel.SeverityMajor: "重要", alarmmodel.SeverityMinor: "次要", alarmmodel.SeverityWarning: "警告", alarmmodel.SeverityInfo: "提示",
}

// RenderDeliveryMessage is the subject and text of one message, times in the Site's zone.
func RenderDeliveryMessage(alarm DeliveryAlarm, routing SiteRouting, kind DeliveryKind, step int, consoleURL string) (string, string) {
	location, err := time.LoadLocation(routing.Timezone)
	if err != nil {
		location = time.UTC
	}
	clock := func(at time.Time) string { return at.In(location).Format("2006-01-02 15:04") }
	label := deliverySeverityLabels[alarm.Severity]
	var subject string
	var body strings.Builder
	switch kind {
	case DeliveryFire:
		subject = fmt.Sprintf("【告警·%s】%s：%s", label, routing.SiteName, alarm.Title)
	case DeliveryEscalate:
		subject = fmt.Sprintf("【告警升级·%s】%s：%s（无人确认）", label, routing.SiteName, alarm.Title)
	case DeliveryAck:
		subject = fmt.Sprintf("【已确认】%s：%s", routing.SiteName, alarm.Title)
	case DeliveryRecovery:
		subject = fmt.Sprintf("【已恢复】%s：%s", routing.SiteName, alarm.Title)
	}
	body.WriteString(subject + "\n")
	if summary := strings.TrimSpace(alarm.Summary); summary != "" && (kind == DeliveryFire || kind == DeliveryEscalate) {
		body.WriteString(clip(summary, 600) + "\n")
	}
	fmt.Fprintf(&body, "开始：%s（%s）\n", clock(alarm.BeganAt), routing.Timezone)
	switch kind {
	case DeliveryFire, DeliveryEscalate:
		route, _ := routing.Route(alarm.AlarmType, alarm.Severity)
		if step+1 < len(route.Steps) {
			fmt.Fprintf(&body, "%d 分钟内无人确认，将通知下一级。\n", route.Steps[step+1].AfterMinutes-route.Steps[step].AfterMinutes)
		}
	case DeliveryAck:
		if alarm.AcknowledgedAt != nil {
			by := alarm.AcknowledgedBy
			if by == "" {
				by = "值班人员"
			}
			fmt.Fprintf(&body, "确认：%s，%s\n", clock(*alarm.AcknowledgedAt), by)
		}
	case DeliveryRecovery:
		if alarm.ClearedAt != nil {
			fmt.Fprintf(&body, "恢复：%s，持续 %d 分钟\n", clock(*alarm.ClearedAt), int(alarm.ClearedAt.Sub(alarm.BeganAt).Minutes()))
		}
	}
	if consoleURL = strings.TrimRight(strings.TrimSpace(consoleURL), "/"); consoleURL != "" {
		if kind == DeliveryFire || kind == DeliveryEscalate {
			fmt.Fprintf(&body, "确认告警：%s/operations/alarms?site=%s&view=all&inspect=%s\n", consoleURL, alarm.SiteID, alarm.AlarmID)
		} else {
			fmt.Fprintf(&body, "查看：%s/operations/alarms?site=%s&view=all&inspect=%s\n", consoleURL, alarm.SiteID, alarm.AlarmID)
		}
	}
	return subject, strings.TrimRight(body.String(), "\n")
}

// HTTPDeliverySender posts messages to the alert adapter's POST /send with a bearer token read
// from a file at each send (rotating the token needs no restart).
type HTTPDeliverySender struct {
	URL       string
	TokenFile string
	Client    *http.Client
}

func (sender HTTPDeliverySender) SendAlarmMessage(ctx context.Context, message OutboundMessage) error {
	token, err := os.ReadFile(sender.TokenFile)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return errors.New("alarm send token file is unreadable or empty")
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, sender.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := sender.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("alert adapter unreachable: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity || response.StatusCode == http.StatusNotImplemented:
		return fmt.Errorf("%w: status %d", ErrDeliveryRejected, response.StatusCode)
	default:
		return fmt.Errorf("alert adapter status %d", response.StatusCode)
	}
}
