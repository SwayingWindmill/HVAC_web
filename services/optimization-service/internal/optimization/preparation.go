package optimization

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var ErrPreparationUnavailable = errors.New("optimization authoritative input is unavailable")

const DefaultPreparationMaxTelemetryAge = 15 * time.Minute

// Why a run is prepared: an operator asked (MANUAL) or the Site's schedule fired (SCHEDULE).
const (
	TriggerManual   = "MANUAL"
	TriggerSchedule = "SCHEDULE"
)

type PreparationRequest struct {
	TenantID    string `json:"-"`
	SiteID      string `json:"siteId"`
	SubjectType string `json:"-"`
	SubjectID   string `json:"-"`
	Trigger     string `json:"-"`
}

func (request PreparationRequest) Validate() error {
	for field, value := range map[string]string{"tenantId": request.TenantID, "siteId": request.SiteID, "subjectId": request.SubjectID} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", field)
		}
	}
	if request.SubjectType != "SITE" || request.SubjectID != request.SiteID {
		return errors.New("optimization subject must be the exact SITE")
	}
	if request.Trigger != TriggerManual && request.Trigger != TriggerSchedule {
		return errors.New("optimization trigger must be MANUAL or SCHEDULE")
	}
	return nil
}

type PreparationDefinition struct {
	PolicyVersionID        string
	TopologyVersionID      string
	LoadForecastSnapshotID string
	PVForecastSnapshotID   *string
	TariffVersionID        string
	DeploymentRevisionID   string
	Objective              string
	Horizon                string
	HorizonMinutes         int
	Granularity            string
	PolicyConstraints      json.RawMessage
	SiteTimezone           string
	DeviceIDs              []string
	ResolvedInputMapping   *InputMapping
}

type InputMapping struct {
	SupplyTemperatureKey string   `json:"supplyTemperatureKey"`
	ZoneTemperatureKey   string   `json:"zoneTemperatureKey"`
	ZoneTemperatureKeys  []string `json:"zoneTemperatureKeys,omitempty"`
}

func (mapping InputMapping) telemetryKeys() []string {
	zones := mapping.effectiveZoneKeys()
	keys := make([]string, 0, 1+len(zones))
	if supply := strings.TrimSpace(mapping.SupplyTemperatureKey); supply != "" {
		keys = append(keys, supply)
	}
	keys = append(keys, zones...)
	return keys
}

func resolveZoneTemperatureKeys(single string, many []string) []string {
	seen := map[string]struct{}{}
	var out []string
	appendKey := func(raw string) {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			return
		}
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(many) > 0 {
		for _, k := range many {
			appendKey(k)
		}
		return out
	}
	appendKey(single)
	return out
}

func (mapping InputMapping) effectiveZoneKeys() []string {
	return resolveZoneTemperatureKeys(mapping.ZoneTemperatureKey, mapping.ZoneTemperatureKeys)
}

type ComfortConstraints struct {
	ZoneTempMinC float64 `json:"zoneTempMinC"`
	ZoneTempMaxC float64 `json:"zoneTempMaxC"`
}

type SafetyConstraints struct {
	SupplyTempMinC    float64 `json:"supplyTempMinC"`
	SupplyTempMaxC    float64 `json:"supplyTempMaxC"`
	MaxSupplyTempStep float64 `json:"maxSupplyTempStepC"`
}

type MaintenanceConstraints struct {
	OutOfService []string `json:"outOfService"`
}

type ManualLocks struct {
	Resources []string `json:"resources"`
}

type FrozenSafetyConstraints struct {
	Comfort                   ComfortConstraints `json:"comfort"`
	Safety                    SafetyConstraints  `json:"safety"`
	InputMapping              InputMapping       `json:"inputMapping"`
	ResponseModel             HVACResponseModel  `json:"responseModel"`
	ModelDeploymentRevisionID string             `json:"modelDeploymentRevisionId"`
}

type SiteResponseCalibration struct {
	Status       string `json:"status,omitempty"`
	Source       string `json:"source,omitempty"`
	CalibratedAt string `json:"calibratedAt,omitempty"`
}

type policyContract struct {
	Comfort                ComfortConstraints                 `json:"comfort"`
	Safety                 SafetyConstraints                  `json:"safety"`
	InputMapping           InputMapping                       `json:"inputMapping"`
	MaintenanceConstraints MaintenanceConstraints             `json:"maintenanceConstraints"`
	ManualLocks            ManualLocks                        `json:"manualLocks"`
	ResponseModel          HVACResponseModel                  `json:"responseModel"`
	SiteCalibrations       map[string]SiteResponseCalibration `json:"siteCalibrations,omitempty"`
}

