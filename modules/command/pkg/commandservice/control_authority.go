package commandservice

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	dispatchSafetyStateKey   = "run_state"
	maxPreDispatchClaimScans = 3
)

var (
	controlWriteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	lowerUUIDPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type ControlAuthorityConfig struct {
	Store                  HTTPAuthority
	Runtime                RuntimeStore
	Twin                   PlantTwinPort
	Live                   TelemetryLivePort
	IAM                    IAMAuthorizerPort
	CommandGrantPublicKey  crypto.PublicKey
	CommandGrantIssuer     string
	CommandGrantPresenter  string
	CommandGrantAudience   string
	CommandGrantUseChecker func(commandauth.GrantClaims) (commandauth.UseStatus, error)
	ControlFailureNotifier ControlFailureNotifier
	Now                    func() time.Time
}

type ControlAuthority struct {
	store                  HTTPAuthority
	runtime                RuntimeStore
	twin                   PlantTwinPort
	live                   TelemetryLivePort
	iam                    IAMAuthorizerPort
	commandGrantPublicKey  crypto.PublicKey
	commandGrantIssuer     string
	commandGrantPresenter  string
	commandGrantAudience   string
	commandGrantUseChecker func(commandauth.GrantClaims) (commandauth.UseStatus, error)
	failureNotifier        ControlFailureNotifier
	now                    func() time.Time
}

func NewControlAuthority(config ControlAuthorityConfig) (*ControlAuthority, error) {
	if config.Store == nil || config.Runtime == nil {
		return nil, errors.New("control authority requires command store and runtime store")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	issuer := strings.TrimSpace(config.CommandGrantIssuer)
	if issuer == "" {
		issuer = "spiffe://hvac.local/iam-service"
	}
	presenter := strings.TrimSpace(config.CommandGrantPresenter)
	if presenter == "" {
		presenter = "spiffe://hvac.local/command-service"
	}
	audience := strings.TrimSpace(config.CommandGrantAudience)
	if audience == "" {
		audience = "command-service"
	}
	return &ControlAuthority{
		store:                  config.Store,
		runtime:                config.Runtime,
		twin:                   config.Twin,
		live:                   config.Live,
		iam:                    config.IAM,
		commandGrantPublicKey:  config.CommandGrantPublicKey,
		commandGrantIssuer:     issuer,
		commandGrantPresenter:  presenter,
		commandGrantAudience:   audience,
		commandGrantUseChecker: config.CommandGrantUseChecker,
		failureNotifier:        config.ControlFailureNotifier,
		now:                    now,
	}, nil
}

// ClaimDispatch wraps PostgresStore.ClaimDispatch with an out-of-transaction 3-phase
// Twin + Live safety gate (`EvaluateControlGate(GatePhasePreDispatch)`).
func (a *ControlAuthority) ClaimDispatch(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	for scan := 0; scan < maxPreDispatchClaimScans; scan++ {
		envelope, err := a.runtime.ClaimDispatch(ctx, tenantID, leaseOwner, leaseFor)
		if err != nil {
			return commandmodel.DispatchEnvelope{}, err
		}
		if a.twin == nil || a.live == nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_UNAVAILABLE",
			})
			return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
		}

		intent, err := a.store.Get(ctx, envelope.TenantID, envelope.CommandID)
		if err != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
		}

		dctx, err := a.twin.GetDispatchContext(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, envelope.PointID)
		if err != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
		}

		decision, rejection, evidenceID, evalErr := a.evaluatePreDispatch(ctx, envelope, intent, dctx)
		if evalErr != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
		}
		if rejection != nil {
			evID := evidenceID
			if evID == "" {
				evID = decision.EvidenceID
			}
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: rejection.Code,
				EvidenceID:  evID,
			})
			if commandmodel.TerminalPreSendRejection(rejection.Code) {
				continue
			}
			return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
		}
		return envelope, nil
	}
	return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
}

func (a *ControlAuthority) ResolveDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	return a.runtime.ResolveDispatch(ctx, envelope, result)
}

func (a *ControlAuthority) ClaimVerification(ctx context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	return a.runtime.ClaimVerification(ctx, tenantID, leaseOwner, leaseFor)
}

func (a *ControlAuthority) ResolveVerification(ctx context.Context, envelope commandmodel.VerificationEnvelope, result commandmodel.VerificationResult) error {
	return a.runtime.ResolveVerification(ctx, envelope, result)
}

func (a *ControlAuthority) PrepareConnectorEvidence(ctx context.Context, evidence commandmodel.PreparedConnectorEvidence) error {
	return a.runtime.PrepareConnectorEvidence(ctx, evidence)
}

func (a *ControlAuthority) CompleteConnectorEvidence(ctx context.Context, evidence commandmodel.CompletedConnectorEvidence) error {
	return a.runtime.CompleteConnectorEvidence(ctx, evidence)
}

func (a *ControlAuthority) CancelPending(ctx context.Context, request commandmodel.CancelPendingRequest) ([]string, error) {
	return a.store.CancelPending(ctx, request)
}

