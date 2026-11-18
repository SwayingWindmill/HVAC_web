package telemetry

import (
	"container/list"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	metadataCacheShards     = 64
	defaultMetadataCacheTTL = 30 * time.Second
	defaultMetadataShardCap = 256

	fnv64Offset = 14695981039346656037
	fnv64Prime  = 1099511628211
)

type metadataFactKind uint8

const (
	metadataKindDevice metadataFactKind = iota + 1
	metadataKindPoint
	metadataKindFreshnessPolicy
	metadataKindPresencePolicy
	metadataKindDeviceScope
)

type metadataCacheKey struct {
	kind     metadataFactKind
	tenantID string
	siteID   string
	deviceID string
	pointKey string
}

type cachedFreshnessPolicy struct {
	Revision           int64
	FreshWithinSeconds int
	Configured         bool
	ValueType          string
	Unit               *string
	MinimumNumber      *float64
	MaximumNumber      *float64
	QualityChecks      *QualityChecks
}

type cachedPresencePolicy struct {
	Revision            int64
	OnlineWithin        time.Duration
	OfflineAfter        time.Duration
	AcceptedSignalTypes []string
	MaxFutureClockSkew  time.Duration
	MaxSourceLag        time.Duration
}

type cachedDeviceScope struct {
	tenantID string
	siteID   string
}

type metadataCacheEntry struct {
	key       metadataCacheKey
	tenantID  string
	siteID    string
	expiresAt time.Time
	elem      *list.Element
	device    *ResolvedDevice
	point     *ResolvedPoint
	freshness *cachedFreshnessPolicy
	presence  *cachedPresencePolicy
	scope     *cachedDeviceScope
}

type metadataCacheShard struct {
	mu      sync.RWMutex
	entries map[metadataCacheKey]*metadataCacheEntry
	order   *list.List
}

// metadataCache is a 64-shard FNV-1a TTL cache for telemetry ingest metadata
// (devices, points, freshness policies, and presence policies).
type metadataCache struct {
	shards      [metadataCacheShards]metadataCacheShard
	ttl         time.Duration
	maxPerShard int
	now         func() time.Time
	epoch       atomic.Uint64
}

func newMetadataCache() *metadataCache {
	return newMetadataCacheWithConfig(defaultMetadataCacheTTL, defaultMetadataShardCap, time.Now)
}

func newMetadataCacheWithConfig(ttl time.Duration, maxPerShard int, now func() time.Time) *metadataCache {
	if ttl <= 0 {
		ttl = defaultMetadataCacheTTL
	}
	if maxPerShard <= 0 {
		maxPerShard = defaultMetadataShardCap
	}
	if now == nil {
		now = time.Now
	}
	cache := &metadataCache{
		ttl:         ttl,
		maxPerShard: maxPerShard,
		now:         now,
	}
	for i := range cache.shards {
		cache.shards[i] = metadataCacheShard{
			entries: make(map[metadataCacheKey]*metadataCacheEntry),
			order:   list.New(),
		}
	}
	return cache
}

func normalizeScopeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func fnv1aHashMetadataKey(key metadataCacheKey) uint64 {
	h := uint64(fnv64Offset)
	h = fnv1aWriteByte(h, byte(key.kind))
	h = fnv1aWriteByte(h, 0)
	h = fnv1aWriteString(h, key.tenantID)
	h = fnv1aWriteByte(h, 0)
	h = fnv1aWriteString(h, key.siteID)
	h = fnv1aWriteByte(h, 0)
	h = fnv1aWriteString(h, key.deviceID)
	h = fnv1aWriteByte(h, 0)
	h = fnv1aWriteString(h, key.pointKey)
	return h
}

func fnv1aWriteByte(h uint64, b byte) uint64 {
	h ^= uint64(b)
	h *= fnv64Prime
	return h
}

func fnv1aWriteString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnv64Prime
	}
	return h
}

func (c *metadataCache) shardFor(key metadataCacheKey) *metadataCacheShard {
	return &c.shards[fnv1aHashMetadataKey(key)%metadataCacheShards]
}

func (c *metadataCache) Epoch() uint64 {
	if c == nil {
		return 0
	}
	return c.epoch.Load()
}

func (c *metadataCache) GetDevice(tenantID, siteID, deviceID string) (ResolvedDevice, bool) {
	if c == nil {
		return ResolvedDevice{}, false
	}
	key := metadataCacheKey{
		kind:     metadataKindDevice,
		tenantID: normalizeScopeID(tenantID),
		siteID:   normalizeScopeID(siteID),
		deviceID: normalizeScopeID(deviceID),
	}
	shard := c.shardFor(key)
	now := c.now().UTC()
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	if !ok || entry.device == nil || !now.Before(entry.expiresAt) {
		shard.mu.RUnlock()
		return ResolvedDevice{}, false
	}
	device := *entry.device
	shard.mu.RUnlock()
	return device, true
}

