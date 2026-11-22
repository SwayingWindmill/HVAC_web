package edgecontrol

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// AggregationWindow1Min and AggregationWindow5Min are the supported edge pre-aggregation tumbling windows.
	AggregationWindow1Min = 60
	AggregationWindow5Min = 300
)

// AggregationPointPolicy defines an edge time-window pre-aggregation rule for eligible points.
type AggregationPointPolicy struct {
	DeviceKey     string `json:"deviceKey"`
	PointCode     string `json:"pointCode"`
	WindowSeconds int    `json:"windowSeconds"`
	EmitMinMax    bool   `json:"emitMinMax,omitempty"`
}

// EdgeAggregationPolicy configures optional 1-minute / 5-minute edge tumbling pre-aggregation.
type EdgeAggregationPolicy struct {
	Enabled      bool                     `json:"enabled"`
	Points       []AggregationPointPolicy `json:"points,omitempty"`
	RawAllowlist []string                 `json:"rawAllowlist,omitempty"`
}

// DefaultEdgeAggregationPolicy returns a safe fail-closed default policy with aggregation disabled.
func DefaultEdgeAggregationPolicy() EdgeAggregationPolicy {
	return EdgeAggregationPolicy{Enabled: false}
}

// Validate checks every aggregation point rule and raw allowlist entry.
func (p EdgeAggregationPolicy) Validate() error {
	seen := make(map[string]struct{}, len(p.Points))
	for i, rule := range p.Points {
		dev := strings.TrimSpace(rule.DeviceKey)
		pt := strings.TrimSpace(rule.PointCode)
		if dev == "" || (dev != "*" && strings.Contains(dev, "/")) {
			return fmt.Errorf("aggregation points[%d] invalid deviceKey %q", i, rule.DeviceKey)
		}
		if pt == "" || (pt != "*" && strings.Contains(pt, "/")) {
			return fmt.Errorf("aggregation points[%d] invalid pointCode %q", i, rule.PointCode)
		}
		if rule.WindowSeconds != AggregationWindow1Min && rule.WindowSeconds != AggregationWindow5Min {
			return fmt.Errorf("aggregation points[%d] windowSeconds must be 60 or 300, got %d", i, rule.WindowSeconds)
		}
		key := dev + "\x00" + pt
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate aggregation rule for %s/%s", dev, pt)
		}
		seen[key] = struct{}{}
	}
	for i, raw := range p.RawAllowlist {
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("aggregation rawAllowlist[%d] must not be blank", i)
		}
	}
	return nil
}

// Clone returns a deep copy of EdgeAggregationPolicy (nil-safe).
func (p *EdgeAggregationPolicy) Clone() *EdgeAggregationPolicy {
	if p == nil {
		return nil
	}
	cp := EdgeAggregationPolicy{
		Enabled:      p.Enabled,
		Points:       make([]AggregationPointPolicy, len(p.Points)),
		RawAllowlist: make([]string, len(p.RawAllowlist)),
	}
	for i, rule := range p.Points {
		cp.Points[i] = AggregationPointPolicy{
			DeviceKey:     strings.TrimSpace(rule.DeviceKey),
			PointCode:     strings.TrimSpace(rule.PointCode),
			WindowSeconds: rule.WindowSeconds,
			EmitMinMax:    rule.EmitMinMax,
		}
	}
	for i, raw := range p.RawAllowlist {
		cp.RawAllowlist[i] = strings.TrimSpace(raw)
	}
	return &cp
}

// AggregationSample is one telemetry observation submitted to WindowAggregator.
type AggregationSample struct {
	DeviceKey  string
	PointCode  string
	Address    string
	Descriptor ChannelDescriptor
	Semantic   SemanticChannel
	Value      Value
	Quality    Quality
	ObservedAt time.Time
}

// AggregatedWindowOutput contains the summary statistics produced when a tumbling window closes.
type AggregatedWindowOutput struct {
	DeviceKey     string
	PointCode     string
	Address       string
	Unit          string
	Priority      DataPriority
	WindowSeconds int
	WindowStart   time.Time
	WindowEnd     time.Time
	Count         int
	Mean          float64
	Min           float64
	Max           float64
	EmitMinMax    bool
}

type aggregationBucket struct {
	address       string
	deviceKey     string
	pointCode     string
	unit          string
	priority      DataPriority
	windowSeconds int
	emitMinMax    bool
	windowID      int64
	windowStart   time.Time
	windowEnd     time.Time
	count         int
	sum           float64
	minVal        float64
	maxVal        float64
}

