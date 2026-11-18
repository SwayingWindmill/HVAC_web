package intelligencemodel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCircuitBreakerTransitionsAndHalfOpenLimits(t *testing.T) {
	clk := newTestClock(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(CircuitBreakerConfig{Now: clk.Now})

	if cb.FailureThreshold != 5 || cb.Cooldown != 30*time.Second || cb.HalfOpenMaxProbes != 1 {
		t.Fatalf("unexpected defaults: %+v", cb)
	}
	if cb.State() != BreakerClosed {
		t.Fatalf("initial state=%s, want CLOSED", cb.State())
	}

	// Four failures followed by a success resets the consecutive failure counter.
	for i := 0; i < 4; i++ {
		if !cb.Allow() {
			t.Fatalf("expected Allow=true on failure %d", i)
		}
		cb.RecordFailure()
	}
	if cb.State() != BreakerClosed {
		t.Fatalf("state after 4 failures=%s, want CLOSED", cb.State())
	}
	cb.RecordSuccess()

	// Five consecutive failures trip CLOSED -> OPEN.
	for i := 0; i < 5; i++ {
		if !cb.Allow() {
			t.Fatalf("expected Allow=true before threshold %d", i)
		}
		cb.RecordFailure()
	}
	if cb.State() != BreakerOpen {
		t.Fatalf("state after 5 failures=%s, want OPEN", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow=false while OPEN before cooldown")
	}

	// Just before 30s cooldown, breaker stays OPEN.
	clk.Advance(29 * time.Second)
	if cb.State() != BreakerOpen || cb.Allow() {
		t.Fatalf("expected OPEN at 29s, got %s", cb.State())
	}

	// At 30s cooldown, breaker enters HALF_OPEN and permits only 1 in-flight probe.
	clk.Advance(time.Second)
	if cb.State() != BreakerHalfOpen {
		t.Fatalf("state at 30s=%s, want HALF_OPEN", cb.State())
	}
	allowed1, gen1 := cb.allowProbe()
	if !allowed1 {
		t.Fatal("expected first HALF_OPEN probe to be allowed")
	}
	allowed2, _ := cb.allowProbe()
	if allowed2 {
		t.Fatal("expected second concurrent HALF_OPEN probe to be blocked")
	}

	// Failed probe trips HALF_OPEN -> OPEN and resets the 30s cooldown anchor.
	cb.recordFailure(gen1, true)
	if cb.State() != BreakerOpen {
		t.Fatalf("state after failed probe=%s, want OPEN", cb.State())
	}
	clk.Advance(29 * time.Second)
	if cb.State() != BreakerOpen {
		t.Fatalf("expected OPEN 29s after re-open, got %s", cb.State())
	}

	// Next cooldown advances to HALF_OPEN, and probe success transitions to CLOSED.
	clk.Advance(time.Second)
	allowed3, gen3 := cb.allowProbe()
	if !allowed3 || cb.State() != BreakerHalfOpen {
		t.Fatalf("expected HALF_OPEN probe allowed, state=%s", cb.State())
	}
	cb.recordSuccess(gen3, true)
	if cb.State() != BreakerClosed {
		t.Fatalf("state after probe success=%s, want CLOSED", cb.State())
	}
}

func TestCircuitBreakerMultiProbeHalfOpen(t *testing.T) {
	clk := newTestClock(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:  2,
		Cooldown:          10 * time.Second,
		HalfOpenMaxProbes: 2,
		Now:               clk.Now,
	})

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != BreakerOpen {
		t.Fatalf("state=%s, want OPEN", cb.State())
	}
	clk.Advance(10 * time.Second)

	ok1, g1 := cb.allowProbe()
	ok2, g2 := cb.allowProbe()
	ok3, _ := cb.allowProbe()
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("expected exactly 2 probes allowed, got ok1=%v ok2=%v ok3=%v", ok1, ok2, ok3)
	}

	cb.recordSuccess(g1, true)
	if cb.State() != BreakerHalfOpen {
		t.Fatalf("state after 1/2 successes=%s, want HALF_OPEN", cb.State())
	}
	cb.recordSuccess(g2, true)
	if cb.State() != BreakerClosed {
		t.Fatalf("state after 2/2 successes=%s, want CLOSED", cb.State())
	}
}

