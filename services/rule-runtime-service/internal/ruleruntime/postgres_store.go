package ruleruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("rule runtime PostgreSQL pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) PutReleasedPlan(ctx context.Context, plan ExecutionPlan, releasedAt time.Time) error {
	if plan.Revision.State != RevisionReleased || plan.Digest == "" || plan.Revision.Digest != plan.Digest {
		return errors.New("only compiled released rule revisions may be persisted")
	}
	content, err := json.Marshal(plan.Revision)
	if err != nil {
		return fmt.Errorf("marshal released rule revision: %w", err)
	}
	command, err := s.pool.Exec(ctx, `INSERT INTO rule_runtime.rule_revisions
(id,tenant_id,rule_id,revision,catalog_version,content,content_digest,released_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::jsonb,$7,$8)
ON CONFLICT (id) DO NOTHING`,
		plan.Revision.ID, plan.Revision.TenantID, plan.Revision.RuleID, plan.Revision.Revision,
		plan.Revision.CatalogVersion, content, plan.Digest, releasedAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() == 1 {
		return nil
	}
	var existingDigest string
	if err := s.pool.QueryRow(ctx, `SELECT content_digest FROM rule_runtime.rule_revisions WHERE id=$1::uuid`, plan.Revision.ID).Scan(&existingDigest); err != nil {
		return err
	}
	if existingDigest != plan.Digest {
		return ErrRuleRevisionIdentity
	}
	return nil
}

func (s *PostgresStore) LoadReleasedPlan(ctx context.Context, ruleRevisionID string, catalog Catalog) (ExecutionPlan, error) {
	var content []byte
	var storedDigest string
	var catalogVersion string
	if err := s.pool.QueryRow(ctx, `SELECT content, content_digest, catalog_version FROM rule_runtime.rule_revisions WHERE id=$1::uuid`, ruleRevisionID).Scan(&content, &storedDigest, &catalogVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionPlan{}, ErrExecutionNotFound
		}
		return ExecutionPlan{}, err
	}
	if catalogVersion != catalog.Version {
		return ExecutionPlan{}, fmt.Errorf("stored rule catalog %q does not match runtime catalog %q", catalogVersion, catalog.Version)
	}
	var revision RuleRevision
	if err := json.Unmarshal(content, &revision); err != nil {
		return ExecutionPlan{}, fmt.Errorf("decode released rule revision: %w", err)
	}
	if revision.ID != ruleRevisionID || revision.State != RevisionReleased {
		return ExecutionPlan{}, ErrRuleRevisionIdentity
	}
	plan, err := Compile(revision, catalog)
	if err != nil {
		return ExecutionPlan{}, err
	}
	if plan.Digest != storedDigest {
		return ExecutionPlan{}, ErrRuleRevisionIdentity
	}
	return plan, nil
}

func (s *PostgresStore) AppendBinding(ctx context.Context, binding RuleBinding, createdAt time.Time) error {
	if binding.Revision <= 0 {
		return errors.New("binding revision must be positive")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO rule_runtime.rule_bindings
(binding_id,tenant_id,site_id,revision,rule_revision_id,priority,created_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7)`,
		binding.ID, binding.TenantID, nullableUUID(binding.SiteID), binding.Revision, binding.RuleRevisionID, binding.Priority, createdAt.UTC())
	return err
}

func (s *PostgresStore) CreateOrLoad(ctx context.Context, seed ExecutionSnapshot) (ExecutionSnapshot, bool, error) {
	payload, err := json.Marshal(seed)
	if err != nil {
		return ExecutionSnapshot{}, false, fmt.Errorf("marshal execution snapshot: %w", err)
	}
	command, err := s.pool.Exec(ctx, `INSERT INTO rule_runtime.executions
(execution_id,tenant_id,site_id,rule_revision_id,binding_id,binding_revision,event_id,ordering_key,status,attempt_budget,lease_fence,terminal_code,rule_digest,snapshot,created_at,updated_at)
VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,0,NULL,$11,$12::jsonb,$13,$13)
ON CONFLICT (execution_id) DO NOTHING`,
		seed.Execution.ExecutionID, seed.Execution.TenantID, nullableUUID(seed.Execution.SiteID), seed.Execution.RuleRevisionID,
		seed.Execution.BindingID, seed.Execution.BindingRevision, seed.Execution.EventID, seed.Execution.OrderingKey,
		string(seed.Execution.Status), seed.Execution.AttemptBudget, seed.RuleDigest, payload, seed.Execution.CreatedAt.UTC())
	if err != nil {
		return ExecutionSnapshot{}, false, err
	}
	if command.RowsAffected() == 1 {
		return seed, true, nil
	}
	existing, err := s.Load(ctx, seed.Execution.ExecutionID)
	if err != nil {
		return ExecutionSnapshot{}, false, err
	}
	if !sameExecutionIdentity(existing, seed) {
		return ExecutionSnapshot{}, false, ErrExecutionIdentity
	}
	return existing, false, nil
}

func (s *PostgresStore) Load(ctx context.Context, executionID string) (ExecutionSnapshot, error) {
	var payload []byte
	if err := s.pool.QueryRow(ctx, `SELECT snapshot FROM rule_runtime.executions WHERE execution_id=$1`, executionID).Scan(&payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionSnapshot{}, ErrExecutionNotFound
		}
		return ExecutionSnapshot{}, err
	}
	return decodeSnapshot(payload)
}

func (s *PostgresStore) ReadRuleState(ctx context.Context, key RuleStateKey) (RuleStateRecord, bool, error) {
	var state RuleStateRecord
	var value []byte
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT schema_version,revision,value,expires_at FROM rule_runtime.rule_states
WHERE tenant_id=$1::uuid AND rule_revision_id=$2::uuid AND node_instance_id=$3 AND scope_key=$4`,
		key.TenantID, key.RuleRevisionID, key.NodeInstanceID, key.ScopeKey).Scan(&state.SchemaVersion, &state.Revision, &value, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleStateRecord{}, false, nil
	}
	if err != nil {
		return RuleStateRecord{}, false, err
	}
	state.TenantID = key.TenantID
	state.RuleRevisionID = key.RuleRevisionID
	state.NodeInstanceID = key.NodeInstanceID
	state.ScopeKey = key.ScopeKey
	state.Value = append(json.RawMessage(nil), value...)
	state.ExpiresAt = expiresAt
	return state, true, nil
}

