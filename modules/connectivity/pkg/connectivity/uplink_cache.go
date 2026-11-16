package connectivity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

const (
	uplinkPositiveTTL     = 30 * time.Second
	uplinkNegativeTTL     = 5 * time.Second
	uplinkCacheMaxEntries = 4096
)

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
	seq       uint64
}

type gatewayLookupResult struct {
	gateway adapter.Gateway
	err     error
}

type deviceCacheKey struct {
	tenantID  string
	gatewayID string
	sourceKey string
}

type pointCacheKey struct {
	tenantID  string
	deviceID  string
	pointCode string
}

type uplinkCache struct {
	mu            sync.Mutex
	seq           uint64
	baseEpoch     uint64
	gatewayEpochs map[string]uint64
	gateways      map[string]cacheEntry[gatewayLookupResult]
	devices       map[deviceCacheKey]cacheEntry[string]
	points        map[pointCacheKey]cacheEntry[*adapter.ResolvedPoint]
}

func (cache *uplinkCache) gatewayEpochLocked(gatewayID string) uint64 {
	norm := strings.ToLower(strings.TrimSpace(gatewayID))
	return cache.baseEpoch + cache.gatewayEpochs[norm]
}

func (cache *uplinkCache) bumpGatewayEpochLocked(gatewayID string) {
	norm := strings.ToLower(strings.TrimSpace(gatewayID))
	if cache.gatewayEpochs == nil {
		cache.gatewayEpochs = make(map[string]uint64)
	}
	if _, exists := cache.gatewayEpochs[norm]; !exists && len(cache.gatewayEpochs) >= uplinkCacheMaxEntries {
		cache.baseEpoch++
		clear(cache.gatewayEpochs)
	}
	cache.gatewayEpochs[norm]++
}

func cacheGetOrEpoch[K comparable, V any](cache *uplinkCache, entries map[K]cacheEntry[V], key K, now time.Time, gatewayID string) (V, bool, uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entries != nil {
		if entry, ok := entries[key]; ok {
			if now.Before(entry.expiresAt) {
				return entry.value, true, 0
			}
			delete(entries, key)
		}
	}
	var zero V
	return zero, false, cache.gatewayEpochLocked(gatewayID)
}

