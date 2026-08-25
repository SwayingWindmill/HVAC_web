package energy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	SourceQualityGood      = "GOOD"
	SourceQualityPartial   = "PARTIAL"
	SourceQualityEstimated = "ESTIMATED"
	SourceQualityManual    = "MANUAL"
	SourceQualityStale     = "STALE"
	SourceQualityInvalid   = "INVALID"

	FactQualityValid   = "VALID"
	FactQualitySuspect = "SUSPECT"
	FactQualityInvalid = "INVALID"

	EnergyTypeElectricity = "electricity"
	MeterRolePrimary      = "PRIMARY"
	PointTypeCounter      = "COUNTER"

	TransitionIncrease         TransitionType = "INCREASE"
	TransitionUnchanged        TransitionType = "UNCHANGED"
	TransitionRecovery         TransitionType = "RECOVERY"
	TransitionReset            TransitionType = "RESET"
	TransitionRollover         TransitionType = "ROLLOVER"
	TransitionInitial          TransitionType = "INITIAL"
	TransitionRevisionBoundary TransitionType = "REVISION_BOUNDARY"
	TransitionUnitBoundary     TransitionType = "UNIT_BOUNDARY"
	TransitionInvalidDecrease  TransitionType = "INVALID_DECREASE"
)

const (
	BindingMatch     BindingResolutionStatus = "MATCH"
	BindingNoMatch   BindingResolutionStatus = "NO_MATCH"
	BindingAmbiguous BindingResolutionStatus = "AMBIGUOUS"
)

const ReasonSourceQualityInvalid = "SOURCE_QUALITY_INVALID"

// TransitionType is calculated by the telemetry-history counter_deltas view.
// The projector consumes it as a contract and does not recalculate deltas.
type TransitionType string

func (t TransitionType) ProducesFact() bool {
	switch t {
	case TransitionIncrease, TransitionUnchanged, TransitionRecovery, TransitionReset, TransitionRollover:
		return true
	default:
		return false
	}
}

// CounterDelta is the canonical source row produced by telemetry_history.counter_deltas.
type CounterDelta struct {
	TenantID               string
	SiteID                 string
	DeviceID               string
	PointID                string
	SensorID               string
	TelemetryKey           string
	PointRevision          uint64
	Unit                   string
	CounterDecreaseMode    string
	CounterRolloverModulus *float64
	CurrentObservationID   string
	CurrentSampledAt       time.Time
	CurrentReceivedAt      time.Time
	CurrentQuality         string
	CurrentQualityReasons  []string
	CurrentSourceEventID   string
	CurrentSourcePartition string
	CurrentSourceOffset    uint64
	PreviousObservationID  string
	PreviousValue          float64
	PreviousSampledAt      time.Time
	PreviousQuality        string
	PreviousQualityReasons []string
	TransitionType         TransitionType
	DeltaValue             *float64
}

type BindingResolveInput struct {
	TenantID  string
	SiteID    string
	DeviceID  string
	PointID   string
	SampledAt time.Time
}

type BindingResolutionStatus string

type BindingResolution struct {
	Status            BindingResolutionStatus
	TenantID          string
	SiteID            string
	MeterID           string
	MeterBindingID    string
	TopologyVersionID string
	BindingVersion    uint64
	BindingRevision   uint64
	EnergyTypeID      string
	EnergyType        string
	MeterRole         string
	Direction         string
	DeviceID          string
	PointID           string
	PointType         string
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
}

type EnergyIntervalFact struct {
	FactID                 string         `json:"fact_id"`
	TenantID               string         `json:"tenant_id"`
	SiteID                 string         `json:"site_id"`
	MeterID                string         `json:"meter_id"`
	MeterBindingID         string         `json:"meter_binding_id"`
	TopologyVersionID      string         `json:"topology_version_id"`
	BindingVersion         uint64         `json:"binding_version"`
	EnergyTypeID           string         `json:"energy_type_id"`
	EnergyType             string         `json:"energy_type"`
	MeterRole              string         `json:"meter_role"`
	Direction              string         `json:"direction"`
	DeviceID               string         `json:"device_id"`
	PointID                string         `json:"point_id"`
	SensorID               *string        `json:"sensor_id,omitempty"`
	TelemetryKey           string         `json:"telemetry_key"`
	PointRevision          uint64         `json:"point_revision"`
	Unit                   string         `json:"unit"`
	CounterDecreaseMode    string         `json:"counter_decrease_mode"`
	CounterRolloverModulus *float64       `json:"counter_rollover_modulus,omitempty"`
	PeriodStart            time.Time      `json:"period_start"`
	PeriodEnd              time.Time      `json:"period_end"`
	EnergyKWh              float64        `json:"energy_kwh"`
	TransitionType         TransitionType `json:"transition_type"`
	Quality                string         `json:"quality"`
	QualityReasons         []string       `json:"quality_reasons"`
	PreviousObservationID  string         `json:"source_previous_observation_id"`
	CurrentObservationID   string         `json:"source_current_observation_id"`
	SourceEventID          string         `json:"source_event_id"`
	SourcePartition        string         `json:"source_partition"`
	SourceOffset           uint64         `json:"source_offset"`
	DatasetRevision        uint64         `json:"dataset_revision"`
	DataWatermark          time.Time      `json:"data_watermark"`
	ProjectedAt            time.Time      `json:"projected_at"`
	FactRevision           uint64         `json:"fact_revision"`
	RebuildRunID           *string        `json:"rebuild_run_id,omitempty"`
}

