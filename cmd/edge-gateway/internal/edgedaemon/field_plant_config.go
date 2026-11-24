package edgedaemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

const (
	defaultPlantTimeoutMS        = 1000
	defaultConcRPCSettleMS       = 300
	defaultMeterPollSec          = 300
	defaultMeterFreshWindowSec   = 900
	defaultFlowPollSec           = 60
	defaultFlowFreshWindowSec    = 180
	defaultConcPollSec           = 60
	defaultConcLowBatteryMV      = 2700
	defaultBTUSpecificHeatFactor = 1.163
	defaultBTUMinPlantPowerKW    = 20.0
	defaultBTUMaxGapSec          = 600
	defaultDOPollSec             = 60
	defaultDOPulseDurationMS     = 500
	defaultDOStopPowerKW         = 5.0
	defaultMeterPowerAddr        = 36
	defaultMeterEnergyAddr       = 60
	defaultMeterPowerScale       = 0.1
	defaultMeterEnergyScale      = 1.0
	defaultMeterStartKW          = 12.75
	defaultMeterStopKW           = 5.0
	defaultMeterNoAuxStartKW     = 57.75
	defaultMeterNoAuxStopKW      = 45.0
	defaultFlowRegisterBase      = 1
	defaultFlowRegisterCount     = 5
	defaultFlowRunningThreshold  = 10.0
)

// FieldPlantConfig declaratively defines a site's southbound RS-485 plant polling,
// virtual energy/cooling calculation, wireless concentrator routing, and DO sequence control.
type FieldPlantConfig struct {
	Enabled          bool                      `json:"enabled"`
	SerialPort       string                    `json:"serialPort"`
	RequestTimeoutMS int                       `json:"requestTimeoutMs,omitempty"`
	Retries          int                       `json:"retries,omitempty"`
	ProtectedUnitIDs []uint8                   `json:"protectedUnitIds,omitempty"`
	ConcentratorRPC  *ConcentratorRPCConfig    `json:"concentratorRpc,omitempty"`
	Meters           *MeterGroupConfig         `json:"meters,omitempty"`
	FlowMeter        *FlowMeterConfig          `json:"flowMeter,omitempty"`
	Concentrator     *ConcentratorPollerConfig `json:"concentrator,omitempty"`
	BTUCalculator    *BTUCalculatorConfig      `json:"btuCalculator,omitempty"`
	DOControl        *DOControlConfig          `json:"doControl,omitempty"`
}

// ConcentratorRPCConfig enables MQTT-driven wireless concentrator parameter/EID RPC.
type ConcentratorRPCConfig struct {
	Enabled  bool `json:"enabled"`
	SettleMS int  `json:"settleMs,omitempty"`
}

// MeterGroupConfig configures periodic polling for a group of Modbus meters and optional sum total.
type MeterGroupConfig struct {
	PollIntervalSec  int                          `json:"pollIntervalSec,omitempty"`
	FreshWindowSec   int                          `json:"freshWindowSec,omitempty"`
	Devices          []FieldMeterSpec             `json:"devices"`
	SynthesizedTotal *SynthesizedTotalMeterConfig `json:"synthesizedTotal,omitempty"`
}

// FieldMeterSpec defines register mapping and chiller run-state inference for one Modbus meter.
type FieldMeterSpec struct {
	UnitID               uint8              `json:"unitId"`
	DeviceKey            string             `json:"deviceKey"`
	ControllerDeviceKey  string             `json:"controllerDeviceKey,omitempty"`
	PowerRegisterAddr    uint16             `json:"powerRegisterAddr,omitempty"`
	PowerScale           float64            `json:"powerScale,omitempty"`
	EnergyRegisterAddr   uint16             `json:"energyRegisterAddr,omitempty"`
	EnergyScale          float64            `json:"energyScale,omitempty"`
	EnergyBaselineKWh    float64            `json:"energyBaselineKwh,omitempty"`
	CombinedFeed         bool               `json:"combinedFeed,omitempty"`
	SharedAuxKW          map[string]float64 `json:"sharedAuxKw,omitempty"`
	StartKW              float64            `json:"startKw,omitempty"`
	StopKW               float64            `json:"stopKw,omitempty"`
	CombinedNoAuxStartKW float64            `json:"combinedNoAuxStartKw,omitempty"`
	CombinedNoAuxStopKW  float64            `json:"combinedNoAuxStopKw,omitempty"`
}

// SynthesizedTotalMeterConfig aggregates fresh meter readings into a virtual total meter device.
type SynthesizedTotalMeterConfig struct {
	DeviceKey        string   `json:"deviceKey"`
	SourceDeviceKeys []string `json:"sourceDeviceKeys,omitempty"`
}

