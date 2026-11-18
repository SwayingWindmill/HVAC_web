package planttwin

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	testTenantID = "019a0000-0000-7000-8000-000000000001"
	testSiteID   = "019a0000-0000-7000-8000-000000000002"
	chillerAID   = "019a0000-3000-7000-8000-000000000001"
	chillerBID   = "019a0000-3000-7000-8000-000000000002"
	chwp1ID      = "019a0000-3000-7000-8000-000000000003"
	chwp2ID      = "019a0000-3000-7000-8000-000000000004"
	cwp1ID       = "019a0000-3000-7000-8000-000000000005"
	tower1ID     = "019a0000-3000-7000-8000-000000000006"
	chwLoopID    = "019a0000-3000-7000-8000-000000000007"
	spaceZone1ID = "019a0000-2000-7000-8000-000000000001"
)

func floatPtr(v float64) *float64 {
	return &v
}

func TestSiteScopeValidate(t *testing.T) {
	if err := (SiteScope{TenantID: testTenantID, SiteID: testSiteID}).Validate(); err != nil {
		t.Fatalf("valid scope rejected: %v", err)
	}
	if err := (SiteScope{TenantID: "019A0000-0000-7000-8000-000000000001", SiteID: testSiteID}).Validate(); err != nil {
		t.Fatalf("uppercase UUID rejected: %v", err)
	}
	badScopes := []SiteScope{
		{},
		{TenantID: testTenantID},
		{TenantID: "not-a-uuid", SiteID: testSiteID},
		{TenantID: "00000000-0000-0000-0000-000000000000", SiteID: testSiteID},
	}
	for _, sc := range badScopes {
		if err := sc.Validate(); !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("expected ErrInvalidScope for %+v, got %v", sc, err)
		}
	}
}

