package alarmservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

type PostgresStore struct {
	pool            *pgxpool.Pool
	newID           idGenerator
	ruleCache       siteRuleCache
	edgeSyncChecker EdgeRuleSyncChecker
}

// SetEdgeRuleSyncChecker attaches a connectivity client used to avoid redundant
// cloud evaluation of pure EDGE rules when the gateway is online and synced.
func (store *PostgresStore) SetEdgeRuleSyncChecker(checker EdgeRuleSyncChecker) {
	if store != nil {
		store.edgeSyncChecker = checker
	}
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Alarm database URL: %w", err)
	}
	if config.ConnConfig.User != "s4_alarm_service" {
		return nil, errors.New("alarm database identity must be s4_alarm_service")
	}
	config.MaxConns = 16
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE s4_alarm_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open Alarm database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping Alarm database: %w", err)
	}
	return &PostgresStore{pool: pool, newID: uuidv7.NewAt}, nil
}

func (store *PostgresStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresStore) List(ctx context.Context, tenantID, siteID string, filter Filter) (alarmmodel.ListResponse, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.ListResponse{}, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return alarmmodel.ListResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var cursorTriggeredAt *time.Time
	var cursorAlarmID any
	if filter.Cursor != "" {
		triggeredAt, alarmID, err := decodeAlarmCursor(tenantID, siteID, filter)
		if err != nil {
			return alarmmodel.ListResponse{}, ErrInvalidCursor
		}
		cursorTriggeredAt = &triggeredAt
		cursorAlarmID = alarmID
	}
	rows, err := tx.Query(ctx, alarmSelect+`
		WHERE ac.tenant_id = $1 AND ac.site_id = $2
		  AND ($3 = '' OR ac.condition = $3)
		  AND ($4 = '' OR ac.current_severity = $4)
		  AND ($5::boolean IS NULL OR (ac.acknowledged_at IS NOT NULL) = $5)
		  AND ($6::boolean IS NULL OR (ac.suppression IS NOT NULL) = $6)
		  AND ($7::timestamptz IS NULL OR ac.first_occurred_at < $7 OR (ac.first_occurred_at = $7 AND ac.alarm_id < $8::uuid))
		  AND ($10::timestamptz IS NULL OR ac.first_occurred_at <= $10)
		  AND ($11::timestamptz IS NULL OR ac.cleared_at IS NULL OR ac.cleared_at >= $11)
		ORDER BY ac.first_occurred_at DESC, ac.alarm_id DESC
		LIMIT $9`, tenantID, siteID, string(filter.Condition), string(filter.Severity), filter.Acknowledged, filter.Suppressed, cursorTriggeredAt, cursorAlarmID, limit+1, filter.ActiveTo, filter.ActiveFrom)
	if err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("list Alarms: %w", err)
	}
	defer rows.Close()
	items := make([]alarmmodel.Alarm, 0, limit+1)
	for rows.Next() {
		alarm, err := scanAlarmBase(rows)
		if err != nil {
			return alarmmodel.ListResponse{}, err
		}
		items = append(items, alarm)
	}
	if err := rows.Err(); err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("iterate Alarms: %w", err)
	}
	for index := range items {
		if err := loadTimeline(ctx, tx, &items[index]); err != nil {
			return alarmmodel.ListResponse{}, err
		}
	}
	response := alarmmodel.ListResponse{SchemaVersion: alarmmodel.SchemaVersion, Items: items}
	if len(response.Items) > limit {
		response.Items = response.Items[:limit]
		cursor, err := encodeAlarmCursor(tenantID, siteID, filter, response.Items[len(response.Items)-1])
		if err != nil {
			return alarmmodel.ListResponse{}, fmt.Errorf("encode Alarm cursor: %w", err)
		}
		response.NextCursor = &cursor
		response.HasMore = true
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.ListResponse{}, fmt.Errorf("commit Alarm list read: %w", err)
	}
	return response, nil
}

