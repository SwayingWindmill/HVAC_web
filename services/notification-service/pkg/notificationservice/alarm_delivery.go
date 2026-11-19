package notificationservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	deliveryAttempts             = 4
	deliveryLease                = 2 * time.Minute
	planLease                    = time.Minute
	defaultDeliveryRunTimeout    = 30 * time.Second
	defaultDeliverySendTimeout   = 20 * time.Second
	defaultDeliverySettleTimeout = 5 * time.Second
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
	var acknowledgedAt, clearedAt, suppressedUntil *time.Time
	var acknowledgedBy, deviceID *string
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
	if trimmed := strings.TrimSpace(event.Attributes["deviceId"]); trimmed != "" {
		clipped := clip(trimmed, 256)
		deviceID = &clipped
	}
	switch event.Action {
	case AlarmSuppressed, AlarmCreated, AlarmSeverityChanged, AlarmAcknowledged:
		if raw := strings.TrimSpace(event.Attributes["suppressedUntil"]); raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				utc := parsed.UTC()
				suppressedUntil = &utc
			}
		}
	}
	clearSuppression := event.Action == AlarmUnsuppressed || event.Action == AlarmCleared
	_, err := tx.Exec(ctx, `
INSERT INTO notification_runtime.alarm_delivery AS d
  (tenant_id, alarm_id, site_id, alarm_type, title, summary, severity, alarm_version, began_at,
   acknowledged_at, acknowledged_by, cleared_at, device_id, suppressed_until, next_due_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15, $15)
ON CONFLICT (tenant_id, alarm_id) DO UPDATE SET
  alarm_type = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.alarm_type ELSE d.alarm_type END,
  title = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.title ELSE d.title END,
  summary = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.summary ELSE d.summary END,
  severity = CASE WHEN EXCLUDED.alarm_version > d.alarm_version THEN EXCLUDED.severity ELSE d.severity END,
  device_id = CASE
    WHEN EXCLUDED.alarm_version > d.alarm_version THEN COALESCE(EXCLUDED.device_id, d.device_id)
    ELSE COALESCE(d.device_id, EXCLUDED.device_id)
  END,
  suppressed_until = CASE
    WHEN EXCLUDED.alarm_version > d.alarm_version THEN
      CASE
        WHEN $16 THEN NULL
        ELSE COALESCE(EXCLUDED.suppressed_until, d.suppressed_until)
      END
    ELSE d.suppressed_until
  END,
  alarm_version = GREATEST(d.alarm_version, EXCLUDED.alarm_version),
  began_at = LEAST(d.began_at, EXCLUDED.began_at),
  acknowledged_at = COALESCE(d.acknowledged_at, EXCLUDED.acknowledged_at),
  acknowledged_by = COALESCE(d.acknowledged_by, EXCLUDED.acknowledged_by),
  cleared_at = COALESCE(d.cleared_at, EXCLUDED.cleared_at),
  next_due_at = CASE
    WHEN EXCLUDED.alarm_version > d.alarm_version
      OR (d.acknowledged_at IS NULL AND EXCLUDED.acknowledged_at IS NOT NULL)
      OR (d.cleared_at IS NULL AND EXCLUDED.cleared_at IS NOT NULL)
    THEN EXCLUDED.next_due_at
    ELSE d.next_due_at
  END,
  updated_at = EXCLUDED.updated_at`,
		event.TenantID, event.AlarmID, event.SiteID, clip(alarmType, 256), clip(title, 512), clip(event.Attributes["summary"], 2048),
		string(event.CurrentSeverity), version, began, acknowledgedAt, acknowledgedBy, clearedAt, deviceID, suppressedUntil, recordedAt.UTC(), clearSuppression)
	return err
}

// AlarmDeliveryDispatcher decides and sends Alarm messages for every Tenant.
type AlarmDeliveryDispatcher struct {
	Scheduler  *Scheduler
	Reloader   *RoutingReloader
	Routing    AlarmRoutingTable
	Recipients RecipientDirectory
	Sender     DeliverySender
	Mode       DeliveryMode
	// ConsoleURL is the web console's origin, for the link to the Alarm in each message.
	ConsoleURL string
	Logger     *slog.Logger
}

