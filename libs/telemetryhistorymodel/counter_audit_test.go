package telemetryhistorymodel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	testAuditTenantID = "018f3e00-1000-7000-8000-000000000001"
	testAuditSiteID   = "018f3e00-2000-7000-8000-000000000002"
	testAuditDeviceID = "018f3e00-3000-7000-8000-000000000003"
	testAuditPointID  = "018f3e00-4000-7000-8000-000000000004"
)

func TestCounterAuditQueryCanonicalAndScopeDigest(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	deviceID := testAuditDeviceID
	pointID := testAuditPointID
	anomaliesOnly := false

	query := CounterAuditQuery{
		TenantID:      testAuditTenantID,
		SiteID:        testAuditSiteID,
		From:          from,
		To:            to,
		DeviceID:      &deviceID,
		PointID:       &pointID,
		AnomaliesOnly: &anomaliesOnly,
	}

	canonical, err := query.Canonical()
	if err != nil {
		t.Fatalf("expected valid canonical query: %v", err)
	}
	if canonical.Limit != DefaultCounterAuditLimit {
		t.Fatalf("expected default limit %d, got %d", DefaultCounterAuditLimit, canonical.Limit)
	}
	if canonical.DeviceID == &deviceID || canonical.PointID == &pointID || canonical.AnomaliesOnly == &anomaliesOnly {
		t.Fatal("expected canonical query pointers to be defensively copied")
	}

	digestA, err := query.ScopeDigest()
	if err != nil {
		t.Fatalf("expected scope digest: %v", err)
	}
	if !strings.HasPrefix(digestA, "counter-audit:") || len(digestA) != len("counter-audit:")+64 {
		t.Fatalf("unexpected scope digest format: %q", digestA)
	}

	query.Limit = DefaultCounterAuditLimit
	digestB, err := query.ScopeDigest()
	if err != nil {
		t.Fatalf("expected scope digest with explicit default limit: %v", err)
	}
	if digestA != digestB {
		t.Fatalf("expected canonical scope digest to match explicit default limit: %q != %q", digestA, digestB)
	}
}

func TestCounterAuditQueryValidationRejectsInvalidInputs(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	nonUTC := time.FixedZone("UTC+8", 8*3600)
	badUUID := "not-a-uuidv7"

	cases := []struct {
		name  string
		query CounterAuditQuery
	}{
		{
			name: "invalid tenant id",
			query: CounterAuditQuery{
				TenantID: "018f3e00-1000-4000-8000-000000000001",
				SiteID:   testAuditSiteID,
				From:     from,
				To:       to,
			},
		},
		{
			name: "non utc timestamp",
			query: CounterAuditQuery{
				TenantID: testAuditTenantID,
				SiteID:   testAuditSiteID,
				From:     time.Date(2026, 10, 1, 8, 0, 0, 0, nonUTC),
				To:       to,
			},
		},
		{
			name: "range exceeds 31 days",
			query: CounterAuditQuery{
				TenantID: testAuditTenantID,
				SiteID:   testAuditSiteID,
				From:     from,
				To:       from.Add(MaximumCounterAuditRange + time.Second),
			},
		},
		{
			name: "limit exceeds max",
			query: CounterAuditQuery{
				TenantID: testAuditTenantID,
				SiteID:   testAuditSiteID,
				From:     from,
				To:       to,
				Limit:    MaximumCounterAuditLimit + 1,
			},
		},
		{
			name: "invalid device id",
			query: CounterAuditQuery{
				TenantID: testAuditTenantID,
				SiteID:   testAuditSiteID,
				From:     from,
				To:       to,
				DeviceID: &badUUID,
			},
		},
		{
			name: "invalid point id",
			query: CounterAuditQuery{
				TenantID: testAuditTenantID,
				SiteID:   testAuditSiteID,
				From:     from,
				To:       to,
				PointID:  &badUUID,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.query.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestCounterAuditResponseValidateFor(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	query := CounterAuditQuery{
		TenantID: testAuditTenantID,
		SiteID:   testAuditSiteID,
		From:     from,
		To:       to,
		Limit:    10,
	}
	canonical, err := query.Canonical()
	if err != nil {
		t.Fatalf("unexpected canonical error: %v", err)
	}

	prev := 100.0
	resp := CounterAuditResponse{
		SiteID: testAuditSiteID,
		From:   from,
		To:     to,
		Summary: CounterAuditSummary{
			TotalSamples:  2,
			AnomalyCount:  1,
			ZeroDropCount: 1,
		},
		Items: []CounterAuditItem{
			{
				PointID:         testAuditPointID,
				DeviceID:        testAuditDeviceID,
				TelemetryKey:    "energy_total_kwh",
				PeriodStart:     from.Add(5 * time.Minute),
				PeriodEnd:       from.Add(10 * time.Minute),
				RawPrevious:     &prev,
				RawCurrent:      0,
				CleanedDeltaKWh: nil,
				TransitionType:  "INVALID_ZERO_DROP",
				Quality:         string(QualityInvalid),
				QualityReasons:  []string{"COUNTER_INVALID_ZERO_DROP"},
				Unit:            "kWh",
			},
		},
		Truncated: false,
	}
	if err := resp.ValidateFor(canonical); err != nil {
		t.Fatalf("expected valid response: %v", err)
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(encoded), `"cleanedDeltaKWh":null`) {
		t.Fatalf("expected nullable cleanedDeltaKWh field in JSON: %s", string(encoded))
	}

	respNilItems := resp
	respNilItems.Items = nil
	if err := respNilItems.ValidateFor(canonical); err == nil {
		t.Fatal("expected error when items slice is nil")
	}

	respNilReasons := resp
	respNilReasons.Items = []CounterAuditItem{resp.Items[0]}
	respNilReasons.Items[0].QualityReasons = nil
	if err := respNilReasons.ValidateFor(canonical); err == nil {
		t.Fatal("expected error when qualityReasons slice is nil")
	}

	respMismatchSite := resp
	respMismatchSite.SiteID = testAuditTenantID
	if err := respMismatchSite.ValidateFor(canonical); err == nil {
		t.Fatal("expected error when siteId mismatches")
	}
}
