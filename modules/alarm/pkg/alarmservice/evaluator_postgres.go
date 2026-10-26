package alarmservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

var ErrEvaluationClaimLost = errors.New("alarm evaluation claim lost")

type EvaluationClaim struct {
	TenantID     string
	SiteID       string
	AssignmentID string
	WorkerID     string
	Fence        uint64
	StateVersion uint64
}

type assignedAlarmPolicy struct {
	AssignmentID       string
	AssignmentRevision uint64
	SubjectType        string
	SubjectID          string
	Policy             AlarmPolicyRevision
}

type persistedEvaluation struct {
	State              AlarmEvaluationState
	Snapshot           EvaluationSnapshot
	AssignmentRevision uint64
	SubjectType        string
	SubjectID          string
	Fingerprint        string
	LeaseOwner         string
	LeaseUntil         *time.Time
	LeaseFence         uint64
}

func (store *PostgresStore) EvaluateAssignedSnapshot(ctx context.Context, assignmentID string, snapshot EvaluationSnapshot, now time.Time) (EvaluationDecision, error) {
	if store == nil || store.pool == nil {
		return EvaluationDecision{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(assignmentID) {
		return EvaluationDecision{}, errors.New("alarm policy assignment identity is invalid")
	}
	if err := snapshot.Validate(); err != nil {
		return EvaluationDecision{}, err
	}
	tx, err := store.beginTenantTransaction(ctx, snapshot.TenantID, false)
	if err != nil {
		return EvaluationDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEvaluationAssignment(ctx, tx, snapshot.TenantID, assignmentID); err != nil {
		return EvaluationDecision{}, err
	}
	assigned, err := loadAssignedAlarmPolicy(ctx, tx, snapshot.TenantID, snapshot.SiteID, assignmentID)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if snapshot.SubjectType != assigned.SubjectType || snapshot.SubjectID != assigned.SubjectID {
		return EvaluationDecision{}, errors.New("alarm evaluation snapshot does not match policy assignment subject")
	}
	persisted, found, err := loadPersistedEvaluation(ctx, tx, snapshot.TenantID, snapshot.SiteID, assignmentID, true)
	if err != nil {
		return EvaluationDecision{}, err
	}
	previous := AlarmEvaluationState{}
	if found {
		previous = persisted.State
	}
	decision, err := evaluateAtSiteClock(ctx, tx, snapshot.TenantID, snapshot.SiteID, assigned.Policy, snapshot, previous, now)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if decision.State.Version == previous.Version {
		if err := tx.Commit(ctx); err != nil {
			return EvaluationDecision{}, fmt.Errorf("commit duplicate Alarm evaluation: %w", err)
		}
		return decision, nil
	}
	if err := store.applyEvaluationEffect(ctx, tx, snapshot.TenantID, snapshot.SiteID, &decision); err != nil {
		return EvaluationDecision{}, err
	}
	if err := persistEvaluation(ctx, tx, assigned, decision, snapshot, persisted); err != nil {
		return EvaluationDecision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvaluationDecision{}, fmt.Errorf("commit Alarm evaluation: %w", err)
	}
	return decision, nil
}

func (store *PostgresStore) ClaimDueEvaluations(ctx context.Context, tenantID, workerID string, now time.Time, leaseDuration time.Duration, limit int) ([]EvaluationClaim, error) {
	if store == nil || store.pool == nil {
		return nil, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !boundedText(workerID, 256) || leaseDuration <= 0 || limit < 1 || limit > 100 {
		return nil, errors.New("alarm evaluation claim request is invalid")
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	leaseUntil := now.UTC().Add(leaseDuration)
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT tenant_id, site_id, assignment_id
			FROM alarm_runtime.alarm_evaluation_state
			WHERE tenant_id = $1
			  AND next_evaluation_at IS NOT NULL
			  AND next_evaluation_at <= $2
			  AND (lease_until IS NULL OR lease_until <= $2)
			ORDER BY next_evaluation_at, assignment_id
			FOR UPDATE SKIP LOCKED
			LIMIT $5
		)
		UPDATE alarm_runtime.alarm_evaluation_state state
		SET lease_owner = $3, lease_until = $4, lease_fence = state.lease_fence + 1
		FROM due
		WHERE state.tenant_id = due.tenant_id AND state.site_id = due.site_id AND state.assignment_id = due.assignment_id
		RETURNING state.site_id::text, state.assignment_id::text, state.lease_fence, state.version`, tenantID, now.UTC(), strings.TrimSpace(workerID), leaseUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due Alarm evaluations: %w", err)
	}
	defer rows.Close()
	claims := make([]EvaluationClaim, 0, limit)
	for rows.Next() {
		claim := EvaluationClaim{TenantID: tenantID, WorkerID: strings.TrimSpace(workerID)}
		if err := rows.Scan(&claim.SiteID, &claim.AssignmentID, &claim.Fence, &claim.StateVersion); err != nil {
			return nil, fmt.Errorf("scan Alarm evaluation claim: %w", err)
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Alarm evaluation claims: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Alarm evaluation claims: %w", err)
	}
	return claims, nil
}

func (store *PostgresStore) EvaluateClaim(ctx context.Context, claim EvaluationClaim, now time.Time) (EvaluationDecision, error) {
	if store == nil || store.pool == nil {
		return EvaluationDecision{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(claim.TenantID) || !alarmmodel.IsUUIDv7(claim.SiteID) || !alarmmodel.IsUUIDv7(claim.AssignmentID) || !boundedText(claim.WorkerID, 256) || claim.Fence == 0 || claim.StateVersion == 0 {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	tx, err := store.beginTenantTransaction(ctx, claim.TenantID, false)
	if err != nil {
		return EvaluationDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	persisted, found, err := loadPersistedEvaluation(ctx, tx, claim.TenantID, claim.SiteID, claim.AssignmentID, true)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if !found || persisted.LeaseOwner != claim.WorkerID || persisted.LeaseUntil == nil || !now.UTC().Before(*persisted.LeaseUntil) || persisted.LeaseFence != claim.Fence || persisted.State.Version != claim.StateVersion {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	assigned, err := loadAssignedAlarmPolicy(ctx, tx, claim.TenantID, claim.SiteID, claim.AssignmentID)
	if errors.Is(err, ErrRuleRetired) {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	if err != nil {
		return EvaluationDecision{}, err
	}
	decision, err := evaluateAtSiteClock(ctx, tx, claim.TenantID, claim.SiteID, assigned.Policy, persisted.Snapshot, persisted.State, now)
	if err != nil {
		return EvaluationDecision{}, err
	}
	if decision.State.Version == persisted.State.Version {
		return EvaluationDecision{}, ErrEvaluationClaimLost
	}
	if err := store.applyEvaluationEffect(ctx, tx, claim.TenantID, claim.SiteID, &decision); err != nil {
		return EvaluationDecision{}, err
	}
	if err := persistEvaluation(ctx, tx, assigned, decision, persisted.Snapshot, persisted); err != nil {
		return EvaluationDecision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvaluationDecision{}, fmt.Errorf("commit claimed Alarm evaluation: %w", err)
	}
	return decision, nil
}

func (store *PostgresStore) applyEvaluationEffect(ctx context.Context, tx pgx.Tx, tenantID, siteID string, decision *EvaluationDecision) error {
	switch decision.Effect {
	case EvaluationEffectNone:
		return nil
	case EvaluationEffectPublish:
		if decision.Publication == nil {
			return errors.New("alarm evaluation publish decision is incomplete")
		}
		incident, err := store.publishInTransaction(ctx, tx, tenantID, siteID, *decision.Publication)
		if err != nil {
			return err
		}
		decision.State.ActiveAlarmID = incident.AlarmID
		decision.State.ActiveIncidentCorrelationID = incident.IncidentCorrelationID
		return nil
	case EvaluationEffectClear:
		if decision.Recovery == nil {
			return errors.New("alarm evaluation clear decision is incomplete")
		}
		if _, err := store.clearInTransaction(ctx, tx, tenantID, siteID, *decision.Recovery); err != nil {
			return err
		}
		decision.State.ActiveAlarmID = ""
		decision.State.ActiveIncidentCorrelationID = ""
		return nil
	default:
		return errors.New("alarm evaluation effect is invalid")
	}
}

func (store *PostgresStore) publishInTransaction(ctx context.Context, tx pgx.Tx, tenantID, siteID string, publication Publication) (alarmmodel.Alarm, error) {
	fingerprint, err := alarmmodel.Fingerprint(tenantID, siteID, publication.SourceType, publication.SourceReference, publication.AlarmType, publication.DeviceID, publication.PointID)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, publication.OccurredAt)
	if err != nil {
		return alarmmodel.Alarm{}, alarmmodel.ErrInvalidOperation
	}
	current, err := getActiveByFingerprint(ctx, tx, tenantID, siteID, fingerprint, true)
	if err == nil {
		updated, err := alarmmodel.RecordOccurrence(current, publication.occurrenceInput())
		if err != nil {
			return alarmmodel.Alarm{}, err
		}
		if err := persistUpdatedAlarm(ctx, tx, current, updated); err != nil {
			return alarmmodel.Alarm{}, err
		}
		return updated, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return alarmmodel.Alarm{}, err
	}

	alarmID, err := store.newID(occurredAt)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	incidentCorrelationID, err := store.newID(occurredAt.Add(time.Nanosecond))
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	incident, err := alarmmodel.NewIncident(publication.incidentInput(alarmID, incidentCorrelationID, tenantID, siteID))
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	inserted, err := insertIncident(ctx, tx, incident)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if inserted {
		if err := insertTimelineEntry(ctx, tx, incident, incident.Timeline[0]); err != nil {
			return alarmmodel.Alarm{}, err
		}
		if err := enqueueNotificationEvent(ctx, tx, store, incident, NotificationCreated, occurredAt); err != nil {
			return alarmmodel.Alarm{}, err
		}
		return incident, nil
	}
	current, err = getActiveByFingerprint(ctx, tx, tenantID, siteID, fingerprint, true)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	updated, err := alarmmodel.RecordOccurrence(current, publication.occurrenceInput())
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := persistUpdatedAlarm(ctx, tx, current, updated); err != nil {
		return alarmmodel.Alarm{}, err
	}
	return updated, nil
}

func (store *PostgresStore) clearInTransaction(ctx context.Context, tx pgx.Tx, tenantID, siteID string, recovery Recovery) (alarmmodel.Alarm, error) {
	current, err := getActiveByFingerprint(ctx, tx, tenantID, siteID, recovery.Fingerprint, true)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if current.IncidentCorrelationID != recovery.IncidentCorrelationID {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	cleared, err := alarmmodel.ClearIncident(current, alarmmodel.ClearInput{
		OccurredAt:    recovery.OccurredAt,
		Reason:        recovery.Reason,
		Evidence:      recovery.Evidence,
		RuleRevision:  recovery.RuleRevision,
		ActorType:     recovery.ActorType,
		ActorID:       recovery.ActorID,
		CorrelationID: recovery.CorrelationID,
	})
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := persistUpdatedAlarm(ctx, tx, current, cleared); err != nil {
		return alarmmodel.Alarm{}, err
	}
	return cleared, nil
}

// backlogGap is how long readings may pause and still be a backlog being caught up.
const backlogGap = 2 * time.Minute

// evaluateAtSiteClock judges the policy at the Site's clock, and stamps what the decision does to
// its alarm with now: an alarm's record never runs backwards, and people act on it at now.
func evaluateAtSiteClock(ctx context.Context, tx pgx.Tx, tenantID, siteID string, policy AlarmPolicyRevision, snapshot EvaluationSnapshot, previous AlarmEvaluationState, now time.Time) (EvaluationDecision, error) {
	judgedAt, err := siteClock(ctx, tx, tenantID, siteID, now.UTC())
	if err != nil {
		return EvaluationDecision{}, err
	}
	decision, err := EvaluatePolicy(policy, snapshot, previous, judgedAt)
	if err != nil {
		return EvaluationDecision{}, err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	if decision.Publication != nil {
		decision.Publication.OccurredAt = at
	}
	if decision.Recovery != nil {
		decision.Recovery.OccurredAt = at
	}
	return decision, nil
}

// siteClock is the time a Site is judged at (#661). While its readings keep arriving, it is the
// newest reading's time run on by how long ago the alarm side received it: caught up, that is now;
// catching up on a backlog, it follows the readings, so each is judged at its own time instead of
// looking too old. Once none has arrived for backlogGap, the backlog is done or the readings
// stopped, and it is now. A Site without readings is judged at now. ThingsBoard keeps judging "no
// data" at now: docs/architecture/alarm-backlog-clock-source-review-2026-10-20.md.
//
// The newest reading and the newest arrival may be different devices': the relay delivers a Site's
// readings in the order telemetry took them.
func siteClock(ctx context.Context, tx pgx.Tx, tenantID, siteID string, now time.Time) (time.Time, error) {
	var newest, received *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT max(evaluated_at), max(updated_at)
		FROM alarm_runtime.telemetry_evaluation_input
		WHERE tenant_id = $1 AND site_id = $2`, tenantID, siteID).Scan(&newest, &received); err != nil {
		return time.Time{}, fmt.Errorf("read the Site's reading clock: %w", err)
	}
	if newest == nil || now.Sub(*received) >= backlogGap {
		return now, nil
	}
	clock := newest.UTC()
	if received.Before(now) {
		clock = clock.Add(now.Sub(*received))
	}
	if clock.After(now) {
		return now, nil
	}
	return clock, nil
}

func lockEvaluationAssignment(ctx context.Context, tx pgx.Tx, tenantID, assignmentID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID+"|"+assignmentID)
	if err != nil {
		return fmt.Errorf("lock Alarm evaluation assignment: %w", err)
	}
	return nil
}

func loadAssignedAlarmPolicy(ctx context.Context, tx pgx.Tx, tenantID, siteID, assignmentID string) (assignedAlarmPolicy, error) {
	var assigned assignedAlarmPolicy
	var policyRevisionID, policyID, digest string
	var policyRevision uint64
	var schemaVersion int
	var policyJSON []byte
	var retired bool
	err := tx.QueryRow(ctx, `
		SELECT assignment.assignment_id::text, assignment.assignment_revision, assignment.subject_type, assignment.subject_id::text,
		       assignment.policy_revision_id::text, assignment.retired, policy.policy_id::text, policy.revision, policy.schema_version, policy.digest, policy.policy
		FROM alarm_runtime.alarm_policy_assignment assignment
		JOIN alarm_runtime.alarm_policy_revision policy
		  ON policy.tenant_id = assignment.tenant_id AND policy.site_id = assignment.site_id AND policy.policy_revision_id = assignment.policy_revision_id
		WHERE assignment.tenant_id = $1 AND assignment.site_id = $2 AND assignment.assignment_id = $3
		ORDER BY assignment.assignment_revision DESC
		LIMIT 1`, tenantID, siteID, assignmentID).Scan(&assigned.AssignmentID, &assigned.AssignmentRevision, &assigned.SubjectType, &assigned.SubjectID, &policyRevisionID, &retired, &policyID, &policyRevision, &schemaVersion, &digest, &policyJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return assignedAlarmPolicy{}, ErrNotFound
	}
	if err == nil && retired {
		return assignedAlarmPolicy{}, ErrRuleRetired
	}
	if err != nil {
		return assignedAlarmPolicy{}, fmt.Errorf("load Alarm policy assignment: %w", err)
	}
	policy, err := decodeAlarmPolicy(policyJSON)
	if err != nil {
		return assignedAlarmPolicy{}, err
	}
	if policy.PolicyRevisionID != policyRevisionID || policy.PolicyID != policyID || policy.Revision != policyRevision || policy.SchemaVersion != schemaVersion || policy.Digest != digest {
		return assignedAlarmPolicy{}, errors.New("stored Alarm policy release metadata does not match immutable policy payload")
	}
	assigned.Policy = policy
	return assigned, nil
}

func loadPersistedEvaluation(ctx context.Context, tx pgx.Tx, tenantID, siteID, assignmentID string, lock bool) (persistedEvaluation, bool, error) {
	query := `
		SELECT assignment_revision, policy_revision_id::text, subject_type, subject_id::text, fingerprint, status,
		       candidate_since, repeat_count, last_input_revision, quality_blocker, next_evaluation_at,
		       active_alarm_id::text, active_incident_correlation_id::text, last_snapshot, last_evaluated_at,
		       version, lease_owner, lease_until, lease_fence
		FROM alarm_runtime.alarm_evaluation_state
		WHERE tenant_id = $1 AND site_id = $2 AND assignment_id = $3`
	if lock {
		query += ` FOR UPDATE`
	}
	var result persistedEvaluation
	var policyRevisionID string
	var status string
	var candidateSince, nextEvaluationAt, leaseUntil *time.Time
	var activeAlarmID, activeCorrelationID, qualityBlocker, leaseOwner *string
	var lastSnapshot []byte
	var lastEvaluatedAt time.Time
	err := tx.QueryRow(ctx, query, tenantID, siteID, assignmentID).Scan(
		&result.AssignmentRevision, &policyRevisionID, &result.SubjectType, &result.SubjectID, &result.Fingerprint, &status,
		&candidateSince, &result.State.RepeatCount, &result.State.LastInputRevision, &qualityBlocker, &nextEvaluationAt,
		&activeAlarmID, &activeCorrelationID, &lastSnapshot, &lastEvaluatedAt, &result.State.Version, &leaseOwner, &leaseUntil, &result.LeaseFence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistedEvaluation{}, false, nil
	}
	if err != nil {
		return persistedEvaluation{}, false, fmt.Errorf("load Alarm evaluation state: %w", err)
	}
	result.State.PolicyRevisionID = policyRevisionID
	result.State.Status = EvaluationStatus(status)
	result.State.CandidateSince = formatOptionalTime(candidateSince)
	result.State.NextEvaluationAt = formatOptionalTime(nextEvaluationAt)
	result.State.LastEvaluatedAt = lastEvaluatedAt.UTC().Format(time.RFC3339Nano)
	if qualityBlocker != nil {
		result.State.QualityBlocker = *qualityBlocker
	}
	if activeAlarmID != nil {
		result.State.ActiveAlarmID = *activeAlarmID
	}
	if activeCorrelationID != nil {
		result.State.ActiveIncidentCorrelationID = *activeCorrelationID
	}
	if leaseOwner != nil {
		result.LeaseOwner = *leaseOwner
	}
	result.LeaseUntil = leaseUntil
	if err := decodeEvaluationSnapshot(lastSnapshot, &result.Snapshot); err != nil {
		return persistedEvaluation{}, false, err
	}
	return result, true, nil
}

func persistEvaluation(ctx context.Context, tx pgx.Tx, assigned assignedAlarmPolicy, decision EvaluationDecision, snapshot EvaluationSnapshot, previous persistedEvaluation) error {
	previousVersion := previous.State.Version
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode Alarm evaluation snapshot: %w", err)
	}
	candidateSince, err := parseOptionalInstant(decision.State.CandidateSince)
	if err != nil {
		return err
	}
	nextEvaluationAt, err := parseOptionalInstant(decision.State.NextEvaluationAt)
	if err != nil {
		return err
	}
	lastEvaluatedAt, err := time.Parse(time.RFC3339Nano, decision.State.LastEvaluatedAt)
	if err != nil {
		return errors.New("alarm evaluation state time is invalid")
	}
	var activeAlarmID, activeCorrelationID any
	if decision.State.ActiveAlarmID != "" {
		activeAlarmID = decision.State.ActiveAlarmID
		activeCorrelationID = decision.State.ActiveIncidentCorrelationID
	}
	var qualityBlocker any
	if decision.State.QualityBlocker != "" {
		qualityBlocker = decision.State.QualityBlocker
	}
	if previousVersion == 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO alarm_runtime.alarm_evaluation_state (
				tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, subject_type, subject_id,
				fingerprint, status, candidate_since, repeat_count, last_input_revision, quality_blocker, next_evaluation_at,
				active_alarm_id, active_incident_correlation_id, last_snapshot, last_evaluated_at, version, lease_owner, lease_until, lease_fence
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NULL,NULL,0)`,
			snapshot.TenantID, snapshot.SiteID, assigned.AssignmentID, assigned.AssignmentRevision, assigned.Policy.PolicyRevisionID,
			assigned.SubjectType, assigned.SubjectID, decision.Fingerprint, decision.State.Status, candidateSince, decision.State.RepeatCount,
			decision.State.LastInputRevision, qualityBlocker, nextEvaluationAt, activeAlarmID, activeCorrelationID, snapshotJSON, lastEvaluatedAt, decision.State.Version)
		if err != nil {
			return fmt.Errorf("insert Alarm evaluation state: %w", err)
		}
	} else {
		command, err := tx.Exec(ctx, `
			UPDATE alarm_runtime.alarm_evaluation_state
			SET assignment_revision=$4, policy_revision_id=$5, subject_type=$6, subject_id=$7, fingerprint=$8, status=$9,
			    candidate_since=$10, repeat_count=$11, last_input_revision=$12, quality_blocker=$13, next_evaluation_at=$14,
			    active_alarm_id=$15, active_incident_correlation_id=$16, last_snapshot=$17, last_evaluated_at=$18, version=$19,
			    lease_owner=NULL, lease_until=NULL
			WHERE tenant_id=$1 AND site_id=$2 AND assignment_id=$3 AND version=$20`,
			snapshot.TenantID, snapshot.SiteID, assigned.AssignmentID, assigned.AssignmentRevision, assigned.Policy.PolicyRevisionID,
			assigned.SubjectType, assigned.SubjectID, decision.Fingerprint, decision.State.Status, candidateSince, decision.State.RepeatCount,
			decision.State.LastInputRevision, qualityBlocker, nextEvaluationAt, activeAlarmID, activeCorrelationID, snapshotJSON, lastEvaluatedAt,
			decision.State.Version, previousVersion)
		if err != nil {
			return fmt.Errorf("update Alarm evaluation state: %w", err)
		}
		if command.RowsAffected() != 1 {
			return ErrEvaluationClaimLost
		}
	}
	if !evaluationOutcomeChanged(previous, assigned, decision) {
		return nil
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_evaluation_event (
			tenant_id, site_id, assignment_id, state_version, assignment_revision, policy_revision_id, input_revision,
			status, effect, quality_blocker, fingerprint, active_alarm_id, active_incident_correlation_id, snapshot, evaluated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		snapshot.TenantID, snapshot.SiteID, assigned.AssignmentID, decision.State.Version, assigned.AssignmentRevision,
		assigned.Policy.PolicyRevisionID, decision.State.LastInputRevision, decision.State.Status, decision.Effect, qualityBlocker,
		decision.Fingerprint, activeAlarmID, activeCorrelationID, snapshotJSON, lastEvaluatedAt)
	if err != nil {
		return fmt.Errorf("append Alarm evaluation evidence: %w", err)
	}
	return nil
}

// The evidence log keeps one row per outcome change, not one per reading: a Site rule sees every
// reading of the Site, and an unchanged outcome is already held by alarm_evaluation_state (#651).
// See docs/architecture/alarm-evaluation-evidence-source-review-2026-10-20.md.
func evaluationOutcomeChanged(previous persistedEvaluation, assigned assignedAlarmPolicy, decision EvaluationDecision) bool {
	return previous.State.Version == 0 ||
		decision.Effect != EvaluationEffectNone ||
		decision.State.Status != previous.State.Status ||
		decision.State.QualityBlocker != previous.State.QualityBlocker ||
		decision.State.PolicyRevisionID != previous.State.PolicyRevisionID ||
		assigned.AssignmentRevision != previous.AssignmentRevision
}

func decodeAlarmPolicy(body []byte) (AlarmPolicyRevision, error) {
	var policy AlarmPolicyRevision
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || ensureJSONEOF(decoder) != nil || policy.Validate() != nil {
		return AlarmPolicyRevision{}, errors.New("stored Alarm policy revision is invalid")
	}
	return policy, nil
}

func decodeEvaluationSnapshot(body []byte, snapshot *EvaluationSnapshot) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(snapshot) != nil || ensureJSONEOF(decoder) != nil || snapshot.Validate() != nil {
		return errors.New("stored Alarm evaluation snapshot is invalid")
	}
	return nil
}

func parseOptionalInstant(value *string) (any, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil, errors.New("alarm evaluation scheduled instant is invalid")
	}
	return parsed.UTC(), nil
}