func (c *metadataCache) LookupDeviceScope(deviceID string) (string, string, bool) {
	if c == nil {
		return "", "", false
	}
	key := metadataCacheKey{
		kind:     metadataKindDeviceScope,
		deviceID: normalizeScopeID(deviceID),
	}
	shard := c.shardFor(key)
	now := c.now().UTC()
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	if !ok || entry.scope == nil || !now.Before(entry.expiresAt) {
		shard.mu.RUnlock()
		return "", "", false
	}
	tenantID, siteID := entry.scope.tenantID, entry.scope.siteID
	shard.mu.RUnlock()
	return tenantID, siteID, true
}

func (c *metadataCache) PutDeviceIfEpoch(device ResolvedDevice, epoch uint64) {
	c.putDevice(device, epoch, true)
}

func (c *metadataCache) putDevice(device ResolvedDevice, epoch uint64, checkEpoch bool) {
	if c == nil {
		return
	}
	tenantID := normalizeScopeID(device.TenantID)
	siteID := normalizeScopeID(device.SiteID)
	deviceID := normalizeScopeID(device.DeviceID)
	if tenantID == "" || siteID == "" || deviceID == "" {
		return
	}
	normalized := ResolvedDevice{TenantID: device.TenantID, SiteID: device.SiteID, DeviceID: device.DeviceID}
	c.putEntry(metadataCacheKey{
		kind:     metadataKindDevice,
		tenantID: tenantID,
		siteID:   siteID,
		deviceID: deviceID,
	}, tenantID, siteID, epoch, checkEpoch, func(entry *metadataCacheEntry) {
		copied := normalized
		entry.device = &copied
	})
	c.putEntry(metadataCacheKey{
		kind:     metadataKindDeviceScope,
		deviceID: deviceID,
	}, tenantID, siteID, epoch, checkEpoch, func(entry *metadataCacheEntry) {
		entry.scope = &cachedDeviceScope{tenantID: tenantID, siteID: siteID}
	})
}

func (c *metadataCache) GetPoint(tenantID, siteID, deviceID, pointKey string) (ResolvedPoint, bool) {
	if c == nil {
		return ResolvedPoint{}, false
	}
	key := metadataCacheKey{
		kind:     metadataKindPoint,
		tenantID: normalizeScopeID(tenantID),
		siteID:   normalizeScopeID(siteID),
		deviceID: normalizeScopeID(deviceID),
		pointKey: pointKey,
	}
	shard := c.shardFor(key)
	now := c.now().UTC()
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	if !ok || entry.point == nil || !now.Before(entry.expiresAt) {
		shard.mu.RUnlock()
		return ResolvedPoint{}, false
	}
	point := cloneResolvedPoint(*entry.point)
	shard.mu.RUnlock()
	return point, true
}

func (c *metadataCache) PutPointIfEpoch(tenantID, siteID, deviceID, pointKey string, point ResolvedPoint, epoch uint64) {
	c.putPoint(tenantID, siteID, deviceID, pointKey, point, epoch, true)
}

func (c *metadataCache) putPoint(tenantID, siteID, deviceID, pointKey string, point ResolvedPoint, epoch uint64, checkEpoch bool) {
	if c == nil {
		return
	}
	tID := normalizeScopeID(tenantID)
	sID := normalizeScopeID(siteID)
	dID := normalizeScopeID(deviceID)
	if tID == "" || sID == "" || dID == "" || pointKey == "" {
		return
	}
	cloned := cloneResolvedPoint(point)
	c.putEntry(metadataCacheKey{
		kind:     metadataKindPoint,
		tenantID: tID,
		siteID:   sID,
		deviceID: dID,
		pointKey: pointKey,
	}, tID, sID, epoch, checkEpoch, func(entry *metadataCacheEntry) {
		copied := cloned
		entry.point = &copied
	})
}

func (c *metadataCache) InvalidatePoint(tenantID, siteID, deviceID, pointKey string) {
	if c == nil {
		return
	}
	c.epoch.Add(1)
	key := metadataCacheKey{
		kind:     metadataKindPoint,
		tenantID: normalizeScopeID(tenantID),
		siteID:   normalizeScopeID(siteID),
		deviceID: normalizeScopeID(deviceID),
		pointKey: pointKey,
	}
	shard := c.shardFor(key)
	shard.mu.Lock()
	if entry, ok := shard.entries[key]; ok {
		shard.removeEntryLocked(entry)
	}
	shard.mu.Unlock()
}