func (dispatcher *AlarmDeliveryDispatcher) currentConfig() (AlarmRoutingTable, RecipientDirectory) {
	if dispatcher.Reloader != nil {
		if snap := dispatcher.Reloader.Snapshot(); snap != nil {
			var routing AlarmRoutingTable
			var recipients RecipientDirectory
			if snap.Routing != nil {
				routing = *snap.Routing
			}
			if snap.Recipients != nil {
				recipients = *snap.Recipients
			}
			return routing, recipients
		}
	}
	return dispatcher.Routing, dispatcher.Recipients
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
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var cancel context.CancelFunc
	ctx, cancel = withDefaultTimeout(ctx, defaultDeliveryRunTimeout)
	defer cancel()
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
       acknowledged_at, acknowledged_by, cleared_at, COALESCE(device_id, ''), suppressed_until
FROM notification_runtime.alarm_delivery
WHERE next_due_at <= $1 AND (lease_until IS NULL OR lease_until <= $1)
ORDER BY next_due_at LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(
		&alarm.TenantID, &alarm.AlarmID, &alarm.SiteID, &alarm.AlarmType, &alarm.Title, &alarm.Summary, &severity, &alarm.Version, &alarm.BeganAt,
		&alarm.AcknowledgedAt, &acknowledgedBy, &alarm.ClearedAt, &alarm.DeviceID, &alarm.SuppressedUntil)
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
	routingTable, _ := dispatcher.currentConfig()
	routing, ok := routingTable.Site(alarm.SiteID)
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
	if len(routing.RootCauseRules) > 0 {
		rows, err := tx.Query(ctx, `SELECT DISTINCT alarm_type, COALESCE(device_id, '') FROM notification_runtime.alarm_delivery
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND cleared_at IS NULL AND alarm_id <> $3::uuid`,
			alarm.TenantID, alarm.SiteID, alarm.AlarmID)
		if err != nil {
			return facts, err
		}
		facts.ActiveAlarms, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ActiveAlarmFact, error) {
			var active ActiveAlarmFact
			err := row.Scan(&active.AlarmType, &active.DeviceID)
			return active, err
		})
		if err != nil {
			return facts, err
		}
		seenTypes := map[string]bool{}
		for _, active := range facts.ActiveAlarms {
			if !seenTypes[active.AlarmType] {
				seenTypes[active.AlarmType] = true
				facts.ActiveAlarmTypes = append(facts.ActiveAlarmTypes, active.AlarmType)
			}
		}
		if routing.RootCauseAlarmType != "" && alarm.AlarmType != routing.RootCauseAlarmType {
			for _, activeType := range facts.ActiveAlarmTypes {
				if activeType == routing.RootCauseAlarmType {
					facts.RootCauseActive = true
					break
				}
			}
		}
	} else if routing.RootCauseAlarmType != "" && alarm.AlarmType != routing.RootCauseAlarmType {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_runtime.alarm_delivery
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND alarm_type = $3 AND cleared_at IS NULL AND alarm_id <> $4::uuid)`,
			alarm.TenantID, alarm.SiteID, routing.RootCauseAlarmType, alarm.AlarmID).Scan(&facts.RootCauseActive); err != nil {
			return facts, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT kind, step, channel, recipient, created_at, COALESCE(reason, ''), state NOT IN ('SUPPRESSED','FAILED')
FROM notification_runtime.alarm_delivery_attempt WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid ORDER BY created_at, dedup_key`, alarm.TenantID, alarm.AlarmID)
	if err != nil {
		return facts, err
	}
	facts.Queued, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (QueuedMessage, error) {
		var message QueuedMessage
		var kind, channel string
		err := row.Scan(&kind, &message.Step, &channel, &message.Recipient, &message.QueuedAt, &message.Reason, &message.Reached)
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
	if err != nil {
		return facts, err
	}
	if route, ok := routing.Route(alarm.AlarmType, alarm.Severity); ok {
		if cooldown := effectiveCooldownMinutes(routing, route); cooldown > 0 && alarm.Severity != alarmmodel.SeverityCritical {
			since := now.Add(-time.Duration(cooldown) * time.Minute).UTC()
			rows, err := tx.Query(ctx, `
SELECT a.recipient, MAX(a.created_at)
FROM notification_runtime.alarm_delivery_attempt a
JOIN notification_runtime.alarm_delivery d
  ON d.tenant_id = a.tenant_id AND d.alarm_id = a.alarm_id
WHERE a.tenant_id = $1::uuid
  AND a.site_id = $2::uuid
  AND d.alarm_type = $3
  AND a.alarm_id <> $4::uuid
  AND a.kind = 'FIRE'
  AND a.state IN ('PENDING','SENDING','SENT','DRY_RUN')
  AND a.created_at >= $5
GROUP BY a.recipient`,
				alarm.TenantID, alarm.SiteID, alarm.AlarmType, alarm.AlarmID, since)
			if err != nil {
				return facts, err
			}
			facts.LastSameTypeNotifiedAt = map[string]time.Time{}
			_, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
				var recipient string
				var lastAt time.Time
				err := row.Scan(&recipient, &lastAt)
				facts.LastSameTypeNotifiedAt[recipient] = lastAt.UTC()
				return struct{}{}, err
			})
			if err != nil {
				return facts, err
			}
		}
	}
	return facts, nil
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

