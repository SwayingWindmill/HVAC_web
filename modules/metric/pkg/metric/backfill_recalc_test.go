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
	maxAfterSeqs   []uint64
}

func (f *fakeBackfillObservationSource) MaxHistorySequence(_ context.Context, afterSeq uint64) (uint64, error) {
	f.maxAfterSeqs = append(f.maxAfterSeqs, afterSeq)
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

	watermarks map[string]BackfillWatermarkState
	wmLoadErr  error
	wmSaveErr  error
}

func (f *fakeBackfillJobStore) LoadBackfillWatermark(_ context.Context, scope string) (BackfillWatermarkState, bool, error) {
	if f.wmLoadErr != nil {
		return BackfillWatermarkState{}, false, f.wmLoadErr
	}
	if f.watermarks == nil {
		return BackfillWatermarkState{}, false, nil
	}
	state, ok := f.watermarks[scope]
	return state, ok, nil
}

func (f *fakeBackfillJobStore) SaveBackfillWatermark(_ context.Context, state BackfillWatermarkState) error {
	if f.wmSaveErr != nil {
		return f.wmSaveErr
	}
	if f.watermarks == nil {
		f.watermarks = make(map[string]BackfillWatermarkState)
	}
	f.watermarks[state.Scope] = state
	return nil
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

	n, err := detector.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("initial DetectAndEnqueue=(%d, %v), want (0, nil)", n, err)
	}
	if wm, ok := detector.Watermark(); !ok || wm != 100 {
		t.Fatalf("watermark=(%d, %v), want (100, true)", wm, ok)
	}

	source.maxSeq = 150
	n, err = detector.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("live-only DetectAndEnqueue=(%d, %v), want (0, nil)", n, err)
	}
	if wm, ok := detector.Watermark(); !ok || wm != 150 {
		t.Fatalf("watermark after live rows=(%d, %v), want (150, true)", wm, ok)
	}

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
	// Expected with T_end+1 boundary continuity: 16 (1MIN) + 4 (5MIN) + 2 (15MIN) + 2 (HOUR) = 24.
	if n != 24 || len(store.enqueued) != 24 {
		t.Fatalf("enqueued=%d (slice=%d), want 24", n, len(store.enqueued))
	}

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
	// 16 (1MIN) + 4 (5MIN) + 2 (15MIN) + 2 (HOUR) + 2 (DAY+T_end+1) + 2 (MONTH+T_end+1) + 1 (QUARTER; next ends 2027-03-31) + 1 (YEAR; next ends 2027-12-31) = 30.
	if n != 30 {
		t.Fatalf("future enqueued=%d, want 30", n)
	}

	periodsByBinding := make(map[string][]Period)
	for _, cand := range store.enqueued {
		periodsByBinding[cand.BindingID] = append(periodsByBinding[cand.BindingID], Period{Start: cand.PeriodStart, End: cand.PeriodEnd})
	}
	wantDayStart := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	if days := periodsByBinding["b-day"]; len(days) != 2 || !days[0].Start.Equal(wantDayStart) || !days[0].End.Equal(wantDayStart.AddDate(0, 0, 1)) || !days[1].Start.Equal(wantDayStart.AddDate(0, 0, 1)) || !days[1].End.Equal(wantDayStart.AddDate(0, 0, 2)) {
		t.Fatalf("DAY periods = %+v", days)
	}
	wantMonthStart := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	if months := periodsByBinding["b-month"]; len(months) != 2 || !months[0].Start.Equal(wantMonthStart) || !months[0].End.Equal(time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC)) || !months[1].End.Equal(time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("MONTH periods = %+v", months)
	}
	wantQuarterStart := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	if quarters := periodsByBinding["b-quarter"]; len(quarters) != 1 || !quarters[0].Start.Equal(wantQuarterStart) || !quarters[0].End.Equal(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("QUARTER periods = %+v", quarters)
	}
	wantYearStart := time.Date(2025, 12, 31, 16, 0, 0, 0, time.UTC)
	if years := periodsByBinding["b-year"]; len(years) != 1 || !years[0].Start.Equal(wantYearStart) || !years[0].End.Equal(time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("YEAR periods = %+v", years)
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
	// seq=10 drains [00:00..00:30)+[00:30,00:45), seq=11 drains [00:30..01:15)+T_end+1 [01:15,01:30).
	if n != 7 || len(store.enqueued) != 7 {
		t.Fatalf("enqueued=%d, want 7 (5 dirty buckets + T_end+1 overlap/tail)", n)
	}
	if source.atSeqCalls < 3 {
		t.Fatalf("expected single-sequence keyset pagination calls, got %d", source.atSeqCalls)
	}
	if wm, _ := detector.Watermark(); wm != 12 {
		t.Fatalf("final watermark=%d, want 12", wm)
	}
}

