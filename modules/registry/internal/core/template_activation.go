package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// TemplatePayloadActivation captures validated optional business-activation sections
// inside a released TemplateRevision payload. Legacy payloads (for example ATV630
// parameter mappings without these keys) continue to release cleanly when absent.
type TemplatePayloadActivation struct {
	DeviceType           string
	StandardPoints       []TemplateStandardPoint
	QualityChecksByPoint map[string]*TemplateQualityCheck
	DefaultQualityCheck  *TemplateQualityCheck
	CapabilityProfiles   []TemplateCapabilityProfile
	ModbusDriver         *TemplateModbusDriver
}

type TemplateStandardPoint struct {
	PointCode              string
	SourceKey              string
	DisplayName            string
	PointType              string
	ValueType              string
	Unit                   *string
	Writable               bool
	SampleIntervalMS       int
	PublishIntervalMS      int
	StaleAfterMS           int
	CounterDecreaseMode    *string
	CounterRolloverModulus *float64
	SourceMetadata         map[string]any
	MinimumNumber          *float64
	MaximumNumber          *float64
	QualityChecks          *TemplateQualityCheck
}

type TemplateQualityCheck struct {
	MinimumNumber            *float64  `json:"minimumNumber,omitempty"`
	MaximumNumber            *float64  `json:"maximumNumber,omitempty"`
	MaxDeltaPerMinute        *float64  `json:"maxDeltaPerMinute,omitempty"`
	UnchangedAlertSeconds    *int      `json:"unchangedAlertSeconds,omitempty"`
	MaxStep                  *float64  `json:"maxStep,omitempty"`
	MaxStepWindowSeconds     *int      `json:"maxStepWindowSeconds,omitempty"`
	FlatlineSeconds          *int      `json:"flatlineSeconds,omitempty"`
	FlatlineRunningSeconds   *int      `json:"flatlineRunningSeconds,omitempty"`
	FlatlineStabilizeSeconds *int      `json:"flatlineStabilizeSeconds,omitempty"`
	Sentinels                []float64 `json:"sentinels,omitempty"`
	Copied                   *bool     `json:"copied,omitempty"`
}

type TemplateCapabilityProfile struct {
	CapabilityName     string  `json:"capabilityName"`
	CapabilityRevision string  `json:"capabilityRevision"`
	Status             string  `json:"status"`
	CanonicalUnit      string  `json:"canonicalUnit"`
	MinimumValue       float64 `json:"minimumValue"`
	MaximumValue       float64 `json:"maximumValue"`
	MaximumDelta       float64 `json:"maximumDelta"`
	RiskLevel          string  `json:"riskLevel"`
	ApprovalPolicy     string  `json:"approvalPolicy"`
	RetryPolicy        string  `json:"retryPolicy"`
	ConnectorKind      string  `json:"connectorKind"`
}

type templateStandardPointJSON struct {
	PointCode              string                `json:"pointCode"`
	SourceKey              string                `json:"sourceKey,omitempty"`
	DisplayName            string                `json:"displayName"`
	PointType              string                `json:"pointType"`
	ValueType              string                `json:"valueType"`
	Unit                   *string               `json:"unit,omitempty"`
	Writable               *bool                 `json:"writable,omitempty"`
	SampleIntervalMS       int                   `json:"sampleIntervalMs"`
	PublishIntervalMS      int                   `json:"publishIntervalMs"`
	StaleAfterMS           int                   `json:"staleAfterMs"`
	CounterDecreaseMode    *string               `json:"counterDecreaseMode,omitempty"`
	CounterRolloverModulus *float64              `json:"counterRolloverModulus,omitempty"`
	SourceMetadata         map[string]any        `json:"sourceMetadata,omitempty"`
	MinimumNumber          *float64              `json:"minimumNumber,omitempty"`
	MaximumNumber          *float64              `json:"maximumNumber,omitempty"`
	QualityChecks          *TemplateQualityCheck `json:"qualityChecks,omitempty"`
}

var qualityCheckDirectKeys = map[string]struct{}{
	"minimumNumber":            {},
	"maximumNumber":            {},
	"maxDeltaPerMinute":        {},
	"unchangedAlertSeconds":    {},
	"maxStep":                  {},
	"maxStepWindowSeconds":     {},
	"flatlineSeconds":          {},
	"flatlineRunningSeconds":   {},
	"flatlineStabilizeSeconds": {},
	"sentinels":                {},
	"copied":                   {},
}

func validateTemplateReleasePayload(kind TemplateKind, payload map[string]any) error {
	_, err := parseAndValidateTemplatePayload(kind, payload)
	return err
}

