package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestGatewayRateLimiterBucketRefillAndQuarantineCooldown(t *testing.T) {
	t.Parallel()

	limiter := NewGatewayRateLimiter(2, 3) // 2 msg/s, burst 3
	t0 := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if !limiter.Allow(testGatewayA, t0) {
			t.Fatalf("expected burst token %d to pass", i+1)
		}
	}
	if limiter.Allow(testGatewayA, t0) {
		t.Fatal("expected 4th immediate message to be rate limited")
	}
	// Separate gateway has its own independent bucket.
	if !limiter.Allow(testGatewayB, t0) {
		t.Fatal("gateway A limit must not starve gateway B")
	}

	// Refill 1 token after 500ms.
	if !limiter.Allow(testGatewayA, t0.Add(500*time.Millisecond)) {
		t.Fatal("expected token refill after 500ms")
	}
	if limiter.Allow(testGatewayA, t0.Add(500*time.Millisecond)) {
		t.Fatal("expected bucket exhausted again after single refilled token")
	}

	// Quarantine cooldown: 5s per (gatewayID, messageType).
	if !limiter.ShouldQuarantine(testGatewayA, MessageTypeTelemetry, t0) {
		t.Fatal("expected initial quarantine write to be allowed")
	}
	if limiter.ShouldQuarantine(testGatewayA, MessageTypeTelemetry, t0.Add(4900*time.Millisecond)) {
		t.Fatal("expected repeated telemetry quarantine within 5s to be suppressed")
	}
	if !limiter.ShouldQuarantine(testGatewayA, MessageTypeEvent, t0.Add(1*time.Second)) {
		t.Fatal("expected independent cooldown for different messageType (event)")
	}
	if !limiter.ShouldQuarantine(testGatewayA, MessageTypeTelemetry, t0.Add(5*time.Second)) {
		t.Fatal("expected quarantine write to open again after 5s cooldown")
	}
}

func TestProcessorRateLimitPreDecodeQuarantineCooldownAndMetrics(t *testing.T) {
	t.Parallel()

	identities := newFakeIdentities()
	telemetry := &fakeTelemetry{}
	metrics := observability.NewRegistry()

	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	processor, err := NewProcessor(
		identities,
		telemetry,
		WithGatewayRateLimit(1, 2),
		WithProcessorMetrics(metrics),
	)
	if err != nil {
		t.Fatal(err)
	}
	processor.now = func() time.Time { return now }

	// Consume 2 burst tokens with valid telemetry.
	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), telemetryPayload(testGatewayA, messageID(i+1), i+1, "METER-01", "active_power")); err != nil {
			t.Fatalf("burst message %d failed: %v", i+1, err)
		}
	}

	// Send 10 flood messages (even malformed garbage JSON!) within 5s window.
	// Enforced BEFORE decode: first throttled message writes RATE_LIMITED quarantine, remaining 9 do NOT amplify DB writes!
	for i := 0; i < 10; i++ {
		_, procErr := processor.Process(context.Background(), telemetryTopic(testGatewayA), []byte(`{{{invalid-json-not-decoded`))
		if procErr == nil {
			t.Fatalf("flood message %d expected permanent rate-limit drop error", i)
		}
	}

	// Same burst exhaust -> send 3 event messages: quarantine allowed once for message_type="event".
	for i := 0; i < 3; i++ {
		_, procErr := processor.Process(context.Background(), "hvac/v1/"+testGatewayA+"/up/event", []byte(`{not-decoded}`))
		if procErr == nil {
			t.Fatalf("event flood %d expected rate-limit error", i)
		}
	}

	// Gateway B remains unaffected.
	if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayB), telemetryPayload(testGatewayB, messageID(50), 1, "METER-01", "active_power")); err != nil {
		t.Fatalf("expected gateway B to succeed while gateway A is rate limited: %v", err)
	}

	// Advance clock by 5s -> rate limit stays exhausted unless time triggers token > 1 (at 1/sec, +5s grants 5 -> consume 2 burst? Actually after 5s, refill grants max(burst=2) tokens; exhaust them again and verify quarantine writes again!).
	now = now.Add(5 * time.Second)
	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), telemetryPayload(testGatewayA, messageID(60+i), 60+i, "METER-01", "active_power")); err != nil {
			t.Fatalf("post-refill message %d failed: %v", i, err)
		}
	}
	if _, err := processor.Process(context.Background(), telemetryTopic(testGatewayA), []byte(`{flood-after-5s}`)); err == nil {
		t.Fatal("expected rate-limit drop after exhausting post-5s burst")
	}

	identities.mu.Lock()
	quarantines := append([]UplinkQuarantine(nil), identities.quarantines...)
	identities.mu.Unlock()

	if len(quarantines) != 3 {
		t.Fatalf("expected 3 throttled quarantine writes (telemetry#1, event#1, telemetry after 5s), got %d: %+v", len(quarantines), quarantines)
	}
	for idx, q := range quarantines {
		if q.ReasonCode != QuarantineReasonRateLimited {
			t.Fatalf("quarantine[%d] reason=%q, want %s (must enforce pre-decode)", idx, q.ReasonCode, QuarantineReasonRateLimited)
		}
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := rec.Body.String()
	if !strings.Contains(text, `hvac_mqtt_gateway_rate_limited_total{message_type="telemetry"} 11`) {
		t.Fatalf("expected telemetry rate limit metric count 11, got:\n%s", text)
	}
	if !strings.Contains(text, `hvac_mqtt_gateway_rate_limited_total{message_type="event"} 3`) {
		t.Fatalf("expected event rate limit metric count 3, got:\n%s", text)
	}
}
