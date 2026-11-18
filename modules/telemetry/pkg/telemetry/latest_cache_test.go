package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
	"github.com/redis/go-redis/v9"
)

type memoryLatestCache struct {
	mu         sync.Mutex
	snapshot   telemetryapi.DeviceObservationSnapshot
	sitePoints map[string]map[string]cachedSiteLiveObservation
	putErr     error
	getErr     error
}

func (cache *memoryLatestCache) PutIfNewer(_ context.Context, snapshot telemetryapi.DeviceObservationSnapshot) (bool, error) {
	if cache.putErr != nil {
		return false, cache.putErr
	}
	if cache.snapshot.BusinessRevision >= snapshot.BusinessRevision {
		return false, nil
	}
	cache.snapshot = snapshot
	return true, nil
}

func (cache *memoryLatestCache) Get(_ context.Context, tenantID, siteID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	if cache.getErr != nil {
		return telemetryapi.DeviceObservationSnapshot{}, cache.getErr
	}
	if cache.snapshot.BusinessRevision == 0 || string(cache.snapshot.TenantId) != tenantID || string(cache.snapshot.SiteId) != siteID || string(cache.snapshot.DeviceId) != deviceID {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	return cache.snapshot, nil
}

func (cache *memoryLatestCache) GetForDevice(_ context.Context, tenantID, deviceID string) (telemetryapi.DeviceObservationSnapshot, error) {
	if cache.getErr != nil {
		return telemetryapi.DeviceObservationSnapshot{}, cache.getErr
	}
	if cache.snapshot.BusinessRevision == 0 || string(cache.snapshot.TenantId) != tenantID || string(cache.snapshot.DeviceId) != deviceID {
		return telemetryapi.DeviceObservationSnapshot{}, ErrLatestCacheMiss
	}
	return cache.snapshot, nil
}

func (cache *memoryLatestCache) BatchGetForDevices(_ context.Context, tenantID string, deviceIDs []string) (map[string]telemetryapi.DeviceObservationSnapshot, error) {
	if cache.getErr != nil {
		return nil, cache.getErr
	}
	result := make(map[string]telemetryapi.DeviceObservationSnapshot, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		if cache.snapshot.BusinessRevision != 0 && string(cache.snapshot.TenantId) == tenantID && string(cache.snapshot.DeviceId) == deviceID {
			result[deviceID] = cache.snapshot
		}
	}
	return result, nil
}

func (cache *memoryLatestCache) Close() error { return nil }

func (cache *memoryLatestCache) Ping(context.Context) error { return nil }

func (cache *memoryLatestCache) PutSiteLiveObservations(_ context.Context, tenantID, siteID string, observations []OptimizationLiveObservation) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.putErr != nil {
		return cache.putErr
	}
	cleanTenant, cleanSite, err := normalizeSiteLiveScope(tenantID, siteID)
	if err != nil {
		return err
	}
	if cache.sitePoints == nil {
		cache.sitePoints = map[string]map[string]cachedSiteLiveObservation{}
	}
	scopeKey := cleanTenant + ":" + cleanSite
	points := cache.sitePoints[scopeKey]
	if points == nil {
		points = map[string]cachedSiteLiveObservation{}
		cache.sitePoints[scopeKey] = points
	}
	for _, obs := range observations {
		entry, err := normalizeCachedSiteLiveObservation(obs)
		if err != nil {
			return err
		}
		newEffMs := max(entry.SampledMs, entry.HealthMs)
		if existing, ok := points[entry.PointID]; ok {
			curEffMs := max(existing.SampledMs, existing.HealthMs)
			if newEffMs < curEffMs || (!existing.Invalidated && entry.SampledMs < existing.SampledMs) {
				continue
			}
		}
		points[entry.PointID] = entry
	}
	return nil
}

