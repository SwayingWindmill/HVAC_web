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
)

const (
	// ConfirmationModeManual marks Decision 3A staging recommendations as strictly
	// human-confirmed: supervisor review and TOTP second-factor confirmation are required
	// before any command intent is issued.
	ConfirmationModeManual = "MANUAL_CONFIRM"

	RecommendationKindChillerStageDown  = "CHILLER_STAGE_DOWN"
	RecommendationKindIdleAuxiliaryStop = "IDLE_AUXILIARY_STOP"

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

	DefaultHeadroomReserveFraction = 0.15
	DefaultMinimumPowerSavingKW    = 5.0
	DefaultIdleAuxiliaryDebounce   = 5 * time.Minute
	DefaultMaxTelemetryAge         = 10 * time.Minute
	DefaultStagingShiftHours       = 8.0
	DefaultChillerRunningPowerKW   = 5.0
	DefaultAuxiliaryRunningPowerKW = 0.5
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
	HeadroomReserveFraction float64       `json:"headroomReserveFraction"`
	MinimumPowerSavingKW    float64       `json:"minimumPowerSavingKW"`
	IdleAuxiliaryDebounce   time.Duration `json:"idleAuxiliaryDebounce"`
	MaxTelemetryAge         time.Duration `json:"maxTelemetryAge"`
	ShiftHours              float64       `json:"shiftHours"`
	ChillerRunningPowerKW   float64       `json:"chillerRunningPowerKW"`
	AuxiliaryRunningPowerKW float64       `json:"auxiliaryRunningPowerKW"`
}