func parseAndValidateTemplatePayload(kind TemplateKind, payload map[string]any) (*TemplatePayloadActivation, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	_, hasDeviceType := payload["deviceType"]
	_, hasPoints := payload["standardPoints"]
	_, hasQuality := payload["qualityChecks"]
	_, hasCapabilities := payload["capabilityProfiles"]
	_, hasModbusDriver := payload["modbusDriver"]
	_, hasDriver := payload["driver"]
	if !hasDeviceType && !hasPoints && !hasQuality && !hasCapabilities && !hasModbusDriver && !hasDriver {
		return nil, nil
	}
	if hasModbusDriver && hasDriver {
		return nil, fmt.Errorf("%w: template payload cannot specify both driver and modbusDriver", ErrInvalidMutation)
	}

	out := &TemplatePayloadActivation{
		QualityChecksByPoint: map[string]*TemplateQualityCheck{},
	}

	if rawDeviceType, exists := payload["deviceType"]; exists {
		value, ok := rawDeviceType.(string)
		if !ok {
			return nil, fmt.Errorf("%w: deviceType must be a string", ErrInvalidMutation)
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed != value || len(trimmed) > 128 || containsControlChar(trimmed) {
			return nil, fmt.Errorf("%w: invalid template deviceType", ErrInvalidMutation)
		}
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: deviceType is only valid on DEVICE templates", ErrInvalidMutation)
		}
		out.DeviceType = trimmed
	}

	pointValueTypes := map[string]string{}
	if rawPoints, exists := payload["standardPoints"]; exists {
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: standardPoints is only valid on DEVICE templates", ErrInvalidMutation)
		}
		items, ok := rawPoints.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: standardPoints must be an array", ErrInvalidMutation)
		}
		seenCodes := make(map[string]struct{}, len(items))
		out.StandardPoints = make([]TemplateStandardPoint, 0, len(items))
		for index, item := range items {
			var parsed templateStandardPointJSON
			if err := decodeStrictJSONValue(item, &parsed); err != nil {
				return nil, fmt.Errorf("%w: standardPoints[%d]: %v", ErrInvalidMutation, index, err)
			}
			pt, err := validateStandardPoint(parsed)
			if err != nil {
				return nil, fmt.Errorf("%w: standardPoints[%d]: %w", ErrInvalidMutation, index, err)
			}
			if _, duplicate := seenCodes[pt.PointCode]; duplicate {
				return nil, fmt.Errorf("%w: duplicate standardPoints pointCode %q", ErrInvalidMutation, pt.PointCode)
			}
			seenCodes[pt.PointCode] = struct{}{}
			pointValueTypes[pt.PointCode] = pt.ValueType
			out.StandardPoints = append(out.StandardPoints, pt)
		}
	}

	if rawQuality, exists := payload["qualityChecks"]; exists {
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: qualityChecks is only valid on DEVICE templates", ErrInvalidMutation)
		}
		if err := parseTemplateQualityChecks(rawQuality, pointValueTypes, hasPoints, out); err != nil {
			return nil, err
		}
	}

	if rawCaps, exists := payload["capabilityProfiles"]; exists {
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: capabilityProfiles is only valid on DEVICE templates", ErrInvalidMutation)
		}
		items, ok := rawCaps.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: capabilityProfiles must be an array", ErrInvalidMutation)
		}
		seen := make(map[string]struct{}, len(items))
		out.CapabilityProfiles = make([]TemplateCapabilityProfile, 0, len(items))
		for index, item := range items {
			var profile TemplateCapabilityProfile
			if err := decodeStrictJSONValue(item, &profile); err != nil {
				return nil, fmt.Errorf("%w: capabilityProfiles[%d]: %v", ErrInvalidMutation, index, err)
			}
			if err := validateTemplateCapabilityProfile(profile); err != nil {
				return nil, fmt.Errorf("%w: capabilityProfiles[%d]: %v", ErrInvalidMutation, index, err)
			}
			pairKey := profile.CapabilityName + "\x00" + profile.CapabilityRevision
			if _, duplicate := seen[pairKey]; duplicate {
				return nil, fmt.Errorf("%w: duplicate capabilityProfile %s@%s", ErrInvalidMutation, profile.CapabilityName, profile.CapabilityRevision)
			}
			seen[pairKey] = struct{}{}
			out.CapabilityProfiles = append(out.CapabilityProfiles, profile)
		}
	}

	if rawModbusDriver, exists := payload["modbusDriver"]; exists {
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: modbusDriver is only valid on DEVICE templates", ErrInvalidMutation)
		}
		driver, err := validateTemplateDriver(rawModbusDriver, false)
		if err != nil {
			return nil, fmt.Errorf("%w: modbusDriver: %w", ErrInvalidMutation, err)
		}
		out.ModbusDriver = driver
	}
	if rawDriver, exists := payload["driver"]; exists {
		if kind != TemplateDevice {
			return nil, fmt.Errorf("%w: driver is only valid on DEVICE templates", ErrInvalidMutation)
		}
		driver, err := validateTemplateDriver(rawDriver, true)
		if err != nil {
			return nil, fmt.Errorf("%w: driver: %w", ErrInvalidMutation, err)
		}
		out.ModbusDriver = driver
	}

	return out, nil
}

