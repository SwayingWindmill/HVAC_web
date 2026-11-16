package optimization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/nameplate"
	"github.com/quanlaihe/hvac-web/libs/planttwin"
)

const (
	// ConfirmationModeManual marks Decision 3A staging recommendations as strictly
	// human-confirmed: supervisor review and TOTP second-factor confirmation are required
	// before any command intent is issued.
	ConfirmationModeManual = "MANUAL_CONFIRM"

	RecommendationKindChillerStageDown  = "CHILLER_STAGE_DOWN"
	RecommendationKindIdleAuxiliaryStop = "IDLE_AUXILIARY_STOP"
	RecommendationKindOffHoursStop      = "OFF_HOURS_STOP"

	SuggestedCapabilityStopUnitSequence = "STOP_UNIT_SEQUENCE"
	SuggestedCapabilityStop             = "STOP"

	AssetTypeChiller      = "CHILLER"
	AssetTypeCHWP         = "CHILLED_WATER_PUMP"
	AssetTypeCWP          = "COOLING_WATER_PUMP"
	AssetTypeCoolingTower = "COOLING_TOWER"

	HydraulicEdgeServes = "SERVES"

	RunStateRunning  = "RUNNING"
	RunStateStopped  = "STOPPED"
	RunStateStarting = "STARTING"
	RunStateUnknown  = "UNKNOWN"

	SiteControlModeRemoteAuto       = "REMOTE_AUTO"
	SiteControlModeManualOverride   = "MANUAL_OVERRIDE"
	SiteControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

	AssetControlLockUnlocked = "UNLOCKED"

	DefaultHeadroomReserveFraction      = 0.15
	DefaultStageDownDeadbandFraction    = 0.05
	DefaultMinimumPowerSavingKW         = 5.0
	DefaultStageDownDwellDuration       = 15 * time.Minute
	DefaultMinChillerRunDuration        = 30 * time.Minute
	DefaultMaxStageDownSupplyTempC      = 12.0
	DefaultIdleAuxiliaryDebounce        = 15 * time.Minute
	DefaultChillerThermalDeltaThreshold = 0.8
	DefaultMaxTelemetryAge              = 10 * time.Minute
	DefaultStagingShiftHours            = 8.0
	DefaultChillerRunningPowerKW        = 5.0
	DefaultAuxiliaryRunningPowerKW      = 0.5
)

var (
	// ErrStagingEvaluationNotFound reports that no staging evaluator or site staging source is active.
	ErrStagingEvaluationNotFound = errors.New("optimization staging evaluation not found")
	// ErrStagingTopologyUnavailable reports that site asset/pairing catalog cannot be loaded.
	ErrStagingTopologyUnavailable = errors.New("optimization staging topology unavailable")
)

// StagingPolicy sets the physical reserve margins, hysteresis, debounce and telemetry freshness
// limits for sensor-honest chiller staging and idle auxiliary recommendations.
type StagingPolicy struct {
	HeadroomReserveFraction   float64       `json:"headroomReserveFraction"`
	StageDownDeadbandFraction float64       `json:"stageDownDeadbandFraction"`
	MinimumPowerSavingKW      float64       `json:"minimumPowerSavingKW"`
	StageDownDwellDuration    time.Duration `json:"stageDownDwellDuration"`
	MinChillerRunDuration     time.Duration `json:"minChillerRunDuration"`
	MaxStageDownSupplyTempC   float64       `json:"maxStageDownSupplyTempC"`
	IdleAuxiliaryDebounce     time.Duration `json:"idleAuxiliaryDebounce"`
	MaxTelemetryAge           time.Duration `json:"maxTelemetryAge"`
	ShiftHours                float64       `json:"shiftHours"`
	ChillerRunningPowerKW     float64       `json:"chillerRunningPowerKW"`
	AuxiliaryRunningPowerKW   float64       `json:"auxiliaryRunningPowerKW"`
}

// DefaultStagingPolicy returns safe defaults aligned with Decision 3A.
func DefaultStagingPolicy() StagingPolicy {
	return StagingPolicy{
		HeadroomReserveFraction:   DefaultHeadroomReserveFraction,
		StageDownDeadbandFraction: DefaultStageDownDeadbandFraction,
		MinimumPowerSavingKW:      DefaultMinimumPowerSavingKW,
		StageDownDwellDuration:    DefaultStageDownDwellDuration,
		MinChillerRunDuration:     DefaultMinChillerRunDuration,
		MaxStageDownSupplyTempC:   DefaultMaxStageDownSupplyTempC,
		IdleAuxiliaryDebounce:     DefaultIdleAuxiliaryDebounce,
		MaxTelemetryAge:           DefaultMaxTelemetryAge,
		ShiftHours:                DefaultStagingShiftHours,
		ChillerRunningPowerKW:     DefaultChillerRunningPowerKW,
		AuxiliaryRunningPowerKW:   DefaultAuxiliaryRunningPowerKW,
	}
}

func (policy StagingPolicy) normalized() (StagingPolicy, error) {
	if policy == (StagingPolicy{}) {
		return DefaultStagingPolicy(), nil
	}
	if policy.HeadroomReserveFraction < 0 || !finite(policy.HeadroomReserveFraction) {
		return StagingPolicy{}, errors.New("headroomReserveFraction must be non-negative")
	}
	if policy.HeadroomReserveFraction == 0 {
		policy.HeadroomReserveFraction = DefaultHeadroomReserveFraction
	}
	if policy.StageDownDeadbandFraction < 0 || !finite(policy.StageDownDeadbandFraction) {
		return StagingPolicy{}, errors.New("stageDownDeadbandFraction must be non-negative")
	}
	if policy.StageDownDeadbandFraction == 0 {
		policy.StageDownDeadbandFraction = DefaultStageDownDeadbandFraction
	}
	if policy.MinimumPowerSavingKW < 0 || !finite(policy.MinimumPowerSavingKW) {
		return StagingPolicy{}, errors.New("minimumPowerSavingKW must be non-negative")
	}
	if policy.MinimumPowerSavingKW == 0 {
		policy.MinimumPowerSavingKW = DefaultMinimumPowerSavingKW
	}
	if policy.StageDownDwellDuration < 0 {
		return StagingPolicy{}, errors.New("stageDownDwellDuration must be non-negative")
	}
	if policy.StageDownDwellDuration == 0 {
		policy.StageDownDwellDuration = DefaultStageDownDwellDuration
	}
	if policy.MinChillerRunDuration < 0 {
		return StagingPolicy{}, errors.New("minChillerRunDuration must be non-negative")
	}
	if policy.MinChillerRunDuration == 0 {
		policy.MinChillerRunDuration = DefaultMinChillerRunDuration
	}
	if policy.MaxStageDownSupplyTempC < 0 || !finite(policy.MaxStageDownSupplyTempC) {
		return StagingPolicy{}, errors.New("maxStageDownSupplyTempC must be non-negative")
	}
	if policy.MaxStageDownSupplyTempC == 0 {
		policy.MaxStageDownSupplyTempC = DefaultMaxStageDownSupplyTempC
	}
	if policy.IdleAuxiliaryDebounce < 0 {
		return StagingPolicy{}, errors.New("idleAuxiliaryDebounce must be non-negative")
	}
	if policy.IdleAuxiliaryDebounce == 0 {
		policy.IdleAuxiliaryDebounce = DefaultIdleAuxiliaryDebounce
	}
	if policy.MaxTelemetryAge < 0 {
		return StagingPolicy{}, errors.New("maxTelemetryAge must be non-negative")
	}
	if policy.MaxTelemetryAge == 0 {
		policy.MaxTelemetryAge = DefaultMaxTelemetryAge
	}
	if policy.ShiftHours < 0 || !finite(policy.ShiftHours) {
		return StagingPolicy{}, errors.New("shiftHours must be non-negative")
	}
	if policy.ShiftHours == 0 {
		policy.ShiftHours = DefaultStagingShiftHours
	}
	if policy.ChillerRunningPowerKW < 0 || !finite(policy.ChillerRunningPowerKW) {
		return StagingPolicy{}, errors.New("chillerRunningPowerKW must be non-negative")
	}
	if policy.ChillerRunningPowerKW == 0 {
		policy.ChillerRunningPowerKW = DefaultChillerRunningPowerKW
	}
	if policy.AuxiliaryRunningPowerKW < 0 || !finite(policy.AuxiliaryRunningPowerKW) {
		return StagingPolicy{}, errors.New("auxiliaryRunningPowerKW must be non-negative")
	}
	if policy.AuxiliaryRunningPowerKW == 0 {
		policy.AuxiliaryRunningPowerKW = DefaultAuxiliaryRunningPowerKW
	}
	return policy, nil
}

// ChillerMeterBinding links a physical CHILLER asset to its dedicated power meter and nameplate.
type ChillerMeterBinding struct {
	AssetID                string   `json:"assetId"`
	AssetCode              string   `json:"assetCode"`
	AssetName              string   `json:"assetName"`
	MeterDeviceID          string   `json:"meterDeviceId"`
	MeterSourceKey         string   `json:"meterSourceKey"`
	BoundDeviceIDs         []string `json:"boundDeviceIds,omitempty"`
	RatedCapacityKW        float64  `json:"ratedCapacityKW,omitempty"`
	RatedPowerKW           float64  `json:"ratedPowerKW,omitempty"`
	FeederAuxiliaryPowerKW float64  `json:"feederAuxiliaryPowerKW,omitempty"`
}

// AssetControlLockInfo mirrors the per-asset lock from core_registry.asset_control_locks.
type AssetControlLockInfo struct {
	LockMode string `json:"lockMode"`
	Reason   string `json:"reason,omitempty"`
}

// TelemetryValue is one timestamped sensor or meter observation with explicit quality.
type TelemetryValue struct {
	DeviceID  string    `json:"deviceId,omitempty"`
	SourceKey string    `json:"sourceKey,omitempty"`
	PointKey  string    `json:"pointKey,omitempty"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit,omitempty"`
	Quality   string    `json:"quality"`
	SampledAt time.Time `json:"sampledAt"`
}

// RunStateSample is one discrete equipment state sample in chronological evaluation windows.
type RunStateSample struct {
	State     string    `json:"state"`
	Quality   string    `json:"quality"`
	SampledAt time.Time `json:"sampledAt"`
}

// ChillerUnitInput carries live meter/temperature readings, runtime state and nameplate metadata
// for a single CHILLER asset.
type ChillerUnitInput struct {
	AssetID                string           `json:"assetId"`
	AssetCode              string           `json:"assetCode,omitempty"`
	AssetName              string           `json:"assetName,omitempty"`
	MeterDeviceID          string           `json:"meterDeviceId,omitempty"`
	MeterSourceKey         string           `json:"meterSourceKey,omitempty"`
	BoundDeviceIDs         []string         `json:"boundDeviceIds,omitempty"`
	RatedCapacityKW        float64          `json:"ratedCapacityKW,omitempty"`
	RatedPowerKW           float64          `json:"ratedPowerKW,omitempty"`
	FeederAuxiliaryPowerKW float64          `json:"feederAuxiliaryPowerKW,omitempty"`
	RunState               string           `json:"runState,omitempty"`
	RunStateQuality        string           `json:"runStateQuality,omitempty"`
	RunStateAt             time.Time        `json:"runStateAt,omitempty"`
	StoppedSince           *time.Time       `json:"stoppedSince,omitempty"`
	RunningSince           *time.Time       `json:"runningSince,omitempty"`
	RunStateHistory        []RunStateSample `json:"runStateHistory,omitempty"`
	Power                  *TelemetryValue  `json:"power,omitempty"`
	ChwEnteringC           *TelemetryValue  `json:"chwEnteringC,omitempty"`
	ChwLeavingC            *TelemetryValue  `json:"chwLeavingC,omitempty"`
	CwLeavingC             *TelemetryValue  `json:"cwLeavingC,omitempty"`
}

// HydraulicEdge represents a directional pairing between an auxiliary asset and the chiller(s)
// it serves (role "SERVES").
type HydraulicEdge struct {
	FromAssetID string `json:"fromAssetId"`
	ToAssetID   string `json:"toAssetId"`
	Role        string `json:"role"`
}

// AuxiliaryUnitInput carries live runtime/power telemetry and pairing metadata for CHWP, CWP, or
// COOLING_TOWER assets.
type AuxiliaryUnitInput struct {
	AssetID          string          `json:"assetId"`
	AssetCode        string          `json:"assetCode,omitempty"`
	AssetName        string          `json:"assetName,omitempty"`
	AssetType        string          `json:"assetType"`
	DeviceID         string          `json:"deviceId,omitempty"`
	DeviceSourceKey  string          `json:"deviceSourceKey,omitempty"`
	BoundDeviceIDs   []string        `json:"boundDeviceIds,omitempty"`
	RatedPowerKW     float64         `json:"ratedPowerKW,omitempty"`
	RunState         string          `json:"runState,omitempty"`
	RunStateQuality  string          `json:"runStateQuality,omitempty"`
	RunStateAt       time.Time       `json:"runStateAt,omitempty"`
	Power            *TelemetryValue `json:"power,omitempty"`
	ServesChillerIDs []string        `json:"servesChillerIds,omitempty"`
}

// StagingTopology supplies authoritative asset, meter-binding, lock state and hydraulic SERVES edge definitions
// for a Site.
type StagingTopology struct {
	CoolingLoadDeviceID string                          `json:"coolingLoadDeviceId,omitempty"`
	SiteControlMode     string                          `json:"siteControlMode,omitempty"`
	SiteControlReason   string                          `json:"siteControlReason,omitempty"`
	AssetControlLocks   map[string]AssetControlLockInfo `json:"assetControlLocks,omitempty"`
	CommandPointIDs     map[string]string               `json:"commandPointIds,omitempty"`
	OperatingSchedule   *planttwin.OperatingSchedule    `json:"operatingSchedule,omitempty"`
	Chillers            []ChillerMeterBinding           `json:"chillers"`
	Auxiliaries         []AuxiliaryUnitBinding          `json:"auxiliaries,omitempty"`
	Edges               []HydraulicEdge                 `json:"edges,omitempty"`
	Twin                *planttwin.SiteTwin             `json:"-"`
}

// AuxiliaryUnitBinding connects an auxiliary asset record to its telemetry device and served chillers.
type AuxiliaryUnitBinding struct {
	AssetID          string   `json:"assetId"`
	AssetCode        string   `json:"assetCode,omitempty"`
	AssetName        string   `json:"assetName,omitempty"`
	AssetType        string   `json:"assetType"`
	DeviceID         string   `json:"deviceId,omitempty"`
	DeviceSourceKey  string   `json:"deviceSourceKey,omitempty"`
	BoundDeviceIDs   []string `json:"boundDeviceIds,omitempty"`
	RatedPowerKW     float64  `json:"ratedPowerKW,omitempty"`
	ServesChillerIDs []string `json:"servesChillerIds,omitempty"`
}

// StagingSiteCatalog reads authoritative Site asset/meter/edge topology when available.
type StagingSiteCatalog interface {
	StagingTopology(ctx context.Context, tenantID, siteID string, at time.Time) (StagingTopology, error)
}

