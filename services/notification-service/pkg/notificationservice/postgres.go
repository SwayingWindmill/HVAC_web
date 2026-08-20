package notificationservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/outbound-delivery-service/pkg/outbounddelivery"
)

var (
	ErrNotFound  = errors.New("notification object not found")
	ErrClaimLost = errors.New("notification claim is no longer owned by this worker")
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

type ExternalDeliveryPort interface {
	SubmitNotification(ctx context.Context, request outbounddelivery.SubmitIntentRequest) (outbounddelivery.DeliveryIntent, error)
}

type S15DeliveryPort struct {
	Store *outbounddelivery.PostgresStore
	pool  *pgxpool.Pool
}

func OpenS15DeliveryPort(ctx context.Context, databaseURL string) (*S15DeliveryPort, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse S15 delivery database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open S15 delivery database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping S15 delivery database: %w", err)
	}
	store, err := outbounddelivery.NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &S15DeliveryPort{Store: store, pool: pool}, nil
}

func (port *S15DeliveryPort) Close() {
	if port != nil && port.pool != nil {
		port.pool.Close()
	}
}

func (port S15DeliveryPort) SubmitNotification(ctx context.Context, request outbounddelivery.SubmitIntentRequest) (outbounddelivery.DeliveryIntent, error) {
	if port.Store == nil {
		return outbounddelivery.DeliveryIntent{}, errors.New("S15 delivery store is required")
	}
	return port.Store.SubmitIntent(ctx, request)
}

func (port S15DeliveryPort) GetDeliveryIntent(ctx context.Context, tenantID, intentID string) (outbounddelivery.DeliveryIntent, error) {
	if port.Store == nil {
		return outbounddelivery.DeliveryIntent{}, errors.New("S15 delivery store is required")
	}
	return port.Store.GetIntent(ctx, tenantID, intentID)
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("notification store is unavailable")
	}
	return store.pool.Ping(ctx)
}

func (store *PostgresStore) ReleaseTemplate(ctx context.Context, tenantID string, template TemplateRevision, releasedAt time.Time, releasedBy string) error {
	if err := template.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		return err
	}
	return store.releaseArtifact(ctx, tenantID, "template", template.TemplateID, template.TemplateRevisionID, template.Revision, releasedAt, releasedBy, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notification_runtime.template_revision
  (tenant_id,template_id,template_revision_id,revision,schema_version,digest,channel,template,released_at,released_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::jsonb,$9,$10)`, tenantID, template.TemplateID, template.TemplateRevisionID,
			template.Revision, template.SchemaVersion, template.Digest, template.Channel, encoded, releasedAt.UTC(), strings.TrimSpace(releasedBy))
		return err
	})
}

func (store *PostgresStore) ReleaseAudience(ctx context.Context, tenantID string, audience AudienceRevision, releasedAt time.Time, releasedBy string) error {
	if err := audience.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(audience)
	if err != nil {
		return err
	}
	return store.releaseArtifact(ctx, tenantID, "audience", audience.AudienceID, audience.AudienceRevisionID, audience.Revision, releasedAt, releasedBy, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notification_runtime.audience_revision
  (tenant_id,audience_id,audience_revision_id,revision,schema_version,digest,audience,released_at,released_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8,$9)`, tenantID, audience.AudienceID, audience.AudienceRevisionID,
			audience.Revision, audience.SchemaVersion, audience.Digest, encoded, releasedAt.UTC(), strings.TrimSpace(releasedBy))
		return err
	})
}