func TestSiteTwinPointResolutionPriorityAndSubjectLookups(t *testing.T) {
	twin, err := NewSiteTwin(SiteTwin{
		Scope:    SiteScope{TenantID: testTenantID, SiteID: testSiteID},
		Timezone: "Asia/Shanghai",
		Units: []PlantUnit{
			{AssetID: chillerBID, AssetCode: "chiller-b", AssetName: "Chiller B", AssetType: AssetTypeChiller, RatedPowerKW: floatPtr(127.5)},
			{AssetID: chillerAID, AssetCode: "chiller-a", AssetName: "Chiller A", AssetType: AssetTypeChiller, RatedPowerKW: floatPtr(127.5)},
			{AssetID: chwLoopID, AssetCode: "chw-loop", AssetName: "CHW Loop", AssetType: AssetTypeChilledWaterLoop},
		},
		Devices: []TwinDevice{
			{DeviceID: "dev-plc", DeviceCode: "plc-1", DeviceType: "PLC", SourceKey: "plc-1"},
			{DeviceID: "dev-meter", DeviceCode: "meter-1", DeviceType: "METER", SourceKey: "ammeter-1"},
		},
		DeviceBindings: []DeviceBinding{
			{AssetID: chillerAID, DeviceID: "dev-meter", BindingRole: BindingMeters},
			{AssetID: chillerAID, DeviceID: "dev-plc", BindingRole: BindingControls},
			{AssetID: chillerAID, DeviceID: "dev-plc", BindingRole: BindingDescribes},
			{AssetID: chillerBID, DeviceID: "dev-plc", BindingRole: BindingControls},
		},
		PointBindings: []PointBinding{
			// Copied DESCRIBES point should rank below non-copied points even if DeviceCode is earlier.
			{
				PointID: "pt-copied", DeviceID: "dev-a", DeviceCode: "aaa-dev", PointCode: "chw_leaving_temp_copy",
				SourceMetadata: json.RawMessage(`{"copied":true}`),
				SubjectType:    SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingDescribes, SemanticRole: RoleCHWLeavingTemp,
			},
			// Non-copied non-DESCRIBES point ranks after non-copied DESCRIBES.
			{
				PointID: "pt-ctrl", DeviceID: "dev-b", DeviceCode: "bbb-dev", PointCode: "chw_leaving_temp",
				SubjectType: SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingControls, SemanticRole: RoleCHWLeavingTemp,
			},
			// Primary non-copied DESCRIBES point must win.
			{
				PointID: "pt-primary", DeviceID: "dev-c", DeviceCode: "ccc-dev", PointCode: "sensor_temp_01",
				SubjectType: SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingDescribes, SemanticRole: RoleCHWLeavingTemp,
			},
			// Unrelated semantic role with misleading point_code must never match RoleCHWLeavingTemp.
			{
				PointID: "pt-wrong-role", DeviceID: "dev-a", DeviceCode: "aaa-dev", PointCode: "chw_leaving_temp",
				SubjectType: SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingDescribes, SemanticRole: RoleCHWEnteringTemp,
			},
			// Subject bindings for loop, space, and site.
			{
				PointID: "pt-loop-supply", DeviceID: "dev-loop", DeviceCode: "loop-plc", PointCode: "t_sup",
				SubjectType: SubjectTypeAsset, AssetID: chwLoopID, BindingRole: BindingDescribes, SemanticRole: RoleSupplyWaterTemp,
			},
			{
				PointID: "pt-zone-1", DeviceID: "dev-zone", DeviceCode: "z-1", PointCode: "temp",
				SubjectType: SubjectTypeSpace, SpaceID: spaceZone1ID, BindingRole: BindingDescribes, SemanticRole: RoleZoneTemp,
			},
			{
				PointID: "pt-site-load", DeviceID: "dev-btu", DeviceCode: "btu-1", PointCode: "load_kw",
				SubjectType: SubjectTypeSite, BindingRole: BindingDescribes, SemanticRole: RoleCoolingLoad,
			},
			// Command routes: explicit asset binding vs shared device fallback vs site-level emergency.
			{
				PointID: "pt-cmd-a", DeviceID: "dev-plc", DeviceCode: "plc-1", PointCode: "chiller_a_stop",
				PointType: PointTypeCommand, Writable: true,
				SubjectType: SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingControls,
			},
			{
				PointID: "pt-cmd-b", DeviceID: "dev-plc", DeviceCode: "plc-1", PointCode: "chiller_b_stop",
				PointType: PointTypeCommand, Writable: true,
				SubjectType: SubjectTypeAsset, AssetID: chillerBID, BindingRole: BindingControls,
			},
			{
				PointID: "pt-cmd-unbound-plc", DeviceID: "dev-plc", DeviceCode: "plc-1", PointCode: "plc_reset",
				PointType: PointTypeCommand, Writable: true,
			},
			{
				PointID: "pt-cmd-site-emg", DeviceID: "dev-plc", DeviceCode: "plc-1", PointCode: "emergency_release",
				PointType: PointTypeCommand, Writable: true,
				SubjectType: SubjectTypeSite, BindingRole: BindingControls,
			},
			{
				PointID: "pt-readonly-setting", DeviceID: "dev-plc", DeviceCode: "plc-1", PointCode: "readonly_cmd",
				PointType: PointTypeCommand, Writable: false,
				SubjectType: SubjectTypeAsset, AssetID: chillerAID, BindingRole: BindingControls,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSiteTwin error: %v", err)
	}

	if twin.SiteControlMode != ControlModeRemoteAuto {
		t.Fatalf("expected default SiteControlMode %q, got %q", ControlModeRemoteAuto, twin.SiteControlMode)
	}

	unit, ok := twin.UnitByID(chillerAID)
	if !ok || unit.AssetCode != "chiller-a" || unit.LockMode != LockModeUnlocked {
		t.Fatalf("unexpected UnitByID result: %+v ok=%v", unit, ok)
	}
	chillers := twin.UnitsByType(AssetTypeChiller)
	if len(chillers) != 2 || chillers[0].AssetCode != "chiller-a" || chillers[1].AssetCode != "chiller-b" {
		t.Fatalf("unexpected UnitsByType order: %+v", chillers)
	}

	pts := twin.ResolveAssetPoints(chillerAID, RoleCHWLeavingTemp)
	if len(pts) != 3 {
		t.Fatalf("expected 3 CHW_LEAVING_TEMP points, got %+v", pts)
	}
	if pts[0].PointID != "pt-primary" || pts[1].PointID != "pt-ctrl" || pts[2].PointID != "pt-copied" {
		t.Fatalf("unexpected point priority order: [%s, %s, %s]", pts[0].PointID, pts[1].PointID, pts[2].PointID)
	}
	if pts[0].IsCopied || !pts[2].IsCopied {
		t.Fatalf("expected IsCopied flags [false, true], got [%v, %v]", pts[0].IsCopied, pts[2].IsCopied)
	}

	best, found := twin.ResolveAssetPoint(chillerAID, RoleCHWLeavingTemp)
	if !found || best.PointID != "pt-primary" {
		t.Fatalf("expected pt-primary, got %+v found=%v", best, found)
	}

	if sup := twin.ResolveSubjectPoints(SubjectTypeAsset, RoleSupplyWaterTemp); len(sup) != 1 || sup[0].PointID != "pt-loop-supply" {
		t.Fatalf("unexpected asset subject lookup: %+v", sup)
	}
	if zone := twin.ResolveSubjectPoints(SubjectTypeSpace, RoleZoneTemp); len(zone) != 1 || zone[0].SpaceID != spaceZone1ID {
		t.Fatalf("unexpected space subject lookup: %+v", zone)
	}
	if load := twin.PointsByRole(RoleCoolingLoad); len(load) != 1 || load[0].SubjectType != SubjectTypeSite {
		t.Fatalf("unexpected site role lookup: %+v", load)
	}

	cmdA := twin.CommandRoutesForAsset(chillerAID)
	if len(cmdA) != 2 || cmdA[0].PointID != "pt-cmd-a" || cmdA[1].PointID != "pt-cmd-unbound-plc" {
		t.Fatalf("unexpected command routes for chiller A: %+v", cmdA)
	}

	allDevs := twin.DevicesForAsset(chillerAID)
	if len(allDevs) != 2 || allDevs[0].DeviceID != "dev-meter" || allDevs[1].DeviceID != "dev-plc" {
		t.Fatalf("unexpected devices for chiller A: %+v", allDevs)
	}
	meterOnly := twin.DevicesForAsset(chillerAID, BindingMeters)
	if len(meterOnly) != 1 || meterOnly[0].DeviceID != "dev-meter" {
		t.Fatalf("unexpected meter devices for chiller A: %+v", meterOnly)
	}

	// Verify SiteTwin.EvaluateSchedule picks up SiteTwin.Timezone when Schedule.Timezone is unset.
	twin.Schedule = OperatingSchedule{
		Slots: []ScheduleSlot{{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60}},
	}
	ev, err := twin.EvaluateSchedule(time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC))
	if err != nil || !ev.InBaseSlot || ev.OperatingState != OperatingStateScheduledOpen {
		t.Fatalf("unexpected SiteTwin.EvaluateSchedule result: %+v err=%v", ev, err)
	}
}

func TestSiteTwinWaterLoopAndParallelBranches(t *testing.T) {
	twin, err := NewSiteTwin(SiteTwin{
		Scope:    SiteScope{TenantID: testTenantID, SiteID: testSiteID},
		Timezone: "Asia/Shanghai",
		Units: []PlantUnit{
			{AssetID: chillerBID, AssetCode: "chiller-b", AssetType: AssetTypeChiller, WaterLoop: WaterLoopChilledWater, BranchCode: "CH-B"},
			{AssetID: chwp2ID, AssetCode: "chwp-2", AssetType: AssetTypeChilledWaterPump, WaterLoop: WaterLoopChilledWater, BranchCode: "CH-B"},
			{AssetID: chillerAID, AssetCode: "chiller-a", AssetType: AssetTypeChiller, WaterLoop: WaterLoopChilledWater, BranchCode: "CH-A"},
			{AssetID: chwp1ID, AssetCode: "chwp-1", AssetType: AssetTypeChilledWaterPump, WaterLoop: WaterLoopChilledWater, BranchCode: "CH-A"},
			{AssetID: cwp1ID, AssetCode: "cwp-1", AssetType: AssetTypeCoolingWaterPump, WaterLoop: WaterLoopCondenserWater, BranchCode: "CW-A"},
			{AssetID: chwLoopID, AssetCode: "chw-loop", AssetType: AssetTypeChilledWaterLoop, WaterLoop: WaterLoopChilledWater},
		},
	})
	if err != nil {
		t.Fatalf("NewSiteTwin failed: %v", err)
	}

	chwUnits := twin.UnitsByLoop(WaterLoopChilledWater)
	if len(chwUnits) != 5 {
		t.Fatalf("expected 5 CHILLED_WATER units, got %d", len(chwUnits))
	}
	branches := twin.ParallelBranches()
	if len(branches) != 3 {
		t.Fatalf("expected 3 branches, got %d (%+v)", len(branches), branches)
	}
	if branches[0].WaterLoop != WaterLoopChilledWater || branches[0].BranchCode != "CH-A" || len(branches[0].Units) != 2 {
		t.Fatalf("unexpected first branch: %+v", branches[0])
	}
	if branches[2].WaterLoop != WaterLoopCondenserWater || branches[2].BranchCode != "CW-A" || len(branches[2].Units) != 1 {
		t.Fatalf("unexpected third branch: %+v", branches[2])
	}

	if _, err := NewSiteTwin(SiteTwin{
		Scope: SiteScope{TenantID: testTenantID, SiteID: testSiteID},
		Units: []PlantUnit{{AssetID: chillerAID, AssetCode: "chiller-a", AssetType: AssetTypeChiller, WaterLoop: WaterLoop("STEAM")}},
	}); !errors.Is(err, ErrInvalidTopology) {
		t.Fatalf("expected ErrInvalidTopology on invalid WaterLoop, got %v", err)
	}
}
