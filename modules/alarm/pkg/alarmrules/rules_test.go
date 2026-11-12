package alarmrules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

const songyaxuanRules = "../../../../deploy/sites/songyaxuan.alarm-rules.json"

// #609: 松雅轩's rules file becomes policies the evaluator accepts, over the points the site file
// marks active; inactive points (no probe wired, like t14) are watched by no rule.
func TestSongyaxuanRulesCompileToValidPoliciesOverActivePoints(t *testing.T) {
	compiled, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.TenantID != "018f3d00-0000-7000-8000-000000000001" || compiled.SiteID != "01a14000-1000-7000-8000-000000000001" {
		t.Fatalf("rules compiled for the wrong Site: %s %s", compiled.TenantID, compiled.SiteID)
	}

	byTitle := map[string]alarmservice.AlarmPolicyRevision{}
	byAlarmType := map[string]alarmservice.AlarmPolicyRevision{}
	for _, sitePolicy := range compiled.Policies {
		policy := sitePolicy.Policy
		policy.PolicyRevisionID = "01a14000-7a00-7000-8000-000000000001"
		policy.Revision = 1
		policy.Digest, err = alarmservice.AlarmPolicyDigest(policy)
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("%s: %v", policy.Title, err)
		}
		if _, duplicate := byTitle[policy.Title]; duplicate {
			t.Fatalf("duplicate policy title %q", policy.Title)
		}
		if _, duplicate := byAlarmType[policy.AlarmType]; duplicate {
			t.Fatalf("duplicate policy alarmType %q", policy.AlarmType)
		}
		byTitle[policy.Title] = policy
		byAlarmType[policy.AlarmType] = policy
	}
	// The supply-temperature rule, the four plant rules kept after 2026-11-10 (供水偏高、供水过低、
	// 冷却水出水过高、机组运行无流量), 网关上报中断, 13 critical-point reading-gap rules (#796/#798: 4 sub-meters ×
	// 2, cooling meter × 2, header supply/return t16 & t18, main chilled-water flow), one
	// 读数长时间没有更新 per device that still has non-critical own active points (10, including
	// meter-total which is kept as a non-critical reconciliation meter under sum-of-sub-meters;
	// cooling-tower and outdoor have only copied points, room has no probe),
	// the data-quality rules from each point's quality profile, the physics checks over active
	// points (16), and five probe-offset suggestions (header + chillers A–D).
	if len(byTitle) != 112 {
		t.Fatalf("got %d rules: %v", len(byTitle), keys(byTitle))
	}

	wantGaps := map[string]string{
		"reading-gap/temperature-uid20/t16":               "温度采集模块：冷冻水总回水温度 1读数中断",
		"reading-gap/temperature-uid20/t18":               "温度采集模块：冷冻水总供水温度 1读数中断",
		"reading-gap/flow-uid10/flow_meter":               "总冷冻水流量计：总冷冻水流量读数中断",
		"reading-gap/meter-uid5/power_total":              "C 机组电表：有功功率读数中断",
		"reading-gap/meter-uid5/active_energy_total":      "C 机组电表：累计电量读数中断",
		"reading-gap/meter-uid2/power_total":              "D 机组电表：有功功率读数中断",
		"reading-gap/meter-uid2/active_energy_total":      "D 机组电表：累计电量读数中断",
		"reading-gap/meter-uid3/power_total":              "A 机组电表：有功功率读数中断",
		"reading-gap/meter-uid3/active_energy_total":      "A 机组电表：累计电量读数中断",
		"reading-gap/meter-uid4/power_total":              "B 机组电表：有功功率读数中断",
		"reading-gap/meter-uid4/active_energy_total":      "B 机组电表：累计电量读数中断",
		"reading-gap/btu-calc/instant_cooling_capacity":   "冷量计：瞬时制冷量读数中断",
		"reading-gap/btu-calc/accumulated_cooling_energy": "冷量计：累计供冷量读数中断",
	}
	for code, wantTitle := range wantGaps {
		gap, ok := byAlarmType[code]
		if !ok {
			t.Fatalf("missing critical-point reading-gap rule %s", code)
		}
		if gap.Title != wantTitle || gap.Severity != "MAJOR" || gap.FreshnessSeconds != 900 || gap.SourceReference != "alarm-rules:songyaxuan/"+code {
			t.Fatalf("reading-gap rule %s mismatch: %#v", code, gap)
		}
		if gap.Raise.Kind != alarmservice.ConditionNoData || gap.Raise.AgeSeconds != 900 || len(gap.Raise.Children) != 0 {
			t.Fatalf("reading-gap %s raise must be a single NO_DATA leaf: %#v", code, gap.Raise)
		}
		if gap.Clear.Kind != alarmservice.ConditionRange || gap.Clear.Input != gap.Raise.Input || len(gap.Clear.Children) != 0 {
			t.Fatalf("reading-gap %s clear must be a single hasReading leaf on the same point: %#v", code, gap.Clear)
		}
		if strings.Contains(gap.Title+gap.Summary, "UID") {
			t.Fatalf("reading-gap %s leaks raw UID wording: %q / %q", code, gap.Title, gap.Summary)
		}
	}

	supply, ok := byTitle["冷冻水供水温度过高"]
	if !ok || supply.Severity != "MAJOR" || supply.DurationSeconds != 900 {
		t.Fatalf("supply-temperature rule: %#v", supply)
	}
	if got := inputs(supply.Raise); !reflect.DeepEqual(got, []string{
		"01a14000-4000-7000-8000-000000000003/run_state", "01a14000-4000-7000-8000-000000000004/run_state",
		"01a14000-4000-7000-8000-000000000005/run_state", "01a14000-4000-7000-8000-000000000006/run_state",
		"01a14000-4000-7000-8000-000000000009/supply_water_temperature",
	}) {
		t.Fatalf("supply-temperature rule reads %v", got)
	}

	// #796: 读数长时间没有更新 watches only the device's own non-critical active points (neither copied
	// temperatures nor critical meter readings); a single remaining point stands as a leaf predicate.
	chillerC, ok := byTitle["C 机组电表读数长时间没有更新"]
	if !ok || chillerC.Severity != "WARNING" {
		t.Fatalf("no readings rule for C 机组电表: %v", keys(byTitle))
	}
	if chillerC.Raise.Kind != alarmservice.ConditionNoData || chillerC.Clear.Kind != alarmservice.ConditionCompare {
		t.Fatalf("single-point readings rule wrapped in compound condition: raise=%#v clear=%#v", chillerC.Raise, chillerC.Clear)
	}
	if got := inputs(chillerC.Raise); !reflect.DeepEqual(got, []string{"01a14000-4000-7000-8000-000000000003/run_state"}) {
		t.Fatalf("C 机组电表 readings rule should watch only run_state, got %v", got)
	}
	if !strings.Contains(chillerC.Summary, "超过 30 分钟没有更新") || !strings.Contains(chillerC.Summary, "运行状态") || strings.Contains(chillerC.Summary, "冷却水出水温度") || strings.Contains(chillerC.Summary, "累计电量") || strings.Contains(chillerC.Title+chillerC.Summary, "UID") {
		t.Fatalf("C 机组电表 readings rule is not said plainly over own non-critical points: %q / %q", chillerC.Title, chillerC.Summary)
	}
	totalMeter, ok := byTitle["空调总电表读数长时间没有更新"]
	if !ok || totalMeter.Severity != "WARNING" {
		t.Fatalf("meter-total reconciliation meter should compile to WARNING readings-not-updated: %#v", totalMeter)
	}
	flowRest, ok := byTitle["总冷冻水流量计读数长时间没有更新"]
	if !ok || flowRest.Raise.Kind != alarmservice.ConditionNoData || !reflect.DeepEqual(inputs(flowRest.Raise), []string{"01a14000-4000-7000-8000-000000000002/flow_velocity"}) {
		t.Fatalf("flow meter readings rule should watch only flow_velocity as leaf: %#v", flowRest)
	}
	for _, omitted := range []string{"冷却塔读数长时间没有更新", "室外环境读数长时间没有更新", "房间读数长时间没有更新"} {
		if _, exists := byTitle[omitted]; exists {
			t.Fatalf("%s should not be emitted when device has 0 own non-critical active points", omitted)
		}
	}

	// Publishing retires an old readings-not-updated rule whose device now has 0 own non-critical points.
	legacyCoolingTower := sitePolicy(compiled.SiteID, "songyaxuan", "readings-not-updated/cooling-tower", alarmservice.AlarmPolicyRevision{Title: "冷却塔读数长时间没有更新"})
	plan, err := alarmservice.PlanSiteRules(compiled.Policies, append([]alarmservice.SiteRule{legacyCoolingTower}, compiled.Policies...))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Retired) != 1 || plan.Retired[0].AssignmentID != legacyCoolingTower.AssignmentID {
		t.Fatalf("expected legacy cooling-tower readings-not-updated rule to be retired, got %#v", plan.Retired)
	}

	// #615: the Site has stopped reporting when none of its 78 active points has a reading for 15 minutes
	// (67 until 2026-11-10 wired every chiller's water temperatures and the cooling tower back in and withdrew
	// the room temperature, which has no probe).
	silent, ok := byTitle["网关上报中断"]
	if !ok || silent.Severity != "MAJOR" || len(inputs(silent.Raise)) != 78 || len(inputs(silent.Clear)) != 78 {
		t.Fatalf("网关上报中断 rule: %#v", silent)
	}
	if !strings.Contains(silent.Summary, "15 分钟") {
		t.Fatalf("网关上报中断 is not said plainly: %q", silent.Summary)
	}

	// Outdoor temperature on channel 15, 3 hours without a change, a 警告. The 室外环境 device
	// only copies that channel, so it has no rule of its own.
	flat, ok := byTitle["室外温度卡住"]
	if !ok || flat.Severity != "WARNING" || !reflect.DeepEqual(inputs(flat.Raise), []string{"01a14000-4000-7000-8000-000000000001/t15"}) {
		t.Fatalf("channel 15 flat-reading rule: %#v / %v", flat, inputs(flat.Raise))
	}
	if !strings.Contains(flat.Summary, "温度采集模块的室外温度连续 180 分钟") {
		t.Fatalf("channel 15 flat-reading rule is not said plainly: %q", flat.Summary)
	}
	power, ok := byTitle["C 机组功率超过铭牌"]
	if !ok || power.Severity != "MAJOR" || !reflect.DeepEqual(power.Raise.Reasons, []string{"PHYSICS_POWER"}) {
		t.Fatalf("chiller C nameplate power rule: %#v", power)
	}
	cwDirection, ok := byTitle["C 机组冷却水出水不高于进水"]
	if !ok || cwDirection.Severity != "WARNING" || !reflect.DeepEqual(cwDirection.Raise.Reasons, []string{"PHYSICS_CW_DIRECTION"}) {
		t.Fatalf("chiller C condenser direction rule: %#v", cwDirection)
	}
	pumps, ok := byTitle["冷冻水泵在跑但没有机组运行"]
	if !ok || pumps.Severity != "WARNING" || pumps.TriggerMode != "DURATION" || pumps.DurationSeconds != 600 {
		t.Fatalf("pump-without-chiller rule: %#v", pumps)
	}
	supplyHealth, ok := byTitle["冷冻水总供水温度 1数据不可信"]
	if !ok || supplyHealth.Severity != "MAJOR" || supplyHealth.Raise.Kind != "REASONS" {
		t.Fatalf("supply data-quality rule: %#v", supplyHealth)
	}
	for title, policy := range byTitle {
		if strings.Contains(title, "卡住") && strings.Contains(inputs(policy.Raise)[0], "-00000000000c/") {
			t.Fatalf("%s watches a copied reading", title)
		}
	}

	module := inputs(byTitle["温度采集模块读数长时间没有更新"].Raise)
	if len(module) != 16 || contains(module, "01a14000-4000-7000-8000-000000000001/t14") || contains(module, "01a14000-4000-7000-8000-000000000001/t20") || contains(module, "01a14000-4000-7000-8000-000000000001/t16") || contains(module, "01a14000-4000-7000-8000-000000000001/t18") {
		t.Fatalf("temperature module readings rule watches inactive or critical channels: %v", module)
	}
	for _, policy := range byTitle {
		if raised := strings.Join(inputs(policy.Raise), " "); strings.Contains(raised, "/ambient_wet_bulb_temperature") || strings.Contains(raised, "/ambient_temperature_2") {
			t.Fatalf("%s watches an outdoor reading no probe sends", policy.Title)
		}
	}

	// All 78 active Site inputs fit within EvaluationSnapshot's 128-input cap, and every compiled
	// Songyaxuan policy evaluates cleanly against a full Site snapshot.
	now := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	fullInputs := map[string]alarmservice.InputFact{}
	for _, key := range inputs(silent.Raise) {
		if strings.HasSuffix(key, "/run_state") {
			fullInputs[key] = alarmservice.StringInput(key, "STOPPED", "GOOD", now.Format(time.RFC3339Nano))
		} else {
			fullInputs[key] = alarmservice.NumberInput(key, 20, "GOOD", now.Format(time.RFC3339Nano))
		}
	}
	if len(fullInputs) != 78 {
		t.Fatalf("expected 78 distinct Site inputs, got %d", len(fullInputs))
	}
	for _, policy := range byTitle {
		_ = evaluate(t, policy, "full-site-1", fullInputs, alarmservice.AlarmEvaluationState{}, now)
	}

	again, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiled, again) {
		t.Fatal("compiling the same file twice gave different policies or ids")
	}
}

