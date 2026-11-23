package metric

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultRecalcClaimLimit        = 64
	maxRecalcClaimLimit            = 500
	defaultRecalcMaxExecPerSpan    = 500
	defaultReconcileScopeLimit     = 100
	defaultReconcilePublicationAge = 5 * time.Minute
)

// DirtyWindowRecord mirrors core_registry.energy_recalc_dirty_windows columns.
type DirtyWindowRecord struct {
	ID                 string
	TenantID           string
	SiteID             string
	Granularity        string
	WindowStart        time.Time
	WindowEnd          time.Time
	Status             string
	AvailableAt        time.Time
	RequestedBy        string
	RequestedByDisplay string
	Reason             string
	Source             string
	AttemptCount       int
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	FinishedAt         *time.Time
}

// TenantSiteScope identifies a scoped (tenant_id, site_id) pair for RLS-scoped operations.
type TenantSiteScope struct {
	TenantID string
	SiteID   string
}

// DirtyWindowStore reads/claims debounced dirty windows and updates lifecycle status under RLS.
type DirtyWindowStore interface {
	ClaimDueDirtyWindows(ctx context.Context, now time.Time, limit int) ([]DirtyWindowRecord, error)
	CompleteDirtyWindow(ctx context.Context, tenantID, siteID, windowID string, now time.Time) error
	FailDirtyWindow(ctx context.Context, tenantID, siteID, windowID, lastError string, now time.Time) error
	LoadSiteBackfillBindings(ctx context.Context, tenantID, siteID string, from, to time.Time) ([]BackfillBinding, error)
}

// RecalcExecutor computes and persists a metric binding for one completed period.
type RecalcExecutor interface {
	Execute(ctx context.Context, request RunRequest) (Result, error)
}

// ScopeReconciler reconciles stale PERSISTING cross_store_publications within a tenant/site scope.
type ScopeReconciler interface {
	ReconcileScope(ctx context.Context, tenantID, siteID string, staleBefore time.Time, limit int) (int, error)
}

// PublicationScopeLister discovers distinct tenant/site scopes with stale PERSISTING publications.
type PublicationScopeLister interface {
	ListStalePublicationScopes(ctx context.Context, staleBefore time.Time, limit int) ([]TenantSiteScope, error)
}

// RecalcWorkerConfig configures bounded debounced dirty-window recalculation.
type RecalcWorkerConfig struct {
	Store                DirtyWindowStore
	Executor             RecalcExecutor
	Reconciler           ScopeReconciler
	ClaimLimit           int
	MaxExecutionsPerSpan int
	Now                  func() time.Time
}

// RecalcBatchStats summarizes one ProcessOnce pass.
type RecalcBatchStats struct {
	Claimed       int
	CoalescedSpan int
	ExecutedRuns  int
	Succeeded     int
	Failed        int
}

// RecalcWorker claims due PENDING dirty windows, coalesces overlapping spans per
// (tenant_id, site_id, granularity), runs bounded metric recalculations, and records outcomes.
type RecalcWorker struct {
	store             DirtyWindowStore
	executor          RecalcExecutor
	reconciler        ScopeReconciler
	claimLimit        int
	maxExecsPerSpan   int
	now               func() time.Time
	staleReconcileAge time.Duration
}

// NewRecalcWorker constructs a RecalcWorker with bounded defaults.
func NewRecalcWorker(config RecalcWorkerConfig) (*RecalcWorker, error) {
	if config.Store == nil {
		return nil, errors.New("recalc worker dirty window store is required")
	}
	if config.Executor == nil {
		return nil, errors.New("recalc worker executor is required")
	}
	claimLimit := config.ClaimLimit
	if claimLimit <= 0 {
		claimLimit = defaultRecalcClaimLimit
	}
	if claimLimit > maxRecalcClaimLimit {
		claimLimit = maxRecalcClaimLimit
	}
	maxExecs := config.MaxExecutionsPerSpan
	if maxExecs <= 0 {
		maxExecs = defaultRecalcMaxExecPerSpan
	}
	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &RecalcWorker{
		store:             config.Store,
		executor:          config.Executor,
		reconciler:        config.Reconciler,
		claimLimit:        claimLimit,
		maxExecsPerSpan:   maxExecs,
		now:               nowFn,
		staleReconcileAge: defaultReconcilePublicationAge,
	}, nil
}