// EnergyDayEvidence is the Site-local day the baseline is priced at: its electricity and the cost
// the query service priced it at (#575), with the data revision and how far the data reached.
type EnergyDayEvidence struct {
	PeriodStart     time.Time `json:"periodStart"`
	PeriodEnd       time.Time `json:"periodEnd"`
	EnergyKWh       float64   `json:"energyKWh"`
	CostCNY         float64   `json:"costCNY"`
	DatasetRevision string    `json:"datasetRevision"`
	DataWatermark   time.Time `json:"dataWatermark"`
}

type TelemetryEvidence struct {
	ObservationID   string    `json:"observationId"`
	DeviceID        string    `json:"deviceId"`
	PointID         string    `json:"pointId"`
	TelemetryKey    string    `json:"telemetryKey"`
	PointRevision   uint64    `json:"pointRevision"`
	SampledAt       time.Time `json:"sampledAt"`
	ReceivedAt      time.Time `json:"receivedAt"`
	Value           float64   `json:"value"`
	Unit            string    `json:"unit"`
	Quality         string    `json:"quality"`
	QualityReasons  []string  `json:"qualityReasons,omitempty"`
	HealthStatus    string    `json:"healthStatus,omitempty"`
	SourceEventID   string    `json:"sourceEventId"`
	SourcePartition string    `json:"sourcePartition"`
	SourceOffset    uint64    `json:"sourceOffset"`
}

type AuthoritativeState struct {
	PricedDay         EnergyDayEvidence
	SupplyTemperature TelemetryEvidence
	ZoneTemperature   TelemetryEvidence
	ZoneTemperatures  []TelemetryEvidence
}

type OptimizationStateQuery struct {
	TenantID             string
	SiteID               string
	SubjectType          string
	SubjectID            string
	SupplyTemperatureKey string
	ZoneTemperatureKey   string
	ZoneTemperatureKeys  []string
	// Timezone is the Site's clock, which days are priced on.
	Timezone string
	At       time.Time
}

func (query OptimizationStateQuery) effectiveZoneKeys() []string {
	return resolveZoneTemperatureKeys(query.ZoneTemperatureKey, query.ZoneTemperatureKeys)
}

type FrozenCurrentState struct {
	SchemaVersion     int                 `json:"schemaVersion"`
	Baseline          HVACBaseline        `json:"baseline"`
	EnergyEvidence    EnergyDayEvidence   `json:"energyEvidence"`
	TelemetryEvidence []TelemetryEvidence `json:"telemetryEvidence"`
}

type PreparedInput struct {
	// Trigger is how the run's job is queued; it is not part of the frozen input.
	Trigger                string
	TenantID               string
	SiteID                 string
	SubjectType            string
	SubjectID              string
	PolicyVersionID        string
	TopologyVersionID      string
	LoadForecastSnapshotID string
	PVForecastSnapshotID   *string
	TariffVersionID        string
	DeploymentRevisionID   string
	Objective              string
	Horizon                string
	HorizonMinutes         int
	Granularity            string
	CapturedAt             time.Time
	CurrentState           FrozenCurrentState
	SafetyConstraints      FrozenSafetyConstraints
	MaintenanceConstraints MaintenanceConstraints
	ManualLocks            ManualLocks
	InputChecksum          string
}

type PreparedOptimization struct {
	OptimizationRunID string `json:"optimizationRunId"`
	InputSnapshotID   string `json:"inputSnapshotId"`
	Status            string `json:"status"`
}

type PreparationStore interface {
	ResolveOptimizationPreparation(context.Context, PreparationRequest, time.Time) (PreparationDefinition, error)
	CreatePreparedOptimization(context.Context, PreparedInput, time.Time) (PreparedOptimization, error)
}

type AuthoritativeInputReader interface {
	ReadOptimizationState(context.Context, OptimizationStateQuery) (AuthoritativeState, error)
}

type Preparer struct {
	store       PreparationStore
	inputs      AuthoritativeInputReader
	pointHealth TelemetryPointHealthChecker
	clock       Clock
}