// FlowMeterConfig configures a Modbus flow meter and optional pump mirror device.
type FlowMeterConfig struct {
	PollIntervalSec        int                   `json:"pollIntervalSec,omitempty"`
	FreshWindowSec         int                   `json:"freshWindowSec,omitempty"`
	UnitID                 uint8                 `json:"unitId"`
	DeviceKey              string                `json:"deviceKey"`
	RegisterBase           uint16                `json:"registerBase,omitempty"`
	RegisterCount          uint16                `json:"registerCount,omitempty"`
	FlowRegisterOffset     uint16                `json:"flowRegisterOffset,omitempty"`
	FlowScale              float64               `json:"flowScale,omitempty"`
	FlowDivisor            float64               `json:"flowDivisor,omitempty"`
	VelocityRegisterOffset *uint16               `json:"velocityRegisterOffset,omitempty"`
	VelocityScale          float64               `json:"velocityScale,omitempty"`
	VelocityDivisor        float64               `json:"velocityDivisor,omitempty"`
	PumpMirror             *FlowPumpMirrorConfig `json:"pumpMirror,omitempty"`
}

// FlowPumpMirrorConfig derives a pump device's flow rate and running status from flow meter output.
type FlowPumpMirrorConfig struct {
	DeviceKey        string  `json:"deviceKey"`
	FlowPointCode    string  `json:"flowPointCode,omitempty"`
	RunningThreshold float64 `json:"runningThreshold,omitempty"`
}

// ConcentratorPollerConfig configures wireless sensor concentrator channel polling and point copying.
type ConcentratorPollerConfig struct {
	PollIntervalSec  int                    `json:"pollIntervalSec,omitempty"`
	UnitID           uint8                  `json:"unitId"`
	ActiveChannels   []int                  `json:"activeChannels"`
	TempDeviceKey    string                 `json:"tempDeviceKey,omitempty"`
	VoltageDeviceKey string                 `json:"voltageDeviceKey,omitempty"`
	RSSIDeviceKey    string                 `json:"rssiDeviceKey,omitempty"`
	StatusDeviceKey  string                 `json:"statusDeviceKey,omitempty"`
	LowBatteryMV     int                    `json:"lowBatteryMv,omitempty"`
	CopyRoutes       []PointCopyRouteConfig `json:"copyRoutes,omitempty"`
}

// PointCopyRouteConfig fans out a good concentrator source channel reading to target devices/points.
type PointCopyRouteConfig struct {
	SourceKey string            `json:"sourceKey"`
	Targets   []PointCopyTarget `json:"targets"`
}

// BTUCalculatorConfig computes virtual plant cooling capacity and integrated cooling energy.
type BTUCalculatorConfig struct {
	DeviceKey            string  `json:"deviceKey"`
	SupplyTempKey        string  `json:"supplyTempKey"`
	ReturnTempKey        string  `json:"returnTempKey"`
	SpecificHeatFactor   float64 `json:"specificHeatFactor,omitempty"`
	MinPlantPowerKW      float64 `json:"minPlantPowerKw,omitempty"`
	IntegrationMaxGapSec int     `json:"integrationMaxGapSec,omitempty"`
}

// DOSequenceTimings configures step, pre-pulse, coast-down, and auxiliary shutdown delays.
type DOSequenceTimings struct {
	AuxStartStepDelayMS    int `json:"auxStartStepDelayMs,omitempty"`
	PreStartPulseDelayMS   int `json:"preStartPulseDelayMs,omitempty"`
	ChillerStopCoastDownMS int `json:"chillerStopCoastDownMs,omitempty"`
	AuxiliaryStopStepMS    int `json:"auxiliaryStopStepMs,omitempty"`
}

// DOCoilConfig maps a DO channel name to a Modbus coil address.
type DOCoilConfig struct {
	Channel string `json:"channel"`
	Coil    uint16 `json:"coil"`
}

// DOAuxStepConfig defines one ordered auxiliary step inside a chiller start/stop train.
type DOAuxStepConfig struct {
	Role              string `json:"role,omitempty"`
	DeviceKey         string `json:"deviceKey"`
	Channel           string `json:"channel"`
	Coil              uint16 `json:"coil"`
	AfterStartDelayMS *int   `json:"afterStartDelayMs,omitempty"`
}

// DOPulseConfig defines a momentary start or stop pulse coil.
type DOPulseConfig struct {
	Channel string `json:"channel"`
	Coil    uint16 `json:"coil"`
}

// DOUnitTrainConfig defines one controllable chiller unit and its auxiliary chain.
type DOUnitTrainConfig struct {
	UnitCode         string            `json:"unitCode"`
	ChillerDeviceKey string            `json:"chillerDeviceKey"`
	MeterDeviceKey   string            `json:"meterDeviceKey,omitempty"`
	Auxiliaries      []DOAuxStepConfig `json:"auxiliaries"`
	StartPulse       DOPulseConfig     `json:"startPulse"`
	StopPulse        DOPulseConfig     `json:"stopPulse"`
}

