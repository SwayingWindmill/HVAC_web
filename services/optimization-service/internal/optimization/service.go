package optimization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Problem struct {
	Request     Request
	Objective   Objective
	Constraints ConstraintSet
}

type Objective struct{ Kind string }

type ConstraintSet struct{ Resources []Resource }

type ProblemBuilder interface {
	Build(context.Context, Request, Objective, ConstraintSet) (Problem, error)
}

type ObjectiveBuilder interface {
	Build(context.Context, Request) (Objective, error)
}

type ConstraintBuilder interface {
	Build(context.Context, Request) (ConstraintSet, error)
}

type SolverAdapter interface {
	Solve(context.Context, Problem) (Plan, error)
}

type defaultProblemBuilder struct{}
type defaultObjectiveBuilder struct{}
type defaultConstraintBuilder struct{}
type safeNoDispatchSolver struct{}

func (defaultObjectiveBuilder) Build(_ context.Context, request Request) (Objective, error) {
	return Objective{Kind: request.Objective}, nil
}

func (defaultConstraintBuilder) Build(_ context.Context, request Request) (ConstraintSet, error) {
	return ConstraintSet{Resources: append([]Resource(nil), request.Resources...)}, nil
}

func (defaultProblemBuilder) Build(_ context.Context, request Request, objective Objective, constraints ConstraintSet) (Problem, error) {
	return Problem{Request: request, Objective: objective, Constraints: constraints}, nil
}

func (safeNoDispatchSolver) Solve(_ context.Context, problem Problem) (Plan, error) {
	request := problem.Request
	if err := request.Validate(); err != nil {
		return Plan{}, err
	}
	validFrom := request.ValidFrom.UTC()
	validTo := validFrom.Add(24 * time.Hour)
	planID := deterministicV7(request.OptimizationRunID, "plan:1", validFrom)
	intervals := make([]Interval, 0, len(problem.Constraints.Resources)*96)
	for _, resource := range problem.Constraints.Resources {
		for ordinal := 0; ordinal < 96; ordinal++ {
			startTime := validFrom.Add(time.Duration(ordinal*15) * time.Minute)
			endTime := startTime.Add(15 * time.Minute)
			margin := map[string]any{
				"mode": "NO_DISPATCH", "availability": resource.Availability, "controlMode": resource.ControlMode,
				"chargePowerLimitKw": resource.ChargePowerLimitKW, "dischargePowerLimitKw": resource.DischargePowerLimitKW,
			}
			intervals = append(intervals, Interval{
				IntervalID: deterministicV7(planID, fmt.Sprintf("%s|%d", resource.ResourceID, ordinal), startTime),
				ResourceID: resource.ResourceID, StartTime: startTime, EndTime: endTime,
				TargetType: "POWER_SETPOINT", TargetValue: 0, Unit: "kW", ExpectedSOC: resource.SOC,
				ConstraintMargin: margin, Ordinal: ordinal,
			})
		}
	}
	return Plan{
		PlanID: planID, OptimizationRunID: request.OptimizationRunID, InputSnapshotID: request.InputSnapshotID,
		InputChecksum: request.InputChecksum, PolicyVersionID: request.PolicyVersionID, TopologyVersionID: request.TopologyVersionID,
		LoadForecastSnapshotID: request.LoadForecastSnapshotID, PVForecastSnapshotID: request.PVForecastSnapshotID,
		TariffVersionID: request.TariffVersionID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		PlanVersion: 1, Quality: "FALLBACK", Status: "DRAFT", ValidFrom: validFrom, ValidTo: validTo,
		Objective: problem.Objective.Kind, FallbackPolicy: "NO_DISPATCH",
		Explanation: map[string]any{
			"reason": "safe_no_dispatch_baseline", "dispatchMode": request.DispatchMode,
			"inputSnapshotId": request.InputSnapshotID, "loadForecastSnapshotId": request.LoadForecastSnapshotID,
			"tariffVersionId": request.TariffVersionID, "topologyVersionId": request.TopologyVersionID,
		},
		Intervals: intervals,
	}, nil
}

type Clock func() time.Time

type Service struct {
	problemBuilder    ProblemBuilder
	objectiveBuilder  ObjectiveBuilder
	constraintBuilder ConstraintBuilder
	solver            SolverAdapter
	publication       EvaluationPublicationStore
	evaluations       EvaluationSink
	clock             Clock
}

