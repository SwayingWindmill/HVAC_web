package energy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/fixedpoint"
	"github.com/quanlaihe/hvac-web/libs/tariffmodel"
)

const maxTariffLookupSpan = 7 * 24 * time.Hour

// HourlyBucketKey identifies a distinct (tenant, site, point, UTC hour) scope whose
// hourly v2 rollup is recomputed from visible interval facts.
type HourlyBucketKey struct {
	TenantID string
	SiteID   string
	PointID  string
	Hour     time.Time
}

func (k HourlyBucketKey) normalized() HourlyBucketKey {
	return HourlyBucketKey{
		TenantID: strings.TrimSpace(k.TenantID),
		SiteID:   strings.TrimSpace(k.SiteID),
		PointID:  strings.TrimSpace(k.PointID),
		Hour:     k.Hour.UTC().Truncate(time.Hour),
	}
}

// ExistingHourlyV2Key describes the latest visible row for one key in
// analytics.energy_hourly_facts_v2 so superseded dimensions can be tombstoned and
// calc_version remains strictly monotonic.
type ExistingHourlyV2Key struct {
	TenantID        string
	SiteID          string
	EnergyType      string
	AccountingScope string
	LoadDeviceID    string
	PointID         string
	Quality         string
	TariffPeriod    string
	Hour            time.Time
	IntervalCount   int64
	CalcVersion     uint64
	DataWatermark   time.Time
	DatasetRevision uint64
}

// HourlyIntervalSource reads all visible interval facts belonging to touched
// (tenant, site, point, UTC hour) buckets for authoritative full-hour rollups.
type HourlyIntervalSource interface {
	VisibleIntervalsForHours(ctx context.Context, buckets []HourlyBucketKey) ([]EnergyIntervalFact, error)
}

// HourlyV2StateSource optionally reads existing energy_hourly_facts_v2 state for touched
// buckets to preserve monotonic calc_version and tombstone superseded keys.
type HourlyV2StateSource interface {
	ExistingHourlyFactsV2(ctx context.Context, buckets []HourlyBucketKey) ([]ExistingHourlyV2Key, error)
}

// PriorFactIntervalSource optionally reads superseded revisions of corrected facts so
// hourly buckets vacated by a revision change are also recomputed.
type PriorFactIntervalSource interface {
	PriorIntervalsForCorrections(ctx context.Context, facts []EnergyIntervalFact) ([]EnergyIntervalFact, error)
}

// TariffWindowSource reads visible UTC TOU tariff windows overlapping [from, to) for a site.
type TariffWindowSource interface {
	TariffPriceWindows(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]tariffmodel.Window, error)
}

// HourlyBackfillQuery scopes an idempotent UTC-hour backfill of analytics.energy_hourly_facts_v2.
type HourlyBackfillQuery struct {
	Since    time.Time
	Until    time.Time
	TenantID string
	SiteID   string
}

// HourlyBackfillSource lists touched (tenant, site, point, UTC hour) buckets for backfill.
type HourlyBackfillSource interface {
	ListHourlyBackfillBuckets(ctx context.Context, query HourlyBackfillQuery) ([]HourlyBucketKey, error)
}

// HourlyV2BackfillConfig configures an idempotent backfill run into analytics.energy_hourly_facts_v2.
type HourlyV2BackfillConfig struct {
	IntervalSource     HourlyIntervalSource
	BucketSource       HourlyBackfillSource
	HourlyV2Sink       HourlyV2Sink
	LoadProfileSource  LoadProfileSource
	TariffWindowSource TariffWindowSource
	Now                func() time.Time
	Since              time.Time
	Until              time.Time
	TenantID           string
	SiteID             string
}

// HourlyV2BackfillResult summarizes an executed backfill run.
type HourlyV2BackfillResult struct {
	BucketsProcessed int
	RowsWritten      int
}

type hourlyV2GroupKey struct {
	tenantID        string
	siteID          string
	energyType      string
	accountingScope string
	loadDeviceID    string
	pointID         string
	quality         string
	tariffPeriod    string
	hourUnix        int64
}

type hourlyV2Accumulator struct {
	key             hourlyV2GroupKey
	hour            time.Time
	energy          fixedpoint.Decimal6
	cost            fixedpoint.Decimal6
	intervalCount   int64
	dataWatermark   time.Time
	datasetRevision uint64
}

type loadProfileCacheKey struct {
	tenantID string
	siteID   string
	deviceID string
	fromUnix int64
	toUnix   int64
}