func TestBackfillRecalcDetectorPersistedWatermarkResumesAcrossRestart(t *testing.T) {
	const (
		tenantID = "01910000-0000-7000-8000-000000000031"
		siteID   = "01910000-0000-7000-8000-000000000032"
		pointID  = "01910000-0000-7000-8000-000000000033"
		bindID   = "01910000-0000-7000-8000-000000000034"
	)
	now := time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)
	bucketTime := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

	source := &fakeBackfillObservationSource{maxSeq: 100}
	store := &fakeBackfillJobStore{
		bindingsBySite: map[string][]BackfillBinding{
			tenantID + "|" + siteID: {{
				TenantID: tenantID, SiteID: siteID, BindingID: bindID,
				SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR", Timezone: "UTC", PointIDs: []string{pointID},
			}},
		},
	}

	firstDetector, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDetector.InitializeWatermark(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state := store.watermarks[DefaultBackfillWatermarkScope]; state.LastProcessedSequence != 100 {
		t.Fatalf("persisted cold-start watermark = %+v, want 100", state)
	}

	// Simulate downtime while sequences 101..120 arrive, including late bucket at seq 112.
	source.maxSeq = 120
	source.buckets = append(source.buckets, BackfillDirtyBucket{
		MaxSeq: 112, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: bucketTime,
	})

	// If PG watermark load transiently fails, InitializeWatermark returns an error instead of jumping to 120.
	store.wmLoadErr = errors.New("pg read timeout")
	restarted, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.InitializeWatermark(context.Background()); err == nil {
		t.Fatal("expected watermark load error")
	}
	store.wmLoadErr = nil

	if err := restarted.InitializeWatermark(context.Background()); err != nil {
		t.Fatal(err)
	}
	if wm, ok := restarted.Watermark(); !ok || wm != 100 {
		t.Fatalf("restarted watermark = (%d, %v), want (100, true)", wm, ok)
	}

	// Simulate watermark save failure: enqueue succeeds idempotently, but memory watermark must not advance past 100.
	store.wmSaveErr = errors.New("pg write failed")
	if _, err := restarted.DetectAndEnqueue(context.Background(), now); err == nil {
		t.Fatal("expected watermark save failure")
	}
	if wm, _ := restarted.Watermark(); wm != 100 {
		t.Fatalf("watermark advanced to %d despite save failure", wm)
	}
	if len(store.enqueued) != 2 {
		t.Fatalf("enqueued before watermark save failure = %d, want 2 [10:00 + T_end+1 11:00]", len(store.enqueued))
	}
	store.wmSaveErr = nil

	// Retry after DB recovery is dedup-safe (0 duplicate inserts) and persists watermark 120.
	n, err := restarted.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("idempotent retry DetectAndEnqueue = (%d, %v), want (0, nil)", n, err)
	}
	if wm, _ := restarted.Watermark(); wm != 120 {
		t.Fatalf("resumed watermark = %d, want 120", wm)
	}
	if state := store.watermarks[DefaultBackfillWatermarkScope]; state.LastProcessedSequence != 120 || state.LastProcessedIngestedAt == nil {
		t.Fatalf("persisted watermark not updated cleanly: %+v", state)
	}

	// Simulate another restart without calling InitializeWatermark directly: DetectAndEnqueue loads 120 from PG.
	source.maxSeq = 130
	source.buckets = append(source.buckets, BackfillDirtyBucket{
		MaxSeq: 125, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: bucketTime.Add(-2 * time.Hour),
	})
	store.enqueued = nil
	restarted2, err := NewBackfillRecalcDetector(store, source, BackfillRecalcConfig{
		FinalizationDelay: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err = restarted2.DetectAndEnqueue(context.Background(), now)
	if err != nil || n != 2 || len(store.enqueued) != 2 {
		t.Fatalf("auto-resume DetectAndEnqueue = (%d, %v) len=%d, want (2, nil)", n, err, len(store.enqueued))
	}
	if wm, _ := restarted2.Watermark(); wm != 130 {
		t.Fatalf("restarted2 watermark = %d, want 130", wm)
	}
}

func TestBackfillRecalcDetectorEndPlusOneAndSameGranularityMetricCascade(t *testing.T) {
	const (
		tenantID = "01910000-0000-7000-8000-000000000041"
		siteID   = "01910000-0000-7000-8000-000000000042"
		pointID  = "01910000-0000-7000-8000-000000000043"
	)
	now := time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)
	bucketTime := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	startSeq := uint64(0)

	source := &fakeBackfillObservationSource{
		maxSeq: 15,
		buckets: []BackfillDirtyBucket{
			{MaxSeq: 15, TenantID: tenantID, SiteID: siteID, PointID: pointID, BucketStart: bucketTime},
		},
	}
	store := &fakeBackfillJobStore{
		bindingsBySite: map[string][]BackfillBinding{
			tenantID + "|" + siteID: {
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-base", MetricID: "m-base",
					SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR", Timezone: "UTC",
					PointIDs:            []string{pointID},
					DependencyMetricIDs: []string{"m-cycle"}, // cycle guard test
				},
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-hop1", MetricID: "m-hop1",
					SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR", Timezone: "UTC",
					PointIDs:            nil, // derived-only binding must NOT be skipped
					DependencyMetricIDs: []string{"m-base"},
				},
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-hop2", MetricID: "m-hop2",
					SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR", Timezone: "UTC",
					DependencyMetricIDs: []string{"m-hop1"},
				},
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-cycle", MetricID: "m-cycle",
					SubjectType: "SITE", SubjectID: siteID, Granularity: "HOUR", Timezone: "UTC",
					DependencyMetricIDs: []string{"m-hop2"},
				},
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-day-mismatch", MetricID: "m-day",
					SubjectType: "SITE", SubjectID: siteID, Granularity: "DAY", Timezone: "UTC",
					DependencyMetricIDs: []string{"m-base"}, // different time_granularity -> excluded
				},
				{
					TenantID: tenantID, SiteID: siteID, BindingID: "b-asset-mismatch", MetricID: "m-asset",
					SubjectType: "ASSET", SubjectID: "01910000-0000-7000-8000-000000000099", Granularity: "HOUR", Timezone: "UTC",
					DependencyMetricIDs: []string{"m-base"}, // different subject_type -> excluded
				},
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
	// 4 same-granularity/same-subject bindings (b-base, b-hop1, b-hop2, b-cycle) * 2 periods (10:00..11:00 + T_end+1 11:00..12:00) = 8.
	if n != 8 {
		t.Fatalf("enqueued=%d, want 8 (got %+v)", n, store.enqueued)
	}
	seen := make(map[string]int)
	for _, cand := range store.enqueued {
		seen[cand.BindingID]++
		if cand.BindingID == "b-day-mismatch" || cand.BindingID == "b-asset-mismatch" {
			t.Fatalf("unexpected cross-granularity or cross-subject cascade: %+v", cand)
		}
	}
	for _, wantID := range []string{"b-base", "b-hop1", "b-hop2", "b-cycle"} {
		if seen[wantID] != 2 {
			t.Fatalf("binding %s count=%d, want 2 (10:00 + 11:00)", wantID, seen[wantID])
		}
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
	var nextSeqReply = "42\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		if strings.Contains(q, "max(history_sequence)") && strings.Contains(q, "FORMAT TabSeparated") {
			_, _ = w.Write([]byte(nextSeqReply))
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
	maxSeq, err := src.MaxHistorySequence(context.Background(), 0)
	if err != nil || maxSeq != 42 {
		t.Fatalf("MaxHistorySequence=(%d, %v), want (42, nil)", maxSeq, err)
	}
	if strings.Contains(queries[0], "WHERE") {
		t.Fatalf("cold-start MaxHistorySequence(0) should not include WHERE filter: %s", queries[0])
	}
	nextSeqReply = "0\n"
	deltaSeq, err := src.MaxHistorySequence(context.Background(), 42)
	if err != nil || deltaSeq != 0 {
		t.Fatalf("MaxHistorySequence(42)=(%d, %v), want (0, nil)", deltaSeq, err)
	}
	if !strings.Contains(queries[1], "WHERE history_sequence > 42") {
		t.Fatalf("incremental MaxHistorySequence(42) missing minmax predicate: %s", queries[1])
	}
	buckets, err := src.ListBackfillDirtyBuckets(context.Background(), 10, 42, 600, 2048)
	if err != nil || len(buckets) != 1 {
		t.Fatalf("ListBackfillDirtyBuckets=(%+v, %v)", buckets, err)
	}
	if buckets[0].MaxSeq != 42 || !buckets[0].BucketStart.Equal(time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected bucket: %+v", buckets[0])
	}
	if len(queries) != 3 || !strings.Contains(queries[2], "telemetry_history.observations AS obs") || !strings.Contains(queries[2], "dateDiff('second', obs.sampled_at, obs.projected_at) >= 600") || !strings.Contains(queries[2], "GROUP BY tenant_key, site_key, point_key, dirty_bucket") {
		t.Fatalf("unexpected ClickHouse queries: %v", queries)
	}
	atSeqBuckets, err := src.ListBackfillDirtyBucketsAtSequence(context.Background(), 42, 600, &BackfillBucketCursor{
		TenantID: "01910000-0000-7000-8000-000000000001", SiteID: "01910000-0000-7000-8000-000000000002", PointID: "01910000-0000-7000-8000-000000000000", BucketStart: time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC),
	}, 2048)
	if err != nil || len(atSeqBuckets) != 1 {
		t.Fatalf("ListBackfillDirtyBucketsAtSequence=(%+v, %v)", atSeqBuckets, err)
	}
	if !strings.Contains(queries[3], "HAVING (tenant_key, site_key, point_key, dirty_bucket) >") {
		t.Fatalf("ListBackfillDirtyBucketsAtSequence missing non-colliding HAVING alias:\n%s", queries[3])
	}
}
