package optimization

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	DefaultExecutionMonitorWindow      = 30 * time.Minute
	DefaultMonitorTickInterval         = time.Minute
	DefaultMonitorTelemetryUnhealthy   = 5 * time.Minute
	DefaultExecutionMonitorClaimLease  = 45 * time.Second
	DefaultExecutionMonitorClaimLimit  = 100
	DefaultSupplyRevalidationDriftMaxC = 0.5
	DefaultChillerPowerRatioLimit      = 0.90
	DefaultAutoControlMaxPointAge      = 90 * time.Second
	DefaultPendingSubmitStaleAge       = 2 * time.Minute
	DefaultSupplyTempHardFloorC        = 5.0
	DefaultCabinetTamperThresholdC     = 0.35
	DefaultCabinetTamperMatchEpsilonC  = 0.01
	DefaultCabinetManualBackoffWindow  = 2 * time.Hour
	DefaultSetpointConvergenceGrace    = 10 * time.Minute
	MaxSetpointDeviationC              = 0.3
	DefaultShadowMaxPerHour            = 12

	ExecutionModeClosedLoop = "CLOSED_LOOP"
	ExecutionModeShadow     = "SHADOW"

	ExecutionMonitorPendingSubmit     = "PENDING_SUBMIT"
	ExecutionMonitorObserving         = "OBSERVING"
	ExecutionMonitorVerified          = "VERIFIED"
	ExecutionMonitorTrippedRolledBack = "TRIPPED_ROLLED_BACK"
	ExecutionMonitorRollbackFailed    = "ROLLBACK_FAILED"
	ExecutionMonitorShadowRecorded    = "SHADOW_RECORDED"
	ExecutionMonitorSubmitFailed      = "SUBMIT_FAILED"
	ExecutionMonitorManualOverride    = "MANUAL_OVERRIDE"
)

var ErrExecutionMonitorClaimLost = errors.New("optimization execution monitor claim lost")

type AutoControlLeaseRecord struct {
	ID                  string
	TenantID            string
	SiteID              string
	Status              string
	ArmedBy             string
	ArmedAt             time.Time
	ExpiresAt           time.Time
	AllowedCapabilities []string
	MaxSetpointStepC    float64
	SupplyTempMinC      float64
	SupplyTempMaxC      float64
	ZoneTempMaxC        float64
	MinRunningChillers  int
	MaxCommandsPerHour  int
	DeadbandC           float64
	ManualBackoffUntil  *time.Time
	ManualBackoffReason string
	ExecutionMode       string
	Revision            int64
}

type OptimizationExecutionMonitorRecord struct {
	ID                      string
	TenantID                string
	SiteID                  string
	LeaseID                 string
	RecommendationID        string
	CommandIntentID         string
	DeviceID                string
	TargetCapability        string
	BaselineSupplyTempC     *float64
	AppliedSupplyTempC      *float64
	RestoreSupplyTempC      *float64
	ZoneTempMaxC            float64
	SupplyTempMinC          float64
	SupplyTempMaxC          float64
	RawRecommendedValue     *float64
	ClampedValue            *float64
	GuardrailVerdict        string
	GuardrailTrace          json.RawMessage
	StartedAt               time.Time
	WindowEndsAt            time.Time
	LastCheckedAt           *time.Time
	TelemetryUnhealthySince *time.Time
	Status                  string
	TripReason              *string
	RollbackCommandIntentID *string
	ClaimOwner              *string
	ClaimUntil              *time.Time
	ExecutionMode           string
	DecisionID              string
	TraceID                 string
}

type SetpointCommandTarget struct {
	DeviceID         string
	PointID          string
	PointCode        string
	FeedbackPointKey string
	AssetID          string
	AssetLocked      bool
	SiteControlMode  string
}

type AutoControlSiteContext struct {
	SiteControlMode       string
	ExecutionMode         string
	LeaseDeadbandC        float64
	InputMapping          InputMapping
	DeviceIDs             []string
	SetpointTarget        *SetpointCommandTarget
	Topology              StagingTopology
	RecentAutoCountHour   int
	RecentShadowCountHour int
	HasActiveMonitor      bool
}

type ChillerPowerViolation struct {
	AssetID      string
	AssetCode    string
	PowerKW      float64
	RatedPowerKW float64
}

type AutoControlLiveObservation struct {
	SupplyTemp       *TelemetryEvidence
	SupplyErr        error
	ZoneTemps        map[string]TelemetryEvidence
	ZoneErr          error
	RunningChillers  int
	ChillersHealthy  bool
	ChillerPowerTrip *ChillerPowerViolation
	OutdoorWetBulbC  *float64
	CoolingLoadKW    *float64
	ChillerPowerKW   *float64
}

type AutoControlDispatchInput struct {
	TenantID               string
	SiteID                 string
	DeviceID               string
	PointID                string
	Capability             commandmodel.Capability
	Parameters             commandmodel.CommandParameters
	VerificationPointKey   string
	IdempotencyKey         string
	IsRollback             bool
	PreconditionMode       commandmodel.PreconditionMode
	BypassReason           string
	EvaluationAvailability string
	Presence               string
	Readiness              string
	Quality                string
	CurrentValue           *float64
	BusinessRevision       uint64
	ObservedAt             time.Time
	DecisionID             string
	TraceID                string
}

// AutomatedCommandRequest aliases AutoControlDispatchInput for callers using command-request terminology.
type AutomatedCommandRequest = AutoControlDispatchInput

type AutomatedDispatchRegistration struct {
	Recommendation   Recommendation
	LeaseID          string
	Target           SetpointCommandTarget
	BaselineSupplyC  float64
	AppliedSupplyC   float64
	RestoreSupplyC   float64
	ZoneMaxC         float64
	SupplyMinC       float64
	SupplyMaxC       float64
	RawRecommendedC  float64
	ClampedC         *float64
	GuardrailVerdict string
	GuardrailTrace   json.RawMessage
	ExecutionMode    string
	DecisionID       string
	TraceID          string
	At               time.Time
}

type PreRegisterDispatchResult struct {
	Monitor    OptimizationExecutionMonitorRecord
	SkipReason string
}

type WorkloadCommandDispatcher interface {
	RequestCommandGrant(ctx context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error)
	SubmitAutoCommand(ctx context.Context, grant string, input AutoControlDispatchInput) (string, error)
}

