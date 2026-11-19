package energy

import (
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/fixedpoint"
)

func TestAllocateCrossGapEnergyMeasuredWithinOneHourOptionA(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}

	// (23:00 CST, 00:00 CST] = (15:00Z, 16:00Z]: Option A attributes to 23:00 CST (Sep 30), not Oct 1.
	start := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	total := fixedpoint.MustFromFloat64(18.75)

	res, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		TenantID:       testTenantID,
		SiteID:         testSiteID,
		PointID:        testPointID,
		EnergyType:     EnergyTypeElectricity,
		PeriodStart:    start,
		PeriodEnd:      end,
		TotalEnergyKWh: total,
		Location:       shanghai,
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy failed: %v", err)
	}
	if res.Method != CrossGapMethodMeasured || res.Estimated || res.FallbackReason != "" || res.UnallocatedKWh != fixedpoint.Zero {
		t.Fatalf("unexpected result header: %+v", res)
	}
	if len(res.Allocations) != 1 {
		t.Fatalf("expected 1 allocation, got %d", len(res.Allocations))
	}
	got := res.Allocations[0]
	wantHour := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	if !got.HourStart.Equal(wantHour) || got.LocalDate != "2026-09-30" {
		t.Fatalf("bucket=%s localDate=%s, want hour=%s localDate=2026-09-30", got.HourStart, got.LocalDate, wantHour)
	}
	if got.EnergyKWh != total || got.WeightShare != 1.0 || got.Quality != FactQualityValid || got.Method != CrossGapMethodMeasured || got.Estimated {
		t.Fatalf("unexpected allocation entry: %+v", got)
	}

	// Interval <= 1h straddling an hour boundary (00:45 -> 01:15) must NOT split and belongs to 01:00.
	straddleStart := time.Date(2026, 10, 1, 0, 45, 0, 0, time.UTC)
	straddleEnd := time.Date(2026, 10, 1, 1, 15, 0, 0, time.UTC)
	res2, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    straddleStart,
		PeriodEnd:      straddleEnd,
		TotalEnergyKWh: fixedpoint.MustFromFloat64(4.5),
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy straddle failed: %v", err)
	}
	if len(res2.Allocations) != 1 {
		t.Fatalf("expected unsplit 1 allocation, got %d", len(res2.Allocations))
	}
	if !res2.Allocations[0].HourStart.Equal(time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)) || res2.Allocations[0].LocalDate != "2026-10-01" {
		t.Fatalf("straddle bucket = %+v", res2.Allocations[0])
	}
}

func TestAllocateCrossGapEnergyLoadWeightedExactMicroConservation(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}

	// 4 hours spanning local midnight in Asia/Shanghai (22:00 CST -> 02:00 CST next day).
	h0 := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC) // 22:00 CST
	h1 := h0.Add(time.Hour)                             // 23:00 CST
	h2 := h0.Add(2 * time.Hour)                         // 00:00 CST (next day)
	h3 := h0.Add(3 * time.Hour)                         // 01:00 CST (next day)

	total, err := fixedpoint.Parse("100.000001")
	if err != nil {
		t.Fatalf("parse total: %v", err)
	}

	res, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		TenantID:       testTenantID,
		SiteID:         testSiteID,
		PointID:        testPointID,
		EnergyType:     EnergyTypeElectricity,
		PeriodStart:    h0,
		PeriodEnd:      h0.Add(4 * time.Hour),
		TotalEnergyKWh: total,
		Location:       shanghai,
		LoadProfile: []HourlyLoadWeight{
			{HourStart: h0, Weight: 100, Source: "cooling_load"},
			{HourStart: h1, Weight: 200, Source: "cooling_load"},
			{HourStart: h2, Weight: 400, Source: "cooling_load"},
			{HourStart: h3, Weight: 300, Source: "cooling_load"},
		},
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy failed: %v", err)
	}
	if res.Method != CrossGapMethodLoadWeightedEstimated || !res.Estimated || res.FallbackReason != "" || res.UnallocatedKWh != fixedpoint.Zero {
		t.Fatalf("unexpected result header: %+v", res)
	}
	if len(res.Allocations) != 4 {
		t.Fatalf("expected 4 allocations, got %d", len(res.Allocations))
	}

	// Total weight = 1000; exact quotas for 100.000001:
	// h0: 10.0000001 (rem 0.1), h1: 20.0000002 (rem 0.2), h2: 40.0000004 (rem 0.4 -> gets +1e-6), h3: 30.0000003 (rem 0.3).
	wantEnergies := []string{"10", "20", "40.000001", "30"}
	wantDates := []string{"2026-10-01", "2026-10-01", "2026-10-02", "2026-10-02"}
	var sum fixedpoint.Decimal6
	for i, alloc := range res.Allocations {
		if alloc.EnergyKWh.String() != wantEnergies[i] {
			t.Fatalf("hour %d energy = %s, want %s", i, alloc.EnergyKWh.String(), wantEnergies[i])
		}
		if alloc.LocalDate != wantDates[i] {
			t.Fatalf("hour %d localDate = %s, want %s", i, alloc.LocalDate, wantDates[i])
		}
		if alloc.Quality != FactQualitySuspect || alloc.Method != CrossGapMethodLoadWeightedEstimated || !alloc.Estimated {
			t.Fatalf("hour %d unexpected metadata: %+v", i, alloc)
		}
		sum, err = sum.Add(alloc.EnergyKWh)
		if err != nil {
			t.Fatalf("sum add: %v", err)
		}
	}
	if sum != total {
		t.Fatalf("allocated sum = %s, want exact %s (micro diff=%d)", sum, total, total.Int64Scaled()-sum.Int64Scaled())
	}
	if res.Allocations[2].EnergyKWh <= res.Allocations[3].EnergyKWh ||
		res.Allocations[3].EnergyKWh <= res.Allocations[1].EnergyKWh ||
		res.Allocations[1].EnergyKWh <= res.Allocations[0].EnergyKWh {
		t.Fatalf("expected heavier hours to receive strictly greater energy: %+v", res.Allocations)
	}
}

