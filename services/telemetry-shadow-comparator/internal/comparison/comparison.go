package comparison

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	SchemaVersion                   = 1
	MinimumValueAgreementRate       = 0.999
	MinimumTimestampAgreementRate   = 0.995
	DefaultNumericAbsoluteTolerance = 0.001
)

type Input struct {
	SchemaVersion                int                                      `json:"schemaVersion"`
	RegistryRevision             int64                                    `json:"registryRevision"`
	RouteRevision                int64                                    `json:"routeRevision"`
	Cohort                       string                                   `json:"cohort"`
	ExpectedSampleIntervalMillis int64                                    `json:"expectedSampleIntervalMillis"`
	NumericAbsoluteTolerance     float64                                  `json:"numericAbsoluteTolerance"`
	Mappings                     []DeviceMapping                          `json:"mappings"`
	LegacyDevices                []LegacyDevice                           `json:"legacyDevices"`
	S2Snapshots                  []telemetryapi.DeviceObservationSnapshot `json:"s2Snapshots"`
}

type DeviceMapping struct {
	LegacyDeviceID string `json:"legacyDeviceId"`
	DeviceID       string `json:"deviceId"`
}

type LegacyDevice struct {
	LegacyDeviceID string                  `json:"legacyDeviceId"`
	Active         *bool                   `json:"active"`
	Values         map[string]LegacySample `json:"values"`
}

type LegacySample struct {
	TimestampMillis int64           `json:"ts"`
	Value           json.RawMessage `json:"value"`
}

type Report struct {
	SchemaVersion               int                   `json:"schemaVersion"`
	RegistryRevision            int64                 `json:"registryRevision"`
	RouteRevision               int64                 `json:"routeRevision"`
	Cohort                      string                `json:"cohort"`
	Thresholds                  Thresholds            `json:"thresholds"`
	MappingMismatches           []MappingDifference   `json:"mappingMismatches"`
	MissingDevices              []string              `json:"missingDevices"`
	ExtraDevices                []string              `json:"extraDevices"`
	ValueDifferences            []ValueDifference     `json:"valueDifferences"`
	TimestampDifferences        []TimestampDifference `json:"timestampDifferences"`
	SemanticDifferences         []SemanticDifference  `json:"semanticDifferences"`
	OverlappingAcceptedValues   int                   `json:"overlappingAcceptedValues"`
	UnmatchedAcceptedValues     int                   `json:"unmatchedAcceptedValues"`
	AcceptedValueAgreements     int                   `json:"acceptedValueAgreements"`
	TimestampComparisons        int                   `json:"timestampComparisons"`
	TimestampAgreements         int                   `json:"timestampAgreements"`
	AcceptedValueAgreementRate  float64               `json:"acceptedValueAgreementRate"`
	TimestampAgreementRate      float64               `json:"timestampAgreementRate"`
	UnclassifiedDifferenceCount int                   `json:"unclassifiedDifferenceCount"`
	PromotionEligible           bool                  `json:"promotionEligible"`
	SideEffects                 SideEffectEvidence    `json:"sideEffects"`
}

type Thresholds struct {
	MappingMismatchMaximum        int     `json:"mappingMismatchMaximum"`
	MissingExtraDeviceMaximum     int     `json:"missingExtraDeviceMaximum"`
	MinimumValueAgreementRate     float64 `json:"minimumValueAgreementRate"`
	MinimumTimestampAgreementRate float64 `json:"minimumTimestampAgreementRate"`
	UnclassifiedDifferenceMaximum int     `json:"unclassifiedDifferenceMaximum"`
	ExpectedSampleIntervalMillis  int64   `json:"expectedSampleIntervalMillis"`
	NumericAbsoluteTolerance      float64 `json:"numericAbsoluteTolerance"`
}

type MappingDifference struct {
	Kind           string `json:"kind"`
	LegacyDeviceID string `json:"legacyDeviceId,omitempty"`
	DeviceID       string `json:"deviceId,omitempty"`
}

type ValueDifference struct {
	DeviceID    string          `json:"deviceId"`
	Key         string          `json:"key"`
	Kind        string          `json:"kind"`
	LegacyValue json.RawMessage `json:"legacyValue,omitempty"`
	S2Value     json.RawMessage `json:"s2Value,omitempty"`
}

type TimestampDifference struct {
	DeviceID              string `json:"deviceId"`
	Key                   string `json:"key"`
	LegacyTimestampMillis int64  `json:"legacyTimestampMillis"`
	S2Timestamp           string `json:"s2Timestamp"`
	DeltaMillis           int64  `json:"deltaMillis"`
}

