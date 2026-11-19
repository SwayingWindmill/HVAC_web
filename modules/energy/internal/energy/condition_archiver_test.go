package energy

import (
	"math"
	"testing"
	"time"
)

func TestBuildSiteConditionAnchorsAggregatesAndSortsDeterministically(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}

	tenantA := "018f4d00-0000-7000-8000-000000000001"
	tenantB := "018f4d00-0000-7000-8000-000000000002"
	site1 := "018f4e00-1000-7000-8000-000000000001"
	site2 := "018f4e00-1000-7000-8000-000000000002"

	h10 := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	h11 := time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)
	updatedEarly := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	updatedLate := time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC)

	inputs := []ConditionSampleInput{
		// Out-of-order rows across tenants/sites/hours + uppercase UUID normalization.
		{
			TenantID:          "018F4D00-0000-7000-8000-000000000002",
			SiteID:            site1,
			Hour:              h10,
			OutdoorDryBulbC:   float64Pointer(26.0),
			ClosedLoopTicks:   1,
			TotalControlTicks: 2,
			CalcVersion:       3,
			UpdatedAt:         updatedEarly,
		},
		{
			TenantID:          tenantA,
			SiteID:            site2,
			Hour:              h11,
			OutdoorWetBulbC:   float64Pointer(21.0),
			OutdoorDryBulbC:   float64Pointer(28.0),
			CoolingKWh:        float64Pointer(600),
			RatedCapacityKW:   float64Pointer(1000),
			ClosedLoopTicks:   3,
			TotalControlTicks: 4,
			CalcVersion:       2,
		},
		// Two samples in the same UTC hour (one given in Asia/Shanghai +08:00 with minutes offset).
		{
			TenantID:          tenantA,
			SiteID:            site1,
			Hour:              h10.In(shanghai).Add(15 * time.Minute),
			OutdoorWetBulbC:   float64Pointer(22.0),
			OutdoorDryBulbC:   float64Pointer(30.0),
			CoolingKWh:        float64Pointer(400),
			RatedCapacityKW:   float64Pointer(1000),
			ClosedLoopTicks:   2,
			TotalControlTicks: 10,
			CalcVersion:       5,
			UpdatedAt:         updatedEarly,
		},
		{
			TenantID:          tenantA,
			SiteID:            site1,
			Hour:              h10.Add(45 * time.Minute),
			OutdoorWetBulbC:   float64Pointer(24.0),
			OutdoorDryBulbC:   float64Pointer(32.0),
			CoolingKWh:        float64Pointer(800),
			RatedCapacityKW:   float64Pointer(1000),
			ClosedLoopTicks:   6,
			TotalControlTicks: 10,
			CalcVersion:       7,
			UpdatedAt:         updatedLate,
		},
		// Invalid inputs must be ignored without panicking or emitting rows.
		{TenantID: "not-a-uuid", SiteID: site1, Hour: h10},
		{TenantID: tenantA, SiteID: "", Hour: h10},
		{TenantID: tenantA, SiteID: site1, Hour: time.Time{}},
	}

	anchors := BuildSiteConditionAnchors(inputs)
	if len(anchors) != 3 {
		t.Fatalf("expected 3 anchors, got %d: %+v", len(anchors), anchors)
	}

	// Verify deterministic ordering: (tenantA, site1, h10), (tenantA, site2, h11), (tenantB, site1, h10).
	if anchors[0].TenantID != tenantA || anchors[0].SiteID != site1 || !anchors[0].Hour.Equal(h10) ||
		anchors[1].TenantID != tenantA || anchors[1].SiteID != site2 || !anchors[1].Hour.Equal(h11) ||
		anchors[2].TenantID != tenantB || anchors[2].SiteID != site1 || !anchors[2].Hour.Equal(h10) {
		t.Fatalf("unexpected deterministic order: %+v", anchors)
	}

	a0 := anchors[0]
	if a0.SampleCount != 2 || a0.CalcVersion != 7 || !a0.UpdatedAt.Equal(updatedLate) {
		t.Fatalf("unexpected metadata on merged anchor: %+v", a0)
	}
	assertFloatPtrClose(t, "OutdoorWetBulbC", a0.OutdoorWetBulbC, 23.0)
	assertFloatPtrClose(t, "OutdoorDryBulbC", a0.OutdoorDryBulbC, 31.0)
	assertFloatPtrClose(t, "PlantLoadRatio", a0.PlantLoadRatio, 0.6)
	assertFloatPtrClose(t, "AIClosedLoopShare", a0.AIClosedLoopShare, 0.4) // (2+6)/(10+10) = 0.4
}

