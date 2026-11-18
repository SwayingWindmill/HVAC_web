package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
	"github.com/redis/go-redis/v9"
)

var (
	ErrLatestCacheMiss        = errors.New("telemetry latest cache miss")
	ErrLatestCacheUnavailable = errors.New("telemetry latest cache unavailable")
)

const (
	defaultLatestCacheKeyPrefix = "hvac:v2:latest:device"
	defaultLatestCacheTTL       = 7 * 24 * time.Hour
)

var redisLatestCAS = redis.NewScript(`
local incoming = ARGV[1]
local new_sampled_ms = tonumber(ARGV[7]) or 0
local ttl_ms = tonumber(ARGV[8]) or 0
local new_keys_sig = ARGV[9] or ''
local current = redis.call('HGET', KEYS[1], 'revision')
if current then
  if string.len(current) > string.len(incoming)
    or (string.len(current) == string.len(incoming) and current > incoming) then
    return 0
  end
end
local cur_sampled_ms = tonumber(redis.call('HGET', KEYS[1], 'sampled_ms')) or 0
local cur_keys_sig = redis.call('HGET', KEYS[1], 'keys_sig') or ''
if cur_sampled_ms > 0 and new_sampled_ms > 0 and new_sampled_ms < cur_sampled_ms then
  if cur_keys_sig == '' or new_keys_sig == '' or cur_keys_sig == new_keys_sig then
    return 0
  end
end
local indexRevision = redis.call('HGET', KEYS[2], 'revision')
if not indexRevision
  or string.len(indexRevision) < string.len(incoming)
  or (string.len(indexRevision) == string.len(incoming) and indexRevision < incoming) then
  redis.call('HSET', KEYS[2], 'revision', incoming, 'site_id', ARGV[3])
  if ttl_ms > 0 then
    redis.call('PEXPIRE', KEYS[2], ttl_ms)
  end
end
if current and string.len(current) == string.len(incoming) and current == incoming then
  return 0
end
redis.call('HSET', KEYS[1],
  'revision', incoming,
  'tenant_id', ARGV[2],
  'site_id', ARGV[3],
  'device_id', ARGV[4],
  'evaluated_at', ARGV[5],
  'snapshot', ARGV[6],
  'sampled_ms', ARGV[7],
  'keys_sig', new_keys_sig)
if ttl_ms > 0 then
  redis.call('PEXPIRE', KEYS[1], ttl_ms)
  redis.call('PEXPIRE', KEYS[2], ttl_ms)
end
return 1
`)

var redisSiteLiveCAS = redis.NewScript(`
local key = KEYS[1]
local ttl_ms = tonumber(ARGV[1]) or 0
local updated = 0
for i = 2, #ARGV, 4 do
  local field = ARGV[i]
  local new_sampled_ms = tonumber(ARGV[i + 1]) or 0
  local new_eff_ms = tonumber(ARGV[i + 2]) or new_sampled_ms
  local payload = ARGV[i + 3]
  local cur_raw = redis.call('HGET', key, field)
  local apply = true
  if cur_raw then
    local ok, cur_obj = pcall(cjson.decode, cur_raw)
    if ok and type(cur_obj) == 'table' then
      local cur_sampled_ms = tonumber(cur_obj['sampledMs']) or 0
      local cur_health_ms = tonumber(cur_obj['healthMs']) or cur_sampled_ms
      local cur_eff_ms = math.max(cur_sampled_ms, cur_health_ms)
      local cur_inv = (cur_obj['invalidated'] == true)
      if new_eff_ms < cur_eff_ms or (not cur_inv and new_sampled_ms < cur_sampled_ms) then
        apply = false
      end
    end
  end
  if apply then
    redis.call('HSET', key, field, payload)
    updated = updated + 1
  end
end
if updated > 0 and ttl_ms > 0 then
  redis.call('PEXPIRE', key, ttl_ms)
end
return updated
`)

