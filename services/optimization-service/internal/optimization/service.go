package optimization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
)

type SolverAdapter interface {
	Recommend(context.Context, Request, time.Time) (Recommendation, error)
}

type HVACRecommendationSolver struct{}

type candidateEvaluation struct {
	supplyTempC       float64
	projectedZoneTemp float64
	dailyEnergyKWh    float64
	dailyCost         float64
	zoneMarginC       float64
}

func (HVACRecommendationSolver) Recommend(_ context.Context, request Request, at time.Time) (Recommendation, error) {
	if err := request.Validate(); err != nil {
		return Recommendation{}, err
	}
	baseline := request.Baseline
	constraints := request.Constraints
	model := request.ResponseModel
	costPerKWh := baseline.DailyCost / baseline.DailyEnergyKWh
	steps := []float64{-constraints.MaxSupplyTempStep, 0, constraints.MaxSupplyTempStep}
	var best *candidateEvaluation
	for _, delta := range steps {
		supplyTemp := baseline.SupplyTempC + delta
		if supplyTemp < constraints.SupplyTempMinC || supplyTemp > constraints.SupplyTempMaxC {
			continue
		}
		zoneTemp := baseline.ZoneTempC + model.ZoneTempDeltaPerSupplyTempC*delta
		zoneLower := zoneTemp - model.ZoneTempUncertaintyP90C
		zoneUpper := zoneTemp + model.ZoneTempUncertaintyP90C
		if zoneLower < constraints.ZoneTempMinC || zoneUpper > constraints.ZoneTempMaxC {
			continue
		}
		energy := math.Max(0, baseline.DailyEnergyKWh+model.DailyEnergyDeltaPerSupplyTempC*delta)
		candidate := candidateEvaluation{
			supplyTempC: supplyTemp, projectedZoneTemp: zoneTemp, dailyEnergyKWh: energy, dailyCost: energy * costPerKWh,
			zoneMarginC: math.Min(zoneLower-constraints.ZoneTempMinC, constraints.ZoneTempMaxC-zoneUpper),
		}
		if best == nil || candidate.dailyCost < best.dailyCost {
			copy := candidate
			best = &copy
		}
	}
	if best == nil {
		return Recommendation{}, errors.New("no HVAC recommendation satisfies the frozen comfort and safety constraints")
	}
	recommendationID := deterministicV7(request.OptimizationRunID, "recommendation:1", request.ValidFrom.UTC())
	riskLevel := "LOW"
	if best.zoneMarginC <= math.Max(0.5, 2*model.ZoneTempUncertaintyP90C) {
		riskLevel = "MEDIUM"
	}
	recommendation := Recommendation{
		ID: recommendationID, TenantID: request.TenantID, SiteID: request.SiteID, InputSnapshotID: request.InputSnapshotID,
		DeploymentRevision: request.DeploymentRevisionID,
		Baseline: map[string]any{
			"dailyEnergyKWh": baseline.DailyEnergyKWh, "dailyCost": baseline.DailyCost,
			"supplyTempC": baseline.SupplyTempC, "zoneTempC": baseline.ZoneTempC,
		},
		Objective: map[string]any{"kind": request.Objective, "horizon": request.Horizon, "horizonMinutes": request.HorizonMinutes},
		Constraints: []map[string]any{
			{"kind": "SUPPLY_TEMP", "minC": constraints.SupplyTempMinC, "maxC": constraints.SupplyTempMaxC, "maxStepC": constraints.MaxSupplyTempStep},
			{"kind": "ZONE_COMFORT", "minC": constraints.ZoneTempMinC, "maxC": constraints.ZoneTempMaxC},
			{"kind": "CURRENT_STATE_REVALIDATION", "required": true},
		},
		Candidate: map[string]any{
			"supplyTempC": best.supplyTempC, "projectedZoneTempC": best.projectedZoneTemp,
			"dailyEnergyKWh": best.dailyEnergyKWh, "dailyCost": best.dailyCost,
		},
		ExpectedImpact: map[string]any{
			"energySavingKWhPerDay": baseline.DailyEnergyKWh - best.dailyEnergyKWh,
			"costSavingPerDay":      baseline.DailyCost - best.dailyCost,
		},
		Uncertainty: map[string]any{
			"energyP90KWh": model.EnergyUncertaintyP90KWh, "zoneTempP90C": model.ZoneTempUncertaintyP90C,
		},
		Risk: map[string]any{"level": riskLevel, "comfortMarginC": best.zoneMarginC},
		RollbackPlan: map[string]any{
			"restoreSupplyTempC": baseline.SupplyTempC,
			"conditions":         []string{"zone_comfort_violation", "energy_saving_not_observed", "operator_cancelled"},
		},
		VerificationPlan: map[string]any{
			"windowMinutes": 30, "requiredSignals": []string{"supply_temperature", "zone_temperature", "site_load"},
			"successCriteria": []string{"zone_temperature_within_frozen_constraint", "site_load_not_worse_than_baseline"},
		},
		Approval: intelligencemodel.RecommendationDraft, CreatedAt: at.UTC(),
	}
	if err := recommendation.ValidateForApproval(); err != nil {
		return Recommendation{}, err
	}
	return recommendation, nil
}

