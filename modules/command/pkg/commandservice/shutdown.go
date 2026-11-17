package commandservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

type shutdownControlTarget struct {
	asset         AssetDTO
	device        DeviceDTO
	point         TelemetryPointDTO
	feedbackPoint TelemetryPointDTO
	capability    commandmodel.Capability
	profile       commandmodel.CapabilityProfile
	actuationMode string
	interlocks    []commandmodel.InterlockCheck
}

type shutdownTopology struct {
	chillers               []AssetDTO
	auxiliaries            []AssetDTO
	chillersByCode         map[string]AssetDTO
	lockedAssets           map[string]bool
	boundDevicesByAsset    map[string][]string
	controlsAssetsByPoint  map[string][]string
	regularStopByAsset     map[string]shutdownControlTarget
	abortSequenceByAsset   map[string]shutdownControlTarget
	statusCheckByAsset     map[string]shutdownControlTarget
	servedChillersByAux    map[string][]string
	emergencyReleaseTarget *shutdownControlTarget
}

func (a *ControlAuthority) ExecuteSiteControlShutdown(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	input SiteControlShutdownInput,
) (SiteControlShutdownResponse, error) {
	if a.twin == nil || a.live == nil || a.iam == nil {
		return SiteControlShutdownResponse{}, unavailableControlError("Command Service shutdown ports are not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return SiteControlShutdownResponse{}, notFoundControlError("The requested Site was not found.")
	}
	mode := strings.TrimSpace(input.Mode)
	switch mode {
	case ShutdownModePlantSafe, ShutdownModeEmergencyDoRelease, ShutdownModeAbortUnitSequence:
	default:
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "mode must be PLANT_SAFE_SHUTDOWN, EMERGENCY_DO_RELEASE, or ABORT_UNIT_SEQUENCE.",
		}
	}
	unitCode := strings.TrimSpace(input.UnitCode)
	if mode == ShutdownModeAbortUnitSequence && unitCode == "" {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "unitCode is required when mode is ABORT_UNIT_SEQUENCE.",
		}
	}
	if mode == ShutdownModeEmergencyDoRelease && !input.ConfirmEmergencyDoRelease {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "confirmEmergencyDoRelease must be explicitly true for EMERGENCY_DO_RELEASE.",
		}
	}

	reason, meta, err := a.resolveControlMutationMeta(input.Reason, input.ExpectedRevision, "", nil, func() (int64, error) {
		return 0, nil
	})
	if err != nil {
		return SiteControlShutdownResponse{}, err
	}

	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return SiteControlShutdownResponse{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN",
			Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to execute Site control shutdown.",
		}
	}
	if !operator.StepUpTOTPVerified {
		return SiteControlShutdownResponse{}, commandStepUpError("High-risk site control shutdown requires a 6-digit verificationCode from your authenticator app.")
	}

	assetModel, err := a.twin.GetSiteAssetModel(ctx, operator, siteID)
	if err != nil {
		return SiteControlShutdownResponse{}, err
	}
	currentState, err := a.twin.GetSiteControlState(ctx, operator, siteID)
	if err != nil {
		return SiteControlShutdownResponse{}, err
	}
	if input.ExpectedRevision == nil {
		meta.ExpectedRevision = currentState.Revision
	}

	topo := buildShutdownTopology(assetModel, currentState, a.now().UTC())
	idemBase := strings.TrimSpace(input.IdempotencyKey)
	principalID := resolveControlPrincipalID(currentState.UpdatedBy, decision.PrincipalID, operator.Subject)

	if mode == ShutdownModeAbortUnitSequence {
		return a.executeAbortUnitSequence(ctx, operator, siteID, topo, unitCode, reason, principalID, idemBase, currentState)
	}
	if mode == ShutdownModeEmergencyDoRelease && topo.emergencyReleaseTarget == nil {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "EMERGENCY_DO_RELEASE_UNAVAILABLE",
			Title: "Emergency DO release unavailable", Detail: "This Site has no active EMERGENCY_DO_RELEASE command point.",
		}
	}

	savedState, err := a.twin.SaveSiteControlMode(ctx, operator, siteID, SiteControlModeSafeShutdownExit, reason, meta)
	if err != nil {
		return SiteControlShutdownResponse{}, err
	}
	principalID = resolveControlPrincipalID(savedState.UpdatedBy, decision.PrincipalID, operator.Subject)
	cancelledIDs, cancelErr := a.store.CancelPending(ctx, commandmodel.CancelPendingRequest{
		TenantID:             operator.TenantID,
		SiteID:               siteID,
		PrincipalID:          principalID,
		Reason:               reason,
		AbortActiveSequences: true,
	})
	if cancelErr != nil {
		return SiteControlShutdownResponse{}, controlErrorFromStore(cancelErr, "Command Service could not cancel pending commands during shutdown.")
	}
	if cancelledIDs == nil {
		cancelledIDs = []string{}
	}
	topo = buildShutdownTopology(assetModel, savedState, a.now().UTC())

	submittedIDs := []string{}
	deferredIDs := []string{}
	if mode == ShutdownModeEmergencyDoRelease {
		target := *topo.emergencyReleaseTarget
		a.reconcileOutcomeUnknownDeviceCommands(ctx, operator, siteID, target)
		obs, _, liveErr := a.live.ReadLivePoint(ctx, operator.TenantID, siteID, target.device.ID, target.feedbackPoint.PointCode)
		precondMode := commandmodel.PreconditionRequireCurrentState
		if liveErr != nil || !livePointSafe(obs) {
			precondMode = commandmodel.PreconditionEmergencyBypass
			obs = nil
		}
		cmdID, cmdErr := a.dispatchShutdownTarget(ctx, operator, savedState, target, obs, precondMode, reason, idemBase, true)
		if cmdErr != nil {
			return SiteControlShutdownResponse{}, cmdErr
		}
		submittedIDs = append(submittedIDs, cmdID)
		a.failureQueue.enqueue(a.failureNotifier, ControlFailureEvent{
			Kind:       "EMERGENCY_DO_RELEASE",
			TenantID:   operator.TenantID,
			SiteID:     siteID,
			DeviceID:   target.device.ID,
			PointID:    target.point.ID,
			CommandID:  cmdID,
			Capability: target.capability,
			ReasonCode: "EMERGENCY_DO_RELEASE",
			Detail:     reason,
			OccurredAt: a.now().UTC(),
			ActorType:  "PRINCIPAL",
			ActorID:    principalID,
		})
	} else {
		chillerSubmitted, confirmedStopped, chillerErr := a.stopUnlockedChillers(ctx, operator, savedState, topo, topo.chillers, false, reason, idemBase)
		if chillerErr != nil {
			return SiteControlShutdownResponse{}, chillerErr
		}
		submittedIDs = append(submittedIDs, chillerSubmitted...)
		auxSubmitted, auxDeferred, auxErr := a.releaseOrDeferHoldingAuxiliaries(ctx, operator, savedState, topo, topo.auxiliaries, confirmedStopped, false, false, reason, idemBase)
		if auxErr != nil {
			return SiteControlShutdownResponse{}, auxErr
		}
		submittedIDs = append(submittedIDs, auxSubmitted...)
		deferredIDs = append(deferredIDs, auxDeferred...)
	}

	return SiteControlShutdownResponse{
		SiteControlState:        savedState,
		Mode:                    mode,
		CancelledCommandIDs:     cancelledIDs,
		SubmittedCommandIDs:     submittedIDs,
		DeferredHoldingAssetIds: deferredIDs,
	}, nil
}

