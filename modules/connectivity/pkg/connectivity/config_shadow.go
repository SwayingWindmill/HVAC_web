package connectivity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	ShadowSyncStatusUnconfigured = "UNCONFIGURED"
	ShadowSyncStatusPendingSync  = "PENDING_SYNC"
	ShadowSyncStatusSynced       = "SYNCED"
	ShadowSyncStatusDrifted      = "DRIFTED"

	AppliedStatusApplied  = "APPLIED"
	AppliedStatusPartial  = "PARTIAL"
	AppliedStatusRejected = "REJECTED"

	DriftSectionPointMappings    = "POINT_MAPPINGS"
	DriftSectionSamplingPolicies = "SAMPLING_POLICIES"
	DriftSectionConcentrator     = "CONCENTRATOR"

	DriftReasonValueMismatch    = "VALUE_MISMATCH"
	DriftReasonMissingOnEdge    = "MISSING_ON_EDGE"
	DriftReasonUnexpectedOnEdge = "UNEXPECTED_ON_EDGE"
	DriftReasonEdgeApplyFailed  = "EDGE_APPLY_FAILED"

	ConcentratorEIDSlotCount = 200

	maxPointMappingsJSONBytes       = 262144
	maxSamplingPoliciesJSONBytes    = 131072
	maxConcentratorConfigsJSONBytes = 262144
	maxDriftDetailsJSONBytes        = 65536
	maxDriftTargetRunes             = 256
	maxDriftDetailRunes             = 512
	maxShadowReasonRunes            = 1000
	maxShadowErrorRunes             = 1024
	minSamplingIntervalMS           = 100
	maxSamplingIntervalMS           = 86400000
)

