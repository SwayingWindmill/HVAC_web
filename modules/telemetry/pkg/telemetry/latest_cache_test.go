package telemetry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
	"github.com/redis/go-redis/v9"
)

type memoryLatestCache struct {
	snapshot telemetryapi.DeviceObservationSnapshot
	putErr   error
	getErr   error
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

func (cache *memoryLatestCache) Close() error { return nil }

func (cache *memoryLatestCache) Ping(context.Context) error { return nil }

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
	cache := &RedisLatestCache{client: client, keyPrefix: defaultLatestCacheKeyPrefix}

	now := time.Date(2026, 8, 13, 1, 2, 3, 456000000, time.UTC)
	snap := realtimeTestSnapshot(now, 9)
	applied, err := cache.PutIfNewer(t.Context(), snap)
	if err != nil || !applied {
		t.Fatalf("PutIfNewer applied=%t err=%v", applied, err)
	}

	mu.Lock()
	defer mu.Unlock()
	// EVAL <script> <numkeys=2> <key1> <key2> <rev> <tenant> <site> <device> <evaluatedAt> <json> <maxSampledMs> <ttlMs>
	if len(evalArgs) != 13 {
		t.Fatalf("unexpected EVAL argument count %d: %#v", len(evalArgs), evalArgs)
	}
	scriptText := evalArgs[1]
	if !strings.Contains(scriptText, "cur_sampled_ms") || !strings.Contains(scriptText, "PEXPIRE") {
		t.Fatalf("redisLatestCAS missing anti-rollback or PEXPIRE logic: %s", scriptText)
	}
	wantSampledMs := strconv.FormatInt(now.UnixMilli(), 10)
	if evalArgs[11] != wantSampledMs {
		t.Fatalf("ARGV[7] sampled_ms=%q, want %q", evalArgs[11], wantSampledMs)
	}
	wantTTLMs := strconv.FormatInt((7 * 24 * time.Hour).Milliseconds(), 10)
	if evalArgs[12] != wantTTLMs {
		t.Fatalf("ARGV[8] ttl_ms=%q, want %q", evalArgs[12], wantTTLMs)
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
