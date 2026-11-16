package fdd

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

func evaluateTwinSchedule(twin planttwin.SiteTwin, windowEnd time.Time) (planttwin.ScheduleEvaluation, error) {
	schedEval, err := twin.EvaluateSchedule(windowEnd)
	if err != nil {
		// Sites with no configured timezone or schedule evaluate unblocked as UNCONFIGURED.
		if errors.Is(err, planttwin.ErrInvalidTimezone) && !hasConfiguredScheduleOrTimezone(twin) {
			return planttwin.ScheduleEvaluation{
				OperatingState: planttwin.OperatingStateUnconfigured,
				Reason:         "no operating schedule configured",
			}, nil
		}
		return planttwin.ScheduleEvaluation{}, err
	}
	return schedEval, nil
}

func hasConfiguredScheduleOrTimezone(twin planttwin.SiteTwin) bool {
	return strings.TrimSpace(twin.Timezone) != "" ||
		strings.TrimSpace(twin.Schedule.Timezone) != "" ||
		strings.TrimSpace(twin.Schedule.ScheduleID) != "" ||
		len(twin.Schedule.Slots) > 0 ||
		len(twin.Schedule.Exceptions) > 0
}

func scheduleSuppressionReason(eval planttwin.ScheduleEvaluation) (string, bool) {
	if eval.Suppressed {
		if eval.ActiveException != nil {
			if kind := strings.ToUpper(strings.TrimSpace(eval.ActiveException.Kind)); kind != "" {
				return kind, true
			}
		}
		return planttwin.OperatingStateExceptionClosed, true
	}
	if eval.OperatingState != planttwin.OperatingStateUnconfigured && !eval.WithinOperatingWindow {
		return planttwin.OperatingStateOutsideSchedule, true
	}
	return "", false
}

func suppressedWindowAssessment(rule RuleRevision, schedEval planttwin.ScheduleEvaluation, suppressionCode string) WindowAssessment {
	skipOrNotInRule := func(present bool) string {
		if !present {
			return SkippedNotInRule
		}
		return suppressionCode
	}
	return WindowAssessment{
		Status:            StatusNotOperating,
		Minutes:           rule.WindowMinutes,
		Suppressed:        true,
		ScheduleState:     schedEval.OperatingState,
		SuppressionReason: suppressionCode,
		SuppressionDetail: schedEval.Reason,
		Gates: []GateOutcome{
			{Gate: ChillerRunningGate, SkippedReason: skipOrNotInRule(rule.OperatingGates.ChillerRunning != nil)},
			{Gate: LoadRatioGate, SkippedReason: skipOrNotInRule(rule.OperatingGates.LoadRatio != nil)},
			{Gate: ChilledWaterPumpRunningGate, SkippedReason: skipOrNotInRule(rule.OperatingGates.ChilledWaterPumpRunning != nil)},
			{Gate: OperatingScheduleGate, Applied: true, Bases: []string{suppressionCode}},
		},
	}
}

func resolveTopologyFromTwin(twin planttwin.SiteTwin, request EvaluationRequest) Topology {
	hasSemanticRoles := siteHasSemanticRoles(twin)
	targetAssetID, chillers, pumps := discoverLoopEquipment(twin, request.AssetID)
	return Topology{
		Loop: LoopSignals{
			SupplyTemp:  resolveLoopSignal(twin, targetAssetID, request.AssetID, request.DeviceID, planttwin.RoleSupplyWaterTemp, hasSemanticRoles),
			ReturnTemp:  resolveLoopSignal(twin, targetAssetID, request.AssetID, request.DeviceID, planttwin.RoleReturnWaterTemp, hasSemanticRoles),
			CoolingLoad: resolveLoopSignal(twin, targetAssetID, request.AssetID, request.DeviceID, planttwin.RoleCoolingLoad, hasSemanticRoles),
		},
		Chillers:       gateAssetsFromUnits(twin, chillers, hasSemanticRoles),
		Pumps:          gateAssetsFromUnits(twin, pumps, hasSemanticRoles),
		ResolvedByTwin: true,
	}
}

