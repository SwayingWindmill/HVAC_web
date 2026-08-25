package limitpolicy

import (
	"context"
	"sync/atomic"
	"time"
)

// Limiter applies a versioned Policy against a Counter. The policy can be
// replaced atomically at runtime; each Allow reads the current version.
type Limiter struct {
	counter Counter
	policy  atomic.Pointer[Policy]
	now     func() time.Time
}

func NewLimiter(counter Counter, policy *Policy) *Limiter {
	limiter := &Limiter{counter: counter, now: time.Now}
	limiter.SetPolicy(policy)
	return limiter
}

// SetPolicy atomically replaces the active policy. A nil policy disables all
// limiting.
func (limiter *Limiter) SetPolicy(policy *Policy) {
	if limiter == nil {
		return
	}
	limiter.policy.Store(policy)
}

// Allow checks dimension for the given scope (tenant id, device id, session id,
// etc.). scope and dimension together form the counter key.
func (limiter *Limiter) Allow(ctx context.Context, dimension Dimension, scope string) Decision {
	policy := limiter.policy.Load()
	if policy == nil {
		return Decision{Allowed: true, Reason: "no-policy"}
	}
	limit, ok := policy.limitFor(dimension)
	if !ok {
		return Decision{Allowed: true, Reason: "unlimited"}
	}
	allowed, err := limiter.counter.Allow(ctx, string(dimension)+":"+scope, limit.Window, limit.Burst)
	if err != nil {
		if limit.FailClosed {
			return Decision{Allowed: false, Reason: "counter-unavailable"}
		}
		return Decision{Allowed: true, Reason: "counter-degraded"}
	}
	if !allowed {
		return Decision{Allowed: false, Reason: "limited", RetryAfter: limit.Window}
	}
	return Decision{Allowed: true, Reason: "allowed"}
}