func NewPreparer(store PreparationStore, inputs AuthoritativeInputReader, clock Clock) (*Preparer, error) {
	if store == nil || inputs == nil {
		return nil, errors.New("optimization preparation store and authoritative input reader are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Preparer{store: store, inputs: inputs, clock: clock}, nil
}

// WithPointHealth configures the remote telemetry point-health checker called before reading inputs.
func (preparer *Preparer) WithPointHealth(checker TelemetryPointHealthChecker) *Preparer {
	preparer.pointHealth = checker
	return preparer
}

func (preparer *Preparer) Prepare(ctx context.Context, request PreparationRequest) (PreparedOptimization, error) {
	if err := request.Validate(); err != nil {
		return PreparedOptimization{}, err
	}
	// Frozen to the microsecond Postgres keeps, so the checksum holds when the worker reloads it.
	capturedAt := preparer.clock().UTC().Truncate(time.Microsecond)
	definition, err := preparer.store.ResolveOptimizationPreparation(ctx, request, capturedAt)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("resolve Optimization preparation lineage: %w", err)
	}
	if definition.Objective != "COST" || definition.Horizon != "DAY_AHEAD" || definition.HorizonMinutes != 1440 || definition.Granularity != "15MIN" {
		return PreparedOptimization{}, unavailable(ReasonPolicyUnsupported, "current HVAC solver requires COST DAY_AHEAD 1440-minute optimization at 15MIN granularity")
	}
	policy, err := decodePolicyContract(definition.PolicyConstraints)
	if err != nil {
		return PreparedOptimization{}, unavailable(ReasonPolicyUnsupported, "Optimization policy mapping is unsupported: %v", err)
	}
	if len(policy.MaintenanceConstraints.OutOfService) != 0 || len(policy.ManualLocks.Resources) != 0 {
		return PreparedOptimization{}, unavailable(ReasonMaintenanceActive, "current HVAC solver cannot safely optimize while maintenance constraints or manual locks are active")
	}
	effectiveMapping := policy.InputMapping
	if definition.ResolvedInputMapping != nil {
		effectiveMapping = *definition.ResolvedInputMapping
	}
	if preparer.pointHealth != nil {
		if err = preparer.pointHealth.CheckTelemetryPointHealth(ctx, request.TenantID, request.SiteID, definition.DeviceIDs, effectiveMapping.telemetryKeys()); err != nil {
			if _, named := PreparationReasonOf(err); named {
				return PreparedOptimization{}, err
			}
			return PreparedOptimization{}, unavailable(ReasonInputsUnreadable, "check telemetry point health: %v", err)
		}
	}
	state, err := preparer.inputs.ReadOptimizationState(ctx, OptimizationStateQuery{
		TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		SupplyTemperatureKey: effectiveMapping.SupplyTemperatureKey,
		ZoneTemperatureKey:   effectiveMapping.ZoneTemperatureKey,
		ZoneTemperatureKeys:  effectiveMapping.ZoneTemperatureKeys,
		Timezone:             definition.SiteTimezone, At: capturedAt,
	})
	if err != nil {
		if _, named := PreparationReasonOf(err); named {
			return PreparedOptimization{}, err
		}
		return PreparedOptimization{}, unavailable(ReasonInputsUnreadable, "read authoritative Optimization state: %v", err)
	}
	currentState, err := freezeAuthoritativeState(state, effectiveMapping, capturedAt)
	if err != nil {
		return PreparedOptimization{}, err
	}
	constraints := HVACConstraints{
		SupplyTempMinC: policy.Safety.SupplyTempMinC, SupplyTempMaxC: policy.Safety.SupplyTempMaxC,
		ZoneTempMinC: policy.Comfort.ZoneTempMinC, ZoneTempMaxC: policy.Comfort.ZoneTempMaxC, MaxSupplyTempStep: policy.Safety.MaxSupplyTempStep,
	}
	if err := validateHVACExecutionInputs(currentState.Baseline, constraints, policy.ResponseModel); err != nil {
		if errors.Is(err, errBaselineOutsideConstraints) {
			return PreparedOptimization{}, unavailable(ReasonBaselineOutsideLimits, "%v", err)
		}
		return PreparedOptimization{}, unavailable(ReasonInputsInvalid, "frozen Optimization inputs are invalid: %v", err)
	}
	prepared := PreparedInput{
		Trigger:  request.Trigger,
		TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		PolicyVersionID: definition.PolicyVersionID, TopologyVersionID: definition.TopologyVersionID,
		LoadForecastSnapshotID: definition.LoadForecastSnapshotID, PVForecastSnapshotID: definition.PVForecastSnapshotID,
		TariffVersionID: definition.TariffVersionID, DeploymentRevisionID: definition.DeploymentRevisionID,
		Objective: definition.Objective, Horizon: definition.Horizon, HorizonMinutes: definition.HorizonMinutes, Granularity: definition.Granularity,
		CapturedAt: capturedAt, CurrentState: currentState,
		SafetyConstraints: FrozenSafetyConstraints{
			Comfort: policy.Comfort, Safety: policy.Safety, InputMapping: effectiveMapping, ResponseModel: policy.ResponseModel,
			ModelDeploymentRevisionID: definition.DeploymentRevisionID,
		},
		MaintenanceConstraints: policy.MaintenanceConstraints, ManualLocks: policy.ManualLocks,
	}
	prepared.InputChecksum, err = checksumPreparedOptimization(prepared)
	if err != nil {
		return PreparedOptimization{}, err
	}
	result, err := preparer.store.CreatePreparedOptimization(ctx, prepared, capturedAt)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("persist prepared Optimization input/run: %w", err)
	}
	return result, nil
}