type AutoControlStore interface {
	ActiveAutoControlLease(ctx context.Context, tenantID, siteID string, at time.Time) (*AutoControlLeaseRecord, error)
	LoadAutoControlSiteContext(ctx context.Context, tenantID, siteID, inputSnapshotID string, at time.Time) (AutoControlSiteContext, error)
	PreRegisterAutomatedDispatch(ctx context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error)
	RecordShadowMonitor(ctx context.Context, reg AutomatedDispatchRegistration) (PreRegisterDispatchResult, error)
	ActivateAutomatedDispatch(ctx context.Context, recommendation Recommendation, monitorID, commandIntentID string, at time.Time) error
	MarkAutomatedDispatchFailed(ctx context.Context, tenantID, siteID, monitorID, errorSummary string, at time.Time) error
	ClaimObservingExecutionMonitors(ctx context.Context, claimOwner string, leaseDuration time.Duration, now time.Time, limit int) ([]OptimizationExecutionMonitorRecord, error)
	ReleaseMonitorClaim(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, releasedAt time.Time) error
	RecordMonitorHealthyHeartbeat(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, checkedAt time.Time) error
	RecordMonitorTelemetryUnhealthy(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, unhealthySince, checkedAt time.Time) error
	CompleteMonitorVerified(ctx context.Context, monitorID, tenantID, siteID, claimOwner string, checkedAt time.Time) error
	TripMonitorAndLease(ctx context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error
	MarkMonitorManualOverride(ctx context.Context, monitor OptimizationExecutionMonitorRecord, claimOwner, reason string, backoffUntil, checkedAt time.Time) error
}

type AutoControlTelemetryReader interface {
	ReadLiveAutoControlObservation(ctx context.Context, tenantID, siteID string, mapping InputMapping, topology StagingTopology, at time.Time) (AutoControlLiveObservation, error)
}

type AutoDispatchOutcome struct {
	Dispatched      bool
	ShadowRecorded  bool
	ExecutionMode   string
	SkipReason      string
	CommandIntentID string
	MonitorID       string
	DecisionID      string
	TraceID         string
}

type MonitorPassSummary struct {
	Checked         int
	Verified        int
	RolledBack      int
	Tripped         int
	ManualOverrides int
}

type AutoController struct {
	store          AutoControlStore
	telemetry      AutoControlTelemetryReader
	dispatcher     WorkloadCommandDispatcher
	pointHealth    TelemetryPointHealthChecker
	controlEvents  ControlEventPublisher
	flywheelStore  GovernanceFlywheelStore
	solverResolver ActiveSolverResolver
	clock          Clock
	logf           func(string, ...any)
	workerID       string
	leaseDuration  time.Duration
}

func (controller *AutoController) WithPointHealth(checker TelemetryPointHealthChecker) *AutoController {
	controller.pointHealth = checker
	return controller
}

func (controller *AutoController) WithControlEvents(publisher ControlEventPublisher) *AutoController {
	controller.controlEvents = publisher
	return controller
}

func (controller *AutoController) WithFlywheelStore(store GovernanceFlywheelStore) *AutoController {
	controller.flywheelStore = store
	return controller
}

func (controller *AutoController) WithSolverResolver(resolver ActiveSolverResolver) *AutoController {
	controller.solverResolver = resolver
	return controller
}

func (controller *AutoController) effectiveFlywheelStore() GovernanceFlywheelStore {
	if controller == nil {
		return nil
	}
	if controller.flywheelStore != nil {
		return controller.flywheelStore
	}
	if store, ok := controller.store.(GovernanceFlywheelStore); ok {
		return store
	}
	return nil
}

func (controller *AutoController) effectiveSolverResolver() ActiveSolverResolver {
	if controller == nil {
		return nil
	}
	if controller.solverResolver != nil {
		return controller.solverResolver
	}
	if resolver, ok := controller.store.(ActiveSolverResolver); ok {
		return resolver
	}
	return nil
}

func (controller *AutoController) WithWorkerID(workerID string) *AutoController {
	if trimmed := strings.TrimSpace(workerID); trimmed != "" {
		controller.workerID = trimmed
	}
	return controller
}

func (controller *AutoController) WithLeaseDuration(duration time.Duration) *AutoController {
	if duration > 0 {
		controller.leaseDuration = duration
	}
	return controller
}

func defaultAutoControlWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "optimization"
	}
	suffix := make([]byte, 4)
	if _, randErr := rand.Read(suffix); randErr != nil {
		return fmt.Sprintf("%s:%d", strings.TrimSpace(hostname), os.Getpid())
	}
	return fmt.Sprintf("%s:%d:%s", strings.TrimSpace(hostname), os.Getpid(), hex.EncodeToString(suffix))
}

func (controller *AutoController) effectiveWorkerID() string {
	if controller != nil && strings.TrimSpace(controller.workerID) != "" {
		return strings.TrimSpace(controller.workerID)
	}
	return defaultAutoControlWorkerID()
}

func (controller *AutoController) effectiveLeaseDuration() time.Duration {
	if controller != nil && controller.leaseDuration > 0 {
		return controller.leaseDuration
	}
	return DefaultExecutionMonitorClaimLease
}

