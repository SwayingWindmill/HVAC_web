package optimization

import (
	"slices"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type autoControlLiveState struct {
	SupplyTemp         *TelemetryLivePoint
	Setpoint           *TelemetryLivePoint
	ZoneTemps          map[string]TelemetryLivePoint
	TargetDiagnostics  []TelemetryLivePoint
	DeviceFaultActive  bool
	CabinetLocalManual bool
	DeviceOffline      bool
	RunningChillers    int
	OutdoorWetBulbC    *float64
	CoolingLoadKW      *float64
	ChillerPowerKW     *float64
}

func autoControlHealthScope(siteCtx AutoControlSiteContext, targetDeviceID string) ([]string, []string) {
	deviceIDs := make([]string, 0, len(siteCtx.DeviceIDs)+1)
	seenDev := make(map[string]struct{}, len(siteCtx.DeviceIDs)+1)
	addDev := func(raw string) {
		clean := strings.TrimSpace(raw)
		if !uuidPattern.MatchString(clean) {
			return
		}
		if _, exists := seenDev[clean]; exists {
			return
		}
		seenDev[clean] = struct{}{}
		deviceIDs = append(deviceIDs, clean)
	}
	for _, devID := range siteCtx.DeviceIDs {
		addDev(devID)
	}
	addDev(targetDeviceID)
	if siteCtx.SetpointTarget != nil {
		addDev(siteCtx.SetpointTarget.DeviceID)
	}
	for _, aux := range siteCtx.AuxiliaryTargets {
		addDev(aux.Target.DeviceID)
	}

	keys := make([]string, 0, 24)
	seenKey := make(map[string]struct{}, 24)
	addKey := func(raw string) {
		clean := strings.TrimSpace(raw)
		if clean == "" {
			return
		}
		if _, exists := seenKey[clean]; exists {
			return
		}
		seenKey[clean] = struct{}{}
		keys = append(keys, clean)
	}
	for _, k := range siteCtx.InputMapping.telemetryKeys() {
		addKey(k)
	}
	if siteCtx.SetpointTarget != nil {
		addKey(siteCtx.SetpointTarget.FeedbackPointKey)
	}
	for _, aux := range siteCtx.AuxiliaryTargets {
		addKey(aux.Target.FeedbackPointKey)
	}
	for _, canonical := range []string{
		"chilled_water_supply_temp",
		"supply_temp_setpoint",
		"chilled_water_supply_temp_sp",
		"frequency_hz",
		"frequency_feedback",
		"vfd_frequency",
		"frequency_setpoint",
		"fan_speed_pct",
		"fan_speed_feedback",
		"fan_speed_setpoint",
		"zone_temp",
		"fault_code",
		"alarm_active",
		"local_remote_mode",
		"chiller_run_state",
		"chiller.fault_code",
		"chiller.alarm_active",
		"chiller.local_remote_mode",
		"chiller.run_state",
	} {
		addKey(canonical)
	}
	if siteCtx.Topology.Twin != nil {
		for _, pb := range siteCtx.Topology.Twin.PointBindings {
			wireKey := firstNonEmptyString(pb.WireSourceKey, pb.PointCode)
			if wireKey == "" {
				continue
			}
			lower := strings.ToLower(wireKey)
			if strings.Contains(lower, "fault_code") ||
				strings.Contains(lower, "alarm_active") ||
				strings.Contains(lower, "local_remote") ||
				strings.Contains(lower, "run_state") ||
				strings.Contains(lower, "setpoint") {
				addKey(wireKey)
			}
		}
	}
	return deviceIDs, keys
}

func extractAutoControlLiveState(points []TelemetryLivePoint, siteCtx AutoControlSiteContext, targetDeviceID string, now time.Time) autoControlLiveState {
	state := autoControlLiveState{
		ZoneTemps: make(map[string]TelemetryLivePoint),
	}

	supplyKeys := []string{strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey), "chilled_water_supply_temp"}
	state.SupplyTemp = findSitePointByKeys(points, "", supplyKeys...)

	var setpointKeys []string
	if siteCtx.SetpointTarget != nil && strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey) != "" {
		setpointKeys = append(setpointKeys, strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey))
	}
	setpointKeys = append(setpointKeys, "supply_temp_setpoint", "chilled_water_supply_temp_sp", "chilled_water_temperature_setpoint")
	state.Setpoint = findDevicePointByKeys(points, targetDeviceID, setpointKeys...)

	zoneKeys := siteCtx.InputMapping.effectiveZoneKeys()
	for _, zk := range zoneKeys {
		var pt *TelemetryLivePoint
		if len(zoneKeys) == 1 {
			pt = findSitePointByKeys(points, "", zk, "zone_temp")
		} else {
			pt = findSitePointByKeys(points, "", zk)
		}
		if pt != nil {
			state.ZoneTemps[zk] = *pt
		}
	}

	if faultPt := findSiteOrDeviceLivePoint(points, targetDeviceID, siteCtx, "fault_code", livePointIndicatesFault); faultPt != nil {
		state.DeviceFaultActive = true
	}
	if alarmPt := findSiteOrDeviceLivePoint(points, targetDeviceID, siteCtx, "alarm_active", livePointIndicatesFault); alarmPt != nil {
		state.DeviceFaultActive = true
	}
	if modePt := findDevicePointByKeys(points, targetDeviceID, "local_remote_mode", "chiller.local_remote_mode", "local_mode"); modePt != nil && livePointIndicatesLocalManual(*modePt) {
		state.CabinetLocalManual = true
		state.TargetDiagnostics = append(state.TargetDiagnostics, *modePt)
	}
	if targetDeviceID != "" {
		for _, pt := range points {
			if pt.DeviceID != targetDeviceID {
				continue
			}
			if commandmodel.IsFaultOrAlarmPointKey(pt.TelemetryKey) || commandmodel.IsLocalManualPointKey(pt.TelemetryKey) {
				state.TargetDiagnostics = append(state.TargetDiagnostics, pt)
			}
		}
	}

	if isPointDeviceOffline(state.SupplyTemp) || isPointDeviceOffline(state.Setpoint) {
		state.DeviceOffline = true
	}
	for _, zp := range state.ZoneTemps {
		if isPointDeviceOffline(&zp) {
			state.DeviceOffline = true
			break
		}
	}
	if !state.DeviceOffline && targetDeviceID != "" {
		for _, pt := range points {
			if pt.DeviceID == targetDeviceID && isPointDeviceOffline(&pt) {
				state.DeviceOffline = true
				break
			}
		}
	}

	runningSeen := make(map[string]struct{})
	for _, pt := range points {
		if !isChillerRunStatePoint(pt, siteCtx, targetDeviceID) {
			continue
		}
		if pt.TextValue != nil && strings.EqualFold(strings.TrimSpace(*pt.TextValue), "FAULT") {
			state.DeviceFaultActive = true
		}
		if isLivePointPresenceOffline(pt.DevicePresence) && pt.DeviceID == targetDeviceID {
			state.DeviceOffline = true
		}
		if !isLivePointHealthyForState(pt, now, DefaultAutoControlMaxPointAge) {
			continue
		}
		if livePointIndicatesRunning(pt) {
			id := strings.TrimSpace(pt.DeviceID)
			if id == "" {
				id = strings.ToLower(strings.TrimSpace(pt.TelemetryKey))
			}
			runningSeen[id] = struct{}{}
		}
	}
	state.RunningChillers = len(runningSeen)
	state.OutdoorWetBulbC = extractOptionalLiveNumeric(points, now, DefaultPreparationMaxTelemetryAge,
		"outdoor_wet_bulb", "weather.ambient_wet_bulb_temperature", "ambient_wet_bulb_temperature", "cooling_tower.ambient_wet_bulb_temperature")
	state.CoolingLoadKW = extractOptionalLiveNumeric(points, now, DefaultAutoControlMaxPointAge,
		"instant_cooling_capacity", "btu_meter.instant_cooling_capacity", "plant_cooling_load", "cooling_load")
	state.ChillerPowerKW = extractChillerPowerSumKW(points, siteCtx, targetDeviceID, now)
	return state
}

