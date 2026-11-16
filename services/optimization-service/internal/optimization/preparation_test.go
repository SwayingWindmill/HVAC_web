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

// A schedule that fails to prepare used to leave only an empty page; each failure now names the one
// input an operator has to fix.
func TestOptimizationPreparationNamesTheMissingInput(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 20, 0, 0, time.UTC)
	request := PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerSchedule,
	}
	withPolicy := func(constraints string) PreparationDefinition {
		definition := testPreparationDefinition()
		definition.PolicyConstraints = []byte(constraints)
		return definition
	}
	withState := func(change func(*AuthoritativeState)) AuthoritativeState {
		state := testAuthoritativeState(at)
		change(&state)
		return state
	}
	cases := map[string]struct {
		store  *preparationStoreStub
		inputs *authoritativeInputStub
		want   PreparationReason
	}{
		"no model deployment": {
			store:  &preparationStoreStub{err: unavailable(ReasonModelDeploymentMissing, "no ACTIVE Optimization model deployment")},
			inputs: &authoritativeInputStub{state: testAuthoritativeState(at)},
			want:   ReasonModelDeploymentMissing,
		},
		"policy names an unknown field": {
			store:  &preparationStoreStub{definition: withPolicy(`{"unknown":1}`)},
			inputs: &authoritativeInputStub{state: testAuthoritativeState(at)},
			want:   ReasonPolicyUnsupported,
		},
		"zone temperature key has no observation": {
			store:  &preparationStoreStub{definition: testPreparationDefinition()},
			inputs: &authoritativeInputStub{err: unavailable(ReasonZoneTempMissing, "zone.temperature has no usable observation")},
			want:   ReasonZoneTempMissing,
		},
		"input owner cannot be asked": {
			store:  &preparationStoreStub{definition: testPreparationDefinition()},
			inputs: &authoritativeInputStub{err: errors.New("dial tcp: connection refused")},
			want:   ReasonInputsUnreadable,
		},
		"supply temperature reading is not GOOD": {
			store:  &preparationStoreStub{definition: testPreparationDefinition()},
			inputs: &authoritativeInputStub{state: withState(func(state *AuthoritativeState) { state.SupplyTemperature.Quality = "UNCERTAIN" })},
			want:   ReasonSupplyTempMissing,
		},
		"chillers off push supply above the safety band": {
			store:  &preparationStoreStub{definition: testPreparationDefinition()},
			inputs: &authoritativeInputStub{state: withState(func(state *AuthoritativeState) { state.SupplyTemperature.Value = 19 })},
			want:   ReasonBaselineOutsideLimits,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			preparer, err := NewPreparer(test.store, test.inputs, func() time.Time { return at })
			if err != nil {
				t.Fatal(err)
			}
			_, err = preparer.Prepare(t.Context(), request)
			if !errors.Is(err, ErrPreparationUnavailable) {
				t.Fatalf("err=%v, want the preparation to be unavailable", err)
			}
			if reason, ok := PreparationReasonOf(err); !ok || reason != test.want {
				t.Fatalf("reason=%q ok=%v, want %q (err=%v)", reason, ok, test.want, err)
			}
			if len(test.store.created) != 0 {
				t.Fatal("an unavailable preparation persisted a run")
			}
		})
	}
}

func TestSiteLocalDateString(t *testing.T) {
	at := time.Date(2026, 11, 15, 16, 30, 0, 0, time.UTC)
	if got := siteLocalDateString("Asia/Shanghai", at); got != "2026-11-16" {
		t.Fatalf("Asia/Shanghai after UTC+8 midnight=%q, want 2026-11-16", got)
	}
	beforeMidnight := time.Date(2026, 11, 15, 15, 30, 0, 0, time.UTC)
	if got := siteLocalDateString("Asia/Shanghai", beforeMidnight); got != "2026-11-15" {
		t.Fatalf("Asia/Shanghai before UTC+8 midnight=%q, want 2026-11-15", got)
	}
	if got := siteLocalDateString("", at); got != "2026-11-15" {
		t.Fatalf("empty timezone=%q, want UTC date 2026-11-15", got)
	}
	if got := siteLocalDateString("Mars/Olympus", at); got != "2026-11-15" {
		t.Fatalf("invalid timezone=%q, want UTC fallback date 2026-11-15", got)
	}
}