type OptimizationLiveObservation struct {
	ObservationID   string   `json:"observationId"`
	DeviceID        string   `json:"deviceId"`
	PointID         string   `json:"pointId"`
	TelemetryKey    string   `json:"telemetryKey"`
	PointRevision   uint64   `json:"pointRevision"`
	SampledAt       string   `json:"sampledAt"`
	ReceivedAt      string   `json:"receivedAt"`
	HealthAt        string   `json:"healthAt,omitempty"`
	Value           float64  `json:"value"`
	ValueType       string   `json:"valueType,omitempty"`
	ValueNumber     *float64 `json:"valueNumber,omitempty"`
	ValueString     *string  `json:"valueString,omitempty"`
	ValueBoolean    *bool    `json:"valueBoolean,omitempty"`
	Unit            string   `json:"unit"`
	Quality         string   `json:"quality"`
	QualityReasons  []string `json:"qualityReasons"`
	HealthStatus    string   `json:"healthStatus,omitempty"`
	HealthReasons   []string `json:"healthReasons,omitempty"`
	SourceID        string   `json:"sourceId,omitempty"`
	SourceEventID   string   `json:"sourceEventId"`
	SourcePartition string   `json:"sourcePartition"`
	SourceOffset    uint64   `json:"sourceOffset"`
}

type cachedSiteLiveObservation struct {
	SampledMs   int64 `json:"sampledMs"`
	HealthMs    int64 `json:"healthMs,omitempty"`
	Invalidated bool  `json:"invalidated,omitempty"`
	OptimizationLiveObservation
}

type SiteLiveInvalidation struct {
	PointID   string
	SampledAt time.Time
}

type LatestCache interface {
	PutIfNewer(context.Context, telemetryapi.DeviceObservationSnapshot) (bool, error)
	Get(context.Context, string, string, string) (telemetryapi.DeviceObservationSnapshot, error)
	GetForDevice(context.Context, string, string) (telemetryapi.DeviceObservationSnapshot, error)
	BatchGetForDevices(context.Context, string, []string) (map[string]telemetryapi.DeviceObservationSnapshot, error)
	Ping(context.Context) error
	Close() error
}

// SiteLiveCache stores the hot per-site latest numeric point observations keyed by pointId.
type SiteLiveCache interface {
	PutSiteLiveObservations(context.Context, string, string, []OptimizationLiveObservation) error
	GetSiteLiveObservations(context.Context, string, string, []string) (map[string]OptimizationLiveObservation, error)
}

// SiteLiveCacheInvalidator records monotonicity-guarded invalidation tombstones on the hot site-point cache.
type SiteLiveCacheInvalidator interface {
	InvalidateSiteLiveObservations(context.Context, string, string, []SiteLiveInvalidation) error
}

type RedisLatestCacheConfig struct {
	URL       string
	KeyPrefix string
	Metrics   *PipelineMetrics
}

type RedisLatestCache struct {
	client    *redis.Client
	keyPrefix string
	metrics   *PipelineMetrics
}

// SetPipelineMetrics attaches bounded Prometheus pipeline counters to the Redis latest cache.
func (cache *RedisLatestCache) SetPipelineMetrics(metrics *PipelineMetrics) {
	if cache != nil {
		cache.metrics = metrics
	}
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
	return &RedisLatestCache{client: client, keyPrefix: strings.TrimSuffix(prefix, ":"), metrics: config.Metrics}, nil
}

func (cache *RedisLatestCache) Close() error {
	if cache == nil || cache.client == nil {
		return nil
	}
	return cache.client.Close()
}

func (cache *RedisLatestCache) Ping(ctx context.Context) error {
	if cache == nil || cache.client == nil {
		return ErrLatestCacheUnavailable
	}
	return cache.client.Ping(ctx).Err()
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
	maxSampledMs := snapshotMaxSampledMs(snapshot)
	keysSig := snapshotKeysSignature(snapshot)
	result, err := redisLatestCAS.Run(ctx, cache.client, []string{
		cache.key(snapshot.TenantId, snapshot.SiteId, snapshot.DeviceId),
		cache.deviceSiteKey(snapshot.TenantId, snapshot.DeviceId),
	},
		fmt.Sprintf("%d", snapshot.BusinessRevision),
		string(snapshot.TenantId), string(snapshot.SiteId), string(snapshot.DeviceId),
		string(snapshot.EvaluatedAt), string(encoded),
		fmt.Sprintf("%d", maxSampledMs), fmt.Sprintf("%d", defaultLatestCacheTTL.Milliseconds()), keysSig,
	).Int64()
	if err != nil {
		cache.metrics.AddLatestCacheUpdates(LatestCacheOutcomeError, 1)
		return false, fmt.Errorf("materialize telemetry latest snapshot: %w", err)
	}
	if result == 1 {
		cache.metrics.AddLatestCacheUpdates(LatestCacheOutcomeApplied, 1)
		return true, nil
	}
	cache.metrics.AddLatestCacheUpdates(LatestCacheOutcomeStaleSkipped, 1)
	return false, nil
}