func NewAutoController(
	store AutoControlStore,
	telemetry AutoControlTelemetryReader,
	dispatcher WorkloadCommandDispatcher,
	clock Clock,
	logf func(string, ...any),
) (*AutoController, error) {
	if store == nil || telemetry == nil || dispatcher == nil {
		return nil, errors.New("auto controller requires store, telemetry reader and command dispatcher")
	}
	if clock == nil {
		clock = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctrl := &AutoController{
		store:         store,
		telemetry:     telemetry,
		dispatcher:    dispatcher,
		clock:         clock,
		logf:          logf,
		workerID:      defaultAutoControlWorkerID(),
		leaseDuration: DefaultExecutionMonitorClaimLease,
	}
	if fw, ok := store.(GovernanceFlywheelStore); ok {
		ctrl.flywheelStore = fw
	}
	if sr, ok := store.(ActiveSolverResolver); ok {
		ctrl.solverResolver = sr
	}
	return ctrl, nil
}

// TryDispatch evaluates Layer 3 and Layer 4 preconditions after an optimization recommendation is
// published. Without an ACTIVE unexpired site auto-control lease, control remains MANUAL_CONFIRM.
func (controller *AutoController) TryDispatch(ctx context.Context, recommendation Recommendation, now time.Time) (AutoDispatchOutcome, error) {
	if controller == nil {
		return AutoDispatchOutcome{SkipReason: "MANUAL_CONFIRM"}, nil
	}
	if now.IsZero() {
		now = controller.clock().UTC()
	} else {
		now = now.UTC()
	}
	ctx, decisionID, traceID, err := ensureDecisionAndTraceContext(ctx, now)
	if err != nil {
		return AutoDispatchOutcome{}, err
	}
	fwState := dispatchFlywheelState{
		recommendation: recommendation,
		decisionID:     decisionID,
		traceID:        traceID,
		now:            now,
	}
	skipOutcome := func(mode, reason string) AutoDispatchOutcome {
		controller.recordDispatchFlywheel(ctx, &fwState, mode, reason, false, false)
		return AutoDispatchOutcome{
			ExecutionMode: mode,
			SkipReason:    reason,
			DecisionID:    decisionID,
			TraceID:       traceID,
		}
	}

	if recommendation.CommandIntentID != "" {
		return AutoDispatchOutcome{
			SkipReason: "ALREADY_DISPATCHED",
			DecisionID: decisionID,
			TraceID:    traceID,
		}, nil
	}
	if recommendation.Approval == intelligencemodel.RecommendationRejected {
		return skipOutcome("", "RECOMMENDATION_REJECTED"), nil
	}

	lease, err := controller.store.ActiveAutoControlLease(ctx, recommendation.TenantID, recommendation.SiteID, now)
	if err != nil {
		return AutoDispatchOutcome{DecisionID: decisionID, TraceID: traceID}, err
	}
	if lease == nil || lease.Status != "ACTIVE" || !lease.ExpiresAt.After(now) {
		return skipOutcome("", "MANUAL_CONFIRM"), nil
	}
	fwState.lease = lease
	executionMode := normalizeExecutionMode(lease.ExecutionMode)

	validUntil := recommendation.EffectiveValidUntil()
	if validUntil.IsZero() || !validUntil.After(now) {
		return skipOutcome(executionMode, "RECOMMENDATION_EXPIRED"), nil
	}

	targetCapability := commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	if !slices.Contains(lease.AllowedCapabilities, string(targetCapability)) {
		return skipOutcome(executionMode, "CAPABILITY_NOT_LEASED"), nil
	}
	candidateSupplyC := recommendation.Candidate.SupplyTempC
	if !finite(candidateSupplyC) || candidateSupplyC == 0 {
		return skipOutcome(executionMode, "CANDIDATE_SUPPLY_TEMP_MISSING"), nil
	}
	baselineSupplyC := recommendation.Baseline.SupplyTempC
	if !finite(baselineSupplyC) || baselineSupplyC == 0 {
		return skipOutcome(executionMode, "BASELINE_SUPPLY_TEMP_MISSING"), nil
	}
	fwState.baselineSupplyC = float64Ptr(baselineSupplyC)
	restoreSupplyC := baselineSupplyC
	if recommendation.RollbackPlan.RestoreSupplyTempC != nil && finite(*recommendation.RollbackPlan.RestoreSupplyTempC) {
		restoreSupplyC = *recommendation.RollbackPlan.RestoreSupplyTempC
	}

	siteCtx, err := controller.store.LoadAutoControlSiteContext(ctx, recommendation.TenantID, recommendation.SiteID, recommendation.InputSnapshotID, now)
	if err != nil {
		return AutoDispatchOutcome{ExecutionMode: executionMode, DecisionID: decisionID, TraceID: traceID}, err
	}
	siteCtxCopy := siteCtx
	fwState.siteCtx = &siteCtxCopy
	if executionMode == ExecutionModeClosedLoop {
		if siteCtx.HasActiveMonitor {
			return skipOutcome(executionMode, "ACTIVE_MONITOR_EXISTS"), nil
		}
		if siteCtx.RecentAutoCountHour >= lease.MaxCommandsPerHour {
			return skipOutcome(executionMode, "RATE_LIMIT_REACHED"), nil
		}
	} else if siteCtx.RecentShadowCountHour >= DefaultShadowMaxPerHour {
		return skipOutcome(executionMode, "SHADOW_RATE_LIMIT_REACHED"), nil
	}
	if siteCtx.SiteControlMode != "" && siteCtx.SiteControlMode != SiteControlModeRemoteAuto {
		return skipOutcome(executionMode, "SITE_CONTROL_EXIT_ACTIVE"), nil
	}
	if siteCtx.SetpointTarget == nil || siteCtx.SetpointTarget.AssetLocked || hasAnyAssetLock(siteCtx.Topology.AssetControlLocks) {
		return skipOutcome(executionMode, "ASSET_CONTROL_LOCKED"), nil
	}
	if strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) == "" || len(siteCtx.InputMapping.effectiveZoneKeys()) == 0 {
		return skipOutcome(executionMode, "INPUT_MAPPING_INCOMPLETE"), nil
	}

	if controller.pointHealth == nil {
		return skipOutcome(executionMode, "TELEMETRY_UNHEALTHY"), nil
	}
	deviceIDs, allKeys := autoControlHealthScope(siteCtx, siteCtx.SetpointTarget.DeviceID)
	healthSnapshot, err := controller.pointHealth.InspectTelemetryPointHealth(ctx, recommendation.TenantID, recommendation.SiteID, deviceIDs, allKeys)
	if err != nil {
		if ctx.Err() != nil {
			return AutoDispatchOutcome{ExecutionMode: executionMode, DecisionID: decisionID, TraceID: traceID}, ctx.Err()
		}
		return skipOutcome(executionMode, "TELEMETRY_UNHEALTHY"), nil
	}
	liveState := extractAutoControlLiveState(healthSnapshot.LivePoints, siteCtx, siteCtx.SetpointTarget.DeviceID, now)
	fwState.liveState = &liveState
	if !healthSnapshot.Healthy {
		return skipOutcome(executionMode, "TELEMETRY_UNHEALTHY"), nil
	}

	var currentSetpointPtr *float64
	if liveState.Setpoint != nil && liveState.Setpoint.NumericValue != nil && finite(*liveState.Setpoint.NumericValue) {
		v := *liveState.Setpoint.NumericValue
		currentSetpointPtr = &v
	}
	var currentMeasuredPtr *float64
	if liveState.SupplyTemp != nil && liveState.SupplyTemp.NumericValue != nil && finite(*liveState.SupplyTemp.NumericValue) {
		v := *liveState.SupplyTemp.NumericValue
		currentMeasuredPtr = &v
	}

	hardMin := math.Max(DefaultSupplyTempHardFloorC, lease.SupplyTempMinC)
	supplyMax := lease.SupplyTempMaxC
	maxStep := lease.MaxSetpointStepC
	deadband := lease.DeadbandC
	hydraulicInterlockOK := liveState.RunningChillers >= lease.MinRunningChillers
	guardrailPoints := buildGuardrailPointSamples(siteCtx, healthSnapshot.LivePoints, liveState)
	guardrailResult := commandmodel.EvaluateGuardrails(commandmodel.GuardrailInput{
		Now:                  now,
		Capability:           targetCapability,
		DeviceID:             siteCtx.SetpointTarget.DeviceID,
		ProposedValue:        candidateSupplyC,
		CurrentSetpoint:      currentSetpointPtr,
		CurrentMeasured:      currentMeasuredPtr,
		Points:               guardrailPoints,
		MaxPointAge:          DefaultAutoControlMaxPointAge,
		HardMin:              float64Ptr(hardMin),
		HardMax:              float64Ptr(supplyMax),
		MaxStep:              float64Ptr(maxStep),
		Deadband:             float64Ptr(deadband),
		ManualBackoffUntil:   lease.ManualBackoffUntil,
		DeviceFaultActive:    liveState.DeviceFaultActive,
		CabinetLocalManual:   liveState.CabinetLocalManual,
		DeviceOffline:        liveState.DeviceOffline,
		HydraulicInterlockOK: boolPtr(hydraulicInterlockOK),
	})
	guardrailCopy := guardrailResult
	fwState.guardrail = &guardrailCopy
	traceBytes := encodeOptimizationJSON(guardrailResult.Trace)
	if len(traceBytes) == 0 || string(traceBytes) == "null" {
		traceBytes = []byte("[]")
	}
	fwState.guardrailTrace = traceBytes

	switch guardrailResult.Verdict {
	case commandmodel.GuardrailVerdictDeadbandSkip:
		return skipOutcome(executionMode, skipReasonOrDefault(guardrailResult.ReasonCode, "DEADBAND_SKIP")), nil
	case commandmodel.GuardrailVerdictVetoed:
		return skipOutcome(executionMode, skipReasonOrDefault(guardrailResult.ReasonCode, "GUARDRAIL_VETOED")), nil
	}
	if !guardrailResult.Allowed {
		return skipOutcome(executionMode, skipReasonOrDefault(guardrailResult.ReasonCode, "GUARDRAIL_VETOED")), nil
	}

	// Fail closed on any missing/stale/non-GOOD/non-ONLINE/non-positive-revision live telemetry point.
	if liveState.SupplyTemp == nil || !isLivePointDispatchable(*liveState.SupplyTemp, now, DefaultAutoControlMaxPointAge) {
		return skipOutcome(executionMode, "TELEMETRY_REVALIDATION_FAILED"), nil
	}
	zoneKeys := siteCtx.InputMapping.effectiveZoneKeys()
	if len(liveState.ZoneTemps) != len(zoneKeys) {
		return skipOutcome(executionMode, "TELEMETRY_REVALIDATION_FAILED"), nil
	}
	for _, zonePt := range liveState.ZoneTemps {
		if !isLivePointDispatchable(zonePt, now, DefaultAutoControlMaxPointAge) {
			return skipOutcome(executionMode, "TELEMETRY_REVALIDATION_FAILED"), nil
		}
	}

	targetPt := liveState.Setpoint
	if strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey) == "" && targetPt == nil {
		targetPt = liveState.SupplyTemp
	}
	if targetPt == nil || !isLivePointDispatchable(*targetPt, now, DefaultAutoControlMaxPointAge) {
		return skipOutcome(executionMode, "TELEMETRY_REVALIDATION_FAILED"), nil
	}

	supplyValue := *liveState.SupplyTemp.NumericValue
	if math.Abs(supplyValue-baselineSupplyC) > DefaultSupplyRevalidationDriftMaxC+1e-9 {
		return skipOutcome(executionMode, "SUPPLY_TEMP_DRIFT_EXCEEDED"), nil
	}
	for _, zonePt := range liveState.ZoneTemps {
		if *zonePt.NumericValue > lease.ZoneTempMaxC {
			return skipOutcome(executionMode, "ZONE_TEMP_LIMIT_EXCEEDED"), nil
		}
	}
	if liveState.RunningChillers < lease.MinRunningChillers {
		return skipOutcome(executionMode, "MIN_RUNNING_CHILLERS_NOT_MET"), nil
	}

	if controller.telemetry != nil {
		obs, obsErr := controller.telemetry.ReadLiveAutoControlObservation(ctx, recommendation.TenantID, recommendation.SiteID, siteCtx.InputMapping, siteCtx.Topology, now)
		if obsErr != nil {
			return AutoDispatchOutcome{ExecutionMode: executionMode, DecisionID: decisionID, TraceID: traceID}, obsErr
		}
		obsCopy := obs
		fwState.liveObs = &obsCopy
		if obs.ChillerPowerTrip != nil {
			return skipOutcome(executionMode, "CHILLER_POWER_RATIO_EXCEEDED"), nil
		}
	}

	dispatchedSupplyC := candidateSupplyC
	var clampedValue *float64
	if guardrailResult.Verdict == commandmodel.GuardrailVerdictClamped {
		if !finite(guardrailResult.AdjustedValue) || guardrailResult.AdjustedValue == 0 {
			return skipOutcome(executionMode, "GUARDRAIL_INVALID_ADJUSTMENT"), nil
		}
		dispatchedSupplyC = guardrailResult.AdjustedValue
		clampedCopy := dispatchedSupplyC
		clampedValue = &clampedCopy
	} else if finite(guardrailResult.AdjustedValue) && guardrailResult.AdjustedValue > 0 {
		dispatchedSupplyC = guardrailResult.AdjustedValue
	}
	if dispatchedSupplyC < DefaultSupplyTempHardFloorC || dispatchedSupplyC > lease.SupplyTempMaxC {
		return skipOutcome(executionMode, "TARGET_OUTSIDE_LEASE_BOUNDS"), nil
	}

	revalidationAt := now
	if !revalidationAt.After(recommendation.CreatedAt) {
		revalidationAt = recommendation.CreatedAt.Add(time.Millisecond)
	}
	expiresRevalidationAt := revalidationAt.Add(5 * time.Minute)
	if !expiresRevalidationAt.After(now) {
		expiresRevalidationAt = now.Add(5 * time.Minute)
	}
	snapshotID := "reval:" + recommendation.InputSnapshotID + ":" + revalidationAt.UTC().Format("20060102T150405.000Z")
	recommendation.Approval = intelligencemodel.RecommendationApproved
	recommendation.Revalidation = &intelligencemodel.CurrentStateRevalidation{
		SnapshotID:  snapshotID,
		Accepted:    true,
		ReasonCode:  "CURRENT_STATE_SAFE",
		ValidatedAt: revalidationAt,
		ExpiresAt:   expiresRevalidationAt,
	}
	if err := recommendation.CanCreateCommand(now); err != nil {
		return skipOutcome(executionMode, "REVALIDATION_GATE_REJECTED"), nil
	}

	profile, ok := commandmodel.CapabilityProfileFor(targetCapability)
	if !ok {
		return skipOutcome(executionMode, "CAPABILITY_UNSUPPORTED"), nil
	}

	dispatchReg := AutomatedDispatchRegistration{
		Recommendation:   recommendation,
		LeaseID:          lease.ID,
		Target:           *siteCtx.SetpointTarget,
		BaselineSupplyC:  baselineSupplyC,
		AppliedSupplyC:   dispatchedSupplyC,
		RestoreSupplyC:   restoreSupplyC,
		ZoneMaxC:         lease.ZoneTempMaxC,
		SupplyMinC:       hardMin,
		SupplyMaxC:       lease.SupplyTempMaxC,
		RawRecommendedC:  candidateSupplyC,
		ClampedC:         clampedValue,
		GuardrailVerdict: string(guardrailResult.Verdict),
		GuardrailTrace:   traceBytes,
		ExecutionMode:    executionMode,
		DecisionID:       decisionID,
		TraceID:          traceID,
		At:               now,
	}

	if executionMode == ExecutionModeShadow {
		shadowResult, shadowErr := controller.store.RecordShadowMonitor(ctx, dispatchReg)
		if shadowErr != nil {
			return AutoDispatchOutcome{ExecutionMode: ExecutionModeShadow, DecisionID: decisionID, TraceID: traceID}, shadowErr
		}
		if shadowResult.SkipReason != "" {
			return skipOutcome(ExecutionModeShadow, shadowResult.SkipReason), nil
		}
		fwState.monitorID = shadowResult.Monitor.ID
		fwState.appliedSupplyC = float64Ptr(dispatchedSupplyC)
		controller.recordDispatchFlywheel(ctx, &fwState, ExecutionModeShadow, "", false, true)
		controller.logf("optimization shadow-mode recorded recommendation=%s site=%s monitor=%s decision=%s trace=%s targetC=%.2f",
			recommendation.ID, recommendation.SiteID, shadowResult.Monitor.ID, decisionID, traceID, dispatchedSupplyC)
		return AutoDispatchOutcome{
			Dispatched:     false,
			ShadowRecorded: true,
			ExecutionMode:  ExecutionModeShadow,
			MonitorID:      shadowResult.Monitor.ID,
			DecisionID:     decisionID,
			TraceID:        traceID,
		}, nil
	}

	currentVal := *targetPt.NumericValue
	params := commandmodel.CommandParameters{commandmodel.ParameterSetpointC: dispatchedSupplyC}
	grantReq := commandauth.WorkloadDecisionRequest{
		TenantID:           recommendation.TenantID,
		SiteID:             recommendation.SiteID,
		DeviceID:           siteCtx.SetpointTarget.DeviceID,
		Capability:         targetCapability,
		CapabilityRevision: profile.Revision,
		Purpose:            commandmodel.AuthorizationCommandSubmit,
		Parameters:         params,
		CurrentValue:       &currentVal,
		IsRollback:         false,
	}
	grant, decision, err := controller.dispatcher.RequestCommandGrant(ctx, grantReq)
	if err != nil {
		return AutoDispatchOutcome{ExecutionMode: executionMode, DecisionID: decisionID, TraceID: traceID}, err
	}
	if !decision.Allowed || grant == "" {
		return skipOutcome(executionMode, "IAM_DENIED:"+string(decision.ReasonCode)), nil
	}

	regResult, err := controller.store.PreRegisterAutomatedDispatch(ctx, dispatchReg)
	if err != nil {
		return AutoDispatchOutcome{ExecutionMode: executionMode, DecisionID: decisionID, TraceID: traceID}, err
	}
	if regResult.SkipReason != "" {
		return skipOutcome(executionMode, regResult.SkipReason), nil
	}
	monitor := regResult.Monitor

	verificationKey := strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey)
	if verificationKey == "" {
		verificationKey = siteCtx.InputMapping.SupplyTemperatureKey
	}
	availability, presence, readiness, quality := livePointCommandCurrentState(*targetPt, true, now)

	commandIntentID, submitErr := controller.dispatcher.SubmitAutoCommand(ctx, grant, AutoControlDispatchInput{
		TenantID:               recommendation.TenantID,
		SiteID:                 recommendation.SiteID,
		DeviceID:               siteCtx.SetpointTarget.DeviceID,
		PointID:                siteCtx.SetpointTarget.PointID,
		Capability:             targetCapability,
		Parameters:             params,
		VerificationPointKey:   verificationKey,
		IdempotencyKey:         "auto-opt:" + recommendation.ID,
		IsRollback:             false,
		PreconditionMode:       commandmodel.PreconditionRequireCurrentState,
		EvaluationAvailability: availability,
		Presence:               presence,
		Readiness:              readiness,
		Quality:                quality,
		CurrentValue:           float64Ptr(currentVal),
		BusinessRevision:       uint64(targetPt.BusinessRevision),
		ObservedAt:             targetPt.SampledAt.UTC(),
		DecisionID:             decisionID,
		TraceID:                traceID,
	})
	if submitErr != nil || strings.TrimSpace(commandIntentID) == "" {
		errToRecord := submitErr
		if errToRecord == nil {
			errToRecord = errors.New("gateway auto-command returned empty commandId")
		}
		failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if markErr := controller.store.MarkAutomatedDispatchFailed(failCtx, recommendation.TenantID, recommendation.SiteID, monitor.ID, errToRecord.Error(), now); markErr != nil {
			controller.logf("monitor %s mark submit failed error: %v", monitor.ID, markErr)
		}
		failCancel()
		fwState.monitorID = monitor.ID
		controller.recordDispatchFlywheel(ctx, &fwState, executionMode, "SUBMIT_FAILED", false, false)
		return AutoDispatchOutcome{
			ExecutionMode: executionMode,
			MonitorID:     monitor.ID,
			DecisionID:    decisionID,
			TraceID:       traceID,
		}, errToRecord
	}

	activateCtx, activateCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer activateCancel()
	if err := controller.store.ActivateAutomatedDispatch(activateCtx, recommendation, monitor.ID, commandIntentID, now); err != nil {
		return AutoDispatchOutcome{
			ExecutionMode:   executionMode,
			CommandIntentID: commandIntentID,
			MonitorID:       monitor.ID,
			DecisionID:      decisionID,
			TraceID:         traceID,
		}, err
	}
	fwState.monitorID = monitor.ID
	fwState.appliedSupplyC = float64Ptr(dispatchedSupplyC)
	controller.recordDispatchFlywheel(ctx, &fwState, ExecutionModeClosedLoop, "", true, false)
	controller.logf("optimization auto-control dispatched recommendation=%s site=%s command=%s monitor=%s decision=%s trace=%s targetC=%.2f",
		recommendation.ID, recommendation.SiteID, commandIntentID, monitor.ID, decisionID, traceID, dispatchedSupplyC)
	return AutoDispatchOutcome{
		Dispatched:      true,
		ExecutionMode:   ExecutionModeClosedLoop,
		CommandIntentID: commandIntentID,
		MonitorID:       monitor.ID,
		DecisionID:      decisionID,
		TraceID:         traceID,
	}, nil
}

