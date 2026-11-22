package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresRegistryWriterCASAndIdempotency(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionAssetWrite, testSiteA1)
	create := AssetMutation{
		SiteID: testSiteA1, Code: "s05-cas-asset", DisplayName: "S05 CAS Asset", AssetType: "AHU", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "s05-cas-create-01", Reason: "verify Registry writer create idempotency"},
	}
	created, replayed, err := store.SaveAsset(ctx, claims, create)
	if err != nil || replayed || created.Revision != 1 {
		t.Fatalf("create Asset result=%#v replayed=%v err=%v", created, replayed, err)
	}
	second, replayed, err := store.SaveAsset(ctx, claims, create)
	if err != nil || !replayed || second.ID != created.ID || second.Revision != created.Revision {
		t.Fatalf("idempotent replay result=%#v replayed=%v err=%v", second, replayed, err)
	}
	conflicting := create
	conflicting.DisplayName = "Changed payload"
	if _, _, err := store.SaveAsset(ctx, claims, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error=%v", err)
	}

	update := AssetMutation{
		ID: created.ID, SiteID: testSiteA1, Code: created.Code, DisplayName: "S05 CAS Asset Updated", AssetType: created.AssetType, Status: created.Status,
		Meta: MutationMeta{ExpectedRevision: 1, IdempotencyKey: "s05-cas-update-01", Reason: "verify Registry writer CAS update"},
	}
	updated, replayed, err := store.SaveAsset(ctx, claims, update)
	if err != nil || replayed || updated.Revision != 2 {
		t.Fatalf("update Asset result=%#v replayed=%v err=%v", updated, replayed, err)
	}
	stale := update
	stale.Meta.IdempotencyKey = "s05-cas-stale-01"
	if _, _, err := store.SaveAsset(ctx, claims, stale); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error=%v", err)
	}
}

func TestPostgresRegistryWriterRejectsSpaceCycleInvalidCardinalityAndCrossSiteBinding(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	spaceClaims := writerIntegrationClaims(registryauth.ActionSpaceWrite, testSiteA1)
	parent, _, err := store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		SiteID: testSiteA1, Code: "s05-space-parent", DisplayName: "S05 Space Parent", SpaceType: "FLOOR", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "s05-space-parent-01", Reason: "create parent Space for cycle verification"},
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		SiteID: testSiteA1, ParentSpaceID: &parent.ID, Code: "s05-space-child", DisplayName: "S05 Space Child", SpaceType: "ZONE", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "s05-space-child-01", Reason: "create child Space for cycle verification"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent.ParentSpaceID = &child.ID
	_, _, err = store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		ID: parent.ID, SiteID: testSiteA1, ParentSpaceID: parent.ParentSpaceID, Code: parent.Code, DisplayName: parent.DisplayName, SpaceType: parent.SpaceType, Status: parent.Status,
		Meta: MutationMeta{ExpectedRevision: parent.Revision, IdempotencyKey: "s05-space-cycle-01", Reason: "prove Space cycle rejection"},
	})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("Space cycle error=%v", err)
	}

	assetClaims := writerIntegrationClaims(registryauth.ActionAssetWrite, testSiteA1)
	secondAsset, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		SiteID: testSiteA1, Code: "s05-binding-asset", DisplayName: "S05 Binding Asset", AssetType: "AHU", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "s05-binding-asset-01", Reason: "create second Asset for cardinality verification"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingClaims := writerIntegrationClaims(registryauth.ActionBindingWrite, testSiteA1)
	err = store.withWriteTransaction(ctx, bindingClaims, registryauth.ActionBindingWrite, func(tx pgx.Tx) error {
		bindingID := "01990000-5100-7000-8000-000000000001"
		_, err := tx.Exec(ctx, `
INSERT INTO core_registry.device_bindings (
 id, tenant_id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'CONTROLLER','ACTIVE',$6,NULL,1,$6,$6)
`, bindingID, testTenantA, testSiteA1, testDeviceA1, secondAsset.ID, time.Now().UTC())
		return mapRegistryWriteError(err)
	})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("single-role binding cardinality error=%v", err)
	}

	crossSite := RebindRequest{
		SiteID: testSiteA1, Kind: BindingDeviceAsset, SourceID: testDeviceA1, TargetID: "018f1e00-3000-7000-8000-000000000002", Role: "METER",
		EffectiveAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		Meta:        MutationMeta{IdempotencyKey: "s05-cross-site-01", Reason: "prove cross-Site binding rejection"},
	}
	if _, err := store.Rebind(ctx, bindingClaims, crossSite); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("cross-Site binding error=%v", err)
	}

	pointClaims := writerIntegrationClaims(registryauth.ActionPointWrite, testSiteA1)
	point, _, err := store.SavePoint(ctx, pointClaims, PointMutation{
		SiteID: testSiteA1, ReportingDeviceID: testDeviceA1, PointCode: "s05_binding_point", SourceKey: "s05.binding.point",
		DisplayName: "S05 Binding Point", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "kW", Writable: false,
		SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 5000, SourceMetadata: map[string]any{}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "s05-binding-point-01", Reason: "create Point for canonical Point-to-Asset binding verification"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pointBinding, err := store.Rebind(ctx, bindingClaims, RebindRequest{
		SiteID: testSiteA1, Kind: BindingPointSubject, SourceID: point.ID, TargetID: testAssetA1, TargetType: "ASSET", Role: "DESCRIBES",
		EffectiveAt: time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano),
		Meta:        MutationMeta{IdempotencyKey: "s05-point-subject-01", Reason: "verify canonical Point subject binding columns"},
	})
	if err != nil || pointBinding.TargetID != testAssetA1 {
		t.Fatalf("Point subject binding result=%#v err=%v", pointBinding, err)
	}
}