// StagingSnapshot holds the sensor-honest inputs needed for staging evaluation without
// requiring zone_temperature or writable chilled-water setpoints.
type StagingSnapshot struct {
	TenantID          string                          `json:"tenantId"`
	SiteID            string                          `json:"siteId"`
	EvaluatedAt       time.Time                       `json:"evaluatedAt"`
	SiteControlMode   string                          `json:"siteControlMode,omitempty"`
	SiteControlReason string                          `json:"siteControlReason,omitempty"`
	AssetControlLocks map[string]AssetControlLockInfo `json:"assetControlLocks,omitempty"`
	CommandPointIDs   map[string]string               `json:"commandPointIds,omitempty"`
	OperatingSchedule *planttwin.OperatingSchedule    `json:"operatingSchedule,omitempty"`
	PlantCoolingLoad  *TelemetryValue                 `json:"plantCoolingLoad,omitempty"`
	PlantLoadHistory  []TelemetryValue                `json:"plantLoadHistory,omitempty"`
	HeaderSupplyC     *TelemetryValue                 `json:"headerSupplyC,omitempty"`
	Chillers          []ChillerUnitInput              `json:"chillers"`
	Auxiliaries       []AuxiliaryUnitInput            `json:"auxiliaries,omitempty"`
	Edges             []HydraulicEdge                 `json:"edges,omitempty"`
	ChillerFits       []ChillerFit                    `json:"chillerFits,omitempty"`
	Twin              *planttwin.SiteTwin             `json:"-"`
}

// StagingSnapshotQuery scopes a live staging snapshot read in ClickHouse.
type StagingSnapshotQuery struct {
	TenantID string
	SiteID   string
	At       time.Time
	Policy   StagingPolicy
	Topology *StagingTopology
	Twin     *planttwin.SiteTwin
}

// StagingSnapshotReader reads live plant cooling load, chiller power/temperature/run-state and
// auxiliary runtime observations up to query.At.
type StagingSnapshotReader interface {
	ReadStagingSnapshot(ctx context.Context, query StagingSnapshotQuery) (StagingSnapshot, error)
}

// StagingRecommendationDraft is a non-executable, human-confirmed energy linkage suggestion.
type StagingRecommendationDraft struct {
	ID                       string   `json:"id"`
	Kind                     string   `json:"kind"`
	ConfirmationMode         string   `json:"confirmationMode"`
	RequiresSupervisorReview bool     `json:"requiresSupervisorReview"`
	RequiresTOTP             bool     `json:"requiresTotp"`
	SuggestedCapability      string   `json:"suggestedCapability"`
	SuggestedCommandPointID  string   `json:"suggestedCommandPointId,omitempty"`
	TargetAssetID            string   `json:"targetAssetId"`
	TargetAssetCode          string   `json:"targetAssetCode,omitempty"`
	TargetAssetName          string   `json:"targetAssetName,omitempty"`
	TargetAssetType          string   `json:"targetAssetType"`
	PredictedPowerSavingKW   float64  `json:"predictedPowerSavingKW"`
	EstimatedHourlySavingKWh float64  `json:"estimatedHourlySavingKWh"`
	EstimatedShiftSavingKWh  float64  `json:"estimatedShiftSavingKWh"`
	ShiftHours               float64  `json:"shiftHours"`
	PowerSavingBasis         string   `json:"powerSavingBasis,omitempty"`
	CurrentTotalPowerKW      *float64 `json:"currentTotalPowerKW,omitempty"`
	ProjectedTotalPowerKW    *float64 `json:"projectedTotalPowerKW,omitempty"`
	CurrentCoolingLoadKW     *float64 `json:"currentCoolingLoadKW,omitempty"`
	RemainingCapacityKW      *float64 `json:"remainingCapacityKW,omitempty"`
	HeadroomMarginFraction   *float64 `json:"headroomMarginFraction,omitempty"`
	COPBefore                *float64 `json:"copBefore,omitempty"`
	ProjectedCOPAfter        *float64 `json:"projectedCopAfter,omitempty"`
	ContinuousIdleSeconds    *int64   `json:"continuousIdleSeconds,omitempty"`
	ContinuousIdleMinutes    *float64 `json:"continuousIdleMinutes,omitempty"`
	ServedChillerIDs         []string `json:"servedChillerIds,omitempty"`
	InterlockSafetyStatement string   `json:"interlockSafetyStatement,omitempty"`
	Extrapolated             bool     `json:"extrapolated,omitempty"`
	Rationale                string   `json:"rationale"`
	Preconditions            []string `json:"preconditions"`
}

// StagingDiagnostic records why a staging check emitted, suppressed or skipped a recommendation.
type StagingDiagnostic struct {
	Scope     string `json:"scope"`
	AssetID   string `json:"assetId,omitempty"`
	AssetCode string `json:"assetCode,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail"`
}

// StagingEvaluation is the combined Decision 3A result for chiller stage-down and idle auxiliary
// shutdown checks.
type StagingEvaluation struct {
	TenantID         string                       `json:"tenantId"`
	SiteID           string                       `json:"siteId"`
	EvaluatedAt      time.Time                    `json:"evaluatedAt"`
	ConfirmationMode string                       `json:"confirmationMode"`
	SiteControlMode  string                       `json:"siteControlMode,omitempty"`
	Recommendations  []StagingRecommendationDraft `json:"recommendations"`
	Diagnostics      []StagingDiagnostic          `json:"diagnostics"`
}

// StagingEvaluator combines 14-day COP fitting and live staging snapshot evaluation independently
// of zone_temperature or writable chilled-water setpoints.
type StagingEvaluator struct {
	History    PlantHistory
	Snapshot   StagingSnapshotReader
	Catalog    StagingSiteCatalog
	TwinLoader planttwin.Loader
	Policy     StagingPolicy
}

func loadTwinAndTopology(
	ctx context.Context,
	tenantID, siteID string,
	at time.Time,
	loader planttwin.Loader,
	catalog StagingSiteCatalog,
) (*planttwin.SiteTwin, *StagingTopology, error) {
	if loader == nil {
		if asLoader, ok := catalog.(planttwin.Loader); ok {
			loader = asLoader
		}
	}
	if loader != nil {
		twin, err := loader.LoadSiteTwin(ctx, planttwin.SiteScope{TenantID: tenantID, SiteID: siteID}, at)
		if err != nil {
			return nil, nil, err
		}
		topology := stagingTopologyFromTwin(twin)
		return topology.Twin, &topology, nil
	}
	if catalog != nil {
		loaded, err := catalog.StagingTopology(ctx, tenantID, siteID, at)
		if err != nil {
			return nil, nil, err
		}
		return loaded.Twin, &loaded, nil
	}
	return nil, nil, nil
}

// Evaluate reads plant history + live telemetry snapshot for a Site and computes staging recommendations.
func (evaluator *StagingEvaluator) Evaluate(ctx context.Context, tenantID, siteID string, at time.Time) (StagingEvaluation, error) {
	if evaluator == nil || evaluator.Snapshot == nil {
		return StagingEvaluation{}, ErrStagingEvaluationNotFound
	}
	if !uuidPattern.MatchString(tenantID) || !uuidPattern.MatchString(siteID) {
		return StagingEvaluation{}, fmt.Errorf("tenantId and siteId must be UUIDs")
	}
	policy, err := evaluator.Policy.normalized()
	if err != nil {
		return StagingEvaluation{}, err
	}
	evaluatedAt := at.UTC()
	twin, topology, catErr := loadTwinAndTopology(ctx, tenantID, siteID, evaluatedAt, evaluator.TwinLoader, evaluator.Catalog)
	if catErr != nil {
		return StagingEvaluation{}, fmt.Errorf("%w: %v", ErrStagingTopologyUnavailable, catErr)
	}
	snapshot, err := evaluator.Snapshot.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		At:       evaluatedAt,
		Policy:   policy,
		Topology: topology,
		Twin:     twin,
	})
	if err != nil {
		return StagingEvaluation{}, err
	}
	if len(snapshot.ChillerFits) == 0 && evaluator.History != nil {
		to := evaluatedAt.Truncate(copBucketMinutes * time.Minute)
		from := to.AddDate(0, 0, -copHistoryDays)
		if buckets, histErr := evaluator.History.PlantBuckets(ctx, PlantHistoryQuery{TenantID: tenantID, SiteID: siteID, From: from, To: to, Twin: twin}); histErr == nil {
			fit := FitPlant(buckets, from, to)
			snapshot.ChillerFits = fit.Chillers
		}
	}
	applyStagingTopology(&snapshot, topology)
	return EvaluateStaging(snapshot, policy)
}

func evaluateStagingWithFit(ctx context.Context, tenantID, siteID string, at time.Time, fit PlantFit, reader StagingSnapshotReader, catalog StagingSiteCatalog, twin *planttwin.SiteTwin, topology *StagingTopology, policy *StagingPolicy) *StagingEvaluation {
	if reader == nil {
		return nil
	}
	effectivePolicy := DefaultStagingPolicy()
	if policy != nil {
		effectivePolicy = *policy
	}
	normPolicy, err := effectivePolicy.normalized()
	if err != nil {
		return &StagingEvaluation{
			TenantID: tenantID, SiteID: siteID, EvaluatedAt: at.UTC(), ConfirmationMode: ConfirmationModeManual,
			Recommendations: []StagingRecommendationDraft{},
			Diagnostics: []StagingDiagnostic{{
				Scope: RecommendationKindChillerStageDown, Status: "SUPPRESSED", Reason: "STAGING_POLICY_INVALID", Detail: err.Error(),
			}},
		}
	}
	if twin == nil && topology == nil && catalog != nil {
		loadedTwin, loadedTopo, _ := loadTwinAndTopology(ctx, tenantID, siteID, at.UTC(), nil, catalog)
		twin, topology = loadedTwin, loadedTopo
	}
	if twin == nil && topology != nil {
		twin = topology.Twin
	}
	snapshot, err := reader.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID, SiteID: siteID, At: at.UTC(), Policy: normPolicy, Topology: topology, Twin: twin,
	})
	if err != nil {
		return &StagingEvaluation{
			TenantID: tenantID, SiteID: siteID, EvaluatedAt: at.UTC(), ConfirmationMode: ConfirmationModeManual,
			Recommendations: []StagingRecommendationDraft{},
			Diagnostics: []StagingDiagnostic{{
				Scope: RecommendationKindChillerStageDown, Status: "SUPPRESSED", Reason: "STAGING_SNAPSHOT_UNAVAILABLE", Detail: err.Error(),
			}},
		}
	}
	applyStagingTopology(&snapshot, topology)
	if len(snapshot.ChillerFits) == 0 {
		snapshot.ChillerFits = fit.Chillers
	}
	eval, evalErr := EvaluateStaging(snapshot, normPolicy)
	if evalErr != nil {
		return &StagingEvaluation{
			TenantID: tenantID, SiteID: siteID, EvaluatedAt: at.UTC(), ConfirmationMode: ConfirmationModeManual,
			Recommendations: []StagingRecommendationDraft{},
			Diagnostics: []StagingDiagnostic{{
				Scope: RecommendationKindChillerStageDown, Status: "SUPPRESSED", Reason: "STAGING_EVALUATION_INVALID", Detail: evalErr.Error(),
			}},
		}
	}
	return &eval
}

// EvaluateStaging runs chiller stage-down, off-hours stop, and idle auxiliary shutdown checks on a sensor snapshot.
func EvaluateStaging(snapshot StagingSnapshot, policy StagingPolicy) (StagingEvaluation, error) {
	normPolicy, err := policy.normalized()
	if err != nil {
		return StagingEvaluation{}, err
	}
	if snapshot.EvaluatedAt.IsZero() {
		return StagingEvaluation{}, errors.New("evaluatedAt is required")
	}
	snapshot = normalizeSnapshotUnits(snapshot)
	evaluatedAt := snapshot.EvaluatedAt.UTC()

	result := StagingEvaluation{
		TenantID:         snapshot.TenantID,
		SiteID:           snapshot.SiteID,
		EvaluatedAt:      evaluatedAt,
		ConfirmationMode: ConfirmationModeManual,
		SiteControlMode:  strings.TrimSpace(snapshot.SiteControlMode),
		Recommendations:  []StagingRecommendationDraft{},
		Diagnostics:      []StagingDiagnostic{},
	}

	schedEval, hasSchedule, schedErr := evaluateStagingSchedule(snapshot, evaluatedAt)
	if schedErr != nil {
		return StagingEvaluation{}, schedErr
	}
	if hasSchedule && schedEval.Suppressed && schedEval.ActiveException != nil &&
		strings.EqualFold(strings.TrimSpace(schedEval.ActiveException.Kind), planttwin.ExceptionMaintenanceWindow) {
		detail := formatScheduleExceptionDetail("站点当前处于已批准的计划检修窗口，暂停生成减台及启停建议", *schedEval.ActiveException)
		result.Diagnostics = append(result.Diagnostics,
			StagingDiagnostic{
				Scope:  RecommendationKindChillerStageDown,
				Status: "SUPPRESSED",
				Reason: "MAINTENANCE_WINDOW_ACTIVE",
				Detail: detail,
			},
			StagingDiagnostic{
				Scope:  RecommendationKindOffHoursStop,
				Status: "SUPPRESSED",
				Reason: "MAINTENANCE_WINDOW_ACTIVE",
				Detail: detail,
			},
		)
		if len(snapshot.Auxiliaries) > 0 {
			result.Diagnostics = append(result.Diagnostics, StagingDiagnostic{
				Scope:  RecommendationKindIdleAuxiliaryStop,
				Status: "SUPPRESSED",
				Reason: "MAINTENANCE_WINDOW_ACTIVE",
				Detail: detail,
			})
		}
		return result, nil
	}

	scheduleConfigured := hasSchedule && schedEval.OperatingState != planttwin.OperatingStateUnconfigured
	outsideCoolingWindow := scheduleConfigured && (schedEval.Suppressed || !schedEval.WithinOperatingWindow)
	if outsideCoolingWindow {
		offDrafts, offDiags := evaluateOffHoursStop(snapshot, normPolicy, schedEval)
		result.Recommendations = append(result.Recommendations, offDrafts...)
		result.Diagnostics = append(result.Diagnostics, offDiags...)
		result.Diagnostics = append(result.Diagnostics, StagingDiagnostic{
			Scope:  RecommendationKindChillerStageDown,
			Status: "NO_OP",
			Reason: "OFF_HOURS_WINDOW_ACTIVE",
			Detail: "当前处于非供冷运行时间窗口，已切换为非营业时段停机评估，不重复生成冷机减台建议。",
		})
		auxDrafts, auxDiags := evaluateIdleAuxiliaries(snapshot, normPolicy)
		result.Recommendations = append(result.Recommendations, auxDrafts...)
		result.Diagnostics = append(result.Diagnostics, auxDiags...)
		return result, nil
	}

	if draft, diag := evaluateChillerStageDown(snapshot, normPolicy); draft != nil {
		result.Recommendations = append(result.Recommendations, *draft)
		result.Diagnostics = append(result.Diagnostics, diag)
	} else {
		result.Diagnostics = append(result.Diagnostics, diag)
	}

	auxDrafts, auxDiags := evaluateIdleAuxiliaries(snapshot, normPolicy)
	result.Recommendations = append(result.Recommendations, auxDrafts...)
	result.Diagnostics = append(result.Diagnostics, auxDiags...)

	if scheduleConfigured {
		result.Diagnostics = append(result.Diagnostics, StagingDiagnostic{
			Scope:  RecommendationKindOffHoursStop,
			Status: "NO_OP",
			Reason: "IN_COOLING_WINDOW",
			Detail: "当前处于有效供冷运行窗口内（含预冷与后吹扫缓冲），不生成非营业时段停机建议。",
		})
	}

	return result, nil
}