func extractOptionalLiveNumeric(points []TelemetryLivePoint, now time.Time, maxAge time.Duration, candidateKeys ...string) *float64 {
	pt := findSitePointByKeys(points, "", candidateKeys...)
	if pt == nil || !isLivePointOptionalMetricHealthy(*pt, now, maxAge) {
		return nil
	}
	v := *pt.NumericValue
	return &v
}

func extractChillerPowerSumKW(points []TelemetryLivePoint, siteCtx AutoControlSiteContext, targetDeviceID string, now time.Time) *float64 {
	total := 0.0
	hasPower := false
	seen := make(map[string]struct{})
	for _, pt := range points {
		if !isChillerPowerPoint(pt, siteCtx, targetDeviceID) {
			continue
		}
		if !isLivePointOptionalMetricHealthy(pt, now, DefaultAutoControlMaxPointAge) {
			continue
		}
		if *pt.NumericValue < 0 {
			continue
		}
		devID := strings.ToLower(strings.TrimSpace(pt.DeviceID))
		if devID == "" {
			devID = strings.ToLower(strings.TrimSpace(pt.TelemetryKey))
		}
		if _, exists := seen[devID]; exists {
			continue
		}
		seen[devID] = struct{}{}
		total += *pt.NumericValue
		hasPower = true
	}
	if !hasPower {
		return nil
	}
	return &total
}