func (cache *memoryLatestCache) GetSiteLiveObservations(_ context.Context, tenantID, siteID string, pointIDs []string) (map[string]OptimizationLiveObservation, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.getErr != nil {
		return nil, cache.getErr
	}
	cleanTenant, cleanSite, err := normalizeSiteLiveScope(tenantID, siteID)
	if err != nil {
		return nil, err
	}
	cleanIDs, err := normalizeSitePointIDs(pointIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]OptimizationLiveObservation, len(cleanIDs))
	points := cache.sitePoints[cleanTenant+":"+cleanSite]
	for _, id := range cleanIDs {
		if entry, ok := points[id]; ok && !entry.Invalidated {
			out[id] = entry.OptimizationLiveObservation
		}
	}
	return out, nil
}

func (cache *memoryLatestCache) InvalidateSiteLiveObservations(_ context.Context, tenantID, siteID string, items []SiteLiveInvalidation) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.putErr != nil {
		return cache.putErr
	}
	cleanTenant, cleanSite, err := normalizeSiteLiveScope(tenantID, siteID)
	if err != nil {
		return err
	}
	if cache.sitePoints == nil {
		cache.sitePoints = map[string]map[string]cachedSiteLiveObservation{}
	}
	scopeKey := cleanTenant + ":" + cleanSite
	points := cache.sitePoints[scopeKey]
	if points == nil {
		points = map[string]cachedSiteLiveObservation{}
		cache.sitePoints[scopeKey] = points
	}
	for _, item := range items {
		cleanID := strings.ToLower(strings.TrimSpace(item.PointID))
		if !uuidPattern.MatchString(cleanID) {
			return errors.New("telemetry site live cache pointId must be a UUID")
		}
		if item.SampledAt.IsZero() {
			return errors.New("telemetry site live invalidation sampledAt is required")
		}
		ms := item.SampledAt.UTC().UnixMilli()
		if existing, ok := points[cleanID]; ok {
			curEffMs := max(existing.SampledMs, existing.HealthMs)
			if ms < curEffMs || (!existing.Invalidated && ms < existing.SampledMs) {
				continue
			}
		}
		points[cleanID] = cachedSiteLiveObservation{
			SampledMs:   ms,
			HealthMs:    ms,
			Invalidated: true,
			OptimizationLiveObservation: OptimizationLiveObservation{
				PointID: cleanID,
			},
		}
	}
	return nil
}

type memoryLatestRebuildSource struct {
	snapshots []telemetryapi.DeviceObservationSnapshot
}