// RunMonitorPass claims due OBSERVING execution monitors under a worker lease, checks live
// zone/supply temperatures, chiller rated power ratios, cabinet manual tamper, and telemetry
// health, and issues rollback and lease trip or manual override backoff on violation.
func (controller *AutoController) RunMonitorPass(ctx context.Context, now time.Time) (MonitorPassSummary, error) {
	if controller == nil {
		return MonitorPassSummary{}, nil
	}
	if now.IsZero() {
		now = controller.clock().UTC()
	} else {
		now = now.UTC()
	}
	workerID := controller.effectiveWorkerID()
	leaseDuration := controller.effectiveLeaseDuration()
	monitors, err := controller.store.ClaimObservingExecutionMonitors(ctx, workerID, leaseDuration, now, DefaultExecutionMonitorClaimLimit)
	if err != nil {
		return MonitorPassSummary{}, err
	}
	var summary MonitorPassSummary
	for idx, monitor := range monitors {
		if ctx.Err() != nil {
			controller.releaseRemainingMonitorClaims(ctx, monitors[idx:], workerID, now)
			return summary, ctx.Err()
		}
		summary.Checked++
		controller.processClaimedMonitor(ctx, monitor, workerID, now, &summary)
	}
	return summary, nil
}

func (controller *AutoController) releaseRemainingMonitorClaims(ctx context.Context, monitors []OptimizationExecutionMonitorRecord, workerID string, at time.Time) {
	for _, monitor := range monitors {
		controller.releaseMonitorClaim(ctx, monitor, workerID, at)
	}
}