func isChillerPowerPoint(pt TelemetryLivePoint, siteCtx AutoControlSiteContext, targetDeviceID string) bool {
	keyLower := strings.ToLower(strings.TrimSpace(pt.TelemetryKey))
	switch {
	case keyLower == "power_total" || strings.HasSuffix(keyLower, ".power_total") || keyLower == "chiller_power_kw":
	case keyLower == "active_power" || strings.HasSuffix(keyLower, ".active_power"):
		if len(siteCtx.Topology.Chillers) > 0 && !isSiteChillerDevice(siteCtx, pt.DeviceID) {
			return false
		}
	default:
		return false
	}
	if len(siteCtx.Topology.Chillers) > 0 {
		return isSiteChillerDevice(siteCtx, pt.DeviceID)
	}
	return targetDeviceID == "" || pt.DeviceID == "" || pt.DeviceID == targetDeviceID || strings.HasPrefix(keyLower, "chiller")
}

func isLivePointOptionalMetricHealthy(pt TelemetryLivePoint, now time.Time, maxAge time.Duration) bool {
	if pt.NumericValue == nil || !finite(*pt.NumericValue) {
		return false
	}
	if pt.Quality != "" && !strings.EqualFold(strings.TrimSpace(pt.Quality), "GOOD") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(pt.Freshness), "STALE") || strings.EqualFold(strings.TrimSpace(pt.Freshness), "MISSING") {
		return false
	}
	if isLivePointPresenceOffline(pt.DevicePresence) {
		return false
	}
	if !pt.SampledAt.IsZero() {
		if pt.SampledAt.UTC().After(now.UTC().Add(5*time.Second)) || now.UTC().Sub(pt.SampledAt.UTC()) > maxAge {
			return false
		}
	}
	return true
}

func buildGuardrailPointSamples(siteCtx AutoControlSiteContext, points []TelemetryLivePoint, liveState autoControlLiveState) []commandmodel.GuardrailPointSample {
	samples := make([]commandmodel.GuardrailPointSample, 0, len(liveState.TargetDiagnostics)+len(siteCtx.InputMapping.effectiveZoneKeys())+4)
	seen := make(map[string]struct{}, 16)
	addSample := func(pt TelemetryLivePoint) {
		id := strings.TrimSpace(pt.DeviceID) + "\x00" + strings.TrimSpace(pt.TelemetryKey)
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		present := !strings.EqualFold(strings.TrimSpace(pt.Freshness), "MISSING") &&
			(!pt.SampledAt.IsZero() || pt.NumericValue != nil || pt.BoolValue != nil || pt.TextValue != nil)
		samples = append(samples, commandmodel.GuardrailPointSample{
			Key:         pt.TelemetryKey,
			Value:       pt.NumericValue,
			BoolValue:   pt.BoolValue,
			StringValue: derefStringPtr(pt.TextValue),
			Quality:     pt.Quality,
			SampledAt:   pt.SampledAt.UTC(),
			Present:     present,
		})
	}

	if liveState.SupplyTemp != nil {
		addSample(*liveState.SupplyTemp)
	} else if supplyKey := strings.TrimSpace(siteCtx.InputMapping.SupplyTemperatureKey); supplyKey != "" {
		samples = append(samples, commandmodel.GuardrailPointSample{Key: supplyKey, Present: false})
	}

	if liveState.Setpoint != nil {
		addSample(*liveState.Setpoint)
	} else if siteCtx.SetpointTarget != nil && strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey) != "" {
		samples = append(samples, commandmodel.GuardrailPointSample{Key: strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey), Present: false})
	}

	for _, zk := range siteCtx.InputMapping.effectiveZoneKeys() {
		if zp, ok := liveState.ZoneTemps[zk]; ok {
			addSample(zp)
		} else {
			samples = append(samples, commandmodel.GuardrailPointSample{Key: zk, Present: false})
		}
	}

	for _, pt := range liveState.TargetDiagnostics {
		addSample(pt)
	}
	if siteCtx.SetpointTarget != nil {
		for _, pt := range points {
			if pt.DeviceID == siteCtx.SetpointTarget.DeviceID && strings.EqualFold(strings.TrimSpace(pt.TelemetryKey), strings.TrimSpace(siteCtx.SetpointTarget.FeedbackPointKey)) {
				addSample(pt)
			}
		}
	}
	return samples
}