type claimedBatch struct {
	claims  []claimedAttempt
	alarms  []DeliveryAlarm
	routing SiteRouting
	to      string
	msgErr  error
}

func (dispatcher *AlarmDeliveryDispatcher) sendOne(ctx context.Context, now time.Time) (bool, error) {
	batch, worked, err := dispatcher.claimBatch(ctx, now)
	if err != nil || batch == nil {
		return worked, err
	}
	primary := batch.claims[0]
	settle := func(state, reason string, retry bool) error {
		settleCtx, cancelSettle := settleContext(ctx, defaultDeliverySettleTimeout)
		defer cancelSettle()
		return dispatcher.settleBatch(settleCtx, batch.claims, now, state, reason, retry)
	}
	if batch.msgErr != nil {
		dispatcher.log().Error("alarm_delivery_message_failed", "error_code", "ALARM_DELIVERY_MESSAGE_FAILED", "alarm_id", primary.alarmID, "key", primary.dedupKey, "reason", batch.msgErr.Error())
		return true, settle("FAILED", "NO_CONTACT", false)
	}
	var subject, text, key string
	if len(batch.claims) == 1 {
		subject, text = RenderDeliveryMessage(batch.alarms[0], batch.routing, primary.kind, primary.step, dispatcher.ConsoleURL)
		key = primary.dedupKey
	} else {
		subject, text = RenderGroupedDeliveryMessage(batch.alarms, batch.routing, primary.step, dispatcher.ConsoleURL)
		key = primary.dedupKey + "+group:" + itoa(len(batch.claims))
	}
	message := OutboundMessage{
		Channel: primary.channel,
		To:      batch.to,
		Subject: subject,
		Text:    text,
		DryRun:  dispatcher.Mode != DeliveryLive,
		Key:     key,
	}
	sendCtx, cancelSend := withBoundedTimeout(ctx, defaultDeliverySendTimeout)
	err = dispatcher.Sender.SendAlarmMessage(sendCtx, message)
	cancelSend()
	switch {
	case err == nil && message.DryRun:
		dispatcher.log().Info("alarm_delivery_dry_run", "alarm_id", primary.alarmID, "kind", string(primary.kind), "step", primary.step, "channel", string(primary.channel), "recipient", primary.recipient, "batch_size", len(batch.claims))
		return true, settle("DRY_RUN", "", false)
	case err == nil:
		dispatcher.log().Info("alarm_delivery_sent", "alarm_id", primary.alarmID, "kind", string(primary.kind), "step", primary.step, "channel", string(primary.channel), "recipient", primary.recipient, "batch_size", len(batch.claims))
		return true, settle("SENT", "", false)
	case errors.Is(err, ErrDeliveryRejected):
		dispatcher.log().Error("alarm_delivery_rejected", "error_code", "ALARM_DELIVERY_REJECTED", "alarm_id", primary.alarmID, "key", message.Key, "batch_size", len(batch.claims))
		return true, settle("FAILED", "REJECTED", false)
	default:
		dispatcher.log().Warn("alarm_delivery_send_failed", "error_code", "ALARM_DELIVERY_SEND_FAILED", "alarm_id", primary.alarmID, "key", message.Key, "attempt", primary.attempts, "batch_size", len(batch.claims))
		return true, settle("PENDING", "", true)
	}
}