// DOControlConfig configures Modbus DO coil polling and safe sequential chiller execution.
type DOControlConfig struct {
	Enabled            *bool               `json:"enabled,omitempty"`
	DeviceKey          string              `json:"deviceKey"`
	WinnerControllerID string              `json:"winnerControllerId,omitempty"`
	UnitID             uint8               `json:"unitId"`
	PollIntervalSec    int                 `json:"pollIntervalSec,omitempty"`
	PulseDurationMS    int                 `json:"pulseDurationMs,omitempty"`
	ChillerStopPowerKW *float64            `json:"chillerStopPowerKw,omitempty"`
	Timings            *DOSequenceTimings  `json:"timings,omitempty"`
	Coils              []DOCoilConfig      `json:"coils"`
	Units              []DOUnitTrainConfig `json:"units"`
}

// IsDOControlEnabled reports whether DO coil polling and sequential command execution are active.
func (c *DOControlConfig) IsEnabled() bool {
	return c != nil && (c.Enabled == nil || *c.Enabled)
}

// StopPowerKW returns the chiller stop verification threshold in kW (defaults to 5 kW).
func (c *DOControlConfig) StopPowerKW() float64 {
	if c == nil || c.ChillerStopPowerKW == nil {
		return defaultDOStopPowerKW
	}
	return *c.ChillerStopPowerKW
}

// EffectiveTimings returns sequence step timings with safe production defaults applied.
func (c *DOControlConfig) EffectiveTimings() DOSequenceTimings {
	out := DOSequenceTimings{
		AuxStartStepDelayMS:    30000,
		PreStartPulseDelayMS:   60000,
		ChillerStopCoastDownMS: 30000,
		AuxiliaryStopStepMS:    10000,
	}
	if c == nil || c.Timings == nil {
		return out
	}
	if c.Timings.AuxStartStepDelayMS > 0 {
		out.AuxStartStepDelayMS = c.Timings.AuxStartStepDelayMS
	}
	if c.Timings.PreStartPulseDelayMS > 0 {
		out.PreStartPulseDelayMS = c.Timings.PreStartPulseDelayMS
	}
	if c.Timings.ChillerStopCoastDownMS > 0 {
		out.ChillerStopCoastDownMS = c.Timings.ChillerStopCoastDownMS
	}
	if c.Timings.AuxiliaryStopStepMS > 0 {
		out.AuxiliaryStopStepMS = c.Timings.AuxiliaryStopStepMS
	}
	return out
}

// IsConcentratorRPCEnabled reports whether wireless concentrator MQTT RPC handling is active.
func (c *FieldPlantConfig) IsConcentratorRPCEnabled() bool {
	return c != nil && c.Enabled && c.ConcentratorRPC != nil && c.ConcentratorRPC.Enabled
}

// IsDOControlEnabled reports whether field DO sequence control is active.
func (c *FieldPlantConfig) IsDOControlEnabled() bool {
	return c != nil && c.Enabled && c.DOControl.IsEnabled()
}

