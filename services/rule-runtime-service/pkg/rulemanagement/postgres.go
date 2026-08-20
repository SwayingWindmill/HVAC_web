package rulemanagement

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
	"github.com/quanlaihe/hvac-web/services/rule-runtime-service/internal/ruleruntime"
)

var (
	ErrNotFound        = errors.New("rule management resource not found")
	ErrConflict        = errors.New("rule management conflict")
	ErrInvalidIdentity = errors.New("rule management identity invalid")
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

type bindingStreamHead struct {
	SiteID   string
	RuleID   string
	Revision int64
}

func nextBindingRevision(head *bindingStreamHead, siteID, ruleID string) (int64, error) {
	if head == nil {
		return 1, nil
	}
	if head.SiteID != siteID || head.RuleID != ruleID {
		return 0, ErrConflict
	}
	return head.Revision + 1, nil
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("rule management database URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) Release(ctx context.Context, tenantID string, draft Draft, at time.Time) (ruleruntime.RuleRevision, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	defer tx.Rollback(ctx)

	ruleID := strings.TrimSpace(draft.RuleID)
	if ruleID == "" {
		ruleID, err = newUUIDv7(at)
		if err != nil {
			return ruleruntime.RuleRevision{}, err
		}
	}
	if !isUUIDv7(ruleID) {
		return ruleruntime.RuleRevision{}, ErrInvalidIdentity
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+ruleID); err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	var revisionNumber int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM rule_runtime.rule_revisions WHERE tenant_id=$1::uuid AND rule_id=$2::uuid`, tenantID, ruleID).Scan(&revisionNumber); err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	revisionID, err := newUUIDv7(at)
	if err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	revision := ruleruntime.RuleRevision{
		ID: revisionID, RuleID: ruleID, TenantID: tenantID, Revision: revisionNumber, State: ruleruntime.RevisionReleased,
		CatalogVersion: firstNonEmpty(strings.TrimSpace(draft.CatalogVersion), CatalogVersion), EntryNodeID: draft.EntryNodeID,
		Nodes: cloneNodes(draft.Nodes), Edges: append([]ruleruntime.Edge(nil), draft.Edges...), AllowedPermissions: append([]string(nil), draft.AllowedPermissions...),
		MaxNodes: draft.MaxNodes, MaxDepth: draft.MaxDepth, MaxFanout: draft.MaxFanout, MaxResourceCost: draft.MaxResourceCost, MaxAttempts: draft.MaxAttempts,
	}
	plan, err := ruleruntime.Compile(revision, ruleruntime.CoreCatalogV1())
	if err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	revision.Digest = plan.Digest
	content, err := json.Marshal(revision)
	if err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rule_runtime.rule_revisions
(id,tenant_id,rule_id,revision,catalog_version,content,content_digest,released_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::jsonb,$7,$8)`, revision.ID, tenantID, ruleID, revision.Revision, revision.CatalogVersion, content, plan.Digest, at.UTC()); err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ruleruntime.RuleRevision{}, err
	}
	return revision, nil
}

