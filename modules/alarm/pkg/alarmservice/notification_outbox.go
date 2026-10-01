package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type NotificationAction string

const (
	NotificationCreated         NotificationAction = "CREATED"
	NotificationSeverityChanged NotificationAction = "SEVERITY_CHANGED"
	NotificationAcknowledged    NotificationAction = "ACKNOWLEDGED"
	NotificationCleared         NotificationAction = "CLEARED"
)

type NotificationOutboxEvent struct {
	TenantID              string
	SiteID                string
	SourceEventID         string
	AlarmID               string
	AlarmVersion          uint64
	IncidentCorrelationID string
	Action                NotificationAction
	CurrentSeverity       alarmmodel.Severity
	PeakSeverity          alarmmodel.Severity
	Condition             alarmmodel.Condition
	Attributes            map[string]string
	OccurredAt            time.Time
	LeaseOwner            string
	LeaseUntil            time.Time
	LeaseFence            uint64
}

type NotificationRelay struct {
	pool *pgxpool.Pool
}

func OpenNotificationRelay(ctx context.Context, databaseURL string) (*NotificationRelay, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Alarm notification relay database URL: %w", err)
	}
	if config.ConnConfig.User != "s4_alarm_service" {
		return nil, errors.New("Alarm notification relay database identity must be s4_alarm_service")
	}
	config.MaxConns = 4
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s4_alarm_notification_relay`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Alarm notification relay database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Alarm notification relay database: %w", err)
	}
	return &NotificationRelay{pool: pool}, nil
}

func (relay *NotificationRelay) Close() {
	if relay != nil && relay.pool != nil {
		relay.pool.Close()
	}
}

func (relay *NotificationRelay) Claim(ctx context.Context, workerID string, now time.Time, leaseDuration time.Duration) (NotificationOutboxEvent, error) {
	if relay == nil || relay.pool == nil || strings.TrimSpace(workerID) == "" || now.IsZero() || leaseDuration <= 0 {
		return NotificationOutboxEvent{}, ErrUnavailable
	}
	tx, err := relay.pool.Begin(ctx)
	if err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("begin Alarm notification relay claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceEventID string
	err = tx.QueryRow(ctx, `
		SELECT source_event_id::text
		FROM alarm_runtime.notification_outbox
		WHERE state='READY' OR (state='LEASED' AND lease_until <= $1)
		ORDER BY created_at, source_event_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, now.UTC()).Scan(&sourceEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationOutboxEvent{}, ErrNotFound
	}
	if err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("claim Alarm notification source event: %w", err)
	}
	leaseUntil := now.UTC().Add(leaseDuration)
	_, err = tx.Exec(ctx, `
		UPDATE alarm_runtime.notification_outbox
		SET state='LEASED', lease_owner=$2, lease_until=$3, lease_fence=lease_fence+1
		WHERE source_event_id=$1::uuid`, sourceEventID, strings.TrimSpace(workerID), leaseUntil)
	if err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("lease Alarm notification source event: %w", err)
	}
	event, err := loadNotificationOutboxEvent(ctx, tx, sourceEventID, true)
	if err != nil {
		return NotificationOutboxEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("commit Alarm notification relay claim: %w", err)
	}
	return event, nil
}

func (relay *NotificationRelay) Complete(ctx context.Context, sourceEventID, workerID string, fence uint64, now time.Time) error {
	if relay == nil || relay.pool == nil || !alarmmodel.IsUUIDv7(sourceEventID) || strings.TrimSpace(workerID) == "" || fence == 0 || now.IsZero() {
		return ErrUnavailable
	}
	command, err := relay.pool.Exec(ctx, `
		UPDATE alarm_runtime.notification_outbox
		SET state='DELIVERED', delivered_at=$4, lease_owner=NULL, lease_until=NULL
		WHERE source_event_id=$1::uuid AND state='LEASED' AND lease_owner=$2 AND lease_fence=$3 AND lease_until > $4`,
		sourceEventID, strings.TrimSpace(workerID), fence, now.UTC())
	if err != nil {
		return fmt.Errorf("complete Alarm notification source event: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func enqueueNotificationEvent(ctx context.Context, tx pgx.Tx, store *PostgresStore, alarm alarmmodel.Alarm, action NotificationAction, occurredAt time.Time) error {
	if store == nil || store.newID == nil || occurredAt.IsZero() {
		return ErrUnavailable
	}
	sourceEventID, err := store.newID(occurredAt.Add(time.Duration(alarm.Version) * time.Nanosecond))
	if err != nil {
		return fmt.Errorf("generate Alarm notification source event ID: %w", err)
	}
	attributesJSON, err := json.Marshal(map[string]string{
		"alarmType":       alarm.AlarmType,
		"title":           alarm.Title,
		"summary":         alarm.Summary,
		"sourceType":      string(alarm.SourceType),
		"sourceReference": alarm.SourceReference,
		"ruleRevision":    alarm.RuleRevision,
	})
	if err != nil {
		return fmt.Errorf("encode Alarm notification attributes: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO alarm_runtime.notification_outbox (
			tenant_id, site_id, source_event_id, alarm_id, alarm_version, incident_correlation_id, action,
			current_severity, peak_severity, condition, attributes, occurred_at, state, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'READY',$13)
		ON CONFLICT (tenant_id, alarm_id, alarm_version, action) DO NOTHING`,
		alarm.TenantID, alarm.SiteID, sourceEventID, alarm.AlarmID, alarm.Version, alarm.IncidentCorrelationID, action,
		alarm.CurrentSeverity, alarm.PeakSeverity, alarm.Condition, attributesJSON, occurredAt.UTC(), occurredAt.UTC())
	if err != nil {
		return fmt.Errorf("enqueue Alarm notification source event: %w", err)
	}
	return nil
}

func loadNotificationOutboxEvent(ctx context.Context, tx pgx.Tx, sourceEventID string, lock bool) (NotificationOutboxEvent, error) {
	query := `SELECT tenant_id::text,site_id::text,source_event_id::text,alarm_id::text,alarm_version,incident_correlation_id::text,
		action,current_severity,peak_severity,condition,attributes,occurred_at,COALESCE(lease_owner,''),lease_until,lease_fence
		FROM alarm_runtime.notification_outbox WHERE source_event_id=$1::uuid`
	if lock {
		query += ` FOR UPDATE`
	}
	var event NotificationOutboxEvent
	var attributesJSON []byte
	var leaseUntil *time.Time
	err := tx.QueryRow(ctx, query, sourceEventID).Scan(&event.TenantID, &event.SiteID, &event.SourceEventID, &event.AlarmID, &event.AlarmVersion,
		&event.IncidentCorrelationID, &event.Action, &event.CurrentSeverity, &event.PeakSeverity, &event.Condition, &attributesJSON,
		&event.OccurredAt, &event.LeaseOwner, &leaseUntil, &event.LeaseFence)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationOutboxEvent{}, ErrNotFound
	}
	if err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("read Alarm notification source event: %w", err)
	}
	if err := json.Unmarshal(attributesJSON, &event.Attributes); err != nil {
		return NotificationOutboxEvent{}, fmt.Errorf("decode Alarm notification attributes: %w", err)
	}
	if leaseUntil != nil {
		event.LeaseUntil = leaseUntil.UTC()
	}
	return event, nil
}
