package connectivity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

func TestUplinkCacheRespectsTTLValidUntilDeepCopyBoundsAndInvalidation(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}

	gwA := adapter.Gateway{ID: "gw-a", TenantID: "t-1", SiteID: "s-1"}
	gwB := adapter.Gateway{ID: "gw-b", TenantID: "t-1", SiteID: "s-1"}

	// Gateway positive cache is capped by credential valid_until (< 30s).
	validUntil := now.Add(12 * time.Second)
	expires := now.Add(uplinkPositiveTTL)
	if validUntil.Before(expires) {
		expires = validUntil
	}
	cacheSetIfEpoch(&store.cache, &store.cache.gateways, gwA.ID, gatewayLookupResult{gateway: gwA}, expires, now, gwA.ID, 0)
	cacheSetIfEpoch(&store.cache, &store.cache.gateways, gwB.ID, gatewayLookupResult{gateway: gwB}, now.Add(uplinkPositiveTTL), now, gwB.ID, 0)

	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gwA.ID, now.Add(11*time.Second), gwA.ID); !ok {
		t.Fatal("gateway A should still be cached before credential valid_until")
	}
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gwA.ID, now.Add(12*time.Second), gwA.ID); ok {
		t.Fatal("gateway A cache entry must expire exactly at credential valid_until")
	}
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gwB.ID, now.Add(15*time.Second), gwB.ID); !ok {
		t.Fatal("gateway B positive TTL should hold for 30s")
	}

	// Negative entry expires after 5s.
	cacheSetIfEpoch(&store.cache, &store.cache.gateways, "unknown-gw", gatewayLookupResult{err: adapter.ErrGatewayUnknown}, now.Add(uplinkNegativeTTL), now, "unknown-gw", 0)
	if res, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, "unknown-gw", now.Add(4*time.Second), "unknown-gw"); !ok || !errors.Is(res.err, adapter.ErrGatewayUnknown) {
		t.Fatalf("negative cache missing within 5s window: ok=%v res=%+v", ok, res)
	}
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, "unknown-gw", now.Add(5*time.Second), "unknown-gw"); ok {
		t.Fatal("negative cache entry must expire after 5s")
	}

	// InvalidateGatewayCache removes target gateway and its device keys, sparing unrelated gateways.
	cacheSetIfEpoch(&store.cache, &store.cache.devices, deviceCacheKey{tenantID: "t-1", gatewayID: gwB.ID, sourceKey: "CH-1"}, "dev-1", now.Add(uplinkPositiveTTL), now, gwB.ID, 0)
	cacheSetIfEpoch(&store.cache, &store.cache.devices, deviceCacheKey{tenantID: "t-1", gatewayID: "gw-other", sourceKey: "CH-1"}, "dev-2", now.Add(uplinkPositiveTTL), now, "gw-other", 0)
	store.InvalidateGatewayCache(gwB.ID)
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.gateways, gwB.ID, now, gwB.ID); ok {
		t.Fatal("InvalidateGatewayCache did not evict target gateway")
	}
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.devices, deviceCacheKey{tenantID: "t-1", gatewayID: gwB.ID, sourceKey: "CH-1"}, now, gwB.ID); ok {
		t.Fatal("InvalidateGatewayCache did not evict target gateway device key")
	}
	if _, ok, _ := cacheGetOrEpoch(&store.cache, store.cache.devices, deviceCacheKey{tenantID: "t-1", gatewayID: "gw-other", sourceKey: "CH-1"}, now, "gw-other"); !ok {
		t.Fatal("InvalidateGatewayCache evicted unrelated gateway")
	}

	// ResolveBindings caches device/point misses, isolates caller mutations,
	// serves repeated full hits with zero DB calls, and caches negative misses for 5s.
	unit := "Cel"
	sensorID := "sensor-1"
	original := &adapter.ResolvedPoint{PointID: "pt-1", SensorID: &sensorID, Unit: &unit, PointRevision: 1}
	uncachedCalls := 0
	fetcher := func(_ context.Context, _ adapter.Gateway, missingKeys []string, cachedPointQueries []resolvedPointTarget, _ []adapter.DevicePointQuery) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
		uncachedCalls++
		devices := map[string]string{}
		points := map[string]*adapter.ResolvedPoint{}
		var queried []resolvedPointTarget
		for _, k := range missingKeys {
			if k == "CH-1" {
				devices[k] = "dev-1"
				queried = append(queried, resolvedPointTarget{deviceID: "dev-1", pointCode: "temp"}, resolvedPointTarget{deviceID: "dev-1", pointCode: "missing-pt"})
				points[adapter.PointBindingKey("dev-1", "temp")] = original
			}
		}
		for _, target := range cachedPointQueries {
			queried = append(queried, target)
			if target.deviceID == "dev-1" && target.pointCode == "temp" {
				points[adapter.PointBindingKey(target.deviceID, target.pointCode)] = original
			}
		}
		return adapter.ResolvedBindings{Devices: devices, Points: points}, queried, nil
	}
	queries := []adapter.DevicePointQuery{
		{SourceKey: "CH-1", PointCode: "temp"},
		{SourceKey: "CH-1", PointCode: "missing-pt"},
		{SourceKey: "MISSING-DEV", PointCode: "temp"},
	}
	b1, err := store.resolveBindingsCachedWith(context.Background(), gwB, queries, fetcher)
	if err != nil {
		t.Fatalf("first resolveBindingsCachedWith: %v", err)
	}
	if b1.Devices["CH-1"] != "dev-1" || b1.Points[adapter.PointBindingKey("dev-1", "temp")] == nil {
		t.Fatalf("unexpected initial ResolveBindings result: %+v", b1)
	}
	if _, hasMissingDev := b1.Devices["MISSING-DEV"]; hasMissingDev {
		t.Fatalf("unregistered source key must stay absent from Devices: %+v", b1.Devices)
	}
	*b1.Points[adapter.PointBindingKey("dev-1", "temp")].Unit = "MUTATED"

	b2, err := store.resolveBindingsCachedWith(context.Background(), gwB, queries, func(context.Context, adapter.Gateway, []string, []resolvedPointTarget, []adapter.DevicePointQuery) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
		uncachedCalls++
		return adapter.ResolvedBindings{}, nil, errors.New("should hit cache for all positive and negative entries")
	})
	if err != nil || uncachedCalls != 1 {
		t.Fatalf("second resolveBindingsCachedWith calls=%d err=%v", uncachedCalls, err)
	}
	pt2 := b2.Points[adapter.PointBindingKey("dev-1", "temp")]
	if pt2 == nil || pt2.Unit == nil || *pt2.Unit != "Cel" {
		t.Fatalf("bindings cache leaked mutation or lost point: %+v", pt2)
	}

	// Partial point miss on an already-cached device queries only the uncached point.
	var partialMissingKeys []string
	var partialMissingPoints []resolvedPointTarget
	b3, err := store.resolveBindingsCachedWith(context.Background(), gwB, []adapter.DevicePointQuery{
		{SourceKey: "CH-1", PointCode: "temp"},
		{SourceKey: "CH-1", PointCode: "hum"},
	}, func(_ context.Context, _ adapter.Gateway, missingKeys []string, cachedPointQueries []resolvedPointTarget, _ []adapter.DevicePointQuery) (adapter.ResolvedBindings, []resolvedPointTarget, error) {
		partialMissingKeys = append([]string(nil), missingKeys...)
		partialMissingPoints = append([]resolvedPointTarget(nil), cachedPointQueries...)
		return adapter.ResolvedBindings{
			Devices: map[string]string{},
			Points: map[string]*adapter.ResolvedPoint{
				adapter.PointBindingKey("dev-1", "hum"): {PointID: "pt-2", PointRevision: 1},
			},
		}, cachedPointQueries, nil
	})
	if err != nil {
		t.Fatalf("partial miss resolveBindingsCachedWith: %v", err)
	}
	if len(partialMissingKeys) != 0 || len(partialMissingPoints) != 1 || partialMissingPoints[0].pointCode != "hum" {
		t.Fatalf("partial miss queried wrong subset: keys=%v points=%+v", partialMissingKeys, partialMissingPoints)
	}
	if b3.Points[adapter.PointBindingKey("dev-1", "temp")] == nil || b3.Points[adapter.PointBindingKey("dev-1", "hum")] == nil {
		t.Fatalf("partial miss did not merge cached and fresh points: %+v", b3.Points)
	}

	// Bounded cache eviction keeps map size <= uplinkCacheMaxEntries.
	for i := 0; i < uplinkCacheMaxEntries+50; i++ {
		key := deviceCacheKey{tenantID: "t-1", gatewayID: "gw-bound", sourceKey: fmt.Sprintf("key-%05d", i)}
		cacheSetIfEpoch(&store.cache, &store.cache.devices, key, key.sourceKey, now.Add(uplinkPositiveTTL), now, "gw-bound", 0)
	}
	store.cache.mu.Lock()
	deviceMapLen := len(store.cache.devices)
	store.cache.mu.Unlock()
	if deviceMapLen > uplinkCacheMaxEntries {
		t.Fatalf("cache size = %d exceeds bound %d", deviceMapLen, uplinkCacheMaxEntries)
	}
}