func (controller *AutoController) releaseMonitorClaim(ctx context.Context, monitor OptimizationExecutionMonitorRecord, workerID string, at time.Time) {
	releaseCtx := ctx
	var cancel context.CancelFunc
	if ctx.Err() != nil {
		releaseCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
	}
	if err := controller.store.ReleaseMonitorClaim(releaseCtx, monitor.ID, monitor.TenantID, monitor.SiteID, workerID, at); err != nil && !errors.Is(err, ErrExecutionMonitorClaimLost) {
		controller.logf("monitor %s claim release failed: %v", monitor.ID, err)
	}
}

func (controller *AutoController) processClaimedMonitor(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	workerID string,
	now time.Time,
	summary *MonitorPassSummary,
) {
	ctx, traceID := ensureMonitorTraceContext(ctx, monitor.TraceID)
	if strings.TrimSpace(monitor.TraceID) == "" {
		monitor.TraceID = traceID
	}

	siteCtx, ctxErr := controller.store.LoadAutoControlSiteContext(ctx, monitor.TenantID, monitor.SiteID, "", now)
	if ctxErr != nil {
		controller.logf("monitor %s site context error: %v", monitor.ID, ctxErr)
		controller.releaseMonitorClaim(ctx, monitor, workerID, now)
		return
	}

	telemetryHealthy := true
	zoneKeys := siteCtx.InputMapping.effectiveZoneKeys()
	if strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) == "" || len(zoneKeys) == 0 || controller.pointHealth == nil {
		telemetryHealthy = false
	}

	var liveState autoControlLiveState
	if controller.pointHealth != nil && strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) != "" && len(zoneKeys) > 0 {
		deviceIDs, allKeys := autoControlHealthScope(siteCtx, monitor.DeviceID)
		healthSnapshot, healthErr := controller.pointHealth.InspectTelemetryPointHealth(ctx, monitor.TenantID, monitor.SiteID, deviceIDs, allKeys)
		if healthErr != nil {
			if ctx.Err() != nil {
				controller.releaseMonitorClaim(ctx, monitor, workerID, now)
				return
			}
			telemetryHealthy = false
		} else {
			liveState = extractAutoControlLiveState(healthSnapshot.LivePoints, siteCtx, monitor.DeviceID, now)
			if !healthSnapshot.Healthy {
				telemetryHealthy = false
			}
		}
	}

	manualOverrideReason := ""
	if detectCabinetSetpointTamper(monitor, liveState.Setpoint, now) {
		manualOverrideReason = "cabinet_setpoint_manual_override"
	} else if liveState.CabinetLocalManual {
		manualOverrideReason = "cabinet_local_manual"
	}
	if manualOverrideReason != "" {
		backoffUntil := now.Add(DefaultCabinetManualBackoffWindow)
		if err := controller.store.MarkMonitorManualOverride(ctx, monitor, workerID, manualOverrideReason, backoffUntil, now); err != nil {
			if !errors.Is(err, ErrExecutionMonitorClaimLost) {
				controller.logf("monitor %s manual override update failed: %v", monitor.ID, err)
				controller.releaseMonitorClaim(ctx, monitor, workerID, now)
			}
			return
		}
		controller.recordMonitorNegativeSample(ctx, monitor, siteCtx, &liveState, nil, telemetryHealthy, NegativeSampleManualOverride, manualOverrideReason, now)
		summary.ManualOverrides++
		return
	}

	if telemetryHealthy {
		if liveState.SupplyTemp == nil || !isLivePointDispatchable(*liveState.SupplyTemp, now, DefaultAutoControlMaxPointAge) || len(liveState.ZoneTemps) != len(zoneKeys) {
			telemetryHealthy = false
		} else {
			for _, zonePt := range liveState.ZoneTemps {
				if !isLivePointDispatchable(zonePt, now, DefaultAutoControlMaxPointAge) {
					telemetryHealthy = false
					break
				}
			}
		}
	}

	var chillerPowerTrip *ChillerPowerViolation
	var liveObs *AutoControlLiveObservation
	if telemetryHealthy && controller.telemetry != nil {
		obs, obsErr := controller.telemetry.ReadLiveAutoControlObservation(ctx, monitor.TenantID, monitor.SiteID, siteCtx.InputMapping, siteCtx.Topology, now)
		if obsErr != nil && ctx.Err() != nil {
			controller.releaseMonitorClaim(ctx, monitor, workerID, now)
			return
		}
		if obsErr == nil {
			obsCopy := obs
			liveObs = &obsCopy
			chillerPowerTrip = obs.ChillerPowerTrip
		}
	}

	if !telemetryHealthy {
		unhealthySince := now
		if monitor.TelemetryUnhealthySince != nil {
			unhealthySince = monitor.TelemetryUnhealthySince.UTC()
		}
		if monitor.TelemetryUnhealthySince != nil && now.Sub(unhealthySince) > DefaultMonitorTelemetryUnhealthy {
			rolledBack, tripErr := controller.tripAndRollback(ctx, monitor, workerID, siteCtx, liveState, false, "telemetry_unhealthy_over_5m", "", now)
			if tripErr != nil {
				if !errors.Is(tripErr, ErrExecutionMonitorClaimLost) {
					controller.logf("monitor %s trip on telemetry failure error: %v", monitor.ID, tripErr)
					controller.releaseMonitorClaim(ctx, monitor, workerID, now)
				}
				return
			}
			controller.recordMonitorNegativeSample(ctx, monitor, siteCtx, &liveState, liveObs, telemetryHealthy, NegativeSampleMonitorTripped, "telemetry_unhealthy_over_5m", now)
			if rolledBack {
				summary.RolledBack++
			} else {
				summary.Tripped++
			}
			return
		}
		if err := controller.store.RecordMonitorTelemetryUnhealthy(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, workerID, unhealthySince, now); err != nil {
			if !errors.Is(err, ErrExecutionMonitorClaimLost) {
				controller.logf("monitor %s unhealthy timestamp update failed: %v", monitor.ID, err)
				controller.releaseMonitorClaim(ctx, monitor, workerID, now)
			}
		}
		return
	}

	liveObsSummary := buildMonitorLiveObservation(liveState, chillerPowerTrip)
	tripReason := ""
	tripDetail := ""
	if liveState.DeviceFaultActive {
		tripReason = "device_fault_active"
	} else if reason := monitorViolationReason(monitor, liveObsSummary); reason != "" {
		tripReason = reason
	} else {
		tripReason, tripDetail = checkSetpointTrackingDeviation(monitor, liveState, siteCtx, now)
	}
	if tripReason != "" {
		rolledBack, tripErr := controller.tripAndRollback(ctx, monitor, workerID, siteCtx, liveState, true, tripReason, tripDetail, now)
		if tripErr != nil {
			if !errors.Is(tripErr, ErrExecutionMonitorClaimLost) {
				controller.logf("monitor %s trip on boundary violation failed: %v", monitor.ID, tripErr)
				controller.releaseMonitorClaim(ctx, monitor, workerID, now)
			}
			return
		}
		negReason := NegativeSampleMonitorTripped
		negDetail := tripReason
		if tripReason == "setpoint_tracking_deviation" {
			negReason = NegativeSampleTrackingDeviation
			negDetail = firstNonEmptyString(tripDetail, tripReason)
		} else if strings.TrimSpace(tripDetail) != "" {
			negDetail = tripReason + ": " + strings.TrimSpace(tripDetail)
		}
		controller.recordMonitorNegativeSample(ctx, monitor, siteCtx, &liveState, liveObs, telemetryHealthy, negReason, negDetail, now)
		if rolledBack {
			summary.RolledBack++
		} else {
			summary.Tripped++
		}
		return
	}

	if !now.Before(monitor.WindowEndsAt) {
		if err := controller.store.CompleteMonitorVerified(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, workerID, now); err != nil {
			if !errors.Is(err, ErrExecutionMonitorClaimLost) {
				controller.logf("monitor %s verify completion failed: %v", monitor.ID, err)
				controller.releaseMonitorClaim(ctx, monitor, workerID, now)
			}
			return
		}
		summary.Verified++
		return
	}

	if err := controller.store.RecordMonitorHealthyHeartbeat(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, workerID, now); err != nil {
		if !errors.Is(err, ErrExecutionMonitorClaimLost) {
			controller.logf("monitor %s heartbeat update failed: %v", monitor.ID, err)
			controller.releaseMonitorClaim(ctx, monitor, workerID, now)
		}
	}
}

