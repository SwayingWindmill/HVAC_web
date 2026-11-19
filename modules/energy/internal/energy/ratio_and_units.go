package energy

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	UnitWh  = "Wh"
	UnitKWh = "kWh"
	UnitMWh = "MWh"
	UnitGJ  = "GJ"
	UnitMJ  = "MJ"
	UnitRTh = "RTh"

	RatioAppModePlatform = "PLATFORM_APPLIED"
	RatioAppModeDevice   = "DEVICE_APPLIED"
	RatioAppModeEdge     = "EDGE_APPLIED"

	RatioStatusDraft      = "DRAFT"
	RatioStatusReleased   = "RELEASED"
	RatioStatusSuperseded = "SUPERSEDED"
)

var unitToKWhFactors = map[string]struct {
	canonical string
	factor    float64
}{
	"wh":  {canonical: UnitWh, factor: 0.001},
	"kwh": {canonical: UnitKWh, factor: 1.0},
	"mwh": {canonical: UnitMWh, factor: 1000.0},
	"gj":  {canonical: UnitGJ, factor: 277.77777777777777},
	"mj":  {canonical: UnitMJ, factor: 0.2777777777777778},
	"rth": {canonical: UnitRTh, factor: 3.5168528420667},
}

// NormalizeEnergyUnit returns the canonical energy unit code or an error if unsupported.
// Empty units default to kWh for backward compatibility with unannotated bindings.
func NormalizeEnergyUnit(unit string) (string, error) {
	trimmed := strings.TrimSpace(unit)
	if trimmed == "" {
		return UnitKWh, nil
	}
	spec, ok := unitToKWhFactors[strings.ToLower(trimmed)]
	if !ok {
		return "", fmt.Errorf("unsupported energy reading unit %q", unit)
	}
	return spec.canonical, nil
}

// UnitToKWhFactor returns the multiplicative factor converting a reading in unit into kWh.
func UnitToKWhFactor(unit string) (float64, error) {
	canonical, err := NormalizeEnergyUnit(unit)
	if err != nil {
		return 0, err
	}
	return unitToKWhFactors[strings.ToLower(canonical)].factor, nil
}

// ConvertEnergyToKWh converts a raw quantity expressed in unit into kWh.
func ConvertEnergyToKWh(value float64, unit string) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("non-finite energy value %v", value)
	}
	factor, err := UnitToKWhFactor(unit)
	if err != nil {
		return 0, err
	}
	return value * factor, nil
}

// RatioVersion describes one effective-dated [EffectiveFrom, EffectiveTo) CT/PT/multiplier and reading unit version.
type RatioVersion struct {
	ID                   string     `json:"id,omitempty"`
	TenantID             string     `json:"tenantId,omitempty"`
	SiteID               string     `json:"siteId,omitempty"`
	MeterBindingID       string     `json:"meterBindingId,omitempty"`
	PointID              string     `json:"pointId,omitempty"`
	CTRatio              float64    `json:"ctRatio"`
	PTRatio              float64    `json:"ptRatio"`
	MeterMultiplier      float64    `json:"meterMultiplier"`
	RatioApplicationMode string     `json:"ratioApplicationMode,omitempty"`
	ReadingUnit          string     `json:"readingUnit"`
	EffectiveFrom        time.Time  `json:"effectiveFrom"`
	EffectiveTo          *time.Time `json:"effectiveTo,omitempty"`
	Status               string     `json:"status,omitempty"`
	Revision             int64      `json:"revision,omitempty"`
}

// ResolvedMultiplier records the effective multiplier and metadata applied to an interval.
type ResolvedMultiplier struct {
	Multiplier     float64
	Unit           string
	RatioVersionID string
	VersionApplied bool
}

// Validate checks positive ratios, finite numbers, supported reading unit, and [EffectiveFrom, EffectiveTo).
func (v RatioVersion) Validate() error {
	if math.IsNaN(v.CTRatio) || math.IsInf(v.CTRatio, 0) || v.CTRatio <= 0 {
		return fmt.Errorf("ct_ratio must be finite and > 0, got %v", v.CTRatio)
	}
	if math.IsNaN(v.PTRatio) || math.IsInf(v.PTRatio, 0) || v.PTRatio <= 0 {
		return fmt.Errorf("pt_ratio must be finite and > 0, got %v", v.PTRatio)
	}
	if math.IsNaN(v.MeterMultiplier) || math.IsInf(v.MeterMultiplier, 0) || v.MeterMultiplier <= 0 {
		return fmt.Errorf("meter_multiplier must be finite and > 0, got %v", v.MeterMultiplier)
	}
	if _, err := NormalizeEnergyUnit(v.ReadingUnit); err != nil {
		return err
	}
	if v.EffectiveTo != nil && !v.EffectiveTo.After(v.EffectiveFrom) {
		return fmt.Errorf("effective_to %s must follow effective_from %s", v.EffectiveTo.Format(time.RFC3339), v.EffectiveFrom.Format(time.RFC3339))
	}
	switch strings.ToUpper(strings.TrimSpace(v.RatioApplicationMode)) {
	case "", RatioAppModePlatform, RatioAppModeDevice, RatioAppModeEdge:
	default:
		return fmt.Errorf("unsupported ratio_application_mode %q", v.RatioApplicationMode)
	}
	switch strings.ToUpper(strings.TrimSpace(v.Status)) {
	case "", RatioStatusDraft, RatioStatusReleased, RatioStatusSuperseded:
	default:
		return fmt.Errorf("unsupported ratio status %q", v.Status)
	}
	return nil
}