func TestOperatingExceptionBlockMessage(t *testing.T) {
	msg, blocked := operatingExceptionBlockMessage("MAINTENANCE_WINDOW", "1# 冷机清洗", "2026-11-16", "2026-11-18", "年度维保")
	if !blocked {
		t.Fatal("expected MAINTENANCE_WINDOW to block preparation")
	}
	if !strings.Contains(msg, "MAINTENANCE_WINDOW") || !strings.Contains(msg, "1# 冷机清洗") || !strings.Contains(msg, "2026-11-16 至 2026-11-18") || !strings.Contains(msg, "年度维保") {
		t.Fatalf("unexpected maintenance message=%q", msg)
	}

	holidayMsg, holidayBlocked := operatingExceptionBlockMessage("HOLIDAY_SHUTDOWN", "", "2026-10-01", "2026-10-01", "")
	if !holidayBlocked {
		t.Fatal("expected HOLIDAY_SHUTDOWN to block preparation")
	}
	if !strings.Contains(holidayMsg, "HOLIDAY_SHUTDOWN") || !strings.Contains(holidayMsg, "2026-10-01") || strings.Contains(holidayMsg, " 至 ") {
		t.Fatalf("unexpected holiday message=%q", holidayMsg)
	}

	overtimeMsg, overtimeBlocked := operatingExceptionBlockMessage("OVERTIME_COOLING", "周末加班供冷", "2026-11-21", "2026-11-21", "活动保障")
	if overtimeBlocked || overtimeMsg != "" {
		t.Fatalf("expected OVERTIME_COOLING not to block, got blocked=%v msg=%q", overtimeBlocked, overtimeMsg)
	}
}

