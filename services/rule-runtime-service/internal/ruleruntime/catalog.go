package ruleruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

type Catalog struct {
	Version     string
	Definitions map[string]NodeDefinition
}

func CoreCatalogV1() Catalog {
	definitions := []NodeDefinition{
		{
			ID: "event_type_filter", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{"match": PortEvent, "no_match": PortEvent}, Deterministic: true, ResourceCost: 1,
			ValidateConfig: validateEventTypeFilterConfig, Evaluate: evaluateEventTypeFilter,
		},
		{
			ID: "json_number", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{"value": PortNumber}, Deterministic: true, ResourceCost: 1,
			ValidateConfig: validateJSONNumberConfig, Evaluate: evaluateJSONNumber,
		},
		{
			ID: "math_number", Version: 1, Inputs: map[string]PortType{"in": PortNumber}, Outputs: map[string]PortType{"value": PortNumber}, Deterministic: true, ResourceCost: 1,
			ValidateConfig: validateMathConfig, Evaluate: evaluateMath,
		},
		{
			ID: "owner_snapshot_read", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{"snapshot": PortSnapshot}, Deterministic: true, ResourceCost: 2,
			RequiredPermission: "owner.snapshot.read", ValidateConfig: validateSnapshotConfig, Evaluate: evaluateSnapshotRead,
		},
		{
			ID: "alarm_intent", Version: 1, Inputs: map[string]PortType{"in": PortSnapshot}, Outputs: map[string]PortType{"intent": PortIntent}, Deterministic: true, ResourceCost: 2,
			RequiredPermission: "alarm.intent.publish", EffectOwner: "ALARM", ValidateConfig: validateAlarmIntentConfig, Evaluate: evaluateAlarmIntent,
		},
		{
			ID: "delay_event", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{"resume": PortEvent}, Deterministic: true, ResourceCost: 1,
			ValidateConfig: validateDelayConfig, Evaluate: evaluateDelay,
		},
		{
			ID: "terminal_event", Version: 1, Inputs: map[string]PortType{"in": PortEvent}, Outputs: map[string]PortType{}, Deterministic: true, ResourceCost: 1,
			Evaluate: evaluateTerminal,
		},
		{
			ID: "terminal_number", Version: 1, Inputs: map[string]PortType{"in": PortNumber}, Outputs: map[string]PortType{}, Deterministic: true, ResourceCost: 1,
			Evaluate: evaluateTerminal,
		},
		{
			ID: "terminal_intent", Version: 1, Inputs: map[string]PortType{"in": PortIntent}, Outputs: map[string]PortType{}, Deterministic: true, ResourceCost: 1,
			Evaluate: evaluateTerminal,
		},
	}
	result := Catalog{Version: "core.v1", Definitions: make(map[string]NodeDefinition, len(definitions))}
	for _, definition := range definitions {
		result.Definitions[definition.ID] = definition
	}
	return result
}

type eventTypeFilterConfig struct {
	Schemas []string `json:"schemas"`
}

func validateEventTypeFilterConfig(raw json.RawMessage) error {
	var config eventTypeFilterConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	if len(config.Schemas) == 0 || len(config.Schemas) > 32 {
		return errors.New("schemas must contain 1..32 values")
	}
	for _, schema := range config.Schemas {
		if schema == "" {
			return errors.New("schema must not be empty")
		}
	}
	return nil
}

func evaluateEventTypeFilter(_ context.Context, _ NodeContext, raw json.RawMessage, input NodeInput) (NodeOutcome, error) {
	var config eventTypeFilterConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	var envelope RuleEventEnvelope
	if err := json.Unmarshal(input.Value.Data, &envelope); err != nil {
		return NodeOutcome{Failure: FailurePoison, FailureCode: "EVENT_ENVELOPE_INVALID"}, nil
	}
	port := "no_match"
	for _, schema := range config.Schemas {
		if envelope.Schema == schema {
			port = "match"
			break
		}
	}
	return NodeOutcome{Outputs: []NodeOutput{{Port: port, Value: input.Value}}}, nil
}

type jsonNumberConfig struct {
	Key string `json:"key"`
}

func validateJSONNumberConfig(raw json.RawMessage) error {
	var config jsonNumberConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	if config.Key == "" {
		return errors.New("key is required")
	}
	return nil
}

func evaluateJSONNumber(_ context.Context, _ NodeContext, raw json.RawMessage, input NodeInput) (NodeOutcome, error) {
	var config jsonNumberConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	var envelope RuleEventEnvelope
	if err := json.Unmarshal(input.Value.Data, &envelope); err != nil {
		return NodeOutcome{Failure: FailurePoison, FailureCode: "EVENT_ENVELOPE_INVALID"}, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return NodeOutcome{Failure: FailurePoison, FailureCode: "EVENT_PAYLOAD_INVALID"}, nil
	}
	value, ok := payload[config.Key]
	if !ok {
		return NodeOutcome{Failure: FailureSchemaUnknown, FailureCode: "NUMBER_KEY_MISSING"}, nil
	}
	var number float64
	if err := json.Unmarshal(value, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return NodeOutcome{Failure: FailurePoison, FailureCode: "NUMBER_VALUE_INVALID"}, nil
	}
	encoded, _ := json.Marshal(number)
	return NodeOutcome{Outputs: []NodeOutput{{Port: "value", Value: TypedValue{Type: PortNumber, Data: encoded}}}}, nil
}