func validateStandardPoint(parsed templateStandardPointJSON) (TemplateStandardPoint, error) {
	if !pointCodePattern.MatchString(parsed.PointCode) {
		return TemplateStandardPoint{}, fmt.Errorf("invalid pointCode %q", parsed.PointCode)
	}
	if parsed.SourceKey != "" && !sourceKeyPattern.MatchString(parsed.SourceKey) {
		return TemplateStandardPoint{}, fmt.Errorf("invalid sourceKey %q", parsed.SourceKey)
	}
	display := strings.TrimSpace(parsed.DisplayName)
	if display == "" || display != parsed.DisplayName || len(display) > 256 || containsControlChar(display) {
		return TemplateStandardPoint{}, errors.New("invalid displayName")
	}
	switch parsed.PointType {
	case "TELEMETRY", "COUNTER", "STATE", "SETTING", "COMMAND":
	default:
		return TemplateStandardPoint{}, fmt.Errorf("unsupported pointType %q", parsed.PointType)
	}
	switch parsed.ValueType {
	case "BOOLEAN", "NUMBER", "STRING", "JSON":
	default:
		return TemplateStandardPoint{}, fmt.Errorf("unsupported valueType %q", parsed.ValueType)
	}
	var unit *string
	if parsed.Unit != nil {
		trimmed := strings.TrimSpace(*parsed.Unit)
		if trimmed == "" || len(trimmed) > 64 || containsControlChar(trimmed) {
			return TemplateStandardPoint{}, errors.New("invalid unit")
		}
		unit = &trimmed
	}
	writable := parsed.PointType == "COMMAND"
	if parsed.Writable != nil {
		writable = *parsed.Writable
	}
	if parsed.PointType == "COMMAND" && !writable || parsed.PointType != "COMMAND" && parsed.PointType != "SETTING" && writable {
		return TemplateStandardPoint{}, errors.New("writable contradicts pointType")
	}
	if parsed.SampleIntervalMS < 100 || parsed.SampleIntervalMS > 86400000 ||
		parsed.PublishIntervalMS < 100 || parsed.PublishIntervalMS > 86400000 ||
		parsed.StaleAfterMS < 100 || parsed.StaleAfterMS > 604800000 ||
		parsed.PublishIntervalMS < parsed.SampleIntervalMS || parsed.StaleAfterMS < parsed.PublishIntervalMS {
		return TemplateStandardPoint{}, errors.New("invalid telemetry sampling/publish/stale intervals")
	}
	if parsed.PointType != "COUNTER" {
		if parsed.CounterDecreaseMode != nil || parsed.CounterRolloverModulus != nil {
			return TemplateStandardPoint{}, errors.New("counter semantics require pointType COUNTER")
		}
	} else {
		if parsed.ValueType != "NUMBER" || parsed.CounterDecreaseMode == nil {
			return TemplateStandardPoint{}, errors.New("COUNTER points require NUMBER valueType and counterDecreaseMode")
		}
		switch *parsed.CounterDecreaseMode {
		case "RESET_TO_ZERO", "INVALID":
			if parsed.CounterRolloverModulus != nil {
				return TemplateStandardPoint{}, errors.New("counterRolloverModulus only allowed for ROLLOVER")
			}
		case "ROLLOVER":
			if parsed.CounterRolloverModulus == nil || !isFinitePositive(*parsed.CounterRolloverModulus) {
				return TemplateStandardPoint{}, errors.New("ROLLOVER requires positive counterRolloverModulus")
			}
		default:
			return TemplateStandardPoint{}, fmt.Errorf("unsupported counterDecreaseMode %q", *parsed.CounterDecreaseMode)
		}
	}
	var inlineQC *TemplateQualityCheck
	if parsed.QualityChecks != nil {
		copyQC := *parsed.QualityChecks
		inlineQC = &copyQC
	}
	if parsed.MinimumNumber != nil || parsed.MaximumNumber != nil {
		if inlineQC == nil {
			inlineQC = &TemplateQualityCheck{}
		}
		if parsed.MinimumNumber != nil {
			inlineQC.MinimumNumber = parsed.MinimumNumber
		}
		if parsed.MaximumNumber != nil {
			inlineQC.MaximumNumber = parsed.MaximumNumber
		}
	}
	if inlineQC != nil {
		if err := validateQualityCheckObject(*inlineQC, parsed.ValueType); err != nil {
			return TemplateStandardPoint{}, err
		}
	}
	meta := map[string]any{}
	for k, v := range parsed.SourceMetadata {
		meta[k] = v
	}
	if _, err := edgecontrol.ParseEdgePointSourceMetadata(meta); err != nil {
		return TemplateStandardPoint{}, err
	}
	if err := edgecontrol.ValidatePointSafetyBoundary(parsed.PointCode, parsed.PointType, writable, meta); err != nil {
		return TemplateStandardPoint{}, err
	}
	if (writable || parsed.PointType == "COMMAND" || parsed.PointType == "SETTING") && parsed.SourceKey != "" {
		if err := edgecontrol.ValidatePointSafetyBoundary(parsed.SourceKey, parsed.PointType, writable, meta); err != nil {
			return TemplateStandardPoint{}, err
		}
	}
	if parsed.PointType == "COMMAND" {
		if err := commandmodel.ValidateCommandPointMetadata(meta); err != nil {
			return TemplateStandardPoint{}, err
		}
	}
	return TemplateStandardPoint{
		PointCode:              parsed.PointCode,
		SourceKey:              parsed.SourceKey,
		DisplayName:            display,
		PointType:              parsed.PointType,
		ValueType:              parsed.ValueType,
		Unit:                   unit,
		Writable:               writable,
		SampleIntervalMS:       parsed.SampleIntervalMS,
		PublishIntervalMS:      parsed.PublishIntervalMS,
		StaleAfterMS:           parsed.StaleAfterMS,
		CounterDecreaseMode:    parsed.CounterDecreaseMode,
		CounterRolloverModulus: parsed.CounterRolloverModulus,
		SourceMetadata:         meta,
		MinimumNumber:          parsed.MinimumNumber,
		MaximumNumber:          parsed.MaximumNumber,
		QualityChecks:          inlineQC,
	}, nil
}

