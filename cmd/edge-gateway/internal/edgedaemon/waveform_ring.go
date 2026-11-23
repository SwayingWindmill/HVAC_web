package edgedaemon

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	defaultWaveformRingHorizon   = 15 * time.Minute
	maxWaveformRingSamplesPerKey = 960
	maxWaveformRingPoints        = 256
)

type waveformPointSample struct {
	tsMs  int64
	value float64
}

type pendingWaveformCapture struct {
	captureID     string
	ruleID        string
	triggerSource string
	triggerReason string
	triggeredAt   time.Time
	windowStart   time.Time
	windowEnd     time.Time
	sampleRateHz  int
	pointCodes    []string
	requestedBy   string
}

// WaveformRingBuffer retains a bounded high-frequency raw process-image window (default 15m @ 1Hz)
// before deadband/aggregation and produces pre/post transient bursts on auto-rule or manual trigger.
type WaveformRingBuffer struct {
	mu          sync.Mutex
	horizon     time.Duration
	points      map[string][]waveformPointSample
	rules       []edgecontrol.WaveformRuleConfig
	lastFiredAt map[string]time.Time
	prevValues  map[string]float64
	pending     []pendingWaveformCapture
}

func NewWaveformRingBuffer(horizon time.Duration) *WaveformRingBuffer {
	if horizon <= 0 {
		horizon = defaultWaveformRingHorizon
	}
	return &WaveformRingBuffer{
		horizon:     horizon,
		points:      make(map[string][]waveformPointSample),
		lastFiredAt: make(map[string]time.Time),
		prevValues:  make(map[string]float64),
	}
}

func (r *WaveformRingBuffer) SetRules(rules []edgecontrol.WaveformRuleConfig) error {
	if r == nil {
		return nil
	}
	cleaned := make([]edgecontrol.WaveformRuleConfig, 0, len(rules))
	for _, raw := range rules {
		v, err := edgecontrol.ValidateWaveformRuleConfig(raw)
		if err != nil {
			return err
		}
		cleaned = append(cleaned, v)
	}
	r.mu.Lock()
	r.rules = cleaned
	r.mu.Unlock()
	return nil
}

func (r *WaveformRingBuffer) Rules() []edgecontrol.WaveformRuleConfig {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]edgecontrol.WaveformRuleConfig, len(r.rules))
	copy(out, r.rules)
	return out
}