func (a *ControlAuthority) evaluatePreDispatch(
	ctx context.Context,
	envelope commandmodel.DispatchEnvelope,
	intent commandmodel.CommandIntent,
	dctx ControlDispatchContext,
) (commandmodel.GateDecision, *commandmodel.GateRejection, string, error) {
	profile, _ := commandmodel.CapabilityProfileFor(intent.Capability)
	actuation := strings.ToUpper(strings.TrimSpace(dctx.ActuationMode))
	isEmergencyDo := intent.Capability == commandmodel.CapabilityStop &&
		actuation == "EMERGENCY_DO_RELEASE" &&
		len(dctx.Interlocks) == 0
	isStopCap := intent.Capability == commandmodel.CapabilityStop || intent.Capability == commandmodel.CapabilityStopUnitSequence
	canBypassLiveErr := len(dctx.Interlocks) == 0 &&
		((intent.PreconditionMode == commandmodel.PreconditionEmergencyBypass && profile.SupportsEmergencyBypass && strings.TrimSpace(intent.BypassReason) != "") ||
			(isStopCap && (actuation == "PULSE" || actuation == "ABORT_UNIT_SEQUENCE")))

	var targetObs *commandmodel.LivePointObservation
	var evidenceID string
	if !isEmergencyDo {
		obs, evID, err := a.live.ReadLivePoint(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, dispatchSafetyStateKey)
		if err != nil {
			if !canBypassLiveErr {
				return commandmodel.GateDecision{}, nil, "", err
			}
		} else {
			targetObs = obs
			evidenceID = evID
		}
	}

	peerObs, err := a.readGateInterlockPeerStates(ctx, envelope.TenantID, envelope.SiteID, dctx.Interlocks)
	if err != nil {
		return commandmodel.GateDecision{}, nil, "", err
	}

	locks, mw := toGateLocksAndWindow(dctx.AssetLocks, dctx.ActiveMaintenanceWindow)
	decision, rejection := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		CommandID:               envelope.CommandID,
		Phase:                   commandmodel.GatePhasePreDispatch,
		AssetID:                 dctx.AssetID,
		DeviceID:                envelope.DeviceID,
		Capability:              intent.Capability,
		CapabilityRevision:      envelope.CapabilityRevision,
		ParameterKey:            profile.ParameterKey,
		ActuationMode:           dctx.ActuationMode,
		Parameters:              envelope.Parameters,
		SiteMode:                commandmodel.SiteControlMode(dctx.ControlMode),
		AssetLocks:              locks,
		ActiveMaintenanceWindow: mw,
		PreconditionMode:        intent.PreconditionMode,
		BypassReason:            intent.BypassReason,
		TargetState:             targetObs,
		Interlocks:              dctx.Interlocks,
		PeerStates:              peerObs,
	})
	return decision, rejection, evidenceID, nil
}

type resolvedCommandPointTarget struct {
	asset         AssetDTO
	device        DeviceDTO
	point         TelemetryPointDTO
	feedbackPoint TelemetryPointDTO
	capability    commandmodel.Capability
	profile       commandmodel.CapabilityProfile
	actuationMode string
	interlocks    []commandmodel.InterlockCheck
}

func (a *ControlAuthority) SubmitOperatorCommand(
	ctx context.Context,
	operator OperatorIdentity,
	input SubmitOperatorCommandInput,
) (SubmitOperatorCommandOutput, error) {
	if a.twin == nil || a.live == nil || a.iam == nil {
		return SubmitOperatorCommandOutput{}, unavailableControlError("Command Service control ports are not configured.")
	}
	if !controlWriteKeyPattern.MatchString(strings.TrimSpace(input.IdempotencyKey)) {
		return SubmitOperatorCommandOutput{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "A valid Idempotency-Key is required.",
		}
	}
	if !commandmodel.IsUUIDv7(input.AssetID) || !commandmodel.IsUUIDv7(input.CommandPointID) {
		return SubmitOperatorCommandOutput{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "The Command request is invalid.",
		}
	}

	asset, err := a.twin.GetAsset(ctx, operator, input.AssetID)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	model, err := a.twin.GetSiteAssetModel(ctx, operator, asset.SiteID)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	target, ctrlErr := resolveAssetCommandTarget(model, asset, input.CommandPointID, a.now().UTC())
	if ctrlErr != nil {
		return SubmitOperatorCommandOutput{}, ctrlErr
	}

	controlState, err := a.twin.GetSiteControlState(ctx, operator, asset.SiteID)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	targetObs, _, err := a.live.ReadLivePoint(ctx, operator.TenantID, asset.SiteID, target.device.ID, target.feedbackPoint.PointCode)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	peerObs, err := a.readGateInterlockPeerStates(ctx, operator.TenantID, asset.SiteID, target.interlocks)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}

	locks, mw := toGateLocksAndWindow(controlState.AssetLocks, controlState.ActiveMaintenanceWindow)
	decision, rejection := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:                   commandmodel.GatePhaseSubmit,
		AssetID:                 target.asset.ID,
		DeviceID:                target.device.ID,
		Capability:              target.capability,
		CapabilityRevision:      target.profile.Revision,
		ParameterKey:            target.profile.ParameterKey,
		ActuationMode:           target.actuationMode,
		AllowEmergencyDoRelease: false,
		Parameters:              input.Parameters,
		SiteMode:                commandmodel.SiteControlMode(controlState.ControlMode),
		AssetLocks:              locks,
		ActiveMaintenanceWindow: mw,
		PreconditionMode:        commandmodel.PreconditionRequireCurrentState,
		TargetState:             targetObs,
		Interlocks:              target.interlocks,
		PeerStates:              peerObs,
		StepUpVerified:          operator.StepUpTOTPVerified,
	})
	if rejection != nil {
		return SubmitOperatorCommandOutput{}, problemFromGateRejection(rejection)
	}
	if err := a.enforcePlantOperationalGuards(ctx, operator, model, target, targetObs); err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	if decision.StepUpRequired {
		return SubmitOperatorCommandOutput{}, commandStepUpError("High-risk commands require a 6-digit verificationCode from your authenticator app.")
	}

	submitted, err := a.mintGrantAndSubmit(
		ctx, operator, asset.SiteID, target.device.ID, target.point.ID,
		strings.TrimSpace(input.IdempotencyKey), target.capability, target.profile,
		input.Parameters, target.feedbackPoint.PointCode,
		commandmodel.PreconditionRequireCurrentState, "", targetObs,
	)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	return SubmitOperatorCommandOutput{
		View:     commandView(submitted.Intent),
		Replayed: submitted.Replayed,
	}, nil
}

