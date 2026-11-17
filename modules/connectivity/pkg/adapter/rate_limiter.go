package adapter

import (
	"math"
	"strings"
	"sync"
	"time"
)

const (
	// QuarantineReasonRateLimited marks uplinks dropped by the per-gateway token-bucket rate limiter.
	QuarantineReasonRateLimited = "RATE_LIMITED"

	defaultQuarantineCooldown  = 5 * time.Second
	defaultRateLimiterGateways = 65536
)

type gatewayRateEntry struct {
	tokens         float64
	lastRefill     time.Time
	lastQuarantine map[string]time.Time
}

// GatewayRateLimiter enforces per-gateway token-bucket rate limiting and throttles
// quarantine persistence per (gatewayID, messageType) across a 5s cooldown window.
type GatewayRateLimiter struct {
	mu                 sync.Mutex
	rate               float64
	burst              int
	quarantineCooldown time.Duration
	gateways           boundedTouchMap[*gatewayRateEntry]
}

// NewGatewayRateLimiter creates a per-gateway token-bucket limiter allowing messagesPerSec
// steady throughput and up to burst tokens immediately. Passing messagesPerSec <= 0 disables
// rate limiting.
func NewGatewayRateLimiter(messagesPerSec float64, burst int) *GatewayRateLimiter {
	if messagesPerSec > 0 && burst <= 0 {
		burst = max(1, int(math.Ceil(messagesPerSec)))
	}
	return &GatewayRateLimiter{
		rate:               messagesPerSec,
		burst:              burst,
		quarantineCooldown: defaultQuarantineCooldown,
		gateways:           newBoundedTouchMap[*gatewayRateEntry](defaultRateLimiterGateways),
	}
}

// Enabled reports whether rate limiting is active.
func (l *GatewayRateLimiter) Enabled() bool {
	return l != nil && l.rate > 0 && l.burst > 0
}

// Allow attempts to consume one message token for gatewayID at time now.
func (l *GatewayRateLimiter) Allow(gatewayID string, now time.Time) bool {
	if !l.Enabled() {
		return true
	}
	gw := strings.TrimSpace(gatewayID)
	if gw == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.getOrCreateLocked(gw, now)
	if now.After(entry.lastRefill) {
		elapsed := now.Sub(entry.lastRefill).Seconds()
		entry.tokens = min(float64(l.burst), entry.tokens+elapsed*l.rate)
		entry.lastRefill = now
	}
	if entry.tokens < 1.0 {
		return false
	}
	entry.tokens -= 1.0
	return true
}

// ShouldQuarantine reports whether a rate-limited rejection for (gatewayID, messageType)
// should be written to quarantine evidence, enforcing a cooldown window (5s by default).
func (l *GatewayRateLimiter) ShouldQuarantine(gatewayID, messageType string, now time.Time) bool {
	if l == nil {
		return true
	}
	gw := strings.TrimSpace(gatewayID)
	msgType := normalizeRateLimitMessageType(messageType)

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.getOrCreateLocked(gw, now)
	if entry.lastQuarantine == nil {
		entry.lastQuarantine = make(map[string]time.Time, 4)
	}
	if prev, seen := entry.lastQuarantine[msgType]; seen && now.Sub(prev) < l.quarantineCooldown {
		return false
	}
	entry.lastQuarantine[msgType] = now
	return true
}

func (l *GatewayRateLimiter) getOrCreateLocked(gatewayID string, now time.Time) *gatewayRateEntry {
	return l.gateways.getOrCreate(gatewayID, func() *gatewayRateEntry {
		return &gatewayRateEntry{
			tokens:     float64(l.burst),
			lastRefill: now,
		}
	})
}

func normalizeRateLimitMessageType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case MessageTypeTelemetry, MessageTypeEvent, MessageTypeStatus:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "unknown"
	}
}
