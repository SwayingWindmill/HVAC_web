package metric

import (
	"context"
	"errors"
	"time"
)

func (s *PostgresStore) CancelRunningMetricJob(ctx context.Context, job SchedulerJob, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,completed_at=$3,error_code='CANCELLED',error_message='CANCELLED',updated_at=$3
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.JobID, job.WorkerID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("metric job cancel transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status='CANCELLED',error_code='CANCELLED',error_message='CANCELLED',duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.JobID, job.AttemptNo, job.WorkerID, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