func normalizeSnapshotUnits(snapshot StagingSnapshot) StagingSnapshot {
	resolved := make([]ChillerUnitInput, len(snapshot.Chillers))
	for index, unit := range snapshot.Chillers {
		if unit.AssetCode == "" {
			unit.AssetCode = unit.AssetID
		}
		if unit.AssetName == "" {
			unit.AssetName = unit.AssetCode
		}
		resolved[index] = unit
	}
	snapshot.Chillers = resolved
	return snapshot
}

func evaluateStagingSchedule(snapshot StagingSnapshot, at time.Time) (planttwin.ScheduleEvaluation, bool, error) {
	var sched planttwin.OperatingSchedule
	switch {
	case snapshot.OperatingSchedule != nil:
		sched = *snapshot.OperatingSchedule
	case snapshot.Twin != nil:
		sched = snapshot.Twin.Schedule
	default:
		return planttwin.ScheduleEvaluation{}, false, nil
	}
	if strings.TrimSpace(sched.Timezone) == "" && snapshot.Twin != nil {
		sched.Timezone = snapshot.Twin.Timezone
	}
	if strings.TrimSpace(sched.Timezone) == "" {
		if len(sched.Slots) == 0 && len(sched.Exceptions) == 0 {
			return planttwin.ScheduleEvaluation{}, false, nil
		}
		sched.Timezone = "UTC"
	}
	eval, err := planttwin.EvaluateOperatingSchedule(at, sched)
	if err != nil {
		return planttwin.ScheduleEvaluation{}, true, err
	}
	return eval, true, nil
}

func evaluateChillerStageDown(snapshot StagingSnapshot, policy StagingPolicy) (*StagingRecommendationDraft, StagingDiagnostic) {
	at := snapshot.EvaluatedAt.UTC()
	if detail, active := siteControlExitDetail(snapshot); active {
		return nil, StagingDiagnostic{
			Scope:  RecommendationKindChillerStageDown,
			Status: "SUPPRESSED",
			Reason: "SITE_CONTROL_EXIT_ACTIVE",
			Detail: fmt.Sprintf("%s，暂停生成冷机减台建议。", detail),
		}
	}
	if len(snapshot.Chillers) < 2 {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, Status: "NO_OP", Reason: "INSUFFICIENT_CHILLERS",
			Detail: "站点登记冷机数量不足 2 台，无需或无法执行冷机减台评估。",
		}
	}

	if snapshot.PlantCoolingLoad == nil {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, Status: "SUPPRESSED", Reason: "COOLING_LOAD_MISSING",
			Detail: "缺少冷站瞬时制冷量（btu-calc）遥测读数，按安全原则不生成减台建议。",
		}
	}
	if ok, reason := isFreshGoodObservation(snapshot.PlantCoolingLoad.Quality, snapshot.PlantCoolingLoad.SampledAt, at, policy.MaxTelemetryAge); !ok {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, Status: "SUPPRESSED", Reason: "COOLING_LOAD_" + reason,
			Detail: fmt.Sprintf("冷站瞬时制冷量（btu-calc）状态不可信（%s），暂停冷机减台评估。", reason),
		}
	}
	coolingLoadKW := snapshot.PlantCoolingLoad.Value
	if !finite(coolingLoadKW) || coolingLoadKW <= 0 {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, Status: "NO_OP", Reason: "COOLING_LOAD_NON_POSITIVE",
			Detail: fmt.Sprintf("当前冷站瞬时制冷量为 %.2f kW，未检测到有效供冷负荷，不建议减台。", coolingLoadKW),
		}
	}

	type activeChiller struct {
		unit      ChillerUnitInput
		powerKW   float64
		fit       *ChillerFit
		ratedCap  float64
		supplyC   *float64
		condenser *float64
	}

	fits := indexChillerFits(snapshot.ChillerFits)
	running := make([]activeChiller, 0, len(snapshot.Chillers))
	totalCurrentPowerKW := 0.0

	for _, unit := range snapshot.Chillers {
		state, reason := resolveChillerRunState(unit, at, policy)
		switch state {
		case RunStateStarting:
			return nil, StagingDiagnostic{
				Scope: RecommendationKindChillerStageDown, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
				Status: "SUPPRESSED", Reason: "PLANT_CHILLER_STARTING",
				Detail: fmt.Sprintf("冷机 %s 处于开机过渡中（STARTING），暂不建议减台。", unitDisplayName(unit)),
			}
		case RunStateUnknown:
			return nil, StagingDiagnostic{
				Scope: RecommendationKindChillerStageDown, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
				Status: "SUPPRESSED", Reason: reason,
				Detail: fmt.Sprintf("冷机 %s 运行状态不可信（%s），无法确认冷站真实开机台数。", unitDisplayName(unit), reason),
			}
		case RunStateStopped:
			continue
		case RunStateRunning:
			if unit.Power == nil {
				return nil, StagingDiagnostic{
					Scope: RecommendationKindChillerStageDown, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
					Status: "SUPPRESSED", Reason: "CHILLER_METER_MISSING",
					Detail: fmt.Sprintf("运行冷机 %s 缺少电表有功功率遥测，不生成减台建议。", unitDisplayName(unit)),
				}
			}
			if ok, pReason := isFreshGoodObservation(unit.Power.Quality, unit.Power.SampledAt, at, policy.MaxTelemetryAge); !ok || !finite(unit.Power.Value) || unit.Power.Value <= 0 {
				code := "CHILLER_METER_" + pReason
				if ok && (!finite(unit.Power.Value) || unit.Power.Value <= 0) {
					code = "CHILLER_METER_INVALID"
				}
				return nil, StagingDiagnostic{
					Scope: RecommendationKindChillerStageDown, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
					Status: "SUPPRESSED", Reason: code,
					Detail: fmt.Sprintf("运行冷机 %s 电表读数不可用（%s），不生成减台建议。", unitDisplayName(unit), code),
				}
			}
			fit := matchChillerFit(unit, fits)
			ratedCap := effectiveChillerCapacityKW(unit, fit)
			supplyC := resolveFreshTemp(unit.ChwLeavingC, snapshot.HeaderSupplyC, at, policy.MaxTelemetryAge)
			condenserC := resolveFreshTemp(unit.CwLeavingC, nil, at, policy.MaxTelemetryAge)
			totalCurrentPowerKW += unit.Power.Value
			running = append(running, activeChiller{
				unit:      unit,
				powerKW:   unit.Power.Value,
				fit:       fit,
				ratedCap:  ratedCap,
				supplyC:   supplyC,
				condenser: condenserC,
			})
		}
	}

	if len(running) == 0 {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, Status: "NO_OP", Reason: "ZERO_CHILLERS_RUNNING",
			Detail: "当前没有正在运行的冷机。",
		}
	}
	if len(running) == 1 {
		return nil, StagingDiagnostic{
			Scope: RecommendationKindChillerStageDown, AssetID: running[0].unit.AssetID, AssetCode: running[0].unit.AssetCode,
			Status: "NO_OP", Reason: "SINGLE_CHILLER_RUNNING",
			Detail: fmt.Sprintf("当前仅 %s 1 台冷机运行，无法继续减台。", unitDisplayName(running[0].unit)),
		}
	}

	if snapshot.HeaderSupplyC != nil {
		if ok, _ := isFreshGoodObservation(snapshot.HeaderSupplyC.Quality, snapshot.HeaderSupplyC.SampledAt, at, policy.MaxTelemetryAge); ok &&
			finite(snapshot.HeaderSupplyC.Value) && snapshot.HeaderSupplyC.Value > policy.MaxStageDownSupplyTempC {
			return nil, StagingDiagnostic{
				Scope:  RecommendationKindChillerStageDown,
				Status: "SUPPRESSED",
				Reason: "SUPPLY_TEMP_TOO_HIGH_FOR_STAGE_DOWN",
				Detail: fmt.Sprintf("总管冷冻水供水温度 %.1f ℃ 高于安全减台上限 %.1f ℃，暂停冷机减台评估。",
					snapshot.HeaderSupplyC.Value, policy.MaxStageDownSupplyTempC),
			}
		}
	}

	hasLoadHistory := len(snapshot.PlantLoadHistory) > 0
	dwellMaxLoadKW := coolingLoadKW
	if hasLoadHistory {
		var dwellReason string
		dwellMaxLoadKW, dwellReason = confirmPlantLoadDwell(snapshot.PlantLoadHistory, at, policy)
		if dwellReason != "" {
			return nil, StagingDiagnostic{
				Scope:  RecommendationKindChillerStageDown,
				Status: "SUPPRESSED",
				Reason: dwellReason,
				Detail: fmt.Sprintf("冷站瞬时制冷量尚未在连续 %.0f 分钟驻留窗口内保持稳定有效低负荷（%s），暂停冷机减台建议。",
					policy.StageDownDwellDuration.Minutes(), dwellReason),
			}
		}
	}

	copBefore := coolingLoadKW / totalCurrentPowerKW
	effectiveReserveFraction := policy.HeadroomReserveFraction + policy.StageDownDeadbandFraction
	requiredCapacityKW := coolingLoadKW * (1 + effectiveReserveFraction)
	dwellRequiredCapacityKW := dwellMaxLoadKW * (1 + effectiveReserveFraction)

	type candidateOutcome struct {
		stopped               activeChiller
		remainingCapacityKW   float64
		headroomFraction      float64
		projectedPowerAfterKW float64
		projectedCOPAfter     float64
		powerSavingKW         float64
		extrapolated          bool
	}

	var best *candidateOutcome
	rejectionReason := "NO_CANDIDATE_IMPROVED"
	rejectionDetail := "未找到满足安全裕度与节能门槛的冷机减台方案。"
	rejectedAssetID := ""
	rejectedAssetCode := ""

	recordRejection := func(reason, detail string) {
		rejectionReason = reason
		rejectionDetail = detail
		rejectedAssetID = ""
		rejectedAssetCode = ""
	}

	for dropIdx, candidate := range running {
		if lock, locked := lookupAssetControlLock(snapshot.AssetControlLocks, candidate.unit.AssetID, candidate.unit.AssetCode); locked {
			recordRejection(
				"ASSET_CONTROL_LOCKED",
				fmt.Sprintf("冷机 %s 已被单机锁定（%s）%s，本次不建议作为减台停机对象。", unitDisplayName(candidate.unit), lock.LockMode, lockReasonSuffix(lock.Reason)),
			)
			rejectedAssetID = candidate.unit.AssetID
			rejectedAssetCode = candidate.unit.AssetCode
			continue
		}
		if !chillerMeetsMinRunDuration(candidate.unit, at, policy) {
			recordRejection(
				"CHILLER_MIN_RUN_TIME_NOT_MET",
				fmt.Sprintf("冷机 %s 连续运行时间尚未满足 %.0f 分钟最小运行时长要求，本次不建议作为减台停机对象。",
					unitDisplayName(candidate.unit), policy.MinChillerRunDuration.Minutes()),
			)
			rejectedAssetID = candidate.unit.AssetID
			rejectedAssetCode = candidate.unit.AssetCode
			continue
		}
		remaining := make([]activeChiller, 0, len(running)-1)
		remainingCapKW := 0.0
		capacityKnown := true
		for idx, rem := range running {
			if idx == dropIdx {
				continue
			}
			if rem.ratedCap <= 0 {
				capacityKnown = false
				break
			}
			remainingCapKW += rem.ratedCap
			remaining = append(remaining, rem)
		}
		if !capacityKnown {
			recordRejection("CHILLER_CAPACITY_UNKNOWN", "部分保留冷机缺少额定冷量或历史参考冷量，无法校核减台后安全容量裕度。")
			continue
		}
		if remainingCapKW < requiredCapacityKW {
			margin := (remainingCapKW - coolingLoadKW) / coolingLoadKW
			recordRejection(
				"HEADROOM_INSUFFICIENT",
				fmt.Sprintf("停运 %s 后剩余总冷量 %.1f kW，相对当前冷负荷 %.1f kW 裕度仅 %.1f%%，低于 %.1f%% 安全储备与迟滞要求。",
					unitDisplayName(candidate.unit), remainingCapKW, coolingLoadKW, margin*100, effectiveReserveFraction*100),
			)
			continue
		}
		if hasLoadHistory && remainingCapKW < dwellRequiredCapacityKW {
			recordRejection(
				"STAGE_DOWN_DWELL_NOT_MET",
				fmt.Sprintf("停运 %s 后剩余总冷量 %.1f kW 无法满足过去 %.0f 分钟窗口峰值冷负荷 %.1f kW 的 %.1f%% 安全储备与迟滞要求（需 %.1f kW），暂视为短时负荷波动。",
					unitDisplayName(candidate.unit), remainingCapKW, policy.StageDownDwellDuration.Minutes(), dwellMaxLoadKW, effectiveReserveFraction*100, dwellRequiredCapacityKW),
			)
			continue
		}

		projectedPowerAfter := 0.0
		candidateExtrapolated := false
		modelValid := true
		for _, rem := range remaining {
			if rem.fit == nil || rem.fit.Status != ChillerFitted {
				modelValid = false
				recordRejection("CHILLER_COP_MODEL_UNAVAILABLE", fmt.Sprintf("保留冷机 %s 缺少可用的历史 COP 拟合模型，无法预测减台后功耗。", unitDisplayName(rem.unit)))
				break
			}
			if rem.supplyC == nil {
				modelValid = false
				recordRejection("CHW_SUPPLY_TEMP_MISSING", fmt.Sprintf("保留冷机 %s 缺少有效的冷冻水出水温度读数。", unitDisplayName(rem.unit)))
				break
			}
			_, hasCondCoeff := rem.fit.Coefficients["condenserReturnTempC"]
			if (rem.fit.withCondenser || hasCondCoeff) && rem.condenser == nil {
				modelValid = false
				recordRejection("CONDENSER_TEMPERATURE_MISSING", fmt.Sprintf("保留冷机 %s 的 COP 模型依赖冷却水温度，但当前缺少有效冷却水出水读数。", unitDisplayName(rem.unit)))
				break
			}
			assignedLoadKW := coolingLoadKW * (rem.ratedCap / remainingCapKW)
			refKW := fitCapacityReferenceKW(rem.unit, *rem.fit)
			if refKW <= 0 {
				modelValid = false
				recordRejection("CHILLER_CAPACITY_UNKNOWN", fmt.Sprintf("保留冷机 %s 缺少有效的 COP 负荷率基准。", unitDisplayName(rem.unit)))
				break
			}
			loadRatio := assignedLoadKW / refKW
			if loadRatio > 1.05 {
				candidateExtrapolated = true
			}
			predictedCOP, ok := rem.fit.safePredictCOP(loadRatio, *rem.supplyC, rem.condenser)
			if !ok || predictedCOP <= 0.5 || !nameplate.COPPlausible(predictedCOP, *rem.supplyC, rem.condenser) {
				modelValid = false
				recordRejection("MODEL_PREDICTION_IMPLAUSIBLE", fmt.Sprintf("保留冷机 %s 在分配冷量 %.1f kW 下的预测 COP 越界，放弃本次减台建议。", unitDisplayName(rem.unit), assignedLoadKW))
				break
			}
			projectedPowerAfter += assignedLoadKW / predictedCOP
		}
		if !modelValid || projectedPowerAfter <= 0 {
			continue
		}

		projectedCOP := coolingLoadKW / projectedPowerAfter
		powerSaving := totalCurrentPowerKW - projectedPowerAfter
		if projectedCOP <= copBefore {
			recordRejection("COP_NOT_IMPROVED", fmt.Sprintf("停运 %s 后系统综合 COP 预计由 %.2f 变为 %.2f，未提升，维持当前开机方式。", unitDisplayName(candidate.unit), copBefore, projectedCOP))
			continue
		}
		if powerSaving < policy.MinimumPowerSavingKW {
			recordRejection("SAVING_BELOW_THRESHOLD", fmt.Sprintf("停运 %s 预计仅节电 %.2f kW，低于防频繁启停阈值 %.1f kW。", unitDisplayName(candidate.unit), powerSaving, policy.MinimumPowerSavingKW))
			continue
		}

		outcome := candidateOutcome{
			stopped:               candidate,
			remainingCapacityKW:   remainingCapKW,
			headroomFraction:      (remainingCapKW - coolingLoadKW) / coolingLoadKW,
			projectedPowerAfterKW: projectedPowerAfter,
			projectedCOPAfter:     projectedCOP,
			powerSavingKW:         powerSaving,
			extrapolated:          candidateExtrapolated,
		}
		if best == nil ||
			outcome.powerSavingKW > best.powerSavingKW+1e-6 ||
			(math.Abs(outcome.powerSavingKW-best.powerSavingKW) <= 1e-6 &&
				(outcome.stopped.powerKW > best.stopped.powerKW+1e-6 ||
					(math.Abs(outcome.stopped.powerKW-best.stopped.powerKW) <= 1e-6 &&
						(outcome.stopped.unit.AssetCode < best.stopped.unit.AssetCode ||
							(outcome.stopped.unit.AssetCode == best.stopped.unit.AssetCode && outcome.stopped.unit.AssetID < best.stopped.unit.AssetID))))) {
			copied := outcome
			best = &copied
		}
	}

	if best == nil {
		return nil, StagingDiagnostic{
			Scope:     RecommendationKindChillerStageDown,
			AssetID:   rejectedAssetID,
			AssetCode: rejectedAssetCode,
			Status:    "SUPPRESSED",
			Reason:    rejectionReason,
			Detail:    rejectionDetail,
		}
	}

	savingKW := round(best.powerSavingKW, 2)
	hourlyKWh := round(best.powerSavingKW, 2)
	shiftKWh := round(best.powerSavingKW*policy.ShiftHours, 2)
	curPower := round(totalCurrentPowerKW, 2)
	projPower := round(best.projectedPowerAfterKW, 2)
	loadKW := round(coolingLoadKW, 2)
	remCap := round(best.remainingCapacityKW, 2)
	headroom := round(best.headroomFraction, 4)
	copBeforeRounded := round(copBefore, 3)
	copAfterRounded := round(best.projectedCOPAfter, 3)

	target := best.stopped.unit
	rationale := fmt.Sprintf(
		"当前 %d 台冷机运行（总电功率 %.1f kW，冷站冷负荷 %.1f kW，实测系统 COP %.2f）。建议人工确认停运 %s：停机后剩余冷量 %.1f kW（安全储备裕度 %.1f%% >= %.0f%%），预测系统 COP 升至 %.2f，可净省电功率 %.1f kW（约 %.1f kWh/小时、%.1f kWh/%.0f小时班次）。",
		len(running), curPower, loadKW, copBeforeRounded, unitDisplayName(target), remCap, headroom*100, effectiveReserveFraction*100, copAfterRounded, savingKW, hourlyKWh, shiftKWh, policy.ShiftHours,
	)
	preconditions := []string{
		fmt.Sprintf("人工确认模式（%s）：必须经值班主管复核并完成 TOTP 双因子验证后方可下发 %s 停机顺控。", ConfirmationModeManual, SuggestedCapabilityStopUnitSequence),
		fmt.Sprintf("确认冷站冷负荷（当前 %.1f kW）稳定低于停机后剩余机组安全容量 %.1f kW（储备裕度 %.1f%%）。", loadKW, remCap, headroom*100),
		fmt.Sprintf("下发前复核 %s 及其配套水泵、冷却塔与电动蝶阀联锁逻辑均处于自动或可顺控状态。", unitDisplayName(target)),
	}
	if best.extrapolated {
		preconditions = append(preconditions, "注：减台后保留机组预测负荷率略高于近 14 天历史观测上限，建议关注停机后 15 分钟供水温度与负荷爬坡。")
	}

	draft := StagingRecommendationDraft{
		ID:                       deterministicV7(snapshot.SiteID+":"+RecommendationKindChillerStageDown, target.AssetID, at),
		Kind:                     RecommendationKindChillerStageDown,
		ConfirmationMode:         ConfirmationModeManual,
		RequiresSupervisorReview: true,
		RequiresTOTP:             true,
		SuggestedCapability:      SuggestedCapabilityStopUnitSequence,
		SuggestedCommandPointID:  lookupCommandPointID(snapshot.CommandPointIDs, target.AssetID, target.AssetCode, SuggestedCapabilityStopUnitSequence),
		TargetAssetID:            target.AssetID,
		TargetAssetCode:          target.AssetCode,
		TargetAssetName:          target.AssetName,
		TargetAssetType:          AssetTypeChiller,
		PredictedPowerSavingKW:   savingKW,
		EstimatedHourlySavingKWh: hourlyKWh,
		EstimatedShiftSavingKWh:  shiftKWh,
		ShiftHours:               policy.ShiftHours,
		PowerSavingBasis:         "CHILLER_COP_MODEL_AND_METER",
		CurrentTotalPowerKW:      &curPower,
		ProjectedTotalPowerKW:    &projPower,
		CurrentCoolingLoadKW:     &loadKW,
		RemainingCapacityKW:      &remCap,
		HeadroomMarginFraction:   &headroom,
		COPBefore:                &copBeforeRounded,
		ProjectedCOPAfter:        &copAfterRounded,
		Extrapolated:             best.extrapolated,
		Rationale:                rationale,
		Preconditions:            preconditions,
	}
	return &draft, StagingDiagnostic{
		Scope: RecommendationKindChillerStageDown, AssetID: target.AssetID, AssetCode: target.AssetCode,
		Status: "RECOMMENDED", Reason: "CHILLER_STAGE_DOWN_ELIGIBLE", Detail: rationale,
	}
}

