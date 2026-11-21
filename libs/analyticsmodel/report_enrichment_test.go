package analyticsmodel

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestReportEnrichmentJSONAndPresenceHelpers(t *testing.T) {
	nan := math.NaN()
	if (ReportConditionAnchorsSummary{MeanWetBulbC: &nan}).HasValues() {
		t.Fatal("expected NaN-only anchors summary not to report finite values")
	}
	if (LayeredEfficiency{SynthesizedTotal: true}).HasMetrics() {
		t.Fatal("expected empty LayeredEfficiency not to report metrics")
	}

	plantCOP := 4.8
	chillerCOP := 6.2
	wetBulb := 22.5
	loadRatio := 0.72
	aiShare := 0.85
	balance := ComputeMeterBalance(1000, 960, 0.05, false)
	payload := ReportEnrichmentResponse{
		LayeredEfficiency: &LayeredEfficiency{
			PlantCOP:   &plantCOP,
			ChillerCOP: &chillerCOP,
		},
		MeterBalance: &balance,
		ConditionAnchors: &ReportConditionAnchorsSummary{
			MeanWetBulbC:      &wetBulb,
			MeanLoadRatio:     &loadRatio,
			AIClosedLoopShare: &aiShare,
		},
	}
	if !payload.LayeredEfficiency.HasMetrics() {
		t.Fatal("expected LayeredEfficiency.HasMetrics() = true")
	}
	if !payload.ConditionAnchors.HasValues() {
		t.Fatal("expected ReportConditionAnchorsSummary.HasValues() = true")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, key := range []string{`"layeredEfficiency":`, `"meterBalance":`, `"conditionAnchors":`, `"meanWetBulbC":22.5`, `"aiClosedLoopShare":0.85`} {
		if !strings.Contains(encoded, key) {
			t.Fatalf("missing JSON key %s in %s", key, encoded)
		}
	}

	var decoded ReportEnrichmentResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LayeredEfficiency == nil || decoded.MeterBalance == nil || decoded.ConditionAnchors == nil {
		t.Fatalf("decoded missing sections: %#v", decoded)
	}
	if decoded.MeterBalance.Status != MeterBalanceBalanced || decoded.ConditionAnchors.MeanDryBulbC != nil {
		t.Fatalf("unexpected decoded payload: %#v", decoded)
	}
}