func TestBuildSiteConditionAnchorsFiltersOutliersNaNAndPsychrometricInconsistencies(t *testing.T) {
	tenant := "018f4d00-0000-7000-8000-000000000001"
	site := "018f4e00-1000-7000-8000-000000000001"
	hour := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)

	inputs := []ConditionSampleInput{
		// Out-of-bounds temperatures and NaN/Inf.
		{
			TenantID:        tenant,
			SiteID:          site,
			Hour:            hour,
			OutdoorWetBulbC: float64Pointer(-40.1),
			OutdoorDryBulbC: float64Pointer(60.1),
			CoolingKWh:      float64Pointer(math.NaN()),
			RatedCapacityKW: float64Pointer(1000),
		},
		{
			TenantID:        tenant,
			SiteID:          site,
			Hour:            hour,
			OutdoorWetBulbC: float64Pointer(math.Inf(1)),
			OutdoorDryBulbC: float64Pointer(math.Inf(-1)),
			CoolingKWh:      float64Pointer(-10),
			RatedCapacityKW: float64Pointer(1000),
		},
		// Psychrometric contradiction: dry-bulb 20 C < wet-bulb 25 C - 0.5 -> both discarded.
		{
			TenantID:        tenant,
			SiteID:          site,
			Hour:            hour,
			OutdoorWetBulbC: float64Pointer(25.0),
			OutdoorDryBulbC: float64Pointer(20.0),
		},
		// Valid readings within bounds and 0.5 C psychrometric tolerance (24.7 >= 25.0 - 0.5).
		{
			TenantID:        tenant,
			SiteID:          site,
			Hour:            hour,
			OutdoorWetBulbC: float64Pointer(25.0),
			OutdoorDryBulbC: float64Pointer(24.7),
		},
	}

	anchors := BuildSiteConditionAnchors(inputs)
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d", len(anchors))
	}
	got := anchors[0]
	assertFloatPtrClose(t, "OutdoorWetBulbC", got.OutdoorWetBulbC, 25.0)
	assertFloatPtrClose(t, "OutdoorDryBulbC", got.OutdoorDryBulbC, 24.7)
	if got.PlantLoadRatio != nil {
		t.Fatalf("expected nil PlantLoadRatio when all PLR samples invalid, got %v", *got.PlantLoadRatio)
	}
	if got.AIClosedLoopShare != nil {
		t.Fatalf("expected nil AIClosedLoopShare when totalControlTicks == 0, got %v", *got.AIClosedLoopShare)
	}
	if got.SampleCount != 4 {
		t.Fatalf("expected SampleCount=4, got %d", got.SampleCount)
	}
}

func TestBuildSiteConditionAnchorsClampsPLRAndAIClosedLoopShare(t *testing.T) {
	tenant := "018f4d00-0000-7000-8000-000000000001"
	site := "018f4e00-1000-7000-8000-000000000001"
	hour := time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)

	anchors := BuildSiteConditionAnchors([]ConditionSampleInput{
		{
			TenantID:          tenant,
			SiteID:            site,
			Hour:              hour,
			CoolingKWh:        float64Pointer(2000),
			RatedCapacityKW:   float64Pointer(1000), // 2.0 -> clamped to 1.5
			ClosedLoopTicks:   15,
			TotalControlTicks: 10, // 1.5 -> clamped to 1.0
		},
		{
			TenantID:          tenant,
			SiteID:            site,
			Hour:              hour.Add(time.Hour),
			CoolingKWh:        float64Pointer(100),
			RatedCapacityKW:   float64Pointer(0), // invalid ratedCapacityKW <= 0 -> ignored
			ClosedLoopTicks:   -5,                // negative closed loop -> clamped to 0
			TotalControlTicks: 10,
		},
	})

	if len(anchors) != 2 {
		t.Fatalf("expected 2 anchors, got %d", len(anchors))
	}
	assertFloatPtrClose(t, "PLR clamped", anchors[0].PlantLoadRatio, 1.5)
	assertFloatPtrClose(t, "AI share clamped high", anchors[0].AIClosedLoopShare, 1.0)
	if anchors[1].PlantLoadRatio != nil {
		t.Fatalf("expected nil PLR when ratedCapacityKW == 0, got %v", *anchors[1].PlantLoadRatio)
	}
	assertFloatPtrClose(t, "AI share clamped low", anchors[1].AIClosedLoopShare, 0.0)
}

func assertFloatPtrClose(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", label, *got, want)
	}
}
