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
		DailyEnergy: MetricEvidence{
			ResultID: "01990000-2100-7000-8000-000000000001", MetricVersionID: "01990000-1510-7000-8000-000000000001",
			MetricCode: "daily_energy", PeriodStart: at.Add(-24 * time.Hour), PeriodEnd: at.Add(-time.Minute), CalculatedAt: at.Add(-30 * time.Second),
			Value: 2400, Unit: "kWh", Quality: "GOOD", Revision: 2,
		},
		DailyCost: MetricEvidence{
			ResultID: "01990000-2100-7000-8000-000000000002", MetricVersionID: "01990000-1510-7000-8000-000000000002",
			MetricCode: "energy_cost", PeriodStart: at.Add(-24 * time.Hour), PeriodEnd: at.Add(-time.Minute), CalculatedAt: at.Add(-20 * time.Second),
			Value: 360, Unit: "CNY", Quality: "GOOD", Revision: 1,
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
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001",
	}
	result, err := preparer.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "PENDING" || len(store.created) != 1 {
		t.Fatalf("prepared=%#v created=%d", result, len(store.created))
	}
	frozen := store.created[0]
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
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001",
	})
	if !errors.Is(err, ErrPreparationUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if len(store.created) != 0 {
		t.Fatal("preparation must not persist a snapshot when authoritative state is unavailable")
	}
}