func decodePolicyContract(raw json.RawMessage) (policyContract, error) {
	var policy policyContract
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policyContract{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return policyContract{}, errors.New("policy constraints contain trailing JSON")
	}
	policy.InputMapping.SupplyTemperatureKey = strings.TrimSpace(policy.InputMapping.SupplyTemperatureKey)
	policy.InputMapping.ZoneTemperatureKey = strings.TrimSpace(policy.InputMapping.ZoneTemperatureKey)
	for i, key := range policy.InputMapping.ZoneTemperatureKeys {
		clean := strings.TrimSpace(key)
		if clean == "" {
			return policyContract{}, errors.New("inputMapping.zoneTemperatureKeys cannot contain empty keys")
		}
		policy.InputMapping.ZoneTemperatureKeys[i] = clean
	}
	if policy.InputMapping.SupplyTemperatureKey == "" || len(policy.InputMapping.effectiveZoneKeys()) == 0 {
		return policyContract{}, errors.New("inputMapping requires supplyTemperatureKey and zoneTemperatureKey")
	}
	if policy.MaintenanceConstraints.OutOfService == nil || policy.ManualLocks.Resources == nil {
		return policyContract{}, errors.New("maintenanceConstraints.outOfService and manualLocks.resources must be explicit arrays")
	}
	return policy, nil
}

func telemetryHealthBlockReason(healthStatus string, reasons []string) (string, bool) {
	status := strings.ToUpper(strings.TrimSpace(healthStatus))
	if status == "UNHEALTHY" || status == "BAD" {
		return status, true
	}
	for _, reason := range reasons {
		clean := strings.ToUpper(strings.TrimSpace(reason))
		if clean == "PROBE_OFFSET" || clean == "UNHEALTHY" {
			return clean, true
		}
	}
	return "", false
}