func (a *ControlAuthority) executeAbortUnitSequence(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	topo shutdownTopology,
	unitCode, reason, principalID, idemBase string,
	currentState SiteControlStateView,
) (SiteControlShutdownResponse, error) {
	chiller, found := resolveShutdownChillerByUnitCode(topo, unitCode)
	if !found {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: fmt.Sprintf("unitCode %q did not match an active CHILLER asset on this Site.", unitCode),
		}
	}
	var unitAux []AssetDTO
	deviceIDs := append([]string(nil), topo.boundDevicesByAsset[chiller.ID]...)
	for _, aux := range topo.auxiliaries {
		if containsString(topo.servedChillersByAux[aux.ID], chiller.ID) {
			unitAux = append(unitAux, aux)
			for _, devID := range topo.boundDevicesByAsset[aux.ID] {
				deviceIDs = appendUniqueShutdownString(deviceIDs, devID)
			}
		}
	}
	cancelledIDs := []string{}
	if len(deviceIDs) > 0 {
		sort.Strings(deviceIDs)
		cancelled, cancelErr := a.store.CancelPending(ctx, commandmodel.CancelPendingRequest{
			TenantID:             operator.TenantID,
			SiteID:               siteID,
			DeviceIDs:            deviceIDs,
			PrincipalID:          principalID,
			Reason:               reason,
			AbortActiveSequences: true,
		})
		if cancelErr != nil {
			return SiteControlShutdownResponse{}, controlErrorFromStore(cancelErr, "Command Service could not abort unit sequence.")
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}
	submittedIDs := []string{}
	abortTarget, hasAbortTarget := topo.abortSequenceByAsset[chiller.ID]
	if hasAbortTarget && !topo.lockedAssets[chiller.ID] {
		abortObs, _, abortLiveErr := a.live.ReadLivePoint(ctx, operator.TenantID, siteID, abortTarget.device.ID, abortTarget.feedbackPoint.PointCode)
		abortPrecond := commandmodel.PreconditionRequireCurrentState
		if abortLiveErr != nil || !livePointSafe(abortObs) {
			abortPrecond = commandmodel.PreconditionEmergencyBypass
			abortObs = nil
		}
		abortID, abortErr := a.dispatchShutdownTarget(ctx, operator, currentState, abortTarget, abortObs, abortPrecond, reason, idemBase, false)
		if abortErr != nil {
			var ce *ControlError
			if !isConflictControlError(abortErr, &ce) {
				return SiteControlShutdownResponse{}, abortErr
			}
		} else {
			submittedIDs = append(submittedIDs, abortID)
		}
	}
	chillerSubmitted, confirmedStopped, chillerErr := a.stopUnlockedChillers(ctx, operator, currentState, topo, []AssetDTO{chiller}, !hasAbortTarget, reason, idemBase)
	if chillerErr != nil {
		return SiteControlShutdownResponse{}, chillerErr
	}
	submittedIDs = append(submittedIDs, chillerSubmitted...)
	auxSubmitted, deferredIDs, auxErr := a.releaseOrDeferHoldingAuxiliaries(ctx, operator, currentState, topo, unitAux, confirmedStopped, true, true, reason, idemBase)
	if auxErr != nil {
		return SiteControlShutdownResponse{}, auxErr
	}
	submittedIDs = append(submittedIDs, auxSubmitted...)
	if len(cancelledIDs) == 0 && len(submittedIDs) == 0 && (topo.lockedAssets[chiller.ID] || len(deferredIDs) > 0) {
		return SiteControlShutdownResponse{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "CONTROL_SHUTDOWN_LOCKED",
			Title: "Control shutdown locked", Detail: "Unable to abort or stop the requested unit sequence because target assets are locked or waiting on active chillers.",
		}
	}
	return SiteControlShutdownResponse{
		SiteControlState:        currentState,
		Mode:                    ShutdownModeAbortUnitSequence,
		CancelledCommandIDs:     cancelledIDs,
		SubmittedCommandIDs:     submittedIDs,
		DeferredHoldingAssetIds: deferredIDs,
	}, nil
}

