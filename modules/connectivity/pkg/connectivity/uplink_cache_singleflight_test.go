package connectivity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

func TestUplinkCacheSingleflightCoalescesConcurrentColdLookups(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	gw := adapter.Gateway{ID: "0194f000-2000-7000-8000-000000000001", TenantID: "0194f000-0000-7000-8000-000000000001", SiteID: "0194f000-1000-7000-8000-000000000001"}

	t.Run("resolveGatewayCachedWith coalesces 32 goroutines", func(t *testing.T) {
		var calls atomic.Int32
		gate := make(chan struct{})
		const workers = 32
		var ready sync.WaitGroup
		ready.Add(workers)
		results := make(chan adapter.Gateway, workers)
		errs := make(chan error, workers)

		for range workers {
			go func() {
				ready.Done()
				ready.Wait()
				got, err := store.resolveGatewayCachedWith(context.Background(), gw.ID, func(ctx context.Context, gatewayID string, _ time.Time) (adapter.Gateway, time.Time, error) {
					calls.Add(1)
					<-gate
					return gw, now.Add(time.Minute), nil
				})
				results <- got
				errs <- err
			}()
		}
		time.Sleep(25 * time.Millisecond)
		close(gate)

		for range workers {
			if err := <-errs; err != nil {
				t.Fatalf("resolveGatewayCachedWith err: %v", err)
			}
			if got := <-results; got != gw {
				t.Fatalf("got gateway=%+v, want %+v", got, gw)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("underlying DB calls=%d, want 1", calls.Load())
		}
	})

	t.Run("resolveDeviceCached coalesces 32 goroutines", func(t *testing.T) {
		var calls atomic.Int32
		gate := make(chan struct{})
		const workers = 32
		var wg sync.WaitGroup
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				id, err := store.resolveDeviceCached(context.Background(), gw, "DEV-01", func(context.Context, adapter.Gateway, string) (string, error) {
					calls.Add(1)
					<-gate
					return "dev-uuid-01", nil
				})
				if err != nil || id != "dev-uuid-01" {
					t.Errorf("got=(%q,%v), want=(dev-uuid-01,nil)", id, err)
				}
			}()
		}
		time.Sleep(25 * time.Millisecond)
		close(gate)
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("resolveDeviceCached DB calls=%d, want 1", calls.Load())
		}
	})

	t.Run("resolveBindingsCachedWith coalesces 32 goroutines and isolates copies", func(t *testing.T) {
		var calls atomic.Int32
		gate := make(chan struct{})
		const workers = 32
		queries := []adapter.DevicePointQuery{
			{SourceKey: "CH-1", PointCode: "temp"},
			{SourceKey: "CH-2", PointCode: "flow"},
		}
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := range workers {
			go func(idx int) {
				defer wg.Done()
				bindings, err := store.resolveBindingsCachedWith(context.Background(), gw, queries, func(context.Context, adapter.Gateway, []string, []resolvedPointTarget, []adapter.DevicePointQuery) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
					calls.Add(1)
					<-gate
					unit := "Cel"
					return adapter.ResolvedBindings{
						Devices: map[string]string{"CH-1": "d1", "CH-2": "d2"},
						Points: map[string]*adapter.ResolvedPoint{
							adapter.PointBindingKey("d1", "temp"): {PointID: "p1", Unit: &unit, PointRevision: 1},
							adapter.PointBindingKey("d2", "flow"): {PointID: "p2", PointRevision: 1},
						},
					}, []resolvedPointTarget{{deviceID: "d1", pointCode: "temp"}, {deviceID: "d2", pointCode: "flow"}}, nil
				})
				if err != nil {
					t.Errorf("worker %d err: %v", idx, err)
					return
				}
				pt := bindings.Point("d1", "temp")
				if pt == nil || pt.Unit == nil || *pt.Unit != "Cel" {
					t.Errorf("worker %d got corrupted point: %+v", idx, pt)
					return
				}
				*pt.Unit = fmt.Sprintf("mutated-%d", idx)
				bindings.Devices["CH-1"] = fmt.Sprintf("mutated-%d", idx)
			}(i)
		}
		time.Sleep(25 * time.Millisecond)
		close(gate)
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("resolveBindingsCachedWith DB calls=%d, want 1", calls.Load())
		}
		cached, err := store.resolveBindingsCachedWith(context.Background(), gw, queries, func(context.Context, adapter.Gateway, []string, []resolvedPointTarget, []adapter.DevicePointQuery) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
			t.Fatal("expected cache hit after coalesced populate")
			return adapter.ResolvedBindings{}, nil, nil
		})
		if err != nil {
			t.Fatalf("post-flight cache hit err: %v", err)
		}
		if cached.Devices["CH-1"] != "d1" || *cached.Point("d1", "temp").Unit != "Cel" {
			t.Fatalf("caller mutation leaked into cache: %+v", cached)
		}
	})
}

