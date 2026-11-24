package edgedaemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type fakePlantModbus struct {
	mu      sync.Mutex
	coils   map[uint8]map[uint16]uint16
	holding map[uint8]map[uint16]uint16
	input   map[uint8]map[uint16]uint16
}

func newFakePlantModbus() *fakePlantModbus {
	return &fakePlantModbus{
		coils:   map[uint8]map[uint16]uint16{},
		holding: map[uint8]map[uint16]uint16{},
		input:   map[uint8]map[uint16]uint16{},
	}
}

func (f *fakePlantModbus) ensureUnit(uid uint8) {
	if f.coils[uid] == nil {
		f.coils[uid] = map[uint16]uint16{}
	}
	if f.holding[uid] == nil {
		f.holding[uid] = map[uint16]uint16{}
	}
	if f.input[uid] == nil {
		f.input[uid] = map[uint16]uint16{}
	}
}

func (f *fakePlantModbus) ReadCoils(_ context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint16, quantity)
	for i := range quantity {
		out[i] = f.coils[unitID][address+i]
	}
	return out, nil
}

func (f *fakePlantModbus) ReadHoldingRegisters(_ context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint16, quantity)
	for i := range quantity {
		out[i] = f.holding[unitID][address+i]
	}
	return out, nil
}

func (f *fakePlantModbus) ReadInputRegisters(_ context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint16, quantity)
	for i := range quantity {
		out[i] = f.input[unitID][address+i]
	}
	return out, nil
}

func setU32ABCD(regs map[uint16]uint16, addr uint16, v uint32) {
	regs[addr] = uint16(v >> 16)
	regs[addr+1] = uint16(v & 0xffff)
}

func collectPointsByDevice(envs []mqttUplinkTelemetryEnvelope) map[string]map[string]mqttUplinkPoint {
	byDev := map[string]map[string]mqttUplinkPoint{}
	for _, env := range envs {
		for _, dev := range env.Payload.Devices {
			pts := byDev[dev.DeviceID]
			if pts == nil {
				pts = map[string]mqttUplinkPoint{}
				byDev[dev.DeviceID] = pts
			}
			for _, pt := range dev.Points {
				pts[pt.Code] = pt
			}
		}
	}
	return byDev
}