func (c *metadataCache) GetFreshnessPolicy(tenantID, siteID, deviceID, pointKey string) (cachedFreshnessPolicy, bool) {
	if c == nil {
		return cachedFreshnessPolicy{}, false
	}
	key := metadataCacheKey{
		kind:     metadataKindFreshnessPolicy,
		tenantID: normalizeScopeID(tenantID),
		siteID:   normalizeScopeID(siteID),
		deviceID: normalizeScopeID(deviceID),
		pointKey: pointKey,
	}
	shard := c.shardFor(key)
	now := c.now().UTC()
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	if !ok || entry.freshness == nil || !now.Before(entry.expiresAt) {
		shard.mu.RUnlock()
		return cachedFreshnessPolicy{}, false
	}
	policy := cloneCachedFreshnessPolicy(*entry.freshness)
	shard.mu.RUnlock()
	return policy, true
}

func (c *metadataCache) PutFreshnessPolicyIfEpoch(tenantID, siteID, deviceID, pointKey string, policy cachedFreshnessPolicy, epoch uint64) {
	c.putFreshnessPolicy(tenantID, siteID, deviceID, pointKey, policy, epoch, true)
}

func (c *metadataCache) putFreshnessPolicy(tenantID, siteID, deviceID, pointKey string, policy cachedFreshnessPolicy, epoch uint64, checkEpoch bool) {
	if c == nil {
		return
	}
	tID := normalizeScopeID(tenantID)
	sID := normalizeScopeID(siteID)
	dID := normalizeScopeID(deviceID)
	if tID == "" || sID == "" || dID == "" || pointKey == "" {
		return
	}
	cloned := cloneCachedFreshnessPolicy(policy)
	c.putEntry(metadataCacheKey{
		kind:     metadataKindFreshnessPolicy,
		tenantID: tID,
		siteID:   sID,
		deviceID: dID,
		pointKey: pointKey,
	}, tID, sID, epoch, checkEpoch, func(entry *metadataCacheEntry) {
		copied := cloned
		entry.freshness = &copied
	})
}

func (c *metadataCache) GetPresencePolicy(tenantID, siteID, deviceID string) (cachedPresencePolicy, bool) {
	if c == nil {
		return cachedPresencePolicy{}, false
	}
	key := metadataCacheKey{
		kind:     metadataKindPresencePolicy,
		tenantID: normalizeScopeID(tenantID),
		siteID:   normalizeScopeID(siteID),
		deviceID: normalizeScopeID(deviceID),
	}
	shard := c.shardFor(key)
	now := c.now().UTC()
	shard.mu.RLock()
	entry, ok := shard.entries[key]
	if !ok || entry.presence == nil || !now.Before(entry.expiresAt) {
		shard.mu.RUnlock()
		return cachedPresencePolicy{}, false
	}
	policy := cloneCachedPresencePolicy(*entry.presence)
	shard.mu.RUnlock()
	return policy, true
}

func (c *metadataCache) PutPresencePolicyIfEpoch(tenantID, siteID, deviceID string, policy cachedPresencePolicy, epoch uint64) {
	c.putPresencePolicy(tenantID, siteID, deviceID, policy, epoch, true)
}

func (c *metadataCache) putPresencePolicy(tenantID, siteID, deviceID string, policy cachedPresencePolicy, epoch uint64, checkEpoch bool) {
	if c == nil {
		return
	}
	tID := normalizeScopeID(tenantID)
	sID := normalizeScopeID(siteID)
	dID := normalizeScopeID(deviceID)
	if tID == "" || sID == "" || dID == "" {
		return
	}
	cloned := cloneCachedPresencePolicy(policy)
	c.putEntry(metadataCacheKey{
		kind:     metadataKindPresencePolicy,
		tenantID: tID,
		siteID:   sID,
		deviceID: dID,
	}, tID, sID, epoch, checkEpoch, func(entry *metadataCacheEntry) {
		copied := cloned
		entry.presence = &copied
	})
}

func (c *metadataCache) putEntry(
	key metadataCacheKey,
	tenantID, siteID string,
	expectedEpoch uint64,
	checkEpoch bool,
	fill func(*metadataCacheEntry),
) {
	shard := c.shardFor(key)
	now := c.now().UTC()
	expiresAt := now.Add(c.ttl)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if checkEpoch && c.epoch.Load() != expectedEpoch {
		return
	}
	if existing, ok := shard.entries[key]; ok {
		existing.tenantID = tenantID
		existing.siteID = siteID
		existing.expiresAt = expiresAt
		fill(existing)
		shard.order.MoveToBack(existing.elem)
		return
	}
	if len(shard.entries) >= c.maxPerShard {
		shard.evictExpiredLocked(now)
		for len(shard.entries) >= c.maxPerShard {
			front := shard.order.Front()
			if front == nil {
				break
			}
			oldest := front.Value.(*metadataCacheEntry)
			shard.removeEntryLocked(oldest)
		}
	}
	entry := &metadataCacheEntry{
		key:       key,
		tenantID:  tenantID,
		siteID:    siteID,
		expiresAt: expiresAt,
	}
	fill(entry)
	entry.elem = shard.order.PushBack(entry)
	shard.entries[key] = entry
}