func evaluateIdleAuxiliaries(snapshot StagingSnapshot, policy StagingPolicy) ([]StagingRecommendationDraft, []StagingDiagnostic) {
	if len(snapshot.Auxiliaries) == 0 {
		return []StagingRecommendationDraft{}, []StagingDiagnostic{}
	}
	if detail, active := siteControlExitDetail(snapshot); active {
		return []StagingRecommendationDraft{}, []StagingDiagnostic{{
			Scope:  RecommendationKindIdleAuxiliaryStop,
			Status: "SUPPRESSED",
			Reason: "SITE_CONTROL_EXIT_ACTIVE",
			Detail: fmt.Sprintf("%s，暂停生成辅助设备联锁停机建议。", detail),
		}}
	}
	at := snapshot.EvaluatedAt.UTC()

	chillersByKey := map[string]ChillerUnitInput{}
	for _, ch := range snapshot.Chillers {
		if ch.AssetID != "" {
			chillersByKey[strings.ToLower(ch.AssetID)] = ch
		}
		if ch.AssetCode != "" {
			chillersByKey[strings.ToLower(ch.AssetCode)] = ch
		}
	}

	servesEdgesByAux := map[string][]string{}
	for _, edge := range snapshot.Edges {
		if !strings.EqualFold(strings.TrimSpace(edge.Role), HydraulicEdgeServes) {
			continue
		}
		from := strings.ToLower(strings.TrimSpace(edge.FromAssetID))
		to := strings.TrimSpace(edge.ToAssetID)
		if from != "" && to != "" {
			servesEdgesByAux[from] = append(servesEdgesByAux[from], to)
		}
	}

	auxList := append([]AuxiliaryUnitInput(nil), snapshot.Auxiliaries...)
	sort.Slice(auxList, func(i, j int) bool {
		if auxList[i].AssetCode != auxList[j].AssetCode {
			return auxList[i].AssetCode < auxList[j].AssetCode
		}
		return auxList[i].AssetID < auxList[j].AssetID
	})

	drafts := make([]StagingRecommendationDraft, 0)
	diagnostics := make([]StagingDiagnostic, 0, len(auxList))

	for _, aux := range auxList {
		assetType, ok := normalizeAuxiliaryAssetType(aux.AssetType)
		if !ok {
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
				Status: "NO_OP", Reason: "UNSUPPORTED_AUXILIARY_TYPE",
				Detail: fmt.Sprintf("设备类型 %q 不属于冷冻泵/冷却泵/冷却塔联动停机范围。", aux.AssetType),
			})
			continue
		}
		if lock, locked := lookupAssetControlLock(snapshot.AssetControlLocks, aux.AssetID, aux.AssetCode); locked {
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
				Status: "SUPPRESSED", Reason: "ASSET_CONTROL_LOCKED",
				Detail: fmt.Sprintf("辅助设备 %s 已被单机锁定（%s）%s，暂停生成联锁停机建议。", auxDisplayName(aux), lock.LockMode, lockReasonSuffix(lock.Reason)),
			})
			continue
		}
		auxRunning, auxReason := resolveAuxiliaryRunning(aux, at, policy)
		if !auxRunning {
			status := "NO_OP"
			if strings.Contains(auxReason, "STALE") || strings.Contains(auxReason, "QUALITY") || strings.Contains(auxReason, "UNKNOWN") || strings.Contains(auxReason, "MISSING") {
				status = "SUPPRESSED"
			}
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
				Status: status, Reason: auxReason,
				Detail: fmt.Sprintf("辅助设备 %s 当前未处于可确认的运行空转状态（%s）。", auxDisplayName(aux), auxReason),
			})
			continue
		}

		servedRefs := collectServedChillers(aux, servesEdgesByAux)
		if len(servedRefs) == 0 {
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
				Status: "SUPPRESSED", Reason: "NO_SERVED_CHILLERS_MAPPED",
				Detail: fmt.Sprintf("辅助设备 %s 缺少液压 SERVES 关联冷机定义，出于联锁安全不建议停机。", auxDisplayName(aux)),
			})
			continue
		}

		var minIdle time.Duration
		firstIdle := true
		blocked := false
		resolvedChillerIDs := make([]string, 0, len(servedRefs))
		for _, ref := range servedRefs {
			chiller, found := chillersByKey[strings.ToLower(ref)]
			if !found {
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: "PAIRED_CHILLER_STATE_MISSING",
					Detail: fmt.Sprintf("辅助设备 %s 所服务的冷机 %s 缺少当前遥测状态，立即抑制停机建议。", auxDisplayName(aux), ref),
				})
				blocked = true
				break
			}
			canonicalID := chiller.AssetID
			if canonicalID == "" {
				canonicalID = chiller.AssetCode
			}
			resolvedChillerIDs = append(resolvedChillerIDs, canonicalID)

			state, stateReason := resolveChillerRunState(chiller, at, policy)
			if state == RunStateRunning {
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: "PAIRED_CHILLER_RUNNING",
					Detail: fmt.Sprintf("所服务冷机 %s 仍在运行（RUNNING），严禁停运辅助设备 %s。", unitDisplayName(chiller), auxDisplayName(aux)),
				})
				blocked = true
				break
			}
			if state == RunStateStarting {
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: "PAIRED_CHILLER_STARTING",
					Detail: fmt.Sprintf("所服务冷机 %s 正在启动（STARTING），严禁停运辅助设备 %s。", unitDisplayName(chiller), auxDisplayName(aux)),
				})
				blocked = true
				break
			}
			if state != RunStateStopped {
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: stateReason,
					Detail: fmt.Sprintf("所服务冷机 %s 状态未确认安全停机（%s），抑制辅助设备 %s 停机建议。", unitDisplayName(chiller), stateReason, auxDisplayName(aux)),
				})
				blocked = true
				break
			}
			if chillerHasActiveThermalDelta(chiller, at, policy.MaxTelemetryAge) {
				deltaC := chiller.ChwEnteringC.Value - chiller.ChwLeavingC.Value
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: "CHILLER_THERMAL_DELTA_ACTIVE",
					Detail: fmt.Sprintf("所服务冷机 %s 蒸发器供回水温差 %.2f ℃（>= %.1f ℃），表明仍有冷量交换，严禁停运辅助设备 %s。",
						unitDisplayName(chiller), deltaC, DefaultChillerThermalDeltaThreshold, auxDisplayName(aux)),
				})
				blocked = true
				break
			}

			idleDur, durReason := confirmChillerStoppedDuration(chiller, at, policy)
			if durReason != "" {
				diagnostics = append(diagnostics, StagingDiagnostic{
					Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
					Status: "SUPPRESSED", Reason: durReason,
					Detail: fmt.Sprintf("冷机 %s 连续停机时长不足或存在遥测断档（%s），不满足 %.0f 分钟消抖门槛。", unitDisplayName(chiller), durReason, policy.IdleAuxiliaryDebounce.Minutes()),
				})
				blocked = true
				break
			}
			if firstIdle || idleDur < minIdle {
				minIdle = idleDur
				firstIdle = false
			}
		}
		if blocked {
			continue
		}
		if minIdle < policy.IdleAuxiliaryDebounce {
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindIdleAuxiliaryStop, AssetID: aux.AssetID, AssetCode: aux.AssetCode,
				Status: "SUPPRESSED", Reason: "IDLE_DEBOUNCE_NOT_MET",
				Detail: fmt.Sprintf("辅助设备 %s 所服务冷机最短连续停机时间为 %.1f 分钟，尚未达到 %.0f 分钟防抖要求。", auxDisplayName(aux), minIdle.Minutes(), policy.IdleAuxiliaryDebounce.Minutes()),
			})
			continue
		}

		savingKW, powerBasis := resolveAuxiliaryPowerSaving(aux, at, policy.MaxTelemetryAge)
		idleSec := int64(minIdle.Seconds())
		idleMin := round(minIdle.Minutes(), 1)
		savingKWRounded := round(savingKW, 2)
		hourlyKWh := savingKWRounded
		shiftKWh := round(savingKW*policy.ShiftHours, 2)
		interlockStatement := fmt.Sprintf("所服务冷机均已停机超过 %.0f 分钟（实测最短连续停机 %.1f 分钟），无 RUNNING/STARTING 状态机组。", policy.IdleAuxiliaryDebounce.Minutes(), idleMin)

		rationale := fmt.Sprintf(
			"辅助设备 %s 仍在运行，但其所服务冷机（%d 台）均已连续停机 %.1f 分钟（>= %.0f 分钟防抖门槛）。建议人工复核并确认下发 %s 停机指令，预计可回收空转电功率 %.1f kW（约 %.1f kWh/小时）。",
			auxDisplayName(aux), len(resolvedChillerIDs), idleMin, policy.IdleAuxiliaryDebounce.Minutes(), SuggestedCapabilityStop, savingKWRounded, hourlyKWh,
		)
		preconditions := []string{
			interlockStatement,
			fmt.Sprintf("人工确认模式（%s）：必须经值班主管复核并完成 TOTP 双因子验证后方可下发 %s 指令。", ConfirmationModeManual, SuggestedCapabilityStop),
			"停机前确认现场无防冻循环、管路排空或总管零点漂移自检作业。",
		}

		targetID := aux.AssetID
		if targetID == "" {
			targetID = aux.AssetCode
		}
		draft := StagingRecommendationDraft{
			ID:                       deterministicV7(snapshot.SiteID+":"+RecommendationKindIdleAuxiliaryStop, targetID, at),
			Kind:                     RecommendationKindIdleAuxiliaryStop,
			ConfirmationMode:         ConfirmationModeManual,
			RequiresSupervisorReview: true,
			RequiresTOTP:             true,
			SuggestedCapability:      SuggestedCapabilityStop,
			SuggestedCommandPointID:  lookupCommandPointID(snapshot.CommandPointIDs, targetID, aux.AssetCode, SuggestedCapabilityStop),
			TargetAssetID:            targetID,
			TargetAssetCode:          aux.AssetCode,
			TargetAssetName:          aux.AssetName,
			TargetAssetType:          assetType,
			PredictedPowerSavingKW:   savingKWRounded,
			EstimatedHourlySavingKWh: hourlyKWh,
			EstimatedShiftSavingKWh:  shiftKWh,
			ShiftHours:               policy.ShiftHours,
			PowerSavingBasis:         powerBasis,
			ContinuousIdleSeconds:    &idleSec,
			ContinuousIdleMinutes:    &idleMin,
			ServedChillerIDs:         resolvedChillerIDs,
			InterlockSafetyStatement: interlockStatement,
			Rationale:                rationale,
			Preconditions:            preconditions,
		}
		drafts = append(drafts, draft)
		diagnostics = append(diagnostics, StagingDiagnostic{
			Scope: RecommendationKindIdleAuxiliaryStop, AssetID: targetID, AssetCode: aux.AssetCode,
			Status: "RECOMMENDED", Reason: "IDLE_AUXILIARY_ELIGIBLE", Detail: rationale,
		})
	}

	return drafts, diagnostics
}

