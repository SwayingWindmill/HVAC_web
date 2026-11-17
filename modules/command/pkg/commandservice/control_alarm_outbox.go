package commandservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type ControlAlarmStatus string

const (
	ControlAlarmStatusPending    ControlAlarmStatus = "PENDING"
	ControlAlarmStatusDelivered  ControlAlarmStatus = "DELIVERED"
	ControlAlarmStatusDeadLetter ControlAlarmStatus = "DEAD_LETTER"

	defaultControlAlarmPollInterval    = 2 * time.Second
	defaultControlAlarmLeaseDuration   = 15 * time.Second
	defaultControlAlarmDeliveryTimeout = 5 * time.Second
	defaultControlAlarmBatchSize       = 32
	defaultControlAlarmMaxAttempts     = 10
	defaultControlAlarmBaseBackoff     = time.Second
	defaultControlAlarmMaxBackoff      = 5 * time.Minute
	maxControlAlarmBatchesPerTenant    = 16
	maxControlAlarmErrorCodeLength     = 128
)

var (
	ErrControlAlarmLeaseLost = errors.New("control alarm outbox lease was lost or already finalized")
	ErrCommandStoreClosed    = errors.New("command store is closed")
)

// ControlAlarmDeliveryError carries a sanitized, bounded error code for outbox persistence and logs.
type ControlAlarmDeliveryError struct {
	Code string
}

func (e *ControlAlarmDeliveryError) Error() string {
	if e == nil {
		return "CONTROL_ALARM_DELIVERY_FAILED"
	}
	return sanitizeControlAlarmErrorCode(e.Code)
}

func ControlAlarmErrorCode(err error) string {
	var deliveryErr *ControlAlarmDeliveryError
	if errors.As(err, &deliveryErr) {
		return sanitizeControlAlarmErrorCode(deliveryErr.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "CONTROL_ALARM_DELIVERY_TIMEOUT"
	}
	if errors.Is(err, context.Canceled) {
		return "CONTROL_ALARM_DELIVERY_CANCELED"
	}
	return "CONTROL_ALARM_DELIVERY_FAILED"
}

func sanitizeControlAlarmErrorCode(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "CONTROL_ALARM_DELIVERY_FAILED"
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed) && b.Len() < maxControlAlarmErrorCodeLength; i++ {
		ch := trimmed[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' || ch == ':' {
			b.WriteByte(ch)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "CONTROL_ALARM_DELIVERY_FAILED"
	}
	return b.String()
}

type ControlAlarmOutboxRecord struct {
	OutboxID      string
	TenantID      string
	CommandID     string
	SiteID        string
	DeviceID      string
	EventKind     string
	ReasonCode    string
	Event         ControlFailureEvent
	Status        ControlAlarmStatus
	Attempts      int
	AvailableAt   time.Time
	LeaseOwner    string
	LeaseUntil    *time.Time
	DeliveredAt   *time.Time
	LastErrorCode string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type controlAlarmOutboxPayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	TenantID      string `json:"tenantId"`
	SiteID        string `json:"siteId"`
	DeviceID      string `json:"deviceId,omitempty"`
	PointID       string `json:"pointId,omitempty"`
	CommandID     string `json:"commandId"`
	EventKind     string `json:"eventKind"`
	Capability    string `json:"capability,omitempty"`
	ReasonCode    string `json:"reasonCode,omitempty"`
	Detail        string `json:"detail,omitempty"`
	OccurredAt    string `json:"occurredAt,omitempty"`
	ActorType     string `json:"actorType,omitempty"`
	ActorID       string `json:"actorId,omitempty"`
	CorrelationID string `json:"correlationId,omitempty"`
}

func buildControlAlarmOutboxPayload(event ControlFailureEvent, fallbackNow time.Time) ([]byte, string, string, time.Time, error) {
	eventKind := strings.ToUpper(strings.TrimSpace(event.Kind))
	if eventKind == "" ||
		strings.TrimSpace(event.TenantID) == "" ||
		strings.TrimSpace(event.SiteID) == "" ||
		strings.TrimSpace(event.DeviceID) == "" ||
		strings.TrimSpace(event.CommandID) == "" {
		return nil, "", "", time.Time{}, ErrInvalidRequest
	}
	occurredAt := event.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = fallbackNow.UTC()
	}
	reasonCode := strings.TrimSpace(event.ReasonCode)
	payload := controlAlarmOutboxPayload{
		SchemaVersion: 1,
		TenantID:      strings.TrimSpace(event.TenantID),
		SiteID:        strings.TrimSpace(event.SiteID),
		DeviceID:      strings.TrimSpace(event.DeviceID),
		PointID:       strings.TrimSpace(event.PointID),
		CommandID:     strings.TrimSpace(event.CommandID),
		EventKind:     eventKind,
		Capability:    strings.TrimSpace(string(event.Capability)),
		ReasonCode:    reasonCode,
		Detail:        strings.TrimSpace(event.Detail),
		OccurredAt:    occurredAt.Format(time.RFC3339Nano),
		ActorType:     strings.TrimSpace(event.ActorType),
		ActorID:       strings.TrimSpace(event.ActorID),
		CorrelationID: strings.TrimSpace(event.CommandID),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", time.Time{}, fmt.Errorf("marshal control alarm outbox payload: %w", err)
	}
	return raw, eventKind, reasonCode, occurredAt, nil
}