func (store *PostgresStore) Get(ctx context.Context, tenantID, siteID, alarmID string) (alarmmodel.Alarm, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.Alarm{}, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	alarm, err := getAlarmRow(ctx, tx, tenantID, siteID, alarmID, false)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("commit Alarm detail read: %w", err)
	}
	return alarm, nil
}

func (store *PostgresStore) ResolveScope(ctx context.Context, tenantID, alarmID string) (AlarmScope, error) {
	if store == nil || store.pool == nil {
		return AlarmScope{}, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return AlarmScope{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var scope AlarmScope
	err = tx.QueryRow(ctx, `SELECT tenant_id::text, site_id::text FROM alarm_runtime.alarm_current WHERE tenant_id = $1 AND alarm_id = $2`, tenantID, alarmID).Scan(&scope.TenantID, &scope.SiteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlarmScope{}, ErrNotFound
	}
	if err != nil {
		return AlarmScope{}, fmt.Errorf("resolve Alarm scope: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AlarmScope{}, fmt.Errorf("commit Alarm scope read: %w", err)
	}
	return scope, nil
}

func (store *PostgresStore) ListAlarmEvaluations(ctx context.Context, tenantID, siteID, alarmID string, limit int) (AlarmEvaluationList, error) {
	if store == nil || store.pool == nil {
		return AlarmEvaluationList{}, ErrUnavailable
	}
	if !alarmmodel.IsUUIDv7(siteID) || !alarmmodel.IsUUIDv7(alarmID) {
		return AlarmEvaluationList{}, ErrNotFound
	}
	limit = normalizeEvaluationLimit(limit)
	tx, err := store.beginTenantTransaction(ctx, tenantID, true)
	if err != nil {
		return AlarmEvaluationList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fingerprint string
	var firstOccurredAt time.Time
	var eventID *string
	err = tx.QueryRow(ctx, `
		SELECT fingerprint, first_occurred_at, event_id::text
		FROM alarm_runtime.alarm_current
		WHERE tenant_id = $1 AND site_id = $2 AND alarm_id = $3`,
		tenantID, siteID, alarmID,
	).Scan(&fingerprint, &firstOccurredAt, &eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlarmEvaluationList{}, ErrNotFound
	}
	if err != nil {
		return AlarmEvaluationList{}, fmt.Errorf("lookup Alarm for evaluations: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT evaluated_at, policy_revision_id::text, assignment_id::text, input_revision,
		       status, effect, quality_blocker, snapshot, active_incident_correlation_id::text
		FROM alarm_runtime.alarm_evaluation_event
		WHERE tenant_id = $1
		  AND site_id = $2
		  AND (
		    active_alarm_id = $3
		    OR (
		      active_alarm_id IS NULL
		      AND fingerprint = $4
		      AND (
		        (evaluated_at >= ($5::timestamptz - interval '10 minutes') AND evaluated_at <= $5::timestamptz)
		        OR ($7::text IS NOT NULL AND snapshot->>'eventId' = $7::text)
		      )
		    )
		  )
		ORDER BY evaluated_at DESC, state_version DESC
		LIMIT $6`,
		tenantID, siteID, alarmID, fingerprint, firstOccurredAt.UTC(), limit, eventID,
	)
	if err != nil {
		return AlarmEvaluationList{}, fmt.Errorf("query Alarm evaluation history: %w", err)
	}
	defer rows.Close()

	items := make([]AlarmEvaluationRecord, 0)
	for rows.Next() {
		var record AlarmEvaluationRecord
		var evaluatedAt time.Time
		var snapshot []byte
		if err := rows.Scan(
			&evaluatedAt,
			&record.PolicyRevisionID,
			&record.AssignmentID,
			&record.InputRevision,
			&record.Status,
			&record.Effect,
			&record.QualityBlocker,
			&snapshot,
			&record.ActiveIncidentCorrelationID,
		); err != nil {
			return AlarmEvaluationList{}, fmt.Errorf("scan Alarm evaluation history row: %w", err)
		}
		record.EvaluatedAt = evaluatedAt.UTC().Format(time.RFC3339Nano)
		record.Snapshot = append(json.RawMessage(nil), snapshot...)
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return AlarmEvaluationList{}, fmt.Errorf("iterate Alarm evaluation history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AlarmEvaluationList{}, fmt.Errorf("commit Alarm evaluation history read: %w", err)
	}
	return AlarmEvaluationList{
		SchemaVersion: AlarmEvaluationsSchemaVersion,
		AlarmID:       alarmID,
		Items:         items,
	}, nil
}

func (store *PostgresStore) Publish(ctx context.Context, tenantID, siteID string, publication Publication) (alarmmodel.Alarm, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.Alarm{}, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	alarm, err := store.publishInTransaction(ctx, tx, tenantID, siteID, publication)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("commit Alarm publication: %w", err)
	}
	return alarm, nil
}

func (store *PostgresStore) ClearActive(ctx context.Context, tenantID, siteID string, recovery Recovery) (alarmmodel.Alarm, error) {
	if store == nil || store.pool == nil {
		return alarmmodel.Alarm{}, ErrUnavailable
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	alarm, err := store.clearInTransaction(ctx, tx, tenantID, siteID, recovery)
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("commit Alarm recovery: %w", err)
	}
	return alarm, nil
}

func (store *PostgresStore) Apply(ctx context.Context, tenantID, siteID, alarmID string, mutation Mutation) (MutationResult, error) {
	if store == nil || store.pool == nil {
		return MutationResult{}, ErrUnavailable
	}
	key := strings.TrimSpace(mutation.IdempotencyKey)
	if key == "" && mutation.Operation != alarmmodel.OperationAcknowledge {
		return MutationResult{}, alarmmodel.ErrInvalidOperation
	}
	digest, err := mutationDigest(mutation)
	if err != nil {
		return MutationResult{}, alarmmodel.ErrInvalidOperation
	}
	tx, err := store.beginTenantTransaction(ctx, tenantID, false)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := getAlarmRow(ctx, tx, tenantID, siteID, alarmID, true)
	if err != nil {
		return MutationResult{}, err
	}
	var responseJSON []byte
	if key != "" {
		var storedDigest string
		err = tx.QueryRow(ctx, `SELECT request_digest, response FROM alarm_runtime.alarm_idempotency WHERE tenant_id = $1 AND site_id = $2 AND alarm_id = $3 AND idempotency_key = $4`, tenantID, siteID, alarmID, key).Scan(&storedDigest, &responseJSON)
		if err == nil {
			if storedDigest != digest {
				return MutationResult{}, ErrIdempotencyConflict
			}
			var replay alarmmodel.Alarm
			if json.Unmarshal(responseJSON, &replay) != nil || replay.Validate() != nil || replay.TenantID != tenantID || replay.SiteID != siteID || replay.AlarmID != alarmID {
				return MutationResult{}, ErrUnavailable
			}
			if err := tx.Commit(ctx); err != nil {
				return MutationResult{}, fmt.Errorf("commit Alarm idempotency replay: %w", err)
			}
			return MutationResult{Alarm: replay, Replayed: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return MutationResult{}, fmt.Errorf("read Alarm idempotency record: %w", err)
		}
	}
	updated, err := alarmmodel.ApplyOperation(current, mutation.operationInput())
	if err != nil {
		return MutationResult{}, err
	}
	if err := persistUpdatedAlarm(ctx, tx, current, updated); err != nil {
		return MutationResult{}, err
	}
	if err := enqueueTransitionNotifications(ctx, tx, store, current, updated); err != nil {
		return MutationResult{}, err
	}
	responseJSON, err = json.Marshal(updated)
	if err != nil {
		return MutationResult{}, fmt.Errorf("encode Alarm mutation response: %w", err)
	}
	if key != "" {
		_, err = tx.Exec(ctx, `INSERT INTO alarm_runtime.alarm_idempotency (tenant_id, site_id, alarm_id, idempotency_key, request_digest, response, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenantID, siteID, alarmID, key, digest, responseJSON, mutation.OccurredAt)
		if err != nil {
			return MutationResult{}, fmt.Errorf("write Alarm idempotency record: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationResult{}, fmt.Errorf("commit Alarm mutation: %w", err)
	}
	return MutationResult{Alarm: updated, Replayed: updated.Version == current.Version}, nil
}

func (publication Publication) occurrenceInput() alarmmodel.OccurrenceInput {
	return alarmmodel.OccurrenceInput{Severity: publication.Severity, OccurredAt: publication.OccurredAt, Evidence: publication.Evidence, RuleRevision: publication.RuleRevision, ActorType: publication.ActorType, ActorID: publication.ActorID, CorrelationID: publication.CorrelationID}
}

func (publication Publication) incidentInput(alarmID, incidentCorrelationID, tenantID, siteID string) alarmmodel.IncidentInput {
	return alarmmodel.IncidentInput{AlarmID: alarmID, TenantID: tenantID, SiteID: siteID, DeviceID: publication.DeviceID, EventID: publication.EventID, PointID: publication.PointID, AlarmType: publication.AlarmType, IncidentCorrelationID: incidentCorrelationID, SourceType: publication.SourceType, SourceReference: publication.SourceReference, RuleRevision: publication.RuleRevision, Title: publication.Title, Summary: publication.Summary, Severity: publication.Severity, OccurredAt: publication.OccurredAt, Evidence: publication.Evidence, ActorType: publication.ActorType, ActorID: publication.ActorID}
}

func (store *PostgresStore) beginTenantTransaction(ctx context.Context, tenantID string, readOnly bool) (pgx.Tx, error) {
	options := pgx.TxOptions{}
	if readOnly {
		options.AccessMode = pgx.ReadOnly
	}
	return store.beginTenantTransactionWithOptions(ctx, tenantID, options)
}

func (store *PostgresStore) beginTenantTransactionWithOptions(ctx context.Context, tenantID string, options pgx.TxOptions) (pgx.Tx, error) {
	contextTenantID, ok := identitycontext.TenantIDFromContext(ctx)
	if !ok || !alarmmodel.IsUUIDv7(tenantID) || contextTenantID != tenantID {
		return nil, ErrUnavailable
	}
	tx, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("begin Alarm transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("activate Alarm Tenant scope: %w", err)
	}
	return tx, nil
}

const alarmSelect = `
	SELECT ac.alarm_id, ac.tenant_id, ac.site_id, ac.device_id, ac.event_id, ac.point_id, ac.alarm_type, ac.fingerprint,
	       ac.incident_correlation_id, ac.source_type, ac.source_reference, ac.rule_revision, ac.title, ac.summary,
	       ac.condition, ac.current_severity, ac.peak_severity, ac.acknowledged_at, ac.acknowledged_by, ac.acknowledgement_comment,
	       ac.assignee_id, ac.suppression, ac.occurrence_count, ac.first_occurred_at, ac.last_occurred_at, ac.cleared_at,
	       ac.evidence, ac.links, ac.version, ac.created_at, ac.updated_at, ev.metadata
	FROM alarm_runtime.alarm_current AS ac
	LEFT JOIN alarm_runtime.events AS ev
	  ON ev.tenant_id = ac.tenant_id AND ev.site_id = ac.site_id AND ev.event_id = ac.event_id`

func getAlarmRow(ctx context.Context, tx pgx.Tx, tenantID, siteID, alarmID string, lock bool) (alarmmodel.Alarm, error) {
	query := alarmSelect + ` WHERE ac.tenant_id = $1 AND ac.site_id = $2 AND ac.alarm_id = $3`
	if lock {
		query += ` FOR UPDATE OF ac`
	}
	alarm, err := scanAlarmBase(tx.QueryRow(ctx, query, tenantID, siteID, alarmID))
	if errors.Is(err, pgx.ErrNoRows) {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := loadTimeline(ctx, tx, &alarm); err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := alarm.Validate(); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("validate stored Alarm: %w", err)
	}
	return alarm, nil
}

func getActiveByFingerprint(ctx context.Context, tx pgx.Tx, tenantID, siteID, fingerprint string, lock bool) (alarmmodel.Alarm, error) {
	query := alarmSelect + ` WHERE ac.tenant_id = $1 AND ac.site_id = $2 AND ac.fingerprint = $3 AND ac.condition = 'ACTIVE'`
	if lock {
		query += ` FOR UPDATE OF ac`
	}
	alarm, err := scanAlarmBase(tx.QueryRow(ctx, query, tenantID, siteID, fingerprint))
	if errors.Is(err, pgx.ErrNoRows) {
		return alarmmodel.Alarm{}, ErrNotFound
	}
	if err != nil {
		return alarmmodel.Alarm{}, err
	}
	if err := loadTimeline(ctx, tx, &alarm); err != nil {
		return alarmmodel.Alarm{}, err
	}
	return alarm, alarm.Validate()
}

type alarmScanner interface{ Scan(...any) error }

func scanAlarmBase(scanner alarmScanner) (alarmmodel.Alarm, error) {
	var alarm alarmmodel.Alarm
	var evidenceJSON, linksJSON, eventMetadataJSON []byte
	var firstOccurredAt, lastOccurredAt, createdAt, updatedAt time.Time
	var acknowledgedAt, clearedAt *time.Time
	var suppressionJSON []byte
	var acknowledgedBy, acknowledgementComment *string
	if err := scanner.Scan(
		&alarm.AlarmID, &alarm.TenantID, &alarm.SiteID, &alarm.DeviceID, &alarm.EventID, &alarm.PointID,
		&alarm.AlarmType, &alarm.Fingerprint, &alarm.IncidentCorrelationID, &alarm.SourceType, &alarm.SourceReference,
		&alarm.RuleRevision, &alarm.Title, &alarm.Summary, &alarm.Condition, &alarm.CurrentSeverity, &alarm.PeakSeverity,
		&acknowledgedAt, &acknowledgedBy, &acknowledgementComment, &alarm.AssigneeID, &suppressionJSON,
		&alarm.OccurrenceCount, &firstOccurredAt, &lastOccurredAt, &clearedAt, &evidenceJSON, &linksJSON,
		&alarm.Version, &createdAt, &updatedAt, &eventMetadataJSON,
	); err != nil {
		return alarmmodel.Alarm{}, err
	}
	alarm.SchemaVersion = alarmmodel.SchemaVersion
	alarm.FirstOccurredAt = firstOccurredAt.UTC().Format(time.RFC3339Nano)
	alarm.LastOccurredAt = lastOccurredAt.UTC().Format(time.RFC3339Nano)
	alarm.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	alarm.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	if acknowledgedAt != nil {
		alarm.Acknowledgement = &alarmmodel.Acknowledgement{AcknowledgedAt: acknowledgedAt.UTC().Format(time.RFC3339Nano), AcknowledgedBy: dereference(acknowledgedBy), Comment: dereference(acknowledgementComment)}
	}
	if clearedAt != nil {
		value := clearedAt.UTC().Format(time.RFC3339Nano)
		alarm.ClearedAt = &value
	}
	if len(suppressionJSON) > 0 && string(suppressionJSON) != "null" {
		if err := json.Unmarshal(suppressionJSON, &alarm.Suppression); err != nil {
			return alarmmodel.Alarm{}, fmt.Errorf("decode Alarm suppression: %w", err)
		}
	}
	if err := json.Unmarshal(evidenceJSON, &alarm.Evidence); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("decode Alarm evidence: %w", err)
	}
	if err := json.Unmarshal(linksJSON, &alarm.Links); err != nil {
		return alarmmodel.Alarm{}, fmt.Errorf("decode Alarm links: %w", err)
	}
	alarm.EdgeProjection = decodeEdgeProjectionMetadata(eventMetadataJSON)
	return alarm, nil
}

func decodeEdgeProjectionMetadata(raw []byte) *alarmmodel.EdgeProjection {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return nil
	}
	var payload struct {
		Source                string   `json:"source"`
		ClearSource           string   `json:"clearSource"`
		GatewayID             string   `json:"gatewayId"`
		ClearGatewayID        string   `json:"clearGatewayId"`
		Offline               bool     `json:"offline"`
		BundleRevision        int64    `json:"bundleRevision"`
		EdgeBundleDigest      string   `json:"edgeBundleDigest"`
		OccurredAt            string   `json:"occurredAt"`
		ReceivedAt            string   `json:"receivedAt"`
		TriggeredActions      []string `json:"triggeredActions"`
		ClearOffline          *bool    `json:"clearOffline"`
		ClearOccurredAt       string   `json:"clearOccurredAt"`
		ClearTriggeredActions []string `json:"clearTriggeredActions"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(payload.Source) != "EDGE_RULE" && strings.TrimSpace(payload.ClearSource) != "EDGE_RULE" {
		return nil
	}
	gatewayID := firstNonEmptyEdgeString(payload.GatewayID, payload.ClearGatewayID)
	if len(gatewayID) == 0 || len(gatewayID) > 128 {
		return nil
	}
	bundleRevision := payload.BundleRevision
	if bundleRevision < 0 {
		bundleRevision = 0
	}
	digest := strings.ToLower(strings.TrimSpace(payload.EdgeBundleDigest))
	if !digest64HexPattern.MatchString(digest) {
		digest = ""
	}
	return &alarmmodel.EdgeProjection{
		GatewayID:             gatewayID,
		Offline:               payload.Offline,
		BundleRevision:        bundleRevision,
		EdgeBundleDigest:      digest,
		OccurredAt:            normalizeOptionalRFC3339Nano(payload.OccurredAt),
		ReceivedAt:            normalizeOptionalRFC3339Nano(payload.ReceivedAt),
		TriggeredActions:      sanitizeEdgeProjectionActions(payload.TriggeredActions),
		ClearOffline:          payload.ClearOffline,
		ClearOccurredAt:       normalizeOptionalRFC3339Nano(payload.ClearOccurredAt),
		ClearTriggeredActions: sanitizeEdgeProjectionActions(payload.ClearTriggeredActions),
	}
}

func normalizeOptionalRFC3339Nano(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func sanitizeEdgeProjectionActions(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cleaned := make([]string, 0, min(len(items), 32))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, truncateBounded(trimmed, 256))
		if len(cleaned) >= 32 {
			break
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func loadTimeline(ctx context.Context, tx pgx.Tx, alarm *alarmmodel.Alarm) error {
	rows, err := tx.Query(ctx, `
		SELECT operation, condition, reason, actor_type, actor_id, assignee_id, suppression, current_severity,
		       policy_revision, correlation_id, occurred_at, version
		FROM alarm_runtime.alarm_timeline
		WHERE tenant_id = $1 AND site_id = $2 AND alarm_id = $3
		ORDER BY version`, alarm.TenantID, alarm.SiteID, alarm.AlarmID)
	if err != nil {
		return fmt.Errorf("read Alarm timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry alarmmodel.TimelineEntry
		var suppressionJSON []byte
		var occurredAt time.Time
		if err := rows.Scan(&entry.Operation, &entry.Condition, &entry.Reason, &entry.ActorType, &entry.ActorID, &entry.AssigneeID, &suppressionJSON, &entry.CurrentSeverity, &entry.PolicyRevision, &entry.CorrelationID, &occurredAt, &entry.Version); err != nil {
			return fmt.Errorf("scan Alarm timeline: %w", err)
		}
		entry.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
		if len(suppressionJSON) > 0 && string(suppressionJSON) != "null" {
			if err := json.Unmarshal(suppressionJSON, &entry.Suppression); err != nil {
				return fmt.Errorf("decode Alarm timeline suppression: %w", err)
			}
		}
		alarm.Timeline = append(alarm.Timeline, entry)
	}
	return rows.Err()
}

func insertIncident(ctx context.Context, tx pgx.Tx, alarm alarmmodel.Alarm) (bool, error) {
	evidenceJSON, _ := json.Marshal(alarm.Evidence)
	linksJSON, _ := json.Marshal(alarm.Links)
	command, err := tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_current (
			alarm_id, tenant_id, site_id, device_id, event_id, point_id, alarm_type, fingerprint, incident_correlation_id,
			source_type, source_reference, rule_revision, title, summary, condition, current_severity, peak_severity,
			occurrence_count, first_occurred_at, last_occurred_at, evidence, links, version, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		ON CONFLICT (tenant_id, site_id, fingerprint) WHERE condition = 'ACTIVE' DO NOTHING`,
		alarm.AlarmID, alarm.TenantID, alarm.SiteID, alarm.DeviceID, alarm.EventID, alarm.PointID, alarm.AlarmType, alarm.Fingerprint,
		alarm.IncidentCorrelationID, alarm.SourceType, alarm.SourceReference, alarm.RuleRevision, alarm.Title, alarm.Summary,
		alarm.Condition, alarm.CurrentSeverity, alarm.PeakSeverity, alarm.OccurrenceCount, alarm.FirstOccurredAt, alarm.LastOccurredAt,
		evidenceJSON, linksJSON, alarm.Version, alarm.CreatedAt, alarm.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("insert Alarm incident: %w", err)
	}
	return command.RowsAffected() == 1, nil
}

func persistUpdatedAlarm(ctx context.Context, tx pgx.Tx, current, updated alarmmodel.Alarm) error {
	if updated.Version == current.Version && updated.OccurrenceCount == current.OccurrenceCount {
		return nil
	}
	evidenceJSON, _ := json.Marshal(updated.Evidence)
	linksJSON, _ := json.Marshal(updated.Links)
	var suppressionJSON any
	if updated.Suppression != nil {
		suppressionJSON, _ = json.Marshal(updated.Suppression)
	}
	var acknowledgedAt, acknowledgedBy, acknowledgementComment any
	if updated.Acknowledgement != nil {
		acknowledgedAt, acknowledgedBy, acknowledgementComment = updated.Acknowledgement.AcknowledgedAt, updated.Acknowledgement.AcknowledgedBy, updated.Acknowledgement.Comment
	}
	command, err := tx.Exec(ctx, `
		UPDATE alarm_runtime.alarm_current
		SET rule_revision=$5, condition=$6, current_severity=$7, peak_severity=$8,
		    acknowledged_at=$9, acknowledged_by=$10, acknowledgement_comment=$11, assignee_id=$12, suppression=$13,
		    occurrence_count=$14, last_occurred_at=$15, cleared_at=$16, evidence=$17, links=$18, version=$19, updated_at=$20
		WHERE tenant_id=$1 AND site_id=$2 AND alarm_id=$3 AND version=$4`,
		updated.TenantID, updated.SiteID, updated.AlarmID, current.Version, updated.RuleRevision, updated.Condition,
		updated.CurrentSeverity, updated.PeakSeverity, acknowledgedAt, acknowledgedBy, acknowledgementComment, updated.AssigneeID,
		suppressionJSON, updated.OccurrenceCount, updated.LastOccurredAt, updated.ClearedAt, evidenceJSON, linksJSON, updated.Version, updated.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update Alarm incident: %w", err)
	}
	if command.RowsAffected() != 1 {
		return alarmmodel.ErrVersionConflict
	}
	if updated.Version == current.Version {
		return nil
	}
	return insertTimelineEntry(ctx, tx, updated, updated.Timeline[len(updated.Timeline)-1])
}

func insertTimelineEntry(ctx context.Context, tx pgx.Tx, alarm alarmmodel.Alarm, entry alarmmodel.TimelineEntry) error {
	var suppressionJSON any
	if entry.Suppression != nil {
		suppressionJSON, _ = json.Marshal(entry.Suppression)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO alarm_runtime.alarm_timeline (
			tenant_id, site_id, alarm_id, version, operation, condition, reason, actor_type, actor_id, assignee_id,
			suppression, current_severity, policy_revision, correlation_id, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		alarm.TenantID, alarm.SiteID, alarm.AlarmID, entry.Version, entry.Operation, entry.Condition, entry.Reason, entry.ActorType,
		entry.ActorID, entry.AssigneeID, suppressionJSON, entry.CurrentSeverity, entry.PolicyRevision, entry.CorrelationID, entry.OccurredAt)
	if err != nil {
		return fmt.Errorf("append Alarm timeline: %w", err)
	}
	return nil
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
