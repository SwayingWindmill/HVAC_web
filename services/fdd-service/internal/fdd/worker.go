package fdd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// An evaluation is one ClickHouse read and one Postgres write; a minute's lease covers it.
const (
	evaluationLease   = time.Minute
	finalizeTimeout   = 5 * time.Second
	defaultJobTimeout = time.Minute
)

type evaluationJobQueue interface {
	Claim(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]schedulerjob.Job, error)
	Start(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Complete(ctx context.Context, job schedulerjob.Job, output any, now time.Time) error
	Fail(ctx context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, now time.Time) error
}

type lowDeltaTEvaluator interface {
	EvaluateLowDeltaT(ctx context.Context, input EvaluationRequest) (EvaluationResult, error)
}

type meterImbalanceEvaluator interface {
	EvaluateMeterImbalance(ctx context.Context, input MeterImbalanceRequest) (MeterImbalanceResult, error)
}

// MeterBalanceSiteLister lists Tenant and Site scopes eligible for periodic meter-imbalance checks.
type MeterBalanceSiteLister interface {
	ListRecentMeterBalanceSites(ctx context.Context, from, to time.Time) ([]SiteRuleScope, error)
}

type scheduledEvaluationPlan struct {
	ruleCode       string
	lowDeltaT      EvaluationRequest
	meterImbalance MeterImbalanceRequest
}

func (store *PostgresStore) EvaluationQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, EvaluationJobType)
}

// RunScheduledEvaluations claims the due FDD_EVALUATION jobs and evaluates each one. A window
// without readings is a settled outcome, not a retry; a failed read or write is retried.
func RunScheduledEvaluations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	return RunScheduledEvaluationsWithLogger(ctx, queue, service, workerID, batch, slog.Default())
}

// RunScheduledEvaluationsWithLogger claims and evaluates due FDD_EVALUATION jobs with structured logging.
func RunScheduledEvaluationsWithLogger(ctx context.Context, queue evaluationJobQueue, service lowDeltaTEvaluator, workerID string, batch int, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	jobs, err := queue.Claim(ctx, workerID, batch, evaluationLease, time.Now().UTC())
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("claim FDD evaluations: %w", err)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return nil
		}
		started, startErr := queue.Start(ctx, job, evaluationLease, time.Now().UTC())
		if startErr != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("fdd_evaluation_start_failed", "error_code", "FDD_JOB_START_FAILED", "job_id", job.JobID, "error", startErr)
			}
			continue
		}
		if !started {
			continue
		}
		plan, planErr := scheduledPlan(job)
		if planErr != nil {
			failEvaluationJob(ctx, queue, job, "FDD_EVALUATION_INVALID", planErr, false, logger)
			continue
		}
		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = defaultJobTimeout
		}
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		output, evaluationErr, unsupported := executeScheduledPlan(jobCtx, service, plan)
		cancel()
		if unsupported != nil {
			failEvaluationJob(ctx, queue, job, "FDD_EVALUATION_UNSUPPORTED", unsupported, false, logger)
			continue
		}
		finalizeEvaluationJob(ctx, queue, job, output, evaluationErr, logger)
	}
	return nil
}

// RunMeterImbalanceSweep evaluates METER_IMBALANCE over the trailing 24 hours for active Sites.
func RunMeterImbalanceSweep(ctx context.Context, sites MeterBalanceSiteLister, evaluator meterImbalanceEvaluator, now time.Time, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	if sites == nil || evaluator == nil {
		return errors.New("meter imbalance sweep dependencies are required")
	}
	to := now.UTC()
	from := to.Add(-meterImbalanceDefaultWindow)
	scopes, err := sites.ListRecentMeterBalanceSites(ctx, from, to)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("list meter imbalance sites: %w", err)
	}
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return nil
		}
		siteCtx, cancel := context.WithTimeout(ctx, defaultJobTimeout)
		_, evalErr := evaluator.EvaluateMeterImbalance(siteCtx, MeterImbalanceRequest{
			TenantID: scope.TenantID,
			SiteID:   scope.SiteID,
			From:     from,
			To:       to,
		})
		cancel()
		if evalErr != nil && ctx.Err() == nil && logger != nil {
			logger.Warn(
				"fdd_meter_imbalance_site_failed",
				"error_code", "FDD_METER_IMBALANCE_SITE_FAILED",
				"tenant_id", scope.TenantID,
				"site_id", scope.SiteID,
				"error", evalErr,
			)
		}
	}
	return nil
}