func resolveChillerRunState(unit ChillerUnitInput, at time.Time, policy StagingPolicy) (string, string) {
	explicit := normalizeRunState(unit.RunState)
	hasExplicit := strings.TrimSpace(unit.RunState) != ""
	var explicitValid bool
	var explicitReason string
	if hasExplicit {
		explicitValid, explicitReason = isFreshGoodObservation(unit.RunStateQuality, unit.RunStateAt, at, policy.MaxTelemetryAge)
	}

	powerValid := false
	powerKW := 0.0
	if unit.Power != nil {
		if ok, _ := isFreshGoodObservation(unit.Power.Quality, unit.Power.SampledAt, at, policy.MaxTelemetryAge); ok && finite(unit.Power.Value) {
			powerValid = true
			powerKW = unit.Power.Value
		}
	}

	runningPowerThresholdKW := policy.ChillerRunningPowerKW + math.Max(0, unit.FeederAuxiliaryPowerKW)
	if hasExplicit {
		if !explicitValid {
			return RunStateUnknown, "CHILLER_STATE_" + explicitReason
		}
		switch explicit {
		case RunStateStarting:
			return RunStateStarting, ""
		case RunStateRunning:
			return RunStateRunning, ""
		case RunStateStopped:
			if powerValid && powerKW > runningPowerThresholdKW {
				return RunStateUnknown, "PAIRED_CHILLER_DRAWING_POWER"
			}
			return RunStateStopped, ""
		default:
			return RunStateUnknown, "CHILLER_STATE_UNKNOWN"
		}
	}

	if powerValid {
		if powerKW > runningPowerThresholdKW {
			return RunStateRunning, ""
		}
		return RunStateStopped, ""
	}
	if unit.Power != nil {
		_, pReason := isFreshGoodObservation(unit.Power.Quality, unit.Power.SampledAt, at, policy.MaxTelemetryAge)
		return RunStateUnknown, "CHILLER_STATE_" + pReason
	}
	return RunStateUnknown, "CHILLER_STATE_MISSING"
}

func chillerHasActiveThermalDelta(unit ChillerUnitInput, at time.Time, maxAge time.Duration) bool {
	if unit.ChwEnteringC == nil || unit.ChwLeavingC == nil {
		return false
	}
	enteringOK, _ := isFreshGoodObservation(unit.ChwEnteringC.Quality, unit.ChwEnteringC.SampledAt, at, maxAge)
	leavingOK, _ := isFreshGoodObservation(unit.ChwLeavingC.Quality, unit.ChwLeavingC.SampledAt, at, maxAge)
	if !enteringOK || !leavingOK || !finite(unit.ChwEnteringC.Value) || !finite(unit.ChwLeavingC.Value) {
		return false
	}
	return unit.ChwEnteringC.Value-unit.ChwLeavingC.Value >= DefaultChillerThermalDeltaThreshold-1e-9
}

func chillerMeetsMinRunDuration(unit ChillerUnitInput, at time.Time, policy StagingPolicy) bool {
	if unit.RunningSince != nil {
		if unit.RunningSince.IsZero() || unit.RunningSince.After(at) || at.Sub(unit.RunningSince.UTC()) < policy.MinChillerRunDuration {
			return false
		}
	}
	if len(unit.RunStateHistory) > 0 {
		_, reason := confirmContinuousRunState(
			unit.RunStateHistory, RunStateRunning, policy.MinChillerRunDuration, policy.MaxTelemetryAge, at,
			"CHILLER_MIN_RUN_TIME_NOT_MET", "CHILLER_MIN_RUN_TIME_NOT_MET", "CHILLER_MIN_RUN_TIME_NOT_MET", "CHILLER_MIN_RUN_TIME_NOT_MET",
		)
		return reason == ""
	}
	return true
}

func confirmPlantLoadDwell(history []TelemetryValue, at time.Time, policy StagingPolicy) (float64, string) {
	cutoff := at.Add(-policy.StageDownDwellDuration)
	samples := make([]TelemetryValue, 0, len(history))
	for _, s := range history {
		sampled := s.SampledAt.UTC()
		if sampled.IsZero() || sampled.After(at) {
			continue
		}
		s.SampledAt = sampled
		samples = append(samples, s)
	}
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].SampledAt.Before(samples[j].SampledAt)
	})
	if len(samples) == 0 || at.Sub(samples[len(samples)-1].SampledAt) > policy.MaxTelemetryAge {
		return 0, "COOLING_LOAD_DWELL_STALE"
	}
	anchorIdx := -1
	for i, s := range samples {
		if !s.SampledAt.After(cutoff) {
			anchorIdx = i
		}
	}
	if anchorIdx < 0 {
		return 0, "STAGE_DOWN_DWELL_NOT_MET"
	}
	maxLoadKW := 0.0
	var prevAt time.Time
	for i := anchorIdx; i < len(samples); i++ {
		s := samples[i]
		if !strings.EqualFold(strings.TrimSpace(s.Quality), "GOOD") || !finite(s.Value) || s.Value <= 0 {
			return 0, "COOLING_LOAD_DWELL_NON_GOOD"
		}
		if !prevAt.IsZero() && s.SampledAt.Sub(prevAt) > policy.MaxTelemetryAge {
			return 0, "COOLING_LOAD_DWELL_TELEMETRY_GAP"
		}
		if s.Value > maxLoadKW {
			maxLoadKW = s.Value
		}
		prevAt = s.SampledAt
	}
	return maxLoadKW, ""
}

func confirmChillerStoppedDuration(unit ChillerUnitInput, at time.Time, policy StagingPolicy) (time.Duration, string) {
	if len(unit.RunStateHistory) > 0 {
		return confirmContinuousRunState(
			unit.RunStateHistory, RunStateStopped, policy.IdleAuxiliaryDebounce, policy.MaxTelemetryAge, at,
			"IDLE_HISTORY_NON_GOOD", "IDLE_HISTORY_TELEMETRY_GAP", "IDLE_DEBOUNCE_NOT_MET", "IDLE_HISTORY_STALE",
		)
	}

	if unit.StoppedSince != nil && !unit.StoppedSince.IsZero() && !unit.StoppedSince.After(at) {
		dur := at.Sub(unit.StoppedSince.UTC())
		if dur < policy.IdleAuxiliaryDebounce {
			return dur, "IDLE_DEBOUNCE_NOT_MET"
		}
		return dur, ""
	}

	return 0, "IDLE_DURATION_UNCONFIRMED"
}

func confirmContinuousRunState(
	history []RunStateSample,
	targetState string,
	requiredDuration, maxGap time.Duration,
	at time.Time,
	nonGoodReason, gapReason, notMetReason, staleReason string,
) (time.Duration, string) {
	cutoff := at.Add(-requiredDuration)
	samples := append([]RunStateSample(nil), history...)
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].SampledAt.Before(samples[j].SampledAt)
	})

	latestGoodAt := time.Time{}
	earliestContinuous := time.Time{}
	hasBeforeCutoff := false
	prevMatchAt := time.Time{}

	for _, s := range samples {
		sampled := s.SampledAt.UTC()
		if sampled.IsZero() || sampled.After(at) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(s.Quality), "GOOD") {
			if !sampled.Before(cutoff) {
				return 0, nonGoodReason
			}
			earliestContinuous = time.Time{}
			hasBeforeCutoff = false
			prevMatchAt = time.Time{}
			continue
		}
		if normalizeRunState(s.State) != targetState {
			if !sampled.Before(cutoff) {
				return 0, notMetReason
			}
			earliestContinuous = time.Time{}
			hasBeforeCutoff = false
			prevMatchAt = time.Time{}
			continue
		}
		if !prevMatchAt.IsZero() && sampled.Sub(prevMatchAt) > maxGap {
			if !sampled.Before(cutoff) {
				return 0, gapReason
			}
			earliestContinuous = sampled
			hasBeforeCutoff = !sampled.After(cutoff)
		} else {
			if earliestContinuous.IsZero() {
				earliestContinuous = sampled
			}
			if !sampled.After(cutoff) {
				hasBeforeCutoff = true
			}
		}
		prevMatchAt = sampled
		latestGoodAt = sampled
	}

	if latestGoodAt.IsZero() || at.Sub(latestGoodAt) > maxGap {
		return 0, staleReason
	}
	if !hasBeforeCutoff || earliestContinuous.IsZero() {
		return at.Sub(earliestContinuous), notMetReason
	}
	return at.Sub(earliestContinuous), ""
}

func resolveAuxiliaryRunning(aux AuxiliaryUnitInput, at time.Time, policy StagingPolicy) (bool, string) {
	if strings.TrimSpace(aux.RunState) != "" {
		ok, reason := isFreshGoodObservation(aux.RunStateQuality, aux.RunStateAt, at, policy.MaxTelemetryAge)
		if !ok {
			return false, "AUXILIARY_STATE_" + reason
		}
		switch normalizeRunState(aux.RunState) {
		case RunStateRunning:
			return true, ""
		case RunStateStopped:
			return false, "AUXILIARY_ALREADY_STOPPED"
		case RunStateStarting:
			return false, "AUXILIARY_STARTING"
		default:
			return false, "AUXILIARY_STATE_UNKNOWN"
		}
	}
	if aux.Power != nil {
		ok, reason := isFreshGoodObservation(aux.Power.Quality, aux.Power.SampledAt, at, policy.MaxTelemetryAge)
		if !ok {
			return false, "AUXILIARY_POWER_" + reason
		}
		if finite(aux.Power.Value) && aux.Power.Value > policy.AuxiliaryRunningPowerKW {
			return true, ""
		}
		return false, "AUXILIARY_ALREADY_STOPPED"
	}
	return false, "AUXILIARY_STATE_MISSING"
}

func resolveAuxiliaryPowerSaving(aux AuxiliaryUnitInput, at time.Time, maxAge time.Duration) (float64, string) {
	if aux.Power != nil {
		if ok, _ := isFreshGoodObservation(aux.Power.Quality, aux.Power.SampledAt, at, maxAge); ok && finite(aux.Power.Value) && aux.Power.Value > 0 {
			return aux.Power.Value, "MEASURED_POWER"
		}
	}
	if aux.RatedPowerKW > 0 && finite(aux.RatedPowerKW) {
		return aux.RatedPowerKW, "RATED_POWER_NAMEPLATE"
	}
	return 0, "UNMETERED"
}

func collectServedChillers(aux AuxiliaryUnitInput, edgesByAux map[string][]string) []string {
	seen := map[string]bool{}
	refs := make([]string, 0, len(aux.ServesChillerIDs))
	appendUnique := func(raw string) {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			return
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, clean)
	}
	for _, id := range aux.ServesChillerIDs {
		appendUnique(id)
	}
	if aux.AssetID != "" {
		for _, id := range edgesByAux[strings.ToLower(aux.AssetID)] {
			appendUnique(id)
		}
	}
	if aux.AssetCode != "" {
		for _, id := range edgesByAux[strings.ToLower(aux.AssetCode)] {
			appendUnique(id)
		}
	}
	sort.Strings(refs)
	return refs
}

func normalizeAuxiliaryAssetType(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case AssetTypeCHWP, "CHWP":
		return AssetTypeCHWP, true
	case AssetTypeCWP, "CWP":
		return AssetTypeCWP, true
	case AssetTypeCoolingTower:
		return AssetTypeCoolingTower, true
	default:
		return "", false
	}
}

func normalizeRunState(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "RUNNING", "ON", "ACTIVE":
		return RunStateRunning
	case "STOPPED", "OFF", "IDLE":
		return RunStateStopped
	case "STARTING":
		return RunStateStarting
	default:
		return RunStateUnknown
	}
}

func isFreshGoodObservation(quality string, sampledAt, at time.Time, maxAge time.Duration) (bool, string) {
	if sampledAt.IsZero() {
		return false, "MISSING"
	}
	sampled := sampledAt.UTC()
	ref := at.UTC()
	if sampled.After(ref) {
		return false, "FUTURE_TIMESTAMP"
	}
	q := strings.ToUpper(strings.TrimSpace(quality))
	if q == "" {
		return false, "QUALITY_UNKNOWN"
	}
	if q != "GOOD" {
		return false, "QUALITY_" + q
	}
	if ref.Sub(sampled) > maxAge {
		return false, "STALE"
	}
	return true, ""
}

func resolveFreshTemp(primary, fallback *TelemetryValue, at time.Time, maxAge time.Duration) *float64 {
	for _, candidate := range []*TelemetryValue{primary, fallback} {
		if candidate == nil {
			continue
		}
		if ok, _ := isFreshGoodObservation(candidate.Quality, candidate.SampledAt, at, maxAge); ok && finite(candidate.Value) {
			value := candidate.Value
			return &value
		}
	}
	return nil
}

func indexChillerFits(fits []ChillerFit) map[string]ChillerFit {
	indexed := make(map[string]ChillerFit, len(fits))
	for _, fit := range fits {
		key := strings.ToLower(strings.TrimSpace(fit.DeviceID))
		if key != "" {
			indexed[key] = fit
		}
	}
	return indexed
}

func matchChillerFit(unit ChillerUnitInput, fits map[string]ChillerFit) *ChillerFit {
	candidates := make([]string, 0, 4+len(unit.BoundDeviceIDs))
	candidates = append(candidates, unit.AssetID, unit.MeterDeviceID, unit.MeterSourceKey, unit.AssetCode)
	candidates = append(candidates, unit.BoundDeviceIDs...)
	for _, candidate := range candidates {
		key := strings.ToLower(strings.TrimSpace(candidate))
		if key == "" {
			continue
		}
		if fit, ok := fits[key]; ok {
			copyFit := fit
			return &copyFit
		}
	}
	return nil
}