func (a *ControlAuthority) stopUnlockedChillers(
	ctx context.Context,
	operator OperatorIdentity,
	stateView SiteControlStateView,
	topo shutdownTopology,
	chillers []AssetDTO,
	forceStopWhenStopped bool,
	reason, idemBase string,
) ([]string, map[string]bool, error) {
	submitted := []string{}
	confirmedStopped := make(map[string]bool, len(chillers))
	for _, chiller := range chillers {
		obs, stopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, stateView.SiteID, topo, chiller.ID)
		degradedBypass := probeErr != nil || unsafeOrMissing || !livePointSafe(obs)
		if degradedBypass {
			confirmedStopped[chiller.ID] = false
		} else {
			confirmedStopped[chiller.ID] = stopped
			if stopped && !forceStopWhenStopped {
				continue
			}
		}
		if topo.lockedAssets[chiller.ID] {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[chiller.ID]
		if !hasStop || (degradedBypass && len(stopTarget.interlocks) > 0) {
			continue
		}
		precondMode := commandmodel.PreconditionRequireCurrentState
		if degradedBypass {
			precondMode = commandmodel.PreconditionEmergencyBypass
			obs = nil
		}
		cmdID, cmdErr := a.dispatchShutdownTarget(ctx, operator, stateView, stopTarget, obs, precondMode, reason, idemBase, false)
		if cmdErr != nil {
			var ce *ControlError
			if isConflictControlError(cmdErr, &ce) || (len(stopTarget.interlocks) > 0 && isServiceUnavailableControlError(cmdErr, &ce)) {
				continue
			}
			return nil, nil, cmdErr
		}
		submitted = append(submitted, cmdID)
	}
	return submitted, confirmedStopped, nil
}

func (a *ControlAuthority) releaseOrDeferHoldingAuxiliaries(
	ctx context.Context,
	operator OperatorIdentity,
	stateView SiteControlStateView,
	topo shutdownTopology,
	auxAssets []AssetDTO,
	confirmedStopped map[string]bool,
	stopAlreadyStopped, includeLockedInDeferred bool,
	reason, idemBase string,
) ([]string, []string, error) {
	submitted := []string{}
	deferred := []string{}
	for _, aux := range auxAssets {
		if topo.lockedAssets[aux.ID] {
			if includeLockedInDeferred {
				_, auxStopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, stateView.SiteID, topo, aux.ID)
				if probeErr != nil || unsafeOrMissing || !auxStopped {
					deferred = append(deferred, aux.ID)
				}
			}
			continue
		}
		auxObs, auxStopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, stateView.SiteID, topo, aux.ID)
		if probeErr != nil || unsafeOrMissing || !livePointSafe(auxObs) {
			deferred = append(deferred, aux.ID)
			continue
		}
		if auxStopped && !stopAlreadyStopped {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[aux.ID]
		if !hasStop {
			if !auxStopped {
				deferred = append(deferred, aux.ID)
			}
			continue
		}
		allServedStopped, checkErr := a.ensureChillersConfirmedStopped(ctx, operator, stateView.SiteID, topo, topo.servedChillersByAux[aux.ID], confirmedStopped)
		if checkErr != nil {
			return nil, nil, checkErr
		}
		if !allServedStopped {
			if !auxStopped {
				deferred = append(deferred, aux.ID)
			}
			continue
		}
		cmdID, cmdErr := a.dispatchShutdownTarget(ctx, operator, stateView, stopTarget, auxObs, commandmodel.PreconditionRequireCurrentState, reason, idemBase, false)
		if cmdErr != nil {
			var ce *ControlError
			if isConflictControlError(cmdErr, &ce) || (len(stopTarget.interlocks) > 0 && isServiceUnavailableControlError(cmdErr, &ce)) {
				deferred = append(deferred, aux.ID)
				continue
			}
			return nil, nil, cmdErr
		}
		submitted = append(submitted, cmdID)
	}
	return submitted, deferred, nil
}