type coalescedDirtySpan struct {
	TenantID    string
	SiteID      string
	Granularity string
	WindowStart time.Time
	WindowEnd   time.Time
	Source      string
	Records     []DirtyWindowRecord
}

// ProcessOnce claims due PENDING dirty windows (available_at <= now), coalesces overlaps,
// executes bounded recalculation runs, and updates each record to SUCCEEDED or FAILED.
func (w *RecalcWorker) ProcessOnce(ctx context.Context) (RecalcBatchStats, error) {
	var stats RecalcBatchStats
	now := w.now().UTC()
	claimed, err := w.store.ClaimDueDirtyWindows(ctx, now, w.claimLimit)
	if err != nil {
		return stats, err
	}
	if len(claimed) == 0 {
		return stats, nil
	}
	stats.Claimed = len(claimed)

	spans := coalesceDirtyWindows(claimed)
	stats.CoalescedSpan = len(spans)

	var failures []error
	for _, span := range spans {
		runs, spanErr := w.processSpan(ctx, span, now)
		stats.ExecutedRuns += runs
		doneAt := w.now().UTC()
		if spanErr != nil {
			errMsg := truncateRunes(spanErr.Error(), 1000)
			for _, rec := range span.Records {
				if failErr := w.store.FailDirtyWindow(ctx, rec.TenantID, rec.SiteID, rec.ID, errMsg, doneAt); failErr != nil {
					failures = append(failures, failErr)
				} else {
					stats.Failed++
				}
			}
			failures = append(failures, fmt.Errorf("recalc span %s/%s (%s): %w", span.TenantID, span.SiteID, span.Granularity, spanErr))
			continue
		}
		for _, rec := range span.Records {
			if okErr := w.store.CompleteDirtyWindow(ctx, rec.TenantID, rec.SiteID, rec.ID, doneAt); okErr != nil {
				failures = append(failures, okErr)
			} else {
				stats.Succeeded++
			}
		}
	}
	return stats, errors.Join(failures...)
}

func coalesceDirtyWindows(records []DirtyWindowRecord) []coalescedDirtySpan {
	type groupKey struct {
		tenantID    string
		siteID      string
		granularity string
	}
	groups := make(map[groupKey][]DirtyWindowRecord)
	for _, rec := range records {
		key := groupKey{
			tenantID:    strings.ToLower(strings.TrimSpace(rec.TenantID)),
			siteID:      strings.ToLower(strings.TrimSpace(rec.SiteID)),
			granularity: strings.ToUpper(strings.TrimSpace(rec.Granularity)),
		}
		rec.WindowStart = rec.WindowStart.UTC()
		rec.WindowEnd = rec.WindowEnd.UTC()
		groups[key] = append(groups[key], rec)
	}

	var spans []coalescedDirtySpan
	for key, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			if !group[i].WindowStart.Equal(group[j].WindowStart) {
				return group[i].WindowStart.Before(group[j].WindowStart)
			}
			if !group[i].WindowEnd.Equal(group[j].WindowEnd) {
				return group[i].WindowEnd.Before(group[j].WindowEnd)
			}
			return group[i].ID < group[j].ID
		})
		for _, rec := range group {
			if n := len(spans); n > 0 &&
				spans[n-1].TenantID == key.tenantID &&
				spans[n-1].SiteID == key.siteID &&
				spans[n-1].Granularity == key.granularity &&
				!rec.WindowStart.After(spans[n-1].WindowEnd) {
				if rec.WindowEnd.After(spans[n-1].WindowEnd) {
					spans[n-1].WindowEnd = rec.WindowEnd
				}
				spans[n-1].Source = higherPriorityDirtySource(spans[n-1].Source, rec.Source)
				spans[n-1].Records = append(spans[n-1].Records, rec)
				continue
			}
			spans = append(spans, coalescedDirtySpan{
				TenantID:    key.tenantID,
				SiteID:      key.siteID,
				Granularity: key.granularity,
				WindowStart: rec.WindowStart,
				WindowEnd:   rec.WindowEnd,
				Source:      rec.Source,
				Records:     []DirtyWindowRecord{rec},
			})
		}
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].TenantID != spans[j].TenantID {
			return spans[i].TenantID < spans[j].TenantID
		}
		if spans[i].SiteID != spans[j].SiteID {
			return spans[i].SiteID < spans[j].SiteID
		}
		if spans[i].Granularity != spans[j].Granularity {
			return spans[i].Granularity < spans[j].Granularity
		}
		return spans[i].WindowStart.Before(spans[j].WindowStart)
	})
	return spans
}

