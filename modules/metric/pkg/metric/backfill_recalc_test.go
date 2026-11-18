package metric

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeBackfillObservationSource struct {
	maxSeq         uint64
	maxSeqErr      error
	buckets        []BackfillDirtyBucket
	listErr        error
	atSeqCalls     int
	lastAfterSeq   uint64
	lastThroughSeq uint64
	lastMinLagSec  int64
}

func (f *fakeBackfillObservationSource) MaxHistorySequence(_ context.Context) (uint64, error) {
	if f.maxSeqErr != nil {
		return 0, f.maxSeqErr
	}
	return f.maxSeq, nil
}

func (f *fakeBackfillObservationSource) ListBackfillDirtyBuckets(
	_ context.Context,
	afterSeq, throughSeq uint64,
	minLagSeconds int64,
	limit int,
) ([]BackfillDirtyBucket, error) {
	f.lastAfterSeq = afterSeq
	f.lastThroughSeq = throughSeq
	f.lastMinLagSec = minLagSeconds
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []BackfillDirtyBucket
	for _, b := range f.buckets {
		if b.MaxSeq > afterSeq && b.MaxSeq <= throughSeq {
			out = append(out, b)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeBackfillObservationSource) ListBackfillDirtyBucketsAtSequence(
	_ context.Context,
	seq uint64,
	minLagSeconds int64,
	afterKey *BackfillBucketCursor,
	limit int,
) ([]BackfillDirtyBucket, error) {
	f.atSeqCalls++
	f.lastMinLagSec = minLagSeconds
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []BackfillDirtyBucket
	for _, b := range f.buckets {
		if b.MaxSeq != seq {
			continue
		}
		if afterKey != nil {
			key := b.TenantID + "|" + b.SiteID + "|" + b.PointID + "|" + b.BucketStart.UTC().Format(time.RFC3339)
			after := afterKey.TenantID + "|" + afterKey.SiteID + "|" + afterKey.PointID + "|" + afterKey.BucketStart.UTC().Format(time.RFC3339)
			if key <= after {
				continue
			}
		}
		out = append(out, b)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type fakeBackfillJobStore struct {
	bindingsBySite map[string][]BackfillBinding
	loadErr        error
	enqueueErr     error
	enqueued       []BackfillRecalcCandidate
	activeKeys     map[string]bool
	dedupKeys      map[string]bool
}

func (f *fakeBackfillJobStore) LoadSiteBackfillBindings(_ context.Context, tenantID, siteID string, _, _ time.Time) ([]BackfillBinding, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return append([]BackfillBinding(nil), f.bindingsBySite[tenantID+"|"+siteID]...), nil
}

func (f *fakeBackfillJobStore) EnqueueMetricRecalcJobs(_ context.Context, candidates []BackfillRecalcCandidate, _ time.Time) (int, error) {
	if f.enqueueErr != nil {
		return 0, f.enqueueErr
	}
	if f.dedupKeys == nil {
		f.dedupKeys = map[string]bool{}
	}
	inserted := 0
	for _, cand := range candidates {
		activeKey := cand.TenantID + "|" + cand.SiteID + "|" + cand.BindingID + "|" + cand.PeriodStart.UTC().Format(time.RFC3339) + "|" + cand.PeriodEnd.UTC().Format(time.RFC3339)
		dedupKey := activeKey + "|" + time.Unix(0, int64(cand.MaxSeq)).UTC().Format(time.RFC3339Nano)
		if f.activeKeys[activeKey] || f.dedupKeys[dedupKey] {
			continue
		}
		f.dedupKeys[dedupKey] = true
		f.enqueued = append(f.enqueued, cand)
		inserted++
	}
	return inserted, nil
}

func TestBackfillRecalcDetectorInitialWatermarkSkipsPreexistingAndAdvancesOverLiveRows(t *testing.T) {
	const (
		tenantID = "01910000-0000-7000-8000-000000000001"
		siteID   = "01910000-0000-7000-8000-000000000002"
		pointID  = "01910000-0000-7000-8000-000000000003"
		bindID   = "01910000-0000-7000-8000-000000000004"
	)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	bucketTime := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

	source := &fakeBackfillObservationSource{
		maxSeq: 100,
		buckets: []BackfillDirtyBucket{
			{MaxSeq: 50, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: bucketTime},
		},
	}
	store := &fakeBackfillJobStore{
		bindingsBySite: map[string][]BackfillBinding{
			tenantID + "|" + siteID: {{
				TenantID:    tenantID,
				SiteID:      siteID,
				BindingID:   bindID,
				SubjectType: "SITE",
				SubjectID:   siteID,
				Granularity: "HOUR",
				Timezone:    "UTC",
				PointIDs:    []string{pointID},
			}},
		},
	}
	detector, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	// First pass initializes watermark at 100 without triggering on historical seq 50.
	n, err := detector.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("initial DetectAndEnqueue=(%d, %v), want (0, nil)", n, err)
	}
	if wm, ok := detector.Watermark(); !ok || wm != 100 {
		t.Fatalf("watermark=(%d, %v), want (100, true)", wm, ok)
	}

	// Live-only rows advance maxSeq to 150 with 0 dirty buckets: watermark must still advance to 150.
	source.maxSeq = 150
	n, err = detector.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("live-only DetectAndEnqueue=(%d, %v), want (0, nil)", n, err)
	}
	if wm, ok := detector.Watermark(); !ok || wm != 150 {
		t.Fatalf("watermark after live rows=(%d, %v), want (150, true)", wm, ok)
	}

	// New backfill arrives at seq 155 -> triggers 1 HOUR recalc job.
	source.maxSeq = 155
	source.buckets = append(source.buckets, BackfillDirtyBucket{
		MaxSeq: 155, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: bucketTime,
	})
	n, err = detector.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 1 {
		t.Fatalf("backfill DetectAndEnqueue=(%d, %v), want (1, nil)", n, err)
	}
	if wm, ok := detector.Watermark(); !ok || wm != 155 {
		t.Fatalf("watermark after backfill=(%d, %v), want (155, true)", wm, ok)
	}
	if len(store.enqueued) != 1 || !store.enqueued[0].PeriodStart.Equal(bucketTime) || !store.enqueued[0].PeriodEnd.Equal(bucketTime.Add(time.Hour)) {
		t.Fatalf("enqueued=%+v", store.enqueued)
	}
}

func TestBackfillRecalcDetectorTimezoneAlignmentCoalescingAndFinalizationGate(t *testing.T) {
	const (
		tenantID = "01910000-0000-7000-8000-000000000011"
		siteID   = "01910000-0000-7000-8000-000000000012"
		point1   = "01910000-0000-7000-8000-000000000013"
		point2   = "01910000-0000-7000-8000-000000000014"
	)
	// 2026-10-12T16:00:00Z is 2026-10-13T00:00:00+08:00 in Asia/Shanghai.
	bucketStart := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	// Now is 2026-10-13T01:02:00Z (09:02+08:00):
	// - 1MIN (15 windows up to 16:15Z), 5MIN (3), 15MIN (1 up to 16:15Z), HOUR (1 up to 17:00Z) are completed (>5m past end).
	// - DAY ends 2026-10-13T16:00:00Z (> now - 5m) -> incomplete -> skipped!
	// - MONTH/QUARTER/YEAR end in future -> incomplete -> skipped!
	now := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)

	startSeq := uint64(0)
	source := &fakeBackfillObservationSource{
		maxSeq: 20,
		buckets: []BackfillDirtyBucket{
			{MaxSeq: 12, TenantID: tenantID, SiteID: siteID, PointID: point1, BucketStart: bucketStart},
			{MaxSeq: 18, TenantID: tenantID, SiteID: siteID, PointID: point2, BucketStart: bucketStart},
		},
	}
	store := &fakeBackfillJobStore{
		bindingsBySite: map[string][]BackfillBinding{
			tenantID + "|" + siteID: {
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-1m", Granularity: "1MIN", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-5m", Granularity: "5MIN", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-15m", Granularity: "15MIN", Timezone: "Asia/Shanghai", PointIDs: []string{point1, point2}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-hour", Granularity: "HOUR", Timezone: "Asia/Shanghai", PointIDs: []string{point1, point2}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-day", Granularity: "DAY", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-month", Granularity: "MONTH", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-quarter", Granularity: "QUARTER", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-year", Granularity: "YEAR", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
				{TenantID: tenantID, SiteID: siteID, BindingID: "b-rt", Granularity: "REALTIME", Timezone: "Asia/Shanghai", PointIDs: []string{point1}},
			},
		},
	}
	detector, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
		StartSequence:     &startSeq,
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := detector.DetectAndEnqueue(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	// Expected: 15 (1MIN) + 3 (5MIN) + 1 (15MIN coalesced) + 1 (HOUR coalesced) = 20.
	if n != 20 || len(store.enqueued) != 20 {
		t.Fatalf("enqueued=%d (slice=%d), want 20", n, len(store.enqueued))
	}

	// Verify coalesced b-15m and b-hour picked up MaxSeq=18 across point1 (12) and point2 (18).
	var found15m, foundHour bool
	for _, cand := range store.enqueued {
		if cand.BindingID == "b-15m" {
			found15m = true
			if cand.MaxSeq != 18 {
				t.Fatalf("b-15m MaxSeq=%d, want coalesced 18", cand.MaxSeq)
			}
		}
		if cand.BindingID == "b-hour" {
			foundHour = true
			if cand.MaxSeq != 18 {
				t.Fatalf("b-hour MaxSeq=%d, want coalesced 18", cand.MaxSeq)
			}
		}
		if cand.BindingID == "b-day" || cand.BindingID == "b-month" || cand.BindingID == "b-quarter" || cand.BindingID == "b-year" || cand.BindingID == "b-rt" {
			t.Fatalf("unexpected incomplete/realtime candidate enqueued: %+v", cand)
		}
	}
	if !found15m || !foundHour {
		t.Fatalf("missing b-15m (%v) or b-hour (%v)", found15m, foundHour)
	}

	// Once now advances past 2027-01-02 (all calendar periods closed), DAY/MONTH/QUARTER/YEAR all enqueue.
	futureNow := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	store.enqueued = nil
	source.maxSeq = 30
	source.buckets = append(source.buckets, BackfillDirtyBucket{
		MaxSeq: 30, TenantID: tenantID, SiteID: siteID, PointID: point1, BucketStart: bucketStart,
	})
	n, err = detector.DetectAndEnqueue(context.Background(), futureNow)
	if err != nil {
		t.Fatal(err)
	}
	// 15 (1MIN) + 3 (5MIN) + 1 (15MIN) + 1 (HOUR) + 1 (DAY) + 1 (MONTH) + 1 (QUARTER) + 1 (YEAR) = 24.
	if n != 24 {
		t.Fatalf("future enqueued=%d, want 24", n)
	}

	byBinding := make(map[string]BackfillRecalcCandidate)
	for _, cand := range store.enqueued {
		byBinding[cand.BindingID] = cand
	}
	wantDayStart := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	if day := byBinding["b-day"]; !day.PeriodStart.Equal(wantDayStart) || !day.PeriodEnd.Equal(wantDayStart.AddDate(0, 0, 1)) {
		t.Fatalf("DAY period = [%s, %s), want [%s, %s)", day.PeriodStart, day.PeriodEnd, wantDayStart, wantDayStart.AddDate(0, 0, 1))
	}
	wantMonthStart := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	if month := byBinding["b-month"]; !month.PeriodStart.Equal(wantMonthStart) || !month.PeriodEnd.Equal(time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("MONTH period = [%s, %s)", month.PeriodStart, month.PeriodEnd)
	}
	wantQuarterStart := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	if quarter := byBinding["b-quarter"]; !quarter.PeriodStart.Equal(wantQuarterStart) || !quarter.PeriodEnd.Equal(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("QUARTER period = [%s, %s)", quarter.PeriodStart, quarter.PeriodEnd)
	}
	wantYearStart := time.Date(2025, 12, 31, 16, 0, 0, 0, time.UTC)
	if year := byBinding["b-year"]; !year.PeriodStart.Equal(wantYearStart) || !year.PeriodEnd.Equal(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("YEAR period = [%s, %s)", year.PeriodStart, year.PeriodEnd)
	}
}

func TestBackfillRecalcDetectorPaginationAndFailureSafety(t *testing.T) {
	const (
		tenantID = "01910000-0000-7000-8000-000000000021"
		siteID   = "01910000-0000-7000-8000-000000000022"
		pointID  = "01910000-0000-7000-8000-000000000023"
		bindID   = "01910000-0000-7000-8000-000000000024"
	)
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	startSeq := uint64(0)

	// 2 items at seq=10 + 3 items at seq=11 with PageLimit=2 triggers both multi-seq truncation and single-seq drain.
	source := &fakeBackfillObservationSource{
		maxSeq: 12,
		buckets: []BackfillDirtyBucket{
			{MaxSeq: 10, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: base},
			{MaxSeq: 10, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: base.Add(15 * time.Minute)},
			{MaxSeq: 11, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: base.Add(30 * time.Minute)},
			{MaxSeq: 11, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: base.Add(45 * time.Minute)},
			{MaxSeq: 11, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: base.Add(60 * time.Minute)},
		},
	}
	store := &fakeBackfillJobStore{
		bindingsBySite: map[string][]BackfillBinding{
			tenantID + "|" + siteID: {{
				TenantID: tenantID, SiteID: siteID, BindingID: bindID, Granularity: "15MIN", Timezone: "UTC", PointIDs: []string{pointID},
			}},
		},
		enqueueErr: errors.New("postgres unavailable"),
	}
	detector, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
		PageLimit:         2,
		StartSequence:     &startSeq,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Enqueue failure must not advance watermark.
	if _, err := detector.DetectAndEnqueue(context.Background(), now); err == nil {
		t.Fatal("expected enqueue failure")
	}
	if wm, _ := detector.Watermark(); wm != 0 {
		t.Fatalf("watermark advanced to %d despite enqueue failure", wm)
	}

	store.enqueueErr = nil
	n, err := detector.DetectAndEnqueue(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || len(store.enqueued) != 5 {
		t.Fatalf("enqueued=%d, want all 5 paginated buckets", n)
	}
	if source.atSeqCalls < 3 {
		t.Fatalf("expected single-sequence keyset pagination calls, got %d", source.atSeqCalls)
	}
	if wm, _ := detector.Watermark(); wm != 12 {
		t.Fatalf("final watermark=%d, want 12", wm)
	}
}

func TestExtractBoundPointIDsAndClickHouseBackfillSourceHTTP(t *testing.T) {
	ids, err := extractBoundPointIDs([]byte(`{"points":{"load":"01910000-0000-7000-8000-000000000001","sub":["01910000-0000-7000-8000-000000000002","01910000-0000-7000-8000-000000000001"]},"externals":{"tariff":0.8}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "01910000-0000-7000-8000-000000000001" || ids[1] != "01910000-0000-7000-8000-000000000002" {
		t.Fatalf("extracted point IDs = %v", ids)
	}

	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		if strings.Contains(q, "max(history_sequence)") && strings.Contains(q, "FORMAT TabSeparated") {
			_, _ = w.Write([]byte("42\n"))
			return
		}
		row, _ := json.Marshal(map[string]any{
			"max_seq":      "42",
			"tenant_id":    "01910000-0000-7000-8000-000000000001",
			"site_id":      "01910000-0000-7000-8000-000000000002",
			"point_id":     "01910000-0000-7000-8000-000000000003",
			"dirty_bucket": "2026-10-12T16:00:00.000Z",
		})
		_, _ = w.Write(append(row, '\n'))
	}))
	defer server.Close()

	src, err := NewClickHouseBackfillSource(server.URL, "metric_engine_runtime", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	maxSeq, err := src.MaxHistorySequence(context.Background())
	if err != nil || maxSeq != 42 {
		t.Fatalf("MaxHistorySequence=(%d, %v), want (42, nil)", maxSeq, err)
	}
	buckets, err := src.ListBackfillDirtyBuckets(context.Background(), 10, 42, 600, 2048)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("ListBackfillDirtyBuckets=(%+v, %v)", buckets, err)
	}
	if buckets[0].MaxSeq != 42 || !buckets[0].BucketStart.Equal(time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected bucket: %+v", buckets[0])
	}
	if len(queries) != 2 || !strings.Contains(queries[1], "telemetry_history.observations") || !strings.Contains(queries[1], "dateDiff('second', sampled_at, projected_at) >= 600") {
		t.Fatalf("unexpected ClickHouse queries: %v", queries)
	}
}