func (a *ControlAuthority) GetOperatorCommand(ctx context.Context, operator OperatorIdentity, commandID string) (CommandView, error) {
	if !lowerUUIDPattern.MatchString(commandID) {
		return CommandView{}, notFoundControlError("The requested Command was not found.")
	}
	intent, err := a.store.Get(ctx, operator.TenantID, commandID)
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command Service could not load the requested Command.")
	}
	return commandView(intent), nil
}

func (a *ControlAuthority) ListOperatorCommands(ctx context.Context, operator OperatorIdentity, siteID string, limit int) ([]CommandView, error) {
	if a.iam == nil {
		return nil, unavailableControlError("IAM authorization is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) || limit < 1 || limit > MaximumListedCommands {
		return nil, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_LIST_REQUEST_INVALID",
			Title: "Command list request invalid", Detail: "siteId must be a Site UUID and limit must be between 1 and 100.",
		}
	}
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return nil, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return nil, notFoundControlError("The requested Site was not found.")
	}
	intents, err := a.store.List(ctx, operator.TenantID, siteID, limit)
	if err != nil {
		return nil, controlErrorFromStore(err, "Command Service could not list Site Commands.")
	}
	views := make([]CommandView, 0, len(intents))
	for _, intent := range intents {
		views = append(views, commandView(intent))
	}
	return views, nil
}

func (a *ControlAuthority) ApproveOperatorCommand(ctx context.Context, operator OperatorIdentity, commandID string) (CommandView, error) {
	if a.iam == nil {
		return CommandView{}, unavailableControlError("IAM authorization is not configured.")
	}
	if !lowerUUIDPattern.MatchString(commandID) {
		return CommandView{}, notFoundControlError("The requested Command was not found.")
	}
	intent, err := a.store.Get(ctx, operator.TenantID, commandID)
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command Service could not load the requested Command.")
	}
	if intent.Status != commandmodel.IntentAwaitingApproval {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "COMMAND_APPROVAL_INVALID",
			Title: "Command approval invalid", Detail: "The Command is not awaiting another approval.",
		}
	}
	if (intent.Risk == commandmodel.RiskHigh || intent.Risk == commandmodel.RiskCritical) &&
		!operator.StepUpSessionMFAVerified && !operator.StepUpTOTPVerified {
		return CommandView{}, commandStepUpError("This high-risk Command approval requires recent multi-factor authentication. Start a high-assurance login and retry the approval.")
	}

	profile, supported := commandmodel.CapabilityProfileFor(intent.Capability)
	if !supported {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
			Title: "Command forbidden", Detail: "The Command Capability is not supported.",
		}
	}
	claims, err := a.mintAndValidateCommandGrant(
		ctx, operator, commandmodel.AuthorizationCommandApprove,
		intent.SiteID, intent.DeviceID, intent.Capability, profile.Revision, intent.Risk,
	)
	if err != nil {
		return CommandView{}, err
	}
	now := a.now().UTC()
	approvalID, err := newUUID(now)
	if err != nil {
		return CommandView{}, unavailableControlError("Unable to allocate Command approval identifier.")
	}
	authorization := commandauth.Snapshot(claims)
	approved, err := a.store.Approve(ctx, commandmodel.ApproveRequest{
		TenantID:  operator.TenantID,
		CommandID: commandID,
		Approval: commandmodel.ApprovalEvidence{
			ApprovalID:         approvalID,
			ApproverID:         authorization.PrincipalID,
			Policy:             intent.ApprovalPolicy,
			PayloadHash:        intent.PayloadHash,
			CapabilityRevision: intent.CapabilityRevision,
			Risk:               intent.Risk,
			RiskRuleRevision:   intent.RiskSnapshot.RuleRevision,
			Authorization:      authorization,
			IssuedAt:           now,
			ExpiresAt:          authorization.ExpiresAt,
		},
	})
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command approval failed.")
	}
	return commandView(approved), nil
}

func (a *ControlAuthority) ReconcileOperatorCommand(
	ctx context.Context,
	operator OperatorIdentity,
	commandID string,
	outcome commandmodel.ReconciledOutcome,
) (CommandView, error) {
	if a.iam == nil {
		return CommandView{}, unavailableControlError("IAM authorization is not configured.")
	}
	if !lowerUUIDPattern.MatchString(commandID) {
		return CommandView{}, notFoundControlError("The requested Command was not found.")
	}
	if outcome != commandmodel.ReconciledApplied && outcome != commandmodel.ReconciledNotApplied {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_RECONCILIATION_REQUEST_INVALID",
			Title: "Command reconciliation invalid", Detail: "outcome must be APPLIED or NOT_APPLIED.",
		}
	}
	intent, err := a.store.Get(ctx, operator.TenantID, commandID)
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command Service could not load the requested Command.")
	}
	if intent.Status != commandmodel.IntentOutcomeUnknown {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "COMMAND_RECONCILIATION_INVALID",
			Title: "Command reconciliation invalid", Detail: "Only OUTCOME_UNKNOWN Commands can be manually reconciled.",
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(intent.Capability)
	if !supported {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
			Title: "Command forbidden", Detail: "The Command Capability is not supported.",
		}
	}
	claims, err := a.mintAndValidateCommandGrant(
		ctx, operator, commandmodel.AuthorizationCommandSubmit,
		intent.SiteID, intent.DeviceID, intent.Capability, profile.Revision, intent.Risk,
	)
	if err != nil {
		return CommandView{}, err
	}
	reconciled, err := a.store.Reconcile(ctx, commandmodel.ReconcileRequest{
		TenantID:      operator.TenantID,
		CommandID:     commandID,
		Outcome:       outcome,
		Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command reconciliation failed.")
	}
	return commandView(reconciled), nil
}

