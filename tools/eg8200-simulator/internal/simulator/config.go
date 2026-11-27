package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const ConfigSchemaVersion = 3

type Config struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	GatewayID       string                 `json:"gatewayId"`
	PublishInterval string                 `json:"publishInterval"`
	Scenario        Scenario               `json:"scenario"`
	Plant           PlantConfig            `json:"plant"`
	Spaces          []SpaceConfig          `json:"spaces"`
	Assets          []AssetConfig          `json:"assets"`
	Devices         []DeviceEndpointConfig `json:"devices"`
	Sensors         []SensorConfig         `json:"sensors"`
	Points          []PointConfig          `json:"points"`
}

type PlantConfig struct {
	InitialEnergyKWh  float64             `json:"initialEnergyKwh,omitempty"`
	ThermalLagSeconds int                 `json:"thermalLagSeconds,omitempty"`
	Mismatch          PlantMismatchConfig `json:"mismatch,omitempty"`
	Chiller           ChillerConfig       `json:"chiller"`
	ChilledWaterPump  PumpConfig          `json:"chilledWaterPump"`
	CoolingWaterPump  PumpConfig          `json:"coolingWaterPump"`
	CoolingTower      CoolingTowerConfig  `json:"coolingTower"`
	PowerMeterID      string              `json:"powerMeterId"`
	BTUMeterID        string              `json:"btuMeterId"`
	WeatherStationID  string              `json:"weatherStationId"`
	ZoneSensorID      string              `json:"zoneSensorId"`
}

// PlantMismatchConfig holds optional plant-model mismatch and physical coupling parameters.
// Zero values preserve the legacy NewPlant physics verbatim.
type PlantMismatchConfig struct {
	CHWPStaticHeadFraction  float64 `json:"chwpStaticHeadFraction,omitempty"`
	CWPStaticHeadFraction   float64 `json:"cwpStaticHeadFraction,omitempty"`
	NonlinearTowerApproach  bool    `json:"nonlinearTowerApproach,omitempty"`
	TowerMinApproachC       float64 `json:"towerMinApproachC,omitempty"`
	TowerLoadApproachCoeff  float64 `json:"towerLoadApproachCoeff,omitempty"`
	TowerNonlinearExp       float64 `json:"towerNonlinearExp,omitempty"`
	TowerLowFanPenaltyC     float64 `json:"towerLowFanPenaltyC,omitempty"`
	ChillerCOPMultiplier    float64 `json:"chillerCopMultiplier,omitempty"`
	ChillerCOPOffset        float64 `json:"chillerCopOffset,omitempty"`
	CondenserMeanTempWeight float64 `json:"condenserMeanTempWeight,omitempty"`
	CondenserCOPSlopePerC   float64 `json:"condenserCopSlopePerC,omitempty"`
	HighLoadCOPPenaltyCoeff float64 `json:"highLoadCopPenaltyCoeff,omitempty"`
	CHWCoilFlowCoupling     bool    `json:"chwCoilFlowCoupling,omitempty"`
	CHWCoilApproachMinC     float64 `json:"chwCoilApproachMinC,omitempty"`
	ThermalLoadMultiplier   float64 `json:"thermalLoadMultiplier,omitempty"`
	ThermalLoadBiasKW       float64 `json:"thermalLoadBiasKw,omitempty"`
	CHWSupplySensorBiasC    float64 `json:"chwSupplySensorBiasC,omitempty"`
	ZoneTempSensorBiasC     float64 `json:"zoneTempSensorBiasC,omitempty"`
}

type ChillerConfig struct {
	ID                     string  `json:"id"`
	RatedCoolingCapacityKW float64 `json:"ratedCoolingCapacityKw"`
	BaseCOP                float64 `json:"baseCop"`
	InitialSetpointC       float64 `json:"initialSetpointC"`
	InitialLoadLimitPct    float64 `json:"initialLoadLimitPct"`
	InitiallyRunning       bool    `json:"initiallyRunning"`
}