type SemanticDifference struct {
	DeviceID       string `json:"deviceId"`
	LegacyActive   *bool  `json:"legacyActive"`
	S2Availability string `json:"s2Availability"`
	S2Presence     string `json:"s2Presence"`
	S2DisplayState string `json:"s2DisplayState,omitempty"`
	Classification string `json:"classification"`
	Expected       bool   `json:"expected"`
}

type SideEffectEvidence struct {
	DatabaseWrites   int  `json:"databaseWrites"`
	Publications     int  `json:"publications"`
	Subscriptions    int  `json:"subscriptions"`
	TokensMinted     int  `json:"tokensMinted"`
	MappingsRepaired int  `json:"mappingsRepaired"`
	ServingPathUsed  bool `json:"servingPathUsed"`
}

func Compare(input Input) (Report, error) {
	if input.SchemaVersion != SchemaVersion || input.RegistryRevision < 1 || input.RouteRevision < 1 || input.Cohort == "" {
		return Report{}, errors.New("comparison identity is invalid")
	}
	if input.ExpectedSampleIntervalMillis <= 0 {
		return Report{}, errors.New("expected sample interval must be positive")
	}
	if input.NumericAbsoluteTolerance == 0 {
		input.NumericAbsoluteTolerance = DefaultNumericAbsoluteTolerance
	}
	if input.NumericAbsoluteTolerance < 0 || math.IsNaN(input.NumericAbsoluteTolerance) || math.IsInf(input.NumericAbsoluteTolerance, 0) {
		return Report{}, errors.New("numeric tolerance is invalid")
	}

	report := Report{
		SchemaVersion:        SchemaVersion,
		RegistryRevision:     input.RegistryRevision,
		RouteRevision:        input.RouteRevision,
		Cohort:               input.Cohort,
		MappingMismatches:    []MappingDifference{},
		MissingDevices:       []string{},
		ExtraDevices:         []string{},
		ValueDifferences:     []ValueDifference{},
		TimestampDifferences: []TimestampDifference{},
		SemanticDifferences:  []SemanticDifference{},
		Thresholds: Thresholds{
			MappingMismatchMaximum:        0,
			MissingExtraDeviceMaximum:     0,
			MinimumValueAgreementRate:     MinimumValueAgreementRate,
			MinimumTimestampAgreementRate: MinimumTimestampAgreementRate,
			UnclassifiedDifferenceMaximum: 0,
			ExpectedSampleIntervalMillis:  input.ExpectedSampleIntervalMillis,
			NumericAbsoluteTolerance:      input.NumericAbsoluteTolerance,
		},
		SideEffects: SideEffectEvidence{},
	}

	legacyByID := make(map[string]LegacyDevice, len(input.LegacyDevices))
	for _, device := range input.LegacyDevices {
		if device.LegacyDeviceID == "" {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "EMPTY_LEGACY_DEVICE_ID"})
			continue
		}
		if _, duplicate := legacyByID[device.LegacyDeviceID]; duplicate {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "DUPLICATE_LEGACY_DEVICE", LegacyDeviceID: device.LegacyDeviceID})
			continue
		}
		legacyByID[device.LegacyDeviceID] = device
	}

	s2ByID := make(map[string]telemetryapi.DeviceObservationSnapshot, len(input.S2Snapshots))
	for _, snapshot := range input.S2Snapshots {
		deviceID := string(snapshot.DeviceId)
		if deviceID == "" {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "EMPTY_S2_DEVICE_ID"})
			continue
		}
		if _, duplicate := s2ByID[deviceID]; duplicate {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "DUPLICATE_S2_DEVICE", DeviceID: deviceID})
			continue
		}
		s2ByID[deviceID] = snapshot
	}

	mappedLegacy := map[string]struct{}{}
	mappedS2 := map[string]struct{}{}
	for _, mapping := range input.Mappings {
		if mapping.LegacyDeviceID == "" || mapping.DeviceID == "" {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "INCOMPLETE_MAPPING", LegacyDeviceID: mapping.LegacyDeviceID, DeviceID: mapping.DeviceID})
			continue
		}
		if _, duplicate := mappedLegacy[mapping.LegacyDeviceID]; duplicate {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "DUPLICATE_LEGACY_MAPPING", LegacyDeviceID: mapping.LegacyDeviceID, DeviceID: mapping.DeviceID})
			continue
		}
		if _, duplicate := mappedS2[mapping.DeviceID]; duplicate {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "DUPLICATE_S2_MAPPING", LegacyDeviceID: mapping.LegacyDeviceID, DeviceID: mapping.DeviceID})
			continue
		}
		mappedLegacy[mapping.LegacyDeviceID] = struct{}{}
		mappedS2[mapping.DeviceID] = struct{}{}

		legacy, legacyExists := legacyByID[mapping.LegacyDeviceID]
		snapshot, s2Exists := s2ByID[mapping.DeviceID]
		if !legacyExists {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "MAPPING_LEGACY_DEVICE_MISSING", LegacyDeviceID: mapping.LegacyDeviceID, DeviceID: mapping.DeviceID})
			continue
		}
		if !s2Exists {
			report.MissingDevices = append(report.MissingDevices, mapping.DeviceID)
			continue
		}
		compareDevice(&report, legacy, snapshot, input.ExpectedSampleIntervalMillis, input.NumericAbsoluteTolerance)
	}

	for legacyID := range legacyByID {
		if _, mapped := mappedLegacy[legacyID]; !mapped {
			report.MappingMismatches = append(report.MappingMismatches, MappingDifference{Kind: "UNMAPPED_LEGACY_DEVICE", LegacyDeviceID: legacyID})
		}
	}
	for deviceID := range s2ByID {
		if _, mapped := mappedS2[deviceID]; !mapped {
			report.ExtraDevices = append(report.ExtraDevices, deviceID)
		}
	}

	sortReport(&report)
	report.AcceptedValueAgreementRate = ratio(report.AcceptedValueAgreements, report.OverlappingAcceptedValues)
	report.TimestampAgreementRate = ratio(report.TimestampAgreements, report.TimestampComparisons)
	report.PromotionEligible = len(report.MappingMismatches) == 0 && len(report.MissingDevices) == 0 && len(report.ExtraDevices) == 0 &&
		report.UnmatchedAcceptedValues == 0 && report.OverlappingAcceptedValues > 0 && report.AcceptedValueAgreementRate >= MinimumValueAgreementRate &&
		report.TimestampComparisons > 0 && report.TimestampAgreementRate >= MinimumTimestampAgreementRate &&
		report.UnclassifiedDifferenceCount == 0
	return report, nil
}

