package edgedaemon

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"
)

type fakePlantModbus struct {
	mu      sync.Mutex
	coils   map[uint8]map[uint16]uint16
	holding map[uint8]map[uint16]uint16
	input   map[uint8]map[uint16]uint16
}

func newFakePlantModbus() *fakePlantModbus {
	return &fakePlantModbus{
		coils:   map[uint8]map[uint16]uint16{31: {}},
		holding: map[uint8]map[uint16]uint16{2: {}, 3: {}, 4: {}, 5: {}, 10: {}},
		input:   map[uint8]map[uint16]uint16{20: {}},
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

func TestSongyaxuanPlantPollerEnvelopesAndFormulas(t *testing.T) {
	bus := newFakePlantModbus()
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

	curNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	var envs []mqttUplinkTelemetryEnvelope
	enabled := true
	runner, err := NewSongyaxuanPlantRunner(SongyaxuanPlantOptions{
		GatewayID: "01a14000-4000-7000-8000-000000000100",
		StateDir:  t.TempDir(),
		Config: SongyaxuanPlantConfig{
			Enabled:             true,
			SerialPort:          "/dev/ttyAS3",
			ConcentratorUnitID:  20,
			ConcentratorEnabled: &enabled,
			ET1010UnitID:        31,
			ET1010Enabled:       &enabled,
		},
		Bus: bus,
		Now: func() time.Time { return curNow },
		PublishTelemetry: func(_ context.Context, payload []byte) {
			var env mqttUplinkTelemetryEnvelope
			if json.Unmarshal(payload, &env) == nil {
				envs = append(envs, env)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewSongyaxuanPlantRunner: %v", err)
	}

	if err := runner.PollET1010Once(t.Context()); err != nil {
		t.Fatalf("PollET1010Once: %v", err)
	}
	if err := runner.PollFlowOnce(t.Context()); err != nil {
		t.Fatalf("PollFlowOnce: %v", err)
	}
	for _, dev := range []string{"ammeter-unit1", "ammeter-unit2", "ammeter-unit3", "ammeter-unit4"} {
		if err := runner.PollAmmeterOnce(t.Context(), dev); err != nil {
			t.Fatalf("PollAmmeterOnce %s: %v", dev, err)
		}
	}
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce: %v", err)
	}
	curNow = curNow.Add(36 * time.Second)
	if err := runner.PollTemperaturesOnce(t.Context()); err != nil {
		t.Fatalf("PollTemperaturesOnce #2: %v", err)
	}

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

	pw, err := runner.ReadChillerPowerKW(t.Context(), ET1010UnitA)
	if err != nil || pw == nil || *pw != 125.0 {
		t.Fatalf("ReadChillerPowerKW unit-a = (%v, %v), want 125.0", pw, err)
	}
}