func TestAllocateCrossGapEnergyPartialOverlapAndZeroWeightExclusion(t *testing.T) {
	base := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	// (00:30, 03:30] touches 4 hours with overlaps 0.5h, 1.0h, 1.0h, 0.5h.
	// Hour 2 has 0 load weight (plant off) and must receive 0 kWh even when remainder is distributed.
	total := fixedpoint.FromInt64Scaled(10_000_001) // 10.000001 kWh
	res, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    base.Add(30 * time.Minute),
		PeriodEnd:      base.Add(3*time.Hour + 30*time.Minute),
		TotalEnergyKWh: total,
		LoadProfile: []HourlyLoadWeight{
			{HourStart: base, Weight: 200},                    // score = 0.5 * 200 = 100
			{HourStart: base.Add(time.Hour), Weight: 100},     // score = 1.0 * 100 = 100
			{HourStart: base.Add(2 * time.Hour), Weight: 0},   // score = 0
			{HourStart: base.Add(3 * time.Hour), Weight: 200}, // score = 0.5 * 200 = 100
		},
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy failed: %v", err)
	}
	if len(res.Allocations) != 4 {
		t.Fatalf("expected 4 allocations, got %d", len(res.Allocations))
	}
	if res.Allocations[2].EnergyKWh != fixedpoint.Zero || res.Allocations[2].WeightShare != 0 {
		t.Fatalf("idle hour must get 0 kWh and 0 share, got %+v", res.Allocations[2])
	}
	if res.Allocations[0].EnergyKWh.String() != "3.333334" ||
		res.Allocations[1].EnergyKWh.String() != "3.333334" ||
		res.Allocations[3].EnergyKWh.String() != "3.333333" {
		t.Fatalf("unexpected partial-overlap distribution: %+v", res.Allocations)
	}
	if !res.Allocations[0].AuditStart.Equal(base.Add(30*time.Minute)) || !res.Allocations[0].AuditEnd.Equal(base.Add(time.Hour)) {
		t.Fatalf("unexpected audit bounds for slot 0: %+v", res.Allocations[0])
	}
	if !res.Allocations[3].AuditStart.Equal(base.Add(3*time.Hour)) || !res.Allocations[3].AuditEnd.Equal(base.Add(3*time.Hour+30*time.Minute)) {
		t.Fatalf("unexpected audit bounds for slot 3: %+v", res.Allocations[3])
	}
}

func TestAllocateCrossGapEnergyFallbackToUniformWhenNoLoadProfile(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	total := fixedpoint.FromInt64Scaled(10_000_000) // 10 kWh across 3 hours

	res, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    base,
		PeriodEnd:      base.Add(3 * time.Hour),
		TotalEnergyKWh: total,
		LoadProfile: []HourlyLoadWeight{
			// Non-overlapping hour and zero-weight hour should trigger uniform fallback.
			{HourStart: base.Add(-time.Hour), Weight: 500},
			{HourStart: base, Weight: 0},
		},
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy failed: %v", err)
	}
	if res.Method != CrossGapMethodUniformEstimated || !res.Estimated || res.FallbackReason != CrossGapFallbackNoLoadProfile {
		t.Fatalf("expected uniform fallback, got %+v", res)
	}
	if len(res.Allocations) != 3 {
		t.Fatalf("expected 3 allocations, got %d", len(res.Allocations))
	}
	// 10 / 3 = 3.333333... -> deterministic tie-break assigns +1 micro to earliest hour.
	if res.Allocations[0].EnergyKWh.String() != "3.333334" ||
		res.Allocations[1].EnergyKWh.String() != "3.333333" ||
		res.Allocations[2].EnergyKWh.String() != "3.333333" {
		t.Fatalf("unexpected uniform allocations: %+v", res.Allocations)
	}
	for _, alloc := range res.Allocations {
		if alloc.Quality != FactQualitySuspect || alloc.Method != CrossGapMethodUniformEstimated || !alloc.Estimated {
			t.Fatalf("unexpected allocation flags: %+v", alloc)
		}
	}
}

