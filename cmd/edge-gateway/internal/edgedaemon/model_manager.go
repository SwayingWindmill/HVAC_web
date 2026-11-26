package edgedaemon

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// EdgeModelManager owns verified hot-swapping, parallel shadow evaluation, and
// automatic safety rollback for EDGE_CONTROL and EDGE_FDD hyperparameter bundles.
type EdgeModelManager struct {
	mu               sync.Mutex
	logger           *slog.Logger
	trustedKeys      map[string]string
	requireSignature bool

	active         map[string]edgecontrol.EdgeModelBundle
	previousActive map[string]edgecontrol.EdgeModelBundle
	shadow         map[string]edgecontrol.EdgeModelBundle
	statuses       map[string]*edgecontrol.EdgeModelRuntimeStatus
	shadowSumAbs   map[string]float64
	lastImage      edgecontrol.ProcessImage
	hasLastImage   bool
}

func NewEdgeModelManager(trustedKeys map[string]string, logger *slog.Logger) *EdgeModelManager {
	if logger == nil {
		logger = slog.Default()
	}
	copied := make(map[string]string, len(trustedKeys))
	for k, v := range trustedKeys {
		trimmedK := strings.TrimSpace(k)
		trimmedV := strings.TrimSpace(v)
		if trimmedK != "" && trimmedV != "" {
			copied[trimmedK] = trimmedV
		}
	}
	return &EdgeModelManager{
		logger:           logger,
		trustedKeys:      copied,
		requireSignature: len(copied) > 0,
		active:           make(map[string]edgecontrol.EdgeModelBundle),
		previousActive:   make(map[string]edgecontrol.EdgeModelBundle),
		shadow:           make(map[string]edgecontrol.EdgeModelBundle),
		statuses:         make(map[string]*edgecontrol.EdgeModelRuntimeStatus),
		shadowSumAbs:     make(map[string]float64),
	}
}

func edgeModelRuntimeKey(domain edgecontrol.EdgeModelDomain, modelKey string) string {
	return string(domain) + "|" + strings.TrimSpace(modelKey)
}