func parseTemplateQualityChecks(raw any, pointValueTypes map[string]string, hasPoints bool, out *TemplatePayloadActivation) error {
	obj, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: qualityChecks must be an object", ErrInvalidMutation)
	}
	if len(obj) == 0 {
		return nil
	}
	isDirectRule := true
	for k, v := range obj {
		if _, known := qualityCheckDirectKeys[k]; !known {
			isDirectRule = false
			break
		}
		if _, nested := v.(map[string]any); nested {
			isDirectRule = false
			break
		}
	}
	if isDirectRule {
		var rule TemplateQualityCheck
		if err := decodeStrictJSONValue(obj, &rule); err != nil {
			return fmt.Errorf("%w: qualityChecks: %v", ErrInvalidMutation, err)
		}
		if err := validateQualityCheckObject(rule, ""); err != nil {
			return fmt.Errorf("%w: qualityChecks: %v", ErrInvalidMutation, err)
		}
		out.DefaultQualityCheck = &rule
		return nil
	}
	for pointCode, entry := range obj {
		if !pointCodePattern.MatchString(pointCode) {
			return fmt.Errorf("%w: qualityChecks key %q must be a valid pointCode", ErrInvalidMutation, pointCode)
		}
		valueType := ""
		if hasPoints {
			vt, exists := pointValueTypes[pointCode]
			if !exists {
				return fmt.Errorf("%w: qualityChecks references unknown standardPoint %q", ErrInvalidMutation, pointCode)
			}
			valueType = vt
		}
		var rule TemplateQualityCheck
		if err := decodeStrictJSONValue(entry, &rule); err != nil {
			return fmt.Errorf("%w: qualityChecks[%q]: %v", ErrInvalidMutation, pointCode, err)
		}
		if err := validateQualityCheckObject(rule, valueType); err != nil {
			return fmt.Errorf("%w: qualityChecks[%q]: %v", ErrInvalidMutation, pointCode, err)
		}
		copyRule := rule
		out.QualityChecksByPoint[pointCode] = &copyRule
	}
	return nil
}

func validateQualityCheckObject(rule TemplateQualityCheck, valueType string) error {
	if rule.MinimumNumber != nil && !isFiniteFloat(*rule.MinimumNumber) {
		return errors.New("minimumNumber must be finite")
	}
	if rule.MaximumNumber != nil && !isFiniteFloat(*rule.MaximumNumber) {
		return errors.New("maximumNumber must be finite")
	}
	if rule.MinimumNumber != nil && rule.MaximumNumber != nil && *rule.MaximumNumber < *rule.MinimumNumber {
		return errors.New("maximumNumber must be >= minimumNumber")
	}
	if rule.MaxDeltaPerMinute != nil && !isFinitePositive(*rule.MaxDeltaPerMinute) {
		return errors.New("maxDeltaPerMinute must be > 0")
	}
	if rule.UnchangedAlertSeconds != nil && *rule.UnchangedAlertSeconds < 60 {
		return errors.New("unchangedAlertSeconds must be >= 60")
	}
	if rule.MaxStep != nil && !isFinitePositive(*rule.MaxStep) {
		return errors.New("maxStep must be > 0")
	}
	if rule.MaxStepWindowSeconds != nil && *rule.MaxStepWindowSeconds < 1 {
		return errors.New("maxStepWindowSeconds must be >= 1")
	}
	if rule.FlatlineSeconds != nil && *rule.FlatlineSeconds < 60 {
		return errors.New("flatlineSeconds must be >= 60")
	}
	if rule.FlatlineRunningSeconds != nil && *rule.FlatlineRunningSeconds < 1 {
		return errors.New("flatlineRunningSeconds must be >= 1")
	}
	if rule.FlatlineStabilizeSeconds != nil && *rule.FlatlineStabilizeSeconds < 1 {
		return errors.New("flatlineStabilizeSeconds must be >= 1")
	}
	for _, sentinel := range rule.Sentinels {
		if !isFiniteFloat(sentinel) {
			return errors.New("sentinels must be finite numbers")
		}
	}
	if valueType != "" && valueType != "NUMBER" {
		if rule.MinimumNumber != nil || rule.MaximumNumber != nil || rule.MaxDeltaPerMinute != nil || rule.MaxStep != nil || len(rule.Sentinels) > 0 {
			return fmt.Errorf("numeric quality checks cannot apply to %s points", valueType)
		}
	}
	return nil
}