type hourlyFallbackMeta struct {
	energyType      string
	accountingScope string
	loadDeviceID    string
	quality         string
	dataWatermark   time.Time
	datasetRevision uint64
}

type tariffSiteKey struct {
	tenantID string
	siteID   string
}

type timeSpan struct {
	from time.Time
	to   time.Time
}

func (p *Projector) projectHourlyV2(ctx context.Context, facts []EnergyIntervalFact, projectedAt time.Time) error {
	if p.hourlyV2Sink == nil || len(facts) == 0 {
		return nil
	}
	v2Rows, err := p.buildHourlyFactsV2(ctx, facts, projectedAt)
	if err != nil || len(v2Rows) == 0 {
		return err
	}
	return p.writeHourlyV2Rows(ctx, v2Rows)
}

func (p *Projector) writeHourlyV2Rows(ctx context.Context, rows []HourlyFactV2) error {
	for start := 0; start < len(rows); start += maxHourlyV2InsertBatch {
		end := min(start+maxHourlyV2InsertBatch, len(rows))
		if err := p.hourlyV2Sink.InsertHourlyFactsV2(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Projector) buildHourlyFactsV2(ctx context.Context, facts []EnergyIntervalFact, projectedAt time.Time) ([]HourlyFactV2, error) {
	if len(facts) == 0 {
		return nil, nil
	}
	if p.hourlyIntervalSource == nil {
		// Nil source is used only by unit tests that verify single-batch projection without a ClickHouse reader.
		return p.aggregateHourlyFactsV2(ctx, facts, nil, nil, nil, projectedAt)
	}

	buckets, fallbackMeta, err := p.collectTouchedHourlyBuckets(ctx, facts)
	if err != nil || len(buckets) == 0 {
		return nil, err
	}
	return p.buildHourlyFactsV2ForBuckets(ctx, buckets, fallbackMeta, projectedAt)
}

func (p *Projector) buildHourlyFactsV2ForBuckets(
	ctx context.Context,
	buckets []HourlyBucketKey,
	fallbackMeta map[HourlyBucketKey]hourlyFallbackMeta,
	projectedAt time.Time,
) ([]HourlyFactV2, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	normalizedBuckets := deduplicateSortedBuckets(buckets)
	touchedSet := make(map[HourlyBucketKey]struct{}, len(normalizedBuckets))
	for _, b := range normalizedBuckets {
		touchedSet[b] = struct{}{}
	}

	visibleFacts, err := p.hourlyIntervalSource.VisibleIntervalsForHours(ctx, normalizedBuckets)
	if err != nil {
		return nil, fmt.Errorf("read visible intervals for hourly v2: %w", err)
	}

	var existingRows []ExistingHourlyV2Key
	if stateSrc, ok := p.hourlyIntervalSource.(HourlyV2StateSource); ok {
		existingRows, err = stateSrc.ExistingHourlyFactsV2(ctx, normalizedBuckets)
		if err != nil {
			return nil, fmt.Errorf("read existing hourly v2 state: %w", err)
		}
	}

	return p.aggregateHourlyFactsV2(ctx, visibleFacts, touchedSet, existingRows, fallbackMeta, projectedAt)
}

func (p *Projector) collectTouchedHourlyBuckets(
	ctx context.Context,
	facts []EnergyIntervalFact,
) ([]HourlyBucketKey, map[HourlyBucketKey]hourlyFallbackMeta, error) {
	bucketSet := make(map[HourlyBucketKey]struct{}, len(facts))
	fallbackMeta := make(map[HourlyBucketKey]hourlyFallbackMeta, len(facts))

	addFactBuckets := func(fact EnergyIntervalFact, recordFallback bool) {
		hours := factTouchedHours(fact)
		if len(hours) == 0 {
			return
		}
		loadDeviceID := normalizedLoadDeviceID(fact.LoadDeviceID, fact.AccountingScope)
		for _, hour := range hours {
			bucket := HourlyBucketKey{
				TenantID: fact.TenantID,
				SiteID:   fact.SiteID,
				PointID:  fact.PointID,
				Hour:     hour,
			}.normalized()
			bucketSet[bucket] = struct{}{}
			if recordFallback || fact.FactRevision > 0 {
				if _, exists := fallbackMeta[bucket]; !exists {
					fallbackMeta[bucket] = hourlyFallbackMeta{
						energyType:      fact.EnergyType,
						accountingScope: fact.AccountingScope,
						loadDeviceID:    loadDeviceID,
						quality:         fact.Quality,
						dataWatermark:   fact.DataWatermark.UTC(),
						datasetRevision: fact.DatasetRevision,
					}
				}
			}
		}
	}

	var corrections []EnergyIntervalFact
	for _, fact := range facts {
		span := fact.PeriodEnd.Sub(fact.PeriodStart)
		addFactBuckets(fact, span <= time.Hour)
		if fact.FactRevision > 0 {
			corrections = append(corrections, fact)
		}
	}

	if len(corrections) > 0 && p.hourlyIntervalSource != nil {
		if priorSrc, ok := p.hourlyIntervalSource.(PriorFactIntervalSource); ok {
			priorFacts, err := priorSrc.PriorIntervalsForCorrections(ctx, corrections)
			if err != nil {
				return nil, nil, fmt.Errorf("read prior revisions for hourly v2 corrections: %w", err)
			}
			for _, prior := range priorFacts {
				addFactBuckets(prior, true)
			}
		}
	}

	buckets := make([]HourlyBucketKey, 0, len(bucketSet))
	for b := range bucketSet {
		buckets = append(buckets, b)
	}
	sortHourlyBuckets(buckets)
	return buckets, fallbackMeta, nil
}

func factTouchedHours(fact EnergyIntervalFact) []time.Time {
	if fact.PeriodStart.IsZero() || fact.PeriodEnd.IsZero() || !fact.PeriodStart.Before(fact.PeriodEnd) {
		return nil
	}
	span := fact.PeriodEnd.Sub(fact.PeriodStart)
	if span <= time.Hour {
		return []time.Time{fact.PeriodEnd.UTC().Add(-time.Millisecond).Truncate(time.Hour)}
	}
	if fact.Quality != FactQualityValid || fact.EnergyKWh < 0 {
		return nil
	}
	return overlappingHoursWithinLimit(fact.PeriodStart.UTC(), fact.PeriodEnd.UTC(), MaxCrossGapAllocationHours)
}

func overlappingHoursWithinLimit(startUTC, endUTC time.Time, maxHours int) []time.Time {
	if maxHours <= 0 {
		maxHours = MaxCrossGapAllocationHours
	}
	var hours []time.Time
	for h := startUTC.Truncate(time.Hour); h.Before(endUTC); h = h.Add(time.Hour) {
		slotEnd := h.Add(time.Hour)
		auditStart := h
		if startUTC.After(auditStart) {
			auditStart = startUTC
		}
		auditEnd := slotEnd
		if endUTC.Before(auditEnd) {
			auditEnd = endUTC
		}
		if auditEnd.Sub(auditStart) <= 0 {
			continue
		}
		if len(hours) >= maxHours {
			return nil
		}
		hours = append(hours, h)
	}
	return hours
}

func (p *Projector) aggregateHourlyFactsV2(
	ctx context.Context,
	sourceFacts []EnergyIntervalFact,
	touchedSet map[HourlyBucketKey]struct{},
	existingRows []ExistingHourlyV2Key,
	fallbackMeta map[HourlyBucketKey]hourlyFallbackMeta,
	projectedAt time.Time,
) ([]HourlyFactV2, error) {
	projectedUTC := projectedAt.UTC()
	tariffsBySite, err := p.loadTariffWindowsForFacts(ctx, sourceFacts, touchedSet)
	if err != nil {
		return nil, err
	}

	accumulators := make(map[hourlyV2GroupKey]*hourlyV2Accumulator, len(sourceFacts))
	profileCache := make(map[loadProfileCacheKey][]HourlyLoadWeight)
	bucketHasOutput := make(map[HourlyBucketKey]bool, len(touchedSet))

	addBucket := func(
		fact EnergyIntervalFact,
		loadDeviceID string,
		quality string,
		hour time.Time,
		deltaEnergy fixedpoint.Decimal6,
		tariffAnchor time.Time,
	) error {
		hourUTC := hour.UTC().Truncate(time.Hour)
		bucketKey := HourlyBucketKey{
			TenantID: fact.TenantID,
			SiteID:   fact.SiteID,
			PointID:  fact.PointID,
			Hour:     hourUTC,
		}.normalized()
		if touchedSet != nil {
			if _, ok := touchedSet[bucketKey]; !ok {
				return nil
			}
		}

		tariffPeriod := ""
		deltaCost := fixedpoint.Zero
		if fact.EnergyType == EnergyTypeElectricity {
			windows := tariffsBySite[tariffSiteKey{tenantID: bucketKey.TenantID, siteID: bucketKey.SiteID}]
			if w, matched := lookupTariffWindowAt(windows, tariffAnchor); matched {
				if math.IsNaN(w.EnergyRatePerKWh) || math.IsInf(w.EnergyRatePerKWh, 0) || w.EnergyRatePerKWh < 0 {
					return fmt.Errorf("invalid tariff rate %v for site %s period %q", w.EnergyRatePerKWh, bucketKey.SiteID, w.PeriodCode)
				}
				rateDec, err := fixedpoint.FromFloat64(w.EnergyRatePerKWh)
				if err != nil {
					return fmt.Errorf("convert tariff rate for site %s: %w", bucketKey.SiteID, err)
				}
				deltaCost, err = deltaEnergy.Mul(rateDec)
				if err != nil {
					return fmt.Errorf("calculate hourly v2 cost for observation %s: %w", fact.CurrentObservationID, err)
				}
				tariffPeriod = strings.TrimSpace(w.PeriodCode)
			}
		}

		key := hourlyV2GroupKey{
			tenantID:        bucketKey.TenantID,
			siteID:          bucketKey.SiteID,
			energyType:      fact.EnergyType,
			accountingScope: fact.AccountingScope,
			loadDeviceID:    loadDeviceID,
			pointID:         bucketKey.PointID,
			quality:         quality,
			tariffPeriod:    tariffPeriod,
			hourUnix:        hourUTC.Unix(),
		}
		acc := accumulators[key]
		if acc == nil {
			acc = &hourlyV2Accumulator{
				key:           key,
				hour:          hourUTC,
				dataWatermark: fact.DataWatermark.UTC(),
			}
			accumulators[key] = acc
		}
		sumEnergy, err := acc.energy.Add(deltaEnergy)
		if err != nil {
			return err
		}
		sumCost, err := acc.cost.Add(deltaCost)
		if err != nil {
			return err
		}
		acc.energy = sumEnergy
		acc.cost = sumCost
		acc.intervalCount++
		if wm := fact.DataWatermark.UTC(); wm.After(acc.dataWatermark) {
			acc.dataWatermark = wm
		}
		if fact.DatasetRevision > acc.datasetRevision {
			acc.datasetRevision = fact.DatasetRevision
		}
		bucketHasOutput[bucketKey] = true
		return nil
	}

	for _, fact := range sourceFacts {
		loadDeviceID := normalizedLoadDeviceID(fact.LoadDeviceID, fact.AccountingScope)
		span := fact.PeriodEnd.Sub(fact.PeriodStart)
		if span <= time.Hour {
			delta, err := fixedpoint.FromFloat64(fact.EnergyKWh)
			if err != nil {
				return nil, fmt.Errorf("convert hourly v2 energy_kwh for observation %s: %w", fact.CurrentObservationID, err)
			}
			anchor := fact.PeriodEnd.UTC().Add(-time.Millisecond)
			hour := anchor.Truncate(time.Hour)
			if err := addBucket(fact, loadDeviceID, fact.Quality, hour, delta, anchor); err != nil {
				return nil, fmt.Errorf("accumulate hourly v2 energy_kwh for observation %s: %w", fact.CurrentObservationID, err)
			}
			continue
		}
		if fact.Quality != FactQualityValid || fact.EnergyKWh < 0 {
			continue
		}
		totalDecimal, err := fixedpoint.FromFloat64(fact.EnergyKWh)
		if err != nil {
			return nil, fmt.Errorf("convert cross-gap v2 energy_kwh for observation %s: %w", fact.CurrentObservationID, err)
		}
		profile := p.lookupCrossGapLoadProfile(ctx, fact, loadDeviceID, profileCache)
		allocRes, err := AllocateCrossGapEnergy(CrossGapAllocationRequest{
			TenantID:       fact.TenantID,
			SiteID:         fact.SiteID,
			PointID:        fact.PointID,
			EnergyType:     fact.EnergyType,
			PeriodStart:    fact.PeriodStart.UTC(),
			PeriodEnd:      fact.PeriodEnd.UTC(),
			TotalEnergyKWh: totalDecimal,
			LoadProfile:    profile,
		})
		if err != nil {
			return nil, fmt.Errorf("allocate cross-gap hourly v2 energy for observation %s: %w", fact.CurrentObservationID, err)
		}
		for _, alloc := range allocRes.Allocations {
			anchor := alloc.AuditEnd.UTC().Add(-time.Millisecond)
			if alloc.AuditEnd.IsZero() {
				anchor = alloc.HourStart.UTC()
			}
			if err := addBucket(fact, loadDeviceID, alloc.Quality, alloc.HourStart, alloc.EnergyKWh, anchor); err != nil {
				return nil, fmt.Errorf("accumulate cross-gap hourly v2 energy for observation %s: %w", fact.CurrentObservationID, err)
			}
		}
	}

	var maxExistingCalcVersion uint64
	for _, ex := range existingRows {
		if ex.CalcVersion > maxExistingCalcVersion {
			maxExistingCalcVersion = ex.CalcVersion
		}
	}
	calcVersion := p.allocateCalcVersion(projectedUTC, maxExistingCalcVersion)

	rows := make([]HourlyFactV2, 0, len(accumulators)+len(existingRows))
	for _, acc := range accumulators {
		rows = append(rows, HourlyFactV2{
			TenantID:        acc.key.tenantID,
			SiteID:          acc.key.siteID,
			EnergyType:      acc.key.energyType,
			AccountingScope: acc.key.accountingScope,
			LoadDeviceID:    acc.key.loadDeviceID,
			PointID:         acc.key.pointID,
			Quality:         acc.key.quality,
			TariffPeriod:    acc.key.tariffPeriod,
			Hour:            acc.hour,
			EnergyKWh:       acc.energy.Float64(),
			CostCNY:         acc.cost.Float64(),
			IntervalCount:   acc.intervalCount,
			CalcVersion:     calcVersion,
			DataWatermark:   nonZeroWatermark(acc.dataWatermark, acc.hour, projectedUTC),
			DatasetRevision: acc.datasetRevision,
			UpdatedAt:       projectedUTC,
		})
	}

	bucketHadExistingNonZero := make(map[HourlyBucketKey]bool, len(existingRows))
	seenTombstones := make(map[hourlyV2GroupKey]struct{}, len(existingRows))
	for _, ex := range existingRows {
		hourUTC := ex.Hour.UTC().Truncate(time.Hour)
		exBucket := HourlyBucketKey{
			TenantID: ex.TenantID,
			SiteID:   ex.SiteID,
			PointID:  ex.PointID,
			Hour:     hourUTC,
		}.normalized()
		if touchedSet != nil {
			if _, ok := touchedSet[exBucket]; !ok {
				continue
			}
		}
		if ex.IntervalCount == 0 {
			continue
		}
		bucketHadExistingNonZero[exBucket] = true
		loadDeviceID := normalizeStoredLoadDeviceID(ex.LoadDeviceID, ex.AccountingScope)
		key := hourlyV2GroupKey{
			tenantID:        exBucket.TenantID,
			siteID:          exBucket.SiteID,
			energyType:      ex.EnergyType,
			accountingScope: ex.AccountingScope,
			loadDeviceID:    loadDeviceID,
			pointID:         exBucket.PointID,
			quality:         ex.Quality,
			tariffPeriod:    ex.TariffPeriod,
			hourUnix:        hourUTC.Unix(),
		}
		if accumulators[key] != nil {
			continue
		}
		if _, dup := seenTombstones[key]; dup {
			continue
		}
		seenTombstones[key] = struct{}{}
		rows = append(rows, HourlyFactV2{
			TenantID:        key.tenantID,
			SiteID:          key.siteID,
			EnergyType:      key.energyType,
			AccountingScope: key.accountingScope,
			LoadDeviceID:    key.loadDeviceID,
			PointID:         key.pointID,
			Quality:         key.quality,
			TariffPeriod:    key.tariffPeriod,
			Hour:            hourUTC,
			EnergyKWh:       0,
			CostCNY:         0,
			IntervalCount:   0,
			CalcVersion:     calcVersion,
			DataWatermark:   nonZeroWatermark(ex.DataWatermark, hourUTC, projectedUTC),
			DatasetRevision: ex.DatasetRevision,
			UpdatedAt:       projectedUTC,
		})
	}

	if touchedSet != nil && len(fallbackMeta) > 0 {
		for bucket := range touchedSet {
			if bucketHasOutput[bucket] || bucketHadExistingNonZero[bucket] {
				continue
			}
			meta, ok := fallbackMeta[bucket]
			if !ok {
				continue
			}
			rows = append(rows, HourlyFactV2{
				TenantID:        bucket.TenantID,
				SiteID:          bucket.SiteID,
				EnergyType:      meta.energyType,
				AccountingScope: meta.accountingScope,
				LoadDeviceID:    meta.loadDeviceID,
				PointID:         bucket.PointID,
				Quality:         meta.quality,
				TariffPeriod:    "",
				Hour:            bucket.Hour,
				EnergyKWh:       0,
				CostCNY:         0,
				IntervalCount:   0,
				CalcVersion:     calcVersion,
				DataWatermark:   nonZeroWatermark(meta.dataWatermark, bucket.Hour, projectedUTC),
				DatasetRevision: meta.datasetRevision,
				UpdatedAt:       projectedUTC,
			})
		}
	}

	sortHourlyFactsV2(rows)
	return rows, nil
}

func (p *Projector) allocateCalcVersion(projectedUTC time.Time, minExisting uint64) uint64 {
	var candidate uint64 = 1
	if millis := projectedUTC.UnixMilli(); millis > 0 {
		candidate = uint64(millis)
	}
	if minExisting >= candidate {
		candidate = minExisting + 1
	}
	for {
		prev := p.lastCalcVersion.Load()
		next := candidate
		if prev >= next {
			next = prev + 1
		}
		if p.lastCalcVersion.CompareAndSwap(prev, next) {
			return next
		}
	}
}

func (p *Projector) loadTariffWindowsForFacts(
	ctx context.Context,
	facts []EnergyIntervalFact,
	touchedSet map[HourlyBucketKey]struct{},
) (map[tariffSiteKey][]tariffmodel.Window, error) {
	if p.tariffWindowSource == nil || len(facts) == 0 {
		return nil, nil
	}
	siteRanges := make(map[tariffSiteKey][]time.Time)
	for _, fact := range facts {
		if fact.EnergyType != EnergyTypeElectricity {
			continue
		}
		hours := factTouchedHours(fact)
		site := tariffSiteKey{
			tenantID: strings.TrimSpace(fact.TenantID),
			siteID:   strings.TrimSpace(fact.SiteID),
		}
		for _, h := range hours {
			if touchedSet != nil {
				bucket := HourlyBucketKey{
					TenantID: site.tenantID,
					SiteID:   site.siteID,
					PointID:  strings.TrimSpace(fact.PointID),
					Hour:     h,
				}
				if _, ok := touchedSet[bucket]; !ok {
					continue
				}
			}
			siteRanges[site] = append(siteRanges[site], h)
		}
	}
	if len(siteRanges) == 0 {
		return nil, nil
	}

	siteKeys := make([]tariffSiteKey, 0, len(siteRanges))
	for site := range siteRanges {
		siteKeys = append(siteKeys, site)
	}
	sort.Slice(siteKeys, func(i, j int) bool {
		if siteKeys[i].tenantID != siteKeys[j].tenantID {
			return siteKeys[i].tenantID < siteKeys[j].tenantID
		}
		return siteKeys[i].siteID < siteKeys[j].siteID
	})

	result := make(map[tariffSiteKey][]tariffmodel.Window, len(siteKeys))
	for _, site := range siteKeys {
		spans := mergeHourSpans(siteRanges[site], maxTariffLookupSpan)
		var allWindows []tariffmodel.Window
		for _, span := range spans {
			windows, err := p.tariffWindowSource.TariffPriceWindows(ctx, site.tenantID, site.siteID, span.from, span.to)
			if err != nil {
				return nil, fmt.Errorf("read tariff price windows for site %s: %w", site.siteID, err)
			}
			allWindows = append(allWindows, windows...)
		}
		sort.Slice(allWindows, func(i, j int) bool {
			if !allWindows[i].Start.Equal(allWindows[j].Start) {
				return allWindows[i].Start.Before(allWindows[j].Start)
			}
			return allWindows[i].End.Before(allWindows[j].End)
		})
		result[site] = allWindows
	}
	return result, nil
}

func mergeHourSpans(hours []time.Time, maxSpan time.Duration) []timeSpan {
	if len(hours) == 0 {
		return nil
	}
	sorted := append([]time.Time(nil), hours...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	var spans []timeSpan
	for _, h := range sorted {
		hUTC := h.UTC().Truncate(time.Hour)
		end := hUTC.Add(time.Hour)
		if len(spans) == 0 {
			spans = append(spans, timeSpan{from: hUTC, to: end})
			continue
		}
		last := &spans[len(spans)-1]
		if !end.After(last.to) {
			continue
		}
		if end.Sub(last.from) <= maxSpan {
			last.to = end
			continue
		}
		spans = append(spans, timeSpan{from: hUTC, to: end})
	}
	return spans
}

func lookupTariffWindowAt(windows []tariffmodel.Window, anchor time.Time) (tariffmodel.Window, bool) {
	if len(windows) == 0 {
		return tariffmodel.Window{}, false
	}
	idx := sort.Search(len(windows), func(i int) bool {
		return windows[i].Start.After(anchor)
	})
	if idx == 0 {
		return tariffmodel.Window{}, false
	}
	w := windows[idx-1]
	if w.End.After(w.Start) && anchor.Before(w.End) {
		return w, true
	}
	return tariffmodel.Window{}, false
}

func (p *Projector) lookupCrossGapLoadProfile(
	ctx context.Context,
	fact EnergyIntervalFact,
	loadDeviceID string,
	cache map[loadProfileCacheKey][]HourlyLoadWeight,
) []HourlyLoadWeight {
	if p.loadProfileSource == nil {
		return nil
	}
	from := fact.PeriodStart.UTC().Truncate(time.Hour)
	to := fact.PeriodEnd.UTC().Add(-time.Millisecond).Truncate(time.Hour).Add(time.Hour)
	if !from.Before(to) {
		return nil
	}

	candidates := make([]string, 0, 2)
	reportingDevice := strings.TrimSpace(fact.DeviceID)
	if reportingDevice != "" {
		candidates = append(candidates, reportingDevice)
	}
	if fact.AccountingScope == AccountingScopeBranch {
		if loadDeviceID != "" && loadDeviceID != reportingDevice {
			candidates = append(candidates, loadDeviceID)
		}
	} else {
		candidates = append(candidates, "")
	}
	if len(candidates) == 0 {
		candidates = append(candidates, "")
	}

	for _, deviceID := range candidates {
		cacheKey := loadProfileCacheKey{
			tenantID: fact.TenantID,
			siteID:   fact.SiteID,
			deviceID: deviceID,
			fromUnix: from.Unix(),
			toUnix:   to.Unix(),
		}
		profile, cached := cache[cacheKey]
		if !cached {
			loaded, err := p.loadProfileSource.HourlyPowerProfile(ctx, fact.TenantID, fact.SiteID, deviceID, from, to)
			if err != nil {
				// Empty/error load profile degrades gracefully to uniform split in AllocateCrossGapEnergy.
				loaded = nil
			}
			cache[cacheKey] = loaded
			profile = loaded
		}
		if len(profile) > 0 {
			return profile
		}
	}
	return nil
}

// BackfillHourlyV2 scans visible energy interval facts in UTC hours from cfg.Since onward
// using the exact same full-hour + TOU cost aggregation and writes authoritative rows into
// analytics.energy_hourly_facts_v2.
func BackfillHourlyV2(ctx context.Context, cfg HourlyV2BackfillConfig) (HourlyV2BackfillResult, error) {
	if cfg.IntervalSource == nil {
		return HourlyV2BackfillResult{}, errors.New("hourly v2 backfill interval source is required")
	}
	if cfg.BucketSource == nil {
		return HourlyV2BackfillResult{}, errors.New("hourly v2 backfill bucket source is required")
	}
	if cfg.HourlyV2Sink == nil {
		return HourlyV2BackfillResult{}, errors.New("hourly v2 backfill sink is required")
	}
	if cfg.Since.IsZero() {
		return HourlyV2BackfillResult{}, errors.New("hourly v2 backfill since timestamp is required")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	sinceHour := cfg.Since.UTC().Truncate(time.Hour)
	untilBound := cfg.Until.UTC()
	if untilBound.IsZero() {
		untilBound = nowFn().UTC().Truncate(time.Hour).Add(time.Hour)
	} else if !untilBound.After(sinceHour) {
		return HourlyV2BackfillResult{}, errors.New("hourly v2 backfill until must be after since")
	}

	p := &Projector{
		hourlyV2Sink:         cfg.HourlyV2Sink,
		hourlyIntervalSource: cfg.IntervalSource,
		loadProfileSource:    cfg.LoadProfileSource,
		tariffWindowSource:   cfg.TariffWindowSource,
		now:                  nowFn,
	}

	var result HourlyV2BackfillResult
	tenantID := strings.TrimSpace(cfg.TenantID)
	siteID := strings.TrimSpace(cfg.SiteID)

	for dayStart := sinceHour; dayStart.Before(untilBound); {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		dayEnd := dayStart.Add(24 * time.Hour)
		if dayEnd.After(untilBound) {
			dayEnd = untilBound
		}

		buckets, err := cfg.BucketSource.ListHourlyBackfillBuckets(ctx, HourlyBackfillQuery{
			Since:    dayStart,
			Until:    dayEnd,
			TenantID: tenantID,
			SiteID:   siteID,
		})
		if err != nil {
			return result, fmt.Errorf("list hourly v2 backfill buckets [%s, %s): %w", dayStart.Format(time.RFC3339), dayEnd.Format(time.RFC3339), err)
		}
		normalized := deduplicateSortedBuckets(buckets)
		for _, chunk := range chunkBackfillBucketsByHour(normalized, 128) {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			projectedAt := p.now().UTC()
			rows, err := p.buildHourlyFactsV2ForBuckets(ctx, chunk, nil, projectedAt)
			if err != nil {
				return result, fmt.Errorf("build hourly v2 backfill rows: %w", err)
			}
			if len(rows) > 0 {
				if err := p.writeHourlyV2Rows(ctx, rows); err != nil {
					return result, fmt.Errorf("write hourly v2 backfill rows: %w", err)
				}
				result.RowsWritten += len(rows)
			}
			result.BucketsProcessed += len(chunk)
		}
		dayStart = dayEnd
	}
	return result, nil
}

func chunkBackfillBucketsByHour(buckets []HourlyBucketKey, maxPerChunk int) [][]HourlyBucketKey {
	if len(buckets) == 0 {
		return nil
	}
	if maxPerChunk <= 0 {
		maxPerChunk = 128
	}
	var chunks [][]HourlyBucketKey
	start := 0
	for start < len(buckets) {
		end := start + 1
		hour := buckets[start].Hour
		for end < len(buckets) && end-start < maxPerChunk && buckets[end].Hour.Equal(hour) {
			end++
		}
		chunks = append(chunks, buckets[start:end])
		start = end
	}
	return chunks
}

func deduplicateSortedBuckets(buckets []HourlyBucketKey) []HourlyBucketKey {
	if len(buckets) == 0 {
		return nil
	}
	seen := make(map[HourlyBucketKey]struct{}, len(buckets))
	out := make([]HourlyBucketKey, 0, len(buckets))
	for _, b := range buckets {
		nb := b.normalized()
		if nb.TenantID == "" || nb.SiteID == "" || nb.PointID == "" || nb.Hour.IsZero() {
			continue
		}
		if _, exists := seen[nb]; exists {
			continue
		}
		seen[nb] = struct{}{}
		out = append(out, nb)
	}
	sortHourlyBuckets(out)
	return out
}

func sortHourlyBuckets(buckets []HourlyBucketKey) {
	sort.Slice(buckets, func(i, j int) bool {
		a, b := buckets[i], buckets[j]
		if !a.Hour.Equal(b.Hour) {
			return a.Hour.Before(b.Hour)
		}
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		return a.PointID < b.PointID
	})
}

func sortHourlyFactsV2(rows []HourlyFactV2) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.SiteID != b.SiteID {
			return a.SiteID < b.SiteID
		}
		if a.EnergyType != b.EnergyType {
			return a.EnergyType < b.EnergyType
		}
		if a.AccountingScope != b.AccountingScope {
			return a.AccountingScope < b.AccountingScope
		}
		if a.LoadDeviceID != b.LoadDeviceID {
			return a.LoadDeviceID < b.LoadDeviceID
		}
		if a.PointID != b.PointID {
			return a.PointID < b.PointID
		}
		if a.Quality != b.Quality {
			return a.Quality < b.Quality
		}
		if a.TariffPeriod != b.TariffPeriod {
			return a.TariffPeriod < b.TariffPeriod
		}
		return a.Hour.Before(b.Hour)
	})
}

func normalizedLoadDeviceID(ptr *string, accountingScope string) string {
	if accountingScope != AccountingScopeBranch || ptr == nil {
		return ""
	}
	return normalizeStoredLoadDeviceID(*ptr, accountingScope)
}

func normalizeStoredLoadDeviceID(raw string, accountingScope string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "00000000-0000-0000-0000-000000000000" || accountingScope != AccountingScopeBranch {
		return ""
	}
	return trimmed
}

func nonZeroWatermark(watermark, hour, fallback time.Time) time.Time {
	if !watermark.IsZero() {
		return watermark.UTC()
	}
	if !hour.IsZero() {
		return hour.UTC().Add(time.Hour)
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}