func TestUplinkCacheSingleflightWithoutCancelPreservesWaitingPeers(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	gw := adapter.Gateway{ID: "0194f000-2000-7000-8000-000000000002", TenantID: "0194f000-0000-7000-8000-000000000001", SiteID: "0194f000-1000-7000-8000-000000000001"}

	started := make(chan struct{})
	release := make(chan struct{})
	ctx1, cancel1 := context.WithCancel(context.Background())

	err1Ch := make(chan error, 1)
	go func() {
		_, err := store.resolveGatewayCachedWith(ctx1, gw.ID, func(queryCtx context.Context, _ string, _ time.Time) (adapter.Gateway, time.Time, error) {
			close(started)
			<-release
			if queryCtx.Err() != nil {
				return adapter.Gateway{}, time.Time{}, queryCtx.Err()
			}
			return gw, now.Add(time.Minute), nil
		})
		err1Ch <- err
	}()

	<-started
	res2Ch := make(chan adapter.Gateway, 1)
	err2Ch := make(chan error, 1)
	go func() {
		got, err := store.resolveGatewayCachedWith(context.Background(), gw.ID, func(context.Context, string, time.Time) (adapter.Gateway, time.Time, error) {
			return adapter.Gateway{}, time.Time{}, errors.New("should coalesce onto caller 1 flight")
		})
		res2Ch <- got
		err2Ch <- err
	}()

	time.Sleep(15 * time.Millisecond)
	cancel1()
	if err := <-err1Ch; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller 1 err=%v, want context.Canceled", err)
	}

	close(release)
	if err := <-err2Ch; err != nil {
		t.Fatalf("caller 2 failed after caller 1 canceled: %v", err)
	}
	if got := <-res2Ch; got != gw {
		t.Fatalf("caller 2 gateway=%+v, want %+v", got, gw)
	}
}

func TestQuarantineBurstCooldownAndBoundedCache(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	gw := "0194f000-2000-7000-8000-000000000010"
	tenant := "0194f000-0000-7000-8000-000000000001"
	topic := "hvac/v1/" + gw + "/up/telemetry"

	var writes []quarantinePersistInput
	failNext := false
	persist := func(_ context.Context, input quarantinePersistInput) error {
		if failNext {
			failNext = false
			return errors.New("db write error")
		}
		writes = append(writes, input)
		return nil
	}

	base := adapter.UplinkQuarantine{
		TenantID:   tenant,
		GatewayID:  gw,
		Topic:      topic,
		ReasonCode: adapter.QuarantineMessageInvalid,
		Detail:     "bad json",
		Payload:    []byte(`{"bad":true}`),
		ReceivedAt: now,
	}
	if err := store.quarantineUplinkWith(context.Background(), base, persist); err != nil {
		t.Fatalf("first quarantine hit failed: %v", err)
	}
	if len(writes) != 1 || writes[0].delta != 1 {
		t.Fatalf("expected 1 synchronous write with delta=1, got %+v", writes)
	}

	// Next 10 hits inside 5s cooldown must incur zero DB writes.
	for i := 1; i <= 10; i++ {
		msg := base
		msg.ReceivedAt = now.Add(time.Duration(i*400) * time.Millisecond)
		if err := store.quarantineUplinkWith(context.Background(), msg, persist); err != nil {
			t.Fatalf("burst hit %d failed: %v", i, err)
		}
	}
	if len(writes) != 1 {
		t.Fatalf("cooldown leaked DB writes: count=%d", len(writes))
	}

	// Flushing bursts pushes pendingDelta=10 in 1 write.
	if err := store.flushQuarantineBurstsWith(context.Background(), gw, persist); err != nil {
		t.Fatalf("flushQuarantineBurstsWith failed: %v", err)
	}
	if len(writes) != 2 || writes[1].delta != 10 {
		t.Fatalf("expected flush delta=10, got writes=%+v", writes)
	}

	// Buffer 3 more in cooldown, simulate failure after cooldown expires, verify pending preserved.
	for i := 1; i <= 3; i++ {
		msg := base
		msg.ReceivedAt = now.Add(4500 * time.Millisecond)
		if err := store.quarantineUplinkWith(context.Background(), msg, persist); err != nil {
			t.Fatalf("second burst hit failed: %v", err)
		}
	}
	failNext = true
	postCooldown := base
	postCooldown.ReceivedAt = now.Add(6 * time.Second)
	if err := store.quarantineUplinkWith(context.Background(), postCooldown, persist); err == nil {
		t.Fatal("expected error from failing persist")
	}
	retry := base
	retry.ReceivedAt = now.Add(6 * time.Second)
	if err := store.quarantineUplinkWith(context.Background(), retry, persist); err != nil {
		t.Fatalf("retry after failed persist failed: %v", err)
	}
	if len(writes) != 3 || writes[2].delta != 4 {
		t.Fatalf("expected retry to carry 3 buffered + 1 retry = 4, got writes=%+v", writes)
	}

	// Verify bounded map cap of 1024 entries.
	for i := 0; i < quarantineCooldownCap+200; i++ {
		entry := base
		entry.Topic = fmt.Sprintf("hvac/v1/%s/up/telemetry/%d", gw, i)
		entry.ReceivedAt = now.Add(time.Duration(i) * time.Millisecond)
		if err := store.quarantineUplinkWith(context.Background(), entry, persist); err != nil {
			t.Fatalf("bounded cap hit %d failed: %v", i, err)
		}
	}
	store.cache.quarantine.mu.Lock()
	mapSize := len(store.cache.quarantine.entries)
	store.cache.quarantine.mu.Unlock()
	if mapSize > quarantineCooldownCap {
		t.Fatalf("quarantine cooldown map size=%d exceeds cap=%d", mapSize, quarantineCooldownCap)
	}
}
