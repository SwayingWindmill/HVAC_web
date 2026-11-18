package telemetry

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sync"
	"testing"
	"time"
)

func TestMetadataCacheFNV1aShardingMatchesStdlib(t *testing.T) {
	keys := []metadataCacheKey{
		{kind: metadataKindDevice, tenantID: "t1", siteID: "s1", deviceID: "d1"},
		{kind: metadataKindPoint, tenantID: "t1", siteID: "s1", deviceID: "d1", pointKey: "zone.temp"},
		{kind: metadataKindFreshnessPolicy, tenantID: "t1", siteID: "s1", deviceID: "d1", pointKey: "zone.temp"},
		{kind: metadataKindPresencePolicy, tenantID: "t1", siteID: "s1", deviceID: "d1"},
	}
	for _, key := range keys {
		h := fnv.New64a()
		_, _ = h.Write([]byte{byte(key.kind), 0})
		_, _ = h.Write([]byte(key.tenantID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(key.siteID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(key.deviceID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(key.pointKey))
		if got, want := fnv1aHashMetadataKey(key), h.Sum64(); got != want {
			t.Fatalf("fnv1aHashMetadataKey(%+v)=%d, want %d", key, got, want)
		}
	}

	cache := newMetadataCache()
	if cache.ttl != 30*time.Second || cache.maxPerShard != 256 || len(cache.shards) != 64 {
		t.Fatalf("unexpected defaults: ttl=%s maxPerShard=%d shards=%d", cache.ttl, cache.maxPerShard, len(cache.shards))
	}
}

func TestMetadataCacheTTLExpiry(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cache := newMetadataCacheWithConfig(30*time.Second, 256, func() time.Time { return now })

	tenantID := "018f2d00-0000-7000-8000-000000000001"
	siteID := "018f2e00-1000-7000-8000-000000000001"
	deviceID := "018f2e00-3000-7000-8000-000000000001"

	cache.PutDevice(ResolvedDevice{TenantID: tenantID, SiteID: siteID, DeviceID: deviceID})
	if got, ok := cache.GetDevice(tenantID, siteID, deviceID); !ok || got.DeviceID != deviceID {
		t.Fatalf("expected immediate hit, got %+v ok=%v", got, ok)
	}
	if gotT, gotS, ok := cache.LookupDeviceScope(deviceID); !ok || gotT != tenantID || gotS != siteID {
		t.Fatalf("expected scope lookup hit, got %q/%q ok=%v", gotT, gotS, ok)
	}

	now = now.Add(29 * time.Second)
	if _, ok := cache.GetDevice(tenantID, siteID, deviceID); !ok {
		t.Fatal("expected hit before 30s TTL")
	}

	now = now.Add(time.Second)
	if _, ok := cache.GetDevice(tenantID, siteID, deviceID); ok {
		t.Fatal("expected miss at 30s TTL expiry")
	}
	if _, _, ok := cache.LookupDeviceScope(deviceID); ok {
		t.Fatal("expected scope lookup miss at 30s TTL expiry")
	}
}

func TestMetadataCacheEvictsExpiredThenOldestAtShardCapacity(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cache := newMetadataCacheWithConfig(30*time.Second, 256, func() time.Time { return now })

	tenantID := "018f2d00-0000-7000-8000-000000000001"
	siteID := "018f2e00-1000-7000-8000-000000000001"
	deviceID := "018f2e00-3000-7000-8000-000000000001"

	// Find 360 point keys that hash into the exact same shard so we test the 256-entry shard cap.
	targetShard := int(fnv1aHashMetadataKey(metadataCacheKey{
		kind:     metadataKindPoint,
		tenantID: tenantID,
		siteID:   siteID,
		deviceID: deviceID,
		pointKey: "seed",
	}) % metadataCacheShards)

	sameShardKeys := make([]string, 0, 360)
	for i := 0; len(sameShardKeys) < 360; i++ {
		k := fmt.Sprintf("p.%d", i)
		s := int(fnv1aHashMetadataKey(metadataCacheKey{
			kind:     metadataKindPoint,
			tenantID: tenantID,
			siteID:   siteID,
			deviceID: deviceID,
			pointKey: k,
		}) % metadataCacheShards)
		if s == targetShard {
			sameShardKeys = append(sameShardKeys, k)
		}
	}

	// Insert 100 entries that expire at t+30s, then advance 10s and insert 156 live entries (expires t+40s).
	for i := 0; i < 100; i++ {
		cache.PutPoint(tenantID, siteID, deviceID, sameShardKeys[i], ResolvedPoint{
			PointID:       fmt.Sprintf("018f2e00-3100-7000-8000-%012d", i+1),
			PointType:     "TELEMETRY",
			ValueType:     "NUMBER",
			PointRevision: int64(i + 1),
		})
	}
	now = now.Add(10 * time.Second)
	for i := 100; i < 256; i++ {
		cache.PutPoint(tenantID, siteID, deviceID, sameShardKeys[i], ResolvedPoint{
			PointID:       fmt.Sprintf("018f2e00-3100-7000-8000-%012d", i+1),
			PointType:     "TELEMETRY",
			ValueType:     "NUMBER",
			PointRevision: int64(i + 1),
		})
	}
	shard := &cache.shards[targetShard]
	if got := len(shard.entries); got != 256 {
		t.Fatalf("shard entries=%d, want 256", got)
	}

	// Advance to t+30s so the first 100 entries expire while the 156 newer entries remain live.
	// Inserting 1 new entry must evict all expired entries first without touching live entry 100.
	now = now.Add(20 * time.Second)
	cache.PutPoint(tenantID, siteID, deviceID, sameShardKeys[256], ResolvedPoint{
		PointID:       "018f2e00-3100-7000-8000-000000009999",
		PointType:     "TELEMETRY",
		ValueType:     "NUMBER",
		PointRevision: 999,
	})
	if got := len(shard.entries); got != 157 {
		t.Fatalf("expected expired entries evicted first leaving 157 entries, got %d", got)
	}
	if _, ok := cache.GetPoint(tenantID, siteID, deviceID, sameShardKeys[100]); !ok {
		t.Fatal("live entry evicted when expired entries existed")
	}

	// Now fill the shard back to 256 live entries and insert 3 more: oldest live entries must evict
	// so shard length stays bounded <= 256.
	for i := 257; i < 356; i++ {
		cache.PutPoint(tenantID, siteID, deviceID, sameShardKeys[i], ResolvedPoint{
			PointID:       fmt.Sprintf("018f2e00-3200-7000-8000-%012d", i+1),
			PointType:     "TELEMETRY",
			ValueType:     "NUMBER",
			PointRevision: 1,
		})
	}
	if len(shard.entries) != 256 {
		t.Fatalf("shard entries=%d, want 256", len(shard.entries))
	}
	for i := 356; i < 359; i++ {
		cache.PutPoint(tenantID, siteID, deviceID, sameShardKeys[i], ResolvedPoint{
			PointID: "018f2e00-3300-7000-8000-000000000001", PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1,
		})
		if len(shard.entries) > 256 {
			t.Fatalf("shard exceeded cap 256: %d", len(shard.entries))
		}
	}
	if len(shard.entries) != 256 {
		t.Fatalf("shard entries=%d, want 256", len(shard.entries))
	}
	// The 3 oldest live entries (indices 100, 101, 102) must have been evicted, while index 103 remains.
	for _, idx := range []int{100, 101, 102} {
		if _, ok := cache.GetPoint(tenantID, siteID, deviceID, sameShardKeys[idx]); ok {
			t.Fatalf("expected oldest live entry %d (%s) to be evicted", idx, sameShardKeys[idx])
		}
	}
	if _, ok := cache.GetPoint(tenantID, siteID, deviceID, sameShardKeys[103]); !ok {
		t.Fatal("expected newer live entry 103 to remain cached")
	}
}

func TestMetadataCacheDeepCopyIsolation(t *testing.T) {
	cache := newMetadataCache()
	tenantID := "018f2d00-0000-7000-8000-000000000001"
	siteID := "018f2e00-1000-7000-8000-000000000001"
	deviceID := "018f2e00-3000-7000-8000-000000000001"

	sensor := "018f2e00-3200-7000-8000-000000000001"
	unit := "kWh"
	mode := "ROLLOVER"
	modulus := 10000.0
	inputPoint := ResolvedPoint{
		PointID:                "018f2e00-3100-7000-8000-000000000001",
		SensorID:               &sensor,
		PointType:              "COUNTER",
		ValueType:              "NUMBER",
		Unit:                   &unit,
		CounterDecreaseMode:    &mode,
		CounterRolloverModulus: &modulus,
		PointRevision:          3,
	}
	cache.PutPoint(tenantID, siteID, deviceID, "energy.total", inputPoint)
	// Mutate caller pointers after Put.
	*inputPoint.SensorID = "mutated"
	*inputPoint.Unit = "MWh"
	*inputPoint.CounterDecreaseMode = "INVALID"
	*inputPoint.CounterRolloverModulus = 1.0

	gotPoint, ok := cache.GetPoint(tenantID, siteID, deviceID, "energy.total")
	if !ok {
		t.Fatal("expected point cache hit")
	}
	if *gotPoint.SensorID != "018f2e00-3200-7000-8000-000000000001" || *gotPoint.Unit != "kWh" || *gotPoint.CounterDecreaseMode != "ROLLOVER" || *gotPoint.CounterRolloverModulus != 10000.0 {
		t.Fatalf("caller mutation leaked into cached point: %+v", gotPoint)
	}
	// Mutate pointer returned by GetPoint and verify cache stays clean.
	*gotPoint.Unit = "W"
	gotPoint2, _ := cache.GetPoint(tenantID, siteID, deviceID, "energy.total")
	if *gotPoint2.Unit != "kWh" {
		t.Fatalf("read mutation leaked back into cached point: %q", *gotPoint2.Unit)
	}

	minNum := 0.0
	maxNum := 100.0
	maxStep := 5.0
	margin := 2.0
	fraction := 0.8
	epsilon := 0.5
	flowPeer := PointRef{DeviceID: deviceID, Key: "flow"}
	inputFP := cachedFreshnessPolicy{
		Revision:           4,
		FreshWithinSeconds: 300,
		Configured:         true,
		ValueType:          "NUMBER",
		Unit:               &unit,
		MinimumNumber:      &minNum,
		MaximumNumber:      &maxNum,
		QualityChecks: &QualityChecks{
			Copied:    true,
			MaxStep:   &maxStep,
			Sentinels: []float64{38.4, 999.9},
			Running:   []OperatingExpect{{DeviceID: deviceID, Key: "run", Equals: "true"}},
			Inputs:    []PointRef{{DeviceID: deviceID, Key: "in"}},
			Physics: []PhysicsRule{{
				Kind:     "cop_carnot",
				MarginK:  &margin,
				Fraction: &fraction,
				EpsilonK: &epsilon,
				Peers:    []PhysicsPeer{{Role: "power", DeviceID: deviceID, Key: "power"}},
				Running:  []OperatingExpect{{DeviceID: deviceID, Key: "run", Equals: "true"}},
			}},
			Uncertainty: &UncertaintyCheck{
				Kind: "cooling",
				Flow: &flowPeer,
			},
		},
	}
	cache.PutFreshnessPolicy(tenantID, siteID, deviceID, "chiller.power", inputFP)
	*inputFP.MinimumNumber = -999
	*inputFP.QualityChecks.MaxStep = 999
	inputFP.QualityChecks.Sentinels[0] = -1
	inputFP.QualityChecks.Physics[0].Peers[0].Key = "corrupted"
	inputFP.QualityChecks.Uncertainty.Flow.Key = "corrupted"

	gotFP, ok := cache.GetFreshnessPolicy(tenantID, siteID, deviceID, "chiller.power")
	if !ok {
		t.Fatal("expected freshness policy cache hit")
	}
	if *gotFP.MinimumNumber != 0 || *gotFP.QualityChecks.MaxStep != 5 || gotFP.QualityChecks.Sentinels[0] != 38.4 ||
		gotFP.QualityChecks.Physics[0].Peers[0].Key != "power" || gotFP.QualityChecks.Uncertainty.Flow.Key != "flow" {
		t.Fatalf("caller mutation leaked into cached freshness policy: %+v", gotFP)
	}
	gotFP.QualityChecks.Sentinels[1] = -2
	gotFP2, _ := cache.GetFreshnessPolicy(tenantID, siteID, deviceID, "chiller.power")
	if gotFP2.QualityChecks.Sentinels[1] != 999.9 {
		t.Fatalf("read mutation leaked into cached freshness policy: %v", gotFP2.QualityChecks.Sentinels)
	}

	inputPP := cachedPresencePolicy{
		Revision:            2,
		OnlineWithin:        2 * time.Minute,
		OfflineAfter:        5 * time.Minute,
		AcceptedSignalTypes: []string{"SOURCE_ACTIVITY", "EXPLICIT_CONNECT"},
		MaxFutureClockSkew:  30 * time.Second,
		MaxSourceLag:        10 * time.Minute,
	}
	cache.PutPresencePolicy(tenantID, siteID, deviceID, inputPP)
	inputPP.AcceptedSignalTypes[0] = "MUTATED"
	gotPP, ok := cache.GetPresencePolicy(tenantID, siteID, deviceID)
	if !ok || gotPP.AcceptedSignalTypes[0] != "SOURCE_ACTIVITY" {
		t.Fatalf("caller mutation leaked into cached presence policy: %+v ok=%v", gotPP, ok)
	}
	gotPP.AcceptedSignalTypes[1] = "MUTATED"
	gotPP2, _ := cache.GetPresencePolicy(tenantID, siteID, deviceID)
	if gotPP2.AcceptedSignalTypes[1] != "EXPLICIT_CONNECT" {
		t.Fatalf("read mutation leaked into cached presence policy: %v", gotPP2.AcceptedSignalTypes)
	}
}

func TestMetadataCacheInvalidateSiteAndConcurrency(t *testing.T) {
	cache := newMetadataCache()
	tenantA := "018f2d00-0000-7000-8000-000000000001"
	siteA1 := "018f2e00-1000-7000-8000-000000000001"
	siteA2 := "018f2e00-1000-7000-8000-000000000002"
	devA1 := "018f2e00-3000-7000-8000-000000000001"
	devA2 := "018f2e00-3000-7000-8000-000000000002"

	cache.PutDevice(ResolvedDevice{TenantID: tenantA, SiteID: siteA1, DeviceID: devA1})
	cache.PutPoint(tenantA, siteA1, devA1, "temp", ResolvedPoint{PointID: "p1", PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1})
	cache.PutFreshnessPolicy(tenantA, siteA1, devA1, "temp", cachedFreshnessPolicy{Revision: 1, FreshWithinSeconds: 300, Configured: true, ValueType: "NUMBER"})
	cache.PutPresencePolicy(tenantA, siteA1, devA1, cachedPresencePolicy{Revision: 1, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute})

	cache.PutDevice(ResolvedDevice{TenantID: tenantA, SiteID: siteA2, DeviceID: devA2})
	cache.PutPoint(tenantA, siteA2, devA2, "temp", ResolvedPoint{PointID: "p2", PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1})
	cache.PutFreshnessPolicy(tenantA, siteA2, devA2, "temp", cachedFreshnessPolicy{Revision: 1, FreshWithinSeconds: 300, Configured: true, ValueType: "NUMBER"})
	cache.PutPresencePolicy(tenantA, siteA2, devA2, cachedPresencePolicy{Revision: 1, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute})

	// Uppercase UUID input on invalidation must still match normalized lowercase entries.
	cache.InvalidateSite("018F2D00-0000-7000-8000-000000000001", "018F2E00-1000-7000-8000-000000000001")

	if _, ok := cache.GetDevice(tenantA, siteA1, devA1); ok {
		t.Fatal("siteA1 device remained cached after InvalidateSite")
	}
	if _, _, ok := cache.LookupDeviceScope(devA1); ok {
		t.Fatal("siteA1 device scope remained cached after InvalidateSite")
	}
	if _, ok := cache.GetPoint(tenantA, siteA1, devA1, "temp"); ok {
		t.Fatal("siteA1 point remained cached after InvalidateSite")
	}
	if _, ok := cache.GetFreshnessPolicy(tenantA, siteA1, devA1, "temp"); ok {
		t.Fatal("siteA1 freshness policy remained cached after InvalidateSite")
	}
	if _, ok := cache.GetPresencePolicy(tenantA, siteA1, devA1); ok {
		t.Fatal("siteA1 presence policy remained cached after InvalidateSite")
	}

	// Site A2 must remain untouched.
	if _, ok := cache.GetDevice(tenantA, siteA2, devA2); !ok {
		t.Fatal("siteA2 device was wrongly invalidated")
	}
	if _, ok := cache.GetPoint(tenantA, siteA2, devA2, "temp"); !ok {
		t.Fatal("siteA2 point was wrongly invalidated")
	}
	if _, ok := cache.GetFreshnessPolicy(tenantA, siteA2, devA2, "temp"); !ok {
		t.Fatal("siteA2 freshness policy was wrongly invalidated")
	}
	if _, ok := cache.GetPresencePolicy(tenantA, siteA2, devA2); !ok {
		t.Fatal("siteA2 presence policy was wrongly invalidated")
	}

	// Verify epoch protection prevents stale inflight reads from repopulating after InvalidateSite.
	epochBefore := cache.Epoch()
	cache.InvalidateSite(tenantA, siteA2)
	cache.PutDeviceIfEpoch(ResolvedDevice{TenantID: tenantA, SiteID: siteA2, DeviceID: devA2}, epochBefore)
	if _, ok := cache.GetDevice(tenantA, siteA2, devA2); ok {
		t.Fatal("stale pre-invalidation read populated cache despite epoch mismatch")
	}

	// Concurrent reads, writes, and site invalidations must be race-free.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				dev := fmt.Sprintf("018f2e00-3000-7000-8000-%012d", (worker*10+i)%32)
				site := fmt.Sprintf("018f2e00-1000-7000-8000-%012d", i%4)
				key := fmt.Sprintf("k.%d", i%16)
				cache.PutDevice(ResolvedDevice{TenantID: tenantA, SiteID: site, DeviceID: dev})
				cache.PutPoint(tenantA, site, dev, key, ResolvedPoint{PointID: dev, PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: int64(i + 1)})
				cache.PutFreshnessPolicy(tenantA, site, dev, key, cachedFreshnessPolicy{
					Revision: int64(i + 1), FreshWithinSeconds: 60, Configured: true, ValueType: "NUMBER",
					QualityChecks: &QualityChecks{Sentinels: []float64{1, 2}},
				})
				cache.PutPresencePolicy(tenantA, site, dev, cachedPresencePolicy{
					Revision: 1, AcceptedSignalTypes: []string{"SOURCE_ACTIVITY"}, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: time.Minute,
				})
				_, _ = cache.GetDevice(tenantA, site, dev)
				_, _ = cache.GetPoint(tenantA, site, dev, key)
				if fp, ok := cache.GetFreshnessPolicy(tenantA, site, dev, key); ok && fp.QualityChecks != nil && len(fp.QualityChecks.Sentinels) > 0 {
					fp.QualityChecks.Sentinels[0] = -999
				}
				_, _ = cache.GetPresencePolicy(tenantA, site, dev)
				if i%25 == 0 {
					cache.InvalidateSite(tenantA, site)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestPrefetchBatchPlanUsesCacheHitsAndQueriesMissesOnly(t *testing.T) {
	cache := newMetadataCache()
	tenantID := "018f2d00-0000-7000-8000-000000000001"
	siteID := "018f2e00-1000-7000-8000-000000000001"
	devHit := "018f2e00-3000-7000-8000-000000000001"
	devMiss := "018f2e00-3000-7000-8000-000000000002"
	pointIDHit := "018f2e00-3100-7000-8000-000000000001"
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	candidates := []ObservationCandidate{
		{
			SourceID:           sourceA,
			SourcePath:         SourcePathHistoryReplay,
			ExternalEntityType: "DEVICE",
			ExternalID:         devHit,
			TelemetryKey:       "zone.temp",
			Value:              json.RawMessage(`22.5`),
			ValueType:          "NUMBER",
			SampledAt:          now,
			ReceivedAt:         now,
			Position:           SourcePosition{Partition: "p", Offset: 1, EventID: "018f2e00-8000-7000-8000-000000000001"},
		},
		{
			SourceID:           sourceA,
			SourcePath:         SourcePathPush,
			ExternalEntityType: "DEVICE",
			ExternalID:         devMiss,
			Device:             &ResolvedDevice{TenantID: tenantID, SiteID: siteID, DeviceID: devMiss},
			Point:              &ResolvedPoint{PointID: "018f2e00-3100-7000-8000-000000000002", PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 1},
			TelemetryKey:       "zone.humidity",
			Value:              json.RawMessage(`50`),
			ValueType:          "NUMBER",
			SampledAt:          now,
			ReceivedAt:         now,
			Position:           SourcePosition{Partition: "p", Offset: 2, EventID: "018f2e00-8000-7000-8000-000000000002"},
		},
	}

	// Cold cache: all lookups miss.
	stateCold, planCold := planBatchPrefetch(cache, candidates)
	if len(planCold.missDeviceIDs) != 2 || len(planCold.missReplayPoints) != 1 || len(planCold.missPolicyPoints) != 2 {
		t.Fatalf("cold misses unexpected: devices=%v replay=%v policies=%v", planCold.missDeviceIDs, planCold.missReplayPoints, planCold.missPolicyPoints)
	}

	// Populate devHit metadata as if fetched from Postgres.
	devHitFact := ResolvedDevice{TenantID: tenantID, SiteID: siteID, DeviceID: devHit}
	stateCold.knownDevices[devHit] = devHitFact
	populatePrefetchCache(
		cache,
		planCold.cacheEpoch,
		stateCold,
		[]ResolvedDevice{devHitFact},
		[]fetchedPointFact{{
			key:   deviceKey{deviceID: devHit, key: "zone.temp"},
			point: ResolvedPoint{PointID: pointIDHit, PointType: "TELEMETRY", ValueType: "NUMBER", PointRevision: 2},
		}},
		[]fetchedPolicyFact{{
			key:       deviceKey{deviceID: devHit, key: "zone.temp"},
			freshness: cachedFreshnessPolicy{Revision: 5, FreshWithinSeconds: 300, Configured: true, ValueType: "NUMBER"},
			presence:  cachedPresencePolicy{Revision: 3, MaxFutureClockSkew: 30 * time.Second, MaxSourceLag: 10 * time.Minute},
		}},
	)

	// Next prefetch hits devHit completely (even for HISTORY_REPLAY) and only misses devMiss.
	stateWarm, planWarm := planBatchPrefetch(cache, candidates)
	if len(planWarm.missDeviceIDs) != 1 || planWarm.missDeviceIDs[0] != devMiss {
		t.Fatalf("expected only devMiss in missDeviceIDs, got %v", planWarm.missDeviceIDs)
	}
	if len(planWarm.missReplayPoints) != 0 {
		t.Fatalf("expected 0 replay point misses, got %v", planWarm.missReplayPoints)
	}
	if len(planWarm.missPolicyPoints) != 1 || planWarm.missPolicyPoints[0].deviceID != devMiss {
		t.Fatalf("expected only devMiss policy miss, got %v", planWarm.missPolicyPoints)
	}
	if stateWarm.knownDevices[devHit] != devHitFact {
		t.Fatalf("missing cached device in working state: %+v", stateWarm.knownDevices)
	}
	if stateWarm.knownPoints[deviceKey{deviceID: devHit, key: "zone.temp"}].PointID != pointIDHit {
		t.Fatalf("missing cached point in working state: %+v", stateWarm.knownPoints)
	}
	if pol := stateWarm.policies[deviceKey{deviceID: devHit, key: "zone.temp"}]; pol.policy.Revision != 5 || pol.policy.PresencePolicyRevision != 3 || pol.freshWithinSeconds != 300 {
		t.Fatalf("unexpected cached policy in working state: %+v", pol)
	}

	// Site invalidation clears devHit immediately so the next batch re-queries Postgres.
	cache.InvalidateSite(tenantID, siteID)
	_, planAfterInvalidation := planBatchPrefetch(cache, candidates)
	if len(planAfterInvalidation.missDeviceIDs) != 2 || len(planAfterInvalidation.missReplayPoints) != 1 || len(planAfterInvalidation.missPolicyPoints) != 2 {
		t.Fatalf("expected full miss after InvalidateSite: devices=%v replay=%v policies=%v", planAfterInvalidation.missDeviceIDs, planAfterInvalidation.missReplayPoints, planAfterInvalidation.missPolicyPoints)
	}

	// Scope-conflict candidate (wrong tenantID) must miss cache so Postgres catches DeviceConflict.
	cache.PutDevice(devHitFact)
	conflictCandidates := []ObservationCandidate{{
		SourceID:           sourceA,
		SourcePath:         SourcePathPush,
		ExternalEntityType: "DEVICE",
		ExternalID:         devHit,
		Device:             &ResolvedDevice{TenantID: "018f2d00-0000-7000-8000-000000000099", SiteID: siteID, DeviceID: devHit},
		TelemetryKey:       "zone.temp",
		Value:              json.RawMessage(`22.5`),
		ValueType:          "NUMBER",
		SampledAt:          now,
		ReceivedAt:         now,
		Position:           SourcePosition{Partition: "p", Offset: 3, EventID: "018f2e00-8000-7000-8000-000000000003"},
	}}
	_, conflictPlan := planBatchPrefetch(cache, conflictCandidates)
	if len(conflictPlan.missDeviceIDs) != 1 || conflictPlan.missDeviceIDs[0] != devHit {
		t.Fatalf("expected conflicting tenant candidate to miss device cache, got %v", conflictPlan.missDeviceIDs)
	}
}
