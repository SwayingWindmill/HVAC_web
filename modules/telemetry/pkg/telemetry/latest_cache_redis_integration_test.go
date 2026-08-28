package telemetry

import (
	"os"
	"testing"
	"time"
)

func TestRedisLatestCacheNeverRegressesBusinessRevision(t *testing.T) {
	redisURL := os.Getenv("S2_TELEMETRY_LATEST_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("S2_TELEMETRY_LATEST_REDIS_TEST_URL is not set")
	}
	cache, err := OpenRedisLatestCache(t.Context(), RedisLatestCacheConfig{
		URL:       redisURL,
		KeyPrefix: "hvac:test:latest:" + time.Now().UTC().Format("20060102T150405.000000000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close() }()

	baseTime := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	revision2 := realtimeTestSnapshot(baseTime, 2)
	applied, err := cache.PutIfNewer(t.Context(), revision2)
	if err != nil || !applied {
		t.Fatalf("revision 2 applied=%t err=%v", applied, err)
	}
	if err := cache.client.Del(t.Context(), cache.deviceSiteKey(revision2.TenantId, revision2.DeviceId)).Err(); err != nil {
		t.Fatal(err)
	}
	repaired, err := cache.PutIfNewer(t.Context(), revision2)
	if err != nil || repaired {
		t.Fatalf("equal revision index repair appliedSnapshot=%t err=%v", repaired, err)
	}
	if _, err := cache.GetForDevice(t.Context(), string(revision2.TenantId), string(revision2.DeviceId)); err != nil {
		t.Fatalf("equal revision rebuild did not repair device/site index: %v", err)
	}

	older := realtimeTestSnapshot(baseTime.Add(-time.Minute), 1)
	older.Values[0].Present.Value = []byte(`999`)
	applied, err = cache.PutIfNewer(t.Context(), older)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("older revision unexpectedly replaced Redis Latest")
	}

	duplicate := realtimeTestSnapshot(baseTime.Add(time.Minute), 2)
	duplicate.Values[0].Present.Value = []byte(`888`)
	applied, err = cache.PutIfNewer(t.Context(), duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("equal revision unexpectedly replaced Redis Latest")
	}

	cached, err := cache.GetForDevice(t.Context(), string(revision2.TenantId), string(revision2.DeviceId))
	if err != nil {
		t.Fatal(err)
	}
	if cached.BusinessRevision != 2 || string(cached.Values[0].Present.Value) != "21.5" {
		t.Fatalf("cached revision 2 drifted: revision=%d value=%s", cached.BusinessRevision, cached.Values[0].Present.Value)
	}

	revision3 := realtimeTestSnapshot(baseTime.Add(2*time.Minute), 3)
	revision3.Values[0].Present.Value = []byte(`22.75`)
	applied, err = cache.PutIfNewer(t.Context(), revision3)
	if err != nil || !applied {
		t.Fatalf("revision 3 applied=%t err=%v", applied, err)
	}
	cached, err = cache.GetForDevice(t.Context(), string(revision3.TenantId), string(revision3.DeviceId))
	if err != nil {
		t.Fatal(err)
	}
	if cached.BusinessRevision != 3 || string(cached.Values[0].Present.Value) != "22.75" {
		t.Fatalf("cached revision 3 mismatch: revision=%d value=%s", cached.BusinessRevision, cached.Values[0].Present.Value)
	}
}
