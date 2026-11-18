package planttwin

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresLoaderRealDatabase(t *testing.T) {
	optDSN := os.Getenv("PLANTTWIN_TEST_POSTGRES_DSN")
	adminDSN := os.Getenv("PLANTTWIN_TEST_ADMIN_DSN")
	if optDSN == "" || adminDSN == "" {
		t.Skip("PLANTTWIN_TEST_POSTGRES_DSN and PLANTTWIN_TEST_ADMIN_DSN are required for PostgresLoader integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	defer adminPool.Close()

	const (
		tenantID   = "0199f000-0000-7000-8000-000000000001"
		siteID     = "0199f000-1000-7000-8000-000000000001"
		otherSite  = "0199f000-1000-7000-8000-000000000002"
		otherTen   = "0199f000-0000-7000-8000-000000000002"
		gatewayID  = "0199f000-4000-7000-8000-000000000099"
		meterDev   = "0199f000-4000-7000-8000-000000000001"
		tempDev    = "0199f000-4000-7000-8000-000000000002"
		btuDev     = "0199f000-4000-7000-8000-000000000003"
		chillerID  = "0199f000-3000-7000-8000-000000000001"
		chwpID     = "0199f000-3000-7000-8000-000000000002"
		loopID     = "0199f000-3000-7000-8000-000000000003"
		valveID    = "0199f000-3000-7000-8000-000000000004"
		pPowerID   = "0199f000-5000-7000-8000-000000000001"
		pTempID    = "0199f000-5000-7000-8000-000000000002"
		pSupplyID  = "0199f000-5000-7000-8000-000000000003"
		pLoadID    = "0199f000-5000-7000-8000-000000000004"
		schedID    = "0199f000-7100-7000-8000-000000000001"
		excID      = "0199f000-7200-7000-8000-000000000001"
		actorID    = "0199f000-9000-7000-8000-000000000001"
		validStart = "2026-01-01T00:00:00Z"
	)

	seedStatements := []struct {
		sql  string
		args []any
	}{
		{
			sql: `INSERT INTO iam.tenants (id, code, display_name, timezone, currency, country, status, revision, created_at, updated_at)
VALUES ($1::uuid, 'twin-test-tenant', 'Twin Test Tenant', 'Asia/Shanghai', 'CNY', 'CN', 'ACTIVE', 1, now(), now()),
       ($2::uuid, 'twin-other-tenant', 'Twin Other Tenant', 'Asia/Shanghai', 'CNY', 'CN', 'ACTIVE', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, otherTen},
		},
		{
			sql: `INSERT INTO core_registry.sites (id, tenant_id, code, display_name, timezone, status, revision, created_at, updated_at)
VALUES ($3::uuid, $1::uuid, 'twin-site-1', 'Twin Site 1', 'Asia/Shanghai', 'ACTIVE', 1, now(), now()),
       ($4::uuid, $2::uuid, 'twin-site-2', 'Twin Site 2', 'Asia/Shanghai', 'ACTIVE', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, otherTen, siteID, otherSite},
		},
		{
			sql: `INSERT INTO core_registry.assets (id, tenant_id, site_id, code, display_name, asset_type, status, rated_capacity, rated_power, nameplate, water_loop, branch_code, revision, created_at, updated_at)
VALUES ($3::uuid, $1::uuid, $2::uuid, 'chiller-1', '1# 主机', 'CHILLER', 'ACTIVE', 572, 127.5, '{"feederAuxiliaryPowerKw":18}'::jsonb, 'CHILLED_WATER', 'CH-A', 1, now(), now()),
       ($4::uuid, $1::uuid, $2::uuid, 'chwp-1', '1# 冷冻水泵', 'CHILLED_WATER_PUMP', 'ACTIVE', NULL, NULL, '{"unconfirmed":["ratedPowerKw","feederAuxiliaryPowerKw"],"feederAuxiliaryPowerKw":15}'::jsonb, 'CHILLED_WATER', 'CH-A', 1, now(), now()),
       ($5::uuid, $1::uuid, $2::uuid, 'chw-loop', '冷冻水环路', 'CHILLED_WATER_LOOP', 'ACTIVE', NULL, NULL, NULL, 'CHILLED_WATER', NULL, 1, now(), now()),
       ($6::uuid, $1::uuid, $2::uuid, 'mv-chw-a', 'A支路电动隔离阀', 'MOTORIZED_VALVE', 'ACTIVE', NULL, NULL, NULL, 'CHILLED_WATER', 'CH-A', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, chillerID, chwpID, loopID, valveID},
		},
		{
			sql: `INSERT INTO core_registry.asset_relationships (id, tenant_id, site_id, from_asset_id, to_asset_id, relationship_role, status, valid_from, valid_to, created_at, updated_at, revision)
VALUES ('0199f000-6600-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $4::uuid, $3::uuid, 'SERVES', 'ACTIVE', $7::timestamptz, NULL, now(), now(), 1),
       ('0199f000-6600-7000-8000-000000000002'::uuid, $1::uuid, $2::uuid, $3::uuid, $5::uuid, 'SERVES', 'ACTIVE', $7::timestamptz, NULL, now(), now(), 1),
       ('0199f000-6600-7000-8000-000000000003'::uuid, $1::uuid, $2::uuid, $6::uuid, $3::uuid, 'ISOLATES', 'ACTIVE', $7::timestamptz, NULL, now(), now(), 1),
       ('0199f000-6600-7000-8000-000000000004'::uuid, $1::uuid, $2::uuid, $3::uuid, $5::uuid, 'FEEDS_HEADER', 'ACTIVE', $7::timestamptz, NULL, now(), now(), 1)
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, chillerID, chwpID, loopID, valveID, validStart},
		},
		{
			sql: `INSERT INTO core_registry.devices (id, tenant_id, site_id, code, display_name, device_type, status, revision, created_at, updated_at)
VALUES ($3::uuid, $1::uuid, $2::uuid, 'gw-twin', 'Twin Gateway', 'GATEWAY', 'ACTIVE', 1, now(), now()),
       ($4::uuid, $1::uuid, $2::uuid, 'meter-twin', 'Twin Chiller Meter', 'METER', 'ACTIVE', 1, now(), now()),
       ($5::uuid, $1::uuid, $2::uuid, 'temp-twin', 'Twin Temp Concentrator', 'SENSOR_HUB', 'ACTIVE', 1, now(), now()),
       ($6::uuid, $1::uuid, $2::uuid, 'btu-twin', 'Twin BTU Calculator', 'BTU_METER', 'ACTIVE', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, gatewayID, meterDev, tempDev, btuDev},
		},
		{
			sql: `INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at)
VALUES ('0199f000-6100-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $3::uuid, 'src-meter-twin', $4::uuid, 'ACTIVE', 1, now(), now()),
       ('0199f000-6100-7000-8000-000000000002'::uuid, $1::uuid, $2::uuid, $3::uuid, 'src-temp-twin', $5::uuid, 'ACTIVE', 1, now(), now()),
       ('0199f000-6100-7000-8000-000000000003'::uuid, $1::uuid, $2::uuid, $3::uuid, 'src-btu-twin', $6::uuid, 'ACTIVE', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, gatewayID, meterDev, tempDev, btuDev},
		},
		{
			sql: `INSERT INTO core_registry.device_bindings (id, tenant_id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES ('0199f000-6400-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'METER', 'ACTIVE', $5::timestamptz, NULL, 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, meterDev, chillerID, validStart},
		},
		{
			sql: `INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, status, revision, created_at, updated_at)
VALUES ($6::uuid, $1::uuid, $2::uuid, $3::uuid, 'custom_kw', 'custom_kw', 'Chiller kW', 'TELEMETRY', 'NUMBER', 'kW', false, 60000, 60000, 180000, 'ACTIVE', 1, now(), now()),
       ($7::uuid, $1::uuid, $2::uuid, $4::uuid, 'probe_t1', 'probe_t1', 'CHW Leaving', 'TELEMETRY', 'NUMBER', 'Cel', false, 60000, 60000, 180000, 'ACTIVE', 1, now(), now()),
       ($8::uuid, $1::uuid, $2::uuid, $5::uuid, 'loop_sup_t', 'loop_sup_t', 'Supply Water', 'TELEMETRY', 'NUMBER', 'Cel', false, 60000, 60000, 180000, 'ACTIVE', 1, now(), now()),
       ($9::uuid, $1::uuid, $2::uuid, $5::uuid, 'loop_load', 'loop_load', 'Cooling Load', 'TELEMETRY', 'NUMBER', 'kW', false, 60000, 60000, 180000, 'ACTIVE', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, meterDev, tempDev, btuDev, pPowerID, pTempID, pSupplyID, pLoadID},
		},
		{
			sql: `INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, asset_id, binding_role, semantic_role, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES ('0199f000-6300-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $5::uuid, 'ASSET', $3::uuid, 'DESCRIBES', 'ACTIVE_POWER', 'ACTIVE', $9::timestamptz, NULL, 1, now(), now()),
       ('0199f000-6300-7000-8000-000000000002'::uuid, $1::uuid, $2::uuid, $6::uuid, 'ASSET', $3::uuid, 'DESCRIBES', 'CHW_LEAVING_TEMP', 'ACTIVE', $9::timestamptz, NULL, 1, now(), now()),
       ('0199f000-6300-7000-8000-000000000003'::uuid, $1::uuid, $2::uuid, $7::uuid, 'ASSET', $4::uuid, 'DESCRIBES', 'SUPPLY_WATER_TEMP', 'ACTIVE', $9::timestamptz, NULL, 1, now(), now()),
       ('0199f000-6300-7000-8000-000000000004'::uuid, $1::uuid, $2::uuid, $8::uuid, 'ASSET', $4::uuid, 'DESCRIBES', 'COOLING_LOAD', 'ACTIVE', $9::timestamptz, NULL, 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, chillerID, loopID, pPowerID, pTempID, pSupplyID, pLoadID, validStart},
		},
		{
			sql: `INSERT INTO core_registry.operating_schedules (id, tenant_id, site_id, name, pre_cool_lead_minutes, coast_down_lag_minutes, revision, updated_by, created_at, updated_at)
VALUES ($3::uuid, $1::uuid, $2::uuid, 'Default Twin Schedule', 30, 15, 1, $4::uuid, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, schedID, actorID},
		},
		{
			sql: `INSERT INTO core_registry.operating_schedule_slots (id, tenant_id, site_id, schedule_id, schedule_revision, day_of_week, start_minute, end_minute, target_supply_temp_c, span_group_id, created_at)
VALUES ('0199f000-7110-7000-8000-000000000001'::uuid, $1::uuid, $2::uuid, $3::uuid, 1, 1, 540, 1080, 7.5, NULL, now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, schedID},
		},
		{
			sql: `INSERT INTO core_registry.operating_exceptions (id, tenant_id, site_id, title, kind, start_date, end_date, replacement_slots, reason, status, requested_by, requested_by_display, decided_by, decided_by_display, decided_at, decision_note, revision, created_at, updated_at)
VALUES ($3::uuid, $1::uuid, $2::uuid, 'Planned Maintenance', 'MAINTENANCE_WINDOW', '2026-10-12'::date, '2026-10-12'::date, '[]'::jsonb, 'Valve check', 'APPROVED', $4::uuid, 'Tester', $4::uuid, 'Tester', now(), 'ok', 1, now(), now())
ON CONFLICT (id) DO NOTHING`,
			args: []any{tenantID, siteID, excID, actorID},
		},
	}
	for i, stmt := range seedStatements {
		if _, err := adminPool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed planttwin postgres fixture step %d: %v", i+1, err)
		}
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.operating_exceptions WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.operating_schedule_slots WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.operating_schedules WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.point_subject_bindings WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.telemetry_points WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.device_bindings WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.gateway_device_source_keys WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.devices WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.asset_relationships WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.assets WHERE tenant_id = $1::uuid AND site_id = $2::uuid`, tenantID, siteID)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM core_registry.sites WHERE id IN ($1::uuid, $2::uuid)`, siteID, otherSite)
		_, _ = adminPool.Exec(cleanCtx, `DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)`, tenantID, otherTen)
	})

	dsns := []struct {
		name string
		dsn  string
	}{
		{name: "optimization_runtime", dsn: optDSN},
	}
	if fddDSN := os.Getenv("PLANTTWIN_TEST_FDD_DSN"); fddDSN != "" {
		dsns = append(dsns, struct {
			name string
			dsn  string
		}{name: "fdd_runtime", dsn: fddDSN})
	}

	evalAt := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	for _, roleCase := range dsns {
		t.Run(roleCase.name, func(t *testing.T) {
			pool, poolErr := pgxpool.New(ctx, roleCase.dsn)
			if poolErr != nil {
				t.Fatalf("connect %s: %v", roleCase.name, poolErr)
			}
			defer pool.Close()

			loader, err := NewPostgresLoader(pool)
			if err != nil {
				t.Fatalf("NewPostgresLoader: %v", err)
			}
			twin, err := loader.LoadSiteTwin(ctx, SiteScope{TenantID: tenantID, SiteID: siteID}, evalAt)
			if err != nil {
				t.Fatalf("LoadSiteTwin: %v", err)
			}
			if twin.Timezone != "Asia/Shanghai" || twin.SiteControlMode != ControlModeRemoteAuto {
				t.Fatalf("unexpected site metadata: tz=%s mode=%s", twin.Timezone, twin.SiteControlMode)
			}

			chiller, ok := twin.UnitByID(chillerID)
			if !ok || chiller.FeederAuxiliaryPowerKW != 18 || chiller.RatedPowerKW == nil || *chiller.RatedPowerKW != 127.5 || chiller.WaterLoop != WaterLoopChilledWater || chiller.BranchCode != "CH-A" {
				t.Fatalf("unexpected chiller unit: %+v ok=%v", chiller, ok)
			}
			rels, err := loader.LoadAssetRelationships(ctx, SiteScope{TenantID: tenantID, SiteID: siteID}, evalAt)
			if err != nil {
				t.Fatalf("LoadAssetRelationships: %v", err)
			}
			if len(rels) != 4 {
				t.Fatalf("expected 4 relationships in plant_twin_asset_relationships_v1, got %d (%+v)", len(rels), rels)
			}
			if valves := twin.Graph.IsolatedBy(chillerID); len(valves) != 1 || valves[0] != valveID {
				t.Fatalf("expected valve isolating chiller, got %v", valves)
			}
			chwp, ok := twin.UnitByID(chwpID)
			if !ok || chwp.FeederAuxiliaryPowerKW != 0 {
				t.Fatalf("expected unconfirmed chwp feeder auxiliary power filtered to 0, got %+v ok=%v", chwp, ok)
			}

			kwPt, ok := twin.ResolveAssetPoint(chillerID, RoleActivePower)
			if !ok || kwPt.DeviceID != meterDev || kwPt.PointCode != "custom_kw" {
				t.Fatalf("expected chiller ACTIVE_POWER on meterDev, got %+v ok=%v", kwPt, ok)
			}
			chwPt, ok := twin.ResolveAssetPoint(chillerID, RoleCHWLeavingTemp)
			if !ok || chwPt.DeviceID != tempDev || chwPt.PointCode != "probe_t1" {
				t.Fatalf("expected split CHW_LEAVING_TEMP on tempDev, got %+v ok=%v", chwPt, ok)
			}
			supPt, ok := twin.ResolveAssetPoint(loopID, RoleSupplyWaterTemp)
			if !ok || supPt.DeviceID != btuDev || supPt.PointCode != "loop_sup_t" {
				t.Fatalf("expected SUPPLY_WATER_TEMP on loop btuDev, got %+v ok=%v", supPt, ok)
			}

			chillers := twin.Graph.EquipmentServingLoop(loopID, AssetTypeChiller)
			if len(chillers) != 1 || chillers[0].AssetID != chillerID {
				t.Fatalf("expected chiller serving loop, got %+v", chillers)
			}
			chwps := twin.Graph.EquipmentServingUnit(chillerID, AssetTypeChilledWaterPump)
			if len(chwps) != 1 || chwps[0].AssetID != chwpID {
				t.Fatalf("expected chwp serving chiller, got %+v", chwps)
			}

			schedEval, err := twin.EvaluateSchedule(evalAt)
			if err != nil {
				t.Fatalf("EvaluateSchedule: %v", err)
			}
			if !schedEval.Suppressed || schedEval.OperatingState != OperatingStateExceptionClosed {
				t.Fatalf("expected maintenance suppression on 2026-10-12, got %+v", schedEval)
			}

			if _, err := loader.LoadSiteTwin(ctx, SiteScope{TenantID: tenantID, SiteID: otherSite}, evalAt); !errors.Is(err, ErrSiteNotFound) {
				t.Fatalf("expected ErrSiteNotFound on cross-tenant site read, got %v", err)
			}
		})
	}
}
