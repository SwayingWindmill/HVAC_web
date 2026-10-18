package alarmrules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	// The supply-temperature rule and one per device with an active point: 13 devices, the cooling
	// tower has none.
	if len(byTitle) != 13 {
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
	if got := inputs(chillerC.Raise); len(got) != 7 || !contains(got, "01a14000-4000-7000-8000-000000000005/cw_leaving_temperature") {
		t.Fatalf("C 机组电表 readings rule reads %v", got)
	}
	if !strings.Contains(chillerC.Summary, "冷却水出水温度") || strings.Contains(chillerC.Title+chillerC.Summary, "UID") {
		t.Fatalf("C 机组电表 readings rule is not said plainly: %q / %q", chillerC.Title, chillerC.Summary)
	}

	module := inputs(byTitle["温度采集模块读数长时间没有更新"].Raise)
	if len(module) != 18 || contains(module, "01a14000-4000-7000-8000-000000000001/t11") || contains(module, "01a14000-4000-7000-8000-000000000001/t20") {
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
	for _, input := range []string{"meter-uid5/no_such_point", "cooling-tower/leaving_water_temperature", "no-such-device/run_state"} {
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
