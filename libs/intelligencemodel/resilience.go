package intelligencemodel

import (
	"context"
	"errors"
	"sync"
	"time"
)

type BreakerState string

const (
	BreakerClosed   BreakerState = "CLOSED"
	BreakerOpen     BreakerState = "OPEN"
	BreakerHalfOpen BreakerState = "HALF_OPEN"
)

const (
	DefaultBreakerFailureThreshold  = 5
	DefaultBreakerCooldown          = 30 * time.Second
	DefaultBreakerHalfOpenMaxProbes = 1
	DefaultInferenceCallTimeout     = 3 * time.Second
	DefaultBulkheadMaxConcurrent    = 8

	FallbackReasonCircuitOpen   = "circuit_open"
	FallbackReasonBulkheadFull  = "bulkhead_full"
	FallbackReasonTimeout       = "timeout"
	FallbackReasonUpstreamError = "upstream_error"
)

var (
	ErrCircuitOpen  = errors.New("circuit breaker is open")
	ErrBulkheadFull = errors.New("bulkhead concurrency limit reached")
)

type CircuitBreakerConfig struct {
	FailureThreshold  int
	Cooldown          time.Duration
	HalfOpenMaxProbes int
	Now               func() time.Time
}

// CircuitBreaker implements a thread-safe 3-state circuit breaker (CLOSED, OPEN, HALF_OPEN).
type CircuitBreaker struct {
	FailureThreshold  int
	Cooldown          time.Duration
	HalfOpenMaxProbes int
	Now               func() time.Time

	mu                  sync.Mutex
	state               BreakerState
	consecutiveFailures int
	openedAt            time.Time
	halfOpenInFlight    int
	halfOpenSuccesses   int
	generation          uint64
}

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = DefaultBreakerFailureThreshold
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = DefaultBreakerCooldown
	}
	if cfg.HalfOpenMaxProbes <= 0 {
		cfg.HalfOpenMaxProbes = DefaultBreakerHalfOpenMaxProbes
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &CircuitBreaker{
		FailureThreshold:  cfg.FailureThreshold,
		Cooldown:          cfg.Cooldown,
		HalfOpenMaxProbes: cfg.HalfOpenMaxProbes,
		Now:               cfg.Now,
		state:             BreakerClosed,
	}
}

func (cb *CircuitBreaker) failureThreshold() int {
	if cb.FailureThreshold <= 0 {
		return DefaultBreakerFailureThreshold
	}
	return cb.FailureThreshold
}

func (cb *CircuitBreaker) cooldown() time.Duration {
	if cb.Cooldown <= 0 {
		return DefaultBreakerCooldown
	}
	return cb.Cooldown
}

func (cb *CircuitBreaker) halfOpenMaxProbes() int {
	if cb.HalfOpenMaxProbes <= 0 {
		return DefaultBreakerHalfOpenMaxProbes
	}
	return cb.HalfOpenMaxProbes
}

func (cb *CircuitBreaker) now() time.Time {
	if cb.Now == nil {
		return time.Now()
	}
	return cb.Now()
}

func (cb *CircuitBreaker) syncStateLocked(now time.Time) {
	if cb.state == "" {
		cb.state = BreakerClosed
	}
	if cb.state == BreakerOpen && !now.Before(cb.openedAt.Add(cb.cooldown())) {
		cb.state = BreakerHalfOpen
		cb.halfOpenInFlight = 0
		cb.halfOpenSuccesses = 0
		cb.generation++
	}
}

// State returns the current breaker state, advancing OPEN to HALF_OPEN once Cooldown elapses.
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.syncStateLocked(cb.now())
	return cb.state
}

// Allow reports whether a request may proceed through the breaker.
func (cb *CircuitBreaker) Allow() bool {
	allowed, _ := cb.allowProbe()
	return allowed
}