func TestPostgresRegistryTemplateRevisionIsImmutableAndRollbackUsesNewAssignment(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionTemplateManage)
	v1, _, err := store.ReleaseTemplate(ctx, claims, ReleaseTemplateRequest{
		TemplateKey: "s05.ahu.control", TemplateKind: TemplateAsset,
		Payload: map[string]any{"setpoint": 12.0}, ReleaseReferences: map[string]string{"productRelease": "hvac-web@2026.08.19", "schema": "asset-template@1"},
		Meta: MutationMeta{IdempotencyKey: "s05-template-v1-01", Reason: "release first immutable template revision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, _, err := store.ReleaseTemplate(ctx, claims, ReleaseTemplateRequest{
		TemplateKey: "s05.ahu.control", TemplateKind: TemplateAsset,
		Payload: map[string]any{"setpoint": 11.0}, ReleaseReferences: map[string]string{"productRelease": "hvac-web@2026.08.19", "schema": "asset-template@2"},
		Meta: MutationMeta{IdempotencyKey: "s05-template-v2-01", Reason: "release second immutable template revision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v2.RevisionNumber != v1.RevisionNumber+1 {
		t.Fatalf("Template revisions are not monotonic: v1=%d v2=%d", v1.RevisionNumber, v2.RevisionNumber)
	}

	err = store.withWriteTransaction(ctx, claims, registryauth.ActionTemplateManage, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE core_registry.registry_template_revisions SET payload='{"setpoint":99}'::jsonb WHERE tenant_id=$1::uuid AND id=$2::uuid`, testTenantA, v1.ID)
		return mapRegistryWriteError(err)
	})
	if !errors.Is(err, ErrTemplateImmutable) {
		t.Fatalf("released TemplateRevision mutation error=%v", err)
	}

	firstEffective := time.Now().UTC().Add(2 * time.Minute)
	first, _, err := store.AssignTemplate(ctx, claims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateAsset, TargetID: testAssetA1, TemplateRevisionID: v2.ID, EffectiveAt: firstEffective.Format(time.RFC3339Nano),
		Meta: MutationMeta{IdempotencyKey: "s05-template-assign-v2", Reason: "assign newer released template revision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackEffective := firstEffective.Add(time.Minute)
	rollback, _, err := store.AssignTemplate(ctx, claims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateAsset, TargetID: testAssetA1, TemplateRevisionID: v1.ID, EffectiveAt: rollbackEffective.Format(time.RFC3339Nano),
		Meta: MutationMeta{IdempotencyKey: "s05-template-rollback", Reason: "rollback by creating a new assignment to the previous released revision"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rollback.ID == first.ID {
		t.Fatal("template rollback mutated the prior assignment instead of creating a new assignment")
	}

	err = store.withWriteTransaction(ctx, claims, registryauth.ActionTemplateManage, func(tx pgx.Tx) error {
		var activeRevisionID string
		var historicalValidTo *time.Time
		if err := tx.QueryRow(ctx, `SELECT template_revision_id::text FROM core_registry.registry_template_assignments WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND target_type='ASSET' AND target_id=$3::uuid AND status='ACTIVE' AND valid_to IS NULL`, testTenantA, testSiteA1, testAssetA1).Scan(&activeRevisionID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT valid_to FROM core_registry.registry_template_assignments WHERE tenant_id=$1::uuid AND id=$2::uuid`, testTenantA, first.ID).Scan(&historicalValidTo); err != nil {
			return err
		}
		if activeRevisionID != v1.ID || historicalValidTo == nil {
			t.Fatalf("rollback assignment state active=%s historicalValidTo=%v", activeRevisionID, historicalValidTo)
		}
		delta := historicalValidTo.Sub(rollbackEffective)
		if delta < -time.Microsecond || delta > time.Microsecond {
			t.Fatalf("rollback assignment validTo=%v expected=%v", historicalValidTo, rollbackEffective)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRegistryImportDryRunAndCommitUseTheSameResolvedTarget(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionRegistryImport, testSiteA1)
	payload, err := json.Marshal(AssetMutation{Code: "s05-import-asset", DisplayName: "S05 Imported Asset", AssetType: "CHILLER", Status: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PlanImport(ctx, claims, ImportPlanRequest{
		SiteID: testSiteA1, Namespace: "s05-test", Rows: []ImportRow{{RowNumber: 1, ResourceType: ResourceAsset, ExternalID: "asset-import-001", Payload: payload}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rows) != 1 || len(plan.Results) != 1 || plan.Results[0].Status != "READY" || plan.Rows[0].TargetID == "" || plan.Rows[0].TargetID != plan.Results[0].TargetID || plan.Rows[0].ExpectedRevision != 0 {
		t.Fatalf("unexpected import plan: %#v", plan)
	}
	commit, err := store.CommitImport(ctx, claims, ImportCommitRequest{
		Plan: plan,
		Meta: MutationMeta{IdempotencyKey: "s05-import-commit-01", Reason: "commit the exact dry-run Registry import plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Replayed || len(commit.Results) != 1 || commit.Results[0].Status != "COMMITTED" || commit.Results[0].TargetID != plan.Rows[0].TargetID {
		t.Fatalf("unexpected import commit: %#v", commit)
	}

	secondPlan, err := store.PlanImport(ctx, claims, ImportPlanRequest{
		SiteID: testSiteA1, Namespace: "s05-test", Rows: []ImportRow{{RowNumber: 1, ResourceType: ResourceAsset, ExternalID: "asset-import-001", Payload: payload}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondPlan.Rows[0].TargetID != plan.Rows[0].TargetID || secondPlan.Rows[0].ExpectedRevision != 1 || secondPlan.Results[0].Status != "READY" {
		t.Fatalf("External ID did not resolve to committed target: %#v", secondPlan)
	}
}

func TestPostgresRegistryRetirementBlocksCommissionedResourceWithDependencies(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionRegistryRetire, testSiteA1)
	if _, err := store.Retire(ctx, claims, RetireRequest{
		SiteID: testSiteA1, ResourceType: ResourceSite, ResourceID: testSiteA2,
		Meta: MutationMeta{ExpectedRevision: 1, IdempotencyKey: "s05-retire-wrong-site", Reason: "prove Site retirement cannot escape the authorized Site identifier"},
	}); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("cross-Site Site retirement error=%v", err)
	}
	result, err := store.Retire(ctx, claims, RetireRequest{
		SiteID: testSiteA1, ResourceType: ResourceAsset, ResourceID: testAssetA1,
		Meta: MutationMeta{ExpectedRevision: 1, IdempotencyKey: "s05-retire-block-01", Reason: "prove commissioned Asset cannot be hard deleted or retired through dependencies"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "BLOCKED" || result.DependencyCount < 1 {
		t.Fatalf("retirement did not block dependencies: %#v", result)
	}
	readClaims := integrationClaims(registryauth.ActionAssetRead)
	readClaims.AllowedSiteIDs = []string{testSiteA1}
	asset, err := store.GetAsset(ctx, readClaims, testAssetA1)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Status != "ACTIVE" || asset.Revision != 1 {
		t.Fatalf("blocked retirement mutated Asset: %#v", asset)
	}
}

func openRegistryWriterIntegrationStore(t *testing.T) (context.Context, *PostgresStore) {
	t.Helper()
	databaseURL := os.Getenv("S1_CORE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S1_CORE_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return ctx, store
}

func writerIntegrationClaims(action registryauth.Action, siteIDs ...string) registryauth.GrantClaims {
	claims := integrationClaims(action)
	claims.AllowedSiteIDs = append([]string(nil), siteIDs...)
	return claims
}

// A Site's profile is optional and kept with the rest of it; a mutation without it clears it.
func TestPostgresRegistryWriterKeepsTheSiteProfile(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	claims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	area, zone := 32000.0, "HOT_SUMMER_COLD_WINTER"
	create := SiteMutation{
		Code: "s485-profile-site", DisplayName: "S485 Profile Site", Timezone: "Asia/Shanghai", Status: "ACTIVE", FloorAreaM2: &area, ClimateZone: &zone,
		Meta: MutationMeta{IdempotencyKey: "s485-profile-create-01", Reason: "verify the Site profile is kept"},
	}
	created, _, err := store.SaveSite(ctx, claims, create)
	if err != nil || created.FloorAreaM2 == nil || *created.FloorAreaM2 != area || created.ClimateZone == nil || *created.ClimateZone != zone {
		t.Fatalf("create Site = %#v err=%v", created, err)
	}
	invalid := create
	invalid.Meta.IdempotencyKey = "s485-profile-invalid-01"
	unknown := "TROPICAL"
	invalid.ClimateZone = &unknown
	if _, _, err := store.SaveSite(ctx, claims, invalid); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("unknown climate zone err=%v", err)
	}
	clear := SiteMutation{
		ID: created.ID, Code: created.Code, DisplayName: created.DisplayName, Timezone: created.Timezone, Status: created.Status,
		Meta: MutationMeta{ExpectedRevision: created.Revision, IdempotencyKey: "s485-profile-clear-01", Reason: "verify the Site profile can be cleared"},
	}
	cleared, _, err := store.SaveSite(ctx, claims, clear)
	if err != nil || cleared.FloorAreaM2 != nil || cleared.ClimateZone != nil {
		t.Fatalf("clear Site profile = %#v err=%v", cleared, err)
	}
}

func TestPostgresRegistryGatewaySourceKeyBlocksAndUnblocksRetire(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code: "sk-retire-site", DisplayName: "Source Key Retire Site", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "sk-retire-site-create-01", Reason: "create isolated site for source-key retire integration test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deviceClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID)
	gateway, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: site.ID, Code: "sk-gw-01", DisplayName: "Source Key Gateway", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "sk-retire-gw-create-01", Reason: "create active gateway device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: site.ID, Code: "sk-dev-01", DisplayName: "Source Key Chiller", DeviceType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "sk-retire-dev-create-01", Reason: "create active non-gateway device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondDevice, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: site.ID, Code: "sk-dev-02", DisplayName: "Source Key Pump", DeviceType: "PUMP", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "sk-retire-dev-create-02", Reason: "create second non-gateway device"},
	})
	if err != nil {
		t.Fatal(err)
	}

	bindClaims := writerIntegrationClaims(registryauth.ActionBindingWrite, site.ID)
	effectiveAt := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)

	// Reject non-gateway TargetID for BindingGatewaySourceKey.
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID, TargetID: secondDevice.ID,
		Role: "chiller-1", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "sk-bind-nongw-01", Reason: "non-gateway target must fail"},
	}); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("non-gateway target err = %v, want ErrBindingConflict", err)
	}

	bound, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID, TargetID: gateway.ID,
		Role: "chiller-1", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "sk-bind-gw-01", Reason: "bind device source key to gateway"},
	})
	if err != nil {
		t.Fatalf("bind source key: %v", err)
	}
	if bound.Kind != string(BindingGatewaySourceKey) || bound.TargetID != gateway.ID || bound.Role != "chiller-1" {
		t.Fatalf("unexpected binding result: %#v", bound)
	}

	retireClaims := writerIntegrationClaims(registryauth.ActionRegistryRetire, site.ID)
	blockedDev, err := store.Retire(ctx, retireClaims, RetireRequest{
		SiteID: site.ID, ResourceType: ResourceDevice, ResourceID: device.ID,
		Meta: MutationMeta{ExpectedRevision: device.Revision, IdempotencyKey: "sk-retire-dev-blocked-01", Reason: "active source key must block device retirement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blockedDev.Status != "BLOCKED" || blockedDev.DependencyCount < 1 || blockedDev.Revision != 1 {
		t.Fatalf("expected device retire BLOCKED with DependencyCount>=1 and Revision=1, got %#v", blockedDev)
	}

	// Retry BLOCKED -> BLOCKED with a distinct idempotency key while dependency remains.
	blockedDevRetry, err := store.Retire(ctx, retireClaims, RetireRequest{
		SiteID: site.ID, ResourceType: ResourceDevice, ResourceID: device.ID,
		Meta: MutationMeta{ExpectedRevision: device.Revision, IdempotencyKey: "sk-retire-dev-blocked-02", Reason: "retry blocked device retirement while still blocked"},
	})
	if err != nil {
		t.Fatalf("retry blocked retirement: %v", err)
	}
	if blockedDevRetry.SagaID != blockedDev.SagaID || blockedDevRetry.Status != "BLOCKED" || blockedDevRetry.Revision != 2 {
		t.Fatalf("expected same sagaId %s BLOCKED revision=2, got %#v", blockedDev.SagaID, blockedDevRetry)
	}

	blockedGW, err := store.Retire(ctx, retireClaims, RetireRequest{
		SiteID: site.ID, ResourceType: ResourceDevice, ResourceID: gateway.ID,
		Meta: MutationMeta{ExpectedRevision: gateway.Revision, IdempotencyKey: "sk-retire-gw-blocked-01", Reason: "active source key must block gateway retirement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blockedGW.Status != "BLOCKED" || blockedGW.DependencyCount < 1 {
		t.Fatalf("expected gateway retire BLOCKED with DependencyCount>=1, got %#v", blockedGW)
	}

	// Wrong gateway or non-existent sourceKey close must fail with ErrNotFound without retiring the binding.
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID, TargetID: gateway.ID,
		Role: "wrong-key", EffectiveAt: time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CloseOnly: true,
		Meta:      MutationMeta{IdempotencyKey: "sk-unbind-wrong-key-01", Reason: "wrong key must fail with ErrNotFound"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong sourceKey closeOnly err = %v, want ErrNotFound", err)
	}
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID, TargetID: secondDevice.ID,
		Role: "chiller-1", EffectiveAt: time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CloseOnly: true,
		Meta:      MutationMeta{IdempotencyKey: "sk-unbind-wrong-gw-01", Reason: "wrong gateway target must fail with ErrNotFound"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong gateway closeOnly err = %v, want ErrNotFound", err)
	}

	closed, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID,
		Role: "chiller-1", EffectiveAt: time.Date(2026, 11, 16, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CloseOnly: true,
		Meta:      MutationMeta{IdempotencyKey: "sk-unbind-gw-01", Reason: "close source key before retirement"},
	})
	if err != nil {
		t.Fatalf("close-only source key: %v", err)
	}
	if closed.TargetID != "" || closed.BindingID != bound.BindingID || closed.Revision != 2 {
		t.Fatalf("unexpected closeOnly source key result (want BindingID=%s Revision=2 TargetID=''): %#v", bound.BindingID, closed)
	}

	// Closing already-retired sourceKey must return ErrNotFound.
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: device.ID, TargetID: gateway.ID,
		Role: "chiller-1", EffectiveAt: time.Date(2026, 11, 16, 10, 6, 0, 0, time.UTC).Format(time.RFC3339Nano),
		CloseOnly: true,
		Meta:      MutationMeta{IdempotencyKey: "sk-unbind-gw-twice-01", Reason: "second close must report ErrNotFound"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate closeOnly err = %v, want ErrNotFound", err)
	}

	retiredDev, err := store.Retire(ctx, retireClaims, RetireRequest{
		SiteID: site.ID, ResourceType: ResourceDevice, ResourceID: device.ID,
		Meta: MutationMeta{ExpectedRevision: device.Revision, IdempotencyKey: "sk-retire-dev-ok-01", Reason: "retire device after closing source key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if retiredDev.SagaID != blockedDev.SagaID || retiredDev.Status != "COMPLETED" || retiredDev.DependencyCount != 0 || retiredDev.Revision != 3 {
		t.Fatalf("expected same sagaId %s device retire COMPLETED revision=3, got %#v", blockedDev.SagaID, retiredDev)
	}
}

func TestPostgresRegistryTemporalBindingCloseOnlySemantics(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code: "close-temp-site", DisplayName: "Temporal Close Site", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "temp-close-site-01", Reason: "setup temporal binding close test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assetClaims := writerIntegrationClaims(registryauth.ActionAssetWrite, site.ID)
	asset, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		SiteID: site.ID, Code: "ch-close-01", DisplayName: "Chiller Close 01", AssetType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "temp-close-asset-01", Reason: "asset for close test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID)
	dev, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "plc-close-01", DisplayName: "PLC Close 01", DeviceType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "temp-close-dev-01", Reason: "device for close test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindClaims := writerIntegrationClaims(registryauth.ActionBindingWrite, site.ID)
	validFrom := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)

	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: dev.ID, TargetID: asset.ID,
		Role: "CONTROLLER", EffectiveAt: validFrom.Format(time.RFC3339Nano), CloseOnly: true,
		Meta: MutationMeta{IdempotencyKey: "temp-close-missing-01", Reason: "close before bind must return ErrNotFound"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("close before bind err = %v, want ErrNotFound", err)
	}

	bound, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: dev.ID, TargetID: asset.ID,
		Role: "CONTROLLER", EffectiveAt: validFrom.Format(time.RFC3339Nano),
		Meta: MutationMeta{IdempotencyKey: "temp-bind-01", Reason: "create temporal device binding"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: dev.ID, TargetID: asset.ID,
		Role: "CONTROLLER", EffectiveAt: validFrom.Format(time.RFC3339Nano), CloseOnly: true,
		Meta: MutationMeta{IdempotencyKey: "temp-close-conflict-01", Reason: "effectiveAt == valid_from must conflict"},
	}); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("close at valid_from err = %v, want ErrBindingConflict", err)
	}

	closed, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: dev.ID, TargetID: asset.ID,
		Role: "CONTROLLER", EffectiveAt: validFrom.Add(5 * time.Minute).Format(time.RFC3339Nano), CloseOnly: true,
		Meta: MutationMeta{IdempotencyKey: "temp-close-ok-01", Reason: "close temporal device binding"},
	})
	if err != nil {
		t.Fatalf("close temporal binding: %v", err)
	}
	if closed.BindingID != bound.BindingID || closed.Revision != 2 {
		t.Fatalf("expected closed BindingID=%s Revision=2, got %#v", bound.BindingID, closed)
	}
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: dev.ID, TargetID: asset.ID,
		Role: "CONTROLLER", EffectiveAt: validFrom.Add(10 * time.Minute).Format(time.RFC3339Nano), CloseOnly: true,
		Meta: MutationMeta{IdempotencyKey: "temp-close-again-01", Reason: "second close must report ErrNotFound"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second close err = %v, want ErrNotFound", err)
	}
}

func TestPostgresDeviceConnectivityModePersistenceAndPreservation(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code: "direct-mode-site", DisplayName: "Direct Mode Site", Timezone: "Asia/Shanghai", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "direct-mode-site-01", Reason: "setup direct connectivity mode test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID)
	readClaims := writerIntegrationClaims(registryauth.ActionDeviceRead, site.ID)

	proxied, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "meter-default-mode", DisplayName: "Default Meter", DeviceType: "HVAC_POWER_METER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "direct-mode-dev-01", Reason: "verify default GATEWAY_PROXIED mode"},
	})
	if err != nil || proxied.ConnectivityMode != "GATEWAY_PROXIED" {
		t.Fatalf("default ConnectivityMode=%q err=%v", proxied.ConnectivityMode, err)
	}

	direct, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "thermostat-direct", DisplayName: "Direct Thermostat", DeviceType: "THERMOSTAT", Status: "ACTIVE",
		ConnectivityMode: "DIRECT",
		Meta:             MutationMeta{IdempotencyKey: "direct-mode-dev-02", Reason: "verify explicit DIRECT connectivity mode"},
	})
	if err != nil || direct.ConnectivityMode != "DIRECT" {
		t.Fatalf("explicit DIRECT ConnectivityMode=%q err=%v", direct.ConnectivityMode, err)
	}

	loaded, err := store.GetDevice(ctx, readClaims, direct.ID)
	if err != nil || loaded.ConnectivityMode != "DIRECT" {
		t.Fatalf("GetDevice ConnectivityMode=%q err=%v", loaded.ConnectivityMode, err)
	}

	// Updating without ConnectivityMode must preserve existing DIRECT mode.
	preserved, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		ID: direct.ID, SiteID: site.ID, Code: direct.Code, DisplayName: "Direct Thermostat Renamed", DeviceType: direct.DeviceType, Status: direct.Status,
		Meta: MutationMeta{ExpectedRevision: direct.Revision, IdempotencyKey: "direct-mode-dev-03", Reason: "update displayName while preserving DIRECT"},
	})
	if err != nil || preserved.ConnectivityMode != "DIRECT" {
		t.Fatalf("preserved ConnectivityMode=%q err=%v", preserved.ConnectivityMode, err)
	}

	switched, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		ID: direct.ID, SiteID: site.ID, Code: direct.Code, DisplayName: preserved.DisplayName, DeviceType: direct.DeviceType, Status: direct.Status,
		ConnectivityMode: "GATEWAY_PROXIED",
		Meta:             MutationMeta{ExpectedRevision: preserved.Revision, IdempotencyKey: "direct-mode-dev-04", Reason: "switch back to GATEWAY_PROXIED"},
	})
	if err != nil || switched.ConnectivityMode != "GATEWAY_PROXIED" {
		t.Fatalf("switched ConnectivityMode=%q err=%v", switched.ConnectivityMode, err)
	}
}
