package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceHasherIsStableAndDomainSeparated(t *testing.T) {
	hasher, err := NewReferenceHasher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	requestOne, _ := hasher.Reference("request", "raw-identifier")
	requestTwo, _ := hasher.Reference("request", "raw-identifier")
	event, _ := hasher.Reference("event", "raw-identifier")
	if requestOne != requestTwo || requestOne == event || !IsHMACReference(requestOne) {
		t.Fatalf("unexpected references: %q %q %q", requestOne, requestTwo, event)
	}
	if strings.Contains(requestOne, "raw-identifier") {
		t.Fatal("reference leaked raw identifier")
	}
}

func TestOperationalRecordRejectsRawSensitiveFields(t *testing.T) {
	if err := ValidateOperationalRecord(map[string]any{"subscription_id": "raw-subscription"}); err == nil {
		t.Fatal("raw subscription identifier was accepted")
	}
	hasher, _ := NewReferenceHasher([]byte(strings.Repeat("x", 32)))
	fields, err := HMACOperationalReferences(hasher, map[string]string{"subscription": "raw-subscription", "revision": "42"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalRecord(fields); err != nil {
		t.Fatal(err)
	}
}

func TestS2MetricCatalogRejectsHighCardinalityAndBadUnits(t *testing.T) {
	catalog := S2MetricCatalog{
		SchemaVersion: 1, Namespace: "hvac_s2",
		LabelPolicy:  S2MetricLabelPolicy{Allowed: []string{"outcome"}, Forbidden: []string{"device_id"}},
		SampleValues: map[string][]string{"outcome": {"success", "rejected"}},
		Families:     []S2MetricDefinition{{Name: "hvac_s2_snapshot_requests_total", Type: "counter", Unit: "requests", Labels: []string{"outcome"}, SeriesBudget: 4}},
	}
	report, err := ValidateS2MetricCatalog(catalog)
	if err != nil || len(report) != 1 || !report[0].WithinBudget {
		t.Fatalf("catalog failed: %#v %v", report, err)
	}
	catalog.Families[0].Labels = []string{"device_id"}
	if _, err := ValidateS2MetricCatalog(catalog); err == nil {
		t.Fatal("high-cardinality device label was accepted")
	}
	catalog.Families[0].Labels = []string{"outcome"}
	catalog.Families[0].Name = "hvac_s2_snapshot_duration"
	catalog.Families[0].Type = "histogram"
	if _, err := ValidateS2MetricCatalog(catalog); err == nil {
		t.Fatal("metric without base-unit suffix was accepted")
	}
}

func TestRepositoryMetricCatalog(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	payload, err := os.ReadFile(filepath.Join(root, "deploy", "s2", "observability", "metric-catalog.v1.json"))
	if err != nil {
		t.Skipf("repository catalog is not available from this test context: %v", err)
	}
	var catalog S2MetricCatalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		t.Fatal(err)
	}
	report, err := ValidateS2MetricCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != len(catalog.Families) {
		t.Fatalf("report length %d != family length %d", len(report), len(catalog.Families))
	}
}
