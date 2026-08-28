package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

var ErrPreparationUnavailable = errors.New("forecast authoritative input is unavailable")

type PreparationRequest struct {
	TenantID    string `json:"-"`
	SiteID      string `json:"siteId"`
	SubjectType string `json:"subjectType"`
	SubjectID   string `json:"subjectId"`
	Target      string `json:"target"`
}

func (request PreparationRequest) Validate() error {
	for field, value := range map[string]string{
		"tenantId":  request.TenantID,
		"siteId":    request.SiteID,
		"subjectId": request.SubjectID,
	} {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s must be a UUID", field)
		}
	}
	if request.SubjectType != "SITE" && request.SubjectType != "ENERGY_NODE" {
		return errors.New("subjectType must be SITE or ENERGY_NODE")
	}
	if request.SubjectType == "SITE" && request.SubjectID != request.SiteID {
		return errors.New("SITE forecast subjectId must equal siteId")
	}
	if request.Target != "SITE_LOAD" && request.Target != "PV_GENERATION" {
		return errors.New("target must be SITE_LOAD or PV_GENERATION")
	}
	return nil
}

type PreparationDefinition struct {
	DeploymentID        string
	ModelID             string
	ModelVersionID      string
	ModelVersion        uint64
	FeatureSetVersionID string
	FeatureSetVersion   uint64
	TopologyVersionID   string
	HorizonMinutes      int
	Granularity         string
	MetricVersionRefs   []string
	FeatureSchema       json.RawMessage
}

type MetricHistoryQuery struct {
	TenantID        string
	SiteID          string
	SubjectType     string
	SubjectID       string
	MetricVersionID string
	From            time.Time
	To              time.Time
}

type MetricFact struct {
	ResultID     string    `json:"resultId"`
	PeriodStart  time.Time `json:"periodStart"`
	PeriodEnd    time.Time `json:"periodEnd"`
	CalculatedAt time.Time `json:"calculatedAt"`
	Value        float64   `json:"value"`
	Unit         string    `json:"unit"`
	Quality      string    `json:"quality"`
	Completeness float64   `json:"completeness"`
	Revision     uint64    `json:"revision"`
}

type FrozenMetricSeries struct {
	MetricVersionID string       `json:"metricVersionId"`
	Unit            string       `json:"unit"`
	Facts           []MetricFact `json:"facts"`
}

type FrozenFeatureValues struct {
	SchemaVersion         int                  `json:"schemaVersion"`
	TargetMetricVersionID string               `json:"targetMetricVersionId"`
	Series                []FrozenMetricSeries `json:"series"`
}

type PreparedInput struct {
	TenantID            string
	SiteID              string
	SubjectType         string
	SubjectID           string
	Target              string
	DeploymentID        string
	ModelID             string
	ModelVersionID      string
	ModelVersion        uint64
	FeatureSetVersionID string
	FeatureSetVersion   uint64
	TopologyVersionID   string
	ForecastOrigin      time.Time
	HorizonMinutes      int
	Granularity         string
	LatestDataTime      time.Time
	WeatherIssueTime    *time.Time
	MetricVersionRefs   []string
	FeatureValues       FrozenFeatureValues
	InputChecksum       string
}

type PreparedForecast struct {
	ForecastJobID      string `json:"forecastJobId"`
	InputSnapshotID    string `json:"inputSnapshotId"`
	ForecastSnapshotID string `json:"forecastSnapshotId"`
	Status             string `json:"status"`
}

type PreparationStore interface {
	ResolvePreparation(context.Context, PreparationRequest, time.Time) (PreparationDefinition, error)
	CreatePreparedForecast(context.Context, PreparedInput, time.Time) (PreparedForecast, error)
}

type InputHistory interface {
	ReadMetricSeries(context.Context, MetricHistoryQuery) ([]MetricFact, error)
}

type Preparer struct {
	store   PreparationStore
	history InputHistory
	clock   Clock
}