type Clock func() time.Time

type Service struct {
	solver      SolverAdapter
	publication EvaluationPublicationStore
	evaluations EvaluationSink
	clock       Clock
}

func NewService(solver SolverAdapter, publication EvaluationPublicationStore, evaluations EvaluationSink, clock Clock) (*Service, error) {
	if solver == nil || publication == nil || evaluations == nil {
		return nil, fmt.Errorf("optimization solver, PostgreSQL publication store and ClickHouse evaluation sink are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{solver: solver, publication: publication, evaluations: evaluations, clock: clock}, nil
}

func NewDefaultService(publication EvaluationPublicationStore, evaluations EvaluationSink, clock Clock) (*Service, error) {
	return NewService(HVACRecommendationSolver{}, publication, evaluations, clock)
}

func (service *Service) Optimize(ctx context.Context, request Request) (Recommendation, error) {
	if err := request.Validate(); err != nil {
		return Recommendation{}, err
	}
	startedAt := service.clock().UTC()
	if err := service.publication.StartRun(ctx, request, startedAt); err != nil {
		return Recommendation{}, fmt.Errorf("start optimization run: %w", err)
	}
	fail := func(code string) { _ = service.publication.FailRun(ctx, request, code, service.clock().UTC()) }
	recommendation, err := service.solver.Recommend(ctx, request, startedAt)
	if err != nil {
		fail("SOLVER_ERROR")
		return Recommendation{}, fmt.Errorf("solve HVAC recommendation: %w", err)
	}
	if err = recommendation.ValidateForApproval(); err != nil {
		fail("RECOMMENDATION_INVALID")
		return Recommendation{}, err
	}
	evaluationID, err := optimizationUUIDv7(service.clock().UTC())
	if err != nil {
		fail("EVALUATION_ID_FAILED")
		return Recommendation{}, err
	}
	evaluationPayload, _ := json.Marshal(recommendation)
	evaluation := Evaluation{
		EvaluationID: evaluationID, TenantID: request.TenantID, SiteID: request.SiteID,
		OptimizationRunID: request.OptimizationRunID, RecommendationID: recommendation.ID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		Objective: request.Objective, SolverOutcome: "FEASIBLE", Quality: "FEASIBLE", ConstraintCount: uint32(len(recommendation.Constraints)),
		InputSnapshotID: request.InputSnapshotID, PolicyVersionID: request.PolicyVersionID, TopologyVersionID: request.TopologyVersionID,
		LoadForecastSnapshotID: request.LoadForecastSnapshotID, PVForecastSnapshotID: request.PVForecastSnapshotID, TariffVersionID: request.TariffVersionID,
		EvaluationJSON: string(evaluationPayload), GeneratedAt: service.clock().UTC(),
	}
	if err = service.publication.BeginPublication(ctx, request, recommendation, evaluation, service.clock().UTC()); err != nil {
		fail("PUBLICATION_BEGIN_FAILED")
		return Recommendation{}, fmt.Errorf("begin optimization evaluation publication: %w", err)
	}
	if err = service.evaluations.InsertEvaluation(ctx, evaluation); err != nil {
		fail("CLICKHOUSE_WRITE_FAILED")
		return Recommendation{}, fmt.Errorf("persist optimization evaluation: %w", err)
	}
	// ClickHouse success makes the Evaluation durable. A PostgreSQL completion
	// failure intentionally leaves the run PERSISTING for reconciliation.
	if err = service.publication.CompletePublication(ctx, evaluation, service.clock().UTC()); err != nil {
		return Recommendation{}, fmt.Errorf("complete optimization evaluation publication: %w", err)
	}
	return recommendation, nil
}

func (service *Service) GetRecommendation(ctx context.Context, tenantID, siteID, runID string) (PublishedRecommendation, error) {
	return service.publication.GetRecommendation(ctx, tenantID, siteID, runID)
}

func (service *Service) GetRecommendationForSites(ctx context.Context, tenantID string, allowedSiteIDs []string, runID string) (PublishedRecommendation, error) {
	return service.publication.GetRecommendationForSites(ctx, tenantID, allowedSiteIDs, runID)
}

func (service *Service) LatestRecommendation(ctx context.Context, tenantID, siteID string) (PublishedRecommendation, error) {
	return service.publication.LatestRecommendation(ctx, tenantID, siteID)
}

func (service *Service) Reconcile(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	stale, err := service.publication.ListStalePublications(ctx, staleBefore.UTC(), limit)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, evaluation := range stale {
		present, checkErr := service.evaluations.HasEvaluation(ctx, evaluation.EvaluationID)
		if checkErr != nil {
			return repaired, checkErr
		}
		if !present {
			continue
		}
		if err = service.publication.CompletePublication(ctx, evaluation, service.clock().UTC()); err != nil {
			return repaired, err
		}
		repaired++
	}
	return repaired, nil
}

func deterministicV7(namespace, value string, at time.Time) string {
	digest := sha256.Sum256([]byte(namespace + "|" + value))
	bytes := append([]byte(nil), digest[:16]...)
	milliseconds := uint64(at.UTC().UnixMilli())
	bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	bytes[6] = (bytes[6] & 0x0f) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}