func effectiveChillerCapacityKW(unit ChillerUnitInput, fit *ChillerFit) float64 {
	if unit.RatedCapacityKW > 0 && finite(unit.RatedCapacityKW) {
		return unit.RatedCapacityKW
	}
	if fit != nil && fit.CapacityReference != nil && fit.CapacityReference.KW > 0 && finite(fit.CapacityReference.KW) {
		return fit.CapacityReference.KW
	}
	return 0
}

func fitCapacityReferenceKW(unit ChillerUnitInput, fit ChillerFit) float64 {
	if fit.CapacityReference != nil && fit.CapacityReference.KW > 0 && finite(fit.CapacityReference.KW) {
		return fit.CapacityReference.KW
	}
	return effectiveChillerCapacityKW(unit, &fit)
}

func (chiller ChillerFit) resolveCoefficients() ([]float64, bool, bool) {
	if len(chiller.coefficients) >= 4 {
		return chiller.coefficients, chiller.withCondenser && len(chiller.coefficients) >= 5, true
	}
	if len(chiller.Coefficients) == 0 {
		return nil, false, false
	}
	intercept, ok0 := chiller.Coefficients["intercept"]
	loadRatio, ok1 := chiller.Coefficients["loadRatio"]
	loadRatioSq, ok2 := chiller.Coefficients["loadRatioSquared"]
	chwSupply, ok3 := chiller.Coefficients["chilledWaterSupplyTempC"]
	if !ok0 || !ok1 || !ok2 || !ok3 {
		return nil, false, false
	}
	cwRet, hasCond := chiller.Coefficients["condenserReturnTempC"]
	if hasCond {
		return []float64{intercept, loadRatio, loadRatioSq, chwSupply, cwRet}, true, true
	}
	return []float64{intercept, loadRatio, loadRatioSq, chwSupply}, false, true
}

func (chiller ChillerFit) safePredictCOP(loadRatio, supply float64, condenser *float64) (float64, bool) {
	coeffs, needsCondenser, ok := chiller.resolveCoefficients()
	if !ok || !finite(loadRatio) || !finite(supply) {
		return 0, false
	}
	if needsCondenser && (condenser == nil || !finite(*condenser)) {
		return 0, false
	}
	value := coeffs[0] + coeffs[1]*loadRatio + coeffs[2]*loadRatio*loadRatio + coeffs[3]*supply
	if needsCondenser {
		value += coeffs[4] * *condenser
	}
	if !finite(value) {
		return 0, false
	}
	return value, true
}

func unitDisplayName(unit ChillerUnitInput) string {
	if unit.AssetName != "" {
		return unit.AssetName
	}
	if unit.AssetCode != "" {
		return unit.AssetCode
	}
	return unit.AssetID
}

func auxDisplayName(aux AuxiliaryUnitInput) string {
	if aux.AssetName != "" {
		return aux.AssetName
	}
	if aux.AssetCode != "" {
		return aux.AssetCode
	}
	return aux.AssetID
}

const maximumStagingSnapshotResponseBytes = int64(4 << 20)

// ReadStagingSnapshot queries ClickHouse telemetry_history.observations for the Site's latest
// chiller, cooling-load, header-supply and auxiliary observations resolved by SiteTwin semantic roles
// and recent chiller run-state and load history.
func (reader *ClickHouseInputReader) ReadStagingSnapshot(ctx context.Context, query StagingSnapshotQuery) (StagingSnapshot, error) {
	if reader == nil || reader.baseURL == nil || reader.client == nil {
		return StagingSnapshot{}, errors.New("optimization staging snapshot reader is unavailable")
	}
	if !uuidPattern.MatchString(query.TenantID) || !uuidPattern.MatchString(query.SiteID) {
		return StagingSnapshot{}, errors.New("tenantId and siteId must be UUIDs")
	}
	if query.At.IsZero() {
		return StagingSnapshot{}, errors.New("at timestamp is required")
	}
	policy, err := query.Policy.normalized()
	if err != nil {
		return StagingSnapshot{}, err
	}
	at := query.At.UTC()
	twin := query.Twin
	if twin == nil && query.Topology != nil {
		twin = query.Topology.Twin
	}
	topology := query.Topology
	if topology == nil && twin != nil {
		derived := stagingTopologyFromTwin(*twin)
		topology = &derived
	}

	latestFrom := at.Add(-2 * maxDuration(policy.MaxTelemetryAge, time.Hour))
	runHistoryFrom := at.Add(-maxDuration(policy.IdleAuxiliaryDebounce, policy.MinChillerRunDuration) - policy.MaxTelemetryAge)
	loadHistoryFrom := at.Add(-maxDuration(20*time.Minute, policy.StageDownDwellDuration+5*time.Minute))
	earliestHistoryFrom := runHistoryFrom
	if loadHistoryFrom.Before(earliestHistoryFrom) {
		earliestHistoryFrom = loadHistoryFrom
	}

	var latestFilterSQL, historyFilterSQL string
	if twin != nil {
		plan := buildTwinStagingQueryPlan(*twin)
		latestFilterSQL = buildDeviceTelemetryFilterSQL(plan.allBindings)
		if latestFilterSQL == "" {
			return buildStagingSnapshotFromObservations(query.TenantID, query.SiteID, at, twin, topology, nil, nil), nil
		}
		var histBranches []string
		if runFilter := buildDeviceTelemetryFilterSQL(plan.chillerRunStateBindings); runFilter != "" {
			histBranches = append(histBranches, fmt.Sprintf("(%s AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC'))", runFilter, formatOptimizationTime(runHistoryFrom)))
		}
		if loadFilter := buildDeviceTelemetryFilterSQL(plan.coolingLoadBindings); loadFilter != "" {
			histBranches = append(histBranches, fmt.Sprintf("(%s AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC'))", loadFilter, formatOptimizationTime(loadHistoryFrom)))
		}
		if len(histBranches) > 0 {
			historyFilterSQL = "(" + strings.Join(histBranches, " OR ") + ")"
		}
	} else {
		canonicalLatest := []string{
			chillerPowerKey, plantCoolingLoadKey, chilledWaterEnteringKey,
			chilledWaterLeavingKey, condenserWaterLeavingKey, "supply_water_temperature", "run_state",
		}
		latestFilterSQL = "telemetry_key IN (" + joinQuotedSQL(canonicalLatest) + ")"
		historyFilterSQL = fmt.Sprintf(
			"((telemetry_key = 'run_state' AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')) OR (telemetry_key = '%s' AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')))",
			formatOptimizationTime(runHistoryFrom), plantCoolingLoadKey, formatOptimizationTime(loadHistoryFrom),
		)
	}

	latestSQL := fmt.Sprintf(`SELECT
 toString(device_id) AS deviceId,
 source_id AS sourceId,
 telemetry_key AS telemetryKey,
 value_number AS valueNumber,
 value_string AS valueString,
 ifNull(unit, '') AS unit,
 quality,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampledAt
FROM (
 SELECT
  device_id,
  source_id,
  telemetry_key,
  value_number,
  value_string,
  unit,
  quality,
  sampled_at,
  row_number() OVER (
   PARTITION BY device_id, telemetry_key
   ORDER BY sampled_at DESC, received_at DESC, history_sequence DESC, observation_id DESC
  ) AS row_num
 FROM telemetry_history.observations
 WHERE tenant_id = toUUID('%s')
   AND site_id = toUUID('%s')
   AND %s
   AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
   AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
   AND device_id IS NOT NULL
)
WHERE row_num = 1
FORMAT JSONEachRow`,
		clickHouseString(query.TenantID), clickHouseString(query.SiteID), latestFilterSQL,
		formatOptimizationTime(latestFrom), formatOptimizationTime(at), formatOptimizationTime(at),
	)

	latestPayload, err := reader.queryLimited(ctx, latestSQL, maximumStagingSnapshotResponseBytes)
	if err != nil {
		return StagingSnapshot{}, fmt.Errorf("query staging latest observations: %w", err)
	}
	latestRows, err := decodeStagingTelemetryRows(bytes.NewReader(latestPayload))
	if err != nil {
		return StagingSnapshot{}, fmt.Errorf("decode staging latest observations: %w", err)
	}

	var historyRows []stagingTelemetryRow
	if historyFilterSQL != "" {
		historySQL := fmt.Sprintf(`SELECT
 toString(device_id) AS deviceId,
 source_id AS sourceId,
 telemetry_key AS telemetryKey,
 value_number AS valueNumber,
 value_string AS valueString,
 ifNull(unit, '') AS unit,
 quality,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampledAt
FROM telemetry_history.observations
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND %s
  AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
  AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
  AND device_id IS NOT NULL
ORDER BY sampled_at ASC, received_at ASC, history_sequence ASC
FORMAT JSONEachRow`,
			clickHouseString(query.TenantID), clickHouseString(query.SiteID), historyFilterSQL,
			formatOptimizationTime(earliestHistoryFrom), formatOptimizationTime(at), formatOptimizationTime(at),
		)

		historyPayload, err := reader.queryLimited(ctx, historySQL, maximumStagingSnapshotResponseBytes)
		if err != nil {
			return StagingSnapshot{}, fmt.Errorf("query staging history: %w", err)
		}
		historyRows, err = decodeStagingTelemetryRows(bytes.NewReader(historyPayload))
		if err != nil {
			return StagingSnapshot{}, fmt.Errorf("decode staging history: %w", err)
		}
	}

	return buildStagingSnapshotFromObservations(query.TenantID, query.SiteID, at, twin, topology, latestRows, historyRows), nil
}

type twinStagingQueryPlan struct {
	allBindings             []planttwin.PointBinding
	chillerRunStateBindings []planttwin.PointBinding
	coolingLoadBindings     []planttwin.PointBinding
	supplyTempBindings      []planttwin.PointBinding
}

func buildTwinStagingQueryPlan(twin planttwin.SiteTwin) twinStagingQueryPlan {
	var plan twinStagingQueryPlan
	seen := map[string]struct{}{}
	addPoint := func(dst *[]planttwin.PointBinding, pb planttwin.PointBinding) {
		keys := collectPointKeys(pb)
		if len(keys) == 0 {
			return
		}
		id := strings.ToLower(strings.TrimSpace(pb.PointID))
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(pb.DeviceID)) + "\x00" + keys[0]
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		*dst = append(*dst, pb)
	}

	chillerRoles := []planttwin.SemanticRole{
		planttwin.RoleActivePower,
		planttwin.RoleRunState,
		planttwin.RoleCHWLeavingTemp,
		planttwin.RoleCHWEnteringTemp,
		planttwin.RoleCWLeavingTemp,
		planttwin.RoleCWEnteringTemp,
	}
	for _, chiller := range twin.UnitsByType(planttwin.AssetTypeChiller) {
		for _, role := range chillerRoles {
			if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, role); ok {
				addPoint(&plan.allBindings, pb)
				if role == planttwin.RoleRunState {
					plan.chillerRunStateBindings = append(plan.chillerRunStateBindings, pb)
				}
			}
		}
	}

	auxTypes := []string{
		planttwin.AssetTypeChilledWaterPump,
		planttwin.AssetTypeCoolingWaterPump,
		planttwin.AssetTypeCoolingTower,
	}
	for _, aux := range twin.UnitsByType(auxTypes...) {
		for _, role := range []planttwin.SemanticRole{planttwin.RoleActivePower, planttwin.RoleRunState} {
			if pb, ok := twin.ResolveAssetPoint(aux.AssetID, role); ok {
				addPoint(&plan.allBindings, pb)
			}
		}
	}

	plan.coolingLoadBindings = resolvePrimaryPlantRolePoints(twin, planttwin.RoleCoolingLoad)
	for _, pb := range plan.coolingLoadBindings {
		addPoint(&plan.allBindings, pb)
	}
	plan.supplyTempBindings = resolvePrimaryPlantRolePoints(twin, planttwin.RoleSupplyWaterTemp)
	for _, pb := range plan.supplyTempBindings {
		addPoint(&plan.allBindings, pb)
	}
	return plan
}

func resolvePrimaryPlantRolePoints(twin planttwin.SiteTwin, role planttwin.SemanticRole) []planttwin.PointBinding {
	all := twin.PointsByRole(role)
	if len(all) == 0 {
		return nil
	}
	primary := make([]planttwin.PointBinding, 0, len(all))
	for _, pb := range all {
		if !pb.IsCopied {
			primary = append(primary, pb)
		}
	}
	if len(primary) == 0 {
		primary = all
	}
	// Prefer loop- or site-bound points before individual branch units when both carry the same role.
	loopIDs := map[string]bool{}
	for _, unit := range twin.Units {
		unitType := strings.ToUpper(strings.TrimSpace(unit.AssetType))
		if unitType == planttwin.AssetTypeChilledWaterLoop || unitType == planttwin.AssetTypeCoolingWaterLoop {
			loopIDs[strings.ToLower(strings.TrimSpace(unit.AssetID))] = true
		}
	}
	var prioritized []planttwin.PointBinding
	var rest []planttwin.PointBinding
	for _, pb := range primary {
		subj := strings.ToUpper(strings.TrimSpace(pb.SubjectType))
		if loopIDs[strings.ToLower(strings.TrimSpace(pb.AssetID))] || subj == planttwin.SubjectTypeSite {
			prioritized = append(prioritized, pb)
		} else {
			rest = append(rest, pb)
		}
	}
	combined := append(prioritized, rest...)
	seen := map[string]struct{}{}
	out := make([]planttwin.PointBinding, 0, len(combined))
	for _, pb := range combined {
		key := strings.ToLower(strings.TrimSpace(pb.PointID))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(pb.DeviceID)) + "\x00" + strings.Join(collectPointKeys(pb), ",")
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, pb)
	}
	return out
}

func collectPointKeys(pb planttwin.PointBinding) []string {
	var keys []string
	for _, raw := range []string{pb.WireSourceKey, pb.PointCode} {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			continue
		}
		dup := false
		for _, existing := range keys {
			if existing == clean {
				dup = true
				break
			}
		}
		if !dup {
			keys = append(keys, clean)
		}
	}
	return keys
}

func buildDeviceTelemetryFilterSQL(bindings []planttwin.PointBinding) string {
	type deviceKeys struct {
		deviceID  string
		sourceKey string
		keys      []string
	}
	byGroup := map[string]*deviceKeys{}
	for _, pb := range bindings {
		keys := collectPointKeys(pb)
		if len(keys) == 0 {
			continue
		}
		devID := strings.TrimSpace(pb.DeviceID)
		sourceID := firstNonEmptyString(pb.DeviceSourceKey, pb.DeviceCode)
		groupKey := "dev:" + strings.ToLower(devID)
		if devID == "" {
			if sourceID == "" {
				groupKey = "any"
			} else {
				groupKey = "src:" + strings.ToLower(sourceID)
			}
		}
		entry := byGroup[groupKey]
		if entry == nil {
			entry = &deviceKeys{deviceID: devID, sourceKey: sourceID}
			byGroup[groupKey] = entry
		}
		for _, k := range keys {
			dup := false
			for _, existing := range entry.keys {
				if existing == k {
					dup = true
					break
				}
			}
			if !dup {
				entry.keys = append(entry.keys, k)
			}
		}
	}
	if len(byGroup) == 0 {
		return ""
	}
	groupOrder := make([]string, 0, len(byGroup))
	for k := range byGroup {
		groupOrder = append(groupOrder, k)
	}
	sort.Strings(groupOrder)

	clauses := make([]string, 0, len(groupOrder))
	for _, gk := range groupOrder {
		entry := byGroup[gk]
		sort.Strings(entry.keys)
		keyClause := "telemetry_key IN (" + joinQuotedSQL(entry.keys) + ")"
		switch {
		case uuidPattern.MatchString(entry.deviceID):
			clauses = append(clauses, fmt.Sprintf("(device_id = toUUID('%s') AND %s)", clickHouseString(entry.deviceID), keyClause))
		case entry.deviceID != "":
			clauses = append(clauses, fmt.Sprintf("(toString(device_id) = '%s' AND %s)", clickHouseString(entry.deviceID), keyClause))
		case entry.sourceKey != "":
			clauses = append(clauses, fmt.Sprintf("(source_id = '%s' AND %s)", clickHouseString(entry.sourceKey), keyClause))
		default:
			clauses = append(clauses, "("+keyClause+")")
		}
	}
	if len(clauses) == 1 {
		return clauses[0]
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

func joinQuotedSQL(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, "'"+clickHouseString(v)+"'")
	}
	return strings.Join(quoted, ",")
}