// ApplyBundles atomically verifies all desired bundles (Ed25519 signature when keys
// are configured, SHA-256 checksum, physical bounds, and EDGE-003 boundary) before
// hot-swapping ACTIVE/SHADOW state. On any validation failure, running bundles are untouched.
func (m *EdgeModelManager) ApplyBundles(bundles []edgecontrol.EdgeModelBundle) ([]edgecontrol.EdgeModelBundle, []ShadowDriftDetail, error) {
	if m == nil {
		return nil, nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var drifts []ShadowDriftDetail
	validated := make([]edgecontrol.EdgeModelBundle, 0, len(bundles))
	seenSlot := make(map[string]struct{}, len(bundles))
	for _, raw := range bundles {
		b := raw.Clone()
		if err := edgecontrol.ValidateEdgeModelBundle(b, m.trustedKeys, m.requireSignature); err != nil {
			target := fmt.Sprintf("%s:%s:%s", b.Domain, strings.TrimSpace(b.ModelKey), b.RolloutMode)
			drifts = append(drifts, ShadowDriftDetail{
				Section: shadowDriftSectionEdgeModels,
				Target:  clampRunes(target, 256),
				Reason:  shadowDriftReasonApplyFailed,
				Detail:  clampRunes(err.Error(), 512),
			})
			continue
		}
		slot := edgeModelRuntimeKey(b.Domain, b.ModelKey) + "|" + string(b.RolloutMode)
		if _, dup := seenSlot[slot]; dup {
			drifts = append(drifts, ShadowDriftDetail{
				Section: shadowDriftSectionEdgeModels,
				Target:  clampRunes(slot, 256),
				Reason:  shadowDriftReasonApplyFailed,
				Detail:  "duplicate rolloutMode bundle for same modelKey",
			})
			continue
		}
		seenSlot[slot] = struct{}{}
		validated = append(validated, b)
	}
	if len(drifts) > 0 {
		return nil, drifts, fmt.Errorf("rejected %d invalid edge model bundle(s)", len(drifts))
	}

	nextActive := make(map[string]edgecontrol.EdgeModelBundle)
	nextShadow := make(map[string]edgecontrol.EdgeModelBundle)
	for _, b := range validated {
		key := edgeModelRuntimeKey(b.Domain, b.ModelKey)
		switch b.RolloutMode {
		case edgecontrol.EdgeModelRolloutActive:
			nextActive[key] = b
		case edgecontrol.EdgeModelRolloutShadow:
			nextShadow[key] = b
		}
	}

	for key, prev := range m.active {
		if cur, ok := nextActive[key]; !ok || cur.BundleID != prev.BundleID || cur.ArtifactSHA256 != prev.ArtifactSHA256 {
			m.previousActive[key] = prev.Clone()
		}
	}
	for key := range nextActive {
		m.ensureStatusLocked(key, nextActive[key].Domain, nextActive[key].ModelKey)
	}
	for key, cand := range nextShadow {
		st := m.ensureStatusLocked(key, cand.Domain, cand.ModelKey)
		if st.ShadowBundleID != cand.BundleID || st.ShadowVersion != cand.Version {
			st.ShadowSampleCount = 0
			st.ShadowMeanAbsDeviation = 0
			st.ShadowMaxAbsDeviation = 0
			m.shadowSumAbs[key] = 0
		}
	}

	m.active = nextActive
	m.shadow = nextShadow

	for key, st := range m.statuses {
		if act, ok := m.active[key]; ok {
			st.ActiveBundleID = act.BundleID
			st.ActiveVersion = act.Version
		} else {
			st.ActiveBundleID = ""
			st.ActiveVersion = ""
		}
		if sh, ok := m.shadow[key]; ok {
			st.ShadowBundleID = sh.BundleID
			st.ShadowVersion = sh.Version
		} else {
			st.ShadowBundleID = ""
			st.ShadowVersion = ""
		}
	}

	m.logger.Info("edge_models_applied", "active_count", len(m.active), "shadow_count", len(m.shadow))
	return cloneEdgeModelSlice(validated), nil, nil
}

func (m *EdgeModelManager) ensureStatusLocked(key string, domain edgecontrol.EdgeModelDomain, modelKey string) *edgecontrol.EdgeModelRuntimeStatus {
	if existing, ok := m.statuses[key]; ok {
		return existing
	}
	st := &edgecontrol.EdgeModelRuntimeStatus{
		ModelKey: strings.TrimSpace(modelKey),
		Domain:   domain,
	}
	m.statuses[key] = st
	return st
}

// ActiveBundles returns a sorted copy of currently loaded ACTIVE and SHADOW bundles.
func (m *EdgeModelManager) ActiveBundles() []edgecontrol.EdgeModelBundle {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]edgecontrol.EdgeModelBundle, 0, len(m.active)+len(m.shadow))
	for _, b := range m.active {
		out = append(out, b.Clone())
	}
	for _, b := range m.shadow {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModelKey != out[j].ModelKey {
			return out[i].ModelKey < out[j].ModelKey
		}
		return out[i].RolloutMode < out[j].RolloutMode
	})
	return out
}

// Statuses returns sorted runtime deviation & rollback telemetry per modelKey.
func (m *EdgeModelManager) Statuses() []edgecontrol.EdgeModelRuntimeStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.statuses) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.statuses))
	for k := range m.statuses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]edgecontrol.EdgeModelRuntimeStatus, 0, len(keys))
	for _, k := range keys {
		out = append(out, *m.statuses[k])
	}
	return out
}

// EvaluateCycle runs non-intrusive shadow deviation checks against the cycle's ProcessImage
// and triggers automatic rollback if a candidate emits NaN/Inf, out-of-bounds setpoints,
// or exceeds MaxShadowDeviation against the active baseline.
func (m *EdgeModelManager) EvaluateCycle(now time.Time, img edgecontrol.ProcessImage) []edgecontrol.EdgeModelRuntimeStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastImage = img
	m.hasLastImage = true
	if len(m.active) == 0 && len(m.shadow) == 0 {
		return m.sortedStatusesLocked()
	}

	signals := extractCycleNumericSignals(img)
	evalTime := now.UTC()

	keys := make(map[string]struct{}, len(m.active)+len(m.shadow))
	for k := range m.active {
		keys[k] = struct{}{}
	}
	for k := range m.shadow {
		keys[k] = struct{}{}
	}

	orderedKeys := make([]string, 0, len(keys))
	for k := range keys {
		orderedKeys = append(orderedKeys, k)
	}
	sort.Strings(orderedKeys)

	for _, key := range orderedKeys {
		act, hasAct := m.active[key]
		sh, hasSh := m.shadow[key]

		refDomain := act.Domain
		refModelKey := act.ModelKey
		if !hasAct {
			refDomain = sh.Domain
			refModelKey = sh.ModelKey
		}
		st := m.ensureStatusLocked(key, refDomain, refModelKey)
		st.LastEvaluatedAt = evalTime

		var activeOut float64
		if hasAct {
			out, ok := evaluateBundleOutput(act, signals)
			if !ok {
				m.rollbackActiveLocked(key, st, fmt.Sprintf("active model %s output out of bounds or invalid", act.Version))
				continue
			}
			activeOut = out
		}

		if !hasSh {
			continue
		}
		shadowOut, ok := evaluateBundleOutput(sh, signals)
		if !ok {
			m.evictShadowLocked(key, st, fmt.Sprintf("shadow model %s output invalid or out of bounds", sh.Version))
			continue
		}
		if !hasAct {
			// No active model configured; compare against standard plant supply baseline.
			activeOut = defaultPlantBaselineSetpoint(sh)
		}
		dev := math.Abs(shadowOut - activeOut)
		st.ShadowSampleCount++
		m.shadowSumAbs[key] += dev
		st.ShadowMeanAbsDeviation = m.shadowSumAbs[key] / float64(st.ShadowSampleCount)
		if dev > st.ShadowMaxAbsDeviation {
			st.ShadowMaxAbsDeviation = dev
		}
		if sh.MaxShadowDeviation > 0 && dev > sh.MaxShadowDeviation {
			m.evictShadowLocked(key, st, fmt.Sprintf("shadow deviation %.3f exceeded limit %.3f", dev, sh.MaxShadowDeviation))
		}
	}

	return m.sortedStatusesLocked()
}

