package commandservice

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
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

	claimedCh := make(chan commandmodel.DispatchEnvelope, 1)
	errCh := make(chan error, 1)
	go func() {
		env, err := store.ClaimAnyDispatch(t.Context(), "worker-wake", 30*time.Second, 2*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		claimedCh <- env
	}()

	time.Sleep(25 * time.Millisecond)
	req := validRequest()
	req.IdempotencyKey = "wake-cmd-1"
	if _, err := store.Submit(t.Context(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ClaimAnyDispatch err=%v", err)
	case env := <-claimedCh:
		if env.TenantID != req.TenantID {
			t.Fatalf("claimed tenant=%q, want %q", env.TenantID, req.TenantID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ClaimAnyDispatch did not wake up promptly after Submit")
	}
}