type stagingTelemetryRow struct {
	DeviceID     string   `json:"deviceId"`
	SourceID     string   `json:"sourceId"`
	TelemetryKey string   `json:"telemetryKey"`
	ValueNumber  *float64 `json:"valueNumber"`
	ValueString  *string  `json:"valueString"`
	Unit         string   `json:"unit"`
	Quality      string   `json:"quality"`
	SampledAt    time.Time
}

func decodeStagingTelemetryRows(source io.Reader) ([]stagingTelemetryRow, error) {
	type rawRow struct {
		DeviceID     string   `json:"deviceId"`
		SourceID     string   `json:"sourceId"`
		TelemetryKey string   `json:"telemetryKey"`
		ValueNumber  *float64 `json:"valueNumber"`
		ValueString  *string  `json:"valueString"`
		Unit         string   `json:"unit"`
		Quality      string   `json:"quality"`
		SampledAt    string   `json:"sampledAt"`
	}
	decoder := json.NewDecoder(source)
	rows := []stagingTelemetryRow{}
	for {
		var r rawRow
		err := decoder.Decode(&r)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		sampledAt, err := time.Parse(time.RFC3339Nano, r.SampledAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, stagingTelemetryRow{
			DeviceID:     r.DeviceID,
			SourceID:     r.SourceID,
			TelemetryKey: r.TelemetryKey,
			ValueNumber:  r.ValueNumber,
			ValueString:  r.ValueString,
			Unit:         r.Unit,
			Quality:      r.Quality,
			SampledAt:    sampledAt.UTC(),
		})
	}
}

func buildStagingSnapshotFromObservations(
	tenantID, siteID string,
	at time.Time,
	twin *planttwin.SiteTwin,
	topology *StagingTopology,
	latest []stagingTelemetryRow,
	history []stagingTelemetryRow,
) StagingSnapshot {
	if twin == nil && topology != nil {
		twin = topology.Twin
	}
	if topology == nil && twin != nil {
		derived := stagingTopologyFromTwin(*twin)
		topology = &derived
	}
	snapshot := StagingSnapshot{
		TenantID:    tenantID,
		SiteID:      siteID,
		EvaluatedAt: at.UTC(),
	}
	applyStagingTopology(&snapshot, topology)

	byDeviceKey := indexStagingRowsByDevice(latest)
	if twin != nil {
		populateSnapshotFromTwin(&snapshot, *twin, topology, byDeviceKey, history)
		return snapshot
	}

	if topology != nil {
		for _, row := range latest {
			if row.ValueNumber == nil {
				continue
			}
			switch row.TelemetryKey {
			case plantCoolingLoadKey:
				if snapshot.PlantCoolingLoad == nil && (topology.CoolingLoadDeviceID == "" ||
					strings.EqualFold(row.DeviceID, topology.CoolingLoadDeviceID) ||
					strings.EqualFold(row.SourceID, topology.CoolingLoadDeviceID)) {
					v := telemetryValueFromRow(row)
					snapshot.PlantCoolingLoad = &v
				}
			case "supply_water_temperature":
				if snapshot.HeaderSupplyC == nil {
					v := telemetryValueFromRow(row)
					snapshot.HeaderSupplyC = &v
				}
			}
		}

		plantLoadDeviceID := topology.CoolingLoadDeviceID
		if plantLoadDeviceID == "" && snapshot.PlantCoolingLoad != nil {
			plantLoadDeviceID = snapshot.PlantCoolingLoad.DeviceID
		}
		historyByDevice := map[string][]RunStateSample{}
		for _, row := range history {
			switch row.TelemetryKey {
			case plantCoolingLoadKey:
				if plantLoadDeviceID != "" &&
					!strings.EqualFold(row.DeviceID, plantLoadDeviceID) &&
					!strings.EqualFold(row.SourceID, plantLoadDeviceID) {
					continue
				}
				snapshot.PlantLoadHistory = append(snapshot.PlantLoadHistory, telemetryValueFromRow(row))
			case "run_state":
				if row.ValueString == nil {
					continue
				}
				sample := RunStateSample{State: *row.ValueString, Quality: row.Quality, SampledAt: row.SampledAt}
				for _, id := range []string{row.DeviceID, row.SourceID} {
					if k := strings.ToLower(strings.TrimSpace(id)); k != "" {
						historyByDevice[k] = append(historyByDevice[k], sample)
					}
				}
			}
		}

		snapshot.Edges = append(snapshot.Edges, topology.Edges...)
		for _, auxB := range topology.Auxiliaries {
			aux := AuxiliaryUnitInput{
				AssetID:          auxB.AssetID,
				AssetCode:        auxB.AssetCode,
				AssetName:        auxB.AssetName,
				AssetType:        auxB.AssetType,
				DeviceID:         auxB.DeviceID,
				DeviceSourceKey:  auxB.DeviceSourceKey,
				BoundDeviceIDs:   append([]string(nil), auxB.BoundDeviceIDs...),
				RatedPowerKW:     auxB.RatedPowerKW,
				ServesChillerIDs: append([]string(nil), auxB.ServesChillerIDs...),
			}
			keys := append([]string{aux.DeviceID, aux.DeviceSourceKey}, aux.BoundDeviceIDs...)
			keys = append(keys, aux.AssetID, aux.AssetCode)
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, "run_state"); ok && r.ValueString != nil {
				aux.RunState, aux.RunStateQuality, aux.RunStateAt = *r.ValueString, r.Quality, r.SampledAt
			}
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, chillerPowerKey); ok && r.ValueNumber != nil {
				v := telemetryValueFromRow(r)
				aux.Power = &v
			}
			snapshot.Auxiliaries = append(snapshot.Auxiliaries, aux)
		}
		for _, binding := range topology.Chillers {
			unit := ChillerUnitInput{
				AssetID:                binding.AssetID,
				AssetCode:              binding.AssetCode,
				AssetName:              binding.AssetName,
				MeterDeviceID:          binding.MeterDeviceID,
				MeterSourceKey:         binding.MeterSourceKey,
				BoundDeviceIDs:         append([]string(nil), binding.BoundDeviceIDs...),
				RatedCapacityKW:        binding.RatedCapacityKW,
				RatedPowerKW:           binding.RatedPowerKW,
				FeederAuxiliaryPowerKW: binding.FeederAuxiliaryPowerKW,
			}
			keys := append([]string{unit.MeterDeviceID, unit.MeterSourceKey}, unit.BoundDeviceIDs...)
			keys = append(keys, unit.AssetID, unit.AssetCode)
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, "run_state"); ok && r.ValueString != nil {
				unit.RunState, unit.RunStateQuality, unit.RunStateAt = *r.ValueString, r.Quality, r.SampledAt
			}
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, chillerPowerKey); ok && r.ValueNumber != nil {
				v := telemetryValueFromRow(r)
				unit.Power = &v
			}
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, chilledWaterEnteringKey); ok && r.ValueNumber != nil {
				v := telemetryValueFromRow(r)
				unit.ChwEnteringC = &v
			}
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, chilledWaterLeavingKey); ok && r.ValueNumber != nil {
				v := telemetryValueFromRow(r)
				unit.ChwLeavingC = &v
			}
			if r, ok := findPointAcrossKeys(byDeviceKey, keys, condenserWaterLeavingKey); ok && r.ValueNumber != nil {
				v := telemetryValueFromRow(r)
				unit.CwLeavingC = &v
			}
			for _, k := range keys {
				if hist := historyByDevice[strings.ToLower(strings.TrimSpace(k))]; len(hist) > 0 {
					unit.RunStateHistory = append([]RunStateSample(nil), hist...)
					break
				}
			}
			snapshot.Chillers = append(snapshot.Chillers, unit)
		}
	}
	return snapshot
}

func populateSnapshotFromTwin(
	snapshot *StagingSnapshot,
	twin planttwin.SiteTwin,
	topology *StagingTopology,
	byDeviceKey map[string]map[string]stagingTelemetryRow,
	history []stagingTelemetryRow,
) {
	plan := buildTwinStagingQueryPlan(twin)

	if len(plan.coolingLoadBindings) > 0 {
		for _, pb := range plan.coolingLoadBindings {
			if row, ok := matchStagingObservation(byDeviceKey, pb); ok && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				snapshot.PlantCoolingLoad = &v
				break
			}
		}
		primaryLoad := plan.coolingLoadBindings[0]
		for _, row := range history {
			if rowMatchesPointBinding(row, primaryLoad) {
				snapshot.PlantLoadHistory = append(snapshot.PlantLoadHistory, telemetryValueFromRow(row))
			}
		}
	}
	for _, pb := range plan.supplyTempBindings {
		if row, ok := matchStagingObservation(byDeviceKey, pb); ok && row.ValueNumber != nil {
			v := telemetryValueFromRow(row)
			snapshot.HeaderSupplyC = &v
			break
		}
	}

	if topology != nil {
		snapshot.Edges = append(snapshot.Edges, topology.Edges...)
	}

	chillerByAssetID := map[string]ChillerMeterBinding{}
	if topology != nil {
		for _, c := range topology.Chillers {
			chillerByAssetID[strings.ToLower(strings.TrimSpace(c.AssetID))] = c
		}
	}
	for _, chiller := range twin.UnitsByType(planttwin.AssetTypeChiller) {
		meta := chillerByAssetID[strings.ToLower(strings.TrimSpace(chiller.AssetID))]
		unit := ChillerUnitInput{
			AssetID:                chiller.AssetID,
			AssetCode:              chiller.AssetCode,
			AssetName:              chiller.AssetName,
			MeterDeviceID:          meta.MeterDeviceID,
			MeterSourceKey:         meta.MeterSourceKey,
			BoundDeviceIDs:         append([]string(nil), meta.BoundDeviceIDs...),
			RatedCapacityKW:        derefFloat(chiller.RatedCapacityKW),
			RatedPowerKW:           derefFloat(chiller.RatedPowerKW),
			FeederAuxiliaryPowerKW: chiller.FeederAuxiliaryPowerKW,
		}
		if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleRunState); ok {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueString != nil {
				unit.RunState, unit.RunStateQuality, unit.RunStateAt = *row.ValueString, row.Quality, row.SampledAt
			}
			for _, row := range history {
				if row.ValueString != nil && rowMatchesPointBinding(row, pb) {
					unit.RunStateHistory = append(unit.RunStateHistory, RunStateSample{
						State: *row.ValueString, Quality: row.Quality, SampledAt: row.SampledAt,
					})
				}
			}
		}
		if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleActivePower); ok {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				unit.Power = &v
			}
		}
		if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleCHWEnteringTemp); ok {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				unit.ChwEnteringC = &v
			}
		}
		if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleCHWLeavingTemp); ok {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				unit.ChwLeavingC = &v
			}
		}
		if pb, ok := twin.ResolveAssetPoint(chiller.AssetID, planttwin.RoleCWLeavingTemp); ok {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				unit.CwLeavingC = &v
			}
		}
		snapshot.Chillers = append(snapshot.Chillers, unit)
	}

	auxByAssetID := map[string]AuxiliaryUnitBinding{}
	if topology != nil {
		for _, a := range topology.Auxiliaries {
			auxByAssetID[strings.ToLower(strings.TrimSpace(a.AssetID))] = a
		}
	}
	auxTypes := []string{
		planttwin.AssetTypeChilledWaterPump,
		planttwin.AssetTypeCoolingWaterPump,
		planttwin.AssetTypeCoolingTower,
	}
	for _, auxUnit := range twin.UnitsByType(auxTypes...) {
		normType, ok := normalizeAuxiliaryAssetType(auxUnit.AssetType)
		if !ok {
			continue
		}
		meta := auxByAssetID[strings.ToLower(strings.TrimSpace(auxUnit.AssetID))]
		aux := AuxiliaryUnitInput{
			AssetID:          auxUnit.AssetID,
			AssetCode:        auxUnit.AssetCode,
			AssetName:        auxUnit.AssetName,
			AssetType:        normType,
			DeviceID:         meta.DeviceID,
			DeviceSourceKey:  meta.DeviceSourceKey,
			BoundDeviceIDs:   append([]string(nil), meta.BoundDeviceIDs...),
			RatedPowerKW:     derefFloat(auxUnit.RatedPowerKW),
			ServesChillerIDs: append([]string(nil), meta.ServesChillerIDs...),
		}
		if pb, foundPB := twin.ResolveAssetPoint(auxUnit.AssetID, planttwin.RoleRunState); foundPB {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueString != nil {
				aux.RunState, aux.RunStateQuality, aux.RunStateAt = *row.ValueString, row.Quality, row.SampledAt
			}
		}
		if pb, foundPB := twin.ResolveAssetPoint(auxUnit.AssetID, planttwin.RoleActivePower); foundPB {
			if row, found := matchStagingObservation(byDeviceKey, pb); found && row.ValueNumber != nil {
				v := telemetryValueFromRow(row)
				aux.Power = &v
			}
		}
		snapshot.Auxiliaries = append(snapshot.Auxiliaries, aux)
	}
}

func indexStagingRowsByDevice(rows []stagingTelemetryRow) map[string]map[string]stagingTelemetryRow {
	byDeviceKey := map[string]map[string]stagingTelemetryRow{}
	for _, row := range rows {
		for _, id := range []string{row.DeviceID, row.SourceID} {
			k := strings.ToLower(strings.TrimSpace(id))
			if k == "" {
				continue
			}
			if byDeviceKey[k] == nil {
				byDeviceKey[k] = map[string]stagingTelemetryRow{}
			}
			byDeviceKey[k][row.TelemetryKey] = row
		}
	}
	return byDeviceKey
}

func telemetryValueFromRow(row stagingTelemetryRow) TelemetryValue {
	val := 0.0
	if row.ValueNumber != nil {
		val = *row.ValueNumber
	}
	return TelemetryValue{
		DeviceID: row.DeviceID, SourceKey: row.SourceID, PointKey: row.TelemetryKey,
		Value: val, Unit: row.Unit, Quality: row.Quality, SampledAt: row.SampledAt,
	}
}

func matchStagingObservation(byDeviceKey map[string]map[string]stagingTelemetryRow, pb planttwin.PointBinding) (stagingTelemetryRow, bool) {
	keys := collectPointKeys(pb)
	if len(keys) == 0 {
		return stagingTelemetryRow{}, false
	}
	devRefs := []string{pb.DeviceID, pb.DeviceSourceKey, pb.DeviceCode}
	for _, devRef := range devRefs {
		points := byDeviceKey[strings.ToLower(strings.TrimSpace(devRef))]
		if points == nil {
			continue
		}
		for _, k := range keys {
			if row, ok := points[k]; ok {
				if pb.DeviceID == "" || row.DeviceID == "" || strings.EqualFold(strings.TrimSpace(row.DeviceID), strings.TrimSpace(pb.DeviceID)) {
					return row, true
				}
			}
		}
	}
	return stagingTelemetryRow{}, false
}