func (a *ControlAuthority) CancelOperatorCommand(
	ctx context.Context,
	operator OperatorIdentity,
	commandID string,
	reason string,
) (CommandView, error) {
	if a.iam == nil {
		return CommandView{}, unavailableControlError("IAM authorization is not configured.")
	}
	if !lowerUUIDPattern.MatchString(commandID) {
		return CommandView{}, notFoundControlError("The requested Command was not found.")
	}
	trimmedReason := strings.TrimSpace(reason)
	if len(trimmedReason) > 256 {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_CANCEL_REQUEST_INVALID",
			Title: "Command cancel invalid", Detail: "reason must be at most 256 characters.",
		}
	}
	intent, err := a.store.Get(ctx, operator.TenantID, commandID)
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command Service could not load the requested Command.")
	}
	isUnitSequence := intent.Capability == commandmodel.CapabilityStartUnitSequence || intent.Capability == commandmodel.CapabilityStopUnitSequence
	if intent.Status == commandmodel.IntentDispatching && isUnitSequence {
		profile, supported := commandmodel.CapabilityProfileFor(intent.Capability)
		if !supported {
			return CommandView{}, &ControlError{
				HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
				Title: "Command forbidden", Detail: "The Command Capability is not supported.",
			}
		}
		claims, err := a.mintAndValidateCommandGrant(
			ctx, operator, commandmodel.AuthorizationCommandSubmit,
			intent.SiteID, intent.DeviceID, intent.Capability, profile.Revision, intent.Risk,
		)
		if err != nil {
			return CommandView{}, err
		}
		abortReason := trimmedReason
		if abortReason == "" {
			abortReason = "ABORT_UNIT_SEQUENCE"
		}
		principalID := resolveControlPrincipalID(nil, claims.PrincipalID, operator.Subject)
		cancelledIDs, err := a.store.CancelPending(ctx, commandmodel.CancelPendingRequest{
			TenantID:             operator.TenantID,
			SiteID:               intent.SiteID,
			DeviceIDs:            []string{intent.DeviceID},
			PrincipalID:          principalID,
			Reason:               abortReason,
			CausationID:          intent.ID,
			AbortActiveSequences: true,
		})
		if err != nil {
			return CommandView{}, controlErrorFromStore(err, "Command cancellation failed.")
		}
		updated, err := a.store.Get(ctx, operator.TenantID, commandID)
		if err != nil {
			return CommandView{}, controlErrorFromStore(err, "Command Service could not reload the cancelled Command.")
		}
		if !containsString(cancelledIDs, commandID) && updated.Status == commandmodel.IntentDispatching {
			return CommandView{}, &ControlError{
				HTTPStatus: http.StatusConflict, Code: "COMMAND_CANCEL_INVALID",
				Title: "Command cannot be cancelled", Detail: "Only a pending, queued, or acknowledged unit-sequence Command can be cancelled or aborted.",
			}
		}
		if a.twin != nil && a.live != nil {
			if model, modelErr := a.twin.GetSiteAssetModel(ctx, operator, intent.SiteID); modelErr == nil {
				topo := resolveSiteControlTopology(model, a.now().UTC())
				if abortTarget, hasAbort := resolveAbortTargetForCommand(topo, intent.PointID, intent.DeviceID); hasAbort {
					controlState, stateErr := a.twin.GetSiteControlState(ctx, operator, intent.SiteID)
					if stateErr != nil {
						controlState = SiteControlStateView{
							TenantID:    operator.TenantID,
							SiteID:      intent.SiteID,
							ControlMode: SiteControlModeRemoteAuto,
						}
					}
					obs, _, liveErr := a.live.ReadLivePoint(ctx, operator.TenantID, intent.SiteID, abortTarget.device.ID, abortTarget.feedbackPoint.PointCode)
					precondMode := commandmodel.PreconditionRequireCurrentState
					if liveErr != nil || !livePointSafe(obs) {
						precondMode = commandmodel.PreconditionEmergencyBypass
						obs = nil
					}
					_, _ = a.dispatchShutdownTarget(ctx, operator, controlState, abortTarget, obs, precondMode, abortReason, "abort-"+intent.ID, false)
				}
			}
		}
		return commandView(updated), nil
	}
	if intent.Status != commandmodel.IntentAwaitingApproval && intent.Status != commandmodel.IntentQueued {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "COMMAND_CANCEL_INVALID",
			Title: "Command cannot be cancelled", Detail: "Only a pending or queued Command can be cancelled before dispatch begins.",
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(intent.Capability)
	if !supported {
		return CommandView{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
			Title: "Command forbidden", Detail: "The Command Capability is not supported.",
		}
	}
	claims, err := a.mintAndValidateCommandGrant(
		ctx, operator, commandmodel.AuthorizationCommandSubmit,
		intent.SiteID, intent.DeviceID, intent.Capability, profile.Revision, intent.Risk,
	)
	if err != nil {
		return CommandView{}, err
	}
	cancelled, err := a.store.Cancel(ctx, commandmodel.CancelRequest{
		TenantID:      operator.TenantID,
		CommandID:     commandID,
		Reason:        trimmedReason,
		Authorization: commandauth.Snapshot(claims),
	})
	if err != nil {
		return CommandView{}, controlErrorFromStore(err, "Command cancellation failed.")
	}
	return commandView(cancelled), nil
}

