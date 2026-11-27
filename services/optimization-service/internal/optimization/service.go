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
		Baseline: intelligencemodel.RecommendationBaseline{
			DailyEnergyKWh: baseline.DailyEnergyKWh, DailyCost: baseline.DailyCost,
			SupplyTempC: baseline.SupplyTempC, ZoneTempC: baseline.ZoneTempC,
		},
		Objective: intelligencemodel.RecommendationObjective{
			Kind: request.Objective, Horizon: request.Horizon, HorizonMinutes: request.HorizonMinutes,
		},
		Constraints: frozenRecommendationConstraints(constraints),
		Candidate: intelligencemodel.RecommendationCandidate{
			SupplyTempC: best.supplyTempC, ProjectedZoneTempC: best.projectedZoneTemp,
			DailyEnergyKWh: best.dailyEnergyKWh, DailyCost: best.dailyCost,
		},
		ExpectedImpact: intelligencemodel.RecommendationExpectedImpact{
			EnergySavingKWhPerDay: baseline.DailyEnergyKWh - best.dailyEnergyKWh,
			CostSavingPerDay:      baseline.DailyCost - best.dailyCost,
		},
		Uncertainty: intelligencemodel.RecommendationUncertainty{
			EnergyP90KWh: model.EnergyUncertaintyP90KWh, ZoneTempP90C: model.ZoneTempUncertaintyP90C,
		},
		Risk:             intelligencemodel.RecommendationRisk{Level: riskLevel, ComfortMarginC: best.zoneMarginC},
		RollbackPlan:     standardRollbackPlan(baseline.SupplyTempC),
		VerificationPlan: standardVerificationPlan(),
		Approval:         intelligencemodel.RecommendationDraft,
		ValidUntil:       timePointer(at.UTC().Add(intelligencemodel.DefaultRecommendationTTL)),
		CreatedAt:        at.UTC(),
	}
	if err := recommendation.ValidateForApproval(); err != nil {
		return Recommendation{}, err
	}
	return recommendation, nil
}

func float64Ptr(value float64) *float64 { return &value }
func boolPtr(value bool) *bool          { return &value }

func frozenRecommendationConstraints(constraints HVACConstraints) []intelligencemodel.RecommendationConstraint {
	return []intelligencemodel.RecommendationConstraint{
		{
			Kind:     "SUPPLY_TEMP",
			MinC:     float64Ptr(constraints.SupplyTempMinC),
			MaxC:     float64Ptr(constraints.SupplyTempMaxC),
			MaxStepC: float64Ptr(constraints.MaxSupplyTempStep),
		},
		{
			Kind: "ZONE_COMFORT",
			MinC: float64Ptr(constraints.ZoneTempMinC),
			MaxC: float64Ptr(constraints.ZoneTempMaxC),
		},
		{
			Kind:     "CURRENT_STATE_REVALIDATION",
			Required: boolPtr(true),
		},
	}
}

func standardRollbackPlan(restoreSupplyTempC float64) intelligencemodel.RecommendationRollbackPlan {
	return intelligencemodel.RecommendationRollbackPlan{
		RestoreSupplyTempC: float64Ptr(restoreSupplyTempC),
		Conditions:         []string{"zone_comfort_violation", "energy_saving_not_observed", "operator_cancelled"},
	}
}

func standardVerificationPlan() intelligencemodel.RecommendationVerificationPlan {
	return intelligencemodel.RecommendationVerificationPlan{
		WindowMinutes:   30,
		RequiredSignals: []string{"supply_temperature", "zone_temperature", "site_load"},
		SuccessCriteria: []string{"zone_temperature_within_frozen_constraint", "site_load_not_worse_than_baseline"},
	}
}

type Clock func() time.Time