func detectCabinetSetpointTamper(monitor OptimizationExecutionMonitorRecord, setpointPt *TelemetryLivePoint, now time.Time) bool {
	if monitor.AppliedSupplyTempC == nil || setpointPt == nil {
		return false
	}
	if !isLivePointHealthyForTamper(*setpointPt, now, DefaultAutoControlMaxPointAge) {
		return false
	}
	liveSetpoint := *setpointPt.NumericValue
	if math.Abs(liveSetpoint-*monitor.AppliedSupplyTempC) <= DefaultCabinetTamperThresholdC {
		return false
	}
	if monitor.RestoreSupplyTempC != nil && math.Abs(liveSetpoint-*monitor.RestoreSupplyTempC) <= DefaultCabinetTamperMatchEpsilonC {
		return false
	}
	return true
}

func buildMonitorLiveObservation(liveState autoControlLiveState, chillerPowerTrip *ChillerPowerViolation) AutoControlLiveObservation {
	obs := AutoControlLiveObservation{
		ZoneTemps:        make(map[string]TelemetryEvidence, len(liveState.ZoneTemps)),
		RunningChillers:  liveState.RunningChillers,
		ChillersHealthy:  true,
		ChillerPowerTrip: chillerPowerTrip,
	}
	if liveState.SupplyTemp != nil && liveState.SupplyTemp.NumericValue != nil {
		obs.SupplyTemp = &TelemetryEvidence{
			DeviceID:      liveState.SupplyTemp.DeviceID,
			TelemetryKey:  liveState.SupplyTemp.TelemetryKey,
			PointRevision: uint64(maxInt64(0, liveState.SupplyTemp.PointRevision)),
			SampledAt:     liveState.SupplyTemp.SampledAt,
			ReceivedAt:    liveState.SupplyTemp.ReceivedAt,
			Value:         *liveState.SupplyTemp.NumericValue,
			Unit:          liveState.SupplyTemp.Unit,
			Quality:       liveState.SupplyTemp.Quality,
		}
	}
	for key, pt := range liveState.ZoneTemps {
		if pt.NumericValue == nil {
			continue
		}
		obs.ZoneTemps[key] = TelemetryEvidence{
			DeviceID:      pt.DeviceID,
			TelemetryKey:  pt.TelemetryKey,
			PointRevision: uint64(maxInt64(0, pt.PointRevision)),
			SampledAt:     pt.SampledAt,
			ReceivedAt:    pt.ReceivedAt,
			Value:         *pt.NumericValue,
			Unit:          pt.Unit,
			Quality:       pt.Quality,
		}
	}
	return obs
}

