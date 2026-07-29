package analyticsmodel

import (
	"testing"
	"time"
)

const (
	testOrganizationID = "018f1e00-0000-7000-8000-000000000001"
	testSiteID         = "018f1e00-1000-7000-8000-000000000001"
)

func TestEnergySeriesQueryValidatesProductBoundary(t *testing.T) {
	query := EnergySeriesQuery{
		OrganizationID: testOrganizationID,
		SiteID:         testSiteID,
		EnergyType:     EnergyTypeElectricity,
		Granularity:    GranularityDay,
		Timezone:       "Asia/Shanghai",
		From:           time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		QualityPolicy:  QualityPolicyValidAndSuspect,
	}
	if err := query.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnergySeriesQueryRejectsUnsafeOrAmbiguousRequests(t *testing.T) {
	valid := EnergySeriesQuery{
		OrganizationID: testOrganizationID,
		SiteID:         testSiteID,
		EnergyType:     EnergyTypeElectricity,
		Granularity:    GranularityDay,
		Timezone:       "Asia/Shanghai",
		From:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		QualityPolicy:  QualityPolicyValidOnly,
	}
	tests := []struct {
		name   string
		mutate func(*EnergySeriesQuery)
	}{
		{"organization is not UUIDv7", func(query *EnergySeriesQuery) { query.OrganizationID = "org-1" }},
		{"site is not UUIDv7", func(query *EnergySeriesQuery) { query.SiteID = "site-1" }},
		{"unsupported energy type", func(query *EnergySeriesQuery) { query.EnergyType = "steam" }},
		{"unsupported granularity", func(query *EnergySeriesQuery) { query.Granularity = "minute" }},
		{"timezone is invalid", func(query *EnergySeriesQuery) { query.Timezone = "Local" }},
		{"range is reversed", func(query *EnergySeriesQuery) { query.To = query.From }},
		{"range exceeds budget", func(query *EnergySeriesQuery) { query.To = query.From.Add(367 * 24 * time.Hour) }},
		{"quality policy is implicit", func(query *EnergySeriesQuery) { query.QualityPolicy = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := valid
			test.mutate(&query)
			if err := query.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestEnergySeriesScopeDigestBindsOrganizationSiteAndQueryKind(t *testing.T) {
	query := EnergySeriesQuery{
		OrganizationID: testOrganizationID,
		SiteID:         testSiteID,
		EnergyType:     EnergyTypeElectricity,
		Granularity:    GranularityHour,
		Timezone:       "Asia/Shanghai",
		From:           time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		QualityPolicy:  QualityPolicyValidOnly,
	}
	digest, err := query.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest length = %d", len(digest))
	}
	other := query
	other.SiteID = "018f1e00-1000-7000-8000-000000000002"
	otherDigest, err := other.ScopeDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest == otherDigest {
		t.Fatal("scope digest did not bind site")
	}
}
