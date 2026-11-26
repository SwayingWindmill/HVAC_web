package edgecontrol

import (
	"context"
	"testing"
	"time"
)

func TestEnergyScheduleControllerSlotsOfflineAndFallbackHandoff(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	preCoolSP := 6.0
	peakShaveSP := 8.5
	pumpMin := 32.0
	pumpMax := 44.0
	oneChiller := 1

	schedule := &EdgeEnergySchedule{
		ScheduleID:                "sched-songyaxuan-1",
		Version:                   2,
		IssuedAt:                  day,
		ValidFrom:                 day,
		ValidUntil:                day.Add(14 * time.Hour),
		LeaseExpiresAt:            day.Add(10 * time.Hour),
		ContinueOfflineWhileValid: true,
		Slots: []EnergyScheduleSlot{
			{SlotID: "precool", StartMinute: 6 * 60, EndMinute: 8 * 60, Mode: ScheduleModePreCool, SupplyTempSetpointC: &preCoolSP, PumpFrequencyMinHz: &pumpMin, PumpFrequencyMaxHz: &pumpMax},
			{SlotID: "peak", StartMinute: 8 * 60, EndMinute: 12 * 60, Mode: ScheduleModePeakShave, SupplyTempSetpointC: &peakShaveSP, MaxRunningChillers: &oneChiller},
		},
	}

	ch1 := makeClusterChillerAdapter("ch-1", day, "RUNNING", 450, false)
	ch2 := makeClusterChillerAdapter("ch-2", day, "STOPPED", 0, false)
	pump1 := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID: "chwp-1", Enabled: true, Kind: ComponentDeviceDriver, Type: "PUMP", FactoryID: DeclarativeModbusFactoryID, Version: "1.0.0",
		Profiles: []CapabilityProfileID{ProfileVariableSpeedPump},
	}, []ChannelDescriptor{
		clusterChannel("chwp-1", "RunState", "chwp1:run", DataTypeString, AccessReadOnly),
		clusterChannel("chwp-1", "FreqSp", "chwp1:freq", DataTypeDouble, AccessReadWrite),
	})
	pump1.SetValue("chwp-1/RunState", StringValue("RUNNING"), QualityGood, day)
	pump1.SetValue("chwp-1/FreqSp", DoubleValue(48.0), QualityGood, day)

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range []DeviceAdapter{ch1, ch2, pump1} {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}

	schedCtrl := NewEnergyScheduleController("")
	if err := schedCtrl.UpdateSchedule(schedule); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	chillerTargets := []ChillerFallbackTarget{
		{ComponentID: "ch-1", SupplySetpointAddr: "ch-1/SupplySp", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault"},
		{ComponentID: "ch-2", SupplySetpointAddr: "ch-2/SupplySp", RunStateAddr: "ch-2/RunState", FaultAddr: "ch-2/Fault"},
	}
	pumpTargets := []PumpFallbackTarget{
		{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState"},
	}
	schedCtrl.UpdateTargets(chillerTargets, pumpTargets, nil)

	// Reject version downgrade cleanly while retaining version 2.
	older := schedule.Clone()
	older.Version = 1
	if err := schedCtrl.UpdateSchedule(older); err == nil {
		t.Fatal("expected version downgrade rejection")
	}
	if st := schedCtrl.StatusAt(day.Add(6*time.Hour + 30*time.Minute)); st.Version != 2 || !st.Active || st.Mode != ScheduleModePreCool {
		t.Fatalf("expected version 2 precool active, got %+v", st)
	}

	fallbackPolicy := DefaultAutonomyPolicyConfig()
	fallbackPolicy.Enabled = true
	fallbackPolicy.OfflineGraceSeconds = 60
	fallbackPolicy.DefaultChilledWaterSupplySetpointC = 7.0
	fallbackPolicy.DefaultPumpFrequencyHz = 36.0
	fallback, err := NewAutonomousFallbackController(AutonomousFallbackConfig{
		Policy:             fallbackPolicy,
		CloudConnected:     true,
		LastCloudContactAt: day,
		Chillers:           chillerTargets,
		Pumps:              pumpTargets,
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback.SetScheduleActiveFn(schedCtrl.IsActiveAt)

	clusterPolicy := DefaultClusterControlPolicy()
	clusterPolicy.Enabled = true
	clusterPolicy.MinRunningChillers = 1
	clusterPolicy.MaxRunningChillers = 2
	clusterPolicy.StageDwellSeconds = 0
	cluster, err := NewChillerPlantClusterController(ClusterControllerConfig{
		Policy: clusterPolicy,
		Chillers: []ClusterChillerBinding{
			{ComponentID: "ch-1", RatedCoolingKW: 500, StartAddr: "ch-1/Start", StopAddr: "ch-1/Stop", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault", SetpointAddr: "ch-1/SupplySp", LoadLimitAddr: "ch-1/LoadLimit", CoolingCapacityAddr: "ch-1/CapacityKW"},
			{ComponentID: "ch-2", RatedCoolingKW: 500, StartAddr: "ch-2/Start", StopAddr: "ch-2/Stop", RunStateAddr: "ch-2/RunState", FaultAddr: "ch-2/Fault", SetpointAddr: "ch-2/SupplySp", LoadLimitAddr: "ch-2/LoadLimit", CoolingCapacityAddr: "ch-2/CapacityKW"},
		},
		Pumps: []ClusterPumpBinding{{ComponentID: "chwp-1", FrequencyAddr: "chwp-1/FreqSp", RunStateAddr: "chwp-1/RunState"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cluster.SetScheduleOverrideFn(schedCtrl.ActiveOverrideAt)

	intentController, err := NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}

	if err := host.Start([]ControllerBinding{
		{Priority: 20, Controller: fallback},
		{Priority: 40, Controller: intentController},
		{Priority: 50, Controller: schedCtrl},
		{Priority: 60, Controller: cluster},
	}); err != nil {
		t.Fatal(err)
	}

	// 1. 06:30 PRE_COOL: setpoint 6.0C and cluster pump frequency (47.8Hz) clamped to <= 44.0Hz.
	t1 := day.Add(6*time.Hour + 30*time.Minute)
	if _, err := host.RunCycle(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 6.0 {
		t.Fatalf("expected PRE_COOL setpoint 6.0C, got %+v", sp)
	}
	if fq, ok := pump1.ReadValue("chwp-1/FreqSp"); !ok || fq.Double != 44.0 {
		t.Fatalf("expected PRE_COOL clamped pump freq 44.0Hz, got %+v", fq)
	}

	// 2. 07:00 Higher-priority IntentController cloud command overrides schedule while leased.
	t2 := day.Add(7 * time.Hour)
	if changed, err := host.IntentStore().Put(ControlIntent{
		ID: "cmd-override", Address: "ch-1/SupplySp", Requested: DoubleValue(9.0),
		IssuedAt: t2, ExpiresAt: t2.Add(5 * time.Minute), Source: "CLOUD_COMMAND",
	}); err != nil || !changed {
		t.Fatalf("IntentStore.Put: changed=%v err=%v", changed, err)
	}
	if _, err := host.RunCycle(context.Background(), t2.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 9.0 {
		t.Fatalf("expected leased cloud override 9.0C over schedule, got %+v", sp)
	}
	// Once intent lease expires, schedule regains control.
	if _, err := host.RunCycle(context.Background(), t2.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 6.0 {
		t.Fatalf("expected schedule 6.0C after intent lease expiry, got %+v", sp)
	}

	// 3. 08:30 PEAK_SHAVE: setpoint 8.5C and MaxRunningChillers=1 stops highest-runtime chiller ch-1 when ch-2 also runs.
	t3 := day.Add(8*time.Hour + 30*time.Minute)
	ch2.SetValue("ch-2/RunState", StringValue("RUNNING"), QualityGood, t3)
	ch2.SetValue("ch-2/CapacityKW", DoubleValue(150), QualityGood, t3)
	if _, err := host.RunCycle(context.Background(), t3); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch2.ReadValue("ch-2/SupplySp"); !ok || sp.Double != 8.5 {
		t.Fatalf("expected PEAK_SHAVE setpoint 8.5C on ch-2, got %+v", sp)
	}
	if stop1, ok := ch1.ReadValue("ch-1/Stop"); !ok || !stop1.Boolean {
		t.Fatalf("expected PEAK_SHAVE MaxRunningChillers=1 to stop highest-runtime chiller ch-1, got %+v", stop1)
	}
	if st := cluster.Status(); st.Mode != ScheduleModePeakShave || st.DesiredChillers != 1 {
		t.Fatalf("unexpected cluster PEAK_SHAVE status: %+v", st)
	}
	ch1.SetValue("ch-1/RunState", StringValue("STOPPED"), QualityGood, t3.Add(time.Minute))

	// 4. 09:00 Cloud disconnects while ContinueOfflineWhileValid=true and LeaseExpiresAt=10:00:
	// schedule stays active past offline grace, keeping AutonomousFallbackController standby.
	tDisconnect := day.Add(9 * time.Hour)
	schedCtrl.SetCloudConnected(false, tDisconnect)
	fallback.SetCloudConnected(false, tDisconnect)
	if _, err := host.RunCycle(context.Background(), tDisconnect.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !schedCtrl.IsActiveAt(tDisconnect.Add(5 * time.Minute)) {
		t.Fatal("expected offline schedule to stay active while ContinueOfflineWhileValid=true and lease valid")
	}
	if fb := fallback.StatusAt(tDisconnect.Add(5 * time.Minute)); fb.FallbackActive {
		t.Fatalf("expected fallback standby while offline schedule governs, got %+v", fb)
	}
	if sp, ok := ch2.ReadValue("ch-2/SupplySp"); !ok || sp.Double != 8.5 {
		t.Fatalf("expected schedule 8.5C during valid offline continuation, got %+v", sp)
	}

	// 5. 10:05 LeaseExpiresAt passes while still offline -> immediate handoff to AutonomousFallbackController.
	tExpired := day.Add(10*time.Hour + 5*time.Minute)
	if _, err := host.RunCycle(context.Background(), tExpired); err != nil {
		t.Fatal(err)
	}
	stExpired := schedCtrl.StatusAt(tExpired)
	if stExpired.Active || stExpired.FallbackReason != "LEASE_EXPIRED" {
		t.Fatalf("expected LEASE_EXPIRED status, got %+v", stExpired)
	}
	if fb := fallback.StatusAt(tExpired); !fb.FallbackActive {
		t.Fatalf("expected AutonomousFallbackController active after lease expiry, got %+v", fb)
	}
	if sp, ok := ch2.ReadValue("ch-2/SupplySp"); !ok || sp.Double != 7.0 {
		t.Fatalf("expected fallback setpoint 7.0C after schedule lease expiry, got %+v", sp)
	}
	if fq, ok := pump1.ReadValue("chwp-1/FreqSp"); !ok || fq.Double != 36.0 {
		t.Fatalf("expected fallback pump freq 36.0Hz after schedule lease expiry, got %+v", fq)
	}

	// 6. When ContinueOfflineWhileValid=false, cloud disconnect immediately relinquishes schedule.
	tOfflineCheck := day.Add(9 * time.Hour)
	noOfflineSched := schedule.Clone()
	noOfflineSched.Version = 3
	noOfflineSched.ContinueOfflineWhileValid = false
	if err := schedCtrl.UpdateSchedule(noOfflineSched); err != nil {
		t.Fatal(err)
	}
	stNoOffline := schedCtrl.StatusAt(tOfflineCheck)
	if stNoOffline.Active || stNoOffline.FallbackReason != "CLOUD_DISCONNECTED_NO_OFFLINE_CONTINUATION" {
		t.Fatalf("expected CLOUD_DISCONNECTED_NO_OFFLINE_CONTINUATION, got %+v", stNoOffline)
	}
}

func TestEdgeEnergyScheduleValidationRejectsInvalidConfigs(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	base := EdgeEnergySchedule{
		ScheduleID: "sched-1",
		Version:    1,
		IssuedAt:   day,
		ValidFrom:  day,
		ValidUntil: day.Add(24 * time.Hour),
		Slots: []EnergyScheduleSlot{
			{SlotID: "s1", StartMinute: 0, EndMinute: 600, Mode: ScheduleModeNormal},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid baseline failed: %v", err)
	}

	badOverlap := base
	badOverlap.Slots = []EnergyScheduleSlot{
		{SlotID: "s1", StartMinute: 0, EndMinute: 600, Mode: ScheduleModeNormal},
		{SlotID: "s2", StartMinute: 540, EndMinute: 720, Mode: ScheduleModeEco},
	}
	if err := badOverlap.Validate(); err == nil {
		t.Fatal("expected overlapping slot error")
	}

	lo := 42.0
	hi := 38.0
	badBounds := base
	badBounds.Slots = []EnergyScheduleSlot{
		{SlotID: "s1", StartMinute: 0, EndMinute: 600, Mode: ScheduleModeNormal, PumpFrequencyMinHz: &lo, PumpFrequencyMaxHz: &hi},
	}
	if err := badBounds.Validate(); err == nil {
		t.Fatal("expected inverted pump bounds error")
	}

	badID := base
	badID.ScheduleID = ""
	if err := badID.Validate(); err == nil {
		t.Fatal("expected missing scheduleId error")
	}
}

func TestEnergyScheduleControllerModelSetpointProviderFallbackAndBounds(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	explicitSP := 6.5
	schedule := &EdgeEnergySchedule{
		ScheduleID: "sched-model-driven",
		Version:    1,
		IssuedAt:   day,
		ValidFrom:  day,
		ValidUntil: day.Add(24 * time.Hour),
		Slots: []EnergyScheduleSlot{
			{SlotID: "model-eco", StartMinute: 8 * 60, EndMinute: 12 * 60, Mode: ScheduleModeEco},
			{SlotID: "explicit-precool", StartMinute: 12 * 60, EndMinute: 14 * 60, Mode: ScheduleModePreCool, SupplyTempSetpointC: &explicitSP},
			{SlotID: "standby-slot", StartMinute: 14 * 60, EndMinute: 16 * 60, Mode: ScheduleModeStandby},
		},
	}

	ch1 := makeClusterChillerAdapter("ch-1", day, "RUNNING", 300, false)
	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(ch1); err != nil {
		t.Fatal(err)
	}

	schedCtrl := NewEnergyScheduleController("")
	if err := schedCtrl.UpdateSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	schedCtrl.UpdateTargets([]ChillerFallbackTarget{
		{ComponentID: "ch-1", SupplySetpointAddr: "ch-1/SupplySp", RunStateAddr: "ch-1/RunState", FaultAddr: "ch-1/Fault"},
	}, nil, nil)

	providerVal := 9.25
	providerOK := true
	schedCtrl.SetModelSetpointProvider(func(img ProcessImage) (float64, bool) {
		if _, ok := img.Get("ch-1/CapacityKW"); !ok {
			return 0, false
		}
		return providerVal, providerOK
	})

	if err := host.Start([]ControllerBinding{{Priority: 50, Controller: schedCtrl}}); err != nil {
		t.Fatal(err)
	}

	// 1. Model-driven slot without explicit SupplyTempSetpointC uses model provider.
	if _, err := host.RunCycle(context.Background(), day.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 9.25 {
		t.Fatalf("expected model-driven setpoint 9.25C, got %+v", sp)
	}

	// 2. Out-of-bounds model setpoint is clamped to [4, 18].
	providerVal = 25.0
	if _, err := host.RunCycle(context.Background(), day.Add(9*time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 18.0 {
		t.Fatalf("expected clamped upper setpoint 18.0C, got %+v", sp)
	}
	providerVal = 1.5
	if _, err := host.RunCycle(context.Background(), day.Add(9*time.Hour+2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 4.0 {
		t.Fatalf("expected clamped lower setpoint 4.0C, got %+v", sp)
	}

	// 3. Explicit slot SupplyTempSetpointC takes precedence over model provider.
	providerVal = 11.0
	if _, err := host.RunCycle(context.Background(), day.Add(12*time.Hour+30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 6.5 {
		t.Fatalf("expected explicit slot setpoint 6.5C to override model 11.0C, got %+v", sp)
	}

	// 4. Standby slot does not push model setpoint.
	ch1.SetValue("ch-1/SupplySp", DoubleValue(7.0), QualityGood, day.Add(14*time.Hour))
	if _, err := host.RunCycle(context.Background(), day.Add(14*time.Hour+5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sp, ok := ch1.ReadValue("ch-1/SupplySp"); !ok || sp.Double != 7.0 {
		t.Fatalf("expected standby slot to leave SupplySp untouched at 7.0C, got %+v", sp)
	}
}