func monitorViolationReason(monitor OptimizationExecutionMonitorRecord, live AutoControlLiveObservation) string {
	if live.SupplyTemp == nil {
		return ""
	}
	minSupplyC := math.Max(DefaultSupplyTempHardFloorC, monitor.SupplyTempMinC)
	if live.SupplyTemp.Value < minSupplyC {
		return fmt.Sprintf("supply_temperature_below_min (%.2fC < %.2fC)", live.SupplyTemp.Value, minSupplyC)
	}
	if live.SupplyTemp.Value > monitor.SupplyTempMaxC {
		return fmt.Sprintf("supply_temperature_exceeded_max (%.2fC > %.2fC)", live.SupplyTemp.Value, monitor.SupplyTempMaxC)
	}
	for key, zone := range live.ZoneTemps {
		if zone.Value > monitor.ZoneTempMaxC {
			return fmt.Sprintf("zone_temperature_exceeded_max %s (%.2fC > %.2fC)", key, zone.Value, monitor.ZoneTempMaxC)
		}
	}
	if live.ChillerPowerTrip != nil {
		return fmt.Sprintf("chiller_power_exceeded_90pct %s (%.1fkW > 0.90*%.1fkW)",
			live.ChillerPowerTrip.AssetCode, live.ChillerPowerTrip.PowerKW, live.ChillerPowerTrip.RatedPowerKW)
	}
	return ""
}

func (controller *AutoController) tripAndRollback(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	claimOwner string,
	siteCtx AutoControlSiteContext,
	liveState autoControlLiveState,
	telemetryHealthy bool,
	tripReason string,
	tripDetail string,
	now time.Time,
) (bool, error) {
	var rollbackID *string
	finalStatus := ExecutionMonitorRollbackFailed
	rollbackFailureReason := ""

	switch {
	case monitor.RestoreSupplyTempC == nil:
		rollbackFailureReason = "restore_supply_temp_missing"
	case siteCtx.SetpointTarget == nil:
		rollbackFailureReason = "setpoint_target_missing"
	case siteCtx.SiteControlMode != "" && siteCtx.SiteControlMode != SiteControlModeRemoteAuto:
		rollbackFailureReason = "site_control_mode_" + siteCtx.SiteControlMode
	case siteCtx.SetpointTarget.AssetLocked:
		rollbackFailureReason = "asset_control_locked"
	default:
		restoreTemp := *monitor.RestoreSupplyTempC
		targetCapability := commandmodel.Capability(monitor.TargetCapability)
		profile, ok := commandmodel.CapabilityProfileFor(targetCapability)
		if !ok {
			rollbackFailureReason = "capability_unsupported"
		} else {
			var currentValPtr *float64
			if monitor.AppliedSupplyTempC != nil && finite(*monitor.AppliedSupplyTempC) {
				v := *monitor.AppliedSupplyTempC
				currentValPtr = &v
			} else if liveState.Setpoint != nil && liveState.Setpoint.NumericValue != nil && finite(*liveState.Setpoint.NumericValue) {
				v := *liveState.Setpoint.NumericValue
				currentValPtr = &v
			} else if liveState.SupplyTemp != nil && liveState.SupplyTemp.NumericValue != nil && finite(*liveState.SupplyTemp.NumericValue) {
				v := *liveState.SupplyTemp.NumericValue
				currentValPtr = &v
			}

			targetPt := liveState.Setpoint
			if strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey) == "" && targetPt == nil {
				targetPt = liveState.SupplyTemp
			}

			var observedAt time.Time
			var businessRevision uint64
			availability := "UNAVAILABLE"
			presence := "UNKNOWN"
			readiness := "INCOMPLETE"
			quality := "UNKNOWN"
			if targetPt != nil {
				observedAt = targetPt.SampledAt.UTC()
				if targetPt.BusinessRevision > 0 {
					businessRevision = uint64(targetPt.BusinessRevision)
				}
				liveOK := telemetryHealthy && isLivePointDispatchable(*targetPt, now, DefaultAutoControlMaxPointAge)
				availability, presence, readiness, quality = livePointCommandCurrentState(*targetPt, liveOK, now)
			}

			preconditionMode := commandmodel.PreconditionRequireCurrentState
			bypassReason := ""
			if !telemetryHealthy || targetPt == nil ||
				!isLivePointDispatchable(*targetPt, now, DefaultAutoControlMaxPointAge) ||
				targetPt.BusinessRevision <= 0 || targetPt.PointRevision <= 0 ||
				availability != "AVAILABLE" || presence != "ONLINE" || readiness != "CURRENT" || quality != "GOOD" {
				preconditionMode = commandmodel.PreconditionEmergencyBypass
				bypassReason = "automated_execution_monitor_rollback:" + tripReason
			}

			params := commandmodel.CommandParameters{commandmodel.ParameterSetpointC: restoreTemp}
			grant, decision, err := controller.dispatcher.RequestCommandGrant(ctx, commandauth.WorkloadDecisionRequest{
				TenantID:           monitor.TenantID,
				SiteID:             monitor.SiteID,
				DeviceID:           monitor.DeviceID,
				Capability:         targetCapability,
				CapabilityRevision: profile.Revision,
				Purpose:            commandmodel.AuthorizationCommandSubmit,
				Parameters:         params,
				CurrentValue:       currentValPtr,
				IsRollback:         true,
			})
			if err != nil {
				rollbackFailureReason = err.Error()
				controller.logf("monitor %s rollback grant failed: %v", monitor.ID, err)
			} else if !decision.Allowed || grant == "" {
				rollbackFailureReason = "IAM_DENIED:" + string(decision.ReasonCode)
			} else {
				verificationKey := strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey)
				if verificationKey == "" {
					verificationKey = siteCtx.InputMapping.SupplyTemperatureKey
				}
				if verificationKey == "" {
					verificationKey = "supply_water_temperature"
				}
				submittedID, submitErr := controller.dispatcher.SubmitAutoCommand(ctx, grant, AutoControlDispatchInput{
					TenantID:               monitor.TenantID,
					SiteID:                 monitor.SiteID,
					DeviceID:               monitor.DeviceID,
					PointID:                siteCtx.SetpointTarget.PointID,
					Capability:             targetCapability,
					Parameters:             params,
					VerificationPointKey:   verificationKey,
					IdempotencyKey:         "auto-rollback:" + monitor.ID,
					IsRollback:             true,
					PreconditionMode:       preconditionMode,
					BypassReason:           bypassReason,
					EvaluationAvailability: availability,
					Presence:               presence,
					Readiness:              readiness,
					Quality:                quality,
					CurrentValue:           currentValPtr,
					BusinessRevision:       businessRevision,
					ObservedAt:             observedAt,
					DecisionID:             monitor.DecisionID,
					TraceID:                monitor.TraceID,
				})
				if submitErr == nil && submittedID != "" {
					rollbackID = &submittedID
					finalStatus = ExecutionMonitorTrippedRolledBack
				} else if submitErr != nil {
					rollbackFailureReason = submitErr.Error()
					controller.logf("monitor %s rollback command submit failed: %v", monitor.ID, submitErr)
				} else {
					rollbackFailureReason = "gateway_empty_command_id"
				}
			}
		}
	}

	if err := controller.store.TripMonitorAndLease(ctx, monitor, claimOwner, finalStatus, tripReason, rollbackID, now); err != nil {
		if !errors.Is(err, ErrExecutionMonitorClaimLost) && finalStatus == ExecutionMonitorRollbackFailed {
			controller.publishRollbackFailedAlarm(ctx, monitor, tripReason, rollbackFailureReason+"; persist_error="+err.Error(), now)
		}
		return false, err
	}
	if tripReason == "setpoint_tracking_deviation" {
		controller.publishTrackingDeviationAlarm(ctx, monitor, tripDetail, now)
	}
	if finalStatus == ExecutionMonitorRollbackFailed {
		controller.publishRollbackFailedAlarm(ctx, monitor, tripReason, rollbackFailureReason, now)
	}
	return finalStatus == ExecutionMonitorTrippedRolledBack, nil
}

