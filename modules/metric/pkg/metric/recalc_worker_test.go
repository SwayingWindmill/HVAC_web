package metric

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeDirtyWindowStore struct {
	due       []DirtyWindowRecord
	bindings  map[string][]BackfillBinding
	completed []string
	failed    map[string]string
	lastNow   time.Time
	lastLimit int
}

func (f *fakeDirtyWindowStore) ClaimDueDirtyWindows(_ context.Context, now time.Time, limit int) ([]DirtyWindowRecord, error) {
	f.lastNow = now
	f.lastLimit = limit
	var out []DirtyWindowRecord
	for _, rec := range f.due {
		if rec.Status == "PENDING" && !rec.AvailableAt.After(now) {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (f *fakeDirtyWindowStore) CompleteDirtyWindow(_ context.Context, _, _, windowID string, _ time.Time) error {
	f.completed = append(f.completed, windowID)
	return nil
}

func (f *fakeDirtyWindowStore) FailDirtyWindow(_ context.Context, _, _, windowID, lastError string, _ time.Time) error {
	if f.failed == nil {
		f.failed = map[string]string{}
	}
	f.failed[windowID] = lastError
	return nil
}

func (f *fakeDirtyWindowStore) LoadSiteBackfillBindings(_ context.Context, tenantID, siteID string, _, _ time.Time) ([]BackfillBinding, error) {
	return append([]BackfillBinding(nil), f.bindings[tenantID+"|"+siteID]...), nil
}

type fakeRecalcExecutor struct {
	requests []RunRequest
	failOn   string
}

func (f *fakeRecalcExecutor) Execute(_ context.Context, req RunRequest) (Result, error) {
	f.requests = append(f.requests, req)
	if f.failOn != "" && req.BindingID == f.failOn {
		return Result{}, errors.New("simulated recalc failure")
	}
	return Result{RunID: "run-1", ResultID: "res-1"}, nil
}

type fakeScopeReconciler struct {
	calls    []TenantSiteScope
	repaired map[string]int
	failSite string
}

func (f *fakeScopeReconciler) ReconcileScope(_ context.Context, tenantID, siteID string, _ time.Time, _ int) (int, error) {
	f.calls = append(f.calls, TenantSiteScope{TenantID: tenantID, SiteID: siteID})
	if f.failSite != "" && siteID == f.failSite {
		return 0, errors.New("reconcile failed")
	}
	return f.repaired[tenantID+"|"+siteID], nil
}

type fakePublicationScopeLister struct {
	scopes []TenantSiteScope
}

func (f *fakePublicationScopeLister) ListStalePublicationScopes(context.Context, time.Time, int) ([]TenantSiteScope, error) {
	return append([]TenantSiteScope(nil), f.scopes...), nil
}

func TestRecalcWorkerCoalescesOverlapsOrdersDependenciesAndCompletes(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	tenant := "018f4d00-0000-7000-8000-000000000001"
	site := "018f4e00-1000-7000-8000-000000000001"
	baseEff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	store := &fakeDirtyWindowStore{
		due: []DirtyWindowRecord{
			{
				ID:          "win-1",
				TenantID:    tenant,
				SiteID:      site,
				Granularity: "HOUR",
				WindowStart: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
				WindowEnd:   time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
				Status:      "PENDING",
				AvailableAt: now.Add(-10 * time.Second),
				Source:      "BACKFILL",
			},
			{
				ID:          "win-2",
				TenantID:    tenant,
				SiteID:      site,
				Granularity: "HOUR",
				WindowStart: time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC),
				WindowEnd:   time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
				Status:      "PENDING",
				AvailableAt: now.Add(-1 * time.Second),
				Source:      "TARIFF_CHANGE",
			},
			{
				ID:          "win-future-debounce",
				TenantID:    tenant,
				SiteID:      site,
				Granularity: "HOUR",
				WindowStart: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
				WindowEnd:   time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
				Status:      "PENDING",
				AvailableAt: now.Add(30 * time.Second),
				Source:      "BACKFILL",
			},
		},
		bindings: map[string][]BackfillBinding{
			tenant + "|" + site: {
				{
					TenantID:            tenant,
					SiteID:              site,
					BindingID:           "bind-cop-derived",
					MetricID:            "metric-cop",
					Granularity:         "HOUR",
					Timezone:            "UTC",
					DependencyMetricIDs: []string{"metric-power"},
					EffectiveFrom:       baseEff,
					VersionFrom:         baseEff,
				},
				{
					TenantID:      tenant,
					SiteID:        site,
					BindingID:     "bind-power-base",
					MetricID:      "metric-power",
					Granularity:   "HOUR",
					Timezone:      "UTC",
					EffectiveFrom: baseEff,
					VersionFrom:   baseEff,
				},
			},
		},
	}
	exec := &fakeRecalcExecutor{}
	reconciler := &fakeScopeReconciler{}
	worker, err := NewRecalcWorker(RecalcWorkerConfig{
		Store:      store,
		Executor:   exec,
		Reconciler: reconciler,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if stats.Claimed != 2 || stats.CoalescedSpan != 1 || stats.ExecutedRuns != 8 || stats.Succeeded != 2 || stats.Failed != 0 {
		t.Fatalf("unexpected batch stats: %+v", stats)
	}
	if len(store.completed) != 2 || len(store.failed) != 0 {
		t.Fatalf("unexpected completion state: completed=%v failed=%v", store.completed, store.failed)
	}
	if len(reconciler.calls) != 1 || reconciler.calls[0].SiteID != site {
		t.Fatalf("expected ReconcileScope once for site, got %+v", reconciler.calls)
	}
	// Base metric executes all 4 coalesced hours (10,11,12,13) before derived COP executes all 4 hours.
	for i := 0; i < 4; i++ {
		if exec.requests[i].BindingID != "bind-power-base" || exec.requests[i].Reason != "LATE_DATA" {
			t.Fatalf("request[%d] = %+v, want base binding with LATE_DATA reason", i, exec.requests[i])
		}
	}
	for i := 4; i < 8; i++ {
		if exec.requests[i].BindingID != "bind-cop-derived" {
			t.Fatalf("request[%d] = %+v, want derived binding second", i, exec.requests[i])
		}
	}
}

func TestRecalcWorkerMarksFailedWhenExecutionOrBoundFails(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	tenant := "018f4d00-0000-7000-8000-000000000001"
	site := "018f4e00-1000-7000-8000-000000000001"
	baseEff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	store := &fakeDirtyWindowStore{
		due: []DirtyWindowRecord{
			{
				ID:          "win-fail-1",
				TenantID:    tenant,
				SiteID:      site,
				Granularity: "HOUR",
				WindowStart: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
				WindowEnd:   time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
				Status:      "PENDING",
				AvailableAt: now.Add(-time.Second),
				Source:      "BACKFILL",
			},
		},
		bindings: map[string][]BackfillBinding{
			tenant + "|" + site: {{
				TenantID: tenant, SiteID: site, BindingID: "bind-fail", Granularity: "HOUR", Timezone: "UTC", EffectiveFrom: baseEff, VersionFrom: baseEff,
			}},
		},
	}
	exec := &fakeRecalcExecutor{failOn: "bind-fail"}
	worker, err := NewRecalcWorker(RecalcWorkerConfig{
		Store:                store,
		Executor:             exec,
		MaxExecutionsPerSpan: 2,
		Now:                  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Window covers 4 hours > MaxExecutionsPerSpan=2 -> bounded guard triggers without calling Execute.
	stats, err := worker.ProcessOnce(context.Background())
	if err == nil || len(exec.requests) != 0 {
		t.Fatalf("expected bounded limit error with 0 executions, stats=%+v err=%v", stats, err)
	}
	if !strings.Contains(store.failed["win-fail-1"], "exceeds limit 2") || stats.Failed != 1 || len(store.completed) != 0 {
		t.Fatalf("expected win-fail-1 FAILED with bound message, got failed=%v stats=%+v", store.failed, stats)
	}

	// With higher limit, execution failure still marks FAILED cleanly.
	worker.maxExecsPerSpan = 10
	store.failed = map[string]string{}
	stats, err = worker.ProcessOnce(context.Background())
	if err == nil || !strings.Contains(store.failed["win-fail-1"], "simulated recalc failure") || stats.Failed != 1 {
		t.Fatalf("expected simulated recalc failure recorded on win-fail-1, stats=%+v failed=%v err=%v", stats, store.failed, err)
	}
}

type fakeSilentHourExecutor struct {
	requests  []RunRequest
	noDataErr error
}

func (f *fakeSilentHourExecutor) Execute(_ context.Context, req RunRequest) (Result, error) {
	f.requests = append(f.requests, req)
	if req.PeriodStart.Hour() == 10 {
		return Result{}, f.noDataErr
	}
	return Result{RunID: "run-ok", ResultID: "res-ok"}, nil
}

func TestRecalcWorkerSkipsSilentNoObservationPeriodsWithoutFailingSpan(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	tenant := "018f4d00-0000-7000-8000-000000000001"
	site := "018f4e00-1000-7000-8000-000000000001"
	baseEff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	store := &fakeDirtyWindowStore{
		due: []DirtyWindowRecord{
			{
				ID:          "win-silent",
				TenantID:    tenant,
				SiteID:      site,
				Granularity: "HOUR",
				WindowStart: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
				WindowEnd:   time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
				Status:      "PENDING",
				AvailableAt: now.Add(-time.Second),
				Source:      "MANUAL",
			},
		},
		bindings: map[string][]BackfillBinding{
			tenant + "|" + site: {{
				TenantID: tenant, SiteID: site, BindingID: "bind-cooling", Granularity: "HOUR", Timezone: "UTC", EffectiveFrom: baseEff, VersionFrom: baseEff,
			}},
		},
	}
	exec := &fakeSilentHourExecutor{
		noDataErr: errors.Join(ErrNoNumericObservations),
	}
	worker, err := NewRecalcWorker(RecalcWorkerConfig{
		Store:    store,
		Executor: exec,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce() unexpected error for silent hour: %v", err)
	}
	if stats.ExecutedRuns != 1 || stats.Succeeded != 1 || stats.Failed != 0 || len(store.completed) != 1 || len(store.failed) != 0 {
		t.Fatalf("unexpected stats/store for silent hour: stats=%+v completed=%v failed=%v", stats, store.completed, store.failed)
	}
}

func TestReconcileStalePublicationsScansAllScopes(t *testing.T) {
	lister := &fakePublicationScopeLister{
		scopes: []TenantSiteScope{
			{TenantID: "t1", SiteID: "s1"},
			{TenantID: "t1", SiteID: "s2"},
		},
	}
	reconciler := &fakeScopeReconciler{
		repaired: map[string]int{"t1|s1": 3},
		failSite: "s2",
	}
	repaired, err := ReconcileStalePublications(context.Background(), lister, reconciler, time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC), 50)
	if err == nil || repaired != 3 || len(reconciler.calls) != 2 {
		t.Fatalf("expected partial error and 3 repaired across 2 scopes, repaired=%d calls=%v err=%v", repaired, reconciler.calls, err)
	}
}
