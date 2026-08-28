package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
)

type AlarmPolicyAssignmentInput struct {
	AssignmentID       string
	AssignmentRevision uint64
	PolicyRevisionID   string
	SubjectType        string
	SubjectID          string
	AssignedAt         string
	AssignedBy         string
}

func (store *PostgresStore) ReleaseAlarmPolicyRevision(ctx context.Context, tenantID, siteID string, policy AlarmPolicyRevision, releasedAt time.Time, releasedBy string) error {
	if store == nil || store.pool == nil {
		return ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || !boundedText(releasedBy, 256) || releasedAt.IsZero() {
		return errors.New("Alarm policy release metadata is invalid")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockArtifactStream(ctx, tx, tenantID+"|"+siteID+"|policy|"+policy.PolicyID); err != nil {
		return err
	}

	var latestRevision uint64
	var latestPolicyJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT revision, policy
		FROM alarm_runtime.alarm_policy_revision
		WHERE tenant_id=$1 AND site_id=$2 AND policy_id=$3
		ORDER BY revision DESC LIMIT 1`, tenantID, siteID, policy.PolicyID).Scan(&latestRevision, &latestPolicyJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		if policy.Revision != 1 {
			return errors.New("first Alarm policy release revision must be 1")
		}
	} else if err != nil {
		return fmt.Errorf("read latest Alarm policy release: %w", err)
	} else {
		if policy.Revision != latestRevision+1 {
			return errors.New("Alarm policy release revision is not contiguous")
		}
		latestPolicy, err := decodeAlarmPolicy(latestPolicyJSON)
		if err != nil {
			return err
		}
		if latestPolicy.AlarmType != policy.AlarmType || latestPolicy.SourceType != policy.SourceType || latestPolicy.SourceReference != policy.SourceReference {
			return errors.New("Alarm policy family cannot change fingerprint identity across revisions")
		}
	}

	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode Alarm policy release: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_policy_revision (
			tenant_id, site_id, policy_id, policy_revision_id, revision, schema_version, digest, policy, released_at, released_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tenantID, siteID, policy.PolicyID, policy.PolicyRevisionID, policy.Revision, policy.SchemaVersion, policy.Digest, policyJSON,
		releasedAt.UTC(), strings.TrimSpace(releasedBy))
	if err != nil {
		return fmt.Errorf("release Alarm policy revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Alarm policy release: %w", err)
	}
	return nil
}

func (store *PostgresStore) AssignAlarmPolicyRevision(ctx context.Context, tenantID, siteID string, input AlarmPolicyAssignmentInput) error {
	if store == nil || store.pool == nil {
		return ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(tenantID) || !alarmmodel.IsUUIDv7(siteID) || !alarmmodel.IsUUIDv7(input.AssignmentID) || input.AssignmentRevision == 0 || !alarmmodel.IsUUIDv7(input.PolicyRevisionID) || !validEvaluationSubjectType(input.SubjectType) || !alarmmodel.IsUUIDv7(input.SubjectID) || !boundedText(input.AssignedBy, 256) {
		return errors.New("Alarm policy assignment is invalid")
	}
	assignedAt, err := time.Parse(time.RFC3339Nano, input.AssignedAt)
	if err != nil {
		return errors.New("Alarm policy assignment time is invalid")
	}
	if input.SubjectType == "SITE" && input.SubjectID != siteID {
		return errors.New("Alarm policy Site assignment is inconsistent")
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockArtifactStream(ctx, tx, tenantID+"|"+siteID+"|assignment|"+input.AssignmentID); err != nil {
		return err
	}

	var targetPolicyID string
	var targetReleasedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT policy_id::text, released_at
		FROM alarm_runtime.alarm_policy_revision
		WHERE tenant_id=$1 AND site_id=$2 AND policy_revision_id=$3`, tenantID, siteID, input.PolicyRevisionID).Scan(&targetPolicyID, &targetReleasedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve Alarm policy assignment target: %w", err)
	}
	if assignedAt.Before(targetReleasedAt) {
		return errors.New("Alarm policy assignment cannot precede policy release")
	}

	var latestRevision uint64
	var latestSubjectType, latestSubjectID, latestPolicyID string
	err = tx.QueryRow(ctx, `
		SELECT assignment.assignment_revision, assignment.subject_type, assignment.subject_id::text, policy.policy_id::text
		FROM alarm_runtime.alarm_policy_assignment assignment
		JOIN alarm_runtime.alarm_policy_revision policy
		  ON policy.tenant_id=assignment.tenant_id AND policy.site_id=assignment.site_id AND policy.policy_revision_id=assignment.policy_revision_id
		WHERE assignment.tenant_id=$1 AND assignment.site_id=$2 AND assignment.assignment_id=$3
		ORDER BY assignment.assignment_revision DESC LIMIT 1`, tenantID, siteID, input.AssignmentID).Scan(&latestRevision, &latestSubjectType, &latestSubjectID, &latestPolicyID)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.AssignmentRevision != 1 {
			return errors.New("first Alarm policy assignment revision must be 1")
		}
		var existingAssignmentID string
		err = tx.QueryRow(ctx, `
			SELECT assignment.assignment_id::text
			FROM alarm_runtime.alarm_policy_assignment assignment
			JOIN alarm_runtime.alarm_policy_revision policy
			  ON policy.tenant_id=assignment.tenant_id AND policy.site_id=assignment.site_id AND policy.policy_revision_id=assignment.policy_revision_id
			WHERE assignment.tenant_id=$1 AND assignment.site_id=$2 AND policy.policy_id=$3
			  AND assignment.subject_type=$4 AND assignment.subject_id=$5
			LIMIT 1`, tenantID, siteID, targetPolicyID, input.SubjectType, input.SubjectID).Scan(&existingAssignmentID)
		if err == nil {
			return errors.New("Alarm policy family already has an assignment stream for this subject")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check duplicate Alarm policy assignment stream: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read latest Alarm policy assignment: %w", err)
	} else {
		if input.AssignmentRevision != latestRevision+1 {
			return errors.New("Alarm policy assignment revision is not contiguous")
		}
		if input.SubjectType != latestSubjectType || input.SubjectID != latestSubjectID {
			return errors.New("Alarm policy assignment subject is immutable")
		}
		if targetPolicyID != latestPolicyID {
			return errors.New("Alarm policy assignment cannot switch policy family")
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_policy_assignment (
			tenant_id, site_id, assignment_id, assignment_revision, policy_revision_id, subject_type, subject_id, assigned_at, assigned_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tenantID, siteID, input.AssignmentID, input.AssignmentRevision,
		input.PolicyRevisionID, input.SubjectType, input.SubjectID, assignedAt.UTC(), strings.TrimSpace(input.AssignedBy))
	if err != nil {
		return fmt.Errorf("assign Alarm policy revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Alarm policy assignment: %w", err)
	}
	return nil
}

func lockArtifactStream(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	if err != nil {
		return fmt.Errorf("lock Alarm released artifact stream: %w", err)
	}
	return nil
}