// #796/#798, the 2026-11-08 outage: D 机组电表 (UID 2) and 空调总电表 stopped sending their own energy
// while copied temperatures kept arriving on UID 2 every minute. Even with UID 2's 运行状态 and
// 有功功率 already in gaps open since 11-01, 累计电量 raises its own MAJOR reading-gap alarm after
// 15 minutes, stays open while stale readings sit alongside fresh copied temperatures, and clears
// cleanly once a fresh 累计电量 reading arrives.
func TestCriticalPointReadingGapReproduces20261108OutageAndClears(t *testing.T) {
	compiled, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	const (
		uid2    = "01a14000-4000-7000-8000-000000000004"
		btuCalc = "01a14000-4000-7000-8000-000000000009"
	)
	energy := published(t, compiled, "D 机组电表：累计电量读数中断")
	power := published(t, compiled, "D 机组电表：有功功率读数中断")
	deviceRest := published(t, compiled, "D 机组电表读数长时间没有更新")
	coolingEnergy := published(t, compiled, "冷量计：累计供冷量读数中断")

	longGone := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	stopped := time.Date(2026, 11, 8, 8, 13, 0, 0, time.UTC) // 16:13 Beijing time

	snapshotAt := func(at time.Time, energySampledAt time.Time, energyValue float64) map[string]alarmservice.InputFact {
		facts := map[string]alarmservice.InputFact{}
		// Copied temperatures keep arriving fresh every minute on UID 2.
		for _, point := range []string{"chw_leaving_temperature", "chw_entering_temperature", "cw_entering_temperature", "cw_leaving_temperature"} {
			facts[uid2+"/"+point] = alarmservice.NumberInput(uid2+"/"+point, 12.4, "GOOD", at.Format(time.RFC3339Nano))
		}
		// Power and run_state have had no new reading since 2026-11-01.
		facts[uid2+"/power_total"] = alarmservice.NumberInput(uid2+"/power_total", 0, "STALE", longGone.Format(time.RFC3339Nano))
		facts[uid2+"/run_state"] = alarmservice.StringInput(uid2+"/run_state", "STOPPED", "STALE", longGone.Format(time.RFC3339Nano))

		energyQuality := "GOOD"
		if !at.Before(energySampledAt.Add(15 * time.Minute)) {
			energyQuality = "STALE"
		}
		facts[uid2+"/active_energy_total"] = alarmservice.NumberInput(uid2+"/active_energy_total", energyValue, energyQuality, energySampledAt.Format(time.RFC3339Nano))
		facts[btuCalc+"/accumulated_cooling_energy"] = alarmservice.NumberInput(btuCalc+"/accumulated_cooling_energy", 210000+energyValue, energyQuality, energySampledAt.Format(time.RFC3339Nano))
		return facts
	}

	// Both power reading-gap and run_state readings-not-updated are already open before 16:13,
	// and fresh copied temperatures on UID 2 do not clear them.
	powerOpen := evaluate(t, power, "rev-0", snapshotAt(stopped, stopped, 51234), alarmservice.AlarmEvaluationState{}, stopped)
	if powerOpen.Effect != alarmservice.EvaluationEffectPublish || powerOpen.Publication.Severity != "MAJOR" {
		t.Fatalf("power absent since 11-01 should have raised MAJOR: %#v", powerOpen)
	}
	restOpen := evaluate(t, deviceRest, "rev-0", snapshotAt(stopped, stopped, 51234), alarmservice.AlarmEvaluationState{}, stopped)
	if restOpen.Effect != alarmservice.EvaluationEffectPublish || restOpen.Publication.Severity != "WARNING" {
		t.Fatalf("run_state absent since 11-01 should have raised WARNING: %#v", restOpen)
	}

	// At 16:13 and 16:27 (+14m), neither UID 2 energy nor btu-calc cooling energy is overdue;
	// btu-calc arms NextEvaluationAt at 16:13 and receives no new snapshot revision after stopping.
	initial := evaluate(t, energy, "rev-1", snapshotAt(stopped, stopped, 51234), alarmservice.AlarmEvaluationState{}, stopped)
	if initial.Effect != alarmservice.EvaluationEffectNone || initial.State.NextEvaluationAt == nil {
		t.Fatalf("fresh energy reading raised or scheduled no follow-up: %#v", initial)
	}
	coolingInitial := evaluate(t, coolingEnergy, "cooling-rev-1", snapshotAt(stopped, stopped, 51234), alarmservice.AlarmEvaluationState{}, stopped)
	if coolingInitial.Effect != alarmservice.EvaluationEffectNone || coolingInitial.State.NextEvaluationAt == nil {
		t.Fatalf("fresh cooling energy raised or scheduled no follow-up: %#v", coolingInitial)
	}
	beforeDue := stopped.Add(14 * time.Minute)
	stillFresh := evaluate(t, energy, "rev-2", snapshotAt(beforeDue, stopped, 51234), initial.State, beforeDue)
	if stillFresh.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("energy raised before 15 minutes: %#v", stillFresh)
	}

	// At 16:28 (+15m), UID 2 energy raises MAJOR reading-gap despite fresh copied temperatures and
	// two already-open rules on UID 2, and silent btu-calc raises MAJOR via due-evaluation with unchanged revision.
	gapAt := stopped.Add(15 * time.Minute)
	raised := evaluate(t, energy, "rev-3", snapshotAt(gapAt, stopped, 51234), stillFresh.State, gapAt)
	if raised.Effect != alarmservice.EvaluationEffectPublish || raised.Publication == nil || raised.Publication.Severity != "MAJOR" || raised.Publication.AlarmType != "reading-gap/meter-uid2/active_energy_total" {
		t.Fatalf("15 minutes without own energy reading did not raise MAJOR reading-gap: %#v", raised)
	}
	coolingRaised := evaluate(t, coolingEnergy, "cooling-rev-1", snapshotAt(gapAt, stopped, 51234), coolingInitial.State, gapAt)
	if coolingRaised.Effect != alarmservice.EvaluationEffectPublish || coolingRaised.Publication == nil || coolingRaised.Publication.Severity != "MAJOR" {
		t.Fatalf("silent cooling meter energy did not raise MAJOR reading-gap on due evaluation: %#v", coolingRaised)
	}

	// At 16:35 (+22m), copied temperatures arrive again while energy remains stale: stays ACTIVE.
	later := stopped.Add(22 * time.Minute)
	stillInGap := evaluate(t, energy, "rev-4", snapshotAt(later, stopped, 51234), active(raised.State), later)
	if stillInGap.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("stale energy reading with fresh copied temperatures cleared prematurely: %#v", stillInGap)
	}

	// At 16:40 (+27m), a fresh energy reading arrives: clears energy reading-gap while power and run_state stay open.
	resumedAt := stopped.Add(27 * time.Minute)
	cleared := evaluate(t, energy, "rev-5", snapshotAt(resumedAt, resumedAt, 51240), active(stillInGap.State), resumedAt)
	if cleared.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("fresh energy reading did not clear reading-gap: %#v", cleared)
	}
	if powerStill := evaluate(t, power, "rev-5", snapshotAt(resumedAt, resumedAt, 51240), active(powerOpen.State), resumedAt); powerStill.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("energy recovery should not clear still-missing power reading-gap: %#v", powerStill)
	}
	if restStill := evaluate(t, deviceRest, "rev-5", snapshotAt(resumedAt, resumedAt, 51240), active(restOpen.State), resumedAt); restStill.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("energy recovery and copied temperatures should not clear still-missing run_state: %#v", restStill)
	}
}

