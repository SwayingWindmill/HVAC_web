package connectivity_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
		for _, table := range []string{"gateway_reported_configs", "gateway_desired_configs", "gateway_concentrator_audit", "gateway_credential_audit", "gateway_enrollments", "gateway_credentials"} {
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