// DefaultStagingPolicy returns safe defaults aligned with Decision 3A.
func DefaultStagingPolicy() StagingPolicy {
	return StagingPolicy{
		HeadroomReserveFraction: DefaultHeadroomReserveFraction,
		MinimumPowerSavingKW:    DefaultMinimumPowerSavingKW,
		IdleAuxiliaryDebounce:   DefaultIdleAuxiliaryDebounce,
		MaxTelemetryAge:         DefaultMaxTelemetryAge,
		ShiftHours:              DefaultStagingShiftHours,
		ChillerRunningPowerKW:   DefaultChillerRunningPowerKW,
		AuxiliaryRunningPowerKW: DefaultAuxiliaryRunningPowerKW,
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
	if policy.MinimumPowerSavingKW < 0 || !finite(policy.MinimumPowerSavingKW) {
		return StagingPolicy{}, errors.New("minimumPowerSavingKW must be non-negative")
	}
	if policy.MinimumPowerSavingKW == 0 {
		policy.MinimumPowerSavingKW = DefaultMinimumPowerSavingKW
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
	AssetID         string   `json:"assetId"`
	AssetCode       string   `json:"assetCode"`
	AssetName       string   `json:"assetName"`
	MeterDeviceID   string   `json:"meterDeviceId"`
	MeterSourceKey  string   `json:"meterSourceKey"`
	BoundDeviceIDs  []string `json:"boundDeviceIds,omitempty"`
	RatedCapacityKW float64  `json:"ratedCapacityKW,omitempty"`
	RatedPowerKW    float64  `json:"ratedPowerKW,omitempty"`
}

// AssetControlLockInfo mirrors the per-asset lock from core_registry.asset_control_locks.
type AssetControlLockInfo struct {
	LockMode string `json:"lockMode"`
	Reason   string `json:"reason,omitempty"`
}

// SongyaxuanChillerMeterBindings returns the verified Songyaxuan plant chiller-to-meter mapping:
// chiller-c -> ammeter-unit1, chiller-d -> ammeter-unit2, chiller-a -> ammeter-unit3, chiller-b -> ammeter-unit4.
func SongyaxuanChillerMeterBindings() []ChillerMeterBinding {
	return []ChillerMeterBinding{
		{
			AssetID:         "01a14000-3000-7000-8000-000000000001",
			AssetCode:       "chiller-a",
			AssetName:       "A 机组",
			MeterDeviceID:   "01a14000-4000-7000-8000-000000000005",
			MeterSourceKey:  "ammeter-unit3",
			RatedCapacityKW: 572,
			RatedPowerKW:    127.5,
		},
		{
			AssetID:         "01a14000-3000-7000-8000-000000000002",
			AssetCode:       "chiller-b",
			AssetName:       "B 机组",
			MeterDeviceID:   "01a14000-4000-7000-8000-000000000006",
			MeterSourceKey:  "ammeter-unit4",
			RatedCapacityKW: 572,
			RatedPowerKW:    127.5,
		},
		{
			AssetID:         "01a14000-3000-7000-8000-000000000003",
			AssetCode:       "chiller-c",
			AssetName:       "C 机组",
			MeterDeviceID:   "01a14000-4000-7000-8000-000000000003",
			MeterSourceKey:  "ammeter-unit1",
			RatedCapacityKW: 572,
			RatedPowerKW:    127.5,
		},
		{
			AssetID:         "01a14000-3000-7000-8000-000000000004",
			AssetCode:       "chiller-d",
			AssetName:       "D 机组",
			MeterDeviceID:   "01a14000-4000-7000-8000-000000000004",
			MeterSourceKey:  "ammeter-unit2",
			RatedCapacityKW: 572,
			RatedPowerKW:    127.5,
		},
	}
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
	AssetID         string           `json:"assetId"`
	AssetCode       string           `json:"assetCode,omitempty"`
	AssetName       string           `json:"assetName,omitempty"`
	MeterDeviceID   string           `json:"meterDeviceId,omitempty"`
	MeterSourceKey  string           `json:"meterSourceKey,omitempty"`
	BoundDeviceIDs  []string         `json:"boundDeviceIds,omitempty"`
	RatedCapacityKW float64          `json:"ratedCapacityKW,omitempty"`
	RatedPowerKW    float64          `json:"ratedPowerKW,omitempty"`
	RunState        string           `json:"runState,omitempty"`
	RunStateQuality string           `json:"runStateQuality,omitempty"`
	RunStateAt      time.Time        `json:"runStateAt,omitempty"`
	StoppedSince    *time.Time       `json:"stoppedSince,omitempty"`
	RunStateHistory []RunStateSample `json:"runStateHistory,omitempty"`
	Power           *TelemetryValue  `json:"power,omitempty"`
	ChwLeavingC     *TelemetryValue  `json:"chwLeavingC,omitempty"`
	CwLeavingC      *TelemetryValue  `json:"cwLeavingC,omitempty"`
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
	Chillers            []ChillerMeterBinding           `json:"chillers"`
	Auxiliaries         []AuxiliaryUnitBinding          `json:"auxiliaries,omitempty"`
	Edges               []HydraulicEdge                 `json:"edges,omitempty"`
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
	PlantCoolingLoad  *TelemetryValue                 `json:"plantCoolingLoad,omitempty"`
	HeaderSupplyC     *TelemetryValue                 `json:"headerSupplyC,omitempty"`
	Chillers          []ChillerUnitInput              `json:"chillers"`
	Auxiliaries       []AuxiliaryUnitInput            `json:"auxiliaries,omitempty"`
	Edges             []HydraulicEdge                 `json:"edges,omitempty"`
	ChillerFits       []ChillerFit                    `json:"chillerFits,omitempty"`
}

// StagingSnapshotQuery scopes a live staging snapshot read in ClickHouse.
type StagingSnapshotQuery struct {
	TenantID string
	SiteID   string
	At       time.Time
	Policy   StagingPolicy
	Topology *StagingTopology
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
	History  PlantHistory
	Snapshot StagingSnapshotReader
	Catalog  StagingSiteCatalog
	Policy   StagingPolicy
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
	var topology *StagingTopology
	if evaluator.Catalog != nil {
		loaded, catErr := evaluator.Catalog.StagingTopology(ctx, tenantID, siteID, evaluatedAt)
		if catErr != nil {
			return StagingEvaluation{}, fmt.Errorf("%w: %v", ErrStagingTopologyUnavailable, catErr)
		}
		topology = &loaded
	}
	snapshot, err := evaluator.Snapshot.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID,
		SiteID:   siteID,
		At:       evaluatedAt,
		Policy:   policy,
		Topology: topology,
	})
	if err != nil {
		return StagingEvaluation{}, err
	}
	if len(snapshot.ChillerFits) == 0 && evaluator.History != nil {
		to := evaluatedAt.Truncate(copBucketMinutes * time.Minute)
		from := to.AddDate(0, 0, -copHistoryDays)
		if buckets, histErr := evaluator.History.PlantBuckets(ctx, PlantHistoryQuery{TenantID: tenantID, SiteID: siteID, From: from, To: to}); histErr == nil {
			fit := FitPlant(buckets, from, to)
			snapshot.ChillerFits = fit.Chillers
		}
	}
	applyStagingTopology(&snapshot, topology)
	return EvaluateStaging(snapshot, policy)
}

