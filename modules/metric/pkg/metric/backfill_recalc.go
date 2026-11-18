package metric

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
)

const (
	defaultBackfillPageLimit     = 2048
	defaultBackfillMaxPages      = 16
	defaultMinBackfillLag        = 5 * time.Minute
	defaultBackfillJobPriority   = 20
	defaultBackfillJobMaxAttempt = 3
	defaultBackfillJobTimeoutSec = 60
	backfillBucketDuration       = 15 * time.Minute
)

// BackfillDirtyBucket describes one 15-minute UTC observation bucket with late arrivals.
type BackfillDirtyBucket struct {
	MaxSeq      uint64
	TenantID    string
	SiteID      string
	PointID     string
	BucketStart time.Time
}

// BackfillBucketCursor identifies the last grouped bucket returned within a single sequence.
type BackfillBucketCursor struct {
	TenantID    string
	SiteID      string
	PointID     string
	BucketStart time.Time
}

// BackfillBinding describes a RELEASED metric binding with point sources and site timezone.
type BackfillBinding struct {
	TenantID      string
	SiteID        string
	BindingID     string
	SubjectType   string
	SubjectID     string
	Granularity   string
	Timezone      string
	PointIDs      []string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	VersionFrom   time.Time
	VersionTo     *time.Time
}

// BackfillRecalcCandidate represents one coalesced METRIC_RECALC job to enqueue.
type BackfillRecalcCandidate struct {
	TenantID    string
	SiteID      string
	BindingID   string
	SubjectType string
	SubjectID   string
	Timezone    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	MaxSeq      uint64
}

// BackfillObservationSource queries incremental late-arrival observation buckets.
type BackfillObservationSource interface {
	MaxHistorySequence(ctx context.Context) (uint64, error)
	ListBackfillDirtyBuckets(ctx context.Context, afterSeq, throughSeq uint64, minLagSeconds int64, limit int) ([]BackfillDirtyBucket, error)
	ListBackfillDirtyBucketsAtSequence(ctx context.Context, seq uint64, minLagSeconds int64, afterKey *BackfillBucketCursor, limit int) ([]BackfillDirtyBucket, error)
}

// BackfillJobStore loads affected bindings and persists coalesced METRIC_RECALC jobs.
type BackfillJobStore interface {
	LoadSiteBackfillBindings(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]BackfillBinding, error)
	EnqueueMetricRecalcJobs(ctx context.Context, candidates []BackfillRecalcCandidate, now time.Time) (int, error)
}

// BackfillRecalcConfig configures incremental ClickHouse backfill detection.
type BackfillRecalcConfig struct {
	FinalizationDelay time.Duration
	MinBackfillLag    time.Duration
	PageLimit         int
	MaxPagesPerScan   int
	StartSequence     *uint64
}

// BackfillRecalcDetector incrementally scans telemetry_history.observations above its
// history_sequence watermark and enqueues coalesced METRIC_RECALC scheduler jobs.
type BackfillRecalcDetector struct {
	store             BackfillJobStore
	source            BackfillObservationSource
	finalizationDelay time.Duration
	minBackfillLag    time.Duration
	pageLimit         int
	maxPagesPerScan   int

	mu          sync.Mutex
	initialized bool
	watermark   uint64
}

// NewBackfillRecalcDetector constructs a BackfillRecalcDetector.
func NewBackfillRecalcDetector(store BackfillJobStore, source BackfillObservationSource, cfg BackfillRecalcConfig) (*BackfillRecalcDetector, error) {
	if store == nil || source == nil {
		return nil, errors.New("metric backfill detector requires store and observation source")
	}
	if cfg.FinalizationDelay < 0 || cfg.FinalizationDelay > 24*time.Hour {
		return nil, errors.New("metric backfill finalization delay is invalid")
	}
	minLag := cfg.MinBackfillLag
	if minLag <= 0 {
		minLag = cfg.FinalizationDelay
		if minLag < defaultMinBackfillLag {
			minLag = defaultMinBackfillLag
		}
	}
	pageLimit := cfg.PageLimit
	if pageLimit <= 0 {
		pageLimit = defaultBackfillPageLimit
	}
	maxPages := cfg.MaxPagesPerScan
	if maxPages <= 0 {
		maxPages = defaultBackfillMaxPages
	}
	detector := &BackfillRecalcDetector{
		store:             store,
		source:            source,
		finalizationDelay: cfg.FinalizationDelay,
		minBackfillLag:    minLag,
		pageLimit:         pageLimit,
		maxPagesPerScan:   maxPages,
	}
	if cfg.StartSequence != nil {
		detector.initialized = true
		detector.watermark = *cfg.StartSequence
	}
	return detector, nil
}