func TestReadingGapsMinimalConfigMorePointsAndEdgeCases(t *testing.T) {
	directory := t.TempDir()
	site := map[string]any{
		"tenantId": "018f3d00-0000-7000-8000-000000000001",
		"site":     map[string]any{"id": "01a14000-1000-7000-8000-000000000001", "code": "demo"},
		"meterPoints": []any{
			map[string]any{"code": "active_energy_total", "name": "累计电量", "valueType": "NUMBER", "critical": true},
		},
		"devices": []any{
			// Device 1: meterPoints critical + critical morePoint + copied critical (ignored) + inactive critical (ignored) -> 0 non-critical own points.
			map[string]any{
				"id": "01a14000-4000-7000-8000-000000000001", "code": "meter-1", "name": "1 号电表（UID 1）", "points": "meter",
				"morePoints": []any{
					map[string]any{"code": "demand_kw", "name": "需量功率", "valueType": "NUMBER", "critical": true},
					map[string]any{"code": "copied_temp", "name": "复制温度", "valueType": "NUMBER", "from": []any{"temp", "t1"}, "critical": true},
					map[string]any{"code": "retired_energy", "name": "停用电量", "valueType": "NUMBER", "status": "INACTIVE", "critical": true},
				},
			},
			// Device 2: 1 boolean critical point + 1 boolean non-critical own point -> leaf Clear/Raise valid.
			map[string]any{
				"id": "01a14000-4000-7000-8000-000000000002", "code": "valve-1", "name": "总管蝶阀",
				"points": []any{
					map[string]any{"code": "open_feedback", "name": "开到位状态", "valueType": "BOOLEAN", "critical": true},
					map[string]any{"code": "fault_state", "name": "故障状态", "valueType": "BOOLEAN"},
				},
			},
		},
	}
	siteBytes, _ := json.Marshal(site)
	if err := os.WriteFile(filepath.Join(directory, "site.json"), siteBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	// Explicit readingGaps config exercising morePoints, boolean Clear/Raise, and ignoring copied/inactive critical points.
	rulesBytes, _ := json.Marshal(map[string]any{
		"schemaVersion":      1,
		"siteFile":           "site.json",
		"readingGaps":        map[string]any{"title": "{device}：{point}读数中断", "summary": "{device}：{point}（{minutes} 分钟）", "severity": "MAJOR", "ageSeconds": 900},
		"readingsNotUpdated": map[string]any{"title": "{device}读数长时间没有更新", "summary": "{device}：{points}（{minutes} 分钟）", "severity": "WARNING", "ageSeconds": 1800},
	})
	rulesPath := filepath.Join(directory, "rules.json")
	if err := os.WriteFile(rulesPath, rulesBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	compiled, err := Load(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	gotTitles := map[string]alarmservice.AlarmPolicyRevision{}
	for _, entry := range compiled.Policies {
		policy := entry.Policy
		policy.PolicyRevisionID, policy.Revision = "01a14000-7a00-7000-8000-000000000001", 1
		policy.Digest, _ = alarmservice.AlarmPolicyDigest(policy)
		if err := policy.Validate(); err != nil {
			t.Fatalf("%s failed validation: %v", policy.Title, err)
		}
		gotTitles[policy.Title] = policy
	}
	wantTitles := []string{
		"1 号电表：累计电量读数中断",
		"1 号电表：需量功率读数中断",
		"总管蝶阀：开到位状态读数中断",
		"总管蝶阀读数长时间没有更新",
	}
	if len(gotTitles) != len(wantTitles) {
		t.Fatalf("got policies %v, want %v", keys(gotTitles), wantTitles)
	}
	for _, title := range wantTitles {
		if _, ok := gotTitles[title]; !ok {
			t.Fatalf("missing expected policy %q in %v", title, keys(gotTitles))
		}
	}

	// Omitting readingGaps when the site file marks active own points critical is refused rather than silently dropping them.
	missingGapsPath := filepath.Join(directory, "rules-without-gaps.json")
	missingGapsBytes, _ := json.Marshal(map[string]any{"schemaVersion": 1, "siteFile": "site.json"})
	if err := os.WriteFile(missingGapsPath, missingGapsBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(missingGapsPath); err == nil || !strings.Contains(err.Error(), "readingGaps") {
		t.Fatalf("expected error requiring readingGaps when critical points exist, got %v", err)
	}
}

func TestRulesNamingAPointTheSiteDoesNotReadAreRefused(t *testing.T) {
	directory := t.TempDir()
	site, err := os.ReadFile("../../../../deploy/sites/songyaxuan.site.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "site.json"), site, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"outdoor/ambient_wet_bulb_temperature", "no-such-device/run_state"} {
		rules, _ := json.Marshal(map[string]any{
			"schemaVersion": 1, "siteFile": "site.json",
			"rules": []any{map[string]any{
				"code": "probe", "title": "探测", "summary": "探测", "severity": "INFO", "qualityPolicy": "USABLE",
				"freshnessSeconds": 60, "triggerMode": "SIMPLE",
				"raise": map[string]any{"kind": "NO_DATA", "input": input, "ageSeconds": 60},
				"clear": map[string]any{"kind": "RANGE", "input": input, "minimum": 0, "maximum": 1},
			}},
		})
		path := filepath.Join(directory, "rules.json")
		if err := os.WriteFile(path, rules, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), input) {
			t.Fatalf("a rule reading %s was accepted: %v", input, err)
		}
	}
}

func inputs(condition alarmservice.Condition) []string {
	if len(condition.Children) == 0 {
		return []string{condition.Input}
	}
	var all []string
	for _, child := range condition.Children {
		all = append(all, inputs(child)...)
	}
	return all
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func keys(policies map[string]alarmservice.AlarmPolicyRevision) []string {
	titles := make([]string, 0, len(policies))
	for title := range policies {
		titles = append(titles, title)
	}
	return titles
}

// #609: the compiled rules raise and clear as their wording says, run through the evaluator itself.
func TestSongyaxuanRulesRaiseAndClearAsTheirWordingSays(t *testing.T) {
	compiled, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)

	// 读数长时间没有更新: watches only own non-critical active points (run_state on C 机组电表);
	// missing copied temperatures do not raise it, missing run_state raises and fresh run_state clears.
	chillerC := published(t, compiled, "C 机组电表读数长时间没有更新")
	meter := func(withRunState bool) map[string]alarmservice.InputFact {
		inputs := map[string]alarmservice.InputFact{
			"cw_leaving_temperature": {Key: "cw_leaving_temperature", Present: false},
			"active_energy_total":    {Key: "active_energy_total", Present: false},
		}
		if withRunState {
			inputs["run_state"] = alarmservice.StringInput("run_state", "RUNNING", "GOOD", start.Format(time.RFC3339Nano))
		} else {
			inputs["run_state"] = alarmservice.InputFact{Key: "run_state", Present: false}
		}
		return prefixed("01a14000-4000-7000-8000-000000000003", inputs)
	}
	if unraised := evaluate(t, chillerC, "c-0", meter(true), alarmservice.AlarmEvaluationState{}, start); unraised.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("missing copied or critical points raised device readings-not-updated: %#v", unraised)
	}
	raised := evaluate(t, chillerC, "c-1", meter(false), alarmservice.AlarmEvaluationState{}, start)
	if raised.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("own non-critical point with no reading did not raise: %#v", raised)
	}
	if cleared := evaluate(t, chillerC, "c-2", meter(true), active(raised.State), start.Add(time.Minute)); cleared.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("fresh own non-critical reading did not clear: %#v", cleared)
	}

	// 冷冻水供水温度过高: 15 minutes above 10 ℃ with a chiller running; 9.5 ℃ is not yet recovered, 8.5 ℃ is.
	supply := published(t, compiled, "冷冻水供水温度过高")
	plant := func(at time.Time, temperature float64) map[string]alarmservice.InputFact {
		observed := at.Format(time.RFC3339Nano)
		inputs := map[string]alarmservice.InputFact{
			"01a14000-4000-7000-8000-000000000009/supply_water_temperature": alarmservice.NumberInput("01a14000-4000-7000-8000-000000000009/supply_water_temperature", temperature, "GOOD", observed),
		}
		for device, state := range map[string]string{"03": "STOPPED", "04": "STOPPED", "05": "RUNNING", "06": "STOPPED"} {
			key := "01a14000-4000-7000-8000-0000000000" + device + "/run_state"
			inputs[key] = alarmservice.StringInput(key, state, "GOOD", observed)
		}
		return inputs
	}
	candidate := evaluate(t, supply, "s-1", plant(start, 11), alarmservice.AlarmEvaluationState{}, start)
	if candidate.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("raised before 15 minutes: %#v", candidate)
	}
	high := evaluate(t, supply, "s-2", plant(start.Add(15*time.Minute), 11), candidate.State, start.Add(15*time.Minute))
	if high.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("15 minutes above 10 ℃ did not raise: %#v", high)
	}
	if still := evaluate(t, supply, "s-3", plant(start.Add(16*time.Minute), 9.5), active(high.State), start.Add(16*time.Minute)); still.Effect != alarmservice.EvaluationEffectNone {
		t.Fatalf("9.5 ℃ already cleared: %#v", still)
	}
	if recovered := evaluate(t, supply, "s-4", plant(start.Add(17*time.Minute), 8.5), active(high.State), start.Add(17*time.Minute)); recovered.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("8.5 ℃ did not clear: %#v", recovered)
	}

	// 网关上报中断 (#615): with no new reading, the same snapshot looked at again 15 minutes on
	// raises; one reading arriving clears it.
	silent := published(t, compiled, "网关上报中断")
	readings := func(at time.Time) map[string]alarmservice.InputFact {
		facts := map[string]alarmservice.InputFact{}
		for _, key := range inputs(silent.Raise) {
			facts[key] = alarmservice.NumberInput(key, 20, "GOOD", start.Format(time.RFC3339Nano))
		}
		supplyKey := "01a14000-4000-7000-8000-000000000009/supply_water_temperature"
		facts[supplyKey] = alarmservice.NumberInput(supplyKey, 7, "GOOD", at.Format(time.RFC3339Nano))
		return facts
	}
	reporting := evaluate(t, silent, "g-1", readings(start), alarmservice.AlarmEvaluationState{}, start)
	if reporting.Effect != alarmservice.EvaluationEffectNone || reporting.State.NextEvaluationAt == nil {
		t.Fatalf("a reporting Site raised, or set no time to look again: %#v", reporting)
	}
	quiet := evaluate(t, silent, "g-1", readings(start), reporting.State, start.Add(15*time.Minute))
	if quiet.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("15 minutes without a reading did not raise: %#v", quiet)
	}
	back := evaluate(t, silent, "g-2", readings(start.Add(20*time.Minute)), active(quiet.State), start.Add(20*time.Minute))
	if back.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("a reading arriving did not clear: %#v", back)
	}
	// Outdoor temperature holding 21.5 ℃ for 3 hours raises; once it moves, it clears.
	flat := published(t, compiled, "室外温度卡住")
	outdoor := func(at, temperatureSince time.Time, temperature float64) map[string]alarmservice.InputFact {
		key := "01a14000-4000-7000-8000-000000000001/t15"
		fact := alarmservice.NumberInput(key, temperature, "GOOD", at.Format(time.RFC3339Nano))
		fact.UnchangedSince = temperatureSince.Format(time.RFC3339Nano)
		return map[string]alarmservice.InputFact{key: fact}
	}
	held := start.Add(3 * time.Hour)
	stuck := evaluate(t, flat, "f-1", outdoor(held, start, 21.5), alarmservice.AlarmEvaluationState{}, held)
	if stuck.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("3 hours at 21.5 ℃ did not raise: %#v", stuck)
	}
	moving := held.Add(time.Minute)
	if moved := evaluate(t, flat, "f-2", outdoor(moving, moving, 21.6), active(stuck.State), moving); moved.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("a moving temperature did not clear: %#v", moved)
	}

	// If that reading was the last before the gateway dropped again, the second silence raises too.
	if again := evaluate(t, silent, "g-2", readings(start.Add(20*time.Minute)), recovered(back.State), start.Add(35*time.Minute)); again.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("a second silence right after a clear did not raise: %#v", again)
	}
}