func TestUplinkCacheEpochPreventsStaleInFlightPopulateAfterInvalidate(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	gw := adapter.Gateway{ID: "gw-race", TenantID: "t-1", SiteID: "s-1"}

	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := store.resolveGatewayCachedWith(context.Background(), gw.ID, func(context.Context, string, time.Time) (adapter.Gateway, time.Time, error) {
			close(started)
			<-release
			return gw, now.Add(time.Minute), nil
		})
		if err != nil {
			t.Errorf("concurrent resolveGatewayCachedWith: %v", err)
		}
	}()

	<-started
	store.InvalidateGatewayCache(gw.ID)
	close(release)
	wg.Wait()

	secondCalls := 0
	_, err := store.resolveGatewayCachedWith(context.Background(), gw.ID, func(context.Context, string, time.Time) (adapter.Gateway, time.Time, error) {
		secondCalls++
		return adapter.Gateway{}, time.Time{}, adapter.ErrGatewayCredentialInactive
	})
	if !errors.Is(err, adapter.ErrGatewayCredentialInactive) || secondCalls != 1 {
		t.Fatalf("stale in-flight read polluted cache: secondCalls=%d err=%v", secondCalls, err)
	}
}

func TestPostgresUplinkCacheRevocationAndCoverageQueries(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN are not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open connectivity admin pool: %v", err)
	}
	defer admin.Close()
	store, err := Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open connectivity runtime store: %v", err)
	}
	defer store.Close()

	const (
		tenantID   = "0199b000-0000-7000-8000-000000000001"
		siteID     = "0199b000-1000-7000-8000-000000000001"
		gwActive   = "0199b000-2000-7000-8000-000000000001"
		gwRevoked  = "0199b000-2000-7000-8000-000000000002"
		gwRetired  = "0199b000-2000-7000-8000-000000000003"
		devActive  = "0199b000-3000-7000-8000-000000000001"
		devRevoked = "0199b000-3000-7000-8000-000000000002"
		ptActive   = "0199b000-4000-7000-8000-000000000001"
	)

	cleanup := func() {
		for _, table := range []string{"gateway_concentrator_audit", "gateway_credential_audit", "gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(ctx, "DELETE FROM connectivity."+table+" WHERE gateway_id IN ($1::uuid,$2::uuid,$3::uuid)", gwActive, gwRevoked, gwRetired)
		}
		_, _ = admin.Exec(ctx, `DELETE FROM connectivity.uplink_quarantine WHERE gateway_id IN ($1,$2,$3)`, gwActive, gwRevoked, gwRetired)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.telemetry_points WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.gateway_device_source_keys WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.devices WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM core_registry.sites WHERE tenant_id=$1::uuid`, tenantID)
		_, _ = admin.Exec(ctx, `DELETE FROM iam.tenants WHERE id=$1::uuid`, tenantID)
	}
	cleanup()
	t.Cleanup(cleanup)

	seedStatements := []string{
		fmt.Sprintf(`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES('%s','p0-cache-tenant','P0 Cache Tenant','UTC','USD','US','ACTIVE',1,now(),now())`, tenantID),
		fmt.Sprintf(`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES('%s','%s','p0-cache-site','P0 Cache Site','UTC','ACTIVE',1,now(),now())`, siteID, tenantID),
		fmt.Sprintf(`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES ('%s','%s','%s','gw-active','GW Active','GATEWAY','ACTIVE',1,now(),now()), ('%s','%s','%s','gw-revoked','GW Revoked','GATEWAY','ACTIVE',1,now(),now()), ('%s','%s','%s','gw-retired','GW Retired','GATEWAY','ACTIVE',1,now(),now()), ('%s','%s','%s','dev-active','Dev Active','CHILLER','ACTIVE',1,now(),now()), ('%s','%s','%s','dev-revoked','Dev Revoked','CHILLER','ACTIVE',1,now(),now())`,
			gwActive, tenantID, siteID, gwRevoked, tenantID, siteID, gwRetired, tenantID, siteID, devActive, tenantID, siteID, devRevoked, tenantID, siteID),
		fmt.Sprintf(`INSERT INTO core_registry.gateway_device_source_keys(id,tenant_id,site_id,gateway_device_id,source_key,device_id,status,revision,created_at,updated_at) VALUES ('0199b000-5000-7000-8000-000000000001','%s','%s','%s','DEV-A','%s','ACTIVE',1,now(),now()), ('0199b000-5000-7000-8000-000000000002','%s','%s','%s','DEV-R','%s','ACTIVE',1,now(),now())`,
			tenantID, siteID, gwActive, devActive, tenantID, siteID, gwRevoked, devRevoked),
		fmt.Sprintf(`INSERT INTO core_registry.telemetry_points(id,tenant_id,site_id,reporting_device_id,point_code,source_key,display_name,point_type,value_type,unit,writable,sample_interval_ms,publish_interval_ms,stale_after_ms,source_metadata,status,revision,created_at,updated_at) VALUES ('%s','%s','%s','%s','temp','temp','Temp','TELEMETRY','NUMBER','Cel',false,1000,1000,60000,'{}'::jsonb,'ACTIVE',1,now(),now())`,
			ptActive, tenantID, siteID, devActive),
		fmt.Sprintf(`INSERT INTO connectivity.gateway_credentials(id,tenant_id,gateway_id,certificate_fingerprint_sha256,status,valid_from,valid_until,revoked_at,created_at,updated_at) VALUES ('0199b000-6000-7000-8000-000000000001','%s','%s','%s','ACTIVE',now()-interval '1 hour',now()+interval '24 hours',NULL,now(),now()), ('0199b000-6000-7000-8000-000000000002','%s','%s','%s','REVOKED',now()-interval '2 hours',now()+interval '24 hours',now()-interval '1 hour',now(),now()), ('0199b000-6000-7000-8000-000000000003','%s','%s','%s','ACTIVE',now()-interval '1 hour',now()+interval '24 hours',NULL,now(),now())`,
			tenantID, gwActive, "a0b1c2d3e4f50000000000000000000000000000000000000000000000000001",
			tenantID, gwRevoked, "a0b1c2d3e4f50000000000000000000000000000000000000000000000000002",
			tenantID, gwRetired, "a0b1c2d3e4f50000000000000000000000000000000000000000000000000003"),
	}
	for _, stmt := range seedStatements {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed connectivity fixture: %v", err)
		}
	}

	children, err := store.ListGatewayChildDeviceIDs(ctx, gwActive)
	if err != nil {
		t.Fatalf("ListGatewayChildDeviceIDs: %v", err)
	}
	if len(children) != 1 || children[0] != devActive {
		t.Fatalf("ListGatewayChildDeviceIDs = %v, want [%s]", children, devActive)
	}

	managed, err := store.ListAllActiveManagedDeviceIDs(ctx)
	if err != nil {
		t.Fatalf("ListAllActiveManagedDeviceIDs: %v", err)
	}
	var sawActive, sawRevoked bool
	for _, id := range managed {
		if id == devActive {
			sawActive = true
		}
		if id == devRevoked {
			sawRevoked = true
		}
	}
	if !sawActive || sawRevoked {
		t.Fatalf("ListAllActiveManagedDeviceIDs sawActive=%v sawRevoked=%v, managed=%v", sawActive, sawRevoked, managed)
	}

	scopes, err := store.ListActiveManagedGatewayScopes(ctx)
	if err != nil {
		t.Fatalf("ListActiveManagedGatewayScopes: %v", err)
	}
	var sawActiveScope, sawRevokedOrRetiredScope bool
	for _, scope := range scopes {
		switch scope.GatewayID {
		case gwActive:
			sawActiveScope = len(scope.DeviceIDs) == 1 && scope.DeviceIDs[0] == devActive
		case gwRevoked, gwRetired:
			sawRevokedOrRetiredScope = true
		}
	}
	if !sawActiveScope || sawRevokedOrRetiredScope {
		t.Fatalf("ListActiveManagedGatewayScopes unexpected scopes=%+v", scopes)
	}

	// Warm ResolveGateway and ResolveBindings caches on gwActive, then verify
	// Revoke and RevokeRetiredGateway immediately evict caches and invoke onRevoke.
	gwObj, err := store.ResolveGateway(ctx, gwActive)
	if err != nil {
		t.Fatalf("initial ResolveGateway gwActive: %v", err)
	}
	bindings, err := store.ResolveBindings(ctx, gwObj, []adapter.DevicePointQuery{
		{SourceKey: "DEV-A", PointCode: "temp"},
		{SourceKey: "DEV-A", PointCode: "missing-pt"},
		{SourceKey: "MISSING-DEV", PointCode: "temp"},
	})
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	if bindings.Devices["DEV-A"] != devActive || bindings.Points[adapter.PointBindingKey(devActive, "temp")] == nil {
		t.Fatalf("unexpected ResolveBindings result: %+v", bindings)
	}
	if _, ok := bindings.Devices["MISSING-DEV"]; ok || bindings.Points[adapter.PointBindingKey(devActive, "missing-pt")] != nil {
		t.Fatalf("unexpected missing bindings entry: %+v", bindings)
	}
	if _, err := store.ResolveGateway(ctx, gwRetired); err != nil {
		t.Fatalf("initial ResolveGateway gwRetired: %v", err)
	}
	service := NewCredentialService(store, &CertificateAuthority{})
	var revokedIDs []string
	service.SetRevokeHook(func(gatewayID string) {
		revokedIDs = append(revokedIDs, gatewayID)
	})
	claims := registryauth.GrantClaims{TenantID: tenantID, PrincipalID: gwActive, AllowedSiteIDs: []string{siteID}}
	if err := service.Revoke(ctx, claims, gwActive); err != nil {
		t.Fatalf("service.Revoke: %v", err)
	}
	if _, err := store.ResolveGateway(ctx, gwActive); !errors.Is(err, adapter.ErrGatewayCredentialInactive) {
		t.Fatalf("ResolveGateway after Revoke = %v, want ErrGatewayCredentialInactive", err)
	}

	if _, err := admin.Exec(ctx, `UPDATE core_registry.devices SET status = 'RETIRED', revision = 2 WHERE id = $1::uuid`, gwRetired); err != nil {
		t.Fatalf("retire gwRetired in registry: %v", err)
	}
	if _, err := store.ResolveGateway(ctx, gwRetired); err != nil {
		t.Fatalf("cached ResolveGateway before RevokeRetiredGateway: %v", err)
	}
	if err := service.RevokeRetiredGateway(ctx, tenantID, gwRetired); err != nil {
		t.Fatalf("service.RevokeRetiredGateway: %v", err)
	}
	if _, err := store.ResolveGateway(ctx, gwRetired); !errors.Is(err, adapter.ErrGatewayUnknown) {
		t.Fatalf("ResolveGateway after RevokeRetiredGateway = %v, want ErrGatewayUnknown", err)
	}
	if len(revokedIDs) != 2 || revokedIDs[0] != gwActive || revokedIDs[1] != gwRetired {
		t.Fatalf("onRevoke hook invocations = %v, want [%s %s]", revokedIDs, gwActive, gwRetired)
	}
}

func TestBuildGatewayCoverageScopesSortsDeduplicatesAndDropsEmptyGateways(t *testing.T) {
	scopes := adapter.BuildGatewayCoverageScopes(map[string][]string{
		"gw-b":     {" dev-2 ", "dev-1", "dev-2", ""},
		"gw-a":     {"dev-3"},
		"gw-empty": {"   "},
	})
	if len(scopes) != 2 {
		t.Fatalf("scopes=%+v, want 2 non-empty sorted gateways", scopes)
	}
	if scopes[0].GatewayID != "gw-a" || len(scopes[0].DeviceIDs) != 1 || scopes[0].DeviceIDs[0] != "dev-3" {
		t.Fatalf("scopes[0]=%+v", scopes[0])
	}
	if scopes[1].GatewayID != "gw-b" || len(scopes[1].DeviceIDs) != 2 || scopes[1].DeviceIDs[0] != "dev-1" || scopes[1].DeviceIDs[1] != "dev-2" {
		t.Fatalf("scopes[1]=%+v", scopes[1])
	}
}
