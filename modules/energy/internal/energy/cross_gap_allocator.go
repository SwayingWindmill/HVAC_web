package energy

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/libs/fixedpoint"
)

// CrossGapAllocationMethod identifies how an energy interval was attributed to hourly buckets.
type CrossGapAllocationMethod string

const (
	CrossGapMethodMeasured                CrossGapAllocationMethod = "MEASURED"
	CrossGapMethodLoadWeightedEstimated   CrossGapAllocationMethod = "LOAD_WEIGHTED_ESTIMATED"
	CrossGapMethodUniformEstimated        CrossGapAllocationMethod = "UNIFORM_ESTIMATED"
	CrossGapMethodUnallocatedExceedsLimit CrossGapAllocationMethod = "UNALLOCATED_EXCEEDS_LIMIT"

	// MaxCrossGapAllocationHours bounds how many hourly buckets a single cross-gap interval
	// may be spread across before being held back as unallocated.
	MaxCrossGapAllocationHours = 72

	CrossGapFallbackExceedsMaxGapHours = "EXCEEDS_MAX_GAP_HOURS"
	CrossGapFallbackNoLoadProfile      = "NO_LOAD_PROFILE"
)

// HourlyLoadWeight supplies an operating load weight (such as running cooling capacity or
// operating chiller/pump power) for a single UTC hour bucket.
type HourlyLoadWeight struct {
	HourStart time.Time
	Weight    float64
	Source    string
}

// CrossGapAllocationRequest describes a counter interval (PeriodStart, PeriodEnd] and an
// optional hourly load profile used to split multi-hour cross-gap energy into hourly buckets.
type CrossGapAllocationRequest struct {
	TenantID       string
	SiteID         string
	PointID        string
	EnergyType     string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	TotalEnergyKWh fixedpoint.Decimal6
	Location       *time.Location
	LoadProfile    []HourlyLoadWeight
	MaxGapHours    int
}

// AllocatedHourEnergy records one hourly bucket's attributed energy and audit metadata.
type AllocatedHourEnergy struct {
	HourStart   time.Time
	LocalDate   string
	EnergyKWh   fixedpoint.Decimal6
	WeightShare float64
	Quality     string
	Method      CrossGapAllocationMethod
	Estimated   bool
	AuditStart  time.Time
	AuditEnd    time.Time
}

// CrossGapAllocationResult summarizes the allocation outcome for one energy interval.
type CrossGapAllocationResult struct {
	Method         CrossGapAllocationMethod
	Estimated      bool
	FallbackReason string
	Allocations    []AllocatedHourEnergy
	UnallocatedKWh fixedpoint.Decimal6
}

type gapHourSlot struct {
	hourStart  time.Time
	auditStart time.Time
	auditEnd   time.Time
	overlap    time.Duration
	weight     float64
}

type quotaEntry struct {
	floorMicro int64
	rem        *big.Rat
	eligible   bool
}

