package optimization

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type memoryEvaluationStore struct {
	started        bool
	begun          []Evaluation
	completed      []Evaluation
	failed         []string
	recommendation PublishedRecommendation
}

func (store *memoryEvaluationStore) StartRun(context.Context, Request, time.Time) error {
	store.started = true
	return nil
}
func (store *memoryEvaluationStore) BeginPublication(_ context.Context, _ Request, _ Recommendation, evaluation Evaluation, _ time.Time) error {
	store.begun = append(store.begun, evaluation)
	return nil
}
func (store *memoryEvaluationStore) CompletePublication(_ context.Context, evaluation Evaluation, _ time.Time) error {
	store.completed = append(store.completed, evaluation)
	return nil
}
func (store *memoryEvaluationStore) FailRun(_ context.Context, _ Request, code string, _ time.Time) error {
	store.failed = append(store.failed, code)
	return nil
}
func (store *memoryEvaluationStore) ListStalePublications(context.Context, time.Time, int) ([]Evaluation, error) {
	return nil, nil
}
func (store *memoryEvaluationStore) GetRecommendation(context.Context, string, string, string) (PublishedRecommendation, error) {
	if store.recommendation.RunID == "" {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	return store.recommendation, nil
}
func (store *memoryEvaluationStore) GetRecommendationForSites(context.Context, string, []string, string) (PublishedRecommendation, error) {
	if store.recommendation.RunID == "" {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	return store.recommendation, nil
}
func (store *memoryEvaluationStore) LatestRecommendation(context.Context, string, string) (PublishedRecommendation, error) {
	if store.recommendation.RunID == "" {
		return PublishedRecommendation{}, ErrOptimizationNotFound
	}
	return store.recommendation, nil
}

type captureEvaluationSink struct {
	evaluations []Evaluation
	err         error
}

func (sink *captureEvaluationSink) InsertEvaluation(_ context.Context, evaluation Evaluation) error {
	if sink.err != nil {
		return sink.err
	}
	sink.evaluations = append(sink.evaluations, evaluation)
	return nil
}
func (sink *captureEvaluationSink) HasEvaluation(_ context.Context, evaluationID string) (bool, error) {
	for _, evaluation := range sink.evaluations {
		if evaluation.EvaluationID == evaluationID {
			return true, nil
		}
	}
	return false, nil
}

func validRequest() Request {
	return Request{
		TenantID:               "01990000-3000-7000-8000-000000000001",
		SiteID:                 "01990000-5000-7000-8000-000000000001",
		SubjectType:            "SITE",
		SubjectID:              "01990000-5000-7000-8000-000000000001",
		OptimizationRunID:      "01990000-1950-7000-8000-000000000001",
		InputSnapshotID:        "01990000-1930-7000-8000-000000000001",
		InputChecksum:          strings.Repeat("a", 64),
		PolicyVersionID:        "01990000-1920-7000-8000-000000000001",
		TopologyVersionID:      "01990000-1300-7000-8000-000000000001",
		LoadForecastSnapshotID: "01990000-1890-7000-8000-000000000001",
		TariffVersionID:        "01990000-1420-7000-8000-000000000001",
		DeploymentRevisionID:   "01990000-1960-7000-8000-000000000001",
		Objective:              "COST",
		Horizon:                "DAY_AHEAD",
		HorizonMinutes:         1440,
		Granularity:            "15MIN",
		ValidFrom:              time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		Baseline: HVACBaseline{
			DailyEnergyKWh: 2400, DailyCost: 360, SupplyTempC: 7, ZoneTempC: 23,
		},
		Constraints: HVACConstraints{
			SupplyTempMinC: 6, SupplyTempMaxC: 10, ZoneTempMinC: 21, ZoneTempMaxC: 25, MaxSupplyTempStep: 1,
		},
		ResponseModel: HVACResponseModel{
			DailyEnergyDeltaPerSupplyTempC: -180, ZoneTempDeltaPerSupplyTempC: 0.4,
			EnergyUncertaintyP90KWh: 60, ZoneTempUncertaintyP90C: 0.2,
		},
	}
}

func TestHVACRecommendationCarriesBenefitConstraintsRollbackAndVerification(t *testing.T) {
	request := validRequest()
	at := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	recommendation, err := (HVACRecommendationSolver{}).Recommend(t.Context(), request, at)
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.Approval != intelligencemodel.RecommendationDraft || recommendation.CommandIntentID != "" {
		t.Fatalf("recommendation must remain non-executable draft: %#v", recommendation)
	}
	candidateSupply, ok := recommendation.Candidate["supplyTempC"].(float64)
	if !ok || candidateSupply != 8 {
		t.Fatalf("candidate=%#v", recommendation.Candidate)
	}
	if recommendation.ExpectedImpact["energySavingKWhPerDay"].(float64) <= 0 || recommendation.ExpectedImpact["costSavingPerDay"].(float64) <= 0 {
		t.Fatalf("expected impact=%#v", recommendation.ExpectedImpact)
	}
	if len(recommendation.Constraints) < 3 || len(recommendation.Uncertainty) == 0 || len(recommendation.Risk) == 0 || len(recommendation.RollbackPlan) == 0 || len(recommendation.VerificationPlan) == 0 {
		t.Fatalf("recommendation evidence is incomplete: %#v", recommendation)
	}
	if err := recommendation.CanCreateCommand(at.Add(time.Minute)); err == nil {
		t.Fatal("draft recommendation must never create a command")
	}
}

func TestHVACRecommendationHonorsComfortUncertainty(t *testing.T) {
	request := validRequest()
	request.Baseline.ZoneTempC = 24.5
	request.ResponseModel.ZoneTempUncertaintyP90C = 0.3
	recommendation, err := (HVACRecommendationSolver{}).Recommend(t.Context(), request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.Candidate["supplyTempC"].(float64) != 7 {
		t.Fatalf("solver selected a candidate whose P90 comfort bound should be rejected: %#v", recommendation.Candidate)
	}
}

func TestOptimizationRejectsInvalidFrozenModelInputs(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"wrong subject":         func(request *Request) { request.SubjectID = "01990000-5000-7000-8000-000000000002" },
		"missing checksum":      func(request *Request) { request.InputChecksum = "" },
		"unsupported objective": func(request *Request) { request.Objective = "CARBON" },
		"wrong horizon":         func(request *Request) { request.HorizonMinutes = 720 },
		"invalid baseline":      func(request *Request) { request.Baseline.DailyEnergyKWh = 0 },
		"invalid coefficient":   func(request *Request) { request.ResponseModel.ZoneTempDeltaPerSupplyTempC = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			request := validRequest()
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("expected optimization request to fail")
			}
		})
	}
}

