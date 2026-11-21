package commandservice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const maxCrossTenantClaimWait = 2 * time.Second

// DueTenantLister enumerates tenants that currently have due dispatch or verification work.
type DueTenantLister interface {
	DueDispatchTenants(ctx context.Context, now time.Time) ([]string, error)
	DueVerificationTenants(ctx context.Context, now time.Time) ([]string, error)
}

// WorkSignalSubscriber exposes non-racy wakeup subscriptions for pending dispatch and
// verification work.
type WorkSignalSubscriber interface {
	SubscribeDispatchWork() <-chan struct{}
	SubscribeVerificationWork() <-chan struct{}
}

// workSignal broadcasts wakeup notifications to waiting claim workers without
// losing wakeups that race between an empty scan and entering Wait.
type workSignal struct {
	mu sync.Mutex
	ch chan struct{}
}

func newWorkSignal() *workSignal {
	return &workSignal{ch: make(chan struct{})}
}

func (s *workSignal) Subscribe() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ch := s.ch
	if ch == nil {
		ch = make(chan struct{})
		s.ch = ch
	}
	s.mu.Unlock()
	return ch
}

func (s *workSignal) Notify() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ch != nil {
		close(s.ch)
		s.ch = make(chan struct{})
	}
	s.mu.Unlock()
}

func waitForWorkSignal(ctx context.Context, ch <-chan struct{}, maxWait time.Duration) bool {
	if maxWait <= 0 || ch == nil {
		return false
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func clampCrossTenantWait(waitDuration time.Duration) time.Duration {
	if waitDuration <= 0 {
		return 0
	}
	if waitDuration > maxCrossTenantClaimWait {
		return maxCrossTenantClaimWait
	}
	return waitDuration
}

func rotateTenants(tenants []string, cursor *atomic.Uint64) []string {
	if len(tenants) <= 1 || cursor == nil {
		return tenants
	}
	offset := int((cursor.Add(1) - 1) % uint64(len(tenants)))
	if offset == 0 {
		return tenants
	}
	rotated := make([]string, len(tenants))
	copy(rotated, tenants[offset:])
	copy(rotated[len(tenants)-offset:], tenants[:offset])
	return rotated
}

func claimAnyWork[T any](
	ctx context.Context,
	waitDuration time.Duration,
	subscribe func() <-chan struct{},
	listDueTenants func(context.Context) ([]string, error),
	cursor *atomic.Uint64,
	claimTenant func(context.Context, string) (T, error),
	noWorkErr error,
) (T, error) {
	var zero T
	wait := clampCrossTenantWait(waitDuration)
	var deadline time.Time
	if wait > 0 {
		deadline = time.Now().Add(wait)
	}
	for {
		var sig <-chan struct{}
		if wait > 0 && subscribe != nil {
			sig = subscribe()
		}
		tenants, err := listDueTenants(ctx)
		if err != nil {
			return zero, err
		}
		ordered := rotateTenants(tenants, cursor)
		for _, tenantID := range ordered {
			item, claimErr := claimTenant(ctx, tenantID)
			if claimErr == nil {
				return item, nil
			}
			if !errors.Is(claimErr, noWorkErr) {
				return zero, claimErr
			}
		}
		if wait <= 0 || sig == nil {
			return zero, noWorkErr
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return zero, noWorkErr
		}
		if !waitForWorkSignal(ctx, sig, remaining) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return zero, ctxErr
			}
			return zero, noWorkErr
		}
	}
}
