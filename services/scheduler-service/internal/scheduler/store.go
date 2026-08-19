package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxCatchUpPerSchedule = 10000

type Schedule struct {
	ID                string
	Code              string
	Name              string
	TenantID          string
	SiteID            string
	JobType           string
	ScheduleType      string
	CronExpression    string
	CronFormatVersion string
	IntervalSeconds   int64
	Timezone          string
	MisfirePolicy     string
	CatchUpLimit      int
	ConcurrencyPolicy string
	TimeoutSeconds    int
	MaxAttempts       int
	RetryPolicy       json.RawMessage
	PayloadTemplate   json.RawMessage
	NextFireAt        time.Time
}

type ScanResult struct {
	Schedules int
	Created   int
	Skipped   int
	Retries   int
	Recovered int
	Cancelled int
	Promoted  int
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("scheduler PostgreSQL pool is required")
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

func (s *Store) Cycle(ctx context.Context, now time.Time, batch int, normalFireTolerance time.Duration) (ScanResult, error) {
	if batch <= 0 || batch > 1000 {
		return ScanResult{}, errors.New("scheduler batch must be within 1..1000")
	}
	result := ScanResult{}
	cancelled, err := s.finalizeRequestedCancellations(ctx, now, batch)
	if err != nil {
		return result, err
	}
	result.Cancelled = cancelled
	retries, err := s.promoteRetries(ctx, now, batch)
	if err != nil {
		return result, err
	}
	result.Retries = retries
	recovered, err := s.recoverExpiredLeases(ctx, now, batch)
	if err != nil {
		return result, err
	}
	result.Recovered = recovered
	promoted, err := s.promotePendingJobs(ctx, now, batch)
	if err != nil {
		return result, err
	}
	result.Promoted = promoted
	scan, err := s.scanDueSchedules(ctx, now, batch, normalFireTolerance)
	if err != nil {
		return result, err
	}
	result.Schedules = scan.Schedules
	result.Created = scan.Created
	result.Skipped = scan.Skipped
	return result, nil
}

func (s *Store) finalizeRequestedCancellations(ctx context.Context, now time.Time, batch int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT job_id::text
FROM core_registry.job_instances
WHERE cancel_requested=true AND state IN ('PENDING','READY','RETRY_WAIT')
ORDER BY scheduled_for ASC
FOR UPDATE SKIP LOCKED
LIMIT $1`, batch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='CANCELLED',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,completed_at=$2,error_code='CANCEL_REQUESTED',error_message='CANCEL_REQUESTED',updated_at=$2
WHERE job_id=$1::uuid AND cancel_requested=true AND state IN ('PENDING','READY','RETRY_WAIT')`, id, now.UTC()); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Store) promoteRetries(ctx context.Context, now time.Time, batch int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT job_id::text
FROM core_registry.job_instances
WHERE state='RETRY_WAIT' AND next_retry_at <= $1
ORDER BY priority DESC, scheduled_for ASC
FOR UPDATE SKIP LOCKED
LIMIT $2`, now.UTC(), batch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state='READY',next_retry_at=NULL,updated_at=$2
WHERE job_id=$1::uuid AND state='RETRY_WAIT'`, id, now.UTC()); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Store) recoverExpiredLeases(ctx context.Context, now time.Time, batch int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT job_id::text,job_type,attempt_count,max_attempts,cancel_requested
FROM core_registry.job_instances
WHERE state IN ('CLAIMED','RUNNING') AND lease_until < $1
ORDER BY lease_until ASC
FOR UPDATE SKIP LOCKED
LIMIT $2`, now.UTC(), batch)
	if err != nil {
		return 0, err
	}
	type expired struct {
		id              string
		jobType         string
		attempts        int
		max             int
		cancelRequested bool
	}
	var jobs []expired
	for rows.Next() {
		var job expired
		if err = rows.Scan(&job.id, &job.jobType, &job.attempts, &job.max, &job.cancelRequested); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, job := range jobs {
		state, resultStatus, errorCode := "FAILED", "FAILED", "LEASE_RECOVERY_REQUIRES_RECONCILIATION"
		var nextRetry any
		var completed any = now.UTC()
		if job.cancelRequested {
			state, resultStatus, errorCode = "CANCELLED", "CANCELLED", "CANCEL_REQUESTED"
		} else if isMetricJob(job.jobType) && job.attempts < job.max {
			state, resultStatus, errorCode = "RETRY_WAIT", "RETRY_WAIT", "LEASE_EXPIRED"
			nextRetry = now.UTC().Add(retryDelay(job.attempts))
			completed = nil
		} else if job.attempts >= job.max {
			state, resultStatus, errorCode = "DEAD", "DEAD", "LEASE_EXPIRED_ATTEMPTS_EXHAUSTED"
		}
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET state=$2,next_retry_at=$3,lease_owner=NULL,lease_until=NULL,completed_at=$4,error_code=$5,error_message=$5,updated_at=$6
WHERE job_id=$1::uuid AND state IN ('CLAIMED','RUNNING')`, job.id, state, nextRetry, completed, errorCode, now.UTC()); err != nil {
			return 0, err
		}
		if job.attempts > 0 {
			if _, err = tx.Exec(ctx, `UPDATE core_registry.job_attempts
SET completed_at=COALESCE(completed_at,$4),result_status=COALESCE(result_status,$3),error_code=COALESCE(error_code,$5),error_message=COALESCE(error_message,$5),duration_ms=COALESCE(duration_ms,GREATEST(0,EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint)
WHERE job_id=$1::uuid AND attempt_no=$2 AND completed_at IS NULL`, job.id, job.attempts, resultStatus, now.UTC(), errorCode); err != nil {
			return 0, err
		}
	}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(jobs), nil
}

