package commandservice

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkSignalBroadcastAndRotation(t *testing.T) {
	t.Parallel()

	sig := newWorkSignal()
	sub1 := sig.Subscribe()
	sub2 := sig.Subscribe()

	// Notification captured before Notify wakes every subscriber without missing.
	sig.Notify()
	if !waitForWorkSignal(t.Context(), sub1, time.Second) {
		t.Fatal("sub1 missed notification")
	}
	if !waitForWorkSignal(t.Context(), sub2, time.Second) {
		t.Fatal("sub2 missed notification")
	}

	// Next subscriber blocks until timeout if no further Notify happens.
	sub3 := sig.Subscribe()
	if waitForWorkSignal(t.Context(), sub3, 15*time.Millisecond) {
		t.Fatal("expected sub3 to time out when idle")
	}

	if got := clampCrossTenantWait(10 * time.Second); got != maxCrossTenantClaimWait {
		t.Fatalf("clampCrossTenantWait(10s)=%s, want %s", got, maxCrossTenantClaimWait)
	}

	var cursor atomic.Uint64
	tenants := []string{"t1", "t2", "t3"}
	r1 := rotateTenants(tenants, &cursor)
	r2 := rotateTenants(tenants, &cursor)
	r3 := rotateTenants(tenants, &cursor)
	if r1[0] != "t1" || r2[0] != "t2" || r3[0] != "t3" {
		t.Fatalf("unexpected rotation sequence: %v %v %v", r1, r2, r3)
	}
}

func TestWorkSignalWaitRespectsCanceledContext(t *testing.T) {
	t.Parallel()

	sig := newWorkSignal()
	ch := sig.Subscribe()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitForWorkSignal(ctx, ch, time.Second) {
		t.Fatal("expected canceled wait to return false")
	}
}

func TestMemoryAuthorityStoreClaimAnyImmediateWakeup(t *testing.T) {
	t.Parallel()

	now := fixedClock()()
	store := NewMemoryAuthorityStore(func() time.Time { return now })
	defer store.Close()

	// 1. Verify MemoryAuthorityStore.Submit wakes dispatch subscribers immediately.
	dispatchSub := store.SubscribeDispatchWork()
	req := validRequest()
	req.IdempotencyKey = "wake-cmd-1"
	if _, err := store.Submit(t.Context(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-dispatchSub:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Submit did not notify dispatch work subscribers")
	}

	// 2. Verify ClaimAnyDispatch claims queued work deterministically across wakeup.
	env, err := store.ClaimAnyDispatch(t.Context(), "worker-wake", 30*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("ClaimAnyDispatch err=%v", err)
	}
	if env.TenantID != req.TenantID {
		t.Fatalf("claimed tenant=%q, want %q", env.TenantID, req.TenantID)
	}

	// 3. Verify claimAnyWork waits after an empty scan and wakes immediately on Notify without sleeping.
	sig := newWorkSignal()
	firstScanDone := make(chan struct{})
	var closeScanOnce sync.Once
	var ready atomic.Bool
	var cursor atomic.Uint64

	claimedCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		item, claimErr := claimAnyWork(
			t.Context(),
			2*time.Second,
			sig.Subscribe,
			func(context.Context) ([]string, error) {
				if !ready.Load() {
					closeScanOnce.Do(func() { close(firstScanDone) })
					return nil, nil
				}
				return []string{req.TenantID}, nil
			},
			&cursor,
			func(_ context.Context, tenantID string) (string, error) {
				return tenantID, nil
			},
			ErrNoDispatchAvailable,
		)
		if claimErr != nil {
			errCh <- claimErr
			return
		}
		claimedCh <- item
	}()

	select {
	case <-firstScanDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("claimAnyWork did not complete initial scan")
	}
	ready.Store(true)
	sig.Notify()

	select {
	case err := <-errCh:
		t.Fatalf("claimAnyWork err=%v", err)
	case item := <-claimedCh:
		if item != req.TenantID {
			t.Fatalf("claimAnyWork item=%q, want %q", item, req.TenantID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("claimAnyWork did not wake up promptly after Notify")
	}
}