func decodeControlAlarmOutboxEvent(
	tenantID, siteID, deviceID, commandID, eventKind, reasonCode string,
	rawPayload []byte,
	fallbackTime time.Time,
) ControlFailureEvent {
	event := ControlFailureEvent{
		Kind:       eventKind,
		TenantID:   tenantID,
		SiteID:     siteID,
		DeviceID:   deviceID,
		CommandID:  commandID,
		ReasonCode: reasonCode,
		OccurredAt: fallbackTime.UTC(),
	}
	var payload controlAlarmOutboxPayload
	if len(rawPayload) > 0 && json.Unmarshal(rawPayload, &payload) == nil {
		if strings.TrimSpace(payload.EventKind) != "" {
			event.Kind = payload.EventKind
		}
		if strings.TrimSpace(payload.PointID) != "" {
			event.PointID = payload.PointID
		}
		if strings.TrimSpace(payload.Capability) != "" {
			event.Capability = commandmodel.Capability(payload.Capability)
		}
		if strings.TrimSpace(payload.ReasonCode) != "" {
			event.ReasonCode = payload.ReasonCode
		}
		event.Detail = payload.Detail
		event.ActorType = payload.ActorType
		event.ActorID = payload.ActorID
		if strings.TrimSpace(payload.OccurredAt) != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, payload.OccurredAt); err == nil {
				event.OccurredAt = parsed.UTC()
			}
		}
	}
	return event
}

// SetControlAlarmOutboxWakeup registers a non-blocking callback invoked after durable alarm outbox inserts commit.
func (store *PostgresStore) SetControlAlarmOutboxWakeup(wakeup func()) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.alarmOutboxWakeup = wakeup
}

func (store *PostgresStore) notifyControlAlarmWakeup() {
	if store == nil {
		return
	}
	store.mu.RLock()
	wakeup := store.alarmOutboxWakeup
	store.mu.RUnlock()
	if wakeup != nil {
		wakeup()
	}
}

