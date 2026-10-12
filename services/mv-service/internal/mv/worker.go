package mv

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// EvaluationJobType is the Scheduler job that verifies a Site's settled days once a day.
const EvaluationJobType = "MV_EVALUATION"

// A run reads at most a few weeks of days from ClickHouse; five minutes covers a catch-up.
const evaluationLease = 5 * time.Minute

// RunScheduledEvaluations claims due MV_EVALUATION jobs and verifies each Site's settled days up
// to the job's scheduled time. A failed read or write is retried; days already verified stay.
func RunScheduledEvaluations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	jobs, err := queue.Claim(ctx, workerID, batch, evaluationLease, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("claim M&V evaluations: %w", err)
	}
	for _, job := range jobs {
		started, startErr := queue.Start(ctx, job, evaluationLease, time.Now().UTC())
		if startErr != nil {
			log.Printf("M&V evaluation %s start failed: %v", job.JobID, startErr)
			continue
		}
		if !started {
			continue
		}
		if job.TriggerType != "SCHEDULE" || job.ScheduledFor.IsZero() {
			finish(queue.Fail(context.Background(), job, "MV_EVALUATION_INVALID", errors.New("an M&V evaluation only comes from an MV_EVALUATION schedule"), false, time.Now().UTC()), job)
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
		evaluationErr := service.VerifyDueDays(jobCtx, job.TenantID, job.SiteID, job.ScheduledFor)
		cancel()
		if evaluationErr != nil {
			finish(queue.Fail(context.Background(), job, "MV_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job)
			continue
		}
		finish(queue.Complete(context.Background(), job, map[string]any{"settledBy": job.ScheduledFor.Add(-SettlingDelay).UTC()}, time.Now().UTC()), job)
	}
	return nil
}

func finish(err error, job schedulerjob.Job) {
	if err != nil {
		log.Printf("M&V evaluation %s could not be finalized: %v", job.JobID, err)
	}
}
