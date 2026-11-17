package commandservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

const (
	chillerStartupBlindWindow  = 6 * time.Minute
	chillerMinRunDwell         = 20 * time.Minute
	chillerMinOffDwell         = 15 * time.Minute
	chillerStartWindowDuration = 60 * time.Minute
	chillerMaxStartsPerHour    = 2
)

func isStartupBlindWindowAuxType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterPump, planttwin.AssetTypeCoolingWaterPump, planttwin.AssetTypeCoolingTower:
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
		if err := checkAuxStopStartupBlindWindow(topo, target.asset, topo.servedChillersByAux[target.asset.ID], commands, now); err != nil {
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
		}
	}
	return nil
}

func checkAuxStopStartupBlindWindow(
	topo shutdownTopology,
	aux AssetDTO,
	servedChillerIDs []string,
	commands []commandmodel.CommandIntent,
	now time.Time,
) error {
	chillerLabels := make(map[string]string, len(topo.chillers))
	for _, ch := range topo.chillers {
		label := strings.TrimSpace(ch.Code)
		if label == "" {
			label = ch.ID
		}
		chillerLabels[ch.ID] = label
	}
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