func TestAllocateCrossGapEnergyDSTLocalDateAssignment(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}

	// Fall-back day 2026-11-01: 04:00Z = 00:00 EDT (Nov 1), 05:00Z = 01:00 EDT, 06:00Z = 01:00 EST, 07:00Z = 02:00 EST.
	// Start at 03:00Z (2026-10-31 23:00 EDT) across midnight into 08:00Z.
	start := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	res, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    start,
		PeriodEnd:      end,
		TotalEnergyKWh: fixedpoint.MustFromFloat64(50),
		Location:       ny,
	})
	if err != nil {
		t.Fatalf("AllocateCrossGapEnergy DST failed: %v", err)
	}
	if len(res.Allocations) != 5 {
		t.Fatalf("expected 5 UTC hour slots across DST fall-back, got %d", len(res.Allocations))
	}
	wantDates := []string{"2026-10-31", "2026-11-01", "2026-11-01", "2026-11-01", "2026-11-01"}
	for i, alloc := range res.Allocations {
		if alloc.LocalDate != wantDates[i] {
			t.Fatalf("slot %d (%s) localDate = %s, want %s", i, alloc.HourStart, alloc.LocalDate, wantDates[i])
		}
		if alloc.EnergyKWh.String() != "10" {
			t.Fatalf("slot %d energy = %s, want 10", i, alloc.EnergyKWh)
		}
	}
}

func TestAllocateCrossGapEnergyGapLimitGuard(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	total := fixedpoint.MustFromFloat64(500.25)

	// Exactly 72 hours is allowed.
	okRes, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    start,
		PeriodEnd:      start.Add(72 * time.Hour),
		TotalEnergyKWh: total,
	})
	if err != nil {
		t.Fatalf("72h gap failed: %v", err)
	}
	if okRes.Method != CrossGapMethodUniformEstimated || len(okRes.Allocations) != 72 || okRes.UnallocatedKWh != fixedpoint.Zero {
		t.Fatalf("unexpected 72h result: method=%s count=%d unallocated=%s", okRes.Method, len(okRes.Allocations), okRes.UnallocatedKWh)
	}

	// 73 hours exceeds default 72h limit.
	exceededRes, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    start,
		PeriodEnd:      start.Add(73 * time.Hour),
		TotalEnergyKWh: total,
	})
	if err != nil {
		t.Fatalf("73h gap failed: %v", err)
	}
	if exceededRes.Method != CrossGapMethodUnallocatedExceedsLimit ||
		exceededRes.Estimated ||
		exceededRes.FallbackReason != CrossGapFallbackExceedsMaxGapHours ||
		len(exceededRes.Allocations) != 0 ||
		exceededRes.UnallocatedKWh != total {
		t.Fatalf("unexpected exceeded result: %+v", exceededRes)
	}

	// Custom MaxGapHours limit is honored.
	customExceeded, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
		PeriodStart:    start,
		PeriodEnd:      start.Add(5 * time.Hour),
		TotalEnergyKWh: total,
		MaxGapHours:    4,
	})
	if err != nil {
		t.Fatalf("custom MaxGapHours failed: %v", err)
	}
	if customExceeded.Method != CrossGapMethodUnallocatedExceedsLimit || customExceeded.UnallocatedKWh != total {
		t.Fatalf("unexpected custom exceeded result: %+v", customExceeded)
	}
}

func TestAllocateCrossGapEnergyValidationErrors(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := []CrossGapAllocationRequest{
		{PeriodEnd: base, TotalEnergyKWh: fixedpoint.Zero},
		{PeriodStart: base, PeriodEnd: base, TotalEnergyKWh: fixedpoint.Zero},
		{PeriodStart: base.Add(time.Hour), PeriodEnd: base, TotalEnergyKWh: fixedpoint.Zero},
		{PeriodStart: base, PeriodEnd: base.Add(2 * time.Hour), TotalEnergyKWh: fixedpoint.FromInt64Scaled(-1)},
		{PeriodStart: base, PeriodEnd: base.Add(2 * time.Hour), LoadProfile: []HourlyLoadWeight{{HourStart: base, Weight: -0.1}}},
		{PeriodStart: base, PeriodEnd: base.Add(2 * time.Hour), LoadProfile: []HourlyLoadWeight{{HourStart: base, Weight: math.NaN()}}},
		{PeriodStart: base, PeriodEnd: base.Add(2 * time.Hour), LoadProfile: []HourlyLoadWeight{{HourStart: base, Weight: math.Inf(1)}}},
	}
	for i, tc := range cases {
		if _, err := AllocateCrossGapEnergy(tc); err == nil {
			t.Fatalf("case %d: expected validation error, got nil", i)
		}
	}
}