func (s *PostgresStore) Claim(ctx context.Context, executionID, owner string, now time.Time, ttl time.Duration) (ExecutionSnapshot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	defer tx.Rollback(ctx)

	var payload []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM rule_runtime.executions WHERE execution_id=$1 FOR UPDATE`, executionID).Scan(&payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionSnapshot{}, ErrExecutionNotFound
		}
		return ExecutionSnapshot{}, err
	}
	snapshot, err := decodeSnapshot(payload)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	if terminal(snapshot.Execution.Status) {
		return snapshot, tx.Commit(ctx)
	}
	if snapshot.Execution.LeaseOwner != "" && snapshot.Execution.LeaseOwner != owner && snapshot.Execution.LeaseUntil.After(now) {
		return ExecutionSnapshot{}, ErrLeaseConflict
	}

	if _, err := tx.Exec(ctx, `INSERT INTO rule_runtime.ordering_locks (tenant_id,ordering_key,execution_id,updated_at)
VALUES ($1::uuid,$2,$3,$4) ON CONFLICT (tenant_id,ordering_key) DO NOTHING`, snapshot.Execution.TenantID, snapshot.Execution.OrderingKey, executionID, now.UTC()); err != nil {
		return ExecutionSnapshot{}, err
	}
	var holder string
	if err := tx.QueryRow(ctx, `SELECT execution_id FROM rule_runtime.ordering_locks
WHERE tenant_id=$1::uuid AND ordering_key=$2 FOR UPDATE`, snapshot.Execution.TenantID, snapshot.Execution.OrderingKey).Scan(&holder); err != nil {
		return ExecutionSnapshot{}, err
	}
	if holder != executionID {
		var holderStatus ExecutionStatus
		if err := tx.QueryRow(ctx, `SELECT status FROM rule_runtime.executions WHERE execution_id=$1`, holder).Scan(&holderStatus); err != nil {
			return ExecutionSnapshot{}, err
		}
		if !terminal(holderStatus) {
			return ExecutionSnapshot{}, ErrLeaseConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE rule_runtime.ordering_locks SET execution_id=$3,updated_at=$4
WHERE tenant_id=$1::uuid AND ordering_key=$2`, snapshot.Execution.TenantID, snapshot.Execution.OrderingKey, executionID, now.UTC()); err != nil {
			return ExecutionSnapshot{}, err
		}
	}

	snapshot.Execution.LeaseFence++
	snapshot.Execution.LeaseOwner = owner
	snapshot.Execution.LeaseUntil = now.Add(ttl)
	snapshot.Execution.Status = normalizeActiveStatus(snapshot)
	snapshot.Execution.UpdatedAt = now
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ExecutionSnapshot{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE rule_runtime.executions
SET status=$2,lease_owner=$3,lease_until=$4,lease_fence=$5,snapshot=$6::jsonb,updated_at=$7
WHERE execution_id=$1`, executionID, string(snapshot.Execution.Status), owner, snapshot.Execution.LeaseUntil.UTC(), snapshot.Execution.LeaseFence, encoded, now.UTC()); err != nil {
		return ExecutionSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionSnapshot{}, err
	}
	return snapshot, nil
}

func (s *PostgresStore) Save(ctx context.Context, snapshot ExecutionSnapshot, owner string, fence int64, now time.Time) error {
	if snapshot.Execution.LeaseOwner != owner || snapshot.Execution.LeaseFence != fence {
		return ErrFenceStale
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal execution snapshot: %w", err)
	}
	command, err := s.pool.Exec(ctx, `UPDATE rule_runtime.executions
SET status=$2,terminal_code=$3,snapshot=$4::jsonb,updated_at=$5
WHERE execution_id=$1 AND lease_owner=$6 AND lease_fence=$7 AND lease_until >= $5`,
		snapshot.Execution.ExecutionID, string(snapshot.Execution.Status), nullableText(snapshot.Execution.TerminalCode), payload, now.UTC(), owner, fence)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenceStale
	}
	return nil
}

func (s *PostgresStore) SaveWithStateCAS(ctx context.Context, snapshot ExecutionSnapshot, owner string, fence int64, now time.Time, key RuleStateKey, transition StateTransition) (RuleStateRecord, error) {
	if snapshot.Execution.LeaseOwner != owner || snapshot.Execution.LeaseFence != fence || key.TenantID != snapshot.Execution.TenantID || key.RuleRevisionID != snapshot.Execution.RuleRevisionID || transition.ScopeKey != key.ScopeKey || !json.Valid(transition.Value) {
		return RuleStateRecord{}, ErrStateCASConflict
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return RuleStateRecord{}, fmt.Errorf("marshal execution snapshot: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuleStateRecord{}, err
	}
	defer tx.Rollback(ctx)
	var leaseOK int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM rule_runtime.executions
WHERE execution_id=$1 AND lease_owner=$2 AND lease_fence=$3 AND lease_until >= $4 FOR UPDATE`,
		snapshot.Execution.ExecutionID, owner, fence, now.UTC()).Scan(&leaseOK); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuleStateRecord{}, ErrFenceStale
		}
		return RuleStateRecord{}, err
	}

	var resultRevision int64
	if transition.ExpectedRevision == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO rule_runtime.rule_states
