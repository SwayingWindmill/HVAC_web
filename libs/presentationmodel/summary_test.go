package presentationmodel

import (
	"testing"
	"time"
)

func TestSiteLocalDayBoundsUsesCalendarDayAcrossDST(t *testing.T) {
	asOf := time.Date(2026, time.March, 8, 18, 0, 0, 0, time.UTC)
	from, to, err := SiteLocalDayBounds(asOf, "America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := from.UTC().Format(time.RFC3339), "2026-03-08T08:00:00Z"; got != want {
		t.Fatalf("from = %s, want %s", got, want)
	}
	if got, want := to.UTC().Format(time.RFC3339), "2026-03-09T07:00:00Z"; got != want {
		t.Fatalf("to = %s, want %s", got, want)
	}
	if got, want := to.Sub(from), 23*time.Hour; got != want {
		t.Fatalf("duration = %s, want %s", got, want)
	}
}

func TestProjectDevicePopulationPartialNeverPublishesSiteRatio(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	population := ProjectDevicePopulation(2, false, []DevicePopulationObservation{
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "ONLINE", DisplayState: "ONLINE", EvaluatedAt: now},
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "OFFLINE", DisplayState: "OFFLINE", EvaluatedAt: now},
	})
	if population.State != StatePartial {
		t.Fatalf("state = %s, want %s", population.State, StatePartial)
	}
	if population.Denominator != nil || population.AvailabilityPercent != nil {
		t.Fatalf("partial population published site ratio: denominator=%v availability=%v", population.Denominator, population.AvailabilityPercent)
	}
}

func TestProjectDevicePopulationEmptyIsNoDataNotHealthy(t *testing.T) {
	population := ProjectDevicePopulation(0, true, nil)
	if population.State != StateNoData {
		t.Fatalf("state = %s, want %s", population.State, StateNoData)
	}
	if population.Denominator != nil || population.AvailabilityPercent != nil {
		t.Fatalf("empty population published a ratio")
	}
}

func TestProjectDevicePopulationCompleteKnownPresencePublishesExplicitDenominator(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	population := ProjectDevicePopulation(3, true, []DevicePopulationObservation{
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "ONLINE", DisplayState: "ONLINE", EvaluatedAt: now},
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "OFFLINE", DisplayState: "OFFLINE", EvaluatedAt: now.Add(-time.Minute)},
		{Applicability: "NOT_APPLICABLE", Availability: "AVAILABLE", Presence: "", DisplayState: "", EvaluatedAt: now.Add(-2 * time.Minute)},
	})
	if population.State != StateReady {
		t.Fatalf("state = %s, want %s", population.State, StateReady)
	}
	if population.Denominator == nil || *population.Denominator != 2 {
		t.Fatalf("denominator = %v, want 2", population.Denominator)
	}
	if population.AvailabilityPercent == nil || *population.AvailabilityPercent != 50 {
		t.Fatalf("availability = %v, want 50", population.AvailabilityPercent)
	}
	if population.DenominatorPolicy != DenominatorApplicableKnownPresence {
		t.Fatalf("policy = %s", population.DenominatorPolicy)
	}
	if population.EvaluatedAt == nil || !population.EvaluatedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("evaluatedAt = %v, want oldest applicable observation", population.EvaluatedAt)
	}
}

func TestWorstStateDoesNotHideUnavailableOrUnintegratedDataBehindNoData(t *testing.T) {
	if got := WorstState(StateNoData, StateNotIntegrated, StateReady); got != StateNotIntegrated {
		t.Fatalf("worst state = %s, want %s", got, StateNotIntegrated)
	}
	if got := WorstState(StateNoData, StateUnavailable, StatePartial); got != StateUnavailable {
		t.Fatalf("worst state = %s, want %s", got, StateUnavailable)
	}
}

func TestProjectDevicePopulationUnknownPresenceSuppressesRatio(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	population := ProjectDevicePopulation(2, true, []DevicePopulationObservation{
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "ONLINE", DisplayState: "ONLINE", EvaluatedAt: now},
		{Applicability: "APPLICABLE", Availability: "AVAILABLE", Presence: "UNKNOWN", DisplayState: "UNKNOWN", EvaluatedAt: now},
	})
	if population.State != StatePartial {
		t.Fatalf("state = %s, want %s", population.State, StatePartial)
	}
	if population.AvailabilityPercent != nil {
		t.Fatalf("unknown presence published availability %v", population.AvailabilityPercent)
	}
}