// AllocateCrossGapEnergy attributes TotalEnergyKWh across hourly buckets under Option A
// (PeriodStart, PeriodEnd] boundary alignment.
//
// Intervals spanning at most 1 hour are never split and attribute 100% as MEASURED / VALID to
// the bucket containing PeriodEnd - 1ms. Multi-hour gaps spanning more than MaxGapHours touched
// UTC hour buckets return UNALLOCATED_EXCEEDS_LIMIT with UnallocatedKWh = TotalEnergyKWh.
// Otherwise energy is distributed proportionally to overlap * weight (or overlap uniformly when
// no positive load weight exists) using exact Decimal6 largest-remainder micro-unit rounding.
func AllocateCrossGapEnergy(req CrossGapAllocationRequest) (CrossGapAllocationResult, error) {
	if req.PeriodStart.IsZero() || req.PeriodEnd.IsZero() {
		return CrossGapAllocationResult{}, errors.New("cross-gap allocation period_start and period_end are required")
	}
	if !req.PeriodStart.Before(req.PeriodEnd) {
		return CrossGapAllocationResult{}, errors.New("cross-gap allocation period_start must be before period_end")
	}
	if req.TotalEnergyKWh < 0 {
		return CrossGapAllocationResult{}, errors.New("cross-gap allocation total_energy_kwh must be non-negative")
	}

	weightsByHour, err := indexHourlyLoadWeights(req.LoadProfile)
	if err != nil {
		return CrossGapAllocationResult{}, err
	}

	loc := req.Location
	if loc == nil {
		loc = time.UTC
	}
	maxGapHours := req.MaxGapHours
	if maxGapHours <= 0 {
		maxGapHours = MaxCrossGapAllocationHours
	}

	startUTC := req.PeriodStart.UTC()
	endUTC := req.PeriodEnd.UTC()
	if endUTC.Sub(startUTC) <= time.Hour {
		endAnchor := endUTC.Add(-time.Millisecond)
		hourUTC := endAnchor.Truncate(time.Hour)
		return CrossGapAllocationResult{
			Method:    CrossGapMethodMeasured,
			Estimated: false,
			Allocations: []AllocatedHourEnergy{{
				HourStart:   hourUTC,
				LocalDate:   endAnchor.In(loc).Format("2006-01-02"),
				EnergyKWh:   req.TotalEnergyKWh,
				WeightShare: 1.0,
				Quality:     FactQualityValid,
				Method:      CrossGapMethodMeasured,
				Estimated:   false,
				AuditStart:  startUTC,
				AuditEnd:    endUTC,
			}},
			UnallocatedKWh: fixedpoint.Zero,
		}, nil
	}

	slots, exceeded := collectOverlappingHourSlots(startUTC, endUTC, weightsByHour, maxGapHours)
	if exceeded {
		return CrossGapAllocationResult{
			Method:         CrossGapMethodUnallocatedExceedsLimit,
			Estimated:      false,
			FallbackReason: CrossGapFallbackExceedsMaxGapHours,
			UnallocatedKWh: req.TotalEnergyKWh,
		}, nil
	}

	method, fallbackReason, scoreRats, totalScoreRat := buildAllocationScores(slots)
	entries := distributeMicroUnitsLargestRemainder(req.TotalEnergyKWh.Int64Scaled(), scoreRats, totalScoreRat)

	allocations := make([]AllocatedHourEnergy, len(slots))
	for i, slot := range slots {
		shareFloat, _ := new(big.Rat).Quo(scoreRats[i], totalScoreRat).Float64()
		allocations[i] = AllocatedHourEnergy{
			HourStart:   slot.hourStart,
			LocalDate:   slot.hourStart.In(loc).Format("2006-01-02"),
			EnergyKWh:   fixedpoint.FromInt64Scaled(entries[i].floorMicro),
			WeightShare: shareFloat,
			Quality:     FactQualitySuspect,
			Method:      method,
			Estimated:   true,
			AuditStart:  slot.auditStart,
			AuditEnd:    slot.auditEnd,
		}
	}

	return CrossGapAllocationResult{
		Method:         method,
		Estimated:      true,
		FallbackReason: fallbackReason,
		Allocations:    allocations,
		UnallocatedKWh: fixedpoint.Zero,
	}, nil
}

func indexHourlyLoadWeights(profile []HourlyLoadWeight) (map[time.Time]float64, error) {
	weights := make(map[time.Time]float64, len(profile))
	for _, item := range profile {
		if math.IsNaN(item.Weight) || math.IsInf(item.Weight, 0) || item.Weight < 0 {
			return nil, fmt.Errorf("cross-gap allocation load weight must be finite and non-negative, got %v", item.Weight)
		}
		if item.HourStart.IsZero() {
			if item.Weight > 0 {
				return nil, errors.New("cross-gap allocation load weight hour_start is required")
			}
			continue
		}
		hourUTC := item.HourStart.UTC().Truncate(time.Hour)
		combined := weights[hourUTC] + item.Weight
		if math.IsInf(combined, 0) {
			return nil, errors.New("cross-gap allocation combined hourly load weight overflowed")
		}
		weights[hourUTC] = combined
	}
	return weights, nil
}