// findDevicePointByKeys matches strictly against deviceID when provided, preventing cross-device
// borrowing of setpoints or local/remote diagnostics across different chillers or pumps.
func findDevicePointByKeys(points []TelemetryLivePoint, deviceID string, candidateKeys ...string) *TelemetryLivePoint {
	for _, key := range candidateKeys {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" {
			continue
		}
		for idx := range points {
			if deviceID != "" && points[idx].DeviceID != deviceID {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(points[idx].TelemetryKey), cleanKey) {
				pt := points[idx]
				return &pt
			}
		}
		for idx := range points {
			if deviceID != "" && points[idx].DeviceID != deviceID {
				continue
			}
			if livePointHasKeySuffix(points[idx].TelemetryKey, cleanKey) {
				pt := points[idx]
				return &pt
			}
		}
	}
	return nil
}

func findSitePointByKeys(points []TelemetryLivePoint, preferredDeviceID string, candidateKeys ...string) *TelemetryLivePoint {
	if preferredDeviceID != "" {
		if pt := findDevicePointByKeys(points, preferredDeviceID, candidateKeys...); pt != nil {
			return pt
		}
	}
	return findDevicePointByKeys(points, "", candidateKeys...)
}

func findSiteOrDeviceLivePoint(points []TelemetryLivePoint, targetDeviceID string, siteCtx AutoControlSiteContext, keySuffix string, pred func(TelemetryLivePoint) bool) *TelemetryLivePoint {
	for idx := range points {
		pt := points[idx]
		if !strings.EqualFold(strings.TrimSpace(pt.TelemetryKey), keySuffix) && !livePointHasKeySuffix(pt.TelemetryKey, keySuffix) {
			continue
		}
		if targetDeviceID != "" && pt.DeviceID != "" && pt.DeviceID != targetDeviceID && !isSiteChillerDevice(siteCtx, pt.DeviceID) {
			continue
		}
		if pred(pt) {
			copyPt := pt
			return &copyPt
		}
	}
	return nil
}

func livePointHasKeySuffix(telemetryKey, suffix string) bool {
	ptLower := strings.ToLower(strings.TrimSpace(telemetryKey))
	sfxLower := strings.ToLower(strings.TrimSpace(suffix))
	return sfxLower != "" && strings.HasSuffix(ptLower, "."+sfxLower)
}

func isSiteChillerDevice(siteCtx AutoControlSiteContext, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	for _, chiller := range siteCtx.Topology.Chillers {
		if chiller.MeterDeviceID == deviceID || slices.Contains(chiller.BoundDeviceIDs, deviceID) {
			return true
		}
	}
	return false
}

func isChillerRunStatePoint(pt TelemetryLivePoint, siteCtx AutoControlSiteContext, targetDeviceID string) bool {
	keyLower := strings.ToLower(strings.TrimSpace(pt.TelemetryKey))
	if keyLower == "chiller_run_state" || strings.HasSuffix(keyLower, ".chiller_run_state") || keyLower == "chiller.run_state" {
		return true
	}
	if keyLower == "run_state" || strings.HasSuffix(keyLower, ".run_state") {
		if strings.HasPrefix(keyLower, "chiller") {
			return true
		}
		if isSiteChillerDevice(siteCtx, pt.DeviceID) {
			return true
		}
		if len(siteCtx.Topology.Chillers) == 0 && (pt.DeviceID == "" || pt.DeviceID == targetDeviceID) {
			return true
		}
	}
	return false
}