func (dispatcher *AlarmDeliveryDispatcher) claimBatch(ctx context.Context, now time.Time) (*claimedBatch, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	routingTable, recipients := dispatcher.currentConfig()
	tx, err := dispatcher.Scheduler.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	primary := claimedAttempt{}
	var kind, channel, state string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, alarm_id::text, dedup_key, kind, step, channel, recipient, state, attempts
FROM notification_runtime.alarm_delivery_attempt
WHERE (state = 'PENDING' AND next_attempt_at <= $1)
   OR (state = 'SENDING' AND (lease_until IS NULL OR lease_until <= $1))
ORDER BY CASE WHEN state = 'SENDING' THEN COALESCE(lease_until, next_attempt_at) ELSE next_attempt_at END,
         next_attempt_at
LIMIT 1 FOR UPDATE SKIP LOCKED`, now.UTC()).Scan(&primary.tenantID, &primary.alarmID, &primary.dedupKey, &kind, &primary.step, &channel, &primary.recipient, &state, &primary.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim Alarm message: %w", err)
	}
	primary.kind, primary.channel = DeliveryKind(kind), DeliveryChannel(channel)
	if state == "SENDING" && primary.attempts >= deliveryAttempts {
		if _, err := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'FAILED', reason = 'GAVE_UP', next_attempt_at = $3, lease_until = NULL, updated_at = $3
WHERE tenant_id = $1::uuid AND dedup_key = $2`,
			primary.tenantID, primary.dedupKey, now.UTC()); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		dispatcher.log().Error("alarm_delivery_gave_up", "error_code", "ALARM_DELIVERY_GAVE_UP", "alarm_id", primary.alarmID, "key", primary.dedupKey, "reason", "LEASE_EXPIRED", "attempts", primary.attempts)
		return nil, true, nil
	}
	primaryAlarm, err := readDeliveryAlarmTx(ctx, tx, primary.tenantID, primary.alarmID)
	if err != nil {
		return nil, false, err
	}
	primary.attempts++
	primary.leaseUntil = now.Add(deliveryLease).UTC()
	if _, err := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'SENDING', attempts = $3, next_attempt_at = $4, lease_until = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND dedup_key = $2`,
		primary.tenantID, primary.dedupKey, primary.attempts, primary.leaseUntil, now.UTC()); err != nil {
		return nil, false, err
	}

	batch := &claimedBatch{
		claims: []claimedAttempt{primary},
		alarms: []DeliveryAlarm{primaryAlarm},
	}
	routing, ok := routingTable.Site(primaryAlarm.SiteID)
	if !ok {
		batch.msgErr = errors.New("the Site has no routing any more")
	} else {
		batch.routing = routing
		to, contactErr := recipientContact(recipients, primary.channel, primary.recipient)
		if contactErr != nil {
			batch.msgErr = contactErr
		} else {
			batch.to = to
		}
	}

	if batch.msgErr == nil && state == "PENDING" && primary.kind == DeliveryFire && primaryAlarm.Severity != alarmmodel.SeverityCritical {
		route, routeIndex, routeOK := routing.routeWithIndex(primaryAlarm.AlarmType, primaryAlarm.Severity)
		if routeOK && route.GroupWindowSeconds > 0 {
			sibClaims, sibAlarms, sibErr := claimSiblingAttemptsTx(ctx, tx, primary, primaryAlarm, routing, route, routeIndex, now)
			if sibErr != nil {
				return nil, false, sibErr
			}
			batch.claims = append(batch.claims, sibClaims...)
			batch.alarms = append(batch.alarms, sibAlarms...)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	if state == "SENDING" {
		dispatcher.log().Warn("alarm_delivery_lease_reclaimed", "alarm_id", primary.alarmID, "key", primary.dedupKey, "attempt", primary.attempts)
	}
	return batch, true, nil
}

func readDeliveryAlarmTx(ctx context.Context, tx pgx.Tx, tenantID, alarmID string) (DeliveryAlarm, error) {
	var alarm DeliveryAlarm
	var severity string
	var acknowledgedBy *string
	if err := tx.QueryRow(ctx, `SELECT tenant_id::text, alarm_id::text, site_id::text, alarm_type, title, summary, severity, alarm_version, began_at,
       acknowledged_at, acknowledged_by, cleared_at, COALESCE(device_id, ''), suppressed_until
