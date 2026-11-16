package optimization

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

const (
	DefaultExecutionMonitorWindow      = 30 * time.Minute
	DefaultMonitorTickInterval         = time.Minute
	DefaultMonitorTelemetryUnhealthy   = 5 * time.Minute
	DefaultSupplyRevalidationDriftMaxC = 0.5
	DefaultChillerPowerRatioLimit      = 0.90

	ExecutionMonitorObserving  = "OBSERVING"
	ExecutionMonitorVerified   = "VERIFIED"
	ExecutionMonitorRolledBack = "ROLLED_BACK"
	ExecutionMonitorTripped    = "TRIPPED"
)

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
	SupplyTempMaxC          float64
	StartedAt               time.Time
	WindowEndsAt            time.Time
	LastCheckedAt           *time.Time
	TelemetryUnhealthySince *time.Time
	Status                  string
	TripReason              *string
	RollbackCommandIntentID *string
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
	SiteControlMode     string
	InputMapping        InputMapping
	SetpointTarget      *SetpointCommandTarget
	Topology            StagingTopology
	RecentAutoCountHour int
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
}

type AutoControlDispatchInput struct {
	TenantID             string
	SiteID               string
	DeviceID             string
	PointID              string
	Capability           commandmodel.Capability
	Parameters           commandmodel.CommandParameters
	VerificationPointKey string
	IdempotencyKey       string
	IsRollback           bool
	CurrentValue         float64
	BusinessRevision     uint64
	ObservedAt           time.Time
}

type WorkloadCommandDispatcher interface {
	RequestCommandGrant(ctx context.Context, req commandauth.WorkloadDecisionRequest) (string, commandauth.Decision, error)
	SubmitAutoCommand(ctx context.Context, grant string, input AutoControlDispatchInput) (string, error)
}

type AutoControlStore interface {
	ActiveAutoControlLease(ctx context.Context, tenantID, siteID string, at time.Time) (*AutoControlLeaseRecord, error)
	LoadAutoControlSiteContext(ctx context.Context, tenantID, siteID, inputSnapshotID string, at time.Time) (AutoControlSiteContext, error)
	CheckSiteTelemetryHealth(ctx context.Context, tenantID, siteID string, keys []string) error
	RecordAutomatedDispatch(
		ctx context.Context,
		recommendation Recommendation,
		leaseID string,
		target SetpointCommandTarget,
		commandIntentID string,
		baselineSupplyC, appliedSupplyC, restoreSupplyC, zoneMaxC, supplyMaxC float64,
		at time.Time,
	) (OptimizationExecutionMonitorRecord, error)
	ListObservingExecutionMonitors(ctx context.Context, limit int) ([]OptimizationExecutionMonitorRecord, error)
	RecordMonitorHealthyHeartbeat(ctx context.Context, monitorID, tenantID, siteID string, checkedAt time.Time) error
	RecordMonitorTelemetryUnhealthy(ctx context.Context, monitorID, tenantID, siteID string, unhealthySince, checkedAt time.Time) error
	CompleteMonitorVerified(ctx context.Context, monitorID, tenantID, siteID string, checkedAt time.Time) error
	TripMonitorAndLease(ctx context.Context, monitor OptimizationExecutionMonitorRecord, newStatus, tripReason string, rollbackCommandIntentID *string, trippedAt time.Time) error
}

type AutoControlTelemetryReader interface {
	ReadLiveAutoControlObservation(ctx context.Context, tenantID, siteID string, mapping InputMapping, topology StagingTopology, at time.Time) (AutoControlLiveObservation, error)
}

type AutoDispatchOutcome struct {
	Dispatched      bool
	SkipReason      string
	CommandIntentID string
	MonitorID       string
}

type MonitorPassSummary struct {
	Checked    int
	Verified   int
	RolledBack int
	Tripped    int
}