func cacheSetIfEpoch[K comparable, V any](cache *uplinkCache, entries *map[K]cacheEntry[V], key K, value V, expiresAt time.Time, now time.Time, gatewayID string, expectedEpoch uint64) {
	if !expiresAt.After(now) {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.gatewayEpochLocked(gatewayID) != expectedEpoch {
		return
	}
	if *entries == nil {
		*entries = make(map[K]cacheEntry[V])
	}
	if _, exists := (*entries)[key]; !exists && len(*entries) >= uplinkCacheMaxEntries {
		evictExpiredAndOldestHalf(*entries, now)
	}
	cache.seq++
	(*entries)[key] = cacheEntry[V]{
		value:     value,
		expiresAt: expiresAt,
		seq:       cache.seq,
	}
}

func evictExpiredAndOldestHalf[K comparable, V any](entries map[K]cacheEntry[V], now time.Time) {
	for k, entry := range entries {
		if !now.Before(entry.expiresAt) {
			delete(entries, k)
		}
	}
	if len(entries) < uplinkCacheMaxEntries {
		return
	}
	type rankedEntry struct {
		key K
		seq uint64
	}
	ranked := make([]rankedEntry, 0, len(entries))
	for k, entry := range entries {
		ranked = append(ranked, rankedEntry{key: k, seq: entry.seq})
	}
	slices.SortFunc(ranked, func(a, b rankedEntry) int {
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})
	dropCount := len(ranked) / 2
	if dropCount < 1 {
		dropCount = 1
	}
	for i := 0; i < dropCount; i++ {
		delete(entries, ranked[i].key)
	}
}

func cloneResolvedPoint(point *adapter.ResolvedPoint) *adapter.ResolvedPoint {
	if point == nil {
		return nil
	}
	copied := *point
	if point.SensorID != nil {
		v := *point.SensorID
		copied.SensorID = &v
	}
	if point.Unit != nil {
		v := *point.Unit
		copied.Unit = &v
	}
	if point.CounterDecreaseMode != nil {
		v := *point.CounterDecreaseMode
		copied.CounterDecreaseMode = &v
	}
	if point.CounterRolloverModulus != nil {
		v := *point.CounterRolloverModulus
		copied.CounterRolloverModulus = &v
	}
	return &copied
}

// InvalidateGatewayCache removes cached identity and source-key entries for a
// Gateway and advances its invalidation epoch so any concurrent read that started
// before commit cannot repopulate stale entries.
func (store *Store) InvalidateGatewayCache(gatewayID string) {
	if store == nil {
		return
	}
	trimmed := strings.TrimSpace(gatewayID)
	if trimmed == "" {
		return
	}
	store.cache.mu.Lock()
	defer store.cache.mu.Unlock()
	store.cache.bumpGatewayEpochLocked(trimmed)
	for key := range store.cache.gateways {
		if strings.EqualFold(key, trimmed) {
			delete(store.cache.gateways, key)
		}
	}
	for key := range store.cache.devices {
		if strings.EqualFold(key.gatewayID, trimmed) {
			delete(store.cache.devices, key)
		}
	}
}

func (store *Store) resolveGatewayCached(ctx context.Context, gatewayID string) (adapter.Gateway, error) {
	return store.resolveGatewayCachedWith(ctx, gatewayID, store.resolveGatewayUncached)
}

func (store *Store) resolveGatewayCachedWith(
	ctx context.Context,
	gatewayID string,
	uncached func(context.Context, string, time.Time) (adapter.Gateway, time.Time, error),
) (adapter.Gateway, error) {
	now := store.clock().UTC()
	cached, ok, epoch := cacheGetOrEpoch(&store.cache, store.cache.gateways, gatewayID, now, gatewayID)
	if ok {
		return cached.gateway, cached.err
	}
	gateway, validUntil, err := uncached(ctx, gatewayID, now)
	switch {
	case err == nil:
		expiresAt := now.Add(uplinkPositiveTTL)
		if !validUntil.IsZero() && validUntil.Before(expiresAt) {
			expiresAt = validUntil
		}
		cacheSetIfEpoch(&store.cache, &store.cache.gateways, gatewayID, gatewayLookupResult{gateway: gateway}, expiresAt, now, gatewayID, epoch)
		return gateway, nil
	case errors.Is(err, adapter.ErrGatewayUnknown), errors.Is(err, adapter.ErrGatewayCredentialInactive):
		cacheSetIfEpoch(&store.cache, &store.cache.gateways, gatewayID, gatewayLookupResult{gateway: gateway, err: err}, now.Add(uplinkNegativeTTL), now, gatewayID, epoch)
		return gateway, err
	default:
		return adapter.Gateway{}, err
	}
}

func (store *Store) resolveGatewayUncached(ctx context.Context, gatewayID string, now time.Time) (adapter.Gateway, time.Time, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return adapter.Gateway{}, time.Time{}, err
	}
	defer tx.Rollback(ctx)
	gateway := adapter.Gateway{ID: gatewayID}
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text, site_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&gateway.TenantID, &gateway.SiteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return adapter.Gateway{}, time.Time{}, adapter.ErrGatewayUnknown
	}
	if err != nil {
		return adapter.Gateway{}, time.Time{}, fmt.Errorf("read Registry gateway directory: %w", err)
	}
	if err := setTenant(ctx, tx, gateway.TenantID); err != nil {
		return adapter.Gateway{}, time.Time{}, err
	}
	var validUntil *time.Time
	if err := tx.QueryRow(ctx, `
SELECT MAX(valid_until)
FROM connectivity.gateway_credentials
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid AND status = 'ACTIVE'
  AND valid_from <= $3 AND valid_until > $3
`, gateway.TenantID, gatewayID, now).Scan(&validUntil); err != nil {
		return adapter.Gateway{}, time.Time{}, fmt.Errorf("read Gateway credential: %w", err)
	}
	if validUntil == nil {
		return gateway, time.Time{}, adapter.ErrGatewayCredentialInactive
	}
	return gateway, validUntil.UTC(), nil
}

func (store *Store) resolveDeviceCached(
	ctx context.Context,
	gateway adapter.Gateway,
	sourceKey string,
	uncached func(context.Context, adapter.Gateway, string) (string, error),
) (string, error) {
	now := store.clock().UTC()
	key := deviceCacheKey{tenantID: gateway.TenantID, gatewayID: gateway.ID, sourceKey: sourceKey}
	cached, ok, epoch := cacheGetOrEpoch(&store.cache, store.cache.devices, key, now, gateway.ID)
	if ok {
		return cached, nil
	}
	deviceID, err := uncached(ctx, gateway, sourceKey)
	if err != nil {
		return "", err
	}
	ttl := uplinkPositiveTTL
	if deviceID == "" {
		ttl = uplinkNegativeTTL
	}
	cacheSetIfEpoch(&store.cache, &store.cache.devices, key, deviceID, now.Add(ttl), now, gateway.ID, epoch)
	return deviceID, nil
}

type resolvedPointTarget struct {
	deviceID  string
	pointCode string
}

type bindingsBatchUncachedFn func(
	ctx context.Context,
	gateway adapter.Gateway,
	missingSourceKeys []string,
	cachedPointQueries []resolvedPointTarget,
	allQueries []adapter.DevicePointQuery,
) (adapter.ResolvedBindings, []resolvedPointTarget, error)

