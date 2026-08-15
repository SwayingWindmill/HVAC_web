package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
	"github.com/redis/go-redis/v9"
)

var (
	ErrLatestCacheMiss        = errors.New("telemetry latest cache miss")
	ErrLatestCacheUnavailable = errors.New("telemetry latest cache unavailable")
)

const defaultLatestCacheKeyPrefix = "hvac:v2:latest:device"

var redisLatestCAS = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], 'revision')
local incoming = ARGV[1]
if current then
  if string.len(current) > string.len(incoming) then
    return 0
  end
  if string.len(current) == string.len(incoming) and current >= incoming then
    return 0
  end
end
redis.call('HSET', KEYS[1],
  'revision', incoming,
  'tenant_id', ARGV[2],
  'site_id', ARGV[3],
  'device_id', ARGV[4],
  'evaluated_at', ARGV[5],
  'snapshot', ARGV[6])
return 1
`)

type LatestCache interface {
	PutIfNewer(context.Context, telemetryapi.DeviceObservationSnapshot) (bool, error)
	Get(context.Context, string, string, string) (telemetryapi.DeviceObservationSnapshot, error)
	Close() error
}

type RedisLatestCacheConfig struct {
	URL       string
	KeyPrefix string
}

type RedisLatestCache struct {
	client    *redis.Client
	keyPrefix string
}

func OpenRedisLatestCache(ctx context.Context, config RedisLatestCacheConfig) (*RedisLatestCache, error) {
	urlValue := strings.TrimSpace(config.URL)
	if urlValue == "" {
		return nil, errors.New("telemetry latest Redis URL is required")
	}
	options, err := redis.ParseURL(urlValue)
	if err != nil {
		return nil, fmt.Errorf("parse telemetry latest Redis URL: %w", err)
	}
	options.ClientName = "telemetry-runtime-latest-cache"
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping telemetry latest Redis: %w", err)
	}
	prefix := strings.TrimSpace(config.KeyPrefix)
	if prefix == "" {
		prefix = defaultLatestCacheKeyPrefix
	}
	return &RedisLatestCache{client: client, keyPrefix: strings.TrimSuffix(prefix, ":")}, nil
}

func (cache *RedisLatestCache) Close() error {
	if cache == nil || cache.client == nil {
		return nil
	}
	return cache.client.Close()
}

func (cache *RedisLatestCache) PutIfNewer(ctx context.Context, snapshot telemetryapi.DeviceObservationSnapshot) (bool, error) {
	if cache == nil || cache.client == nil {
		return false, ErrLatestCacheUnavailable
	}
	if err := validateLatestCacheSnapshot(snapshot); err != nil {
		return false, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return false, fmt.Errorf("encode telemetry latest snapshot: %w", err)
	}
	result, err := redisLatestCAS.Run(ctx, cache.client, []string{cache.key(snapshot.TenantId, snapshot.SiteId, snapshot.DeviceId)},
		fmt.Sprintf("%d", snapshot.BusinessRevision),
		string(snapshot.TenantId), string(snapshot.SiteId), string(snapshot.DeviceId),
		string(snapshot.EvaluatedAt), string(encoded),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("materialize telemetry latest snapshot: %w", err)
	}
	return result == 1, nil
}

func (cache *RedisLatestCache) Get(ctx context.Context, tenantID, siteID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	if cache == nil || cache.client == nil {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheUnavailable
	}
	fields, err := cache.client.HGetAll(ctx, cache.key(telemetryapi.UUIDv7(tenantID), telemetryapi.UUIDv7(siteID), telemetryapi.UUIDv7(deviceID))).Result()
	if err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fmt.Errorf("read telemetry latest snapshot: %w", err)
	}
	if len(fields) == 0 || fields["snapshot"] == "" {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	var snapshot telemetryapi.DeviceObservationSnapshot
	if err := json.Unmarshal([]byte(fields["snapshot"]), &snapshot); err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fmt.Errorf("decode telemetry latest snapshot: %w", err)
	}
	if err := validateLatestCacheSnapshot(snapshot); err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fmt.Errorf("validate telemetry latest snapshot: %w", err)
	}
	if string(snapshot.TenantId) != tenantID || string(snapshot.SiteId) != siteID || string(snapshot.DeviceId) != deviceID || fields["revision"] != fmt.Sprintf("%d", snapshot.BusinessRevision) {
		return telemetryapi.DeviceObservationSnapshot{}, errors.New("telemetry latest cache identity/revision mismatch")
	}
	return snapshot, nil
}

func (cache *RedisLatestCache) key(tenantID, siteID, deviceID telemetryapi.UUIDv7) string {
	return fmt.Sprintf("%s:%s:%s:%s", cache.keyPrefix, tenantID, siteID, deviceID)
}

func validateLatestCacheSnapshot(snapshot telemetryapi.DeviceObservationSnapshot) error {
	if snapshot.SchemaVersion != 1 || snapshot.BusinessRevision < 1 {
		return errors.New("telemetry latest snapshot schema/revision is invalid")
	}
	if !uuidV7Pattern.MatchString(string(snapshot.TenantId)) || !uuidV7Pattern.MatchString(string(snapshot.SiteId)) || !uuidV7Pattern.MatchString(string(snapshot.DeviceId)) {
		return errors.New("telemetry latest snapshot identity must be UUIDv7")
	}
	if _, err := time.Parse(time.RFC3339Nano, string(snapshot.EvaluatedAt)); err != nil {
		return errors.New("telemetry latest snapshot evaluatedAt is invalid")
	}
	return nil
}

type LatestCacheMaterialization struct {
	EventID  string
	DeviceID string
	Revision int64
	Attempts int
	Snapshot telemetryapi.DeviceObservationSnapshot
}

type LatestCacheOutbox interface {
	PendingLatestCacheMaterializations(context.Context, int, time.Time) ([]LatestCacheMaterialization, error)
	MarkLatestCacheMaterialized(context.Context, string, time.Time) error
	MarkLatestCacheFailed(context.Context, string, time.Time, string) error
}

type LatestCacheRebuildSource interface {
	LatestCacheRebuildBatch(context.Context, string, int) ([]telemetryapi.DeviceObservationSnapshot, error)
}

func RebuildLatestCache(ctx context.Context, source LatestCacheRebuildSource, cache LatestCache) (int, error) {
	if source == nil || cache == nil {
		return 0, errors.New("latest cache rebuild source and cache are required")
	}
	const batchSize = 256
	afterDeviceID := ""
	rebuilt := 0
	for {
		batch, err := source.LatestCacheRebuildBatch(ctx, afterDeviceID, batchSize)
		if err != nil {
			return rebuilt, fmt.Errorf("read latest-cache rebuild batch: %w", err)
		}
		if len(batch) == 0 {
			return rebuilt, nil
		}
		for _, snapshot := range batch {
			if _, err := cache.PutIfNewer(ctx, snapshot); err != nil {
				return rebuilt, fmt.Errorf("rebuild telemetry latest snapshot: %w", err)
			}
			rebuilt++
			afterDeviceID = string(snapshot.DeviceId)
		}
		if len(batch) < batchSize {
			return rebuilt, nil
		}
	}
}

type LatestCacheRelay struct {
	repository LatestCacheOutbox
	cache      LatestCache
	now        func() time.Time
}

func NewLatestCacheRelay(repository LatestCacheOutbox, cache LatestCache, now func() time.Time) (*LatestCacheRelay, error) {
	if repository == nil || cache == nil {
		return nil, errors.New("latest cache relay repository and cache are required")
	}
	if now == nil {
		now = time.Now
	}
	return &LatestCacheRelay{repository: repository, cache: cache, now: now}, nil
}

func (relay *LatestCacheRelay) RelayOnce(ctx context.Context, limit int) (int, error) {
	if relay == nil || relay.repository == nil || relay.cache == nil {
		return 0, ErrLatestCacheUnavailable
	}
	if limit <= 0 || limit > 256 {
		limit = 64
	}
	now := relay.now().UTC()
	pending, err := relay.repository.PendingLatestCacheMaterializations(ctx, limit, now)
	if err != nil {
		return 0, fmt.Errorf("load telemetry latest cache outbox: %w", err)
	}
	materialized := 0
	for _, item := range pending {
		_, putErr := relay.cache.PutIfNewer(ctx, item.Snapshot)
		if putErr != nil {
			next := now.Add(latestCacheRetryDelay(item.Attempts + 1))
			if markErr := relay.repository.MarkLatestCacheFailed(ctx, item.EventID, next, "REDIS_WRITE_FAILED"); markErr != nil {
				return materialized, fmt.Errorf("record telemetry latest cache failure: %w", markErr)
			}
			return materialized, ErrLatestCacheUnavailable
		}
		if err := relay.repository.MarkLatestCacheMaterialized(ctx, item.EventID, now); err != nil {
			return materialized, fmt.Errorf("mark telemetry latest cache materialized: %w", err)
		}
		materialized++
	}
	return materialized, nil
}

func latestCacheRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}
