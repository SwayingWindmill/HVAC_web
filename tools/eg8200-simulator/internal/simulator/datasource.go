package simulator

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type PlantDatasourceType string

const (
	PlantDatasourceStatic   PlantDatasourceType = "STATIC"
	PlantDatasourceScenario PlantDatasourceType = "SCENARIO"
)

type PlantInputs struct {
	AmbientDryBulbC float64 `json:"ambientDryBulbC"`
	AmbientWetBulbC float64 `json:"ambientWetBulbC"`
	LoadFraction    float64 `json:"loadFraction"`
}

type PlantScenarioRecord struct {
	Offset string      `json:"offset"`
	Inputs PlantInputs `json:"inputs"`
}

type PlantScenarioConfig struct {
	Name    string                `json:"name"`
	Records []PlantScenarioRecord `json:"records"`
}

type PlantDatasourceConfig struct {
	Type     PlantDatasourceType  `json:"type"`
	Static   *PlantInputs         `json:"static,omitempty"`
	Scenario *PlantScenarioConfig `json:"scenario,omitempty"`
}

type PlantDatasource interface {
	InputsAt(elapsed time.Duration) PlantInputs
}

type staticPlantDatasource struct {
	inputs PlantInputs
}

func (datasource staticPlantDatasource) InputsAt(time.Duration) PlantInputs {
	return datasource.inputs
}

type scenarioPlantDatasource struct {
	records []timedPlantInputs
}

type timedPlantInputs struct {
	offset time.Duration
	inputs PlantInputs
}

func (datasource scenarioPlantDatasource) InputsAt(elapsed time.Duration) PlantInputs {
	selected := datasource.records[0].inputs
	for _, record := range datasource.records[1:] {
		if elapsed < record.offset {
			break
		}
		selected = record.inputs
	}
	return selected
}

func newPlantDatasource(config PlantDatasourceConfig) (PlantDatasource, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	switch config.Type {
	case PlantDatasourceStatic:
		return staticPlantDatasource{inputs: *config.Static}, nil
	case PlantDatasourceScenario:
		records := make([]timedPlantInputs, 0, len(config.Scenario.Records))
		for _, record := range config.Scenario.Records {
			offset, _ := time.ParseDuration(record.Offset)
			records = append(records, timedPlantInputs{offset: offset, inputs: record.Inputs})
		}
		return scenarioPlantDatasource{records: records}, nil
	default:
		return nil, fmt.Errorf("unsupported plant datasource type %q", config.Type)
	}
}

func (config PlantDatasourceConfig) Validate() error {
	switch config.Type {
	case PlantDatasourceStatic:
		if config.Static == nil || config.Scenario != nil {
			return errors.New("STATIC plant datasource requires static inputs only")
		}
		return config.Static.Validate()
	case PlantDatasourceScenario:
		if config.Static != nil || config.Scenario == nil {
			return errors.New("SCENARIO plant datasource requires scenario records only")
		}
		if strings.TrimSpace(config.Scenario.Name) == "" {
			return errors.New("plant scenario name is required")
		}
		if len(config.Scenario.Records) == 0 {
			return errors.New("plant scenario requires at least one record")
		}
		var previous time.Duration
		for index, record := range config.Scenario.Records {
			offset, err := time.ParseDuration(record.Offset)
			if err != nil || offset < 0 || offset > 7*24*time.Hour {
				return fmt.Errorf("plant scenario record %d has invalid offset", index)
			}
			if index == 0 && offset != 0 {
				return errors.New("plant scenario first record must start at 0s")
			}
			if index > 0 && offset <= previous {
				return errors.New("plant scenario record offsets must be strictly increasing")
			}
			if err := record.Inputs.Validate(); err != nil {
				return fmt.Errorf("plant scenario record %d: %w", index, err)
			}
			previous = offset
		}
		return nil
	default:
		return fmt.Errorf("plant datasource type must be %s or %s", PlantDatasourceStatic, PlantDatasourceScenario)
	}
}

func (inputs PlantInputs) Validate() error {
	if inputs.AmbientDryBulbC < -30 || inputs.AmbientDryBulbC > 60 || inputs.AmbientWetBulbC < -40 || inputs.AmbientWetBulbC > inputs.AmbientDryBulbC {
		return errors.New("plant ambient conditions are invalid")
	}
	if inputs.LoadFraction < 0 || inputs.LoadFraction > 1.2 {
		return errors.New("plant loadFraction must be between 0 and 1.2")
	}
	return nil
}
