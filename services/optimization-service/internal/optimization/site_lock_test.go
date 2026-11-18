package optimization

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakePgxRow struct {
	scan func(dest ...any) error
}

func (r fakePgxRow) Scan(dest ...any) error {
	return r.scan(dest...)
}

type capturedExecContext struct {
	err         error
	deadline    time.Time
	hasDeadline bool
}

type fakeSiteAdvisoryLockConn struct {
	mu           sync.Mutex
	registry     *fakeAdvisoryLockRegistry
	lockKey      string
	acquired     bool
	tryErr       error
	unlockErr    error
	querySQLs    []string
	queryArgs    [][]any
	execSQLs     []string
	execContexts []capturedExecContext
	releaseCount atomic.Int32
	closeCount   atomic.Int32
	unlockCount  atomic.Int32
}

type fakeAdvisoryLockRegistry struct {
	mu    sync.Mutex
	locks map[string]struct{}
}

func newFakeAdvisoryLockRegistry() *fakeAdvisoryLockRegistry {
	return &fakeAdvisoryLockRegistry{locks: make(map[string]struct{})}
}

func (c *fakeSiteAdvisoryLockConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.mu.Lock()
	c.querySQLs = append(c.querySQLs, sql)
	c.queryArgs = append(c.queryArgs, append([]any(nil), args...))
	tryErr := c.tryErr
	c.mu.Unlock()

	return fakePgxRow{
		scan: func(dest ...any) error {
			if tryErr != nil {
				return tryErr
			}
			out, ok := dest[0].(*bool)
			if !ok {
				return errors.New("expected *bool scan destination")
			}
			if c.registry != nil && len(args) == 2 {
				tenantID, _ := args[0].(string)
				siteID, _ := args[1].(string)
				key := tenantID + ":" + siteID
				c.registry.mu.Lock()
				_, held := c.registry.locks[key]
				if !held {
					c.registry.locks[key] = struct{}{}
					c.lockKey = key
					*out = true
				} else {
					*out = false
				}
				c.registry.mu.Unlock()
				return nil
			}
			*out = c.acquired
			return nil
		},
	}
}

func (c *fakeSiteAdvisoryLockConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.unlockCount.Add(1)
	deadline, hasDeadline := ctx.Deadline()
	c.mu.Lock()
	c.execSQLs = append(c.execSQLs, sql)
	c.execContexts = append(c.execContexts, capturedExecContext{
		err:         ctx.Err(),
		deadline:    deadline,
		hasDeadline: hasDeadline,
	})
	unlockErr := c.unlockErr
	lockKey := c.lockKey
	c.mu.Unlock()

	if c.registry != nil && lockKey != "" {
		c.registry.mu.Lock()
		delete(c.registry.locks, lockKey)
		c.registry.mu.Unlock()
	}
	return pgconn.CommandTag{}, unlockErr
}

func (c *fakeSiteAdvisoryLockConn) Release() {
	c.releaseCount.Add(1)
}

func (c *fakeSiteAdvisoryLockConn) CloseOnError(context.Context) {
	c.closeCount.Add(1)
}

func TestAcquireSiteCycleLockValidationAndErrors(t *testing.T) {
	var _ SiteCycleLock = (*PostgresStore)(nil)

	var nilStore *PostgresStore
	if rel, acquired, err := nilStore.AcquireSiteCycleLock(context.Background(), "t1", "s1"); err == nil || acquired || rel != nil {
		t.Fatalf("expected error on nil PostgresStore, got relNonNil=%t acquired=%v err=%v", rel != nil, acquired, err)
	}
	emptyStore := &PostgresStore{}
	if rel, acquired, err := emptyStore.AcquireSiteCycleLock(context.Background(), "", "s1"); err == nil || acquired || rel != nil {
		t.Fatalf("expected error on missing pool/tenant, got relNonNil=%t acquired=%v err=%v", rel != nil, acquired, err)
	}
}

