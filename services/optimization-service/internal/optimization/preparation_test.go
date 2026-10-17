package optimization

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type preparationStoreStub struct {
	definition PreparationDefinition
	created    []PreparedInput
	result     PreparedOptimization
	err        error
}

func (store *preparationStoreStub) ResolveOptimizationPreparation(context.Context, PreparationRequest, time.Time) (PreparationDefinition, error) {
	if store.err != nil {
		return PreparationDefinition{}, store.err
	}
	return store.definition, nil
}

func (store *preparationStoreStub) CreatePreparedOptimization(_ context.Context, input PreparedInput, _ time.Time) (PreparedOptimization, error) {
	store.created = append(store.created, input)
	return store.result, store.err
}

type authoritativeInputStub struct {
	state AuthoritativeState
	err   error
}

func (source *authoritativeInputStub) ReadOptimizationState(context.Context, OptimizationStateQuery) (AuthoritativeState, error) {
	if source.err != nil {
		return AuthoritativeState{}, source.err
	}
	return source.state, nil
}

func testPreparationDefinition() PreparationDefinition {
	return PreparationDefinition{
		PolicyVersionID:        "01990000-1920-7000-8000-000000000001",
		TopologyVersionID:      "01990000-1300-7000-8000-000000000001",
		LoadForecastSnapshotID: "01990000-1890-7000-8000-000000000001",
		TariffVersionID:        "01990000-1420-7000-8000-000000000001",
		DeploymentRevisionID:   "01990000-1982-7000-8000-000000000001",
		Objective:              "COST",
		Horizon:                "DAY_AHEAD",
		HorizonMinutes:         1440,
		Granularity:            "15MIN",
		PolicyConstraints: []byte(`{
          "comfort":{"zoneTempMinC":21,"zoneTempMaxC":25},
          "safety":{"supplyTempMinC":6,"supplyTempMaxC":10,"maxSupplyTempStepC":1},
          "inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.temperature"},
          "maintenanceConstraints":{"outOfService":[]},
          "manualLocks":{"resources":[]},
          "responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-180,"zoneTempDeltaCPerSupplyTempC":0.4,"energyUncertaintyP90KWh":60,"zoneTempUncertaintyP90C":0.2}
        }`),
	}
}

func testAuthoritativeState(at time.Time) AuthoritativeState {
	return AuthoritativeState{
		PricedDay: EnergyDayEvidence{
			PeriodStart: at.Add(-24 * time.Hour), PeriodEnd: at.Add(-time.Minute), EnergyKWh: 2400, CostCNY: 360,
			DatasetRevision: "local-development-v1:1792218568383000", DataWatermark: at.Add(-30 * time.Second),
		},
		SupplyTemperature: TelemetryEvidence{
			ObservationID: "01990000-2200-7000-8000-000000000001", DeviceID: "01990000-2300-7000-8000-000000000001", PointID: "01990000-2400-7000-8000-000000000001",
			TelemetryKey: "btu_meter.supply_water_temperature", PointRevision: 3, SampledAt: at.Add(-20 * time.Second), ReceivedAt: at.Add(-19 * time.Second),
			Value: 7, Unit: "Cel", Quality: "GOOD", SourceEventID: "01990000-2500-7000-8000-000000000001", SourcePartition: "telemetry-0", SourceOffset: 12,
		},
		ZoneTemperature: TelemetryEvidence{
			ObservationID: "01990000-2200-7000-8000-000000000002", DeviceID: "01990000-2300-7000-8000-000000000002", PointID: "01990000-2400-7000-8000-000000000002",
			TelemetryKey: "zone.temperature", PointRevision: 4, SampledAt: at.Add(-15 * time.Second), ReceivedAt: at.Add(-14 * time.Second),
			Value: 23, Unit: "Cel", Quality: "GOOD", SourceEventID: "01990000-2500-7000-8000-000000000002", SourcePartition: "telemetry-0", SourceOffset: 13,
		},
	}
}