func (a *ControlAuthority) ensureChillersConfirmedStopped(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	topo shutdownTopology,
	chillerIDs []string,
	confirmedStopped map[string]bool,
) (bool, error) {
	if len(chillerIDs) == 0 {
		return false, nil
	}
	for _, chID := range chillerIDs {
		stopped, checked := confirmedStopped[chID]
		if !checked {
			_, probedStopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, siteID, topo, chID)
			stopped = probeErr == nil && !unsafeOrMissing && probedStopped
			confirmedStopped[chID] = stopped
		}
		if !stopped {
			return false, nil
		}
	}
	return true, nil
}

func (a *ControlAuthority) probeAssetRunState(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	topo shutdownTopology,
	assetID string,
) (*commandmodel.LivePointObservation, bool, bool, error) {
	target, ok := topo.statusCheckByAsset[assetID]
	if !ok {
		return nil, false, false, nil
	}
	obs, _, err := a.live.ReadLivePoint(ctx, operator.TenantID, siteID, target.device.ID, target.feedbackPoint.PointCode)
	if err != nil {
		return nil, false, false, err
	}
	if !livePointSafe(obs) {
		return obs, false, true, nil
	}
	stopped := commandmodel.ScalarMatches(obs.ReportedValue, commandmodel.TextScalar("STOPPED"), 0)
	return obs, stopped, false, nil
}

func (a *ControlAuthority) dispatchShutdownTarget(
	ctx context.Context,
	operator OperatorIdentity,
	stateView SiteControlStateView,
	target shutdownControlTarget,
	obs *commandmodel.LivePointObservation,
	precondMode commandmodel.PreconditionMode,
	bypassReason string,
	idemBase string,
	allowEmergencyDo bool,
) (string, error) {
	peerObs, err := a.readGateInterlockPeerStates(ctx, operator.TenantID, stateView.SiteID, target.interlocks)
	if err != nil {
		return "", err
	}
	cleanBypassReason := strings.TrimSpace(bypassReason)
	if precondMode == commandmodel.PreconditionEmergencyBypass && cleanBypassReason == "" {
		cleanBypassReason = "degraded safe shutdown dispatch"
	}
	locks, mw := toGateLocksAndWindow(stateView.AssetLocks, stateView.ActiveMaintenanceWindow)
	_, rejection := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:                   commandmodel.GatePhaseSubmit,
		AssetID:                 target.asset.ID,
		DeviceID:                target.device.ID,
		Capability:              target.capability,
		CapabilityRevision:      target.profile.Revision,
		ParameterKey:            target.profile.ParameterKey,
		ActuationMode:           target.actuationMode,
		AllowEmergencyDoRelease: allowEmergencyDo,
		Parameters:              commandmodel.CommandParameters{},
		SiteMode:                commandmodel.SiteControlMode(stateView.ControlMode),
		AssetLocks:              locks,
		ActiveMaintenanceWindow: mw,
		PreconditionMode:        precondMode,
		BypassReason:            cleanBypassReason,
		TargetState:             obs,
		Interlocks:              target.interlocks,
		PeerStates:              peerObs,
		StepUpVerified:          true,
	})
	if rejection != nil {
		return "", problemFromGateRejection(rejection)
	}

	idemKey := a.buildShutdownCommandIdempotencyKey(idemBase, target.point.ID)
	res, err := a.mintGrantAndSubmit(
		ctx, operator, target.device.SiteID, target.device.ID, target.point.ID,
		idemKey, "", target.capability, target.profile, commandmodel.CommandParameters{},
		target.feedbackPoint.PointCode, precondMode, cleanBypassReason, obs,
	)
	if err != nil {
		return "", err
	}
	return res.Intent.ID, nil
}

func (a *ControlAuthority) reconcileOutcomeUnknownDeviceCommands(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	target shutdownControlTarget,
) {
	intents, err := a.store.List(ctx, operator.TenantID, siteID, MaximumListedCommands)
	if err != nil {
		return
	}
	for _, intent := range intents {
		if intent.DeviceID != target.device.ID || intent.Status != commandmodel.IntentOutcomeUnknown {
			continue
		}
		profile, ok := commandmodel.CapabilityProfileFor(intent.Capability)
		if !ok {
			continue
		}
		claims, authErr := a.mintAndValidateCommandGrant(
			ctx, operator, commandmodel.AuthorizationCommandSubmit,
			target.device.SiteID, target.device.ID, intent.Capability, profile.Revision, intent.Risk,
		)
		if authErr != nil {
			continue
		}
		_, _ = a.store.Reconcile(ctx, commandmodel.ReconcileRequest{
			TenantID:      operator.TenantID,
			CommandID:     intent.ID,
			Outcome:       commandmodel.ReconciledNotApplied,
			Authorization: commandauth.Snapshot(claims),
		})
	}
}

