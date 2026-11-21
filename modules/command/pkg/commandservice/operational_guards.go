package commandservice

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

const (
	chillerStartupBlindWindow   = 6 * time.Minute
	chillerPostStopPumpCooldown = 5 * time.Minute
	chillerMinRunDwell          = 20 * time.Minute
	chillerMinOffDwell          = 15 * time.Minute
	chillerStartWindowDuration  = 60 * time.Minute
	chillerMaxStartsPerHour     = 2
)

func isStartupBlindWindowAuxType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterPump, planttwin.AssetTypeCoolingWaterPump, planttwin.AssetTypeCoolingTower:
		return true
	default:
		return false
	}
}

func isPostStopCooldownPumpType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterPump, planttwin.AssetTypeCoolingWaterPump:
		return true
	default:
		return false
	}
}

func isStartCapability(capability commandmodel.Capability) bool {
	return capability == commandmodel.CapabilityStart || capability == commandmodel.CapabilityStartUnitSequence
}

func isStopCapability(capability commandmodel.Capability) bool {
	return capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence
}

func isNonTerminalCommandStatus(status commandmodel.IntentStatus) bool {
	switch status {
	case commandmodel.IntentSubmitted, commandmodel.IntentValidating, commandmodel.IntentAwaitingApproval,
		commandmodel.IntentApproved, commandmodel.IntentQueued, commandmodel.IntentDispatching,
		commandmodel.IntentOutcomeUnknown:
		return true
	default:
		return false
	}
}