func TestCircuitBreakerConcurrentAccess(t *testing.T) {
	clk := newTestClock(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:  3,
		Cooldown:          5 * time.Millisecond,
		HalfOpenMaxProbes: 1,
		Now:               clk.Now,
	})

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if ok, gen := cb.allowProbe(); ok {
					if (id+i)%3 == 0 {
						cb.recordFailure(gen, true)
					} else {
						cb.recordSuccess(gen, true)
					}
				}
				_ = cb.State()
				if i%10 == 0 {
					clk.Advance(2 * time.Millisecond)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestBulkheadAcquireRelease(t *testing.T) {
	bh := NewBulkhead(2)
	if bh.Capacity() != 2 || bh.InUse() != 0 {
		t.Fatalf("unexpected initial bulkhead cap=%d inUse=%d", bh.Capacity(), bh.InUse())
	}
	if !bh.TryAcquire() {
		t.Fatal("expected 1st acquire to succeed")
	}
	if !bh.TryAcquire() {
		t.Fatal("expected 2nd acquire to succeed")
	}
	if bh.TryAcquire() {
		t.Fatal("expected 3rd acquire to fail when full")
	}
	bh.Release()
	if !bh.TryAcquire() {
		t.Fatal("expected acquire after release to succeed")
	}
	bh.Release()
	bh.Release()
	bh.Release() // extra release must not block
	if bh.InUse() != 0 {
		t.Fatalf("inUse=%d, want 0", bh.InUse())
	}

	zeroBh := NewBulkhead(0)
	if zeroBh.Capacity() != 1 {
		t.Fatalf("non-positive bulkhead should default to capacity 1, got %d", zeroBh.Capacity())
	}
}

func TestInvokeWithBumplessFallback(t *testing.T) {
	if DefaultInferenceCallTimeout != 3*time.Second {
		t.Fatalf("DefaultInferenceCallTimeout=%s, want 3s", DefaultInferenceCallTimeout)
	}

	type tc struct {
		name             string
		setup            func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error))
		fallback         func(context.Context, error) (string, error)
		wantVal          string
		wantFallbackUsed bool
		wantReason       string
		wantErr          bool
		assertAfter      func(t *testing.T, cfg ResilientInvokeConfig, calls, fbCalls int32)
	}

	cases := []tc{
		{
			name: "primary succeeds without calling fallback",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				cfg := ResilientInvokeConfig{
					Timeout:  100 * time.Millisecond,
					Breaker:  NewCircuitBreaker(CircuitBreakerConfig{}),
					Bulkhead: NewBulkhead(2),
				}
				return cfg, &calls, &fbCalls, func(context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "ai-setpoint:7.8", nil
				}
			},
			fallback: func(context.Context, error) (string, error) {
				return "fallback", nil
			},
			wantVal:          "ai-setpoint:7.8",
			wantFallbackUsed: false,
			wantReason:       "",
			assertAfter: func(t *testing.T, cfg ResilientInvokeConfig, calls, _ int32) {
				if calls != 1 || cfg.Bulkhead.InUse() != 0 || cfg.Breaker.State() != BreakerClosed {
					t.Fatalf("calls=%d inUse=%d state=%s", calls, cfg.Bulkhead.InUse(), cfg.Breaker.State())
				}
			},
		},
		{
			name: "circuit_open short-circuits call and runs fallback",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				clk := newTestClock(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
				cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: 30 * time.Second, Now: clk.Now})
				cb.RecordFailure()
				return ResilientInvokeConfig{Breaker: cb}, &calls, &fbCalls, func(context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "unexpected", nil
				}
			},
			fallback: func(_ context.Context, err error) (string, error) {
				if !errors.Is(err, ErrCircuitOpen) {
					return "", err
				}
				return "rule-fallback:7.0", nil
			},
			wantVal:          "rule-fallback:7.0",
			wantFallbackUsed: true,
			wantReason:       FallbackReasonCircuitOpen,
			assertAfter: func(t *testing.T, _ ResilientInvokeConfig, calls, _ int32) {
				if calls != 0 {
					t.Fatalf("primary call must not execute when circuit is open, calls=%d", calls)
				}
			},
		},
		{
			name: "bulkhead_full short-circuits call and releases half-open slot without tripping breaker",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				clk := newTestClock(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
				cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: 10 * time.Second, Now: clk.Now})
				cb.RecordFailure()
				clk.Advance(10 * time.Second) // now HALF_OPEN
				bh := NewBulkhead(1)
				if !bh.TryAcquire() {
					t.Fatal("pre-acquire failed")
				}
				return ResilientInvokeConfig{Breaker: cb, Bulkhead: bh}, &calls, &fbCalls, func(context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "unexpected", nil
				}
			},
			fallback: func(_ context.Context, err error) (string, error) {
				if !errors.Is(err, ErrBulkheadFull) {
					return "", err
				}
				return "rule-fallback:bulkhead", nil
			},
			wantVal:          "rule-fallback:bulkhead",
			wantFallbackUsed: true,
			wantReason:       FallbackReasonBulkheadFull,
			assertAfter: func(t *testing.T, cfg ResilientInvokeConfig, calls, _ int32) {
				defer cfg.Bulkhead.Release()
				if calls != 0 {
					t.Fatalf("calls=%d, want 0", calls)
				}
				if cfg.Breaker.State() != BreakerHalfOpen || !cfg.Breaker.Allow() {
					t.Fatalf("half-open slot must remain available after bulkhead rejection, state=%s", cfg.Breaker.State())
				}
			},
		},
		{
			name: "timeout triggers fallback with non-expired parent context and records breaker failure",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1})
				bh := NewBulkhead(1)
				return ResilientInvokeConfig{Timeout: 20 * time.Millisecond, Breaker: cb, Bulkhead: bh}, &calls, &fbCalls, func(ctx context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					time.Sleep(80 * time.Millisecond)
					return "late", nil
				}
			},
			fallback: func(ctx context.Context, err error) (string, error) {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					return "", err
				}
				return "rule-fallback:timeout", nil
			},
			wantVal:          "rule-fallback:timeout",
			wantFallbackUsed: true,
			wantReason:       FallbackReasonTimeout,
			assertAfter: func(t *testing.T, cfg ResilientInvokeConfig, calls, _ int32) {
				if calls != 1 {
					t.Fatalf("calls=%d, want 1", calls)
				}
				if cfg.Breaker.State() != BreakerOpen {
					t.Fatalf("breaker state=%s, want OPEN after timeout failure", cfg.Breaker.State())
				}
				// Timed-out goroutine finishes sleeping at ~80ms; bulkhead must stay held until it exits, then release to 0.
				deadline := time.Now().Add(300 * time.Millisecond)
				for cfg.Bulkhead.InUse() != 0 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if cfg.Bulkhead.InUse() != 0 {
					t.Fatalf("bulkhead inUse=%d, want 0 after timed-out worker exits", cfg.Bulkhead.InUse())
				}
			},
		},
		{
			name: "upstream_error triggers fallback and trips breaker at threshold",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1})
				return ResilientInvokeConfig{Breaker: cb}, &calls, &fbCalls, func(context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "", errors.New("502 bad gateway")
				}
			},
			fallback: func(context.Context, error) (string, error) {
				return "rule-fallback:upstream", nil
			},
			wantVal:          "rule-fallback:upstream",
			wantFallbackUsed: true,
			wantReason:       FallbackReasonUpstreamError,
			assertAfter: func(t *testing.T, cfg ResilientInvokeConfig, calls, _ int32) {
				if calls != 1 || cfg.Breaker.State() != BreakerOpen {
					t.Fatalf("calls=%d state=%s", calls, cfg.Breaker.State())
				}
			},
		},
		{
			name: "fallback failure preserves reason and marks FallbackUsed=false",
			setup: func(t *testing.T) (ResilientInvokeConfig, *int32, *int32, func(context.Context) (string, error)) {
				var calls, fbCalls int32
				return ResilientInvokeConfig{}, &calls, &fbCalls, func(context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "", errors.New("upstream down")
				}
			},
			fallback: func(context.Context, error) (string, error) {
				return "", errors.New("fallback also unavailable")
			},
			wantVal:          "",
			wantFallbackUsed: false,
			wantReason:       FallbackReasonUpstreamError,
			wantErr:          true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, calls, fbCalls, call := c.setup(t)
			got, status, err := InvokeWithBumplessFallback(t.Context(), cfg, call, c.fallback)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, c.wantErr)
			}
			if got != c.wantVal || status.FallbackUsed != c.wantFallbackUsed || status.Reason != c.wantReason {
				t.Fatalf("got=(%q, %+v), want=(%q, used=%v reason=%q)",
					got, status, c.wantVal, c.wantFallbackUsed, c.wantReason)
			}
			if c.assertAfter != nil {
				c.assertAfter(t, cfg, *calls, *fbCalls)
			}
		})
	}
}

