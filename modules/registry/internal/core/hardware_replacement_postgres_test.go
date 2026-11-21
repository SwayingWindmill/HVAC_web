package core

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestPostgresReplaceDeviceHardwarePreservesLogicalContinuity(t *testing.T) {
	ctx, store := openRegistryWriterIntegrationStore(t)
	siteClaims := writerIntegrationClaims(registryauth.ActionSiteWrite)
	site, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code:        "hw-replace-site",
		DisplayName: "Hardware Replace Site",
		Timezone:    "Asia/Shanghai",
		Status:      "ACTIVE",
		Meta: MutationMeta{
			IdempotencyKey: "hw-site-create-01",
			Reason:         "create isolated site for hardware replacement verification",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherSite, _, err := store.SaveSite(ctx, siteClaims, SiteMutation{
		Code:        "hw-replace-other-site",
		DisplayName: "Hardware Replace Other Site",
		Timezone:    "Asia/Shanghai",
		Status:      "ACTIVE",
		Meta: MutationMeta{
			IdempotencyKey: "hw-site-create-02",
			Reason:         "create second site for cross-site isolation verification",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	devClaims := writerIntegrationClaims(registryauth.ActionDeviceWrite, site.ID, otherSite.ID)
	gwA, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "hw-gw-a", DisplayName: "Gateway A", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-gw-a-create-01", Reason: "create original gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	gwB, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "hw-gw-b", DisplayName: "Gateway B", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-gw-b-create-01", Reason: "create replacement gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherSiteGW, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: otherSite.ID, Code: "hw-gw-other", DisplayName: "Other Site Gateway", DeviceType: "GATEWAY", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-gw-other-create-01", Reason: "create gateway in sibling site"},
	})
	if err != nil {
		t.Fatal(err)
	}
	meter, _, err := store.SaveDevice(ctx, devClaims, DeviceMutation{
		SiteID: site.ID, Code: "hw-meter-01", DisplayName: "Chiller Plant Main Meter", DeviceType: "POWER_METER", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-meter-create-01", Reason: "create logical power meter device"},
	})
	if err != nil {
		t.Fatal(err)
	}

	assetClaims := writerIntegrationClaims(registryauth.ActionAssetWrite, site.ID)
	asset, _, err := store.SaveAsset(ctx, assetClaims, AssetMutation{
		SiteID: site.ID, Code: "hw-chiller-plant", DisplayName: "Chiller Plant", AssetType: "CHILLER_PLANT", Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-asset-create-01", Reason: "create asset linked to logical meter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindClaims := writerIntegrationClaims(registryauth.ActionBindingWrite, site.ID)
	effectiveAt := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingDeviceAsset, SourceID: meter.ID, TargetID: asset.ID, Role: "METER", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "hw-bind-asset-01", Reason: "bind logical meter to asset"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rebind(ctx, bindClaims, RebindRequest{
		SiteID: site.ID, Kind: BindingGatewaySourceKey, SourceID: meter.ID, TargetID: gwA.ID, Role: "meter_main_01", EffectiveAt: effectiveAt,
		Meta: MutationMeta{IdempotencyKey: "hw-bind-gw-01", Reason: "bind logical meter behind gateway A"},
	}); err != nil {
		t.Fatal(err)
	}

	pointClaims := writerIntegrationClaims(registryauth.ActionPointWrite, site.ID)
	decreaseMode := "RESET_TO_ZERO"
	point, _, err := store.SavePoint(ctx, pointClaims, PointMutation{
		SiteID: site.ID, ReportingDeviceID: meter.ID, PointCode: "active_energy_kwh", SourceKey: "kwh_total",
		DisplayName: "有功总电量", PointType: "COUNTER", ValueType: "NUMBER", Unit: "kWh", Writable: false,
		SampleIntervalMS: 1000, PublishIntervalMS: 5000, StaleAfterMS: 30000, CounterDecreaseMode: &decreaseMode, SourceMetadata: map[string]any{}, Status: "ACTIVE",
		Meta: MutationMeta{IdempotencyKey: "hw-point-create-01", Reason: "create counter point on logical meter"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Replace meter physical hardware & move to backup Gateway B, preserving meter.ID, asset.ID, and point.ID.
	req := ReplaceDeviceHardwareRequest{
		SiteID:             site.ID,
		DeviceID:           meter.ID,
		NewExternalID:      "METER-SN-2026-B02",
		NewGatewayDeviceID: &gwB.ID,
		NewProtocolAddress: map[string]any{"modbusUnit": 4, "channel": "RS485-2"},
		Meta: MutationMeta{
			ExpectedRevision: meter.Revision,
			IdempotencyKey:   "hw-replace-meter-01",
			Reason:           "电表烧毁更换备件并改接二号备用网关",
		},
	}
	res, err := store.ReplaceDeviceHardware(ctx, devClaims, req)
	if err != nil {
		t.Fatalf("ReplaceDeviceHardware error=%v", err)
	}
	if res.Replayed || res.DeviceID != meter.ID || res.Device.ID != meter.ID || res.Revision != meter.Revision+1 {
		t.Fatalf("unexpected hardware replacement result: %#v", res)
	}
	if res.Device.ExternalID == nil || *res.Device.ExternalID != "METER-SN-2026-B02" {
		t.Fatalf("updated device externalId=%v", res.Device.ExternalID)
	}
	if res.PreviousGatewayDeviceID == nil || *res.PreviousGatewayDeviceID != gwA.ID || res.NewGatewayDeviceID == nil || *res.NewGatewayDeviceID != gwB.ID {
		t.Fatalf("gateway lineage mismatch: prev=%v new=%v", res.PreviousGatewayDeviceID, res.NewGatewayDeviceID)
	}

	// Idempotent replay succeeds cleanly without creating a second history record.
	replayed, err := store.ReplaceDeviceHardware(ctx, devClaims, req)
	if err != nil || !replayed.Replayed || replayed.ReplacementID != res.ReplacementID {
		t.Fatalf("replay failed: res=%#v err=%v", replayed, err)
	}

	// Stale revision conflict -> ErrRevisionConflict.
	staleReq := req
	staleReq.Meta.IdempotencyKey = "hw-replace-meter-stale"
	if _, err := store.ReplaceDeviceHardware(ctx, devClaims, staleReq); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected ErrRevisionConflict, got %v", err)
	}

	// Cross-site gateway target -> ErrBindingConflict; cross-site device -> ErrNotFound.
	crossGW := req
	crossGW.NewGatewayDeviceID = &otherSiteGW.ID
	crossGW.Meta.ExpectedRevision = res.Revision
	crossGW.Meta.IdempotencyKey = "hw-replace-cross-gw"
	if _, err := store.ReplaceDeviceHardware(ctx, devClaims, crossGW); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("expected ErrBindingConflict for cross-site gateway, got %v", err)
	}
	crossDevice := req
	crossDevice.SiteID = otherSite.ID
	crossDevice.Meta.IdempotencyKey = "hw-replace-cross-site-dev"
	if _, err := store.ReplaceDeviceHardware(ctx, devClaims, crossDevice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for wrong site, got %v", err)
	}

	// Cannot bind a GATEWAY device itself under another gateway, but can replace a GATEWAY's own hardware.
	gwBindAttempt := ReplaceDeviceHardwareRequest{
		SiteID: site.ID, DeviceID: gwB.ID, NewExternalID: "EG8200-NEW-1", NewGatewayDeviceID: &gwA.ID,
		Meta: MutationMeta{ExpectedRevision: gwB.Revision, IdempotencyKey: "hw-replace-gw-bind", Reason: "invalid parent gateway on gateway"},
	}
	if _, err := store.ReplaceDeviceHardware(ctx, devClaims, gwBindAttempt); !errors.Is(err, ErrInvalidMutation) {
		t.Fatalf("expected ErrInvalidMutation attaching gateway to gateway, got %v", err)
	}
	gwRes, err := store.ReplaceDeviceHardware(ctx, devClaims, ReplaceDeviceHardwareRequest{
		SiteID: site.ID, DeviceID: gwB.ID, NewExternalID: "EG8200-SN-9902",
		Meta: MutationMeta{ExpectedRevision: gwB.Revision, IdempotencyKey: "hw-replace-gw-self", Reason: "现场更换边缘网关主机"},
	})
	if err != nil || gwRes.DeviceID != gwB.ID || gwRes.NewExternalID != "EG8200-SN-9902" {
		t.Fatalf("gateway self replacement failed: res=%#v err=%v", gwRes, err)
	}

	// Verify DB rows: logical device/asset/point IDs unchanged, source_key moved to gwB, replacement history recorded.
	readClaims := writerIntegrationClaims(registryauth.ActionDeviceRead, site.ID)
	readDevice, err := store.GetDevice(ctx, readClaims, meter.ID)
	if err != nil || readDevice.ID != meter.ID || readDevice.ExternalID == nil || *readDevice.ExternalID != "METER-SN-2026-B02" {
		t.Fatalf("GetDevice continuity check failed: dev=%#v err=%v", readDevice, err)
	}
	modelClaims := writerIntegrationClaims(registryauth.ActionAssetModelRead, site.ID)
	assetModel, err := store.GetSiteAssetModel(ctx, modelClaims, site.ID)
	if err != nil {
		t.Fatalf("GetSiteAssetModel error=%v", err)
	}
	var foundPoint bool
	for _, pt := range assetModel.TelemetryPoints {
		if pt.ID == point.ID && pt.ReportingDeviceID == meter.ID {
			foundPoint = true
		}
	}
	if !foundPoint {
		t.Fatalf("telemetry point %s did not stay bound to logical device %s", point.ID, meter.ID)
	}

	err = store.withWriteTransaction(ctx, devClaims, registryauth.ActionDeviceWrite, func(tx pgx.Tx) error {
		var activeGW, activeKey string
		if err := tx.QueryRow(ctx, `SELECT gateway_device_id::text, source_key FROM core_registry.gateway_device_source_keys WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND device_id=$3::uuid AND status='ACTIVE'`, testTenantA, site.ID, meter.ID).Scan(&activeGW, &activeKey); err != nil {
			return err
		}
		if activeGW != gwB.ID || activeKey != "meter_main_01" {
			t.Fatalf("active source key row=(%s,%s) want=(%s,meter_main_01)", activeGW, activeKey, gwB.ID)
		}
		var historyCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM core_registry.device_hardware_replacements WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND device_id=$3::uuid AND new_external_id='METER-SN-2026-B02'`, testTenantA, site.ID, meter.ID).Scan(&historyCount); err != nil {
			return err
		}
		if historyCount != 1 {
			t.Fatalf("expected 1 device_hardware_replacements row, got %d", historyCount)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