func (a *ControlAuthority) enforcePlantOperationalGuards(
	ctx context.Context,
	operator OperatorIdentity,
	model SiteAssetModelDTO,
	target resolvedCommandPointTarget,
	currentObs *commandmodel.LivePointObservation,
) error {
	assetType := strings.ToUpper(strings.TrimSpace(target.asset.AssetType))
	isAuxStop := isStartupBlindWindowAuxType(assetType) && isStopCapability(target.capability)
	isChillerStart := assetType == planttwin.AssetTypeChiller && isStartCapability(target.capability)
	isChillerStop := assetType == planttwin.AssetTypeChiller && isStopCapability(target.capability)
	if !isAuxStop && !isChillerStart && !isChillerStop {
		return nil
	}

	commands, err := a.store.List(ctx, operator.TenantID, target.asset.SiteID, MaximumListedCommands)
	if err != nil {
		return controlErrorFromStore(err, "Command Service could not verify operational interlock history.")
	}

	now := a.now().UTC()
	topo := resolveSiteControlTopology(model, now)

	if isAuxStop {
		servedChillers := topo.servedChillersByAux[target.asset.ID]
		if err := checkAuxStopStartupBlindWindow(topo, target.asset, servedChillers, commands, now); err != nil {
			return err
		}
		if err := checkChillerPostStopPumpCooldown(topo, target.asset, servedChillers, commands, now); err != nil {
			return err
		}
	}

	if isChillerStart || isChillerStop {
		chillerDevices := appendUniqueShutdownString(append([]string(nil), topo.boundDevicesByAsset[target.asset.ID]...), target.device.ID)
		if isChillerStop {
			if err := checkChillerMinRunCooldown(target.asset, chillerDevices, commands, now); err != nil {
				return err
			}
			if err := a.enforceLastActiveChillerGuard(ctx, operator, topo, target, currentObs, now); err != nil {
				return err
			}
		}
		if isChillerStart {
			if err := checkChillerStartCooldowns(target.asset, chillerDevices, commands, now); err != nil {
				return err
			}
			if err := a.enforceChillerStartHydraulicInterlock(ctx, operator, model, topo, target, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildChillerLabelMap(topo shutdownTopology) map[string]string {
	chillerLabels := make(map[string]string, len(topo.chillers))
	for _, ch := range topo.chillers {
		label := strings.TrimSpace(ch.Code)
		if label == "" {
			label = ch.ID
		}
		chillerLabels[ch.ID] = label
	}
	return chillerLabels
}

func checkAuxStopStartupBlindWindow(
	topo shutdownTopology,
	aux AssetDTO,
	servedChillerIDs []string,
	commands []commandmodel.CommandIntent,
	now time.Time,
) error {
	chillerLabels := buildChillerLabelMap(topo)
	for _, chID := range servedChillerIDs {
		chillerDevices := topo.boundDevicesByAsset[chID]
		if len(chillerDevices) == 0 {
			continue
		}
		chillerLabel := chillerLabels[chID]
		if chillerLabel == "" {
			chillerLabel = chID
		}
		var latestSucceededStart time.Time
		hasRecentSucceededStart := false
		var latestSucceededStop time.Time
		hasSucceededStop := false

		for _, cmd := range commands {
			if !containsString(chillerDevices, cmd.DeviceID) {
				continue
			}
			if isStartCapability(cmd.Capability) {
				if isNonTerminalCommandStatus(cmd.Status) {
					return &ControlError{
						HTTPStatus: http.StatusConflict,
						Code:       "COMMAND_INTERLOCK_BLOCKED",
						Title:      "Command Interlock Blocked",
						Detail:     fmt.Sprintf("Cannot stop auxiliary %s while served chiller %s has an active %s command (%s) in progress.", aux.Code, chillerLabel, cmd.Capability, cmd.Status),
					}
				}
				if cmd.Status == commandmodel.IntentSucceeded {
					elapsed := now.Sub(cmd.UpdatedAt)
					if elapsed >= 0 && elapsed < chillerStartupBlindWindow {
						if !hasRecentSucceededStart || cmd.UpdatedAt.After(latestSucceededStart) {
							latestSucceededStart = cmd.UpdatedAt
							hasRecentSucceededStart = true
						}
					}
				}
			} else if isStopCapability(cmd.Capability) && cmd.Status == commandmodel.IntentSucceeded {
				if !hasSucceededStop || cmd.UpdatedAt.After(latestSucceededStop) {
					latestSucceededStop = cmd.UpdatedAt
					hasSucceededStop = true
				}
			}
		}

		if hasRecentSucceededStart && (!hasSucceededStop || !latestSucceededStop.After(latestSucceededStart)) {
			remaining := (chillerStartupBlindWindow - now.Sub(latestSucceededStart)).Round(time.Second)
			if remaining < time.Second {
				remaining = time.Second
			}
			return &ControlError{
				HTTPStatus: http.StatusConflict,
				Code:       "COMMAND_INTERLOCK_BLOCKED",
				Title:      "Command Interlock Blocked",
				Detail:     fmt.Sprintf("Cannot stop auxiliary %s within the 6-minute startup pulse and meter blind window after chiller %s started (%s remaining).", aux.Code, chillerLabel, remaining),
			}
		}
	}
	return nil
}

func checkChillerPostStopPumpCooldown(
	topo shutdownTopology,
	aux AssetDTO,
	servedChillerIDs []string,
	commands []commandmodel.CommandIntent,
	now time.Time,
) error {
	if !isPostStopCooldownPumpType(aux.AssetType) {
		return nil
	}
	chillerLabels := buildChillerLabelMap(topo)
	for _, chID := range servedChillerIDs {
		chillerDevices := topo.boundDevicesByAsset[chID]
		if len(chillerDevices) == 0 {
			continue
		}
		chillerLabel := chillerLabels[chID]
		if chillerLabel == "" {
			chillerLabel = chID
		}
		var latestSucceededStop time.Time
		hasSucceededStop := false
		var latestSucceededStart time.Time
		hasSucceededStart := false

		for _, cmd := range commands {
			if !containsString(chillerDevices, cmd.DeviceID) {
				continue
			}
			if isStopCapability(cmd.Capability) {
				if isNonTerminalCommandStatus(cmd.Status) {
					return &ControlError{
						HTTPStatus: http.StatusConflict,
						Code:       "CHILLER_POST_STOP_COOLDOWN_ACTIVE",
						Title:      "Chiller Post-Stop Pump Cool-Down Active",
						Detail:     fmt.Sprintf("Cannot stop pump %s while served chiller %s has a %s command (%s) in progress.", aux.Code, chillerLabel, cmd.Capability, cmd.Status),
					}
				}
				if cmd.Status == commandmodel.IntentSucceeded {
					if !hasSucceededStop || cmd.UpdatedAt.After(latestSucceededStop) {
						latestSucceededStop = cmd.UpdatedAt
						hasSucceededStop = true
					}
				}
			} else if isStartCapability(cmd.Capability) && cmd.Status == commandmodel.IntentSucceeded {
				if !hasSucceededStart || cmd.UpdatedAt.After(latestSucceededStart) {
					latestSucceededStart = cmd.UpdatedAt
					hasSucceededStart = true
				}
			}
		}

		if hasSucceededStop && (!hasSucceededStart || !latestSucceededStart.After(latestSucceededStop)) {
			elapsed := now.Sub(latestSucceededStop)
			if elapsed >= 0 && elapsed < chillerPostStopPumpCooldown {
				remaining := (chillerPostStopPumpCooldown - elapsed).Round(time.Second)
				if remaining < time.Second {
					remaining = time.Second
				}
				return &ControlError{
					HTTPStatus: http.StatusConflict,
					Code:       "CHILLER_POST_STOP_COOLDOWN_ACTIVE",
					Title:      "Chiller Post-Stop Pump Cool-Down Active",
					Detail:     fmt.Sprintf("Cannot stop pump %s within the 5-minute post-stop cool-down interlock after chiller %s stopped (%s remaining).", aux.Code, chillerLabel, remaining),
				}
			}
		}
	}
	return nil
}

func checkChillerMinRunCooldown(
	chiller AssetDTO,
	chillerDevices []string,
	commands []commandmodel.CommandIntent,
	now time.Time,
) error {
	var latestStartAt time.Time
	hasStart := false
	var latestStopAt time.Time
	hasStop := false

	for _, cmd := range commands {
		if !containsString(chillerDevices, cmd.DeviceID) || cmd.Status != commandmodel.IntentSucceeded {
			continue
		}
		if isStartCapability(cmd.Capability) {
			if !hasStart || cmd.UpdatedAt.After(latestStartAt) {
				latestStartAt = cmd.UpdatedAt
				hasStart = true
			}
		} else if isStopCapability(cmd.Capability) {
			if !hasStop || cmd.UpdatedAt.After(latestStopAt) {
				latestStopAt = cmd.UpdatedAt
				hasStop = true
			}
		}
	}
	if !hasStart || (hasStop && latestStopAt.After(latestStartAt)) {
		return nil
	}
	elapsed := now.Sub(latestStartAt)
	if elapsed >= 0 && elapsed < chillerMinRunDwell {
		remaining := (chillerMinRunDwell - elapsed).Round(time.Second)
		if remaining < time.Second {
			remaining = time.Second
		}
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_COOLDOWN_ACTIVE",
			Title:      "Chiller Minimum Run Cooldown Active",
			Detail:     fmt.Sprintf("Chiller %s must satisfy the 20-minute minimum compressor run dwell before stopping (%s remaining).", chiller.Code, remaining),
		}
	}
	return nil
}

func checkChillerStartCooldowns(
	chiller AssetDTO,
	chillerDevices []string,
	commands []commandmodel.CommandIntent,
	now time.Time,
) error {
	var latestStopAt time.Time
	hasStop := false
	var latestStartAt time.Time
	hasStart := false
	windowStarts := make([]time.Time, 0, len(commands))

	for _, cmd := range commands {
		if !containsString(chillerDevices, cmd.DeviceID) {
			continue
		}
		if isStopCapability(cmd.Capability) && cmd.Status == commandmodel.IntentSucceeded {
			if !hasStop || cmd.UpdatedAt.After(latestStopAt) {
				latestStopAt = cmd.UpdatedAt
				hasStop = true
			}
		}
		if isStartCapability(cmd.Capability) {
			if cmd.Status == commandmodel.IntentSucceeded {
				if !hasStart || cmd.UpdatedAt.After(latestStartAt) {
					latestStartAt = cmd.UpdatedAt
					hasStart = true
				}
			}
			if cmd.Status != commandmodel.IntentFailed && cmd.Status != commandmodel.IntentCancelled &&
				cmd.Status != commandmodel.IntentRejected && cmd.Status != commandmodel.IntentExpired {
				refTime := cmd.UpdatedAt
				if refTime.IsZero() {
					refTime = cmd.CreatedAt
				}
				elapsed := now.Sub(refTime)
				if elapsed >= 0 && elapsed < chillerStartWindowDuration {
					windowStarts = append(windowStarts, refTime)
				}
			}
		}
	}

	if hasStop && (!hasStart || !latestStartAt.After(latestStopAt)) {
		elapsed := now.Sub(latestStopAt)
		if elapsed >= 0 && elapsed < chillerMinOffDwell {
			remaining := (chillerMinOffDwell - elapsed).Round(time.Second)
			if remaining < time.Second {
				remaining = time.Second
			}
			return &ControlError{
				HTTPStatus: http.StatusConflict,
				Code:       "COMMAND_COOLDOWN_ACTIVE",
				Title:      "Chiller Minimum Off Cooldown Active",
				Detail:     fmt.Sprintf("Chiller %s must satisfy the 15-minute minimum compressor off dwell before restarting (%s remaining).", chiller.Code, remaining),
			}
		}
	}

	if len(windowStarts) >= chillerMaxStartsPerHour {
		sort.Slice(windowStarts, func(i, j int) bool { return windowStarts[i].Before(windowStarts[j]) })
		oldestRelevant := windowStarts[len(windowStarts)-chillerMaxStartsPerHour]
		remaining := (chillerStartWindowDuration - now.Sub(oldestRelevant)).Round(time.Second)
		if remaining < time.Second {
			remaining = time.Second
		}
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_COOLDOWN_ACTIVE",
			Title:      "Chiller Anti-Recycle Limit Active",
			Detail:     fmt.Sprintf("Chiller %s reached the maximum of %d starts per 60 minutes (%s remaining before next start).", chiller.Code, chillerMaxStartsPerHour, remaining),
		}
	}
	return nil
}

func (a *ControlAuthority) enforceLastActiveChillerGuard(
	ctx context.Context,
	operator OperatorIdentity,
	topo shutdownTopology,
	target resolvedCommandPointTarget,
	currentObs *commandmodel.LivePointObservation,
	now time.Time,
) error {
	inSlot, err := a.isSiteInActiveCoolingSlot(ctx, operator, target.asset.SiteID, now)
	if err != nil {
		return err
	}
	if !inSlot {
		return nil
	}
	runningChillerIDs := make([]string, 0, len(topo.chillers))
	for _, ch := range topo.chillers {
		if ch.ID == target.asset.ID {
			if livePointSafe(currentObs) && commandmodel.ScalarMatches(currentObs.ReportedValue, commandmodel.TextScalar("RUNNING"), 0) {
				runningChillerIDs = append(runningChillerIDs, ch.ID)
				continue
			}
			st, _, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, target.asset.SiteID, topo, ch.ID)
			if probeErr == nil && !unsafeOrMissing && st != nil && commandmodel.ScalarMatches(st.ReportedValue, commandmodel.TextScalar("RUNNING"), 0) {
				runningChillerIDs = append(runningChillerIDs, ch.ID)
			}
			continue
		}
		st, _, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, target.asset.SiteID, topo, ch.ID)
		if probeErr == nil && !unsafeOrMissing && st != nil && commandmodel.ScalarMatches(st.ReportedValue, commandmodel.TextScalar("RUNNING"), 0) {
			runningChillerIDs = append(runningChillerIDs, ch.ID)
		}
	}
	if len(runningChillerIDs) == 1 && runningChillerIDs[0] == target.asset.ID {
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_LAST_ACTIVE_CHILLER_PROTECTED",
			Title:      "Last Active Chiller Protected",
			Detail:     fmt.Sprintf("Cannot stop the last running chiller %s while an active cooling operating slot is in effect. Use PLANT_SAFE_SHUTDOWN via /control-shutdown if a full-plant shutdown is intended.", target.asset.Code),
		}
	}
	return nil
}