func (source *memoryLatestRebuildSource) LatestCacheRebuildBatch(_ context.Context, afterDeviceID string, limit int) ([]telemetryapi.DeviceObservationSnapshot, error) {
	result := make([]telemetryapi.DeviceObservationSnapshot, 0, limit)
	for _, snapshot := range source.snapshots {
		if afterDeviceID != "" && string(snapshot.DeviceId) <= afterDeviceID {
			continue
		}
		result = append(result, snapshot)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

type memoryLatestOutbox struct {
	pending      []LatestCacheMaterialization
	materialized []string
	failed       []string
	nextRetry    time.Time
}

func (outbox *memoryLatestOutbox) PendingLatestCacheMaterializations(_ context.Context, limit int, _ time.Time) ([]LatestCacheMaterialization, error) {
	if limit < len(outbox.pending) {
		return append([]LatestCacheMaterialization(nil), outbox.pending[:limit]...), nil
	}
	return append([]LatestCacheMaterialization(nil), outbox.pending...), nil
}

func (outbox *memoryLatestOutbox) MarkLatestCacheMaterialized(_ context.Context, eventID string, _ time.Time) error {
	outbox.materialized = append(outbox.materialized, eventID)
	return nil
}

func (outbox *memoryLatestOutbox) MarkLatestCacheFailed(_ context.Context, eventID string, next time.Time, _ string) error {
	outbox.failed = append(outbox.failed, eventID)
	outbox.nextRetry = next
	return nil
}

func TestRebuildLatestCacheRestoresBusinessSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	first := realtimeTestSnapshot(now, 4)
	second := realtimeTestSnapshot(now.Add(time.Minute), 6)
	second.DeviceId = realtimeTestDevice2
	source := &memoryLatestRebuildSource{snapshots: []telemetryapi.DeviceObservationSnapshot{first, second}}
	cache := &memoryLatestCache{}

	rebuilt, err := RebuildLatestCache(t.Context(), source, cache)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt != 2 {
		t.Fatalf("rebuilt=%d", rebuilt)
	}
	if cache.snapshot.DeviceId != second.DeviceId || cache.snapshot.BusinessRevision != 6 {
		t.Fatalf("last rebuilt snapshot=%#v", cache.snapshot)
	}
}

func TestLatestCacheRelayMaterializesCommittedSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	snapshot := realtimeTestSnapshot(now, 7)
	outbox := &memoryLatestOutbox{pending: []LatestCacheMaterialization{{
		EventID: "018f2e00-5000-7000-8000-000000000007", DeviceID: string(snapshot.DeviceId), Revision: 7, Snapshot: snapshot,
	}}}
	cache := &memoryLatestCache{}
	relay, err := NewLatestCacheRelay(outbox, cache, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	count, err := relay.RelayOnce(t.Context(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(outbox.materialized) != 1 || outbox.materialized[0] != outbox.pending[0].EventID {
		t.Fatalf("materialization count=%d outbox=%v", count, outbox.materialized)
	}
	if cache.snapshot.BusinessRevision != 7 || cache.snapshot.DeviceId != snapshot.DeviceId {
		t.Fatalf("cache snapshot=%#v", cache.snapshot)
	}
}

func TestLatestCacheRelayBacksOffRedisFailure(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	snapshot := realtimeTestSnapshot(now, 8)
	outbox := &memoryLatestOutbox{pending: []LatestCacheMaterialization{{
		EventID: "018f2e00-5000-7000-8000-000000000008", DeviceID: string(snapshot.DeviceId), Revision: 8, Attempts: 2, Snapshot: snapshot,
	}}}
	cache := &memoryLatestCache{putErr: errors.New("redis unavailable")}
	relay, _ := NewLatestCacheRelay(outbox, cache, func() time.Time { return now })
	count, err := relay.RelayOnce(t.Context(), 64)
	if !errors.Is(err, ErrLatestCacheUnavailable) || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if len(outbox.failed) != 1 || outbox.failed[0] != outbox.pending[0].EventID {
		t.Fatalf("failed=%v", outbox.failed)
	}
	if !outbox.nextRetry.Equal(now.Add(time.Second)) {
		t.Fatalf("next retry=%s", outbox.nextRetry)
	}
}

func TestSnapshotMaxSampledMs(t *testing.T) {
	base := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	empty := realtimeTestSnapshot(base, 1)
	empty.Values = []telemetryapi.TelemetryKeyState{
		{Missing: &telemetryapi.TelemetryMissingState{Key: "zone.humidity", State: "MISSING", Freshness: "MISSING", MissingReason: "NEVER_OBSERVED"}},
	}
	if got := snapshotMaxSampledMs(empty); got != 0 {
		t.Fatalf("snapshotMaxSampledMs(empty)=%d, want 0", got)
	}

	later := base.Add(45 * time.Second)
	snap := realtimeTestSnapshot(base, 2)
	secondVal := *snap.Values[0].Present
	secondVal.Key = "chiller.power"
	secondVal.SampledAt = telemetryapi.Instant(later.UTC().Format(time.RFC3339Nano))
	snap.Values = append(snap.Values, telemetryapi.TelemetryKeyState{
		Present: &secondVal,
	})
	if got := snapshotMaxSampledMs(snap); got != later.UnixMilli() {
		t.Fatalf("snapshotMaxSampledMs=%d, want %d", got, later.UnixMilli())
	}
}

func TestSnapshotKeysSignatureDetectsKeySetChanges(t *testing.T) {
	base := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	s1 := realtimeTestSnapshot(base, 1)
	s2 := realtimeTestSnapshot(base.Add(time.Minute), 2)
	s2.Values = append(s2.Values, telemetryapi.TelemetryKeyState{
		Missing: &telemetryapi.TelemetryMissingState{Key: "zone.humidity", State: "MISSING", Freshness: "MISSING", MissingReason: "NEVER_OBSERVED"},
	})
	s2Reordered := realtimeTestSnapshot(base.Add(time.Minute), 3)
	s2Reordered.Values = slices.Clone(s2.Values)
	slices.Reverse(s2Reordered.Values)

	if snapshotKeysSignature(s2) != snapshotKeysSignature(s2Reordered) {
		t.Fatal("snapshotKeysSignature must be order-independent")
	}
	if snapshotKeysSignature(s1) == snapshotKeysSignature(s2) {
		t.Fatal("snapshotKeysSignature failed to distinguish added/removed point key")
	}
}

func TestRedisLatestCachePutIfNewerPassesSampledMsAndTTL(t *testing.T) {
	var mu sync.Mutex
	var evalArgs []string

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for {
					args, err := readRESPArray(reader)
					if err != nil {
						return
					}
					cmd := strings.ToUpper(args[0])
					switch cmd {
					case "HELLO":
						_, _ = io.WriteString(c, "-ERR unknown command 'HELLO'\r\n")
					case "PING":
						_, _ = io.WriteString(c, "+PONG\r\n")
					case "EVALSHA":
						_, _ = io.WriteString(c, "-NOSCRIPT No matching script. Please use EVAL.\r\n")
					case "EVAL":
						mu.Lock()
						evalArgs = append([]string(nil), args...)
						mu.Unlock()
						_, _ = io.WriteString(c, ":1\r\n")
					default:
						_, _ = io.WriteString(c, "+OK\r\n")
					}
				}
			}(conn)
		}
	}()

	client := redis.NewClient(&redis.Options{
		Addr:            ln.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
	})
	defer func() { _ = client.Close() }()
	registry := observability.NewRegistry()
	cache := &RedisLatestCache{client: client, keyPrefix: defaultLatestCacheKeyPrefix}
	cache.SetPipelineMetrics(NewPipelineMetrics(registry))

	now := time.Date(2026, 8, 13, 1, 2, 3, 456000000, time.UTC)
	snap := realtimeTestSnapshot(now, 9)
	applied, err := cache.PutIfNewer(t.Context(), snap)
	if err != nil || !applied {
		t.Fatalf("PutIfNewer applied=%t err=%v", applied, err)
	}

	mu.Lock()
	defer mu.Unlock()
	// EVAL <script> <numkeys=2> <key1> <key2> <rev> <tenant> <site> <device> <evaluatedAt> <json> <maxSampledMs> <ttlMs> <keysSig>
	if len(evalArgs) != 14 {
		t.Fatalf("unexpected EVAL argument count %d: %#v", len(evalArgs), evalArgs)
	}
	scriptText := evalArgs[1]
	if !strings.Contains(scriptText, "cur_sampled_ms") || !strings.Contains(scriptText, "keys_sig") || !strings.Contains(scriptText, "PEXPIRE") {
		t.Fatalf("redisLatestCAS missing anti-rollback, keys_sig, or PEXPIRE logic: %s", scriptText)
	}
	wantSampledMs := strconv.FormatInt(now.UnixMilli(), 10)
	if evalArgs[11] != wantSampledMs {
		t.Fatalf("ARGV[7] sampled_ms=%q, want %q", evalArgs[11], wantSampledMs)
	}
	wantTTLMs := strconv.FormatInt((7 * 24 * time.Hour).Milliseconds(), 10)
	if evalArgs[12] != wantTTLMs {
		t.Fatalf("ARGV[8] ttl_ms=%q, want %q", evalArgs[12], wantTTLMs)
	}
	if evalArgs[13] != snapshotKeysSignature(snap) {
		t.Fatalf("ARGV[9] keys_sig=%q, want %q", evalArgs[13], snapshotKeysSignature(snap))
	}
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `hvac_s2_latest_cache_updates_total{outcome="applied"} 1`) {
		t.Fatalf("missing PutIfNewer applied metric:\n%s", rec.Body.String())
	}
}

