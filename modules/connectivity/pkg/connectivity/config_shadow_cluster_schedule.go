package connectivity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	DriftSectionClusterPolicy  = "CLUSTER_POLICY"
	DriftSectionEnergySchedule = "ENERGY_SCHEDULE"

	maxClusterPolicyJSONBytes  = 65536
	maxEnergyScheduleJSONBytes = 262144
)

// OptionalClusterControlPolicy represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null (Set=true,
// Value=nil) to clear, or a replacement cluster control policy object (Value!=nil).
type OptionalClusterControlPolicy struct {
	Set   bool
	Value *edgecontrol.ClusterControlPolicy
}

func (o OptionalClusterControlPolicy) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalClusterControlPolicy) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalClusterControlPolicy) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalClusterControlPolicy) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalClusterControlPolicy: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var policy edgecontrol.ClusterControlPolicy
	if err := dec.Decode(&policy); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in clusterPolicy")
	}
	o.Value = &policy
	return nil
}

// OptionalEdgeEnergySchedule represents tri-state JSON presence for PUT partial
// updates: omitted (Set=false, Value=nil), explicit JSON null (Set=true,
// Value=nil) to clear, or a replacement energy schedule object (Value!=nil).
type OptionalEdgeEnergySchedule struct {
	Set   bool
	Value *edgecontrol.EdgeEnergySchedule
}

func (o OptionalEdgeEnergySchedule) IsSet() bool {
	return o.Set || o.Value != nil
}

func (o OptionalEdgeEnergySchedule) IsZero() bool {
	return !o.Set && o.Value == nil
}

func (o OptionalEdgeEnergySchedule) MarshalJSON() ([]byte, error) {
	if o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o *OptionalEdgeEnergySchedule) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("OptionalEdgeEnergySchedule: UnmarshalJSON on nil pointer")
	}
	trimmed := bytes.TrimSpace(data)
	o.Set = true
	if bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var schedule edgecontrol.EdgeEnergySchedule
	if err := dec.Decode(&schedule); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content in energySchedule")
	}
	o.Value = &schedule
	return nil
}

func normalizeZeroFloatPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := normalizeZeroFloat(*p)
	return &v
}

func equalOptionalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return normalizeZeroFloat(*a) == normalizeZeroFloat(*b)
}

func canonicalizeClusterPolicy(input *edgecontrol.ClusterControlPolicy) *edgecontrol.ClusterControlPolicy {
	if input == nil {
		return nil
	}
	cp := input.Clone()
	cp.StageUpLoadPct = normalizeZeroFloat(cp.StageUpLoadPct)
	cp.StageDownLoadPct = normalizeZeroFloat(cp.StageDownLoadPct)
	cp.DefaultSupplySetpointC = normalizeZeroFloat(cp.DefaultSupplySetpointC)
	cp.MinPumpFrequencyHz = normalizeZeroFloat(cp.MinPumpFrequencyHz)
	cp.MaxPumpFrequencyHz = normalizeZeroFloat(cp.MaxPumpFrequencyHz)
	cp.MinTowerFanSpeedPct = normalizeZeroFloat(cp.MinTowerFanSpeedPct)
	cp.MaxTowerFanSpeedPct = normalizeZeroFloat(cp.MaxTowerFanSpeedPct)
	return cp
}

func validateClusterPolicy(input *edgecontrol.ClusterControlPolicy) (*edgecontrol.ClusterControlPolicy, error) {
	if input == nil {
		return nil, nil
	}
	cp := canonicalizeClusterPolicy(input)
	if err := cp.Validate(); err != nil {
		return nil, fmt.Errorf("%w: clusterPolicy: %v", ErrConfigShadowInvalid, err)
	}
	return cp, nil
}

