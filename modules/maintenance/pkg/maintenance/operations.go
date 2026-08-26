package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type ExecutionError struct {
	Code      string
	Retryable bool
	Err       error
}

func (e *ExecutionError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (s *Store) ExecuteJob(ctx context.Context, job Job, now time.Time) (map[string]any, error) {
	if err := validateJob(job); err != nil {
		return nil, &ExecutionError{Code: "MAINTENANCE_JOB_INVALID", Err: err}
	}
	switch job.Type {
	case "CERTIFICATE_EXPIRY_SCAN":
		var payload struct {
			HorizonHours int `json:"horizonHours"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.HorizonHours <= 0 || payload.HorizonHours > 24*365 {
			return nil, &ExecutionError{Code: "CREDENTIAL_EXPIRY_PAYLOAD_INVALID", Err: errors.New("horizonHours must be within 1..8760")}
		}
		count, err := s.scanCredentialExpiry(ctx, now, time.Duration(payload.HorizonHours)*time.Hour)
		if err != nil {
			return nil, &ExecutionError{Code: "CREDENTIAL_EXPIRY_SCAN_FAILED", Retryable: true, Err: err}
		}
		return map[string]any{"eventsObserved": count}, nil
	case "DEAD_WORK_DISPOSITION":
		count, err := s.scanDeadWork(ctx, now)
		if err != nil {
			return nil, &ExecutionError{Code: "DEAD_WORK_SCAN_FAILED", Retryable: true, Err: err}
		}
		return map[string]any{"deadWorkObserved": count}, nil
	case "TENANT_RETIREMENT":
		var payload struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Reason == "" {
			return nil, &ExecutionError{Code: "TENANT_RETIREMENT_PAYLOAD_INVALID", Err: errors.New("retirement reason is required")}
		}
		state, err := s.advanceTenantRetirement(ctx, job.ID, job.TenantID, payload.Reason, now)
		if err != nil {
			return nil, err
		}
		return map[string]any{"retirementId": job.ID, "state": state}, nil
	default:
		return nil, &ExecutionError{Code: "MAINTENANCE_JOB_UNSUPPORTED", Err: fmt.Errorf("unsupported maintenance type %s", job.Type)}
	}
}

func (s *Store) scanCredentialExpiry(ctx context.Context, now time.Time, horizon time.Duration) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT source_type,source_id,tenant_id,expires_at,reference
FROM (
  SELECT 'IAM_API_CREDENTIAL'::text AS source_type,id::text AS source_id,tenant_id::text AS tenant_id,expires_at,NULL::text AS reference
  FROM iam.api_credentials
  WHERE status='ACTIVE' AND expires_at <= $1
  UNION ALL
  SELECT 'CONNECTIVITY_MTLS_CERTIFICATE'::text,id::text,tenant_id::text,valid_until,certificate_fingerprint_sha256
  FROM connectivity.credential_refs
  WHERE credential_kind='MTLS_CERTIFICATE' AND status='ACTIVE' AND valid_until <= $1
) expiry
ORDER BY expires_at ASC`, now.UTC().Add(horizon))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type expiry struct {
		sourceType string
		sourceID   string
		tenantID   string
		expiresAt  time.Time
		reference  *string
	}
	var found []expiry
	for rows.Next() {
		var item expiry
		if err = rows.Scan(&item.sourceType, &item.sourceID, &item.tenantID, &item.expiresAt, &item.reference); err != nil {
			return 0, err
		}
		found = append(found, item)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range found {
		eventID, idErr := uuidv7(now)
		if idErr != nil {
			return 0, idErr
		}
		details := map[string]any{"expiresAt": item.expiresAt.UTC().Format(time.RFC3339Nano)}
		if item.reference != nil && *item.reference != "" {
			details["certificateFingerprintSha256"] = *item.reference
		}
		encoded, marshalErr := json.Marshal(details)
		if marshalErr != nil {
			return 0, marshalErr
		}
		dedup := fmt.Sprintf("credential-expiry:%s:%s:%s", item.sourceType, item.sourceID, item.expiresAt.UTC().Format(time.RFC3339Nano))
		if _, err = s.pool.Exec(ctx, `INSERT INTO core_registry.maintenance_events(
 event_id,tenant_id,event_type,source_type,source_id,severity,status,action_code,dedup_key,details,detected_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,'CREDENTIAL_EXPIRY',$3,$4,$5,'OPEN','ROTATE_CREDENTIAL',$6,$7::jsonb,$8,$8,$8)
ON CONFLICT (dedup_key) DO UPDATE SET severity=EXCLUDED.severity,details=EXCLUDED.details,updated_at=EXCLUDED.updated_at`,
			eventID, item.tenantID, item.sourceType, item.sourceID, credentialSeverity(item.expiresAt, now), dedup, encoded, now.UTC()); err != nil {
			return 0, err
		}
	}
	return len(found), nil
}

func (s *Store) scanDeadWork(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT job_id::text,COALESCE(tenant_id::text,''),job_type,COALESCE(error_code,'UNKNOWN'),COALESCE(completed_at,updated_at)
FROM core_registry.job_instances
WHERE state='DEAD'
ORDER BY completed_at DESC NULLS LAST
LIMIT 1000`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type dead struct {
		jobID      string
		tenantID   string
		jobType    string
		errorCode  string
		completed  time.Time
	}
	var jobs []dead
	for rows.Next() {
		var item dead
		if err = rows.Scan(&item.jobID, &item.tenantID, &item.jobType, &item.errorCode, &item.completed); err != nil {
			return 0, err
		}
		jobs = append(jobs, item)
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range jobs {
		eventID, idErr := uuidv7(now)
		if idErr != nil {
			return 0, idErr
		}
		details, _ := json.Marshal(map[string]any{"jobType": item.jobType, "errorCode": item.errorCode, "completedAt": item.completed.UTC().Format(time.RFC3339Nano)})
		var tenant any
		if item.tenantID != "" {
			tenant = item.tenantID
		}
		if _, err = s.pool.Exec(ctx, `INSERT INTO core_registry.maintenance_events(
 event_id,tenant_id,event_type,source_type,source_id,severity,status,action_code,dedup_key,details,detected_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,'DEAD_WORK','JOB',$3,'CRITICAL','OPEN','REVIEW_DEAD_WORK',$4,$5::jsonb,$6,$6,$6)
ON CONFLICT (dedup_key) DO UPDATE SET details=EXCLUDED.details,updated_at=EXCLUDED.updated_at`,
			eventID, tenant, item.jobID, "dead-work:"+item.jobID, details, now.UTC()); err != nil {
			return 0, err
		}
	}
	return len(jobs), nil
}

func (s *Store) advanceTenantRetirement(ctx context.Context, retirementID, tenantID, reason string, now time.Time) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_STORE_FAILED", Retryable: true, Err: err}
	}
	defer tx.Rollback(ctx)
	var tenantStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM iam.tenants WHERE id=$1::uuid`, tenantID).Scan(&tenantStatus); err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_TENANT_NOT_FOUND", Err: err}
	}
	if tenantStatus == "RETIRED" {
		return "COMPLETED", nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.tenant_retirement_runs(
 retirement_id,tenant_id,state,reason,requested_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,'PENDING',$3,$4,$4,$4)
ON CONFLICT (retirement_id) DO NOTHING`, retirementID, tenantID, reason, now.UTC()); err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_CREATE_FAILED", Retryable: true, Err: err}
	}
	for _, owner := range requiredRetirementOwners {
		if _, err = tx.Exec(ctx, `INSERT INTO core_registry.tenant_retirement_owner_steps(retirement_id,tenant_id,owner_code,state,updated_at)
VALUES($1::uuid,$2::uuid,$3,'PENDING',$4)
ON CONFLICT (retirement_id,owner_code) DO NOTHING`, retirementID, tenantID, owner, now.UTC()); err != nil {
			return "", &ExecutionError{Code: "TENANT_RETIREMENT_OWNER_INIT_FAILED", Retryable: true, Err: err}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.tenant_retirement_runs
SET state='RUNNING',started_at=COALESCE(started_at,$3),revision=revision+1,updated_at=$3
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid AND state='PENDING'`, retirementID, tenantID, now.UTC()); err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_START_FAILED", Retryable: true, Err: err}
	}
	var runState string
	if err = tx.QueryRow(ctx, `SELECT state FROM core_registry.tenant_retirement_runs
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid`, retirementID, tenantID).Scan(&runState); err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_STATE_LOAD_FAILED", Retryable: true, Err: err}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", &ExecutionError{Code: "TENANT_RETIREMENT_COMMIT_FAILED", Retryable: true, Err: err}
	}
	return runState, nil
}