func (a *ControlAuthority) buildShutdownCommandIdempotencyKey(idemBase, pointID string) string {
	cleanBase := strings.TrimSpace(idemBase)
	if cleanBase != "" && controlWriteKeyPattern.MatchString(cleanBase) {
		maxBase := 128 - 5 - len(pointID)
		if maxBase > 0 && len(cleanBase) > maxBase {
			cleanBase = cleanBase[:maxBase]
		}
		return "shd-" + cleanBase + "-" + pointID
	}
	token, err := newUUID(a.now().UTC())
	if err != nil {
		return "shd-fallback-" + pointID
	}
	return "shd-" + token
}

func resolveSiteControlTopology(model SiteAssetModelDTO, now time.Time) shutdownTopology {
	topo := shutdownTopology{
		chillersByCode:        map[string]AssetDTO{},
		lockedAssets:          map[string]bool{},
		boundDevicesByAsset:   map[string][]string{},
		controlsAssetsByPoint: map[string][]string{},
		regularStopByAsset:    map[string]shutdownControlTarget{},
		abortSequenceByAsset:  map[string]shutdownControlTarget{},
		statusCheckByAsset:    map[string]shutdownControlTarget{},
		servedChillersByAux:   map[string][]string{},
	}
	assetsByID := make(map[string]AssetDTO, len(model.Assets))
	plantUnits := make([]planttwin.PlantUnit, 0, len(model.Assets))
	for _, asset := range model.Assets {
		if !strings.EqualFold(asset.Status, "ACTIVE") {
			continue
		}
		assetsByID[asset.ID] = asset
		plantUnits = append(plantUnits, planttwin.PlantUnit{
			AssetID:   asset.ID,
			AssetCode: asset.Code,
			AssetName: asset.DisplayName,
			AssetType: asset.AssetType,
		})
		switch strings.ToUpper(strings.TrimSpace(asset.AssetType)) {
		case planttwin.AssetTypeChiller:
			topo.chillers = append(topo.chillers, asset)
			if code := strings.ToLower(strings.TrimSpace(asset.Code)); code != "" {
				topo.chillersByCode[code] = asset
			}
		default:
			if isShutdownHoldingAuxiliaryType(asset.AssetType) {
				topo.auxiliaries = append(topo.auxiliaries, asset)
			}
		}
	}
	sortShutdownAssets(topo.chillers)
	sortShutdownAssets(topo.auxiliaries)

	devicesByID := make(map[string]DeviceDTO, len(model.Devices))
	for _, device := range model.Devices {
		if strings.EqualFold(device.Status, "ACTIVE") && device.TenantID == model.TenantID && device.SiteID == model.SiteID {
			devicesByID[device.ID] = device
		}
	}

	points := append([]TelemetryPointDTO(nil), model.TelemetryPoints...)
	sort.Slice(points, func(i, j int) bool {
		if points[i].PointCode != points[j].PointCode {
			return points[i].PointCode < points[j].PointCode
		}
		return points[i].ID < points[j].ID
	})
	pointsByID := make(map[string]TelemetryPointDTO, len(points))
	feedbackByDeviceKey := make(map[string]TelemetryPointDTO, len(points))
	for _, pt := range points {
		if !strings.EqualFold(pt.Status, "ACTIVE") || pt.TenantID != model.TenantID || pt.SiteID != model.SiteID {
			continue
		}
		pointsByID[pt.ID] = pt
		if pt.PointType == "STATE" || pt.PointType == "TELEMETRY" || pt.PointType == "SETTING" {
			feedbackByDeviceKey[pt.ReportingDeviceID+"\x00"+pt.PointCode] = pt
		}
	}

	controlsByPoint := map[string][]string{}
	controlsSite := map[string]bool{}
	var hydraulicEdges []planttwin.HydraulicEdge
	for _, rel := range model.Relationships {
		if !commandRelationshipCurrent(rel, now) {
			continue
		}
		switch {
		case rel.FromType == "POINT" && rel.Role == "CONTROLS":
			if rel.ToType == "SITE" && rel.ToID == model.SiteID {
				controlsSite[rel.FromID] = true
			}
			if rel.ToType == "ASSET" {
				if asset, ok := assetsByID[rel.ToID]; ok {
					controlsByPoint[rel.FromID] = appendUniqueShutdownString(controlsByPoint[rel.FromID], asset.ID)
					if pt, ok := pointsByID[rel.FromID]; ok {
						topo.boundDevicesByAsset[asset.ID] = appendUniqueShutdownString(topo.boundDevicesByAsset[asset.ID], pt.ReportingDeviceID)
					}
				}
			}
		case rel.FromType == "DEVICE" && rel.ToType == "ASSET":
			if asset, ok := assetsByID[rel.ToID]; ok {
				topo.boundDevicesByAsset[asset.ID] = appendUniqueShutdownString(topo.boundDevicesByAsset[asset.ID], rel.FromID)
			}
		case strings.EqualFold(rel.FromType, planttwin.SubjectTypeAsset) &&
			strings.EqualFold(rel.ToType, planttwin.SubjectTypeAsset) &&
			strings.EqualFold(rel.Role, planttwin.RelationshipServes):
			if _, fromOK := assetsByID[rel.FromID]; fromOK {
				if _, toOK := assetsByID[rel.ToID]; toOK {
					hydraulicEdges = append(hydraulicEdges, planttwin.HydraulicEdge{
						FromAssetID:      rel.FromID,
						ToAssetID:        rel.ToID,
						RelationshipRole: planttwin.RelationshipServes,
					})
				}
			}
		}
	}

	if graph, err := planttwin.NewHydraulicGraph(plantUnits, hydraulicEdges); err == nil {
		for _, aux := range topo.auxiliaries {
			for _, downstreamID := range graph.ReachableDownstream(aux.ID) {
				if asset, ok := assetsByID[downstreamID]; ok && strings.EqualFold(strings.TrimSpace(asset.AssetType), planttwin.AssetTypeChiller) {
					topo.servedChillersByAux[aux.ID] = appendUniqueShutdownString(topo.servedChillersByAux[aux.ID], asset.ID)
				}
			}
			for _, directID := range graph.Downstream(aux.ID) {
				if directAsset, ok := assetsByID[directID]; ok && isHydraulicLoopAssetType(directAsset.AssetType) {
					for _, ch := range graph.EquipmentServingLoop(directAsset.ID, planttwin.AssetTypeChiller) {
						topo.servedChillersByAux[aux.ID] = appendUniqueShutdownString(topo.servedChillersByAux[aux.ID], ch.AssetID)
					}
				}
			}
			if chIDs := topo.servedChillersByAux[aux.ID]; len(chIDs) > 0 {
				sort.Strings(chIDs)
				topo.servedChillersByAux[aux.ID] = chIDs
			}
		}
	}

	topo.controlsAssetsByPoint = controlsByPoint
	topo.resolveTargets(model, assetsByID, devicesByID, points, feedbackByDeviceKey, controlsByPoint, controlsSite)
	topo.assignFallbackRunStates(assetsByID, devicesByID, feedbackByDeviceKey)
	return topo
}

