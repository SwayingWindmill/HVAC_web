package energy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	CumulativeElectricityTelemetryKey = "hvac_meter.energy"
	EnergyTypeElectricity             = "electricity"

	SourceQualityGood    = "GOOD"
	SourceQualitySuspect = "SUSPECT"

	QualityValid   = "VALID"
	QualitySuspect = "SUSPECT"
	QualityInvalid = "INVALID"

	ReasonMeterResetOrRollback    = "METER_RESET_OR_ROLLBACK"
	ReasonNegativeCumulativeValue = "NEGATIVE_CUMULATIVE_VALUE"
	ReasonSourceQualityInvalid    = "SOURCE_QUALITY_INVALID"
)

type Candidate struct {
	PreviousObservationID  string
	CurrentObservationID   string
	TenantID               string
	OrganizationID         string
	SiteID                 string
	DeviceID               string
	PointID                string
	SensorID               *string
	TelemetryKey           string
	PreviousValue          float64
	CurrentValue           float64
	PreviousQuality        string
	CurrentQuality         string
	PreviousQualityReasons []string
	CurrentQualityReasons  []string
	PreviousSampledAt      time.Time
	CurrentSampledAt       time.Time
	SourceOffset           uint64
}

type Fact struct {
	FactID                      string    `json:"fact_id"`
	TenantID                    string    `json:"tenant_id"`
	OrganizationID              string    `json:"organization_id"`
	SiteID                      string    `json:"site_id"`
	DeviceID                    string    `json:"device_id"`
	PointID                     string    `json:"point_id"`
	SensorID                    *string   `json:"sensor_id"`
	TelemetryKey                string    `json:"telemetry_key"`
	EnergyType                  string    `json:"energy_type"`
	PeriodStart                 time.Time `json:"period_start"`
	PeriodEnd                   time.Time `json:"period_end"`
	EnergyKWh                   float64   `json:"energy_kwh"`
	Quality                     string    `json:"quality"`
	QualityReasons              []string  `json:"quality_reasons"`
	ObservationCount            uint8     `json:"observation_count"`
	SourcePreviousObservationID string    `json:"source_previous_observation_id"`
	SourceCurrentObservationID  string    `json:"source_current_observation_id"`
	SourceOffset                uint64    `json:"source_offset"`
	DatasetRevision             uint64    `json:"dataset_revision"`
	DataWatermark               time.Time `json:"data_watermark"`
	ProjectedAt                 time.Time `json:"projected_at"`
}

func BuildFact(candidate Candidate, projectedAt time.Time) (Fact, error) {
	if strings.TrimSpace(candidate.PreviousObservationID) == "" || strings.TrimSpace(candidate.CurrentObservationID) == "" ||
		strings.TrimSpace(candidate.TenantID) == "" || strings.TrimSpace(candidate.OrganizationID) == "" || strings.TrimSpace(candidate.SiteID) == "" ||
		strings.TrimSpace(candidate.DeviceID) == "" || strings.TrimSpace(candidate.PointID) == "" {
		return Fact{}, errors.New("energy interval candidate identifiers are required")
	}
	if candidate.TelemetryKey != CumulativeElectricityTelemetryKey {
		return Fact{}, errors.New("energy interval candidate telemetry key is unsupported")
	}
	if candidate.PreviousSampledAt.IsZero() || candidate.CurrentSampledAt.IsZero() || !candidate.PreviousSampledAt.Before(candidate.CurrentSampledAt) {
		return Fact{}, errors.New("energy interval candidate time range is invalid")
	}
	if candidate.SourceOffset == 0 {
		return Fact{}, errors.New("energy interval candidate source offset is required")
	}
	if math.IsNaN(candidate.PreviousValue) || math.IsNaN(candidate.CurrentValue) || math.IsInf(candidate.PreviousValue, 0) || math.IsInf(candidate.CurrentValue, 0) {
		return Fact{}, errors.New("energy interval candidate cumulative values must be finite")
	}
	if projectedAt.IsZero() {
		projectedAt = time.Now()
	}

	quality, reasons := sourceQuality(candidate)
	energyKWh := candidate.CurrentValue - candidate.PreviousValue
	switch {
	case candidate.PreviousValue < 0 || candidate.CurrentValue < 0:
		energyKWh = 0
		quality = QualityInvalid
		reasons = append(reasons, ReasonNegativeCumulativeValue)
	case energyKWh < 0:
		energyKWh = 0
		if quality != QualityInvalid {
			quality = QualitySuspect
		}
		reasons = append(reasons, ReasonMeterResetOrRollback)
	}

	return Fact{
		FactID:                      candidate.CurrentObservationID,
		TenantID:                    candidate.TenantID,
		OrganizationID:              candidate.OrganizationID,
		SiteID:                      candidate.SiteID,
		DeviceID:                    candidate.DeviceID,
		PointID:                     candidate.PointID,
		SensorID:                    candidate.SensorID,
		TelemetryKey:                candidate.TelemetryKey,
		EnergyType:                  EnergyTypeElectricity,
		PeriodStart:                 candidate.PreviousSampledAt.UTC(),
		PeriodEnd:                   candidate.CurrentSampledAt.UTC(),
		EnergyKWh:                   energyKWh,
		Quality:                     quality,
		QualityReasons:              canonicalReasons(reasons),
		ObservationCount:            2,
		SourcePreviousObservationID: candidate.PreviousObservationID,
		SourceCurrentObservationID:  candidate.CurrentObservationID,
		SourceOffset:                candidate.SourceOffset,
		DatasetRevision:             candidate.SourceOffset,
		DataWatermark:               candidate.CurrentSampledAt.UTC(),
		ProjectedAt:                 projectedAt.UTC(),
	}, nil
}