func (f EnergyIntervalFact) LogicalKey() string {
	return strings.Join([]string{f.TenantID, f.SiteID, f.MeterBindingID, f.CurrentObservationID}, "|")
}

type CounterSource interface {
	ListDeltas(context.Context, int) ([]CounterDelta, error)
}

type BindingResolver interface {
	Resolve(context.Context, BindingResolveInput) (BindingResolution, error)
}

type FactSink interface {
	InsertFacts(context.Context, []EnergyIntervalFact) error
}

type ProjectorConfig struct {
	CounterSource   CounterSource
	BindingResolver BindingResolver
	FactSink        FactSink
	BatchSize       int
	Now             func() time.Time
}

type Projector struct {
	source    CounterSource
	resolver  BindingResolver
	sink      FactSink
	batchSize int
	now       func() time.Time
}

func NewProjector(config ProjectorConfig) (*Projector, error) {
	if config.CounterSource == nil {
		return nil, errors.New("energy projector counter source is required")
	}
	if config.BindingResolver == nil {
		return nil, errors.New("energy projector binding resolver is required")
	}
	if config.FactSink == nil {
		return nil, errors.New("energy projector fact sink is required")
	}
	if config.BatchSize <= 0 {
		return nil, errors.New("energy projector batch size must be positive")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Projector{
		source:    config.CounterSource,
		resolver:  config.BindingResolver,
		sink:      config.FactSink,
		batchSize: config.BatchSize,
		now:       config.Now,
	}, nil
}

func (p *Projector) ProjectOnce(ctx context.Context) (int, error) {
	deltas, err := p.source.ListDeltas(ctx, p.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list counter deltas: %w", err)
	}
	if len(deltas) == 0 {
		return 0, nil
	}

	projectedAt := p.now().UTC()
	facts := make([]EnergyIntervalFact, 0, len(deltas))
	seen := make(map[string]struct{}, len(deltas))
	for _, delta := range deltas {
		if !delta.TransitionType.ProducesFact() {
			return 0, fmt.Errorf("counter delta %s has non-fact transition %q", delta.CurrentObservationID, delta.TransitionType)
		}
		resolution, resolveErr := p.resolver.Resolve(ctx, BindingResolveInput{
			TenantID:  delta.TenantID,
			SiteID:    delta.SiteID,
			DeviceID:  delta.DeviceID,
			PointID:   delta.PointID,
			SampledAt: delta.CurrentSampledAt,
		})
		if resolveErr != nil {
			return 0, fmt.Errorf("resolve meter binding for observation %s: %w", delta.CurrentObservationID, resolveErr)
		}
		if resolution.Status != BindingMatch {
			return 0, fmt.Errorf("resolve meter binding for observation %s: %s", delta.CurrentObservationID, resolution.Status)
		}
		if err := validateBindingResolution(delta, resolution); err != nil {
			return 0, fmt.Errorf("validate meter binding for observation %s: %w", delta.CurrentObservationID, err)
		}
		fact, buildErr := BuildFact(delta, resolution, projectedAt)
		if buildErr != nil {
			return 0, fmt.Errorf("build energy fact for observation %s: %w", delta.CurrentObservationID, buildErr)
		}
		key := fact.LogicalKey()
		if _, exists := seen[key]; exists {
			return 0, fmt.Errorf("duplicate energy fact logical key %q in batch", key)
		}
		seen[key] = struct{}{}
		facts = append(facts, fact)
	}

	if err := p.sink.InsertFacts(ctx, facts); err != nil {
		return 0, fmt.Errorf("insert energy facts: %w", err)
	}
	return len(facts), nil
}

func BuildFact(delta CounterDelta, binding BindingResolution, projectedAt time.Time) (EnergyIntervalFact, error) {
	if err := validateCounterDelta(delta); err != nil {
		return EnergyIntervalFact{}, err
	}
	if err := validateBindingResolution(delta, binding); err != nil {
		return EnergyIntervalFact{}, err
	}
	quality, qualityReasons := mapQuality(delta.CurrentQuality, delta.CurrentQualityReasons, delta.PreviousQuality, delta.PreviousQualityReasons)
	var sensorID *string
	if strings.TrimSpace(delta.SensorID) != "" {
		sensorID = &delta.SensorID
	}

	return EnergyIntervalFact{
		FactID:                 delta.CurrentObservationID,
		TenantID:               delta.TenantID,
		SiteID:                 delta.SiteID,
		MeterID:                binding.MeterID,
		MeterBindingID:         binding.MeterBindingID,
		TopologyVersionID:      binding.TopologyVersionID,
		BindingVersion:         binding.BindingVersion,
		EnergyTypeID:           binding.EnergyTypeID,
		EnergyType:             binding.EnergyType,
		MeterRole:              binding.MeterRole,
		Direction:              binding.Direction,
		DeviceID:               delta.DeviceID,
		PointID:                delta.PointID,
		SensorID:               sensorID,
		TelemetryKey:           delta.TelemetryKey,
		PointRevision:          delta.PointRevision,
		Unit:                   delta.Unit,
		CounterDecreaseMode:    delta.CounterDecreaseMode,
		CounterRolloverModulus: delta.CounterRolloverModulus,
		PeriodStart:            delta.PreviousSampledAt.UTC(),
		PeriodEnd:              delta.CurrentSampledAt.UTC(),
		EnergyKWh:              *delta.DeltaValue,
		TransitionType:         delta.TransitionType,
		Quality:                quality,
		QualityReasons:         qualityReasons,
		PreviousObservationID:  delta.PreviousObservationID,
		CurrentObservationID:   delta.CurrentObservationID,
		SourceEventID:          delta.CurrentSourceEventID,
		SourcePartition:        delta.CurrentSourcePartition,
		SourceOffset:           delta.CurrentSourceOffset,
		DatasetRevision:        delta.CurrentSourceOffset,
		DataWatermark:          delta.CurrentSampledAt.UTC(),
		ProjectedAt:            projectedAt.UTC(),
		FactRevision:           0,
	}, nil
}

func validateCounterDelta(delta CounterDelta) error {
	for name, value := range map[string]string{
		"tenant_id":               delta.TenantID,
		"site_id":                 delta.SiteID,
		"device_id":               delta.DeviceID,
		"point_id":                delta.PointID,
		"current_observation_id":  delta.CurrentObservationID,
		"previous_observation_id": delta.PreviousObservationID,
		"unit":                    delta.Unit,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if delta.CurrentSampledAt.IsZero() || delta.PreviousSampledAt.IsZero() {
		return errors.New("sampled_at values are required")
	}
	if !delta.CurrentSampledAt.After(delta.PreviousSampledAt) {
		return errors.New("current sampled_at must be after previous sampled_at")
	}
	if !delta.TransitionType.ProducesFact() {
		return fmt.Errorf("transition %q does not produce a fact", delta.TransitionType)
	}
	if delta.DeltaValue == nil || math.IsNaN(*delta.DeltaValue) || math.IsInf(*delta.DeltaValue, 0) {
		return errors.New("delta_value must be finite and non-null")
	}
	if *delta.DeltaValue < 0 {
		return errors.New("delta_value cannot be negative")
	}
	return nil
}

func validateBindingResolution(delta CounterDelta, binding BindingResolution) error {
	if binding.Status != BindingMatch {
		return fmt.Errorf("binding resolution status is %q", binding.Status)
	}
	for name, value := range map[string]string{
		"meter_id":            binding.MeterID,
		"meter_binding_id":    binding.MeterBindingID,
		"topology_version_id": binding.TopologyVersionID,
		"energy_type_id":      binding.EnergyTypeID,
		"device_id":           binding.DeviceID,
		"point_id":            binding.PointID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if binding.TenantID != delta.TenantID || binding.SiteID != delta.SiteID || binding.DeviceID != delta.DeviceID || binding.PointID != delta.PointID {
		return errors.New("binding snapshot does not match counter delta scope")
	}
	if binding.EnergyType != EnergyTypeElectricity || binding.MeterRole != MeterRolePrimary || binding.PointType != PointTypeCounter {
		return errors.New("binding snapshot is not a released PRIMARY electricity counter")
	}
	if binding.EffectiveFrom.IsZero() || delta.CurrentSampledAt.Before(binding.EffectiveFrom) || (binding.EffectiveTo != nil && !delta.CurrentSampledAt.Before(*binding.EffectiveTo)) {
		return errors.New("binding snapshot is outside its effective interval")
	}
	return nil
}

func mapQuality(currentQuality string, currentReasons []string, previousQuality string, previousReasons []string) (string, []string) {
	current, currentInvalid := normalizeQuality(currentQuality)
	previous, previousInvalid := normalizeQuality(previousQuality)
	quality := FactQualityValid
	if current == FactQualityInvalid || previous == FactQualityInvalid {
		quality = FactQualityInvalid
	} else if current == FactQualitySuspect || previous == FactQualitySuspect {
		quality = FactQualitySuspect
	}
	reasons := append([]string{}, previousReasons...)
	reasons = append(reasons, currentReasons...)
	if currentInvalid || previousInvalid {
		reasons = append(reasons, ReasonSourceQualityInvalid)
	}
	return quality, uniqueStrings(reasons)
}

func normalizeQuality(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case SourceQualityGood:
		return FactQualityValid, false
	case SourceQualityPartial, SourceQualityEstimated, SourceQualityManual:
		return FactQualitySuspect, false
	case SourceQualityStale, SourceQualityInvalid:
		return FactQualityInvalid, false
	default:
		return FactQualityInvalid, true
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
