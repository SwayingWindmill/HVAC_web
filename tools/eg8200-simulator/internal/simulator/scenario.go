package simulator

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
)

const ScenarioSchemaVersion = 1

type ScenarioMode string

const (
	ScenarioModeStatic   ScenarioMode = "STATIC"
	ScenarioModeScenario ScenarioMode = "SCENARIO"
)

type ScenarioInputs struct {
	AmbientDryBulbC float64 `json:"ambientDryBulbC"`
	AmbientWetBulbC float64 `json:"ambientWetBulbC"`
	CoolingLoadKW   float64 `json:"coolingLoadKw"`
}

type ScenarioStep struct {
	Offset time.Duration
	Inputs ScenarioInputs
}

type Scenario struct {
	SchemaVersion int
	Mode          ScenarioMode
	Name          string
	Description   string
	Inputs        *ScenarioInputs
	Steps         []ScenarioStep

	inputsPresent bool
	stepsPresent  bool
}

type scenarioDocumentJSON struct {
	SchemaVersion *int            `json:"schemaVersion"`
	Mode          *ScenarioMode   `json:"mode"`
	Name          string          `json:"name,omitempty"`
	Description   string          `json:"description,omitempty"`
	Inputs        json.RawMessage `json:"inputs"`
	Steps         json.RawMessage `json:"steps"`
}

type scenarioInputsJSON struct {
	AmbientDryBulbC *float64 `json:"ambientDryBulbC"`
	AmbientWetBulbC *float64 `json:"ambientWetBulbC"`
	CoolingLoadKW   *float64 `json:"coolingLoadKw"`
}

type scenarioStepJSON struct {
	Offset *string         `json:"offset"`
	Inputs json.RawMessage `json:"inputs"`
}

func DecodeScenarioJSON(reader io.Reader) (Scenario, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var scenario Scenario
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Scenario{}, errors.New("scenario contains trailing JSON")
	}
	return scenario, nil
}

func DecodeScenarioCSV(reader io.Reader) (Scenario, error) {
	csvReader := csv.NewReader(io.LimitReader(reader, 1<<20))
	csvReader.FieldsPerRecord = 4
	records, err := csvReader.ReadAll()
	if err != nil {
		return Scenario{}, fmt.Errorf("decode scenario CSV: %w", err)
	}
	if len(records) == 0 {
		return Scenario{}, errors.New("scenario CSV header is required")
	}
	wantHeader := []string{"offset", "ambientDryBulbC", "ambientWetBulbC", "coolingLoadKw"}
	for index := range wantHeader {
		if records[0][index] != wantHeader[index] {
			return Scenario{}, errors.New("scenario CSV header must be offset,ambientDryBulbC,ambientWetBulbC,coolingLoadKw")
		}
	}

	steps := make([]ScenarioStep, 0, len(records)-1)
	for rowIndex, record := range records[1:] {
		offset, err := time.ParseDuration(record[0])
		if err != nil {
			return Scenario{}, fmt.Errorf("scenario CSV row %d offset is invalid: %w", rowIndex+2, err)
		}
		inputs, err := parseScenarioCSVInputs(record[1:])
		if err != nil {
			return Scenario{}, fmt.Errorf("scenario CSV row %d: %w", rowIndex+2, err)
		}
		steps = append(steps, ScenarioStep{Offset: offset, Inputs: inputs})
	}

	scenario := Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		Mode:          ScenarioModeScenario,
		Steps:         steps,
		stepsPresent:  true,
	}
	if err := scenario.Validate(); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

func (scenario *Scenario) UnmarshalJSON(data []byte) error {
	var document scenarioDocumentJSON
	if err := decodeStrictJSON(data, &document); err != nil {
		return err
	}
	if document.SchemaVersion == nil {
		return errors.New("scenario schemaVersion is required")
	}
	if document.Mode == nil {
		return errors.New("scenario mode is required")
	}

	decoded := Scenario{
		SchemaVersion: *document.SchemaVersion,
		Mode:          *document.Mode,
		Name:          document.Name,
		Description:   document.Description,
		inputsPresent: document.Inputs != nil,
		stepsPresent:  document.Steps != nil,
	}
	if decoded.inputsPresent {
		inputs, err := decodeScenarioInputs(document.Inputs)
		if err != nil {
			return err
		}
		decoded.Inputs = &inputs
	}
	if decoded.stepsPresent {
		steps, err := decodeScenarioSteps(document.Steps)
		if err != nil {
			return err
		}
		decoded.Steps = steps
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*scenario = decoded
	return nil
}

func (scenario Scenario) MarshalJSON() ([]byte, error) {
	type scenarioStepOutput struct {
		Offset string         `json:"offset"`
		Inputs ScenarioInputs `json:"inputs"`
	}
	type scenarioOutput struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Mode          ScenarioMode         `json:"mode"`
		Name          string               `json:"name,omitempty"`
		Description   string               `json:"description,omitempty"`
		Inputs        *ScenarioInputs      `json:"inputs,omitempty"`
		Steps         []scenarioStepOutput `json:"steps,omitempty"`
	}

	output := scenarioOutput{
		SchemaVersion: scenario.SchemaVersion,
		Mode:          scenario.Mode,
		Name:          scenario.Name,
		Description:   scenario.Description,
		Inputs:        scenario.Inputs,
	}
	if scenario.Mode == ScenarioModeScenario {
		output.Steps = make([]scenarioStepOutput, 0, len(scenario.Steps))
		for _, step := range scenario.Steps {
			output.Steps = append(output.Steps, scenarioStepOutput{Offset: step.Offset.String(), Inputs: step.Inputs})
		}
	}
	return json.Marshal(output)
}