func normalizeScheduleTimestamp(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

func canonicalizeEnergySchedule(input *edgecontrol.EdgeEnergySchedule) *edgecontrol.EdgeEnergySchedule {
	if input == nil {
		return nil
	}
	cp := input.Clone()
	cp.ScheduleID = strings.TrimSpace(cp.ScheduleID)
	cp.IssuedAt = normalizeScheduleTimestamp(cp.IssuedAt)
	cp.ValidFrom = normalizeScheduleTimestamp(cp.ValidFrom)
	cp.ValidUntil = normalizeScheduleTimestamp(cp.ValidUntil)
	cp.LeaseExpiresAt = normalizeScheduleTimestamp(cp.LeaseExpiresAt)
	for i := range cp.Slots {
		cp.Slots[i].SlotID = strings.TrimSpace(cp.Slots[i].SlotID)
		cp.Slots[i].Mode = edgecontrol.ScheduleOperatingMode(strings.TrimSpace(string(cp.Slots[i].Mode)))
		cp.Slots[i].SupplyTempSetpointC = normalizeZeroFloatPtr(cp.Slots[i].SupplyTempSetpointC)
		cp.Slots[i].PumpFrequencyMinHz = normalizeZeroFloatPtr(cp.Slots[i].PumpFrequencyMinHz)
		cp.Slots[i].PumpFrequencyMaxHz = normalizeZeroFloatPtr(cp.Slots[i].PumpFrequencyMaxHz)
		cp.Slots[i].TowerFanSpeedMinPct = normalizeZeroFloatPtr(cp.Slots[i].TowerFanSpeedMinPct)
		cp.Slots[i].TowerFanSpeedMaxPct = normalizeZeroFloatPtr(cp.Slots[i].TowerFanSpeedMaxPct)
		cp.Slots[i].TargetCoolingLoadKW = normalizeZeroFloatPtr(cp.Slots[i].TargetCoolingLoadKW)
	}
	slices.SortFunc(cp.Slots, func(a, b edgecontrol.EnergyScheduleSlot) int {
		if a.StartMinute != b.StartMinute {
			if a.StartMinute < b.StartMinute {
				return -1
			}
			return 1
		}
		if a.EndMinute != b.EndMinute {
			if a.EndMinute < b.EndMinute {
				return -1
			}
			return 1
		}
		return strings.Compare(a.SlotID, b.SlotID)
	})
	return cp
}

func validateEnergySchedule(input *edgecontrol.EdgeEnergySchedule) (*edgecontrol.EdgeEnergySchedule, error) {
	if input == nil {
		return nil, nil
	}
	cp := canonicalizeEnergySchedule(input)
	if err := cp.Validate(); err != nil {
		return nil, fmt.Errorf("%w: energySchedule: %v", ErrConfigShadowInvalid, err)
	}
	return cp, nil
}

func clusterPoliciesEqual(a, b *edgecontrol.ClusterControlPolicy) bool {
	if a == nil || b == nil {
		return a == b
	}
	ca := canonicalizeClusterPolicy(a)
	cb := canonicalizeClusterPolicy(b)
	return *ca == *cb
}

func energyScheduleSlotsEqual(a, b edgecontrol.EnergyScheduleSlot) bool {
	return a.SlotID == b.SlotID &&
		a.StartMinute == b.StartMinute &&
		a.EndMinute == b.EndMinute &&
		a.Mode == b.Mode &&
		equalOptionalFloat(a.SupplyTempSetpointC, b.SupplyTempSetpointC) &&
		equalOptionalFloat(a.PumpFrequencyMinHz, b.PumpFrequencyMinHz) &&
		equalOptionalFloat(a.PumpFrequencyMaxHz, b.PumpFrequencyMaxHz) &&
		equalOptionalFloat(a.TowerFanSpeedMinPct, b.TowerFanSpeedMinPct) &&
		equalOptionalFloat(a.TowerFanSpeedMaxPct, b.TowerFanSpeedMaxPct) &&
		equalOptionalInt(a.MaxRunningChillers, b.MaxRunningChillers) &&
		equalOptionalFloat(a.TargetCoolingLoadKW, b.TargetCoolingLoadKW)
}

func energySchedulesEqual(a, b *edgecontrol.EdgeEnergySchedule) bool {
	if a == nil || b == nil {
		return a == b
	}
	ca := canonicalizeEnergySchedule(a)
	cb := canonicalizeEnergySchedule(b)
	if ca.ScheduleID != cb.ScheduleID ||
		ca.Version != cb.Version ||
		!ca.IssuedAt.Equal(cb.IssuedAt) ||
		!ca.ValidFrom.Equal(cb.ValidFrom) ||
		!ca.ValidUntil.Equal(cb.ValidUntil) ||
		!ca.LeaseExpiresAt.Equal(cb.LeaseExpiresAt) ||
		ca.ContinueOfflineWhileValid != cb.ContinueOfflineWhileValid ||
		len(ca.Slots) != len(cb.Slots) {
		return false
	}
	for i := range ca.Slots {
		if !energyScheduleSlotsEqual(ca.Slots[i], cb.Slots[i]) {
			return false
		}
	}
	return true
}

func energyScheduleDigest(s *edgecontrol.EdgeEnergySchedule) string {
	if s == nil {
		return ""
	}
	encoded, err := json.Marshal(canonicalizeEnergySchedule(s))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ComputeConfigShadowDigest computes a deterministic SHA-256 hex digest across all
// canonicalized desired config shadow sections (including clusterPolicy and energySchedule).
func ComputeConfigShadowDigest(desired *GatewayDesiredConfig) (string, error) {
	if desired == nil {
		return "", nil
	}
	mappings := clonePointMappingsList(desired.PointMappings)
	for i := range mappings {
		slices.SortFunc(mappings[i].CopyTargets, func(a, b PointCopyTarget) int {
			if c := strings.Compare(a.DeviceKey, b.DeviceKey); c != 0 {
				return c
			}
			return strings.Compare(a.PointCode, b.PointCode)
		})
	}
	slices.SortFunc(mappings, func(a, b GatewayPointMapping) int {
		if c := strings.Compare(a.PrimaryDeviceKey, b.PrimaryDeviceKey); c != 0 {
			return c
		}
		if c := strings.Compare(a.PrimaryPointCode, b.PrimaryPointCode); c != 0 {
			return c
		}
		return strings.Compare(a.SourceChannel, b.SourceChannel)
	})

	policies := cloneSamplingPoliciesList(desired.SamplingPolicies)
	for i := range policies {
		policies[i].PointCode = canonicalPolicyPointCode(policies[i].PointCode)
	}
	slices.SortFunc(policies, func(a, b GatewaySamplingPolicy) int {
		if c := strings.Compare(a.DeviceKey, b.DeviceKey); c != 0 {
			return c
		}
		return strings.Compare(a.PointCode, b.PointCode)
	})

	drivers := cloneDriverConfigsList(desired.DriverConfigs)
	for i := range drivers {
		slices.SortFunc(drivers[i].Registers, compareRegisterMappings)
		if drivers[i].BACnet != nil {
			slices.SortFunc(drivers[i].BACnet.Objects, func(a, b edgecontrol.BACnetObjectInput) int {
				return strings.Compare(a.PointCode, b.PointCode)
			})
		}
		if drivers[i].OPCUA != nil {
			slices.SortFunc(drivers[i].OPCUA.Nodes, func(a, b edgecontrol.OPCUANodeInput) int {
				return strings.Compare(a.PointCode, b.PointCode)
			})
		}
	}
	slices.SortFunc(drivers, func(a, b GatewayModbusDriverConfig) int {
		return strings.Compare(a.DeviceKey, b.DeviceKey)
	})

	models := cloneEdgeModelBundles(desired.EdgeModels)
	slices.SortFunc(models, func(a, b edgecontrol.EdgeModelBundle) int {
		if c := strings.Compare(string(a.Domain), string(b.Domain)); c != 0 {
			return c
		}
		if c := strings.Compare(a.ModelKey, b.ModelKey); c != 0 {
			return c
		}
		return strings.Compare(string(a.RolloutMode), string(b.RolloutMode))
	})

	canonical := struct {
		PointMappings       []GatewayPointMapping             `json:"pointMappings"`
		SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
		ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
		DriverConfigs       []GatewayModbusDriverConfig       `json:"driverConfigs"`
		AutonomyPolicy      *GatewayAutonomyPolicy            `json:"autonomyPolicy,omitempty"`
		EdgeRuleBundle      *edgecontrol.EdgeRuleBundle       `json:"edgeRuleBundle,omitempty"`
		EdgeModels          []edgecontrol.EdgeModelBundle     `json:"edgeModels,omitempty"`
		ClusterPolicy       *edgecontrol.ClusterControlPolicy `json:"clusterPolicy,omitempty"`
		EnergySchedule      *edgecontrol.EdgeEnergySchedule   `json:"energySchedule,omitempty"`
	}{
		PointMappings:       mappings,
		SamplingPolicies:    policies,
		ConcentratorConfigs: cloneConcentratorUnits(desired.ConcentratorConfigs),
		DriverConfigs:       drivers,
		AutonomyPolicy:      cloneAutonomyPolicy(desired.AutonomyPolicy),
		EdgeRuleBundle:      desired.EdgeRuleBundle.Clone(),
		EdgeModels:          models,
		ClusterPolicy:       canonicalizeClusterPolicy(desired.ClusterPolicy),
		EnergySchedule:      canonicalizeEnergySchedule(desired.EnergySchedule),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical shadow sections: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func diffClusterPolicy(desired, reported *edgecontrol.ClusterControlPolicy) []DriftDetail {
	switch {
	case desired == nil && reported == nil:
		return nil
	case desired != nil && reported == nil:
		return []DriftDetail{{
			Section: DriftSectionClusterPolicy,
			Target:  "clusterPolicy",
			Reason:  DriftReasonMissingOnEdge,
			Detail:  "desired cluster policy is missing on edge",
		}}
	case desired == nil && reported != nil:
		return []DriftDetail{{
			Section: DriftSectionClusterPolicy,
			Target:  "clusterPolicy",
			Reason:  DriftReasonUnexpectedOnEdge,
			Detail:  "unexpected cluster policy present on edge",
		}}
	case !clusterPoliciesEqual(desired, reported):
		return []DriftDetail{{
			Section: DriftSectionClusterPolicy,
			Target:  "clusterPolicy",
			Reason:  DriftReasonValueMismatch,
			Detail: clampUTF8Runes(fmt.Sprintf(
				"cluster policy mismatch (desired enabled=%t chillers=%d..%d setpoint=%.2fC, reported enabled=%t chillers=%d..%d setpoint=%.2fC)",
				desired.Enabled, desired.MinRunningChillers, desired.MaxRunningChillers, desired.DefaultSupplySetpointC,
				reported.Enabled, reported.MinRunningChillers, reported.MaxRunningChillers, reported.DefaultSupplySetpointC,
			), maxDriftDetailRunes),
		}}
	default:
		return nil
	}
}

func diffEnergySchedule(desired, reported *edgecontrol.EdgeEnergySchedule) []DriftDetail {
	switch {
	case desired == nil && reported == nil:
		return nil
	case desired != nil && reported == nil:
		return []DriftDetail{{
			Section: DriftSectionEnergySchedule,
			Target:  "energySchedule",
			Reason:  DriftReasonMissingOnEdge,
			Detail: clampUTF8Runes(fmt.Sprintf(
				"desired energy schedule (%s@v%d slots=%d) is missing on edge",
				strings.TrimSpace(desired.ScheduleID), desired.Version, len(desired.Slots),
			), maxDriftDetailRunes),
		}}
	case desired == nil && reported != nil:
		return []DriftDetail{{
			Section: DriftSectionEnergySchedule,
			Target:  "energySchedule",
			Reason:  DriftReasonUnexpectedOnEdge,
			Detail: clampUTF8Runes(fmt.Sprintf(
				"unexpected energy schedule (%s@v%d slots=%d) present on edge",
				strings.TrimSpace(reported.ScheduleID), reported.Version, len(reported.Slots),
			), maxDriftDetailRunes),
		}}
	case !energySchedulesEqual(desired, reported):
		return []DriftDetail{{
			Section: DriftSectionEnergySchedule,
			Target:  "energySchedule",
			Reason:  DriftReasonValueMismatch,
			Detail: clampUTF8Runes(fmt.Sprintf(
				"energy schedule mismatch (desired %s@v%d slots=%d digest=%s, reported %s@v%d slots=%d digest=%s)",
				strings.TrimSpace(desired.ScheduleID), desired.Version, len(desired.Slots), shortEdgeBundleDigest(energyScheduleDigest(desired)),
				strings.TrimSpace(reported.ScheduleID), reported.Version, len(reported.Slots), shortEdgeBundleDigest(energyScheduleDigest(reported)),
			), maxDriftDetailRunes),
		}}
	default:
		return nil
	}
}
