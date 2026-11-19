package fdd

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type meterBalanceEvidence struct {
	loopEvidence
	hours       []MeterBalanceHour
	err         error
	lastTenant  string
	lastSite    string
	lastFrom    time.Time
	lastTo      time.Time
	queryCalled int
}

func (m *meterBalanceEvidence) MeterBalanceHours(_ context.Context, tenantID, siteID string, from, to time.Time) ([]MeterBalanceHour, error) {
	m.queryCalled++
	m.lastTenant = tenantID
	m.lastSite = siteID
	m.lastFrom = from
	m.lastTo = to
	if m.err != nil {
		return nil, m.err
	}
	return m.hours, nil
}

func ptrFloat64(v float64) *float64 { return &v }

func makeBalanceHour(hour time.Time, siteKWh, submeterSumKWh float64) MeterBalanceHour {
	diff := siteKWh - submeterSumKWh
	var ratio *float64
	status := "NO_MAIN_METER"
	if siteKWh > 0 {
		r := diff / siteKWh
		ratio = &r
		switch {
		case math.Abs(r) <= 0.05:
			status = "BALANCED"
		case r > 0.05:
			status = "UNMETERED_GAP"
		default:
			status = "SUBMETER_EXCEEDS_MAIN"
		}
	}
	return MeterBalanceHour{
		Hour:            hour.UTC(),
		SiteKWh:         siteKWh,
		SubmeterSumKWh:  submeterSumKWh,
		DiffKWh:         diff,
		ImbalanceRatio:  ratio,
		SubmeterCount:   3,
		ImbalanceStatus: status,
	}
}

func TestClickHouseEvidenceMeterBalanceHoursQueryAndDecoding(t *testing.T) {
	var capturedSQL string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		capturedSQL = string(body)
		if user, _, ok := request.BasicAuth(); !ok || user != "fdd_service_reader" {
			t.Errorf("reader account=%q", user)
		}
		_, _ = writer.Write([]byte(strings.Join([]string{
			`{"hour_utc":"2026-10-16T00:00:00Z","site_kwh":120.5,"submeter_sum_kwh":100.0,"diff_kwh":20.5,"imbalance_ratio":0.17012448,"submeter_count":"4","imbalance_status":"UNMETERED_GAP"}`,
			`{"hour_utc":"2026-10-16T01:00:00Z","site_kwh":0,"submeter_sum_kwh":12.0,"diff_kwh":-12.0,"imbalance_ratio":null,"submeter_count":2,"imbalance_status":"NO_MAIN_METER"}`,
		}, "\n")))
	}))
	defer server.Close()

	evidence, err := NewClickHouseEvidence(server.URL, "fdd_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	req := validEvaluation()
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	rows, err := evidence.MeterBalanceHours(t.Context(), req.TenantID, req.SiteID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].SubmeterCount != 4 || rows[0].ImbalanceStatus != "UNMETERED_GAP" || rows[0].ImbalanceRatio == nil || math.Abs(*rows[0].ImbalanceRatio-0.17012448) > 1e-6 {
		t.Fatalf("unexpected row 0: %+v", rows[0])
	}
	if rows[1].ImbalanceRatio != nil || rows[1].SubmeterCount != 2 || rows[1].ImbalanceStatus != "NO_MAIN_METER" {
		t.Fatalf("unexpected row 1: %+v", rows[1])
	}

	for _, want := range []string{
		"FROM analytics.site_meter_balance_hourly AS b",
		"b.tenant_id=toUUID('" + req.TenantID + "')",
		"b.site_id=toUUID('" + req.SiteID + "')",
		"parseDateTime64BestEffort('2026-10-16T00:00:00Z',3,'UTC')",
		"parseDateTime64BestEffort('2026-10-16T02:00:00Z',3,'UTC')",
		"formatDateTime(b.hour,'%Y-%m-%dT%H:%i:%SZ','UTC') AS hour_utc",
	} {
		if !strings.Contains(capturedSQL, want) {
			t.Fatalf("SQL missing %q:\n%s", want, capturedSQL)
		}
	}

	if _, err := evidence.MeterBalanceHours(t.Context(), "bad-uuid", req.SiteID, from, to); err == nil {
		t.Fatal("expected invalid tenant UUID error")
	}
	if _, err := evidence.MeterBalanceHours(t.Context(), req.TenantID, req.SiteID, to, from); err == nil {
		t.Fatal("expected invalid time window error")
	}
}