func (a *ControlAuthority) isSiteInActiveCoolingSlot(ctx context.Context, operator OperatorIdentity, siteID string, now time.Time) (bool, error) {
	if a == nil || a.twin == nil {
		return false, nil
	}
	schedule, exceptions, err := a.twin.GetSiteOperatingSchedule(ctx, operator, siteID)
	if err != nil {
		var ce *ControlError
		if errors.As(err, &ce) && ce.HTTPStatus == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	inSlot, evalErr := evaluateActiveCoolingSlot(schedule, exceptions, now)
	if evalErr != nil {
		return false, unavailableControlError("Command Service could not evaluate the Site operating schedule.")
	}
	return inSlot, nil
}

func evaluateActiveCoolingSlot(schedule OperatingScheduleDTO, exceptions []OperatingExceptionDTO, now time.Time) (bool, error) {
	eval, err := planttwin.EvaluateOperatingSchedule(now, toPlantTwinOperatingSchedule(schedule, exceptions))
	if err != nil {
		return false, err
	}
	return eval.InBaseSlot, nil
}

func toPlantTwinOperatingSchedule(schedule OperatingScheduleDTO, exceptions []OperatingExceptionDTO) planttwin.OperatingSchedule {
	slots := make([]planttwin.ScheduleSlot, 0, len(schedule.Slots))
	for _, slot := range schedule.Slots {
		slots = append(slots, planttwin.ScheduleSlot{
			DayOfWeek:   slot.DayOfWeek,
			StartMinute: slot.StartMinute,
			EndMinute:   slot.EndMinute,
		})
	}
	approvedExceptions := make([]planttwin.ScheduleException, 0, len(exceptions))
	for _, exc := range exceptions {
		if !strings.EqualFold(strings.TrimSpace(exc.Status), "APPROVED") {
			continue
		}
		repSlots := make([]planttwin.ScheduleSlot, 0, len(exc.ReplacementSlots))
		for _, slot := range exc.ReplacementSlots {
			repSlots = append(repSlots, planttwin.ScheduleSlot{
				StartMinute: slot.StartMinute,
				EndMinute:   slot.EndMinute,
			})
		}
		approvedExceptions = append(approvedExceptions, planttwin.ScheduleException{
			Kind:             exc.Kind,
			StartDate:        exc.StartDate,
			EndDate:          exc.EndDate,
			ReplacementSlots: repSlots,
		})
	}
	return planttwin.OperatingSchedule{
		Timezone:   schedule.Timezone,
		Slots:      slots,
		Exceptions: approvedExceptions,
	}
}

type hydraulicInterlockKind int

const (
	hydraulicCheckNone hydraulicInterlockKind = iota
	hydraulicCheckValveOpen
	hydraulicCheckFlowProven
)

const minButterflyValveOpenPct = 80.0

type hydraulicInterlockCheck struct {
	point         TelemetryPointDTO
	kind          hydraulicInterlockKind
	analogOpening bool
}

func (a *ControlAuthority) enforceChillerStartHydraulicInterlock(
	ctx context.Context,
	operator OperatorIdentity,
	model SiteAssetModelDTO,
	topo shutdownTopology,
	target resolvedCommandPointTarget,
	now time.Time,
) error {
	deviceIDs := collectChillerHydraulicDeviceIDs(model, topo, target.asset.ID, target.device.ID, now)
	if len(deviceIDs) == 0 {
		return nil
	}

	checks := make([]hydraulicInterlockCheck, 0)
	for _, pt := range model.TelemetryPoints {
		if !strings.EqualFold(pt.Status, "ACTIVE") ||
			pt.TenantID != model.TenantID ||
			pt.SiteID != model.SiteID ||
			pt.PointType == "COMMAND" ||
			!deviceIDs[pt.ReportingDeviceID] {
			continue
		}
		kind, analogOpening := classifyHydraulicInterlockPoint(pt)
		if kind == hydraulicCheckNone {
			continue
		}
		checks = append(checks, hydraulicInterlockCheck{
			point:         pt,
			kind:          kind,
			analogOpening: analogOpening,
		})
	}
	if len(checks) == 0 {
		return nil
	}

	sort.Slice(checks, func(i, j int) bool {
		if checks[i].point.ReportingDeviceID != checks[j].point.ReportingDeviceID {
			return checks[i].point.ReportingDeviceID < checks[j].point.ReportingDeviceID
		}
		if checks[i].point.PointCode != checks[j].point.PointCode {
			return checks[i].point.PointCode < checks[j].point.PointCode
		}
		return checks[i].point.ID < checks[j].point.ID
	})

	chillerLabel := strings.TrimSpace(target.asset.Code)
	if chillerLabel == "" {
		chillerLabel = target.asset.ID
	}

	for _, chk := range checks {
		if a.live == nil {
			return hydraulicInterlockError(chillerLabel, chk.point.PointCode, chk.kind, "live telemetry reader unavailable")
		}
		obs, _, err := a.live.ReadLivePoint(ctx, operator.TenantID, target.asset.SiteID, chk.point.ReportingDeviceID, chk.point.PointCode)
		if err != nil {
			return hydraulicInterlockError(chillerLabel, chk.point.PointCode, chk.kind, "live telemetry read failed")
		}
		if !livePointSafe(obs) {
			return hydraulicInterlockError(chillerLabel, chk.point.PointCode, chk.kind, "telemetry is stale, offline, or not good quality")
		}
		ok, observed := evaluateHydraulicInterlockObservation(chk.kind, chk.analogOpening, obs)
		if !ok {
			return hydraulicInterlockError(chillerLabel, chk.point.PointCode, chk.kind, fmt.Sprintf("observed %s", observed))
		}
	}
	return nil
}

func hydraulicInterlockError(chillerLabel, pointCode string, kind hydraulicInterlockKind, reason string) *ControlError {
	requirement := "hydraulic flow switch must be proven"
	if kind == hydraulicCheckValveOpen {
		requirement = "isolation butterfly valve must be open"
	}
	return &ControlError{
		HTTPStatus: http.StatusConflict,
		Code:       "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN",
		Title:      "Chiller Hydraulic Interlock Not Proven",
		Detail:     fmt.Sprintf("Cannot start chiller %s: %s (point %s: %s).", chillerLabel, requirement, pointCode, reason),
	}
}

func collectChillerHydraulicDeviceIDs(
	model SiteAssetModelDTO,
	topo shutdownTopology,
	chillerID, targetDeviceID string,
	now time.Time,
) map[string]bool {
	out := map[string]bool{}
	addAssetDevices := func(assetID string) {
		for _, devID := range topo.boundDevicesByAsset[assetID] {
			if strings.TrimSpace(devID) != "" {
				out[devID] = true
			}
		}
	}
	addAssetDevices(chillerID)
	if strings.TrimSpace(targetDeviceID) != "" {
		out[targetDeviceID] = true
	}

	assetsByID := make(map[string]AssetDTO, len(model.Assets))
	relatedAssets := map[string]bool{chillerID: true}
	for _, asset := range model.Assets {
		if strings.EqualFold(asset.Status, "ACTIVE") && asset.TenantID == model.TenantID && asset.SiteID == model.SiteID {
			assetsByID[asset.ID] = asset
		}
	}
	for _, aux := range topo.auxiliaries {
		if !isPostStopCooldownPumpType(aux.AssetType) {
			continue
		}
		if containsString(topo.servedChillersByAux[aux.ID], chillerID) {
			relatedAssets[aux.ID] = true
			addAssetDevices(aux.ID)
		}
	}

	for _, rel := range model.Relationships {
		if !commandRelationshipCurrent(rel, now) ||
			!strings.EqualFold(rel.FromType, planttwin.SubjectTypeAsset) ||
			!strings.EqualFold(rel.ToType, planttwin.SubjectTypeAsset) ||
			!isHydraulicInterlockRelRole(rel.Role) {
			continue
		}
		if relatedAssets[rel.FromID] {
			if peer, ok := assetsByID[rel.ToID]; ok && isHydraulicValveAssetType(peer.AssetType) {
				addAssetDevices(peer.ID)
			}
		}
		if relatedAssets[rel.ToID] {
			if peer, ok := assetsByID[rel.FromID]; ok && isHydraulicValveAssetType(peer.AssetType) {
				addAssetDevices(peer.ID)
			}
		}
	}
	return out
}

func isHydraulicInterlockRelRole(role string) bool {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case planttwin.RelationshipServes, "CONTROLS", "MEASURES", "ATTACHED_TO":
		return true
	default:
		return false
	}
}