func topologyFromSite(twin planttwin.SiteTwin) Topology {
	hasSemanticRoles := siteHasSemanticRoles(twin)
	return Topology{
		Chillers:       gateAssetsFromUnits(twin, twin.UnitsByType(planttwin.AssetTypeChiller), hasSemanticRoles),
		Pumps:          gateAssetsFromUnits(twin, twin.UnitsByType(planttwin.AssetTypeChilledWaterPump), hasSemanticRoles),
		ResolvedByTwin: true,
	}
}

func discoverLoopEquipment(twin planttwin.SiteTwin, assetID string) (string, []planttwin.PlantUnit, []planttwin.PlantUnit) {
	if twin.Graph == nil || !twin.Graph.HasExplicitTopology() {
		return assetID, twin.UnitsByType(planttwin.AssetTypeChiller), twin.UnitsByType(planttwin.AssetTypeChilledWaterPump)
	}

	targetAssetID := assetID
	targetUnit, hasTarget := twin.UnitByID(targetAssetID)
	if hasTarget && strings.EqualFold(strings.TrimSpace(targetUnit.AssetType), planttwin.AssetTypeChiller) {
		if loops := twin.Graph.CoolingLoopsForChiller(targetAssetID); len(loops) > 0 {
			targetAssetID = loops[0]
			targetUnit, hasTarget = twin.UnitByID(targetAssetID)
		}
	}

	if (hasTarget && isLoopAssetType(targetUnit.AssetType)) || len(twin.Graph.EquipmentServingLoop(targetAssetID)) > 0 {
		return targetAssetID,
			twin.Graph.EquipmentServingLoop(targetAssetID, planttwin.AssetTypeChiller),
			twin.Graph.EquipmentServingLoop(targetAssetID, planttwin.AssetTypeChilledWaterPump)
	}

	if hasTarget && strings.EqualFold(strings.TrimSpace(targetUnit.AssetType), planttwin.AssetTypeChiller) {
		return targetAssetID,
			[]planttwin.PlantUnit{targetUnit},
			twin.Graph.EquipmentServingUnit(targetAssetID, planttwin.AssetTypeChilledWaterPump)
	}

	chwLoops := twin.UnitsByType(planttwin.AssetTypeChilledWaterLoop)
	if len(chwLoops) == 1 {
		loopID := chwLoops[0].AssetID
		return loopID,
			twin.Graph.EquipmentServingLoop(loopID, planttwin.AssetTypeChiller),
			twin.Graph.EquipmentServingLoop(loopID, planttwin.AssetTypeChilledWaterPump)
	}
	if len(chwLoops) == 0 {
		// Sites with SERVES edges between equipment but no loop asset (e.g., simulator seed) fall back site-wide.
		return targetAssetID, twin.UnitsByType(planttwin.AssetTypeChiller), twin.UnitsByType(planttwin.AssetTypeChilledWaterPump)
	}
	return targetAssetID, nil, nil
}

func isLoopAssetType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterLoop, "CHW_LOOP", "CHILLED_WATER_HEADER", "LOOP", "PLANT_LOOP", "HYDRAULIC_LOOP", "HEADER":
		return true
	default:
		return false
	}
}

func siteHasSemanticRoles(twin planttwin.SiteTwin) bool {
	for _, pb := range twin.PointBindings {
		if strings.TrimSpace(string(pb.SemanticRole)) != "" {
			return true
		}
	}
	return false
}