func validateTemplateCapabilityProfile(profile TemplateCapabilityProfile) error {
	if !validIdentifierToken(profile.CapabilityName, 128) || !validIdentifierToken(profile.CapabilityRevision, 128) {
		return errors.New("capabilityName and capabilityRevision must be non-empty trimmed tokens <= 128 bytes")
	}
	switch profile.Status {
	case "DRAFT", "VERIFIED":
	default:
		return fmt.Errorf("capability status must be DRAFT or VERIFIED, got %q", profile.Status)
	}
	unit := strings.TrimSpace(profile.CanonicalUnit)
	if unit == "" || unit != profile.CanonicalUnit || len(unit) > 64 || containsControlChar(unit) {
		return errors.New("invalid canonicalUnit")
	}
	if !isFiniteFloat(profile.MinimumValue) || !isFiniteFloat(profile.MaximumValue) || profile.MaximumValue < profile.MinimumValue {
		return errors.New("maximumValue must be >= minimumValue and finite")
	}
	if !isFiniteFloat(profile.MaximumDelta) || profile.MaximumDelta < 0 {
		return errors.New("maximumDelta must be >= 0 and finite")
	}
	switch commandmodel.RiskLevel(profile.RiskLevel) {
	case commandmodel.RiskLow, commandmodel.RiskMedium, commandmodel.RiskHigh, commandmodel.RiskCritical:
	default:
		return fmt.Errorf("unsupported riskLevel %q", profile.RiskLevel)
	}
	switch commandmodel.ApprovalPolicy(profile.ApprovalPolicy) {
	case commandmodel.ApprovalNone, commandmodel.ApprovalSingleApprover, commandmodel.ApprovalTwoPerson:
	default:
		return fmt.Errorf("unsupported approvalPolicy %q", profile.ApprovalPolicy)
	}
	if profile.RetryPolicy != string(commandmodel.RetryPreSendOnly) {
		return fmt.Errorf("retryPolicy must be %q", commandmodel.RetryPreSendOnly)
	}
	connector := strings.TrimSpace(profile.ConnectorKind)
	if connector == "" || connector != profile.ConnectorKind || len(connector) > 64 || containsControlChar(connector) {
		return errors.New("invalid connectorKind")
	}
	return nil
}

// TemplateModbusDriver holds the validated declarative southbound driver template payload
// stored on a RELEASED DEVICE TemplateRevision and materialized to bound gateways.
type TemplateModbusDriver struct {
	DriverType        string                             `json:"driverType"`
	Endpoint          *string                            `json:"endpoint,omitempty"`
	UnitID            *int                               `json:"unitId,omitempty"`
	CapabilityProfile string                             `json:"capabilityProfile,omitempty"`
	TimeoutMS         *int                               `json:"timeoutMs,omitempty"`
	Retries           *int                               `json:"retries,omitempty"`
	Registers         []edgecontrol.ModbusRegisterInput  `json:"registers,omitempty"`
	BACnet            *edgecontrol.BACnetDriverSpecInput `json:"bacnet,omitempty"`
	OPCUA             *edgecontrol.OPCUADriverSpecInput  `json:"opcua,omitempty"`
}