func (store *PostgresStore) insertControlAlarmOutbox(ctx context.Context, tx postgresQueryExecutor, event ControlFailureEvent, now time.Time) error {
	if store == nil || tx == nil {
		return ErrInvalidRequest
	}
	nowUTC := now.UTC()
	rawPayload, eventKind, reasonCode, _, err := buildControlAlarmOutboxPayload(event, nowUTC)
	if err != nil {
		return err
	}
	outboxID, err := store.newID(nowUTC)
	if err != nil {
		return fmt.Errorf("allocate control alarm outbox identifier: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO command_runtime.control_alarm_outbox (
  outbox_id, tenant_id, command_id, site_id, device_id,
  event_kind, reason_code, payload, status, attempts,
  available_at, lease_owner, lease_until, delivered_at,
  last_error_code, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
  $6, $7, $8::jsonb, 'PENDING', 0,
  $9, NULL, NULL, NULL,
  NULL, $9, $9
)
ON CONFLICT (command_id, event_kind) DO NOTHING
`, outboxID, event.TenantID, event.CommandID, event.SiteID, event.DeviceID, eventKind, reasonCode, rawPayload, nowUTC); err != nil {
		return fmt.Errorf("insert control alarm outbox: %w", err)
	}
	return nil
}

// EnqueueControlAlarm durably persists an operational control alarm event into control_alarm_outbox for its tenant.
func (store *PostgresStore) EnqueueControlAlarm(ctx context.Context, event ControlFailureEvent) error {
	if store == nil || store.pool == nil {
		return ErrCommandStoreClosed
	}
	if !commandmodel.IsUUIDv7(event.TenantID) {
		return ErrInvalidRequest
	}
	now := store.now().UTC()
	if err := store.runRetryableTenantTx(ctx, event.TenantID, pgx.Serializable, func(tx pgx.Tx) error {
		return store.insertControlAlarmOutbox(ctx, tx, event, now)
	}); err != nil {
		return err
	}
	store.notifyControlAlarmWakeup()
	return nil
}

// ListTenantsWithPendingControlAlarms returns tenant identifiers that currently have claimable control alarm outbox items.
func (store *PostgresStore) ListTenantsWithPendingControlAlarms(ctx context.Context, readyAt time.Time) ([]string, error) {
	if store == nil || store.pool == nil {
		return nil, ErrCommandStoreClosed
	}
	if readyAt.IsZero() {
		readyAt = store.now().UTC()
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin pending control alarm tenant scan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s3_command_runtime`); err != nil {
		return nil, fmt.Errorf("activate command runtime database identity: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id::text FROM command_runtime.tenants_with_pending_control_alarms($1)`, readyAt.UTC())
	if err != nil {
		return nil, fmt.Errorf("list pending control alarm tenants: %w", err)
	}

	var tenants []string
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending control alarm tenant: %w", err)
		}
		tenants = append(tenants, tenantID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending control alarm tenants: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit pending control alarm tenant scan: %w", err)
	}
	return tenants, nil
}

// ClaimControlAlarmOutbox leases ready control alarm outbox records for a single tenant under RLS.
func (store *PostgresStore) ClaimControlAlarmOutbox(
	ctx context.Context,
	tenantID string,
	leaseOwner string,
	leaseFor time.Duration,
	limit int,
) ([]ControlAlarmOutboxRecord, error) {
	if store == nil || store.pool == nil {
		return nil, ErrCommandStoreClosed
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if !commandmodel.IsUUIDv7(tenantID) || leaseOwner == "" || leaseFor <= 0 || limit <= 0 {
		return nil, ErrInvalidRequest
	}
	var records []ControlAlarmOutboxRecord
	err := store.runRetryableTenantTx(ctx, tenantID, pgx.ReadCommitted, func(tx pgx.Tx) error {
		now := store.now().UTC()
		leaseUntil := now.Add(leaseFor)
		rows, err := tx.Query(ctx, `
WITH due AS (
  SELECT outbox_id
  FROM command_runtime.control_alarm_outbox
  WHERE tenant_id = $1::uuid
    AND status = 'PENDING'
    AND available_at <= $2
    AND (lease_until IS NULL OR lease_until <= $2)
  ORDER BY available_at ASC, created_at ASC, outbox_id ASC
  LIMIT $3
  FOR UPDATE SKIP LOCKED
)
UPDATE command_runtime.control_alarm_outbox AS o
SET lease_owner = $4,
    lease_until = $5,
    attempts = o.attempts + 1,
    updated_at = $2
FROM due
WHERE o.outbox_id = due.outbox_id
RETURNING
  o.outbox_id::text,
  o.tenant_id::text,
  o.command_id::text,
  o.site_id::text,
  o.device_id::text,
  o.event_kind,
  o.reason_code,
  o.payload,
  o.status,
  o.attempts,
  o.available_at,
  o.lease_owner,
  o.lease_until,
  o.delivered_at,
  COALESCE(o.last_error_code, ''),
  o.created_at,
  o.updated_at
`, tenantID, now, limit, leaseOwner, leaseUntil)
		if err != nil {
			return fmt.Errorf("claim control alarm outbox: %w", err)
		}
		defer rows.Close()

		records = nil
		for rows.Next() {
			var (
				rec        ControlAlarmOutboxRecord
				rawPayload []byte
				statusText string
				leaseOwner sql.NullString
				leaseAt    sql.NullTime
				delivered  sql.NullTime
			)
			if err := rows.Scan(
				&rec.OutboxID,
				&rec.TenantID,
				&rec.CommandID,
				&rec.SiteID,
				&rec.DeviceID,
				&rec.EventKind,
				&rec.ReasonCode,
				&rawPayload,
				&statusText,
				&rec.Attempts,
				&rec.AvailableAt,
				&leaseOwner,
				&leaseAt,
				&delivered,
				&rec.LastErrorCode,
				&rec.CreatedAt,
				&rec.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan claimed control alarm outbox: %w", err)
			}
			rec.Status = ControlAlarmStatus(statusText)
			if leaseOwner.Valid {
				rec.LeaseOwner = leaseOwner.String
			}
			if leaseAt.Valid {
				t := leaseAt.Time.UTC()
				rec.LeaseUntil = &t
			}
			if delivered.Valid {
				t := delivered.Time.UTC()
				rec.DeliveredAt = &t
			}
			rec.Event = decodeControlAlarmOutboxEvent(
				rec.TenantID, rec.SiteID, rec.DeviceID, rec.CommandID,
				rec.EventKind, rec.ReasonCode, rawPayload, rec.CreatedAt,
			)
			records = append(records, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// MarkControlAlarmDelivered marks an active leased control alarm outbox entry as delivered.
func (store *PostgresStore) MarkControlAlarmDelivered(ctx context.Context, tenantID, outboxID, leaseOwner string) error {
	if store == nil || store.pool == nil {
		return ErrCommandStoreClosed
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if !commandmodel.IsUUIDv7(tenantID) || strings.TrimSpace(outboxID) == "" || leaseOwner == "" {
		return ErrInvalidRequest
	}
	return store.runRetryableTenantTx(ctx, tenantID, pgx.ReadCommitted, func(tx pgx.Tx) error {
		now := store.now().UTC()
		tag, err := tx.Exec(ctx, `
UPDATE command_runtime.control_alarm_outbox
SET status = 'DELIVERED',
    delivered_at = $4,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = '',
    updated_at = $4
WHERE tenant_id = $1::uuid
  AND outbox_id = $2::uuid
  AND status = 'PENDING'
  AND lease_owner = $3
`, tenantID, outboxID, leaseOwner, now)
		if err != nil {
			return fmt.Errorf("mark control alarm delivered: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrControlAlarmLeaseLost
		}
		return nil
	})
}

// FailControlAlarmOutbox reschedules a failed delivery with backoff or transitions it to DEAD_LETTER past maxAttempts.
func (store *PostgresStore) FailControlAlarmOutbox(
	ctx context.Context,
	tenantID string,
	outboxID string,
	leaseOwner string,
	errorCode string,
	maxAttempts int,
	backoff time.Duration,
) error {
	if store == nil || store.pool == nil {
		return ErrCommandStoreClosed
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if !commandmodel.IsUUIDv7(tenantID) || strings.TrimSpace(outboxID) == "" || leaseOwner == "" || maxAttempts <= 0 {
		return ErrInvalidRequest
	}
	if backoff < 0 {
		backoff = 0
	}
	sanitizedCode := sanitizeControlAlarmErrorCode(errorCode)
	return store.runRetryableTenantTx(ctx, tenantID, pgx.ReadCommitted, func(tx pgx.Tx) error {
		now := store.now().UTC()
		nextAvailable := now.Add(backoff)
		tag, err := tx.Exec(ctx, `
UPDATE command_runtime.control_alarm_outbox
SET status = CASE WHEN attempts >= $4 THEN 'DEAD_LETTER' ELSE 'PENDING' END,
    available_at = CASE WHEN attempts >= $4 THEN available_at ELSE $5 END,
    lease_owner = NULL,
    lease_until = NULL,
    last_error_code = $6,
    updated_at = $7
WHERE tenant_id = $1::uuid
  AND outbox_id = $2::uuid
  AND status = 'PENDING'
  AND lease_owner = $3
`, tenantID, outboxID, leaseOwner, maxAttempts, nextAvailable, sanitizedCode, now)
		if err != nil {
			return fmt.Errorf("fail control alarm outbox: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrControlAlarmLeaseLost
		}
		return nil
	})
}

func (store *PostgresStore) runRetryableTenantTx(
	ctx context.Context,
	tenantID string,
	isoLevel pgx.TxIsoLevel,
	fn func(tx pgx.Tx) error,
) error {
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isoLevel})
		if err != nil {
			return fmt.Errorf("begin command tenant transaction: %w", err)
		}
		err = func() error {
			defer func() { _ = tx.Rollback(ctx) }()
			if err := activateTenant(ctx, tx, tenantID); err != nil {
				return err
			}
			if err := fn(tx); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return nil
		}()
		if err == nil {
			return nil
		}
		if isRetryablePostgresTransaction(err) && attempt < 2 {
			continue
		}
		return err
	}
	return nil
}

type ControlAlarmOutboxStore interface {
	ListTenantsWithPendingControlAlarms(ctx context.Context, readyAt time.Time) ([]string, error)
	ClaimControlAlarmOutbox(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration, limit int) ([]ControlAlarmOutboxRecord, error)
	MarkControlAlarmDelivered(ctx context.Context, tenantID, outboxID, leaseOwner string) error
	FailControlAlarmOutbox(ctx context.Context, tenantID, outboxID, leaseOwner, errorCode string, maxAttempts int, backoff time.Duration) error
}

type ControlAlarmSender func(context.Context, ControlFailureEvent) error

type ControlAlarmRelayConfig struct {
	Store           ControlAlarmOutboxStore
	Sender          ControlAlarmSender
	LeaseOwner      string
	PollInterval    time.Duration
	LeaseDuration   time.Duration
	DeliveryTimeout time.Duration
	BatchSize       int
	MaxAttempts     int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	Now             func() time.Time
	Logger          *slog.Logger
	Metrics         *observability.Registry
}

type ControlAlarmRelay struct {
	store           ControlAlarmOutboxStore
	sender          ControlAlarmSender
	leaseOwner      string
	pollInterval    time.Duration
	leaseDuration   time.Duration
	deliveryTimeout time.Duration
	batchSize       int
	maxAttempts     int
	baseBackoff     time.Duration
	maxBackoff      time.Duration
	now             func() time.Time
	logger          *slog.Logger
	metrics         *observability.Registry
	notifyCh        chan struct{}
}

func NewControlAlarmRelay(cfg ControlAlarmRelayConfig) (*ControlAlarmRelay, error) {
	if cfg.Store == nil {
		return nil, errors.New("control alarm relay store is required")
	}
	if cfg.Sender == nil {
		return nil, errors.New("control alarm relay sender is required")
	}
	leaseOwner := strings.TrimSpace(cfg.LeaseOwner)
	if leaseOwner == "" {
		leaseOwner = "command-alarm-relay"
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultControlAlarmPollInterval
	}
	leaseDuration := cfg.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = defaultControlAlarmLeaseDuration
	}
	deliveryTimeout := cfg.DeliveryTimeout
	if deliveryTimeout <= 0 {
		deliveryTimeout = defaultControlAlarmDeliveryTimeout
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultControlAlarmBatchSize
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultControlAlarmMaxAttempts
	}
	baseBackoff := cfg.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = defaultControlAlarmBaseBackoff
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultControlAlarmMaxBackoff
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &ControlAlarmRelay{
		store:           cfg.Store,
		sender:          cfg.Sender,
		leaseOwner:      leaseOwner,
		pollInterval:    pollInterval,
		leaseDuration:   leaseDuration,
		deliveryTimeout: deliveryTimeout,
		batchSize:       batchSize,
		maxAttempts:     maxAttempts,
		baseBackoff:     baseBackoff,
		maxBackoff:      maxBackoff,
		now:             nowFn,
		logger:          cfg.Logger,
		metrics:         cfg.Metrics,
		notifyCh:        make(chan struct{}, 1),
	}, nil
}

// Notify triggers an immediate non-blocking relay sweep.
func (r *ControlAlarmRelay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

func (r *ControlAlarmRelay) NextRetryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return r.baseBackoff
	}
	delay := r.baseBackoff
	for i := 1; i < attempts; i++ {
		if delay >= r.maxBackoff/2 {
			return r.maxBackoff
		}
		delay *= 2
	}
	if delay > r.maxBackoff {
		return r.maxBackoff
	}
	return delay
}

// Run polls and wakes on Notify until ctx is canceled.
func (r *ControlAlarmRelay) Run(ctx context.Context) {
	if r == nil {
		return
	}
	logger := r.logger
	if logger == nil {
		logger = slog.Default()
	}
	defer observability.Recover(logger, r.metrics, "command.control_alarm_relay")

	r.runSweep(ctx, logger)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runSweep(ctx, logger)
		case <-r.notifyCh:
			r.runSweep(ctx, logger)
		}
	}
}

func (r *ControlAlarmRelay) runSweep(ctx context.Context, logger *slog.Logger) {
	if ctx.Err() != nil {
		return
	}
	defer observability.Recover(logger, r.metrics, "command.control_alarm_relay.sweep")
	if _, err := r.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && logger != nil {
		logger.Warn("command_control_alarm_relay_sweep_failed", "error_code", ControlAlarmErrorCode(err))
	}
}

// ProcessOnce claims and delivers due control alarm outbox entries across active tenants.
func (r *ControlAlarmRelay) ProcessOnce(ctx context.Context) (int, error) {
	if r == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tenants, err := r.store.ListTenantsWithPendingControlAlarms(ctx, r.now().UTC())
	if err != nil {
		return 0, err
	}
	deliveredCount := 0
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			return deliveredCount, ctx.Err()
		}
		for batch := 0; batch < maxControlAlarmBatchesPerTenant; batch++ {
			records, err := r.store.ClaimControlAlarmOutbox(ctx, tenantID, r.leaseOwner, r.leaseDuration, r.batchSize)
			if err != nil {
				return deliveredCount, err
			}
			if len(records) == 0 {
				break
			}
			for _, rec := range records {
				if ctx.Err() != nil {
					return deliveredCount, ctx.Err()
				}
				if r.processRecord(ctx, rec) {
					deliveredCount++
				}
			}
			if len(records) < r.batchSize {
				break
			}
		}
	}
	return deliveredCount, nil
}

func (r *ControlAlarmRelay) processRecord(ctx context.Context, rec ControlAlarmOutboxRecord) bool {
	if rec.Attempts > r.maxAttempts {
		r.recordFailure(ctx, rec, "CONTROL_ALARM_LEASE_EXHAUSTED")
		return false
	}
	sendCtx, cancel := context.WithTimeout(ctx, r.deliveryTimeout)
	defer cancel()
	if err := r.invokeSenderSafely(sendCtx, rec.Event); err != nil {
		r.recordFailure(ctx, rec, ControlAlarmErrorCode(err))
		return false
	}
	if err := r.store.MarkControlAlarmDelivered(ctx, rec.TenantID, rec.OutboxID, r.leaseOwner); err != nil {
		if r.logger != nil {
			r.logger.Warn(
				"command_control_alarm_mark_delivered_failed",
				"error_code", ControlAlarmErrorCode(err),
				"outbox_id", rec.OutboxID,
				"command_id", rec.CommandID,
				"event_kind", rec.EventKind,
			)
		}
		return false
	}
	return true
}

func (r *ControlAlarmRelay) invokeSenderSafely(ctx context.Context, event ControlFailureEvent) (err error) {
	logger := r.logger
	if logger == nil {
		logger = slog.Default()
	}
	defer observability.Recover(logger, r.metrics, "command.control_alarm_relay.sender", func(any) {
		err = &ControlAlarmDeliveryError{Code: "CONTROL_ALARM_SENDER_PANIC"}
	})
	return r.sender(ctx, event)
}

func (r *ControlAlarmRelay) recordFailure(ctx context.Context, rec ControlAlarmOutboxRecord, errCode string) {
	backoff := r.NextRetryDelay(rec.Attempts)
	deadLetter := rec.Attempts >= r.maxAttempts
	if err := r.store.FailControlAlarmOutbox(ctx, rec.TenantID, rec.OutboxID, r.leaseOwner, errCode, r.maxAttempts, backoff); err != nil {
		if r.logger != nil {
			r.logger.Warn(
				"command_control_alarm_fail_update_failed",
				"error_code", ControlAlarmErrorCode(err),
				"outbox_id", rec.OutboxID,
				"command_id", rec.CommandID,
				"event_kind", rec.EventKind,
			)
		}
		return
	}
	if r.logger != nil {
		if deadLetter {
			r.logger.Error(
				"command_control_alarm_dead_lettered",
				"error_code", errCode,
				"outbox_id", rec.OutboxID,
				"command_id", rec.CommandID,
				"event_kind", rec.EventKind,
				"attempts", rec.Attempts,
			)
		} else {
			r.logger.Warn(
				"command_control_alarm_delivery_retrying",
				"error_code", errCode,
				"outbox_id", rec.OutboxID,
				"command_id", rec.CommandID,
				"event_kind", rec.EventKind,
				"attempts", rec.Attempts,
				"retry_after_ms", backoff.Milliseconds(),
			)
		}
	}
}