type PumpConfig struct {
	ID                 string  `json:"id"`
	RatedPowerKW       float64 `json:"ratedPowerKw"`
	RatedFlowM3H       float64 `json:"ratedFlowM3h"`
	InitialFrequencyHz float64 `json:"initialFrequencyHz"`
	InitiallyRunning   bool    `json:"initiallyRunning"`
}

type CoolingTowerConfig struct {
	ID                 string  `json:"id"`
	RatedFanPowerKW    float64 `json:"ratedFanPowerKw"`
	InitialFanSpeedPct float64 `json:"initialFanSpeedPct"`
	InitiallyRunning   bool    `json:"initiallyRunning"`
}

func DecodeConfig(reader io.Reader) (Config, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode simulator config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("simulator config contains trailing JSON")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("unsupported simulator config schemaVersion %d", config.SchemaVersion)
	}
	if strings.TrimSpace(config.GatewayID) == "" {
		return errors.New("gatewayId is required")
	}
	interval, err := time.ParseDuration(config.PublishInterval)
	if err != nil || interval < time.Second || interval > time.Minute {
		return errors.New("publishInterval must be between 1s and 1m")
	}
	if err := config.Scenario.Validate(); err != nil {
		return err
	}
	if err := config.Plant.Validate(); err != nil {
		return err
	}
	if err := validateAssetModel(config); err != nil {
		return err
	}
	return nil
}

func (m PlantMismatchConfig) Validate() error {
	for name, val := range map[string]float64{
		"chwpStaticHeadFraction":  m.CHWPStaticHeadFraction,
		"cwpStaticHeadFraction":   m.CWPStaticHeadFraction,
		"towerMinApproachC":       m.TowerMinApproachC,
		"towerLoadApproachCoeff":  m.TowerLoadApproachCoeff,
		"towerNonlinearExp":       m.TowerNonlinearExp,
		"towerLowFanPenaltyC":     m.TowerLowFanPenaltyC,
		"chillerCopMultiplier":    m.ChillerCOPMultiplier,
		"chillerCopOffset":        m.ChillerCOPOffset,
		"condenserMeanTempWeight": m.CondenserMeanTempWeight,
		"condenserCopSlopePerC":   m.CondenserCOPSlopePerC,
		"highLoadCopPenaltyCoeff": m.HighLoadCOPPenaltyCoeff,
		"chwCoilApproachMinC":     m.CHWCoilApproachMinC,
		"thermalLoadMultiplier":   m.ThermalLoadMultiplier,
		"thermalLoadBiasKw":       m.ThermalLoadBiasKW,
		"chwSupplySensorBiasC":    m.CHWSupplySensorBiasC,
		"zoneTempSensorBiasC":     m.ZoneTempSensorBiasC,
	} {
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return fmt.Errorf("plant mismatch %s must be finite", name)
		}
	}
	if m.CHWPStaticHeadFraction < 0 || m.CHWPStaticHeadFraction > 0.5 {
		return errors.New("plant mismatch chwpStaticHeadFraction must be in [0, 0.5]")
	}
	if m.CWPStaticHeadFraction < 0 || m.CWPStaticHeadFraction > 0.5 {
		return errors.New("plant mismatch cwpStaticHeadFraction must be in [0, 0.5]")
	}
	if m.ChillerCOPMultiplier < 0 || m.ChillerCOPMultiplier > 2.0 {
		return errors.New("plant mismatch chillerCopMultiplier must be in [0, 2.0]")
	}
	if m.CondenserMeanTempWeight < 0 || m.CondenserMeanTempWeight > 1.0 {
		return errors.New("plant mismatch condenserMeanTempWeight must be in [0, 1.0]")
	}
	if m.CondenserCOPSlopePerC < 0 || m.CondenserCOPSlopePerC > 1.0 {
		return errors.New("plant mismatch condenserCopSlopePerC must be in [0, 1.0]")
	}
	if m.HighLoadCOPPenaltyCoeff < 0 || m.HighLoadCOPPenaltyCoeff > 0.5 {
		return errors.New("plant mismatch highLoadCopPenaltyCoeff must be in [0, 0.5]")
	}
	if m.ThermalLoadMultiplier < 0 || m.ThermalLoadMultiplier > 3.0 {
		return errors.New("plant mismatch thermalLoadMultiplier must be in [0, 3.0]")
	}
	if m.TowerNonlinearExp < 0 || m.TowerNonlinearExp > 4.0 {
		return errors.New("plant mismatch towerNonlinearExp must be in [0, 4.0]")
	}
	return nil
}