func isHydraulicValveAssetType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case "VALVE", "BUTTERFLY_VALVE", "ISOLATION_VALVE", "CHILLED_WATER_VALVE", "COOLING_WATER_VALVE", "CHW_VALVE", "CW_VALVE":
		return true
	default:
		return false
	}
}

func classifyHydraulicInterlockPoint(pt TelemetryPointDTO) (hydraulicInterlockKind, bool) {
	pointToken := normalizeHydraulicPointToken(pt.PointCode)
	sourceSuffix := normalizeHydraulicPointToken(lastDotSegment(pt.SourceKey))
	unitMeta, _ := pt.SourceMetadata["unit"].(string)
	metaHint, _ := pt.SourceMetadata["hydraulicInterlock"].(string)
	trimmedHint := strings.ToUpper(strings.TrimSpace(metaHint))

	isAnalogValve := strings.Contains(pointToken, "position") ||
		strings.Contains(pointToken, "opening") ||
		strings.Contains(sourceSuffix, "position") ||
		strings.Contains(sourceSuffix, "opening") ||
		strings.TrimSpace(unitMeta) == "%"

	switch trimmedHint {
	case "VALVE_OPEN":
		return hydraulicCheckValveOpen, isAnalogValve
	case "FLOW_PROVEN":
		return hydraulicCheckFlowProven, false
	}

	if isValveInterlockToken(pointToken) || isValveInterlockToken(sourceSuffix) {
		return hydraulicCheckValveOpen, isAnalogValve
	}
	if isFlowInterlockToken(pointToken) || isFlowInterlockToken(sourceSuffix) {
		return hydraulicCheckFlowProven, false
	}
	return hydraulicCheckNone, false
}