(tenant_id,rule_revision_id,node_instance_id,scope_key,schema_version,revision,value,expires_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3,$4,$5,1,$6::jsonb,$7,$8)
ON CONFLICT (tenant_id,rule_revision_id,node_instance_id,scope_key) DO NOTHING
RETURNING revision`,
			key.TenantID, key.RuleRevisionID, key.NodeInstanceID, key.ScopeKey, transition.SchemaVersion, transition.Value, transition.ExpiresAt, now.UTC()).Scan(&resultRevision)
	} else {
		err = tx.QueryRow(ctx, `UPDATE rule_runtime.rule_states
SET revision=revision+1,value=$6::jsonb,expires_at=$7,updated_at=$8
WHERE tenant_id=$1::uuid AND rule_revision_id=$2::uuid AND node_instance_id=$3 AND scope_key=$4
  AND schema_version=$5 AND revision=$9
RETURNING revision`,
			key.TenantID, key.RuleRevisionID, key.NodeInstanceID, key.ScopeKey, transition.SchemaVersion, transition.Value, transition.ExpiresAt, now.UTC(), transition.ExpectedRevision).Scan(&resultRevision)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleStateRecord{}, ErrStateCASConflict
	}
	if err != nil {
		return RuleStateRecord{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE rule_runtime.executions
SET status=$2,terminal_code=$3,snapshot=$4::jsonb,updated_at=$5
WHERE execution_id=$1 AND lease_owner=$6 AND lease_fence=$7`,
		snapshot.Execution.ExecutionID, string(snapshot.Execution.Status), nullableText(snapshot.Execution.TerminalCode), payload, now.UTC(), owner, fence); err != nil {
		return RuleStateRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleStateRecord{}, err
	}
	return RuleStateRecord{
		TenantID: key.TenantID, RuleRevisionID: key.RuleRevisionID, NodeInstanceID: key.NodeInstanceID, ScopeKey: key.ScopeKey,
		SchemaVersion: transition.SchemaVersion, Revision: resultRevision, Value: append(json.RawMessage(nil), transition.Value...), ExpiresAt: transition.ExpiresAt,
	}, nil
}

func (s *PostgresStore) Release(ctx context.Context, executionID, owner string, fence int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var payload []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM rule_runtime.executions
WHERE execution_id=$1 AND lease_owner=$2 AND lease_fence=$3 FOR UPDATE`, executionID, owner, fence).Scan(&payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFenceStale
		}
		return err
	}
	snapshot, err := decodeSnapshot(payload)
	if err != nil {
		return err
	}
	snapshot.Execution.LeaseOwner = ""
	snapshot.Execution.LeaseUntil = time.Time{}
	snapshot.Execution.UpdatedAt = now
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE rule_runtime.executions
SET lease_owner=NULL,lease_until=NULL,snapshot=$4::jsonb,updated_at=$5
WHERE execution_id=$1 AND lease_owner=$2 AND lease_fence=$3`, executionID, owner, fence, encoded, now.UTC()); err != nil {
		return err
	}
	if terminal(snapshot.Execution.Status) {
		if _, err := tx.Exec(ctx, `DELETE FROM rule_runtime.ordering_locks
WHERE tenant_id=$1::uuid AND ordering_key=$2 AND execution_id=$3`, snapshot.Execution.TenantID, snapshot.Execution.OrderingKey, executionID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func decodeSnapshot(payload []byte) (ExecutionSnapshot, error) {
	var snapshot ExecutionSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("decode persisted execution snapshot: %w", err)
	}
	return snapshot, nil
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