FROM notification_runtime.alarm_delivery WHERE tenant_id = $1::uuid AND alarm_id = $2::uuid`,
		tenantID, alarmID).Scan(&alarm.TenantID, &alarm.AlarmID, &alarm.SiteID, &alarm.AlarmType, &alarm.Title, &alarm.Summary, &severity, &alarm.Version,
		&alarm.BeganAt, &alarm.AcknowledgedAt, &acknowledgedBy, &alarm.ClearedAt, &alarm.DeviceID, &alarm.SuppressedUntil); err != nil {
		return DeliveryAlarm{}, fmt.Errorf("read the Alarm: %w", err)
	}
	alarm.Severity = alarmmodel.Severity(severity)
	if acknowledgedBy != nil {
		alarm.AcknowledgedBy = *acknowledgedBy
	}
	return alarm, nil
}

func claimSiblingAttemptsTx(
	ctx context.Context,
	tx pgx.Tx,
	primary claimedAttempt,
	primaryAlarm DeliveryAlarm,
	routing SiteRouting,
	primaryRoute AlarmRoute,
	primaryRouteIndex int,
	now time.Time,
) ([]claimedAttempt, []DeliveryAlarm, error) {
	maxSiblings := effectiveGroupMaxItems(primaryRoute) - 1
	if maxSiblings <= 0 {
		return nil, nil, nil
	}
	windowEnd := now.Add(time.Duration(primaryRoute.GroupWindowSeconds) * time.Second).UTC()
	var severityFilter, alarmTypeFilter string
	switch effectiveGroupBy(primaryRoute) {
	case "severity":
		severityFilter = string(primaryAlarm.Severity)
	case "alarmType":
		alarmTypeFilter = primaryAlarm.AlarmType
	}
	rows, err := tx.Query(ctx, `
SELECT a.dedup_key, a.alarm_id::text, a.attempts,
       d.tenant_id::text, d.site_id::text, d.alarm_type, d.title, d.summary, d.severity, d.alarm_version, d.began_at,
       d.acknowledged_at, d.acknowledged_by, d.cleared_at, COALESCE(d.device_id, ''), d.suppressed_until
FROM notification_runtime.alarm_delivery_attempt a
JOIN notification_runtime.alarm_delivery d
  ON d.tenant_id = a.tenant_id AND d.alarm_id = a.alarm_id
WHERE a.tenant_id = $1::uuid
  AND a.site_id = $2::uuid
  AND a.channel = $3
  AND a.recipient = $4
  AND a.kind = 'FIRE'
  AND a.step = $5
  AND a.state = 'PENDING'
  AND a.attempts < $6
  AND a.next_attempt_at <= $7
  AND a.dedup_key <> $8
  AND d.severity <> 'CRITICAL'
  AND ($9 = '' OR d.severity = $9)
  AND ($10 = '' OR d.alarm_type = $10)
ORDER BY a.next_attempt_at, a.created_at, a.dedup_key
FOR UPDATE OF a SKIP LOCKED`,
		primary.tenantID, primaryAlarm.SiteID, string(primary.channel), primary.recipient, primary.step,
		deliveryAttempts, windowEnd, primary.dedupKey, severityFilter, alarmTypeFilter)
	if err != nil {
		return nil, nil, fmt.Errorf("claim grouped sibling messages: %w", err)
	}
	var sibClaims []claimedAttempt
	var sibAlarms []DeliveryAlarm
	for rows.Next() {
		var sib claimedAttempt
		var sibAlarm DeliveryAlarm
		var severityStr string
		var ackBy *string
		if scanErr := rows.Scan(
			&sib.dedupKey, &sib.alarmID, &sib.attempts,
			&sibAlarm.TenantID, &sibAlarm.SiteID, &sibAlarm.AlarmType, &sibAlarm.Title, &sibAlarm.Summary, &severityStr, &sibAlarm.Version, &sibAlarm.BeganAt,
			&sibAlarm.AcknowledgedAt, &ackBy, &sibAlarm.ClearedAt, &sibAlarm.DeviceID, &sibAlarm.SuppressedUntil,
		); scanErr != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan grouped sibling message: %w", scanErr)
		}
		sibAlarm.AlarmID = sib.alarmID
		sibAlarm.Severity = alarmmodel.Severity(severityStr)
		if ackBy != nil {
			sibAlarm.AcknowledgedBy = *ackBy
		}
		if sibAlarm.Severity == alarmmodel.SeverityCritical {
			continue
		}
		sibRoute, sibRouteIndex, sibOK := routing.routeWithIndex(sibAlarm.AlarmType, sibAlarm.Severity)
		if !sibOK || sibRouteIndex != primaryRouteIndex || sibRoute.GroupWindowSeconds <= 0 {
			continue
		}
		sib.tenantID = primary.tenantID
		sib.kind = primary.kind
		sib.step = primary.step
		sib.channel = primary.channel
		sib.recipient = primary.recipient
		sib.attempts++
		sib.leaseUntil = primary.leaseUntil
		sibClaims = append(sibClaims, sib)
		sibAlarms = append(sibAlarms, sibAlarm)
		if len(sibClaims) >= maxSiblings {
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("iterate grouped sibling messages: %w", err)
	}
	rows.Close()

	if len(sibClaims) > 0 {
		dedupKeys := make([]string, len(sibClaims))
		for i, sib := range sibClaims {
			dedupKeys[i] = sib.dedupKey
		}
		if _, err := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = 'SENDING', attempts = attempts + 1, next_attempt_at = $3, lease_until = $3, updated_at = $4
WHERE tenant_id = $1::uuid AND dedup_key = ANY($2::text[]) AND state = 'PENDING'`,
			primary.tenantID, dedupKeys, primary.leaseUntil, now.UTC()); err != nil {
			return nil, nil, fmt.Errorf("mark grouped sibling messages SENDING: %w", err)
		}
	}
	return sibClaims, sibAlarms, nil
}

