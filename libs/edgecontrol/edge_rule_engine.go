package edgecontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmexpr"
)

type EdgeChannelResolver func(deviceID, pointCode string) string
type EdgeCommandChannelResolver func(deviceID, pointCode, capability string) (address string, dataType DataType, paramKey string, ok bool)

type EdgeRuleControllerConfig struct {
	ID                     string
	ChannelResolver        EdgeChannelResolver
	CommandChannelResolver EdgeCommandChannelResolver
}

type edgeEvalStatus uint8

const (
	edgeEvalNotMatched edgeEvalStatus = iota
	edgeEvalMatched
	edgeEvalIndeterminate
)

type edgeTimedSample struct {
	input      string
	observedAt time.Time
	value      float64
}

type edgeRuleRuntimeState struct {
	ruleDigest    string
	active        bool
	candidateAt   *time.Time
	repeatCount   uint64
	lastInputSig  string
	sampleWindow  []edgeTimedSample
	transitionSeq uint64
}

type EdgeRuleController struct {
	mu                     sync.Mutex
	id                     string
	channelResolver        EdgeChannelResolver
	commandChannelResolver EdgeCommandChannelResolver

	bundle                  *EdgeRuleBundle
	orderedRules            []CompiledEdgeRule
	states                  map[string]*edgeRuleRuntimeState
	lastTransitionSeqByRule map[string]uint64
	lastNow                 time.Time
	offline                 bool
	events                  []EdgeRuleEvent
}

func NewEdgeRuleController(cfg EdgeRuleControllerConfig) *EdgeRuleController {
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = DefaultEdgeRuleControllerID
	}
	return &EdgeRuleController{
		id:                      id,
		channelResolver:         cfg.ChannelResolver,
		commandChannelResolver:  cfg.CommandChannelResolver,
		states:                  make(map[string]*edgeRuleRuntimeState),
		lastTransitionSeqByRule: make(map[string]uint64),
	}
}

func (c *EdgeRuleController) ID() string {
	return c.id
}

func (c *EdgeRuleController) SetOffline(offline bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.offline = offline
	c.mu.Unlock()
}