func (a *ControlAuthority) GetSiteControlState(ctx context.Context, operator OperatorIdentity, siteID string) (SiteControlStateView, error) {
	if a.twin == nil {
		return SiteControlStateView{}, unavailableControlError("Core Registry is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return SiteControlStateView{}, notFoundControlError("The requested Site was not found.")
	}
	state, err := a.twin.GetSiteControlState(ctx, operator, siteID)
	if err != nil {
		return SiteControlStateView{}, err
	}
	if state.ControlMode == SiteControlModeSafeShutdownExit {
		a.releaseSafeShutdownHoldingAuxiliariesBestEffort(ctx, operator, siteID, state)
	}
	return state, nil
}

func (a *ControlAuthority) PutSiteControlMode(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	input SiteControlModeInput,
) (SiteControlModeResponse, error) {
	if a.twin == nil || a.iam == nil {
		return SiteControlModeResponse{}, unavailableControlError("Control exit ports are not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return SiteControlModeResponse{}, notFoundControlError("The requested Site was not found.")
	}
	if !validSiteControlMode(input.ControlMode) {
		return SiteControlModeResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "controlMode must be REMOTE_AUTO, MANUAL_OVERRIDE, or SAFE_SHUTDOWN_EXIT.",
		}
	}
	reason, meta, err := a.resolveControlMutationMeta(input.Reason, input.ExpectedRevision, input.IdempotencyKey, input.Meta, func() (int64, error) {
		current, getErr := a.twin.GetSiteControlState(ctx, operator, siteID)
		if getErr != nil {
			return 0, getErr
		}
		return current.Revision, nil
	})
	if err != nil {
		return SiteControlModeResponse{}, err
	}

	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return SiteControlModeResponse{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return SiteControlModeResponse{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN",
			Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to change Site control mode.",
		}
	}
	if input.ControlMode == SiteControlModeSafeShutdownExit {
		intents, listErr := a.store.List(ctx, operator.TenantID, siteID, MaximumListedCommands)
		if listErr != nil {
			return SiteControlModeResponse{}, controlErrorFromStore(listErr, "Command Service could not verify active Site Commands.")
		}
		for _, intent := range intents {
			if intent.Status == commandmodel.IntentDispatching || intent.Status == commandmodel.IntentOutcomeUnknown {
				return SiteControlModeResponse{}, &ControlError{
					HTTPStatus: http.StatusConflict,
					Code:       "SAFE_SHUTDOWN_PRECONDITION_FAILED",
					Title:      "Safe shutdown blocked by active commands",
					Detail:     "Wait for dispatching commands to complete or reconcile unknown command outcomes before safe shutdown exit.",
				}
			}
		}
	}

	saved, err := a.twin.SaveSiteControlMode(ctx, operator, siteID, input.ControlMode, reason, meta)
	if err != nil {
		return SiteControlModeResponse{}, err
	}

	cancelledIDs := []string{}
	if saved.ControlMode != SiteControlModeRemoteAuto {
		principalID := resolveControlPrincipalID(saved.UpdatedBy, decision.PrincipalID, operator.Subject)
		cancelled, cancelErr := a.store.CancelPending(ctx, commandmodel.CancelPendingRequest{
			TenantID:             operator.TenantID,
			SiteID:               siteID,
			PrincipalID:          principalID,
			Reason:               reason,
			AbortActiveSequences: saved.ControlMode == SiteControlModeSafeShutdownExit,
		})
		if cancelErr != nil {
			return SiteControlModeResponse{}, controlErrorFromStore(cancelErr, "Command Service could not cancel pending commands after control mode change.")
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}

	return SiteControlModeResponse{
		SiteControlStateView: saved,
		CancelledCommandIDs:  cancelledIDs,
	}, nil
}

func (a *ControlAuthority) PutAssetControlLock(
	ctx context.Context,
	operator OperatorIdentity,
	assetID string,
	input AssetControlLockInput,
) (AssetControlLockResponse, error) {
	if a.twin == nil || a.iam == nil {
		return AssetControlLockResponse{}, unavailableControlError("Control lock ports are not configured.")
	}
	if !commandmodel.IsUUIDv7(assetID) {
		return AssetControlLockResponse{}, notFoundControlError("The requested Asset was not found.")
	}
	switch input.LockMode {
	case AssetControlLockUnlocked, AssetControlLockManual, AssetControlLockMaintenance:
	default:
		return AssetControlLockResponse{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control lock request invalid", Detail: "lockMode must be UNLOCKED, MANUAL_LOCK, or MAINTENANCE_LOCK.",
		}
	}

	asset, err := a.twin.GetAsset(ctx, operator, assetID)
	if err != nil {
		return AssetControlLockResponse{}, err
	}
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return AssetControlLockResponse{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, asset.SiteID) {
		return AssetControlLockResponse{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "CONTROL_EXIT_FORBIDDEN",
			Title: "Control exit forbidden", Detail: "The authenticated Principal is not allowed to lock or unlock Assets on this Site.",
		}
	}

	reason, meta, err := a.resolveControlMutationMeta(input.Reason, input.ExpectedRevision, input.IdempotencyKey, input.Meta, func() (int64, error) {
		current, getErr := a.twin.GetSiteControlState(ctx, operator, asset.SiteID)
		if getErr != nil {
			return 0, getErr
		}
		for _, item := range current.AssetLocks {
			if item.AssetID == assetID {
				return item.Revision, nil
			}
		}
		return 0, nil
	})
	if err != nil {
		return AssetControlLockResponse{}, err
	}

	saved, err := a.twin.SaveAssetControlLock(ctx, operator, assetID, asset.SiteID, input.LockMode, reason, meta)
	if err != nil {
		return AssetControlLockResponse{}, err
	}

	cancelledIDs := []string{}
	if saved.LockMode != AssetControlLockUnlocked && len(saved.BoundDeviceIDs) > 0 {
		principalID := resolveControlPrincipalID(saved.UpdatedBy, decision.PrincipalID, operator.Subject)
		cancelled, cancelErr := a.store.CancelPending(ctx, commandmodel.CancelPendingRequest{
			TenantID:             operator.TenantID,
			SiteID:               saved.SiteID,
			DeviceIDs:            saved.BoundDeviceIDs,
			PrincipalID:          principalID,
			Reason:               reason,
			CausationID:          saved.ID,
			AbortActiveSequences: false,
		})
		if cancelErr != nil {
			return AssetControlLockResponse{}, controlErrorFromStore(cancelErr, "Command Service could not cancel pending commands after Asset lock.")
		}
		if len(cancelled) > 0 {
			cancelledIDs = cancelled
		}
	}

	return AssetControlLockResponse{
		AssetControlLockView: saved,
		CancelledCommandIDs:  cancelledIDs,
	}, nil
}

func (a *ControlAuthority) mintGrantAndSubmit(
	ctx context.Context,
	operator OperatorIdentity,
	siteID, deviceID, pointID, idempotencyKey string,
	capability commandmodel.Capability,
	profile commandmodel.CapabilityProfile,
	parameters commandmodel.CommandParameters,
	verificationPointKey string,
	precondMode commandmodel.PreconditionMode,
	bypassReason string,
	obs *commandmodel.LivePointObservation,
) (SubmitResult, error) {
	riskLevel, _, ok := commandmodel.EvaluateRiskLevel(capability, parameters, liveObservationCurrentValue(obs))
	if !ok {
		riskLevel = profile.BaseRisk
	}
	claims, err := a.mintAndValidateCommandGrant(
		ctx, operator, commandmodel.AuthorizationCommandSubmit,
		siteID, deviceID, capability, profile.Revision, riskLevel,
	)
	if err != nil {
		return SubmitResult{}, err
	}
	var currentState commandmodel.CurrentStateEvidence
	if precondMode == commandmodel.PreconditionRequireCurrentState && obs != nil {
		currentState = commandmodel.CurrentStateEvidence{
			EvaluationAvailability: obs.EvaluationAvailability,
			Presence:               obs.Presence,
			Readiness:              obs.Readiness,
			Quality:                obs.Quality,
			BusinessRevision:       obs.BusinessRevision,
			CurrentValue:           obs.CurrentValue,
			ObservedAt:             obs.ObservedAt,
		}
	}
	res, err := a.store.Submit(ctx, commandmodel.SubmitRequest{
		TenantID:             operator.TenantID,
		SiteID:               siteID,
		DeviceID:             deviceID,
		PointID:              pointID,
		PrincipalID:          claims.PrincipalID,
		IdempotencyKey:       idempotencyKey,
		Capability:           capability,
		Parameters:           cloneParameters(parameters),
		VerificationPointKey: verificationPointKey,
		PreconditionMode:     precondMode,
		BypassReason:         bypassReason,
		CurrentState:         currentState,
		Authorization:        commandauth.Snapshot(claims),
	})
	if err != nil {
		return SubmitResult{}, controlErrorFromStore(err, "Command Service failed to accept the Command.")
	}
	return res, nil
}

func (a *ControlAuthority) mintAndValidateCommandGrant(
	ctx context.Context,
	operator OperatorIdentity,
	purpose commandmodel.AuthorizationPurpose,
	siteID, deviceID string,
	capability commandmodel.Capability,
	capabilityRevision string,
	risk commandmodel.RiskLevel,
) (commandauth.GrantClaims, error) {
	_, claims, err := a.iam.AuthorizeCommand(ctx, operator, purpose, siteID, deviceID, capability, capabilityRevision)
	if err != nil {
		return commandauth.GrantClaims{}, err
	}
	if a.commandGrantPublicKey != nil {
		validation := commandauth.Validation{
			Now:                a.now().UTC(),
			Issuer:             a.commandGrantIssuer,
			Presenter:          a.commandGrantPresenter,
			Audience:           a.commandGrantAudience,
			Purpose:            purpose,
			PrincipalID:        claims.PrincipalID,
			TenantID:           operator.TenantID,
			SiteID:             siteID,
			DeviceID:           deviceID,
			Capability:         capability,
			CapabilityRevision: capabilityRevision,
			Risk:               risk,
			UseChecker:         a.commandGrantUseChecker,
		}
		if commandauth.ValidateGrant(claims, validation) != nil {
			return commandauth.GrantClaims{}, &ControlError{
				HTTPStatus: http.StatusForbidden, Code: "COMMAND_GRANT_REJECTED",
				Title: "Command forbidden", Detail: "Command authorization grant failed single-use or policy validation.",
			}
		}
	} else if a.commandGrantUseChecker != nil {
		status, useErr := a.commandGrantUseChecker(claims)
		if useErr != nil || status.Replayed {
			return commandauth.GrantClaims{}, &ControlError{
				HTTPStatus: http.StatusForbidden, Code: "COMMAND_GRANT_REJECTED",
				Title: "Command forbidden", Detail: "Command authorization grant has already been consumed or revoked.",
			}
		}
	}
	return claims, nil
}

func (a *ControlAuthority) readGateInterlockPeerStates(
	ctx context.Context,
	tenantID, siteID string,
	checks []commandmodel.InterlockCheck,
) (map[string]*commandmodel.LivePointObservation, error) {
	if len(checks) == 0 {
		return nil, nil
	}
	peers := make(map[string]*commandmodel.LivePointObservation, len(checks))
	for _, check := range checks {
		key := commandmodel.PeerStateKey(check.DeviceID, check.Key)
		if _, seen := peers[key]; seen {
			continue
		}
		obs, _, err := a.live.ReadLivePoint(ctx, tenantID, siteID, check.DeviceID, check.Key)
		if err != nil {
			return nil, err
		}
		peers[key] = obs
	}
	return peers, nil
}

func resolveAssetCommandTarget(
	model SiteAssetModelDTO,
	asset AssetDTO,
	commandPointID string,
	now time.Time,
) (resolvedCommandPointTarget, *ControlError) {
	if !strings.EqualFold(asset.Status, "ACTIVE") {
		return resolvedCommandPointTarget{}, notFoundControlError("The requested Asset was not found.")
	}
	var point TelemetryPointDTO
	var pointFound bool
	for _, item := range model.TelemetryPoints {
		if item.ID == commandPointID && item.TenantID == model.TenantID && item.SiteID == model.SiteID {
			point = item
			pointFound = true
			break
		}
	}
	if !pointFound || !strings.EqualFold(point.Status, "ACTIVE") || point.PointType != "COMMAND" || !point.Writable {
		return resolvedCommandPointTarget{}, notFoundControlError("The requested Command Point was not found.")
	}

	controlsTarget := false
	for _, rel := range model.Relationships {
		if !commandRelationshipCurrent(rel, now) {
			continue
		}
		if rel.FromType == "POINT" && rel.FromID == point.ID && rel.Role == "CONTROLS" &&
			((rel.ToType == "ASSET" && rel.ToID == asset.ID) || (rel.ToType == "SITE" && rel.ToID == model.SiteID)) {
			controlsTarget = true
			break
		}
	}
	if !controlsTarget {
		return resolvedCommandPointTarget{}, notFoundControlError("The Command Point is not bound to the requested Asset.")
	}

	var device DeviceDTO
	var deviceFound bool
	for _, item := range model.Devices {
		if item.ID == point.ReportingDeviceID && strings.EqualFold(item.Status, "ACTIVE") && item.TenantID == model.TenantID && item.SiteID == model.SiteID {
			device = item
			deviceFound = true
			break
		}
	}
	if !deviceFound {
		return resolvedCommandPointTarget{}, unavailableControlError("The Command Device is unavailable.")
	}

	capabilityName, _ := point.SourceMetadata["capability"].(string)
	capability := commandmodel.Capability(strings.TrimSpace(capabilityName))
	profile, supported := commandmodel.CapabilityProfileFor(capability)
	declaredRevision, _ := point.SourceMetadata["capabilityRevision"].(string)
	feedbackPointKey, _ := point.SourceMetadata["feedbackPointKey"].(string)
	feedbackPointKey = strings.TrimSpace(feedbackPointKey)
	if !supported || strings.TrimSpace(declaredRevision) != profile.Revision || feedbackPointKey == "" {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "COMMAND_UNAVAILABLE",
			Title: "Command unavailable", Detail: "The COMMAND Point does not declare a supported authoritative capability contract.", Retryable: true,
		}
	}

	var feedbackPoint TelemetryPointDTO
	var feedbackFound bool
	for _, candidate := range model.TelemetryPoints {
		if candidate.ReportingDeviceID == device.ID && candidate.PointCode == feedbackPointKey && strings.EqualFold(candidate.Status, "ACTIVE") {
			feedbackPoint = candidate
			feedbackFound = true
			break
		}
	}
	if !feedbackFound {
		return resolvedCommandPointTarget{}, unavailableControlError("The Command feedback Point is unavailable.")
	}

	interlocks, interlockErr := parsePointInterlockChecks(point.SourceMetadata, model)
	if interlockErr != nil {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Command point interlock configuration is invalid.",
		}
	}
	actuationMode, _ := point.SourceMetadata["actuationMode"].(string)
	return resolvedCommandPointTarget{
		asset:         asset,
		device:        device,
		point:         point,
		feedbackPoint: feedbackPoint,
		capability:    capability,
		profile:       profile,
		actuationMode: strings.ToUpper(strings.TrimSpace(actuationMode)),
		interlocks:    interlocks,
	}, nil
}