// The plant rules added 2026-10-31 against policy v3 (supply 5–8 ℃, comfort 22–26 ℃): each raises
// only after its duration with a chiller running, ignores implausible readings, and clears as worded.
func TestSongyaxuanPlantRulesRaiseAndClearAsTheirWordingSays(t *testing.T) {
	compiled, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 31, 4, 0, 0, 0, time.UTC)
	const (
		btu  = "01a14000-4000-7000-8000-000000000009/"
		flow = "01a14000-4000-7000-8000-000000000002/flow_meter"
		c    = "01a14000-4000-7000-8000-000000000005/"
		d    = "01a14000-4000-7000-8000-000000000006/"
	)
	// plant is the readings at one moment: chiller C running unless stopped, the rest stopped.
	plant := func(at time.Time, running bool, values map[string]float64) map[string]alarmservice.InputFact {
		observed := at.Format(time.RFC3339Nano)
		facts := map[string]alarmservice.InputFact{}
		for key, value := range values {
			facts[key] = alarmservice.NumberInput(key, value, "GOOD", observed)
		}
		for _, device := range []string{"03", "04", "05", "06"} {
			key := "01a14000-4000-7000-8000-0000000000" + device + "/run_state"
			state := "STOPPED"
			if running && device == "05" {
				state = "RUNNING"
			}
			facts[key] = alarmservice.StringInput(key, state, "GOOD", observed)
		}
		return facts
	}
	type step struct {
		after   time.Duration
		running bool
		values  map[string]float64
		effect  alarmservice.EvaluationEffect
	}
	run := func(title string, steps []step) {
		t.Helper()
		policy := published(t, compiled, title)
		state := alarmservice.AlarmEvaluationState{}
		for index, current := range steps {
			at := start.Add(current.after)
			decision := evaluate(t, policy, fmt.Sprintf("%s-%d", title, index), plant(at, current.running, current.values), state, at)
			if decision.Effect != current.effect {
				t.Fatalf("%s step %d (%v): got %s, want %s", title, index, current.after, decision.Effect, current.effect)
			}
			state = decision.State
			if decision.Effect == alarmservice.EvaluationEffectPublish {
				state = active(state)
			}
		}
	}
	none, raise, clear := alarmservice.EvaluationEffectNone, alarmservice.EvaluationEffectPublish, alarmservice.EvaluationEffectClear

	// 冷冻水供水温度高于设定范围: 45 minutes above 8.5 ℃; a start-up pull-down shorter than that is not.
	supply := btu + "supply_water_temperature"
	run("冷冻水供水温度高于设定范围", []step{
		{0, true, map[string]float64{supply: 9.2}, none},
		{30 * time.Minute, true, map[string]float64{supply: 6}, none},
		{31 * time.Minute, true, map[string]float64{supply: 9}, none},
		{76 * time.Minute, true, map[string]float64{supply: 9}, raise},
		{77 * time.Minute, true, map[string]float64{supply: 7.9}, clear},
	})
	// 冷冻水供水温度过低: 15 minutes below 4 ℃; clears above 4.5 ℃ or when every chiller stops.
	run("冷冻水供水温度过低", []step{
		{0, true, map[string]float64{supply: 3.8}, none},
		{15 * time.Minute, true, map[string]float64{supply: 3.8}, raise},
		{16 * time.Minute, true, map[string]float64{supply: 4.3}, none},
		{17 * time.Minute, false, map[string]float64{supply: 4.3}, clear},
	})
	// 冷却水出水温度过高: only the running chiller's condenser counts.
	run("冷却水出水温度过高", []step{
		{0, true, map[string]float64{c + "cw_leaving_temperature": 37.5, d + "cw_leaving_temperature": 25}, none},
		{15 * time.Minute, true, map[string]float64{c + "cw_leaving_temperature": 37.5, d + "cw_leaving_temperature": 25}, raise},
		{16 * time.Minute, true, map[string]float64{c + "cw_leaving_temperature": 34.5, d + "cw_leaving_temperature": 25}, clear},
	})
	run("冷却水出水温度过高", []step{
		{0, true, map[string]float64{c + "cw_leaving_temperature": 30, d + "cw_leaving_temperature": 40}, none},
		{30 * time.Minute, true, map[string]float64{c + "cw_leaving_temperature": 30, d + "cw_leaving_temperature": 40}, none},
	})
	// 机组运行但冷冻水没有流量: 5 minutes under 100 m³/h with a chiller running.
	run("机组运行但冷冻水没有流量", []step{
		{0, true, map[string]float64{flow: 0}, none},
		{5 * time.Minute, true, map[string]float64{flow: 0}, raise},
		{6 * time.Minute, true, map[string]float64{flow: 150}, none},
		{7 * time.Minute, true, map[string]float64{flow: 489}, clear},
	})
}

