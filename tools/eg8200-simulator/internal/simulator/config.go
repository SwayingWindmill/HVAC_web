package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const ConfigSchemaVersion = 1

type Config struct {
	SchemaVersion      int               `json:"schemaVersion"`
	GatewayID          string            `json:"gatewayId"`
	ThingsBoardBaseURL string            `json:"thingsBoardBaseUrl"`
	PublishInterval    string            `json:"publishInterval"`
	Plant              PlantConfig       `json:"plant"`
	Credentials        map[string]string `json:"credentialEnvByDeviceId"`
}

type PlantConfig struct {
	AmbientDryBulbC  float64            `json:"ambientDryBulbC"`
	AmbientWetBulbC  float64            `json:"ambientWetBulbC"`
	LoadFraction     float64            `json:"loadFraction"`
	Chiller          ChillerConfig      `json:"chiller"`
	ChilledWaterPump PumpConfig         `json:"chilledWaterPump"`
	CoolingWaterPump PumpConfig         `json:"coolingWaterPump"`
	CoolingTower     CoolingTowerConfig `json:"coolingTower"`
	PowerMeterID     string             `json:"powerMeterId"`
	BTUMeterID       string             `json:"btuMeterId"`
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
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.ThingsBoardBaseURL), "/"))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return errors.New("thingsBoardBaseUrl must be an HTTP(S) origin")
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return errors.New("thingsBoardBaseUrl must not contain a path")
	}
	if baseURL.Scheme == "http" && !localThingsBoardHost(baseURL.Hostname()) {
		return errors.New("thingsBoardBaseUrl must use HTTPS for non-local hosts")
	}
	interval, err := time.ParseDuration(config.PublishInterval)
	if err != nil || interval < time.Second || interval > time.Minute {
		return errors.New("publishInterval must be between 1s and 1m")
	}
	if err := config.Plant.Validate(); err != nil {
		return err
	}
	deviceIDs := config.Plant.DeviceIDs()
	if len(config.Credentials) != len(deviceIDs) {
		return errors.New("credentialEnvByDeviceId must contain every simulated device exactly once")
	}
	for _, deviceID := range deviceIDs {
		envName := strings.TrimSpace(config.Credentials[deviceID])
		if envName == "" {
			return fmt.Errorf("credential environment variable is missing for %s", deviceID)
		}
	}
	return nil
}

func (config PlantConfig) Validate() error {
	if config.AmbientDryBulbC < -30 || config.AmbientDryBulbC > 60 || config.AmbientWetBulbC < -40 || config.AmbientWetBulbC > config.AmbientDryBulbC {
		return errors.New("plant ambient conditions are invalid")
	}
	if config.LoadFraction < 0 || config.LoadFraction > 1.2 {
		return errors.New("plant loadFraction must be between 0 and 1.2")
	}
	if config.Chiller.RatedCoolingCapacityKW <= 0 || config.Chiller.BaseCOP < 2 || config.Chiller.BaseCOP > 9 || config.Chiller.InitialSetpointC < 5 || config.Chiller.InitialSetpointC > 12 || config.Chiller.InitialLoadLimitPct < 20 || config.Chiller.InitialLoadLimitPct > 100 {
		return errors.New("chiller config is invalid")
	}
	for name, pump := range map[string]PumpConfig{"chilledWaterPump": config.ChilledWaterPump, "coolingWaterPump": config.CoolingWaterPump} {
		if strings.TrimSpace(pump.ID) == "" || pump.RatedPowerKW <= 0 || pump.RatedFlowM3H <= 0 || pump.InitialFrequencyHz < 20 || pump.InitialFrequencyHz > 50 {
			return fmt.Errorf("%s config is invalid", name)
		}
	}
	if strings.TrimSpace(config.Chiller.ID) == "" || strings.TrimSpace(config.CoolingTower.ID) == "" || strings.TrimSpace(config.PowerMeterID) == "" || strings.TrimSpace(config.BTUMeterID) == "" || config.CoolingTower.RatedFanPowerKW <= 0 || config.CoolingTower.InitialFanSpeedPct < 20 || config.CoolingTower.InitialFanSpeedPct > 100 {
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
	}
}

func (config Config) Interval() time.Duration {
	interval, _ := time.ParseDuration(config.PublishInterval)
	return interval
}

func localThingsBoardHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "host.docker.internal":
		return true
	default:
		return false
	}
}