type Service struct {
	solver             SolverAdapter
	publication        EvaluationPublicationStore
	evaluations        EvaluationSink
	clock              Clock
	shadow             *shadowSolver
	shadowReader       ShadowEvaluationReader
	staging            *StagingEvaluator
	stagingSiteLister  StagingSiteLister
	solverResolver     ActiveSolverResolver
	flywheelStore      GovernanceFlywheelStore
	siteVisibility     SiteVisibilityChecker
	canaryStore        CanaryBindingStore
	edgeModelStore     EdgeModelStore
	edgeModelSigner    *edgeModelSigner
	allowedSolverHosts []string
}

var ErrShadowEvaluationNotFound = errors.New("optimization shadow evaluation not found")

type ShadowEvaluationQuery struct {
	TenantID          string
	SiteID            string
	OptimizationRunID string
}

type ShadowEvaluationRecord struct {
	EvaluationID      string
	OptimizationRunID string
	RecommendationID  string
	GeneratedAt       time.Time
	EvaluationJSON    string
}

// ShadowEvaluationReader reads SHADOW rows recorded beside published runs in ClickHouse.
type ShadowEvaluationReader interface {
	ReadShadowEvaluation(context.Context, ShadowEvaluationQuery) (ShadowEvaluationRecord, error)
}

// ShadowSummary projects only read-only diagnostic figures of what the shadow solver would have
// chosen, omitting Recommendation identity, approval state, and rollback/command fields.
type ShadowSummary struct {
	SupplyTempC                   float64 `json:"supplyTempC"`
	ProjectedZoneTempC            float64 `json:"projectedZoneTempC"`
	DailyEnergyKWh                float64 `json:"dailyEnergyKWh"`
	DailyCost                     float64 `json:"dailyCost"`
	EnergySavingKWhPerDay         float64 `json:"energySavingKWhPerDay"`
	CostSavingPerDay              float64 `json:"costSavingPerDay"`
	EnergyP90KWh                  float64 `json:"energyP90KWh"`
	ZoneTempP90C                  float64 `json:"zoneTempP90C"`
	ComfortMarginC                float64 `json:"comfortMarginC"`
	RiskLevel                     string  `json:"riskLevel"`
	Extrapolated                  bool    `json:"extrapolated"`
	SavingWithinEnergyUncertainty bool    `json:"savingWithinEnergyUncertainty"`
}

