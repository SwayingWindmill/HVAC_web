package forecast

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type preparationStoreStub struct {
	definition PreparationDefinition
	created    []PreparedInput
}

func (store *preparationStoreStub) ResolvePreparation(context.Context, PreparationRequest, time.Time) (PreparationDefinition, error) {
	return store.definition, nil
}

func (store *preparationStoreStub) CreatePreparedForecast(_ context.Context, input PreparedInput, _ time.Time) (PreparedForecast, error) {
	store.created = append(store.created, input)
	return PreparedForecast{
		ForecastJobID: "01990000-1880-7000-8000-000000000001", InputSnapshotID: "01990000-1870-7000-8000-000000000001",
		ForecastSnapshotID: "01990000-1890-7000-8000-000000000001", Status: "PENDING",
	}, nil
}

type metricHistoryStub struct {
	facts []MetricFact
	calls int
}

func (history *metricHistoryStub) ReadMetricSeries(context.Context, MetricHistoryQuery) ([]MetricFact, error) {
	history.calls++
	result := slices.Clone(history.facts)
	if history.calls%2 == 0 {
		slices.Reverse(result)
	}
	return result, nil
}

func preparationRequest() PreparationRequest {
	return PreparationRequest{
		TenantID: "01990000-3000-7000-8000-000000000001", SiteID: "01990000-5000-7000-8000-000000000001",
		SubjectType: "SITE", SubjectID: "01990000-5000-7000-8000-000000000001", Target: "SITE_LOAD",
	}
}

func preparationDefinition() PreparationDefinition {
	return PreparationDefinition{
		DeploymentID: "01990000-1720-7000-8000-000000000001", ModelID: "01990000-1730-7000-8000-000000000001",
		ModelVersionID: "01990000-1740-7000-8000-000000000001", ModelVersion: 3,
		FeatureSetVersionID: "01990000-1750-7000-8000-000000000001", FeatureSetVersion: 7,
		TopologyVersionID: "01990000-1770-7000-8000-000000000001", HorizonMinutes: 1440, Granularity: "15MIN",
		MetricVersionRefs: []string{"01990000-1300-7000-8000-000000000001"}, FeatureSchema: []byte(`{"features":["load_t_15m","load_t_24h","hour_of_day"]}`),
	}
}

func TestPreparerFreezesAuthoritativeMetricHistoryDeterministically(t *testing.T) {
	origin := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	facts := []MetricFact{
		metricFact(origin.Add(-15*time.Minute), 805, 1),
		metricFact(origin.Add(-60*time.Minute), 760, 1),
		metricFact(origin.Add(-30*time.Minute), 790, 1),
		metricFact(origin.Add(-45*time.Minute), 770, 1),
		metricFact(origin.Add(-45*time.Minute), 775, 2),
	}
	store := &preparationStoreStub{definition: preparationDefinition()}
	history := &metricHistoryStub{facts: facts}
	preparer, err := NewPreparer(store, history, func() time.Time { return origin })
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, prepareErr := preparer.Prepare(t.Context(), preparationRequest())
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if result.Status != "PENDING" {
			t.Fatalf("status=%q", result.Status)
		}
	}
	if len(store.created) != 2 {
		t.Fatalf("prepared snapshots=%d", len(store.created))
	}
	first, second := store.created[0], store.created[1]
	if first.InputChecksum != second.InputChecksum {
		t.Fatalf("checksum changed with history row order: %s != %s", first.InputChecksum, second.InputChecksum)
	}
	if len(first.FeatureValues.Series) != 1 || len(first.FeatureValues.Series[0].Facts) != 4 {
		t.Fatalf("frozen feature values=%#v", first.FeatureValues)
	}
	frozen := first.FeatureValues.Series[0].Facts
	if frozen[1].Value != 775 || frozen[0].PeriodEnd.After(frozen[1].PeriodEnd) {
		t.Fatalf("history was not normalized to latest revision in time order: %#v", frozen)
	}
	if first.LatestDataTime != origin.Add(-15*time.Minute) || first.ForecastOrigin != origin {
		t.Fatalf("times latest=%s origin=%s", first.LatestDataTime, first.ForecastOrigin)
	}
}

func TestPreparerFailsClosedBeforeCreatingJobWhenTargetHistoryIsInsufficient(t *testing.T) {
	origin := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := &preparationStoreStub{definition: preparationDefinition()}
	history := &metricHistoryStub{facts: []MetricFact{
		metricFact(origin.Add(-45*time.Minute), 770, 1),
		metricFact(origin.Add(-30*time.Minute), 790, 1),
		metricFact(origin.Add(-15*time.Minute), 805, 1),
	}}
	preparer, _ := NewPreparer(store, history, func() time.Time { return origin })
	_, err := preparer.Prepare(t.Context(), preparationRequest())
	if !errors.Is(err, ErrPreparationUnavailable) {
		t.Fatalf("expected preparation unavailable, got %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("insufficient authoritative history created %d jobs", len(store.created))
	}
}

func metricFact(periodEnd time.Time, value float64, revision uint64) MetricFact {
	return MetricFact{
		ResultID: "01990000-1400-7000-8000-000000000001", PeriodStart: periodEnd.Add(-15 * time.Minute), PeriodEnd: periodEnd,
		CalculatedAt: periodEnd.Add(time.Minute), Value: value, Unit: "kW", Quality: "GOOD", Completeness: 1, Revision: revision,
	}
}