var (
	ErrConfigShadowConflict = errors.New("gateway config shadow desired revision conflict")
	ErrConfigShadowInvalid  = errors.New("gateway config shadow configuration is invalid")

	shadowPointCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type PointCopyTarget struct {
	DeviceKey string `json:"deviceKey"`
	PointCode string `json:"pointCode"`
}

type GatewayPointMapping struct {
	SourceChannel    string            `json:"sourceChannel"`
	PrimaryDeviceKey string            `json:"primaryDeviceKey"`
	PrimaryPointCode string            `json:"primaryPointCode"`
	CopyTargets      []PointCopyTarget `json:"copyTargets,omitempty"`
}

type GatewaySamplingPolicy struct {
	DeviceKey         string `json:"deviceKey"`
	PointCode         string `json:"pointCode,omitempty"`
	SampleIntervalMS  int    `json:"sampleIntervalMs"`
	PublishIntervalMS int    `json:"publishIntervalMs"`
}

type ConcentratorShadowParams struct {
	ModbusID        int `json:"modbusId"`
	BaudRateCode    int `json:"baudRateCode"`
	OffLineTime     int `json:"offLineTime"`
	DataUplinkCycle int `json:"dataUplinkCycle"`
	LocalNet        int `json:"localNet"`
	TimeMark        int `json:"timeMark"`
	RfTxPower       int `json:"rfTxPower"`
	RfFrequency     int `json:"rfFrequency"`
	RfBandWidth     int `json:"rfBandWidth"`
	RfSf            int `json:"rfSf"`
	RfLeadCode      int `json:"rfLeadCode"`
	TxTimeout       int `json:"txTimeout"`
	RxTimeout       int `json:"rxTimeout"`
	RssiOut         int `json:"rssiOut"`
}

type ConcentratorShadowEIDSlot struct {
	Slot int    `json:"slot"`
	EID  uint32 `json:"eid"`
}

type ConcentratorUnitShadow struct {
	Params *ConcentratorShadowParams   `json:"params,omitempty"`
	EIDs   []ConcentratorShadowEIDSlot `json:"eids,omitempty"`
}

type DriftDetail struct {
	Section string `json:"section"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

type GatewayDesiredConfig struct {
	DesiredRevision     int64                             `json:"desiredRevision"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig       `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy            `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle     `json:"edgeModels,omitempty"`
	UpdatedBy           string                            `json:"updatedBy"`
	Reason              string                            `json:"reason"`
	CreatedAt           time.Time                         `json:"createdAt"`
	UpdatedAt           time.Time                         `json:"updatedAt"`
}

type GatewayReportedConfig struct {
	ReportedRevision    int64                                `json:"reportedRevision"`
	AppliedStatus       string                               `json:"appliedStatus"`
	PointMappings       []GatewayPointMapping                `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy              `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow    `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig          `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy               `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle          `json:"edgeRuleBundle,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle        `json:"edgeModels,omitempty"`
	EdgeModelStatuses   []edgecontrol.EdgeModelRuntimeStatus `json:"edgeModelStatuses,omitempty"`
	DriftDetails        []DriftDetail                        `json:"driftDetails"`
	ErrorMessage        *string                              `json:"errorMessage,omitempty"`
	ReportedAt          time.Time                            `json:"reportedAt"`
	UpdatedAt           time.Time                            `json:"updatedAt"`
}

type GatewayConfigShadow struct {
	TenantID   string                 `json:"tenantId"`
	GatewayID  string                 `json:"gatewayId"`
	SyncStatus string                 `json:"syncStatus"`
	Desired    *GatewayDesiredConfig  `json:"desired,omitempty"`
	Reported   *GatewayReportedConfig `json:"reported,omitempty"`
	Drift      []DriftDetail          `json:"drift"`
}

type UpdateDesiredConfigInput struct {
	ExpectedDesiredRevision *int64                             `json:"expectedDesiredRevision,omitempty"`
	PointMappings           *[]GatewayPointMapping             `json:"pointMappings,omitempty"`
	SamplingPolicies        *[]GatewaySamplingPolicy           `json:"samplingPolicies,omitempty"`
	ConcentratorConfigs     *map[string]ConcentratorUnitShadow `json:"concentratorConfigs,omitempty"`
	DriverConfigs           *[]GatewayModbusDriverConfig       `json:"driverConfigs,omitempty"`
	AutonomyPolicy          OptionalAutonomyPolicy             `json:"autonomyPolicy,omitzero"`
	EdgeRuleBundle          OptionalEdgeRuleBundle             `json:"edgeRuleBundle,omitzero"`
	EdgeModels              OptionalEdgeModelBundles           `json:"edgeModels,omitzero"`
	Reason                  string                             `json:"reason"`
}

type GatewayShadowPullResponse struct {
	GatewayID       string                `json:"gatewayId"`
	DesiredRevision int64                 `json:"desiredRevision"`
	HasDelta        bool                  `json:"hasDelta"`
	Desired         *GatewayDesiredConfig `json:"desired,omitempty"`
}

type GatewayShadowReportInput struct {
	ReportedRevision    int64                                `json:"reportedRevision"`
	AppliedStatus       string                               `json:"appliedStatus"`
	PointMappings       []GatewayPointMapping                `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy              `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow    `json:"concentratorConfigs"`
	DriverConfigs       []GatewayModbusDriverConfig          `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *GatewayAutonomyPolicy               `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *edgecontrol.EdgeRuleBundle          `json:"edgeRuleBundle,omitempty"`
	EdgeModels          []edgecontrol.EdgeModelBundle        `json:"edgeModels,omitempty"`
	EdgeModelStatuses   []edgecontrol.EdgeModelRuntimeStatus `json:"edgeModelStatuses,omitempty"`
	DriftDetails        []DriftDetail                        `json:"driftDetails,omitempty"`
	ErrorMessage        *string                              `json:"errorMessage,omitempty"`
	ReportedAt          *time.Time                           `json:"reportedAt,omitempty"`
}

func IsProtectedConcentratorUID(unitID int) bool {
	switch unitID {
	case 2, 3, 4, 5, 10, 31:
		return true
	default:
		return false
	}
}

func ValidateConcentratorShadowParams(p ConcentratorShadowParams) error {
	bounds := []struct {
		name string
		val  int
		min  int
		max  int
	}{
		{"modbusId", p.ModbusID, 1, 247},
		{"baudRateCode", p.BaudRateCode, 0, 5},
		{"offLineTime", p.OffLineTime, 1, 65535},
		{"dataUplinkCycle", p.DataUplinkCycle, 1, 65535},
		{"localNet", p.LocalNet, 0, 65535},
		{"timeMark", p.TimeMark, 0, 3},
		{"rfTxPower", p.RfTxPower, 0, 30},
		{"rfFrequency", p.RfFrequency, 100, 1000},
		{"rfBandWidth", p.RfBandWidth, 0, 2},
		{"rfSf", p.RfSf, 5, 12},
		{"rfLeadCode", p.RfLeadCode, 1, 65535},
		{"txTimeout", p.TxTimeout, 1, 65535},
		{"rxTimeout", p.RxTimeout, 1, 65535},
		{"rssiOut", p.RssiOut, 0, 1},
	}
	for _, field := range bounds {
		if field.val < field.min || field.val > field.max {
			return fmt.Errorf("%w: concentrator %s=%d out of range [%d,%d]", ErrConfigShadowInvalid, field.name, field.val, field.min, field.max)
		}
		if field.name == "modbusId" && IsProtectedConcentratorUID(field.val) {
			return fmt.Errorf("%w: concentrator modbusId %d is protected on RS485 bus", ErrConfigShadowInvalid, field.val)
		}
	}
	return nil
}

func ValidateConcentratorShadowEIDSlots(slots []ConcentratorShadowEIDSlot) ([]ConcentratorShadowEIDSlot, error) {
	if len(slots) > ConcentratorEIDSlotCount {
		return nil, fmt.Errorf("%w: at most %d EID slots allowed, got %d", ErrConfigShadowInvalid, ConcentratorEIDSlotCount, len(slots))
	}
	seenSlots := make(map[int]struct{}, len(slots))
	seenEIDs := make(map[uint32]int, len(slots))
	cleaned := make([]ConcentratorShadowEIDSlot, 0, len(slots))
	for _, entry := range slots {
		if entry.Slot < 1 || entry.Slot > ConcentratorEIDSlotCount {
			return nil, fmt.Errorf("%w: EID slot %d out of range [1,%d]", ErrConfigShadowInvalid, entry.Slot, ConcentratorEIDSlotCount)
		}
		if _, dup := seenSlots[entry.Slot]; dup {
			return nil, fmt.Errorf("%w: duplicate EID slot %d", ErrConfigShadowInvalid, entry.Slot)
		}
		seenSlots[entry.Slot] = struct{}{}
		if entry.EID > 0 {
			if prevSlot, dup := seenEIDs[entry.EID]; dup {
				return nil, fmt.Errorf("%w: duplicate non-zero EID %d in slots %d and %d", ErrConfigShadowInvalid, entry.EID, prevSlot, entry.Slot)
			}
			seenEIDs[entry.EID] = entry.Slot
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned, nil
}

type validatedShadowSections struct {
	mappings       []GatewayPointMapping
	policies       []GatewaySamplingPolicy
	units          map[string]ConcentratorUnitShadow
	drivers        []GatewayModbusDriverConfig
	autonomy       *GatewayAutonomyPolicy
	edgeRuleBundle *edgecontrol.EdgeRuleBundle
	encoded        encodedShadowSections
}

func normalizeAndValidateDesiredShadow(
	pointMappings []GatewayPointMapping,
	samplingPolicies []GatewaySamplingPolicy,
	concentratorConfigs map[string]ConcentratorUnitShadow,
	driverConfigs []GatewayModbusDriverConfig,
	autonomyPolicy *GatewayAutonomyPolicy,
	edgeRuleBundle *edgecontrol.EdgeRuleBundle,
) (validatedShadowSections, error) {
	cleanedMappings, err := validatePointMappings(pointMappings)
	if err != nil {
		return validatedShadowSections{}, err
	}
	cleanedPolicies, err := validateSamplingPolicies(samplingPolicies)
	if err != nil {
		return validatedShadowSections{}, err
	}
	cleanedUnits, err := validateConcentratorConfigs(concentratorConfigs)
	if err != nil {
		return validatedShadowSections{}, err
	}
	cleanedDrivers, err := validateDriverConfigs(driverConfigs)
	if err != nil {
		return validatedShadowSections{}, err
	}
	cleanedAutonomy, err := validateAutonomyPolicy(autonomyPolicy)
	if err != nil {
		return validatedShadowSections{}, err
	}
	cleanedEdgeRules, err := validateEdgeRuleBundle(edgeRuleBundle)
	if err != nil {
		return validatedShadowSections{}, err
	}
	encoded, err := marshalShadowSections(cleanedMappings, cleanedPolicies, cleanedUnits, cleanedDrivers, cleanedAutonomy, cleanedEdgeRules, nil, nil)
	if err != nil {
		return validatedShadowSections{}, err
	}
	return validatedShadowSections{
		mappings:       cleanedMappings,
		policies:       cleanedPolicies,
		units:          cleanedUnits,
		drivers:        cleanedDrivers,
		autonomy:       cleanedAutonomy,
		edgeRuleBundle: cleanedEdgeRules,
		encoded:        encoded,
	}, nil
}

func validatePointMappings(input []GatewayPointMapping) ([]GatewayPointMapping, error) {
	type targetKey struct {
		deviceKey string
		pointCode string
	}
	claimed := make(map[targetKey]string, len(input))
	out := make([]GatewayPointMapping, 0, len(input))
	for i, item := range input {
		sourceChannel := strings.TrimSpace(item.SourceChannel)
		primaryDevice := strings.TrimSpace(item.PrimaryDeviceKey)
		primaryPoint := strings.TrimSpace(item.PrimaryPointCode)
		if sourceChannel == "" || len(sourceChannel) > 128 {
			return nil, fmt.Errorf("%w: pointMappings[%d].sourceChannel must be 1..128 characters", ErrConfigShadowInvalid, i)
		}
		if primaryDevice == "" || len(primaryDevice) > 128 {
			return nil, fmt.Errorf("%w: pointMappings[%d].primaryDeviceKey must be 1..128 characters", ErrConfigShadowInvalid, i)
		}
		if !shadowPointCodePattern.MatchString(primaryPoint) {
			return nil, fmt.Errorf("%w: pointMappings[%d].primaryPointCode %q is invalid", ErrConfigShadowInvalid, i, primaryPoint)
		}
		primaryKey := targetKey{deviceKey: primaryDevice, pointCode: primaryPoint}
		if existingOwner, dup := claimed[primaryKey]; dup {
			return nil, fmt.Errorf("%w: point target (%s, %s) claimed multiple times (%s)", ErrConfigShadowInvalid, primaryDevice, primaryPoint, existingOwner)
		}
		claimed[primaryKey] = fmt.Sprintf("primary of %s", sourceChannel)

		seenCopies := make(map[targetKey]struct{}, len(item.CopyTargets))
		var copies []PointCopyTarget
		if len(item.CopyTargets) > 0 {
			copies = make([]PointCopyTarget, 0, len(item.CopyTargets))
		}
		for j, target := range item.CopyTargets {
			copyDevice := strings.TrimSpace(target.DeviceKey)
			copyPoint := strings.TrimSpace(target.PointCode)
			if copyDevice == "" || len(copyDevice) > 128 {
				return nil, fmt.Errorf("%w: pointMappings[%d].copyTargets[%d].deviceKey must be 1..128 characters", ErrConfigShadowInvalid, i, j)
			}
			if !shadowPointCodePattern.MatchString(copyPoint) {
				return nil, fmt.Errorf("%w: pointMappings[%d].copyTargets[%d].pointCode %q is invalid", ErrConfigShadowInvalid, i, j, copyPoint)
			}
			copyKey := targetKey{deviceKey: copyDevice, pointCode: copyPoint}
			if copyKey == primaryKey {
				return nil, fmt.Errorf("%w: pointMappings[%d].copyTargets[%d] equals primary target (%s, %s)", ErrConfigShadowInvalid, i, j, copyDevice, copyPoint)
			}
			if _, dup := seenCopies[copyKey]; dup {
				return nil, fmt.Errorf("%w: duplicate copyTarget (%s, %s) in pointMappings[%d]", ErrConfigShadowInvalid, copyDevice, copyPoint, i)
			}
			if existingOwner, dup := claimed[copyKey]; dup {
				return nil, fmt.Errorf("%w: copyTarget (%s, %s) collides with %s", ErrConfigShadowInvalid, copyDevice, copyPoint, existingOwner)
			}
			seenCopies[copyKey] = struct{}{}
			claimed[copyKey] = fmt.Sprintf("copyTarget of %s", sourceChannel)
			copies = append(copies, PointCopyTarget{DeviceKey: copyDevice, PointCode: copyPoint})
		}
		out = append(out, GatewayPointMapping{
			SourceChannel:    sourceChannel,
			PrimaryDeviceKey: primaryDevice,
			PrimaryPointCode: primaryPoint,
			CopyTargets:      copies,
		})
	}
	return out, nil
}

func canonicalPolicyPointCode(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "*" {
		return "*"
	}
	return trimmed
}

func validateSamplingPolicies(input []GatewaySamplingPolicy) ([]GatewaySamplingPolicy, error) {
	type policyKey struct {
		deviceKey string
		pointCode string
	}
	seen := make(map[policyKey]struct{}, len(input))
	out := make([]GatewaySamplingPolicy, 0, len(input))
	for i, item := range input {
		deviceKey := strings.TrimSpace(item.DeviceKey)
		if deviceKey == "" || len(deviceKey) > 128 {
			return nil, fmt.Errorf("%w: samplingPolicies[%d].deviceKey must be 1..128 characters", ErrConfigShadowInvalid, i)
		}
		rawPoint := strings.TrimSpace(item.PointCode)
		if rawPoint != "" && rawPoint != "*" && !shadowPointCodePattern.MatchString(rawPoint) {
			return nil, fmt.Errorf("%w: samplingPolicies[%d].pointCode %q is invalid", ErrConfigShadowInvalid, i, rawPoint)
		}
		if item.SampleIntervalMS < minSamplingIntervalMS || item.SampleIntervalMS > maxSamplingIntervalMS {
			return nil, fmt.Errorf("%w: samplingPolicies[%d].sampleIntervalMs=%d out of range [%d,%d]", ErrConfigShadowInvalid, i, item.SampleIntervalMS, minSamplingIntervalMS, maxSamplingIntervalMS)
		}
		if item.PublishIntervalMS < item.SampleIntervalMS || item.PublishIntervalMS > maxSamplingIntervalMS {
			return nil, fmt.Errorf("%w: samplingPolicies[%d].publishIntervalMs=%d must be in [%d,%d]", ErrConfigShadowInvalid, i, item.PublishIntervalMS, item.SampleIntervalMS, maxSamplingIntervalMS)
		}
		key := policyKey{deviceKey: deviceKey, pointCode: canonicalPolicyPointCode(rawPoint)}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: duplicate sampling policy for (%s, %s)", ErrConfigShadowInvalid, deviceKey, key.pointCode)
		}
		seen[key] = struct{}{}
		out = append(out, GatewaySamplingPolicy{
			DeviceKey:         deviceKey,
			PointCode:         rawPoint,
			SampleIntervalMS:  item.SampleIntervalMS,
			PublishIntervalMS: item.PublishIntervalMS,
		})
	}
	return out, nil
}

func validateConcentratorUnitKey(key string) (int, error) {
	unitID, err := strconv.Atoi(key)
	if err != nil || strconv.Itoa(unitID) != key || unitID < 1 || unitID > 247 {
		return 0, fmt.Errorf("%w: concentratorConfigs key %q must be canonical integer 1..247", ErrConfigShadowInvalid, key)
	}
	if IsProtectedConcentratorUID(unitID) {
		return 0, fmt.Errorf("%w: concentratorConfigs unitId %d is protected on RS485 bus", ErrConfigShadowInvalid, unitID)
	}
	return unitID, nil
}

func validateConcentratorConfigs(input map[string]ConcentratorUnitShadow) (map[string]ConcentratorUnitShadow, error) {
	out := make(map[string]ConcentratorUnitShadow, len(input))
	for rawKey, unit := range input {
		if _, err := validateConcentratorUnitKey(rawKey); err != nil {
			return nil, err
		}
		var clonedParams *ConcentratorShadowParams
		if unit.Params != nil {
			if err := ValidateConcentratorShadowParams(*unit.Params); err != nil {
				return nil, err
			}
			cp := *unit.Params
			clonedParams = &cp
		}
		var clonedEIDs []ConcentratorShadowEIDSlot
		if unit.EIDs != nil {
			validatedSlots, err := ValidateConcentratorShadowEIDSlots(unit.EIDs)
			if err != nil {
				return nil, err
			}
			clonedEIDs = validatedSlots
		}
		out[rawKey] = ConcentratorUnitShadow{
			Params: clonedParams,
			EIDs:   clonedEIDs,
		}
	}
	return out, nil
}

// estimatedPostgresJSONBTextBytes returns an upper bound on the octet length
// PostgreSQL produces when formatting compact Go JSON via jsonb::text (one extra
// space after every ':' and ',' token outside strings, plus scientific-exponent
// decimal expansion for large/small floats).
func estimatedPostgresJSONBTextBytes(compactJSON []byte) int {
	extra := 0
	inString := false
	escaped := false
	for i := 0; i < len(compactJSON); i++ {
		b := compactJSON[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case ':', ',':
			extra++
		case 'e', 'E':
			// Account for PostgreSQL jsonb::text expanding Go scientific floats (e.g. 1e+30).
			j := i + 1
			if j < len(compactJSON) && (compactJSON[j] == '+' || compactJSON[j] == '-') {
				j++
			}
			expStart := j
			for j < len(compactJSON) && compactJSON[j] >= '0' && compactJSON[j] <= '9' {
				j++
			}
			if j > expStart {
				if expVal, err := strconv.Atoi(string(compactJSON[expStart:j])); err == nil {
					if expVal < 0 {
						expVal = -expVal
					}
					if expVal > 350 {
						expVal = 350
					}
					extra += expVal + 8
				}
			}
		}
	}
	return len(compactJSON) + extra
}

func clampUTF8Runes(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	count := 0
	for idx := range trimmed {
		if count == maxRunes {
			return trimmed[:idx]
		}
		count++
	}
	return trimmed
}

func clonePointMappingsList(src []GatewayPointMapping) []GatewayPointMapping {
	if len(src) == 0 {
		return []GatewayPointMapping{}
	}
	out := make([]GatewayPointMapping, len(src))
	for i, item := range src {
		out[i] = item
		if len(item.CopyTargets) > 0 {
			out[i].CopyTargets = slices.Clone(item.CopyTargets)
		}
	}
	return out
}

func cloneSamplingPoliciesList(src []GatewaySamplingPolicy) []GatewaySamplingPolicy {
	if len(src) == 0 {
		return []GatewaySamplingPolicy{}
	}
	return slices.Clone(src)
}

func cloneConcentratorUnits(src map[string]ConcentratorUnitShadow) map[string]ConcentratorUnitShadow {
	if len(src) == 0 {
		return map[string]ConcentratorUnitShadow{}
	}
	out := make(map[string]ConcentratorUnitShadow, len(src))
	for k, u := range src {
		var copied ConcentratorUnitShadow
		if u.Params != nil {
			cp := *u.Params
			copied.Params = &cp
		}
		if u.EIDs != nil {
			copied.EIDs = slices.Clone(u.EIDs)
		}
		out[k] = copied
	}
	return out
}

func marshalDriftDetailsChecked(details []DriftDetail) ([]byte, error) {
	if details == nil {
		details = []DriftDetail{}
	}
	clamped := make([]DriftDetail, len(details))
	for i, d := range details {
		target := clampUTF8Runes(d.Target, maxDriftTargetRunes)
		if target == "" {
			target = "unknown"
		}
		detail := clampUTF8Runes(d.Detail, maxDriftDetailRunes)
		if detail == "" {
			detail = d.Reason
		}
		clamped[i] = DriftDetail{
			Section: d.Section,
			Target:  target,
			Reason:  d.Reason,
			Detail:  detail,
		}
	}
	encoded, err := json.Marshal(clamped)
	if err != nil {
		return nil, fmt.Errorf("%w: encode driftDetails: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(encoded) > maxDriftDetailsJSONBytes {
		return nil, fmt.Errorf("%w: driftDetails exceeds %d bytes", ErrConfigShadowInvalid, maxDriftDetailsJSONBytes)
	}
	return encoded, nil
}

type encodedShadowSections struct {
	mappingsJSON         []byte
	policiesJSON         []byte
	unitsJSON            []byte
	driversJSON          []byte
	autonomyArg          *string
	edgeRulesArg         *string
	edgeModelsArg        *string
	edgeModelStatusesArg *string
}

func marshalShadowSections(
	mappings []GatewayPointMapping,
	policies []GatewaySamplingPolicy,
	units map[string]ConcentratorUnitShadow,
	drivers []GatewayModbusDriverConfig,
	autonomy *GatewayAutonomyPolicy,
	edgeRules *edgecontrol.EdgeRuleBundle,
	edgeModels []edgecontrol.EdgeModelBundle,
	edgeModelStatuses []edgecontrol.EdgeModelRuntimeStatus,
) (encodedShadowSections, error) {
	if mappings == nil {
		mappings = []GatewayPointMapping{}
	}
	if policies == nil {
		policies = []GatewaySamplingPolicy{}
	}
	if units == nil {
		units = map[string]ConcentratorUnitShadow{}
	}
	if drivers == nil {
		drivers = []GatewayModbusDriverConfig{}
	}
	mappingsBytes, err := json.Marshal(mappings)
	if err != nil {
		return encodedShadowSections{}, fmt.Errorf("%w: encode pointMappings: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(mappingsBytes) > maxPointMappingsJSONBytes {
		return encodedShadowSections{}, fmt.Errorf("%w: pointMappings exceeds %d bytes", ErrConfigShadowInvalid, maxPointMappingsJSONBytes)
	}
	policiesBytes, err := json.Marshal(policies)
	if err != nil {
		return encodedShadowSections{}, fmt.Errorf("%w: encode samplingPolicies: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(policiesBytes) > maxSamplingPoliciesJSONBytes {
		return encodedShadowSections{}, fmt.Errorf("%w: samplingPolicies exceeds %d bytes", ErrConfigShadowInvalid, maxSamplingPoliciesJSONBytes)
	}
	unitsBytes, err := json.Marshal(units)
	if err != nil {
		return encodedShadowSections{}, fmt.Errorf("%w: encode concentratorConfigs: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(unitsBytes) > maxConcentratorConfigsJSONBytes {
		return encodedShadowSections{}, fmt.Errorf("%w: concentratorConfigs exceeds %d bytes", ErrConfigShadowInvalid, maxConcentratorConfigsJSONBytes)
	}
	driversBytes, err := json.Marshal(drivers)
	if err != nil {
		return encodedShadowSections{}, fmt.Errorf("%w: encode driverConfigs: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(driversBytes) > maxDriverConfigsJSONBytes {
		return encodedShadowSections{}, fmt.Errorf("%w: driverConfigs exceeds %d bytes", ErrConfigShadowInvalid, maxDriverConfigsJSONBytes)
	}
	var autonomyArg *string
	if autonomy != nil {
		autonomyBytes, err := json.Marshal(autonomy)
		if err != nil {
			return encodedShadowSections{}, fmt.Errorf("%w: encode autonomyPolicy: %v", ErrConfigShadowInvalid, err)
		}
		if estimatedPostgresJSONBTextBytes(autonomyBytes) > maxAutonomyPolicyJSONBytes {
			return encodedShadowSections{}, fmt.Errorf("%w: autonomyPolicy exceeds %d bytes", ErrConfigShadowInvalid, maxAutonomyPolicyJSONBytes)
		}
		encoded := string(autonomyBytes)
		autonomyArg = &encoded
	}
	var edgeRulesArg *string
	if edgeRules != nil {
		edgeRulesBytes, err := json.Marshal(edgeRules)
		if err != nil {
			return encodedShadowSections{}, fmt.Errorf("%w: encode edgeRuleBundle: %v", ErrConfigShadowInvalid, err)
		}
		if estimatedPostgresJSONBTextBytes(edgeRulesBytes) > maxEdgeRuleBundleJSONBytes {
			return encodedShadowSections{}, fmt.Errorf("%w: edgeRuleBundle exceeds %d bytes", ErrConfigShadowInvalid, maxEdgeRuleBundleJSONBytes)
		}
		encoded := string(edgeRulesBytes)
		edgeRulesArg = &encoded
	}
	var edgeModelsArg *string
	if len(edgeModels) > 0 {
		encodedBytes, err := json.Marshal(edgeModels)
		if err != nil {
			return encodedShadowSections{}, fmt.Errorf("%w: encode edgeModels: %v", ErrConfigShadowInvalid, err)
		}
		if estimatedPostgresJSONBTextBytes(encodedBytes) > maxEdgeModelsJSONBytes {
			return encodedShadowSections{}, fmt.Errorf("%w: edgeModels exceeds %d bytes", ErrConfigShadowInvalid, maxEdgeModelsJSONBytes)
		}
		encoded := string(encodedBytes)
		edgeModelsArg = &encoded
	}
	var edgeModelStatusesArg *string
	if len(edgeModelStatuses) > 0 {
		encodedBytes, err := json.Marshal(edgeModelStatuses)
		if err != nil {
			return encodedShadowSections{}, fmt.Errorf("%w: encode edgeModelStatuses: %v", ErrConfigShadowInvalid, err)
		}
		if estimatedPostgresJSONBTextBytes(encodedBytes) > maxEdgeModelStatusesJSONBytes {
			return encodedShadowSections{}, fmt.Errorf("%w: edgeModelStatuses exceeds %d bytes", ErrConfigShadowInvalid, maxEdgeModelStatusesJSONBytes)
		}
		encoded := string(encodedBytes)
		edgeModelStatusesArg = &encoded
	}
	return encodedShadowSections{
		mappingsJSON:         mappingsBytes,
		policiesJSON:         policiesBytes,
		unitsJSON:            unitsBytes,
		driversJSON:          driversBytes,
		autonomyArg:          autonomyArg,
		edgeRulesArg:         edgeRulesArg,
		edgeModelsArg:        edgeModelsArg,
		edgeModelStatusesArg: edgeModelStatusesArg,
	}, nil
}

func validateShadowReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	runes := utf8.RuneCountInString(trimmed)
	if runes == 0 || runes > maxShadowReasonRunes {
		return "", fmt.Errorf("%w: reason must be 1..%d characters", ErrConfigShadowInvalid, maxShadowReasonRunes)
	}
	return trimmed, nil
}

func ComputeConfigShadowDrift(desired *GatewayDesiredConfig, reported *GatewayReportedConfig) []DriftDetail {
	return computeConfigShadowDrift(desired, reported)
}

func computeConfigShadowDrift(desired *GatewayDesiredConfig, reported *GatewayReportedConfig) []DriftDetail {
	drift := []DriftDetail{}
	if desired == nil && reported == nil {
		return drift
	}
	var desiredMappings, reportedMappings []GatewayPointMapping
	var desiredPolicies, reportedPolicies []GatewaySamplingPolicy
	var desiredUnits, reportedUnits map[string]ConcentratorUnitShadow
	var desiredDrivers, reportedDrivers []GatewayModbusDriverConfig
	var desiredAutonomy, reportedAutonomy *GatewayAutonomyPolicy
	var desiredEdgeRules, reportedEdgeRules *edgecontrol.EdgeRuleBundle
	var desiredEdgeModels, reportedEdgeModels []edgecontrol.EdgeModelBundle
	if desired != nil {
		desiredMappings = desired.PointMappings
		desiredPolicies = desired.SamplingPolicies
		desiredUnits = desired.ConcentratorConfigs
		desiredDrivers = desired.DriverConfigs
		desiredAutonomy = desired.AutonomyPolicy
		desiredEdgeRules = desired.EdgeRuleBundle
		desiredEdgeModels = desired.EdgeModels
	}
	if reported != nil {
		reportedMappings = reported.PointMappings
		reportedPolicies = reported.SamplingPolicies
		reportedUnits = reported.ConcentratorConfigs
		reportedDrivers = reported.DriverConfigs
		reportedAutonomy = reported.AutonomyPolicy
		reportedEdgeRules = reported.EdgeRuleBundle
		reportedEdgeModels = reported.EdgeModels
	}
	drift = append(drift, diffPointMappings(desiredMappings, reportedMappings)...)
	drift = append(drift, diffSamplingPolicies(desiredPolicies, reportedPolicies)...)
	drift = append(drift, diffConcentratorConfigs(desiredUnits, reportedUnits)...)
	drift = append(drift, diffDriverConfigs(desiredDrivers, reportedDrivers)...)
	drift = append(drift, diffAutonomyPolicy(desiredAutonomy, reportedAutonomy)...)
	drift = append(drift, diffEdgeRuleBundle(desiredEdgeRules, reportedEdgeRules)...)
	drift = append(drift, diffEdgeModelBundles(desiredEdgeModels, reportedEdgeModels)...)
	return drift
}

func diffPointMappings(desired, reported []GatewayPointMapping) []DriftDetail {
	canonicalize := func(items []GatewayPointMapping) map[string]GatewayPointMapping {
		out := make(map[string]GatewayPointMapping, len(items))
		for _, item := range items {
			copies := slices.Clone(item.CopyTargets)
			slices.SortFunc(copies, func(a, b PointCopyTarget) int {
				if c := strings.Compare(a.DeviceKey, b.DeviceKey); c != 0 {
					return c
				}
				return strings.Compare(a.PointCode, b.PointCode)
			})
			key := item.PrimaryDeviceKey + "/" + item.PrimaryPointCode
			out[key] = GatewayPointMapping{
				SourceChannel:    item.SourceChannel,
				PrimaryDeviceKey: item.PrimaryDeviceKey,
				PrimaryPointCode: item.PrimaryPointCode,
				CopyTargets:      copies,
			}
		}
		return out
	}
	dMap := canonicalize(desired)
	rMap := canonicalize(reported)
	keys := unionSortedKeys(dMap, rMap)
	var out []DriftDetail
	for _, key := range keys {
		dVal, inD := dMap[key]
		rVal, inR := rMap[key]
		targetKey := clampUTF8Runes(key, maxDriftTargetRunes)
		switch {
		case inD && !inR:
			out = append(out, DriftDetail{
				Section: DriftSectionPointMappings,
				Target:  targetKey,
				Reason:  DriftReasonMissingOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("desired mapping from channel %q is missing on edge", dVal.SourceChannel), maxDriftDetailRunes),
			})
		case !inD && inR:
			out = append(out, DriftDetail{
				Section: DriftSectionPointMappings,
				Target:  targetKey,
				Reason:  DriftReasonUnexpectedOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("unexpected mapping from channel %q present on edge", rVal.SourceChannel), maxDriftDetailRunes),
			})
		case dVal.SourceChannel != rVal.SourceChannel || !slices.Equal(dVal.CopyTargets, rVal.CopyTargets):
			out = append(out, DriftDetail{
				Section: DriftSectionPointMappings,
				Target:  targetKey,
				Reason:  DriftReasonValueMismatch,
				Detail:  clampUTF8Runes(fmt.Sprintf("mapping mismatch (desired channel=%q copies=%d, reported channel=%q copies=%d)", dVal.SourceChannel, len(dVal.CopyTargets), rVal.SourceChannel, len(rVal.CopyTargets)), maxDriftDetailRunes),
			})
		}
	}
	return out
}

func diffSamplingPolicies(desired, reported []GatewaySamplingPolicy) []DriftDetail {
	canonicalize := func(items []GatewaySamplingPolicy) map[string]GatewaySamplingPolicy {
		out := make(map[string]GatewaySamplingPolicy, len(items))
		for _, item := range items {
			pc := canonicalPolicyPointCode(item.PointCode)
			key := item.DeviceKey + "/" + pc
			out[key] = GatewaySamplingPolicy{
				DeviceKey:         item.DeviceKey,
				PointCode:         pc,
				SampleIntervalMS:  item.SampleIntervalMS,
				PublishIntervalMS: item.PublishIntervalMS,
			}
		}
		return out
	}
	dMap := canonicalize(desired)
	rMap := canonicalize(reported)
	keys := unionSortedKeys(dMap, rMap)
	var out []DriftDetail
	for _, key := range keys {
		dVal, inD := dMap[key]
		rVal, inR := rMap[key]
		targetKey := clampUTF8Runes(key, maxDriftTargetRunes)
		switch {
		case inD && !inR:
			out = append(out, DriftDetail{
				Section: DriftSectionSamplingPolicies,
				Target:  targetKey,
				Reason:  DriftReasonMissingOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("desired sampling policy (%dms/%dms) is missing on edge", dVal.SampleIntervalMS, dVal.PublishIntervalMS), maxDriftDetailRunes),
			})
		case !inD && inR:
			out = append(out, DriftDetail{
				Section: DriftSectionSamplingPolicies,
				Target:  targetKey,
				Reason:  DriftReasonUnexpectedOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("unexpected sampling policy (%dms/%dms) present on edge", rVal.SampleIntervalMS, rVal.PublishIntervalMS), maxDriftDetailRunes),
			})
		case dVal.SampleIntervalMS != rVal.SampleIntervalMS || dVal.PublishIntervalMS != rVal.PublishIntervalMS:
			out = append(out, DriftDetail{
				Section: DriftSectionSamplingPolicies,
				Target:  targetKey,
				Reason:  DriftReasonValueMismatch,
				Detail:  clampUTF8Runes(fmt.Sprintf("desired (%dms/%dms) != reported (%dms/%dms)", dVal.SampleIntervalMS, dVal.PublishIntervalMS, rVal.SampleIntervalMS, rVal.PublishIntervalMS), maxDriftDetailRunes),
			})
		}
	}
	return out
}

func canonicalNonZeroEIDMap(slots []ConcentratorShadowEIDSlot) map[int]uint32 {
	out := make(map[int]uint32)
	for _, slot := range slots {
		if slot.EID > 0 {
			out[slot.Slot] = slot.EID
		}
	}
	return out
}

func diffConcentratorConfigs(desired, reported map[string]ConcentratorUnitShadow) []DriftDetail {
	keys := unionSortedNumericKeys(desired, reported)
	var out []DriftDetail
	for _, unitKey := range keys {
		dUnit, inD := desired[unitKey]
		rUnit, inR := reported[unitKey]
		targetPrefix := clampUTF8Runes("unit:"+unitKey, maxDriftTargetRunes-8)
		switch {
		case inD && !inR:
			out = append(out, DriftDetail{
				Section: DriftSectionConcentrator,
				Target:  targetPrefix,
				Reason:  DriftReasonMissingOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("desired concentrator unit %s is missing on edge", unitKey), maxDriftDetailRunes),
			})
		case !inD && inR:
			out = append(out, DriftDetail{
				Section: DriftSectionConcentrator,
				Target:  targetPrefix,
				Reason:  DriftReasonUnexpectedOnEdge,
				Detail:  clampUTF8Runes(fmt.Sprintf("unexpected concentrator unit %s present on edge", unitKey), maxDriftDetailRunes),
			})
		default:
			if dUnit.Params != nil && rUnit.Params == nil {
				out = append(out, DriftDetail{
					Section: DriftSectionConcentrator,
					Target:  targetPrefix + ".params",
					Reason:  DriftReasonMissingOnEdge,
					Detail:  clampUTF8Runes(fmt.Sprintf("desired concentrator unit %s params are missing on edge", unitKey), maxDriftDetailRunes),
				})
			} else if dUnit.Params == nil && rUnit.Params != nil {
				out = append(out, DriftDetail{
					Section: DriftSectionConcentrator,
					Target:  targetPrefix + ".params",
					Reason:  DriftReasonUnexpectedOnEdge,
					Detail:  clampUTF8Runes(fmt.Sprintf("unexpected concentrator unit %s params present on edge", unitKey), maxDriftDetailRunes),
				})
			} else if dUnit.Params != nil && rUnit.Params != nil && *dUnit.Params != *rUnit.Params {
				out = append(out, DriftDetail{
					Section: DriftSectionConcentrator,
					Target:  targetPrefix + ".params",
					Reason:  DriftReasonValueMismatch,
					Detail:  clampUTF8Runes(fmt.Sprintf("concentrator unit %s params differ from desired snapshot", unitKey), maxDriftDetailRunes),
				})
			}

			dEIDs := canonicalNonZeroEIDMap(dUnit.EIDs)
			rEIDs := canonicalNonZeroEIDMap(rUnit.EIDs)
			if !maps.Equal(dEIDs, rEIDs) {
				out = append(out, DriftDetail{
					Section: DriftSectionConcentrator,
					Target:  targetPrefix + ".eids",
					Reason:  DriftReasonValueMismatch,
					Detail:  clampUTF8Runes(fmt.Sprintf("concentrator unit %s EID bindings differ (desired non-zero=%d, reported non-zero=%d)", unitKey, len(dEIDs), len(rEIDs)), maxDriftDetailRunes),
				})
			}
		}
	}
	return out
}

func unionSortedKeys[V any](left, right map[string]V) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for k := range left {
		seen[k] = struct{}{}
	}
	for k := range right {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func unionSortedNumericKeys[V any](left, right map[string]V) []string {
	keys := unionSortedKeys(left, right)
	slices.SortFunc(keys, func(a, b string) int {
		na, errA := strconv.Atoi(a)
		nb, errB := strconv.Atoi(b)
		if errA == nil && errB == nil {
			return na - nb
		}
		return strings.Compare(a, b)
	})
	return keys
}

func computeSyncStatus(desired *GatewayDesiredConfig, reported *GatewayReportedConfig, drift []DriftDetail) string {
	switch {
	case desired == nil && reported == nil:
		return ShadowSyncStatusUnconfigured
	case desired != nil && (reported == nil || reported.ReportedRevision < desired.DesiredRevision):
		return ShadowSyncStatusPendingSync
	case desired != nil && reported != nil && reported.ReportedRevision == desired.DesiredRevision && reported.AppliedStatus == AppliedStatusApplied && len(drift) == 0:
		return ShadowSyncStatusSynced
	case reported != nil && (reported.AppliedStatus != AppliedStatusApplied || len(drift) > 0 || (desired != nil && reported.ReportedRevision != desired.DesiredRevision)):
		return ShadowSyncStatusDrifted
	default:
		return ShadowSyncStatusUnconfigured
	}
}

func buildGatewayConfigShadow(tenantID, gatewayID string, desired *GatewayDesiredConfig, reported *GatewayReportedConfig) GatewayConfigShadow {
	drift := []DriftDetail{}
	if reported != nil {
		switch {
		case desired == nil, reported.ReportedRevision == desired.DesiredRevision, reported.ReportedRevision > desired.DesiredRevision:
			drift = computeConfigShadowDrift(desired, reported)
			if reported.AppliedStatus != AppliedStatusApplied {
				drift = mergeDriftDetails(drift, reported.DriftDetails)
			}
		}
	}
	return GatewayConfigShadow{
		TenantID:   tenantID,
		GatewayID:  gatewayID,
		SyncStatus: computeSyncStatus(desired, reported, drift),
		Desired:    desired,
		Reported:   reported,
		Drift:      drift,
	}
}

func mergeDriftDetails(computed, reported []DriftDetail) []DriftDetail {
	out := make([]DriftDetail, 0, len(computed)+len(reported))
	seen := make(map[string]struct{}, len(computed)+len(reported))
	add := func(item DriftDetail) {
		key := item.Section + "\x00" + item.Target + "\x00" + item.Reason + "\x00" + item.Detail
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	for _, item := range computed {
		add(item)
	}
	for _, item := range reported {
		add(item)
	}
	return out
}

func (store *Store) resolveActiveGatewayTenant(ctx context.Context, gatewayID string) (string, error) {
	if store == nil || store.pool == nil {
		return "", errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(gatewayID) {
		return "", ErrEnrollmentRejected
	}
	var tenantID string
	err := store.pool.QueryRow(ctx, `
SELECT tenant_id::text
FROM core_registry.gateway_directory_v1
WHERE gateway_id = $1::uuid AND status = 'ACTIVE'
`, gatewayID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read Registry gateway directory: %w", err)
	}
	return tenantID, nil
}

func loadDesiredConfigInTx(ctx context.Context, tx pgx.Tx, tenantID, gatewayID string, forUpdate bool) (*GatewayDesiredConfig, error) {
	query := `
SELECT desired_revision, point_mappings, sampling_policies, concentrator_configs,
       COALESCE(driver_configs, '[]'::jsonb), autonomy_policy, edge_rule_bundle, edge_models,
       updated_by::text, reason, created_at, updated_at
FROM connectivity.gateway_desired_configs
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}
	var cfg GatewayDesiredConfig
	var mappingsJSON, policiesJSON, unitsJSON, driversJSON, autonomyJSON, edgeRulesJSON, edgeModelsJSON []byte
	err := tx.QueryRow(ctx, query, tenantID, gatewayID).Scan(
		&cfg.DesiredRevision, &mappingsJSON, &policiesJSON, &unitsJSON,
		&driversJSON, &autonomyJSON, &edgeRulesJSON, &edgeModelsJSON,
		&cfg.UpdatedBy, &cfg.Reason, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load gateway desired config: %w", err)
	}
	cfg.CreatedAt = cfg.CreatedAt.UTC()
	cfg.UpdatedAt = cfg.UpdatedAt.UTC()
	if err := decodeShadowConfigPayload(
		mappingsJSON, policiesJSON, unitsJSON, driversJSON, autonomyJSON, edgeRulesJSON, edgeModelsJSON, nil,
		&cfg.PointMappings, &cfg.SamplingPolicies, &cfg.ConcentratorConfigs, &cfg.DriverConfigs, &cfg.AutonomyPolicy, &cfg.EdgeRuleBundle, &cfg.EdgeModels, nil,
	); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadReportedConfigInTx(ctx context.Context, tx pgx.Tx, tenantID, gatewayID string, forUpdate bool) (*GatewayReportedConfig, error) {
	query := `
SELECT reported_revision, applied_status, point_mappings, sampling_policies, concentrator_configs,
       COALESCE(driver_configs, '[]'::jsonb), autonomy_policy, edge_rule_bundle, edge_models, edge_model_statuses,
       drift_details, error_message, reported_at, updated_at
FROM connectivity.gateway_reported_configs
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}
	var cfg GatewayReportedConfig
	var mappingsJSON, policiesJSON, unitsJSON, driversJSON, autonomyJSON, edgeRulesJSON, edgeModelsJSON, edgeModelStatusesJSON, driftJSON []byte
	err := tx.QueryRow(ctx, query, tenantID, gatewayID).Scan(
		&cfg.ReportedRevision, &cfg.AppliedStatus, &mappingsJSON, &policiesJSON, &unitsJSON,
		&driversJSON, &autonomyJSON, &edgeRulesJSON, &edgeModelsJSON, &edgeModelStatusesJSON,
		&driftJSON, &cfg.ErrorMessage, &cfg.ReportedAt, &cfg.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load gateway reported config: %w", err)
	}
	cfg.ReportedAt = cfg.ReportedAt.UTC()
	cfg.UpdatedAt = cfg.UpdatedAt.UTC()
	if err := decodeShadowConfigPayload(
		mappingsJSON, policiesJSON, unitsJSON, driversJSON, autonomyJSON, edgeRulesJSON, edgeModelsJSON, edgeModelStatusesJSON,
		&cfg.PointMappings, &cfg.SamplingPolicies, &cfg.ConcentratorConfigs, &cfg.DriverConfigs, &cfg.AutonomyPolicy, &cfg.EdgeRuleBundle, &cfg.EdgeModels, &cfg.EdgeModelStatuses,
	); err != nil {
		return nil, err
	}
	cfg.DriftDetails = []DriftDetail{}
	if len(driftJSON) > 0 {
		if err := json.Unmarshal(driftJSON, &cfg.DriftDetails); err != nil {
			return nil, fmt.Errorf("decode gateway drift details: %w", err)
		}
		if cfg.DriftDetails == nil {
			cfg.DriftDetails = []DriftDetail{}
		}
	}
	return &cfg, nil
}

func decodeShadowConfigPayload(
	mappingsJSON, policiesJSON, unitsJSON, driversJSON, autonomyJSON, edgeRulesJSON, edgeModelsJSON, edgeModelStatusesJSON []byte,
	mappingsOut *[]GatewayPointMapping,
	policiesOut *[]GatewaySamplingPolicy,
	unitsOut *map[string]ConcentratorUnitShadow,
	driversOut *[]GatewayModbusDriverConfig,
	autonomyOut **GatewayAutonomyPolicy,
	edgeRulesOut **edgecontrol.EdgeRuleBundle,
	edgeModelsOut *[]edgecontrol.EdgeModelBundle,
	edgeModelStatusesOut *[]edgecontrol.EdgeModelRuntimeStatus,
) error {
	*mappingsOut = []GatewayPointMapping{}
	if len(mappingsJSON) > 0 {
		if err := json.Unmarshal(mappingsJSON, mappingsOut); err != nil {
			return fmt.Errorf("decode pointMappings: %w", err)
		}
		if *mappingsOut == nil {
			*mappingsOut = []GatewayPointMapping{}
		}
	}
	*policiesOut = []GatewaySamplingPolicy{}
	if len(policiesJSON) > 0 {
		if err := json.Unmarshal(policiesJSON, policiesOut); err != nil {
			return fmt.Errorf("decode samplingPolicies: %w", err)
		}
		if *policiesOut == nil {
			*policiesOut = []GatewaySamplingPolicy{}
		}
	}
	*unitsOut = map[string]ConcentratorUnitShadow{}
	if len(unitsJSON) > 0 {
		if err := json.Unmarshal(unitsJSON, unitsOut); err != nil {
			return fmt.Errorf("decode concentratorConfigs: %w", err)
		}
		if *unitsOut == nil {
			*unitsOut = map[string]ConcentratorUnitShadow{}
		}
	}
	*driversOut = []GatewayModbusDriverConfig{}
	if len(driversJSON) > 0 {
		if err := json.Unmarshal(driversJSON, driversOut); err != nil {
			return fmt.Errorf("decode driverConfigs: %w", err)
		}
		if *driversOut == nil {
			*driversOut = []GatewayModbusDriverConfig{}
		}
	}
	*autonomyOut = nil
	if trimmed := bytes.TrimSpace(autonomyJSON); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var parsed GatewayAutonomyPolicy
		if err := json.Unmarshal(trimmed, &parsed); err != nil {
			return fmt.Errorf("decode autonomyPolicy: %w", err)
		}
		*autonomyOut = &parsed
	}
	*edgeRulesOut = nil
	if trimmed := bytes.TrimSpace(edgeRulesJSON); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var parsed edgecontrol.EdgeRuleBundle
		if err := json.Unmarshal(trimmed, &parsed); err != nil {
			return fmt.Errorf("decode edgeRuleBundle: %w", err)
		}
		*edgeRulesOut = &parsed
	}
	if edgeModelsOut != nil && len(bytes.TrimSpace(edgeModelsJSON)) > 0 && !bytes.Equal(bytes.TrimSpace(edgeModelsJSON), []byte("null")) {
		var parsed []edgecontrol.EdgeModelBundle
		if err := json.Unmarshal(edgeModelsJSON, &parsed); err != nil {
			return fmt.Errorf("decode edgeModels: %w", err)
		}
		*edgeModelsOut = parsed
	}
	if edgeModelStatusesOut != nil && len(bytes.TrimSpace(edgeModelStatusesJSON)) > 0 && !bytes.Equal(bytes.TrimSpace(edgeModelStatusesJSON), []byte("null")) {
		var parsed []edgecontrol.EdgeModelRuntimeStatus
		if err := json.Unmarshal(edgeModelStatusesJSON, &parsed); err != nil {
			return fmt.Errorf("decode edgeModelStatuses: %w", err)
		}
		*edgeModelStatusesOut = parsed
	}
	return nil
}

func (store *Store) GetGatewayConfigShadowForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (GatewayConfigShadow, error) {
	tenantID, err := store.ResolveGatewayScopeForOperator(ctx, claims, gatewayID)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	var desired *GatewayDesiredConfig
	var reported *GatewayReportedConfig
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var loadErr error
		desired, loadErr = loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, false)
		if loadErr != nil {
			return loadErr
		}
		reported, loadErr = loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, false)
		return loadErr
	}); err != nil {
		return GatewayConfigShadow{}, err
	}
	return buildGatewayConfigShadow(tenantID, gatewayID, desired, reported), nil
}