type AutoController struct {
	store      AutoControlStore
	telemetry  AutoControlTelemetryReader
	dispatcher WorkloadCommandDispatcher
	clock      Clock
	logf       func(string, ...any)
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
	return &AutoController{
		store:      store,
		telemetry:  telemetry,
		dispatcher: dispatcher,
		clock:      clock,
		logf:       logf,
	}, nil
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
	if recommendation.CommandIntentID != "" {
		return AutoDispatchOutcome{SkipReason: "ALREADY_DISPATCHED"}, nil
	}
	if recommendation.Approval == intelligencemodel.RecommendationRejected {
		return AutoDispatchOutcome{SkipReason: "RECOMMENDATION_REJECTED"}, nil
	}

	lease, err := controller.store.ActiveAutoControlLease(ctx, recommendation.TenantID, recommendation.SiteID, now)
	if err != nil {
		return AutoDispatchOutcome{}, err
	}
	if lease == nil || lease.Status != "ACTIVE" || !lease.ExpiresAt.After(now) {
		return AutoDispatchOutcome{SkipReason: "MANUAL_CONFIRM"}, nil
	}

	validUntil := recommendation.EffectiveValidUntil()
	if validUntil.IsZero() || !validUntil.After(now) {
		return AutoDispatchOutcome{SkipReason: "RECOMMENDATION_EXPIRED"}, nil
	}

	targetCapability := commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	if !slices.Contains(lease.AllowedCapabilities, string(targetCapability)) {
		return AutoDispatchOutcome{SkipReason: "CAPABILITY_NOT_LEASED"}, nil
	}
	candidateSupplyC, ok := extractNumber(recommendation.Candidate, "supplyTempC")
	if !ok {
		return AutoDispatchOutcome{SkipReason: "CANDIDATE_SUPPLY_TEMP_MISSING"}, nil
	}
	baselineSupplyC, ok := extractNumber(recommendation.Baseline, "supplyTempC")
	if !ok {
		return AutoDispatchOutcome{SkipReason: "BASELINE_SUPPLY_TEMP_MISSING"}, nil
	}
	restoreSupplyC, hasRestore := extractNumber(recommendation.RollbackPlan, "restoreSupplyTempC")
	if !hasRestore {
		restoreSupplyC = baselineSupplyC
	}

	siteCtx, err := controller.store.LoadAutoControlSiteContext(ctx, recommendation.TenantID, recommendation.SiteID, recommendation.InputSnapshotID, now)
	if err != nil {
		return AutoDispatchOutcome{}, err
	}
	if siteCtx.RecentAutoCountHour >= lease.MaxCommandsPerHour {
		return AutoDispatchOutcome{SkipReason: "RATE_LIMIT_REACHED"}, nil
	}
	if siteCtx.SiteControlMode != "" && siteCtx.SiteControlMode != SiteControlModeRemoteAuto {
		return AutoDispatchOutcome{SkipReason: "SITE_CONTROL_EXIT_ACTIVE"}, nil
	}
	if siteCtx.SetpointTarget == nil || siteCtx.SetpointTarget.AssetLocked || hasAnyAssetLock(siteCtx.Topology.AssetControlLocks) {
		return AutoDispatchOutcome{SkipReason: "ASSET_CONTROL_LOCKED"}, nil
	}
	if strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) == "" || len(siteCtx.InputMapping.effectiveZoneKeys()) == 0 {
		return AutoDispatchOutcome{SkipReason: "INPUT_MAPPING_INCOMPLETE"}, nil
	}

	allKeys := append([]string{siteCtx.InputMapping.SupplyTemperatureKey}, siteCtx.InputMapping.effectiveZoneKeys()...)
	if err := controller.store.CheckSiteTelemetryHealth(ctx, recommendation.TenantID, recommendation.SiteID, allKeys); err != nil {
		return AutoDispatchOutcome{SkipReason: "TELEMETRY_UNHEALTHY"}, nil
	}

	live, err := controller.telemetry.ReadLiveAutoControlObservation(ctx, recommendation.TenantID, recommendation.SiteID, siteCtx.InputMapping, siteCtx.Topology, now)
	if err != nil {
		return AutoDispatchOutcome{}, err
	}
	if live.SupplyErr != nil || live.SupplyTemp == nil || live.ZoneErr != nil || len(live.ZoneTemps) == 0 || !live.ChillersHealthy {
		return AutoDispatchOutcome{SkipReason: "TELEMETRY_REVALIDATION_FAILED"}, nil
	}
	if math.Abs(live.SupplyTemp.Value-baselineSupplyC) > DefaultSupplyRevalidationDriftMaxC+1e-9 {
		return AutoDispatchOutcome{SkipReason: "SUPPLY_TEMP_DRIFT_EXCEEDED"}, nil
	}
	for _, zone := range live.ZoneTemps {
		if zone.Value > lease.ZoneTempMaxC {
			return AutoDispatchOutcome{SkipReason: "ZONE_TEMP_LIMIT_EXCEEDED"}, nil
		}
	}
	if live.RunningChillers < lease.MinRunningChillers {
		return AutoDispatchOutcome{SkipReason: "MIN_RUNNING_CHILLERS_NOT_MET"}, nil
	}
	if live.ChillerPowerTrip != nil {
		return AutoDispatchOutcome{SkipReason: "CHILLER_POWER_RATIO_EXCEEDED"}, nil
	}
	if candidateSupplyC < lease.SupplyTempMinC || candidateSupplyC > lease.SupplyTempMaxC {
		return AutoDispatchOutcome{SkipReason: "TARGET_OUTSIDE_LEASE_BOUNDS"}, nil
	}
	if math.Abs(candidateSupplyC-live.SupplyTemp.Value) > lease.MaxSetpointStepC+1e-9 {
		return AutoDispatchOutcome{SkipReason: "SETPOINT_STEP_EXCEEDED"}, nil
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
		return AutoDispatchOutcome{SkipReason: "REVALIDATION_GATE_REJECTED"}, nil
	}

	profile, ok := commandmodel.CapabilityProfileFor(targetCapability)
	if !ok {
		return AutoDispatchOutcome{SkipReason: "CAPABILITY_UNSUPPORTED"}, nil
	}
	currentVal := live.SupplyTemp.Value
	params := commandmodel.CommandParameters{commandmodel.ParameterSetpointC: candidateSupplyC}
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
		return AutoDispatchOutcome{}, err
	}
	if !decision.Allowed || grant == "" {
		return AutoDispatchOutcome{SkipReason: "IAM_DENIED:" + string(decision.ReasonCode)}, nil
	}

	verificationKey := strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey)
	if verificationKey == "" {
		verificationKey = siteCtx.InputMapping.SupplyTemperatureKey
	}
	pointRevision := live.SupplyTemp.PointRevision
	if pointRevision == 0 {
		pointRevision = 1
	}
	commandIntentID, err := controller.dispatcher.SubmitAutoCommand(ctx, grant, AutoControlDispatchInput{
		TenantID:             recommendation.TenantID,
		SiteID:               recommendation.SiteID,
		DeviceID:             siteCtx.SetpointTarget.DeviceID,
		PointID:              siteCtx.SetpointTarget.PointID,
		Capability:           targetCapability,
		Parameters:           params,
		VerificationPointKey: verificationKey,
		IdempotencyKey:       "auto-opt:" + recommendation.ID,
		IsRollback:           false,
		CurrentValue:         currentVal,
		BusinessRevision:     pointRevision,
		ObservedAt:           live.SupplyTemp.SampledAt,
	})
	if err != nil {
		return AutoDispatchOutcome{}, err
	}

	monitor, err := controller.store.RecordAutomatedDispatch(
		ctx, recommendation, lease.ID, *siteCtx.SetpointTarget, commandIntentID,
		baselineSupplyC, candidateSupplyC, restoreSupplyC, lease.ZoneTempMaxC, lease.SupplyTempMaxC, now,
	)
	if err != nil {
		return AutoDispatchOutcome{}, err
	}
	controller.logf("optimization auto-control dispatched recommendation=%s site=%s command=%s monitor=%s targetC=%.2f",
		recommendation.ID, recommendation.SiteID, commandIntentID, monitor.ID, candidateSupplyC)
	return AutoDispatchOutcome{
		Dispatched:      true,
		CommandIntentID: commandIntentID,
		MonitorID:       monitor.ID,
	}, nil
}

