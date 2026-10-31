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
// marks active; the cooling tower's probe is inactive (there is none), so no rule watches it.
func TestSongyaxuanRulesCompileToValidPoliciesOverActivePoints(t *testing.T) {
	compiled, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.TenantID != "018f3d00-0000-7000-8000-000000000001" || compiled.SiteID != "01a14000-1000-7000-8000-000000000001" {
		t.Fatalf("rules compiled for the wrong Site: %s %s", compiled.TenantID, compiled.SiteID)
	}

	byTitle := map[string]alarmservice.AlarmPolicyRevision{}
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
		byTitle[policy.Title] = policy
	}
	// The supply-temperature rule, the five plant rules added 2026-10-31 (供水偏高、供水过低、房间过热、
	// 冷却水出水过高、机组运行无流量), 网关上报中断, one 读数长时间没有更新 per device with an active
	// point (13, the cooling tower has none) and one 一动不动 per temperature a device reads itself
	// (16 channels of the temperature module: t8 has no probe since #614, t14 since 2026-10-29; the cooling meter's 2).
	if len(byTitle) != 37 {
		t.Fatalf("got %d rules: %v", len(byTitle), keys(byTitle))
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

	chillerC, ok := byTitle["C 机组电表读数长时间没有更新"]
	if !ok || chillerC.Severity != "WARNING" {
		t.Fatalf("no readings rule for C 机组电表: %v", keys(byTitle))
	}
	if got := inputs(chillerC.Raise); len(got) != 6 || !contains(got, "01a14000-4000-7000-8000-000000000005/cw_leaving_temperature") {
		t.Fatalf("C 机组电表 readings rule reads %v", got)
	}
	if !strings.Contains(chillerC.Summary, "超过 30 分钟没有更新") || !strings.Contains(chillerC.Summary, "冷却水出水温度") || strings.Contains(chillerC.Title+chillerC.Summary, "UID") {
		t.Fatalf("C 机组电表 readings rule is not said plainly: %q / %q", chillerC.Title, chillerC.Summary)
	}

	// #615: the Site has stopped reporting when none of its 67 active points has a reading for 15 minutes
	// (69 until 2026-10-29 withdrew probeless t14 and the outdoor copy of it; 78 until #614).
	silent, ok := byTitle["网关上报中断"]
	if !ok || silent.Severity != "MAJOR" || len(inputs(silent.Raise)) != 67 || len(inputs(silent.Clear)) != 67 {
		t.Fatalf("网关上报中断 rule: %#v", silent)
	}
	if !strings.Contains(silent.Summary, "15 分钟") {
		t.Fatalf("网关上报中断 is not said plainly: %q", silent.Summary)
	}

	// #616: the outdoor temperature on channel 13, 6 hours without the slightest change, a 提示. The
	// 室外环境 device only copies that channel, so it has no rule of its own: one stuck probe, one alarm.
	flat, ok := byTitle["室外温度（通道 13）一动不动"]
	if !ok || flat.Severity != "INFO" || !reflect.DeepEqual(inputs(flat.Raise), []string{"01a14000-4000-7000-8000-000000000001/t13"}) {
		t.Fatalf("channel 13 flat-reading rule: %#v / %v", flat, inputs(flat.Raise))
	}
	if !strings.Contains(flat.Summary, "温度采集模块的室外温度（通道 13）连续 6 小时") {
		t.Fatalf("channel 13 flat-reading rule is not said plainly: %q", flat.Summary)
	}
	for title, policy := range byTitle {
		if strings.HasSuffix(title, "一动不动") && strings.Contains(inputs(policy.Raise)[0], "-00000000000c/") {
			t.Fatalf("%s watches a copied reading", title)
		}
	}

	module := inputs(byTitle["温度采集模块读数长时间没有更新"].Raise)
	if len(module) != 16 || contains(module, "01a14000-4000-7000-8000-000000000001/t8") || contains(module, "01a14000-4000-7000-8000-000000000001/t11") || contains(module, "01a14000-4000-7000-8000-000000000001/t14") || contains(module, "01a14000-4000-7000-8000-000000000001/t20") {
		t.Fatalf("temperature module readings rule watches inactive channels: %v", module)
	}
	for title, policy := range byTitle {
		if strings.Contains(title, "冷却塔") || strings.Contains(strings.Join(inputs(policy.Raise), " "), "-00000000000a/") {
			t.Fatalf("%s watches the cooling tower, which has no probe", title)
		}
	}

	again, err := Load(songyaxuanRules)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiled, again) {
		t.Fatal("compiling the same file twice gave different policies or ids")
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
	for _, input := range []string{"cooling-tower/leaving_water_temperature", "no-such-device/run_state"} {
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

	// 读数长时间没有更新: raised while 冷却水出水温度 has no reading, cleared once every reading is fresh.
	chillerC := published(t, compiled, "C 机组电表读数长时间没有更新")
	meter := func(withLeavingWater bool) map[string]alarmservice.InputFact {
		inputs := map[string]alarmservice.InputFact{}
		for _, point := range []string{"power_total", "active_energy_total", "chw_leaving_temperature", "chw_entering_temperature", "cw_entering_temperature", "cw_leaving_temperature"} {
			inputs[point] = alarmservice.NumberInput(point, 20, "GOOD", start.Format(time.RFC3339Nano))
		}
		inputs["run_state"] = alarmservice.StringInput("run_state", "RUNNING", "GOOD", start.Format(time.RFC3339Nano))
		if !withLeavingWater {
			inputs["cw_leaving_temperature"] = alarmservice.InputFact{Key: "cw_leaving_temperature", Present: false}
		}
		return prefixed("01a14000-4000-7000-8000-000000000005", inputs)
	}
	raised := evaluate(t, chillerC, "c-1", meter(false), alarmservice.AlarmEvaluationState{}, start)
	if raised.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("a point with no reading did not raise: %#v", raised)
	}
	if cleared := evaluate(t, chillerC, "c-2", meter(true), active(raised.State), start.Add(time.Minute)); cleared.Effect != alarmservice.EvaluationEffectClear {
		t.Fatalf("fresh readings did not clear: %#v", cleared)
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
	// 一动不动 (#616): the outdoor temperature holding 21.5 ℃ for 6 hours raises; once it moves, it clears.
	flat := published(t, compiled, "室外温度（通道 13）一动不动")
	outdoor := func(at, temperatureSince time.Time, temperature float64) map[string]alarmservice.InputFact {
		key := "01a14000-4000-7000-8000-000000000001/t13"
		fact := alarmservice.NumberInput(key, temperature, "GOOD", at.Format(time.RFC3339Nano))
		fact.UnchangedSince = temperatureSince.Format(time.RFC3339Nano)
		return map[string]alarmservice.InputFact{key: fact}
	}
	held := start.Add(6 * time.Hour)
	stuck := evaluate(t, flat, "f-1", outdoor(held, start, 21.5), alarmservice.AlarmEvaluationState{}, held)
	if stuck.Effect != alarmservice.EvaluationEffectPublish {
		t.Fatalf("6 hours at 21.5 ℃ did not raise: %#v", stuck)
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
		room = "01a14000-4000-7000-8000-00000000000d/room_temperature"
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

	// 房间温度过高: 28.6 ℃ (the reading on 2026-10-31) for an hour with a chiller running raises; a
	// 176.8 ℃ spike is not a reading; 27.5 ℃ is not yet recovered, 26.8 ℃ is.
	run("房间温度过高", []step{
		{0, true, map[string]float64{room: 176.8}, none},
		{time.Minute, true, map[string]float64{room: 28.6}, none},
		{61 * time.Minute, true, map[string]float64{room: 28.6}, raise},
		{62 * time.Minute, true, map[string]float64{room: 27.5}, none},
		{63 * time.Minute, true, map[string]float64{room: 26.8}, clear},
	})
	// …and a warm room with every chiller stopped is not raised.
	run("房间温度过高", []step{
		{0, false, map[string]float64{room: 30}, none},
		{2 * time.Hour, false, map[string]float64{room: 30}, none},
	})
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