func (c *EdgeRuleController) Status() EdgeRuleEngineStatus {
	if c == nil {
		return EdgeRuleEngineStatus{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	activeCount := 0
	for _, r := range c.orderedRules {
		if st := c.states[r.RuleID]; st != nil && st.active {
			activeCount++
		}
	}
	var rev uint64
	var digest string
	if c.bundle != nil {
		rev = c.bundle.BundleRevision
		digest = c.bundle.EdgeBundleDigest
	}
	return EdgeRuleEngineStatus{
		BundleRevision:   rev,
		EdgeBundleDigest: digest,
		RuleCount:        len(c.orderedRules),
		ActiveRuleCount:  activeCount,
		Offline:          c.offline,
	}
}

// PeekEvents returns a copy of pending edge rule events without clearing the queue.
func (c *EdgeRuleController) PeekEvents() []EdgeRuleEvent {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	out := make([]EdgeRuleEvent, len(c.events))
	copy(out, c.events)
	return out
}

// AckEvents removes the oldest count acknowledged events from the buffer.
func (c *EdgeRuleController) AckEvents(count int) {
	if c == nil || count <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if count >= len(c.events) {
		c.events = nil
		return
	}
	remaining := make([]EdgeRuleEvent, len(c.events)-count)
	copy(remaining, c.events[count:])
	c.events = remaining
}

func (c *EdgeRuleController) DrainEvents() []EdgeRuleEvent {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	out := make([]EdgeRuleEvent, len(c.events))
	copy(out, c.events)
	c.events = nil
	return out
}

func (c *EdgeRuleController) emitBundleSwapClearedLocked(rule CompiledEdgeRule, st *edgeRuleRuntimeState, bundleRev uint64, edgeDigest, reason string, occurredAt time.Time) {
	if st == nil || !st.active {
		return
	}
	seq := max(st.transitionSeq, c.lastTransitionSeqByRule[rule.RuleID]) + 1
	c.lastTransitionSeqByRule[rule.RuleID] = seq
	st.transitionSeq = seq
	st.active = false
	c.appendEventLocked(EdgeRuleEvent{
		EventID:          fmt.Sprintf("edge-rule:%s:%d", rule.RuleID, seq),
		EventType:        EdgeRuleEventCleared,
		RuleID:           rule.RuleID,
		AlarmType:        rule.AlarmType,
		Title:            rule.Title,
		Severity:         rule.Severity,
		OccurredAt:       occurredAt.Format(time.RFC3339Nano),
		BundleRevision:   bundleRev,
		EdgeBundleDigest: edgeDigest,
		Offline:          c.offline,
		Reason:           reason,
	})
}

func (c *EdgeRuleController) UpdateBundle(bundle *EdgeRuleBundle) error {
	if c == nil {
		return errors.New("edge rule controller is nil")
	}
	if bundle == nil {
		c.mu.Lock()
		occurredAt := c.lastNow
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		var prevRev uint64
		var prevDigest string
		if c.bundle != nil {
			prevRev = c.bundle.BundleRevision
			prevDigest = c.bundle.EdgeBundleDigest
		}
		for _, prevRule := range c.orderedRules {
			if st := c.states[prevRule.RuleID]; st != nil && st.active {
				c.emitBundleSwapClearedLocked(prevRule, st, prevRev, prevDigest, "RULE_REMOVED", occurredAt)
			}
		}
		c.bundle = nil
		c.orderedRules = nil
		c.states = make(map[string]*edgeRuleRuntimeState)
		c.mu.Unlock()
		return nil
	}
	if err := bundle.Validate(); err != nil {
		return err
	}
	cloned := bundle.Clone()
	ordered, err := TopologicalOrderEdgeRules(cloned.Rules)
	if err != nil {
		return err
	}

	nextDigests := make(map[string]string, len(ordered))
	for _, r := range ordered {
		dig, digErr := compiledRuleDigest(r)
		if digErr != nil {
			return digErr
		}
		nextDigests[r.RuleID] = dig
	}

	c.mu.Lock()
	occurredAt := c.lastNow
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	prevStates := c.states
	nextStates := make(map[string]*edgeRuleRuntimeState, len(ordered))
	for _, prevRule := range c.orderedRules {
		st := prevStates[prevRule.RuleID]
		if st == nil || !st.active {
			continue
		}
		newDig, stillPresent := nextDigests[prevRule.RuleID]
		if !stillPresent {
			c.emitBundleSwapClearedLocked(prevRule, st, bundle.BundleRevision, bundle.EdgeBundleDigest, "RULE_REMOVED", occurredAt)
		} else if st.ruleDigest != newDig {
			c.emitBundleSwapClearedLocked(prevRule, st, bundle.BundleRevision, bundle.EdgeBundleDigest, "RULE_UPDATED", occurredAt)
		}
	}
	for _, r := range ordered {
		dig := nextDigests[r.RuleID]
		if prev, ok := prevStates[r.RuleID]; ok && prev.ruleDigest == dig {
			nextStates[r.RuleID] = prev
			continue
		}
		nextStates[r.RuleID] = &edgeRuleRuntimeState{
			ruleDigest:    dig,
			transitionSeq: c.lastTransitionSeqByRule[r.RuleID],
		}
	}
	c.bundle = cloned
	c.orderedRules = ordered
	c.states = nextStates
	c.mu.Unlock()
	return nil
}

func compiledRuleDigest(r CompiledEdgeRule) (string, error) {
	cloned := r.Clone()
	sort.Strings(cloned.DependsOnRuleIDs)
	raw, err := json.Marshal(cloned)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (c *EdgeRuleController) Run(_ context.Context, image ProcessImage, plan *ControlPlan) error {
	if plan == nil {
		return errors.New("edge rule control plan is nil")
	}
	if c == nil {
		return nil
	}
	now := image.At()
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastNow = now

	if len(c.orderedRules) == 0 {
		return nil
	}
	var bundleRev uint64
	var edgeDigest string
	if c.bundle != nil {
		bundleRev = c.bundle.BundleRevision
		edgeDigest = c.bundle.EdgeBundleDigest
	}

	activeNow := make(map[string]bool, len(c.orderedRules))
	for _, rule := range c.orderedRules {
		st := c.states[rule.RuleID]
		if st == nil {
			dig, _ := compiledRuleDigest(rule)
			st = &edgeRuleRuntimeState{ruleDigest: dig}
			c.states[rule.RuleID] = st
		}

		inputSig := c.ruleInputSignature(rule, image)
		newInput := inputSig != st.lastInputSig
		if newInput {
			st.lastInputSig = inputSig
			st.sampleWindow = c.updateRuleSampleWindow(rule, image, st.sampleWindow, now)
		}

		if st.active {
			clearRes := c.evalCondition(rule, rule.Clear, image, st.sampleWindow, true, now)
			if clearRes == edgeEvalMatched {
				st.active = false
				st.candidateAt = nil
				st.repeatCount = 0
				st.transitionSeq = max(st.transitionSeq, c.lastTransitionSeqByRule[rule.RuleID]) + 1
				c.lastTransitionSeqByRule[rule.RuleID] = st.transitionSeq
				triggeredActions, err := c.applyActions(rule, EdgeActionTriggerClear, image, plan)
				if err != nil {
					return err
				}
				c.appendEventLocked(EdgeRuleEvent{
					EventID:          fmt.Sprintf("edge-rule:%s:%d", rule.RuleID, st.transitionSeq),
					EventType:        EdgeRuleEventCleared,
					RuleID:           rule.RuleID,
					AlarmType:        rule.AlarmType,
					Title:            rule.Title,
					Severity:         rule.Severity,
					OccurredAt:       now.Format(time.RFC3339Nano),
					BundleRevision:   bundleRev,
					EdgeBundleDigest: edgeDigest,
					Offline:          c.offline,
					Reason:           "CLEAR_PREDICATE_MATCHED",
					TriggeredActions: triggeredActions,
				})
			} else {
				// Sustain protection commands while an edge safety rule remains active so
				// lower-priority fallback or remote intents cannot override active protection.
				if _, err := c.applyActions(rule, EdgeActionTriggerRaise, image, plan); err != nil {
					return err
				}
			}
			activeNow[rule.RuleID] = st.active
			continue
		}

		if !edgeDependencyGateSatisfied(rule, activeNow) {
			st.candidateAt = nil
			st.repeatCount = 0
			activeNow[rule.RuleID] = false
			continue
		}

		raiseRes := c.evalCondition(rule, rule.Raise, image, st.sampleWindow, false, now)
		if raiseRes != edgeEvalMatched {
			st.candidateAt = nil
			st.repeatCount = 0
			activeNow[rule.RuleID] = false
			continue
		}

		fired := false
		switch rule.TriggerMode {
		case EdgeTriggerSimple:
			fired = true
		case EdgeTriggerDuration:
			if st.candidateAt == nil {
				t := now
				st.candidateAt = &t
			}
			due := st.candidateAt.Add(time.Duration(rule.DurationSeconds) * time.Second)
			if !now.Before(due) {
				fired = true
			}
		case EdgeTriggerRepeating:
			if newInput {
				st.repeatCount++
			}
			if st.repeatCount >= rule.RepeatCount {
				fired = true
			}
		}

		if fired {
			st.active = true
			st.candidateAt = nil
			st.repeatCount = 0
			st.transitionSeq = max(st.transitionSeq, c.lastTransitionSeqByRule[rule.RuleID]) + 1
			c.lastTransitionSeqByRule[rule.RuleID] = st.transitionSeq
			triggeredActions, err := c.applyActions(rule, EdgeActionTriggerRaise, image, plan)
			if err != nil {
				return err
			}
			c.appendEventLocked(EdgeRuleEvent{
				EventID:          fmt.Sprintf("edge-rule:%s:%d", rule.RuleID, st.transitionSeq),
				EventType:        EdgeRuleEventRaised,
				RuleID:           rule.RuleID,
				AlarmType:        rule.AlarmType,
				Title:            rule.Title,
				Severity:         rule.Severity,
				OccurredAt:       now.Format(time.RFC3339Nano),
				BundleRevision:   bundleRev,
				EdgeBundleDigest: edgeDigest,
				Offline:          c.offline,
				Reason:           "RAISE_PREDICATE_MATCHED",
				TriggeredActions: triggeredActions,
			})
		}
		activeNow[rule.RuleID] = st.active
	}
	return nil
}

func (c *EdgeRuleController) appendEventLocked(ev EdgeRuleEvent) {
	if len(c.events) >= MaxEdgeRuleEventQueue {
		copy(c.events, c.events[1:])
		c.events[len(c.events)-1] = ev
		return
	}
	c.events = append(c.events, ev)
}

func (c *EdgeRuleController) resolveChannelAddress(inputKey string) string {
	dev, pt, ok := strings.Cut(inputKey, "/")
	if !ok {
		return inputKey
	}
	if c.channelResolver != nil {
		if resolved := strings.TrimSpace(c.channelResolver(dev, pt)); resolved != "" {
			return resolved
		}
	}
	return inputKey
}

func (c *EdgeRuleController) ruleInputSignature(rule CompiledEdgeRule, image ProcessImage) string {
	seen := make(map[string]struct{})
	collectEdgeInputs(rule.Raise, seen)
	collectEdgeInputs(rule.Clear, seen)
	if len(seen) == 0 {
		return ""
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		addr := c.resolveChannelAddress(k)
		snap, ok := image.Get(addr)
		b.WriteString(k)
		b.WriteByte('=')
		if !ok || !snap.HasValue {
			b.WriteString("missing;")
			continue
		}
		b.WriteString(string(snap.Sample.Quality))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(snap.Sample.ObservedAt.UnixNano(), 10))
		b.WriteByte(':')
		b.WriteString(formatEdgeValueSig(snap.Sample.Value))
		b.WriteByte(';')
	}
	return b.String()
}

func formatEdgeValueSig(v Value) string {
	switch v.Type {
	case DataTypeBoolean:
		if v.Boolean {
			return "B1"
		}
		return "B0"
	case DataTypeString:
		return "S:" + v.String
	default:
		if num, ok := v.NumericFloat64(); ok {
			return "N:" + strconv.FormatFloat(num, 'g', 12, 64)
		}
		return "U"
	}
}

func collectEdgeInputs(cond EdgeCondition, inputs map[string]struct{}) {
	if len(cond.Children) > 0 {
		for _, ch := range cond.Children {
			collectEdgeInputs(ch, inputs)
		}
		return
	}
	if cond.Kind == EdgeConditionExpression {
		for _, k := range cond.Inputs {
			if k != "" {
				inputs[k] = struct{}{}
			}
		}
		return
	}
	if cond.Input != "" {
		inputs[cond.Input] = struct{}{}
	}
}

func edgeRuleSampleWindows(rule CompiledEdgeRule) map[string]int64 {
	windows := make(map[string]int64)
	collectEdgeConditionSampleWindows(rule.Raise, windows)
	collectEdgeConditionSampleWindows(rule.Clear, windows)
	return windows
}

func collectEdgeConditionSampleWindows(cond EdgeCondition, windows map[string]int64) {
	if (cond.Kind == EdgeConditionSlope || cond.Kind == EdgeConditionWindowAgg) && cond.Input != "" {
		if cond.WindowSeconds > windows[cond.Input] {
			windows[cond.Input] = cond.WindowSeconds
		}
		return
	}
	for _, ch := range cond.Children {
		collectEdgeConditionSampleWindows(ch, windows)
	}
}

func (c *EdgeRuleController) updateRuleSampleWindow(rule CompiledEdgeRule, image ProcessImage, prev []edgeTimedSample, now time.Time) []edgeTimedSample {
	windows := edgeRuleSampleWindows(rule)
	if len(windows) == 0 {
		return nil
	}
	kept := make([]edgeTimedSample, 0, len(prev)+len(windows))
	seenAtByInput := make(map[string]map[int64]struct{}, len(windows))
	markSeen := func(input string, at time.Time) bool {
		m := seenAtByInput[input]
		if m == nil {
			m = make(map[int64]struct{})
			seenAtByInput[input] = m
		}
		ns := at.UnixNano()
		if _, dup := m[ns]; dup {
			return false
		}
		m[ns] = struct{}{}
		return true
	}
	for _, s := range prev {
		winSec, ok := windows[s.input]
		if !ok || !isFiniteFloat(s.value) {
			continue
		}
		at := s.observedAt.UTC()
		if at.Before(now.Add(-time.Duration(winSec) * time.Second)) {
			continue
		}
		if markSeen(s.input, at) {
			kept = append(kept, edgeTimedSample{input: s.input, observedAt: at, value: s.value})
		}
	}

	keys := make([]string, 0, len(windows))
	for k := range windows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		addr := c.resolveChannelAddress(k)
		snap, exists := image.Get(addr)
		if !trustedEdgeSample(rule, snap, exists, now) {
			continue
		}
		num, ok := snap.Sample.Value.NumericFloat64()
		if !ok || !isFiniteFloat(num) {
			continue
		}
		at := snap.Sample.ObservedAt.UTC()
		if at.IsZero() {
			at = now
		}
		if at.Before(now.Add(-time.Duration(windows[k]) * time.Second)) {
			continue
		}
		if markSeen(k, at) {
			kept = append(kept, edgeTimedSample{input: k, observedAt: at, value: num})
		}
	}
	if len(kept) == 0 {
		return nil
	}
	sort.Slice(kept, func(i, j int) bool {
		if !kept[i].observedAt.Equal(kept[j].observedAt) {
			return kept[i].observedAt.Before(kept[j].observedAt)
		}
		return kept[i].input < kept[j].input
	})

	// Enforce per-input limit first so one fast channel cannot evict another channel's samples.
	perInputCount := make(map[string]int, len(windows))
	filtered := make([]edgeTimedSample, 0, len(kept))
	for i := len(kept) - 1; i >= 0; i-- {
		s := kept[i]
		if perInputCount[s.input] >= MaxEdgeRuleSamplesPerInput {
			continue
		}
		perInputCount[s.input]++
		filtered = append(filtered, s)
	}
	slices.Reverse(filtered)
	if len(filtered) > MaxEdgeRuleSamplesPerRule {
		filtered = filtered[len(filtered)-MaxEdgeRuleSamplesPerRule:]
	}
	return filtered
}

func trustedEdgeSample(rule CompiledEdgeRule, snap ChannelSnapshot, exists bool, now time.Time) bool {
	if !exists || !snap.HasValue {
		return false
	}
	switch snap.Sample.Quality {
	case QualityGood:
	case QualitySuspect:
		if rule.QualityPolicy != EdgeQualityUsable {
			return false
		}
	default:
		return false
	}
	if !snap.Sample.ObservedAt.IsZero() && rule.FreshnessSeconds > 0 {
		freshUntil := snap.Sample.ObservedAt.UTC().Add(time.Duration(rule.FreshnessSeconds) * time.Second)
		if !now.Before(freshUntil) {
			return false
		}
	}
	return true
}

func edgeDependencyGateSatisfied(rule CompiledEdgeRule, activeNow map[string]bool) bool {
	switch rule.DependencyGate {
	case "", EdgeDependencyGateNone:
		return true
	case EdgeDependencyGateAllActive:
		if len(rule.DependsOnRuleIDs) == 0 {
			return false
		}
		for _, depID := range rule.DependsOnRuleIDs {
			if !activeNow[depID] {
				return false
			}
		}
		return true
	case EdgeDependencyGateAnyActive:
		for _, depID := range rule.DependsOnRuleIDs {
			if activeNow[depID] {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (c *EdgeRuleController) evalCondition(
	rule CompiledEdgeRule,
	cond EdgeCondition,
	image ProcessImage,
	sampleWindow []edgeTimedSample,
	active bool,
	now time.Time,
) edgeEvalStatus {
	switch cond.Kind {
	case EdgeConditionAnd:
		res := edgeEvalMatched
		for _, ch := range cond.Children {
			chRes := c.evalCondition(rule, ch, image, sampleWindow, active, now)
			if chRes == edgeEvalNotMatched {
				return edgeEvalNotMatched
			}
			if chRes == edgeEvalIndeterminate {
				res = edgeEvalIndeterminate
			}
		}
		return res
	case EdgeConditionOr:
		res := edgeEvalNotMatched
		for _, ch := range cond.Children {
			chRes := c.evalCondition(rule, ch, image, sampleWindow, active, now)
			if chRes == edgeEvalMatched {
				return edgeEvalMatched
			}
			if chRes == edgeEvalIndeterminate {
				res = edgeEvalIndeterminate
			}
		}
		return res
	case EdgeConditionExpression:
		return c.evalEdgeExpression(rule, cond, image, now)
	case EdgeConditionSlope:
		return c.evalEdgeSlope(rule, cond, image, sampleWindow, now)
	case EdgeConditionWindowAgg:
		return c.evalEdgeWindowAgg(rule, cond, image, sampleWindow, now)
	}

	addr := c.resolveChannelAddress(cond.Input)
	snap, exists := image.Get(addr)
	if !trustedEdgeSample(rule, snap, exists, now) {
		return edgeEvalIndeterminate
	}
	switch cond.Kind {
	case EdgeConditionCompare:
		if cond.Value == nil {
			return edgeEvalIndeterminate
		}
		return compareEdgeValue(snap.Sample.Value, cond.Operator, *cond.Value)
	case EdgeConditionRange:
		num, ok := snap.Sample.Value.NumericFloat64()
		if !ok || !isFiniteFloat(num) || cond.Minimum == nil || cond.Maximum == nil {
			return edgeEvalIndeterminate
		}
		if num >= *cond.Minimum && num <= *cond.Maximum {
			return edgeEvalMatched
		}
		return edgeEvalNotMatched
	case EdgeConditionHysteresis:
		num, ok := snap.Sample.Value.NumericFloat64()
		if !ok || !isFiniteFloat(num) || cond.Trigger == nil || cond.Reset == nil {
			return edgeEvalIndeterminate
		}
		if cond.Direction == EdgeDirectionAbove {
			if active {
				if num > *cond.Reset {
					return edgeEvalMatched
				}
				return edgeEvalNotMatched
			}
			if num >= *cond.Trigger {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		if active {
			if num < *cond.Reset {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		if num <= *cond.Trigger {
			return edgeEvalMatched
		}
		return edgeEvalNotMatched
	default:
		return edgeEvalIndeterminate
	}
}

func (c *EdgeRuleController) evalEdgeSlope(
	rule CompiledEdgeRule,
	cond EdgeCondition,
	image ProcessImage,
	sampleWindow []edgeTimedSample,
	now time.Time,
) edgeEvalStatus {
	addr := c.resolveChannelAddress(cond.Input)
	snap, exists := image.Get(addr)
	if !trustedEdgeSample(rule, snap, exists, now) {
		return edgeEvalIndeterminate
	}
	curVal, ok := snap.Sample.Value.NumericFloat64()
	if !ok || !isFiniteFloat(curVal) || cond.Delta == nil {
		return edgeEvalIndeterminate
	}
	curObs := snap.Sample.ObservedAt.UTC()
	if curObs.IsZero() {
		curObs = now
	}
	windowStart := now.Add(-time.Duration(cond.WindowSeconds) * time.Second)
	var earliestTime time.Time
	var earliestVal float64
	found := false
	for _, s := range sampleWindow {
		if s.input != cond.Input || !isFiniteFloat(s.value) {
			continue
		}
		if s.observedAt.Before(windowStart) || s.observedAt.After(now) || !s.observedAt.Before(curObs) {
			continue
		}
		if !found || s.observedAt.Before(earliestTime) {
			earliestTime = s.observedAt
			earliestVal = s.value
			found = true
		}
	}
	if !found {
		return edgeEvalNotMatched
	}
	change := curVal - earliestVal
	if compareNumbers(change, cond.Operator, *cond.Delta) {
		return edgeEvalMatched
	}
	return edgeEvalNotMatched
}

func (c *EdgeRuleController) evalEdgeWindowAgg(
	rule CompiledEdgeRule,
	cond EdgeCondition,
	image ProcessImage,
	sampleWindow []edgeTimedSample,
	now time.Time,
) edgeEvalStatus {
	addr := c.resolveChannelAddress(cond.Input)
	snap, exists := image.Get(addr)
	if !trustedEdgeSample(rule, snap, exists, now) {
		return edgeEvalIndeterminate
	}
	if curVal, ok := snap.Sample.Value.NumericFloat64(); !ok || !isFiniteFloat(curVal) || cond.Threshold == nil {
		return edgeEvalIndeterminate
	}
	windowStart := WindowStartTime(now, cond.WindowType, cond.WindowSeconds)
	values := make([]float64, 0, len(sampleWindow))
	for _, s := range sampleWindow {
		if s.input != cond.Input || !isFiniteFloat(s.value) {
			continue
		}
		if s.observedAt.Before(windowStart) || s.observedAt.After(now) {
			continue
		}
		values = append(values, s.value)
	}
	minSamples := cond.MinSamples
	if minSamples < 1 {
		minSamples = 1
	}
	if cond.Agg == EdgeWindowAggStdDev && minSamples < 2 {
		minSamples = 2
	}
	if len(values) < minSamples {
		return edgeEvalNotMatched
	}
	metric, ok := ComputeWindowAggregate(cond.Agg, values)
	if !ok {
		return edgeEvalNotMatched
	}
	if compareNumbers(metric, cond.Operator, *cond.Threshold) {
		return edgeEvalMatched
	}
	return edgeEvalNotMatched
}

// WindowStartTime returns the aligned start timestamp for a SLIDING or TUMBLING window ending at asOf.
func WindowStartTime(asOf time.Time, windowType EdgeWindowType, windowSeconds int64) time.Time {
	asOf = asOf.UTC()
	if windowType == EdgeWindowTumbling && windowSeconds > 0 {
		bucketStart := (asOf.Unix() / windowSeconds) * windowSeconds
		return time.Unix(bucketStart, 0).UTC()
	}
	return asOf.Add(-time.Duration(windowSeconds) * time.Second)
}

func compareEdgeValue(actual Value, op EdgeCompareOperator, expected EdgeTypedValue) edgeEvalStatus {
	switch expected.Type {
	case EdgeValueNumber:
		num, ok := actual.NumericFloat64()
		if !ok || !isFiniteFloat(num) {
			return edgeEvalIndeterminate
		}
		if compareNumbers(num, op, expected.Number) {
			return edgeEvalMatched
		}
		return edgeEvalNotMatched
	case EdgeValueString:
		if actual.Type != DataTypeString {
			return edgeEvalIndeterminate
		}
		if op == EdgeCompareEQ {
			if actual.String == expected.String {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		if op == EdgeCompareNE {
			if actual.String != expected.String {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		return edgeEvalIndeterminate
	case EdgeValueBoolean:
		if actual.Type != DataTypeBoolean {
			return edgeEvalIndeterminate
		}
		if op == EdgeCompareEQ {
			if actual.Boolean == expected.Boolean {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		if op == EdgeCompareNE {
			if actual.Boolean != expected.Boolean {
				return edgeEvalMatched
			}
			return edgeEvalNotMatched
		}
		return edgeEvalIndeterminate
	default:
		return edgeEvalIndeterminate
	}
}

func compareNumbers(actual float64, op EdgeCompareOperator, expected float64) bool {
	switch op {
	case EdgeCompareEQ:
		return actual == expected
	case EdgeCompareNE:
		return actual != expected
	case EdgeCompareGT:
		return actual > expected
	case EdgeCompareGTE:
		return actual >= expected
	case EdgeCompareLT:
		return actual < expected
	case EdgeCompareLTE:
		return actual <= expected
	default:
		return false
	}
}

func (c *EdgeRuleController) evalEdgeExpression(rule CompiledEdgeRule, cond EdgeCondition, image ProcessImage, now time.Time) edgeEvalStatus {
	compiled, err := alarmexpr.ParseAndValidate(cond.Expression, cond.Inputs)
	if err != nil {
		return edgeEvalIndeterminate
	}
	res := compiled.Evaluate(alarmexpr.EvalModeTwoValued, cond.Inputs, func(_, inputKey string) alarmexpr.InputResolution {
		addr := c.resolveChannelAddress(inputKey)
		snap, exists := image.Get(addr)
		if !trustedEdgeSample(rule, snap, exists, now) {
			return alarmexpr.InputResolution{
				Trusted: false,
				Blocker: "UNTRUSTED_INPUT:" + inputKey,
			}
		}
		switch snap.Sample.Value.Type {
		case DataTypeBoolean:
			return alarmexpr.InputResolution{Trusted: true, Value: alarmexpr.BooleanValue(snap.Sample.Value.Boolean)}
		case DataTypeString:
			return alarmexpr.InputResolution{Trusted: true, Value: alarmexpr.StringValue(snap.Sample.Value.String)}
		default:
			num, ok := snap.Sample.Value.NumericFloat64()
			if !ok || !isFiniteFloat(num) {
				return alarmexpr.InputResolution{
					Trusted: false,
					Blocker: alarmexpr.BlockerInvalidPrefix + inputKey,
				}
			}
			return alarmexpr.InputResolution{Trusted: true, Value: alarmexpr.NumberValue(num)}
		}
	})
	if res.Indeterminate {
		return edgeEvalIndeterminate
	}
	if res.Matched {
		return edgeEvalMatched
	}
	return edgeEvalNotMatched
}

func (c *EdgeRuleController) applyActions(
	rule CompiledEdgeRule,
	on EdgeActionTriggerOn,
	image ProcessImage,
	plan *ControlPlan,
) ([]string, error) {
	if len(rule.Actions) == 0 {
		return nil, nil
	}
	var triggered []string
	for _, action := range rule.Actions {
		effOn := action.EffectiveTriggerOn()
		if effOn != on && effOn != EdgeActionTriggerBoth {
			continue
		}
		if action.Kind != EdgeRuleActionProtectionCommand {
			continue
		}
		if _, protected := ClassifyHardRealtimeProtection(action.TargetPointCode, action.Capability, nil); protected {
			continue
		}
		addr, dataType, val, resolved := c.resolveProtectionCommandValue(action, image)
		if !resolved {
			continue
		}
		if snap, exists := image.Get(addr); exists && IsHardRealtimeChannel(snap.Descriptor) {
			continue
		}
		decision, newlyAccepted, err := plan.Request(addr, c.id, val)
		if err != nil {
			// Skip channel mismatch / read-only targets gracefully so one misbound action
			// does not abort remaining rules in the cycle, matching non-critical fault isolation.
			_ = dataType
			continue
		}
		if newlyAccepted && decision.Accepted {
			triggered = append(triggered, fmt.Sprintf("%s:%s@%s", action.Capability, action.TargetDeviceID, addr))
		}
	}
	return triggered, nil
}

func (c *EdgeRuleController) resolveProtectionCommandValue(action EdgeRuleAction, image ProcessImage) (string, DataType, Value, bool) {
	if c.commandChannelResolver != nil {
		addr, dataType, paramKey, ok := c.commandChannelResolver(action.TargetDeviceID, action.TargetPointCode, action.Capability)
		if !ok || addr == "" {
			return "", "", Value{}, false
		}
		if _, exists := image.Get(addr); !exists {
			return "", "", Value{}, false
		}
		val, vOK := encodeEdgeActionValue(action, dataType, paramKey)
		if !vOK {
			return "", "", Value{}, false
		}
		return addr, dataType, val, true
	}
	pointCode := strings.TrimSpace(action.TargetPointCode)
	if pointCode == "" {
		return "", "", Value{}, false
	}
	addr := c.resolveChannelAddress(action.TargetDeviceID + "/" + pointCode)
	snap, exists := image.Get(addr)
	if !exists {
		return "", "", Value{}, false
	}
	val, vOK := encodeEdgeActionValue(action, snap.Descriptor.DataType, "")
	if !vOK {
		return "", "", Value{}, false
	}
	return addr, snap.Descriptor.DataType, val, true
}

func encodeEdgeActionValue(action EdgeRuleAction, dataType DataType, paramKey string) (Value, bool) {
	if len(action.Parameters) == 0 {
		if dataType == DataTypeBoolean {
			return BooleanValue(true), true
		}
		return Value{}, false
	}
	var numericVal float64
	if paramKey != "" {
		v, ok := action.Parameters[paramKey]
		if !ok || !isFiniteFloat(v) {
			return Value{}, false
		}
		numericVal = v
	} else {
		if len(action.Parameters) != 1 {
			return Value{}, false
		}
		for _, v := range action.Parameters {
			if !isFiniteFloat(v) {
				return Value{}, false
			}
			numericVal = v
		}
	}
	switch dataType {
	case DataTypeDouble:
		return DoubleValue(numericVal), true
	case DataTypeFloat:
		return FloatValue(float32(numericVal)), true
	case DataTypeInteger:
		return IntegerValue(int32(math.Round(numericVal))), true
	case DataTypeLong:
		return LongValue(int64(math.Round(numericVal))), true
	default:
		return Value{}, false
	}
}