func (store *Store) LoadGatewayConfigShadowByGatewayID(ctx context.Context, gatewayID string) (GatewayConfigShadow, error) {
	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	var desired *GatewayDesiredConfig
	var reported *GatewayReportedConfig
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var loadErr error
		desired, loadErr = loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, false)
		if loadErr != nil {
			return loadErr
		}
		reported, loadErr = loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, false)
		return loadErr
	}); err != nil {
		return GatewayConfigShadow{}, err
	}
	return buildGatewayConfigShadow(tenantID, gatewayID, desired, reported), nil
}

func persistDesiredConfigInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, gatewayID string,
	desired GatewayDesiredConfig,
	encoded encodedShadowSections,
	isInsert bool,
) error {
	if isInsert {
		if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_desired_configs (
  tenant_id, gateway_id, desired_revision, point_mappings, sampling_policies, concentrator_configs,
  driver_configs, autonomy_policy, edge_rule_bundle, edge_models, updated_by, reason, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb, $10::jsonb, $11::uuid, $12, $13, $13
)`, tenantID, gatewayID, desired.DesiredRevision,
			string(encoded.mappingsJSON), string(encoded.policiesJSON), string(encoded.unitsJSON),
			string(encoded.driversJSON), encoded.autonomyArg, encoded.edgeRulesArg, encoded.edgeModelsArg, desired.UpdatedBy, desired.Reason, desired.UpdatedAt); err != nil {
			return fmt.Errorf("insert gateway desired config: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.gateway_desired_configs
SET desired_revision = $3,
    point_mappings = $4::jsonb,
    sampling_policies = $5::jsonb,
    concentrator_configs = $6::jsonb,
    driver_configs = $7::jsonb,
    autonomy_policy = $8::jsonb,
    edge_rule_bundle = $9::jsonb,
    edge_models = $10::jsonb,
    updated_by = $11::uuid,
    reason = $12,
    updated_at = $13
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
`, tenantID, gatewayID, desired.DesiredRevision,
		string(encoded.mappingsJSON), string(encoded.policiesJSON), string(encoded.unitsJSON),
		string(encoded.driversJSON), encoded.autonomyArg, encoded.edgeRulesArg, encoded.edgeModelsArg, desired.UpdatedBy, desired.Reason, desired.UpdatedAt); err != nil {
		return fmt.Errorf("update gateway desired config: %w", err)
	}
	return nil
}

func upsertReportedConfigInTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, gatewayID string,
	reported GatewayReportedConfig,
	encoded encodedShadowSections,
	driftJSON []byte,
) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.gateway_reported_configs (
  tenant_id, gateway_id, reported_revision, applied_status,
  point_mappings, sampling_policies, concentrator_configs, driver_configs, autonomy_policy, edge_rule_bundle, edge_models, edge_model_statuses, drift_details,
  error_message, reported_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb, $10::jsonb, $11::jsonb, $12::jsonb, $13::jsonb, $14, $15, $16
)
ON CONFLICT (tenant_id, gateway_id) DO UPDATE SET
  reported_revision = EXCLUDED.reported_revision,
  applied_status = EXCLUDED.applied_status,
  point_mappings = EXCLUDED.point_mappings,
  sampling_policies = EXCLUDED.sampling_policies,
  concentrator_configs = EXCLUDED.concentrator_configs,
  driver_configs = EXCLUDED.driver_configs,
  autonomy_policy = EXCLUDED.autonomy_policy,
  edge_rule_bundle = EXCLUDED.edge_rule_bundle,
  edge_models = EXCLUDED.edge_models,
  edge_model_statuses = EXCLUDED.edge_model_statuses,
  drift_details = EXCLUDED.drift_details,
  error_message = EXCLUDED.error_message,
  reported_at = EXCLUDED.reported_at,
  updated_at = EXCLUDED.updated_at