func (r *WaveformRingBuffer) TriggerManual(cmd edgecontrol.DiagnosticsDownlinkPayload, now time.Time) (bool, *edgecontrol.WaveformBurstPayload) {
	if r == nil {
		return false, nil
	}
	ts := now.UTC()
	preSec := cmd.PreSeconds
	if preSec <= 0 {
		preSec = 30
	}
	if preSec > edgecontrol.WaveformSampleMaxPreSeconds {
		preSec = edgecontrol.WaveformSampleMaxPreSeconds
	}
	postSec := cmd.PostSeconds
	if postSec < 0 {
		postSec = 0
	}
	if postSec > edgecontrol.WaveformSampleMaxPostSeconds {
		postSec = edgecontrol.WaveformSampleMaxPostSeconds
	}
	rateHz := cmd.SampleRateHz
	if rateHz <= 0 {
		rateHz = 1
	}
	pts := cleanPointList(cmd.DeviceCode, cmd.PointCode, cmd.PointCodes)
	captureID := strings.TrimSpace(cmd.CaptureID)
	if captureID == "" {
		captureID = strings.TrimSpace(cmd.RequestID)
	}
	req := pendingWaveformCapture{
		captureID:     captureID,
		ruleID:        strings.TrimSpace(cmd.RuleID),
		triggerSource: "MANUAL",
		triggerReason: firstNonEmpty(cmd.TriggerReason, "运维手动触发高频波形抓拍"),
		triggeredAt:   ts,
		windowStart:   ts.Add(-time.Duration(preSec) * time.Second),
		windowEnd:     ts.Add(time.Duration(postSec) * time.Second),
		sampleRateHz:  rateHz,
		pointCodes:    pts,
		requestedBy:   strings.TrimSpace(cmd.RequestedBy),
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if postSec == 0 {
		burst := r.extractBurstLocked(req)
		return true, &burst
	}
	r.pending = append(r.pending, req)
	return false, nil
}

// ObserveSnapshots ingests raw ProcessImage snapshots BEFORE aggregator/deadband, evaluates
// trigger rules, and returns any newly completed [t-pre, t+post] bursts ready to upload.
func (r *WaveformRingBuffer) ObserveSnapshots(now time.Time, img edgecontrol.ProcessImage) []edgecontrol.WaveformBurstPayload {
	if r == nil {
		return nil
	}
	ts := now.UTC()
	tsMs := ts.UnixMilli()
	cutoffMs := ts.Add(-r.horizon).UnixMilli()

	current := make(map[string]float64)
	for _, snap := range img.Channels() {
		if !snap.HasValue {
			continue
		}
		num, ok := snapshotWaveformValue(snap.Sample.Value)
		if !ok {
			continue
		}
		comp := strings.TrimSpace(snap.Descriptor.ComponentID)
		ch := strings.TrimSpace(snap.Descriptor.ChannelID)
		pt := strings.TrimSpace(snap.Descriptor.PointID)
		if comp != "" && ch != "" {
			current[strings.ToLower(comp+"."+ch)] = num
		}
		if comp != "" && pt != "" && pt != ch {
			current[strings.ToLower(comp+"."+pt)] = num
		}
		for _, rawKey := range []string{snap.Descriptor.Address(), ch, pt} {
			k := strings.ToLower(strings.TrimSpace(rawKey))
			if k != "" {
				if _, exists := current[k]; !exists {
					current[k] = num
				}
			}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for key, val := range current {
		series, exists := r.points[key]
		if !exists && len(r.points) >= maxWaveformRingPoints {
			continue
		}
		series = append(series, waveformPointSample{tsMs: tsMs, value: val})
		// Drop expired samples beyond horizon and cap length.
		dropIdx := 0
		for dropIdx < len(series) && series[dropIdx].tsMs < cutoffMs {
			dropIdx++
		}
		if len(series)-dropIdx > maxWaveformRingSamplesPerKey {
			dropIdx = len(series) - maxWaveformRingSamplesPerKey
		}
		if dropIdx > 0 {
			copy(series, series[dropIdx:])
			series = series[:len(series)-dropIdx]
		}
		r.points[key] = series
	}

	for _, rule := range r.rules {
		if !rule.Enabled {
			continue
		}
		targetKey := strings.ToLower(strings.TrimSpace(rule.DeviceCode + "." + rule.PointCode))
		fallbackKey := strings.ToLower(strings.TrimSpace(rule.PointCode))
		val, ok := current[targetKey]
		matchedKey := targetKey
		if !ok {
			val, ok = current[fallbackKey]
			matchedKey = fallbackKey
		}
		if !ok {
			continue
		}
		prevVal, hadPrev := r.prevValues[matchedKey]
		r.prevValues[matchedKey] = val

		cooldownKey := firstNonEmpty(rule.RuleID, rule.RuleName)
		if last, fired := r.lastFiredAt[cooldownKey]; fired && ts.Sub(last) < time.Duration(rule.CooldownSeconds)*time.Second {
			continue
		}

		triggered, reason := r.matchRuleLocked(rule, matchedKey, val, prevVal, hadPrev, tsMs)
		if triggered {
			r.lastFiredAt[cooldownKey] = ts
			captureID := fmt.Sprintf("0194f000-9900-7000-8000-%012x", ts.UnixNano()&0xffffffffffff)
			r.pending = append(r.pending, pendingWaveformCapture{
				captureID:     captureID,
				ruleID:        rule.RuleID,
				triggerSource: "AUTO_RULE",
				triggerReason: reason,
				triggeredAt:   ts,
				windowStart:   ts.Add(-time.Duration(rule.PreSeconds) * time.Second),
				windowEnd:     ts.Add(time.Duration(rule.PostSeconds) * time.Second),
				sampleRateHz:  rule.SampleRateHz,
				pointCodes:    append([]string(nil), rule.PointCodes...),
				requestedBy:   "edge-rule:" + rule.RuleName,
			})
		}
	}

	var completed []edgecontrol.WaveformBurstPayload
	remaining := r.pending[:0]
	for _, item := range r.pending {
		if !ts.Before(item.windowEnd) {
			completed = append(completed, r.extractBurstLocked(item))
		} else {
			remaining = append(remaining, item)
		}
	}
	r.pending = remaining
	return completed
}

func (r *WaveformRingBuffer) matchRuleLocked(rule edgecontrol.WaveformRuleConfig, key string, val, prev float64, hadPrev bool, nowMs int64) (bool, string) {
	switch rule.TriggerType {
	case edgecontrol.WaveformTriggerThresholdHigh:
		if val >= rule.ThresholdValue {
			return true, fmt.Sprintf("%s 触发高限阈值 (%.2f >= %.2f)", rule.RuleName, val, rule.ThresholdValue)
		}
	case edgecontrol.WaveformTriggerThresholdLow:
		if val <= rule.ThresholdValue {
			return true, fmt.Sprintf("%s 触发低限阈值 (%.2f <= %.2f)", rule.RuleName, val, rule.ThresholdValue)
		}
	case edgecontrol.WaveformTriggerDeltaJump:
		refVal := prev
		refFound := hadPrev
		sinceMs := nowMs - int64(rule.DeltaWindowSeconds)*1000
		if series := r.points[key]; len(series) > 1 {
			for i := len(series) - 2; i >= 0; i-- {
				if series[i].tsMs <= sinceMs {
					refVal = series[i].value
					refFound = true
					break
				}
				refVal = series[i].value
				refFound = true
			}
		}
		if refFound && math.Abs(val-refVal) >= math.Abs(rule.ThresholdValue) {
			return true, fmt.Sprintf("%s 触发突变差值 (|%.2f - %.2f| >= %.2f)", rule.RuleName, val, refVal, math.Abs(rule.ThresholdValue))
		}
	case edgecontrol.WaveformTriggerTripState:
		if hadPrev && prev == 0 && val != 0 {
			return true, fmt.Sprintf("%s 监测到跳机状态跃变 (%.0f -> %.0f)", rule.RuleName, prev, val)
		}
	}
	return false, ""
}

func (r *WaveformRingBuffer) extractBurstLocked(req pendingWaveformCapture) edgecontrol.WaveformBurstPayload {
	startMs := req.windowStart.UnixMilli()
	endMs := req.windowEnd.UnixMilli()
	targetKeys := make([]string, 0, len(req.pointCodes))
	for _, pt := range req.pointCodes {
		k := strings.ToLower(strings.TrimSpace(pt))
		if k != "" {
			targetKeys = append(targetKeys, k)
		}
	}
	if len(targetKeys) == 0 {
		for k := range r.points {
			if strings.Contains(k, ".") {
				targetKeys = append(targetKeys, k)
			}
		}
		sort.Strings(targetKeys)
	}

	byTimestamp := make(map[int64]map[string]float64)
	for _, key := range targetKeys {
		series := r.points[key]
		if len(series) == 0 {
			if dot := strings.LastIndex(key, "."); dot >= 0 && dot+1 < len(key) {
				series = r.points[key[dot+1:]]
			}
		}
		for _, pt := range series {
			if pt.tsMs < startMs || pt.tsMs > endMs {
				continue
			}
			row, exists := byTimestamp[pt.tsMs]
			if !exists {
				row = make(map[string]float64)
				byTimestamp[pt.tsMs] = row
			}
			row[key] = pt.value
		}
	}

	timestamps := make([]int64, 0, len(byTimestamp))
	for ts := range byTimestamp {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	if len(timestamps) > edgecontrol.WaveformMaxCaptureSamples {
		timestamps = timestamps[len(timestamps)-edgecontrol.WaveformMaxCaptureSamples:]
	}

	samples := make([]edgecontrol.WaveformSampleRow, 0, len(timestamps))
	for _, ts := range timestamps {
		samples = append(samples, edgecontrol.WaveformSampleRow{
			SampledAtMs: ts,
			Values:      byTimestamp[ts],
		})
	}
	return edgecontrol.WaveformBurstPayload{
		CaptureID:     req.captureID,
		RuleID:        req.ruleID,
		TriggerSource: req.triggerSource,
		TriggerReason: req.triggerReason,
		Status:        "COMPLETED",
		TriggeredAtMs: req.triggeredAt.UnixMilli(),
		WindowStartMs: startMs,
		WindowEndMs:   endMs,
		SampleRateHz:  req.sampleRateHz,
		Samples:       samples,
		RequestedBy:   req.requestedBy,
	}
}

func (r *WaveformRingBuffer) BufferStats() (activePoints int, approxBytes int64) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var sampleTotal int64
	for _, series := range r.points {
		sampleTotal += int64(len(series))
	}
	// Each sample is 16B struct + map bucket overhead ~24B.
	return len(r.points), sampleTotal * 24
}

func snapshotWaveformValue(v edgecontrol.Value) (float64, bool) {
	if v.Type == edgecontrol.DataTypeBoolean {
		if v.Boolean {
			return 1, true
		}
		return 0, true
	}
	return sampleNumericValue(v)
}

func cleanPointList(deviceCode, pointCode string, extra []string) []string {
	seen := make(map[string]struct{}, len(extra)+1)
	out := make([]string, 0, len(extra)+1)
	add := func(s string) {
		trimmed := strings.ToLower(strings.TrimSpace(s))
		if trimmed == "" {
			return
		}
		if _, dup := seen[trimmed]; dup {
			return
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if strings.TrimSpace(deviceCode) != "" && strings.TrimSpace(pointCode) != "" {
		add(deviceCode + "." + pointCode)
	} else if strings.TrimSpace(pointCode) != "" {
		add(pointCode)
	}
	for _, code := range extra {
		add(code)
	}
	return out
}