func TestOptimizationPreparationFreezesAuthoritativeInputsAndChecksum(t *testing.T) {
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	store := &preparationStoreStub{
		definition: testPreparationDefinition(),
		result: PreparedOptimization{
			OptimizationRunID: "01990000-1950-7000-8000-000000000101",
			InputSnapshotID:   "01990000-1930-7000-8000-000000000101",
			Status:            "PENDING",
		},
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	request := PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerManual,
	}
	result, err := preparer.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "PENDING" || len(store.created) != 1 {
		t.Fatalf("prepared=%#v created=%d", result, len(store.created))
	}
	frozen := store.created[0]
	if frozen.Trigger != TriggerManual {
		t.Fatalf("the run is queued as %q, want the request's trigger", frozen.Trigger)
	}
	untriggered := request
	untriggered.Trigger = ""
	if _, err := preparer.Prepare(t.Context(), untriggered); err == nil {
		t.Fatal("a run without a trigger was prepared")
	}
	if frozen.CurrentState.Baseline.DailyEnergyKWh != 2400 || frozen.CurrentState.Baseline.DailyCost != 360 || frozen.CurrentState.Baseline.SupplyTempC != 7 || frozen.CurrentState.Baseline.ZoneTempC != 23 {
		t.Fatalf("baseline=%#v", frozen.CurrentState.Baseline)
	}
	if len(frozen.InputChecksum) != 64 || strings.Trim(frozen.InputChecksum, "0123456789abcdef") != "" {
		t.Fatalf("checksum=%q", frozen.InputChecksum)
	}
	checksum, err := checksumPreparedOptimization(frozen)
	if err != nil || checksum != frozen.InputChecksum {
		t.Fatalf("checksum recompute=%q err=%v", checksum, err)
	}
}

func TestOptimizationPreparationFailsClosedWhenAuthoritativeStateIsUnavailable(t *testing.T) {
	store := &preparationStoreStub{definition: testPreparationDefinition()}
	preparer, err := NewPreparer(store, &authoritativeInputStub{err: errors.New("owner unavailable")}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(t.Context(), PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerManual,
	})
	if !errors.Is(err, ErrPreparationUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if len(store.created) != 0 {
		t.Fatal("preparation must not persist a snapshot when authoritative state is unavailable")
	}
}

// A day priced before all its energy arrived would price the plant at part of a day's mix of peak
// and valley hours.
func TestOptimizationPreparationRefusesADayItsEnergyHasNotReached(t *testing.T) {
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	state := testAuthoritativeState(at)
	state.PricedDay.DataWatermark = state.PricedDay.PeriodEnd.Add(-time.Hour)
	store := &preparationStoreStub{definition: testPreparationDefinition()}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: state}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.Prepare(t.Context(), PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerManual,
	}); err == nil {
		t.Fatal("prepared an optimization on a day whose energy had not all arrived")
	}
	if len(store.created) != 0 {
		t.Fatal("preparation must not persist a snapshot on an incomplete day")
	}
}

// Postgres keeps timestamps to the microsecond. A run prepared on a nanosecond wall clock once
// failed every time with "frozen Optimization input checksum does not match persisted
// provenance", because the reloaded capture time no longer matched the one checksummed.
func TestOptimizationPreparationChecksumSurvivesTheDatabaseRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 11, 16, 44, 30, 123456789, time.UTC)
	store := &preparationStoreStub{definition: testPreparationDefinition(), result: PreparedOptimization{Status: "PENDING"}}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = preparer.Prepare(t.Context(), PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := store.created[0]
	reloaded.CapturedAt = reloaded.CapturedAt.Truncate(time.Microsecond)
	if checksum, err := checksumPreparedOptimization(reloaded); err != nil || checksum != store.created[0].InputChecksum {
		t.Fatalf("checksum after the round trip=%q, frozen=%q, err=%v", checksum, store.created[0].InputChecksum, err)
	}
}