func livePointIndicatesFault(pt TelemetryLivePoint) bool {
	return commandmodel.DiagnosticValueIndicatesActiveFault(pt.BoolValue, pt.NumericValue, derefStringPtr(pt.TextValue))
}

func livePointIndicatesLocalManual(pt TelemetryLivePoint) bool {
	return commandmodel.DiagnosticPointIndicatesLocalManual(pt.TelemetryKey, pt.BoolValue, pt.NumericValue, derefStringPtr(pt.TextValue))
}

func livePointIndicatesRunning(pt TelemetryLivePoint) bool {
	if pt.BoolValue != nil {
		return *pt.BoolValue
	}
	if pt.NumericValue != nil && finite(*pt.NumericValue) {
		return *pt.NumericValue > 0
	}
	if pt.TextValue != nil {
		return strings.EqualFold(strings.TrimSpace(*pt.TextValue), RunStateRunning)
	}
	return false
}

func isPointDeviceOffline(pt *TelemetryLivePoint) bool {
	if pt == nil {
		return false
	}
	return isLivePointPresenceOffline(pt.DevicePresence)
}

func isLivePointPresenceOffline(presence string) bool {
	upper := strings.ToUpper(strings.TrimSpace(presence))
	return upper == "OFFLINE" || upper == "UNKNOWN"
}

func isLivePointDispatchable(pt TelemetryLivePoint, now time.Time, maxAge time.Duration) bool {
	if pt.NumericValue == nil || !finite(*pt.NumericValue) {
		return false
	}
	if pt.PointRevision <= 0 || pt.BusinessRevision <= 0 {
		return false
	}
	return isLivePointHealthyForTamper(pt, now, maxAge)
}

func isLivePointHealthyForTamper(pt TelemetryLivePoint, now time.Time, maxAge time.Duration) bool {
	if pt.NumericValue == nil || !finite(*pt.NumericValue) {
		return false
	}
	return isLivePointHealthyForState(pt, now, maxAge)
}

func isLivePointHealthyForState(pt TelemetryLivePoint, now time.Time, maxAge time.Duration) bool {
	if !strings.EqualFold(strings.TrimSpace(pt.Quality), "GOOD") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(pt.Freshness), "FRESH") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(pt.DevicePresence), "ONLINE") {
		return false
	}
	if pt.SampledAt.IsZero() || pt.SampledAt.UTC().After(now.UTC().Add(5*time.Second)) {
		return false
	}
	return now.UTC().Sub(pt.SampledAt.UTC()) <= maxAge
}

func livePointCommandCurrentState(pt TelemetryLivePoint, healthy bool, now time.Time) (availability, presence, readiness, quality string) {
	quality = strings.ToUpper(strings.TrimSpace(pt.Quality))
	if quality == "" {
		quality = "UNKNOWN"
	}
	presence = strings.ToUpper(strings.TrimSpace(pt.DevicePresence))
	if presence == "" {
		presence = "UNKNOWN"
	}
	fresh := strings.EqualFold(strings.TrimSpace(pt.Freshness), "FRESH") &&
		!pt.SampledAt.IsZero() &&
		!pt.SampledAt.UTC().After(now.UTC().Add(5*time.Second)) &&
		now.UTC().Sub(pt.SampledAt.UTC()) <= DefaultAutoControlMaxPointAge
	switch {
	case fresh && quality == "GOOD" && presence == "ONLINE":
		readiness = "CURRENT"
	case pt.SampledAt.IsZero() || strings.EqualFold(strings.TrimSpace(pt.Freshness), "MISSING"):
		readiness = "INCOMPLETE"
	default:
		readiness = "DEGRADED"
	}
	if healthy && presence == "ONLINE" && readiness == "CURRENT" && quality == "GOOD" && pt.BusinessRevision > 0 && pt.PointRevision > 0 {
		availability = "AVAILABLE"
	} else {
		availability = "UNAVAILABLE"
	}
	return availability, presence, readiness, quality
}