func parsePointInterlockChecks(sourceMetadata map[string]any, model SiteAssetModelDTO) ([]commandmodel.InterlockCheck, error) {
	if len(sourceMetadata) == 0 {
		return nil, nil
	}
	raw, present := sourceMetadata["interlockChecks"]
	if !present || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var parsed []commandmodel.InterlockCheck
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return nil, nil
	}

	activeDevices := make(map[string]struct{}, len(model.Devices))
	for _, d := range model.Devices {
		if strings.EqualFold(d.Status, "ACTIVE") && d.TenantID == model.TenantID && d.SiteID == model.SiteID {
			activeDevices[d.ID] = struct{}{}
		}
	}
	activeFeedbackPoints := make(map[string]struct{}, len(model.TelemetryPoints))
	for _, p := range model.TelemetryPoints {
		if strings.EqualFold(p.Status, "ACTIVE") && p.TenantID == model.TenantID && p.SiteID == model.SiteID &&
			(p.PointType == "STATE" || p.PointType == "TELEMETRY" || p.PointType == "SETTING") {
			activeFeedbackPoints[p.ReportingDeviceID+"\x00"+p.PointCode] = struct{}{}
		}
	}

	checks := make([]commandmodel.InterlockCheck, 0, len(parsed))
	for _, c := range parsed {
		devID := strings.TrimSpace(c.DeviceID)
		key := strings.TrimSpace(c.Key)
		expected := strings.TrimSpace(c.Expected)
		if devID == "" || key == "" || expected == "" {
			return nil, errors.New("interlock check requires deviceId, key, and expected")
		}
		if _, ok := activeDevices[devID]; !ok {
			return nil, errors.New("interlock peer device is not active in site")
		}
		if _, ok := activeFeedbackPoints[devID+"\x00"+key]; !ok {
			return nil, errors.New("interlock peer feedback point is not active on peer device")
		}
		checks = append(checks, commandmodel.InterlockCheck{
			DeviceID: devID,
			Key:      key,
			Expected: expected,
			Reason:   strings.TrimSpace(c.Reason),
		})
	}
	return checks, nil
}