func buildShutdownTopology(model SiteAssetModelDTO, state SiteControlStateView, now time.Time) shutdownTopology {
	topo := resolveSiteControlTopology(model, now)
	topo.indexAssetLocks(state.AssetLocks)
	return topo
}

func resolveAbortTargetForCommand(topo shutdownTopology, pointID, deviceID string) (shutdownControlTarget, bool) {
	for _, assetID := range topo.controlsAssetsByPoint[pointID] {
		if target, ok := topo.abortSequenceByAsset[assetID]; ok {
			return target, true
		}
	}
	for _, target := range topo.abortSequenceByAsset {
		if target.device.ID == deviceID {
			return target, true
		}
	}
	return shutdownControlTarget{}, false
}

func (topo *shutdownTopology) indexAssetLocks(locks []AssetControlLockView) {
	for _, lock := range locks {
		if lock.LockMode == AssetControlLockUnlocked {
			continue
		}
		topo.lockedAssets[lock.AssetID] = true
		for _, devID := range lock.BoundDeviceIDs {
			topo.boundDevicesByAsset[lock.AssetID] = appendUniqueShutdownString(topo.boundDevicesByAsset[lock.AssetID], devID)
		}
	}
	for assetID, devIDs := range topo.boundDevicesByAsset {
		for _, lock := range locks {
			if lock.LockMode == AssetControlLockUnlocked {
				continue
			}
			for _, devID := range devIDs {
				if containsString(lock.BoundDeviceIDs, devID) {
					topo.lockedAssets[assetID] = true
					break
				}
			}
		}
	}
}