func NewService(problemBuilder ProblemBuilder, objectiveBuilder ObjectiveBuilder, constraintBuilder ConstraintBuilder, solver SolverAdapter, publication EvaluationPublicationStore, evaluations EvaluationSink, clock Clock) (*Service, error) {
	if problemBuilder == nil || objectiveBuilder == nil || constraintBuilder == nil || solver == nil {
		return nil, fmt.Errorf("optimization builders and solver adapter are required")
	}
	if publication == nil || evaluations == nil {
		return nil, fmt.Errorf("optimization PostgreSQL publication store and ClickHouse evaluation sink are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{problemBuilder: problemBuilder, objectiveBuilder: objectiveBuilder, constraintBuilder: constraintBuilder, solver: solver, publication: publication, evaluations: evaluations, clock: clock}, nil
}

func NewDefaultService(publication EvaluationPublicationStore, evaluations EvaluationSink, clock Clock) (*Service, error) {
	return NewService(defaultProblemBuilder{}, defaultObjectiveBuilder{}, defaultConstraintBuilder{}, safeNoDispatchSolver{}, publication, evaluations, clock)
}

func (service *Service) Optimize(ctx context.Context, request Request) (Plan, error) {
	if err := request.Validate(); err != nil {
		return Plan{}, err
	}
	startedAt := service.clock().UTC()
	if err := service.publication.StartRun(ctx, request, startedAt); err != nil {
		return Plan{}, fmt.Errorf("start optimization run: %w", err)
	}
	fail := func(code string) { _ = service.publication.FailRun(ctx, request, code, service.clock().UTC()) }
	objective, err := service.objectiveBuilder.Build(ctx, request)
	if err != nil {
		fail("OBJECTIVE_BUILD_FAILED")
		return Plan{}, fmt.Errorf("build objective: %w", err)
	}
	constraints, err := service.constraintBuilder.Build(ctx, request)
	if err != nil {
		fail("CONSTRAINT_BUILD_FAILED")
		return Plan{}, fmt.Errorf("build constraints: %w", err)
	}
	problem, err := service.problemBuilder.Build(ctx, request, objective, constraints)
	if err != nil {
		fail("PROBLEM_BUILD_FAILED")
		return Plan{}, fmt.Errorf("build optimization problem: %w", err)
	}
	plan, err := service.solver.Solve(ctx, problem)
	if err != nil {
		fail("SOLVER_ERROR")
		return Plan{}, fmt.Errorf("solve optimization problem: %w", err)
	}
	evaluationID, err := optimizationUUIDv7(service.clock().UTC())
	if err != nil {
		fail("EVALUATION_ID_FAILED")
		return Plan{}, err
	}
	evaluationPayload, _ := json.Marshal(map[string]any{"planId": plan.PlanID, "explanation": plan.Explanation, "fallbackPolicy": plan.FallbackPolicy})
	evaluation := Evaluation{
		EvaluationID: evaluationID, TenantID: request.TenantID, SiteID: request.SiteID,
		OptimizationRunID: request.OptimizationRunID, DispatchPlanID: plan.PlanID, SubjectType: request.SubjectType, SubjectID: request.SubjectID,
		Objective: request.Objective, SolverOutcome: "FEASIBLE", Quality: plan.Quality, IntervalCount: uint32(len(plan.Intervals)),
		InputSnapshotID: request.InputSnapshotID, PolicyVersionID: request.PolicyVersionID, TopologyVersionID: request.TopologyVersionID,
		LoadForecastSnapshotID: request.LoadForecastSnapshotID, PVForecastSnapshotID: request.PVForecastSnapshotID, TariffVersionID: request.TariffVersionID,
		EvaluationJSON: string(evaluationPayload), GeneratedAt: service.clock().UTC(),
	}
	if err = service.publication.BeginPublication(ctx, request, plan, evaluation, service.clock().UTC()); err != nil {
		fail("PUBLICATION_BEGIN_FAILED")
		return Plan{}, fmt.Errorf("begin optimization evaluation publication: %w", err)
	}
	if err = service.evaluations.InsertEvaluation(ctx, evaluation); err != nil {
		fail("CLICKHOUSE_WRITE_FAILED")
		return Plan{}, fmt.Errorf("persist optimization evaluation: %w", err)
	}
	// ClickHouse success makes the Evaluation durable. A PostgreSQL completion
	// failure intentionally leaves the run PERSISTING for reconciliation.
	if err = service.publication.CompletePublication(ctx, evaluation, service.clock().UTC()); err != nil {
		return Plan{}, fmt.Errorf("complete optimization evaluation publication: %w", err)
	}
	return plan, nil
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