func validateTemplateDriver(raw any, allowNonModbus bool) (*TemplateModbusDriver, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("must be an object")
	}
	var parsed TemplateModbusDriver
	if err := decodeStrictJSONValue(obj, &parsed); err != nil {
		return nil, err
	}
	if regSlice, ok := obj["registers"].([]any); ok {
		for i, item := range regSlice {
			regMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("registers[%d] must be an object", i)
			}
			for _, req := range []string{"address", "offset"} {
				if val, exists := regMap[req]; !exists || val == nil {
					return nil, fmt.Errorf("registers[%d].%s is required", i, req)
				}
			}
		}
	}
	southbound, err := edgecontrol.ValidateSouthboundDriverInput(edgecontrol.ModbusDriverInput{
		DriverType:        parsed.DriverType,
		Endpoint:          parsed.Endpoint,
		UnitID:            parsed.UnitID,
		CapabilityProfile: parsed.CapabilityProfile,
		TimeoutMS:         parsed.TimeoutMS,
		Retries:           parsed.Retries,
		Registers:         parsed.Registers,
		BACnet:            parsed.BACnet,
		OPCUA:             parsed.OPCUA,
	}, edgecontrol.ModbusDriverValidationOptions{
		RequireDeviceKey:       false,
		RequireGatewayBindings: false,
	})
	if err != nil {
		return nil, err
	}
	if !allowNonModbus && southbound.Kind != edgecontrol.SouthboundKindModbus {
		return nil, fmt.Errorf("modbusDriver only supports Modbus driverType, got %q", parsed.DriverType)
	}
	normalizedInput := southbound.ToDriverInput(false)
	normalized := &TemplateModbusDriver{
		DriverType:        normalizedInput.DriverType,
		CapabilityProfile: normalizedInput.CapabilityProfile,
		Registers:         normalizedInput.Registers,
		BACnet:            normalizedInput.BACnet.Clone(),
		OPCUA:             normalizedInput.OPCUA.Clone(),
	}
	if parsed.Endpoint != nil {
		var ep string
		switch southbound.Kind {
		case edgecontrol.SouthboundKindModbus:
			ep = southbound.Modbus.Endpoint
		case edgecontrol.SouthboundKindBACnetIP:
			ep = southbound.BACnet.Endpoint
		case edgecontrol.SouthboundKindOPCUA:
			ep = southbound.OPCUA.Endpoint
		}
		normalized.Endpoint = &ep
	}
	if parsed.UnitID != nil && southbound.Modbus != nil {
		u := int(southbound.Modbus.UnitID)
		normalized.UnitID = &u
	}
	if parsed.TimeoutMS != nil {
		tmo := *parsed.TimeoutMS
		normalized.TimeoutMS = &tmo
	}
	if parsed.Retries != nil {
		ret := *parsed.Retries
		normalized.Retries = &ret
	}
	return normalized, nil
}

func validIdentifierToken(value string, maxBytes int) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(trimmed) > maxBytes {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isFinitePositive(v float64) bool {
	return isFiniteFloat(v) && v > 0
}

func decodeStrictJSONValue(raw any, dest any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing tokens")
	}
	return nil
}

func deriveTemplatePointSourceKey(deviceCode, pointCode string, templateSourceKey string) (string, error) {
	if templateSourceKey != "" {
		if !sourceKeyPattern.MatchString(templateSourceKey) {
			return "", fmt.Errorf("%w: invalid sourceKey %q", ErrInvalidMutation, templateSourceKey)
		}
		return templateSourceKey, nil
	}
	candidate := deviceCode + "." + pointCode
	if !sourceKeyPattern.MatchString(candidate) {
		candidate = "dev." + candidate
	}
	if len(candidate) > 128 {
		candidate = candidate[:128]
	}
	if !sourceKeyPattern.MatchString(candidate) {
		return "", fmt.Errorf("%w: unable to derive valid sourceKey from %q and %q", ErrInvalidMutation, deviceCode, pointCode)
	}
	return candidate, nil
}

func (activation *TemplatePayloadActivation) effectiveQualityForPoint(pt TemplateStandardPoint) *TemplateQualityCheck {
	if activation == nil && pt.QualityChecks == nil {
		return nil
	}
	var merged TemplateQualityCheck
	var anySet bool
	applyRule := func(src *TemplateQualityCheck) {
		if src == nil {
			return
		}
		anySet = true
		if src.MinimumNumber != nil {
			merged.MinimumNumber = src.MinimumNumber
		}
		if src.MaximumNumber != nil {
			merged.MaximumNumber = src.MaximumNumber
		}
		if src.MaxDeltaPerMinute != nil {
			merged.MaxDeltaPerMinute = src.MaxDeltaPerMinute
		}
		if src.UnchangedAlertSeconds != nil {
			merged.UnchangedAlertSeconds = src.UnchangedAlertSeconds
		}
		if src.MaxStep != nil {
			merged.MaxStep = src.MaxStep
		}
		if src.MaxStepWindowSeconds != nil {
			merged.MaxStepWindowSeconds = src.MaxStepWindowSeconds
		}
		if src.FlatlineSeconds != nil {
			merged.FlatlineSeconds = src.FlatlineSeconds
		}
		if src.FlatlineRunningSeconds != nil {
			merged.FlatlineRunningSeconds = src.FlatlineRunningSeconds
		}
		if src.FlatlineStabilizeSeconds != nil {
			merged.FlatlineStabilizeSeconds = src.FlatlineStabilizeSeconds
		}
		if src.Sentinels != nil {
			merged.Sentinels = append([]float64(nil), src.Sentinels...)
		}
		if src.Copied != nil {
			merged.Copied = src.Copied
		}
	}
	if activation != nil {
		applyRule(activation.DefaultQualityCheck)
		applyRule(activation.QualityChecksByPoint[pt.PointCode])
	}
	applyRule(pt.QualityChecks)
	if !anySet {
		return nil
	}
	return &merged
}

