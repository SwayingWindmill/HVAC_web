package connectivity

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
	"golang.org/x/sync/singleflight"
)

const (
	uplinkPositiveTTL        = 30 * time.Second
	uplinkNegativeTTL        = 5 * time.Second
	credVerifyPositiveTTL    = 15 * time.Second
	uplinkFlightQueryTimeout = 5 * time.Second
	uplinkCacheMaxEntries    = 4096
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

type credVerifyCacheKey struct {
	gatewayID   string
	fingerprint string
}

type credVerifyCacheResult struct {
	gatewayID string
}

type uplinkCache struct {
	mu            sync.Mutex
	seq           uint64
	baseEpoch     uint64
	gatewayEpochs map[string]uint64
	gateways      map[string]cacheEntry[gatewayLookupResult]
	devices       map[deviceCacheKey]cacheEntry[string]
	points        map[pointCacheKey]cacheEntry[*adapter.ResolvedPoint]
	credVerify    map[credVerifyCacheKey]cacheEntry[credVerifyCacheResult]
	inFlightKeys  map[string]map[string]int
	sfGroup       singleflight.Group
	quarantine    quarantineBurstCache
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

func (cache *uplinkCache) registerFlight(gatewayID, sfKey string) {
	norm := strings.ToLower(strings.TrimSpace(gatewayID))
	if norm == "" || sfKey == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.inFlightKeys == nil {
		cache.inFlightKeys = make(map[string]map[string]int)
	}
	keys := cache.inFlightKeys[norm]
	if keys == nil {
		keys = make(map[string]int)
		cache.inFlightKeys[norm] = keys
	}
	keys[sfKey]++
}

func (cache *uplinkCache) unregisterFlight(gatewayID, sfKey string) {
	norm := strings.ToLower(strings.TrimSpace(gatewayID))
	if norm == "" || sfKey == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	keys := cache.inFlightKeys[norm]
	if keys == nil {
		return
	}
	if count := keys[sfKey]; count <= 1 {
		delete(keys, sfKey)
	} else {
		keys[sfKey] = count - 1
	}
	if len(keys) == 0 {
		delete(cache.inFlightKeys, norm)
	}
}

func gatewayFlightKey(gatewayID string) string {
	return "gw:" + strings.ToLower(strings.TrimSpace(gatewayID))
}

func deviceFlightKey(tenantID, gatewayID, sourceKey string) string {
	return "dev:" + tenantID + "\x00" + strings.ToLower(strings.TrimSpace(gatewayID)) + "\x00" + sourceKey
}

func credFlightKey(gatewayID, fingerprint string) string {
	return "cred:" + strings.ToLower(strings.TrimSpace(gatewayID)) + "\x00" + fingerprint
}

func bindingsFlightKey(tenantID, gatewayID string, queries []adapter.DevicePointQuery) string {
	pairs := make([]string, 0, len(queries))
	seen := make(map[string]struct{}, len(queries))
	for _, query := range queries {
		if query.SourceKey == "" {
			continue
		}
		pair := query.SourceKey + "\x00" + query.PointCode
		if _, dup := seen[pair]; dup {
			continue
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	slices.Sort(pairs)
	hasher := sha256.New()
	var lenBuf [4]byte
	writePart := func(s string) {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
		_, _ = hasher.Write(lenBuf[:])
		_, _ = hasher.Write([]byte(s))
	}
	writePart(tenantID)
	writePart(strings.ToLower(strings.TrimSpace(gatewayID)))
	for _, pair := range pairs {
		writePart(pair)
	}
	return "bind:" + hex.EncodeToString(hasher.Sum(nil))
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

func cloneResolvedBindings(src adapter.ResolvedBindings) adapter.ResolvedBindings {
	dst := adapter.ResolvedBindings{
		Devices: make(map[string]string, len(src.Devices)),
		Points:  make(map[string]*adapter.ResolvedPoint, len(src.Points)),
	}
	for k, v := range src.Devices {
		dst.Devices[k] = v
	}
	for k, v := range src.Points {
		dst.Points[k] = cloneResolvedPoint(v)
	}
	return dst
}

// InvalidateGatewayCache removes cached identity, credential verification, and source-key
// entries for a Gateway and advances its invalidation epoch so any concurrent read that
// started before commit cannot repopulate stale entries.
func (store *Store) InvalidateGatewayCache(gatewayID string) {
	if store == nil {
		return
	}
	trimmed := strings.TrimSpace(gatewayID)
	if trimmed == "" {
		return
	}
	norm := strings.ToLower(trimmed)

	store.cache.mu.Lock()
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
	for key := range store.cache.credVerify {
		if strings.EqualFold(key.gatewayID, trimmed) {
			delete(store.cache.credVerify, key)
		}
	}
	var flightKeys []string
	if keys := store.cache.inFlightKeys[norm]; len(keys) > 0 {
		flightKeys = make([]string, 0, len(keys))
		for k := range keys {
			flightKeys = append(flightKeys, k)
		}
	}
	store.cache.mu.Unlock()

	store.cache.sfGroup.Forget(gatewayFlightKey(trimmed))
	for _, k := range flightKeys {
		store.cache.sfGroup.Forget(k)
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
	cached, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gatewayID, now, gatewayID)
	if ok {
		return cached.gateway, cached.err
	}
	if err := ctx.Err(); err != nil {
		return adapter.Gateway{}, err
	}

	sfKey := gatewayFlightKey(gatewayID)
	ch := store.cache.sfGroup.DoChan(sfKey, func() (any, error) {
		store.cache.registerFlight(gatewayID, sfKey)
		defer store.cache.unregisterFlight(gatewayID, sfKey)

		flightNow := store.clock().UTC()
		if hitVal, hit, epoch := cacheGetOrEpoch(&store.cache, store.cache.gateways, gatewayID, flightNow, gatewayID); hit {
			return hitVal, nil
		} else {
			queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uplinkFlightQueryTimeout)
			defer cancel()
			gateway, validUntil, err := uncached(queryCtx, gatewayID, flightNow)
			switch {
			case err == nil:
				expiresAt := flightNow.Add(uplinkPositiveTTL)
				if !validUntil.IsZero() && validUntil.Before(expiresAt) {
					expiresAt = validUntil
				}
				cacheSetIfEpoch(&store.cache, &store.cache.gateways, gatewayID, gatewayLookupResult{gateway: gateway}, expiresAt, flightNow, gatewayID, epoch)
				return gatewayLookupResult{gateway: gateway}, nil
			case errors.Is(err, adapter.ErrGatewayUnknown), errors.Is(err, adapter.ErrGatewayCredentialInactive):
				cacheSetIfEpoch(&store.cache, &store.cache.gateways, gatewayID, gatewayLookupResult{gateway: gateway, err: err}, flightNow.Add(uplinkNegativeTTL), flightNow, gatewayID, epoch)
				return gatewayLookupResult{gateway: gateway, err: err}, nil
			default:
				return gatewayLookupResult{}, err
			}
		}
	})

	select {
	case <-ctx.Done():
		return adapter.Gateway{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return adapter.Gateway{}, res.Err
		}
		out := res.Val.(gatewayLookupResult)
		return out.gateway, out.err
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
	cached, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.devices, key, now, gateway.ID)
	if ok {
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	sfKey := deviceFlightKey(gateway.TenantID, gateway.ID, sourceKey)
	ch := store.cache.sfGroup.DoChan(sfKey, func() (any, error) {
		store.cache.registerFlight(gateway.ID, sfKey)
		defer store.cache.unregisterFlight(gateway.ID, sfKey)

		flightNow := store.clock().UTC()
		if hitVal, hit, epoch := cacheGetOrEpoch(&store.cache, store.cache.devices, key, flightNow, gateway.ID); hit {
			return hitVal, nil
		} else {
			queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uplinkFlightQueryTimeout)
			defer cancel()
			deviceID, err := uncached(queryCtx, gateway, sourceKey)
			if err != nil {
				return "", err
			}
			ttl := uplinkPositiveTTL
			if deviceID == "" {
				ttl = uplinkNegativeTTL
			}
			cacheSetIfEpoch(&store.cache, &store.cache.devices, key, deviceID, flightNow.Add(ttl), flightNow, gateway.ID, epoch)
			return deviceID, nil
		}
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	}
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

func (store *Store) readCachedBindingsLocked(
	gateway adapter.Gateway,
	queries []adapter.DevicePointQuery,
	uniqueSourceKeys []string,
	now time.Time,
) (adapter.ResolvedBindings, []string, []resolvedPointTarget, uint64) {
	result := adapter.ResolvedBindings{
		Devices: make(map[string]string),
		Points:  make(map[string]*adapter.ResolvedPoint),
	}
	cachedDevices := make(map[string]string, len(uniqueSourceKeys))
	var missingSourceKeys []string
	var missingPointTargets []resolvedPointTarget
	pointTargetSeen := make(map[resolvedPointTarget]struct{}, len(queries))

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
	return result, missingSourceKeys, missingPointTargets, epoch
}

func (store *Store) resolveBindingsCachedWith(
	ctx context.Context,
	gateway adapter.Gateway,
	queries []adapter.DevicePointQuery,
	uncached bindingsBatchUncachedFn,
) (adapter.ResolvedBindings, error) {
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
		return adapter.ResolvedBindings{
			Devices: make(map[string]string),
			Points:  make(map[string]*adapter.ResolvedPoint),
		}, nil
	}

	now := store.clock().UTC()
	store.cache.mu.Lock()
	result, missingSourceKeys, missingPointTargets, _ := store.readCachedBindingsLocked(gateway, queries, uniqueSourceKeys, now)
	store.cache.mu.Unlock()

	if len(missingSourceKeys) == 0 && len(missingPointTargets) == 0 {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return adapter.ResolvedBindings{}, err
	}

	sfKey := bindingsFlightKey(gateway.TenantID, gateway.ID, queries)
	ch := store.cache.sfGroup.DoChan(sfKey, func() (any, error) {
		store.cache.registerFlight(gateway.ID, sfKey)
		defer store.cache.unregisterFlight(gateway.ID, sfKey)

		flightNow := store.clock().UTC()
		store.cache.mu.Lock()
		flightResult, flightMissingKeys, flightMissingTargets, epoch := store.readCachedBindingsLocked(gateway, queries, uniqueSourceKeys, flightNow)
		store.cache.mu.Unlock()

		if len(flightMissingKeys) == 0 && len(flightMissingTargets) == 0 {
			return flightResult, nil
		}

		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), uplinkFlightQueryTimeout)
		defer cancel()
		fresh, queriedTargets, err := uncached(queryCtx, gateway, flightMissingKeys, flightMissingTargets, queries)
		if err != nil {
			return adapter.ResolvedBindings{}, err
		}

		for _, sourceKey := range flightMissingKeys {
			deviceID := fresh.Devices[sourceKey]
			ttl := uplinkPositiveTTL
			if deviceID != "" {
				flightResult.Devices[sourceKey] = deviceID
			} else {
				ttl = uplinkNegativeTTL
			}
			cacheSetIfEpoch(
				&store.cache,
				&store.cache.devices,
				deviceCacheKey{tenantID: gateway.TenantID, gatewayID: gateway.ID, sourceKey: sourceKey},
				deviceID,
				flightNow.Add(ttl),
				flightNow,
				gateway.ID,
				epoch,
			)
		}
		for _, target := range queriedTargets {
			bindingKey := adapter.PointBindingKey(target.deviceID, target.pointCode)
			point := fresh.Points[bindingKey]
			ttl := uplinkPositiveTTL
			if point != nil {
				flightResult.Points[bindingKey] = cloneResolvedPoint(point)
			} else {
				ttl = uplinkNegativeTTL
			}
			cacheSetIfEpoch(
				&store.cache,
				&store.cache.points,
				pointCacheKey{tenantID: gateway.TenantID, deviceID: target.deviceID, pointCode: target.pointCode},
				cloneResolvedPoint(point),
				flightNow.Add(ttl),
				flightNow,
				gateway.ID,
				epoch,
			)
		}
		return flightResult, nil
	})

	select {
	case <-ctx.Done():
		return adapter.ResolvedBindings{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return adapter.ResolvedBindings{}, res.Err
		}
		return cloneResolvedBindings(res.Val.(adapter.ResolvedBindings)), nil
	}
}