// ActiveControlSetpointC returns the bounded [4, 18]°C setpoint from the active
// EDGE_CONTROL bundle evaluated on the latest ProcessImage (never SHADOW or rolled-back bundles).
func (m *EdgeModelManager) ActiveControlSetpointC() (float64, bool) {
	if m == nil {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasLastImage {
		return 0, false
	}
	return m.activeControlSetpointCLocked(m.lastImage)
}

// ActiveControlSetpointCForImage evaluates active EDGE_CONTROL bundles against img
// and returns a bounded [4, 18]°C setpoint without consulting SHADOW or rolled-back bundles.
func (m *EdgeModelManager) ActiveControlSetpointCForImage(img edgecontrol.ProcessImage) (float64, bool) {
	if m == nil {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeControlSetpointCLocked(img)
}

func (m *EdgeModelManager) activeControlSetpointCLocked(img edgecontrol.ProcessImage) (float64, bool) {
	if len(m.active) == 0 {
		return 0, false
	}
	keys := make([]string, 0, len(m.active))
	for k, b := range m.active {
		if b.Domain == edgecontrol.EdgeModelDomainControl {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return 0, false
	}
	sort.Strings(keys)
	signals := extractCycleNumericSignals(img)
	for _, k := range keys {
		out, ok := evaluateBundleOutput(m.active[k], signals)
		if ok && !math.IsNaN(out) && !math.IsInf(out, 0) && out >= 4.0 && out <= 18.0 {
			return out, true
		}
	}
	return 0, false
}

func (m *EdgeModelManager) evictShadowLocked(key string, st *edgecontrol.EdgeModelRuntimeStatus, reason string) {
	delete(m.shadow, key)
	st.ShadowBundleID = ""
	st.ShadowVersion = ""
	st.RollbackCount++
	st.LastRollbackReason = clampRunes(reason, 256)
	m.logger.Warn("edge_model_shadow_auto_rollback", "model_key", st.ModelKey, "reason", st.LastRollbackReason, "rollback_count", st.RollbackCount)
}

func (m *EdgeModelManager) rollbackActiveLocked(key string, st *edgecontrol.EdgeModelRuntimeStatus, reason string) {
	if prev, ok := m.previousActive[key]; ok {
		m.active[key] = prev.Clone()
		delete(m.previousActive, key)
		st.ActiveBundleID = prev.BundleID
		st.ActiveVersion = prev.Version
	} else {
		delete(m.active, key)
		st.ActiveBundleID = ""
		st.ActiveVersion = ""
	}
	st.RollbackCount++
	st.LastRollbackReason = clampRunes(reason, 256)
	m.logger.Warn("edge_model_active_auto_rollback", "model_key", st.ModelKey, "reason", st.LastRollbackReason, "rollback_count", st.RollbackCount)
}

func (m *EdgeModelManager) sortedStatusesLocked() []edgecontrol.EdgeModelRuntimeStatus {
	if len(m.statuses) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.statuses))
	for k := range m.statuses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]edgecontrol.EdgeModelRuntimeStatus, 0, len(keys))
	for _, k := range keys {
		out = append(out, *m.statuses[k])
	}
	return out
}