func resolveLoopSignal(twin planttwin.SiteTwin, targetAssetID, requestAssetID, requestDeviceID string, role planttwin.SemanticRole, hasSemanticRoles bool) []PointRef {
	if !hasSemanticRoles {
		if requestDeviceID == "" {
			return nil
		}
		if key := legacyLoopKeyForRole(role); key != "" {
			return []PointRef{{DeviceID: requestDeviceID, Key: key}}
		}
		return nil
	}

	if pts := twin.ResolveAssetPoints(targetAssetID, role); len(pts) > 0 {
		return pointRefsFromBindings(pts)
	}
	if !strings.EqualFold(strings.TrimSpace(requestAssetID), strings.TrimSpace(targetAssetID)) {
		if pts := twin.ResolveAssetPoints(requestAssetID, role); len(pts) > 0 {
			return pointRefsFromBindings(pts)
		}
	}

	// Songyaxuan and simulator bind loop meters at SITE scope (no asset_id); exclude bindings owned by other assets.
	var onRequestDevice []planttwin.PointBinding
	var siteScoped []planttwin.PointBinding
	for _, pb := range twin.PointsByRole(role) {
		if strings.TrimSpace(pb.AssetID) != "" {
			continue
		}
		siteScoped = append(siteScoped, pb)
		if requestDeviceID != "" && strings.EqualFold(strings.TrimSpace(pb.DeviceID), strings.TrimSpace(requestDeviceID)) {
			onRequestDevice = append(onRequestDevice, pb)
		}
	}
	if len(onRequestDevice) > 0 {
		return pointRefsFromBindings(onRequestDevice)
	}
	return pointRefsFromBindings(siteScoped)
}

func legacyLoopKeyForRole(role planttwin.SemanticRole) string {
	switch role {
	case planttwin.RoleSupplyWaterTemp:
		return supplyTemperatureKey
	case planttwin.RoleReturnWaterTemp:
		return returnTemperatureKey
	case planttwin.RoleCoolingLoad:
		return coolingLoadKey
	default:
		return ""
	}
}

func gateAssetsFromUnits(twin planttwin.SiteTwin, units []planttwin.PlantUnit, hasSemanticRoles bool) []GateAsset {
	if len(units) == 0 {
		return nil
	}
	assets := make([]GateAsset, 0, len(units))
	for _, unit := range units {
		powerRefs := pointRefsFromBindings(twin.ResolveAssetPoints(unit.AssetID, planttwin.RoleActivePower))
		runStateRefs := pointRefsFromBindings(twin.ResolveAssetPoints(unit.AssetID, planttwin.RoleRunState))
		if !hasSemanticRoles && len(powerRefs) == 0 && len(runStateRefs) == 0 {
			for _, dev := range twin.DevicesForAsset(unit.AssetID) {
				powerRefs = append(powerRefs, PointRef{DeviceID: dev.DeviceID, Key: powerTotalKey})
				runStateRefs = append(runStateRefs, PointRef{DeviceID: dev.DeviceID, Key: runStateKey})
			}
		}
		assets = append(assets, GateAsset{
			AssetID:                unit.AssetID,
			DeviceIDs:              uniqueDevices(powerRefs, runStateRefs),
			PowerPoints:            powerRefs,
			RunStatePoints:         runStateRefs,
			RatedPowerKW:           derefFloat(unit.RatedPowerKW),
			RatedCapacityKW:        derefFloat(unit.RatedCapacityKW),
			FeederAuxiliaryPowerKW: unit.FeederAuxiliaryPowerKW,
		})
	}
	return assets
}

func pointRefsFromBindings(bindings []planttwin.PointBinding) []PointRef {
	if len(bindings) == 0 {
		return nil
	}
	seen := make(map[PointRef]bool, len(bindings))
	refs := make([]PointRef, 0, len(bindings))
	for _, pb := range bindings {
		ref := PointRef{DeviceID: strings.TrimSpace(pb.DeviceID), Key: strings.TrimSpace(pb.PointCode)}
		if ref.DeviceID == "" || ref.Key == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

func uniqueDevices(groups ...[]PointRef) []string {
	seen := map[string]bool{}
	var devices []string
	for _, group := range groups {
		for _, ref := range group {
			if ref.DeviceID != "" && !seen[ref.DeviceID] {
				seen[ref.DeviceID] = true
				devices = append(devices, ref.DeviceID)
			}
		}
	}
	sort.Strings(devices)
	return devices
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