func collectOverlappingHourSlots(
	startUTC time.Time,
	endUTC time.Time,
	weightsByHour map[time.Time]float64,
	maxGapHours int,
) ([]gapHourSlot, bool) {
	slots := make([]gapHourSlot, 0, min(maxGapHours, MaxCrossGapAllocationHours))
	for h := startUTC.Truncate(time.Hour); h.Before(endUTC); h = h.Add(time.Hour) {
		slotEnd := h.Add(time.Hour)
		auditStart := h
		if startUTC.After(auditStart) {
			auditStart = startUTC
		}
		auditEnd := slotEnd
		if endUTC.Before(auditEnd) {
			auditEnd = endUTC
		}
		overlap := auditEnd.Sub(auditStart)
		if overlap <= 0 {
			continue
		}
		if len(slots) >= maxGapHours {
			return nil, true
		}
		slots = append(slots, gapHourSlot{
			hourStart:  h,
			auditStart: auditStart,
			auditEnd:   auditEnd,
			overlap:    overlap,
			weight:     weightsByHour[h],
		})
	}
	return slots, false
}

func buildAllocationScores(slots []gapHourSlot) (CrossGapAllocationMethod, string, []*big.Rat, *big.Rat) {
	hasPositiveWeight := false
	for _, slot := range slots {
		if slot.weight > 0 {
			hasPositiveWeight = true
			break
		}
	}

	method := CrossGapMethodLoadWeightedEstimated
	fallbackReason := ""
	if !hasPositiveWeight {
		method = CrossGapMethodUniformEstimated
		fallbackReason = CrossGapFallbackNoLoadProfile
	}

	const hourNanos = int64(time.Hour)
	scoreRats := make([]*big.Rat, len(slots))
	totalScoreRat := new(big.Rat)
	for i, slot := range slots {
		overlapRat := big.NewRat(int64(slot.overlap), hourNanos)
		var scoreRat *big.Rat
		if hasPositiveWeight {
			if slot.weight > 0 {
				weightRat := new(big.Rat).SetFloat64(slot.weight)
				scoreRat = new(big.Rat).Mul(overlapRat, weightRat)
			} else {
				scoreRat = new(big.Rat)
			}
		} else {
			scoreRat = overlapRat
		}
		scoreRats[i] = scoreRat
		totalScoreRat.Add(totalScoreRat, scoreRat)
	}
	return method, fallbackReason, scoreRats, totalScoreRat
}

func distributeMicroUnitsLargestRemainder(totalMicro int64, scoreRats []*big.Rat, totalScoreRat *big.Rat) []quotaEntry {
	entries := make([]quotaEntry, len(scoreRats))
	if totalMicro == 0 {
		return entries
	}

	totalMicroRat := new(big.Rat).SetInt64(totalMicro)
	var allocatedFloorSum int64
	for i, scoreRat := range scoreRats {
		if scoreRat.Sign() == 0 {
			entries[i] = quotaEntry{rem: new(big.Rat)}
			continue
		}
		shareRat := new(big.Rat).Quo(scoreRat, totalScoreRat)
		quotaRat := new(big.Rat).Mul(totalMicroRat, shareRat)
		quot, mod := new(big.Int).QuoRem(quotaRat.Num(), quotaRat.Denom(), new(big.Int))
		floorVal := quot.Int64()
		entries[i] = quotaEntry{
			floorMicro: floorVal,
			rem:        new(big.Rat).SetFrac(mod, quotaRat.Denom()),
			eligible:   true,
		}
		allocatedFloorSum += floorVal
	}

	leftover := totalMicro - allocatedFloorSum
	if leftover > 0 {
		ranked := make([]int, 0, len(entries))
		for i, entry := range entries {
			if entry.eligible {
				ranked = append(ranked, i)
			}
		}
		sort.SliceStable(ranked, func(a, b int) bool {
			ia, ib := ranked[a], ranked[b]
			cmp := entries[ia].rem.Cmp(entries[ib].rem)
			if cmp != 0 {
				return cmp > 0
			}
			return ia < ib
		})
		for k := int64(0); k < leftover && k < int64(len(ranked)); k++ {
			entries[ranked[k]].floorMicro++
		}
	}
	return entries
}