func evaluateStagingWithFit(ctx context.Context, tenantID, siteID string, at time.Time, fit PlantFit, reader StagingSnapshotReader, catalog StagingSiteCatalog, policy *StagingPolicy) *StagingEvaluation {
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
	var topology *StagingTopology
	if catalog != nil {
		if loaded, catErr := catalog.StagingTopology(ctx, tenantID, siteID, at.UTC()); catErr == nil {
			topology = &loaded
		}
	}
	snapshot, err := reader.ReadStagingSnapshot(ctx, StagingSnapshotQuery{
		TenantID: tenantID, SiteID: siteID, At: at.UTC(), Policy: normPolicy, Topology: topology,
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

// EvaluateStaging runs both CHILLER_STAGE_DOWN and IDLE_AUXILIARY_STOP checks on a sensor snapshot.
func EvaluateStaging(snapshot StagingSnapshot, policy StagingPolicy) (StagingEvaluation, error) {
	normPolicy, err := policy.normalized()
	if err != nil {
		return StagingEvaluation{}, err
	}
	if snapshot.EvaluatedAt.IsZero() {
		return StagingEvaluation{}, errors.New("evaluatedAt is required")
	}
	snapshot = resolveSnapshotBindings(snapshot)
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

	if draft, diag := evaluateChillerStageDown(snapshot, normPolicy); draft != nil {
		result.Recommendations = append(result.Recommendations, *draft)
		result.Diagnostics = append(result.Diagnostics, diag)
	} else {
		result.Diagnostics = append(result.Diagnostics, diag)
	}

	auxDrafts, auxDiags := evaluateIdleAuxiliaries(snapshot, normPolicy)
	result.Recommendations = append(result.Recommendations, auxDrafts...)
	result.Diagnostics = append(result.Diagnostics, auxDiags...)

	return result, nil
}

func resolveSnapshotBindings(snapshot StagingSnapshot) StagingSnapshot {
	songyaxuan := SongyaxuanChillerMeterBindings()
	byAssetID := map[string]ChillerMeterBinding{}
	byAssetCode := map[string]ChillerMeterBinding{}
	byMeterID := map[string]ChillerMeterBinding{}
	byMeterSource := map[string]ChillerMeterBinding{}
	for _, b := range songyaxuan {
		byAssetID[strings.ToLower(b.AssetID)] = b
		byAssetCode[strings.ToLower(b.AssetCode)] = b
		byMeterID[strings.ToLower(b.MeterDeviceID)] = b
		byMeterSource[strings.ToLower(b.MeterSourceKey)] = b
	}

	resolved := make([]ChillerUnitInput, len(snapshot.Chillers))
	for index, unit := range snapshot.Chillers {
		if match, ok := lookupSongyaxuanBinding(unit, byAssetID, byAssetCode, byMeterID, byMeterSource); ok {
			if unit.AssetID == "" {
				unit.AssetID = match.AssetID
			}
			if unit.AssetCode == "" {
				unit.AssetCode = match.AssetCode
			}
			if unit.AssetName == "" {
				unit.AssetName = match.AssetName
			}
			if unit.MeterDeviceID == "" {
				unit.MeterDeviceID = match.MeterDeviceID
			}
			if unit.MeterSourceKey == "" {
				unit.MeterSourceKey = match.MeterSourceKey
			}
			if unit.RatedCapacityKW <= 0 {
				unit.RatedCapacityKW = match.RatedCapacityKW
			}
			if unit.RatedPowerKW <= 0 {
				unit.RatedPowerKW = match.RatedPowerKW
			}
		}
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

func lookupSongyaxuanBinding(
	unit ChillerUnitInput,
	byAssetID, byAssetCode, byMeterID, byMeterSource map[string]ChillerMeterBinding,
) (ChillerMeterBinding, bool) {
	if b, ok := byMeterID[strings.ToLower(strings.TrimSpace(unit.MeterDeviceID))]; ok {
		return b, true
	}
	if b, ok := byMeterSource[strings.ToLower(strings.TrimSpace(unit.MeterSourceKey))]; ok {
		return b, true
	}
	if b, ok := byAssetID[strings.ToLower(strings.TrimSpace(unit.AssetID))]; ok {
		return b, true
	}
	if unit.MeterDeviceID == "" && unit.MeterSourceKey == "" {
		if b, ok := byAssetCode[strings.ToLower(strings.TrimSpace(unit.AssetCode))]; ok {
			return b, true
		}
	}
	return ChillerMeterBinding{}, false
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

	copBefore := coolingLoadKW / totalCurrentPowerKW
	requiredCapacityKW := coolingLoadKW * (1 + policy.HeadroomReserveFraction)

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
				fmt.Sprintf("停运 %s 后剩余总冷量 %.1f kW，相对当前冷负荷 %.1f kW 裕度仅 %.1f%%，低于 %.1f%% 安全储备要求。",
					unitDisplayName(candidate.unit), remainingCapKW, coolingLoadKW, margin*100, policy.HeadroomReserveFraction*100),
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
		len(running), curPower, loadKW, copBeforeRounded, unitDisplayName(target), remCap, headroom*100, policy.HeadroomReserveFraction*100, copAfterRounded, savingKW, hourlyKWh, shiftKWh, policy.ShiftHours,
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
		interlockStatement := fmt.Sprintf("所服务冷机均已停机超过 5 分钟（实测最短连续停机 %.1f 分钟），无 RUNNING/STARTING 状态机组。", idleMin)
		if policy.IdleAuxiliaryDebounce > 5*time.Minute {
			interlockStatement = fmt.Sprintf("所服务冷机均已停机超过 %.0f 分钟（实测最短连续停机 %.1f 分钟），无 RUNNING/STARTING 状态机组。", policy.IdleAuxiliaryDebounce.Minutes(), idleMin)
		}

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
			if powerValid && powerKW > policy.ChillerRunningPowerKW {
				return RunStateUnknown, "PAIRED_CHILLER_DRAWING_POWER"
			}
			return RunStateStopped, ""
		default:
			return RunStateUnknown, "CHILLER_STATE_UNKNOWN"
		}
	}

	if powerValid {
		if powerKW > policy.ChillerRunningPowerKW {
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

func confirmChillerStoppedDuration(unit ChillerUnitInput, at time.Time, policy StagingPolicy) (time.Duration, string) {
	if len(unit.RunStateHistory) > 0 {
		cutoff := at.Add(-policy.IdleAuxiliaryDebounce)
		samples := append([]RunStateSample(nil), unit.RunStateHistory...)
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].SampledAt.Before(samples[j].SampledAt)
		})

		latestGoodAt := time.Time{}
		earliestContinuousStopped := time.Time{}
		hasBeforeCutoff := false
		prevStoppedAt := time.Time{}

		for _, s := range samples {
			sampled := s.SampledAt.UTC()
			if sampled.IsZero() || sampled.After(at) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(s.Quality), "GOOD") {
				if !sampled.Before(cutoff) {
					return 0, "IDLE_HISTORY_NON_GOOD"
				}
				earliestContinuousStopped = time.Time{}
				hasBeforeCutoff = false
				prevStoppedAt = time.Time{}
				continue
			}
			state := normalizeRunState(s.State)
			if state != RunStateStopped {
				if !sampled.Before(cutoff) {
					return 0, "IDLE_DEBOUNCE_NOT_MET"
				}
				earliestContinuousStopped = time.Time{}
				hasBeforeCutoff = false
				prevStoppedAt = time.Time{}
				continue
			}
			if !prevStoppedAt.IsZero() && sampled.Sub(prevStoppedAt) > policy.MaxTelemetryAge {
				if !sampled.Before(cutoff) {
					return 0, "IDLE_HISTORY_TELEMETRY_GAP"
				}
				earliestContinuousStopped = sampled
				hasBeforeCutoff = !sampled.After(cutoff)
			} else {
				if earliestContinuousStopped.IsZero() {
					earliestContinuousStopped = sampled
				}
				if !sampled.After(cutoff) {
					hasBeforeCutoff = true
				}
			}
			prevStoppedAt = sampled
			latestGoodAt = sampled
		}

		if latestGoodAt.IsZero() || at.Sub(latestGoodAt) > policy.MaxTelemetryAge {
			return 0, "IDLE_HISTORY_STALE"
		}
		if !hasBeforeCutoff || earliestContinuousStopped.IsZero() {
			return at.Sub(earliestContinuousStopped), "IDLE_DEBOUNCE_NOT_MET"
		}
		return at.Sub(earliestContinuousStopped), ""
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
	candidates := []string{unit.MeterDeviceID, unit.MeterSourceKey, unit.AssetID, unit.AssetCode}
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
// chiller, btu-calc and auxiliary observations (never falling back past newer non-GOOD readings)
// and recent chiller run-state history for continuous idle verification.
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
	latestFrom := at.Add(-2 * maxDuration(policy.MaxTelemetryAge, time.Hour))
	historyFrom := at.Add(-policy.IdleAuxiliaryDebounce - policy.MaxTelemetryAge)

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
   AND telemetry_key IN (
    'power_total','instant_cooling_capacity','btu_meter.instant_cooling_capacity',
    'chw_leaving_temperature','cw_leaving_temperature',
    'supply_water_temperature','btu_meter.supply_water_temperature',
    'run_state','chiller.run_state','chwp.run_state','cwp.run_state','cooling_tower.run_state'
   )
   AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
   AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
   AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
   AND device_id IS NOT NULL
)
WHERE row_num = 1
FORMAT JSONEachRow`,
		clickHouseString(query.TenantID), clickHouseString(query.SiteID),
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

	historySQL := fmt.Sprintf(`SELECT
 toString(device_id) AS deviceId,
 source_id AS sourceId,
 telemetry_key AS telemetryKey,
 value_string AS valueString,
 quality,
 formatDateTime(sampled_at,'%%Y-%%m-%%dT%%H:%%i:%%S.%%fZ','UTC') AS sampledAt
FROM telemetry_history.observations
WHERE tenant_id = toUUID('%s')
  AND site_id = toUUID('%s')
  AND telemetry_key IN ('run_state','chiller.run_state')
  AND sampled_at >= parseDateTime64BestEffort('%s',3,'UTC')
  AND sampled_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND received_at <= parseDateTime64BestEffort('%s',3,'UTC')
  AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER')
  AND device_id IS NOT NULL
ORDER BY sampled_at ASC, received_at ASC, history_sequence ASC
FORMAT JSONEachRow`,
		clickHouseString(query.TenantID), clickHouseString(query.SiteID),
		formatOptimizationTime(historyFrom), formatOptimizationTime(at), formatOptimizationTime(at),
	)

	historyPayload, err := reader.queryLimited(ctx, historySQL, maximumStagingSnapshotResponseBytes)
	if err != nil {
		return StagingSnapshot{}, fmt.Errorf("query staging run state history: %w", err)
	}
	historyRows, err := decodeStagingRunStateRows(bytes.NewReader(historyPayload))
	if err != nil {
		return StagingSnapshot{}, fmt.Errorf("decode staging run state history: %w", err)
	}

	return buildStagingSnapshotFromObservations(query.TenantID, query.SiteID, at, query.Topology, latestRows, historyRows), nil
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

func decodeStagingRunStateRows(source io.Reader) ([]stagingTelemetryRow, error) {
	type rawRow struct {
		DeviceID     string  `json:"deviceId"`
		SourceID     string  `json:"sourceId"`
		TelemetryKey string  `json:"telemetryKey"`
		ValueString  *string `json:"valueString"`
		Quality      string  `json:"quality"`
		SampledAt    string  `json:"sampledAt"`
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
			ValueString:  r.ValueString,
			Quality:      r.Quality,
			SampledAt:    sampledAt.UTC(),
		})
	}
}

func buildStagingSnapshotFromObservations(
	tenantID, siteID string,
	at time.Time,
	topology *StagingTopology,
	latest []stagingTelemetryRow,
	history []stagingTelemetryRow,
) StagingSnapshot {
	snapshot := StagingSnapshot{
		TenantID:    tenantID,
		SiteID:      siteID,
		EvaluatedAt: at.UTC(),
	}
	applyStagingTopology(&snapshot, topology)

	byDeviceKey := map[string]map[string]stagingTelemetryRow{}
	for _, row := range latest {
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

	historyByDevice := map[string][]RunStateSample{}
	for _, row := range history {
		if row.ValueString == nil {
			continue
		}
		sample := RunStateSample{State: *row.ValueString, Quality: row.Quality, SampledAt: row.SampledAt}
		for _, id := range []string{row.DeviceID, row.SourceID} {
			k := strings.ToLower(strings.TrimSpace(id))
			if k != "" {
				historyByDevice[k] = append(historyByDevice[k], sample)
			}
		}
	}

	var coolingMatches []TelemetryValue
	var supplyMatches []TelemetryValue
	for _, row := range latest {
		if row.ValueNumber == nil {
			continue
		}
		switch row.TelemetryKey {
		case plantCoolingLoadKey, "btu_meter.instant_cooling_capacity":
			if topology == nil || topology.CoolingLoadDeviceID == "" ||
				strings.EqualFold(row.DeviceID, topology.CoolingLoadDeviceID) ||
				strings.EqualFold(row.SourceID, topology.CoolingLoadDeviceID) {
				coolingMatches = append(coolingMatches, TelemetryValue{
					DeviceID: row.DeviceID, SourceKey: row.SourceID, PointKey: row.TelemetryKey,
					Value: *row.ValueNumber, Unit: row.Unit, Quality: row.Quality, SampledAt: row.SampledAt,
				})
			}
		case "supply_water_temperature", "btu_meter.supply_water_temperature":
			supplyMatches = append(supplyMatches, TelemetryValue{
				DeviceID: row.DeviceID, SourceKey: row.SourceID, PointKey: row.TelemetryKey,
				Value: *row.ValueNumber, Unit: row.Unit, Quality: row.Quality, SampledAt: row.SampledAt,
			})
		}
	}
	if len(coolingMatches) == 1 {
		v := coolingMatches[0]
		snapshot.PlantCoolingLoad = &v
	} else if len(coolingMatches) > 1 {
		for _, m := range coolingMatches {
			if strings.EqualFold(m.SourceKey, "btu-calc") || m.DeviceID == "01a14000-4000-7000-8000-000000000009" {
				v := m
				snapshot.PlantCoolingLoad = &v
				break
			}
		}
	}
	if len(supplyMatches) >= 1 {
		v := supplyMatches[0]
		snapshot.HeaderSupplyC = &v
	}

	chillerBindings := []ChillerMeterBinding(nil)
	if topology != nil {
		chillerBindings = topology.Chillers
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
			populateAuxTelemetry(&aux, byDeviceKey)
			snapshot.Auxiliaries = append(snapshot.Auxiliaries, aux)
		}
	} else {
		chillerBindings = discoverChillerBindingsFromRows(latest)
	}

	for _, binding := range chillerBindings {
		unit := ChillerUnitInput{
			AssetID:         binding.AssetID,
			AssetCode:       binding.AssetCode,
			AssetName:       binding.AssetName,
			MeterDeviceID:   binding.MeterDeviceID,
			MeterSourceKey:  binding.MeterSourceKey,
			BoundDeviceIDs:  append([]string(nil), binding.BoundDeviceIDs...),
			RatedCapacityKW: binding.RatedCapacityKW,
			RatedPowerKW:    binding.RatedPowerKW,
		}
		populateChillerTelemetry(&unit, byDeviceKey, historyByDevice)
		snapshot.Chillers = append(snapshot.Chillers, unit)
	}

	return snapshot
}

func discoverChillerBindingsFromRows(rows []stagingTelemetryRow) []ChillerMeterBinding {
	songyaxuan := SongyaxuanChillerMeterBindings()
	byMeter := map[string]ChillerMeterBinding{}
	for _, b := range songyaxuan {
		byMeter[strings.ToLower(b.MeterDeviceID)] = b
		byMeter[strings.ToLower(b.MeterSourceKey)] = b
	}
	hasSongyaxuan := false
	seenDevice := map[string]bool{}
	hasPower := map[string]bool{}
	hasChwLeaving := map[string]bool{}
	for _, r := range rows {
		if _, ok := byMeter[strings.ToLower(r.DeviceID)]; ok {
			hasSongyaxuan = true
		}
		if _, ok := byMeter[strings.ToLower(r.SourceID)]; ok {
			hasSongyaxuan = true
		}
		seenDevice[r.DeviceID] = true
		if r.TelemetryKey == chillerPowerKey {
			hasPower[r.DeviceID] = true
		}
		if r.TelemetryKey == chilledWaterLeavingKey {
			hasChwLeaving[r.DeviceID] = true
		}
	}
	if hasSongyaxuan {
		return songyaxuan
	}
	discovered := make([]ChillerMeterBinding, 0)
	for devID := range seenDevice {
		if hasPower[devID] && hasChwLeaving[devID] {
			discovered = append(discovered, ChillerMeterBinding{
				AssetID:       devID,
				AssetCode:     devID,
				MeterDeviceID: devID,
			})
		}
	}
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].AssetID < discovered[j].AssetID })
	return discovered
}

func populateChillerTelemetry(
	unit *ChillerUnitInput,
	byDeviceKey map[string]map[string]stagingTelemetryRow,
	historyByDevice map[string][]RunStateSample,
) {
	keys := append([]string{unit.MeterDeviceID, unit.MeterSourceKey}, unit.BoundDeviceIDs...)
	keys = append(keys, unit.AssetID, unit.AssetCode)
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, "chiller.run_state", "run_state"); ok && r.ValueString != nil {
		unit.RunState = *r.ValueString
		unit.RunStateQuality = r.Quality
		unit.RunStateAt = r.SampledAt
	}
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, chillerPowerKey); ok && r.ValueNumber != nil {
		unit.Power = &TelemetryValue{DeviceID: r.DeviceID, SourceKey: r.SourceID, PointKey: r.TelemetryKey, Value: *r.ValueNumber, Unit: r.Unit, Quality: r.Quality, SampledAt: r.SampledAt}
	}
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, chilledWaterLeavingKey); ok && r.ValueNumber != nil {
		unit.ChwLeavingC = &TelemetryValue{DeviceID: r.DeviceID, SourceKey: r.SourceID, PointKey: r.TelemetryKey, Value: *r.ValueNumber, Unit: r.Unit, Quality: r.Quality, SampledAt: r.SampledAt}
	}
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, condenserWaterLeavingKey); ok && r.ValueNumber != nil {
		unit.CwLeavingC = &TelemetryValue{DeviceID: r.DeviceID, SourceKey: r.SourceID, PointKey: r.TelemetryKey, Value: *r.ValueNumber, Unit: r.Unit, Quality: r.Quality, SampledAt: r.SampledAt}
	}
	for _, k := range keys {
		if hist := historyByDevice[strings.ToLower(strings.TrimSpace(k))]; len(hist) > 0 {
			unit.RunStateHistory = append([]RunStateSample(nil), hist...)
			break
		}
	}
}

func populateAuxTelemetry(aux *AuxiliaryUnitInput, byDeviceKey map[string]map[string]stagingTelemetryRow) {
	keys := append([]string{aux.DeviceID, aux.DeviceSourceKey}, aux.BoundDeviceIDs...)
	keys = append(keys, aux.AssetID, aux.AssetCode)
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, "chwp.run_state", "cwp.run_state", "cooling_tower.run_state", "run_state"); ok && r.ValueString != nil {
		aux.RunState = *r.ValueString
		aux.RunStateQuality = r.Quality
		aux.RunStateAt = r.SampledAt
	}
	if r, ok := findPointAcrossKeys(byDeviceKey, keys, chillerPowerKey); ok && r.ValueNumber != nil {
		aux.Power = &TelemetryValue{DeviceID: r.DeviceID, SourceKey: r.SourceID, PointKey: r.TelemetryKey, Value: *r.ValueNumber, Unit: r.Unit, Quality: r.Quality, SampledAt: r.SampledAt}
	}
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