func TestAcquireSiteCycleLockOnConnLifecycle(t *testing.T) {
	t.Run("releases connection immediately when advisory lock not acquired", func(t *testing.T) {
		conn := &fakeSiteAdvisoryLockConn{acquired: false}
		release, acquired, err := acquireSiteCycleLockOnConn(context.Background(), conn, "tenant-a", "site-1")
		if err != nil || acquired || release != nil {
			t.Fatalf("got releaseNonNil=%t acquired=%v err=%v, want false,false,nil", release != nil, acquired, err)
		}
		if conn.releaseCount.Load() != 1 || conn.unlockCount.Load() != 0 {
			t.Fatalf("expected immediate release without unlock: releases=%d unlocks=%d", conn.releaseCount.Load(), conn.unlockCount.Load())
		}
		if len(conn.querySQLs) != 1 || conn.querySQLs[0] != siteCycleTryLockSQL {
			t.Fatalf("unexpected try lock SQL: %+v", conn.querySQLs)
		}
	})

	t.Run("releases connection immediately on query error", func(t *testing.T) {
		wantErr := errors.New("db failure")
		conn := &fakeSiteAdvisoryLockConn{tryErr: wantErr}
		release, acquired, err := acquireSiteCycleLockOnConn(context.Background(), conn, "tenant-a", "site-1")
		if !errors.Is(err, wantErr) || acquired || release != nil {
			t.Fatalf("got releaseNonNil=%t acquired=%v err=%v, want wrapped %v", release != nil, acquired, err, wantErr)
		}
		if conn.releaseCount.Load() != 1 || conn.unlockCount.Load() != 0 {
			t.Fatalf("expected immediate release without unlock on error: releases=%d unlocks=%d", conn.releaseCount.Load(), conn.unlockCount.Load())
		}
	})

	t.Run("release unlocks exactly once using fresh detached context even when caller ctx canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		conn := &fakeSiteAdvisoryLockConn{acquired: true}
		release, acquired, err := acquireSiteCycleLockOnConn(ctx, conn, "tenant-a", "site-1")
		if err != nil || !acquired || release == nil {
			t.Fatalf("got releaseNonNil=%t acquired=%v err=%v", release != nil, acquired, err)
		}
		cancel()

		release()
		release()
		release()

		if conn.unlockCount.Load() != 1 || conn.releaseCount.Load() != 1 || conn.closeCount.Load() != 0 {
			t.Fatalf("expected exactly 1 unlock and 1 release, got unlocks=%d releases=%d closes=%d",
				conn.unlockCount.Load(), conn.releaseCount.Load(), conn.closeCount.Load())
		}
		if len(conn.execSQLs) != 1 || conn.execSQLs[0] != siteCycleUnlockSQL {
			t.Fatalf("unexpected unlock SQL: %+v", conn.execSQLs)
		}
		unlockCtx := conn.execContexts[0]
		if unlockCtx.err != nil {
			t.Fatalf("unlock context must be detached from canceled caller ctx during Exec, got err=%v", unlockCtx.err)
		}
		if !unlockCtx.hasDeadline || time.Until(unlockCtx.deadline) <= 0 || time.Until(unlockCtx.deadline) > 3*time.Second {
			t.Fatalf("expected ~2s unlock timeout deadline, got ok=%v remaining=%v", unlockCtx.hasDeadline, time.Until(unlockCtx.deadline))
		}
	})

	t.Run("closes underlying connection before release if unlock fails", func(t *testing.T) {
		conn := &fakeSiteAdvisoryLockConn{acquired: true, unlockErr: errors.New("socket closed")}
		release, acquired, err := acquireSiteCycleLockOnConn(context.Background(), conn, "tenant-a", "site-1")
		if err != nil || !acquired {
			t.Fatalf("unexpected acquire result: acquired=%v err=%v", acquired, err)
		}
		release()
		if conn.closeCount.Load() != 1 || conn.releaseCount.Load() != 1 {
			t.Fatalf("expected CloseOnError then Release on unlock error, got closes=%d releases=%d",
				conn.closeCount.Load(), conn.releaseCount.Load())
		}
	})

	t.Run("concurrent acquisition serializes same site while allowing different sites", func(t *testing.T) {
		registry := newFakeAdvisoryLockRegistry()
		start := make(chan struct{})
		const workers = 6
		acquiredSiteA := make([]bool, workers)
		releases := make([]func(), workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				conn := &fakeSiteAdvisoryLockConn{registry: registry}
				rel, acq, err := acquireSiteCycleLockOnConn(context.Background(), conn, "tenant-a", "site-a")
				if err != nil {
					t.Errorf("worker %d err=%v", idx, err)
					return
				}
				acquiredSiteA[idx] = acq
				releases[idx] = rel
			}(i)
		}
		close(start)
		wg.Wait()

		wins := 0
		var winnerRelease func()
		for i, acq := range acquiredSiteA {
			if acq {
				wins++
				winnerRelease = releases[i]
			}
		}
		if wins != 1 || winnerRelease == nil {
			t.Fatalf("expected exactly 1 winner for site-a, got wins=%d", wins)
		}

		// Different site can acquire concurrently while site-a is held.
		connB := &fakeSiteAdvisoryLockConn{registry: registry}
		relB, acqB, errB := acquireSiteCycleLockOnConn(context.Background(), connB, "tenant-a", "site-b")
		if errB != nil || !acqB || relB == nil {
			t.Fatalf("expected concurrent lock on site-b to succeed, got acq=%v err=%v", acqB, errB)
		}
		relB()

		// Releasing site-a allows the next cycle to acquire it.
		winnerRelease()
		connA2 := &fakeSiteAdvisoryLockConn{registry: registry}
		relA2, acqA2, errA2 := acquireSiteCycleLockOnConn(context.Background(), connA2, "tenant-a", "site-a")
		if errA2 != nil || !acqA2 || relA2 == nil {
			t.Fatalf("expected re-acquire on site-a after release to succeed, got acq=%v err=%v", acqA2, errA2)
		}
		relA2()
	})
}