func (cb *CircuitBreaker) allowProbe() (bool, uint64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.syncStateLocked(cb.now())
	switch cb.state {
	case BreakerClosed:
		return true, cb.generation
	case BreakerOpen:
		return false, cb.generation
	case BreakerHalfOpen:
		if cb.halfOpenInFlight+cb.halfOpenSuccesses >= cb.halfOpenMaxProbes() {
			return false, cb.generation
		}
		cb.halfOpenInFlight++
		return true, cb.generation
	default:
		return false, cb.generation
	}
}

// releaseProbe returns an unused half-open probe slot when a downstream admission check
// (such as a full Bulkhead) rejects the request before any upstream call happens.
func (cb *CircuitBreaker) releaseProbe(gen uint64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.generation != gen {
		return
	}
	if cb.state == BreakerHalfOpen && cb.halfOpenInFlight > 0 {
		cb.halfOpenInFlight--
	}
}

// RecordSuccess records a successful upstream execution.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.recordSuccess(0, false)
}

func (cb *CircuitBreaker) recordSuccess(gen uint64, enforceGen bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if enforceGen && cb.generation != gen {
		return
	}
	switch cb.state {
	case BreakerClosed, "":
		cb.state = BreakerClosed
		cb.consecutiveFailures = 0
	case BreakerHalfOpen:
		if cb.halfOpenInFlight > 0 {
			cb.halfOpenInFlight--
		}
		cb.halfOpenSuccesses++
		if cb.halfOpenSuccesses >= cb.halfOpenMaxProbes() {
			cb.state = BreakerClosed
			cb.consecutiveFailures = 0
			cb.halfOpenInFlight = 0
			cb.halfOpenSuccesses = 0
			cb.generation++
		}
	case BreakerOpen:
		// Ignore late completions from calls started before the breaker opened.
	}
}

// RecordFailure records a failed or timed-out upstream execution.
func (cb *CircuitBreaker) RecordFailure() {
	cb.recordFailure(0, false)
}

func (cb *CircuitBreaker) recordFailure(gen uint64, enforceGen bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := cb.now()
	cb.syncStateLocked(now)
	if enforceGen && cb.generation != gen {
		return
	}
	switch cb.state {
	case BreakerClosed, "":
		cb.state = BreakerClosed
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.failureThreshold() {
			cb.state = BreakerOpen
			cb.openedAt = now
			cb.halfOpenInFlight = 0
			cb.halfOpenSuccesses = 0
			cb.generation++
		}
	case BreakerHalfOpen:
		cb.state = BreakerOpen
		cb.openedAt = now
		cb.consecutiveFailures = cb.failureThreshold()
		cb.halfOpenInFlight = 0
		cb.halfOpenSuccesses = 0
		cb.generation++
	case BreakerOpen:
		// Already open; keep original cooldown anchor.
	}
}

// Bulkhead bounds concurrent upstream calls via a non-blocking buffered channel semaphore.
type Bulkhead struct {
	sem chan struct{}
}

func NewBulkhead(maxConcurrent int) *Bulkhead {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Bulkhead{
		sem: make(chan struct{}, maxConcurrent),
	}
}

