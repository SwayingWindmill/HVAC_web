package alarmrules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/rulegraph"
)

func TestLoadDependencyGateActionsInheritancePlaceholderAndPhysicalCycle(t *testing.T) {
	const (
		tenantID   = "018f3d00-0000-7000-8000-000000000001"
		siteID     = "01a15000-1000-7000-8000-000000000001"
		chillerID  = "01a15000-4000-7000-8000-000000000101"
		pumpID     = "01a15000-4000-7000-8000-000000000102"
		towerID    = "01a15000-4000-7000-8000-000000000103"
		valveDevID = "01a15000-4000-7000-8000-000000000104"
	)

	writeBundle := func(t *testing.T, name string, rulesDoc map[string]any) (Compiled, error) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		siteDoc := map[string]any{
			"tenantId": tenantID,
			"site":     map[string]any{"id": siteID, "code": "dag-site"},
			"devices": []any{
				map[string]any{
					"id": chillerID, "code": "chiller-1", "name": "1 号冷机（定频）",
					"points": []any{
						map[string]any{"code": "leaving_temp", "name": "出水温度", "valueType": "NUMBER"},
						map[string]any{"code": "cmd_stop", "name": "停机控制", "valueType": "STRING", "pointType": "COMMAND"},
					},
				},
				map[string]any{
					"id": pumpID, "code": "pump-1", "name": "1 号冷冻泵",
					"points": []any{
						map[string]any{"code": "dp", "name": "压差", "valueType": "NUMBER"},
					},
				},
				map[string]any{
					"id": towerID, "code": "tower-1", "name": "1 号冷却塔",
					"points": []any{
						map[string]any{"code": "fan_cmd", "name": "风机频率", "valueType": "NUMBER", "pointType": "COMMAND"},
					},
				},
				map[string]any{
					"id": valveDevID, "code": "valve-1", "name": "旁通阀",
					"points": []any{
						map[string]any{"code": "pos", "name": "开度", "valueType": "NUMBER"},
					},
				},
			},
		}
		siteBytes, err := json.Marshal(siteDoc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "site.json"), siteBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		rulesBytes, err := json.Marshal(rulesDoc)
		if err != nil {
			t.Fatal(err)
		}
		rulesPath := filepath.Join(dir, "rules.json")
		if err := os.WriteFile(rulesPath, rulesBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(rulesPath)
	}

	t.Run("inherits and overrides dependencyGate and actions with device placeholder substitution", func(t *testing.T) {
		compiled, err := writeBundle(t, "valid", map[string]any{
			"schemaVersion": 1,
			"siteFile":      "site.json",
			"templates": []any{
				map[string]any{
					"key":              "base-protection",
					"severity":         "MAJOR",
					"qualityPolicy":    "VALID_ONLY",
					"freshnessSeconds": 900,
					"triggerMode":      "SIMPLE",
					"dependencyGate":   "NONE",
					"actions": []any{
						map[string]any{
							"kind":              "WORK_ORDER",
							"triggerOn":         "RAISE",
							"workOrderPriority": "MEDIUM",
							"workOrderTitle":    "检查 {device}",
						},
					},
				},
			},
			"rules": []any{
				map[string]any{
					"code":             "pump-low-dp",
					"title":            "冷冻泵压差低",
					"summary":          "冷冻泵压差过低",
					"severity":         "WARNING",
					"qualityPolicy":    "VALID_ONLY",
					"freshnessSeconds": 900,
					"triggerMode":      "SIMPLE",
					"raise":            map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "LT", "value": map[string]any{"type": "NUMBER", "number": 30}},
					"clear":            map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "GTE", "value": map[string]any{"type": "NUMBER", "number": 40}},
				},
				map[string]any{
					"code":           "chiller-protect",
					"template":       "base-protection",
					"device":         "chiller-1",
					"dependsOn":      []string{"pump-low-dp"},
					"dependencyGate": "ALL_ACTIVE",
					"title":          "{device} 断流保护",
					"summary":        "{device} 出水低温联动保护",
					"raise":          map[string]any{"kind": "COMPARE", "input": "{device}/leaving_temp", "operator": "LT", "value": map[string]any{"type": "NUMBER", "number": 4.5}},
					"clear":          map[string]any{"kind": "COMPARE", "input": "{device}/leaving_temp", "operator": "GTE", "value": map[string]any{"type": "NUMBER", "number": 6.0}},
					"actions": []any{
						map[string]any{
							"kind":              "WORK_ORDER",
							"triggerOn":         "RAISE",
							"workOrderPriority": "HIGH",
							"workOrderTitle":    "紧急巡检 {device}",
						},
						map[string]any{
							"kind":            "PROTECTION_COMMAND",
							"triggerOn":       "RAISE",
							"targetDeviceId":  "tower-1",
							"targetPointCode": "fan_cmd",
							"capability":      "SET_FREQUENCY",
							"parameters":      map[string]float64{"frequencyHz": 35},
						},
						map[string]any{
							"kind":       "WEBHOOK",
							"triggerOn":  "BOTH",
							"webhookUrl": "https://hooks.example.com/alarm",
							"secretRef":  "webhook-hmac:v1",
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("expected valid DAG rules to compile, got %v", err)
		}
		var downstream alarmservice.SiteRule
		for _, item := range compiled.Policies {
			if item.Policy.AlarmType == "chiller-protect" {
				downstream = item
			}
		}
		if downstream.Policy.DependencyGate != alarmservice.DependencyGateAllActive {
			t.Fatalf("unexpected dependencyGate: %q", downstream.Policy.DependencyGate)
		}
		if !slices.Contains(downstream.Policy.OverriddenFields, "dependencyGate") || !slices.Contains(downstream.Policy.OverriddenFields, "actions") {
			t.Fatalf("expected overriddenFields to track dependencyGate and actions, got %v", downstream.Policy.OverriddenFields)
		}
		if len(downstream.Policy.Actions) != 3 {
			t.Fatalf("expected 3 compiled actions, got %d", len(downstream.Policy.Actions))
		}
		if downstream.Policy.Actions[0].WorkOrderTitle != "紧急巡检 1 号冷机" {
			t.Fatalf("expected placeholder expansion in workOrderTitle, got %q", downstream.Policy.Actions[0].WorkOrderTitle)
		}
		if downstream.Policy.Actions[1].TargetDeviceID != towerID {
			t.Fatalf("expected tower-1 code to resolve to UUIDv7 %s, got %q", towerID, downstream.Policy.Actions[1].TargetDeviceID)
		}
	})

	t.Run("rejects dependencyGate without dependsOn", func(t *testing.T) {
		_, err := writeBundle(t, "gate-no-deps", map[string]any{
			"schemaVersion": 1,
			"siteFile":      "site.json",
			"rules": []any{
				map[string]any{
					"code": "r1", "title": "t", "summary": "s", "severity": "WARNING",
					"qualityPolicy": "VALID_ONLY", "freshnessSeconds": 300, "triggerMode": "SIMPLE",
					"dependencyGate": "ANY_ACTIVE",
					"raise":          map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "LT", "value": map[string]any{"type": "NUMBER", "number": 10}},
					"clear":          map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "GTE", "value": map[string]any{"type": "NUMBER", "number": 20}},
				},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "requires non-empty dependsOn") {
			t.Fatalf("expected dependencyGate error without dependsOn, got %v", err)
		}
	})

	t.Run("rejects closed physical control write to sensor read cycle", func(t *testing.T) {
		_, err := writeBundle(t, "physical-cycle", map[string]any{
			"schemaVersion": 1,
			"siteFile":      "site.json",
			"rules": []any{
				map[string]any{
					"code": "r-pump", "title": "t1", "summary": "s1", "severity": "MAJOR",
					"qualityPolicy": "VALID_ONLY", "freshnessSeconds": 300, "triggerMode": "SIMPLE",
					"raise": map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "LT", "value": map[string]any{"type": "NUMBER", "number": 10}},
					"clear": map[string]any{"kind": "COMPARE", "input": "pump-1/dp", "operator": "GTE", "value": map[string]any{"type": "NUMBER", "number": 20}},
					"actions": []any{
						map[string]any{
							"kind": "PROTECTION_COMMAND", "targetDeviceId": "valve-1", "capability": "SET_OPENING",
							"parameters": map[string]float64{"openingPct": 100},
						},
					},
				},
				map[string]any{
					"code": "r-valve", "title": "t2", "summary": "s2", "severity": "MAJOR",
					"qualityPolicy": "VALID_ONLY", "freshnessSeconds": 300, "triggerMode": "SIMPLE",
					"raise": map[string]any{"kind": "COMPARE", "input": "valve-1/pos", "operator": "GT", "value": map[string]any{"type": "NUMBER", "number": 90}},
					"clear": map[string]any{"kind": "COMPARE", "input": "valve-1/pos", "operator": "LTE", "value": map[string]any{"type": "NUMBER", "number": 50}},
					"actions": []any{
						map[string]any{
							"kind": "PROTECTION_COMMAND", "targetDeviceId": "pump-1", "capability": "STOP",
						},
					},
				},
			},
		})
		if !errors.Is(err, rulegraph.ErrRuleCycleDetected) {
			t.Fatalf("expected ErrRuleCycleDetected for physical write-to-read feedback loop, got %v", err)
		}
	})
}