func TestOptimizationPreparationStalenessMultiZoneAndHealthGates(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 20, 0, 0, time.UTC)
	request := PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Trigger: TriggerSchedule,
	}

	t.Run("enforces_15m_max_telemetry_age", func(t *testing.T) {
		staleSupply := testAuthoritativeState(at)
		staleSupply.SupplyTemperature.SampledAt = at.Add(-16 * time.Minute)
		preparer, err := NewPreparer(&preparationStoreStub{definition: testPreparationDefinition()}, &authoritativeInputStub{state: staleSupply}, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		_, err = preparer.Prepare(t.Context(), request)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonSupplyTempMissing {
			t.Fatalf("expected SUPPLY_TEMPERATURE_MISSING for 16m stale supply, got reason=%q ok=%v err=%v", reason, ok, err)
		}

		staleZone := testAuthoritativeState(at)
		staleZone.ZoneTemperature.SampledAt = at.Add(-20 * time.Minute)
		preparerZone, err := NewPreparer(&preparationStoreStub{definition: testPreparationDefinition()}, &authoritativeInputStub{state: staleZone}, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		_, err = preparerZone.Prepare(t.Context(), request)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonZoneTempMissing {
			t.Fatalf("expected ZONE_TEMPERATURE_MISSING for 20m stale zone, got reason=%q ok=%v err=%v", reason, ok, err)
		}

		fresh14m := testAuthoritativeState(at)
		fresh14m.SupplyTemperature.SampledAt = at.Add(-14 * time.Minute)
		fresh14m.ZoneTemperature.SampledAt = at.Add(-14 * time.Minute)
		storeOK := &preparationStoreStub{definition: testPreparationDefinition(), result: PreparedOptimization{Status: "PENDING"}}
		preparerOK, err := NewPreparer(storeOK, &authoritativeInputStub{state: fresh14m}, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := preparerOK.Prepare(t.Context(), request); err != nil {
			t.Fatalf("expected 14m reading to pass, got %v", err)
		}
	})

	t.Run("multi_zone_prevents_overheating_warmest_zone", func(t *testing.T) {
		def := testPreparationDefinition()
		def.PolicyConstraints = []byte(`{
          "comfort":{"zoneTempMinC":21,"zoneTempMaxC":25},
          "safety":{"supplyTempMinC":6,"supplyTempMaxC":10,"maxSupplyTempStepC":1},
          "inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.t53","zoneTemperatureKeys":["zone.t53","zone.t54"]},
          "maintenanceConstraints":{"outOfService":[]},
          "manualLocks":{"resources":[]},
          "responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-180,"zoneTempDeltaCPerSupplyTempC":0.4,"energyUncertaintyP90KWh":60,"zoneTempUncertaintyP90C":0.2}
        }`)

		state := testAuthoritativeState(at)
		t53 := state.ZoneTemperature
		t53.TelemetryKey = "zone.t53"
		t53.Value = 22.0
		t54 := state.ZoneTemperature
		t54.TelemetryKey = "zone.t54"
		t54.ObservationID = "01990000-2200-7000-8000-000000000003"
		t54.PointID = "01990000-2400-7000-8000-000000000003"
		t54.Value = 24.8
		state.ZoneTemperatures = []TelemetryEvidence{t53, t54}

		store := &preparationStoreStub{definition: def, result: PreparedOptimization{Status: "PENDING"}}
		preparer, err := NewPreparer(store, &authoritativeInputStub{state: state}, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.Prepare(t.Context(), request); err != nil {
			t.Fatalf("expected multi-zone preparation to succeed, got %v", err)
		}
		if store.created[0].CurrentState.Baseline.ZoneTempC != 24.8 || len(store.created[0].CurrentState.TelemetryEvidence) != 3 {
			t.Fatalf("expected worst-case zone temp 24.8 and 3 telemetry evidences, got baseline=%+v evidences=%d",
				store.created[0].CurrentState.Baseline, len(store.created[0].CurrentState.TelemetryEvidence))
		}

		state.ZoneTemperatures[1].Value = 25.3
		_, err = preparer.Prepare(t.Context(), request)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonBaselineOutsideLimits {
			t.Fatalf("expected BASELINE_OUTSIDE_LIMITS when t54=25.3, got reason=%q ok=%v err=%v", reason, ok, err)
		}
	})

	t.Run("probe_offset_and_unhealthy_gates_block_with_inputs_invalid", func(t *testing.T) {
		if reason, blocked := telemetryHealthBlockReason("HEALTHY", []string{"LOW_CONFIDENCE"}); blocked || reason != "" {
			t.Fatalf("expected HEALTHY+LOW_CONFIDENCE not blocked, got blocked=%v reason=%q", blocked, reason)
		}
		for _, tc := range []struct {
			status  string
			reasons []string
		}{
			{status: "HEALTHY", reasons: []string{"PROBE_OFFSET"}},
			{status: "UNHEALTHY", reasons: nil},
			{status: "BAD", reasons: nil},
		} {
			if _, blocked := telemetryHealthBlockReason(tc.status, tc.reasons); !blocked {
				t.Fatalf("expected %s/%v to block", tc.status, tc.reasons)
			}
		}

		state := testAuthoritativeState(at)
		state.SupplyTemperature.QualityReasons = []string{"PROBE_OFFSET"}
		preparer, err := NewPreparer(&preparationStoreStub{definition: testPreparationDefinition()}, &authoritativeInputStub{state: state}, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		_, err = preparer.Prepare(t.Context(), request)
		if reason, ok := PreparationReasonOf(err); !ok || reason != ReasonInputsInvalid {
			t.Fatalf("expected INPUTS_INVALID on PROBE_OFFSET, got reason=%q ok=%v err=%v", reason, ok, err)
		}
	})
}

func TestOperatingScheduleWindowBlockMessage(t *testing.T) {
	shanghai := func(year int, month time.Month, day, hour, minute int) time.Time {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			loc = time.FixedZone("Asia/Shanghai", 8*3600)
		}
		return time.Date(year, month, day, hour, minute, 0, 0, loc)
	}

	if _, blocked := operatingScheduleWindowBlockMessage(nil, shanghai(2026, 11, 16, 22, 0)); blocked {
		t.Fatal("nil schedule should not block")
	}
	if _, blocked := operatingScheduleWindowBlockMessage(&OperatingScheduleContext{SiteTimezone: "Asia/Shanghai", Configured: false}, shanghai(2026, 11, 16, 22, 0)); blocked {
		t.Fatal("unconfigured schedule should not block")
	}
	if _, blocked := operatingScheduleWindowBlockMessage(&OperatingScheduleContext{SiteTimezone: "Asia/Shanghai", Configured: true}, shanghai(2026, 11, 16, 22, 0)); blocked {
		t.Fatal("configured schedule with zero slots should not block 24x7")
	}

	schedule := &OperatingScheduleContext{
		SiteTimezone:        "Asia/Shanghai",
		Configured:          true,
		PreCoolLeadMinutes:  30,
		CoastDownLagMinutes: 20,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8*60 + 30, EndMinute: 18 * 60},
		},
	}

	cases := []struct {
		name        string
		at          time.Time
		wantBlocked bool
	}{
		{name: "before pre-cool lead", at: shanghai(2026, 11, 16, 7, 59), wantBlocked: true},
		{name: "at pre-cool start", at: shanghai(2026, 11, 16, 8, 0), wantBlocked: false},
		{name: "midday inside slot", at: shanghai(2026, 11, 16, 14, 0), wantBlocked: false},
		{name: "31m before endMinute", at: shanghai(2026, 11, 16, 17, 29), wantBlocked: false},
		{name: "exact 30m before endMinute", at: shanghai(2026, 11, 16, 17, 30), wantBlocked: true},
		{name: "10m before endMinute", at: shanghai(2026, 11, 16, 17, 50), wantBlocked: true},
		{name: "during coast-down lag after endMinute", at: shanghai(2026, 11, 16, 18, 10), wantBlocked: true},
		{name: "off-hours evening", at: shanghai(2026, 11, 16, 21, 0), wantBlocked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, blocked := operatingScheduleWindowBlockMessage(schedule, tc.at)
			if blocked != tc.wantBlocked {
				t.Fatalf("at=%s blocked=%v, want %v", tc.at, blocked, tc.wantBlocked)
			}
		})
	}

	crossMidnight := &OperatingScheduleContext{
		SiteTimezone:       "Asia/Shanghai",
		Configured:         true,
		PreCoolLeadMinutes: 30,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 6, StartMinute: 22 * 60, EndMinute: 24 * 60},
			{DayOfWeek: 7, StartMinute: 0, EndMinute: 2 * 60},
		},
	}
	if _, blocked := operatingScheduleWindowBlockMessage(crossMidnight, shanghai(2026, 11, 14, 23, 45)); blocked {
		t.Fatal("expected Sat 23:45 cross-midnight slot not to be blocked")
	}
	if _, blocked := operatingScheduleWindowBlockMessage(crossMidnight, shanghai(2026, 11, 15, 1, 35)); !blocked {
		t.Fatal("expected Sun 01:35 within 30m of 02:00 slot end to be blocked")
	}

	overtime := &OperatingScheduleContext{
		SiteTimezone:       "Asia/Shanghai",
		Configured:         true,
		PreCoolLeadMinutes: 30,
		Slots: []OperatingScheduleSlotWindow{
			{DayOfWeek: 1, StartMinute: 8 * 60, EndMinute: 18 * 60},
		},
		ApprovedExceptions: []ApprovedOperatingExceptionWindow{{
			ID: "exc-ot-1", Title: "周末加班", Kind: "OVERTIME_COOLING",
			StartDate: "2026-11-15", EndDate: "2026-11-15",
			ReplacementSlots: []OperatingExceptionReplacementSlot{{StartMinute: 10 * 60, EndMinute: 15 * 60}},
		}},
	}
	if _, blocked := operatingScheduleWindowBlockMessage(overtime, shanghai(2026, 11, 15, 9, 40)); blocked {
		t.Fatal("expected OVERTIME_COOLING pre-cool window at 09:40 to be allowed")
	}
	if _, blocked := operatingScheduleWindowBlockMessage(overtime, shanghai(2026, 11, 15, 14, 35)); !blocked {
		t.Fatal("expected OVERTIME_COOLING within 30m of 15:00 end to be blocked")
	}
}