type mathConfig struct {
	Operation string  `json:"operation"`
	Operand   float64 `json:"operand"`
}

func validateMathConfig(raw json.RawMessage) error {
	var config mathConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	switch config.Operation {
	case "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE":
	default:
		return errors.New("operation must be ADD, SUBTRACT, MULTIPLY or DIVIDE")
	}
	if config.Operation == "DIVIDE" && config.Operand == 0 {
		return errors.New("divide operand must not be zero")
	}
	return nil
}

func evaluateMath(_ context.Context, _ NodeContext, raw json.RawMessage, input NodeInput) (NodeOutcome, error) {
	var config mathConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	var value float64
	if err := json.Unmarshal(input.Value.Data, &value); err != nil {
		return NodeOutcome{Failure: FailurePoison, FailureCode: "NUMBER_INPUT_INVALID"}, nil
	}
	switch config.Operation {
	case "ADD":
		value += config.Operand
	case "SUBTRACT":
		value -= config.Operand
	case "MULTIPLY":
		value *= config.Operand
	case "DIVIDE":
		value /= config.Operand
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return NodeOutcome{Failure: FailureValidation, FailureCode: "NUMBER_RESULT_NOT_FINITE"}, nil
	}
	encoded, _ := json.Marshal(value)
	return NodeOutcome{Outputs: []NodeOutput{{Port: "value", Value: TypedValue{Type: PortNumber, Data: encoded}}}}, nil
}

type snapshotConfig struct {
	OwnerDomain string `json:"ownerDomain"`
	Kind        string `json:"kind"`
	Revision    int64  `json:"revision"`
}

func validateSnapshotConfig(raw json.RawMessage) error {
	var config snapshotConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	if config.OwnerDomain == "" || config.Kind == "" || config.Revision <= 0 {
		return errors.New("ownerDomain, kind and positive revision are required")
	}
	return nil
}

func evaluateSnapshotRead(ctx context.Context, nodeContext NodeContext, raw json.RawMessage, _ NodeInput) (NodeOutcome, error) {
	var config snapshotConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	if nodeContext.SnapshotReader == nil {
		return NodeOutcome{Failure: FailureTransient, FailureCode: "SNAPSHOT_READER_UNAVAILABLE"}, nil
	}
	value, err := nodeContext.SnapshotReader.ReadSnapshot(ctx, SnapshotRequest{
		OwnerDomain: config.OwnerDomain, Kind: config.Kind, TenantID: nodeContext.TenantID, SiteID: nodeContext.SiteID,
		SubjectType: nodeContext.SubjectType, SubjectID: nodeContext.SubjectID, Revision: config.Revision,
	})
	if err != nil {
		return NodeOutcome{Failure: FailureTransient, FailureCode: "SNAPSHOT_READ_FAILED"}, nil
	}
	if value.Type != PortSnapshot {
		return NodeOutcome{Failure: FailureSchemaUnknown, FailureCode: "SNAPSHOT_TYPE_INVALID"}, nil
	}
	return NodeOutcome{Outputs: []NodeOutput{{Port: "snapshot", Value: value}}}, nil
}

type alarmIntentConfig struct {
	IntentType string `json:"intentType"`
}

func validateAlarmIntentConfig(raw json.RawMessage) error {
	var config alarmIntentConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	if config.IntentType != "ALARM_CONDITION_OBSERVATION" && config.IntentType != "ALARM_PUBLICATION" {
		return errors.New("unsupported Alarm intent type")
	}
	return nil
}

func evaluateAlarmIntent(_ context.Context, _ NodeContext, raw json.RawMessage, input NodeInput) (NodeOutcome, error) {
	var config alarmIntentConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	return NodeOutcome{
		Outputs: []NodeOutput{{Port: "intent", Value: TypedValue{Type: PortIntent, Data: input.Value.Data}}},
		Effects: []EffectIntent{{OutputPort: "intent", OwnerDomain: "ALARM", IntentType: config.IntentType, Payload: append(json.RawMessage(nil), input.Value.Data...)}},
	}, nil
}

type delayConfig struct {
	DelayMillis int64 `json:"delayMillis"`
}

func validateDelayConfig(raw json.RawMessage) error {
	var config delayConfig
	if err := decodeStrict(raw, &config); err != nil {
		return err
	}
	if config.DelayMillis < 1 || config.DelayMillis > int64((24*time.Hour)/time.Millisecond) {
		return errors.New("delayMillis must be within 1ms..24h")
	}
	return nil
}

func evaluateDelay(_ context.Context, nodeContext NodeContext, raw json.RawMessage, input NodeInput) (NodeOutcome, error) {
	var config delayConfig
	if err := decodeStrict(raw, &config); err != nil {
		return NodeOutcome{}, err
	}
	wakeAt := nodeContext.OccurredAt.Add(time.Duration(config.DelayMillis) * time.Millisecond)
	return NodeOutcome{Continuation: &Continuation{WakeAt: wakeAt, OutputPort: "resume", Payload: append(json.RawMessage(nil), input.Value.Data...)}}, nil
}

func evaluateTerminal(_ context.Context, _ NodeContext, _ json.RawMessage, _ NodeInput) (NodeOutcome, error) {
	return NodeOutcome{}, nil
}

func decodeStrict(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid node config: %w", err)
	}
	return nil
}
