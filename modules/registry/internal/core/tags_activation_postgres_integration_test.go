package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

type captureTelemetryClient struct {
	mu       sync.Mutex
	requests []RelayTelemetrySyncRequest
}

func (c *captureTelemetryClient) SyncRegistry(_ context.Context, req RelayTelemetrySyncRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return nil
}

func (c *captureTelemetryClient) RetireTelemetry(context.Context, string, string, []string, []RelayTelemetryRetirePt) error {
	return nil
}

type captureConnectivityClient struct{}

func (c *captureConnectivityClient) RetireGateway(context.Context, string, string) error {
	return nil
}

type captureCommandClient struct{}

func (c *captureCommandClient) CancelRetiredTarget(context.Context, RelayCommandCancelRequest) error {
	return nil
}

type captureCommandCapabilityClient struct {
	mu       sync.Mutex
	profiles [][]TemplateCapabilityProfile
}

func (c *captureCommandCapabilityClient) SyncCapabilityProfiles(_ context.Context, _ string, _ time.Time, profiles []TemplateCapabilityProfile) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := append([]TemplateCapabilityProfile(nil), profiles...)
	c.profiles = append(c.profiles, copied)
	return nil
}

func TestPostgresTagsSearchTemplateReadAndActivationFlow(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	suffix := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	runTag := "run-" + suffix

	spaceClaims := writerIntegrationClaims(registryauth.ActionSpaceWrite, testSiteA1)
	assetClaims := writerIntegrationClaims(registryauth.ActionAssetWrite, testSiteA1, testSiteA2)
	deviceClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, testSiteA1, testSiteA2)
	bindingClaims := writerIntegrationClaims(registryauth.ActionBindingWrite, testSiteA1, testSiteA2)
	tplClaims := writerIntegrationClaims(registryauth.ActionTemplateManage, testSiteA1, testSiteA2)
	pointClaims := writerIntegrationClaims(registryauth.ActionPointWrite, testSiteA1)

	rootSpace, _, err := store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		SiteID: testSiteA1, Code: "p1-root-" + suffix, DisplayName: "Root Building", SpaceType: "BUILDING", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "p1-space-root-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveSpace(root): %v", err)
	}
	floorSpace, _, err := store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		SiteID: testSiteA1, ParentSpaceID: &rootSpace.ID, Code: "p1-flr-" + suffix, DisplayName: "Basement Floor", SpaceType: "FLOOR", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "p1-space-flr-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveSpace(floor): %v", err)
	}
	plantRoom, _, err := store.SaveSpace(ctx, spaceClaims, SpaceMutation{
		SiteID: testSiteA1, ParentSpaceID: &floorSpace.ID, Code: "p1-rm-" + suffix, DisplayName: "Chiller Plant Room", SpaceType: "PLANT_ROOM", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "p1-space-rm-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveSpace(room): %v", err)
	}

	assetA1, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		SiteID: testSiteA1, Code: "p1-ch1-" + suffix, DisplayName: "P1 Chiller A1", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{"plant": "north", "stage": "p1", "testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-asset-a1-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveAsset(a1): %v", err)
	}

	// Nil Tags on update must preserve existing tags.
	preservedAsset, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		ID: assetA1.ID, SiteID: testSiteA1, Code: assetA1.Code, DisplayName: "P1 Chiller A1 Preserved", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: nil,
		Meta: MutationMeta{ExpectedRevision: assetA1.Revision, IdempotencyKey: "p1-asset-pres-" + suffix, Reason: "preserve tags"},
	})
	if err != nil || preservedAsset.Tags["plant"] != "north" || preservedAsset.Tags["stage"] != "p1" {
		t.Fatalf("expected tags preserved on nil update, got (%+v, %v)", preservedAsset.Tags, err)
	}

	// Non-nil empty map clears tags.
	clearedAsset, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		ID: assetA1.ID, SiteID: testSiteA1, Code: assetA1.Code, DisplayName: "P1 Chiller A1 Cleared", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{},
		Meta: MutationMeta{ExpectedRevision: preservedAsset.Revision, IdempotencyKey: "p1-asset-clr-" + suffix, Reason: "clear tags"},
	})
	if err != nil || len(clearedAsset.Tags) != 0 {
		t.Fatalf("expected tags cleared on empty map update, got (%+v, %v)", clearedAsset.Tags, err)
	}

	// Nil Tags vs empty map with same idempotencyKey must conflict instead of returning a cached replay.
	if _, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		ID: assetA1.ID, SiteID: testSiteA1, Code: assetA1.Code, DisplayName: "P1 Chiller A1 Cleared", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: nil,
		Meta: MutationMeta{ExpectedRevision: preservedAsset.Revision, IdempotencyKey: "p1-asset-clr-" + suffix, Reason: "clear tags"},
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict reusing clear key with nil tags, got %v", err)
	}

	// Restore tags on Asset A1.
	assetA1, _, err = store.SaveAsset(ctx, assetClaims, AssetMutation{
		ID: assetA1.ID, SiteID: testSiteA1, Code: assetA1.Code, DisplayName: "P1 Chiller A1", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{"plant": "north", "stage": "p1", "testrun": runTag},
		Meta: MutationMeta{ExpectedRevision: clearedAsset.Revision, IdempotencyKey: "p1-asset-rest-" + suffix, Reason: "restore tags"},
	})
	if err != nil {
		t.Fatalf("SaveAsset(restore): %v", err)
	}

	// Bind Asset A1 to 3rd-level plantRoom.
	effectiveAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Rebind(ctx, bindingClaims, RebindRequest{
		SiteID: testSiteA1, Kind: BindingAssetSpace, SourceID: assetA1.ID, TargetID: plantRoom.ID, Role: "INSTALLED_IN", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-bind-as1-" + suffix, Reason: "bind asset to leaf room"},
	}); err != nil {
		t.Fatalf("Rebind(ASSET_SPACE): %v", err)
	}

	assetA2, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		SiteID: testSiteA2, Code: "p1-ch2-" + suffix, DisplayName: "P1 Chiller A2", AssetType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{"plant": "north", "testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-asset-a2-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveAsset(a2): %v", err)
	}

	chillerDev, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: testSiteA1, Code: "01-ch-" + suffix, DisplayName: "P1 Chiller Ctrl 1", DeviceType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{"vendor": "trane", "testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-dev-ch1-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveDevice(chiller): %v", err)
	}
	if _, err := store.Rebind(ctx, bindingClaims, RebindRequest{
		SiteID: testSiteA1, Kind: BindingDeviceAsset, SourceID: chillerDev.ID, TargetID: assetA1.ID, Role: "CONTROLLER", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-bind-da1-" + suffix, Reason: "bind device to asset"},
	}); err != nil {
		t.Fatalf("Rebind(DEVICE_ASSET): %v", err)
	}

	vfdDev, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: testSiteA1, Code: "p1-vfd-" + suffix, DisplayName: "P1 VFD 1", DeviceType: "VFD", Status: "ACTIVE",
		Tags: map[string]string{"vendor": "schneider", "testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-dev-vfd-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveDevice(vfd): %v", err)
	}
	if _, err := store.Rebind(ctx, bindingClaims, RebindRequest{
		SiteID: testSiteA1, Kind: BindingDeviceSpace, SourceID: vfdDev.ID, TargetID: floorSpace.ID, Role: "INSTALLED_IN", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-bind-ds1-" + suffix, Reason: "bind vfd directly to floor"},
	}); err != nil {
		t.Fatalf("Rebind(DEVICE_SPACE): %v", err)
	}

	chillerDevSite2, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: testSiteA2, Code: "p1-ch2-" + suffix, DisplayName: "P1 Chiller Ctrl 2", DeviceType: "CHILLER", Status: "ACTIVE",
		Tags: map[string]string{"vendor": "carrier", "testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-dev-ch2-" + suffix, Reason: "P1 integration test"},
	})
	if err != nil {
		t.Fatalf("SaveDevice(chiller site2): %v", err)
	}

	retiredDev, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: testSiteA1, Code: "p1-ret-" + suffix, DisplayName: "P1 Retired Dev", DeviceType: "CHILLER", Status: "RETIRED",
		Tags: map[string]string{"testrun": runTag},
		Meta: MutationMeta{IdempotencyKey: "p1-dev-ret-" + suffix, Reason: "P1 retired device"},
	})
	if err != nil {
		t.Fatalf("SaveDevice(retired): %v", err)
	}

	// Multi-dimensional & cross-site search checks.
	crossAssetClaims := writerIntegrationClaims(registryauth.ActionAssetList, testSiteA1, testSiteA2)
	crossAssets, err := store.ListAssetsFiltered(ctx, crossAssetClaims, "", AssetFilter{
		Tags: map[string]string{"plant": "north", "testrun": runTag},
	}, PageRequest{Limit: 20})
	if err != nil || len(crossAssets.Items) != 2 {
		t.Fatalf("expected 2 cross-site assets, got len=%d err=%v", len(crossAssets.Items), err)
	}

	deniedClaims := writerIntegrationClaims(registryauth.ActionAssetList, testSiteA1, testSiteA2)
	deniedClaims.DeniedSiteIDs = []string{testSiteA2}
	filteredDenied, err := store.ListAssetsFiltered(ctx, deniedClaims, "", AssetFilter{
		Tags: map[string]string{"plant": "north", "testrun": runTag},
	}, PageRequest{Limit: 20})
	if err != nil || len(filteredDenied.Items) != 1 || filteredDenied.Items[0].ID != assetA1.ID {
		t.Fatalf("expected only siteA1 asset when siteA2 denied, got items=%+v err=%v", filteredDenied.Items, err)
	}
	_ = assetA2

	spaceAssets, err := store.ListAssetsFiltered(ctx, crossAssetClaims, "", AssetFilter{
		SpaceID: rootSpace.ID,
		TagKeys: []string{"stage"},
		Tags:    map[string]string{"testrun": runTag},
	}, PageRequest{Limit: 20})
	if err != nil || len(spaceAssets.Items) != 1 || spaceAssets.Items[0].ID != assetA1.ID {
		t.Fatalf("expected recursive space_subtree + bare tag key match on Asset A1, got items=%+v err=%v", spaceAssets.Items, err)
	}

	crossDevClaims := writerIntegrationClaims(registryauth.ActionDeviceList, testSiteA1, testSiteA2)
	spaceDevices, err := store.ListDevicesFiltered(ctx, crossDevClaims, testSiteA1, DeviceFilter{
		SpaceID: rootSpace.ID,
		Tags:    map[string]string{"testrun": runTag},
	}, PageRequest{Limit: 20})
	if err != nil || len(spaceDevices.Items) != 2 {
		t.Fatalf("expected 2 devices via direct device_space_bindings OR device->asset_space_bindings, got len=%d err=%v", len(spaceDevices.Items), err)
	}

	crossChillers, err := store.ListDevicesFiltered(ctx, crossDevClaims, "", DeviceFilter{
		Status:     "ACTIVE",
		DeviceType: "CHILLER",
		Tags:       map[string]string{"testrun": runTag},
	}, PageRequest{Limit: 20})
	if err != nil || len(crossChillers.Items) != 2 {
		t.Fatalf("expected 2 cross-site active CHILLER devices, got len=%d err=%v", len(crossChillers.Items), err)
	}
	gotIDs := []string{crossChillers.Items[0].ID, crossChillers.Items[1].ID}
	if !slices.Contains(gotIDs, chillerDev.ID) || !slices.Contains(gotIDs, chillerDevSite2.ID) {
		t.Fatalf("unexpected chiller device IDs: %v", gotIDs)
	}

	// Template Release + Business Activation checks.
	tplKey := "p1.chiller.tpl." + suffix
	capName := "template.p1_" + suffix + ".chw_sp"
	minTemp := -2.0
	maxTemp := 35.0
	rev, _, err := store.ReleaseTemplate(ctx, tplClaims, ReleaseTemplateRequest{
		TemplateKey:  tplKey,
		TemplateKind: TemplateDevice,
		Payload: map[string]any{
			"deviceType": "CHILLER",
			"standardPoints": []any{
				map[string]any{
					"pointCode":         "chw_supply_temp",
					"displayName":       "冷冻水供水温度",
					"pointType":         "TELEMETRY",
					"valueType":         "NUMBER",
					"unit":              "degC",
					"sampleIntervalMs":  5000,
					"publishIntervalMs": 10000,
					"staleAfterMs":      60000,
					"minimumNumber":     minTemp,
					"maximumNumber":     maxTemp,
				},
				map[string]any{
					"pointCode":           "run_hours",
					"displayName":         "运行小时",
					"pointType":           "COUNTER",
					"valueType":           "NUMBER",
					"unit":                "h",
					"sampleIntervalMs":    10000,
					"publishIntervalMs":   10000,
					"staleAfterMs":        60000,
					"counterDecreaseMode": "RESET_TO_ZERO",
				},
			},
			"qualityChecks": map[string]any{
				"chw_supply_temp": map[string]any{
					"maxDeltaPerMinute":     4.5,
					"unchangedAlertSeconds": 1200,
				},
			},
			"capabilityProfiles": []any{
				map[string]any{
					"capabilityName":     capName,
					"capabilityRevision": "v1",
					"status":             "VERIFIED",
					"canonicalUnit":      "degC",
					"minimumValue":       5.0,
					"maximumValue":       15.0,
					"maximumDelta":       2.0,
					"riskLevel":          "MEDIUM",
					"approvalPolicy":     "NONE",
					"retryPolicy":        "PRE_SEND_ONLY",
					"connectorKind":      "MODBUS_TCP",
				},
			},
		},
		ReleaseReferences: map[string]string{"spec": "P1-3"},
		Meta:              MutationMeta{IdempotencyKey: "p1-tpl-rel-" + suffix, Reason: "release chiller template"},
	})
	if err != nil {
		t.Fatalf("ReleaseTemplate: %v", err)
	}

	// Verify template read APIs work under s1_core_runtime RLS.
	readDevClaims := writerIntegrationClaims(registryauth.ActionDeviceList, testSiteA1)
	tplList, err := store.ListTemplates(ctx, readDevClaims, "DEVICE", PageRequest{Limit: 100})
	if err != nil {
		t.Fatalf("ListTemplates under runtime role failed: %v", err)
	}
	foundTpl := false
	for _, item := range tplList.Items {
		if item.TemplateKey == tplKey {
			foundTpl = true
			break
		}
	}
	if !foundTpl {
		t.Fatalf("released template %q not visible via ListTemplates", tplKey)
	}
	gotRev, err := store.GetTemplateRevision(ctx, readDevClaims, rev.ID)
	if err != nil || gotRev.ID != rev.ID {
		t.Fatalf("GetTemplateRevision failed: (%+v, %v)", gotRev, err)
	}

	// Negative AssignTemplate cases: wrong targetType, wrong deviceType, retired target.
	if _, _, err := store.AssignTemplate(ctx, tplClaims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateAsset, TargetID: assetA1.ID, TemplateRevisionID: rev.ID, EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-assign-badkind-" + suffix, Reason: "kind mismatch"},
	}); err == nil {
		t.Fatal("expected AssignTemplate to reject assigning DEVICE template to ASSET")
	}
	if _, _, err := store.AssignTemplate(ctx, tplClaims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateDevice, TargetID: vfdDev.ID, TemplateRevisionID: rev.ID, EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-assign-badtype-" + suffix, Reason: "deviceType mismatch"},
	}); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("expected ErrBindingConflict assigning CHILLER template to VFD, got %v", err)
	}
	if _, _, err := store.AssignTemplate(ctx, tplClaims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateDevice, TargetID: retiredDev.ID, TemplateRevisionID: rev.ID, EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-assign-ret-" + suffix, Reason: "retired device"},
	}); err == nil {
		t.Fatal("expected AssignTemplate to reject RETIRED target device")
	}

	// Valid AssignTemplate materializes standardPoints & qualityChecks into telemetry_points.
	assignment, _, err := store.AssignTemplate(ctx, tplClaims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateDevice, TargetID: chillerDev.ID, TemplateRevisionID: rev.ID, EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-assign-ok-" + suffix, Reason: "assign chiller template"},
	})
	if err != nil {
		t.Fatalf("AssignTemplate(chiller): %v", err)
	}

	// Filter devices by templateKey & templateRevisionId.
	byTpl, err := store.ListDevicesFiltered(ctx, crossDevClaims, "", DeviceFilter{
		TemplateKey:        tplKey,
		TemplateRevisionID: rev.ID,
	}, PageRequest{Limit: 20})
	if err != nil || len(byTpl.Items) != 1 || byTpl.Items[0].ID != chillerDev.ID {
		t.Fatalf("expected 1 device matching template filter, got (%+v, %v)", byTpl.Items, err)
	}

	assignments, err := store.ListSiteTemplateAssignments(ctx, readDevClaims, testSiteA1, "DEVICE", chillerDev.ID, PageRequest{Limit: 20})
	if err != nil || len(assignments.Items) != 1 || assignments.Items[0].ID != assignment.ID {
		t.Fatalf("ListSiteTemplateAssignments failed: (%+v, %v)", assignments.Items, err)
	}

	// Conflict check: existing point with incompatible valueType blocks template materialization atomically.
	conflictDev, _, err := store.SaveDevice(ctx, deviceClaims, DeviceMutation{
		SiteID: testSiteA1, Code: "p1-conf-" + suffix, DisplayName: "Conflict Dev", DeviceType: "CHILLER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "p1-dev-conf-" + suffix, Reason: "conflict test"},
	})
	if err != nil {
		t.Fatalf("SaveDevice(conflict): %v", err)
	}
	if _, _, err := store.SavePoint(ctx, pointClaims, PointMutation{
		SiteID: testSiteA1, ReportingDeviceID: conflictDev.ID, PointCode: "chw_supply_temp", SourceKey: fmt.Sprintf("conf.%s.chw", suffix),
		DisplayName: "Manual Boolean Point", PointType: "TELEMETRY", ValueType: "BOOLEAN",
		SampleIntervalMS: 1000, PublishIntervalMS: 1000, StaleAfterMS: 5000,
		SourceMetadata: map[string]any{"source": "manual"}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "p1-pt-conf-" + suffix, Reason: "conflicting type"},
	}); err != nil {
		t.Fatalf("SavePoint(conflict): %v", err)
	}
	if _, _, err := store.AssignTemplate(ctx, tplClaims, AssignTemplateRequest{
		SiteID: testSiteA1, TargetType: TemplateDevice, TargetID: conflictDev.ID, TemplateRevisionID: rev.ID, EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "p1-assign-conf-" + suffix, Reason: "type conflict"},
	}); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("expected ErrBindingConflict when existing point valueType differs, got %v", err)
	}

	// OutboxRelay end-to-end delivery for template revision + template assignment.
	telemetryStub := &captureTelemetryClient{}
	capabilityStub := &captureCommandCapabilityClient{}
	relay, err := NewOutboxRelay(store, OutboxRelayConfig{
		TelemetryClient:         telemetryStub,
		ConnectivityClient:      &captureConnectivityClient{},
		CommandClient:           &captureCommandClient{},
		CommandCapabilityClient: capabilityStub,
		LeaseOwner:              "p1-test-relay",
		BatchSize:               100,
	})
	if err != nil {
		t.Fatalf("NewOutboxRelay: %v", err)
	}
	for range 10 {
		processed, err := relay.ProcessOnce(ctx)
		if err != nil {
			t.Fatalf("OutboxRelay.ProcessOnce: %v", err)
		}
		if processed == 0 {
			break
		}
	}

	capabilityStub.mu.Lock()
	var syncedCap bool
	for _, batch := range capabilityStub.profiles {
		for _, p := range batch {
			if p.CapabilityName == capName && p.Status == "VERIFIED" {
				syncedCap = true
			}
		}
	}
	capabilityStub.mu.Unlock()
	if !syncedCap {
		t.Fatalf("expected OutboxRelay to sync template capability profile %q", capName)
	}

	telemetryStub.mu.Lock()
	var syncedPoint *RelayTelemetryPoint
	for _, req := range telemetryStub.requests {
		for _, dev := range req.Devices {
			if dev.DeviceID == chillerDev.ID {
				for i := range dev.Points {
					if dev.Points[i].TelemetryKey == "chw_supply_temp" {
						ptCopy := dev.Points[i]
						syncedPoint = &ptCopy
					}
				}
			}
		}
	}
	telemetryStub.mu.Unlock()
	if syncedPoint == nil || syncedPoint.MinimumNumber == nil || *syncedPoint.MinimumNumber != -2.0 ||
		syncedPoint.MaximumNumber == nil || *syncedPoint.MaximumNumber != 35.0 ||
		syncedPoint.QualityChecks == nil || syncedPoint.QualityChecks["maxStep"] != 4.5 {
		t.Fatalf("expected OutboxRelay telemetry sync to forward min/max/qualityChecks for chw_supply_temp, got %+v", syncedPoint)
	}
}