func isValveInterlockToken(token string) bool {
	switch token {
	case "valve_open",
		"butterfly_valve_open",
		"chw_valve_open",
		"cw_valve_open",
		"evap_valve_open",
		"cond_valve_open",
		"chilled_water_valve_open",
		"cooling_water_valve_open",
		"valve_status",
		"butterfly_valve_status",
		"chw_valve_status",
		"cw_valve_status",
		"valve_position",
		"valve_opening",
		"butterfly_valve_position",
		"butterfly_valve_opening",
		"chw_valve_position",
		"cw_valve_position",
		"chw_valve_opening",
		"cw_valve_opening":
		return true
	default:
		return false
	}
}

func isFlowInterlockToken(token string) bool {
	switch token {
	case "flow_switch",
		"water_flow_switch",
		"chw_flow_switch",
		"cw_flow_switch",
		"evap_flow_switch",
		"cond_flow_switch",
		"chilled_water_flow_switch",
		"cooling_water_flow_switch",
		"flow_status",
		"water_flow_status",
		"chw_flow_status",
		"cw_flow_status":
		return true
	default:
		return false
	}
}

func normalizeHydraulicPointToken(raw string) string {
	token := strings.ToLower(strings.TrimSpace(raw))
	token = strings.ReplaceAll(token, "-", "_")
	return token
}