func (b *aggregationBucket) output() AggregatedWindowOutput {
	mean := b.sum / float64(b.count)
	return AggregatedWindowOutput{
		DeviceKey:     b.deviceKey,
		PointCode:     b.pointCode,
		Address:       b.address,
		Unit:          b.unit,
		Priority:      b.priority,
		WindowSeconds: b.windowSeconds,
		WindowStart:   b.windowStart,
		WindowEnd:     b.windowEnd,
		Count:         b.count,
		Mean:          mean,
		Min:           b.minVal,
		Max:           b.maxVal,
		EmitMinMax:    b.emitMinMax,
	}
}

// WindowAggregator folds eligible read-only auxiliary analog channels into
// clock-aligned 1m/5m mean/min/max windows while fail-closing critical control
// feedback, alarm triggers, quality degradation, and energy metering to raw transmission.
type WindowAggregator struct {
	mu                 sync.Mutex
	policy             EdgeAggregationPolicy
	allowlist          map[string]struct{}
	protectedAddresses map[string]struct{}
	semantics          map[string]SemanticChannel
	buckets            map[string]*aggregationBucket
}

// NewWindowAggregator creates a WindowAggregator validated against policy.
func NewWindowAggregator(policy EdgeAggregationPolicy) (*WindowAggregator, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	cloned := policy.Clone()
	return &WindowAggregator{
		policy:             *cloned,
		allowlist:          buildRawSet(cloned.RawAllowlist),
		protectedAddresses: make(map[string]struct{}),
		semantics:          make(map[string]SemanticChannel),
		buckets:            make(map[string]*aggregationBucket),
	}, nil
}

// UpdatePolicy hot-reloads the aggregation policy and drops incompatible active buckets.
func (a *WindowAggregator) UpdatePolicy(policy EdgeAggregationPolicy) error {
	if a == nil {
		return errors.New("window aggregator is nil")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	cloned := policy.Clone()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.policy = *cloned
	a.allowlist = buildRawSet(cloned.RawAllowlist)
	for addr, b := range a.buckets {
		rule, ok := matchAggregationRule(a.policy, b.deviceKey, b.pointCode, addr, a.allowlist, a.protectedAddresses)
		if !a.policy.Enabled || !ok || rule.WindowSeconds != b.windowSeconds {
			delete(a.buckets, addr)
			continue
		}
		b.emitMinMax = rule.EmitMinMax
	}
	return nil
}

// SetProtectedAddresses updates the runtime set of raw-preserved addresses (such as
// active EdgeRuleBundle condition inputs) so alarm triggers never lose sample resolution.
func (a *WindowAggregator) SetProtectedAddresses(addrs []string) {
	if a == nil {
		return
	}
	next := buildRawSet(addrs)
	a.mu.Lock()
	a.protectedAddresses = next
	for addr := range a.buckets {
		if _, protected := next[addr]; protected {
			delete(a.buckets, addr)
		}
	}
	a.mu.Unlock()
}

// SetChannelSemantics registers SemanticChannel tags by channel address so samples without an
// inline Semantic field still obey energy/safety fail-closed raw preservation rules.
func (a *WindowAggregator) SetChannelSemantics(semantics map[string]SemanticChannel) {
	if a == nil {
		return
	}
	next := make(map[string]SemanticChannel, len(semantics))
	for rawAddr, sem := range semantics {
		addr := strings.TrimSpace(rawAddr)
		if addr != "" && sem != "" {
			next[addr] = sem
		}
	}
	a.mu.Lock()
	a.semantics = next
	for addr := range a.buckets {
		if sem, ok := next[addr]; ok && isProtectedAggregationSemantic(sem) {
			delete(a.buckets, addr)
		}
	}
	a.mu.Unlock()
}

// MustPreserveRaw reports whether sample must bypass window aggregation and transmit raw.
func (a *WindowAggregator) MustPreserveRaw(sample AggregationSample) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	preserve, _ := a.evaluatePolicyLocked(sample)
	return preserve
}