// RunMonitorPass checks every OBSERVING execution monitor against live zone/supply temperatures,
// chiller rated power ratios, and telemetry health, issuing rollback and tripping the lease on violation.
func (controller *AutoController) RunMonitorPass(ctx context.Context, now time.Time) (MonitorPassSummary, error) {
	if controller == nil {
		return MonitorPassSummary{}, nil
	}
	if now.IsZero() {
		now = controller.clock().UTC()
	} else {
		now = now.UTC()
	}
	monitors, err := controller.store.ListObservingExecutionMonitors(ctx, 100)
	if err != nil {
		return MonitorPassSummary{}, err
	}
	var summary MonitorPassSummary
	for _, monitor := range monitors {
		summary.Checked++
		siteCtx, ctxErr := controller.store.LoadAutoControlSiteContext(ctx, monitor.TenantID, monitor.SiteID, "", now)
		if ctxErr != nil {
			controller.logf("monitor %s site context error: %v", monitor.ID, ctxErr)
			continue
		}

		telemetryHealthy := true
		allKeys := make([]string, 0, 1+len(siteCtx.InputMapping.effectiveZoneKeys()))
		if strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) != "" {
			allKeys = append(allKeys, siteCtx.InputMapping.SupplyTemperatureKey)
		}
		allKeys = append(allKeys, siteCtx.InputMapping.effectiveZoneKeys()...)
		if len(allKeys) == 0 || strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey) == "" || len(siteCtx.InputMapping.effectiveZoneKeys()) == 0 {
			telemetryHealthy = false
		}
		if telemetryHealthy {
			if healthErr := controller.store.CheckSiteTelemetryHealth(ctx, monitor.TenantID, monitor.SiteID, allKeys); healthErr != nil {
				telemetryHealthy = false
			}
		}

		var live AutoControlLiveObservation
		if telemetryHealthy {
			var obsErr error
			live, obsErr = controller.telemetry.ReadLiveAutoControlObservation(ctx, monitor.TenantID, monitor.SiteID, siteCtx.InputMapping, siteCtx.Topology, now)
			if obsErr != nil || live.SupplyErr != nil || live.SupplyTemp == nil || live.ZoneErr != nil || len(live.ZoneTemps) == 0 || !live.ChillersHealthy {
				telemetryHealthy = false
			}
		}

		if !telemetryHealthy {
			unhealthySince := now
			if monitor.TelemetryUnhealthySince != nil {
				unhealthySince = monitor.TelemetryUnhealthySince.UTC()
			}
			if monitor.TelemetryUnhealthySince != nil && now.Sub(unhealthySince) > DefaultMonitorTelemetryUnhealthy {
				rolledBack, tripErr := controller.tripAndRollback(ctx, monitor, siteCtx, live, "telemetry_unhealthy_over_5m", now)
				if tripErr != nil {
					controller.logf("monitor %s trip on telemetry failure error: %v", monitor.ID, tripErr)
					continue
				}
				if rolledBack {
					summary.RolledBack++
				} else {
					summary.Tripped++
				}
				continue
			}
			if err := controller.store.RecordMonitorTelemetryUnhealthy(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, unhealthySince, now); err != nil {
				controller.logf("monitor %s unhealthy timestamp update failed: %v", monitor.ID, err)
			}
			continue
		}

		tripReason := ""
		if live.SupplyTemp.Value > monitor.SupplyTempMaxC {
			tripReason = fmt.Sprintf("supply_temperature_exceeded_max (%.2fC > %.2fC)", live.SupplyTemp.Value, monitor.SupplyTempMaxC)
		}
		if tripReason == "" {
			for key, zone := range live.ZoneTemps {
				if zone.Value > monitor.ZoneTempMaxC {
					tripReason = fmt.Sprintf("zone_temperature_exceeded_max %s (%.2fC > %.2fC)", key, zone.Value, monitor.ZoneTempMaxC)
					break
				}
			}
		}
		if tripReason == "" && live.ChillerPowerTrip != nil {
			tripReason = fmt.Sprintf("chiller_power_exceeded_90pct %s (%.1fkW > 0.90*%.1fkW)",
				live.ChillerPowerTrip.AssetCode, live.ChillerPowerTrip.PowerKW, live.ChillerPowerTrip.RatedPowerKW)
		}

		if tripReason != "" {
			rolledBack, tripErr := controller.tripAndRollback(ctx, monitor, siteCtx, live, tripReason, now)
			if tripErr != nil {
				controller.logf("monitor %s trip on boundary violation failed: %v", monitor.ID, tripErr)
				continue
			}
			if rolledBack {
				summary.RolledBack++
			} else {
				summary.Tripped++
			}
			continue
		}

		if !now.Before(monitor.WindowEndsAt) {
			if err := controller.store.CompleteMonitorVerified(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, now); err != nil {
				controller.logf("monitor %s verify completion failed: %v", monitor.ID, err)
				continue
			}
			summary.Verified++
			continue
		}

		if err := controller.store.RecordMonitorHealthyHeartbeat(ctx, monitor.ID, monitor.TenantID, monitor.SiteID, now); err != nil {
			controller.logf("monitor %s heartbeat update failed: %v", monitor.ID, err)
		}
	}
	return summary, nil
}