func rowMatchesPointBinding(row stagingTelemetryRow, pb planttwin.PointBinding) bool {
	matchedKey := false
	for _, k := range collectPointKeys(pb) {
		if row.TelemetryKey == k {
			matchedKey = true
			break
		}
	}
	if !matchedKey {
		return false
	}
	if strings.TrimSpace(pb.DeviceID) != "" && strings.TrimSpace(row.DeviceID) != "" {
		return strings.EqualFold(strings.TrimSpace(row.DeviceID), strings.TrimSpace(pb.DeviceID))
	}
	for _, ref := range []string{pb.DeviceID, pb.DeviceSourceKey, pb.DeviceCode} {
		clean := strings.TrimSpace(ref)
		if clean == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.DeviceID), clean) || strings.EqualFold(strings.TrimSpace(row.SourceID), clean) {
			return true
		}
	}
	return false
}

func findPointAcrossKeys(
	byDeviceKey map[string]map[string]stagingTelemetryRow,
	deviceKeys []string,
	pointKeys ...string,
) (stagingTelemetryRow, bool) {
	for _, dk := range deviceKeys {
		points := byDeviceKey[strings.ToLower(strings.TrimSpace(dk))]
		if points == nil {
			continue
		}
		for _, pk := range pointKeys {
			if row, ok := points[pk]; ok {
				return row, true
			}
		}
	}
	return stagingTelemetryRow{}, false
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func applyStagingTopology(snapshot *StagingSnapshot, topology *StagingTopology) {
	if snapshot == nil || topology == nil {
		return
	}
	if topology.Twin != nil && snapshot.Twin == nil {
		twinCopy := *topology.Twin
		snapshot.Twin = &twinCopy
	}
	if strings.TrimSpace(snapshot.SiteControlMode) == "" && strings.TrimSpace(topology.SiteControlMode) != "" {
		snapshot.SiteControlMode = strings.TrimSpace(topology.SiteControlMode)
		if strings.TrimSpace(snapshot.SiteControlReason) == "" {
			snapshot.SiteControlReason = strings.TrimSpace(topology.SiteControlReason)
		}
	}
	if len(topology.AssetControlLocks) > 0 {
		if snapshot.AssetControlLocks == nil {
			snapshot.AssetControlLocks = cloneAssetControlLocks(topology.AssetControlLocks)
		} else {
			for k, v := range topology.AssetControlLocks {
				normKey := strings.ToLower(strings.TrimSpace(k))
				if normKey == "" {
					continue
				}
				if _, exists := snapshot.AssetControlLocks[normKey]; !exists {
					snapshot.AssetControlLocks[normKey] = v
				}
			}
		}
	}
	if len(topology.CommandPointIDs) > 0 {
		if snapshot.CommandPointIDs == nil {
			snapshot.CommandPointIDs = make(map[string]string, len(topology.CommandPointIDs))
		}
		for k, v := range topology.CommandPointIDs {
			cleanVal := strings.TrimSpace(v)
			if cleanVal == "" {
				continue
			}
			if _, exists := snapshot.CommandPointIDs[k]; !exists {
				snapshot.CommandPointIDs[k] = cleanVal
			}
		}
	}
	if topology.OperatingSchedule != nil && snapshot.OperatingSchedule == nil {
		snapshot.OperatingSchedule = cloneOperatingSchedule(topology.OperatingSchedule)
	}
}

func cloneOperatingSchedule(src *planttwin.OperatingSchedule) *planttwin.OperatingSchedule {
	if src == nil {
		return nil
	}
	out := &planttwin.OperatingSchedule{
		ScheduleID:          src.ScheduleID,
		Timezone:            strings.TrimSpace(src.Timezone),
		PreCoolLeadMinutes:  src.PreCoolLeadMinutes,
		CoastDownLagMinutes: src.CoastDownLagMinutes,
		Revision:            src.Revision,
	}
	if len(src.Slots) > 0 {
		out.Slots = append([]planttwin.ScheduleSlot(nil), src.Slots...)
	}
	if len(src.Exceptions) > 0 {
		out.Exceptions = make([]planttwin.ScheduleException, len(src.Exceptions))
		for i, exc := range src.Exceptions {
			out.Exceptions[i] = exc
			if len(exc.ReplacementSlots) > 0 {
				out.Exceptions[i].ReplacementSlots = append([]planttwin.ScheduleSlot(nil), exc.ReplacementSlots...)
			}
		}
	}
	return out
}

func formatScheduleExceptionDetail(prefix string, exc planttwin.ScheduleException) string {
	title := strings.TrimSpace(exc.Title)
	if title == "" {
		title = strings.TrimSpace(exc.Kind)
	}
	dateRange := strings.TrimSpace(exc.StartDate)
	if end := strings.TrimSpace(exc.EndDate); end != "" && end != dateRange {
		dateRange += " 至 " + end
	}
	detail := prefix + "（" + title
	if dateRange != "" {
		detail += "，" + dateRange
	}
	if reason := strings.TrimSpace(exc.Reason); reason != "" {
		detail += "，原因：" + reason
	}
	return detail + "）。"
}

func evaluateOffHoursStop(snapshot StagingSnapshot, policy StagingPolicy, schedEval planttwin.ScheduleEvaluation) ([]StagingRecommendationDraft, []StagingDiagnostic) {
	if detail, active := siteControlExitDetail(snapshot); active {
		return []StagingRecommendationDraft{}, []StagingDiagnostic{{
			Scope:  RecommendationKindOffHoursStop,
			Status: "SUPPRESSED",
			Reason: "SITE_CONTROL_EXIT_ACTIVE",
			Detail: fmt.Sprintf("%s，暂停生成非营业时段停机建议。", detail),
		}}
	}
	at := snapshot.EvaluatedAt.UTC()
	chillers := append([]ChillerUnitInput(nil), snapshot.Chillers...)
	sort.Slice(chillers, func(i, j int) bool {
		if chillers[i].AssetCode != chillers[j].AssetCode {
			return chillers[i].AssetCode < chillers[j].AssetCode
		}
		return chillers[i].AssetID < chillers[j].AssetID
	})

	drafts := make([]StagingRecommendationDraft, 0)
	diagnostics := make([]StagingDiagnostic, 0, len(chillers))

	for _, unit := range chillers {
		if lock, locked := lookupAssetControlLock(snapshot.AssetControlLocks, unit.AssetID, unit.AssetCode); locked {
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindOffHoursStop, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
				Status: "SUPPRESSED", Reason: "ASSET_CONTROL_LOCKED",
				Detail: fmt.Sprintf("冷机 %s 已被单机锁定（%s）%s，暂停生成非营业时段停机建议。", unitDisplayName(unit), lock.LockMode, lockReasonSuffix(lock.Reason)),
			})
			continue
		}

		runState, runReason := resolveChillerRunState(unit, at, policy)
		if runState != RunStateRunning && runState != RunStateStarting {
			status := "NO_OP"
			reason := "CHILLER_ALREADY_STOPPED"
			if runState == RunStateUnknown {
				status = "SUPPRESSED"
				if runReason != "" {
					reason = runReason
				} else {
					reason = "CHILLER_STATE_UNKNOWN"
				}
			}
			diagnostics = append(diagnostics, StagingDiagnostic{
				Scope: RecommendationKindOffHoursStop, AssetID: unit.AssetID, AssetCode: unit.AssetCode,
				Status: status, Reason: reason,
				Detail: fmt.Sprintf("冷机 %s 当前状态为 %s，无需触发非营业时段停机建议。", unitDisplayName(unit), runState),
			})
			continue
		}

		targetID := unit.AssetID
		if targetID == "" {
			targetID = unit.AssetCode
		}
		savingKW, powerBasis := resolveChillerOffHoursPowerSaving(unit, at, policy)
		savingKWRounded := round(savingKW, 2)
		hourlyKWh := savingKWRounded
		shiftKWh := round(savingKW*policy.ShiftHours, 2)

		suggestedCap := SuggestedCapabilityStopUnitSequence
		suggestedPointID := lookupCommandPointID(snapshot.CommandPointIDs, targetID, unit.AssetCode, SuggestedCapabilityStopUnitSequence)
		if suggestedPointID == "" {
			if stopPointID := lookupCommandPointID(snapshot.CommandPointIDs, targetID, unit.AssetCode, SuggestedCapabilityStop); stopPointID != "" {
				suggestedCap = SuggestedCapabilityStop
				suggestedPointID = stopPointID
			}
		}

		tzLabel := "UTC"
		if loc := schedEval.LocalTime.Location(); loc != nil {
			tzLabel = loc.String()
		}
		scheduleNotice := fmt.Sprintf("站点 %s（%s）当前处于非供冷营业时间窗口", schedEval.LocalDate, tzLabel)
		if schedEval.ActiveException != nil && strings.EqualFold(strings.TrimSpace(schedEval.ActiveException.Kind), planttwin.ExceptionHolidayShutdown) {
			scheduleNotice = fmt.Sprintf("站点 %s 处于已批准节假日停机窗口（%s）", schedEval.LocalDate, strings.TrimSpace(schedEval.ActiveException.Title))
		}
		rationale := fmt.Sprintf(
			"%s，但冷机 %s 仍处于 %s 状态。建议值班主管人工复核并确认下发 %s 停机指令，预计可避免无效电耗 %.1f kW（约 %.1f kWh/小时、%.1f kWh/%.0f小时班次）。",
			scheduleNotice, unitDisplayName(unit), runState, suggestedCap, savingKWRounded, hourlyKWh, shiftKWh, policy.ShiftHours,
		)
		preconditions := []string{
			fmt.Sprintf("人工确认模式（%s）：必须经值班主管复核并完成 TOTP 双因子验证后方可下发 %s 停机指令。", ConfirmationModeManual, suggestedCap),
			"停机前确认现场无临时加班供冷、防冻保护或设备调试需求。",
			fmt.Sprintf("确认 %s 配套水泵、冷却塔与电动蝶阀联锁逻辑均处于正常受控状态。", unitDisplayName(unit)),
		}

		draft := StagingRecommendationDraft{
			ID:                       deterministicV7(snapshot.SiteID+":"+RecommendationKindOffHoursStop, targetID, at),
			Kind:                     RecommendationKindOffHoursStop,
			ConfirmationMode:         ConfirmationModeManual,
			RequiresSupervisorReview: true,
			RequiresTOTP:             true,
			SuggestedCapability:      suggestedCap,
			SuggestedCommandPointID:  suggestedPointID,
			TargetAssetID:            targetID,
			TargetAssetCode:          unit.AssetCode,
			TargetAssetName:          unit.AssetName,
			TargetAssetType:          AssetTypeChiller,
			PredictedPowerSavingKW:   savingKWRounded,
			EstimatedHourlySavingKWh: hourlyKWh,
			EstimatedShiftSavingKWh:  shiftKWh,
			ShiftHours:               policy.ShiftHours,
			PowerSavingBasis:         powerBasis,
			Rationale:                rationale,
			Preconditions:            preconditions,
		}
		drafts = append(drafts, draft)
		diagnostics = append(diagnostics, StagingDiagnostic{
			Scope: RecommendationKindOffHoursStop, AssetID: targetID, AssetCode: unit.AssetCode,
			Status: "RECOMMENDED", Reason: "OFF_HOURS_STOP_ELIGIBLE", Detail: rationale,
		})
	}

	return drafts, diagnostics
}

func resolveChillerOffHoursPowerSaving(unit ChillerUnitInput, at time.Time, policy StagingPolicy) (float64, string) {
	if unit.Power != nil {
		if ok, _ := isFreshGoodObservation(unit.Power.Quality, unit.Power.SampledAt, at, policy.MaxTelemetryAge); ok && finite(unit.Power.Value) && unit.Power.Value > 0 {
			return unit.Power.Value, "METER"
		}
	}
	if unit.RatedPowerKW > 0 && finite(unit.RatedPowerKW) {
		return unit.RatedPowerKW, "NAMEPLATE"
	}
	return policy.ChillerRunningPowerKW, "DEFAULT"
}

func cloneAssetControlLocks(src map[string]AssetControlLockInfo) map[string]AssetControlLockInfo {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]AssetControlLockInfo, len(src))
	for k, v := range src {
		normKey := strings.ToLower(strings.TrimSpace(k))
		if normKey == "" {
			continue
		}
		out[normKey] = v
	}
	return out
}

func siteControlExitDetail(snapshot StagingSnapshot) (string, bool) {
	mode := strings.ToUpper(strings.TrimSpace(snapshot.SiteControlMode))
	if mode == "" || mode == SiteControlModeRemoteAuto {
		return "", false
	}
	var detail string
	switch mode {
	case SiteControlModeManualOverride:
		detail = "站点处于手动接管（MANUAL_OVERRIDE）控制退出状态，暂停自动生成减台与节能联动建议"
	case SiteControlModeSafeShutdownExit:
		detail = "站点处于安全停机退出（SAFE_SHUTDOWN_EXIT）状态，暂停自动生成减台与节能联动建议"
	default:
		detail = fmt.Sprintf("站点当前处于 %s 非自动控制模式，暂停生成节能联动建议", mode)
	}
	if reason := strings.TrimSpace(snapshot.SiteControlReason); reason != "" {
		detail += fmt.Sprintf("（原因：%s）", reason)
	}
	return detail + "。", true
}

func lookupAssetControlLock(locks map[string]AssetControlLockInfo, assetID, assetCode string) (AssetControlLockInfo, bool) {
	if len(locks) == 0 {
		return AssetControlLockInfo{}, false
	}
	for _, ref := range []string{assetID, assetCode} {
		cleanRef := strings.TrimSpace(ref)
		if cleanRef == "" {
			continue
		}
		if info, ok := locks[strings.ToLower(cleanRef)]; ok {
			mode := strings.ToUpper(strings.TrimSpace(info.LockMode))
			if mode != "" && mode != AssetControlLockUnlocked {
				info.LockMode = mode
				return info, true
			}
		}
		for k, info := range locks {
			if strings.EqualFold(strings.TrimSpace(k), cleanRef) {
				mode := strings.ToUpper(strings.TrimSpace(info.LockMode))
				if mode != "" && mode != AssetControlLockUnlocked {
					info.LockMode = mode
					return info, true
				}
			}
		}
	}
	return AssetControlLockInfo{}, false
}

func lockReasonSuffix(reason string) string {
	clean := strings.TrimSpace(reason)
	if clean == "" {
		return ""
	}
	return "：" + clean
}

func canonicalCommandPointKey(assetRef, capability string) string {
	a := strings.ToLower(strings.TrimSpace(assetRef))
	c := strings.ToUpper(strings.TrimSpace(capability))
	if a == "" || c == "" {
		return ""
	}
	return a + "|" + c
}

func lookupCommandPointID(commandPointIDs map[string]string, assetID, assetCode, capability string) string {
	if len(commandPointIDs) == 0 {
		return ""
	}
	for _, ref := range []string{assetID, assetCode} {
		if key := canonicalCommandPointKey(ref, capability); key != "" {
			if pointID := strings.TrimSpace(commandPointIDs[key]); pointID != "" {
				return pointID
			}
		}
	}
	return ""
}
