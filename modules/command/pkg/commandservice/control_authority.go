package commandservice

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	dispatchSafetyStateKey   = "run_state"
	maxPreDispatchClaimScans = 3
)

var (
	controlWriteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	lowerUUIDPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type DevicePriorityLocker interface {
	AcquireDevicePriorityLock(ctx context.Context, lock commandmodel.DevicePriorityLock) (commandmodel.DevicePriorityLock, error)
	ReleaseDevicePriorityLock(ctx context.Context, tenantID, deviceID string) error
	GetDevicePriorityLock(ctx context.Context, tenantID, deviceID string) (*commandmodel.DevicePriorityLock, error)
	ListDevicePriorityLocks(ctx context.Context, tenantID, siteID string) ([]commandmodel.DevicePriorityLock, error)
}

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
	failureQueue           controlFailureQueue
	now                    func() time.Time
	dispatchCursor         atomic.Uint64
	verificationCursor     atomic.Uint64
}

var errTransientSafetyEvaluation = fmt.Errorf("%w: transient safety evaluation unavailable", ErrNoDispatchAvailable)

// Close drains queued control failure notifications before returning.
func (a *ControlAuthority) Close() {
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlFailureCloseTimeout)
	defer cancel()
	_ = a.failureQueue.Close(ctx)
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
			return commandmodel.DispatchEnvelope{}, errTransientSafetyEvaluation
		}

		intent, err := a.store.Get(ctx, envelope.TenantID, envelope.CommandID)
		if err != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, errTransientSafetyEvaluation
		}

		dctx, err := a.twin.GetDispatchContext(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, envelope.PointID)
		if err != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, errTransientSafetyEvaluation
		}

		decision, rejection, evidenceID, evalErr := a.evaluatePreDispatch(ctx, envelope, intent, dctx)
		if evalErr != nil {
			_ = a.runtime.ResolveDispatch(ctx, envelope, commandmodel.ConnectorResult{
				Phase:       commandmodel.ConnectorPreSendRejected,
				FailureCode: "DISPATCH_SAFETY_EVALUATION_FAILED",
			})
			return commandmodel.DispatchEnvelope{}, errTransientSafetyEvaluation
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
			if commandmodel.TerminalPreSendRejection(rejection.Code) ||
				(commandmodel.NormalizeDeliveryMode(intent.DeliveryMode) == commandmodel.DeliveryModeQueueIfOffline &&
					commandmodel.IsDeferrableOfflinePreSendCode(rejection.Code)) {
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

func (a *ControlAuthority) WakeDeferredDispatches(ctx context.Context, tenantID string, deviceIDs []string) (int, error) {
	if waker, ok := a.runtime.(DeferredDispatchWaker); ok {
		return waker.WakeDeferredDispatches(ctx, tenantID, deviceIDs)
	}
	if waker, ok := a.store.(DeferredDispatchWaker); ok {
		return waker.WakeDeferredDispatches(ctx, tenantID, deviceIDs)
	}
	return 0, nil
}

// ClaimAnyDispatch scans tenants with due dispatchable work via DueTenantLister,
// enforcing the full 3-phase Twin + Live pre-dispatch safety gate through ClaimDispatch.
func (a *ControlAuthority) ClaimAnyDispatch(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.DispatchEnvelope, error) {
	if a == nil || a.runtime == nil {
		return commandmodel.DispatchEnvelope{}, errors.New("control authority is not configured")
	}
	if strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > 2*time.Minute {
		return commandmodel.DispatchEnvelope{}, ErrInvalidRequest
	}
	lister, ok := a.runtime.(DueTenantLister)
	if !ok {
		return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
	}
	var subscribe func() <-chan struct{}
	if sub, hasSub := a.runtime.(WorkSignalSubscriber); hasSub {
		subscribe = sub.SubscribeDispatchWork
	}
	nowFn := a.now
	if nowFn == nil {
		nowFn = time.Now
	}
	return claimAnyWork(
		ctx,
		waitDuration,
		subscribe,
		func(scanCtx context.Context) ([]string, error) {
			return lister.DueDispatchTenants(scanCtx, nowFn().UTC())
		},
		&a.dispatchCursor,
		func(claimCtx context.Context, tenantID string) (commandmodel.DispatchEnvelope, error) {
			return a.ClaimDispatch(claimCtx, tenantID, leaseOwner, leaseFor)
		},
		ErrNoDispatchAvailable,
	)
}

// ClaimAnyVerification scans tenants with due verification work via DueTenantLister
// and delegates verification claim to ClaimVerification.
func (a *ControlAuthority) ClaimAnyVerification(ctx context.Context, leaseOwner string, leaseFor, waitDuration time.Duration) (commandmodel.VerificationEnvelope, error) {
	if a == nil || a.runtime == nil {
		return commandmodel.VerificationEnvelope{}, errors.New("control authority is not configured")
	}
	if strings.TrimSpace(leaseOwner) == "" || leaseFor < time.Second || leaseFor > time.Minute {
		return commandmodel.VerificationEnvelope{}, ErrInvalidRequest
	}
	lister, ok := a.runtime.(DueTenantLister)
	if !ok {
		return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
	}
	var subscribe func() <-chan struct{}
	if sub, hasSub := a.runtime.(WorkSignalSubscriber); hasSub {
		subscribe = sub.SubscribeVerificationWork
	}
	nowFn := a.now
	if nowFn == nil {
		nowFn = time.Now
	}
	return claimAnyWork(
		ctx,
		waitDuration,
		subscribe,
		func(scanCtx context.Context) ([]string, error) {
			return lister.DueVerificationTenants(scanCtx, nowFn().UTC())
		},
		&a.verificationCursor,
		func(claimCtx context.Context, tenantID string) (commandmodel.VerificationEnvelope, error) {
			return a.ClaimVerification(claimCtx, tenantID, leaseOwner, leaseFor)
		},
		ErrVerificationNotAvailable,
	)
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
		((intent.PreconditionMode == commandmodel.PreconditionEmergencyBypass &&
			commandmodel.AllowsEmergencyPreconditionBypass(intent.Capability, intent.PriorityTier, intent.CommandSource, intent.IdempotencyKey, intent.BypassReason)) ||
			(isStopCap && (actuation == "PULSE" || actuation == "ABORT_UNIT_SEQUENCE")))

	feedbackKey := strings.TrimSpace(dctx.FeedbackPointKey)
	if feedbackKey == "" {
		feedbackKey = dispatchSafetyStateKey
	}

	var targetObs *commandmodel.LivePointObservation
	var evidenceID string
	if !isEmergencyDo {
		obs, evID, err := a.live.ReadLivePoint(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, feedbackKey)
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
	_, hardProtected := edgecontrol.ClassifyHardRealtimeProtection(intent.PointID, string(intent.Capability), nil)
	if strings.EqualFold(strings.TrimSpace(dctx.SafetyBoundary), string(edgecontrol.SafetyBoundaryHardRealtime)) {
		hardProtected = true
	}
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
		HardRealtimeProtected:   hardProtected,
		IdempotencyKey:          intent.IdempotencyKey,
		PriorityTier:            intent.PriorityTier,
		CommandSource:           intent.CommandSource,
		DeliveryMode:            intent.DeliveryMode,
		TTLSeconds:              intent.TTLSeconds,
		ExpiresAt:               intent.ExpiresAt,
		Now:                     a.now().UTC(),
		TargetState:             targetObs,
		Interlocks:              dctx.Interlocks,
		PeerStates:              peerObs,
	})
	if rejection != nil {
		return decision, rejection, evidenceID, nil
	}
	if !isEmergencyDo && a.live != nil {
		if diagRejection := a.evaluatePreDispatchDiagnostics(ctx, envelope, intent, isStopCap); diagRejection != nil {
			return decision, diagRejection, evidenceID, nil
		}
	}
	return decision, nil, evidenceID, nil
}

func (a *ControlAuthority) evaluatePreDispatchDiagnostics(
	ctx context.Context,
	envelope commandmodel.DispatchEnvelope,
	intent commandmodel.CommandIntent,
	isStopCap bool,
) *commandmodel.GateRejection {
	if modeObs, _, err := a.live.ReadLivePoint(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, "local_remote_mode"); err == nil &&
		isUsableDiagnosticObservation(modeObs) && isLocalManualModeValue(modeObs.ReportedValue) {
		return &commandmodel.GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "DISPATCH_SAFETY_LOCAL_MANUAL_MODE",
			Title:      "Device in local manual mode",
			Detail:     "Target device local_remote_mode indicates local manual control.",
		}
	}
	if isStopCap || intent.Capability == commandmodel.CapabilityResetFault {
		return nil
	}
	if faultObs, _, err := a.live.ReadLivePoint(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, "fault_code"); err == nil &&
		isUsableDiagnosticObservation(faultObs) && isActiveFaultValue(faultObs.ReportedValue) {
		return &commandmodel.GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "DISPATCH_SAFETY_DEVICE_FAULT",
			Title:      "Device fault active",
			Detail:     "Target device reported an active fault_code.",
		}
	}
	if alarmObs, _, err := a.live.ReadLivePoint(ctx, envelope.TenantID, envelope.SiteID, envelope.DeviceID, "alarm_active"); err == nil &&
		isUsableDiagnosticObservation(alarmObs) && isActiveAlarmValue(alarmObs.ReportedValue) {
		return &commandmodel.GateRejection{
			HTTPStatus: http.StatusConflict,
			Code:       "DISPATCH_SAFETY_DEVICE_FAULT",
			Title:      "Device alarm active",
			Detail:     "Target device reported alarm_active.",
		}
	}
	return nil
}