func TestEvaluateMeterImbalanceBalancedAndTransientSpikes(t *testing.T) {
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	req := validEvaluation()

	t.Run("balanced hours return CLEAR with no finding", func(t *testing.T) {
		store := newStore()
		ev := &meterBalanceEvidence{
			hours: []MeterBalanceHour{
				makeBalanceHour(base, 100, 97),
				makeBalanceHour(base.Add(time.Hour), 100, 103),
				makeBalanceHour(base.Add(2*time.Hour), 100, 95), // exact 5% boundary is balanced
			},
		}
		service := newTestService(t, store, ev, func() time.Time { return base.Add(3 * time.Hour) })
		res, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base,
			To:       base.Add(3 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != StatusClear || res.ImbalanceStatus != "BALANCED" || res.AffectedHours != 0 || res.Finding != nil || len(store.findings) != 0 {
			t.Fatalf("expected CLEAR without finding, got %+v findings=%d", res, len(store.findings))
		}
		if res.ThresholdRatio != 0.05 || math.Abs(res.WorstRatio-0.05) > 1e-9 || res.ObservedGapKWh != 5 {
			t.Fatalf("unexpected balanced metrics: %+v", res)
		}
	})

	t.Run("transient 1-hour spike and non-adjacent spikes are ignored", func(t *testing.T) {
		store := newStore()
		ev := &meterBalanceEvidence{
			hours: []MeterBalanceHour{
				makeBalanceHour(base, 100, 70),                  // 1h spike (+30%)
				makeBalanceHour(base.Add(time.Hour), 100, 98),   // balanced breaks run
				makeBalanceHour(base.Add(2*time.Hour), 100, 75), // 1h spike (+25%)
				makeBalanceHour(base.Add(3*time.Hour), 5, 1),    // below 10 kWh deadband
				makeBalanceHour(base.Add(4*time.Hour), 100, 60), // after low-load hour -> still 1h
				makeBalanceHour(base.Add(6*time.Hour), 100, 65), // missing hour at +5h -> breaks run
			},
		}
		service := newTestService(t, store, ev, func() time.Time { return base.Add(7 * time.Hour) })
		res, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base,
			To:       base.Add(7 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != StatusClear || res.AffectedHours != 0 || res.Finding != nil || len(store.findings) != 0 {
			t.Fatalf("transient spikes must not trigger finding: %+v", res)
		}
		if math.Abs(res.WorstRatio-0.40) > 1e-9 || res.ObservedGapKWh != 40 {
			t.Fatalf("expected overall worst 0.40 / 40 kWh recorded, got %+v", res)
		}
	})
}

func TestEvaluateMeterImbalanceSustainedTriggersAndRecurrenceSuppression(t *testing.T) {
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	req := validEvaluation()

	t.Run("sustained 2+ hours triggers UNMETERED_GAP finding and 24h recurrence suppresses duplicate", func(t *testing.T) {
		store := newStore()
		// Pass hours out of order to verify sorting before consecutive run detection.
		ev := &meterBalanceEvidence{
			hours: []MeterBalanceHour{
				makeBalanceHour(base.Add(2*time.Hour), 200, 140), // +30% (gap 60 kWh)
				makeBalanceHour(base, 100, 99),                   // balanced
				makeBalanceHour(base.Add(time.Hour), 100, 80),    // +20% (gap 20 kWh)
			},
		}
		now := base.Add(3 * time.Hour)
		service := newTestService(t, store, ev, func() time.Time { return now })

		first, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base,
			To:       base.Add(3 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if first.Status != "FINDING" || first.ImbalanceStatus != "UNMETERED_GAP" || first.AffectedHours != 2 || first.Finding == nil || len(store.findings) != 1 {
			t.Fatalf("expected UNMETERED_GAP finding, got %+v findings=%d", first, len(store.findings))
		}
		if math.Abs(first.WorstRatio-0.30) > 1e-9 || first.ObservedGapKWh != 60 || first.ThresholdRatio != 0.05 {
			t.Fatalf("unexpected finding metrics: %+v", first)
		}
		f := first.Finding
		if f.AssetID != req.SiteID || f.FindingType != MeterImbalanceRuleCode || f.RuleRevisionID != MeterBalanceRuleRevisionID || f.ValueUnit != "1" || len(f.EvidenceIDs) != 2 || f.Confidence <= 0.5 || f.Confidence > 1.0 {
			t.Fatalf("unexpected persisted FDDFinding: %+v", f)
		}

		// Re-evaluating within 24 hours returns ONGOING without inserting a second finding.
		now = base.Add(12 * time.Hour)
		second, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base.Add(9 * time.Hour),
			To:       base.Add(12 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if second.Status != "ONGOING" || second.ImbalanceStatus != "UNMETERED_GAP" || second.Finding == nil || second.Finding.ID != f.ID || len(store.findings) != 1 {
			t.Fatalf("expected ONGOING deduped finding within 24h, got %+v findings=%d", second, len(store.findings))
		}

		// Beyond 24 hours creates a fresh finding.
		now = base.Add(28 * time.Hour)
		ev.hours = []MeterBalanceHour{
			makeBalanceHour(base.Add(26*time.Hour), 150, 120),
			makeBalanceHour(base.Add(27*time.Hour), 150, 110),
		}
		third, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base.Add(25 * time.Hour),
			To:       base.Add(28 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if third.Status != "FINDING" || third.Finding == nil || third.Finding.ID == f.ID || len(store.findings) != 2 {
			t.Fatalf("expected new FINDING after 24h recurrence window, got %+v findings=%d", third, len(store.findings))
		}
	})

	t.Run("sustained negative imbalance triggers SUBMETER_EXCEEDS_MAIN", func(t *testing.T) {
		store := newStore()
		ev := &meterBalanceEvidence{
			hours: []MeterBalanceHour{
				makeBalanceHour(base, 100, 115),                // -15% (-15 kWh)
				makeBalanceHour(base.Add(time.Hour), 100, 125), // -25% (-25 kWh)
			},
		}
		service := newTestService(t, store, ev, func() time.Time { return base.Add(2 * time.Hour) })
		res, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base,
			To:       base.Add(2 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "FINDING" || res.ImbalanceStatus != "SUBMETER_EXCEEDS_MAIN" || res.AffectedHours != 2 || res.Finding == nil {
			t.Fatalf("expected SUBMETER_EXCEEDS_MAIN finding, got %+v", res)
		}
		if math.Abs(res.WorstRatio-(-0.25)) > 1e-9 || res.ObservedGapKWh != -25 {
			t.Fatalf("expected negative worst ratio -0.25 and gap -25, got %+v", res)
		}
		if res.Finding.ObservedValue != res.WorstRatio || res.Finding.ThresholdValue != 0.05 {
			t.Fatalf("unexpected finding measurement: %+v", res.Finding)
		}
	})

	t.Run("nil ratio and NaN/Inf hours break runs cleanly", func(t *testing.T) {
		store := newStore()
		ev := &meterBalanceEvidence{
			hours: []MeterBalanceHour{
				makeBalanceHour(base, 100, 70),
				{Hour: base.Add(time.Hour), SiteKWh: 100, ImbalanceRatio: nil},
				{Hour: base.Add(2 * time.Hour), SiteKWh: 100, ImbalanceRatio: ptrFloat64(math.NaN())},
				makeBalanceHour(base.Add(3*time.Hour), 100, 70),
			},
		}
		service := newTestService(t, store, ev, func() time.Time { return base.Add(4 * time.Hour) })
		res, err := service.EvaluateMeterImbalance(t.Context(), MeterImbalanceRequest{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			From:     base,
			To:       base.Add(4 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != StatusClear || res.AffectedHours != 0 || len(store.findings) != 0 {
			t.Fatalf("expected broken run to clear, got %+v", res)
		}
	})
}