func (store *PostgresStore) ReleasePolicy(ctx context.Context, tenantID string, policy NotificationPolicyRevision, releasedAt time.Time, releasedBy string) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return store.releaseArtifact(ctx, tenantID, "policy", policy.PolicyID, policy.PolicyRevisionID, policy.Revision, releasedAt, releasedBy, func(tx pgx.Tx) error {
		for _, stage := range policy.Stages {
			var templateChannel Channel
			if err := tx.QueryRow(ctx, `SELECT channel FROM notification_runtime.template_revision WHERE tenant_id=$1::uuid AND template_revision_id=$2::uuid`, tenantID, stage.TemplateRevisionID).Scan(&templateChannel); err != nil {
				return fmt.Errorf("resolve notification template revision: %w", err)
			}
			if templateChannel != stage.Channel {
				return errors.New("notification policy stage channel does not match template release")
			}
			var audienceExists bool
			if err := tx.QueryRow(ctx, `SELECT true FROM notification_runtime.audience_revision WHERE tenant_id=$1::uuid AND audience_revision_id=$2::uuid`, tenantID, stage.AudienceRevisionID).Scan(&audienceExists); err != nil {
				return fmt.Errorf("resolve notification audience revision: %w", err)
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO notification_runtime.policy_revision
  (tenant_id,policy_id,policy_revision_id,revision,schema_version,digest,mandatory_safety,policy,released_at,released_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8::jsonb,$9,$10)`, tenantID, policy.PolicyID, policy.PolicyRevisionID,
			policy.Revision, policy.SchemaVersion, policy.Digest, policy.MandatorySafety, encoded, releasedAt.UTC(), strings.TrimSpace(releasedBy))
		return err
	})
}

func (store *PostgresStore) releaseArtifact(ctx context.Context, tenantID, kind, artifactID, revisionID string, revision uint64, releasedAt time.Time, releasedBy string, insert func(pgx.Tx) error) error {
	if store == nil || store.pool == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(releasedBy) == "" || releasedAt.IsZero() {
		return errors.New("notification release metadata is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+"|"+kind+"|"+artifactID); err != nil {
		return err
	}
	var latest uint64
	table, idColumn := artifactTable(kind)
	query := fmt.Sprintf("SELECT revision FROM notification_runtime.%s WHERE tenant_id=$1::uuid AND %s=$2::uuid ORDER BY revision DESC LIMIT 1", table, idColumn)
	err = tx.QueryRow(ctx, query, tenantID, artifactID).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		if revision != 1 {
			return errors.New("first notification artifact revision must be 1")
		}
	} else if err != nil {
		return err
	} else if revision != latest+1 {
		return errors.New("notification artifact revision is not contiguous")
	}
	if err := insert(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func artifactTable(kind string) (string, string) {
	switch kind {
	case "template":
		return "template_revision", "template_id"
	case "audience":
		return "audience_revision", "audience_id"
	default:
		return "policy_revision", "policy_id"
	}
}

func (store *PostgresStore) AssignPolicy(ctx context.Context, assignment PolicyAssignment) error {
	if assignment.TenantID == "" || assignment.SiteID == "" || assignment.AssignmentID == "" || assignment.PolicyRevisionID == "" || assignment.AssignmentRevision == 0 || assignment.AssignedAt.IsZero() || strings.TrimSpace(assignment.AssignedBy) == "" {
		return errors.New("notification policy assignment is invalid")
	}
	tx, err := store.beginTenant(ctx, assignment.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, assignment.TenantID+"|notification-assignment|"+assignment.AssignmentID); err != nil {
		return err
	}
	var policyID string
	var releasedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT policy_id::text,released_at FROM notification_runtime.policy_revision WHERE tenant_id=$1::uuid AND policy_revision_id=$2::uuid`, assignment.TenantID, assignment.PolicyRevisionID).Scan(&policyID, &releasedAt); err != nil {
		return err
	}
	if assignment.AssignedAt.Before(releasedAt) {
		return errors.New("notification policy assignment cannot precede policy release")
	}
	var latestRevision uint64
	var latestPolicyID string
	err = tx.QueryRow(ctx, `SELECT assignment_revision,policy_id::text FROM notification_runtime.policy_assignment
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND assignment_id=$3::uuid ORDER BY assignment_revision DESC LIMIT 1`, assignment.TenantID, assignment.SiteID, assignment.AssignmentID).Scan(&latestRevision, &latestPolicyID)
	if errors.Is(err, pgx.ErrNoRows) {
		if assignment.AssignmentRevision != 1 {
			return errors.New("first notification assignment revision must be 1")
		}
		var existing string
		err = tx.QueryRow(ctx, `SELECT assignment_id::text FROM notification_runtime.policy_assignment
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND policy_id=$3::uuid LIMIT 1`, assignment.TenantID, assignment.SiteID, policyID).Scan(&existing)
		if err == nil {
			return errors.New("notification policy family already has an assignment stream for this Site")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if assignment.AssignmentRevision != latestRevision+1 {
			return errors.New("notification assignment revision is not contiguous")
		}
		if latestPolicyID != policyID {
			return errors.New("notification assignment cannot switch policy family")
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO notification_runtime.policy_assignment
  (tenant_id,site_id,assignment_id,assignment_revision,policy_id,policy_revision_id,enabled,assigned_at,assigned_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,true,$7,$8)`, assignment.TenantID, assignment.SiteID, assignment.AssignmentID,
		assignment.AssignmentRevision, policyID, assignment.PolicyRevisionID, assignment.AssignedAt.UTC(), strings.TrimSpace(assignment.AssignedBy))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) SetAdvisoryPreference(ctx context.Context, tenantID, principalID string, channel Channel, enabled bool, now time.Time) (uint64, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(principalID) == "" || !validChannel(channel) || now.IsZero() {
		return 0, errors.New("notification preference is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var revision uint64
	err = tx.QueryRow(ctx, `INSERT INTO notification_runtime.user_preference
  (tenant_id,principal_id,channel,advisory_enabled,revision,updated_at)
VALUES ($1::uuid,$2,$3,$4,1,$5)
ON CONFLICT (tenant_id,principal_id,channel) DO UPDATE
SET advisory_enabled=EXCLUDED.advisory_enabled,revision=notification_runtime.user_preference.revision+1,updated_at=EXCLUDED.updated_at
RETURNING revision`, tenantID, strings.TrimSpace(principalID), channel, enabled, now.UTC()).Scan(&revision)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return revision, nil
}

type assignedPolicy struct {
	AssignmentID       string
	AssignmentRevision uint64
	Policy             NotificationPolicyRevision
}

func (store *PostgresStore) ProcessAlarmEvent(ctx context.Context, event AlarmEvent, recordedAt time.Time) ([]NotificationIntent, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	if recordedAt.IsZero() {
		return nil, errors.New("notification event recorded time is required")
	}
	tx, err := store.beginTenant(ctx, event.TenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	encodedEvent, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	commandTag, err := tx.Exec(ctx, `INSERT INTO notification_runtime.source_alarm_event
  (tenant_id,site_id,source_event_id,alarm_id,incident_correlation_id,action,current_severity,peak_severity,condition,event,occurred_at,recorded_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10::jsonb,$11,$12)
ON CONFLICT (tenant_id,source_event_id) DO NOTHING`, event.TenantID, event.SiteID, event.SourceEventID, event.AlarmID, event.IncidentCorrelationID,
		event.Action, event.CurrentSeverity, event.PeakSeverity, event.Condition, encodedEvent, event.OccurredAt.UTC(), recordedAt.UTC())
	if err != nil {
		return nil, err
	}
	if commandTag.RowsAffected() == 0 {
		intents, err := loadIntentsForSource(ctx, tx, event.TenantID, event.SourceEventID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return intents, nil
	}

	if event.Action == AlarmAcknowledged || event.Action == AlarmCleared {
		rows, err := tx.Query(ctx, `UPDATE notification_runtime.notification_intent
SET status='CANCELLED',disposition='CANCELLED',lease_owner=NULL,lease_until=NULL,updated_at=$4
WHERE tenant_id=$1::uuid AND site_id=$2::uuid AND incident_correlation_id=$3::uuid
  AND source_action IN ('CREATED','SEVERITY_CHANGED') AND stage > 0
  AND status IN ('SCHEDULED','CLAIMED')
RETURNING intent_id::text`, event.TenantID, event.SiteID, event.IncidentCorrelationID, recordedAt.UTC())
		if err != nil {
			return nil, err
		}
		var cancelled []string
		for rows.Next() {
			var intentID string
			if err := rows.Scan(&intentID); err != nil {
				rows.Close()
				return nil, err
			}
			cancelled = append(cancelled, intentID)
		}
		rows.Close()
		for _, intentID := range cancelled {
			if err := appendIntentEvent(ctx, tx, event.TenantID, intentID, "CANCELLED", map[string]any{"bySourceEventId": event.SourceEventID, "action": event.Action}, recordedAt.UTC()); err != nil {
				return nil, err
			}
		}
	}

	assignments, err := loadAssignedPolicies(ctx, tx, event.TenantID, event.SiteID)
	if err != nil {
		return nil, err
	}
	values := EventTemplateValues(event)
	for _, assigned := range assignments {
		if !policyMatches(assigned.Policy, event) {
			continue
		}
		for _, stage := range assigned.Policy.Stages {
			template, err := loadTemplate(ctx, tx, event.TenantID, stage.TemplateRevisionID)
			if err != nil {
				return nil, err
			}
			audience, err := loadAudience(ctx, tx, event.TenantID, stage.AudienceRevisionID)
			if err != nil {
				return nil, err
			}
			recipients, err := freezeRecipients(ctx, tx, event.TenantID, stage.Channel, assigned.Policy.MandatorySafety, audience.Recipients)
			if err != nil {
				return nil, err
			}
			subject, body, err := RenderTemplate(template, values)
			if err != nil {
				return nil, err
			}
			intentID, err := uuidv7(recordedAt.Add(time.Duration(stage.Stage) * time.Microsecond))
			if err != nil {
				return nil, err
			}
			recipientsJSON, _ := json.Marshal(recipients)
			status := IntentScheduled
			disposition := DispositionPending
			if len(recipients) == 0 {
				status = IntentCancelled
				disposition = DispositionCancelled
			}
			_, err = tx.Exec(ctx, `INSERT INTO notification_runtime.notification_intent
  (tenant_id,site_id,intent_id,source_event_id,alarm_id,incident_correlation_id,source_action,current_severity,
   policy_revision_id,assignment_id,assignment_revision,stage,channel,integration_id,mandatory_safety,recipients,
   template_revision_id,rendered_subject,rendered_body,due_at,status,disposition,lease_fence,created_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9::uuid,$10::uuid,$11,$12,$13,$14::uuid,$15,$16::jsonb,$17::uuid,$18,$19,$20,$21,$22,0,$23,$23)`,
				event.TenantID, event.SiteID, intentID, event.SourceEventID, event.AlarmID, event.IncidentCorrelationID, event.Action, event.CurrentSeverity,
				assigned.Policy.PolicyRevisionID, assigned.AssignmentID, assigned.AssignmentRevision, stage.Stage, stage.Channel, nilUUID(stage.IntegrationID),
				assigned.Policy.MandatorySafety, recipientsJSON, stage.TemplateRevisionID, subject, body, event.OccurredAt.UTC().Add(time.Duration(stage.DelaySeconds)*time.Second),
				status, disposition, recordedAt.UTC())
			if err != nil {
				return nil, err
			}
			if err := appendIntentEvent(ctx, tx, event.TenantID, intentID, "CREATED", map[string]any{"sourceEventId": event.SourceEventID, "stage": stage.Stage, "recipientCount": len(recipients)}, recordedAt.UTC()); err != nil {
				return nil, err
			}
			if status == IntentCancelled {
				if err := appendIntentEvent(ctx, tx, event.TenantID, intentID, "CANCELLED", map[string]any{"reason": "ADVISORY_PREFERENCE"}, recordedAt.UTC()); err != nil {
					return nil, err
				}
			}
		}
	}
	intents, err := loadIntentsForSource(ctx, tx, event.TenantID, event.SourceEventID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return intents, nil
}

func loadAssignedPolicies(ctx context.Context, tx pgx.Tx, tenantID, siteID string) ([]assignedPolicy, error) {
	rows, err := tx.Query(ctx, `SELECT assignment_id::text,assignment_revision,policy
FROM (
  SELECT DISTINCT ON (assignment_id) assignment_id,assignment_revision,policy_revision_id,enabled
  FROM notification_runtime.policy_assignment
  WHERE tenant_id=$1::uuid AND site_id=$2::uuid
  ORDER BY assignment_id,assignment_revision DESC
) assignment
JOIN notification_runtime.policy_revision policy_revision
  ON policy_revision.tenant_id=$1::uuid AND policy_revision.policy_revision_id=assignment.policy_revision_id
WHERE assignment.enabled=true
ORDER BY assignment.assignment_id`, tenantID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []assignedPolicy
	for rows.Next() {
		var assigned assignedPolicy
		var encoded []byte
		if err := rows.Scan(&assigned.AssignmentID, &assigned.AssignmentRevision, &encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &assigned.Policy); err != nil || assigned.Policy.Validate() != nil {
			return nil, errors.New("stored notification policy release is invalid")
		}
		result = append(result, assigned)
	}
	return result, rows.Err()
}

func loadTemplate(ctx context.Context, tx pgx.Tx, tenantID, revisionID string) (TemplateRevision, error) {
	var encoded []byte
	if err := tx.QueryRow(ctx, `SELECT template FROM notification_runtime.template_revision WHERE tenant_id=$1::uuid AND template_revision_id=$2::uuid`, tenantID, revisionID).Scan(&encoded); err != nil {
		return TemplateRevision{}, err
	}
	var template TemplateRevision
	if err := json.Unmarshal(encoded, &template); err != nil || template.Validate() != nil {
		return TemplateRevision{}, errors.New("stored notification template release is invalid")
	}
	return template, nil
}

func loadAudience(ctx context.Context, tx pgx.Tx, tenantID, revisionID string) (AudienceRevision, error) {
	var encoded []byte
	if err := tx.QueryRow(ctx, `SELECT audience FROM notification_runtime.audience_revision WHERE tenant_id=$1::uuid AND audience_revision_id=$2::uuid`, tenantID, revisionID).Scan(&encoded); err != nil {
		return AudienceRevision{}, err
	}
	var audience AudienceRevision
	if err := json.Unmarshal(encoded, &audience); err != nil || audience.Validate() != nil {
		return AudienceRevision{}, errors.New("stored notification audience release is invalid")
	}
	return audience, nil
}

func freezeRecipients(ctx context.Context, tx pgx.Tx, tenantID string, channel Channel, mandatory bool, source []Recipient) ([]Recipient, error) {
	if mandatory {
		return append([]Recipient(nil), source...), nil
	}
	result := make([]Recipient, 0, len(source))
	for _, recipient := range source {
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT advisory_enabled FROM notification_runtime.user_preference WHERE tenant_id=$1::uuid AND principal_id=$2 AND channel=$3`, tenantID, recipient.PrincipalID, channel).Scan(&enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			result = append(result, recipient)
			continue
		}
		if err != nil {
			return nil, err
		}
		if enabled {
			result = append(result, recipient)
		}
	}
	return result, nil
}

func (store *PostgresStore) ClaimDue(ctx context.Context, tenantID, workerID string, now time.Time, leaseDuration time.Duration) (*IntentClaim, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workerID) == "" || now.IsZero() || leaseDuration <= 0 {
		return nil, errors.New("notification claim input is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var intentID string
	err = tx.QueryRow(ctx, `SELECT intent_id::text FROM notification_runtime.notification_intent
WHERE tenant_id=$1::uuid AND due_at <= $2
  AND (status='SCHEDULED' OR (status='CLAIMED' AND lease_until <= $2))
ORDER BY due_at,intent_id FOR UPDATE SKIP LOCKED LIMIT 1`, tenantID, now.UTC()).Scan(&intentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	leaseUntil := now.UTC().Add(leaseDuration)
	_, err = tx.Exec(ctx, `UPDATE notification_runtime.notification_intent
SET status='CLAIMED',lease_owner=$3,lease_until=$4,lease_fence=lease_fence+1,updated_at=$2
WHERE tenant_id=$1::uuid AND intent_id=$5::uuid`, tenantID, now.UTC(), strings.TrimSpace(workerID), leaseUntil, intentID)
	if err != nil {
		return nil, err
	}
	claim, err := loadClaim(ctx, tx, tenantID, intentID)
	if err != nil {
		return nil, err
	}
	if err := appendIntentEvent(ctx, tx, tenantID, intentID, "CLAIMED", map[string]any{"workerId": workerID, "fence": claim.Fence}, now.UTC()); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}

func (store *PostgresStore) MaterializeInApp(ctx context.Context, claim IntentClaim, now time.Time) ([]InboxItem, error) {
	if claim.Channel != ChannelInApp || now.IsZero() {
		return nil, errors.New("in-app notification claim is invalid")
	}
	tx, err := store.beginTenant(ctx, claim.TenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	current, err := lockClaim(ctx, tx, claim.TenantID, claim.IntentID, claim.WorkerID, claim.Fence, now)
	if err != nil {
		return nil, err
	}
	items := make([]InboxItem, 0, len(current.Recipients))
	for index, recipient := range current.Recipients {
		itemID, err := uuidv7(now.Add(time.Duration(index) * time.Microsecond))
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO notification_runtime.inbox_item
  (tenant_id,site_id,inbox_item_id,intent_id,principal_id,alarm_id,incident_correlation_id,source_action,severity,subject,body,status,created_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,$8,$9,$10,$11,'UNREAD',$12)
ON CONFLICT (tenant_id,intent_id,principal_id) DO NOTHING`, current.TenantID, current.SiteID, itemID, current.IntentID, recipient.PrincipalID,
			current.AlarmID, current.IncidentCorrelationID, current.SourceAction, current.CurrentSeverity, current.RenderedSubject, current.RenderedBody, now.UTC())
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE notification_runtime.notification_intent
SET status='MATERIALIZED',disposition='DELIVERED',lease_owner=NULL,lease_until=NULL,updated_at=$3
WHERE tenant_id=$1::uuid AND intent_id=$2::uuid`, current.TenantID, current.IntentID, now.UTC())
	if err != nil {
		return nil, err
	}
	if err := appendIntentEvent(ctx, tx, current.TenantID, current.IntentID, "MATERIALIZED", map[string]any{"recipientCount": len(current.Recipients)}, now.UTC()); err != nil {
		return nil, err
	}
	items, err = listInboxByIntent(ctx, tx, current.TenantID, current.IntentID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (store *PostgresStore) SubmitExternal(ctx context.Context, claim IntentClaim, port ExternalDeliveryPort, now time.Time) (string, error) {
	if claim.Channel == ChannelInApp || port == nil || now.IsZero() {
		return "", errors.New("external notification claim is invalid")
	}
	tx, err := store.beginTenant(ctx, claim.TenantID)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	current, err := lockClaim(ctx, tx, claim.TenantID, claim.IntentID, claim.WorkerID, claim.Fence, now)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE notification_runtime.notification_intent
SET status='EXTERNAL_SUBMITTED',lease_owner=NULL,lease_until=NULL,updated_at=$3
WHERE tenant_id=$1::uuid AND intent_id=$2::uuid`, claim.TenantID, claim.IntentID, now.UTC())
	if err != nil {
		return "", err
	}
	if err := appendIntentEvent(ctx, tx, claim.TenantID, claim.IntentID, "EXTERNAL_SUBMITTED", map[string]any{"phase": "HANDOFF_COMMITTED"}, now.UTC()); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return store.submitCommittedExternalHandoff(ctx, current, port, now)
}

func (store *PostgresStore) ResumeExternalHandoff(ctx context.Context, tenantID, intentID string, port ExternalDeliveryPort, now time.Time) (string, error) {
	if port == nil || now.IsZero() {
		return "", errors.New("external notification recovery input is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	intent, err := loadIntentForUpdate(ctx, tx, tenantID, intentID)
	if err != nil {
		return "", err
	}
	if intent.Status != IntentExternalSubmitted || intent.Channel == ChannelInApp {
		return "", ErrNotFound
	}
	if intent.ExternalDeliveryIntentID != "" {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return intent.ExternalDeliveryIntentID, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return store.submitCommittedExternalHandoff(ctx, intent, port, now)
}

func (store *PostgresStore) submitCommittedExternalHandoff(ctx context.Context, intent NotificationIntent, port ExternalDeliveryPort, now time.Time) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"schemaVersion":         1,
		"notificationIntentId":  intent.IntentID,
		"channel":               intent.Channel,
		"recipients":            intent.Recipients,
		"subject":               intent.RenderedSubject,
		"body":                  intent.RenderedBody,
		"sourceEventId":         intent.SourceEventID,
		"alarmId":               intent.AlarmID,
		"incidentCorrelationId": intent.IncidentCorrelationID,
	})
	if err != nil {
		return "", err
	}
	delivery, err := port.SubmitNotification(ctx, outbounddelivery.SubmitIntentRequest{
		TenantID: intent.TenantID, SiteID: intent.SiteID, IntegrationID: intent.IntegrationID,
		Purpose: "NOTIFICATION_" + string(intent.Channel), PayloadSchema: "notification-delivery/v1", Payload: payload,
		IdempotencyKey: intent.IntentID, SourceAggregateType: "NOTIFICATION_INTENT", SourceAggregateID: intent.IntentID,
		Classification: "NOTIFICATION_RECIPIENT", CreatedAt: intent.DueAt,
	})
	if err != nil {
		return "", err
	}
	tx, err := store.beginTenant(ctx, intent.TenantID)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	commandTag, err := tx.Exec(ctx, `UPDATE notification_runtime.notification_intent
SET external_delivery_intent_id=$3::uuid,updated_at=$4
WHERE tenant_id=$1::uuid AND intent_id=$2::uuid AND status='EXTERNAL_SUBMITTED'
  AND (external_delivery_intent_id IS NULL OR external_delivery_intent_id=$3::uuid)`, intent.TenantID, intent.IntentID, delivery.ID, now.UTC())
	if err != nil {
		return "", err
	}
	if commandTag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	if err := appendIntentEvent(ctx, tx, intent.TenantID, intent.IntentID, "DELIVERY_UPDATED", map[string]any{"phase": "S15_INTENT_BOUND", "deliveryIntentId": delivery.ID}, now.UTC()); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return delivery.ID, nil
}

func (store *PostgresStore) RecordExternalDisposition(ctx context.Context, tenantID, intentID, deliveryIntentID string, deliveryState outbounddelivery.IntentState, now time.Time) error {
	if now.IsZero() {
		return errors.New("notification delivery disposition time is required")
	}
	status, disposition := IntentExternalSubmitted, DispositionPending
	switch deliveryState {
	case outbounddelivery.IntentDelivered:
		status, disposition = IntentDelivered, DispositionDelivered
	case outbounddelivery.IntentOutcomeUnknown:
		status, disposition = IntentExternalSubmitted, DispositionUnknown
	case outbounddelivery.IntentDead:
		status, disposition = IntentFailed, DispositionFailed
	case outbounddelivery.IntentReady, outbounddelivery.IntentLeased, outbounddelivery.IntentRetryWait:
		return nil
	default:
		return errors.New("unsupported S15 delivery state")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	commandTag, err := tx.Exec(ctx, `UPDATE notification_runtime.notification_intent SET status=$4,disposition=$5,updated_at=$6
WHERE tenant_id=$1::uuid AND intent_id=$2::uuid AND external_delivery_intent_id=$3::uuid`, tenantID, intentID, deliveryIntentID, status, disposition, now.UTC())
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := appendIntentEvent(ctx, tx, tenantID, intentID, "DELIVERY_UPDATED", map[string]any{"deliveryIntentId": deliveryIntentID, "deliveryState": deliveryState, "disposition": disposition}, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListInbox(ctx context.Context, tenantID, principalID string, limit int) ([]InboxItem, error) {
	if limit <= 0 || limit > 200 || strings.TrimSpace(principalID) == "" {
		return nil, errors.New("notification inbox query is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT inbox_item_id::text,intent_id::text,tenant_id::text,site_id::text,principal_id,alarm_id::text,incident_correlation_id::text,
source_action,severity,subject,body,status,created_at,read_at
FROM notification_runtime.inbox_item WHERE tenant_id=$1::uuid AND principal_id=$2
ORDER BY created_at DESC,inbox_item_id DESC LIMIT $3`, tenantID, principalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []InboxItem
	for rows.Next() {
		var item InboxItem
		if err := rows.Scan(&item.InboxItemID, &item.IntentID, &item.TenantID, &item.SiteID, &item.PrincipalID, &item.AlarmID, &item.IncidentCorrelationID,
			&item.SourceAction, &item.Severity, &item.Subject, &item.Body, &item.Status, &item.CreatedAt, &item.ReadAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (store *PostgresStore) MarkRead(ctx context.Context, tenantID, principalID, inboxItemID string, now time.Time) (InboxItem, error) {
	if strings.TrimSpace(principalID) == "" || now.IsZero() {
		return InboxItem{}, errors.New("notification read input is invalid")
	}
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return InboxItem{}, err
	}
	defer tx.Rollback(ctx)
	commandTag, err := tx.Exec(ctx, `UPDATE notification_runtime.inbox_item
SET status=CASE WHEN status='UNREAD' THEN 'READ' ELSE status END,
    read_at=CASE WHEN status='UNREAD' THEN $4 ELSE read_at END
WHERE tenant_id=$1::uuid AND principal_id=$2 AND inbox_item_id=$3::uuid`, tenantID, principalID, inboxItemID, now.UTC())
	if err != nil {
		return InboxItem{}, err
	}
	if commandTag.RowsAffected() == 0 {
		return InboxItem{}, ErrNotFound
	}
	item, err := loadInboxItem(ctx, tx, tenantID, principalID, inboxItemID)
	if err != nil {
		return InboxItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InboxItem{}, err
	}
	return item, nil
}

func (store *PostgresStore) ListIntentsForSource(ctx context.Context, tenantID, sourceEventID string) ([]NotificationIntent, error) {
	tx, err := store.beginTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	intents, err := loadIntentsForSource(ctx, tx, tenantID, sourceEventID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return intents, nil
}

func loadIntentsForSource(ctx context.Context, tx pgx.Tx, tenantID, sourceEventID string) ([]NotificationIntent, error) {
	rows, err := tx.Query(ctx, intentSelect+` WHERE tenant_id=$1::uuid AND source_event_id=$2::uuid ORDER BY assignment_id,assignment_revision,stage`, tenantID, sourceEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []NotificationIntent
	for rows.Next() {
		intent, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

const intentSelect = `SELECT intent_id::text,tenant_id::text,site_id::text,source_event_id::text,alarm_id::text,incident_correlation_id::text,
source_action,current_severity,policy_revision_id::text,assignment_id::text,assignment_revision,stage,channel,COALESCE(integration_id::text,''),mandatory_safety,
recipients::text,template_revision_id::text,rendered_subject,rendered_body,due_at,status,disposition,COALESCE(external_delivery_intent_id::text,'')
FROM notification_runtime.notification_intent`

const intentClaimSelect = `SELECT intent_id::text,tenant_id::text,site_id::text,source_event_id::text,alarm_id::text,incident_correlation_id::text,
source_action,current_severity,policy_revision_id::text,assignment_id::text,assignment_revision,stage,channel,COALESCE(integration_id::text,''),mandatory_safety,
recipients::text,template_revision_id::text,rendered_subject,rendered_body,due_at,status,disposition,COALESCE(external_delivery_intent_id::text,''),lease_owner,lease_until,lease_fence
FROM notification_runtime.notification_intent`

func loadClaim(ctx context.Context, tx pgx.Tx, tenantID, intentID string) (IntentClaim, error) {
	return loadClaimWithLock(ctx, tx, tenantID, intentID, false)
}

func loadClaimWithLock(ctx context.Context, tx pgx.Tx, tenantID, intentID string, lock bool) (IntentClaim, error) {
	query := intentClaimSelect + ` WHERE tenant_id=$1::uuid AND intent_id=$2::uuid`
	if lock {
		query += ` FOR UPDATE`
	}
	row := tx.QueryRow(ctx, query, tenantID, intentID)
	var claim IntentClaim
	var recipients []byte
	var leaseOwner *string
	var leaseUntil *time.Time
	if err := row.Scan(&claim.IntentID, &claim.TenantID, &claim.SiteID, &claim.SourceEventID, &claim.AlarmID, &claim.IncidentCorrelationID,
		&claim.SourceAction, &claim.CurrentSeverity, &claim.PolicyRevisionID, &claim.AssignmentID, &claim.AssignmentRevision, &claim.Stage, &claim.Channel, &claim.IntegrationID,
		&claim.MandatorySafety, &recipients, &claim.TemplateRevisionID, &claim.RenderedSubject, &claim.RenderedBody, &claim.DueAt, &claim.Status,
		&claim.Disposition, &claim.ExternalDeliveryIntentID, &leaseOwner, &leaseUntil, &claim.Fence); err != nil {
		return IntentClaim{}, err
	}
	if err := json.Unmarshal(recipients, &claim.Recipients); err != nil {
		return IntentClaim{}, err
	}
	if leaseOwner != nil {
		claim.WorkerID = *leaseOwner
	}
	if leaseUntil != nil {
		claim.LeaseUntil = *leaseUntil
	}
	return claim, nil
}

func lockClaim(ctx context.Context, tx pgx.Tx, tenantID, intentID, workerID string, fence uint64, now time.Time) (NotificationIntent, error) {
	claim, err := loadClaimWithLock(ctx, tx, tenantID, intentID, true)
	if err != nil {
		return NotificationIntent{}, err
	}
	if claim.Status != IntentClaimed || claim.WorkerID != workerID || claim.Fence != fence || !now.UTC().Before(claim.LeaseUntil) {
		return NotificationIntent{}, ErrClaimLost
	}
	return claim.NotificationIntent, nil
}

func loadIntentForUpdate(ctx context.Context, tx pgx.Tx, tenantID, intentID string) (NotificationIntent, error) {
	return scanIntent(tx.QueryRow(ctx, intentSelect+` WHERE tenant_id=$1::uuid AND intent_id=$2::uuid FOR UPDATE`, tenantID, intentID))
}

func scanIntent(row interface{ Scan(...any) error }) (NotificationIntent, error) {
	var intent NotificationIntent
	var recipients []byte
	err := row.Scan(&intent.IntentID, &intent.TenantID, &intent.SiteID, &intent.SourceEventID, &intent.AlarmID, &intent.IncidentCorrelationID,
		&intent.SourceAction, &intent.CurrentSeverity, &intent.PolicyRevisionID, &intent.AssignmentID, &intent.AssignmentRevision, &intent.Stage, &intent.Channel,
		&intent.IntegrationID, &intent.MandatorySafety, &recipients, &intent.TemplateRevisionID, &intent.RenderedSubject, &intent.RenderedBody, &intent.DueAt,
		&intent.Status, &intent.Disposition, &intent.ExternalDeliveryIntentID)
	if err != nil {
		return NotificationIntent{}, err
	}
	if err := json.Unmarshal(recipients, &intent.Recipients); err != nil {
		return NotificationIntent{}, err
	}
	return intent, nil
}

func listInboxByIntent(ctx context.Context, tx pgx.Tx, tenantID, intentID string) ([]InboxItem, error) {
	rows, err := tx.Query(ctx, `SELECT inbox_item_id::text,intent_id::text,tenant_id::text,site_id::text,principal_id,alarm_id::text,incident_correlation_id::text,
source_action,severity,subject,body,status,created_at,read_at FROM notification_runtime.inbox_item
WHERE tenant_id=$1::uuid AND intent_id=$2::uuid ORDER BY principal_id`, tenantID, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []InboxItem
	for rows.Next() {
		var item InboxItem
		if err := rows.Scan(&item.InboxItemID, &item.IntentID, &item.TenantID, &item.SiteID, &item.PrincipalID, &item.AlarmID, &item.IncidentCorrelationID,
			&item.SourceAction, &item.Severity, &item.Subject, &item.Body, &item.Status, &item.CreatedAt, &item.ReadAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadInboxItem(ctx context.Context, tx pgx.Tx, tenantID, principalID, inboxItemID string) (InboxItem, error) {
	var item InboxItem
	err := tx.QueryRow(ctx, `SELECT inbox_item_id::text,intent_id::text,tenant_id::text,site_id::text,principal_id,alarm_id::text,incident_correlation_id::text,
source_action,severity,subject,body,status,created_at,read_at FROM notification_runtime.inbox_item
WHERE tenant_id=$1::uuid AND principal_id=$2 AND inbox_item_id=$3::uuid`, tenantID, principalID, inboxItemID).Scan(&item.InboxItemID, &item.IntentID,
		&item.TenantID, &item.SiteID, &item.PrincipalID, &item.AlarmID, &item.IncidentCorrelationID, &item.SourceAction, &item.Severity, &item.Subject,
		&item.Body, &item.Status, &item.CreatedAt, &item.ReadAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxItem{}, ErrNotFound
	}
	return item, err
}

func appendIntentEvent(ctx context.Context, tx pgx.Tx, tenantID, intentID, eventType string, detail map[string]any, occurredAt time.Time) error {
	eventID, err := uuidv7(occurredAt)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO notification_runtime.intent_event (tenant_id,event_id,intent_id,event_type,detail,occurred_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::jsonb,$6)`, tenantID, eventID, intentID, eventType, encoded, occurredAt.UTC())
	return err
}

func (store *PostgresStore) beginTenant(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if store == nil || store.pool == nil || strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("notification Tenant scope is required")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE s16_notification_runtime`); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func validChannel(channel Channel) bool {
	return channel == ChannelInApp || channel == ChannelEmail || channel == ChannelREST
}

func nilUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func uuidv7(now time.Time) (string, error) {
	if now.IsZero() {
		return "", errors.New("uuidv7 timestamp is required")
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate uuidv7 randomness: %w", err)
	}
	milliseconds := uint64(now.UnixMilli())
	buffer[0], buffer[1], buffer[2], buffer[3], buffer[4], buffer[5] = byte(milliseconds>>40), byte(milliseconds>>32), byte(milliseconds>>24), byte(milliseconds>>16), byte(milliseconds>>8), byte(milliseconds)
	buffer[6] = (buffer[6] & 0x0f) | 0x70
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(buffer)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}