// InitializeWatermark anchors the detector watermark at the current maximum history_sequence
// so pre-existing historical rows do not trigger a startup recalculation storm.
func (d *BackfillRecalcDetector) InitializeWatermark(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.initialized {
		return nil
	}
	maxSeq, err := d.source.MaxHistorySequence(ctx)
	if err != nil {
		return err
	}
	d.watermark = maxSeq
	d.initialized = true
	return nil
}

// Watermark returns whether the detector is initialized and its current history_sequence cursor.
func (d *BackfillRecalcDetector) Watermark() (uint64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermark, d.initialized
}

// DetectAndEnqueue scans newly projected backfill observations above the current watermark,
// resolves affected released bindings, and enqueues coalesced METRIC_RECALC jobs.
func (d *BackfillRecalcDetector) DetectAndEnqueue(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	maxAvailSeq, err := d.source.MaxHistorySequence(ctx)
	if err != nil {
		return 0, err
	}
	if !d.initialized {
		d.watermark = maxAvailSeq
		d.initialized = true
		return 0, nil
	}
	if maxAvailSeq <= d.watermark {
		return 0, nil
	}

	minLagSeconds := int64(d.minBackfillLag / time.Second)
	totalEnqueued := 0
	for page := 0; page < d.maxPagesPerScan && d.watermark < maxAvailSeq; page++ {
		buckets, err := d.source.ListBackfillDirtyBuckets(ctx, d.watermark, maxAvailSeq, minLagSeconds, d.pageLimit)
		if err != nil {
			return totalEnqueued, err
		}
		if len(buckets) == 0 {
			d.watermark = maxAvailSeq
			break
		}

		if len(buckets) < d.pageLimit {
			enqueued, err := d.enqueueBuckets(ctx, buckets, now)
			if err != nil {
				return totalEnqueued, err
			}
			totalEnqueued += enqueued
			d.watermark = maxAvailSeq
			break
		}

		firstSeq := buckets[0].MaxSeq
		lastSeq := buckets[len(buckets)-1].MaxSeq
		if lastSeq > firstSeq {
			prefixEnd := len(buckets)
			for prefixEnd > 0 && buckets[prefixEnd-1].MaxSeq == lastSeq {
				prefixEnd--
			}
			enqueued, err := d.enqueueBuckets(ctx, buckets[:prefixEnd], now)
			if err != nil {
				return totalEnqueued, err
			}
			totalEnqueued += enqueued
			d.watermark = lastSeq - 1
			continue
		}

		// Single sequence spans >= pageLimit dirty buckets: drain the whole sequence via keyset pagination.
		enqueued, err := d.drainSingleSequence(ctx, firstSeq, minLagSeconds, now)
		if err != nil {
			return totalEnqueued, err
		}
		totalEnqueued += enqueued
		d.watermark = firstSeq
	}
	return totalEnqueued, nil
}

func (d *BackfillRecalcDetector) drainSingleSequence(ctx context.Context, seq uint64, minLagSeconds int64, now time.Time) (int, error) {
	totalEnqueued := 0
	var cursor *BackfillBucketCursor
	for {
		buckets, err := d.source.ListBackfillDirtyBucketsAtSequence(ctx, seq, minLagSeconds, cursor, d.pageLimit)
		if err != nil {
			return totalEnqueued, err
		}
		if len(buckets) == 0 {
			return totalEnqueued, nil
		}
		enqueued, err := d.enqueueBuckets(ctx, buckets, now)
		if err != nil {
			return totalEnqueued, err
		}
		totalEnqueued += enqueued
		if len(buckets) < d.pageLimit {
			return totalEnqueued, nil
		}
		last := buckets[len(buckets)-1]
		cursor = &BackfillBucketCursor{
			TenantID:    last.TenantID,
			SiteID:      last.SiteID,
			PointID:     last.PointID,
			BucketStart: last.BucketStart,
		}
	}
}