func recipientContact(recipients RecipientDirectory, channel DeliveryChannel, recipientID string) (string, error) {
	if channel == ChannelWeComBot {
		return "", nil
	}
	entry := recipients.Recipients[recipientID]
	var to string
	switch channel {
	case ChannelSMTP:
		to = entry.Email
	case ChannelWeComApp:
		to = entry.WeComUserID
	}
	if strings.TrimSpace(to) == "" {
		return "", fmt.Errorf("recipient %s has no contact for %s", recipientID, channel)
	}
	return to, nil
}

// settleBatch records how a single or grouped send ended; a retry waits attempts² minutes, until the last.
func (dispatcher *AlarmDeliveryDispatcher) settleBatch(ctx context.Context, claims []claimedAttempt, now time.Time, state, reason string, retry bool) error {
	if len(claims) == 0 {
		return nil
	}
	tx, err := dispatcher.Scheduler.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, claim := range claims {
		itemState, itemReason, next := state, reason, now
		if retry {
			if claim.attempts >= deliveryAttempts {
				itemState, itemReason = "FAILED", "GAVE_UP"
				dispatcher.log().Error("alarm_delivery_gave_up", "error_code", "ALARM_DELIVERY_GAVE_UP", "alarm_id", claim.alarmID, "key", claim.dedupKey)
			} else {
				next = now.Add(time.Duration(claim.attempts*claim.attempts) * time.Minute)
			}
		}
		var reasonValue any
		if itemReason != "" {
			reasonValue = itemReason
		}
		tag, execErr := tx.Exec(ctx, `UPDATE notification_runtime.alarm_delivery_attempt
SET state = $3, reason = $4, next_attempt_at = $5, lease_until = NULL, updated_at = $6
WHERE tenant_id = $1::uuid AND dedup_key = $2 AND state = 'SENDING' AND lease_until = $7`,
			claim.tenantID, claim.dedupKey, itemState, reasonValue, next.UTC(), now.UTC(), claim.leaseUntil)
		if execErr != nil {
			return execErr
		}
		if tag.RowsAffected() == 0 {
			dispatcher.log().Warn("alarm_delivery_lease_lost", "alarm_id", claim.alarmID, "key", claim.dedupKey, "target_state", itemState)
		}
	}
	return tx.Commit(ctx)
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func withBoundedTimeout(ctx context.Context, maxDuration time.Duration) (context.Context, context.CancelFunc) {
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && time.Until(deadline) <= maxDuration {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, maxDuration)
}

func settleContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

var deliverySeverityLabels = map[alarmmodel.Severity]string{
	alarmmodel.SeverityCritical: "紧急", alarmmodel.SeverityMajor: "重要", alarmmodel.SeverityMinor: "次要", alarmmodel.SeverityWarning: "警告", alarmmodel.SeverityInfo: "提示",
}

// RenderGroupedDeliveryMessage renders a combined digest for a batch of grouped FIRE alarms in the Site's zone.
func RenderGroupedDeliveryMessage(alarms []DeliveryAlarm, routing SiteRouting, step int, consoleURL string) (string, string) {
	if len(alarms) == 0 {
		return "", ""
	}
	if len(alarms) == 1 {
		return RenderDeliveryMessage(alarms[0], routing, DeliveryFire, step, consoleURL)
	}
	location, err := time.LoadLocation(routing.Timezone)
	if err != nil {
		location = time.UTC
	}
	clock := func(at time.Time) string { return at.In(location).Format("2006-01-02 15:04") }

	sameSeverity := true
	firstSeverity := alarms[0].Severity
	for _, alarm := range alarms[1:] {
		if alarm.Severity != firstSeverity {
			sameSeverity = false
			break
		}
	}
	siteName := sanitizeOneLine(routing.SiteName)
	var subject string
	if sameSeverity {
		label := deliverySeverityLabels[firstSeverity]
		subject = fmt.Sprintf("[%s] %d 条%s告警汇总", siteName, len(alarms), label)
	} else {
		subject = fmt.Sprintf("[%s] %d 条告警汇总", siteName, len(alarms))
	}

	maxItems := defaultGroupMaxItems
	route, hasRoute := routing.Route(alarms[0].AlarmType, alarms[0].Severity)
	if hasRoute {
		if route.GroupWindowSeconds > 0 {
			maxItems = effectiveGroupMaxItems(route)
		} else if route.GroupMaxItems >= minGroupMaxItems && route.GroupMaxItems <= maxGroupMaxItems {
			maxItems = route.GroupMaxItems
		}
	}
	shown := min(len(alarms), maxItems)

	var body strings.Builder
	body.WriteString(subject + "\n")
	fmt.Fprintf(&body, "时区：%s\n", routing.Timezone)
	for index, alarm := range alarms[:shown] {
		title := clip(sanitizeOneLine(alarm.Title), 80)
		device := strings.TrimSpace(sanitizeOneLine(alarm.DeviceID))
		prefix := ""
		if !sameSeverity {
			if label := deliverySeverityLabels[alarm.Severity]; label != "" {
				prefix = "[" + label + "] "
			}
		}
		if device != "" {
			fmt.Fprintf(&body, "%d. %s%s（设备：%s，开始：%s）\n", index+1, prefix, title, clip(device, 64), clock(alarm.BeganAt))
		} else {
			fmt.Fprintf(&body, "%d. %s%s（开始：%s）\n", index+1, prefix, title, clock(alarm.BeganAt))
		}
	}
	if len(alarms) > shown {
		fmt.Fprintf(&body, "其余 %d 条告警请在控制台查看。\n", len(alarms)-shown)
	}
	if hasRoute && step+1 < len(route.Steps) {
		fmt.Fprintf(&body, "%d 分钟内无人确认，将通知下一级。\n", route.Steps[step+1].AfterMinutes-route.Steps[step].AfterMinutes)
	}
	if consoleURL = strings.TrimRight(strings.TrimSpace(consoleURL), "/"); consoleURL != "" {
		fmt.Fprintf(&body, "查看告警：%s/operations/alarms?site=%s&view=all\n", consoleURL, alarms[0].SiteID)
	}
	return subject, strings.TrimRight(body.String(), "\n")
}

func sanitizeOneLine(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(strings.TrimSpace(value))
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

var defaultDeliveryHTTPClient = newDefaultDeliveryHTTPClient()

func newDefaultDeliveryHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:       defaultDeliverySendTimeout,
		Transport:     transport,
		CheckRedirect: refuseDeliveryRedirect,
	}
}

func refuseDeliveryRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func deliveryHTTPClient(custom *http.Client) *http.Client {
	if custom == nil {
		return defaultDeliveryHTTPClient
	}
	client := *custom
	if client.Timeout <= 0 {
		client.Timeout = defaultDeliverySendTimeout
	}
	client.CheckRedirect = refuseDeliveryRedirect
	return &client
}

// HTTPDeliverySender posts messages to the alert adapter's POST /send with a bearer token read
// from a file at each send (rotating the token needs no restart).
type HTTPDeliverySender struct {
	URL       string
	TokenFile string
	Client    *http.Client
}

func (sender HTTPDeliverySender) SendAlarmMessage(ctx context.Context, message OutboundMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cancel context.CancelFunc
	ctx, cancel = withDefaultTimeout(ctx, defaultDeliverySendTimeout)
	defer cancel()
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
	response, err := deliveryHTTPClient(sender.Client).Do(request)
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