func snapshotKeysSignature(snapshot telemetryapi.DeviceObservationSnapshot) string {
	keys := make([]string, 0, len(snapshot.Values))
	for _, value := range snapshot.Values {
		switch {
		case value.Present != nil:
			keys = append(keys, string(value.Present.Key))
		case value.Missing != nil:
			keys = append(keys, string(value.Missing.Key))
		}
	}
	slices.Sort(keys)
	hasher := sha256.New()
	for _, key := range keys {
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
	}
	var sum [sha256.Size]byte
	hasher.Sum(sum[:0])
	return hex.EncodeToString(sum[:8])
}

func snapshotMaxSampledMs(snapshot telemetryapi.DeviceObservationSnapshot) int64 {
	var maxMs int64
	for _, value := range snapshot.Values {
		if value.Present == nil {
			continue
		}
		sampledAt, err := time.Parse(time.RFC3339Nano, string(value.Present.SampledAt))
		if err != nil {
			continue
		}
		ms := sampledAt.UTC().UnixMilli()
		if ms > maxMs {
			maxMs = ms
		}
	}
	return maxMs
}

func (cache *RedisLatestCache) GetForDevice(ctx context.Context, tenantID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	if cache == nil || cache.client == nil {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheUnavailable
	}
	siteID, err := cache.client.HGet(ctx, cache.deviceSiteKey(telemetryapi.UUIDv7(tenantID), telemetryapi.UUIDv7(deviceID)), "site_id").Result()
	if errors.Is(err, redis.Nil) {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	if err != nil {
		return telemetryapi.DeviceObservationSnapshot{}, fmt.Errorf("read telemetry latest device site index: %w", err)
	}
	return cache.Get(ctx, tenantID, siteID, deviceID)
}

func (cache *RedisLatestCache) BatchGetForDevices(ctx context.Context, tenantID string, deviceIDs []string) (map[string]telemetryapi.DeviceObservationSnapshot, error) {
	if cache == nil || cache.client == nil {
		return nil, ErrLatestCacheUnavailable
	}
	uniqueIDs := make([]string, 0, len(deviceIDs))
	seen := make(map[string]struct{}, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		if deviceID == "" {
			continue
		}
		if _, ok := seen[deviceID]; !ok {
			seen[deviceID] = struct{}{}
			uniqueIDs = append(uniqueIDs, deviceID)
		}
	}
	if len(uniqueIDs) == 0 {
		return map[string]telemetryapi.DeviceObservationSnapshot{}, nil
	}

	siteCmds := make(map[string]*redis.StringCmd, len(uniqueIDs))
	if _, err := cache.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, deviceID := range uniqueIDs {
			siteCmds[deviceID] = pipe.HGet(ctx, cache.deviceSiteKey(telemetryapi.UUIDv7(tenantID), telemetryapi.UUIDv7(deviceID)), "site_id")
		}
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: batch read telemetry latest device site index: %w", ErrLatestCacheUnavailable, err)
	}

	siteIDs := make(map[string]string, len(uniqueIDs))
	hitIDs := make([]string, 0, len(uniqueIDs))
	for _, deviceID := range uniqueIDs {
		siteID, err := siteCmds[deviceID].Result()
		if errors.Is(err, redis.Nil) || siteID == "" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: batch read telemetry latest device site index: %w", ErrLatestCacheUnavailable, err)
		}
		siteIDs[deviceID] = siteID
		hitIDs = append(hitIDs, deviceID)
	}
	if len(hitIDs) == 0 {
		return map[string]telemetryapi.DeviceObservationSnapshot{}, nil
	}

	snapshotCmds := make(map[string]*redis.StringCmd, len(hitIDs))
	if _, err := cache.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, deviceID := range hitIDs {
			key := cache.key(telemetryapi.UUIDv7(tenantID), telemetryapi.UUIDv7(siteIDs[deviceID]), telemetryapi.UUIDv7(deviceID))
			snapshotCmds[deviceID] = pipe.HGet(ctx, key, "snapshot")
		}
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: batch read telemetry latest snapshot: %w", ErrLatestCacheUnavailable, err)
	}

	snapshots := make(map[string]telemetryapi.DeviceObservationSnapshot, len(hitIDs))
	for _, deviceID := range hitIDs {
		raw, err := snapshotCmds[deviceID].Result()
		if errors.Is(err, redis.Nil) || raw == "" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: batch read telemetry latest snapshot: %w", ErrLatestCacheUnavailable, err)
		}
		var snapshot telemetryapi.DeviceObservationSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			continue
		}
		if err := validateLatestCacheSnapshot(snapshot); err != nil {
			continue
		}
		if string(snapshot.TenantId) != tenantID || string(snapshot.SiteId) != siteIDs[deviceID] || string(snapshot.DeviceId) != deviceID {
			continue
		}
		snapshots[deviceID] = snapshot
	}
	return snapshots, nil
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

