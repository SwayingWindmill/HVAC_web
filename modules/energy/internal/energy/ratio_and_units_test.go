package energy

import (
	"math"
	"testing"
	"time"
)

func TestUnitToKWhConversions(t *testing.T) {
	cases := []struct {
		name string
		val  float64
		unit string
		want float64
	}{
		{name: "empty defaults to kWh", val: 12.5, unit: "", want: 12.5},
		{name: "Wh", val: 2500, unit: "Wh", want: 2.5},
		{name: "kWh", val: 42.25, unit: "kWh", want: 42.25},
		{name: "MWh", val: 1.5, unit: "MWh", want: 1500},
		{name: "GJ", val: 3.6, unit: "GJ", want: 1000},
		{name: "MJ", val: 3.6, unit: "MJ", want: 1},
		{name: "RTh", val: 10, unit: "RTh", want: 35.168528420667},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConvertEnergyToKWh(tc.val, tc.unit)
			if err != nil {
				t.Fatalf("ConvertEnergyToKWh(%v, %q) unexpected error: %v", tc.val, tc.unit, err)
			}
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("ConvertEnergyToKWh(%v, %q) = %.12f, want %.12f", tc.val, tc.unit, got, tc.want)
			}
		})
	}

	if _, err := ConvertEnergyToKWh(10, "kW"); err == nil {
		t.Fatal("expected unsupported power unit kW to return error")
	}
}

func TestEffectiveMultiplierAtHistoricalBoundary(t *testing.T) {
	janStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	febStart := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	versions := []RatioVersion{
		{
			ID:                   "ratio-v1-jan",
			CTRatio:              50,
			PTRatio:              1,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModePlatform,
			ReadingUnit:          UnitKWh,
			EffectiveFrom:        janStart,
			EffectiveTo:          &febStart,
			Status:               RatioStatusReleased,
		},
		{
			ID:                   "ratio-v2-feb",
			CTRatio:              100,
			PTRatio:              10,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModePlatform,
			ReadingUnit:          UnitWh,
			EffectiveFrom:        febStart,
			EffectiveTo:          nil,
			Status:               RatioStatusReleased,
		},
		{
			ID:                   "ratio-draft",
			CTRatio:              999,
			PTRatio:              1,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModePlatform,
			ReadingUnit:          UnitKWh,
			EffectiveFrom:        febStart,
			Status:               RatioStatusDraft,
		},
	}

	// January interval ending right at febStart uses midpoint -> resolves to Jan v1 (never retro-mutated by Feb v2).
	janLookup := IntervalRatioLookupTime(febStart.Add(-5*time.Minute), febStart)
	janResolved, err := EffectiveMultiplierAt(versions, UnitKWh, janLookup)
	if err != nil {
		t.Fatalf("resolve Jan multiplier: %v", err)
	}
	if !janResolved.VersionApplied || janResolved.RatioVersionID != "ratio-v1-jan" || math.Abs(janResolved.Multiplier-50) > 1e-9 {
		t.Fatalf("unexpected Jan resolution: %+v", janResolved)
	}

	// February interval [febStart, febStart+5m) uses Feb v2: 100 * 10 * 1 * 0.001 (Wh->kWh) = 1.0.
	febLookup := IntervalRatioLookupTime(febStart, febStart.Add(5*time.Minute))
	febResolved, err := EffectiveMultiplierAt(versions, UnitKWh, febLookup)
	if err != nil {
		t.Fatalf("resolve Feb multiplier: %v", err)
	}
	if !febResolved.VersionApplied || febResolved.RatioVersionID != "ratio-v2-feb" || math.Abs(febResolved.Multiplier-1.0) > 1e-9 || febResolved.Unit != UnitWh {
		t.Fatalf("unexpected Feb resolution: %+v", febResolved)
	}

	// Pre-history timestamp falls back cleanly to default multiplier = 1.
	preResolved, err := EffectiveMultiplierAt(versions, UnitKWh, janStart.Add(-time.Hour))
	if err != nil {
		t.Fatalf("resolve pre-history multiplier: %v", err)
	}
	if preResolved.VersionApplied || math.Abs(preResolved.Multiplier-1.0) > 1e-9 {
		t.Fatalf("expected fallback multiplier 1.0, got %+v", preResolved)
	}
}

func TestEffectiveMultiplierDeviceAppliedAndOverlapRejection(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	deviceApplied := []RatioVersion{
		{
			ID:                   "device-applied-rth",
			CTRatio:              40,
			PTRatio:              1,
			MeterMultiplier:      1,
			RatioApplicationMode: RatioAppModeDevice,
			ReadingUnit:          UnitRTh,
			EffectiveFrom:        t0,
			Status:               RatioStatusReleased,
		},
	}
	res, err := EffectiveMultiplierAt(deviceApplied, "", t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("resolve device-applied RTh: %v", err)
	}
	if math.Abs(res.Multiplier-3.5168528420667) > 1e-9 {
		t.Fatalf("expected unit-only factor 3.5168528420667 for DEVICE_APPLIED, got %v", res.Multiplier)
	}

	overlapping := []RatioVersion{
		{ID: "a", CTRatio: 10, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: UnitKWh, EffectiveFrom: t0, Status: RatioStatusReleased},
		{ID: "b", CTRatio: 20, PTRatio: 1, MeterMultiplier: 1, ReadingUnit: UnitKWh, EffectiveFrom: t0, Status: RatioStatusReleased},
	}
	if _, err := EffectiveMultiplierAt(overlapping, "", t0.Add(time.Minute)); err == nil {
		t.Fatal("expected overlapping released ratio versions to error")
	}
}