func freezeAuthoritativeState(state AuthoritativeState, mapping InputMapping, at time.Time) (FrozenCurrentState, error) {
	if err := validateEnergyEvidence(state.PricedDay, at); err != nil {
		return FrozenCurrentState{}, unavailable(ReasonPricedDayMissing, "%v", err)
	}
	// A reading that is not GOOD or not fresh is as unusable as no reading.
	if err := validateTelemetryEvidence(state.SupplyTemperature, mapping.SupplyTemperatureKey, at); err != nil {
		return FrozenCurrentState{}, unavailable(ReasonSupplyTempMissing, "%v", err)
	}
	if reason, unhealthy := telemetryHealthBlockReason(state.SupplyTemperature.HealthStatus, state.SupplyTemperature.QualityReasons); unhealthy {
		return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", mapping.SupplyTemperatureKey, reason)
	}
	if state.SupplyTemperature.Unit != "Cel" {
		return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative Optimization inputs use unsupported units")
	}

	zoneKeys := mapping.effectiveZoneKeys()
	var zoneReadings []TelemetryEvidence
	if len(state.ZoneTemperatures) > 0 {
		byKey := make(map[string]TelemetryEvidence, len(state.ZoneTemperatures))
		for _, item := range state.ZoneTemperatures {
			byKey[item.TelemetryKey] = item
		}
		for _, expectedKey := range zoneKeys {
			item, ok := byKey[expectedKey]
			if !ok {
				return FrozenCurrentState{}, unavailable(ReasonZoneTempMissing, "authoritative telemetry %s evidence is missing", expectedKey)
			}
			if err := validateTelemetryEvidence(item, expectedKey, at); err != nil {
				return FrozenCurrentState{}, unavailable(ReasonZoneTempMissing, "%v", err)
			}
			if reason, unhealthy := telemetryHealthBlockReason(item.HealthStatus, item.QualityReasons); unhealthy {
				return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", expectedKey, reason)
			}
			if item.Unit != "Cel" {
				return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative Optimization inputs use unsupported units")
			}
			zoneReadings = append(zoneReadings, item)
		}
	} else {
		if len(zoneKeys) != 1 {
			return FrozenCurrentState{}, unavailable(ReasonZoneTempMissing, "authoritative multi-zone telemetry requires observations for all zone keys")
		}
		if err := validateTelemetryEvidence(state.ZoneTemperature, zoneKeys[0], at); err != nil {
			return FrozenCurrentState{}, unavailable(ReasonZoneTempMissing, "%v", err)
		}
		if reason, unhealthy := telemetryHealthBlockReason(state.ZoneTemperature.HealthStatus, state.ZoneTemperature.QualityReasons); unhealthy {
			return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative telemetry %s is unhealthy (%s)", zoneKeys[0], reason)
		}
		if state.ZoneTemperature.Unit != "Cel" {
			return FrozenCurrentState{}, unavailable(ReasonInputsInvalid, "authoritative Optimization inputs use unsupported units")
		}
		zoneReadings = []TelemetryEvidence{state.ZoneTemperature}
	}

	worstZone := zoneReadings[0]
	for _, item := range zoneReadings[1:] {
		if item.Value > worstZone.Value {
			worstZone = item
		}
	}

	telemetryEvidence := make([]TelemetryEvidence, 0, 1+len(zoneReadings))
	telemetryEvidence = append(telemetryEvidence, canonicalTelemetryEvidence(state.SupplyTemperature))
	for _, item := range zoneReadings {
		telemetryEvidence = append(telemetryEvidence, canonicalTelemetryEvidence(item))
	}

	return FrozenCurrentState{
		SchemaVersion: 1,
		Baseline: HVACBaseline{
			DailyEnergyKWh: state.PricedDay.EnergyKWh, DailyCost: state.PricedDay.CostCNY,
			SupplyTempC: state.SupplyTemperature.Value, ZoneTempC: worstZone.Value,
		},
		EnergyEvidence:    canonicalEnergyEvidence(state.PricedDay),
		TelemetryEvidence: telemetryEvidence,
	}, nil
}

// validateEnergyEvidence checks the priced day's shape; the reader chose it as the latest complete,
// fully priced day.
func validateEnergyEvidence(evidence EnergyDayEvidence, at time.Time) error {
	if evidence.PeriodStart.IsZero() || !evidence.PeriodEnd.After(evidence.PeriodStart) || evidence.PeriodEnd.After(at) ||
		evidence.DatasetRevision == "" || !finite(evidence.EnergyKWh) || !finite(evidence.CostCNY) {
		return errors.New("authoritative priced day evidence is invalid")
	}
	return nil
}

func validateTelemetryEvidence(evidence TelemetryEvidence, expectedKey string, at time.Time) error {
	if !uuidPattern.MatchString(evidence.ObservationID) || !uuidPattern.MatchString(evidence.DeviceID) || !uuidPattern.MatchString(evidence.PointID) ||
		!uuidPattern.MatchString(evidence.SourceEventID) || evidence.TelemetryKey != expectedKey || evidence.PointRevision == 0 || evidence.Quality != "GOOD" ||
		evidence.Unit == "" || evidence.SampledAt.IsZero() || evidence.SampledAt.After(at) || evidence.ReceivedAt.IsZero() || evidence.ReceivedAt.After(at) ||
		evidence.SourcePartition == "" || !finite(evidence.Value) || at.UTC().Sub(evidence.SampledAt.UTC()) > DefaultPreparationMaxTelemetryAge {
		return fmt.Errorf("authoritative telemetry %s evidence is invalid", expectedKey)
	}
	return nil
}

func canonicalEnergyEvidence(evidence EnergyDayEvidence) EnergyDayEvidence {
	evidence.PeriodStart = evidence.PeriodStart.UTC()
	evidence.PeriodEnd = evidence.PeriodEnd.UTC()
	evidence.DataWatermark = evidence.DataWatermark.UTC()
	return evidence
}

