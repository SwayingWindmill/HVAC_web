package domainoutbox

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

type Event struct {
	ID               string
	TenantID         string
	SiteID           string
	EventType        string
	SchemaVersion    int
	SubjectType      string
	SubjectID        string
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	OccurredAt       time.Time
	TraceID          string
	CorrelationID    string
	Payload          json.RawMessage
}

type Delivery struct {
	Event
	ConsumerName string
	Attempt      int
	LeaseOwner   string
	LeaseUntil   time.Time
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("domain outbox postgres pool is required")
	}
	return &Store{pool: pool}, nil
}

func activateScope(ctx context.Context, tx pgx.Tx, tenantID, siteID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return errors.New("domain event Tenant scope is required")
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	if strings.TrimSpace(siteID) == "" {
		_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids','{}',true)")
		return err
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.authorized_site_ids',$1,true)", "{"+siteID+"}")
	return err
}

// Claim leases one delivery for one consumer. Fan-out is represented by one
// delivery row per consumer; no consumer can consume another consumer's work.
func (s *Store) Claim(ctx context.Context, tenantID, siteID, consumerName, leaseOwner string, leaseFor time.Duration) (Delivery, error) {
	if s == nil || s.pool == nil || strings.TrimSpace(consumerName) == "" || strings.TrimSpace(leaseOwner) == "" || leaseFor <= 0 {
		return Delivery{}, errors.New("domain delivery claim arguments are invalid")
	}
	now := time.Now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Delivery{}, err
	}
	defer tx.Rollback(ctx)
	if err = activateScope(ctx, tx, tenantID, siteID); err != nil {
		return Delivery{}, err
	}
	var delivery Delivery
	var payload []byte
	var nullableSite, traceID, correlationID *string
	leaseUntil := now.Add(leaseFor)
	err = tx.QueryRow(ctx, `
SELECT e.id::text,e.tenant_id::text,e.site_id::text,e.event_type,e.schema_version,
       e.subject_type,e.subject_id::text,e.aggregate_type,e.aggregate_id::text,e.aggregate_version,
       e.occurred_at,e.trace_id,e.correlation_id,e.payload,d.consumer_name,d.attempt
FROM core_registry.domain_event_deliveries d
JOIN core_registry.domain_outbox_events e ON e.id=d.event_id
WHERE e.tenant_id=$1::uuid
  AND ($2='' OR e.site_id=$2::uuid)
  AND d.consumer_name=$3
  AND d.status IN ('PENDING','FAILED')
  AND (d.next_retry_at IS NULL OR d.next_retry_at <= $4)
  AND (d.lease_until IS NULL OR d.lease_until <= $4)
ORDER BY e.aggregate_type,e.aggregate_id,e.aggregate_version,e.occurred_at,e.id
FOR UPDATE OF d SKIP LOCKED
LIMIT 1`, tenantID, siteID, consumerName, now).Scan(
		&delivery.ID, &delivery.TenantID, &nullableSite, &delivery.EventType, &delivery.SchemaVersion,
		&delivery.SubjectType, &delivery.SubjectID, &delivery.AggregateType, &delivery.AggregateID, &delivery.AggregateVersion,
		&delivery.OccurredAt, &traceID, &correlationID, &payload, &delivery.ConsumerName, &delivery.Attempt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, pgx.ErrNoRows
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("claim domain event delivery: %w", err)
	}
	if nullableSite != nil {
		delivery.SiteID = *nullableSite
	}
	if traceID != nil {
		delivery.TraceID = *traceID
	}
	if correlationID != nil {
		delivery.CorrelationID = *correlationID
	}
	delivery.Payload = append(json.RawMessage(nil), payload...)
	delivery.LeaseOwner = leaseOwner
	delivery.LeaseUntil = leaseUntil
	_, err = tx.Exec(ctx, `UPDATE core_registry.domain_event_deliveries
SET status='LEASED',lease_owner=$3,lease_until=$4,attempt=attempt+1,updated_at=$5
WHERE event_id=$1::uuid AND consumer_name=$2`, delivery.ID, consumerName, leaseOwner, leaseUntil, now)
	if err != nil {
		return Delivery{}, err
	}
	delivery.Attempt++
	if err = tx.Commit(ctx); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// ShouldApply is the aggregate-ordering fence. Equal/older versions are
// idempotently ignored; a gap is allowed because another delivery may be
// intentionally absent for this consumer, but versions can never move backward.
func (s *Store) ShouldApply(ctx context.Context, delivery Delivery) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = activateScope(ctx, tx, delivery.TenantID, delivery.SiteID); err != nil {
		return false, err
	}
	var last int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(aggregate_version),0)
FROM core_registry.domain_consumer_inbox
WHERE consumer_name=$1 AND aggregate_type=$2 AND aggregate_id=$3::uuid`, delivery.ConsumerName, delivery.AggregateType, delivery.AggregateID).Scan(&last)
	if err != nil {
		return false, err
	}
	return delivery.AggregateVersion > last, tx.Commit(ctx)
}

// Complete atomically records the consumer inbox/offset and completes this
// consumer's delivery. This is the at-least-once + idempotent-consumer boundary.
func (s *Store) Complete(ctx context.Context, delivery Delivery, applied bool) error {
	now := time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = activateScope(ctx, tx, delivery.TenantID, delivery.SiteID); err != nil {
		return err
	}
	if applied {
		_, err = tx.Exec(ctx, `INSERT INTO core_registry.domain_consumer_inbox(
event_id,consumer_name,aggregate_type,aggregate_id,aggregate_version,applied_at)
VALUES($1::uuid,$2,$3,$4::uuid,$5,$6)
ON CONFLICT (event_id,consumer_name) DO NOTHING`, delivery.ID, delivery.ConsumerName, delivery.AggregateType, delivery.AggregateID, delivery.AggregateVersion, now)
		if err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.domain_event_deliveries
SET status='DELIVERED',lease_owner=NULL,lease_until=NULL,delivered_at=$4,last_error=NULL,updated_at=$4
WHERE event_id=$1::uuid AND consumer_name=$2 AND status='LEASED' AND lease_owner=$3`, delivery.ID, delivery.ConsumerName, delivery.LeaseOwner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("domain event delivery lease is no longer owned")
	}
	return tx.Commit(ctx)
}

func (s *Store) Retry(ctx context.Context, delivery Delivery, processingErr error, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = 5 * time.Second
	}
	now := time.Now().UTC()
	failure := "consumer failed"
	if processingErr != nil {
		failure = processingErr.Error()
		if len(failure) > 2048 {
			failure = failure[:2048]
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = activateScope(ctx, tx, delivery.TenantID, delivery.SiteID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.domain_event_deliveries
SET status='FAILED',lease_owner=NULL,lease_until=NULL,next_retry_at=$4,last_error=$5,updated_at=$6
WHERE event_id=$1::uuid AND consumer_name=$2 AND status='LEASED' AND lease_owner=$3`,
		delivery.ID, delivery.ConsumerName, delivery.LeaseOwner, now.Add(retryAfter), failure, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("domain event delivery lease is no longer owned")
	}
	return tx.Commit(ctx)
}