func (s *PostgresStore) ListRevisions(ctx context.Context, tenantID, ruleID string) ([]ruleruntime.RuleRevision, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query := `SELECT content FROM rule_runtime.rule_revisions WHERE tenant_id=$1::uuid`
	args := []any{tenantID}
	if ruleID != "" {
		if !isUUIDv7(ruleID) {
			return nil, ErrInvalidIdentity
		}
		query += ` AND rule_id=$2::uuid`
		args = append(args, ruleID)
	}
	query += ` ORDER BY released_at DESC, revision DESC LIMIT 200`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ruleruntime.RuleRevision{}
	for rows.Next() {
		var content []byte
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		var revision ruleruntime.RuleRevision
		if err := json.Unmarshal(content, &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, rows.Err()
}

func (s *PostgresStore) AppendBinding(ctx context.Context, tenantID string, request AssignmentRequest, at time.Time) (BindingView, error) {
	if !isUUIDv7(request.SiteID) || !isUUIDv7(request.RuleRevisionID) {
		return BindingView{}, ErrInvalidIdentity
	}
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return BindingView{}, err
	}
	defer tx.Rollback(ctx)
	var requestedRuleID string
	if err := tx.QueryRow(ctx, `SELECT rule_id::text FROM rule_runtime.rule_revisions WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, request.RuleRevisionID).Scan(&requestedRuleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BindingView{}, ErrNotFound
		}
		return BindingView{}, err
	}
	bindingID := strings.TrimSpace(request.BindingID)
	if bindingID == "" {
		bindingID, err = newUUIDv7(at)
		if err != nil {
			return BindingView{}, err
		}
	} else if !isUUIDv7(bindingID) {
		return BindingView{}, ErrInvalidIdentity
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+bindingID); err != nil {
		return BindingView{}, err
	}
	var head bindingStreamHead
	err = tx.QueryRow(ctx, `SELECT b.site_id::text,rr.rule_id::text,b.revision
FROM rule_runtime.rule_bindings b
JOIN rule_runtime.rule_revisions rr ON rr.tenant_id=b.tenant_id AND rr.id=b.rule_revision_id
WHERE b.tenant_id=$1::uuid AND b.binding_id=$2::uuid
ORDER BY b.revision DESC LIMIT 1`, tenantID, bindingID).Scan(&head.SiteID, &head.RuleID, &head.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BindingView{}, err
	}
	var existing *bindingStreamHead
	if err == nil {
		existing = &head
	}
	revision, err := nextBindingRevision(existing, request.SiteID, requestedRuleID)
	if err != nil {
		return BindingView{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rule_runtime.rule_bindings(binding_id,tenant_id,site_id,revision,rule_revision_id,priority,created_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7)`, bindingID, tenantID, request.SiteID, revision, request.RuleRevisionID, request.Priority, at.UTC()); err != nil {
		return BindingView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BindingView{}, err
	}
	return BindingView{RuleBinding: ruleruntime.RuleBinding{ID: bindingID, TenantID: tenantID, SiteID: request.SiteID, Revision: revision, RuleRevisionID: request.RuleRevisionID, Priority: request.Priority}, Active: true, CreatedAt: at.UTC()}, nil
}

func (s *PostgresStore) ListBindings(ctx context.Context, tenantID, siteID string) ([]BindingView, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query := `WITH latest AS (
 SELECT DISTINCT ON (binding_id) binding_id,tenant_id,site_id,revision,rule_revision_id,priority,created_at
 FROM rule_runtime.rule_bindings WHERE tenant_id=$1::uuid`
	args := []any{tenantID}
	if siteID != "" {
		if !isUUIDv7(siteID) {
			return nil, ErrInvalidIdentity
		}
		query += ` AND site_id=$2::uuid`
		args = append(args, siteID)
	}
	query += ` ORDER BY binding_id,revision DESC
) SELECT latest.binding_id::text,latest.tenant_id::text,COALESCE(latest.site_id::text,''),latest.revision,latest.rule_revision_id::text,latest.priority,latest.created_at,
 retirement.retired_at
FROM latest LEFT JOIN rule_runtime.rule_binding_retirements retirement
 ON retirement.tenant_id=latest.tenant_id AND retirement.binding_id=latest.binding_id AND retirement.binding_revision=latest.revision
ORDER BY latest.created_at DESC LIMIT 200`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BindingView{}
	for rows.Next() {
		var view BindingView
		var retiredAt *time.Time
		if err := rows.Scan(&view.ID, &view.TenantID, &view.SiteID, &view.Revision, &view.RuleRevisionID, &view.Priority, &view.CreatedAt, &retiredAt); err != nil {
			return nil, err
		}
		view.RetiredAt = retiredAt
		view.Active = retiredAt == nil
		result = append(result, view)
	}
	return result, rows.Err()
}

func (s *PostgresStore) RetireBinding(ctx context.Context, tenantID, bindingID, siteID, reason string, at time.Time) (BindingView, error) {
	if !isUUIDv7(bindingID) || !isUUIDv7(siteID) {
		return BindingView{}, ErrInvalidIdentity
	}
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return BindingView{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+bindingID); err != nil {
		return BindingView{}, err
	}
	var view BindingView
	if err := tx.QueryRow(ctx, `SELECT binding_id::text,tenant_id::text,COALESCE(site_id::text,''),revision,rule_revision_id::text,priority,created_at
FROM rule_runtime.rule_bindings WHERE tenant_id=$1::uuid AND binding_id=$2::uuid AND site_id=$3::uuid ORDER BY revision DESC LIMIT 1`, tenantID, bindingID, siteID).Scan(&view.ID, &view.TenantID, &view.SiteID, &view.Revision, &view.RuleRevisionID, &view.Priority, &view.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BindingView{}, ErrNotFound
		}
		return BindingView{}, err
	}
	retirementID, err := newUUIDv7(at)
	if err != nil {
		return BindingView{}, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO rule_runtime.rule_binding_retirements(id,tenant_id,binding_id,binding_revision,reason,retired_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6) ON CONFLICT (tenant_id,binding_id,binding_revision) DO NOTHING`, retirementID, tenantID, bindingID, view.Revision, reason, at.UTC())
	if err != nil {
		return BindingView{}, err
	}
	if command.RowsAffected() != 1 {
		return BindingView{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return BindingView{}, err
	}
	view.Active = false
	retiredAt := at.UTC()
	view.RetiredAt = &retiredAt
	return view, nil
}

func (s *PostgresStore) ListExecutionEvidence(ctx context.Context, tenantID, siteID string, limit int) ([]ExecutionEvidence, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query := `SELECT execution_id,COALESCE(site_id::text,''),rule_revision_id::text,binding_id::text,binding_revision,status,COALESCE(terminal_code,''),snapshot,updated_at
FROM rule_runtime.executions WHERE tenant_id=$1::uuid`
	args := []any{tenantID}
	argLimit := 2
	if siteID != "" {
		if !isUUIDv7(siteID) {
			return nil, ErrInvalidIdentity
		}
		query += ` AND site_id=$2::uuid`
		args = append(args, siteID)
		argLimit = 3
	}
	query += fmt.Sprintf(` ORDER BY updated_at DESC,execution_id DESC LIMIT $%d`, argLimit)
	args = append(args, limit)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ExecutionEvidence{}
	for rows.Next() {
		var evidence ExecutionEvidence
		var status string
		var snapshotBytes []byte
		if err := rows.Scan(&evidence.ExecutionID, &evidence.SiteID, &evidence.RuleRevisionID, &evidence.BindingID, &evidence.BindingRevision, &status, &evidence.TerminalCode, &snapshotBytes, &evidence.UpdatedAt); err != nil {
			return nil, err
		}
		var snapshot ruleruntime.ExecutionSnapshot
		if err := json.Unmarshal(snapshotBytes, &snapshot); err != nil {
			return nil, err
		}
		evidence.Status = ruleruntime.ExecutionStatus(status)
		evidence.Trace = snapshot.Trace
		evidence.Effects = snapshot.Effects
		result = append(result, evidence)
	}
	return result, rows.Err()
}

func (s *PostgresStore) tenantTx(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if s == nil || s.pool == nil || !isUUIDv7(tenantID) {
		return nil, ErrInvalidIdentity
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func newUUIDv7(now time.Time) (string, error) {
	millis := now.UTC().UnixMilli()
	if millis < 0 || millis > (1<<48)-1 {
		return "", errors.New("UUIDv7 timestamp is out of range")
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return value[19] == '8' || value[19] == '9' || value[19] == 'a' || value[19] == 'b'
}