type preparedSiteLiveEntry struct {
	field     string
	sampledMs string
	effMs     string
	payload   string
}

func (cache *RedisLatestCache) runSiteLiveCAS(ctx context.Context, tenantID, siteID string, entries []preparedSiteLiveEntry, appliedOutcome string) error {
	if cache == nil || cache.client == nil {
		return ErrLatestCacheUnavailable
	}
	cleanTenant, cleanSite, err := normalizeSiteLiveScope(tenantID, siteID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	hashKey := cache.sitePointsKey(cleanTenant, cleanSite)
	ttlMs := strconv.FormatInt(defaultLatestCacheTTL.Milliseconds(), 10)
	const chunkSize = 256
	for start := 0; start < len(entries); start += chunkSize {
		end := min(start+chunkSize, len(entries))
		chunk := entries[start:end]
		args := make([]any, 0, 1+len(chunk)*4)
		args = append(args, ttlMs)
		for _, item := range chunk {
			args = append(args, item.field, item.sampledMs, item.effMs, item.payload)
		}
		applied, runErr := redisSiteLiveCAS.Run(ctx, cache.client, []string{hashKey}, args...).Int64()
		if runErr != nil {
			cache.metrics.AddLatestCacheUpdates(LatestCacheOutcomeError, len(entries)-start)
			return fmt.Errorf("materialize telemetry site live observations: %w", runErr)
		}
		updated := max(0, min(len(chunk), int(applied)))
		if updated > 0 {
			cache.metrics.AddLatestCacheUpdates(appliedOutcome, updated)
		}
		if skipped := len(chunk) - updated; skipped > 0 {
			cache.metrics.AddLatestCacheUpdates(LatestCacheOutcomeStaleSkipped, skipped)
		}
	}
	return nil
}

func (cache *RedisLatestCache) PutSiteLiveObservations(ctx context.Context, tenantID, siteID string, observations []OptimizationLiveObservation) error {
	if cache == nil || cache.client == nil {
		return ErrLatestCacheUnavailable
	}
	if len(observations) == 0 {
		_, _, err := normalizeSiteLiveScope(tenantID, siteID)
		return err
	}
	entries := make([]preparedSiteLiveEntry, 0, len(observations))
	for _, obs := range observations {
		entry, err := normalizeCachedSiteLiveObservation(obs)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("encode telemetry site live observation: %w", err)
		}
		entries = append(entries, preparedSiteLiveEntry{
			field:     entry.PointID,
			sampledMs: strconv.FormatInt(entry.SampledMs, 10),
			effMs:     strconv.FormatInt(max(entry.SampledMs, entry.HealthMs), 10),
			payload:   string(encoded),
		})
	}
	return cache.runSiteLiveCAS(ctx, tenantID, siteID, entries, LatestCacheOutcomeApplied)
}