type ShadowCondenserReset struct {
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ShadowEnergyResponse struct {
	Source                            string  `json:"source,omitempty"`
	DailyEnergyDeltaKWhPerSupplyTempC float64 `json:"dailyEnergyDeltaKWhPerSupplyTempC"`
	ReferenceChillerEnergyKWh         float64 `json:"referenceChillerEnergyKWh"`
	ReferenceSamples                  int     `json:"referenceSamples"`
	ReferenceHours                    int     `json:"referenceHours"`
	ReplacesPolicyValue               float64 `json:"replacesPolicyValue"`
}

type ShadowZoneResponse struct {
	Source                       string  `json:"source,omitempty"`
	ZoneTempDeltaCPerSupplyTempC float64 `json:"zoneTempDeltaCPerSupplyTempC"`
	ZoneTempUncertaintyP90C      float64 `json:"zoneTempUncertaintyP90C"`
}

type ShadowCandidate struct {
	SupplyTempC           float64 `json:"supplyTempC"`
	ProjectedZoneTempC    float64 `json:"projectedZoneTempC"`
	ChillerEnergyDeltaKWh float64 `json:"chillerEnergyDeltaKWh"`
	DailyEnergyKWh        float64 `json:"dailyEnergyKWh"`
	DailyCost             float64 `json:"dailyCost"`
	Extrapolated          bool    `json:"extrapolated"`
	Feasible              bool    `json:"feasible"`
	Reason                string  `json:"reason,omitempty"`
}

// PublishedShadowEvaluation is the read-only shadow diagnostics envelope for a Site's latest
// published Optimization run. It never carries an executable Recommendation.
type PublishedShadowEvaluation struct {
	EvaluationID              string                `json:"evaluationId"`
	RunID                     string                `json:"runId"`
	PublishedRecommendationID string                `json:"publishedRecommendationId"`
	GeneratedAt               time.Time             `json:"generatedAt"`
	Solver                    string                `json:"solver"`
	Mode                      string                `json:"mode"`
	Outcome                   string                `json:"outcome"`
	Reason                    string                `json:"reason,omitempty"`
	Fit                       *PlantFit             `json:"fit,omitempty"`
	CVRMSE                    *float64              `json:"cvRmse"`
	CondenserReset            *ShadowCondenserReset `json:"condenserReset"`
	EnergyResponse            *ShadowEnergyResponse `json:"energyResponse,omitempty"`
	ZoneResponse              *ShadowZoneResponse   `json:"zoneResponse,omitempty"`
	Candidates                []ShadowCandidate     `json:"candidates,omitempty"`
	Staging                   *StagingEvaluation    `json:"staging,omitempty"`
	Summary                   *ShadowSummary        `json:"summary,omitempty"`
}

func (service *Service) WithShadowReader(reader ShadowEvaluationReader) *Service {
	service.shadowReader = reader
	return service
}

// WithStaging configures Decision 3A chiller stage-down and idle auxiliary shutdown evaluation
// without requiring zone_temperature or writable chilled-water setpoints.
func (service *Service) WithStaging(evaluator *StagingEvaluator) *Service {
	service.staging = evaluator
	return service
}

func (service *Service) WithSolver(solver SolverAdapter) *Service {
	if solver != nil {
		service.solver = solver
	}
	return service
}

func (service *Service) WithSolverResolver(resolver ActiveSolverResolver) *Service {
	service.solverResolver = resolver
	return service
}

func (service *Service) WithGovernanceFlywheel(store GovernanceFlywheelStore) *Service {
	service.flywheelStore = store
	return service
}

func (service *Service) WithSiteVisibility(checker SiteVisibilityChecker) *Service {
	service.siteVisibility = checker
	return service
}

func (service *Service) WithCanaryStore(store CanaryBindingStore) *Service {
	service.canaryStore = store
	return service
}

func (service *Service) WithAllowedSolverHosts(hosts []string) *Service {
	service.allowedSolverHosts = append([]string(nil), hosts...)
	return service
}

func (service *Service) effectiveSolverResolver() ActiveSolverResolver {
	if service == nil {
		return nil
	}
	if service.solverResolver != nil {
		return service.solverResolver
	}
	if resolver, ok := service.publication.(ActiveSolverResolver); ok {
		return resolver
	}
	return nil
}

func (service *Service) effectiveFlywheelStore() GovernanceFlywheelStore {
	if service == nil {
		return nil
	}
	if service.flywheelStore != nil {
		return service.flywheelStore
	}
	if flywheel, ok := service.publication.(GovernanceFlywheelStore); ok {
		return flywheel
	}
	return nil
}

func (service *Service) effectiveSiteVisibilityChecker() SiteVisibilityChecker {
	if service == nil {
		return nil
	}
	if service.siteVisibility != nil {
		return service.siteVisibility
	}
	if checker, ok := service.publication.(SiteVisibilityChecker); ok {
		return checker
	}
	return nil
}

// ShadowSolver is a solver run beside the published one: it only records what it would have
// recommended and never publishes a Recommendation.
type ShadowSolver interface {
	Evaluate(context.Context, Request, time.Time) ShadowEvaluation
}

type shadowSolver struct {
	solver  ShadowSolver
	timeout time.Duration
	logf    func(string, ...any)
}

// WithShadow runs solver after every published Optimization run and records its evaluation as a
// SHADOW row in the same Evaluation sink. Its failure never changes the published run.
func (service *Service) WithShadow(solver ShadowSolver, timeout time.Duration, logf func(string, ...any)) *Service {
	if solver == nil {
		service.shadow = nil
		return service
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	service.shadow = &shadowSolver{solver: solver, timeout: timeout, logf: logf}
	return service
}

func (service *Service) recordShadow(ctx context.Context, request Request, published Recommendation, at time.Time) {
	if service.shadow == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.shadow.timeout)
	defer cancel()
	result := service.shadow.solver.Evaluate(ctx, request, at)
	payload, err := json.Marshal(struct {
		ShadowEvaluation
		PublishedRecommendationID string `json:"publishedRecommendationId"`
	}{result, published.ID})
	if err != nil {
		service.shadow.logf("optimization shadow %s for run %s: encode: %v", result.Solver, request.OptimizationRunID, err)
		return
	}
	evaluationID, err := optimizationUUIDv7(service.clock().UTC())
	if err != nil {
		service.shadow.logf("optimization shadow %s for run %s: evaluation id: %v", result.Solver, request.OptimizationRunID, err)
		return
	}
	constraints := uint32(0)
	if result.Recommendation != nil {
		constraints = uint32(len(result.Recommendation.Constraints))
	}
	evaluation := Evaluation{
		EvaluationID: evaluationID, TenantID: request.TenantID, SiteID: request.SiteID,
		OptimizationRunID: request.OptimizationRunID, RecommendationID: published.ID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		Objective: request.Objective, SolverOutcome: result.Outcome, Quality: "SHADOW", ConstraintCount: constraints,
		InputSnapshotID: request.InputSnapshotID, PolicyVersionID: request.PolicyVersionID, TopologyVersionID: request.TopologyVersionID,
		LoadForecastSnapshotID: request.LoadForecastSnapshotID, PVForecastSnapshotID: request.PVForecastSnapshotID, TariffVersionID: request.TariffVersionID,
		EvaluationJSON: string(payload), GeneratedAt: service.clock().UTC(),
	}
	if err = service.evaluations.InsertEvaluation(ctx, evaluation); err != nil {
		service.shadow.logf("optimization shadow %s for run %s: persist: %v", result.Solver, request.OptimizationRunID, err)
	}
}

func NewService(solver SolverAdapter, publication EvaluationPublicationStore, evaluations EvaluationSink, clock Clock) (*Service, error) {
	if solver == nil || publication == nil || evaluations == nil {
		return nil, fmt.Errorf("optimization solver, PostgreSQL publication store and ClickHouse evaluation sink are required")
	}
	if clock == nil {
		clock = time.Now
	}
	service := &Service{solver: solver, publication: publication, evaluations: evaluations, clock: clock}
	if resolver, ok := publication.(ActiveSolverResolver); ok {
		service.solverResolver = resolver
	}
	if flywheel, ok := publication.(GovernanceFlywheelStore); ok {
		service.flywheelStore = flywheel
	}
	if checker, ok := publication.(SiteVisibilityChecker); ok {
		service.siteVisibility = checker
	}
	return service, nil
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
	service.recordShadow(ctx, request, recommendation, startedAt)
	return recommendation, nil
}

func (service *Service) LatestRecommendation(ctx context.Context, tenantID, siteID string) (PublishedRecommendation, error) {
	return service.publication.LatestRecommendation(ctx, tenantID, siteID)
}

// LatestPreparation is how the Site's latest Optimization schedule firing went.
func (service *Service) LatestPreparation(ctx context.Context, tenantID, siteID string) (PreparationOutcome, error) {
	return service.publication.LatestPreparation(ctx, tenantID, siteID)
}

// LatestShadowEvaluation reads the SHADOW evaluation recorded beside the Site's latest published
// Optimization run, stripping any executable Recommendation payload.
func (service *Service) LatestShadowEvaluation(ctx context.Context, tenantID, siteID string) (PublishedShadowEvaluation, error) {
	published, err := service.publication.LatestRecommendation(ctx, tenantID, siteID)
	if errors.Is(err, ErrOptimizationNotFound) {
		return PublishedShadowEvaluation{}, ErrShadowEvaluationNotFound
	}
	if err != nil {
		return PublishedShadowEvaluation{}, err
	}
	if service.shadowReader == nil {
		return PublishedShadowEvaluation{}, ErrShadowEvaluationNotFound
	}
	record, err := service.shadowReader.ReadShadowEvaluation(ctx, ShadowEvaluationQuery{
		TenantID:          tenantID,
		SiteID:            siteID,
		OptimizationRunID: published.RunID,
	})
	if err != nil {
		return PublishedShadowEvaluation{}, err
	}
	var stored struct {
		ShadowEvaluation
		PublishedRecommendationID string `json:"publishedRecommendationId"`
	}
	if err := json.Unmarshal([]byte(record.EvaluationJSON), &stored); err != nil {
		return PublishedShadowEvaluation{}, fmt.Errorf("decode shadow evaluation payload: %w", err)
	}
	publishedRecID := stored.PublishedRecommendationID
	if publishedRecID == "" {
		publishedRecID = record.RecommendationID
	}
	return PublishedShadowEvaluation{
		EvaluationID:              record.EvaluationID,
		RunID:                     published.RunID,
		PublishedRecommendationID: publishedRecID,
		GeneratedAt:               record.GeneratedAt.UTC(),
		Solver:                    stored.Solver,
		Mode:                      stored.Mode,
		Outcome:                   stored.Outcome,
		Reason:                    stored.Reason,
		Fit:                       stored.Fit,
		CVRMSE:                    stored.CVRMSE,
		CondenserReset:            stored.CondenserReset,
		EnergyResponse:            stored.EnergyResponse,
		ZoneResponse:              stored.ZoneResponse,
		Candidates:                stored.Candidates,
		Staging:                   stored.Staging,
		Summary:                   summarizeShadowRecommendation(stored.Recommendation),
	}, nil
}

// LatestStagingEvaluation evaluates Decision 3A chiller staging and idle auxiliary recommendations
// directly from COP fits and live telemetry, without requiring zone_temperature.
func (service *Service) LatestStagingEvaluation(ctx context.Context, tenantID, siteID string) (StagingEvaluation, error) {
	if service.staging == nil {
		return StagingEvaluation{}, ErrStagingEvaluationNotFound
	}
	return service.staging.Evaluate(ctx, tenantID, siteID, service.clock().UTC())
}

func summarizeShadowRecommendation(rec *Recommendation) *ShadowSummary {
	if rec == nil {
		return nil
	}
	return &ShadowSummary{
		SupplyTempC:                   rec.Candidate.SupplyTempC,
		ProjectedZoneTempC:            rec.Candidate.ProjectedZoneTempC,
		DailyEnergyKWh:                rec.Candidate.DailyEnergyKWh,
		DailyCost:                     rec.Candidate.DailyCost,
		EnergySavingKWhPerDay:         rec.ExpectedImpact.EnergySavingKWhPerDay,
		CostSavingPerDay:              rec.ExpectedImpact.CostSavingPerDay,
		EnergyP90KWh:                  rec.Uncertainty.EnergyP90KWh,
		ZoneTempP90C:                  rec.Uncertainty.ZoneTempP90C,
		ComfortMarginC:                rec.Risk.ComfortMarginC,
		RiskLevel:                     rec.Risk.Level,
		Extrapolated:                  rec.Risk.Extrapolated != nil && *rec.Risk.Extrapolated,
		SavingWithinEnergyUncertainty: rec.Risk.SavingWithinEnergyUncertainty != nil && *rec.Risk.SavingWithinEnergyUncertainty,
	}
}

func (service *Service) Reconcile(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	stale, err := service.publication.ListStalePublications(ctx, staleBefore.UTC(), limit)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, evaluation := range stale {
		present, checkErr := service.evaluations.HasEvaluation(ctx, evaluation)
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

func timePointer(at time.Time) *time.Time {
	copyAt := at.UTC()
	return &copyAt
}