func extractCycleNumericSignals(img edgecontrol.ProcessImage) map[string]float64 {
	snaps := img.Channels()
	signals := make(map[string]float64, len(snaps))
	for _, snap := range snaps {
		if !snap.HasValue || snap.Sample.Quality != edgecontrol.QualityGood {
			continue
		}
		if num, ok := sampleNumericValue(snap.Sample.Value); ok && !math.IsNaN(num) && !math.IsInf(num, 0) {
			signals[strings.ToLower(snap.Descriptor.PointID)] = num
			signals[strings.ToLower(snap.Descriptor.Address())] = num
		}
	}
	return signals
}

func sampleNumericValue(v edgecontrol.Value) (float64, bool) {
	switch v.Type {
	case edgecontrol.DataTypeDouble:
		return v.Double, true
	case edgecontrol.DataTypeFloat:
		return float64(v.Float), true
	case edgecontrol.DataTypeInteger:
		return float64(v.Integer), true
	case edgecontrol.DataTypeLong:
		return float64(v.Long), true
	default:
		return 0, false
	}
}

func defaultPlantBaselineSetpoint(b edgecontrol.EdgeModelBundle) float64 {
	if b.Domain == edgecontrol.EdgeModelDomainFDD {
		if b.Hyperparams.FDDDeltaTMinK != nil {
			return *b.Hyperparams.FDDDeltaTMinK
		}
		return 2.5
	}
	if b.Hyperparams.SupplyTempSetpointMinC != nil && b.Hyperparams.SupplyTempSetpointMaxC != nil {
		return (*b.Hyperparams.SupplyTempSetpointMinC + *b.Hyperparams.SupplyTempSetpointMaxC) * 0.5
	}
	return 7.5
}

func evaluateBundleOutput(b edgecontrol.EdgeModelBundle, signals map[string]float64) (float64, bool) {
	hp := b.Hyperparams
	if b.Domain == edgecontrol.EdgeModelDomainFDD {
		if hp.FDDDeltaTMinK != nil {
			v := *hp.FDDDeltaTMinK
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0.5 || v > 15 {
				return 0, false
			}
			supply, hasS := firstSignal(signals, "chw_supply_temp", "chws_temp", "supply_temp")
			ret, hasR := firstSignal(signals, "chw_return_temp", "chwr_temp", "return_temp")
			if hasS && hasR {
				return (ret - supply) - v, true
			}
			return v, true
		}
		if hp.FDDAnomalyZScore != nil {
			return *hp.FDDAnomalyZScore, true
		}
		return 0, false
	}

	// EDGE_CONTROL: evaluate predicted chilled-water supply setpoint within [4, 18] °C.
	minC := 4.0
	maxC := 18.0
	if hp.SupplyTempSetpointMinC != nil {
		minC = *hp.SupplyTempSetpointMinC
	}
	if hp.SupplyTempSetpointMaxC != nil {
		maxC = *hp.SupplyTempSetpointMaxC
	}
	if minC > maxC || minC < 4.0 || maxC > 18.0 {
		return 0, false
	}
	out := (minC + maxC) * 0.5
	if loadPct, ok := firstSignal(signals, "cooling_load_pct", "load_pct"); ok && len(hp.COPCoefficients) > 0 {
		// Monotonic polynomial adjustment bounded within [minC, maxC] before clamping.
		out = hp.COPCoefficients[0]
		if len(hp.COPCoefficients) > 1 {
			out += hp.COPCoefficients[1] * (loadPct / 100.0)
		}
		if math.IsNaN(out) || math.IsInf(out, 0) {
			return 0, false
		}
		if out < 4.0 || out > 18.0 {
			return 0, false
		}
	}
	if out < minC {
		out = minC
	} else if out > maxC {
		out = maxC
	}
	return out, true
}

func firstSignal(signals map[string]float64, candidates ...string) (float64, bool) {
	for _, cand := range candidates {
		if val, ok := signals[cand]; ok {
			return val, true
		}
	}
	for k, val := range signals {
		for _, cand := range candidates {
			if strings.Contains(k, cand) {
				return val, true
			}
		}
	}
	return 0, false
}

func cloneEdgeModelSlice(in []edgecontrol.EdgeModelBundle) []edgecontrol.EdgeModelBundle {
	if len(in) == 0 {
		return nil
	}
	out := make([]edgecontrol.EdgeModelBundle, len(in))
	for i, b := range in {
		out[i] = b.Clone()
	}
	return out
}