func (d *BackfillRecalcDetector) enqueueBuckets(ctx context.Context, buckets []BackfillDirtyBucket, now time.Time) (int, error) {
	candidates, err := d.buildCandidates(ctx, buckets, now)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	return d.store.EnqueueMetricRecalcJobs(ctx, candidates, now)
}

type tenantSiteKey struct {
	tenantID string
	siteID   string
}

type candidateKey struct {
	tenantID    string
	siteID      string
	bindingID   string
	periodStart int64
	periodEnd   int64
}

func (d *BackfillRecalcDetector) buildCandidates(ctx context.Context, buckets []BackfillDirtyBucket, now time.Time) ([]BackfillRecalcCandidate, error) {
	grouped := make(map[tenantSiteKey][]BackfillDirtyBucket)
	var siteKeys []tenantSiteKey
	for _, bucket := range buckets {
		tenantID := strings.ToLower(strings.TrimSpace(bucket.TenantID))
		siteID := strings.ToLower(strings.TrimSpace(bucket.SiteID))
		pointID := strings.ToLower(strings.TrimSpace(bucket.PointID))
		if tenantID == "" || siteID == "" || pointID == "" || bucket.BucketStart.IsZero() {
			continue
		}
		bucket.TenantID = tenantID
		bucket.SiteID = siteID
		bucket.PointID = pointID
		bucket.BucketStart = bucket.BucketStart.UTC()
		key := tenantSiteKey{tenantID: tenantID, siteID: siteID}
		if _, exists := grouped[key]; !exists {
			siteKeys = append(siteKeys, key)
		}
		grouped[key] = append(grouped[key], bucket)
	}
	sort.Slice(siteKeys, func(i, j int) bool {
		if siteKeys[i].tenantID != siteKeys[j].tenantID {
			return siteKeys[i].tenantID < siteKeys[j].tenantID
		}
		return siteKeys[i].siteID < siteKeys[j].siteID
	})

	cutoff := now.UTC().Add(-d.finalizationDelay)
	locations := make(map[string]*time.Location)
	coalesced := make(map[candidateKey]BackfillRecalcCandidate)

	for _, siteKey := range siteKeys {
		siteBuckets := grouped[siteKey]
		minStart := siteBuckets[0].BucketStart
		maxEnd := siteBuckets[0].BucketStart.Add(backfillBucketDuration)
		for _, bucket := range siteBuckets[1:] {
			if bucket.BucketStart.Before(minStart) {
				minStart = bucket.BucketStart
			}
			if end := bucket.BucketStart.Add(backfillBucketDuration); end.After(maxEnd) {
				maxEnd = end
			}
		}

		bindings, err := d.store.LoadSiteBackfillBindings(ctx, siteKey.tenantID, siteKey.siteID, minStart, maxEnd)
		if err != nil {
			return nil, err
		}
		if len(bindings) == 0 {
			continue
		}

		byPoint := make(map[string][]BackfillBinding)
		for _, binding := range bindings {
			for _, pointID := range binding.PointIDs {
				normalized := strings.ToLower(strings.TrimSpace(pointID))
				if normalized != "" {
					byPoint[normalized] = append(byPoint[normalized], binding)
				}
			}
		}

		for _, bucket := range siteBuckets {
			matched := byPoint[bucket.PointID]
			if len(matched) == 0 {
				continue
			}
			bStart := bucket.BucketStart
			bEnd := bStart.Add(backfillBucketDuration)
			for _, binding := range matched {
				if !windowOverlaps(binding.EffectiveFrom, binding.EffectiveTo, bStart, bEnd) ||
					!windowOverlaps(binding.VersionFrom, binding.VersionTo, bStart, bEnd) {
					continue
				}
				tz := strings.TrimSpace(binding.Timezone)
				if tz == "" {
					tz = "UTC"
				}
				loc, ok := locations[tz]
				if !ok {
					loaded, tzErr := time.LoadLocation(tz)
					if tzErr != nil {
						return nil, fmt.Errorf("load site timezone %q: %w", tz, tzErr)
					}
					loc = loaded
					locations[tz] = loc
				}

				periods := alignedPeriodsForBucket(binding.Granularity, loc, bStart, bEnd)
				for _, period := range periods {
					if !windowOverlaps(binding.EffectiveFrom, binding.EffectiveTo, period.Start, period.Start) ||
						!windowOverlaps(binding.VersionFrom, binding.VersionTo, period.Start, period.Start) {
						continue
					}
					if period.End.After(cutoff) {
						continue
					}
					ckey := candidateKey{
						tenantID:    siteKey.tenantID,
						siteID:      siteKey.siteID,
						bindingID:   binding.BindingID,
						periodStart: period.Start.UnixNano(),
						periodEnd:   period.End.UnixNano(),
					}
					if existing, exists := coalesced[ckey]; exists {
						if bucket.MaxSeq > existing.MaxSeq {
							existing.MaxSeq = bucket.MaxSeq
							coalesced[ckey] = existing
						}
						continue
					}
					coalesced[ckey] = BackfillRecalcCandidate{
						TenantID:    siteKey.tenantID,
						SiteID:      siteKey.siteID,
						BindingID:   binding.BindingID,
						SubjectType: binding.SubjectType,
						SubjectID:   binding.SubjectID,
						Timezone:    tz,
						PeriodStart: period.Start,
						PeriodEnd:   period.End,
						MaxSeq:      bucket.MaxSeq,
					}
				}
			}
		}
	}

	candidates := make([]BackfillRecalcCandidate, 0, len(coalesced))
	for _, cand := range coalesced {
		candidates = append(candidates, cand)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].TenantID != candidates[j].TenantID {
			return candidates[i].TenantID < candidates[j].TenantID
		}
		if candidates[i].SiteID != candidates[j].SiteID {
			return candidates[i].SiteID < candidates[j].SiteID
		}
		if !candidates[i].PeriodStart.Equal(candidates[j].PeriodStart) {
			return candidates[i].PeriodStart.Before(candidates[j].PeriodStart)
		}
		if !candidates[i].PeriodEnd.Equal(candidates[j].PeriodEnd) {
			return candidates[i].PeriodEnd.Before(candidates[j].PeriodEnd)
		}
		return candidates[i].BindingID < candidates[j].BindingID
	})
	return candidates, nil
}