func (controller *AutoController) tripAndRollback(
	ctx context.Context,
	monitor OptimizationExecutionMonitorRecord,
	siteCtx AutoControlSiteContext,
	live AutoControlLiveObservation,
	tripReason string,
	now time.Time,
) (bool, error) {
	var rollbackID *string
	finalStatus := ExecutionMonitorTripped

	if monitor.RestoreSupplyTempC != nil && siteCtx.SetpointTarget != nil &&
		(siteCtx.SiteControlMode == "" || siteCtx.SiteControlMode == SiteControlModeRemoteAuto) &&
		!siteCtx.SetpointTarget.AssetLocked {
		restoreTemp := *monitor.RestoreSupplyTempC
		targetCapability := commandmodel.Capability(monitor.TargetCapability)
		if profile, ok := commandmodel.CapabilityProfileFor(targetCapability); ok {
			currentVal := restoreTemp
			if live.SupplyTemp != nil {
				currentVal = live.SupplyTemp.Value
			} else if monitor.AppliedSupplyTempC != nil {
				currentVal = *monitor.AppliedSupplyTempC
			}
			observedAt := monitor.StartedAt
			var pointRevision uint64 = 1
			if live.SupplyTemp != nil {
				if !live.SupplyTemp.SampledAt.IsZero() {
					observedAt = live.SupplyTemp.SampledAt
				}
				if live.SupplyTemp.PointRevision > 0 {
					pointRevision = live.SupplyTemp.PointRevision
				}
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
				CurrentValue:       &currentVal,
				IsRollback:         true,
			})
			if err == nil && decision.Allowed && grant != "" {
				verificationKey := strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey)
				if verificationKey == "" {
					verificationKey = siteCtx.InputMapping.SupplyTemperatureKey
				}
				if verificationKey == "" {
					verificationKey = "supply_water_temperature"
				}
				submittedID, submitErr := controller.dispatcher.SubmitAutoCommand(ctx, grant, AutoControlDispatchInput{
					TenantID:             monitor.TenantID,
					SiteID:               monitor.SiteID,
					DeviceID:             monitor.DeviceID,
					PointID:              siteCtx.SetpointTarget.PointID,
					Capability:           targetCapability,
					Parameters:           params,
					VerificationPointKey: verificationKey,
					IdempotencyKey:       "auto-rollback:" + monitor.ID,
					IsRollback:           true,
					CurrentValue:         currentVal,
					BusinessRevision:     pointRevision,
					ObservedAt:           observedAt,
				})
				if submitErr == nil && submittedID != "" {
					rollbackID = &submittedID
					finalStatus = ExecutionMonitorRolledBack
				} else if submitErr != nil {
					controller.logf("monitor %s rollback command submit failed: %v", monitor.ID, submitErr)
				}
			} else if err != nil {
				controller.logf("monitor %s rollback grant failed: %v", monitor.ID, err)
			}
		}
	}

	if err := controller.store.TripMonitorAndLease(ctx, monitor, finalStatus, tripReason, rollbackID, now); err != nil {
		return false, err
	}
	return finalStatus == ExecutionMonitorRolledBack, nil
}

func extractNumber(fields map[string]any, key string) (float64, bool) {
	if len(fields) == 0 {
		return 0, false
	}
	raw, ok := fields[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case float32:
		f := float64(v)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func hasAnyAssetLock(locks map[string]AssetControlLockInfo) bool {
	for _, lock := range locks {
		if lock.LockMode != "" && lock.LockMode != AssetControlLockUnlocked {
			return true
		}
	}
	return false
}
