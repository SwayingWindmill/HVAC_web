package limitpolicy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterEnforcesBurst(t *testing.T) {
	limiter := NewLimiter(NewMemoryCounter(100), &Policy{Version: 1, Limits: []Limit{
		{Dimension: DimensionRest, Window: time.Minute, Burst: 3},
	}})
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		if decision := limiter.Allow(ctx, DimensionRest, "tenant-1"); !decision.Allowed {
			t.Fatalf("request %d unexpectedly limited: %+v", index, decision)
		}
	}
	if decision := limiter.Allow(ctx, DimensionRest, "tenant-1"); decision.Allowed {
		t.Fatalf("4th request should be limited: %+v", decision)
	}
	if decision := limiter.Allow(ctx, DimensionRest, "tenant-2"); !decision.Allowed {
		t.Fatalf("different scope should be independent: %+v", decision)
	}
}

func TestLimiterRefillsAfterWindow(t *testing.T) {
	counter := NewMemoryCounter(100)
	var now time.Time
	counter.now = func() time.Time { return now }
	limiter := NewLimiter(counter, &Policy{Version: 1, Limits: []Limit{
		{Dimension: DimensionRest, Window: time.Minute, Burst: 1},
	}})
	ctx := context.Background()
	now = time.Unix(0, 0)
	if decision := limiter.Allow(ctx, DimensionRest, "t"); !decision.Allowed {
		t.Fatalf("first request should pass: %+v", decision)
	}
	if decision := limiter.Allow(ctx, DimensionRest, "t"); decision.Allowed {
		t.Fatalf("second request should be limited: %+v", decision)
	}
	now = time.Unix(60, 0)
	if decision := limiter.Allow(ctx, DimensionRest, "t"); !decision.Allowed {
		t.Fatalf("bucket should refill after window: %+v", decision)
	}
}

func TestLimiterUnconfiguredDimensionIsUnlimited(t *testing.T) {
	limiter := NewLimiter(NewMemoryCounter(10), &Policy{Version: 1})
	if decision := limiter.Allow(context.Background(), DimensionCommandWrite, "t"); !decision.Allowed || decision.Reason != "unlimited" {
		t.Fatalf("unconfigured dimension = %+v", decision)
	}
}

func TestLimiterNilPolicyAllows(t *testing.T) {
	limiter := NewLimiter(NewMemoryCounter(10), nil)
	if decision := limiter.Allow(context.Background(), DimensionRest, "t"); !decision.Allowed || decision.Reason != "no-policy" {
		t.Fatalf("nil policy = %+v", decision)
	}
}

type failingCounter struct{}

func (failingCounter) Allow(context.Context, string, time.Duration, int) (bool, error) {
	return false, errors.New("backend down")
}

func TestLimiterFailClosedOnCounterError(t *testing.T) {
	limiter := NewLimiter(failingCounter{}, &Policy{Version: 1, Limits: []Limit{
		{Dimension: DimensionCommandWrite, Window: time.Minute, Burst: 10, FailClosed: true},
	}})
	if decision := limiter.Allow(context.Background(), DimensionCommandWrite, "t"); decision.Allowed {
		t.Fatalf("fail-closed should reject on counter error: %+v", decision)
	}
}

func TestLimiterFailOpenOnCounterError(t *testing.T) {
	limiter := NewLimiter(failingCounter{}, &Policy{Version: 1, Limits: []Limit{
		{Dimension: DimensionRest, Window: time.Minute, Burst: 10},
	}})
	if decision := limiter.Allow(context.Background(), DimensionRest, "t"); !decision.Allowed || decision.Reason != "counter-degraded" {
		t.Fatalf("fail-open = %+v", decision)
	}
}

func TestMemoryCounterIsBounded(t *testing.T) {
	counter := NewMemoryCounter(2)
	ctx := context.Background()
	_, _ = counter.Allow(ctx, "a", time.Minute, 1)
	_, _ = counter.Allow(ctx, "b", time.Minute, 1)
	_, _ = counter.Allow(ctx, "c", time.Minute, 1)
	if len(counter.buckets) > 2 {
		t.Fatalf("counter grew to %d keys, want <= 2", len(counter.buckets))
	}
}