func alignedPeriodsForBucket(granularity string, loc *time.Location, bucketStart, bucketEnd time.Time) []Period {
	g := strings.ToUpper(strings.TrimSpace(granularity))
	if g == "" || g == "REALTIME" {
		return nil
	}
	var periods []Period
	wStart, wEnd := periodContaining(g, bucketStart.In(loc), loc)
	if wStart.IsZero() || !wEnd.After(wStart) {
		return nil
	}
	for wStart.Before(bucketEnd) {
		startUTC := wStart.UTC()
		endUTC := wEnd.UTC()
		if endUTC.After(bucketStart) && startUTC.Before(bucketEnd) && endUTC.After(startUTC) {
			periods = append(periods, Period{Start: startUTC, End: endUTC})
		}
		nextStart, nextEnd := periodContaining(g, wEnd.In(loc), loc)
		if !nextStart.After(wStart) || !nextEnd.After(nextStart) {
			break
		}
		wStart, wEnd = nextStart, nextEnd
	}
	return periods
}

func windowOverlaps(from time.Time, to *time.Time, start, end time.Time) bool {
	if from.After(end) {
		return false
	}
	if to != nil && !to.After(start) {
		return false
	}
	return true
}

func periodContaining(granularity string, local time.Time, loc *time.Location) (time.Time, time.Time) {
	switch granularity {
	case "1MIN":
		start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, loc)
		return start, start.Add(time.Minute)
	case "5MIN":
		minute := local.Minute() - local.Minute()%5
		start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, loc)
		return start, start.Add(5 * time.Minute)
	case "15MIN":
		minute := local.Minute() - local.Minute()%15
		start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, loc)
		return start, start.Add(15 * time.Minute)
	case "HOUR":
		start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, loc)
		return start, start.Add(time.Hour)
	case "DAY":
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 0, 1)
	case "MONTH":
		start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 1, 0)
	case "QUARTER":
		quarterMonth := time.Month(((int(local.Month())-1)/3)*3 + 1)
		start := time.Date(local.Year(), quarterMonth, 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 3, 0)
	case "YEAR":
		start := time.Date(local.Year(), time.January, 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(1, 0, 0)
	default:
		return time.Time{}, time.Time{}
	}
}

type bindingSourcePoints struct {
	Points map[string]json.RawMessage `json:"points"`
}