// Observe evaluates sample: if raw preservation applies it returns (true, nil);
// otherwise it folds sample into the matching tumbling window and returns any closed window.
func (a *WindowAggregator) Observe(sample AggregationSample) (passRaw bool, flushed []AggregatedWindowOutput) {
	if a == nil {
		return true, nil
	}
	observedAt := sample.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	preserve, rule := a.evaluatePolicyLocked(sample)
	if preserve || rule == nil {
		return true, nil
	}
	numVal, ok := sample.Value.NumericFloat64()
	if !ok || !isFiniteFloat(numVal) {
		return true, nil
	}

	addr := canonicalAggregationAddress(sample)
	winSec := int64(rule.WindowSeconds)
	winID := observedAt.Unix() / winSec
	winStart := time.Unix(winID*winSec, 0).UTC()
	winEnd := winStart.Add(time.Duration(winSec) * time.Second)

	priority := effectiveSamplePriority(sample.Descriptor)
	existing, exists := a.buckets[addr]
	if exists {
		if winID < existing.windowID {
			return false, nil
		}
		if winID > existing.windowID {
			flushed = append(flushed, existing.output())
			exists = false
		} else {
			existing.count++
			existing.sum += numVal
			if numVal < existing.minVal {
				existing.minVal = numVal
			}
			if numVal > existing.maxVal {
				existing.maxVal = numVal
			}
			if priorityRank(priority) < priorityRank(existing.priority) {
				existing.priority = priority
			}
			return false, nil
		}
	}

	if !exists {
		devKey, ptCode := resolveAggregationDevicePoint(sample)
		a.buckets[addr] = &aggregationBucket{
			address:       addr,
			deviceKey:     devKey,
			pointCode:     ptCode,
			unit:          sample.Descriptor.Unit,
			priority:      priority,
			windowSeconds: rule.WindowSeconds,
			emitMinMax:    rule.EmitMinMax,
			windowID:      winID,
			windowStart:   winStart,
			windowEnd:     winEnd,
			count:         1,
			sum:           numVal,
			minVal:        numVal,
			maxVal:        numVal,
		}
	}
	return false, flushed
}

// FlushDue flushes and removes every window whose WindowEnd <= now.UTC(), sorted deterministically.
func (a *WindowAggregator) FlushDue(now time.Time) []AggregatedWindowOutput {
	if a == nil {
		return nil
	}
	cutoff := now.UTC()
	if cutoff.IsZero() {
		cutoff = time.Now().UTC()
	}

	a.mu.Lock()
	var due []AggregatedWindowOutput
	for addr, b := range a.buckets {
		if !b.windowEnd.After(cutoff) {
			due = append(due, b.output())
			delete(a.buckets, addr)
		}
	}
	a.mu.Unlock()

	sort.Slice(due, func(i, j int) bool {
		if !due[i].WindowEnd.Equal(due[j].WindowEnd) {
			return due[i].WindowEnd.Before(due[j].WindowEnd)
		}
		if due[i].DeviceKey != due[j].DeviceKey {
			return due[i].DeviceKey < due[j].DeviceKey
		}
		return due[i].PointCode < due[j].PointCode
	})
	return due
}

func (a *WindowAggregator) evaluatePolicyLocked(sample AggregationSample) (bool, *AggregationPointPolicy) {
	if !a.policy.Enabled {
		return true, nil
	}
	if sample.Descriptor.Access != "" && sample.Descriptor.Access != AccessReadOnly {
		return true, nil
	}
	if sample.Descriptor.PollPriority == PriorityVeryHigh ||
		sample.Descriptor.ResendPriority == PriorityVeryHigh ||
		IsHardRealtimeChannel(sample.Descriptor) {
		return true, nil
	}
	if sample.Descriptor.DataType == DataTypeBoolean || sample.Descriptor.DataType == DataTypeString {
		return true, nil
	}
	if sample.Quality != QualityGood {
		return true, nil
	}
	numVal, ok := sample.Value.NumericFloat64()
	if !ok || !isFiniteFloat(numVal) {
		return true, nil
	}
	addr := canonicalAggregationAddress(sample)
	sem := sample.Semantic
	if sem == "" {
		sem = a.semantics[addr]
	}
	if isProtectedAggregationSemantic(sem) {
		return true, nil
	}

	devKey, ptCode := resolveAggregationDevicePoint(sample)
	if isProtectedAggregationPointCode(ptCode) {
		return true, nil
	}
	rule, matched := matchAggregationRule(a.policy, devKey, ptCode, addr, a.allowlist, a.protectedAddresses)
	if !matched {
		return true, nil
	}
	return false, &rule
}