func isUsableDiagnosticObservation(obs *commandmodel.LivePointObservation) bool {
	if obs == nil || obs.Freshness != "FRESH" || obs.Quality != "GOOD" {
		return false
	}
	if obs.EvaluationAvailability != "" && obs.EvaluationAvailability != "AVAILABLE" {
		return false
	}
	if obs.Presence != "" && obs.Presence != "ONLINE" {
		return false
	}
	if obs.Readiness != "" && obs.Readiness != "CURRENT" {
		return false
	}
	return true
}

func isLocalManualModeValue(val commandmodel.ScalarValue) bool {
	return commandmodel.DiagnosticPointIndicatesLocalManual("local_remote_mode", val.Boolean, val.Number, derefOptionalString(val.Text))
}

func isActiveFaultValue(val commandmodel.ScalarValue) bool {
	return commandmodel.DiagnosticValueIndicatesActiveFault(val.Boolean, val.Number, derefOptionalString(val.Text))
}

func isActiveAlarmValue(val commandmodel.ScalarValue) bool {
	return commandmodel.DiagnosticValueIndicatesActiveFault(val.Boolean, val.Number, derefOptionalString(val.Text))
}

func derefOptionalString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
	now := a.now().UTC()
	deliveryMode, ttlSeconds, expiresAt, deliveryErr := commandmodel.ValidateDeliveryAndTTL(target.capability, input.DeliveryMode, input.TTLSeconds, nil, now)
	if deliveryErr != nil {
		return SubmitOperatorCommandOutput{}, controlErrorFromStore(deliveryErr, deliveryErr.Error())
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
	_, hardProtected := edgecontrol.ClassifyHardRealtimeProtection(target.point.PointCode, string(target.capability), target.point.SourceMetadata)
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
		IdempotencyKey:          strings.TrimSpace(input.IdempotencyKey),
		PriorityTier:            commandmodel.PriorityManualOverride,
		CommandSource:           commandmodel.SourceOperator,
		DeliveryMode:            deliveryMode,
		TTLSeconds:              ttlSeconds,
		ExpiresAt:               expiresAt,
		Now:                     now,
		PreconditionMode:        commandmodel.PreconditionRequireCurrentState,
		HardRealtimeProtected:   hardProtected,
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

	traceID := commandmodel.NormalizeTraceID(input.TraceID)
	if traceID == "" {
		traceID = commandmodel.NormalizeTraceID(observability.TraceID(ctx))
	}
	submitted, err := a.mintGrantAndSubmit(
		ctx, operator, asset.SiteID, target.device.ID, target.point.ID,
		strings.TrimSpace(input.IdempotencyKey), traceID, target.capability, target.profile,
		input.Parameters, target.feedbackPoint.PointCode,
		commandmodel.PreconditionRequireCurrentState, "", "", "",
		deliveryMode, ttlSeconds, expiresAt, nil, nil, targetObs,
	)
	if err != nil {
		return SubmitOperatorCommandOutput{}, err
	}
	if !submitted.Replayed {
		if locker, ok := a.store.(DevicePriorityLocker); ok {
			lockedBy := strings.TrimSpace(operator.Subject)
			if lockedBy == "" {
				lockedBy = submitted.Intent.PrincipalID
			}
			if _, lockErr := locker.AcquireDevicePriorityLock(ctx, commandmodel.DevicePriorityLock{
				TenantID:       operator.TenantID,
				SiteID:         asset.SiteID,
				DeviceID:       target.device.ID,
				LockedPriority: commandmodel.PriorityManualOverride,
				CommandSource:  commandmodel.SourceOperator,
				LockedBy:       lockedBy,
				LockedUntil:    a.now().UTC().Add(2 * time.Hour),
				Reason:         "operator manual command cooldown",
			}); lockErr != nil && !errors.Is(lockErr, ErrDevicePriorityLocked) {
				slog.WarnContext(ctx, "operator manual cooldown lock skipped",
					"tenant_id", operator.TenantID,
					"device_id", target.device.ID,
					"error", "[REDACTED]",
				)
			}
		}
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

func (a *ControlAuthority) populateDevicePriorityLocks(
	ctx context.Context,
	tenantID, siteID string,
	state *SiteControlStateView,
) error {
	if state == nil {
		return nil
	}
	locksView := make([]DevicePriorityLockView, 0)
	if locker, ok := a.store.(DevicePriorityLocker); ok {
		locks, lockErr := locker.ListDevicePriorityLocks(ctx, tenantID, siteID)
		if lockErr != nil {
			return controlErrorFromStore(lockErr, "Command Service could not load active Device priority locks.")
		}
		for _, lock := range locks {
			locksView = append(locksView, devicePriorityLockView(lock))
		}
	}
	state.DevicePriorityLocks = locksView
	return nil
}

func (a *ControlAuthority) SubmitProtectionRuleCommand(
	ctx context.Context,
	input ProtectionRuleCommandInput,
) (SubmitResult, error) {
	if a.twin == nil || a.live == nil || a.store == nil {
		return SubmitResult{}, unavailableControlError("Command Service control ports are not configured.")
	}
	idemKey := strings.TrimSpace(input.IdempotencyKey)
	if !commandmodel.IsUUIDv7(input.TenantID) || !commandmodel.IsUUIDv7(input.SiteID) || !commandmodel.IsUUIDv7(input.DeviceID) || !controlWriteKeyPattern.MatchString(idemKey) {
		return SubmitResult{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Protection command requires valid tenantId, siteId, deviceId, and idempotencyKey.",
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(input.Capability)
	if !supported || !commandmodel.ParametersValid(input.Capability, input.Parameters) {
		return SubmitResult{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Protection command capability or parameters are invalid.",
		}
	}
	dctx, err := a.twin.ResolveProtectionDispatchContext(ctx, input.TenantID, input.SiteID, input.DeviceID, strings.TrimSpace(input.TargetPointCode), input.Capability)
	if err != nil {
		return SubmitResult{}, controlErrorFromStore(err, "Core Registry could not resolve protection command dispatch context.")
	}
	feedbackKey := strings.TrimSpace(dctx.FeedbackPointKey)
	if !commandmodel.IsUUIDv7(dctx.PointID) || feedbackKey == "" {
		return SubmitResult{}, unavailableControlError("Core Registry returned an incomplete protection command dispatch context.")
	}
	targetObs, _, err := a.live.ReadLivePoint(ctx, input.TenantID, input.SiteID, input.DeviceID, feedbackKey)
	if err != nil {
		return SubmitResult{}, err
	}
	peerObs, err := a.readGateInterlockPeerStates(ctx, input.TenantID, input.SiteID, dctx.Interlocks)
	if err != nil {
		return SubmitResult{}, err
	}

	locks, mw := toGateLocksAndWindow(dctx.AssetLocks, dctx.ActiveMaintenanceWindow)
	_, hardProtected := edgecontrol.ClassifyHardRealtimeProtection(input.TargetPointCode, string(input.Capability), nil)
	if strings.EqualFold(strings.TrimSpace(dctx.SafetyBoundary), string(edgecontrol.SafetyBoundaryHardRealtime)) {
		hardProtected = true
	}
	decision, rejection := commandmodel.EvaluateControlGate(commandmodel.GateInput{
		Phase:                   commandmodel.GatePhaseSubmit,
		AssetID:                 dctx.AssetID,
		DeviceID:                input.DeviceID,
		Capability:              input.Capability,
		CapabilityRevision:      profile.Revision,
		ParameterKey:            profile.ParameterKey,
		ActuationMode:           dctx.ActuationMode,
		AllowEmergencyDoRelease: false,
		Parameters:              input.Parameters,
		PriorityTier:            commandmodel.PriorityProtectionRule,
		CommandSource:           commandmodel.SourceProtectionRule,
		SiteMode:                commandmodel.SiteControlMode(dctx.ControlMode),
		AssetLocks:              locks,
		ActiveMaintenanceWindow: mw,
		IdempotencyKey:          idemKey,
		PreconditionMode:        commandmodel.PreconditionRequireCurrentState,
		HardRealtimeProtected:   hardProtected,
		TargetState:             targetObs,
		Interlocks:              dctx.Interlocks,
		PeerStates:              peerObs,
	})
	if rejection != nil {
		return SubmitResult{}, problemFromGateRejection(rejection)
	}
	if decision.Risk == commandmodel.RiskCritical {
		return SubmitResult{}, &ControlError{
			HTTPStatus: http.StatusForbidden,
			Code:       "COMMAND_AUTHORIZATION_DENIED",
			Title:      "Command forbidden",
			Detail:     "Critical-risk commands cannot be executed automatically by protection rules.",
		}
	}

	now := a.now().UTC()
	grantID := strings.TrimSpace(input.OutboxID)
	if grantID == "" {
		grantID = idemKey
	}
	principalSuffix := strings.TrimSpace(input.AssignmentID)
	if principalSuffix == "" {
		principalSuffix = grantID
	}
	principalID := "alarm-protection-rule:" + principalSuffix
	auth := commandmodel.AuthorizationSnapshot{
		GrantID:            "protection-rule:" + grantID,
		PolicyRevision:     "alarm-protection-rule:v1",
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		PrincipalID:        principalID,
		TenantID:           input.TenantID,
		SiteID:             input.SiteID,
		DeviceID:           input.DeviceID,
		Capability:         input.Capability,
		MaximumRisk:        commandmodel.RiskHigh,
		CapabilityRevision: profile.Revision,
		WorkloadSPIFFE:     "spiffe://hvac.local/alarm-service",
		IssuedAt:           now,
		ExpiresAt:          now.Add(25 * time.Second),
	}
	var currentState commandmodel.CurrentStateEvidence
	if targetObs != nil {
		currentState = commandmodel.CurrentStateEvidence{
			EvaluationAvailability: targetObs.EvaluationAvailability,
			Presence:               targetObs.Presence,
			Readiness:              targetObs.Readiness,
			Quality:                targetObs.Quality,
			BusinessRevision:       targetObs.BusinessRevision,
			CurrentValue:           targetObs.CurrentValue,
			ObservedAt:             targetObs.ObservedAt,
		}
	}
	normalizedTraceID := commandmodel.NormalizeTraceID(input.TraceID)
	if normalizedTraceID == "" {
		normalizedTraceID = commandmodel.NormalizeTraceID(observability.TraceID(ctx))
	}
	res, err := a.store.Submit(ctx, commandmodel.SubmitRequest{
		TenantID:             input.TenantID,
		SiteID:               input.SiteID,
		DeviceID:             input.DeviceID,
		PointID:              dctx.PointID,
		PrincipalID:          principalID,
		IdempotencyKey:       idemKey,
		TraceID:              normalizedTraceID,
		Capability:           input.Capability,
		Parameters:           cloneParameters(input.Parameters),
		VerificationPointKey: feedbackKey,
		PreconditionMode:     commandmodel.PreconditionRequireCurrentState,
		PriorityTier:         commandmodel.PriorityProtectionRule,
		CommandSource:        commandmodel.SourceProtectionRule,
		CurrentState:         currentState,
		Authorization:        auth,
	})
	if err != nil {
		return SubmitResult{}, controlErrorFromStore(err, "Command Service failed to accept the protection rule Command.")
	}
	return res, nil
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
	if err := a.populateDevicePriorityLocks(ctx, operator.TenantID, siteID, &state); err != nil {
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

	if err := a.populateDevicePriorityLocks(ctx, operator.TenantID, siteID, &saved); err != nil {
		return SiteControlModeResponse{}, err
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

func (a *ControlAuthority) PutDevicePriorityLock(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	deviceID string,
	input DevicePriorityLockInput,
) (DevicePriorityLockView, error) {
	if a.twin == nil || a.iam == nil {
		return DevicePriorityLockView{}, unavailableControlError("Control lock ports are not configured.")
	}
	locker, ok := a.store.(DevicePriorityLocker)
	if !ok {
		return DevicePriorityLockView{}, unavailableControlError("Command Service priority lock store is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return DevicePriorityLockView{}, notFoundControlError("The requested Site was not found.")
	}
	if !commandmodel.IsUUIDv7(deviceID) {
		return DevicePriorityLockView{}, notFoundControlError("The requested Device was not found.")
	}
	priority := commandmodel.PriorityTier(input.LockedPriority)
	switch priority {
	case commandmodel.PriorityManualOverride, commandmodel.PriorityProtectionRule, commandmodel.PrioritySafetyInterlock:
	default:
		return DevicePriorityLockView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Device priority lock invalid", Detail: "lockedPriority must be MANUAL_OVERRIDE, PROTECTION_RULE, or SAFETY_INTERLOCK.",
		}
	}
	durationMinutes := 120
	if input.DurationMinutes != nil {
		durationMinutes = *input.DurationMinutes
	}
	if durationMinutes < 5 || durationMinutes > 1440 {
		return DevicePriorityLockView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CONTROL_EXIT_REQUEST_INVALID",
			Title: "Device priority lock invalid", Detail: "durationMinutes must be between 5 and 1440 minutes.",
		}
	}
	reason, ctrlErr := validatePriorityLockReason(input.Reason, "Device priority lock invalid")
	if ctrlErr != nil {
		return DevicePriorityLockView{}, ctrlErr
	}
	decision, err := a.authorizeSiteScheduleManage(ctx, operator, siteID, "The authenticated Principal is not allowed to lock Devices on this Site.")
	if err != nil {
		return DevicePriorityLockView{}, err
	}
	if err := a.verifySiteDevice(ctx, operator, siteID, deviceID); err != nil {
		return DevicePriorityLockView{}, err
	}
	lockedBy := resolveControlPrincipalID(nil, decision.PrincipalID, operator.Subject)
	lockedUntil := a.now().UTC().Add(time.Duration(durationMinutes) * time.Minute)
	stored, err := locker.AcquireDevicePriorityLock(ctx, commandmodel.DevicePriorityLock{
		TenantID:       operator.TenantID,
		SiteID:         siteID,
		DeviceID:       deviceID,
		LockedPriority: priority,
		CommandSource:  commandmodel.DefaultCommandSourceForPriority(priority),
		LockedBy:       lockedBy,
		LockedUntil:    lockedUntil,
		Reason:         reason,
	})
	if err != nil {
		return DevicePriorityLockView{}, controlErrorFromStore(err, "Command Service could not acquire the Device priority lock.")
	}
	return devicePriorityLockView(stored), nil
}

func (a *ControlAuthority) DeleteDevicePriorityLock(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	deviceID string,
	rawReason string,
) (DevicePriorityLockReleaseResponse, error) {
	if a.twin == nil || a.iam == nil {
		return DevicePriorityLockReleaseResponse{}, unavailableControlError("Control lock ports are not configured.")
	}
	locker, ok := a.store.(DevicePriorityLocker)
	if !ok {
		return DevicePriorityLockReleaseResponse{}, unavailableControlError("Command Service priority lock store is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return DevicePriorityLockReleaseResponse{}, notFoundControlError("The requested Site was not found.")
	}
	if !commandmodel.IsUUIDv7(deviceID) {
		return DevicePriorityLockReleaseResponse{}, notFoundControlError("The requested Device was not found.")
	}
	if _, ctrlErr := validatePriorityLockReason(rawReason, "Device priority lock release invalid"); ctrlErr != nil {
		return DevicePriorityLockReleaseResponse{}, ctrlErr
	}
	if _, err := a.authorizeSiteScheduleManage(ctx, operator, siteID, "The authenticated Principal is not allowed to release Device priority locks on this Site."); err != nil {
		return DevicePriorityLockReleaseResponse{}, err
	}
	if err := a.verifySiteDevice(ctx, operator, siteID, deviceID); err != nil {
		return DevicePriorityLockReleaseResponse{}, err
	}
	existing, err := locker.GetDevicePriorityLock(ctx, operator.TenantID, deviceID)
	if err != nil {
		return DevicePriorityLockReleaseResponse{}, controlErrorFromStore(err, "Command Service could not inspect the Device priority lock.")
	}
	released := existing != nil
	if err := locker.ReleaseDevicePriorityLock(ctx, operator.TenantID, deviceID); err != nil {
		return DevicePriorityLockReleaseResponse{}, controlErrorFromStore(err, "Command Service could not release the Device priority lock.")
	}
	return DevicePriorityLockReleaseResponse{
		Released: released,
		DeviceID: deviceID,
	}, nil
}

func (a *ControlAuthority) ListControlAlarmDeadLetters(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	limit int,
) ([]ControlAlarmDeadLetterView, error) {
	if a.iam == nil {
		return nil, unavailableControlError("IAM authorization is not configured.")
	}
	dlq, ok := a.store.(ControlAlarmDeadLetterStore)
	if !ok {
		return nil, unavailableControlError("Command Service control alarm dead-letter store is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return nil, notFoundControlError("The requested Site was not found.")
	}
	if _, err := a.authorizeSiteScheduleManage(ctx, operator, siteID, "The authenticated Principal is not allowed to inspect Control Alarm dead letters on this Site."); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaximumControlAlarmDeadLetters {
		limit = MaximumControlAlarmDeadLetters
	}
	views, err := dlq.ListControlAlarmDeadLetters(ctx, operator.TenantID, siteID, limit)
	if err != nil {
		return nil, controlErrorFromStore(err, "Command Service could not load Control Alarm dead letters.")
	}
	if views == nil {
		return []ControlAlarmDeadLetterView{}, nil
	}
	return views, nil
}

func (a *ControlAuthority) ReplayControlAlarmDeadLetter(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	outboxID string,
) (ControlAlarmDeadLetterReplayResponse, error) {
	if a.iam == nil {
		return ControlAlarmDeadLetterReplayResponse{}, unavailableControlError("IAM authorization is not configured.")
	}
	dlq, ok := a.store.(ControlAlarmDeadLetterStore)
	if !ok {
		return ControlAlarmDeadLetterReplayResponse{}, unavailableControlError("Command Service control alarm dead-letter store is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) {
		return ControlAlarmDeadLetterReplayResponse{}, notFoundControlError("The requested Site was not found.")
	}
	if !lowerUUIDPattern.MatchString(outboxID) {
		return ControlAlarmDeadLetterReplayResponse{}, notFoundControlError("The requested Control Alarm dead-letter entry was not found.")
	}
	if _, err := a.authorizeSiteScheduleManage(ctx, operator, siteID, "The authenticated Principal is not allowed to replay Control Alarm dead letters on this Site."); err != nil {
		return ControlAlarmDeadLetterReplayResponse{}, err
	}
	replayed, err := dlq.ReplayControlAlarmDeadLetter(ctx, operator.TenantID, siteID, outboxID)
	if err != nil {
		return ControlAlarmDeadLetterReplayResponse{}, controlErrorFromStore(err, "Command Service could not replay the Control Alarm dead-letter entry.")
	}
	return ControlAlarmDeadLetterReplayResponse{
		Replayed: replayed,
		OutboxID: outboxID,
	}, nil
}

func (a *ControlAuthority) authorizeSiteScheduleManage(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	forbiddenDetail string,
) (RegistryDecisionResult, error) {
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionScheduleManage)
	if err != nil {
		return RegistryDecisionResult{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return RegistryDecisionResult{}, &ControlError{
			HTTPStatus: http.StatusForbidden,
			Code:       "CONTROL_EXIT_FORBIDDEN",
			Title:      "Control exit forbidden",
			Detail:     forbiddenDetail,
		}
	}
	return decision, nil
}

func (a *ControlAuthority) verifySiteDevice(ctx context.Context, operator OperatorIdentity, siteID, deviceID string) error {
	model, err := a.twin.GetSiteAssetModel(ctx, operator, siteID)
	if err != nil {
		return err
	}
	for _, dev := range model.Devices {
		if dev.ID == deviceID && dev.TenantID == operator.TenantID && dev.SiteID == siteID &&
			(strings.TrimSpace(dev.Status) == "" || strings.EqualFold(dev.Status, "ACTIVE")) {
			return nil
		}
	}
	return notFoundControlError("The requested Device was not found on this Site.")
}

func validatePriorityLockReason(rawReason, title string) (string, *ControlError) {
	reason := strings.TrimSpace(rawReason)
	if reason == "" || utf8.RuneCountInString(reason) > 1000 {
		return "", &ControlError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "CONTROL_EXIT_REQUEST_INVALID",
			Title:      title,
			Detail:     "A non-empty reason up to 1000 characters is required.",
		}
	}
	return reason, nil
}

func devicePriorityLockView(lock commandmodel.DevicePriorityLock) DevicePriorityLockView {
	return DevicePriorityLockView{
		DeviceID:       lock.DeviceID,
		LockedPriority: string(lock.LockedPriority),
		CommandSource:  string(lock.CommandSource),
		LockedBy:       lock.LockedBy,
		LockedUntil:    lock.LockedUntil.UTC().Format(time.RFC3339Nano),
		Reason:         lock.Reason,
		UpdatedAt:      lock.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (a *ControlAuthority) mintGrantAndSubmit(
	ctx context.Context,
	operator OperatorIdentity,
	siteID, deviceID, pointID, idempotencyKey, traceID string,
	capability commandmodel.Capability,
	profile commandmodel.CapabilityProfile,
	parameters commandmodel.CommandParameters,
	verificationPointKey string,
	precondMode commandmodel.PreconditionMode,
	bypassReason string,
	priorityTier commandmodel.PriorityTier,
	commandSource commandmodel.CommandSource,
	deliveryMode commandmodel.DeliveryMode,
	ttlSeconds *int,
	expiresAt *time.Time,
	batchID *string,
	batchItemIndex *int,
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
	normalizedTraceID := commandmodel.NormalizeTraceID(traceID)
	if normalizedTraceID == "" {
		normalizedTraceID = commandmodel.NormalizeTraceID(observability.TraceID(ctx))
	}
	res, err := a.store.Submit(ctx, commandmodel.SubmitRequest{
		TenantID:             operator.TenantID,
		SiteID:               siteID,
		DeviceID:             deviceID,
		PointID:              pointID,
		PrincipalID:          claims.PrincipalID,
		IdempotencyKey:       idempotencyKey,
		TraceID:              normalizedTraceID,
		Capability:           capability,
		Parameters:           cloneParameters(parameters),
		VerificationPointKey: verificationPointKey,
		PreconditionMode:     precondMode,
		BypassReason:         bypassReason,
		PriorityTier:         priorityTier,
		CommandSource:        commandSource,
		DeliveryMode:         deliveryMode,
		TTLSeconds:           cloneIntPtr(ttlSeconds),
		ExpiresAt:            cloneTimePtr(expiresAt),
		BatchID:              cloneStringPtr(batchID),
		BatchItemIndex:       cloneIntPtr(batchItemIndex),
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

	meta, metaErr := commandmodel.ParseCommandPointMetadata(point.SourceMetadata)
	if metaErr != nil {
		if errors.Is(metaErr, commandmodel.ErrInvalidInterlockMetadata) {
			return resolvedCommandPointTarget{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
				Title: "Command request invalid", Detail: "Command point interlock configuration is invalid.",
			}
		}
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "COMMAND_UNAVAILABLE",
			Title: "Command unavailable", Detail: "The COMMAND Point does not declare a supported authoritative capability contract.", Retryable: true,
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(meta.Capability)
	if !supported || meta.CapabilityRevision != profile.Revision || meta.FeedbackPointKey == "" {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusServiceUnavailable, Code: "COMMAND_UNAVAILABLE",
			Title: "Command unavailable", Detail: "The COMMAND Point does not declare a supported authoritative capability contract.", Retryable: true,
		}
	}

	var feedbackPoint TelemetryPointDTO
	var feedbackFound bool
	for _, candidate := range model.TelemetryPoints {
		if candidate.ReportingDeviceID == device.ID && candidate.PointCode == meta.FeedbackPointKey && strings.EqualFold(candidate.Status, "ACTIVE") {
			feedbackPoint = candidate
			feedbackFound = true
			break
		}
	}
	if !feedbackFound {
		return resolvedCommandPointTarget{}, unavailableControlError("The Command feedback Point is unavailable.")
	}

	interlocks, interlockErr := validatePointInterlockChecks(meta.InterlockChecks, model)
	if interlockErr != nil {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Command point interlock configuration is invalid.",
		}
	}
	return resolvedCommandPointTarget{
		asset:         asset,
		device:        device,
		point:         point,
		feedbackPoint: feedbackPoint,
		capability:    meta.Capability,
		profile:       profile,
		actuationMode: meta.ActuationMode,
		interlocks:    interlocks,
	}, nil
}

func validatePointInterlockChecks(parsed []commandmodel.InterlockCheck, model SiteAssetModelDTO) ([]commandmodel.InterlockCheck, error) {
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
