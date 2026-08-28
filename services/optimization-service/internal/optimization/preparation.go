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
	"time"
)

var ErrPreparationUnavailable = errors.New("optimization authoritative input is unavailable")

type PreparationRequest struct {
	TenantID    string `json:"-"`
	SiteID      string `json:"siteId"`
	SubjectType string `json:"-"`
	SubjectID   string `json:"-"`
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
}

type InputMapping struct {
	SupplyTemperatureKey string `json:"supplyTemperatureKey"`
	ZoneTemperatureKey   string `json:"zoneTemperatureKey"`
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

type policyContract struct {
	Comfort                ComfortConstraints     `json:"comfort"`
	Safety                 SafetyConstraints      `json:"safety"`
	InputMapping           InputMapping           `json:"inputMapping"`
	MaintenanceConstraints MaintenanceConstraints `json:"maintenanceConstraints"`
	ManualLocks            ManualLocks            `json:"manualLocks"`
	ResponseModel          HVACResponseModel      `json:"responseModel"`
}

type MetricEvidence struct {
	ResultID        string    `json:"resultId"`
	MetricVersionID string    `json:"metricVersionId"`
	MetricCode      string    `json:"metricCode"`
	PeriodStart     time.Time `json:"periodStart"`
	PeriodEnd       time.Time `json:"periodEnd"`
	CalculatedAt    time.Time `json:"calculatedAt"`
	Value           float64   `json:"value"`
	Unit            string    `json:"unit"`
	Quality         string    `json:"quality"`
	Revision        uint64    `json:"revision"`
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
	SourceEventID   string    `json:"sourceEventId"`
	SourcePartition string    `json:"sourcePartition"`
	SourceOffset    uint64    `json:"sourceOffset"`
}

type AuthoritativeState struct {
	DailyEnergy       MetricEvidence
	DailyCost         MetricEvidence
	SupplyTemperature TelemetryEvidence
	ZoneTemperature   TelemetryEvidence
}

type OptimizationStateQuery struct {
	TenantID             string
	SiteID               string
	SubjectType          string
	SubjectID            string
	SupplyTemperatureKey string
	ZoneTemperatureKey   string
	At                   time.Time
}

type FrozenCurrentState struct {
	SchemaVersion     int                 `json:"schemaVersion"`
	Baseline          HVACBaseline        `json:"baseline"`
	MetricEvidence    []MetricEvidence    `json:"metricEvidence"`
	TelemetryEvidence []TelemetryEvidence `json:"telemetryEvidence"`
}

type PreparedInput struct {
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
	store  PreparationStore
	inputs AuthoritativeInputReader
	clock  Clock
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

func (preparer *Preparer) Prepare(ctx context.Context, request PreparationRequest) (PreparedOptimization, error) {
	if err := request.Validate(); err != nil {
		return PreparedOptimization{}, err
	}
	capturedAt := preparer.clock().UTC()
	definition, err := preparer.store.ResolveOptimizationPreparation(ctx, request, capturedAt)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("resolve Optimization preparation lineage: %w", err)
	}
	if definition.Objective != "COST" || definition.Horizon != "DAY_AHEAD" || definition.HorizonMinutes != 1440 || definition.Granularity != "15MIN" {
		return PreparedOptimization{}, fmt.Errorf("%w: current HVAC solver requires COST DAY_AHEAD 1440-minute optimization at 15MIN granularity", ErrPreparationUnavailable)
	}
	policy, err := decodePolicyContract(definition.PolicyConstraints)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("%w: Optimization policy mapping is unsupported: %v", ErrPreparationUnavailable, err)
	}
	if len(policy.MaintenanceConstraints.OutOfService) != 0 || len(policy.ManualLocks.Resources) != 0 {
		return PreparedOptimization{}, fmt.Errorf("%w: current HVAC solver cannot safely optimize while maintenance constraints or manual locks are active", ErrPreparationUnavailable)
	}
	state, err := preparer.inputs.ReadOptimizationState(ctx, OptimizationStateQuery{
		TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		SupplyTemperatureKey: policy.InputMapping.SupplyTemperatureKey, ZoneTemperatureKey: policy.InputMapping.ZoneTemperatureKey, At: capturedAt,
	})
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("%w: read authoritative Optimization state: %v", ErrPreparationUnavailable, err)
	}
	currentState, err := freezeAuthoritativeState(state, policy.InputMapping, capturedAt)
	if err != nil {
		return PreparedOptimization{}, fmt.Errorf("%w: %v", ErrPreparationUnavailable, err)
	}
	constraints := HVACConstraints{
		SupplyTempMinC: policy.Safety.SupplyTempMinC, SupplyTempMaxC: policy.Safety.SupplyTempMaxC,
		ZoneTempMinC: policy.Comfort.ZoneTempMinC, ZoneTempMaxC: policy.Comfort.ZoneTempMaxC, MaxSupplyTempStep: policy.Safety.MaxSupplyTempStep,
	}
	if err := validateHVACExecutionInputs(currentState.Baseline, constraints, policy.ResponseModel); err != nil {
		return PreparedOptimization{}, fmt.Errorf("%w: frozen Optimization inputs are invalid: %v", ErrPreparationUnavailable, err)
	}
	prepared := PreparedInput{
		TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		PolicyVersionID: definition.PolicyVersionID, TopologyVersionID: definition.TopologyVersionID,
		LoadForecastSnapshotID: definition.LoadForecastSnapshotID, PVForecastSnapshotID: definition.PVForecastSnapshotID,
		TariffVersionID: definition.TariffVersionID, DeploymentRevisionID: definition.DeploymentRevisionID,
		Objective: definition.Objective, Horizon: definition.Horizon, HorizonMinutes: definition.HorizonMinutes, Granularity: definition.Granularity,
		CapturedAt: capturedAt, CurrentState: currentState,
		SafetyConstraints: FrozenSafetyConstraints{
			Comfort: policy.Comfort, Safety: policy.Safety, InputMapping: policy.InputMapping, ResponseModel: policy.ResponseModel,
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
	if policy.InputMapping.SupplyTemperatureKey == "" || policy.InputMapping.ZoneTemperatureKey == "" {
		return policyContract{}, errors.New("inputMapping requires supplyTemperatureKey and zoneTemperatureKey")
	}
	if policy.MaintenanceConstraints.OutOfService == nil || policy.ManualLocks.Resources == nil {
		return policyContract{}, errors.New("maintenanceConstraints.outOfService and manualLocks.resources must be explicit arrays")
	}
	return policy, nil
}

func freezeAuthoritativeState(state AuthoritativeState, mapping InputMapping, at time.Time) (FrozenCurrentState, error) {
	for expectedCode, evidence := range map[string]MetricEvidence{"daily_energy": state.DailyEnergy, "energy_cost": state.DailyCost} {
		if err := validateMetricEvidence(evidence, expectedCode, at); err != nil {
			return FrozenCurrentState{}, err
		}
	}
	for expectedKey, evidence := range map[string]TelemetryEvidence{mapping.SupplyTemperatureKey: state.SupplyTemperature, mapping.ZoneTemperatureKey: state.ZoneTemperature} {
		if err := validateTelemetryEvidence(evidence, expectedKey, at); err != nil {
			return FrozenCurrentState{}, err
		}
	}
	if state.DailyEnergy.Unit != "kWh" || state.SupplyTemperature.Unit != "Cel" || state.ZoneTemperature.Unit != "Cel" {
		return FrozenCurrentState{}, errors.New("authoritative Optimization inputs use unsupported units")
	}
	return FrozenCurrentState{
		SchemaVersion: 1,
		Baseline: HVACBaseline{
			DailyEnergyKWh: state.DailyEnergy.Value, DailyCost: state.DailyCost.Value,
			SupplyTempC: state.SupplyTemperature.Value, ZoneTempC: state.ZoneTemperature.Value,
		},
		MetricEvidence:    []MetricEvidence{canonicalMetricEvidence(state.DailyEnergy), canonicalMetricEvidence(state.DailyCost)},
		TelemetryEvidence: []TelemetryEvidence{canonicalTelemetryEvidence(state.SupplyTemperature), canonicalTelemetryEvidence(state.ZoneTemperature)},
	}, nil
}

func validateMetricEvidence(evidence MetricEvidence, expectedCode string, at time.Time) error {
	if !uuidPattern.MatchString(evidence.ResultID) || !uuidPattern.MatchString(evidence.MetricVersionID) || evidence.MetricCode != expectedCode ||
		evidence.Quality != "GOOD" || evidence.Unit == "" || evidence.PeriodStart.IsZero() || !evidence.PeriodEnd.After(evidence.PeriodStart) ||
		evidence.PeriodEnd.After(at) || evidence.CalculatedAt.IsZero() || evidence.CalculatedAt.After(at) || !finite(evidence.Value) || evidence.Revision == 0 {
		return fmt.Errorf("authoritative metric %s evidence is invalid", expectedCode)
	}
	return nil
}

func validateTelemetryEvidence(evidence TelemetryEvidence, expectedKey string, at time.Time) error {
	if !uuidPattern.MatchString(evidence.ObservationID) || !uuidPattern.MatchString(evidence.DeviceID) || !uuidPattern.MatchString(evidence.PointID) ||
		!uuidPattern.MatchString(evidence.SourceEventID) || evidence.TelemetryKey != expectedKey || evidence.PointRevision == 0 || evidence.Quality != "GOOD" ||
		evidence.Unit == "" || evidence.SampledAt.IsZero() || evidence.SampledAt.After(at) || evidence.ReceivedAt.IsZero() || evidence.ReceivedAt.After(at) ||
		evidence.SourcePartition == "" || !finite(evidence.Value) {
		return fmt.Errorf("authoritative telemetry %s evidence is invalid", expectedKey)
	}
	return nil
}

func canonicalMetricEvidence(evidence MetricEvidence) MetricEvidence {
	evidence.PeriodStart = evidence.PeriodStart.UTC()
	evidence.PeriodEnd = evidence.PeriodEnd.UTC()
	evidence.CalculatedAt = evidence.CalculatedAt.UTC()
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