func TestRedisSitePointsCacheKeyNormalizationAndCommands(t *testing.T) {
	defaultCache := &RedisLatestCache{keyPrefix: defaultLatestCacheKeyPrefix}
	wantDefaultKey := "hvac:v2:latest:site-points:" + tenantA + ":" + siteA
	if got := defaultCache.sitePointsKey(tenantA, siteA); got != wantDefaultKey {
		t.Fatalf("default sitePointsKey=%q, want %q", got, wantDefaultKey)
	}
	customCache := &RedisLatestCache{keyPrefix: "hvac:test:latest"}
	wantCustomKey := "hvac:test:latest:site-points:" + tenantA + ":" + siteA
	if got := customCache.sitePointsKey(tenantA, siteA); got != wantCustomKey {
		t.Fatalf("custom sitePointsKey=%q, want %q", got, wantCustomKey)
	}

	var mu sync.Mutex
	hashes := map[string]map[string]string{}
	var commands [][]string

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for {
					args, err := readRESPArray(reader)
					if err != nil {
						return
					}
					cmd := strings.ToUpper(args[0])
					mu.Lock()
					commands = append(commands, append([]string(nil), args...))
					mu.Unlock()
					switch cmd {
					case "HELLO":
						_, _ = io.WriteString(c, "-ERR unknown command 'HELLO'\r\n")
					case "PING":
						_, _ = io.WriteString(c, "+PONG\r\n")
					case "EVALSHA":
						_, _ = io.WriteString(c, "-NOSCRIPT No matching script. Please use EVAL.\r\n")
					case "EVAL":
						hashKey := args[3]
						var applied int64
						mu.Lock()
						h := hashes[hashKey]
						if h == nil {
							h = map[string]string{}
							hashes[hashKey] = h
						}
						for i := 5; i+3 < len(args); i += 4 {
							field := args[i]
							newSampledMs, _ := strconv.ParseInt(args[i+1], 10, 64)
							newEffMs, _ := strconv.ParseInt(args[i+2], 10, 64)
							payload := args[i+3]
							shouldApply := true
							if curRaw, ok := h[field]; ok {
								var cur cachedSiteLiveObservation
								if json.Unmarshal([]byte(curRaw), &cur) == nil {
									curEffMs := max(cur.SampledMs, cur.HealthMs)
									if newEffMs < curEffMs || (!cur.Invalidated && newSampledMs < cur.SampledMs) {
										shouldApply = false
									}
								}
							}
							if shouldApply {
								h[field] = payload
								applied++
							}
						}
						mu.Unlock()
						_, _ = fmt.Fprintf(c, ":%d\r\n", applied)
					case "HMGET":
						hashKey := args[1]
						fields := args[2:]
						mu.Lock()
						h := hashes[hashKey]
						var resp strings.Builder
						fmt.Fprintf(&resp, "*%d\r\n", len(fields))
						for _, f := range fields {
							val, ok := h[f]
							if !ok {
								resp.WriteString("$-1\r\n")
								continue
							}
							fmt.Fprintf(&resp, "$%d\r\n%s\r\n", len(val), val)
						}
						mu.Unlock()
						_, _ = io.WriteString(c, resp.String())
					case "HDEL":
						hashKey := args[1]
						var removed int64
						mu.Lock()
						h := hashes[hashKey]
						for _, f := range args[2:] {
							if _, ok := h[f]; ok {
								delete(h, f)
								removed++
							}
						}
						mu.Unlock()
						_, _ = fmt.Fprintf(c, ":%d\r\n", removed)
					default:
						_, _ = io.WriteString(c, "+OK\r\n")
					}
				}
			}(conn)
		}
	}()

	client := redis.NewClient(&redis.Options{
		Addr:            ln.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
	})
	defer func() { _ = client.Close() }()
	registry := observability.NewRegistry()
	cache := &RedisLatestCache{client: client, keyPrefix: defaultLatestCacheKeyPrefix}
	cache.SetPipelineMetrics(NewPipelineMetrics(registry))

	pointA := "018f2e00-3100-7000-8000-000000000001"
	pointB := "018f2e00-3100-7000-8000-000000000002"
	t1 := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	obs1 := OptimizationLiveObservation{
		ObservationID:   "018f2e00-8000-7000-8000-000000000001",
		DeviceID:        deviceA,
		PointID:         pointA,
		TelemetryKey:    "supply_water_temperature",
		PointRevision:   3,
		SampledAt:       t1.Format(time.RFC3339Nano),
		ReceivedAt:      t1.Add(time.Second).Format(time.RFC3339Nano),
		Value:           6.8,
		Unit:            "Cel",
		Quality:         "GOOD",
		QualityReasons:  []string{},
		HealthStatus:    HealthHealthy,
		SourceEventID:   "018f2e00-8000-7000-8000-000000000011",
		SourcePartition: "p0",
		SourceOffset:    10,
	}
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{obs1}); err != nil {
		t.Fatal(err)
	}

	older := obs1
	older.SampledAt = t1.Add(-30 * time.Second).Format(time.RFC3339Nano)
	older.Value = 99.9
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{older}); err != nil {
		t.Fatal(err)
	}

	sameTimeUpdated := obs1
	sameTimeUpdated.HealthStatus = HealthBad
	sameTimeUpdated.HealthReasons = []string{"OUT_OF_RANGE"}
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{sameTimeUpdated}); err != nil {
		t.Fatal(err)
	}

	got, err := cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointA, pointB})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[pointA].Value != 6.8 || got[pointA].HealthStatus != HealthBad || !slices.Equal(got[pointA].HealthReasons, []string{"OUT_OF_RANGE"}) {
		t.Fatalf("unexpected GetSiteLiveObservations result: %#v", got)
	}
	if _, exists := got[pointB]; exists {
		t.Fatalf("expected miss for pointB, got %#v", got[pointB])
	}

	rejectAt := t1.Add(15 * time.Second)
	if err := cache.InvalidateSiteLiveObservations(t.Context(), tenantA, siteA, []SiteLiveInvalidation{{PointID: pointA, SampledAt: rejectAt}}); err != nil {
		t.Fatal(err)
	}
	afterInvalidate, err := cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointA})
	if err != nil || len(afterInvalidate) != 0 {
		t.Fatalf("expected empty after invalidate, got=%#v err=%v", afterInvalidate, err)
	}

	staleBackfill := obs1
	staleBackfill.HealthAt = t1.Add(time.Second).Format(time.RFC3339Nano)
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{staleBackfill}); err != nil {
		t.Fatal(err)
	}
	stillInvalidated, err := cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointA})
	if err != nil || len(stillInvalidated) != 0 {
		t.Fatalf("stale backfill must not overwrite newer invalidation tombstone: %#v err=%v", stillInvalidated, err)
	}

	postRejectPG := obs1
	postRejectPG.HealthStatus = HealthBad
	postRejectPG.HealthReasons = []string{"OUT_OF_RANGE"}
	postRejectPG.HealthAt = rejectAt.Format(time.RFC3339Nano)
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{postRejectPG}); err != nil {
		t.Fatal(err)
	}
	afterPostRejectBackfill, err := cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointA})
	if err != nil || len(afterPostRejectBackfill) != 1 || afterPostRejectBackfill[pointA].HealthStatus != HealthBad {
		t.Fatalf("expected post-rejection PG read to replace tombstone, got=%#v err=%v", afterPostRejectBackfill, err)
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	for _, needle := range []string{
		`hvac_s2_latest_cache_updates_total{outcome="applied"} 3`,
		`hvac_s2_latest_cache_updates_total{outcome="stale_skipped"} 2`,
		`hvac_s2_latest_cache_updates_total{outcome="invalidated"} 1`,
	} {
		if !strings.Contains(metricsBody, needle) {
			t.Fatalf("missing %q in /metrics output:\n%s", needle, metricsBody)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	var sawEval bool
	for _, cmd := range commands {
		if strings.ToUpper(cmd[0]) == "EVAL" {
			sawEval = true
			if cmd[3] != wantDefaultKey {
				t.Fatalf("EVAL hash key=%q, want %q", cmd[3], wantDefaultKey)
			}
			if !strings.Contains(cmd[1], "new_sampled_ms < cur_sampled_ms") || !strings.Contains(cmd[1], "PEXPIRE") || !strings.Contains(cmd[1], "cjson.decode") {
				t.Fatalf("unexpected Lua CAS script: %s", cmd[1])
			}
			wantTTL := strconv.FormatInt(defaultLatestCacheTTL.Milliseconds(), 10)
			if cmd[4] != wantTTL {
				t.Fatalf("EVAL ttl=%q, want %q", cmd[4], wantTTL)
			}
			break
		}
	}
	if !sawEval {
		t.Fatal("expected EVAL command for PutSiteLiveObservations")
	}
}

func TestMemoryLatestCacheSitePointsCAS(t *testing.T) {
	cache := &memoryLatestCache{}
	pointID := "018f2e00-3100-7000-8000-000000000001"
	t1 := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	obs := OptimizationLiveObservation{
		ObservationID:   "018f2e00-8000-7000-8000-000000000001",
		DeviceID:        deviceA,
		PointID:         pointID,
		TelemetryKey:    "zone.temperature",
		PointRevision:   2,
		SampledAt:       t1.Format(time.RFC3339Nano),
		ReceivedAt:      t1.Add(time.Second).Format(time.RFC3339Nano),
		Value:           22.5,
		Unit:            "Cel",
		Quality:         "GOOD",
		QualityReasons:  []string{},
		HealthStatus:    HealthHealthy,
		SourceEventID:   "018f2e00-8000-7000-8000-000000000011",
		SourcePartition: "p0",
		SourceOffset:    1,
	}
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{obs}); err != nil {
		t.Fatal(err)
	}
	older := obs
	older.SampledAt = t1.Add(-time.Minute).Format(time.RFC3339Nano)
	older.Value = 19.0
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{older}); err != nil {
		t.Fatal(err)
	}
	got, err := cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointID})
	if err != nil || got[pointID].Value != 22.5 {
		t.Fatalf("expected 22.5 without rollback, got=%#v err=%v", got, err)
	}
	same := obs
	same.HealthStatus = HealthSuspect
	same.HealthReasons = []string{"SPIKE"}
	if err := cache.PutSiteLiveObservations(t.Context(), tenantA, siteA, []OptimizationLiveObservation{same}); err != nil {
		t.Fatal(err)
	}
	got, err = cache.GetSiteLiveObservations(t.Context(), tenantA, siteA, []string{pointID})
	if err != nil || got[pointID].HealthStatus != HealthSuspect {
		t.Fatalf("expected equal-timestamp health overwrite, got=%#v err=%v", got, err)
	}
}