func (a *ControlAuthority) resolveControlMutationMeta(
	rawReason string,
	expectedRevision *int64,
	headerIdempotencyKey string,
	providedMeta *ControlMutationMeta,
	fetchRevision func() (int64, error),
) (string, ControlMutationMeta, error) {
	reason := strings.TrimSpace(rawReason)
	if providedMeta != nil {
		if reason == "" {
			reason = strings.TrimSpace(providedMeta.Reason)
		}
		metaReason := strings.TrimSpace(providedMeta.Reason)
		if metaReason == "" {
			metaReason = reason
		}
		if reason == "" || utf8.RuneCountInString(reason) > 1000 || utf8.RuneCountInString(metaReason) > 1000 ||
			!controlWriteKeyPattern.MatchString(providedMeta.IdempotencyKey) || providedMeta.ExpectedRevision < 0 {
			return "", ControlMutationMeta{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
				Title: "Control exit request invalid", Detail: "A valid reason, non-negative expectedRevision, and valid idempotencyKey are required.",
			}
		}
		return reason, ControlMutationMeta{
			ExpectedRevision: providedMeta.ExpectedRevision,
			IdempotencyKey:   providedMeta.IdempotencyKey,
			Reason:           metaReason,
		}, nil
	}

	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return "", ControlMutationMeta{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "A non-empty reason up to 1000 characters is required.",
		}
	}

	idem := strings.TrimSpace(headerIdempotencyKey)
	if idem != "" && !controlWriteKeyPattern.MatchString(idem) {
		return "", ControlMutationMeta{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Control exit request invalid", Detail: "The Idempotency-Key header format is invalid.",
		}
	}
	if idem == "" {
		token, err := newUUID(a.now().UTC())
		if err != nil {
			return "", ControlMutationMeta{}, unavailableControlError("Unable to allocate control mutation idempotency key.")
		}
		idem = "ctrl-" + token
	}

	var revision int64
	if expectedRevision != nil {
		if *expectedRevision < 0 {
			return "", ControlMutationMeta{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
				Title: "Control exit request invalid", Detail: "expectedRevision must be non-negative.",
			}
		}
		revision = *expectedRevision
	} else {
		rev, err := fetchRevision()
		if err != nil {
			return "", ControlMutationMeta{}, err
		}
		revision = rev
	}
	return reason, ControlMutationMeta{
		ExpectedRevision: revision,
		IdempotencyKey:   idem,
		Reason:           reason,
	}, nil
}