func buildTemplatePointSourceMetadata(
	existingRaw []byte,
	pt TemplateStandardPoint,
	templateKey string,
	templateRevisionID string,
	assignmentID string,
	qc *TemplateQualityCheck,
) ([]byte, error) {
	merged := map[string]any{}
	if len(existingRaw) > 0 {
		_ = json.Unmarshal(existingRaw, &merged)
	}
	for k, v := range pt.SourceMetadata {
		merged[k] = v
	}
	merged["templateKey"] = templateKey
	merged["templateRevisionId"] = templateRevisionID
	merged["templateAssignmentId"] = assignmentID
	delete(merged, "minimumNumber")
	delete(merged, "maximumNumber")
	delete(merged, "qualityChecks")
	if qc != nil {
		if pt.ValueType == "NUMBER" {
			if qc.MinimumNumber != nil {
				merged["minimumNumber"] = *qc.MinimumNumber
			}
			if qc.MaximumNumber != nil {
				merged["maximumNumber"] = *qc.MaximumNumber
			}
		}
		qcDoc := qualityCheckToDocument(qc, pt.ValueType)
		if len(qcDoc) > 0 {
			merged["qualityChecks"] = qcDoc
		}
	}
	return json.Marshal(merged)
}

func qualityCheckToDocument(qc *TemplateQualityCheck, valueType string) map[string]any {
	if qc == nil {
		return nil
	}
	doc := map[string]any{}
	if valueType == "" || valueType == "NUMBER" {
		if qc.MaxDeltaPerMinute != nil {
			doc["maxDeltaPerMinute"] = *qc.MaxDeltaPerMinute
			if qc.MaxStep == nil {
				doc["maxStep"] = *qc.MaxDeltaPerMinute
			}
			if qc.MaxStepWindowSeconds == nil {
				doc["maxStepWindowSeconds"] = 60
			}
		}
		if qc.MaxStep != nil {
			doc["maxStep"] = *qc.MaxStep
		}
		if qc.MaxStepWindowSeconds != nil {
			doc["maxStepWindowSeconds"] = *qc.MaxStepWindowSeconds
		}
		if len(qc.Sentinels) > 0 {
			doc["sentinels"] = qc.Sentinels
		}
	}
	if qc.UnchangedAlertSeconds != nil {
		doc["unchangedAlertSeconds"] = *qc.UnchangedAlertSeconds
		if qc.FlatlineSeconds == nil {
			doc["flatlineSeconds"] = *qc.UnchangedAlertSeconds
		}
	}
	if qc.FlatlineSeconds != nil {
		doc["flatlineSeconds"] = *qc.FlatlineSeconds
	}
	if qc.FlatlineRunningSeconds != nil {
		doc["flatlineRunningSeconds"] = *qc.FlatlineRunningSeconds
	}
	if qc.FlatlineStabilizeSeconds != nil {
		doc["flatlineStabilizeSeconds"] = *qc.FlatlineStabilizeSeconds
	}
	if qc.Copied != nil {
		doc["copied"] = *qc.Copied
	}
	if len(doc) == 0 {
		return nil
	}
	return doc
}

func extractPointQualityFromSourceMetadata(raw []byte, valueType string) (*float64, *float64, map[string]any) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil || len(meta) == 0 {
		return nil, nil, nil
	}
	var minNum, maxNum *float64
	if valueType == "NUMBER" {
		if val, ok := numericFromAny(meta["minimumNumber"]); ok {
			minNum = &val
		}
		if val, ok := numericFromAny(meta["maximumNumber"]); ok {
			maxNum = &val
		}
	}
	var qcOut map[string]any
	if rawQC, ok := meta["qualityChecks"].(map[string]any); ok && len(rawQC) > 0 {
		qcOut = make(map[string]any, len(rawQC)+2)
		for k, v := range rawQC {
			switch k {
			case "minimumNumber":
				if valueType == "NUMBER" && minNum == nil {
					if val, ok := numericFromAny(v); ok {
						minNum = &val
					}
				}
			case "maximumNumber":
				if valueType == "NUMBER" && maxNum == nil {
					if val, ok := numericFromAny(v); ok {
						maxNum = &val
					}
				}
			default:
				qcOut[k] = v
			}
		}
		if valueType == "NUMBER" {
			if delta, ok := numericFromAny(qcOut["maxDeltaPerMinute"]); ok {
				if _, exists := qcOut["maxStep"]; !exists {
					qcOut["maxStep"] = delta
				}
				if _, exists := qcOut["maxStepWindowSeconds"]; !exists {
					qcOut["maxStepWindowSeconds"] = 60
				}
			}
		}
		if unchanged, ok := numericFromAny(qcOut["unchangedAlertSeconds"]); ok {
			if _, exists := qcOut["flatlineSeconds"]; !exists {
				qcOut["flatlineSeconds"] = int(unchanged)
			}
		}
		if len(qcOut) == 0 {
			qcOut = nil
		}
	}
	return minNum, maxNum, qcOut
}

func numericFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		if isFiniteFloat(typed) {
			return typed, true
		}
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		if f, err := typed.Float64(); err == nil && isFiniteFloat(f) {
			return f, true
		}
	}
	return 0, false
}

func materializeTemplatePointsInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	siteID string,
	deviceID string,
	deviceCode string,
	templateKey string,
	templateRevisionID string,
	assignmentID string,
	activation *TemplatePayloadActivation,
	now time.Time,
) (int, error) {
	if activation == nil || len(activation.StandardPoints) == 0 {
		return 0, nil
	}
	for _, pt := range activation.StandardPoints {
		sourceKey, err := deriveTemplatePointSourceKey(deviceCode, pt.PointCode, pt.SourceKey)
		if err != nil {
			return 0, err
		}
		effectiveQC := activation.effectiveQualityForPoint(pt)

		var existingID, existingPointType, existingValueType string
		var existingMeta []byte
		err = tx.QueryRow(ctx, `
SELECT id::text, point_type, value_type, source_metadata
FROM core_registry.telemetry_points
WHERE tenant_id = $1::uuid
  AND site_id = $2::uuid
  AND reporting_device_id = $3::uuid
  AND point_code = $4
FOR UPDATE
`, tenantID, siteID, deviceID, pt.PointCode).Scan(&existingID, &existingPointType, &existingValueType, &existingMeta)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("lookup existing telemetry point %q: %w", pt.PointCode, err)
		}
		if err == nil {
			if existingPointType != pt.PointType || existingValueType != pt.ValueType {
				return 0, fmt.Errorf("%w: existing point %s (%s/%s) conflicts with template point (%s/%s)", ErrBindingConflict, pt.PointCode, existingPointType, existingValueType, pt.PointType, pt.ValueType)
			}
			mergedMeta, err := buildTemplatePointSourceMetadata(existingMeta, pt, templateKey, templateRevisionID, assignmentID, effectiveQC)
			if err != nil {
				return 0, err
			}
			if _, err := tx.Exec(ctx, `
UPDATE core_registry.telemetry_points
SET source_key = $4,
    display_name = $5,
				unit = $6,
				writable = $7,
				sample_interval_ms = $8,
				publish_interval_ms = $9,
				stale_after_ms = $10,
				counter_decrease_mode = $11,
				counter_rollover_modulus = $12,
				source_metadata = $13::jsonb,
				revision = revision + 1,
				updated_at = $14
WHERE tenant_id = $1::uuid AND site_id = $2::uuid AND id = $3::uuid
`, tenantID, siteID, existingID, sourceKey, pt.DisplayName, pt.Unit, pt.Writable, pt.SampleIntervalMS, pt.PublishIntervalMS, pt.StaleAfterMS, pt.CounterDecreaseMode, pt.CounterRolloverModulus, string(mergedMeta), now); err != nil {
				return 0, fmt.Errorf("update materialized telemetry point %q: %w", pt.PointCode, err)
			}
			continue
		}

		pointID, err := newCoreUUIDv7(now)
		if err != nil {
			return 0, err
		}
		mergedMeta, err := buildTemplatePointSourceMetadata(nil, pt, templateKey, templateRevisionID, assignmentID, effectiveQC)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO core_registry.telemetry_points (
  id, tenant_id, site_id, reporting_device_id, sensor_id,
  point_code, source_key, display_name, point_type, value_type,
  unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms,
  counter_decrease_mode, counter_rollover_modulus, source_metadata,
  status, revision, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, NULL,
  $5, $6, $7, $8, $9,
  $10, $11, $12, $13, $14,
  $15, $16, $17::jsonb,
  'ACTIVE', 1, $18, $18
)
`, pointID, tenantID, siteID, deviceID,
			pt.PointCode, sourceKey, pt.DisplayName, pt.PointType, pt.ValueType,
			pt.Unit, pt.Writable, pt.SampleIntervalMS, pt.PublishIntervalMS, pt.StaleAfterMS,
			pt.CounterDecreaseMode, pt.CounterRolloverModulus, string(mergedMeta), now); err != nil {
			return 0, fmt.Errorf("insert materialized telemetry point %q: %w", pt.PointCode, err)
		}
	}
	return len(activation.StandardPoints), nil
}

// CommandCapabilitySyncClient syncs template-derived capability profiles into Command runtime.
type CommandCapabilitySyncClient interface {
	SyncCapabilityProfiles(context.Context, string, time.Time, []TemplateCapabilityProfile) error
}

func (c *httpDownstreamClient) SyncCapabilityProfiles(ctx context.Context, tenantID string, sourceSyncedAt time.Time, profiles []TemplateCapabilityProfile) error {
	if len(profiles) == 0 {
		return nil
	}
	var syncedAtPtr *time.Time
	if !sourceSyncedAt.IsZero() {
		utc := sourceSyncedAt.UTC()
		syncedAtPtr = &utc
	}
	body, err := json.Marshal(struct {
		TenantID       string                      `json:"tenantId"`
		SourceSyncedAt *time.Time                  `json:"sourceSyncedAt,omitempty"`
		Profiles       []TemplateCapabilityProfile `json:"profiles"`
	}{TenantID: tenantID, SourceSyncedAt: syncedAtPtr, Profiles: profiles})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/internal/v1/capability-profiles:sync", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("command capability sync status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
}