func NewPreparer(store PreparationStore, history InputHistory, clock Clock) (*Preparer, error) {
	if store == nil || history == nil {
		return nil, errors.New("forecast preparation store and authoritative history reader are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Preparer{store: store, history: history, clock: clock}, nil
}

func (preparer *Preparer) Prepare(ctx context.Context, request PreparationRequest) (PreparedForecast, error) {
	if err := request.Validate(); err != nil {
		return PreparedForecast{}, err
	}
	forecastOrigin := preparer.clock().UTC()
	definition, err := preparer.store.ResolvePreparation(ctx, request, forecastOrigin)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("resolve forecast preparation lineage: %w", err)
	}
	if definition.HorizonMinutes <= 0 {
		return PreparedForecast{}, errors.New("resolved Forecast model horizon is invalid")
	}
	granularity, err := granularityMinutes(definition.Granularity)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("resolved Forecast model granularity: %w", err)
	}
	metricRefs, targetMetricVersionID, err := canonicalMetricReferences(definition.MetricVersionRefs, definition.FeatureSchema)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("resolve Forecast feature metric provenance: %w", err)
	}
	lookbackMinutes := max(definition.HorizonMinutes, granularity*4)
	from := forecastOrigin.Add(-time.Duration(lookbackMinutes) * time.Minute)
	series := make([]FrozenMetricSeries, 0, len(metricRefs))
	latestDataTime := time.Time{}
	for _, metricVersionID := range metricRefs {
		facts, readErr := preparer.history.ReadMetricSeries(ctx, MetricHistoryQuery{
			TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
			MetricVersionID: metricVersionID, From: from, To: forecastOrigin,
		})
		if readErr != nil {
			return PreparedForecast{}, fmt.Errorf("%w: metric %s history read failed: %v", ErrPreparationUnavailable, metricVersionID, readErr)
		}
		facts, unit, normalizeErr := normalizeMetricFacts(facts, from, forecastOrigin)
		if normalizeErr != nil {
			return PreparedForecast{}, fmt.Errorf("%w: metric %s: %v", ErrPreparationUnavailable, metricVersionID, normalizeErr)
		}
		if metricVersionID == targetMetricVersionID && len(facts) < 4 {
			return PreparedForecast{}, fmt.Errorf("%w: target metric %s requires at least four historical facts", ErrPreparationUnavailable, metricVersionID)
		}
		if facts[len(facts)-1].PeriodEnd.After(latestDataTime) {
			latestDataTime = facts[len(facts)-1].PeriodEnd
		}
		series = append(series, FrozenMetricSeries{MetricVersionID: metricVersionID, Unit: unit, Facts: facts})
	}
	values := FrozenFeatureValues{SchemaVersion: 1, TargetMetricVersionID: targetMetricVersionID, Series: series}
	prepared := PreparedInput{
		TenantID: request.TenantID, SiteID: request.SiteID, SubjectType: request.SubjectType, SubjectID: request.SubjectID, Target: request.Target,
		DeploymentID: definition.DeploymentID, ModelID: definition.ModelID, ModelVersionID: definition.ModelVersionID, ModelVersion: definition.ModelVersion,
		FeatureSetVersionID: definition.FeatureSetVersionID, FeatureSetVersion: definition.FeatureSetVersion, TopologyVersionID: definition.TopologyVersionID,
		ForecastOrigin: forecastOrigin, HorizonMinutes: definition.HorizonMinutes, Granularity: definition.Granularity,
		LatestDataTime: latestDataTime, MetricVersionRefs: metricRefs, FeatureValues: values,
	}
	prepared.InputChecksum, err = checksumPreparedInput(prepared)
	if err != nil {
		return PreparedForecast{}, err
	}
	result, err := preparer.store.CreatePreparedForecast(ctx, prepared, forecastOrigin)
	if err != nil {
		return PreparedForecast{}, fmt.Errorf("persist prepared Forecast input/job: %w", err)
	}
	return result, nil
}

func canonicalMetricReferences(refs []string, featureSchema json.RawMessage) ([]string, string, error) {
	if len(refs) == 0 {
		return nil, "", errors.New("Forecast dataset has no metric_version_refs")
	}
	canonical := slices.Clone(refs)
	for _, ref := range canonical {
		if !uuidPattern.MatchString(ref) {
			return nil, "", fmt.Errorf("metric_version_refs contains invalid UUID %q", ref)
		}
	}
	slices.Sort(canonical)
	canonical = slices.Compact(canonical)
	var schema struct {
		TargetMetricVersionID string `json:"targetMetricVersionId"`
	}
	if len(featureSchema) > 0 {
		if err := json.Unmarshal(featureSchema, &schema); err != nil {
			return nil, "", fmt.Errorf("feature_schema is invalid: %w", err)
		}
	}
	target := schema.TargetMetricVersionID
	if target == "" {
		if len(canonical) != 1 {
			return nil, "", errors.New("feature_schema.targetMetricVersionId is required when multiple metric versions are referenced")
		}
		target = canonical[0]
	}
	if !slices.Contains(canonical, target) {
		return nil, "", errors.New("feature_schema.targetMetricVersionId is not present in metric_version_refs")
	}
	return canonical, target, nil
}