func (cache *RedisLatestCache) GetSiteLiveObservations(ctx context.Context, tenantID, siteID string, pointIDs []string) (map[string]OptimizationLiveObservation, error) {
	if cache == nil || cache.client == nil {
		return nil, ErrLatestCacheUnavailable
	}
	cleanTenant, cleanSite, err := normalizeSiteLiveScope(tenantID, siteID)
	if err != nil {
		return nil, err
	}
	cleanIDs, err := normalizeSitePointIDs(pointIDs)
	if err != nil {
		return nil, err
	}
	if len(cleanIDs) == 0 {
		return map[string]OptimizationLiveObservation{}, nil
	}
	values, err := cache.client.HMGet(ctx, cache.sitePointsKey(cleanTenant, cleanSite), cleanIDs...).Result()
	if err != nil {
		return nil, fmt.Errorf("read telemetry site live observations: %w", err)
	}
	out := make(map[string]OptimizationLiveObservation, len(cleanIDs))
	for index, rawVal := range values {
		if rawVal == nil || index >= len(cleanIDs) {
			continue
		}
		var rawBytes []byte
		switch typed := rawVal.(type) {
		case string:
			rawBytes = []byte(typed)
		case []byte:
			rawBytes = typed
		default:
			continue
		}
		if len(rawBytes) == 0 {
			continue
		}
		var decoded cachedSiteLiveObservation
		if err := json.Unmarshal(rawBytes, &decoded); err != nil {
			continue
		}
		if decoded.Invalidated {
			continue
		}
		if math.IsNaN(decoded.Value) || math.IsInf(decoded.Value, 0) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(decoded.PointID), cleanIDs[index]) {
			continue
		}
		decoded.PointID = cleanIDs[index]
		if decoded.QualityReasons == nil {
			decoded.QualityReasons = []string{}
		}
		out[cleanIDs[index]] = decoded.OptimizationLiveObservation
	}
	return out, nil
}

func (cache *RedisLatestCache) InvalidateSiteLiveObservations(ctx context.Context, tenantID, siteID string, items []SiteLiveInvalidation) error {
	if cache == nil || cache.client == nil {
		return ErrLatestCacheUnavailable
	}
	if len(items) == 0 {
		_, _, err := normalizeSiteLiveScope(tenantID, siteID)
		return err
	}
	entries := make([]preparedSiteLiveEntry, 0, len(items))
	seen := make(map[string]int, len(items))
	for _, item := range items {
		cleanID := strings.ToLower(strings.TrimSpace(item.PointID))
		if !uuidPattern.MatchString(cleanID) {
			return errors.New("telemetry site live cache pointId must be a UUID")
		}
		if item.SampledAt.IsZero() {
			return errors.New("telemetry site live invalidation sampledAt is required")
		}
		ms := item.SampledAt.UTC().UnixMilli()
		tombstone := cachedSiteLiveObservation{
			SampledMs:   ms,
			HealthMs:    ms,
			Invalidated: true,
			OptimizationLiveObservation: OptimizationLiveObservation{
				PointID: cleanID,
			},
		}
		encoded, err := json.Marshal(tombstone)
		if err != nil {
			return fmt.Errorf("encode telemetry site live invalidation: %w", err)
		}
		prepared := preparedSiteLiveEntry{
			field:     cleanID,
			sampledMs: strconv.FormatInt(ms, 10),
			effMs:     strconv.FormatInt(ms, 10),
			payload:   string(encoded),
		}
		if idx, exists := seen[cleanID]; exists {
			entries[idx] = prepared
		} else {
			seen[cleanID] = len(entries)
			entries = append(entries, prepared)
		}
	}
	return cache.runSiteLiveCAS(ctx, tenantID, siteID, entries, LatestCacheOutcomeInvalidated)
}

func (cache *RedisLatestCache) key(tenantID, siteID, deviceID telemetryapi.UUIDv7) string {
	return fmt.Sprintf("%s:%s:%s:%s", cache.keyPrefix, tenantID, siteID, deviceID)
}

func (cache *RedisLatestCache) deviceSiteKey(tenantID, deviceID telemetryapi.UUIDv7) string {
	return fmt.Sprintf("%s:index:%s:%s", cache.keyPrefix, tenantID, deviceID)
}

func (cache *RedisLatestCache) sitePointsKey(tenantID, siteID string) string {
	prefix := strings.TrimSuffix(strings.TrimSpace(cache.keyPrefix), ":")
	if prefix == "" {
		prefix = defaultLatestCacheKeyPrefix
	}
	prefix = strings.TrimSuffix(prefix, ":device")
	if prefix == "" {
		prefix = "hvac:v2:latest"
	}
	return fmt.Sprintf("%s:site-points:%s:%s", prefix, tenantID, siteID)
}