func matchAggregationRule(
	policy EdgeAggregationPolicy,
	deviceKey, pointCode, addr string,
	allowlist, protectedAddresses map[string]struct{},
) (AggregationPointPolicy, bool) {
	for _, set := range []map[string]struct{}{allowlist, protectedAddresses} {
		if matchesRawSet(set, deviceKey, pointCode, addr) {
			return AggregationPointPolicy{}, false
		}
	}

	bestSpecificity := -1
	var best AggregationPointPolicy
	for _, rule := range policy.Points {
		dev := strings.TrimSpace(rule.DeviceKey)
		pt := strings.TrimSpace(rule.PointCode)
		if (dev != "*" && dev != deviceKey) || (pt != "*" && pt != pointCode) {
			continue
		}
		spec := 0
		if dev != "*" {
			spec += 2
		}
		if pt != "*" {
			spec++
		}
		if spec > bestSpecificity {
			bestSpecificity = spec
			best = rule
		}
	}
	if bestSpecificity < 0 {
		return AggregationPointPolicy{}, false
	}
	return best, true
}

func matchesRawSet(set map[string]struct{}, deviceKey, pointCode, addr string) bool {
	if len(set) == 0 {
		return false
	}
	if _, ok := set[addr]; ok {
		return true
	}
	if _, ok := set[deviceKey+"/"+pointCode]; ok {
		return true
	}
	_, ok := set[pointCode]
	return ok
}

func buildRawSet(entries []string) map[string]struct{} {
	out := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			out[trimmed] = struct{}{}
		}
	}
	return out
}

func canonicalAggregationAddress(sample AggregationSample) string {
	if addr := strings.TrimSpace(sample.Address); addr != "" {
		return addr
	}
	return sample.Descriptor.Address()
}

func resolveAggregationDevicePoint(sample AggregationSample) (string, string) {
	dev := strings.TrimSpace(sample.DeviceKey)
	if dev == "" {
		dev = strings.TrimSpace(sample.Descriptor.ComponentID)
	}
	pt := strings.TrimSpace(sample.PointCode)
	if pt == "" {
		pt = strings.TrimSpace(sample.Descriptor.ChannelID)
	}
	return dev, pt
}

func effectiveSamplePriority(desc ChannelDescriptor) DataPriority {
	for _, candidate := range []DataPriority{desc.ResendPriority, desc.PollPriority} {
		if validPriority(candidate) {
			return candidate
		}
	}
	return PriorityMedium
}

func isProtectedAggregationSemantic(sem SemanticChannel) bool {
	switch sem {
	case SemanticEnergy,
		SemanticCoolingEnergy,
		SemanticFaultCode,
		SemanticRunState,
		SemanticTemperatureSetpoint,
		SemanticFrequencySetpoint,
		SemanticFanSpeedSetpoint,
		SemanticLoadLimit,
		SemanticStartCommand,
		SemanticStopCommand,
		SemanticResetFaultCommand:
		return true
	default:
		return false
	}
}

var protectedPointTokens = []string{
	"energy", "kwh", "alarm", "fault", "run_state",
	"flow_switch", "valve", "local_remote", "setpoint",
}

func isProtectedAggregationPointCode(pointCode string) bool {
	lower := strings.ToLower(strings.TrimSpace(pointCode))
	if lower == "" {
		return true
	}
	return strings.HasSuffix(lower, "_sp") || slices.ContainsFunc(protectedPointTokens, func(tok string) bool {
		return strings.Contains(lower, tok)
	})
}

// CollectEdgeRuleAddresses walks Raise and Clear condition trees in bundle and returns a sorted,
// deduplicated slice of all referenced channel/point inputs so they can remain unaggregated.
func CollectEdgeRuleAddresses(bundle *EdgeRuleBundle) []string {
	if bundle == nil || len(bundle.Rules) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var walk func(cond EdgeCondition)
	walk = func(cond EdgeCondition) {
		if trimmed := strings.TrimSpace(cond.Input); trimmed != "" {
			seen[trimmed] = struct{}{}
		}
		for _, val := range cond.Inputs {
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				seen[trimmed] = struct{}{}
			}
		}
		for _, child := range cond.Children {
			walk(child)
		}
	}
	for _, rule := range bundle.Rules {
		walk(rule.Raise)
		walk(rule.Clear)
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}