func skipReasonOrDefault(reason, fallback string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	return fallback
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func hasAnyAssetLock(locks map[string]AssetControlLockInfo) bool {
	for _, lock := range locks {
		if lock.LockMode != "" && lock.LockMode != AssetControlLockUnlocked {
			return true
		}
	}
	return false
}

type auxiliaryTargetLiveState struct {
	Feedback           *TelemetryLivePoint
	Diagnostics        []TelemetryLivePoint
	DeviceFaultActive  bool
	CabinetLocalManual bool
	DeviceOffline      bool
}

func auxiliaryFeedbackKeys(capability commandmodel.Capability, configuredKey string) []string {
	keys := make([]string, 0, 5)
	if clean := strings.TrimSpace(configuredKey); clean != "" {
		keys = append(keys, clean)
	}
	switch capability {
	case commandmodel.CapabilitySetFrequency:
		keys = append(keys, "frequency_hz", "frequency_feedback", "vfd_frequency", "frequency_setpoint")
	case commandmodel.CapabilitySetFanSpeed:
		keys = append(keys, "fan_speed_pct", "fan_speed_feedback", "fan_speed_setpoint")
	}
	return keys
}

func extractAuxiliaryTargetLiveState(points []TelemetryLivePoint, aux AuxiliaryLoopTarget) auxiliaryTargetLiveState {
	state := auxiliaryTargetLiveState{}
	devID := strings.TrimSpace(aux.Target.DeviceID)
	feedbackKeys := auxiliaryFeedbackKeys(aux.Capability, aux.Target.FeedbackPointKey)
	if devID != "" {
		state.Feedback = findDevicePointByKeys(points, devID, feedbackKeys...)
	}
	if state.Feedback != nil && isPointDeviceOffline(state.Feedback) {
		state.DeviceOffline = true
	}
	for _, pt := range points {
		if devID == "" || pt.DeviceID != devID {
			continue
		}
		if isPointDeviceOffline(&pt) {
			state.DeviceOffline = true
		}
		if pt.TextValue != nil && strings.EqualFold(strings.TrimSpace(*pt.TextValue), "FAULT") {
			state.DeviceFaultActive = true
		}
		if commandmodel.IsFaultOrAlarmPointKey(pt.TelemetryKey) {
			if livePointIndicatesFault(pt) {
				state.DeviceFaultActive = true
			}
			state.Diagnostics = append(state.Diagnostics, pt)
		}
		if commandmodel.IsLocalManualPointKey(pt.TelemetryKey) {
			if livePointIndicatesLocalManual(pt) {
				state.CabinetLocalManual = true
			}
			state.Diagnostics = append(state.Diagnostics, pt)
		}
	}
	return state
}

func buildAuxiliaryGuardrailPointSamples(
	siteCtx AutoControlSiteContext,
	liveState autoControlLiveState,
	aux AuxiliaryLoopTarget,
	auxLive auxiliaryTargetLiveState,
) []commandmodel.GuardrailPointSample {
	samples := make([]commandmodel.GuardrailPointSample, 0, len(auxLive.Diagnostics)+len(siteCtx.InputMapping.effectiveZoneKeys())+4)
	seen := make(map[string]struct{}, 16)
	addSample := func(pt TelemetryLivePoint) {
		id := strings.TrimSpace(pt.DeviceID) + "\x00" + strings.TrimSpace(pt.TelemetryKey)
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		present := !strings.EqualFold(strings.TrimSpace(pt.Freshness), "MISSING") &&
			(!pt.SampledAt.IsZero() || pt.NumericValue != nil || pt.BoolValue != nil || pt.TextValue != nil)
		samples = append(samples, commandmodel.GuardrailPointSample{
			Key:         pt.TelemetryKey,
			Value:       pt.NumericValue,
			BoolValue:   pt.BoolValue,
			StringValue: derefStringPtr(pt.TextValue),
			Quality:     pt.Quality,
			SampledAt:   pt.SampledAt.UTC(),
			Present:     present,
		})
	}
	if liveState.SupplyTemp != nil {
		addSample(*liveState.SupplyTemp)
	}
	for _, zk := range siteCtx.InputMapping.effectiveZoneKeys() {
		if zp, ok := liveState.ZoneTemps[zk]; ok {
			addSample(zp)
		}
	}
	if auxLive.Feedback != nil {
		addSample(*auxLive.Feedback)
	} else {
		fallbackKey := firstNonEmptyString(strings.TrimSpace(aux.Target.FeedbackPointKey), fallbackVerificationKeyForCapability(aux.Capability, siteCtx.InputMapping))
		samples = append(samples, commandmodel.GuardrailPointSample{Key: fallbackKey, Present: false})
	}
	for _, diagPt := range auxLive.Diagnostics {
		addSample(diagPt)
	}
	return samples
}