func checkSetpointTrackingDeviation(
	monitor OptimizationExecutionMonitorRecord,
	liveState autoControlLiveState,
	siteCtx AutoControlSiteContext,
	now time.Time,
) (string, string) {
	if monitor.Status == ExecutionMonitorManualOverride || liveState.CabinetLocalManual {
		return "", ""
	}
	if monitor.AppliedSupplyTempC == nil || !finite(*monitor.AppliedSupplyTempC) {
		return "", ""
	}
	if now.UTC().Sub(monitor.StartedAt.UTC()) < DefaultSetpointConvergenceGrace {
		return "", ""
	}
	if liveState.Setpoint == nil || !isLivePointHealthyForTamper(*liveState.Setpoint, now, DefaultAutoControlMaxPointAge) {
		return "", ""
	}
	fbVal := *liveState.Setpoint.NumericValue
	threshold := math.Max(MaxSetpointDeviationC, siteCtx.LeaseDeadbandC)
	deviation := math.Abs(fbVal - *monitor.AppliedSupplyTempC)
	if deviation <= threshold+1e-9 {
		return "", ""
	}
	detail := fmt.Sprintf("applied_setpoint=%.2fC feedback_setpoint=%.2fC deviation=%.2fC threshold=%.2fC",
		*monitor.AppliedSupplyTempC, fbVal, deviation, threshold)
	return "setpoint_tracking_deviation", detail
}

func (controller *AutoController) publishTrackingDeviationAlarm(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	detail string,
	now time.Time,
) {
	controller.publishControlEvent(ctx, buildMonitorControlEvent(
		monitor,
		controller.effectiveWorkerID(),
		ControlEventSetpointTrackingDeviation,
		ControlEventSetpointTrackingDeviation,
		"MAJOR",
		"设定点跟踪偏差过大并触发自动回滚",
		fmt.Sprintf("自动优化指令执行 %s 后目标设定点仍未收敛（%s），已触发保护回滚并熔断站点自动控制租约。", DefaultSetpointConvergenceGrace, detail),
		detail,
		now,
	))
}

func (controller *AutoController) publishRollbackFailedAlarm(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	tripReason string,
	failureReason string,
	now time.Time,
) {
	detail := strings.TrimSpace(tripReason)
	if cleanFailure := strings.TrimSpace(failureReason); cleanFailure != "" {
		if detail != "" {
			detail += "; "
		}
		detail += "rollback_error=" + cleanFailure
	}
	controller.publishControlEvent(ctx, buildMonitorControlEvent(
		monitor,
		controller.effectiveWorkerID(),
		ControlEventAutoControlRollbackFailed,
		ControlEventAutoControlRollbackFailed,
		"CRITICAL",
		"自动控制回滚下发失败",
		fmt.Sprintf("执行监控 %s 触发安全熔断（%s）后未能完成自动回滚恢复，请立即人工核查现场设定点与控制权状态。", monitor.ID, detail),
		detail,
		now,
	))
}

func buildMonitorControlEvent(
	monitor OptimizationExecutionMonitorRecord,
	actorID string,
	eventKind string,
	reasonCode string,
	severity string,
	title string,
	summary string,
	detail string,
	now time.Time,
) ControlEvent {
	commandID := strings.TrimSpace(monitor.CommandIntentID)
	if commandID == "" {
		commandID = monitor.ID
	}
	correlationID := strings.TrimSpace(monitor.DecisionID)
	if correlationID == "" {
		correlationID = strings.TrimSpace(monitor.TraceID)
	}
	if correlationID == "" {
		correlationID = monitor.ID
	}
	var deviceID *string
	if trimmed := strings.TrimSpace(monitor.DeviceID); trimmed != "" {
		deviceID = &trimmed
	}
	return ControlEvent{
		SchemaVersion: 1,
		TenantID:      monitor.TenantID,
		SiteID:        monitor.SiteID,
		DeviceID:      deviceID,
		CommandID:     commandID,
		EventKind:     eventKind,
		Capability:    monitor.TargetCapability,
		Title:         title,
		Summary:       summary,
		Severity:      severity,
		ReasonCode:    reasonCode,
		Detail:        detail,
		OccurredAt:    now.UTC().Format(time.RFC3339Nano),
		ActorType:     "OPTIMIZATION_RUNTIME",
		ActorID:       actorID,
		CorrelationID: correlationID,
	}
}

func (controller *AutoController) publishControlEvent(ctx context.Context, event ControlEvent) {
	if controller == nil || controller.controlEvents == nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultControlEventTimeout)
	defer cancel()
	if err := controller.controlEvents.PublishControlEvent(pubCtx, event); err != nil {
		controller.logf("monitor %s control alarm publish failed (%s): %v", event.CommandID, event.EventKind, err)
	}
}

func normalizeExecutionMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), ExecutionModeShadow) {
		return ExecutionModeShadow
	}
	return ExecutionModeClosedLoop
}

func ensureDecisionAndTraceContext(ctx context.Context, now time.Time) (context.Context, string, string, error) {
	decisionID, err := optimizationUUIDv7(now)
	if err != nil {
		return ctx, "", "", fmt.Errorf("generate decision id: %w", err)
	}
	spanCtx := observability.SpanContextFromContext(ctx)
	if spanCtx.Valid() {
		return ctx, decisionID, spanCtx.TraceID, nil
	}
	traceID := randomNonZeroHexID(16)
	spanID := randomNonZeroHexID(8)
	traceparent := fmt.Sprintf("00-%s-%s-01", traceID, spanID)
	tracer := observability.NewTracer("optimization-service", nil)
	return observability.ContextWithRemoteParent(ctx, tracer, traceparent, ""), decisionID, traceID, nil
}

func ensureMonitorTraceContext(ctx context.Context, monitorTraceID string) (context.Context, string) {
	if clean := normalizeTraceID(monitorTraceID); clean != "" {
		if spanCtx := observability.SpanContextFromContext(ctx); spanCtx.Valid() && spanCtx.TraceID == clean {
			return ctx, clean
		}
		traceparent := fmt.Sprintf("00-%s-%s-01", clean, randomNonZeroHexID(8))
		tracer := observability.NewTracer("optimization-service", nil)
		return observability.ContextWithRemoteParent(ctx, tracer, traceparent, ""), clean
	}
	if spanCtx := observability.SpanContextFromContext(ctx); spanCtx.Valid() {
		return ctx, spanCtx.TraceID
	}
	traceID := randomNonZeroHexID(16)
	traceparent := fmt.Sprintf("00-%s-%s-01", traceID, randomNonZeroHexID(8))
	tracer := observability.NewTracer("optimization-service", nil)
	return observability.ContextWithRemoteParent(ctx, tracer, traceparent, ""), traceID
}

func normalizeTraceID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if len(trimmed) != 32 {
		return ""
	}
	allZero := true
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		isHex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')
		if !isHex {
			return ""
		}
		if ch != '0' {
			allZero = false
		}
	}
	if allZero {
		return ""
	}
	return trimmed
}

func randomNonZeroHexID(byteLen int) string {
	buf := make([]byte, byteLen)
	for {
		if _, err := rand.Read(buf); err != nil {
			// Fallback to timestamp entropy if CSPRNG fails unexpectedly.
			now := uint64(time.Now().UnixNano())
			for idx := range buf {
				buf[idx] = byte(now >> ((idx % 8) * 8))
			}
			buf[0] |= 0x01
			return hex.EncodeToString(buf)
		}
		nonZero := false
		for _, b := range buf {
			if b != 0 {
				nonZero = true
				break
			}
		}
		if nonZero {
			return hex.EncodeToString(buf)
		}
	}
}
