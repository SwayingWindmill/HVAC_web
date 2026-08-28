package limitpolicy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCounter is a shared Counter backend backed by Redis. It keeps the same
// token-bucket semantics as MemoryCounter, so a deployment can switch backends
// without changing rate-limit behaviour. Use it once more than one instance
// serves traffic.
type RedisCounter struct {
	client    *redis.Client
	keyPrefix string
}

func NewRedisCounter(client *redis.Client, keyPrefix string) *RedisCounter {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix == "" {
		prefix = "hvac:limit"
	}
	return &RedisCounter{client: client, keyPrefix: strings.TrimSuffix(prefix, ":")}
}

// tokenBucketScript atomically refills and consumes one token. KEYS[1] holds
// the token count, KEYS[2] the last-refill timestamp. Both expire after 2x the
// window so idle buckets do not accumulate.
var tokenBucketScript = redis.NewScript(`
local tokens = tonumber(redis.call('GET', KEYS[1]))
local burst = tonumber(ARGV[2])
if tokens == nil then tokens = burst end
local last = tonumber(redis.call('GET', KEYS[2]))
local now = tonumber(ARGV[3])
if last == nil then last = now end
local elapsed = (now - last) / 1000
if elapsed < 0 then elapsed = 0 end
local refilled = math.min(burst, tokens + elapsed * tonumber(ARGV[1]))
local ttl = tonumber(ARGV[4])
if refilled >= 1 then
  redis.call('SET', KEYS[1], refilled - 1, 'PX', ttl)
  redis.call('SET', KEYS[2], now, 'PX', ttl)
  return 1
end
redis.call('SET', KEYS[1], refilled, 'PX', ttl)
redis.call('SET', KEYS[2], now, 'PX', ttl)
return 0
`)

func (counter *RedisCounter) Allow(ctx context.Context, key string, window time.Duration, burst int) (bool, error) {
	if counter == nil || counter.client == nil {
		return false, fmt.Errorf("rate limit Redis backend is unavailable")
	}
	if window <= 0 || burst <= 0 {
		return false, nil
	}
	rate := float64(burst) / window.Seconds()
	ttl := 2 * window.Milliseconds()
	result, err := tokenBucketScript.Run(ctx, counter.client, []string{
		counter.keyPrefix + ":" + key,
		counter.keyPrefix + ":" + key + ":last",
	}, rate, burst, time.Now().UnixMilli(), ttl).Int()
	if err != nil {
		return false, fmt.Errorf("reserve rate limit bucket: %w", err)
	}
	return result == 1, nil
}