func (s *metadataCacheShard) evictExpiredLocked(now time.Time) {
	for elem := s.order.Front(); elem != nil; {
		next := elem.Next()
		entry := elem.Value.(*metadataCacheEntry)
		if !now.Before(entry.expiresAt) {
			s.removeEntryLocked(entry)
		}
		elem = next
	}
}

func (s *metadataCacheShard) removeEntryLocked(entry *metadataCacheEntry) {
	delete(s.entries, entry.key)
	if entry.elem != nil {
		s.order.Remove(entry.elem)
		entry.elem = nil
	}
}

// InvalidateSite clears all cached device, point, and policy facts belonging to (tenantID, siteID).
func (c *metadataCache) InvalidateSite(tenantID, siteID string) {
	c.InvalidateSiteDevices(tenantID, siteID, nil)
}

// InvalidateSiteDevices clears cached entries for (tenantID, siteID) plus any prior-scope
// entries matching deviceIDs (for example, when a device moves across sites or retires).
func (c *metadataCache) InvalidateSiteDevices(tenantID, siteID string, deviceIDs []string) {
	if c == nil {
		return
	}
	tID := normalizeScopeID(tenantID)
	sID := normalizeScopeID(siteID)
	var devSet map[string]struct{}
	if len(deviceIDs) > 0 {
		devSet = make(map[string]struct{}, len(deviceIDs))
		for _, rawID := range deviceIDs {
			if normalized := normalizeScopeID(rawID); normalized != "" {
				devSet[normalized] = struct{}{}
			}
		}
	}
	if (tID == "" || sID == "") && len(devSet) == 0 {
		return
	}
	// Increment epoch before locking shards so concurrent miss-population checks ordered after
	// shard lock acquisition reject reads that started before invalidation.
	c.epoch.Add(1)
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		for elem := shard.order.Front(); elem != nil; {
			next := elem.Next()
			entry := elem.Value.(*metadataCacheEntry)
			matchesSite := tID != "" && sID != "" && entry.tenantID == tID && entry.siteID == sID
			_, matchesDevice := devSet[entry.key.deviceID]
			if matchesSite || matchesDevice {
				shard.removeEntryLocked(entry)
			}
			elem = next
		}
		shard.mu.Unlock()
	}
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneResolvedPoint(point ResolvedPoint) ResolvedPoint {
	point.SensorID = cloneString(point.SensorID)
	point.Unit = cloneString(point.Unit)
	point.CounterDecreaseMode = cloneString(point.CounterDecreaseMode)
	point.CounterRolloverModulus = clonePtr(point.CounterRolloverModulus)
	return point
}

func cloneCachedFreshnessPolicy(policy cachedFreshnessPolicy) cachedFreshnessPolicy {
	policy.Unit = cloneString(policy.Unit)
	policy.MinimumNumber = clonePtr(policy.MinimumNumber)
	policy.MaximumNumber = clonePtr(policy.MaximumNumber)
	policy.QualityChecks = cloneQualityChecks(policy.QualityChecks)
	return policy
}

func cloneCachedPresencePolicy(policy cachedPresencePolicy) cachedPresencePolicy {
	policy.AcceptedSignalTypes = slices.Clone(policy.AcceptedSignalTypes)
	return policy
}

func cloneQualityChecks(checks *QualityChecks) *QualityChecks {
	if checks == nil {
		return nil
	}
	cloned := *checks
	cloned.MaxStep = clonePtr(checks.MaxStep)
	cloned.Sentinels = slices.Clone(checks.Sentinels)
	cloned.Running = slices.Clone(checks.Running)
	cloned.Inputs = slices.Clone(checks.Inputs)
	if checks.Physics != nil {
		cloned.Physics = make([]PhysicsRule, len(checks.Physics))
		for i, rule := range checks.Physics {
			clonedRule := rule
			clonedRule.MarginK = clonePtr(rule.MarginK)
			clonedRule.Fraction = clonePtr(rule.Fraction)
			clonedRule.EpsilonK = clonePtr(rule.EpsilonK)
			clonedRule.Peers = slices.Clone(rule.Peers)
			clonedRule.Running = slices.Clone(rule.Running)
			cloned.Physics[i] = clonedRule
		}
	}
	if checks.Uncertainty != nil {
		clonedUnc := *checks.Uncertainty
		clonedUnc.Flow = clonePtr(checks.Uncertainty.Flow)
		clonedUnc.Power = clonePtr(checks.Uncertainty.Power)
		cloned.Uncertainty = &clonedUnc
	}
	return &cloned
}