func (b *Bulkhead) TryAcquire() bool {
	if b == nil || b.sem == nil {
		return true
	}
	select {
	case b.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (b *Bulkhead) Release() {
	if b == nil || b.sem == nil {
		return
	}
	select {
	case <-b.sem:
	default:
	}
}

func (b *Bulkhead) Capacity() int {
	if b == nil || b.sem == nil {
		return 0
	}
	return cap(b.sem)
}

func (b *Bulkhead) InUse() int {
	if b == nil || b.sem == nil {
		return 0
	}
	return len(b.sem)
}

type InvocationFallbackStatus struct {
	FallbackUsed bool   `json:"fallbackUsed"`
	Reason       string `json:"reason,omitempty"`
}

type ResilientInvokeConfig struct {
	Timeout  time.Duration
	Breaker  *CircuitBreaker
	Bulkhead *Bulkhead
}

func DefaultResilientInvokeConfig() ResilientInvokeConfig {
	return ResilientInvokeConfig{
		Timeout:  DefaultInferenceCallTimeout,
		Breaker:  NewCircuitBreaker(CircuitBreakerConfig{}),
		Bulkhead: NewBulkhead(DefaultBulkheadMaxConcurrent),
	}
}

// InvokeWithBumplessFallback executes call guarded by optional CircuitBreaker, Bulkhead,
// and Timeout (defaulting to DefaultInferenceCallTimeout). On failure or admission rejection,
// fallback runs with the original parent context and returns a bumpless replacement value
// along with InvocationFallbackStatus.
func InvokeWithBumplessFallback[T any](
	ctx context.Context,
	cfg ResilientInvokeConfig,
	call func(context.Context) (T, error),
	fallback func(context.Context, error) (T, error),
) (T, InvocationFallbackStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if call == nil {
		var zero T
		return zero, InvocationFallbackStatus{}, errors.New("resilient invoke call function is required")
	}

	res, reason, err := executeResilient(ctx, cfg, call)
	if err == nil {
		return res, InvocationFallbackStatus{FallbackUsed: false}, nil
	}
	if fallback == nil {
		var zero T
		return zero, InvocationFallbackStatus{FallbackUsed: false, Reason: reason}, err
	}

	fbRes, fbErr := fallback(ctx, err)
	if fbErr != nil {
		return fbRes, InvocationFallbackStatus{FallbackUsed: false, Reason: reason}, fbErr
	}
	return fbRes, InvocationFallbackStatus{FallbackUsed: true, Reason: reason}, nil
}

func executeResilient[T any](
	ctx context.Context,
	cfg ResilientInvokeConfig,
	call func(context.Context) (T, error),
) (T, string, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}

	var probeGen uint64
	if cfg.Breaker != nil {
		allowed, gen := cfg.Breaker.allowProbe()
		if !allowed {
			return zero, FallbackReasonCircuitOpen, ErrCircuitOpen
		}
		probeGen = gen
	}

	var releaseOnce sync.Once
	releaseBulkhead := func() {
		if cfg.Bulkhead != nil {
			releaseOnce.Do(cfg.Bulkhead.Release)
		}
	}
	if cfg.Bulkhead != nil {
		if !cfg.Bulkhead.TryAcquire() {
			if cfg.Breaker != nil {
				cfg.Breaker.releaseProbe(probeGen)
			}
			return zero, FallbackReasonBulkheadFull, ErrBulkheadFull
		}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultInferenceCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, panicked, panicVal, err := runWithContextTimeout(callCtx, call, releaseBulkhead, func() {
		if cfg.Breaker != nil {
			cfg.Breaker.recordFailure(probeGen, true)
		}
	})
	if panicked {
		panic(panicVal)
	}
	if err != nil {
		if cfg.Breaker != nil {
			cfg.Breaker.recordFailure(probeGen, true)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return zero, FallbackReasonTimeout, err
		}
		return zero, FallbackReasonUpstreamError, err
	}

	if cfg.Breaker != nil {
		cfg.Breaker.recordSuccess(probeGen, true)
	}
	return res, "", nil
}

type callOutcome[T any] struct {
	val      T
	err      error
	panicked bool
	panicVal any
}

func runWithContextTimeout[T any](
	ctx context.Context,
	call func(context.Context) (T, error),
	onFinish func(),
	onPanic func(),
) (T, bool, any, error) {
	if err := ctx.Err(); err != nil {
		if onFinish != nil {
			onFinish()
		}
		var zero T
		return zero, false, nil, err
	}
	done := make(chan callOutcome[T], 1)
	go func() {
		if onFinish != nil {
			defer onFinish()
		}
		defer func() {
			if r := recover(); r != nil {
				if onPanic != nil {
					onPanic()
				}
				var zero T
				done <- callOutcome[T]{val: zero, panicked: true, panicVal: r}
			}
		}()
		val, err := call(ctx)
		done <- callOutcome[T]{val: val, err: err}
	}()

	select {
	case out := <-done:
		if out.panicked {
			var zero T
			return zero, true, out.panicVal, nil
		}
		return out.val, false, nil, out.err
	case <-ctx.Done():
		var zero T
		return zero, false, nil, ctx.Err()
	}
}