func scheduledPlan(job schedulerjob.Job) (scheduledEvaluationPlan, error) {
	if job.JobType != EvaluationJobType || job.TriggerType != "SCHEDULE" || job.ScheduledFor.IsZero() {
		return scheduledEvaluationPlan{}, errors.New("an FDD evaluation plan only comes from an FDD_EVALUATION schedule")
	}
	var raw struct {
		RuleCode string     `json:"ruleCode"`
		AssetID  string     `json:"assetId"`
		DeviceID string     `json:"deviceId"`
		FromUTC  *time.Time `json:"fromUtc"`
		ToUTC    *time.Time `json:"toUtc"`
	}
	if err := decodeStrictFDDJSON(bytes.NewReader(job.Payload), &raw); err != nil {
		return scheduledEvaluationPlan{}, fmt.Errorf("decode FDD evaluation plan: %w", err)
	}
	switch strings.TrimSpace(raw.RuleCode) {
	case "", LowDeltaTScheduleRuleCode, LowDeltaTRuleCode:
		if raw.FromUTC != nil || raw.ToUTC != nil {
			return scheduledEvaluationPlan{}, errors.New("fromUtc and toUtc are only valid for METER_IMBALANCE evaluations")
		}
		request := EvaluationRequest{
			TenantID: job.TenantID,
			SiteID:   job.SiteID,
			AssetID:  raw.AssetID,
			DeviceID: raw.DeviceID,
			At:       job.ScheduledFor.UTC(),
		}
		if err := request.Validate(); err != nil {
			return scheduledEvaluationPlan{}, err
		}
		return scheduledEvaluationPlan{ruleCode: LowDeltaTScheduleRuleCode, lowDeltaT: request}, nil
	case MeterImbalanceRuleCode:
		if raw.AssetID != "" || raw.DeviceID != "" {
			return scheduledEvaluationPlan{}, errors.New("assetId and deviceId are not valid for METER_IMBALANCE evaluations")
		}
		from, to, err := resolveMeterImbalanceWindow(job.ScheduledFor.UTC(), raw.FromUTC, raw.ToUTC)
		if err != nil {
			return scheduledEvaluationPlan{}, err
		}
		request := MeterImbalanceRequest{
			TenantID: job.TenantID,
			SiteID:   job.SiteID,
			From:     from,
			To:       to,
		}
		if err := request.Validate(); err != nil {
			return scheduledEvaluationPlan{}, err
		}
		return scheduledEvaluationPlan{ruleCode: MeterImbalanceRuleCode, meterImbalance: request}, nil
	default:
		return scheduledEvaluationPlan{}, fmt.Errorf("unsupported FDD evaluation ruleCode %q", raw.RuleCode)
	}
}

func executeScheduledPlan(ctx context.Context, service lowDeltaTEvaluator, plan scheduledEvaluationPlan) (map[string]any, error, error) {
	if plan.ruleCode == MeterImbalanceRuleCode {
		evaluator, ok := service.(meterImbalanceEvaluator)
		if !ok {
			return nil, nil, errors.New("FDD evaluator does not support meter imbalance")
		}
		result, err := evaluator.EvaluateMeterImbalance(ctx, plan.meterImbalance)
		if err != nil {
			return nil, err, nil
		}
		return meterImbalanceJobOutput(result), nil, nil
	}
	result, err := service.EvaluateLowDeltaT(ctx, plan.lowDeltaT)
	if err != nil {
		return nil, err, nil
	}
	return lowDeltaTJobOutput(result), nil, nil
}

func lowDeltaTJobOutput(result EvaluationResult) map[string]any {
	output := map[string]any{"status": result.Status, "deltaTC": result.DeltaTC, "operatingShare": result.Window.OperatingShare, "lowShare": result.Window.LowShare}
	if result.Window.Suppressed {
		output["suppressed"] = true
		output["suppressionReason"] = result.Window.SuppressionReason
		output["scheduleState"] = result.Window.ScheduleState
		if result.Window.SuppressionDetail != "" {
			output["suppressionDetail"] = result.Window.SuppressionDetail
		}
	}
	if result.Finding != nil {
		output["findingId"] = result.Finding.ID
	}
	return output
}

func meterImbalanceJobOutput(result MeterImbalanceResult) map[string]any {
	output := map[string]any{
		"ruleCode":        MeterImbalanceRuleCode,
		"status":          result.Status,
		"imbalanceStatus": result.ImbalanceStatus,
		"worstRatio":      result.WorstRatio,
		"observedGapKWh":  result.ObservedGapKWh,
		"thresholdRatio":  result.ThresholdRatio,
		"affectedHours":   result.AffectedHours,
	}
	if result.Finding != nil {
		output["findingId"] = result.Finding.ID
	}
	return output
}

func failEvaluationJob(ctx context.Context, queue evaluationJobQueue, job schedulerjob.Job, errorCode string, err error, retryable bool, logger *slog.Logger) {
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer finalizeCancel()
	finish(queue.Fail(finalizeCtx, job, errorCode, err, retryable, time.Now().UTC()), job, logger)
}

func finalizeEvaluationJob(ctx context.Context, queue evaluationJobQueue, job schedulerjob.Job, output map[string]any, evaluationErr error, logger *slog.Logger) {
	finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer finalizeCancel()
	switch {
	case evaluationErr != nil && ctx.Err() != nil:
		finish(queue.Fail(finalizeCtx, job, "FDD_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job, logger)
	case errors.Is(evaluationErr, ErrRuleNotFound):
		finish(queue.Fail(finalizeCtx, job, "FDD_RULE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job, logger)
	case errors.Is(evaluationErr, ErrNoLoopEvidence):
		finish(queue.Fail(finalizeCtx, job, "FDD_EVIDENCE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job, logger)
	case evaluationErr != nil:
		finish(queue.Fail(finalizeCtx, job, "FDD_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job, logger)
	default:
		finish(queue.Complete(finalizeCtx, job, output, time.Now().UTC()), job, logger)
	}
}

func finish(err error, job schedulerjob.Job, logger *slog.Logger) {
	if err != nil && logger != nil {
		logger.Warn("fdd_evaluation_finalize_failed", "error_code", "FDD_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}