// ProtectedRS485UnitIDs returns deduplicated, ascending RS-485 unit IDs protected from concentrator RPC.
func (c *FieldPlantConfig) ProtectedRS485UnitIDs() []uint8 {
	if c == nil {
		return nil
	}
	concUID := uint8(0)
	if c.Concentrator != nil {
		concUID = c.Concentrator.UnitID
	}
	set := make(map[uint8]struct{})
	add := func(uid uint8) {
		if uid >= 1 && uid <= 247 && uid != concUID {
			set[uid] = struct{}{}
		}
	}
	if c.Meters != nil {
		for _, m := range c.Meters.Devices {
			add(m.UnitID)
		}
	}
	if c.FlowMeter != nil {
		add(c.FlowMeter.UnitID)
	}
	if c.DOControl.IsEnabled() {
		add(c.DOControl.UnitID)
	}
	for _, uid := range c.ProtectedUnitIDs {
		add(uid)
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]uint8, 0, len(set))
	for uid := range set {
		out = append(out, uid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ApplyDefaults populates safe production defaults for omitted FieldPlantConfig fields.
func (c *FieldPlantConfig) ApplyDefaults() {
	if c == nil || !c.Enabled {
		return
	}
	c.SerialPort = strings.TrimSpace(c.SerialPort)
	if c.RequestTimeoutMS == 0 {
		c.RequestTimeoutMS = defaultPlantTimeoutMS
	}
	if c.ConcentratorRPC != nil && c.ConcentratorRPC.Enabled && c.ConcentratorRPC.SettleMS == 0 {
		c.ConcentratorRPC.SettleMS = defaultConcRPCSettleMS
	}
	if c.Meters != nil {
		if c.Meters.PollIntervalSec == 0 {
			c.Meters.PollIntervalSec = defaultMeterPollSec
		}
		if c.Meters.FreshWindowSec == 0 {
			c.Meters.FreshWindowSec = defaultMeterFreshWindowSec
		}
		for i := range c.Meters.Devices {
			d := &c.Meters.Devices[i]
			d.DeviceKey = strings.TrimSpace(d.DeviceKey)
			d.ControllerDeviceKey = strings.TrimSpace(d.ControllerDeviceKey)
			if d.PowerRegisterAddr == 0 && d.EnergyRegisterAddr == 0 {
				d.PowerRegisterAddr = defaultMeterPowerAddr
				d.EnergyRegisterAddr = defaultMeterEnergyAddr
			}
			if d.PowerScale == 0 {
				d.PowerScale = defaultMeterPowerScale
			}
			if d.EnergyScale == 0 {
				d.EnergyScale = defaultMeterEnergyScale
			}
			if d.StartKW == 0 {
				d.StartKW = defaultMeterStartKW
			}
			if d.StopKW == 0 {
				d.StopKW = defaultMeterStopKW
			}
			if d.CombinedNoAuxStartKW == 0 {
				d.CombinedNoAuxStartKW = defaultMeterNoAuxStartKW
			}
			if d.CombinedNoAuxStopKW == 0 {
				d.CombinedNoAuxStopKW = defaultMeterNoAuxStopKW
			}
		}
		if c.Meters.SynthesizedTotal != nil {
			c.Meters.SynthesizedTotal.DeviceKey = strings.TrimSpace(c.Meters.SynthesizedTotal.DeviceKey)
			if len(c.Meters.SynthesizedTotal.SourceDeviceKeys) == 0 {
				src := make([]string, 0, len(c.Meters.Devices))
				for _, d := range c.Meters.Devices {
					src = append(src, d.DeviceKey)
				}
				c.Meters.SynthesizedTotal.SourceDeviceKeys = src
			}
		}
	}
	if c.FlowMeter != nil {
		f := c.FlowMeter
		f.DeviceKey = strings.TrimSpace(f.DeviceKey)
		if f.PollIntervalSec == 0 {
			f.PollIntervalSec = defaultFlowPollSec
		}
		if f.FreshWindowSec == 0 {
			f.FreshWindowSec = defaultFlowFreshWindowSec
		}
		if f.RegisterBase == 0 && f.RegisterCount == 0 {
			f.RegisterBase = defaultFlowRegisterBase
			f.RegisterCount = defaultFlowRegisterCount
		}
		if f.FlowScale == 0 {
			f.FlowScale = 1
		}
		if f.FlowDivisor == 0 {
			f.FlowDivisor = 1
		}
		if f.VelocityScale == 0 {
			f.VelocityScale = 1
		}
		if f.VelocityDivisor == 0 {
			f.VelocityDivisor = 1
		}
		if f.PumpMirror != nil {
			f.PumpMirror.DeviceKey = strings.TrimSpace(f.PumpMirror.DeviceKey)
			f.PumpMirror.FlowPointCode = strings.TrimSpace(f.PumpMirror.FlowPointCode)
			if f.PumpMirror.FlowPointCode == "" {
				f.PumpMirror.FlowPointCode = "flow_rate"
			}
			if f.PumpMirror.RunningThreshold == 0 {
				f.PumpMirror.RunningThreshold = defaultFlowRunningThreshold
			}
		}
	}
	if c.Concentrator != nil {
		p := c.Concentrator
		if p.PollIntervalSec == 0 {
			p.PollIntervalSec = defaultConcPollSec
		}
		p.TempDeviceKey = firstNonEmpty(p.TempDeviceKey, "temperature")
		p.VoltageDeviceKey = firstNonEmpty(p.VoltageDeviceKey, "temperature-voltage")
		p.RSSIDeviceKey = firstNonEmpty(p.RSSIDeviceKey, "temperature-rssi")
		p.StatusDeviceKey = firstNonEmpty(p.StatusDeviceKey, "temperature-status")
		if p.LowBatteryMV == 0 {
			p.LowBatteryMV = defaultConcLowBatteryMV
		}
	}
	if c.BTUCalculator != nil {
		b := c.BTUCalculator
		b.DeviceKey = strings.TrimSpace(b.DeviceKey)
		b.SupplyTempKey = strings.TrimSpace(b.SupplyTempKey)
		b.ReturnTempKey = strings.TrimSpace(b.ReturnTempKey)
		if b.SpecificHeatFactor == 0 {
			b.SpecificHeatFactor = defaultBTUSpecificHeatFactor
		}
		if b.MinPlantPowerKW == 0 {
			b.MinPlantPowerKW = defaultBTUMinPlantPowerKW
		}
		if b.IntegrationMaxGapSec == 0 {
			b.IntegrationMaxGapSec = defaultBTUMaxGapSec
		}
	}
	if c.DOControl != nil {
		d := c.DOControl
		if d.Enabled == nil {
			en := true
			d.Enabled = &en
		}
		d.DeviceKey = strings.TrimSpace(d.DeviceKey)
		if d.IsEnabled() {
			if d.PollIntervalSec == 0 {
				d.PollIntervalSec = defaultDOPollSec
			}
			if d.PulseDurationMS == 0 {
				d.PulseDurationMS = defaultDOPulseDurationMS
			}
			if d.ChillerStopPowerKW == nil {
				kw := defaultDOStopPowerKW
				d.ChillerStopPowerKW = &kw
			}
			if strings.TrimSpace(d.WinnerControllerID) == "" && d.DeviceKey != "" && d.UnitID > 0 {
				d.WinnerControllerID = fmt.Sprintf("%s-uid%d", d.DeviceKey, d.UnitID)
			} else {
				d.WinnerControllerID = strings.TrimSpace(d.WinnerControllerID)
			}
		}
	}
}

// Validate enforces fail-closed invariants across all declarative field plant sections.
func (c FieldPlantConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.SerialPort) == "" {
		return errors.New("fieldPlant.serialPort is required when enabled")
	}
	if c.RequestTimeoutMS < 50 || c.RequestTimeoutMS > 10000 {
		return fmt.Errorf("fieldPlant.requestTimeoutMs %d must be in [50,10000]", c.RequestTimeoutMS)
	}
	if c.Retries < 0 || c.Retries > 5 {
		return fmt.Errorf("fieldPlant.retries %d must be in [0,5]", c.Retries)
	}
	for _, uid := range c.ProtectedUnitIDs {
		if uid < 1 || uid > 247 {
			return fmt.Errorf("fieldPlant.protectedUnitIds contains invalid UID %d", uid)
		}
	}
	if c.ConcentratorRPC != nil && c.ConcentratorRPC.Enabled {
		if c.ConcentratorRPC.SettleMS < 1 || c.ConcentratorRPC.SettleMS > 60000 {
			return fmt.Errorf("fieldPlant.concentratorRpc.settleMs %d must be in [1,60000]", c.ConcentratorRPC.SettleMS)
		}
	}

	meterDeviceKeys := make(map[string]struct{})
	if c.Meters != nil {
		if err := validateMetersConfig(c.Meters, meterDeviceKeys); err != nil {
			return err
		}
	}
	if c.FlowMeter != nil {
		if err := validateFlowMeterConfig(c.FlowMeter); err != nil {
			return err
		}
	}
	if c.Concentrator != nil {
		if err := validateConcentratorPollerConfig(c.Concentrator); err != nil {
			return err
		}
	}
	if c.BTUCalculator != nil {
		if err := validateBTUConfig(c.BTUCalculator, c.Concentrator != nil, c.FlowMeter != nil, c.Meters != nil); err != nil {
			return err
		}
	}
	if c.DOControl.IsEnabled() {
		if err := validateDOControlConfig(c.DOControl, c.Meters != nil, meterDeviceKeys); err != nil {
			return err
		}
	}
	return nil
}

func validateMetersConfig(m *MeterGroupConfig, meterKeys map[string]struct{}) error {
	if m.PollIntervalSec < 1 || m.PollIntervalSec > 86400 {
		return fmt.Errorf("fieldPlant.meters.pollIntervalSec %d must be in [1,86400]", m.PollIntervalSec)
	}
	if m.FreshWindowSec < 1 || m.FreshWindowSec > 86400 {
		return fmt.Errorf("fieldPlant.meters.freshWindowSec %d must be in [1,86400]", m.FreshWindowSec)
	}
	if len(m.Devices) == 0 {
		return errors.New("fieldPlant.meters.devices must not be empty")
	}
	for _, dev := range m.Devices {
		if dev.UnitID < 1 || dev.UnitID > 247 {
			return fmt.Errorf("fieldPlant.meters.devices[%q].unitId %d must be in [1,247]", dev.DeviceKey, dev.UnitID)
		}
		if dev.DeviceKey == "" {
			return errors.New("fieldPlant.meters.devices[].deviceKey is required")
		}
		if _, dup := meterKeys[dev.DeviceKey]; dup {
			return fmt.Errorf("fieldPlant.meters.devices duplicate deviceKey %q", dev.DeviceKey)
		}
		meterKeys[dev.DeviceKey] = struct{}{}
		if dev.PowerRegisterAddr == dev.EnergyRegisterAddr {
			return fmt.Errorf("fieldPlant.meters.devices[%q] powerRegisterAddr and energyRegisterAddr must differ", dev.DeviceKey)
		}
		if !isFinitePositive(dev.PowerScale) || !isFinitePositive(dev.EnergyScale) {
			return fmt.Errorf("fieldPlant.meters.devices[%q] powerScale and energyScale must be finite > 0", dev.DeviceKey)
		}
		if !isFiniteNonNeg(dev.EnergyBaselineKWh) {
			return fmt.Errorf("fieldPlant.meters.devices[%q].energyBaselineKwh must be finite >= 0", dev.DeviceKey)
		}
		if !isFiniteNonNeg(dev.StartKW) || !isFiniteNonNeg(dev.StopKW) || dev.StopKW > dev.StartKW {
			return fmt.Errorf("fieldPlant.meters.devices[%q] stopKw must be <= startKw and >= 0", dev.DeviceKey)
		}
		if !isFiniteNonNeg(dev.CombinedNoAuxStartKW) || !isFiniteNonNeg(dev.CombinedNoAuxStopKW) || dev.CombinedNoAuxStopKW > dev.CombinedNoAuxStartKW {
			return fmt.Errorf("fieldPlant.meters.devices[%q] combinedNoAuxStopKw must be <= combinedNoAuxStartKw and >= 0", dev.DeviceKey)
		}
		for ch, kw := range dev.SharedAuxKW {
			if strings.TrimSpace(ch) == "" || !isFiniteNonNeg(kw) {
				return fmt.Errorf("fieldPlant.meters.devices[%q].sharedAuxKw has invalid entry %q", dev.DeviceKey, ch)
			}
		}
	}
	if m.SynthesizedTotal != nil {
		if m.SynthesizedTotal.DeviceKey == "" {
			return errors.New("fieldPlant.meters.synthesizedTotal.deviceKey is required")
		}
		if _, collision := meterKeys[m.SynthesizedTotal.DeviceKey]; collision {
			return fmt.Errorf("fieldPlant.meters.synthesizedTotal.deviceKey %q collides with a meter deviceKey", m.SynthesizedTotal.DeviceKey)
		}
		seenSrc := make(map[string]struct{}, len(m.SynthesizedTotal.SourceDeviceKeys))
		for _, src := range m.SynthesizedTotal.SourceDeviceKeys {
			if _, ok := meterKeys[src]; !ok {
				return fmt.Errorf("fieldPlant.meters.synthesizedTotal references unknown meter %q", src)
			}
			if _, dup := seenSrc[src]; dup {
				return fmt.Errorf("fieldPlant.meters.synthesizedTotal duplicate sourceDeviceKey %q", src)
			}
			seenSrc[src] = struct{}{}
		}
	}
	return nil
}

func validateFlowMeterConfig(f *FlowMeterConfig) error {
	if f.PollIntervalSec < 1 || f.PollIntervalSec > 86400 {
		return fmt.Errorf("fieldPlant.flowMeter.pollIntervalSec %d must be in [1,86400]", f.PollIntervalSec)
	}
	if f.FreshWindowSec < 1 || f.FreshWindowSec > 86400 {
		return fmt.Errorf("fieldPlant.flowMeter.freshWindowSec %d must be in [1,86400]", f.FreshWindowSec)
	}
	if f.UnitID < 1 || f.UnitID > 247 {
		return fmt.Errorf("fieldPlant.flowMeter.unitId %d must be in [1,247]", f.UnitID)
	}
	if f.DeviceKey == "" {
		return errors.New("fieldPlant.flowMeter.deviceKey is required")
	}
	if f.RegisterCount < 1 || f.FlowRegisterOffset >= f.RegisterCount {
		return errors.New("fieldPlant.flowMeter.flowRegisterOffset must be < registerCount")
	}
	if !isFinitePositive(f.FlowScale) || !isFinitePositive(f.FlowDivisor) {
		return errors.New("fieldPlant.flowMeter flowScale and flowDivisor must be finite > 0")
	}
	if f.VelocityRegisterOffset != nil {
		if *f.VelocityRegisterOffset >= f.RegisterCount {
			return errors.New("fieldPlant.flowMeter.velocityRegisterOffset must be < registerCount")
		}
		if !isFinitePositive(f.VelocityScale) || !isFinitePositive(f.VelocityDivisor) {
			return errors.New("fieldPlant.flowMeter velocityScale and velocityDivisor must be finite > 0")
		}
	}
	if f.PumpMirror != nil {
		if f.PumpMirror.DeviceKey == "" || f.PumpMirror.FlowPointCode == "" {
			return errors.New("fieldPlant.flowMeter.pumpMirror deviceKey and flowPointCode are required")
		}
		if !isFiniteNonNeg(f.PumpMirror.RunningThreshold) {
			return errors.New("fieldPlant.flowMeter.pumpMirror.runningThreshold must be finite >= 0")
		}
	}
	return nil
}

func validateConcentratorPollerConfig(p *ConcentratorPollerConfig) error {
	if p.PollIntervalSec < 1 || p.PollIntervalSec > 86400 {
		return fmt.Errorf("fieldPlant.concentrator.pollIntervalSec %d must be in [1,86400]", p.PollIntervalSec)
	}
	if p.UnitID < 1 || p.UnitID > 247 {
		return fmt.Errorf("fieldPlant.concentrator.unitId %d must be in [1,247]", p.UnitID)
	}
	if len(p.ActiveChannels) == 0 {
		return errors.New("fieldPlant.concentrator.activeChannels must not be empty")
	}
	seenCh := make(map[int]struct{}, len(p.ActiveChannels))
	for _, ch := range p.ActiveChannels {
		if ch < 1 || ch > concentratorEIDSlotCount {
			return fmt.Errorf("fieldPlant.concentrator.activeChannels channel %d must be in [1,%d]", ch, concentratorEIDSlotCount)
		}
		if _, dup := seenCh[ch]; dup {
			return fmt.Errorf("fieldPlant.concentrator.activeChannels duplicate channel %d", ch)
		}
		seenCh[ch] = struct{}{}
	}
	if p.LowBatteryMV < 0 || p.LowBatteryMV > 10000 {
		return fmt.Errorf("fieldPlant.concentrator.lowBatteryMv %d must be in [0,10000]", p.LowBatteryMV)
	}
	seenSrc := make(map[string]struct{}, len(p.CopyRoutes))
	for _, route := range p.CopyRoutes {
		src := strings.TrimSpace(route.SourceKey)
		if src == "" {
			return errors.New("fieldPlant.concentrator.copyRoutes[].sourceKey is required")
		}
		if _, dup := seenSrc[src]; dup {
			return fmt.Errorf("fieldPlant.concentrator.copyRoutes duplicate sourceKey %q", src)
		}
		seenSrc[src] = struct{}{}
		if len(route.Targets) == 0 {
			return fmt.Errorf("fieldPlant.concentrator.copyRoutes[%q].targets must not be empty", src)
		}
		for _, tgt := range route.Targets {
			if strings.TrimSpace(tgt.DeviceKey) == "" || strings.TrimSpace(tgt.PointCode) == "" {
				return fmt.Errorf("fieldPlant.concentrator.copyRoutes[%q] targets require deviceKey and pointCode", src)
			}
		}
	}
	return nil
}

func validateBTUConfig(b *BTUCalculatorConfig, hasConc, hasFlow, hasMeters bool) error {
	if !hasConc || !hasFlow || !hasMeters {
		return errors.New("fieldPlant.btuCalculator requires concentrator, flowMeter, and meters to be configured")
	}
	if b.DeviceKey == "" || b.SupplyTempKey == "" || b.ReturnTempKey == "" {
		return errors.New("fieldPlant.btuCalculator requires deviceKey, supplyTempKey, and returnTempKey")
	}
	if !isFinitePositive(b.SpecificHeatFactor) {
		return errors.New("fieldPlant.btuCalculator.specificHeatFactor must be finite > 0")
	}
	if !isFiniteNonNeg(b.MinPlantPowerKW) {
		return errors.New("fieldPlant.btuCalculator.minPlantPowerKw must be finite >= 0")
	}
	if b.IntegrationMaxGapSec < 1 || b.IntegrationMaxGapSec > 86400 {
		return fmt.Errorf("fieldPlant.btuCalculator.integrationMaxGapSec %d must be in [1,86400]", b.IntegrationMaxGapSec)
	}
	return nil
}

func validateDOControlConfig(d *DOControlConfig, hasMeters bool, meterKeys map[string]struct{}) error {
	if d.DeviceKey == "" {
		return errors.New("fieldPlant.doControl.deviceKey is required")
	}
	if d.WinnerControllerID == "" {
		return errors.New("fieldPlant.doControl.winnerControllerId is required")
	}
	if d.UnitID < 1 || d.UnitID > 247 {
		return fmt.Errorf("fieldPlant.doControl.unitId %d must be in [1,247]", d.UnitID)
	}
	if d.PollIntervalSec < 1 || d.PollIntervalSec > 86400 {
		return fmt.Errorf("fieldPlant.doControl.pollIntervalSec %d must be in [1,86400]", d.PollIntervalSec)
	}
	if d.PulseDurationMS < 50 || d.PulseDurationMS > 10000 {
		return fmt.Errorf("fieldPlant.doControl.pulseDurationMs %d must be in [50,10000]", d.PulseDurationMS)
	}
	if !isFiniteNonNeg(d.StopPowerKW()) {
		return errors.New("fieldPlant.doControl.chillerStopPowerKw must be finite >= 0")
	}
	if d.Timings != nil {
		for label, ms := range map[string]int{
			"auxStartStepDelayMs":    d.Timings.AuxStartStepDelayMS,
			"preStartPulseDelayMs":   d.Timings.PreStartPulseDelayMS,
			"chillerStopCoastDownMs": d.Timings.ChillerStopCoastDownMS,
			"auxiliaryStopStepMs":    d.Timings.AuxiliaryStopStepMS,
		} {
			if ms < 0 || ms > 3600000 {
				return fmt.Errorf("fieldPlant.doControl.timings.%s %d must be in [0,3600000]", label, ms)
			}
		}
	}
	if len(d.Coils) == 0 {
		return errors.New("fieldPlant.doControl.coils must not be empty")
	}
	coilByChannel := make(map[string]uint16, len(d.Coils))
	seenCoil := make(map[uint16]struct{}, len(d.Coils))
	for _, c := range d.Coils {
		ch := strings.TrimSpace(c.Channel)
		if ch == "" {
			return errors.New("fieldPlant.doControl.coils[].channel is required")
		}
		if _, dup := coilByChannel[ch]; dup {
			return fmt.Errorf("fieldPlant.doControl.coils duplicate channel %q", ch)
		}
		if _, dup := seenCoil[c.Coil]; dup {
			return fmt.Errorf("fieldPlant.doControl.coils duplicate coil %d", c.Coil)
		}
		coilByChannel[ch] = c.Coil
		seenCoil[c.Coil] = struct{}{}
	}
	checkPulse := func(label string, p DOPulseConfig) error {
		ch := strings.TrimSpace(p.Channel)
		coil, ok := coilByChannel[ch]
		if !ok || coil != p.Coil {
			return fmt.Errorf("%s (%q, coil %d) does not match fieldPlant.doControl.coils", label, ch, p.Coil)
		}
		return nil
	}

	seenUnits := make(map[string]struct{}, len(d.Units))
	seenDevices := map[string]struct{}{d.DeviceKey: {}}
	for _, u := range d.Units {
		unitCode := strings.TrimSpace(u.UnitCode)
		chillerKey := strings.TrimSpace(u.ChillerDeviceKey)
		if unitCode == "" || chillerKey == "" {
			return errors.New("fieldPlant.doControl.units[] requires unitCode and chillerDeviceKey")
		}
		if _, dup := seenUnits[unitCode]; dup {
			return fmt.Errorf("fieldPlant.doControl.units duplicate unitCode %q", unitCode)
		}
		seenUnits[unitCode] = struct{}{}
		if _, dup := seenDevices[chillerKey]; dup {
			return fmt.Errorf("fieldPlant.doControl.units duplicate deviceKey %q", chillerKey)
		}
		seenDevices[chillerKey] = struct{}{}
		if meterKey := strings.TrimSpace(u.MeterDeviceKey); meterKey != "" {
			if !hasMeters {
				return fmt.Errorf("fieldPlant.doControl.units[%q].meterDeviceKey %q requires fieldPlant.meters", unitCode, meterKey)
			}
			if _, ok := meterKeys[meterKey]; !ok {
				return fmt.Errorf("fieldPlant.doControl.units[%q] references unknown meterDeviceKey %q", unitCode, meterKey)
			}
		}
		if len(u.Auxiliaries) == 0 {
			return fmt.Errorf("fieldPlant.doControl.units[%q].auxiliaries must not be empty", unitCode)
		}
		for _, aux := range u.Auxiliaries {
			devKey := strings.TrimSpace(aux.DeviceKey)
			ch := strings.TrimSpace(aux.Channel)
			if devKey == "" || ch == "" {
				return fmt.Errorf("fieldPlant.doControl.units[%q].auxiliaries require deviceKey and channel", unitCode)
			}
			if _, dup := seenDevices[devKey]; dup {
				return fmt.Errorf("fieldPlant.doControl.units duplicate deviceKey %q", devKey)
			}
			seenDevices[devKey] = struct{}{}
			coil, ok := coilByChannel[ch]
			if !ok || coil != aux.Coil {
				return fmt.Errorf("fieldPlant.doControl.units[%q] auxiliary %q (%q, coil %d) does not match coils", unitCode, devKey, ch, aux.Coil)
			}
			if aux.AfterStartDelayMS != nil && (*aux.AfterStartDelayMS < 0 || *aux.AfterStartDelayMS > 3600000) {
				return fmt.Errorf("fieldPlant.doControl.units[%q] auxiliary %q afterStartDelayMs out of [0,3600000]", unitCode, devKey)
			}
		}
		if err := checkPulse("fieldPlant.doControl.units["+unitCode+"].startPulse", u.StartPulse); err != nil {
			return err
		}
		if err := checkPulse("fieldPlant.doControl.units["+unitCode+"].stopPulse", u.StopPulse); err != nil {
			return err
		}
	}
	return nil
}

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isFiniteNonNeg(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// ParseFieldPlantConfig strictly decodes a declarative FieldPlantConfig JSON document.
func ParseFieldPlantConfig(raw []byte) (FieldPlantConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	var fp FieldPlantConfig
	if err := dec.Decode(&fp); err != nil {
		return FieldPlantConfig{}, fmt.Errorf("decode fieldPlant config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return FieldPlantConfig{}, errors.New("fieldPlant config contains trailing JSON")
	}
	return fp, nil
}

// LoadFieldPlantConfigFile reads and strictly decodes a FieldPlantConfig JSON file.
func LoadFieldPlantConfigFile(path string) (FieldPlantConfig, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return FieldPlantConfig{}, errors.New("fieldPlantFile path is required")
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return FieldPlantConfig{}, fmt.Errorf("read fieldPlantFile %s: %w", trimmed, err)
	}
	return ParseFieldPlantConfig(raw)
}