func (scenario Scenario) Validate() error {
	if scenario.SchemaVersion != ScenarioSchemaVersion {
		return fmt.Errorf("unsupported scenario schemaVersion %d", scenario.SchemaVersion)
	}
	inputsPresent := scenario.inputsPresent || scenario.Inputs != nil
	stepsPresent := scenario.stepsPresent || scenario.Steps != nil

	switch scenario.Mode {
	case ScenarioModeStatic:
		if !inputsPresent || scenario.Inputs == nil {
			return errors.New("STATIC scenario inputs are required")
		}
		if stepsPresent {
			return errors.New("STATIC scenario must not contain steps")
		}
		return scenario.Inputs.Validate()
	case ScenarioModeScenario:
		if inputsPresent {
			return errors.New("SCENARIO must not contain top-level inputs")
		}
		if !stepsPresent || len(scenario.Steps) == 0 {
			return errors.New("SCENARIO steps are required")
		}
		if scenario.Steps[0].Offset != 0 {
			return errors.New("SCENARIO first step must start at 0s")
		}
		for index, step := range scenario.Steps {
			if index > 0 && step.Offset <= scenario.Steps[index-1].Offset {
				return errors.New("SCENARIO step offsets must be strictly increasing")
			}
			if err := step.Inputs.Validate(); err != nil {
				return fmt.Errorf("SCENARIO step %d inputs: %w", index, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported scenario mode %q", scenario.Mode)
	}
}

func (inputs ScenarioInputs) Validate() error {
	for name, value := range map[string]float64{
		"ambientDryBulbC": inputs.AmbientDryBulbC,
		"ambientWetBulbC": inputs.AmbientWetBulbC,
		"coolingLoadKw":   inputs.CoolingLoadKW,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s must be finite", name)
		}
	}
	if inputs.AmbientWetBulbC > inputs.AmbientDryBulbC {
		return errors.New("ambientWetBulbC must not exceed ambientDryBulbC")
	}
	if inputs.CoolingLoadKW < 0 {
		return errors.New("coolingLoadKw must be non-negative")
	}
	return nil
}

func (scenario Scenario) InputsAt(elapsed time.Duration) ScenarioInputs {
	if scenario.Mode == ScenarioModeStatic {
		return *scenario.Inputs
	}
	inputs := scenario.Steps[0].Inputs
	for _, step := range scenario.Steps[1:] {
		if elapsed < step.Offset {
			break
		}
		inputs = step.Inputs
	}
	return inputs
}

func (scenario Scenario) nextTransitionAfter(elapsed time.Duration) (time.Duration, bool) {
	if scenario.Mode != ScenarioModeScenario {
		return 0, false
	}
	for _, step := range scenario.Steps[1:] {
		if step.Offset > elapsed {
			return step.Offset, true
		}
	}
	return 0, false
}

func decodeScenarioInputs(data []byte) (ScenarioInputs, error) {
	var inputJSON scenarioInputsJSON
	if err := decodeStrictJSON(data, &inputJSON); err != nil {
		return ScenarioInputs{}, fmt.Errorf("decode scenario inputs: %w", err)
	}
	if inputJSON.AmbientDryBulbC == nil || inputJSON.AmbientWetBulbC == nil || inputJSON.CoolingLoadKW == nil {
		return ScenarioInputs{}, errors.New("scenario inputs require ambientDryBulbC, ambientWetBulbC and coolingLoadKw")
	}
	inputs := ScenarioInputs{
		AmbientDryBulbC: *inputJSON.AmbientDryBulbC,
		AmbientWetBulbC: *inputJSON.AmbientWetBulbC,
		CoolingLoadKW:   *inputJSON.CoolingLoadKW,
	}
	if err := inputs.Validate(); err != nil {
		return ScenarioInputs{}, err
	}
	return inputs, nil
}

func decodeScenarioSteps(data []byte) ([]ScenarioStep, error) {
	var stepJSON []scenarioStepJSON
	if err := decodeStrictJSON(data, &stepJSON); err != nil {
		return nil, fmt.Errorf("decode scenario steps: %w", err)
	}
	steps := make([]ScenarioStep, 0, len(stepJSON))
	for index, rawStep := range stepJSON {
		if rawStep.Offset == nil {
			return nil, fmt.Errorf("SCENARIO step %d offset is required", index)
		}
		if rawStep.Inputs == nil {
			return nil, fmt.Errorf("SCENARIO step %d inputs are required", index)
		}
		offset, err := time.ParseDuration(*rawStep.Offset)
		if err != nil {
			return nil, fmt.Errorf("SCENARIO step %d offset is invalid: %w", index, err)
		}
		inputs, err := decodeScenarioInputs(rawStep.Inputs)
		if err != nil {
			return nil, fmt.Errorf("SCENARIO step %d: %w", index, err)
		}
		steps = append(steps, ScenarioStep{Offset: offset, Inputs: inputs})
	}
	return steps, nil
}

func parseScenarioCSVInputs(values []string) (ScenarioInputs, error) {
	parsed := make([]float64, len(values))
	for index, value := range values {
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return ScenarioInputs{}, fmt.Errorf("input %d is not a number: %w", index+1, err)
		}
		parsed[index] = number
	}
	inputs := ScenarioInputs{AmbientDryBulbC: parsed[0], AmbientWetBulbC: parsed[1], CoolingLoadKW: parsed[2]}
	if err := inputs.Validate(); err != nil {
		return ScenarioInputs{}, err
	}
	return inputs, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}