func normalizeMetricFacts(facts []MetricFact, from, to time.Time) ([]MetricFact, string, error) {
	if len(facts) == 0 {
		return nil, "", errors.New("authoritative metric history is empty")
	}
	ordered := slices.Clone(facts)
	slices.SortFunc(ordered, func(left, right MetricFact) int {
		if cmp := left.PeriodEnd.Compare(right.PeriodEnd); cmp != 0 {
			return cmp
		}
		if left.Revision != right.Revision {
			if left.Revision > right.Revision {
				return -1
			}
			return 1
		}
		if cmp := left.CalculatedAt.Compare(right.CalculatedAt); cmp != 0 {
			return -cmp
		}
		if left.ResultID < right.ResultID {
			return -1
		}
		if left.ResultID > right.ResultID {
			return 1
		}
		return 0
	})
	result := make([]MetricFact, 0, len(ordered))
	unit := ""
	var previousEnd time.Time
	for _, fact := range ordered {
		fact.PeriodStart = fact.PeriodStart.UTC()
		fact.PeriodEnd = fact.PeriodEnd.UTC()
		fact.CalculatedAt = fact.CalculatedAt.UTC()
		if fact.ResultID == "" || fact.PeriodStart.IsZero() || !fact.PeriodEnd.After(fact.PeriodStart) || fact.PeriodEnd.Before(from) || !fact.PeriodEnd.Before(to) {
			return nil, "", errors.New("authoritative metric history returned a fact outside the requested half-open window")
		}
		if math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) {
			return nil, "", errors.New("authoritative metric history returned a non-finite value")
		}
		if fact.Unit == "" {
			return nil, "", errors.New("authoritative metric history returned an empty unit")
		}
		if unit == "" {
			unit = fact.Unit
		} else if fact.Unit != unit {
			return nil, "", errors.New("authoritative metric history changed unit within the frozen window")
		}
		if !previousEnd.IsZero() && fact.PeriodEnd.Equal(previousEnd) {
			continue
		}
		result = append(result, fact)
		previousEnd = fact.PeriodEnd
	}
	if len(result) == 0 {
		return nil, "", errors.New("authoritative metric history has no usable facts")
	}
	return result, unit, nil
}

func checksumPreparedInput(input PreparedInput) (string, error) {
	payload := struct {
		TenantID            string              `json:"tenantId"`
		SiteID              string              `json:"siteId"`
		SubjectType         string              `json:"subjectType"`
		SubjectID           string              `json:"subjectId"`
		Target              string              `json:"target"`
		DeploymentID        string              `json:"deploymentId"`
		ModelVersionID      string              `json:"modelVersionId"`
		FeatureSetVersionID string              `json:"featureSetVersionId"`
		TopologyVersionID   string              `json:"topologyVersionId"`
		LatestDataTime      time.Time           `json:"latestDataTime"`
		WeatherIssueTime    *time.Time          `json:"weatherIssueTime"`
		MetricVersionRefs   []string            `json:"metricVersionRefs"`
		FeatureValues       FrozenFeatureValues `json:"featureValues"`
	}{
		TenantID: input.TenantID, SiteID: input.SiteID, SubjectType: input.SubjectType, SubjectID: input.SubjectID, Target: input.Target,
		DeploymentID: input.DeploymentID, ModelVersionID: input.ModelVersionID, FeatureSetVersionID: input.FeatureSetVersionID,
		TopologyVersionID: input.TopologyVersionID, LatestDataTime: input.LatestDataTime.UTC(), WeatherIssueTime: input.WeatherIssueTime,
		MetricVersionRefs: input.MetricVersionRefs, FeatureValues: input.FeatureValues,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Forecast input checksum payload: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func executionInput(values FrozenFeatureValues) ([]Observation, string, error) {
	for _, series := range values.Series {
		if series.MetricVersionID != values.TargetMetricVersionID {
			continue
		}
		if len(series.Facts) < 4 {
			return nil, "", errors.New("frozen target metric history has fewer than four facts")
		}
		observations := make([]Observation, 0, len(series.Facts))
		for _, fact := range series.Facts {
			observations = append(observations, Observation{ObservedAt: fact.PeriodEnd.UTC(), Value: fact.Value})
		}
		return observations, series.Unit, nil
	}
	return nil, "", errors.New("frozen feature values do not contain the target metric series")
}