func TestOptimizationPreparationAcceptsCalibrationMetadataInPolicyContract(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	def := testPreparationDefinition()
	def.PolicyConstraints = []byte(`{
		"comfort":{"zoneTempMinC":21,"zoneTempMaxC":25},
		"safety":{"supplyTempMinC":6,"supplyTempMaxC":10,"maxSupplyTempStepC":1},
		"inputMapping":{"supplyTemperatureKey":"btu_meter.supply_water_temperature","zoneTemperatureKey":"zone.temperature"},
		"maintenanceConstraints":{"outOfService":[]},
		"manualLocks":{"resources":[]},
		"responseModel":{"dailyEnergyDeltaKWhPerSupplyTempC":-180,"zoneTempDeltaCPerSupplyTempC":0.4,"energyUncertaintyP90KWh":60,"zoneTempUncertaintyP90C":0.2,"source":"SITE_DATA","calibratedAt":"2026-11-15T08:00:00Z"},
		"siteCalibrations":{"01990000-5000-7000-8000-000000000001":{"status":"CALIBRATED","source":"SITE_DATA","calibratedAt":"2026-11-15T08:00:00Z"}}
	}`)
	store := &preparationStoreStub{
		definition: def,
		result: PreparedOptimization{
			OptimizationRunID: "01990000-1950-7000-8000-000000000102",
			InputSnapshotID:   "01990000-1930-7000-8000-000000000102",
			Status:            "PENDING",
		},
	}
	preparer, err := NewPreparer(store, &authoritativeInputStub{state: testAuthoritativeState(at)}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Prepare(t.Context(), PreparationRequest{
		TenantID:    "01990000-3000-7000-8000-000000000001",
		SiteID:      "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE",
		SubjectID:   "01990000-5000-7000-8000-000000000001",
		Trigger:     TriggerManual,
	})
	if err != nil {
		t.Fatalf("expected calibrated policy contract to prepare cleanly, got %v", err)
	}
	if len(store.created) != 1 || store.created[0].SafetyConstraints.ResponseModel.Source != "SITE_DATA" || store.created[0].SafetyConstraints.ResponseModel.CalibratedAt != "2026-11-15T08:00:00Z" {
		t.Fatalf("unexpected frozen response model metadata: %#v", store.created)
	}
}