func sourceQuality(candidate Candidate) (string, []string) {
	reasons := append(append([]string(nil), candidate.PreviousQualityReasons...), candidate.CurrentQualityReasons...)
	if candidate.PreviousQuality == SourceQualityGood && candidate.CurrentQuality == SourceQualityGood {
		return QualityValid, reasons
	}
	if (candidate.PreviousQuality == SourceQualityGood || candidate.PreviousQuality == SourceQualitySuspect) &&
		(candidate.CurrentQuality == SourceQualityGood || candidate.CurrentQuality == SourceQualitySuspect) {
		return QualitySuspect, reasons
	}
	return QualityInvalid, append(reasons, ReasonSourceQualityInvalid)
}

func canonicalReasons(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type Source interface {
	ListCandidates(context.Context, int) ([]Candidate, error)
}

type Sink interface {
	InsertFacts(context.Context, []Fact) error
}

type ProjectorConfig struct {
	Source    Source
	Sink      Sink
	BatchSize int
	Now       func() time.Time
}

type Projector struct {
	source    Source
	sink      Sink
	batchSize int
	now       func() time.Time
}

func NewProjector(config ProjectorConfig) (*Projector, error) {
	if config.Source == nil || config.Sink == nil {
		return nil, errors.New("analytics projector source and sink are required")
	}
	if config.BatchSize < 1 || config.BatchSize > 4096 {
		return nil, errors.New("analytics projector batch size must be between 1 and 4096")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Projector{source: config.Source, sink: config.Sink, batchSize: config.BatchSize, now: config.Now}, nil
}

func (projector *Projector) ProjectOnce(ctx context.Context) (int, error) {
	if projector == nil {
		return 0, errors.New("analytics projector is nil")
	}
	candidates, err := projector.source.ListCandidates(ctx, projector.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list energy interval candidates: %w", err)
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	facts := make([]Fact, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	projectedAt := projector.now().UTC()
	for _, candidate := range candidates {
		if _, exists := seen[candidate.CurrentObservationID]; exists {
			return 0, errors.New("energy interval candidate batch contains duplicate current observation")
		}
		seen[candidate.CurrentObservationID] = struct{}{}
		fact, err := BuildFact(candidate, projectedAt)
		if err != nil {
			return 0, fmt.Errorf("build energy interval fact %s: %w", candidate.CurrentObservationID, err)
		}
		facts = append(facts, fact)
	}
	if err := projector.sink.InsertFacts(ctx, facts); err != nil {
		return 0, fmt.Errorf("insert energy interval facts: %w", err)
	}
	return len(facts), nil
}
