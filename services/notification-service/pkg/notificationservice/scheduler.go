package notificationservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Scheduler struct {
	pool *pgxpool.Pool
}

func OpenScheduler(ctx context.Context, databaseURL string) (*Scheduler, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse Notification scheduler database URL: %w", err)
	}
	if config.ConnConfig.User != "s16_notification_service" {
		return nil, errors.New("Notification scheduler database identity must be s16_notification_service")
	}
	config.MaxConns = 4
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s16_notification_scheduler`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Notification scheduler database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Notification scheduler database: %w", err)
	}
	return &Scheduler{pool: pool}, nil
}

func (scheduler *Scheduler) Close() {
	if scheduler != nil && scheduler.pool != nil {
		scheduler.pool.Close()
	}
}

func (scheduler *Scheduler) ClaimDue(ctx context.Context, workerID string, now time.Time, leaseDuration time.Duration) (*IntentClaim, error) {
	if scheduler == nil || scheduler.pool == nil || strings.TrimSpace(workerID) == "" || now.IsZero() || leaseDuration <= 0 {
		return nil, errors.New("Notification scheduler claim input is invalid")
	}
	tx, err := scheduler.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tenantID, intentID string
	err = tx.QueryRow(ctx, `SELECT tenant_id::text,intent_id::text FROM notification_runtime.notification_intent
WHERE due_at <= $1 AND (status='SCHEDULED' OR (status='CLAIMED' AND lease_until <= $1))
ORDER BY due_at,intent_id FOR UPDATE SKIP LOCKED LIMIT 1`, now.UTC()).Scan(&tenantID, &intentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	leaseUntil := now.UTC().Add(leaseDuration)
	_, err = tx.Exec(ctx, `UPDATE notification_runtime.notification_intent
SET status='CLAIMED',lease_owner=$3,lease_until=$4,lease_fence=lease_fence+1,updated_at=$2
WHERE tenant_id=$1::uuid AND intent_id=$5::uuid`, tenantID, now.UTC(), strings.TrimSpace(workerID), leaseUntil, intentID)
	if err != nil {
		return nil, err
	}
	claim, err := scanSchedulerClaim(tx.QueryRow(ctx, intentClaimSelect+` WHERE tenant_id=$1::uuid AND intent_id=$2::uuid`, tenantID, intentID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}

func (scheduler *Scheduler) NextUnboundExternalHandoff(ctx context.Context) (NotificationIntent, error) {
	if scheduler == nil || scheduler.pool == nil {
		return NotificationIntent{}, ErrNotFound
	}
	return scanIntent(scheduler.pool.QueryRow(ctx, intentSelect+`
 WHERE status='EXTERNAL_SUBMITTED' AND external_delivery_intent_id IS NULL
 ORDER BY updated_at,intent_id LIMIT 1`))
}

func (scheduler *Scheduler) NextPendingExternalDisposition(ctx context.Context) (NotificationIntent, error) {
	if scheduler == nil || scheduler.pool == nil {
		return NotificationIntent{}, ErrNotFound
	}
	return scanIntent(scheduler.pool.QueryRow(ctx, intentSelect+`
 WHERE status='EXTERNAL_SUBMITTED' AND external_delivery_intent_id IS NOT NULL AND disposition='PENDING'
 ORDER BY updated_at,intent_id LIMIT 1`))
}

func scanSchedulerClaim(row interface{ Scan(...any) error }) (IntentClaim, error) {
	var claim IntentClaim
	var recipients []byte
	var leaseOwner *string
	var leaseUntil *time.Time
	if err := row.Scan(&claim.IntentID, &claim.TenantID, &claim.SiteID, &claim.SourceEventID, &claim.AlarmID, &claim.IncidentCorrelationID,
		&claim.SourceAction, &claim.CurrentSeverity, &claim.PolicyRevisionID, &claim.AssignmentID, &claim.AssignmentRevision, &claim.Stage, &claim.Channel, &claim.IntegrationID,
		&claim.MandatorySafety, &recipients, &claim.TemplateRevisionID, &claim.RenderedSubject, &claim.RenderedBody, &claim.DueAt, &claim.Status,
		&claim.Disposition, &claim.ExternalDeliveryIntentID, &leaseOwner, &leaseUntil, &claim.Fence); err != nil {
		return IntentClaim{}, err
	}
	if err := json.Unmarshal(recipients, &claim.Recipients); err != nil {
		return IntentClaim{}, err
	}
	if leaseOwner != nil {
		claim.WorkerID = *leaseOwner
	}
	if leaseUntil != nil {
		claim.LeaseUntil = *leaseUntil
	}
	return claim, nil
}