func (config PlantConfig) Validate() error {
	if config.InitialEnergyKWh < 0 || math.IsNaN(config.InitialEnergyKWh) || math.IsInf(config.InitialEnergyKWh, 0) {
		return errors.New("plant initialEnergyKwh must be a finite non-negative number")
	}
	if config.ThermalLagSeconds != 0 && (config.ThermalLagSeconds < 60 || config.ThermalLagSeconds > 1800) {
		return errors.New("plant thermalLagSeconds must be 0 or between 60 and 1800")
	}
	if err := config.Mismatch.Validate(); err != nil {
		return err
	}
	if config.Chiller.RatedCoolingCapacityKW <= 0 || config.Chiller.BaseCOP < 2 || config.Chiller.BaseCOP > 9 || config.Chiller.InitialSetpointC < 5 || config.Chiller.InitialSetpointC > 12 || config.Chiller.InitialLoadLimitPct < 20 || config.Chiller.InitialLoadLimitPct > 100 {
		return errors.New("chiller config is invalid")
	}
	for name, pump := range map[string]PumpConfig{"chilledWaterPump": config.ChilledWaterPump, "coolingWaterPump": config.CoolingWaterPump} {
		if strings.TrimSpace(pump.ID) == "" || pump.RatedPowerKW <= 0 || pump.RatedFlowM3H <= 0 || pump.InitialFrequencyHz < 20 || pump.InitialFrequencyHz > 50 {
			return fmt.Errorf("%s config is invalid", name)
		}
	}
	if strings.TrimSpace(config.Chiller.ID) == "" || strings.TrimSpace(config.CoolingTower.ID) == "" || strings.TrimSpace(config.PowerMeterID) == "" || strings.TrimSpace(config.BTUMeterID) == "" || strings.TrimSpace(config.WeatherStationID) == "" || strings.TrimSpace(config.ZoneSensorID) == "" || config.CoolingTower.RatedFanPowerKW <= 0 || config.CoolingTower.InitialFanSpeedPct < 20 || config.CoolingTower.InitialFanSpeedPct > 100 {
		return errors.New("plant device config is incomplete")
	}
	seen := map[string]struct{}{}
	for _, deviceID := range config.DeviceIDs() {
		if _, duplicate := seen[deviceID]; duplicate {
			return fmt.Errorf("duplicate device id %s", deviceID)
		}
		seen[deviceID] = struct{}{}
	}
	return nil
}

func (config PlantConfig) DeviceIDs() []string {
	return []string{
		config.Chiller.ID,
		config.ChilledWaterPump.ID,
		config.CoolingWaterPump.ID,
		config.CoolingTower.ID,
		config.PowerMeterID,
		config.BTUMeterID,
		config.WeatherStationID,
		config.ZoneSensorID,
	}
}

func (config Config) Interval() time.Duration {
	interval, _ := time.ParseDuration(config.PublishInterval)
	for _, point := range config.Points {
		sample, _ := time.ParseDuration(point.SampleInterval)
		if sample > 0 && sample < interval {
			interval = sample
		}
	}
	return interval
}

func (config Config) ReportingDeviceIDs() []string {
	ids := make([]string, 0, len(config.Devices))
	for _, device := range config.Devices {
		ids = append(ids, device.ID)
	}
	return ids
}

func (point PointConfig) SampleEvery() time.Duration {
	duration, _ := time.ParseDuration(point.SampleInterval)
	return duration
}

func (point PointConfig) PublishEvery() time.Duration {
	duration, _ := time.ParseDuration(point.PublishInterval)
	return duration
}
