package edgecontrol

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyHardRealtimeProtectionAndEmergencyStopExclusion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		point    string
		capCode  string
		meta     map[string]any
		wantOK   bool
		wantCat  HardRealtimeProtectionCategory
	}{
		{name: "plc estop", point: "plc_estop_trip", wantOK: true, wantCat: HardRealtimeCategoryPLCEStop},
		{name: "fire interlock", point: "fire_interlock_shutdown", wantOK: true, wantCat: HardRealtimeCategoryFireInterlock},
		{name: "high pressure trip", point: "condenser_high_pressure_trip", wantOK: true, wantCat: HardRealtimeCategoryHighPressureTrip},
		{name: "freeze protection", point: "evaporator_freeze_protection_relay", wantOK: true, wantCat: HardRealtimeCategoryFreezeProtection},
		{name: "hardware safety protection", point: "safety_relay_interlock", wantOK: true, wantCat: HardRealtimeCategoryHardwareSafety},
		{name: "explicit metadata boundary", point: "chiller_cmd", meta: map[string]any{"safetyBoundary": "HARD_REALTIME_SAFETY"}, wantOK: true, wantCat: HardRealtimeCategoryHardwareSafety},
		{name: "explicit metadata owner", point: "chiller_cmd", meta: map[string]any{"safetyOwner": "PLC", "hardRealtimeCategory": "FIRE_INTERLOCK"}, wantOK: true, wantCat: HardRealtimeCategoryFireInterlock},
		{name: "emergency stop all exclusion", point: "emergency_stop_all", capCode: "EMERGENCY_DO_RELEASE", wantOK: false},
		{name: "normal setpoint", point: "chw_supply_setpoint_c", capCode: "SET_CHILLED_WATER_TEMPERATURE_SETPOINT", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotCat, gotOK := ClassifyHardRealtimeProtection(tc.point, tc.capCode, tc.meta)
			if gotOK != tc.wantOK {
				t.Fatalf("ClassifyHardRealtimeProtection(%q, %q) ok = %v, want %v (cat=%s)", tc.point, tc.capCode, gotOK, tc.wantOK, gotCat)
			}
			if tc.wantOK && gotCat != tc.wantCat {
				t.Fatalf("category = %s, want %s", gotCat, tc.wantCat)
			}
		})
	}
}