func normalizeSiteLiveScope(tenantID, siteID string) (string, string, error) {
	cleanTenant := strings.ToLower(strings.TrimSpace(tenantID))
	cleanSite := strings.ToLower(strings.TrimSpace(siteID))
	if !uuidPattern.MatchString(cleanTenant) || !uuidPattern.MatchString(cleanSite) {
		return "", "", errors.New("telemetry site live cache tenantId and siteId must be UUIDs")
	}
	return cleanTenant, cleanSite, nil
}

func normalizeSitePointIDs(pointIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(pointIDs))
	out := make([]string, 0, len(pointIDs))
	for _, raw := range pointIDs {
		clean := strings.ToLower(strings.TrimSpace(raw))
		if clean == "" {
			continue
		}
		if !uuidPattern.MatchString(clean) {
			return nil, errors.New("telemetry site live cache pointId must be a UUID")
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}

func normalizeCachedSiteLiveObservation(obs OptimizationLiveObservation) (cachedSiteLiveObservation, error) {
	pointID := strings.ToLower(strings.TrimSpace(obs.PointID))
	if !uuidPattern.MatchString(pointID) {
		return cachedSiteLiveObservation{}, errors.New("telemetry site live observation pointId must be a UUID")
	}
	if math.IsNaN(obs.Value) || math.IsInf(obs.Value, 0) {
		return cachedSiteLiveObservation{}, errors.New("telemetry site live observation value must be finite")
	}
	sampledAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(obs.SampledAt))
	if err != nil {
		return cachedSiteLiveObservation{}, fmt.Errorf("telemetry site live observation sampledAt is invalid: %w", err)
	}
	normalized := obs
	normalized.ObservationID = strings.ToLower(strings.TrimSpace(obs.ObservationID))
	normalized.DeviceID = strings.ToLower(strings.TrimSpace(obs.DeviceID))
	normalized.PointID = pointID
	normalized.TelemetryKey = strings.TrimSpace(obs.TelemetryKey)
	normalized.SampledAt = sampledAt.UTC().Format(time.RFC3339Nano)
	if rawReceived := strings.TrimSpace(obs.ReceivedAt); rawReceived != "" {
		receivedAt, err := time.Parse(time.RFC3339Nano, rawReceived)
		if err != nil {
			return cachedSiteLiveObservation{}, fmt.Errorf("telemetry site live observation receivedAt is invalid: %w", err)
		}
		normalized.ReceivedAt = receivedAt.UTC().Format(time.RFC3339Nano)
	} else {
		normalized.ReceivedAt = ""
	}
	sampledMs := sampledAt.UTC().UnixMilli()
	healthMs := sampledMs
	if rawHealth := strings.TrimSpace(obs.HealthAt); rawHealth != "" {
		healthAt, err := time.Parse(time.RFC3339Nano, rawHealth)
		if err != nil {
			return cachedSiteLiveObservation{}, fmt.Errorf("telemetry site live observation healthAt is invalid: %w", err)
		}
		normalized.HealthAt = healthAt.UTC().Format(time.RFC3339Nano)
		healthMs = healthAt.UTC().UnixMilli()
	}
	normalized.SourceEventID = strings.ToLower(strings.TrimSpace(obs.SourceEventID))
	if normalized.QualityReasons == nil {
		normalized.QualityReasons = []string{}
	} else {
		normalized.QualityReasons = slices.Clone(normalized.QualityReasons)
	}
	if len(normalized.HealthReasons) > 0 {
		normalized.HealthReasons = slices.Clone(normalized.HealthReasons)
	}
	return cachedSiteLiveObservation{
		SampledMs:                   sampledMs,
		HealthMs:                    healthMs,
		OptimizationLiveObservation: normalized,
	}, nil
}

var (
	_ LatestCache              = (*RedisLatestCache)(nil)
	_ SiteLiveCache            = (*RedisLatestCache)(nil)
	_ SiteLiveCacheInvalidator = (*RedisLatestCache)(nil)
)

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
