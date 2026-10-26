package notificationservice

import (
	"context"
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
		return nil, errors.New("notification scheduler database identity must be s16_notification_service")
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