func TestFieldPlantSongyaxuanConfigParityAndSourceKeys(t *testing.T) {
	plantPath := filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.edge-plant.json")
	cfg, err := LoadFieldPlantConfigFile(plantPath)
	if err != nil {
		t.Fatalf("LoadFieldPlantConfigFile: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate songyaxuan.edge-plant.json: %v", err)
	}
	wantProtected := []uint8{2, 3, 4, 5, 10, 31}
	if got := cfg.ProtectedRS485UnitIDs(); !slices.Equal(got, wantProtected) {
		t.Fatalf("ProtectedRS485UnitIDs = %v, want %v", got, wantProtected)
	}

	siteBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.site.json"))
	if err != nil {
		t.Fatalf("read songyaxuan.site.json: %v", err)
	}
	var siteDoc struct {
		Devices []struct {
			SourceKey string `json:"sourceKey"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(siteBytes, &siteDoc); err != nil {
		t.Fatalf("decode songyaxuan.site.json: %v", err)
	}
	siteKeys := make(map[string]struct{}, len(siteDoc.Devices))
	for _, d := range siteDoc.Devices {
		siteKeys[d.SourceKey] = struct{}{}
	}
	assertSiteHasKey := func(key string) {
		t.Helper()
		if _, ok := siteKeys[key]; !ok {
			t.Fatalf("deviceKey %q in songyaxuan.edge-plant.json missing from songyaxuan.site.json", key)
		}
	}
	for _, m := range cfg.Meters.Devices {
		assertSiteHasKey(m.DeviceKey)
		if m.ControllerDeviceKey != "" {
			assertSiteHasKey(m.ControllerDeviceKey)
		}
	}
	assertSiteHasKey(cfg.Meters.SynthesizedTotal.DeviceKey)
	assertSiteHasKey(cfg.FlowMeter.DeviceKey)
	assertSiteHasKey(cfg.FlowMeter.PumpMirror.DeviceKey)
	assertSiteHasKey(cfg.Concentrator.TempDeviceKey)
	assertSiteHasKey(cfg.Concentrator.VoltageDeviceKey)
	assertSiteHasKey(cfg.Concentrator.RSSIDeviceKey)
	assertSiteHasKey(cfg.Concentrator.StatusDeviceKey)
	for _, route := range cfg.Concentrator.CopyRoutes {
		for _, target := range route.Targets {
			assertSiteHasKey(target.DeviceKey)
		}
	}
	assertSiteHasKey(cfg.BTUCalculator.DeviceKey)
	assertSiteHasKey(cfg.DOControl.DeviceKey)
	for _, u := range cfg.DOControl.Units {
		assertSiteHasKey(u.ChillerDeviceKey)
		assertSiteHasKey(u.MeterDeviceKey)
		for _, aux := range u.Auxiliaries {
			assertSiteHasKey(aux.DeviceKey)
		}
	}

	bus := newFakePlantModbus()
	for _, uid := range []uint8{2, 3, 4, 5, 10, 20, 31} {
		bus.ensureUnit(uid)
	}
	bus.coils[31][8] = 1
	bus.holding[10][1] = 35315
	bus.holding[10][5] = 3600
	setU32ABCD(bus.holding[4], 36, 1250)
	setU32ABCD(bus.holding[4], 60, 77038)
	setU32ABCD(bus.holding[3], 36, 200)
	setU32ABCD(bus.holding[3], 60, 500)
	setU32ABCD(bus.holding[5], 36, 280)
	setU32ABCD(bus.holding[5], 60, 300)
	setU32ABCD(bus.holding[2], 36, 20)
	setU32ABCD(bus.holding[2], 60, 100)

	for ch := 1; ch <= 20; ch++ {
		bus.input[20][concRegSensorTypeBase+uint16(ch-1)] = 0x35
		bus.input[20][concRegVoltageBase+uint16(ch-1)] = 3100
		bus.input[20][concRegRSSIBase+uint16(ch-1)] = uint16(0x10000 - 65)
		bus.input[20][concRegTempBase+uint16(ch-1)] = uint16(int16(100 + ch))
	}
	bus.input[20][concRegSensorTypeBase+13] = 0
	bus.input[20][concRegTempBase+19] = 999
	bus.input[20][concRegTempBase+17] = 75
	bus.input[20][concRegTempBase+15] = 125
	bus.input[20][concRegSensorTypeBase+54] = 0x35
	bus.input[20][concRegVoltageBase+54] = 3200
	bus.input[20][concRegRSSIBase+54] = uint16(0x10000 - 60)
	bus.input[20][concRegTempBase+54] = 285

	stateDir := t.TempDir()
	curNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	var envs []mqttUplinkTelemetryEnvelope
	runner, err := NewFieldPlantRunner(FieldPlantOptions{
		GatewayID: "01a14000-4000-7000-8000-000000000100",
		StateDir:  stateDir,
		Config:    cfg,
		Bus:       bus,
		Now:       func() time.Time { return curNow },
		PublishTelemetry: func(_ context.Context, payload []byte) {
			var env mqttUplinkTelemetryEnvelope
			if json.Unmarshal(payload, &env) == nil {
				envs = append(envs, env)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewFieldPlantRunner: %v", err)
	}

	if err := runner.PollDOOnce(t.Context()); err != nil {
		t.Fatalf("PollDOOnce: %v", err)
	}
	if err := runner.PollFlowOnce(t.Context()); err != nil {
		t.Fatalf("PollFlowOnce: %v", err)
	}
	for _, dev := range []string{"ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4"} {
		if err := runner.PollMeterOnce(t.Context(), dev); err != nil {
			t.Fatalf("PollMeterOnce %s: %v", dev, err)
		}
	}
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce: %v", err)
	}
	curNow = curNow.Add(36 * time.Second)
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce #2: %v", err)
	}

	byDev := collectPointsByDevice(envs)
	if _, ok := byDev["temperature"]["t20"]; ok {
		t.Fatal("SPARE channel t20 must not be emitted")
	}
	if pt := byDev["temperature"]["t14"]; pt.Quality != 1 {
		t.Fatalf("offline t14 must have quality=1, got %+v", pt)
	}
	if pt := byDev["ammeter-unit4"]["active_energy_total"]; pt.Value != 100.0 {
		t.Fatalf("ammeter-unit4 baseline energy = %v, want 100.0", pt.Value)
	}
	if pt := byDev["ammeter-unit1"]["run_state"]; pt.Value != "RUNNING" {
		t.Fatalf("ammeter-unit1 shared aux hysteresis = %v, want RUNNING", pt.Value)
	}
	if pt := byDev["ctrl-chiller-c"]["run_state"]; pt.Value != "RUNNING" {
		t.Fatalf("ctrl-chiller-c run_state = %v, want RUNNING", pt.Value)
	}
	if pt := byDev["ammeter-total"]["power_total"]; pt.Value != 175.0 {
		t.Fatalf("ammeter-total power_total = %v, want 175.0", pt.Value)
	}
	if pt := byDev["cooling-tower"]["cooling_tower_leaving_temperature"]; pt.Value != 28.5 {
		t.Fatalf("expected t55 priority copy 28.5, got %v", pt)
	}
	if pt := byDev["btu-calc"]["instant_cooling_capacity"]; math.Abs(pt.Value.(float64)-5815.0) > 0.2 {
		t.Fatalf("btu instant capacity = %v, want 5815.0", pt.Value)
	}
	if pt := byDev["btu-calc"]["accumulated_cooling_energy"]; pt.Value.(float64) <= 58.0 {
		t.Fatalf("expected accumulated_cooling_energy ~58.15 kWh over 36s, got %v", pt.Value)
	}

	pw, err := runner.ReadDevicePowerKW(t.Context(), "ammeter-unit4")
	if err != nil || pw == nil || *pw != 125.0 {
		t.Fatalf("ReadDevicePowerKW ammeter-unit4 = (%v, %v), want 125.0", pw, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, fieldPlantStateFileName)); err != nil {
		t.Fatalf("expected persisted %s: %v", fieldPlantStateFileName, err)
	}
}

func TestFieldPlantArbitrarySiteBConfigAndShadowHotReload(t *testing.T) {
	siteBJSON := []byte(`{
		"enabled": true,
		"serialPort": "/dev/ttyS4",
		"meters": {
			"pollIntervalSec": 60,
			"freshWindowSec": 300,
			"devices": [
				{
					"unitId": 11,
					"deviceKey": "siteb-meter-1",
					"controllerDeviceKey": "siteb-chiller-1",
					"powerRegisterAddr": 100,
					"powerScale": 0.1,
					"energyRegisterAddr": 102,
					"energyScale": 0.1,
					"startKw": 10,
					"stopKw": 3
				},
				{
					"unitId": 12,
					"deviceKey": "siteb-meter-2",
					"controllerDeviceKey": "siteb-chiller-2",
					"powerRegisterAddr": 100,
					"powerScale": 0.1,
					"energyRegisterAddr": 102,
					"energyScale": 0.1,
					"startKw": 10,
					"stopKw": 3
				}
			],
			"synthesizedTotal": {
				"deviceKey": "siteb-total"
			}
		},
		"flowMeter": {
			"unitId": 15,
			"deviceKey": "siteb-flow",
			"registerBase": 0,
			"registerCount": 2,
			"flowRegisterOffset": 0,
			"flowScale": 0.1,
			"flowDivisor": 1,
			"pumpMirror": {
				"deviceKey": "siteb-pump-main",
				"flowPointCode": "flow_rate",
				"runningThreshold": 5
			}
		},
		"concentrator": {
			"unitId": 25,
			"activeChannels": [1, 2, 10, 11],
			"tempDeviceKey": "siteb-temp",
			"voltageDeviceKey": "siteb-volt",
			"rssiDeviceKey": "siteb-rssi",
			"statusDeviceKey": "siteb-status",
			"copyRoutes": [
				{
					"sourceKey": "t1",
					"targets": [{ "deviceKey": "siteb-chiller-1", "pointCode": "chw_out" }]
				}
			]
		},
		"btuCalculator": {
			"deviceKey": "siteb-btu",
			"supplyTempKey": "t1",
			"returnTempKey": "t10",
			"specificHeatFactor": 1.163,
			"minPlantPowerKw": 5
		}
	}`)
	cfg, err := ParseFieldPlantConfig(siteBJSON)
	if err != nil {
		t.Fatalf("ParseFieldPlantConfig Site B: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate Site B: %v", err)
	}
	if want := []uint8{11, 12, 15}; !slices.Equal(cfg.ProtectedRS485UnitIDs(), want) {
		t.Fatalf("Site B ProtectedRS485UnitIDs = %v, want %v", cfg.ProtectedRS485UnitIDs(), want)
	}

	bus := newFakePlantModbus()
	for _, uid := range []uint8{11, 12, 15, 25} {
		bus.ensureUnit(uid)
	}
	bus.holding[15][0] = 500
	setU32ABCD(bus.holding[11], 100, 150)
	setU32ABCD(bus.holding[11], 102, 2000)
	setU32ABCD(bus.holding[12], 100, 50)
	setU32ABCD(bus.holding[12], 102, 1000)

	for _, ch := range []int{1, 2, 10, 11} {
		bus.input[25][concRegSensorTypeBase+uint16(ch-1)] = 0x35
		bus.input[25][concRegVoltageBase+uint16(ch-1)] = 3300
		bus.input[25][concRegRSSIBase+uint16(ch-1)] = uint16(0x10000 - 58)
	}
	bus.input[25][concRegTempBase+0] = 70
	bus.input[25][concRegTempBase+1] = 185
	bus.input[25][concRegTempBase+9] = 120
	bus.input[25][concRegTempBase+10] = 300
	bus.input[25][concRegTempBase+4] = 999

	stateDir := t.TempDir()
	curNow := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	var envs []mqttUplinkTelemetryEnvelope
	runner, err := NewFieldPlantRunner(FieldPlantOptions{
		GatewayID: "0194f000-9000-7000-8000-000000000200",
		StateDir:  stateDir,
		Config:    cfg,
		Bus:       bus,
		Now:       func() time.Time { return curNow },
		PublishTelemetry: func(_ context.Context, payload []byte) {
			var env mqttUplinkTelemetryEnvelope
			if json.Unmarshal(payload, &env) == nil {
				envs = append(envs, env)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewFieldPlantRunner Site B: %v", err)
	}

	if err := runner.PollFlowOnce(t.Context()); err != nil {
		t.Fatalf("PollFlowOnce Site B: %v", err)
	}
	if err := runner.PollMeterOnce(t.Context(), "siteb-meter-1"); err != nil {
		t.Fatalf("PollMeterOnce siteb-meter-1: %v", err)
	}
	if err := runner.PollMeterOnce(t.Context(), "siteb-meter-2"); err != nil {
		t.Fatalf("PollMeterOnce siteb-meter-2: %v", err)
	}
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce Site B: %v", err)
	}

	byDev := collectPointsByDevice(envs)
	if _, skipped := byDev["siteb-temp"]["t5"]; skipped {
		t.Fatal("expected inactive gap channel t5 to be skipped")
	}
	if pt := byDev["siteb-total"]["power_total"]; pt.Value != 20.0 {
		t.Fatalf("Site B total power = %v, want 20.0", pt.Value)
	}
	if pt := byDev["siteb-pump-main"]["run_state"]; pt.Value != "RUNNING" {
		t.Fatalf("Site B pump mirror run_state = %v, want RUNNING", pt.Value)
	}
	if pt := byDev["siteb-chiller-1"]["chw_out"]; pt.Value != 7.0 {
		t.Fatalf("Site B baseline copy chw_out = %v, want 7.0", pt.Value)
	}
	if pt := byDev["siteb-btu"]["instant_cooling_capacity"]; math.Abs(pt.Value.(float64)-290.8) > 0.2 {
		t.Fatalf("Site B btu capacity = %v, want 290.8", pt.Value)
	}

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	reg := NewProtocolBridgeRegistry(nil)
	defer func() { _ = reg.Close() }()
	shadow := NewShadowManager("0194f000-9000-7000-8000-000000000200", stateDir, host, reg, nil, nil, nil, slog.Default())
	shadow.AttachFieldPlant(runner)
	if err := shadow.LoadOrBootstrap(Config{GatewayID: "0194f000-9000-7000-8000-000000000200", StateDir: stateDir}); err != nil {
		t.Fatalf("LoadOrBootstrap: %v", err)
	}

	report, stale, err := shadow.ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 1,
		PointMappings: []GatewayPointMapping{
			{
				SourceChannel:    "ch2",
				PrimaryDeviceKey: "siteb-temp",
				PrimaryPointCode: "t2",
				CopyTargets:      []PointCopyTarget{{DeviceKey: "siteb-chiller-1", PointCode: "chw_out"}},
			},
		},
	})
	if err != nil || stale || report.AppliedStatus != shadowStatusApplied {
		t.Fatalf("ApplyDesired rev 1 failed: report=%+v stale=%v err=%v", report, stale, err)
	}

	envs = nil
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce after shadow update: %v", err)
	}
	byDev = collectPointsByDevice(envs)
	if pt := byDev["siteb-chiller-1"]["chw_out"]; pt.Value != 18.5 {
		t.Fatalf("expected live hot-reloaded copy chw_out = 18.5 from t2, got %+v", pt)
	}

	report, stale, err = shadow.ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 2,
		PointMappings:   []GatewayPointMapping{},
	})
	if err != nil || stale || report.AppliedStatus != shadowStatusApplied {
		t.Fatalf("ApplyDesired rev 2 failed: report=%+v stale=%v err=%v", report, stale, err)
	}
	envs = nil
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce after shadow reset: %v", err)
	}
	byDev = collectPointsByDevice(envs)
	if pt := byDev["siteb-chiller-1"]["chw_out"]; pt.Value != 7.0 {
		t.Fatalf("expected cleared shadow PointMappings to fall back to t1=7.0, got %+v", pt)
	}
}
