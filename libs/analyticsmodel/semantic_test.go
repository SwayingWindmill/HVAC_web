package analyticsmodel

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func semanticQuery() SemanticQuery {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	return SemanticQuery{
		TenantID: "018f1d00-0000-7000-8000-000000000001", SiteID: "018f1e00-1000-7000-8000-000000000001",
		View: "site_energy", Measures: []string{"electricity_valid_kwh", "cooling_valid_kwh", "cop"},
		Granularity: GranularityDay, Timezone: "Asia/Shanghai",
		From: time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai), To: time.Date(2026, 10, 8, 0, 0, 0, 0, shanghai),
	}
}

// A question may take several measures of one view, and group them by its listed dimensions.
func TestSemanticQueryTakesTheViewsListedMembers(t *testing.T) {
	query := semanticQuery()
	if err := query.Validate(); err != nil {
		t.Fatal(err)
	}
	cost := query
	cost.View, cost.Measures, cost.Dimensions = "site_energy_cost", []string{"cost_valid_cny", "priced_valid_kwh"}, []string{"tariff_period"}
	if err := cost.Validate(); err != nil {
		t.Fatal(err)
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	other := query
	other.Measures = []string{"electricity_valid_kwh", "cooling_valid_kwh"}
	if otherDigest, _ := other.ScopeDigest(); otherDigest == digest || len(digest) != 64 {
		t.Fatalf("digests %q and %q", digest, otherDigest)
	}
}

// Only what the catalog lists can be asked for: the Site comes from the grant, time from the
// granularity, and the internal cubes are not a surface at all.
func TestSemanticQueryRefusesMembersTheCatalogDoesNotList(t *testing.T) {
	for name, change := range map[string]func(*SemanticQuery){
		"an internal cube":         func(query *SemanticQuery) { query.View = "site_energy_facts" },
		"a member of another view": func(query *SemanticQuery) { query.Measures = []string{"cost_valid_cny"} },
		"an unknown measure":       func(query *SemanticQuery) { query.Measures = []string{"electricity_kwh"} },
		"a dimension as a measure": func(query *SemanticQuery) { query.Measures = []string{"energy_type"} },
		"the Site as a dimension":  func(query *SemanticQuery) { query.Dimensions = []string{"site_id"} },
		"time as a dimension":      func(query *SemanticQuery) { query.Dimensions = []string{"period_end"} },
		"no measure":               func(query *SemanticQuery) { query.Measures = nil },
		"a measure twice":          func(query *SemanticQuery) { query.Measures = []string{"cop", "cop"} },
		"five measures": func(query *SemanticQuery) {
			query.Measures = []string{"cop", "electricity_valid_kwh", "cooling_valid_kwh", "valid_count", "suspect_count"}
		},
		"three dimensions": func(query *SemanticQuery) {
			query.View, query.Measures, query.Dimensions = "equipment_energy", []string{"energy_valid_kwh"}, []string{"equipment_id", "energy_type", "quality"}
		},
		"a Site that is not a UUIDv7": func(query *SemanticQuery) { query.SiteID = "site" },
	} {
		query := semanticQuery()
		change(&query)
		if err := query.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A day of hours is fine; more than 744 time buckets is more than one answer can carry.
func TestSemanticQueryKeepsWithinTheBucketLimit(t *testing.T) {
	query := semanticQuery()
	query.Granularity = GranularityHour
	query.To = query.From.AddDate(0, 0, 31)
	if err := query.Validate(); err != nil {
		t.Fatalf("31 days by hour: %v", err)
	}
	query.To = query.From.AddDate(0, 0, 32)
	if err := query.Validate(); err == nil || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("32 days by hour: %v", err)
	}
}

// The catalog is what the AI 助手 reads: figures an operator asks for, described in their words and
// with the values the Tool hands back (#590): no 区间/口径, no English codes.
func TestSemanticCatalogSpeaksTheOperatorsWords(t *testing.T) {
	english := regexp.MustCompile(`\b[A-Z][A-Z_]{2,}\b|electricity|cooling`)
	for _, view := range Catalog().Views {
		for _, member := range append(append([]SemanticMember{}, view.Measures...), view.Dimensions...) {
			for _, code := range english.FindAllString(member.Description, -1) {
				if code != "COP" {
					t.Errorf("%s.%s describes English value %s: %s", view.Name, member.Name, code, member.Description)
				}
			}
			for _, word := range []string{"区间", "口径"} {
				if strings.Contains(member.Title+member.Description, word) {
					t.Errorf("%s.%s says %s: %s %s", view.Name, member.Name, word, member.Title, member.Description)
				}
			}
		}
		if strings.Contains(view.Description, "区间") {
			t.Errorf("view %s says 区间", view.Name)
		}
	}
}
