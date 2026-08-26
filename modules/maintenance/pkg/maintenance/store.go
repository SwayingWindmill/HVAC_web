package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Job struct {
	ID             string
	Type           string
	TenantID       string
	Payload        json.RawMessage
	AttemptNo      int
	MaxAttempts    int
	TimeoutSeconds int
	WorkerID       string
}

type Store struct {
	pool *pgxpool.Pool
}

func OpenStore(ctx context.Context, dsn string) (*Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("maintenance PostgreSQL DSN is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse maintenance PostgreSQL DSN: %w", err)
	}
	if config.ConnConfig.User != "maintenance_runtime" {
		return nil, errors.New("maintenance PostgreSQL identity must be maintenance_runtime")
	}
	config.MaxConns = 4
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := NewStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("maintenance PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) ClaimJobs(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]Job, error) {
	if workerID == "" || batch <= 0 || batch > 100 || leaseDuration <= 0 {
		return nil, errors.New("maintenance job claim parameters are invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT job_id::text,job_type,COALESCE(tenant_id::text,''),payload,attempt_count,max_attempts,timeout_seconds
FROM core_registry.job_instances
WHERE state='READY' AND cancel_requested=false
  AND job_type IN ('CERTIFICATE_EXPIRY_SCAN','DEAD_WORK_DISPOSITION','TENANT_RETIREMENT')
ORDER BY priority DESC,scheduled_for ASC
FOR UPDATE SKIP LOCKED
LIMIT $1`, batch)
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for rows.Next() {
		var job Job
		var currentAttempt int
		if err = rows.Scan(&job.ID, &job.Type, &job.TenantID, &job.Payload, &currentAttempt, &job.MaxAttempts, &job.TimeoutSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		job.AttemptNo = currentAttempt + 1
		job.WorkerID = workerID
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, job := range jobs {
		tag, updateErr := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CLAIMED',lease_owner=$2,lease_until=$3,updated_at=$4,error_code=NULL,error_message=NULL
WHERE job_id=$1::uuid AND state='READY'`, job.ID, workerID, now.UTC().Add(leaseDuration), now.UTC())
		if updateErr != nil {
			return nil, updateErr
		}
		if tag.RowsAffected() != 1 {
			return nil, errors.New("maintenance job claim lost after row lock")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) StartJob(ctx context.Context, job Job, leaseDuration time.Duration, now time.Time) (bool, error) {
	attemptID, err := uuidv7(now)
	if err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var cancelRequested bool
	if err = tx.QueryRow(ctx, `SELECT cancel_requested FROM core_registry.job_instances
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2 FOR UPDATE`, job.ID, job.WorkerID).Scan(&cancelRequested); err != nil {
		return false, err
	}
	if cancelRequested {
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,completed_at=$3,error_code='CANCEL_REQUESTED',error_message='CANCEL_REQUESTED',updated_at=$3
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2`, job.ID, job.WorkerID, now.UTC()); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.job_attempts(attempt_id,job_id,attempt_no,worker_id,started_at)
VALUES($1::uuid,$2::uuid,$3,$4,$5)`, attemptID, job.ID, job.AttemptNo, job.WorkerID, now.UTC()); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='RUNNING',attempt_count=$3,started_at=COALESCE(started_at,$4),lease_until=$4+$5::interval,updated_at=$4
WHERE job_id=$1::uuid AND state='CLAIMED' AND lease_owner=$2`, job.ID, job.WorkerID, job.AttemptNo, now.UTC(), leaseDuration.String())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, errors.New("maintenance job RUNNING transition rejected")
	}
	return true, tx.Commit(ctx)
}

func (s *Store) RenewJobLease(ctx context.Context, job Job, leaseDuration time.Duration, now time.Time) (bool, error) {
	var cancelRequested bool
	err := s.pool.QueryRow(ctx, `UPDATE core_registry.job_instances SET lease_until=$3,updated_at=$4
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2
RETURNING cancel_requested`, job.ID, job.WorkerID, now.UTC().Add(leaseDuration), now.UTC()).Scan(&cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("maintenance job lease ownership was lost")
	}
	return cancelRequested, err
}

func (s *Store) CancelJob(ctx context.Context, job Job, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,completed_at=$3,error_code='CANCEL_REQUESTED',error_message='CANCEL_REQUESTED',updated_at=$3
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.ID, job.WorkerID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("maintenance job cancel transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status='CANCELLED',error_code='CANCEL_REQUESTED',error_message='CANCEL_REQUESTED',duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.ID, job.AttemptNo, job.WorkerID, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteJob(ctx context.Context, job Job, output map[string]any, now time.Time) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='SUCCEEDED',lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,completed_at=$3,error_code=NULL,error_message=NULL,updated_at=$3
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.ID, job.WorkerID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("maintenance job success transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status='SUCCEEDED',duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint,output_summary=$5::jsonb
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.ID, job.AttemptNo, job.WorkerID, now.UTC(), encoded); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailJob(ctx context.Context, job Job, code string, executionErr error, retryable bool, now time.Time) error {
	if code == "" {
		code = "MAINTENANCE_JOB_FAILED"
	}
	message := code
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
		nextRetry = now.UTC().Add(retryDelayWithJitter(job.AttemptNo))
		completed = nil
	} else if job.AttemptNo >= job.MaxAttempts {
		state, attemptStatus = "DEAD", "DEAD"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state=$3,lease_owner=NULL,lease_until=NULL,next_retry_at=$4,completed_at=$5,error_code=$6,error_message=$7,updated_at=$8
WHERE job_id=$1::uuid AND state='RUNNING' AND lease_owner=$2`, job.ID, job.WorkerID, state, nextRetry, completed, code, message, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("maintenance job failure transition rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=$4,result_status=$5,error_code=$6,error_message=$7,duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint
WHERE job_id=$1::uuid AND attempt_no=$2 AND worker_id=$3 AND completed_at IS NULL`, job.ID, job.AttemptNo, job.WorkerID, now.UTC(), attemptStatus, code, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retryDelayWithJitter(attempt int) time.Duration {
	var base time.Duration
	switch attempt {
	case 0, 1:
		base = 5 * time.Second
	case 2:
		base = 30 * time.Second
	case 3:
		base = 2 * time.Minute
	case 4:
		base = 10 * time.Minute
	default:
		base = 30 * time.Minute
	}
	return time.Duration(float64(base) * (0.8 + mathrand.Float64()*0.4))
}

func uuidv7(now time.Time) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ms := uint64(now.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

func validateJob(job Job) error {
	if job.ID == "" || job.WorkerID == "" || job.AttemptNo <= 0 || job.TimeoutSeconds <= 0 {
		return errors.New("maintenance scheduler job identity is incomplete")
	}
	switch job.Type {
	case "CERTIFICATE_EXPIRY_SCAN", "DEAD_WORK_DISPOSITION":
		return nil
	case "TENANT_RETIREMENT":
		if job.TenantID == "" {
			return errors.New("Tenant retirement requires tenant identity")
		}
		return nil
	default:
		return fmt.Errorf("unsupported maintenance scheduler job type %s", job.Type)
	}
}
