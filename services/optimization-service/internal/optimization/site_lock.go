package optimization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	siteCycleTryLockSQL    = `SELECT pg_try_advisory_lock(hashtext('hvac_opt_site_cycle:' || $1::text), hashtext($2::text))`
	siteCycleUnlockSQL     = `SELECT pg_advisory_unlock(hashtext('hvac_opt_site_cycle:' || $1::text), hashtext($2::text))`
	siteCycleUnlockTimeout = 2 * time.Second
)

// SiteCycleLock serializes optimization cycles for a given (tenantID, siteID) across replicas.
type SiteCycleLock interface {
	AcquireSiteCycleLock(ctx context.Context, tenantID, siteID string) (release func(), acquired bool, err error)
}

type siteAdvisoryLockConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Release()
	CloseOnError(ctx context.Context)
}

type pgxSiteAdvisoryLockConn struct {
	conn *pgxpool.Conn
}

func (c pgxSiteAdvisoryLockConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.conn.QueryRow(ctx, sql, args...)
}

func (c pgxSiteAdvisoryLockConn) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return c.conn.Exec(ctx, sql, arguments...)
}

func (c pgxSiteAdvisoryLockConn) Release() {
	c.conn.Release()
}

func (c pgxSiteAdvisoryLockConn) CloseOnError(ctx context.Context) {
	if c.conn != nil && c.conn.Conn() != nil {
		_ = c.conn.Conn().Close(ctx)
	}
}

// AcquireSiteCycleLock obtains a session-scoped PostgreSQL advisory lock for (tenantID, siteID)
// so concurrent replicas never run overlapping optimization or auto-dispatch cycles for the same site.
func (store *PostgresStore) AcquireSiteCycleLock(ctx context.Context, tenantID, siteID string) (func(), bool, error) {
	if store == nil || store.pool == nil {
		return nil, false, errors.New("optimization postgres store is unavailable")
	}
	tenant := strings.TrimSpace(tenantID)
	site := strings.TrimSpace(siteID)
	if tenant == "" || site == "" {
		return nil, false, errors.New("site cycle lock scope is invalid")
	}
	conn, err := store.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire site cycle lock connection: %w", err)
	}
	return acquireSiteCycleLockOnConn(ctx, pgxSiteAdvisoryLockConn{conn: conn}, tenant, site)
}

func acquireSiteCycleLockOnConn(ctx context.Context, conn siteAdvisoryLockConn, tenantID, siteID string) (func(), bool, error) {
	var acquired bool
	if err := conn.QueryRow(ctx, siteCycleTryLockSQL, tenantID, siteID).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("try site cycle advisory lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), siteCycleUnlockTimeout)
			defer cancel()
			if _, err := conn.Exec(unlockCtx, siteCycleUnlockSQL, tenantID, siteID); err != nil {
				conn.CloseOnError(unlockCtx)
			}
			conn.Release()
		})
	}
	return release, true, nil
}