func (s *Store) RecordOwnerResult(ctx context.Context, retirementID, ownerCode string, succeeded bool, proof map[string]any, errorCode string, now time.Time) error {
	validOwner := false
	for _, owner := range requiredRetirementOwners {
		if owner == ownerCode {
			validOwner = true
			break
		}
	}
	if !validOwner {
		return errors.New("retirement owner is invalid")
	}
	encodedProof, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	if succeeded && len(proof) == 0 {
		return errors.New("successful owner retirement requires proof")
	}
	if !succeeded && errorCode == "" {
		return errors.New("failed owner retirement requires error code")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var tenantID, runState string
	if err = tx.QueryRow(ctx, `SELECT tenant_id::text,state FROM core_registry.tenant_retirement_runs
WHERE retirement_id=$1::uuid FOR UPDATE`, retirementID).Scan(&tenantID, &runState); err != nil {
		return err
	}
	if runState == "COMPLETED" {
		return errors.New("completed Tenant retirement cannot accept new owner proof")
	}
	var attemptNo int
	if err = tx.QueryRow(ctx, `SELECT attempt_count+1 FROM core_registry.tenant_retirement_owner_steps
WHERE retirement_id=$1::uuid AND owner_code=$2 FOR UPDATE`, retirementID, ownerCode).Scan(&attemptNo); err != nil {
		return err
	}
	attemptID, err := uuidv7(now)
	if err != nil {
		return err
	}
	result := "FAILED"
	state := "FAILED"
	var storedError any = errorCode
	if succeeded {
		result, state, storedError = "SUCCEEDED", "SUCCEEDED", nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core_registry.tenant_retirement_owner_attempts(
 attempt_id,retirement_id,tenant_id,owner_code,attempt_no,result,error_code,proof,recorded_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::jsonb,$9)`, attemptID, retirementID, tenantID, ownerCode, attemptNo, result, storedError, encodedProof, now.UTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core_registry.tenant_retirement_owner_steps
SET state=$3,attempt_count=$4,last_error_code=$5,proof=$6::jsonb,updated_at=$7
WHERE retirement_id=$1::uuid AND owner_code=$2`, retirementID, ownerCode, state, attemptNo, storedError, encodedProof, now.UTC()); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT owner_code,state FROM core_registry.tenant_retirement_owner_steps
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid ORDER BY owner_code`, retirementID, tenantID)
	if err != nil {
		return err
	}
	var steps []OwnerStep
	for rows.Next() {
		var step OwnerStep
		if err = rows.Scan(&step.OwnerCode, &step.State); err != nil {
			rows.Close()
			return err
		}
		steps = append(steps, step)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}

	switch decideRetirement(steps) {
	case RetirementIncomplete:
		if _, err = tx.Exec(ctx, `UPDATE core_registry.tenant_retirement_runs
SET state='INCOMPLETE',revision=revision+1,updated_at=$3
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid AND state <> 'COMPLETED'`, retirementID, tenantID, now.UTC()); err != nil {
			return err
		}
		eventID, idErr := uuidv7(now)
		if idErr != nil {
			return idErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core_registry.maintenance_events(
 event_id,tenant_id,event_type,source_type,source_id,severity,status,action_code,dedup_key,details,detected_at,created_at,updated_at)
VALUES($1::uuid,$2::uuid,'RETIREMENT_INCOMPLETE','TENANT_RETIREMENT',$3,'CRITICAL','OPEN','RESOLVE_OWNER_FAILURE',$4,'{}'::jsonb,$5,$5,$5)
ON CONFLICT (dedup_key) DO UPDATE SET status='OPEN',acknowledged_at=NULL,resolved_at=NULL,updated_at=EXCLUDED.updated_at`,
			eventID, tenantID, retirementID, "retirement-incomplete:"+retirementID, now.UTC()); err != nil {
			return err
		}
	case RetirementWaiting:
		if runState == "INCOMPLETE" {
			if _, err = tx.Exec(ctx, `UPDATE core_registry.tenant_retirement_runs
SET state='RUNNING',revision=revision+1,updated_at=$3
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid AND state='INCOMPLETE'`, retirementID, tenantID, now.UTC()); err != nil {
				return err
			}
		}
	case RetirementComplete:
		if _, err = tx.Exec(ctx, `UPDATE core_registry.tenant_retirement_runs
SET state='COMPLETED',completed_at=$3,revision=revision+1,updated_at=$3
WHERE retirement_id=$1::uuid AND tenant_id=$2::uuid AND state IN ('RUNNING','INCOMPLETE')`, retirementID, tenantID, now.UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT iam.finalize_tenant_retirement($1::uuid,$2::uuid,$3)`, tenantID, retirementID, now.UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE core_registry.maintenance_events
SET status='RESOLVED',resolved_at=$2,updated_at=$2
WHERE dedup_key=$1 AND status <> 'RESOLVED'`, "retirement-incomplete:"+retirementID, now.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type MaintenanceEvent struct {
	EventID    string          `json:"eventId"`
	TenantID   *string         `json:"tenantId,omitempty"`
	EventType  string          `json:"eventType"`
	SourceType string          `json:"sourceType"`
	SourceID   string          `json:"sourceId"`
	Severity   string          `json:"severity"`
	Status     string          `json:"status"`
	ActionCode string          `json:"actionCode"`
	Details    json.RawMessage `json:"details"`
	DetectedAt time.Time       `json:"detectedAt"`
}

type TenantPolicyUsage struct {
	TenantID                  string `json:"tenantId"`
	TenantStatus              string `json:"tenantStatus"`
	TenantRevision            int64  `json:"tenantRevision"`
	LifecyclePolicyCount      int64  `json:"lifecyclePolicyCount"`
	ActiveLegalHoldCount      int64  `json:"activeLegalHoldCount"`
	ReadyWorkCount            int64  `json:"readyWorkCount"`
	RunningWorkCount          int64  `json:"runningWorkCount"`
	RetryWorkCount            int64  `json:"retryWorkCount"`
	DeadWorkCount             int64  `json:"deadWorkCount"`
	OpenMaintenanceEventCount int64  `json:"openMaintenanceEventCount"`
}

func (s *Store) ListOpenEvents(ctx context.Context, limit int) ([]MaintenanceEvent, error) {
	if limit <= 0 || limit > 200 {
		return nil, errors.New("maintenance event limit must be within 1..200")
	}
	rows, err := s.pool.Query(ctx, `SELECT event_id::text,tenant_id::text,event_type,source_type,source_id,severity,status,action_code,details,detected_at
FROM core_registry.maintenance_events
WHERE status <> 'RESOLVED'
ORDER BY CASE severity WHEN 'CRITICAL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END,detected_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []MaintenanceEvent
	for rows.Next() {
		var event MaintenanceEvent
		if err = rows.Scan(&event.EventID, &event.TenantID, &event.EventType, &event.SourceType, &event.SourceID, &event.Severity, &event.Status, &event.ActionCode, &event.Details, &event.DetectedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) LoadTenantPolicyUsage(ctx context.Context, tenantID string) (TenantPolicyUsage, error) {
	var usage TenantPolicyUsage
	err := s.pool.QueryRow(ctx, `SELECT tenant_id::text,tenant_status,tenant_revision,lifecycle_policy_count,active_legal_hold_count,
ready_work_count,running_work_count,retry_work_count,dead_work_count,open_maintenance_event_count
FROM core_registry.tenant_policy_usage_view WHERE tenant_id=$1::uuid`, tenantID).Scan(
		&usage.TenantID, &usage.TenantStatus, &usage.TenantRevision, &usage.LifecyclePolicyCount, &usage.ActiveLegalHoldCount,
		&usage.ReadyWorkCount, &usage.RunningWorkCount, &usage.RetryWorkCount, &usage.DeadWorkCount, &usage.OpenMaintenanceEventCount,
	)
	return usage, err
}

func (s *Store) AcknowledgeEvent(ctx context.Context, eventID string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE core_registry.maintenance_events
SET status='ACKNOWLEDGED',acknowledged_at=$2,updated_at=$2
WHERE event_id=$1::uuid AND status='OPEN'`, eventID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}