func TestHardRealtimeBoundaryGuardsAcrossEdgeControl(t *testing.T) {
	t.Parallel()

	// 1. ValidatePointSafetyBoundary allows read-only telemetry/state, blocks writable / COMMAND / SETTING.
	if err := ValidatePointSafetyBoundary("plc_estop_trip", "STATE", false, nil); err != nil {
		t.Fatalf("expected read-only STATE safety observation point to be allowed, got %v", err)
	}
	if err := ValidatePointSafetyBoundary("emergency_stop_all", "COMMAND", true, map[string]any{"actuationMode": "EMERGENCY_DO_RELEASE"}); err != nil {
		t.Fatalf("expected emergency_stop_all to remain allowed, got %v", err)
	}
	for _, tc := range []struct {
		code     string
		ptType   string
		writable bool
		meta     map[string]any
	}{
		{code: "plc_estop_trip", ptType: "COMMAND", writable: true},
		{code: "fire_interlock_trip", ptType: "SETTING", writable: false},
		{code: "custom_cmd", ptType: "TELEMETRY", writable: true, meta: map[string]any{"safetyBoundary": "HARD_REALTIME_SAFETY"}},
	} {
		err := ValidatePointSafetyBoundary(tc.code, tc.ptType, tc.writable, tc.meta)
		if !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
			t.Fatalf("ValidatePointSafetyBoundary(%q, %s, writable=%v) error = %v, want ErrHardRealtimeSafetyBoundary", tc.code, tc.ptType, tc.writable, err)
		}
	}

	// 2. ChannelDescriptor.validate allows ReadOnly observation and blocks ReadWrite/WriteOnly.
	roSafety := ChannelDescriptor{
		ComponentID: "PLC-1", ChannelID: "plc_estop_trip", PointID: "pt-estop", DataType: DataTypeBoolean,
		Access: AccessReadOnly, SafetyBoundary: SafetyBoundaryHardRealtime,
		Category: ChannelCategoryOpenemsType,
		PollPriority: PriorityVeryHigh, LocalPersistencePriority: PriorityVeryHigh,
		RemotePersistencePriority: PriorityVeryHigh, AggregationPriority: PriorityMedium, ResendPriority: PriorityVeryHigh,
	}
	if err := roSafety.validate(); err != nil {
		t.Fatalf("expected ReadOnly safety channel to validate, got %v", err)
	}
	rwSafety := roSafety
	rwSafety.Access = AccessReadWrite
	if err := rwSafety.validate(); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("expected ReadWrite safety channel to fail with ErrHardRealtimeSafetyBoundary, got %v", err)
	}

	// 3. ControlPlan and IntentStore reject hard real-time target channels.
	rt := NewRuntime()
	if err := rt.Register(ChannelDescriptor{
		ComponentID: "PLC-1", ChannelID: "fire_interlock_cmd", PointID: "pt-fire", DataType: DataTypeBoolean, Access: AccessReadWrite,
		Category: ChannelCategoryOpenemsType,
		PollPriority: PriorityVeryHigh, LocalPersistencePriority: PriorityVeryHigh,
		RemotePersistencePriority: PriorityVeryHigh, AggregationPriority: PriorityMedium, ResendPriority: PriorityVeryHigh,
	}); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("expected Register writable hard-realtime to fail, got %v", err)
	}
	if err := rt.Register(roSafety); err != nil {
		t.Fatalf("Register read-only safety error = %v", err)
	}
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	if err := rt.PublishNext(roSafety.Address(), Sample{Value: BooleanValue(false), Quality: QualityGood, ObservedAt: now, Sequence: 1}); err != nil {
		t.Fatalf("PublishNext error = %v", err)
	}
	img := rt.SwitchProcessImage(now)
	plan := NewControlPlan(img)
	if _, err := plan.Deny(roSafety.Address(), "c1", "deny"); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("plan.Deny error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}
	minVal := 1.0
	if _, err := plan.ConstrainNumber(roSafety.Address(), "c1", "bound", &minVal, nil); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("plan.ConstrainNumber error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}
	if _, _, err := plan.Request(roSafety.Address(), "c1", BooleanValue(true)); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("plan.Request error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	store, err := NewIntentStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ControlIntent{
		ID: "int-safety", Address: roSafety.Address(), Requested: BooleanValue(true),
		IssuedAt: now, ExpiresAt: now.Add(time.Minute), Source: "CLOUD_COMMAND",
	}); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("IntentStore.Put error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	// 4. ValidateModbusDriverInput allows read-only safety register and blocks writable safety register.
	timeoutMS := 500
	retries := 1
	unitID := 1
	ep := "127.0.0.1:502"
	readOnlyInput := ModbusDriverInput{
		DeviceKey: "PLC-1", DriverType: "MODBUS_TCP", Endpoint: &ep, UnitID: &unitID, TimeoutMS: &timeoutMS, Retries: &retries,
		Registers: []ModbusRegisterInput{{
			PointCode: "high_pressure_trip", SafetyBoundary: string(SafetyBoundaryHardRealtime),
			FunctionCode: 3, Address: 10, RegisterCount: 1, RawType: "BIT_BOOL", BitIndex: intPtr(0),
			ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: string(PriorityVeryHigh),
		}},
	}
	if _, err := ValidateModbusDriverInput(readOnlyInput, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true}); err != nil {
		t.Fatalf("expected read-only safety Modbus register to validate, got %v", err)
	}
	writeFC := int(ModbusFunctionWriteSingleRegister)
	writableInput := readOnlyInput
	writableInput.Registers = []ModbusRegisterInput{{
		PointCode: "high_pressure_trip", FunctionCode: 3, WriteFunctionCode: &writeFC, Address: 10, RegisterCount: 1,
		RawType: "UINT16", ByteOrder: "BIG_ENDIAN", WordOrder: "BIG_ENDIAN", Scale: 1, Offset: 0, PollPriority: string(PriorityVeryHigh),
	}}
	if _, err := ValidateModbusDriverInput(writableInput, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true}); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("expected writable safety Modbus register error ErrHardRealtimeSafetyBoundary, got %v", err)
	}

	// 5. EdgeRuleAction validation and runtime execution block hard-realtime channels.
	badAction := EdgeRuleAction{
		Kind: EdgeRuleActionProtectionCommand, TargetDeviceID: "PLC-1", TargetPointCode: "freeze_protection_trip", Capability: "TRIP",
	}
	if err := badAction.validate(); !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("EdgeRuleAction.validate() error = %v, want ErrHardRealtimeSafetyBoundary", err)
	}

	eng := NewEdgeRuleController(EdgeRuleControllerConfig{
		CommandChannelResolver: func(deviceID, pointCode, capability string) (string, DataType, string, bool) {
			return roSafety.Address(), DataTypeBoolean, "", true
		},
	})
	triggered, err := eng.applyActions(CompiledEdgeRule{
		RuleID: "rule-safety",
		Actions: []EdgeRuleAction{{
			Kind: EdgeRuleActionProtectionCommand, TargetDeviceID: "PLC-1", TargetPointCode: "plc_estop_trip", Capability: "TRIGGER",
		}},
	}, EdgeActionTriggerRaise, img, plan)
	if err != nil {
		t.Fatalf("applyActions error = %v", err)
	}
	if len(triggered) != 0 {
		t.Fatalf("expected hard-realtime rule action to be skipped, got triggered=%v", triggered)
	}
}

func intPtr(v int) *int { return &v }
