package fdd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// An evaluation is one ClickHouse read and one Postgres write; a minute's lease covers it.
const evaluationLease = time.Minute

func (store *PostgresStore) EvaluationQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, EvaluationJobType)
}

// RunScheduledEvaluations claims the due FDD_EVALUATION jobs and evaluates each one. A window
// without readings is a settled outcome, not a retry; a failed read or write is retried.
func RunScheduledEvaluations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	jobs, err := queue.Claim(ctx, workerID, batch, evaluationLease, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("claim FDD evaluations: %w", err)
	}
	for _, job := range jobs {
		started, startErr := queue.Start(ctx, job, evaluationLease, time.Now().UTC())
		if startErr != nil {
			log.Printf("FDD evaluation %s start failed: %v", job.JobID, startErr)
			continue
		}
		if !started {
			continue
		}
		request, planErr := ScheduledRequest(job)
		if planErr != nil {
			finish(queue.Fail(context.Background(), job, "FDD_EVALUATION_INVALID", planErr, false, time.Now().UTC()), job)
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
		result, evaluationErr := service.EvaluateLowDeltaT(jobCtx, request)
		cancel()
		switch {
		case errors.Is(evaluationErr, ErrRuleNotFound):
			finish(queue.Fail(context.Background(), job, "FDD_RULE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job)
		case errors.Is(evaluationErr, ErrNoLoopEvidence):
			finish(queue.Fail(context.Background(), job, "FDD_EVIDENCE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job)
		case evaluationErr != nil:
			finish(queue.Fail(context.Background(), job, "FDD_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job)
		default:
			output := map[string]any{"status": result.Status, "deltaTC": result.DeltaTC, "operatingShare": result.Window.OperatingShare, "lowShare": result.Window.LowShare}
			if result.Finding != nil {
				output["findingId"] = result.Finding.ID
			}
			finish(queue.Complete(context.Background(), job, output, time.Now().UTC()), job)
		}
	}
	return nil
}

func finish(err error, job schedulerjob.Job) {
	if err != nil {
		log.Printf("FDD evaluation %s could not be finalized: %v", job.JobID, err)
	}
}
