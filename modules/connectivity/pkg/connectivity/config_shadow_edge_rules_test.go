package connectivity

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func sampleValidEdgeRuleBundle() *edgecontrol.EdgeRuleBundle {
	trig := 45.0
	rst := 43.0
	bundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  edgecontrol.EdgeRuleSchemaVersion,
		SiteID:         "0194f000-2000-7000-8000-000000000001",
		BundleRevision: 3,
		BundleDigest:   strings.Repeat("a", 64),
		CompiledAt:     "2026-11-22T00:00:00Z",
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0194f000-4000-7000-8000-000000000001",
			AlarmType:        "CHILLER_HIGH_TEMP",
			Title:            "Chiller High Supply Temp",
			Severity:         "CRITICAL",
			ExecutionTarget:  edgecontrol.EdgeTargetCloudAndEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 30,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:      edgecontrol.EdgeConditionHysteresis,
				Input:     "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Direction: edgecontrol.EdgeDirectionAbove,
				Trigger:   &trig,
				Reset:     &rst,
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    "0194f000-5000-7000-8000-000000000001/chilled_water_supply_temperature",
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: 43},
			},
		}},
	}
	digest, err := bundle.ComputeDigest()
	if err != nil {
		panic(err)
	}
	bundle.EdgeBundleDigest = digest
	return bundle
}

func TestConfigShadowEdgeRuleBundleValidationDriftAndTriState(t *testing.T) {
	t.Parallel()

	valid := sampleValidEdgeRuleBundle()
	cleaned, err := validateEdgeRuleBundle(valid)
	if err != nil || cleaned == nil || cleaned == valid {
		t.Fatalf("validateEdgeRuleBundle() = %+v, err=%v", cleaned, err)
	}

	tampered := valid.Clone()
	tampered.EdgeBundleDigest = strings.Repeat("f", 64)
	if _, err := validateEdgeRuleBundle(tampered); !errors.Is(err, ErrConfigShadowInvalid) {
		t.Fatalf("expected ErrConfigShadowInvalid for digest mismatch, got %v", err)
	}

	if d := ComputeConfigShadowDrift(
		&GatewayDesiredConfig{EdgeRuleBundle: valid},
		&GatewayReportedConfig{EdgeRuleBundle: valid.Clone()},
	); len(d) != 0 {
		t.Fatalf("expected zero drift when edgeRuleBundle matches, got %+v", d)
	}

	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{EdgeRuleBundle: valid}, &GatewayReportedConfig{}); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Target != "edgeRuleBundle" || d[0].Reason != DriftReasonMissingOnEdge {
		t.Fatalf("unexpected missing drift: %+v", d)
	}
	if d := ComputeConfigShadowDrift(&GatewayDesiredConfig{}, &GatewayReportedConfig{EdgeRuleBundle: valid}); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Target != "edgeRuleBundle" || d[0].Reason != DriftReasonUnexpectedOnEdge {
		t.Fatalf("unexpected unexpected drift: %+v", d)
	}

	mismatched := valid.Clone()
	mismatched.BundleRevision = 4
	d2, _ := mismatched.ComputeDigest()
	mismatched.EdgeBundleDigest = d2
	if d := ComputeConfigShadowDrift(
		&GatewayDesiredConfig{EdgeRuleBundle: valid},
		&GatewayReportedConfig{EdgeRuleBundle: mismatched},
	); len(d) != 1 || d[0].Section != DriftSectionEdgeRules || d[0].Reason != DriftReasonValueMismatch || !strings.Contains(d[0].Detail, valid.EdgeBundleDigest[:12]) || !strings.Contains(d[0].Detail, d2[:12]) {
		t.Fatalf("unexpected mismatch drift: %+v", d)
	}

	if _, _, err := validateReportDriftDetails([]DriftDetail{{
		Section: DriftSectionEdgeRules,
		Target:  "edgeRuleBundle",
		Reason:  DriftReasonEdgeApplyFailed,
		Detail:  "bundle apply failed",
	}}); err != nil {
		t.Fatalf("expected EDGE_RULES drift section allowed, got %v", err)
	}

	var omitted UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"reason":"omit"}`), &omitted); err != nil || omitted.EdgeRuleBundle.IsSet() || !omitted.EdgeRuleBundle.IsZero() {
		t.Fatalf("expected omitted edgeRuleBundle, got %+v err=%v", omitted.EdgeRuleBundle, err)
	}
	omittedBytes, _ := json.Marshal(omitted)
	if strings.Contains(string(omittedBytes), "edgeRuleBundle") {
		t.Fatalf("omitted edgeRuleBundle must not marshal key, got %s", string(omittedBytes))
	}

	var cleared UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"edgeRuleBundle":null,"reason":"clear"}`), &cleared); err != nil || !cleared.EdgeRuleBundle.IsSet() || cleared.EdgeRuleBundle.Value != nil || cleared.EdgeRuleBundle.IsZero() {
		t.Fatalf("expected explicit null edgeRuleBundle, got %+v err=%v", cleared.EdgeRuleBundle, err)
	}
	clearedBytes, _ := json.Marshal(cleared)
	if !strings.Contains(string(clearedBytes), `"edgeRuleBundle":null`) {
		t.Fatalf("explicit null edgeRuleBundle must marshal null, got %s", string(clearedBytes))
	}

	payload, _ := json.Marshal(map[string]any{"edgeRuleBundle": valid, "reason": "set"})
	var replaced UpdateDesiredConfigInput
	if err := json.Unmarshal(payload, &replaced); err != nil || !replaced.EdgeRuleBundle.IsSet() || replaced.EdgeRuleBundle.Value == nil || replaced.EdgeRuleBundle.Value.EdgeBundleDigest != valid.EdgeBundleDigest {
		t.Fatalf("expected object edgeRuleBundle, got %+v err=%v", replaced.EdgeRuleBundle, err)
	}

	var unknownField UpdateDesiredConfigInput
	if err := json.Unmarshal([]byte(`{"edgeRuleBundle":{"schemaVersion":1,"extraField":1},"reason":"bad"}`), &unknownField); err == nil {
		t.Fatalf("expected unknown field inside edgeRuleBundle to be rejected")
	}
}
