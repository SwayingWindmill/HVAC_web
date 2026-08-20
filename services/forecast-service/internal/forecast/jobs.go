package forecast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
)

type SchedulerJob struct {
	JobID          string
	ScheduleID     string
	TriggerType    string
	JobType        string
	TenantID       string
	SiteID         string
	ScheduledFor   time.Time
	Payload        json.RawMessage
	AttemptNo      int
	MaxAttempts    int
	TimeoutSeconds int
	TraceID        string
	WorkerID       string
}

func (store *PostgresStore) ClaimForecastJobs(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]SchedulerJob, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("forecast postgres store is unavailable")
	}
	if workerID == "" || batch <= 0 || batch > 100 || leaseDuration <= 0 {
		return nil, errors.New("forecast job claim parameters are invalid")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT job_id::text,COALESCE(schedule_id::text,''),trigger_type,job_type,tenant_id::text,site_id::text,scheduled_for,payload,attempt_count,max_attempts,timeout_seconds,COALESCE(trace_id,'')
FROM core_registry.job_instances
WHERE state='READY' AND cancel_requested=false AND tenant_id IS NOT NULL AND site_id IS NOT NULL AND job_type='FORECAST_RUN'
ORDER BY priority DESC, scheduled_for ASC
FOR UPDATE SKIP LOCKED
LIMIT $1`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]SchedulerJob, 0, batch)
	for rows.Next() {
		var job SchedulerJob
		var currentAttempt int
		if err = rows.Scan(&job.JobID, &job.ScheduleID, &job.TriggerType, &job.JobType, &job.TenantID, &job.SiteID, &job.ScheduledFor, &job.Payload, &currentAttempt, &job.MaxAttempts, &job.TimeoutSeconds, &job.TraceID); err != nil {
			return nil, err
		}
		job.AttemptNo = currentAttempt + 1
		job.WorkerID = workerID
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, job := range jobs {
		tag, updateErr := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CLAIMED',lease_owner=$2,lease_until=$3,updated_at=$4,error_code=NULL,error_message=NULL
WHERE job_id=$1::uuid AND state='READY'`, job.JobID, workerID, now.UTC().Add(leaseDuration), now.UTC())
		if updateErr != nil {
			return nil, updateErr
		}
		if tag.RowsAffected() != 1 {
			return nil, errors.New("forecast job claim lost after row lock")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (store *PostgresStore) StartForecastJob(ctx context.Context, job SchedulerJob, leaseDuration time.Duration, now time.Time) (bool, error) {
	attemptID, err := uuidv7(now)
	if err != nil {
		return false, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var cancelRequested bool
	if err = tx.QueryRow(ctx, `SELECT cancel_requested FROM core_registry.job_instances
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2 FOR UPDATE`, job.JobID, job.WorkerID).Scan(&cancelRequested); err != nil {
		return false, err
	}
	if cancelRequested {
		_, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,completed_at=$3,error_code='CANCEL_REQUESTED',error_message='CANCEL_REQUESTED',updated_at=$3
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2`, job.JobID, job.WorkerID, now.UTC())
		if err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.job_attempts(attempt_id,job_id,attempt_no,worker_id,started_at)
VALUES($1::uuid,$2::uuid,$3,$4,$5)`, attemptID, job.JobID, job.AttemptNo, job.WorkerID, now.UTC()); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='RUNNING',attempt_count=$3,started_at=COALESCE(started_at,$4),lease_until=$4+$5::interval,updated_at=$4
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2`, job.JobID, job.WorkerID, job.AttemptNo, now.UTC(), leaseDuration.String())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, errors.New("forecast job RUNNING transition rejected")
	}
	return true, tx.Commit(ctx)
}

func (store *PostgresStore) RenewForecastJobLease(ctx context.Context, job SchedulerJob, leaseDuration time.Duration, now time.Time) (bool, error) {
	var cancelRequested bool
	err := store.pool.QueryRow(ctx, `UPDATE core_registry.job_instances
SET lease_until=$3,updated_at=$4
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2
RETURNING cancel_requested`, job.JobID, job.WorkerID, now.UTC().Add(leaseDuration), now.UTC()).Scan(&cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("forecast job lease ownership was lost")
	}
	return cancelRequested, err
}

func (store *PostgresStore) CompleteForecastJob(ctx context.Context, job SchedulerJob, output map[string]any, now time.Time) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='SUCCEEDED',lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,completed_at=$3,error_code=NULL,error_message=NULL,updated_at=$3
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.JobID, job.WorkerID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("forecast job success transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status='SUCCEEDED',duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint,output_summary=$5::jsonb
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.JobID, job.AttemptNo, job.WorkerID, now.UTC(), encoded); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) FailForecastJob(ctx context.Context, job SchedulerJob, errorCode string, executionErr error, retryable bool, now time.Time) error {
	if errorCode == "" {
		errorCode = "FORECAST_JOB_FAILED"
	}
	message := errorCode
	if executionErr != nil {
		message = executionErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	state, attemptStatus := "FAILED", "FAILED"
	var nextRetry any
	var completed any = now.UTC()
	if retryable && job.AttemptNo < job.MaxAttempts {
		state, attemptStatus = "RETRY_WAIT", "RETRY_WAIT"
		nextRetry = now.UTC().Add(forecastRetryDelay(job.AttemptNo))
		completed = nil
	} else if job.AttemptNo >= job.MaxAttempts {
		state, attemptStatus = "DEAD", "DEAD"
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state=$3,lease_owner=NULL,lease_until=NULL,next_retry_at=$4,completed_at=$5,error_code=$6,error_message=$7,updated_at=$8
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.JobID, job.WorkerID, state, nextRetry, completed, errorCode, message, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("forecast job failure transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status=$5,error_code=$6,error_message=$7,duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.JobID, job.AttemptNo, job.WorkerID, now.UTC(), attemptStatus, errorCode, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ValidateForecastSchedulerJob(job SchedulerJob) (Request, error) {
	if job.JobType != "FORECAST_RUN" || job.TenantID == "" || job.SiteID == "" || job.TimeoutSeconds <= 0 || job.AttemptNo <= 0 || job.MaxAttempts <= 0 {
		return Request{}, errors.New("forecast scheduler job envelope is invalid")
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("decode FORECAST_RUN payload: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Request{}, err
	}
	if request.TenantID != job.TenantID || request.SiteID != job.SiteID {
		return Request{}, errors.New("FORECAST_RUN payload scope must match scheduler Job scope")
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

func forecastRetryDelay(attempt int) time.Duration {
	base := 5 * time.Second
	switch attempt {
	case 2:
		base = 30 * time.Second
	case 3:
		base = 2 * time.Minute
	case 4:
		base = 10 * time.Minute
	default:
		if attempt >= 5 {
			base = 30 * time.Minute
		}
	}
	return time.Duration(float64(base) * (0.8 + rand.Float64()*0.4))
}