func canonicalTelemetryEvidence(evidence TelemetryEvidence) TelemetryEvidence {
	evidence.SampledAt = evidence.SampledAt.UTC()
	evidence.ReceivedAt = evidence.ReceivedAt.UTC()
	return evidence
}

func checksumPreparedOptimization(input PreparedInput) (string, error) {
	payload := struct {
		TenantID               string                  `json:"tenantId"`
		SiteID                 string                  `json:"siteId"`
		SubjectType            string                  `json:"subjectType"`
		SubjectID              string                  `json:"subjectId"`
		PolicyVersionID        string                  `json:"policyVersionId"`
		TopologyVersionID      string                  `json:"topologyVersionId"`
		LoadForecastSnapshotID string                  `json:"loadForecastSnapshotId"`
		PVForecastSnapshotID   *string                 `json:"pvForecastSnapshotId"`
		TariffVersionID        string                  `json:"tariffVersionId"`
		DeploymentRevisionID   string                  `json:"deploymentRevisionId"`
		Objective              string                  `json:"objective"`
		Horizon                string                  `json:"horizon"`
		HorizonMinutes         int                     `json:"horizonMinutes"`
		Granularity            string                  `json:"granularity"`
		CapturedAt             time.Time               `json:"capturedAt"`
		CurrentState           FrozenCurrentState      `json:"currentState"`
		SafetyConstraints      FrozenSafetyConstraints `json:"safetyConstraints"`
		MaintenanceConstraints MaintenanceConstraints  `json:"maintenanceConstraints"`
		ManualLocks            ManualLocks             `json:"manualLocks"`
	}{
		TenantID: input.TenantID, SiteID: input.SiteID, SubjectType: input.SubjectType, SubjectID: input.SubjectID,
		PolicyVersionID: input.PolicyVersionID, TopologyVersionID: input.TopologyVersionID,
		LoadForecastSnapshotID: input.LoadForecastSnapshotID, PVForecastSnapshotID: input.PVForecastSnapshotID,
		TariffVersionID: input.TariffVersionID, DeploymentRevisionID: input.DeploymentRevisionID,
		Objective: input.Objective, Horizon: input.Horizon, HorizonMinutes: input.HorizonMinutes, Granularity: input.Granularity,
		CapturedAt: input.CapturedAt.UTC(), CurrentState: input.CurrentState, SafetyConstraints: input.SafetyConstraints,
		MaintenanceConstraints: input.MaintenanceConstraints, ManualLocks: input.ManualLocks,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Optimization input checksum payload: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func executionRequestFromPrepared(input PreparedInput, runID, snapshotID string) (Request, error) {
	if len(input.MaintenanceConstraints.OutOfService) != 0 || len(input.ManualLocks.Resources) != 0 {
		return Request{}, errors.New("frozen Optimization input contains unsupported active maintenance constraints or manual locks")
	}
	request := Request{
		TenantID: input.TenantID, SiteID: input.SiteID, SubjectType: input.SubjectType, SubjectID: input.SubjectID,
		OptimizationRunID: runID, InputSnapshotID: snapshotID, InputChecksum: input.InputChecksum,
		PolicyVersionID: input.PolicyVersionID, TopologyVersionID: input.TopologyVersionID,
		LoadForecastSnapshotID: input.LoadForecastSnapshotID, PVForecastSnapshotID: input.PVForecastSnapshotID,
		TariffVersionID: input.TariffVersionID, DeploymentRevisionID: input.DeploymentRevisionID,
		Objective: input.Objective, Horizon: input.Horizon, HorizonMinutes: input.HorizonMinutes, Granularity: input.Granularity,
		ValidFrom: input.CapturedAt.UTC(), Baseline: input.CurrentState.Baseline,
		Constraints: HVACConstraints{
			SupplyTempMinC:    input.SafetyConstraints.Safety.SupplyTempMinC,
			SupplyTempMaxC:    input.SafetyConstraints.Safety.SupplyTempMaxC,
			ZoneTempMinC:      input.SafetyConstraints.Comfort.ZoneTempMinC,
			ZoneTempMaxC:      input.SafetyConstraints.Comfort.ZoneTempMaxC,
			MaxSupplyTempStep: input.SafetyConstraints.Safety.MaxSupplyTempStep,
		},
		ResponseModel: input.SafetyConstraints.ResponseModel,
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}