func compareDevice(report *Report, legacy LegacyDevice, snapshot telemetryapi.DeviceObservationSnapshot, expectedIntervalMillis int64, tolerance float64) {
	deviceID := string(snapshot.DeviceId)
	semantic := classifySemantic(deviceID, legacy.Active, snapshot)
	report.SemanticDifferences = append(report.SemanticDifferences, semantic)
	if !semantic.Expected {
		report.UnclassifiedDifferenceCount++
	}

	s2Values := make(map[string]*telemetryapi.TelemetryPresentState, len(snapshot.Values))
	for _, state := range snapshot.Values {
		if state.Present != nil {
			s2Values[string(state.Present.Key)] = state.Present
		}
	}
	keys := make([]string, 0, len(legacy.Values))
	for key := range legacy.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		legacySample := legacy.Values[key]
		s2Sample, exists := s2Values[key]
		if !exists {
			report.UnmatchedAcceptedValues++
			report.ValueDifferences = append(report.ValueDifferences, ValueDifference{DeviceID: deviceID, Key: key, Kind: "S2_ACCEPTED_VALUE_MISSING", LegacyValue: cloneRaw(legacySample.Value)})
			continue
		}
		report.OverlappingAcceptedValues++
		if valuesEqual(legacySample.Value, s2Sample.Value, tolerance) {
			report.AcceptedValueAgreements++
		} else {
			report.ValueDifferences = append(report.ValueDifferences, ValueDifference{DeviceID: deviceID, Key: key, Kind: "ACCEPTED_VALUE_DIFFERENCE", LegacyValue: cloneRaw(legacySample.Value), S2Value: cloneRaw(s2Sample.Value)})
		}
		report.TimestampComparisons++
		sampledAt, err := time.Parse(time.RFC3339Nano, string(s2Sample.SampledAt))
		if err != nil {
			report.TimestampDifferences = append(report.TimestampDifferences, TimestampDifference{DeviceID: deviceID, Key: key, LegacyTimestampMillis: legacySample.TimestampMillis, S2Timestamp: string(s2Sample.SampledAt), DeltaMillis: math.MaxInt64})
			continue
		}
		delta := legacySample.TimestampMillis - sampledAt.UnixMilli()
		if delta < 0 {
			delta = -delta
		}
		if delta <= expectedIntervalMillis {
			report.TimestampAgreements++
		} else {
			report.TimestampDifferences = append(report.TimestampDifferences, TimestampDifference{DeviceID: deviceID, Key: key, LegacyTimestampMillis: legacySample.TimestampMillis, S2Timestamp: string(s2Sample.SampledAt), DeltaMillis: delta})
		}
	}
	for key, sample := range s2Values {
		if _, exists := legacy.Values[key]; !exists {
			report.UnmatchedAcceptedValues++
			report.ValueDifferences = append(report.ValueDifferences, ValueDifference{DeviceID: deviceID, Key: key, Kind: "LEGACY_ACCEPTED_VALUE_MISSING", S2Value: cloneRaw(sample.Value)})
		}
	}
}

