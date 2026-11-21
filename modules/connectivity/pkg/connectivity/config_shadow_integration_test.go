package connectivity_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

func TestConfigShadowPostgresRLSConcurrencyAndDrift(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("CONNECTIVITY_ADMIN_DSN"), os.Getenv("CONNECTIVITY_POSTGRES_DSN")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("CONNECTIVITY_ADMIN_DSN and CONNECTIVITY_POSTGRES_DSN required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()

	const tenantA = "0194f000-1000-7000-8000-000000000011"
	const tenantB = "0194f000-1000-7000-8000-000000000012"
	const siteA = "0194f000-2000-7000-8000-000000000011"
	const gatewayA = "0194f000-3000-7000-8000-000000000011"
	const actorA = "0194f000-4000-7000-8000-000000000011"

	cleanup := func() {
		_, _ = admin.Exec(ctx, "TRUNCATE TABLE connectivity.gateway_concentrator_audit, connectivity.gateway_credential_audit")
		for _, table := range []string{"gateway_reported_configs", "gateway_desired_configs", "gateway_enrollments", "gateway_credentials"} {
			_, _ = admin.Exec(ctx, "DELETE FROM connectivity."+table+" WHERE gateway_id = $1::uuid", gatewayA)
		}
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.devices WHERE id = $1::uuid", gatewayA)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.sites WHERE id = $1::uuid", siteA)
		_, _ = admin.Exec(ctx, "DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
	}
	cleanup()
	t.Cleanup(cleanup)

	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'shadow-test-a','Shadow Test A','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenantA}},
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'shadow-test-b','Shadow Test B','UTC','USD','US','ACTIVE',1,now(),now())`, []any{tenantB}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'shadow-site-a','Shadow Site A','UTC','ACTIVE',1,now(),now())`, []any{tenantA, siteA}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES($3,$1,$2,'shadow-gw-a','Shadow GW A','GATEWAY','ACTIVE',1,now(),now())`, []any{tenantA, siteA, gatewayA}},
	} {
		if _, err := admin.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatalf("seed %s: %v", step.sql, err)
		}
	}

	store, err := connectivity.Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime store: %v", err)
	}
	defer store.Close()

	claimsA := registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA}}
	claimsB := registryauth.GrantClaims{TenantID: tenantB, PrincipalID: actorA, AllowedSiteIDs: []string{siteA}}

	initial, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || initial.SyncStatus != connectivity.ShadowSyncStatusUnconfigured {
		t.Fatalf("initial shadow = %+v, err=%v", initial, err)
	}

	// Wrong tenant must be rejected.
	if _, err := store.GetGatewayConfigShadowForOperator(ctx, claimsB, gatewayA); !errors.Is(err, connectivity.ErrCredentialForbidden) {
		t.Fatalf("cross-tenant shadow read got %v, want ErrCredentialForbidden", err)
	}

	// Expected revision 1 when no row exists -> 409 conflict.
	revOne := int64(1)
	if _, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &revOne,
		Reason:                  "must conflict on missing row",
	}); !errors.Is(err, connectivity.ErrConfigShadowConflict) {
		t.Fatalf("expected ErrConfigShadowConflict, got %v", err)
	}

	revZero := int64(0)
	mappings := []connectivity.GatewayPointMapping{{
		SourceChannel:    "t10",
		PrimaryDeviceKey: "chiller-a",
		PrimaryPointCode: "chw_entering_temp",
		CopyTargets:      []connectivity.PointCopyTarget{{DeviceKey: "plant-header", PointCode: "chw_branch_temp"}},
	}}
	policies := []connectivity.GatewaySamplingPolicy{{
		DeviceKey:         "chiller-a",
		PointCode:         "*",
		SampleIntervalMS:  5000,
		PublishIntervalMS: 10000,
	}}
	created, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &revZero,
		PointMappings:           &mappings,
		SamplingPolicies:        &policies,
		Reason:                  "initial mapping shadow",
	})
	if err != nil || created.Desired == nil || created.Desired.DesiredRevision != 1 || created.SyncStatus != connectivity.ShadowSyncStatusPendingSync {
		t.Fatalf("created shadow = %+v, err=%v", created, err)
	}

	// Partial PUT omitting pointMappings must preserve existing pointMappings.
	updatedPolicies := []connectivity.GatewaySamplingPolicy{{
		DeviceKey:         "chiller-a",
		PointCode:         "*",
		SampleIntervalMS:  2500,
		PublishIntervalMS: 5000,
	}}
	revOneCur := int64(1)
	partial, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &revOneCur,
		SamplingPolicies:        &updatedPolicies,
		Reason:                  "partial sampling policy update",
	})
	if err != nil || partial.Desired == nil || partial.Desired.DesiredRevision != 2 || len(partial.Desired.PointMappings) != 1 || len(partial.Desired.SamplingPolicies) != 1 || partial.Desired.SamplingPolicies[0].SampleIntervalMS != 2500 {
		t.Fatalf("partial shadow = %+v, err=%v", partial, err)
	}

	// Restore rev 1 expectations for report tests below (rev=3 matches mappings+policies).
	revTwo := int64(2)
	_, err = store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &revTwo,
		SamplingPolicies:        &policies,
		Reason:                  "restore policies",
	})
	if err != nil {
		t.Fatalf("restore desired: %v", err)
	}

	// Stale expected revision 0 after row exists -> conflict.
	if _, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &revZero,
		Reason:                  "stale rev 0",
	}); !errors.Is(err, connectivity.ErrConfigShadowConflict) {
		t.Fatalf("stale rev 0 got %v, want ErrConfigShadowConflict", err)
	}

	// Delta pull since 0 -> hasDelta=true.
	pull1, err := store.PullGatewayDesiredConfig(ctx, gatewayA, 0)
	if err != nil || !pull1.HasDelta || pull1.DesiredRevision != 3 || pull1.Desired == nil {
		t.Fatalf("pull1 = %+v, err=%v", pull1, err)
	}
	// Delta pull since 3 -> hasDelta=false, omits desired body.
	pull0, err := store.PullGatewayDesiredConfig(ctx, gatewayA, 3)
	if err != nil || pull0.HasDelta || pull0.Desired != nil {
		t.Fatalf("pull0 = %+v, err=%v", pull0, err)
	}

	// Invalid pointCode in edge report must be rejected (ErrConfigShadowInvalid).
	if _, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 3,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    []connectivity.GatewayPointMapping{{SourceChannel: "t1", PrimaryDeviceKey: "chiller-a", PrimaryPointCode: "BadUpper"}},
		SamplingPolicies: policies,
	}); !errors.Is(err, connectivity.ErrConfigShadowInvalid) {
		t.Fatalf("invalid reported pointMapping got %v, want ErrConfigShadowInvalid", err)
	}

	// Mismatched report -> DRIFTED.
	drifted, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 3,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    []connectivity.GatewayPointMapping{},
		SamplingPolicies: policies,
	})
	if err != nil || drifted.SyncStatus != connectivity.ShadowSyncStatusDrifted || len(drifted.Drift) != 1 {
		t.Fatalf("drifted = %+v, err=%v", drifted, err)
	}

	// Decreasing reportedRevision -> 409 conflict.
	if _, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 2,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
	}); !errors.Is(err, connectivity.ErrConfigShadowConflict) {
		t.Fatalf("stale reportedRevision got %v, want ErrConfigShadowConflict", err)
	}

	// Matching report -> SYNCED.
	synced, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 3,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
	})
	if err != nil || synced.SyncStatus != connectivity.ShadowSyncStatusSynced || len(synced.Drift) != 0 {
		t.Fatalf("synced = %+v, err=%v", synced, err)
	}

	// Server-owned concentrator state cannot be spoofed or wiped by subsequent edge report.
	paramsUnit1 := connectivity.ConcentratorShadowParams{
		ModbusID: 1, BaudRateCode: 3, OffLineTime: 60, DataUplinkCycle: 30,
		LocalNet: 100, TimeMark: 1, RfTxPower: 22, RfFrequency: 470,
		RfBandWidth: 2, RfSf: 7, RfLeadCode: 8, TxTimeout: 3000, RxTimeout: 3000, RssiOut: 1,
	}
	revConc, err := store.UpsertConcentratorShadowState(ctx, tenantA, gatewayA, 1, &paramsUnit1, nil, actorA, "applied online", true)
	if err != nil || revConc != 4 {
		t.Fatalf("UpsertConcentratorShadowState rev=%d err=%v", revConc, err)
	}
	afterSpoofReport, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision:    4,
		AppliedStatus:       connectivity.AppliedStatusApplied,
		PointMappings:       mappings,
		SamplingPolicies:    policies,
		ConcentratorConfigs: map[string]connectivity.ConcentratorUnitShadow{},
	})
	if err != nil || afterSpoofReport.SyncStatus != connectivity.ShadowSyncStatusSynced || afterSpoofReport.Reported == nil || afterSpoofReport.Reported.ConcentratorConfigs["1"].Params == nil || afterSpoofReport.Reported.ConcentratorConfigs["1"].Params.RfTxPower != 22 {
		t.Fatalf("expected server-owned concentrator state preserved on edge report, got %+v err=%v", afterSpoofReport, err)
	}

	// Offline modbusId rename stays under old key "1" in desired until hardware reconcile succeeds and rekeys to "7".
	paramsRename := paramsUnit1
	paramsRename.ModbusID = 7
	revStagedRename, err := store.UpsertConcentratorShadowState(ctx, tenantA, gatewayA, 1, &paramsRename, nil, actorA, "offline modbusId rename", false)
	if err != nil || revStagedRename != 5 {
		t.Fatalf("UpsertConcentratorShadowState offline rename rev=%d err=%v", revStagedRename, err)
	}
	stagedShadow, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || stagedShadow.SyncStatus != connectivity.ShadowSyncStatusPendingSync || stagedShadow.Desired.ConcentratorConfigs["1"].Params == nil || stagedShadow.Desired.ConcentratorConfigs["1"].Params.ModbusID != 7 {
		t.Fatalf("expected staged offline rename kept under key 1, got %+v err=%v", stagedShadow, err)
	}

	// Edge reports rev 5 with REJECTED mapping drift -> unreachable reconcile must preserve REJECTED + non-conc drift.
	rev5Err := "invalid mapping rejected"
	if _, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 5,
		AppliedStatus:    connectivity.AppliedStatusRejected,
		PointMappings:    []connectivity.GatewayPointMapping{},
		SamplingPolicies: policies,
		DriftDetails: []connectivity.DriftDetail{{
			Section: connectivity.DriftSectionPointMappings,
			Target:  "chiller-a/chw_entering_temp",
			Reason:  connectivity.DriftReasonEdgeApplyFailed,
			Detail:  "channel conflict on edge",
		}},
		ErrorMessage: &rev5Err,
	}); err != nil {
		t.Fatalf("ReportGatewayAppliedConfig rev 5 REJECTED failed: %v", err)
	}

	// Reconcile succeeds for unit 1 -> 7: rekeys desired & reported to "7", while preserving edge REJECTED status + mapping drift.
	if err := store.RecordConcentratorReconcileResult(ctx, tenantA, gatewayA, 5,
		map[string]connectivity.ConcentratorUnitShadow{"7": {Params: &paramsRename}},
		map[string]string{"1": "7"},
		nil, nil, false,
	); err != nil {
		t.Fatalf("RecordConcentratorReconcileResult rename failed: %v", err)
	}
	afterRename, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || afterRename.Desired.ConcentratorConfigs["7"].Params == nil || afterRename.Reported.ConcentratorConfigs["7"].Params == nil {
		t.Fatalf("expected rekeyed unit 7 in both desired and reported, got %+v err=%v", afterRename, err)
	}
	if _, hasOld := afterRename.Desired.ConcentratorConfigs["1"]; hasOld {
		t.Fatalf("expected old key 1 removed from desired after rename")
	}
	if afterRename.Reported.AppliedStatus != connectivity.AppliedStatusRejected || afterRename.SyncStatus != connectivity.ShadowSyncStatusDrifted {
		t.Fatalf("expected edge REJECTED status preserved after concentrator reconcile, got %+v", afterRename)
	}

	// Path 1: Mixed push (pointMappings + concentratorConfigs): edge reports APPLIED (downgraded
	// to PARTIAL because concentrator lags), then concentrator reconcile succeeds -> must recover to APPLIED + SYNCED.
	rev5 := int64(5)
	paramsRev6 := paramsRename
	paramsRev6.RfTxPower = 24
	concRev6 := map[string]connectivity.ConcentratorUnitShadow{"7": {Params: &paramsRev6}}
	if _, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev5,
		PointMappings:           &mappings,
		SamplingPolicies:        &policies,
		ConcentratorConfigs:     &concRev6,
		Reason:                  "mixed rev 6 recovery check",
	}); err != nil {
		t.Fatalf("UpdateGatewayDesiredConfig rev 6 failed: %v", err)
	}
	downgraded, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 6,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
	})
	if err != nil || downgraded.Reported == nil || downgraded.Reported.AppliedStatus != connectivity.AppliedStatusPartial || downgraded.SyncStatus != connectivity.ShadowSyncStatusDrifted {
		t.Fatalf("expected APPLIED downgraded to PARTIAL/DRIFTED while concentrator lags, got %+v err=%v", downgraded, err)
	}
	if err := store.RecordConcentratorReconcileResult(ctx, tenantA, gatewayA, 6, concRev6, nil, nil, nil, false); err != nil {
		t.Fatalf("RecordConcentratorReconcileResult rev 6 recovery failed: %v", err)
	}
	recoveredMixed, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || recoveredMixed.Reported == nil || recoveredMixed.Reported.AppliedStatus != connectivity.AppliedStatusApplied || recoveredMixed.Reported.ErrorMessage != nil || recoveredMixed.SyncStatus != connectivity.ShadowSyncStatusSynced || len(recoveredMixed.Drift) != 0 {
		t.Fatalf("expected mixed reconcile completion to clear PARTIAL latch -> APPLIED/SYNCED, got %+v err=%v", recoveredMixed, err)
	}

	// Path 2: Transient concentrator reconcile failure (PARTIAL + ErrorMessage) at rev 7 must
	// recover cleanly to APPLIED/SYNCED with nil ErrorMessage when retry succeeds at same rev 7.
	rev6 := int64(6)
	paramsRev7 := paramsRename
	paramsRev7.RfTxPower = 26
	concRev7 := map[string]connectivity.ConcentratorUnitShadow{"7": {Params: &paramsRev7}}
	if _, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev6,
		PointMappings:           &mappings,
		SamplingPolicies:        &policies,
		ConcentratorConfigs:     &concRev7,
		Reason:                  "transient failure retry check",
	}); err != nil {
		t.Fatalf("UpdateGatewayDesiredConfig rev 7 failed: %v", err)
	}
	if _, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 7,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
	}); err != nil {
		t.Fatalf("ReportGatewayAppliedConfig rev 7 failed: %v", err)
	}
	concErr := "unit 7 params: transient timeout"
	if err := store.RecordConcentratorReconcileResult(ctx, tenantA, gatewayA, 7, nil, nil, []connectivity.DriftDetail{{
		Section: connectivity.DriftSectionConcentrator,
		Target:  "unit:7.params",
		Reason:  connectivity.DriftReasonEdgeApplyFailed,
		Detail:  "transient timeout",
	}}, &concErr, false); err != nil {
		t.Fatalf("RecordConcentratorReconcileResult rev 7 failure record failed: %v", err)
	}
	if err := store.RecordConcentratorReconcileResult(ctx, tenantA, gatewayA, 7, concRev7, nil, nil, nil, false); err != nil {
		t.Fatalf("RecordConcentratorReconcileResult rev 7 retry recovery failed: %v", err)
	}
	recoveredRetry, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || recoveredRetry.Reported == nil || recoveredRetry.Reported.AppliedStatus != connectivity.AppliedStatusApplied || recoveredRetry.Reported.ErrorMessage != nil || recoveredRetry.SyncStatus != connectivity.ShadowSyncStatusSynced || len(recoveredRetry.Drift) != 0 {
		t.Fatalf("expected retry completion to clear PARTIAL and ErrorMessage -> APPLIED/SYNCED, got %+v err=%v", recoveredRetry, err)
	}

	// DriverConfigs + AutonomyPolicy: persist, canonicalize, tri-state PUT, pull/report round-trip, and concentrator preservation.
	rev7 := int64(7)
	writeFC16 := 16
	bitZero := 0
	driversRev8 := []connectivity.GatewayModbusDriverConfig{
		{
			DeviceKey: "vfd-b", DriverType: "MODBUS_RTU", Endpoint: "/dev/ttyS1", UnitID: 4, CapabilityProfile: "VARIABLE_SPEED_PUMP", TimeoutMS: 1200, Retries: 2,
			Registers: []connectivity.GatewayModbusRegisterMapping{
				{PointCode: "run_cmd", SemanticChannel: "StartCommand", FunctionCode: 3, WriteFunctionCode: &writeFC16, Address: 8501, RegisterCount: 1, RawType: "BIT_BOOL", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", BitIndex: &bitZero, Scale: 1, Offset: 0, PollPriority: "VERY_HIGH"},
				{PointCode: "drive_state", SemanticChannel: "RunState", FunctionCode: 3, Address: 8500, RegisterCount: 1, RawType: "ENUM16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: "HIGH", EnumMap: map[string]string{"0": "OFF", "1": "RUN"}},
			},
		},
		{
			DeviceKey: "chiller-a", DriverType: "MODBUS_TCP", Endpoint: "10.1.1.10:502", UnitID: 1, TimeoutMS: 2000, Retries: 1,
			Registers: []connectivity.GatewayModbusRegisterMapping{
				{PointCode: "chw_temp", FunctionCode: 4, Address: 10, RegisterCount: 2, RawType: "FLOAT32", ByteOrder: "BIG_ENDIAN", WordOrder: "LITTLE_ENDIAN", Scale: 0.1, Offset: 0, PollPriority: "MEDIUM"},
			},
		},
	}
	autonomyRev8 := connectivity.GatewayAutonomyPolicy{
		Enabled:                            true,
		OfflineGraceSeconds:                120,
		DefaultChilledWaterSupplySetpointC: 7.0,
		DefaultPumpFrequencyHz:             35.0,
		MinPumpFrequencyHz:                 20.0,
		MaxPumpFrequencyHz:                 48.0,
		MinRunSeconds:                      300,
		MinStopSeconds:                     180,
		SnapshotIntervalSeconds:            60,
	}
	shadowRev8, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev7,
		DriverConfigs:           &driversRev8,
		AutonomyPolicy:          connectivity.OptionalAutonomyPolicy{Set: true, Value: &autonomyRev8},
		Reason:                  "set driverConfigs and autonomyPolicy",
	})
	if err != nil || shadowRev8.Desired == nil || shadowRev8.Desired.DesiredRevision != 8 || len(shadowRev8.Desired.DriverConfigs) != 2 || shadowRev8.Desired.AutonomyPolicy == nil {
		t.Fatalf("rev 8 update = %+v, err=%v", shadowRev8, err)
	}
	if shadowRev8.Desired.DriverConfigs[0].DeviceKey != "chiller-a" || shadowRev8.Desired.DriverConfigs[1].Registers[0].PointCode != "drive_state" {
		t.Fatalf("expected canonicalized driver/register sorting, got %+v", shadowRev8.Desired.DriverConfigs)
	}

	// Pull returns driverConfigs and autonomyPolicy; LoadGatewayConfigShadowByGatewayID matches.
	pulledRev8, err := store.PullGatewayDesiredConfig(ctx, gatewayA, 7)
	if err != nil || !pulledRev8.HasDelta || pulledRev8.Desired == nil || len(pulledRev8.Desired.DriverConfigs) != 2 || pulledRev8.Desired.AutonomyPolicy == nil || pulledRev8.Desired.AutonomyPolicy.OfflineGraceSeconds != 120 {
		t.Fatalf("PullGatewayDesiredConfig rev 8 = %+v, err=%v", pulledRev8, err)
	}
	loadedByGW, err := store.LoadGatewayConfigShadowByGatewayID(ctx, gatewayA)
	if err != nil || loadedByGW.Desired == nil || len(loadedByGW.Desired.DriverConfigs) != 2 || loadedByGW.Desired.AutonomyPolicy == nil {
		t.Fatalf("LoadGatewayConfigShadowByGatewayID = %+v, err=%v", loadedByGW, err)
	}

	// Edge report with unsorted driverConfigs & matching autonomyPolicy -> SYNCED.
	unsortedReportDrivers := slices.Clone(driversRev8)
	syncedRev8, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 8,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
		DriverConfigs:    unsortedReportDrivers,
		AutonomyPolicy:   &autonomyRev8,
	})
	if err != nil || syncedRev8.SyncStatus != connectivity.ShadowSyncStatusSynced || len(syncedRev8.Drift) != 0 {
		t.Fatalf("expected SYNCED with unsorted driverConfigs, got %+v err=%v", syncedRev8, err)
	}

	// Edge report with PARTIAL + EDGE_APPLY_FAILED on DRIVER_CONFIGS and mismatched autonomyPolicy -> DRIFTED preserving EDGE_APPLY_FAILED.
	driftedAutonomy := autonomyRev8
	driftedAutonomy.OfflineGraceSeconds = 300
	partialDriversErr := "driver vfd-b timeout"
	partialRev8, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 8,
		AppliedStatus:    connectivity.AppliedStatusPartial,
		PointMappings:    mappings,
		SamplingPolicies: policies,
		DriverConfigs:    unsortedReportDrivers[:1], // omit chiller-a -> MISSING_ON_EDGE
		AutonomyPolicy:   &driftedAutonomy,
		DriftDetails: []connectivity.DriftDetail{{
			Section: connectivity.DriftSectionDriverConfigs,
			Target:  "vfd-b",
			Reason:  connectivity.DriftReasonEdgeApplyFailed,
			Detail:  "modbus rtu poll timeout",
		}},
		ErrorMessage: &partialDriversErr,
	})
	if err != nil || partialRev8.SyncStatus != connectivity.ShadowSyncStatusDrifted || len(partialRev8.Drift) < 3 {
		t.Fatalf("expected DRIFTED with computed + EDGE_APPLY_FAILED drifts, got %+v err=%v", partialRev8, err)
	}

	// Partial PUT omitting driverConfigs & autonomyPolicy preserves both.
	rev8 := int64(8)
	preservedRev9, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev8,
		Reason:                  "preserve driverConfigs and autonomyPolicy",
	})
	if err != nil || preservedRev9.Desired == nil || len(preservedRev9.Desired.DriverConfigs) != 2 || preservedRev9.Desired.AutonomyPolicy == nil || preservedRev9.Desired.AutonomyPolicy.OfflineGraceSeconds != 120 {
		t.Fatalf("partial PUT omitting drivers/autonomy should preserve both, got %+v err=%v", preservedRev9, err)
	}

	// Concentrator shadow upsert preserves driverConfigs & autonomyPolicy too.
	paramsRev10 := paramsRename
	paramsRev10.RfTxPower = 25
	concRev10, err := store.UpsertConcentratorShadowState(ctx, tenantA, gatewayA, 7, &paramsRev10, nil, actorA, "keep drivers and autonomy", false)
	if err != nil || concRev10 != 10 {
		t.Fatalf("UpsertConcentratorShadowState rev=%d err=%v", concRev10, err)
	}
	afterConcShadow, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || len(afterConcShadow.Desired.DriverConfigs) != 2 || afterConcShadow.Desired.AutonomyPolicy == nil {
		t.Fatalf("expected UpsertConcentratorShadowState to preserve driverConfigs & autonomyPolicy, got %+v err=%v", afterConcShadow, err)
	}

	// Tri-state clear: explicit autonomyPolicy=null clears DB column to NULL while preserving driverConfigs; empty driverConfigs=[] clears driver_configs to [].
	rev10 := int64(10)
	clearedAutonomyShadow, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev10,
		AutonomyPolicy:          connectivity.OptionalAutonomyPolicy{Set: true, Value: nil},
		Reason:                  "clear autonomyPolicy only",
	})
	if err != nil || clearedAutonomyShadow.Desired == nil || clearedAutonomyShadow.Desired.AutonomyPolicy != nil || len(clearedAutonomyShadow.Desired.DriverConfigs) != 2 {
		t.Fatalf("expected autonomyPolicy cleared and driverConfigs preserved, got %+v err=%v", clearedAutonomyShadow, err)
	}
	rev11 := int64(11)
	emptyDrivers := []connectivity.GatewayModbusDriverConfig{}
	clearedDriversShadow, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev11,
		DriverConfigs:           &emptyDrivers,
		Reason:                  "clear driverConfigs",
	})
	if err != nil || clearedDriversShadow.Desired == nil || len(clearedDriversShadow.Desired.DriverConfigs) != 0 || clearedDriversShadow.Desired.AutonomyPolicy != nil {
		t.Fatalf("expected driverConfigs cleared to empty slice, got %+v err=%v", clearedDriversShadow, err)
	}

	// Template driver upsert: skips without endpoint/unitId on new driver, materializes on full input,
	// stays idempotent when unchanged, inherits existing endpoint/unitId on update, and enforces tenant isolation.
	endpoint10 := "10.10.0.10:502"
	unit7 := 7
	tplReg := []connectivity.GatewayModbusRegisterMapping{{
		PointCode: "tpl_temp", SemanticChannel: "SupplyTemperature", FunctionCode: 3,
		Address: 100, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN",
		Scale: 0.1, Offset: 0, PollPriority: "HIGH",
	}}
	skippedRes, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-unbound-drv", DriverType: "MODBUS_TCP",
		Registers: tplReg, Reason: "skip without endpoint",
	})
	if err != nil || !skippedRes.Skipped || skippedRes.Changed {
		t.Fatalf("expected skipped=true changed=false without endpoint/unitId, got %+v err=%v", skippedRes, err)
	}
	res13, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-chiller-1", DriverType: "MODBUS_TCP",
		Endpoint: &endpoint10, UnitID: &unit7, Registers: tplReg,
		UpdatedBy: actorA, Reason: "materialize template driver",
	})
	if err != nil || !res13.Changed || res13.Skipped || res13.Shadow.Desired == nil || res13.Shadow.Desired.DesiredRevision != 13 || len(res13.Shadow.Desired.DriverConfigs) != 1 {
		t.Fatalf("expected rev 13 changed=true with 1 driver, got %+v err=%v", res13, err)
	}
	idempotentRes, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-chiller-1", DriverType: "MODBUS_TCP",
		Endpoint: &endpoint10, UnitID: &unit7, Registers: tplReg,
		Reason: "idempotent repeat",
	})
	if err != nil || idempotentRes.Changed || idempotentRes.Skipped || idempotentRes.Shadow.Desired.DesiredRevision != 13 {
		t.Fatalf("expected idempotent unchanged rev 13, got %+v err=%v", idempotentRes, err)
	}
	tplRegUpdated := append(slices.Clone(tplReg), connectivity.GatewayModbusRegisterMapping{
		PointCode: "tpl_return_temp", SemanticChannel: "ReturnTemperature", FunctionCode: 3,
		Address: 102, RegisterCount: 1, RawType: "INT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN",
		Scale: 0.1, Offset: 0, PollPriority: "HIGH",
	})
	res14, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-chiller-1", DriverType: "MODBUS_TCP",
		Registers: tplRegUpdated, Reason: "inherit endpoint and update registers",
	})
	if err != nil || !res14.Changed || res14.Shadow.Desired.DesiredRevision != 14 || len(res14.Shadow.Desired.DriverConfigs[0].Registers) != 2 || res14.Shadow.Desired.DriverConfigs[0].Endpoint != endpoint10 {
		t.Fatalf("expected rev 14 inheriting endpoint & 2 registers, got %+v err=%v", res14, err)
	}
	if _, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: "0194f000-1000-7000-8000-000000000099", DeviceKey: "tpl-chiller-1", DriverType: "MODBUS_TCP",
		Endpoint: &endpoint10, UnitID: &unit7, Registers: tplReg, Reason: "cross tenant denied",
	}); !errors.Is(err, connectivity.ErrCredentialForbidden) {
		t.Fatalf("expected ErrCredentialForbidden for cross-tenant upsert, got %v", err)
	}
	removedRes, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-chiller-1", Remove: true, Reason: "remove template driver",
	})
	if err != nil || !removedRes.Changed || removedRes.Skipped || removedRes.Shadow.Desired == nil || removedRes.Shadow.Desired.DesiredRevision != 15 || len(removedRes.Shadow.Desired.DriverConfigs) != 0 {
		t.Fatalf("expected rev 15 removing template driver, got %+v err=%v", removedRes, err)
	}
	removedRepeat, err := store.UpsertGatewayTemplateDriverConfig(ctx, gatewayA, connectivity.TemplateDriverUpsertInput{
		TenantID: tenantA, DeviceKey: "tpl-chiller-1", Remove: true, Reason: "idempotent repeat remove",
	})
	if err != nil || removedRepeat.Changed || removedRepeat.Skipped || removedRepeat.Shadow.Desired.DesiredRevision != 15 {
		t.Fatalf("expected idempotent repeat remove at rev 15, got %+v err=%v", removedRepeat, err)
	}

	// Edge rule bundle: PUT desired, Pull, Report SYNCED/DRIFTED, preserve on partial PUT & concentrator upsert, explicit null clear.
	trig := 45.0
	rst := 43.0
	edgeBundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
		SiteID:         siteA,
		BundleRevision: 2,
		BundleDigest:   strings.Repeat("b", 64),
		CompiledAt:     "2026-11-22T00:00:00Z",
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0194f000-4000-7000-8000-000000000001",
			AlarmType:        "CHILLER_HIGH_TEMP",
			Title:            "High Supply Temp",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetCloudAndEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 30,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:      edgecontrol.EdgeConditionHysteresis,
				Input:     "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Direction: edgecontrol.EdgeDirectionAbove,
				Trigger:   &trig,
				Reset:     &rst,
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 43},
			},
		}},
	}
	bundleDigest, err := edgeBundle.ComputeDigest()
	if err != nil {
		t.Fatalf("ComputeDigest failed: %v", err)
	}
	edgeBundle.EdgeBundleDigest = bundleDigest

	rev15 := int64(15)
	wrongSiteBundle := edgeBundle.Clone()
	wrongSiteBundle.SiteID = "0194f000-2000-7000-8000-000000000099"
	wrongDigest, _ := wrongSiteBundle.ComputeDigest()
	wrongSiteBundle.EdgeBundleDigest = wrongDigest
	if _, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev15,
		EdgeRuleBundle:          connectivity.OptionalEdgeRuleBundle{Set: true, Value: wrongSiteBundle},
		Reason:                  "reject cross-site edgeRuleBundle",
	}); !errors.Is(err, connectivity.ErrConfigShadowInvalid) {
		t.Fatalf("expected ErrConfigShadowInvalid for cross-site edgeRuleBundle, got %v", err)
	}

	edgePut, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev15,
		EdgeRuleBundle:          connectivity.OptionalEdgeRuleBundle{Set: true, Value: edgeBundle},
		Reason:                  "set edgeRuleBundle",
	})
	if err != nil || edgePut.Desired == nil || edgePut.Desired.DesiredRevision != 16 || edgePut.Desired.EdgeRuleBundle == nil || edgePut.Desired.EdgeRuleBundle.EdgeBundleDigest != bundleDigest {
		t.Fatalf("rev 16 edgeRuleBundle update = %+v, err=%v", edgePut, err)
	}
	pulledRev16, err := store.PullGatewayDesiredConfig(ctx, gatewayA, 15)
	if err != nil || !pulledRev16.HasDelta || pulledRev16.Desired == nil || pulledRev16.Desired.EdgeRuleBundle == nil || len(pulledRev16.Desired.EdgeRuleBundle.Rules) != 1 {
		t.Fatalf("PullGatewayDesiredConfig rev 16 = %+v, err=%v", pulledRev16, err)
	}
	rev16Partial, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 16,
		AppliedStatus:    connectivity.AppliedStatusApplied,
		PointMappings:    mappings,
		SamplingPolicies: policies,
		EdgeRuleBundle:   edgeBundle.Clone(),
	})
	if err != nil || rev16Partial.Reported == nil || rev16Partial.Reported.AppliedStatus != connectivity.AppliedStatusPartial || rev16Partial.SyncStatus != connectivity.ShadowSyncStatusDrifted {
		t.Fatalf("expected rev 16 edge report downgraded to PARTIAL due to rev-10 concentrator delta, got %+v err=%v", rev16Partial, err)
	}
	if err := store.RecordConcentratorReconcileResult(ctx, tenantA, gatewayA, 16, pulledRev16.Desired.ConcentratorConfigs, nil, nil, nil, false); err != nil {
		t.Fatalf("RecordConcentratorReconcileResult rev 16 failed: %v", err)
	}
	syncedRev16, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || syncedRev16.SyncStatus != connectivity.ShadowSyncStatusSynced || len(syncedRev16.Drift) != 0 || syncedRev16.Reported == nil || syncedRev16.Reported.AppliedStatus != connectivity.AppliedStatusApplied || syncedRev16.Reported.EdgeRuleBundle == nil || syncedRev16.Reported.EdgeRuleBundle.EdgeBundleDigest != bundleDigest {
		t.Fatalf("expected SYNCED with edgeRuleBundle preserved after concentrator reconcile, got %+v err=%v", syncedRev16, err)
	}

	driftedBundle := edgeBundle.Clone()
	driftedBundle.BundleRevision = 1
	driftedDigest, _ := driftedBundle.ComputeDigest()
	driftedBundle.EdgeBundleDigest = driftedDigest
	driftedErr := "edge rule mismatch"
	driftedRev16, err := store.ReportGatewayAppliedConfig(ctx, gatewayA, connectivity.GatewayShadowReportInput{
		ReportedRevision: 16,
		AppliedStatus:    connectivity.AppliedStatusPartial,
		PointMappings:    mappings,
		SamplingPolicies: policies,
		EdgeRuleBundle:   driftedBundle,
		DriftDetails: []connectivity.DriftDetail{{
			Section: connectivity.DriftSectionEdgeRules,
			Target:  "edgeRuleBundle",
			Reason:  connectivity.DriftReasonEdgeApplyFailed,
			Detail:  "bundle reload failed",
		}},
		ErrorMessage: &driftedErr,
	})
	if err != nil || driftedRev16.SyncStatus != connectivity.ShadowSyncStatusDrifted || len(driftedRev16.Drift) != 2 || driftedRev16.Drift[0].Section != connectivity.DriftSectionEdgeRules || driftedRev16.Drift[1].Section != connectivity.DriftSectionEdgeRules {
		t.Fatalf("expected DRIFTED with 2 EDGE_RULES entries for mismatched edgeRuleBundle, got %+v err=%v", driftedRev16, err)
	}

	rev16 := int64(16)
	preservedRev17, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev16,
		Reason:                  "preserve edgeRuleBundle on partial PUT",
	})
	if err != nil || preservedRev17.Desired == nil || preservedRev17.Desired.DesiredRevision != 17 || preservedRev17.Desired.EdgeRuleBundle == nil || preservedRev17.Desired.EdgeRuleBundle.EdgeBundleDigest != bundleDigest {
		t.Fatalf("partial PUT omitting edgeRuleBundle should preserve it, got %+v err=%v", preservedRev17, err)
	}

	paramsRev18 := paramsRename
	paramsRev18.RfTxPower = 24
	concRev18, err := store.UpsertConcentratorShadowState(ctx, tenantA, gatewayA, 7, &paramsRev18, nil, actorA, "preserve edgeRuleBundle on concentrator upsert", false)
	if err != nil || concRev18 != 18 {
		t.Fatalf("UpsertConcentratorShadowState rev=%d err=%v", concRev18, err)
	}
	afterConcEdge, err := store.GetGatewayConfigShadowForOperator(ctx, claimsA, gatewayA)
	if err != nil || afterConcEdge.Desired == nil || afterConcEdge.Desired.EdgeRuleBundle == nil {
		t.Fatalf("expected UpsertConcentratorShadowState to preserve edgeRuleBundle, got %+v err=%v", afterConcEdge, err)
	}

	rev18 := int64(18)
	clearedEdge, err := store.UpdateGatewayDesiredConfig(ctx, claimsA, gatewayA, connectivity.UpdateDesiredConfigInput{
		ExpectedDesiredRevision: &rev18,
		EdgeRuleBundle:          connectivity.OptionalEdgeRuleBundle{Set: true, Value: nil},
		Reason:                  "clear edgeRuleBundle",
	})
	if err != nil || clearedEdge.Desired == nil || clearedEdge.Desired.DesiredRevision != 19 || clearedEdge.Desired.EdgeRuleBundle != nil || len(clearedEdge.Desired.PointMappings) == 0 {
		t.Fatalf("expected edgeRuleBundle cleared to nil while preserving mappings, got %+v err=%v", clearedEdge, err)
	}

	// Query without app.tenant_id returns 0 rows under FORCE ROW LEVEL SECURITY.
	runtimePool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	for _, table := range []string{"gateway_desired_configs", "gateway_reported_configs"} {
		var count int
		if err := runtimePool.QueryRow(ctx, "SELECT count(*) FROM connectivity."+table).Scan(&count); err != nil {
			t.Fatalf("query %s without tenant context: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("RLS leak on %s without app.tenant_id: count=%d", table, count)
		}
	}
}
