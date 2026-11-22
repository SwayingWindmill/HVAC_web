package connectivity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

func TestOTAPostgresRLSTagInheritanceAndCanaryLifecycle(t *testing.T) {
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

	const (
		tenantA  = "0194f010-1000-7000-8000-000000000001"
		tenantB  = "0194f010-1000-7000-8000-000000000002"
		siteA1   = "0194f010-2000-7000-8000-000000000001"
		siteA2   = "0194f010-2000-7000-8000-000000000002"
		siteB1   = "0194f010-2000-7000-8000-000000000003"
		gw1      = "0194f010-3000-7000-8000-000000000001"
		gw2      = "0194f010-3000-7000-8000-000000000002"
		gw3      = "0194f010-3000-7000-8000-000000000003"
		asset1   = "0194f010-4000-7000-8000-000000000001"
		binding1 = "0194f010-5000-7000-8000-000000000001"
		actorA   = "0194f010-9000-7000-8000-000000000001"
		actorB   = "0194f010-9000-7000-8000-000000000002"
	)

	cleanup := func() {
		_, _ = admin.Exec(ctx, "DELETE FROM connectivity.ota_device_tasks WHERE tenant_id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
		_, _ = admin.Exec(ctx, "DELETE FROM connectivity.ota_campaigns WHERE tenant_id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
		_, _ = admin.Exec(ctx, "DELETE FROM connectivity.ota_packages WHERE tenant_id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.device_bindings WHERE id = $1::uuid", binding1)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.assets WHERE id = $1::uuid", asset1)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.devices WHERE id IN ($1::uuid, $2::uuid, $3::uuid)", gw1, gw2, gw3)
		_, _ = admin.Exec(ctx, "DELETE FROM core_registry.sites WHERE id IN ($1::uuid, $2::uuid, $3::uuid)", siteA1, siteA2, siteB1)
		_, _ = admin.Exec(ctx, "DELETE FROM iam.tenants WHERE id IN ($1::uuid, $2::uuid)", tenantA, tenantB)
	}
	cleanup()
	t.Cleanup(cleanup)

	seedTime := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'ota-test-a','OTA Test A','UTC','USD','US','ACTIVE',1,$2,$2)`, []any{tenantA, seedTime}},
		{`INSERT INTO iam.tenants(id,code,display_name,timezone,currency,country,status,revision,created_at,updated_at) VALUES($1,'ota-test-b','OTA Test B','UTC','USD','US','ACTIVE',1,$2,$2)`, []any{tenantB, seedTime}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'ota-site-a1','OTA Site A1','UTC','ACTIVE',1,$3,$3)`, []any{tenantA, siteA1, seedTime}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'ota-site-a2','OTA Site A2','UTC','ACTIVE',1,$3,$3)`, []any{tenantA, siteA2, seedTime}},
		{`INSERT INTO core_registry.sites(id,tenant_id,code,display_name,timezone,status,revision,created_at,updated_at) VALUES($2,$1,'ota-site-b1','OTA Site B1','UTC','ACTIVE',1,$3,$3)`, []any{tenantB, siteB1, seedTime}},
		{`INSERT INTO core_registry.assets(id,tenant_id,site_id,code,display_name,asset_type,status,tags,revision,created_at,updated_at) VALUES($3,$1,$2,'ota-plant-1','Chiller Plant 1','CHILLER_PLANT','ACTIVE','{"plant":"plant-1","zone":"asset-override-me"}'::jsonb,1,$4,$4)`, []any{tenantA, siteA1, asset1, seedTime}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,tags,revision,created_at,updated_at) VALUES($3,$1,$2,'ota-gw-1','OTA GW 1','GATEWAY','ACTIVE','{"zone":"north","tier":"canary"}'::jsonb,1,$4,$4)`, []any{tenantA, siteA1, gw1, seedTime}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,tags,revision,created_at,updated_at) VALUES($3,$1,$2,'ota-gw-2','OTA GW 2','GATEWAY','ACTIVE','{"plant":"plant-1","zone":"north"}'::jsonb,1,$4,$4)`, []any{tenantA, siteA1, gw2, seedTime}},
		{`INSERT INTO core_registry.devices(id,tenant_id,site_id,code,display_name,device_type,status,tags,revision,created_at,updated_at) VALUES($3,$1,$2,'ota-gw-3','OTA GW 3','GATEWAY','ACTIVE','{"plant":"plant-2","zone":"south"}'::jsonb,1,$4,$4)`, []any{tenantA, siteA2, gw3, seedTime}},
		{`INSERT INTO core_registry.device_bindings(id,tenant_id,site_id,device_id,asset_id,binding_role,status,valid_from,valid_to,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'GATEWAY','ACTIVE',$6,NULL,1,$6,$6)`, []any{binding1, tenantA, siteA1, gw1, asset1, seedTime}},
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

	claimsA1 := registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA1}}
	claimsAAll := registryauth.GrantClaims{TenantID: tenantA, PrincipalID: actorA, AllowedSiteIDs: []string{siteA1, siteA2}}
	claimsB := registryauth.GrantClaims{TenantID: tenantB, PrincipalID: actorB, AllowedSiteIDs: []string{siteB1}}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const (
		pkgID1 = "0194f010-6000-7000-8000-000000000001"
		pkgID2 = "0194f010-6000-7000-8000-000000000002"
	)
	sum1 := sha256.Sum256([]byte("ota-v1.6.0-payload"))
	sha1 := hex.EncodeToString(sum1[:])
	sig1, err := edgecontrol.SignOTAPackageEd25519(priv, pkgID1, "1.6.0", edgecontrol.OTAComponentEdgeGateway, "linux", "armv7l", sha1, 1024)
	if err != nil {
		t.Fatal(err)
	}

	pkg1, err := store.CreateOTAPackage(ctx, claimsA1, connectivity.CreateOTAPackageInput{
		PackageID:      pkgID1,
		Component:      edgecontrol.OTAComponentEdgeGateway,
		Version:        "1.6.0",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
		HardwareModels: []string{"EG8200"},
		FileName:       "edge-gateway-1.6.0.bin",
		FileSizeBytes:  1024,
		SHA256:         sha1,
		SigningKeyID:   "release-2026",
		SignatureB64:   sig1,
		StorageRelPath: tenantA + "/" + pkgID1 + ".bin",
		ReleaseNotes:   "Upgrade v1.6.0",
	})
	if err != nil || pkg1.ID != pkgID1 {
		t.Fatalf("CreateOTAPackage = %+v, err=%v", pkg1, err)
	}

	// Duplicate (component, version, target_os, target_arch) rejected with ErrOTAConflict.
	if _, err := store.CreateOTAPackage(ctx, claimsA1, connectivity.CreateOTAPackageInput{
		PackageID:      pkgID2,
		Component:      edgecontrol.OTAComponentEdgeGateway,
		Version:        "1.6.0",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
		FileName:       "edge-gateway-1.6.0-dup.bin",
		FileSizeBytes:  1024,
		SHA256:         sha1,
		SigningKeyID:   "release-2026",
		SignatureB64:   sig1,
		StorageRelPath: tenantA + "/" + pkgID2 + ".bin",
	}); !errors.Is(err, connectivity.ErrOTAConflict) {
		t.Fatalf("duplicate package got %v, want ErrOTAConflict", err)
	}

	// Cross-tenant package isolation.
	pkgsB, err := store.ListOTAPackages(ctx, claimsB, connectivity.ListOTAPackagesFilter{})
	if err != nil || len(pkgsB) != 0 {
		t.Fatalf("cross-tenant package list = %+v, err=%v", pkgsB, err)
	}

	// Unauthorized siteA2 in TargetSiteIDs for claimsA1 must fail with ErrCredentialForbidden.
	if _, _, err := store.CreateOTACampaign(ctx, claimsA1, connectivity.CreateOTACampaignInput{
		Name:                    "Unauthorized Site",
		PackageID:               pkgID1,
		TargetSiteIDs:           []string{siteA2},
		CanaryPercent:           50,
		FailureRateThresholdPct: 50,
		Reason:                  "should fail scope",
	}); !errors.Is(err, connectivity.ErrCredentialForbidden) {
		t.Fatalf("unauthorized site got %v, want ErrCredentialForbidden", err)
	}

	// Tag inheritance: gw1 inherits plant=plant-1 from bound asset1 AND overrides zone=north over asset's zone=asset-override-me.
	autoPromote := true
	camp1, dispatches1, err := store.CreateOTACampaign(ctx, claimsA1, connectivity.CreateOTACampaignInput{
		Name:                    "Canary Rollout 1.6.0",
		PackageID:               pkgID1,
		TargetSiteIDs:           []string{siteA1},
		TargetTags:              map[string]string{"plant": "plant-1", "zone": "north"},
		CanaryPercent:           50,
		FailureRateThresholdPct: 50,
		AutoPromoteRollout:      &autoPromote,
		Reason:                  "siteA1 plant-1 rollout",
	})
	if err != nil {
		t.Fatalf("CreateOTACampaign: %v", err)
	}
	if camp1.TotalCount != 2 || len(camp1.Tasks) != 2 || len(dispatches1) != 1 || dispatches1[0].GatewayID != gw1 {
		t.Fatalf("unexpected camp1 targets/canary split: detail=%+v dispatches=%+v", camp1, dispatches1)
	}
	if camp1.Tasks[0].Wave != connectivity.OTAWaveCanary || camp1.Tasks[0].Stage != connectivity.OTATaskStageDispatched ||
		camp1.Tasks[1].Wave != connectivity.OTAWaveRollout || camp1.Tasks[1].Stage != connectivity.OTATaskStagePending {
		t.Fatalf("unexpected wave split: %+v", camp1.Tasks)
	}

	// Cross-tenant and unauthorized-site reads must fail.
	if _, err := store.GetOTACampaign(ctx, claimsB, camp1.ID); !errors.Is(err, connectivity.ErrNotFound) {
		t.Fatalf("cross-tenant GetOTACampaign got %v, want ErrNotFound", err)
	}

	// Desired stage visible for dispatched canary gw1, nil for pending rollout gw2.
	desiredGW1, err := store.GetGatewayDesiredOTAStage(ctx, gw1)
	if err != nil || desiredGW1 == nil || desiredGW1.TaskID != camp1.Tasks[0].ID {
		t.Fatalf("GetGatewayDesiredOTAStage(gw1) = %+v, err=%v", desiredGW1, err)
	}
	desiredGW2, err := store.GetGatewayDesiredOTAStage(ctx, gw2)
	if err != nil || desiredGW2 != nil {
		t.Fatalf("GetGatewayDesiredOTAStage(gw2) expected nil before promotion, got %+v err=%v", desiredGW2, err)
	}

	// Canary gw1 SUCCEEDED auto-promotes gw2 rollout and returns rollout dispatch.
	nowMs := time.Now().UTC().UnixMilli()
	_, promoted, err := store.ApplyGatewayOTAResult(ctx, gw1, edgecontrol.OTAResultPayload{
		CampaignID:     camp1.ID,
		TaskID:         camp1.Tasks[0].ID,
		PackageID:      pkgID1,
		Stage:          edgecontrol.OTAStageSucceeded,
		ProgressPct:    100,
		CurrentVersion: "1.6.0",
		TargetVersion:  "1.6.0",
		OccurredAt:     nowMs,
	})
	if err != nil || len(promoted) != 1 || promoted[0].GatewayID != gw2 {
		t.Fatalf("canary completion promotion = %+v, err=%v", promoted, err)
	}

	// Rollout gw2 SUCCEEDED completes campaign.
	_, afterDone, err := store.ApplyGatewayOTAResult(ctx, gw2, edgecontrol.OTAResultPayload{
		CampaignID:     camp1.ID,
		TaskID:         camp1.Tasks[1].ID,
		PackageID:      pkgID1,
		Stage:          edgecontrol.OTAStageSucceeded,
		ProgressPct:    100,
		CurrentVersion: "1.6.0",
		TargetVersion:  "1.6.0",
		OccurredAt:     nowMs + 1000,
	})
	if err != nil || len(afterDone) != 0 {
		t.Fatalf("rollout completion = %+v, err=%v", afterDone, err)
	}
	finishedCamp, err := store.GetOTACampaign(ctx, claimsA1, camp1.ID)
	if err != nil || finishedCamp.Status != connectivity.OTACampaignStatusCompleted || finishedCamp.SucceededCount != 2 {
		t.Fatalf("finishedCamp = %+v, err=%v", finishedCamp, err)
	}

	// Second campaign across all sites under claimsAAll: test Pause, Resume, Circuit Breaker, and Rollback.
	sum2 := sha256.Sum256([]byte("ota-v1.7.0-payload"))
	sha2 := hex.EncodeToString(sum2[:])
	sig2, err := edgecontrol.SignOTAPackageEd25519(priv, pkgID2, "1.7.0", edgecontrol.OTAComponentEdgeGateway, "linux", "armv7l", sha2, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOTAPackage(ctx, claimsAAll, connectivity.CreateOTAPackageInput{
		PackageID:      pkgID2,
		Component:      edgecontrol.OTAComponentEdgeGateway,
		Version:        "1.7.0",
		TargetOS:       "linux",
		TargetArch:     "armv7l",
		FileName:       "edge-gateway-1.7.0.bin",
		FileSizeBytes:  2048,
		SHA256:         sha2,
		SigningKeyID:   "release-2026",
		SignatureB64:   sig2,
		StorageRelPath: tenantA + "/" + pkgID2 + ".bin",
	}); err != nil {
		t.Fatal(err)
	}
	camp2, _, err := store.CreateOTACampaign(ctx, claimsAAll, connectivity.CreateOTACampaignInput{
		Name:                    "Canary Circuit Breaker Test",
		PackageID:               pkgID2,
		CanaryPercent:           34, // 2 of 3 gateways in canary
		FailureRateThresholdPct: 40,
		AutoPromoteRollout:      &autoPromote,
		Reason:                  "test pause resume circuit breaker",
	})
	if err != nil || camp2.TotalCount != 3 {
		t.Fatalf("camp2 = %+v, err=%v", camp2, err)
	}

	// Pause & Resume while CANARY_RUNNING.
	pausedManual, err := store.PauseOTACampaign(ctx, claimsAAll, camp2.ID, "maintenance window")
	if err != nil || pausedManual.Status != connectivity.OTACampaignStatusPaused ||
		pausedManual.PauseReason == nil || *pausedManual.PauseReason != connectivity.OTAPauseReasonManual {
		t.Fatalf("PauseOTACampaign = %+v, err=%v", pausedManual, err)
	}
	resumed, resumeDispatches, err := store.ResumeOTACampaign(ctx, claimsAAll, camp2.ID, "window cleared")
	if err != nil || resumed.Status != connectivity.OTACampaignStatusCanaryRunning || len(resumeDispatches) != 2 {
		t.Fatalf("ResumeOTACampaign = %+v dispatches=%d err=%v", resumed, len(resumeDispatches), err)
	}

	// 1 of 2 canary tasks fails -> 50% >= 40% threshold -> circuit breaker pauses campaign.
	_, _, err = store.ApplyGatewayOTAResult(ctx, camp2.Tasks[0].GatewayID, edgecontrol.OTAResultPayload{
		CampaignID:     camp2.ID,
		TaskID:         camp2.Tasks[0].ID,
		PackageID:      pkgID2,
		Stage:          edgecontrol.OTAStageFailed,
		ProgressPct:    60,
		CurrentVersion: "1.6.0",
		TargetVersion:  "1.7.0",
		ErrorCode:      "SIGNATURE_INVALID",
		ErrorMessage:   "signature check failed",
		OccurredAt:     nowMs + 2000,
	})
	if err != nil {
		t.Fatalf("ApplyGatewayOTAResult failure: %v", err)
	}
	tripped, err := store.GetOTACampaign(ctx, claimsAAll, camp2.ID)
	if err != nil || tripped.Status != connectivity.OTACampaignStatusPaused ||
		tripped.PauseReason == nil || *tripped.PauseReason != connectivity.OTAPauseReasonFailureRateExceeded {
		t.Fatalf("expected PAUSED FAILURE_RATE_THRESHOLD_EXCEEDED, got %+v err=%v", tripped, err)
	}

	// Manual Rollback cancels pending rollout task and issues ROLLBACK dispatches to dispatched canary tasks.
	rolledBack, rbDispatches, err := store.RollbackOTACampaign(ctx, claimsAAll, camp2.ID, "abort bad 1.7.0")
	if err != nil || rolledBack.Status != connectivity.OTACampaignStatusRolledBack || rolledBack.CancelledCount != 1 || len(rbDispatches) != 2 {
		t.Fatalf("RollbackOTACampaign = %+v dispatches=%d err=%v", rolledBack, len(rbDispatches), err)
	}
	for _, d := range rbDispatches {
		if d.Stage.Action != edgecontrol.OTAActionRollback {
			t.Fatalf("expected ROLLBACK action in dispatch, got %+v", d.Stage)
		}
	}

	// Query without app.tenant_id returns 0 rows under FORCE ROW LEVEL SECURITY.
	runtimePool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	for _, table := range []string{"ota_packages", "ota_campaigns", "ota_device_tasks"} {
		var count int
		if err := runtimePool.QueryRow(ctx, "SELECT count(*) FROM connectivity."+table).Scan(&count); err != nil {
			t.Fatalf("query %s without tenant context: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("RLS leak on %s without app.tenant_id: count=%d", table, count)
		}
	}
}