func toGateLocksAndWindow(locks []AssetControlLockView, mw *ActiveMaintenanceWindowView) ([]commandmodel.ControlLockSnapshot, *commandmodel.MaintenanceWindowSnapshot) {
	gateLocks := make([]commandmodel.ControlLockSnapshot, 0, len(locks))
	for _, item := range locks {
		gateLocks = append(gateLocks, commandmodel.ControlLockSnapshot{
			AssetID:        item.AssetID,
			LockMode:       commandmodel.AssetControlLockMode(item.LockMode),
			BoundDeviceIDs: item.BoundDeviceIDs,
		})
	}
	var gateWindow *commandmodel.MaintenanceWindowSnapshot
	if mw != nil {
		gateWindow = &commandmodel.MaintenanceWindowSnapshot{
			ID:        mw.ID,
			Title:     mw.Title,
			StartDate: mw.StartDate,
			EndDate:   mw.EndDate,
			Reason:    mw.Reason,
		}
	}
	return gateLocks, gateWindow
}

func commandRelationshipCurrent(rel AssetRelationshipDTO, now time.Time) bool {
	if !strings.EqualFold(rel.Status, "ACTIVE") {
		return false
	}
	if rel.ValidFrom != "" {
		if validFrom, err := time.Parse(time.RFC3339Nano, rel.ValidFrom); err == nil && now.Before(validFrom) {
			return false
		}
	}
	if rel.ValidTo != nil && *rel.ValidTo != "" {
		if validTo, err := time.Parse(time.RFC3339Nano, *rel.ValidTo); err == nil && !now.Before(validTo) {
			return false
		}
	}
	return true
}

func resolveControlPrincipalID(updatedBy *string, fallbackPrincipal, subject string) string {
	if updatedBy != nil && strings.TrimSpace(*updatedBy) != "" {
		return strings.TrimSpace(*updatedBy)
	}
	if strings.TrimSpace(fallbackPrincipal) != "" {
		return strings.TrimSpace(fallbackPrincipal)
	}
	return strings.TrimSpace(subject)
}

func liveObservationCurrentValue(obs *commandmodel.LivePointObservation) *float64 {
	if obs == nil {
		return nil
	}
	return obs.CurrentValue
}

func unavailableControlError(detail string) *ControlError {
	return &ControlError{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       "COMMAND_UNAVAILABLE",
		Title:      "Command unavailable",
		Detail:     detail,
		Retryable:  true,
	}
}

func notFoundControlError(detail string) *ControlError {
	return &ControlError{
		HTTPStatus: http.StatusNotFound,
		Code:       "RESOURCE_NOT_FOUND",
		Title:      "Resource not found",
		Detail:     detail,
	}
}

func commandStepUpError(detail string) *ControlError {
	return &ControlError{
		HTTPStatus: http.StatusPreconditionRequired,
		Code:       "STEP_UP_REQUIRED",
		Title:      "Security confirmation required",
		Detail:     detail,
	}
}
