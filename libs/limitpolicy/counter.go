package limitpolicy

import (
	"context"
	"math"
	"sync"
	"time"
)

// Counter is a swappable rate-limit backend. A non-nil error means the backend
// could not be evaluated; the Limiter turns that into a fail-closed or
// fail-open decision per the Limit.
type Counter interface {
	Allow(ctx context.Context, key string, window time.Duration, burst int) (bool, error)
}

type tokenBucket struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(window time.Duration, burst int, now time.Time) *tokenBucket {
	return &tokenBucket{
		rate:   float64(burst) / window.Seconds(),
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

func (bucket *tokenBucket) allow(now time.Time) bool {
	if elapsed := now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = math.Min(bucket.burst, bucket.tokens+elapsed*bucket.rate)
		bucket.last = now
	}
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}
	return false
}

// MemoryCounter is a bounded in-process Counter. It never grows past maxKeys:
// expired buckets are evicted first, then the least recently used active
// bucket. Suitable for a single-node deployment; swap in a shared backend
// before multi-instance.
type MemoryCounter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	maxKeys int
	now     func() time.Time
}

func NewMemoryCounter(maxKeys int) *MemoryCounter {
	if maxKeys < 1 {
		maxKeys = 1
	}
	return &MemoryCounter{
		buckets: make(map[string]*tokenBucket),
		maxKeys: maxKeys,
		now:     time.Now,
	}
}

func (counter *MemoryCounter) Allow(_ context.Context, key string, window time.Duration, burst int) (bool, error) {
	if window <= 0 || burst <= 0 {
		return false, nil
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	bucket, ok := counter.buckets[key]
	if !ok {
		if len(counter.buckets) >= counter.maxKeys {
			counter.evict(window)
		}
		bucket = newTokenBucket(window, burst, counter.now())
		counter.buckets[key] = bucket
	}
	return bucket.allow(counter.now()), nil
}

func (counter *MemoryCounter) evict(window time.Duration) {
	now := counter.now()
	for key, bucket := range counter.buckets {
		if now.Sub(bucket.last) > window {
			delete(counter.buckets, key)
		}
	}
	if len(counter.buckets) < counter.maxKeys {
		return
	}
	for len(counter.buckets) >= counter.maxKeys {
		var oldestKey string
		var oldest time.Time
		for key, bucket := range counter.buckets {
			if oldestKey == "" || bucket.last.Before(oldest) {
				oldestKey, oldest = key, bucket.last
			}
		}
		if oldestKey == "" {
			return
		}
		delete(counter.buckets, oldestKey)
	}
}