// Covers reports whether the version is active and covers timestamp at under half-open [EffectiveFrom, EffectiveTo).
func (v RatioVersion) Covers(at time.Time) bool {
	status := strings.ToUpper(strings.TrimSpace(v.Status))
	if status != "" && status != RatioStatusReleased {
		return false
	}
	if at.Before(v.EffectiveFrom) {
		return false
	}
	if v.EffectiveTo != nil && !at.Before(*v.EffectiveTo) {
		return false
	}
	return true
}

// EffectiveMultiplier returns CTRatio * PTRatio * MeterMultiplier * unitFactor(ReadingUnit)
// for platform-applied ratios, or 1 * unitFactor(ReadingUnit) when already applied by device/edge.
func (v RatioVersion) EffectiveMultiplier() (float64, string, error) {
	if err := v.Validate(); err != nil {
		return 0, "", err
	}
	canonicalUnit, err := NormalizeEnergyUnit(v.ReadingUnit)
	if err != nil {
		return 0, "", err
	}
	unitFactor, err := UnitToKWhFactor(canonicalUnit)
	if err != nil {
		return 0, "", err
	}
	mode := strings.ToUpper(strings.TrimSpace(v.RatioApplicationMode))
	if mode == RatioAppModeDevice || mode == RatioAppModeEdge {
		return unitFactor, canonicalUnit, nil
	}
	return v.CTRatio * v.PTRatio * v.MeterMultiplier * unitFactor, canonicalUnit, nil
}

// LookupRatioVersion returns the active RatioVersion covering timestamp at, rejecting overlapping matches.
func LookupRatioVersion(versions []RatioVersion, at time.Time) (RatioVersion, bool, error) {
	var matched RatioVersion
	found := false
	for _, candidate := range versions {
		if !candidate.Covers(at) {
			continue
		}
		if err := candidate.Validate(); err != nil {
			return RatioVersion{}, false, err
		}
		if found {
			return RatioVersion{}, false, fmt.Errorf("overlapping released ratio versions cover %s (%s and %s)", at.UTC().Format(time.RFC3339), matched.ID, candidate.ID)
		}
		matched = candidate
		found = true
	}
	return matched, found, nil
}

// EffectiveMultiplierAt resolves the historical multiplier at instant at, defaulting to
// 1 * unitFactor(defaultUnit) when no version matches or versions is empty.
func EffectiveMultiplierAt(versions []RatioVersion, defaultUnit string, at time.Time) (ResolvedMultiplier, error) {
	matched, ok, err := LookupRatioVersion(versions, at)
	if err != nil {
		return ResolvedMultiplier{}, err
	}
	if ok {
		mult, canonicalUnit, err := matched.EffectiveMultiplier()
		if err != nil {
			return ResolvedMultiplier{}, err
		}
		return ResolvedMultiplier{
			Multiplier:     mult,
			Unit:           canonicalUnit,
			RatioVersionID: matched.ID,
			VersionApplied: true,
		}, nil
	}
	canonicalDefault, err := NormalizeEnergyUnit(defaultUnit)
	if err != nil {
		return ResolvedMultiplier{}, err
	}
	factor, err := UnitToKWhFactor(canonicalDefault)
	if err != nil {
		return ResolvedMultiplier{}, err
	}
	return ResolvedMultiplier{
		Multiplier:     factor,
		Unit:           canonicalDefault,
		VersionApplied: false,
	}, nil
}

// IntervalRatioLookupTime returns the interval midpoint when start < end, else end,
// ensuring boundary-aligned intervals [start, end) map deterministically into [EffectiveFrom, EffectiveTo).
func IntervalRatioLookupTime(periodStart, periodEnd time.Time) time.Time {
	if periodStart.IsZero() || !periodEnd.After(periodStart) {
		return periodEnd
	}
	return periodStart.Add(periodEnd.Sub(periodStart) / 2)
}