func TestInvokeResilienceBulkheadTimeoutAndHalfOpenPanicRecovery(t *testing.T) {
	bh := NewBulkhead(1)
	releaseCh := make(chan struct{})
	startedCh := make(chan struct{})
	cfg := ResilientInvokeConfig{Timeout: 20 * time.Millisecond, Bulkhead: bh}

	// 1. Timed-out worker keeps bulkhead slot occupied until goroutine actually completes.
	timeoutErrDone := make(chan error, 1)
	go func() {
		_, _, err := InvokeWithBumplessFallback(t.Context(), cfg, func(ctx context.Context) (string, error) {
			close(startedCh)
			<-releaseCh
			return "late", nil
		}, nil)
		timeoutErrDone <- err
	}()
	<-startedCh
	if err := <-timeoutErrDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	// Concurrent caller must still hit ErrBulkheadFull while late worker runs.
	_, status, fullErr := InvokeWithBumplessFallback(t.Context(), cfg, func(context.Context) (string, error) {
		return "should-not-run", nil
	}, nil)
	if !errors.Is(fullErr, ErrBulkheadFull) || status.Reason != FallbackReasonBulkheadFull {
		t.Fatalf("expected ErrBulkheadFull while timed-out worker holds slot, got status=%+v err=%v", status, fullErr)
	}
	close(releaseCh)
	deadline := time.Now().Add(time.Second)
	for bh.InUse() != 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if bh.InUse() != 0 {
		t.Fatalf("expected bulkhead released after late worker exit, inUse=%d", bh.InUse())
	}

	// 2. Panic inside half-open probe must trip breaker rather than wedging in HALF_OPEN.
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold:  1,
		Cooldown:          10 * time.Second,
		HalfOpenMaxProbes: 1,
		Now:               func() time.Time { return now },
	})
	_, _, _ = InvokeWithBumplessFallback(t.Context(), ResilientInvokeConfig{Breaker: cb}, func(context.Context) (string, error) {
		return "", errors.New("initial trip")
	}, nil)
	now = now.Add(11 * time.Second)
	if cb.State() != BreakerHalfOpen {
		t.Fatalf("expected BreakerHalfOpen after cooldown, got %s", cb.State())
	}
	func() {
		defer func() { _ = recover() }()
		_, _, _ = InvokeWithBumplessFallback(t.Context(), ResilientInvokeConfig{Breaker: cb}, func(context.Context) (string, error) {
			panic("boom in probe")
		}, nil)
	}()
	if cb.State() != BreakerOpen {
		t.Fatalf("expected panicked probe to reopen breaker instead of wedging HALF_OPEN, got %s", cb.State())
	}
	now = now.Add(11 * time.Second)
	val, _, err := InvokeWithBumplessFallback(t.Context(), ResilientInvokeConfig{Breaker: cb}, func(context.Context) (string, error) {
		return "recovered", nil
	}, nil)
	if err != nil || val != "recovered" || cb.State() != BreakerClosed {
		t.Fatalf("expected recovery after reopened breaker cooldown, got val=%q err=%v state=%s", val, err, cb.State())
	}
}