func TestRedisLatestCacheBatchGetForDevicesPipelinedPasses(t *testing.T) {
	now := time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	snap1 := realtimeTestSnapshot(now, 10)
	snap2 := realtimeTestSnapshot(now.Add(time.Minute), 11)
	snap2.DeviceId = realtimeTestDevice2
	snap3 := realtimeTestSnapshot(now.Add(2*time.Minute), 12)
	snap3Device := "018f2e00-3000-7000-8000-000000000099"
	snap3.DeviceId = telemetryapi.UUIDv7(snap3Device)
	missDevice := "018f2e00-3000-7000-8000-000000000088"

	snap1JSON, err := json.Marshal(snap1)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var recordedCommands [][]string
	var failNextHGet bool

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	siteIndex := map[string]string{
		defaultLatestCacheKeyPrefix + ":index:" + string(snap1.TenantId) + ":" + string(snap1.DeviceId): string(snap1.SiteId),
		defaultLatestCacheKeyPrefix + ":index:" + string(snap2.TenantId) + ":" + string(snap2.DeviceId): string(snap2.SiteId),
	}
	snapshotValues := map[string]string{
		defaultLatestCacheKeyPrefix + ":" + string(snap1.TenantId) + ":" + string(snap1.SiteId) + ":" + string(snap1.DeviceId): string(snap1JSON),
		defaultLatestCacheKeyPrefix + ":" + string(snap2.TenantId) + ":" + string(snap2.SiteId) + ":" + string(snap2.DeviceId): "{corrupt-json",
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for {
					args, err := readRESPArray(reader)
					if err != nil {
						return
					}
					cmd := strings.ToUpper(args[0])
					switch cmd {
					case "HELLO":
						_, _ = io.WriteString(c, "-ERR unknown command 'HELLO'\r\n")
					case "PING":
						_, _ = io.WriteString(c, "+PONG\r\n")
					case "HGET":
						mu.Lock()
						fail := failNextHGet
						recordedCommands = append(recordedCommands, append([]string(nil), args...))
						mu.Unlock()
						if fail {
							_, _ = io.WriteString(c, "-ERR simulated redis error\r\n")
							continue
						}
						key, field := args[1], args[2]
						var value string
						var ok bool
						if field == "site_id" {
							value, ok = siteIndex[key]
						} else if field == "snapshot" {
							value, ok = snapshotValues[key]
						}
						if !ok {
							_, _ = io.WriteString(c, "$-1\r\n")
						} else {
							_, _ = io.WriteString(c, fmt.Sprintf("$%d\r\n%s\r\n", len(value), value))
						}
					default:
						_, _ = io.WriteString(c, "+OK\r\n")
					}
				}
			}(conn)
		}
	}()

	client := redis.NewClient(&redis.Options{
		Addr:            ln.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
	})
	defer func() { _ = client.Close() }()
	cache := &RedisLatestCache{client: client, keyPrefix: defaultLatestCacheKeyPrefix}

	emptyResult, err := cache.BatchGetForDevices(t.Context(), string(snap1.TenantId), nil)
	if err != nil || len(emptyResult) != 0 {
		t.Fatalf("empty batch got=%v err=%v", emptyResult, err)
	}
	mu.Lock()
	emptyCommandCount := len(recordedCommands)
	mu.Unlock()
	if emptyCommandCount != 0 {
		t.Fatalf("empty batch issued %d redis commands", emptyCommandCount)
	}

	got, err := cache.BatchGetForDevices(t.Context(), string(snap1.TenantId), []string{
		string(snap1.DeviceId),
		missDevice,
		string(snap1.DeviceId),
		string(snap2.DeviceId),
	})
	if err != nil {
		t.Fatalf("BatchGetForDevices failed: %v", err)
	}
	if len(got) != 1 || got[string(snap1.DeviceId)].BusinessRevision != 10 {
		t.Fatalf("unexpected BatchGetForDevices result: %#v", got)
	}
	if _, hasCorrupt := got[string(snap2.DeviceId)]; hasCorrupt {
		t.Fatalf("corrupt cache snapshot should be omitted for postgres fallback: %#v", got)
	}

	mu.Lock()
	commands := append([][]string(nil), recordedCommands...)
	failNextHGet = true
	mu.Unlock()
	if len(commands) != 5 {
		t.Fatalf("expected 5 pipelined HGET commands across 2 passes, got %d: %#v", len(commands), commands)
	}
	for i := 0; i < 3; i++ {
		if commands[i][2] != "site_id" {
			t.Fatalf("pass 1 command %d expected field site_id, got %#v", i, commands[i])
		}
	}
	for i := 3; i < 5; i++ {
		if commands[i][2] != "snapshot" {
			t.Fatalf("pass 2 command %d expected field snapshot, got %#v", i, commands[i])
		}
	}

	if _, err := cache.BatchGetForDevices(t.Context(), string(snap1.TenantId), []string{string(snap1.DeviceId)}); !errors.Is(err, ErrLatestCacheUnavailable) {
		t.Fatalf("expected ErrLatestCacheUnavailable on Redis error, got %v", err)
	}
}

func readRESPArray(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("unexpected RESP prefix: %q", line)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	out := make([]string, count)
	for i := 0; i < count; i++ {
		hdr, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(hdr, "$") {
			return nil, fmt.Errorf("unexpected bulk header: %q", hdr)
		}
		length, err := strconv.Atoi(strings.TrimSpace(hdr[1:]))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		out[i] = string(buf[:length])
	}
	return out, nil
}