func (store *Store) resolveBindingsCached(ctx context.Context, gateway adapter.Gateway, queries []adapter.DevicePointQuery) (adapter.ResolvedBindings, error) {
	return store.resolveBindingsCachedWith(ctx, gateway, queries, store.resolveBindingsUncached)
}

func (store *Store) resolveBindingsCachedWith(
	ctx context.Context,
	gateway adapter.Gateway,
	queries []adapter.DevicePointQuery,
	uncached bindingsBatchUncachedFn,
) (adapter.ResolvedBindings, error) {
	result := adapter.ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*adapter.ResolvedPoint),
	}
	uniqueSourceKeys := make([]string, 0, len(queries))
	sourceKeySeen := make(map[string]struct{}, len(queries))
	for _, query := range queries {
		if query.SourceKey == "" {
			continue
		}
		if _, dup := sourceKeySeen[query.SourceKey]; !dup {
			sourceKeySeen[query.SourceKey] = struct{}{}
			uniqueSourceKeys = append(uniqueSourceKeys, query.SourceKey)
		}
	}
	if len(uniqueSourceKeys) == 0 {
		return result, nil
	}

	now := store.clock().UTC()
	cachedDevices := make(map[string]string, len(uniqueSourceKeys))
	var missingSourceKeys []string
	var missingPointTargets []resolvedPointTarget
	pointTargetSeen := make(map[resolvedPointTarget]struct{}, len(queries))

	store.cache.mu.Lock()
	epoch := store.cache.gatewayEpochLocked(gateway.ID)
	for _, sourceKey := range uniqueSourceKeys {
		key := deviceCacheKey{tenantID: gateway.TenantID, gatewayID: gateway.ID, sourceKey: sourceKey}
		if store.cache.devices != nil {
			if entry, ok := store.cache.devices[key]; ok {
				if now.Before(entry.expiresAt) {
					cachedDevices[sourceKey] = entry.value
					if entry.value != "" {
						result.Devices[sourceKey] = entry.value
					}
					continue
				}
				delete(store.cache.devices, key)
			}
		}
		missingSourceKeys = append(missingSourceKeys, sourceKey)
	}
	for _, query := range queries {
		if query.SourceKey == "" || query.PointCode == "" {
			continue
		}
		deviceID, hasDevice := cachedDevices[query.SourceKey]
		if !hasDevice || deviceID == "" {
			continue
		}
		ptKey := pointCacheKey{tenantID: gateway.TenantID, deviceID: deviceID, pointCode: query.PointCode}
		if store.cache.points != nil {
			if entry, ok := store.cache.points[ptKey]; ok {
				if now.Before(entry.expiresAt) {
					if entry.value != nil {
						result.Points[adapter.PointBindingKey(deviceID, query.PointCode)] = cloneResolvedPoint(entry.value)
					}
					continue
				}
				delete(store.cache.points, ptKey)
			}
		}
		target := resolvedPointTarget{deviceID: deviceID, pointCode: query.PointCode}
		if _, dup := pointTargetSeen[target]; !dup {
			pointTargetSeen[target] = struct{}{}
			missingPointTargets = append(missingPointTargets, target)
		}
	}
	store.cache.mu.Unlock()

	if len(missingSourceKeys) == 0 && len(missingPointTargets) == 0 {
		return result, nil
	}

	fresh, queriedTargets, err := uncached(ctx, gateway, missingSourceKeys, missingPointTargets, queries)
	if err != nil {
		return adapter.ResolvedBindings{}, err
	}

	for _, sourceKey := range missingSourceKeys {
		deviceID := fresh.Devices[sourceKey]
		ttl := uplinkPositiveTTL
		if deviceID != "" {
			result.Devices[sourceKey] = deviceID
		} else {
			ttl = uplinkNegativeTTL
		}
		cacheSetIfEpoch(
			&store.cache,
			&store.cache.devices,
			deviceCacheKey{tenantID: gateway.TenantID, gatewayID: gateway.ID, sourceKey: sourceKey},
			deviceID,
			now.Add(ttl),
			now,
			gateway.ID,
			epoch,
		)
	}
	for _, target := range queriedTargets {
		bindingKey := adapter.PointBindingKey(target.deviceID, target.pointCode)
		point := fresh.Points[bindingKey]
		ttl := uplinkPositiveTTL
		if point != nil {
			result.Points[bindingKey] = cloneResolvedPoint(point)
		} else {
			ttl = uplinkNegativeTTL
		}
		cacheSetIfEpoch(
			&store.cache,
			&store.cache.points,
			pointCacheKey{tenantID: gateway.TenantID, deviceID: target.deviceID, pointCode: target.pointCode},
			cloneResolvedPoint(point),
			now.Add(ttl),
			now,
			gateway.ID,
			epoch,
		)
	}
	return result, nil
}