func higherPriorityDirtySource(a, b string) string {
	rank := func(src string) int {
		switch strings.ToUpper(strings.TrimSpace(src)) {
		case "MANUAL":
			return 4
		case "RATIO_CHANGE":
			return 3
		case "TARIFF_CHANGE":
			return 2
		default:
			return 1
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func (w *RecalcWorker) processSpan(ctx context.Context, span coalescedDirtySpan, now time.Time) (int, error) {
	if span.TenantID == "" || span.SiteID == "" || !span.WindowEnd.After(span.WindowStart) {
		return 0, errors.New("dirty window span is invalid")
	}
	if w.reconciler != nil {
		if _, err := w.reconciler.ReconcileScope(ctx, span.TenantID, span.SiteID, now.Add(-w.staleReconcileAge), defaultReconcileScopeLimit); err != nil {
			return 0, fmt.Errorf("reconcile stale publications before recalc: %w", err)
		}
	}

	bindings, err := w.store.LoadSiteBackfillBindings(ctx, span.TenantID, span.SiteID, span.WindowStart, span.WindowEnd)
	if err != nil {
		return 0, err
	}
	filtered := make([]BackfillBinding, 0, len(bindings))
	for _, b := range bindings {
		if !granularityMatchesDirtyWindow(span.Granularity, b.Granularity) {
			continue
		}
		if !windowOverlaps(b.EffectiveFrom, b.EffectiveTo, span.WindowStart, span.WindowEnd) ||
			!windowOverlaps(b.VersionFrom, b.VersionTo, span.WindowStart, span.WindowEnd) {
			continue
		}
		filtered = append(filtered, b)
	}
	if len(filtered) == 0 {
		return 0, nil
	}

	orderedBindings := orderBindingsByDependencies(filtered)
	reason := runReasonForDirtySource(span.Source)
	locations := make(map[string]*time.Location)
	requests := make([]RunRequest, 0, len(orderedBindings))

	for _, b := range orderedBindings {
		tz := strings.TrimSpace(b.Timezone)
		if tz == "" {
			tz = "UTC"
		}
		loc, ok := locations[tz]
		if !ok {
			loaded, tzErr := time.LoadLocation(tz)
			if tzErr != nil {
				return 0, fmt.Errorf("load site timezone %q: %w", tz, tzErr)
			}
			loc = loaded
			locations[tz] = loc
		}
		periods := alignedPeriodsForBucket(b.Granularity, loc, span.WindowStart, span.WindowEnd)
		for _, p := range periods {
			if p.End.After(now) {
				continue
			}
			if !windowOverlaps(b.EffectiveFrom, b.EffectiveTo, p.Start, p.Start) ||
				!windowOverlaps(b.VersionFrom, b.VersionTo, p.Start, p.Start) {
				continue
			}
			requests = append(requests, RunRequest{
				TenantID:    span.TenantID,
				SiteID:      span.SiteID,
				BindingID:   b.BindingID,
				PeriodStart: p.Start,
				PeriodEnd:   p.End,
				Reason:      reason,
			})
			if len(requests) > w.maxExecsPerSpan {
				return 0, fmt.Errorf("recalculation period count exceeds limit %d", w.maxExecsPerSpan)
			}
		}
	}

	executed := 0
	for _, req := range requests {
		if _, execErr := w.executor.Execute(ctx, req); execErr != nil {
			if isNoDataMetricError(execErr) {
				continue
			}
			return executed, fmt.Errorf("execute binding %s [%s,%s): %w", req.BindingID, req.PeriodStart.Format(time.RFC3339), req.PeriodEnd.Format(time.RFC3339), execErr)
		}
		executed++
	}
	return executed, nil
}

func isNoDataMetricError(err error) bool {
	return errors.Is(err, ErrNoNumericObservations) ||
		errors.Is(err, ErrEmptyPointDependency) ||
		errors.Is(err, ErrMissingMetricDependency)
}

func granularityMatchesDirtyWindow(windowGranularity, bindingGranularity string) bool {
	bg := strings.ToUpper(strings.TrimSpace(bindingGranularity))
	if bg == "" || bg == "REALTIME" {
		return false
	}
	wg := strings.ToUpper(strings.TrimSpace(windowGranularity))
	return wg == "ALL" || bg == wg
}

func runReasonForDirtySource(source string) string {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "MANUAL":
		return "MANUAL"
	case "BACKFILL":
		return "BACKFILL"
	case "TARIFF_CHANGE", "RATIO_CHANGE":
		return "LATE_DATA"
	default:
		return "LATE_DATA"
	}
}

func orderBindingsByDependencies(bindings []BackfillBinding) []BackfillBinding {
	tiers := make(map[string]int, len(bindings))
	byMetric := make(map[string][]BackfillBinding)
	for _, b := range bindings {
		key := strings.ToLower(strings.TrimSpace(b.MetricID))
		if key != "" {
			byMetric[key] = append(byMetric[key], b)
		}
	}
	var visit func(b BackfillBinding, stack map[string]bool) int
	visit = func(b BackfillBinding, stack map[string]bool) int {
		if t, ok := tiers[b.BindingID]; ok {
			return t
		}
		if stack[b.BindingID] {
			return 0
		}
		stack[b.BindingID] = true
		maxDep := 0
		for _, depMetric := range b.DependencyMetricIDs {
			for _, provider := range byMetric[strings.ToLower(strings.TrimSpace(depMetric))] {
				if provider.BindingID == b.BindingID {
					continue
				}
				if pt := visit(provider, stack) + 1; pt > maxDep {
					maxDep = pt
				}
			}
		}
		delete(stack, b.BindingID)
		tiers[b.BindingID] = maxDep
		return maxDep
	}
	for _, b := range bindings {
		visit(b, map[string]bool{})
	}
	ordered := append([]BackfillBinding(nil), bindings...)
	sort.Slice(ordered, func(i, j int) bool {
		ti, tj := tiers[ordered[i].BindingID], tiers[ordered[j].BindingID]
		if ti != tj {
			return ti < tj
		}
		if ordered[i].Granularity != ordered[j].Granularity {
			return ordered[i].Granularity < ordered[j].Granularity
		}
		return ordered[i].BindingID < ordered[j].BindingID
	})
	return ordered
}

// ReconcileStalePublications scans stale PERSISTING scopes and repairs each via ReconcileScope.
func ReconcileStalePublications(ctx context.Context, lister PublicationScopeLister, reconciler ScopeReconciler, staleBefore time.Time, limit int) (int, error) {
	if lister == nil || reconciler == nil {
		return 0, errors.New("publication scope lister and reconciler are required")
	}
	if limit <= 0 || limit > 1000 {
		limit = defaultReconcileScopeLimit
	}
	scopes, err := lister.ListStalePublicationScopes(ctx, staleBefore.UTC(), limit)
	if err != nil {
		return 0, err
	}
	totalRepaired := 0
	var failures []error
	for _, sc := range scopes {
		repaired, recErr := reconciler.ReconcileScope(ctx, sc.TenantID, sc.SiteID, staleBefore.UTC(), limit)
		totalRepaired += repaired
		if recErr != nil {
			failures = append(failures, fmt.Errorf("reconcile scope %s/%s: %w", sc.TenantID, sc.SiteID, recErr))
		}
	}
	return totalRepaired, errors.Join(failures...)
}

func truncateRunes(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	var out strings.Builder
	count := 0
	for _, r := range trimmed {
		if count >= maxRunes {
			break
		}
		out.WriteRune(r)
		count++
	}
	return out.String()
}