func (s *Store) promotePendingJobs(ctx context.Context, now time.Time, batch int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT j.job_id::text
FROM core_registry.job_instances j
JOIN core_registry.schedule_definitions s ON s.schedule_id=j.schedule_id
WHERE j.state='PENDING' AND j.cancel_requested=false AND s.concurrency_policy='FORBID'
  AND NOT EXISTS (
    SELECT 1 FROM core_registry.job_instances active
    WHERE active.schedule_id=j.schedule_id
      AND active.job_id<>j.job_id
      AND active.state IN ('READY','CLAIMED','RUNNING','RETRY_WAIT')
  )
  AND NOT EXISTS (
    SELECT 1 FROM core_registry.job_instances earlier
    WHERE earlier.schedule_id=j.schedule_id
      AND earlier.state='PENDING'
      AND earlier.cancel_requested=false
      AND (earlier.scheduled_for < j.scheduled_for OR (earlier.scheduled_for=j.scheduled_for AND earlier.job_id < j.job_id))
  )
ORDER BY j.priority DESC,j.scheduled_for ASC
FOR UPDATE OF j SKIP LOCKED
LIMIT $1`, batch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances SET state='READY',updated_at=$2 WHERE job_id=$1::uuid AND state='PENDING'`, id, now.UTC()); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Store) scanDueSchedules(ctx context.Context, now time.Time, batch int, normalFireTolerance time.Duration) (ScanResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT schedule_id::text,schedule_code,schedule_name,
COALESCE(tenant_id::text,''),COALESCE(site_id::text,''),job_type,schedule_type,
COALESCE(cron_expression,''),COALESCE(cron_format_version,''),COALESCE(interval_seconds,0),timezone,
misfire_policy,COALESCE(catch_up_limit,0),concurrency_policy,timeout_seconds,max_attempts,retry_policy,payload_template,next_fire_at
FROM core_registry.schedule_definitions
WHERE enabled=true AND next_fire_at IS NOT NULL AND next_fire_at <= $1
ORDER BY next_fire_at ASC
FOR UPDATE SKIP LOCKED
LIMIT $2`, now.UTC(), batch)
	if err != nil {
		return ScanResult{}, err
	}
	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		if err = rows.Scan(
			&schedule.ID, &schedule.Code, &schedule.Name, &schedule.TenantID, &schedule.SiteID,
			&schedule.JobType, &schedule.ScheduleType, &schedule.CronExpression, &schedule.CronFormatVersion,
			&schedule.IntervalSeconds, &schedule.Timezone, &schedule.MisfirePolicy, &schedule.CatchUpLimit,
			&schedule.ConcurrencyPolicy, &schedule.TimeoutSeconds, &schedule.MaxAttempts,
			&schedule.RetryPolicy, &schedule.PayloadTemplate, &schedule.NextFireAt,
		); err != nil {
			rows.Close()
			return ScanResult{}, err
		}
		schedules = append(schedules, schedule)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ScanResult{}, err
	}
	rows.Close()

	result := ScanResult{Schedules: len(schedules)}
	for _, schedule := range schedules {
		fires, nextFire, disable, err := planSchedule(schedule, now.UTC(), normalFireTolerance)
		if err != nil {
			return result, fmt.Errorf("schedule %s: %w", schedule.Code, err)
		}
		var lastTriggered any
		for _, scheduledFor := range fires {
			created, skipped, err := createScheduledJob(ctx, tx, schedule, scheduledFor, now.UTC())
			if err != nil {
				return result, fmt.Errorf("schedule %s create job: %w", schedule.Code, err)
			}
			if created {
				result.Created++
				lastTriggered = scheduledFor.UTC()
			}
			if skipped {
				result.Skipped++
			}
		}
		if disable {
			if _, err = tx.Exec(ctx, `UPDATE core_registry.schedule_definitions
SET enabled=false,next_fire_at=NULL,last_fire_at=COALESCE($2,last_fire_at),version=version+1,updated_at=$3
WHERE schedule_id=$1::uuid`, schedule.ID, lastTriggered, now.UTC()); err != nil {
				return result, err
			}
			continue
		}
		if nextFire == nil {
			return result, fmt.Errorf("schedule %s did not produce next_fire_at", schedule.Code)
		}
		if _, err = tx.Exec(ctx, `UPDATE core_registry.schedule_definitions
SET next_fire_at=$2,last_fire_at=COALESCE($3,last_fire_at),version=version+1,updated_at=$4
WHERE schedule_id=$1::uuid`, schedule.ID, nextFire.UTC(), lastTriggered, now.UTC()); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func planSchedule(schedule Schedule, now time.Time, normalFireTolerance time.Duration) ([]time.Time, *time.Time, bool, error) {
	if schedule.NextFireAt.IsZero() {
		return nil, nil, false, errors.New("next_fire_at is required")
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil, nil, false, errors.New("schedule timezone must be a valid IANA timezone")
	}
	var cron cronSpec
	if schedule.ScheduleType == "CRON" {
		if schedule.CronFormatVersion != "5-field-v1" {
			return nil, nil, false, errors.New("unsupported cron_format_version")
		}
		cron, err = parseCron5(schedule.CronExpression)
		if err != nil {
			return nil, nil, false, err
		}
	}
	due := []time.Time{}
	cursor := schedule.NextFireAt.UTC()
	for !cursor.After(now) {
		due = append(due, cursor)
		if len(due) > maxCatchUpPerSchedule {
			return nil, nil, false, errors.New("misfire backlog exceeds scheduler safety bound")
		}
		if schedule.ScheduleType == "ONCE" {
			break
		}
		cursor, err = nextFire(schedule, cron, location, cursor)
		if err != nil {
			return nil, nil, false, err
		}
	}
	if len(due) == 0 {
		return nil, &cursor, false, nil
	}
	if schedule.ScheduleType == "ONCE" {
		return selectMisfires(schedule, due, now, normalFireTolerance), nil, true, nil
	}
	for !cursor.After(now) {
		cursor, err = nextFire(schedule, cron, location, cursor)
		if err != nil {
			return nil, nil, false, err
		}
	}
	return selectMisfires(schedule, due, now, normalFireTolerance), &cursor, false, nil
}

func nextFire(schedule Schedule, cron cronSpec, location *time.Location, current time.Time) (time.Time, error) {
	switch schedule.ScheduleType {
	case "CRON":
		return cron.next(current, location)
	case "INTERVAL":
		if schedule.IntervalSeconds <= 0 {
			return time.Time{}, errors.New("interval_seconds must be positive")
		}
		return current.UTC().Add(time.Duration(schedule.IntervalSeconds) * time.Second), nil
	default:
		return time.Time{}, errors.New("schedule type does not have a next fire")
	}
}

func selectMisfires(schedule Schedule, due []time.Time, now time.Time, tolerance time.Duration) []time.Time {
	if len(due) == 0 {
		return nil
	}
	latest := due[len(due)-1]
	switch schedule.MisfirePolicy {
	case "SKIP":
		if tolerance > 0 && now.Sub(latest) <= tolerance {
			return []time.Time{latest}
		}
		return nil
	case "FIRE_ONCE":
		return []time.Time{latest}
	case "CATCH_UP":
		return due
	case "CATCH_UP_LIMITED":
		limit := schedule.CatchUpLimit
		if limit <= 0 || len(due) <= limit {
			return due
		}
		return due[len(due)-limit:]
	default:
		return nil
	}
}

func createScheduledJob(ctx context.Context, tx pgx.Tx, schedule Schedule, scheduledFor, now time.Time) (created bool, skipped bool, err error) {
	active := false
	if schedule.ConcurrencyPolicy != "ALLOW" {
		if err = tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM core_registry.job_instances
WHERE schedule_id=$1::uuid AND state IN ('PENDING','READY','CLAIMED','RUNNING','RETRY_WAIT')
)`, schedule.ID).Scan(&active); err != nil {
			return false, false, err
		}
	}
	if active && schedule.ConcurrencyPolicy == "REPLACE" {
		if _, err = tx.Exec(ctx, `UPDATE core_registry.job_instances
SET cancel_requested=true,updated_at=$2
WHERE schedule_id=$1::uuid AND state IN ('PENDING','READY','CLAIMED','RUNNING','RETRY_WAIT')`, schedule.ID, now.UTC()); err != nil {
			return false, false, err
		}
		active = false
	}

	jobID, err := uuidv7(now)
	if err != nil {
		return false, false, err
	}
	traceID, err := randomHex(16)
	if err != nil {
		return false, false, err
	}
	dedupKey := fmt.Sprintf("schedule:%s:%s", schedule.ID, scheduledFor.UTC().Format(time.RFC3339Nano))
	priority := priorityForJobType(schedule.JobType)
	tag, err := tx.Exec(ctx, `INSERT INTO core_registry.job_instances(
job_id,schedule_id,trigger_type,job_type,tenant_id,site_id,scheduled_for,schedule_timezone,priority,dedup_key,payload,state,max_attempts,timeout_seconds,trace_id,created_at,updated_at)
VALUES($1::uuid,$2::uuid,'SCHEDULE',$3,$4::uuid,$5::uuid,$6,$7,$8,$9,$10::jsonb,'PENDING',$11,$12,$13,$14,$14)
ON CONFLICT (dedup_key) DO NOTHING`, jobID, schedule.ID, schedule.JobType, nullableUUID(schedule.TenantID), nullableUUID(schedule.SiteID), scheduledFor.UTC(), schedule.Timezone, priority, dedupKey, string(schedule.PayloadTemplate), schedule.MaxAttempts, schedule.TimeoutSeconds, traceID, now.UTC())
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() == 0 {
		return false, false, nil
	}
	if active && schedule.ConcurrencyPolicy == "FORBID" {
		return true, false, nil
	}
	_, err = tx.Exec(ctx, `UPDATE core_registry.job_instances SET state='READY',updated_at=$2 WHERE job_id=$1::uuid AND state='PENDING'`, jobID, now.UTC())
	return true, false, err
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func priorityForJobType(jobType string) int {
	switch jobType {
	case "METRIC_WINDOW_CALC":
		return 80
	case "FORECAST_RUN", "OPTIMIZATION_RUN", "SETTLEMENT_CALC", "SETTLEMENT_RECONCILE":
		return 70
	case "REPORT_GENERATE", "EXPORT_GENERATE":
		return 50
	case "DATA_RETENTION_SCAN", "DATA_ARCHIVE", "CERTIFICATE_EXPIRY_SCAN":
		return 40
	case "METRIC_BACKFILL":
		return 20
	default:
		return 60
	}
}

func isMetricJob(jobType string) bool {
	return jobType == "METRIC_WINDOW_CALC" || jobType == "METRIC_RECALC" || jobType == "METRIC_BACKFILL"
}

func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 0, 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	case 3:
		return 2 * time.Minute
	case 4:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
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

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
