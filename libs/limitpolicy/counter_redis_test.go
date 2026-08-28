package limitpolicy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func openTestRedisCounter(t *testing.T) *RedisCounter {
	t.Helper()
	redisURL := os.Getenv("HVAC_LIMIT_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("HVAC_LIMIT_REDIS_TEST_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse test Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisCounter(client, "hvac:test:limit:"+time.Now().UTC().Format("20060102T150405.000000000"))
}

func TestRedisCounterEnforcesBurst(t *testing.T) {
	counter := openTestRedisCounter(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		allowed, err := counter.Allow(ctx, "tenant-a", time.Minute, 3)
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed=%t err=%v", index, allowed, err)
		}
	}
	allowed, err := counter.Allow(ctx, "tenant-a", time.Minute, 3)
	if err != nil || allowed {
		t.Fatalf("4th request: allowed=%t err=%v", allowed, err)
	}
}

func TestRedisCounterScopesAreIndependent(t *testing.T) {
	counter := openTestRedisCounter(t)
	ctx := context.Background()
	if _, err := counter.Allow(ctx, "tenant-a", time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	allowed, err := counter.Allow(ctx, "tenant-b", time.Minute, 1)
	if err != nil || !allowed {
		t.Fatalf("independent scope: allowed=%t err=%v", allowed, err)
	}
}