func lastDotSegment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 && idx+1 < len(trimmed) {
		return trimmed[idx+1:]
	}
	return trimmed
}

func evaluateHydraulicInterlockObservation(
	kind hydraulicInterlockKind,
	analogOpening bool,
	obs *commandmodel.LivePointObservation,
) (bool, string) {
	val := obs.ReportedValue
	if val.Boolean != nil {
		return *val.Boolean, fmt.Sprintf("%t", *val.Boolean)
	}
	if val.Number != nil {
		num := *val.Number
		if math.IsNaN(num) || math.IsInf(num, 0) {
			return false, "non-finite"
		}
		if kind == hydraulicCheckValveOpen && analogOpening {
			return num >= minButterflyValveOpenPct, fmt.Sprintf("%.1f%%", num)
		}
		if kind == hydraulicCheckValveOpen {
			return num >= 1.0, fmt.Sprintf("%.2f", num)
		}
		return num > 0, fmt.Sprintf("%.2f", num)
	}
	if val.Text != nil {
		raw := strings.TrimSpace(*val.Text)
		upper := strings.ToUpper(raw)
		if kind == hydraulicCheckValveOpen {
			switch upper {
			case "OPEN", "OPENED", "ON", "TRUE":
				return true, raw
			case "CLOSED", "CLOSE", "OFF", "FALSE", "FAULT", "":
				return false, raw
			}
			if parsed, err := strconv.ParseFloat(strings.TrimSuffix(upper, "%"), 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
				if analogOpening {
					return parsed >= minButterflyValveOpenPct, raw
				}
				return parsed >= 1.0, raw
			}
			return false, raw
		}
		if kind == hydraulicCheckFlowProven {
			switch upper {
			case "FLOW", "PROVEN", "NORMAL", "ON", "OPEN", "TRUE", "OK":
				return true, raw
			case "NO_FLOW", "NOFLOW", "FALSE", "OFF", "CLOSED", "FAULT", "":
				return false, raw
			}
			if parsed, err := strconv.ParseFloat(upper, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
				return parsed > 0, raw
			}
			return false, raw
		}
	}
	return false, "empty"
}