func TestOptimizationServicePublishesRecommendationNotDispatchPlan(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{}
	at := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	service, err := NewDefaultService(store, sink, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	recommendation, err := service.Optimize(t.Context(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !store.started || len(store.begun) != 1 || len(store.completed) != 1 || len(sink.evaluations) != 1 {
		t.Fatalf("publication state started=%v begun=%d completed=%d evaluations=%d", store.started, len(store.begun), len(store.completed), len(sink.evaluations))
	}
	if store.completed[0].RecommendationID != recommendation.ID || store.completed[0].ConstraintCount != uint32(len(recommendation.Constraints)) {
		t.Fatalf("evaluation lineage=%#v recommendation=%#v", store.completed[0], recommendation)
	}
}

func TestOptimizationClickHouseFailureIsExplicit(t *testing.T) {
	store := &memoryEvaluationStore{}
	sink := &captureEvaluationSink{err: errors.New("clickhouse unavailable")}
	service, _ := NewDefaultService(store, sink, time.Now)
	if _, err := service.Optimize(t.Context(), validRequest()); err == nil {
		t.Fatal("expected evaluation persistence failure")
	}
	if len(store.failed) != 1 || store.failed[0] != "CLICKHOUSE_WRITE_FAILED" {
		t.Fatalf("failure evidence=%v", store.failed)
	}
}