func classifySemantic(deviceID string, active *bool, snapshot telemetryapi.DeviceObservationSnapshot) SemanticDifference {
	presence := "NULL"
	if snapshot.Presence.CurrentState != nil {
		presence = string(*snapshot.Presence.CurrentState)
	}
	display := ""
	if snapshot.DisplayState != nil {
		display = string(*snapshot.DisplayState)
	}
	difference := SemanticDifference{
		DeviceID: deviceID, LegacyActive: active,
		S2Availability: string(snapshot.EvaluationAvailability),
		S2Presence:     presence, S2DisplayState: display,
	}
	switch {
	case string(snapshot.EvaluationAvailability) != "AVAILABLE":
		difference.Classification, difference.Expected = "LEGACY_ACTIVE_VS_S2_UNAVAILABLE", true
	case string(snapshot.Presence.Applicability) == "NOT_APPLICABLE":
		difference.Classification, difference.Expected = "S2_PRESENCE_NOT_APPLICABLE", true
	case active == nil:
		difference.Classification, difference.Expected = "LEGACY_ACTIVE_UNKNOWN", true
	case display == "STALE":
		difference.Classification, difference.Expected = "LEGACY_ACTIVE_COARSE_VS_S2_STALE", true
	case presence == "UNKNOWN" || presence == "NULL":
		difference.Classification, difference.Expected = "LEGACY_ACTIVE_COARSE_VS_S2_UNKNOWN", true
	case (*active && presence == "ONLINE") || (!*active && presence == "OFFLINE"):
		difference.Classification, difference.Expected = "PRESENCE_EQUIVALENT", true
	default:
		difference.Classification, difference.Expected = "UNCLASSIFIED_ACTIVE_PRESENCE_CONFLICT", false
	}
	return difference
}

func valuesEqual(left, right json.RawMessage, tolerance float64) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftNumber, leftIsNumber := leftValue.(float64)
	rightNumber, rightIsNumber := rightValue.(float64)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && math.Abs(leftNumber-rightNumber) <= tolerance
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func ratio(matches, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(matches) / float64(total)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func sortReport(report *Report) {
	sort.Slice(report.MappingMismatches, func(i, j int) bool {
		left := fmt.Sprintf("%s\x00%s\x00%s", report.MappingMismatches[i].Kind, report.MappingMismatches[i].LegacyDeviceID, report.MappingMismatches[i].DeviceID)
		right := fmt.Sprintf("%s\x00%s\x00%s", report.MappingMismatches[j].Kind, report.MappingMismatches[j].LegacyDeviceID, report.MappingMismatches[j].DeviceID)
		return left < right
	})
	sort.Strings(report.MissingDevices)
	sort.Strings(report.ExtraDevices)
	sort.Slice(report.ValueDifferences, func(i, j int) bool {
		return report.ValueDifferences[i].DeviceID+"\x00"+report.ValueDifferences[i].Key+"\x00"+report.ValueDifferences[i].Kind <
			report.ValueDifferences[j].DeviceID+"\x00"+report.ValueDifferences[j].Key+"\x00"+report.ValueDifferences[j].Kind
	})
	sort.Slice(report.TimestampDifferences, func(i, j int) bool {
		return report.TimestampDifferences[i].DeviceID+"\x00"+report.TimestampDifferences[i].Key < report.TimestampDifferences[j].DeviceID+"\x00"+report.TimestampDifferences[j].Key
	})
	sort.Slice(report.SemanticDifferences, func(i, j int) bool {
		return report.SemanticDifferences[i].DeviceID < report.SemanticDifferences[j].DeviceID
	})
}
