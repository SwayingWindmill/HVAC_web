package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"
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