`, tenantID, gatewayID, reported.ReportedRevision, reported.AppliedStatus,
		string(encoded.mappingsJSON), string(encoded.policiesJSON), string(encoded.unitsJSON),
		string(encoded.driversJSON), encoded.autonomyArg, encoded.edgeRulesArg, encoded.edgeModelsArg, encoded.edgeModelStatusesArg, string(driftJSON),
		reported.ErrorMessage, reported.ReportedAt, reported.UpdatedAt); err != nil {
		return fmt.Errorf("upsert gateway reported config: %w", err)
	}
	return nil
}

func (store *Store) UpdateGatewayDesiredConfig(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, input UpdateDesiredConfigInput) (GatewayConfigShadow, error) {
	if input.ExpectedDesiredRevision != nil && *input.ExpectedDesiredRevision < 0 {
		return GatewayConfigShadow{}, fmt.Errorf("%w: expectedDesiredRevision must be non-negative", ErrConfigShadowInvalid)
	}
	reason, err := validateShadowReason(input.Reason)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	if !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return GatewayConfigShadow{}, fmt.Errorf("%w: actor principalId must be UUIDv7", ErrConfigShadowInvalid)
	}
	var cleanedMappings []GatewayPointMapping
	if input.PointMappings != nil {
		cleanedMappings, err = validatePointMappings(*input.PointMappings)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	var cleanedPolicies []GatewaySamplingPolicy
	if input.SamplingPolicies != nil {
		cleanedPolicies, err = validateSamplingPolicies(*input.SamplingPolicies)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	var cleanedUnits map[string]ConcentratorUnitShadow
	if input.ConcentratorConfigs != nil {
		cleanedUnits, err = validateConcentratorConfigs(*input.ConcentratorConfigs)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	var cleanedDrivers []GatewayModbusDriverConfig
	if input.DriverConfigs != nil {
		cleanedDrivers, err = validateDriverConfigs(*input.DriverConfigs)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	var cleanedAutonomy *GatewayAutonomyPolicy
	if input.AutonomyPolicy.Value != nil {
		cleanedAutonomy, err = validateAutonomyPolicy(input.AutonomyPolicy.Value)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	var cleanedEdgeRules *edgecontrol.EdgeRuleBundle
	if input.EdgeRuleBundle.Value != nil {
		cleanedEdgeRules, err = validateEdgeRuleBundle(input.EdgeRuleBundle.Value)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	tenantID, siteID, err := store.resolveGatewayTenantAndSiteForOperator(ctx, claims, gatewayID)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	if cleanedEdgeRules != nil && !strings.EqualFold(cleanedEdgeRules.SiteID, siteID) {
		return GatewayConfigShadow{}, fmt.Errorf("%w: edgeRuleBundle siteId does not match gateway site", ErrConfigShadowInvalid)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return GatewayConfigShadow{}, err
	}

	existing, err := loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return GatewayConfigShadow{}, err
	}

	effectiveMappings := []GatewayPointMapping{}
	if input.PointMappings != nil {
		effectiveMappings = cleanedMappings
	} else if existing != nil {
		effectiveMappings = clonePointMappingsList(existing.PointMappings)
	}

	effectivePolicies := []GatewaySamplingPolicy{}
	if input.SamplingPolicies != nil {
		effectivePolicies = cleanedPolicies
	} else if existing != nil {
		effectivePolicies = cloneSamplingPoliciesList(existing.SamplingPolicies)
	}

	effectiveUnits := map[string]ConcentratorUnitShadow{}
	if input.ConcentratorConfigs != nil {
		effectiveUnits = cleanedUnits
	} else if existing != nil {
		effectiveUnits = cloneConcentratorUnits(existing.ConcentratorConfigs)
	}

	effectiveDrivers := []GatewayModbusDriverConfig{}
	if input.DriverConfigs != nil {
		effectiveDrivers = cleanedDrivers
	} else if existing != nil {
		effectiveDrivers = cloneDriverConfigsList(existing.DriverConfigs)
	}

	var effectiveAutonomy *GatewayAutonomyPolicy
	if input.AutonomyPolicy.IsSet() {
		effectiveAutonomy = cleanedAutonomy
	} else if existing != nil {
		effectiveAutonomy = cloneAutonomyPolicy(existing.AutonomyPolicy)
	}

	var effectiveEdgeRules *edgecontrol.EdgeRuleBundle
	if input.EdgeRuleBundle.IsSet() {
		effectiveEdgeRules = cleanedEdgeRules
	} else if existing != nil {
		effectiveEdgeRules = existing.EdgeRuleBundle.Clone()
	}

	var effectiveEdgeModels []edgecontrol.EdgeModelBundle
	if input.EdgeModels.IsSet() {
		effectiveEdgeModels, err = validateEdgeModelBundles(input.EdgeModels.Value)
		if err != nil {
			return GatewayConfigShadow{}, err
		}
	} else if existing != nil {
		effectiveEdgeModels = cloneEdgeModelBundles(existing.EdgeModels)
	}

	encoded, err := marshalShadowSections(effectiveMappings, effectivePolicies, effectiveUnits, effectiveDrivers, effectiveAutonomy, effectiveEdgeRules, effectiveEdgeModels, nil)
	if err != nil {
		return GatewayConfigShadow{}, err
	}

	now := store.clock().UTC()
	var desired GatewayDesiredConfig
	if existing == nil {
		if input.ExpectedDesiredRevision != nil && *input.ExpectedDesiredRevision != 0 {
			return GatewayConfigShadow{}, ErrConfigShadowConflict
		}
		desired = GatewayDesiredConfig{
			DesiredRevision:     1,
			PointMappings:       effectiveMappings,
			SamplingPolicies:    effectivePolicies,
			ConcentratorConfigs: effectiveUnits,
			DriverConfigs:       effectiveDrivers,
			AutonomyPolicy:      effectiveAutonomy,
			EdgeRuleBundle:      effectiveEdgeRules,
			EdgeModels:          effectiveEdgeModels,
			UpdatedBy:           claims.PrincipalID,
			Reason:              reason,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := persistDesiredConfigInTx(ctx, tx, tenantID, gatewayID, desired, encoded, true); err != nil {
			return GatewayConfigShadow{}, err
		}
	} else {
		if input.ExpectedDesiredRevision != nil && *input.ExpectedDesiredRevision != existing.DesiredRevision {
			return GatewayConfigShadow{}, ErrConfigShadowConflict
		}
		desired = GatewayDesiredConfig{
			DesiredRevision:     existing.DesiredRevision + 1,
			PointMappings:       effectiveMappings,
			SamplingPolicies:    effectivePolicies,
			ConcentratorConfigs: effectiveUnits,
			DriverConfigs:       effectiveDrivers,
			AutonomyPolicy:      effectiveAutonomy,
			EdgeRuleBundle:      effectiveEdgeRules,
			EdgeModels:          effectiveEdgeModels,
			UpdatedBy:           claims.PrincipalID,
			Reason:              reason,
			CreatedAt:           existing.CreatedAt,
			UpdatedAt:           now,
		}
		if err := persistDesiredConfigInTx(ctx, tx, tenantID, gatewayID, desired, encoded, false); err != nil {
			return GatewayConfigShadow{}, err
		}
	}

	reported, err := loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, false)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GatewayConfigShadow{}, err
	}
	return buildGatewayConfigShadow(tenantID, gatewayID, &desired, reported), nil
}

func (store *Store) PullGatewayDesiredConfig(ctx context.Context, gatewayID string, sinceRevision int64) (GatewayShadowPullResponse, error) {
	if sinceRevision < 0 {
		return GatewayShadowPullResponse{}, fmt.Errorf("%w: sinceRevision must be non-negative", ErrConfigShadowInvalid)
	}
	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return GatewayShadowPullResponse{}, err
	}
	var desired *GatewayDesiredConfig
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var loadErr error
		desired, loadErr = loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, false)
		return loadErr
	}); err != nil {
		return GatewayShadowPullResponse{}, err
	}
	if desired == nil {
		return GatewayShadowPullResponse{
			GatewayID:       gatewayID,
			DesiredRevision: 0,
			HasDelta:        false,
		}, nil
	}
	hasDelta := desired.DesiredRevision > sinceRevision
	resp := GatewayShadowPullResponse{
		GatewayID:       gatewayID,
		DesiredRevision: desired.DesiredRevision,
		HasDelta:        hasDelta,
	}
	if hasDelta {
		resp.Desired = desired
	}
	return resp, nil
}

func validateAppliedStatus(status string) (string, error) {
	trimmed := strings.TrimSpace(status)
	switch trimmed {
	case AppliedStatusApplied, AppliedStatusPartial, AppliedStatusRejected:
		return trimmed, nil
	default:
		return "", fmt.Errorf("%w: unsupported appliedStatus %q", ErrConfigShadowInvalid, status)
	}
}

func validateReportDriftDetails(details []DriftDetail) ([]DriftDetail, []byte, error) {
	if details == nil {
		details = []DriftDetail{}
	}
	out := make([]DriftDetail, 0, len(details))
	for i, item := range details {
		section := strings.TrimSpace(item.Section)
		reason := strings.TrimSpace(item.Reason)
		target := strings.TrimSpace(item.Target)
		detail := strings.TrimSpace(item.Detail)
		switch section {
		case DriftSectionPointMappings, DriftSectionSamplingPolicies, DriftSectionConcentrator, DriftSectionDriverConfigs, DriftSectionAutonomyPolicy, DriftSectionEdgeRules, DriftSectionEdgeModels:
		default:
			return nil, nil, fmt.Errorf("%w: driftDetails[%d].section %q is invalid", ErrConfigShadowInvalid, i, item.Section)
		}
		switch reason {
		case DriftReasonValueMismatch, DriftReasonMissingOnEdge, DriftReasonUnexpectedOnEdge, DriftReasonEdgeApplyFailed:
		default:
			return nil, nil, fmt.Errorf("%w: driftDetails[%d].reason %q is invalid", ErrConfigShadowInvalid, i, item.Reason)
		}
		targetRunes := utf8.RuneCountInString(target)
		if targetRunes == 0 || targetRunes > maxDriftTargetRunes {
			return nil, nil, fmt.Errorf("%w: driftDetails[%d].target must be 1..%d characters", ErrConfigShadowInvalid, i, maxDriftTargetRunes)
		}
		detailRunes := utf8.RuneCountInString(detail)
		if detailRunes == 0 || detailRunes > maxDriftDetailRunes {
			return nil, nil, fmt.Errorf("%w: driftDetails[%d].detail must be 1..%d characters", ErrConfigShadowInvalid, i, maxDriftDetailRunes)
		}
		out = append(out, DriftDetail{
			Section: section,
			Target:  target,
			Reason:  reason,
			Detail:  detail,
		})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encode driftDetails: %v", ErrConfigShadowInvalid, err)
	}
	if estimatedPostgresJSONBTextBytes(encoded) > maxDriftDetailsJSONBytes {
		return nil, nil, fmt.Errorf("%w: driftDetails exceeds %d bytes", ErrConfigShadowInvalid, maxDriftDetailsJSONBytes)
	}
	return out, encoded, nil
}

func mergeShadowErrorMessages(messages ...*string) *string {
	var parts []string
	seen := make(map[string]struct{}, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		trimmed := strings.TrimSpace(*msg)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 {
		return nil
	}
	joined := clampUTF8Runes(strings.Join(parts, "; "), maxShadowErrorRunes)
	return &joined
}

func (store *Store) ReportGatewayAppliedConfig(ctx context.Context, gatewayID string, input GatewayShadowReportInput) (GatewayConfigShadow, error) {
	if input.ReportedRevision < 0 {
		return GatewayConfigShadow{}, fmt.Errorf("%w: reportedRevision must be non-negative", ErrConfigShadowInvalid)
	}
	appliedStatus, err := validateAppliedStatus(input.AppliedStatus)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	var errMsg *string
	if input.ErrorMessage != nil {
		trimmed := strings.TrimSpace(*input.ErrorMessage)
		if utf8.RuneCountInString(trimmed) > maxShadowErrorRunes {
			return GatewayConfigShadow{}, fmt.Errorf("%w: errorMessage exceeds %d characters", ErrConfigShadowInvalid, maxShadowErrorRunes)
		}
		if trimmed != "" {
			errMsg = &trimmed
		}
	}
	mappings, err := validatePointMappings(input.PointMappings)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	policies, err := validateSamplingPolicies(input.SamplingPolicies)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	if input.ConcentratorConfigs != nil {
		if _, err := validateConcentratorConfigs(input.ConcentratorConfigs); err != nil {
			return GatewayConfigShadow{}, err
		}
	}
	drivers, err := validateDriverConfigs(input.DriverConfigs)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	autonomy, err := validateAutonomyPolicy(input.AutonomyPolicy)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	edgeRules, err := validateEdgeRuleBundle(input.EdgeRuleBundle)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	inputDrifts, _, err := validateReportDriftDetails(input.DriftDetails)
	if err != nil {
		return GatewayConfigShadow{}, err
	}

	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	now := store.clock().UTC()
	reportedAt := now
	if input.ReportedAt != nil && !input.ReportedAt.IsZero() {
		reportedAt = input.ReportedAt.UTC()
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return GatewayConfigShadow{}, err
	}

	desired, err := loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	existingReported, err := loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	if existingReported != nil && input.ReportedRevision < existingReported.ReportedRevision {
		return GatewayConfigShadow{}, ErrConfigShadowConflict
	}

	// ConcentratorConfigs are owned server-side by concentrator.Service reconciliation;
	// edge shadow reports must never overwrite or clear hardware-verified unit state.
	preservedUnits := map[string]ConcentratorUnitShadow{}
	if existingReported != nil {
		preservedUnits = cloneConcentratorUnits(existingReported.ConcentratorConfigs)
	}
	edgeModels, err := validateEdgeModelBundles(input.EdgeModels)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	edgeModelStatuses, err := validateEdgeModelStatuses(input.EdgeModelStatuses)
	if err != nil {
		return GatewayConfigShadow{}, err
	}
	encoded, err := marshalShadowSections(mappings, policies, preservedUnits, drivers, autonomy, edgeRules, edgeModels, edgeModelStatuses)
	if err != nil {
		return GatewayConfigShadow{}, err
	}

	reported := GatewayReportedConfig{
		ReportedRevision:    input.ReportedRevision,
		AppliedStatus:       appliedStatus,
		PointMappings:       mappings,
		SamplingPolicies:    policies,
		ConcentratorConfigs: preservedUnits,
		DriverConfigs:       drivers,
		AutonomyPolicy:      autonomy,
		EdgeRuleBundle:      edgeRules,
		EdgeModels:          edgeModels,
		EdgeModelStatuses:   edgeModelStatuses,
		ErrorMessage:        errMsg,
		ReportedAt:          reportedAt,
		UpdatedAt:           now,
	}

	var existingConcFailureDrifts []DriftDetail
	var existingConcErrMsg *string
	if existingReported != nil && desired != nil && reported.ReportedRevision >= desired.DesiredRevision {
		for _, item := range existingReported.DriftDetails {
			if item.Section == DriftSectionConcentrator && item.Reason == DriftReasonEdgeApplyFailed {
				existingConcFailureDrifts = append(existingConcFailureDrifts, item)
			}
		}
		if len(existingConcFailureDrifts) > 0 {
			existingConcErrMsg = existingReported.ErrorMessage
		}
	}

	var storedDrift []DriftDetail
	if desired == nil || reported.ReportedRevision >= desired.DesiredRevision {
		storedDrift = computeConfigShadowDrift(desired, &reported)
		if appliedStatus != AppliedStatusApplied {
			storedDrift = mergeDriftDetails(storedDrift, inputDrifts)
		}
		if len(existingConcFailureDrifts) > 0 {
			storedDrift = mergeDriftDetails(storedDrift, existingConcFailureDrifts)
			if reported.AppliedStatus == AppliedStatusApplied {
				reported.AppliedStatus = AppliedStatusPartial
			}
			reported.ErrorMessage = mergeShadowErrorMessages(reported.ErrorMessage, existingConcErrMsg)
		} else if desired != nil && len(diffConcentratorConfigs(desired.ConcentratorConfigs, preservedUnits)) > 0 && reported.AppliedStatus == AppliedStatusApplied {
			reported.AppliedStatus = AppliedStatusPartial
		}
	} else if appliedStatus != AppliedStatusApplied {
		storedDrift = inputDrifts
	} else {
		storedDrift = []DriftDetail{}
	}
	reported.DriftDetails = storedDrift

	driftJSON, err := marshalDriftDetailsChecked(storedDrift)
	if err != nil {
		return GatewayConfigShadow{}, err
	}

	if err := upsertReportedConfigInTx(ctx, tx, tenantID, gatewayID, reported, encoded, driftJSON); err != nil {
		return GatewayConfigShadow{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return GatewayConfigShadow{}, err
	}
	return buildGatewayConfigShadow(tenantID, gatewayID, desired, &reported), nil
}

func (store *Store) UpsertConcentratorShadowState(
	ctx context.Context,
	tenantID, gatewayID string,
	unitID int,
	params *ConcentratorShadowParams,
	eids []ConcentratorShadowEIDSlot,
	actorID, reason string,
	appliedToEdge bool,
) (int64, error) {
	if store == nil || store.pool == nil {
		return 0, errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(tenantID) || !gatewayIDPattern.MatchString(gatewayID) || !gatewayIDPattern.MatchString(actorID) {
		return 0, fmt.Errorf("%w: tenantId, gatewayId, and actorId must be UUIDv7", ErrConfigShadowInvalid)
	}
	if unitID < 1 || unitID > 247 || IsProtectedConcentratorUID(unitID) {
		return 0, fmt.Errorf("%w: invalid or protected concentrator unitId %d", ErrConfigShadowInvalid, unitID)
	}
	validatedReason, err := validateShadowReason(reason)
	if err != nil {
		return 0, err
	}
	var cleanedParams *ConcentratorShadowParams
	if params != nil {
		if err := ValidateConcentratorShadowParams(*params); err != nil {
			return 0, err
		}
		cp := *params
		cleanedParams = &cp
	}
	var cleanedEIDs []ConcentratorShadowEIDSlot
	if eids != nil {
		cleanedEIDs, err = ValidateConcentratorShadowEIDSlots(eids)
		if err != nil {
			return 0, err
		}
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return 0, err
	}

	existingDesired, err := loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return 0, err
	}
	existingReported, err := loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return 0, err
	}

	now := store.clock().UTC()
	oldUnitKey := strconv.Itoa(unitID)
	targetUnitKey := oldUnitKey
	if appliedToEdge && cleanedParams != nil && cleanedParams.ModbusID > 0 && cleanedParams.ModbusID != unitID {
		targetUnitKey = strconv.Itoa(cleanedParams.ModbusID)
	}

	var desired GatewayDesiredConfig
	var oldDesiredRev int64
	if existingDesired == nil {
		desired = GatewayDesiredConfig{
			DesiredRevision:     1,
			PointMappings:       []GatewayPointMapping{},
			SamplingPolicies:    []GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
			DriverConfigs:       []GatewayModbusDriverConfig{},
			UpdatedBy:           actorID,
			Reason:              validatedReason,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
	} else {
		oldDesiredRev = existingDesired.DesiredRevision
		desired = *existingDesired
		desired.DesiredRevision = existingDesired.DesiredRevision + 1
		desired.UpdatedBy = actorID
		desired.Reason = validatedReason
		desired.UpdatedAt = now
		desired.ConcentratorConfigs = cloneConcentratorUnits(desired.ConcentratorConfigs)
		desired.DriverConfigs = cloneDriverConfigsList(desired.DriverConfigs)
		desired.AutonomyPolicy = cloneAutonomyPolicy(desired.AutonomyPolicy)
		desired.EdgeRuleBundle = desired.EdgeRuleBundle.Clone()
	}

	if !appliedToEdge && cleanedParams != nil && cleanedParams.ModbusID > 0 && cleanedParams.ModbusID != unitID {
		newKey := strconv.Itoa(cleanedParams.ModbusID)
		if _, collides := desired.ConcentratorConfigs[newKey]; collides {
			return 0, fmt.Errorf("%w: target modbusId %d already exists in concentratorConfigs", ErrConfigShadowInvalid, cleanedParams.ModbusID)
		}
	}

	unitDesired := desired.ConcentratorConfigs[oldUnitKey]
	if targetUnitKey != oldUnitKey {
		if targetExisting, ok := desired.ConcentratorConfigs[targetUnitKey]; ok && unitDesired.EIDs == nil {
			unitDesired.EIDs = targetExisting.EIDs
		}
		delete(desired.ConcentratorConfigs, oldUnitKey)
	}
	if cleanedParams != nil {
		unitDesired.Params = cleanedParams
	}
	if cleanedEIDs != nil {
		unitDesired.EIDs = cleanedEIDs
	}
	desired.ConcentratorConfigs[targetUnitKey] = unitDesired

	dEncoded, err := marshalShadowSections(desired.PointMappings, desired.SamplingPolicies, desired.ConcentratorConfigs, desired.DriverConfigs, desired.AutonomyPolicy, desired.EdgeRuleBundle, desired.EdgeModels, nil)
	if err != nil {
		return 0, err
	}
	if err := persistDesiredConfigInTx(ctx, tx, tenantID, gatewayID, desired, dEncoded, existingDesired == nil); err != nil {
		return 0, err
	}

	if appliedToEdge {
		var reported GatewayReportedConfig
		if existingReported == nil {
			reported = GatewayReportedConfig{
				ReportedRevision:    desired.DesiredRevision,
				AppliedStatus:       AppliedStatusApplied,
				PointMappings:       []GatewayPointMapping{},
				SamplingPolicies:    []GatewaySamplingPolicy{},
				ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
				DriverConfigs:       []GatewayModbusDriverConfig{},
				DriftDetails:        []DriftDetail{},
				ReportedAt:          now,
				UpdatedAt:           now,
			}
			if len(desired.PointMappings) > 0 || len(desired.SamplingPolicies) > 0 || len(desired.DriverConfigs) > 0 || desired.AutonomyPolicy != nil || desired.EdgeRuleBundle != nil {
				reported.ReportedRevision = 0
			}
		} else {
			reported = *existingReported
			reported.ReportedAt = now
			reported.UpdatedAt = now
			if reported.ConcentratorConfigs == nil {
				reported.ConcentratorConfigs = map[string]ConcentratorUnitShadow{}
			}
			reported.DriverConfigs = cloneDriverConfigsList(existingReported.DriverConfigs)
			reported.AutonomyPolicy = cloneAutonomyPolicy(existingReported.AutonomyPolicy)
			reported.EdgeRuleBundle = existingReported.EdgeRuleBundle.Clone()
			if oldDesiredRev > 0 && existingReported.ReportedRevision == oldDesiredRev && existingReported.AppliedStatus == AppliedStatusApplied {
				reported.ReportedRevision = desired.DesiredRevision
			}
		}
		unitReported := reported.ConcentratorConfigs[oldUnitKey]
		if targetUnitKey != oldUnitKey {
			if targetExisting, ok := reported.ConcentratorConfigs[targetUnitKey]; ok && unitReported.EIDs == nil {
				unitReported.EIDs = targetExisting.EIDs
			}
			delete(reported.ConcentratorConfigs, oldUnitKey)
		}
		if cleanedParams != nil {
			cp := *cleanedParams
			unitReported.Params = &cp
		}
		if cleanedEIDs != nil {
			unitReported.EIDs = slices.Clone(cleanedEIDs)
		}
		reported.ConcentratorConfigs[targetUnitKey] = unitReported

		drift := []DriftDetail{}
		if reported.ReportedRevision == desired.DesiredRevision {
			drift = computeConfigShadowDrift(&desired, &reported)
		}
		reported.DriftDetails = drift
		if len(drift) == 0 && reported.ReportedRevision == desired.DesiredRevision {
			reported.AppliedStatus = AppliedStatusApplied
			reported.ErrorMessage = nil
		}

		rEncoded, err := marshalShadowSections(reported.PointMappings, reported.SamplingPolicies, reported.ConcentratorConfigs, reported.DriverConfigs, reported.AutonomyPolicy, reported.EdgeRuleBundle, reported.EdgeModels, reported.EdgeModelStatuses)
		if err != nil {
			return 0, err
		}
		rDriftJSON, err := marshalDriftDetailsChecked(reported.DriftDetails)
		if err != nil {
			return 0, err
		}
		if err := upsertReportedConfigInTx(ctx, tx, tenantID, gatewayID, reported, rEncoded, rDriftJSON); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return desired.DesiredRevision, nil
}

func (store *Store) RecordConcentratorReconcileResult(
	ctx context.Context,
	tenantID, gatewayID string,
	desiredRevision int64,
	reconciledUnits map[string]ConcentratorUnitShadow,
	renamedUnits map[string]string,
	failedDrifts []DriftDetail,
	errorMessage *string,
	gatewayUnreachable bool,
) error {
	if store == nil || store.pool == nil {
		return errors.New("connectivity store is closed")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return err
	}

	desired, err := loadDesiredConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return err
	}
	existingReported, err := loadReportedConfigInTx(ctx, tx, tenantID, gatewayID, true)
	if err != nil {
		return err
	}

	now := store.clock().UTC()
	var reported GatewayReportedConfig
	if existingReported == nil {
		reported = GatewayReportedConfig{
			ReportedRevision:    0,
			AppliedStatus:       AppliedStatusApplied,
			PointMappings:       []GatewayPointMapping{},
			SamplingPolicies:    []GatewaySamplingPolicy{},
			ConcentratorConfigs: map[string]ConcentratorUnitShadow{},
			DriverConfigs:       []GatewayModbusDriverConfig{},
			DriftDetails:        []DriftDetail{},
			ReportedAt:          now,
			UpdatedAt:           now,
		}
	} else {
		reported = *existingReported
		reported.ReportedAt = now
		reported.UpdatedAt = now
		reported.PointMappings = clonePointMappingsList(existingReported.PointMappings)
		reported.SamplingPolicies = cloneSamplingPoliciesList(existingReported.SamplingPolicies)
		reported.ConcentratorConfigs = cloneConcentratorUnits(existingReported.ConcentratorConfigs)
		reported.DriverConfigs = cloneDriverConfigsList(existingReported.DriverConfigs)
		reported.AutonomyPolicy = cloneAutonomyPolicy(existingReported.AutonomyPolicy)
		reported.EdgeRuleBundle = existingReported.EdgeRuleBundle.Clone()
		reported.DriftDetails = slices.Clone(existingReported.DriftDetails)
	}

	for oldKey, newKey := range renamedUnits {
		if oldKey == "" || newKey == "" || oldKey == newKey {
			continue
		}
		if prev, exists := reported.ConcentratorConfigs[oldKey]; exists {
			if target, hasTarget := reported.ConcentratorConfigs[newKey]; hasTarget && prev.EIDs == nil {
				prev.EIDs = target.EIDs
			}
			delete(reported.ConcentratorConfigs, oldKey)
			reported.ConcentratorConfigs[newKey] = prev
		}
	}

	for unitKey, unit := range reconciledUnits {
		current := reported.ConcentratorConfigs[unitKey]
		if unit.Params != nil {
			cp := *unit.Params
			current.Params = &cp
		}
		if unit.EIDs != nil {
			current.EIDs = slices.Clone(unit.EIDs)
		}
		reported.ConcentratorConfigs[unitKey] = current
	}

	if desired != nil && desired.DesiredRevision == desiredRevision && len(renamedUnits) > 0 {
		rekeyedDesired := cloneConcentratorUnits(desired.ConcentratorConfigs)
		desiredChanged := false
		for oldKey, newKey := range renamedUnits {
			if oldKey == "" || newKey == "" || oldKey == newKey {
				continue
			}
			if prev, exists := rekeyedDesired[oldKey]; exists {
				if target, hasTarget := rekeyedDesired[newKey]; hasTarget && prev.EIDs == nil {
					prev.EIDs = target.EIDs
				}
				delete(rekeyedDesired, oldKey)
				rekeyedDesired[newKey] = prev
				desiredChanged = true
			}
		}
		if desiredChanged {
			desired.ConcentratorConfigs = rekeyedDesired
			dEncoded, err := marshalShadowSections(desired.PointMappings, desired.SamplingPolicies, desired.ConcentratorConfigs, desired.DriverConfigs, desired.AutonomyPolicy, desired.EdgeRuleBundle, desired.EdgeModels, nil)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE connectivity.gateway_desired_configs
SET concentrator_configs = $3::jsonb,
    updated_at = $4
WHERE tenant_id = $1::uuid AND gateway_id = $2::uuid
`, tenantID, gatewayID, string(dEncoded.unitsJSON), now); err != nil {
				return fmt.Errorf("rekey desired concentrator unit: %w", err)
			}
		}
	}

	sameDesiredRev := desired != nil && desired.DesiredRevision == desiredRevision
	sameReportedDesiredRev := sameDesiredRev && existingReported != nil && existingReported.ReportedRevision == desiredRevision

	priorNonConcDrifts := make([]DriftDetail, 0, len(reported.DriftDetails))
	sameRevEdgeFailDrifts := make([]DriftDetail, 0, len(reported.DriftDetails))
	existingHadConcIssue := false
	for _, item := range reported.DriftDetails {
		if item.Section == DriftSectionConcentrator {
			existingHadConcIssue = true
			continue
		}
		priorNonConcDrifts = append(priorNonConcDrifts, item)
		if sameReportedDesiredRev && item.Reason == DriftReasonEdgeApplyFailed {
			sameRevEdgeFailDrifts = append(sameRevEdgeFailDrifts, item)
		}
	}
	if !existingHadConcIssue && existingReported != nil && desired != nil && len(diffConcentratorConfigs(desired.ConcentratorConfigs, existingReported.ConcentratorConfigs)) > 0 {
		existingHadConcIssue = true
	}

	if sameDesiredRev {
		edgeCaughtUp := (existingReported != nil && existingReported.ReportedRevision == desiredRevision) ||
			(len(desired.PointMappings) == 0 && len(desired.SamplingPolicies) == 0 && len(desired.DriverConfigs) == 0 && desired.AutonomyPolicy == nil && desired.EdgeRuleBundle == nil && len(desired.EdgeModels) == 0 &&
				len(reported.PointMappings) == 0 && len(reported.SamplingPolicies) == 0 && len(reported.DriverConfigs) == 0 && reported.AutonomyPolicy == nil && reported.EdgeRuleBundle == nil && len(reported.EdgeModels) == 0)

		if len(failedDrifts) == 0 {
			if edgeCaughtUp {
				reported.ReportedRevision = desiredRevision
				computedDrift := computeConfigShadowDrift(desired, &reported)
				allDrift := mergeDriftDetails(computedDrift, sameRevEdgeFailDrifts)
				reported.DriftDetails = allDrift
				if sameReportedDesiredRev && existingReported.AppliedStatus == AppliedStatusRejected {
					reported.AppliedStatus = AppliedStatusRejected
					reported.ErrorMessage = existingReported.ErrorMessage
				} else if sameReportedDesiredRev && existingReported.AppliedStatus == AppliedStatusPartial && !existingHadConcIssue {
					reported.AppliedStatus = AppliedStatusPartial
					reported.ErrorMessage = existingReported.ErrorMessage
				} else if len(allDrift) > 0 {
					reported.AppliedStatus = AppliedStatusPartial
					if len(sameRevEdgeFailDrifts) > 0 {
						reported.ErrorMessage = existingReported.ErrorMessage
					} else {
						reported.ErrorMessage = nil
					}
				} else {
					reported.AppliedStatus = AppliedStatusApplied
					reported.ErrorMessage = nil
				}
			} else {
				// Preserve existing ReportedRevision (< desiredRevision), edge AppliedStatus,
				// ErrorMessage, and non-concentrator drift until the gateway reports this revision.
				reported.DriftDetails = priorNonConcDrifts
			}
		} else {
			if edgeCaughtUp && !gatewayUnreachable {
				reported.ReportedRevision = desiredRevision
			}
			sameReportedRev := existingReported != nil && existingReported.ReportedRevision == reported.ReportedRevision
			if !sameReportedRev || existingReported.AppliedStatus == AppliedStatusApplied {
				reported.AppliedStatus = AppliedStatusPartial
			} else {
				reported.AppliedStatus = existingReported.AppliedStatus
			}
			var existingErr *string
			if sameReportedRev {
				existingErr = existingReported.ErrorMessage
			}
			reported.ErrorMessage = mergeShadowErrorMessages(existingErr, errorMessage)
			if reported.ReportedRevision == desiredRevision {
				computedDrift := computeConfigShadowDrift(desired, &reported)
				reported.DriftDetails = mergeDriftDetails(mergeDriftDetails(computedDrift, failedDrifts), sameRevEdgeFailDrifts)
			} else {
				reported.DriftDetails = mergeDriftDetails(priorNonConcDrifts, failedDrifts)
			}
		}
	} else {
		// Desired advanced concurrently while reconciling an older revision: persist
		// hardware-verified ConcentratorConfigs without corrupting newer status/drifts.
		if existingReported != nil {
			reported.ReportedRevision = existingReported.ReportedRevision
			reported.AppliedStatus = existingReported.AppliedStatus
			reported.ErrorMessage = existingReported.ErrorMessage
			reported.DriftDetails = slices.Clone(existingReported.DriftDetails)
		} else {
			reported.ReportedRevision = 0
			reported.DriftDetails = []DriftDetail{}
		}
	}

	rEncoded, err := marshalShadowSections(reported.PointMappings, reported.SamplingPolicies, reported.ConcentratorConfigs, reported.DriverConfigs, reported.AutonomyPolicy, reported.EdgeRuleBundle, reported.EdgeModels, reported.EdgeModelStatuses)
	if err != nil {
		return err
	}
	rDriftJSON, err := marshalDriftDetailsChecked(reported.DriftDetails)
	if err != nil {
		return err
	}
	if err := upsertReportedConfigInTx(ctx, tx, tenantID, gatewayID, reported, rEncoded, rDriftJSON); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