func extractBoundPointIDs(sourceDefBytes []byte) ([]string, error) {
	if len(bytes.TrimSpace(sourceDefBytes)) == 0 {
		return nil, nil
	}
	var parsed bindingSourcePoints
	if err := json.Unmarshal(sourceDefBytes, &parsed); err != nil {
		return nil, fmt.Errorf("decode metric binding source definition: %w", err)
	}
	if len(parsed.Points) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var ids []string
	addID := func(raw string) {
		normalized := strings.ToLower(strings.TrimSpace(raw))
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; !exists {
			seen[normalized] = struct{}{}
			ids = append(ids, normalized)
		}
	}
	for _, rawVal := range parsed.Points {
		trimmed := bytes.TrimSpace(rawVal)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		if trimmed[0] == '"' {
			var single string
			if err := json.Unmarshal(trimmed, &single); err == nil {
				addID(single)
			}
			continue
		}
		if trimmed[0] == '[' {
			var list []string
			if err := json.Unmarshal(trimmed, &list); err == nil {
				for _, item := range list {
					addID(item)
				}
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// LoadSiteBackfillBindings loads active RELEASED bindings for a tenant/site scoped by RLS.
func (s *PostgresStore) LoadSiteBackfillBindings(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]BackfillBinding, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("metric postgres store is unavailable")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = scope(ctx, tx, tenantID, siteID); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `SELECT b.id::text, b.subject_type, b.subject_id::text, b.time_granularity, b.source_definition,
       b.effective_from, b.effective_to,
       v.effective_from, v.effective_to,
       s.timezone
FROM core_registry.metric_bindings b
JOIN core_registry.metric_versions v ON v.tenant_id = b.tenant_id AND v.id = b.metric_version_id
JOIN core_registry.sites s ON s.tenant_id = b.tenant_id AND s.id = b.site_id
WHERE b.tenant_id = $1::uuid
  AND b.site_id = $2::uuid
  AND b.status = 'RELEASED'
  AND v.status = 'RELEASED'
  AND b.time_granularity <> 'REALTIME'
  AND b.effective_from <= $4
  AND (b.effective_to IS NULL OR b.effective_to > $3)
  AND v.effective_from <= $4
  AND (v.effective_to IS NULL OR v.effective_to > $3)`, tenantID, siteID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("load metric backfill bindings: %w", err)
	}
	defer rows.Close()

	var bindings []BackfillBinding
	for rows.Next() {
		var (
			b              BackfillBinding
			sourceDefBytes []byte
		)
		b.TenantID = tenantID
		b.SiteID = siteID
		if err = rows.Scan(
			&b.BindingID, &b.SubjectType, &b.SubjectID, &b.Granularity, &sourceDefBytes,
			&b.EffectiveFrom, &b.EffectiveTo, &b.VersionFrom, &b.VersionTo, &b.Timezone,
		); err != nil {
			return nil, fmt.Errorf("scan metric backfill binding: %w", err)
		}
		pointIDs, decErr := extractBoundPointIDs(sourceDefBytes)
		if decErr != nil {
			return nil, decErr
		}
		if len(pointIDs) == 0 {
			continue
		}
		b.PointIDs = pointIDs
		b.EffectiveFrom = b.EffectiveFrom.UTC()
		if b.EffectiveTo != nil {
			t := b.EffectiveTo.UTC()
			b.EffectiveTo = &t
		}
		b.VersionFrom = b.VersionFrom.UTC()
		if b.VersionTo != nil {
			t := b.VersionTo.UTC()
			b.VersionTo = &t
		}
		bindings = append(bindings, b)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bindings, nil
}

type recalcJobPayload struct {
	BindingID   string `json:"bindingId"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
}

// EnqueueMetricRecalcJobs inserts coalesced READY METRIC_RECALC jobs if no active job
// already covers the same binding and period.
func (s *PostgresStore) EnqueueMetricRecalcJobs(ctx context.Context, candidates []BackfillRecalcCandidate, now time.Time) (int, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("metric postgres store is unavailable")
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	nowUTC := now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	enqueued := 0
	for _, cand := range candidates {
		jobID, idErr := uuidv7(nowUTC)
		if idErr != nil {
			return 0, idErr
		}
		startUTC := cand.PeriodStart.UTC()
		endUTC := cand.PeriodEnd.UTC()
		startRFC := startUTC.Format(time.RFC3339)
		endRFC := endUTC.Format(time.RFC3339)
		payloadBytes, marErr := json.Marshal(recalcJobPayload{
			BindingID:   cand.BindingID,
			PeriodStart: startRFC,
			PeriodEnd:   endRFC,
		})
		if marErr != nil {
			return 0, marErr
		}
		dedupKey := fmt.Sprintf("metric-recalc:%s:%s:%s:%d", cand.BindingID, startRFC, endRFC, cand.MaxSeq)
		tz := strings.TrimSpace(cand.Timezone)
		var scheduleTZ any
		if tz != "" {
			scheduleTZ = tz
		}
		tag, execErr := tx.Exec(ctx, `INSERT INTO core_registry.job_instances (
  job_id, trigger_type, job_type, tenant_id, site_id,
  subject_type, subject_id, scheduled_for, schedule_timezone,
  priority, dedup_key, payload, state, max_attempts, timeout_seconds,
  created_at, updated_at
)
SELECT
  $1::uuid, 'BACKFILL', 'METRIC_RECALC', $2::uuid, $3::uuid,
  NULLIF($4, ''), NULLIF($5, ''), $6, $7,
  $8, $9, $10::jsonb, 'READY', $11, $12,
  $13, $13
WHERE NOT EXISTS (
  SELECT 1
  FROM core_registry.job_instances j
  WHERE j.job_type = 'METRIC_RECALC'
    AND j.state IN ('PENDING','READY','RETRY_WAIT')
    AND j.tenant_id = $2::uuid
    AND j.site_id = $3::uuid
    AND j.payload->>'bindingId' = $14
    AND NULLIF(j.payload->>'periodStart', '')::timestamptz = $15
    AND NULLIF(j.payload->>'periodEnd', '')::timestamptz = $6
)
ON CONFLICT (dedup_key) DO NOTHING`,
			jobID, cand.TenantID, cand.SiteID,
			cand.SubjectType, cand.SubjectID, endUTC, scheduleTZ,
			defaultBackfillJobPriority, dedupKey, string(payloadBytes), defaultBackfillJobMaxAttempt, defaultBackfillJobTimeoutSec,
			nowUTC, cand.BindingID, startUTC,
		)
		if execErr != nil {
			return 0, fmt.Errorf("enqueue metric recalc job: %w", execErr)
		}
		enqueued += int(tag.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return enqueued, nil
}

// ClickHouseBackfillSource reads incremental late-arrival observations from ClickHouse.
type ClickHouseBackfillSource struct {
	base           *url.URL
	user, password string
	client         *http.Client
}

// NewClickHouseBackfillSource constructs an HTTP reader for ClickHouse observation backfills.
func NewClickHouseBackfillSource(baseURL, user, password string, client *http.Client) (*ClickHouseBackfillSource, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("metric ClickHouse URL must be absolute http/https")
	}
	if client == nil {
		client = newDefaultClickHouseHTTPClient(15 * time.Second)
	}
	return &ClickHouseBackfillSource{base: u, user: user, password: password, client: client}, nil
}

func (s *ClickHouseBackfillSource) query(ctx context.Context, sql string) ([]byte, error) {
	return doClickHouseHTTPQuery(ctx, s.base, s.user, s.password, s.client, http.MethodGet, sql, 4<<20, "metric backfill ClickHouse query")
}

// MaxHistorySequence returns the maximum observed history_sequence in telemetry_history.observations.
func (s *ClickHouseBackfillSource) MaxHistorySequence(ctx context.Context) (uint64, error) {
	body, err := s.query(ctx, "SELECT coalesce(max(history_sequence), 0) FROM telemetry_history.observations FORMAT TabSeparated")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed == `\N` {
		return 0, nil
	}
	seq, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ClickHouse max history_sequence: %w", err)
	}
	return seq, nil
}

type flexUint64 uint64

func (v *flexUint64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*v = 0
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return err
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(str), 10, 64)
		if err != nil {
			return err
		}
		*v = flexUint64(parsed)
		return nil
	}
	parsed, err := strconv.ParseUint(string(trimmed), 10, 64)
	if err != nil {
		return err
	}
	*v = flexUint64(parsed)
	return nil
}

type clickHouseDirtyBucketRow struct {
	MaxSeq      flexUint64 `json:"max_seq"`
	TenantID    string     `json:"tenant_id"`
	SiteID      string     `json:"site_id"`
	PointID     string     `json:"point_id"`
	DirtyBucket string     `json:"dirty_bucket"`
}

// ListBackfillDirtyBuckets fetches distinct 15-minute UTC observation buckets with late arrivals
// strictly above afterSeq and up to throughSeq.
func (s *ClickHouseBackfillSource) ListBackfillDirtyBuckets(
	ctx context.Context,
	afterSeq, throughSeq uint64,
	minLagSeconds int64,
	limit int,
) ([]BackfillDirtyBucket, error) {
	where := fmt.Sprintf("obs.history_sequence > %d AND obs.history_sequence <= %d", afterSeq, throughSeq)
	return s.listDirtyBuckets(ctx, where, minLagSeconds, "", "max_seq ASC", limit)
}

// ListBackfillDirtyBucketsAtSequence paginates dirty buckets within a single history_sequence.
func (s *ClickHouseBackfillSource) ListBackfillDirtyBucketsAtSequence(
	ctx context.Context,
	seq uint64,
	minLagSeconds int64,
	afterKey *BackfillBucketCursor,
	limit int,
) ([]BackfillDirtyBucket, error) {
	where := fmt.Sprintf("obs.history_sequence = %d", seq)
	having := ""
	if afterKey != nil {
		having = fmt.Sprintf(
			"HAVING (tenant_key, site_key, point_key, dirty_bucket) > (%s, %s, %s, toStartOfInterval(parseDateTimeBestEffort(%s, 'UTC'), INTERVAL 15 MINUTE, 'UTC'))\n",
			quote(afterKey.TenantID),
			quote(afterKey.SiteID),
			quote(afterKey.PointID),
			quote(afterKey.BucketStart.UTC().Format(time.RFC3339)),
		)
	}
	return s.listDirtyBuckets(ctx, where, minLagSeconds, having, "tenant_key ASC, site_key ASC, point_key ASC, dirty_bucket ASC", limit)
}

func (s *ClickHouseBackfillSource) listDirtyBuckets(
	ctx context.Context,
	seqPredicate string,
	minLagSeconds int64,
	havingClause string,
	orderByClause string,
	limit int,
) ([]BackfillDirtyBucket, error) {
	if limit <= 0 {
		limit = defaultBackfillPageLimit
	}
	sql := fmt.Sprintf(`SELECT
  max_seq,
  tenant_key AS tenant_id,
  site_key AS site_id,
  point_key AS point_id,
  dirty_bucket
FROM (
  SELECT
    max(obs.history_sequence) AS max_seq,
    toString(assumeNotNull(obs.tenant_id)) AS tenant_key,
    toString(assumeNotNull(obs.site_id)) AS site_key,
    toString(assumeNotNull(obs.point_id)) AS point_key,
    toStartOfInterval(obs.sampled_at, INTERVAL 15 MINUTE, 'UTC') AS dirty_bucket
  FROM telemetry_history.observations AS obs
  WHERE %s
    AND obs.acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
    AND obs.tenant_id IS NOT NULL AND obs.site_id IS NOT NULL AND obs.point_id IS NOT NULL
    AND dateDiff('second', obs.sampled_at, obs.projected_at) >= %d
  GROUP BY tenant_key, site_key, point_key, dirty_bucket
  %s
)
ORDER BY %s
LIMIT %d
FORMAT JSONEachRow`, seqPredicate, minLagSeconds, havingClause, orderByClause, limit)
	return s.queryDirtyBucketRows(ctx, sql)
}

func (s *ClickHouseBackfillSource) queryDirtyBucketRows(ctx context.Context, sql string) ([]BackfillDirtyBucket, error) {
	body, err := s.query(ctx, sql)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var out []BackfillDirtyBucket
	for {
		var row clickHouseDirtyBucketRow
		if err := dec.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse backfill dirty bucket: %w", err)
		}
		bucketTime, err := parseClickHouseBucketTime(row.DirtyBucket)
		if err != nil {
			return nil, fmt.Errorf("parse ClickHouse dirty_bucket %q: %w", row.DirtyBucket, err)
		}
		out = append(out, BackfillDirtyBucket{
			MaxSeq:      uint64(row.MaxSeq),
			TenantID:    strings.TrimSpace(row.TenantID),
			SiteID:      strings.TrimSpace(row.SiteID),
			PointID:     strings.TrimSpace(row.PointID),
			BucketStart: bucketTime,
		})
	}
	return out, nil
}

func parseClickHouseBucketTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unsupported ClickHouse timestamp format")
}
