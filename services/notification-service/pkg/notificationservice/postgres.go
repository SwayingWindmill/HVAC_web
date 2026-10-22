package notificationservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("notification object not found")

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("notification store is unavailable")
	}
	return store.pool.Ping(ctx)
}

// RecordAlarmEvent takes one Alarm event from the Alarm owner's outbox. Only a new urgent or
// important Alarm leads anywhere: a WeChat reminder (wechat_alarm_reminders.go). Seeing the same
// Alarm again changes nothing.
func (store *PostgresStore) RecordAlarmEvent(ctx context.Context, event AlarmEvent, recordedAt time.Time) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if recordedAt.IsZero() {
		return errors.New("notification event recorded time is required")
	}
	tx, err := store.beginTenant(ctx, event.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := recordAlarmReminder(ctx, tx, event, recordedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if store == nil || store.pool == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("notification Tenant scope is required")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s16_notification_runtime`); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
