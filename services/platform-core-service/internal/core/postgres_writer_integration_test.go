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