func (topo *shutdownTopology) resolveTargets(
	model SiteAssetModelDTO,
	assetsByID map[string]AssetDTO,
	devicesByID map[string]DeviceDTO,
	points []TelemetryPointDTO,
	feedbackByDeviceKey map[string]TelemetryPointDTO,
	controlsByPoint map[string][]string,
	controlsSite map[string]bool,
) {
	var fallbackEmergency *shutdownControlTarget
	for _, point := range points {
		if !strings.EqualFold(point.Status, "ACTIVE") || point.PointType != "COMMAND" || !point.Writable ||
			point.TenantID != model.TenantID || point.SiteID != model.SiteID {
			continue
		}
		device, hasDevice := devicesByID[point.ReportingDeviceID]
		if !hasDevice {
			continue
		}
		capName, _ := point.SourceMetadata["capability"].(string)
		capability := commandmodel.Capability(strings.TrimSpace(capName))
		profile, supported := commandmodel.CapabilityProfileFor(capability)
		declaredRev, _ := point.SourceMetadata["capabilityRevision"].(string)
		feedbackKey, _ := point.SourceMetadata["feedbackPointKey"].(string)
		feedbackKey = strings.TrimSpace(feedbackKey)
		if !supported || strings.TrimSpace(declaredRev) != profile.Revision || feedbackKey == "" {
			continue
		}
		feedbackPoint, hasFeedback := feedbackByDeviceKey[device.ID+"\x00"+feedbackKey]
		if !hasFeedback {
			continue
		}
		interlocks, interlockErr := parsePointInterlockChecks(point.SourceMetadata, model)
		if interlockErr != nil {
			continue
		}
		actuationMode, _ := point.SourceMetadata["actuationMode"].(string)
		trimmedMode := strings.ToUpper(strings.TrimSpace(actuationMode))
		baseTarget := shutdownControlTarget{
			device: device, point: point, feedbackPoint: feedbackPoint,
			capability: capability, profile: profile, actuationMode: trimmedMode, interlocks: interlocks,
		}
		if capability == commandmodel.CapabilityStop && trimmedMode == "EMERGENCY_DO_RELEASE" {
			candidate := baseTarget
			if len(controlsByPoint[point.ID]) > 0 {
				candidate.asset = assetsByID[controlsByPoint[point.ID][0]]
			} else {
				candidate.asset = AssetDTO{TenantID: model.TenantID, SiteID: model.SiteID}
			}
			if controlsSite[point.ID] && topo.emergencyReleaseTarget == nil {
				topo.emergencyReleaseTarget = &candidate
			} else if fallbackEmergency == nil {
				fallbackEmergency = &candidate
			}
			continue
		}
		if (capability == commandmodel.CapabilityStop || capability == commandmodel.CapabilityStopUnitSequence) && trimmedMode == "ABORT_UNIT_SEQUENCE" {
			for _, assetID := range controlsByPoint[point.ID] {
				target := baseTarget
				target.asset = assetsByID[assetID]
				if _, exists := topo.abortSequenceByAsset[assetID]; !exists {
					topo.abortSequenceByAsset[assetID] = target
				}
			}
			continue
		}
		for _, assetID := range controlsByPoint[point.ID] {
			target := baseTarget
			target.asset = assetsByID[assetID]
			if capability == commandmodel.CapabilityStop {
				if _, exists := topo.regularStopByAsset[assetID]; !exists {
					topo.regularStopByAsset[assetID] = target
				}
				topo.statusCheckByAsset[assetID] = target
			} else if _, exists := topo.statusCheckByAsset[assetID]; !exists {
				topo.statusCheckByAsset[assetID] = target
			}
		}
	}
	if topo.emergencyReleaseTarget == nil {
		topo.emergencyReleaseTarget = fallbackEmergency
	}
}

func (topo *shutdownTopology) assignFallbackRunStates(
	assetsByID map[string]AssetDTO,
	devicesByID map[string]DeviceDTO,
	feedbackByDeviceKey map[string]TelemetryPointDTO,
) {
	for assetID, devIDs := range topo.boundDevicesByAsset {
		if _, exists := topo.statusCheckByAsset[assetID]; exists {
			continue
		}
		for _, devID := range devIDs {
			device, hasDevice := devicesByID[devID]
			if !hasDevice {
				continue
			}
			if feedbackPoint, hasFeedback := feedbackByDeviceKey[devID+"\x00run_state"]; hasFeedback {
				topo.statusCheckByAsset[assetID] = shutdownControlTarget{
					asset: assetsByID[assetID], device: device, feedbackPoint: feedbackPoint,
				}
				break
			}
		}
	}
}

func resolveShutdownChillerByUnitCode(topo shutdownTopology, unitCode string) (AssetDTO, bool) {
	normalized := strings.ToLower(unitCode)
	if chiller, found := topo.chillersByCode[normalized]; found {
		return chiller, true
	}
	if strings.HasPrefix(normalized, "unit-") {
		if chiller, found := topo.chillersByCode["chiller-"+strings.TrimPrefix(normalized, "unit-")]; found {
			return chiller, true
		}
	}
	return AssetDTO{}, false
}

func isShutdownHoldingAuxiliaryType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterPump, planttwin.AssetTypeCoolingWaterPump, planttwin.AssetTypeCoolingTower, "PUMP":
		return true
	default:
		return false
	}
}

func isHydraulicLoopAssetType(assetType string) bool {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case planttwin.AssetTypeChilledWaterLoop,
		planttwin.AssetTypeCoolingWaterLoop,
		"CHW_LOOP",
		"CW_LOOP",
		"CHILLED_WATER_HEADER",
		"COOLING_WATER_HEADER",
		"LOOP",
		"PLANT_LOOP",
		"HYDRAULIC_LOOP",
		"HEADER":
		return true
	default:
		return false
	}
}

func appendUniqueShutdownString(items []string, candidate string) []string {
	if candidate == "" || containsString(items, candidate) {
		return items
	}
	return append(items, candidate)
}

func sortShutdownAssets(items []AssetDTO) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].ID < items[j].ID
	})
}

func livePointSafe(obs *commandmodel.LivePointObservation) bool {
	return obs != nil &&
		obs.EvaluationAvailability == "AVAILABLE" &&
		obs.Presence == "ONLINE" &&
		obs.Readiness == "CURRENT" &&
		obs.Freshness == "FRESH" &&
		obs.Quality == "GOOD" &&
		obs.BusinessRevision > 0 &&
		!obs.ObservedAt.IsZero()
}