func published(t *testing.T, compiled Compiled, title string) alarmservice.AlarmPolicyRevision {
	t.Helper()
	for _, sitePolicy := range compiled.Policies {
		if sitePolicy.Policy.Title == title {
			policy := sitePolicy.Policy
			policy.PolicyRevisionID, policy.Revision = "01a14000-7a00-7000-8000-000000000001", 1
			policy.Digest, _ = alarmservice.AlarmPolicyDigest(policy)
			return policy
		}
	}
	t.Fatalf("no rule %s", title)
	return alarmservice.AlarmPolicyRevision{}
}

func prefixed(deviceID string, inputs map[string]alarmservice.InputFact) map[string]alarmservice.InputFact {
	keyed := map[string]alarmservice.InputFact{}
	for key, fact := range inputs {
		fact.Key = deviceID + "/" + key
		keyed[fact.Key] = fact
	}
	return keyed
}

func evaluate(t *testing.T, policy alarmservice.AlarmPolicyRevision, revision string, inputs map[string]alarmservice.InputFact, previous alarmservice.AlarmEvaluationState, now time.Time) alarmservice.EvaluationDecision {
	t.Helper()
	const site = "01a14000-1000-7000-8000-000000000001"
	decision, err := alarmservice.EvaluatePolicy(policy, alarmservice.EvaluationSnapshot{
		TenantID: "018f3d00-0000-7000-8000-000000000001", SiteID: site, SubjectType: "SITE", SubjectID: site,
		InputRevision: revision, AsOf: now.Format(time.RFC3339Nano), Inputs: inputs, CorrelationID: revision,
	}, previous, now)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

// recovered is the state after the owner recorded the clear.
func recovered(state alarmservice.AlarmEvaluationState) alarmservice.AlarmEvaluationState {
	state.ActiveAlarmID, state.ActiveIncidentCorrelationID = "", ""
	return state
}

// active is the state after the owner recorded the raised alarm.
func active(state alarmservice.AlarmEvaluationState) alarmservice.AlarmEvaluationState {
	state.ActiveAlarmID = "01a14000-7a03-7000-8000-000000000001"
	state.ActiveIncidentCorrelationID = "01a14000-7a03-7000-8000-000000000002"
	return state
}