func isConflictControlError(err error, target **ControlError) bool {
	if err == nil {
		return false
	}
	var ce *ControlError
	if errors.As(err, &ce) {
		if target != nil {
			*target = ce
		}
		return ce.HTTPStatus == http.StatusConflict
	}
	return false
}

func isServiceUnavailableControlError(err error, target **ControlError) bool {
	if err == nil {
		return false
	}
	var ce *ControlError
	if errors.As(err, &ce) {
		if target != nil {
			*target = ce
		}
		return ce.HTTPStatus == http.StatusServiceUnavailable
	}
	return false
}

const safeShutdownAuxCoastDown = 60 * time.Second

func (a *ControlAuthority) releaseSafeShutdownHoldingAuxiliariesBestEffort(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	state SiteControlStateView,
) {
	if a == nil || a.twin == nil || a.live == nil || a.store == nil || a.iam == nil {
		return
	}
	assetModel, modelErr := a.twin.GetSiteAssetModel(ctx, operator, siteID)
	if modelErr != nil {
		return
	}
	now := a.now().UTC()
	topo := buildShutdownTopology(assetModel, state, now)
	if len(topo.auxiliaries) == 0 {
		return
	}
	listedCommands, _ := a.store.List(ctx, operator.TenantID, siteID, MaximumListedCommands)

	var safeShutdownEnteredAt time.Time
	if state.UpdatedAt != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*state.UpdatedAt)); err == nil {
			safeShutdownEnteredAt = parsed.UTC()
		}
	}

	chillerReady := make(map[string]bool, len(topo.chillers))
	isServedChillerCoastDownComplete := func(chID string) bool {
		if ready, ok := chillerReady[chID]; ok {
			return ready
		}
		chObs, stopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, siteID, topo, chID)
		if probeErr != nil || unsafeOrMissing || !stopped {
			chillerReady[chID] = false
			return false
		}
		latestStopSucceededAt, hasStopSucceeded, hasInFlightStop := latestDeviceStopStatus(topo.boundDevicesByAsset[chID], listedCommands)
		if hasInFlightStop {
			chillerReady[chID] = false
			return false
		}
		if hasStopSucceeded && now.Sub(latestStopSucceededAt) < safeShutdownAuxCoastDown {
			chillerReady[chID] = false
			return false
		}
		ready := (hasStopSucceeded && now.Sub(latestStopSucceededAt) >= safeShutdownAuxCoastDown) ||
			(chObs != nil && !chObs.ObservedAt.IsZero() && now.Sub(chObs.ObservedAt) >= safeShutdownAuxCoastDown) ||
			(!safeShutdownEnteredAt.IsZero() && now.Sub(safeShutdownEnteredAt) >= safeShutdownAuxCoastDown)
		chillerReady[chID] = ready
		return ready
	}

	for _, aux := range topo.auxiliaries {
		if topo.lockedAssets[aux.ID] {
			continue
		}
		stopTarget, hasStop := topo.regularStopByAsset[aux.ID]
		if !hasStop {
			continue
		}
		auxObs, auxStopped, unsafeOrMissing, probeErr := a.probeAssetRunState(ctx, operator, siteID, topo, aux.ID)
		if probeErr != nil || unsafeOrMissing || auxStopped || !livePointSafe(auxObs) {
			continue
		}
		auxDevices := append([]string{stopTarget.device.ID}, topo.boundDevicesByAsset[aux.ID]...)
		if auxStopAt, hasAuxSucceeded, hasAuxInFlight := latestDeviceStopStatus(auxDevices, listedCommands); hasAuxInFlight || (hasAuxSucceeded && now.Sub(auxStopAt) < safeShutdownAuxCoastDown) {
			continue
		}
		served := topo.servedChillersByAux[aux.ID]
		if len(served) == 0 {
			continue
		}
		allReady := true
		for _, chID := range served {
			if !isServedChillerCoastDownComplete(chID) {
				allReady = false
				break
			}
		}
		if !allReady {
			continue
		}
		_, _ = a.dispatchShutdownTarget(ctx, operator, state, stopTarget, auxObs, commandmodel.PreconditionRequireCurrentState, state.Reason, "", false)
	}
}

func latestDeviceStopStatus(deviceIDs []string, commands []commandmodel.CommandIntent) (time.Time, bool, bool) {
	var latestSucceeded time.Time
	hasSucceeded := false
	hasInFlight := false
	for _, cmd := range commands {
		if !containsString(deviceIDs, cmd.DeviceID) {
			continue
		}
		if !isStopCapability(cmd.Capability) {
			continue
		}
		switch cmd.Status {
		case commandmodel.IntentSubmitted, commandmodel.IntentValidating, commandmodel.IntentAwaitingApproval,
			commandmodel.IntentApproved, commandmodel.IntentQueued, commandmodel.IntentDispatching, commandmodel.IntentOutcomeUnknown:
			hasInFlight = true
		case commandmodel.IntentSucceeded:
			if !hasSucceeded || cmd.UpdatedAt.After(latestSucceeded) {
				latestSucceeded = cmd.UpdatedAt
				hasSucceeded = true
			}
		}
	}
	return latestSucceeded, hasSucceeded, hasInFlight
}
