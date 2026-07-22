package iam

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const iamReconcilerDatabaseRole = "s1_iam_reconciler"

type PostgresReconciliationStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func OpenPostgresReconciliationStore(ctx context.Context, databaseURL string) (*PostgresReconciliationStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("IAM reconciler database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse IAM reconciler database configuration: %w", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "iam-reconciler"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10s"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "2s"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10s"
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		var role string
		if err := connection.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
			return fmt.Errorf("read IAM reconciler database role: %w", err)
		}
		if role != iamReconcilerDatabaseRole {
			return fmt.Errorf("IAM reconciler database role %q is not allowed", role)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open IAM reconciliation store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping IAM reconciliation store: %w", err)
	}
	return &PostgresReconciliationStore{pool: pool, now: time.Now}, nil
}

func (store *PostgresReconciliationStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresReconciliationStore) Reconcile(ctx context.Context, input ReconciliationRequest) (ReconciliationResult, error) {
	if store == nil || store.pool == nil {
		return ReconciliationResult{}, errors.New("IAM reconciliation store is closed")
	}
	request, inputHash, err := prepareReconciliationRequest(input)
	if err != nil {
		return ReconciliationResult{}, err
	}
	now := store.now().UTC()
	eventID, err := newUUIDv7(now)
	if err != nil {
		return ReconciliationResult{}, err
	}
	result := ReconciliationResult{
		EventID:       eventID,
		PrincipalID:   request.Principal.ID,
		SourceVersion: request.SourceVersion,
		InputHash:     inputHash,
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("begin IAM reconciliation: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := lockReconciliationKeys(ctx, transaction, request); err != nil {
		return ReconciliationResult{}, err
	}
	current, found, err := loadReconciliationState(ctx, transaction, request.SourceSystem, request.SourceKey)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if found {
		switch {
		case request.SourceVersion < current.version:
			return quarantineReconciliation(ctx, transaction, request, result, ReasonStaleSourceVersion, current, now)
		case request.SourceVersion == current.version && inputHash == current.inputHash:
			result.Status = ReconciliationNoChange
			result.ReasonCode = ReasonInputUnchanged
			if err := insertReconciliationEvent(ctx, transaction, request, result, now); err != nil {
				return ReconciliationResult{}, err
			}
			if err := transaction.Commit(ctx); err != nil {
				return ReconciliationResult{}, fmt.Errorf("commit unchanged IAM reconciliation: %w", err)
			}
			return result, nil
		case request.SourceVersion == current.version:
			return quarantineReconciliation(ctx, transaction, request, result, ReasonSourceVersionConflict, current, now)
		case current.principalID != request.Principal.ID:
			return quarantineReconciliation(ctx, transaction, request, result, ReasonSourcePrincipalConflict, current, now)
		}
	}
	otherSource, otherSourceFound, err := loadPrincipalReconciliationState(ctx, transaction, request.Principal.ID, request.SourceSystem, request.SourceKey)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if otherSourceFound {
		return quarantineReconciliation(ctx, transaction, request, result, ReasonSourcePrincipalConflict, otherSource, now)
	}

	identityPrincipalID, err := findPrincipalByIdentity(ctx, transaction, request.Principal.SubjectIssuer, request.Principal.Subject)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if identityPrincipalID != "" && identityPrincipalID != request.Principal.ID {
		conflict, err := loadAnyPrincipalReconciliationState(ctx, transaction, identityPrincipalID)
		if err != nil {
			return ReconciliationResult{}, err
		}
		return quarantineReconciliation(ctx, transaction, request, result, ReasonImmutableIdentityConflict, conflict, now)
	}
	issuer, subject, principalFound, err := findPrincipalIdentityByID(ctx, transaction, request.Principal.ID)
	if err != nil {
		return ReconciliationResult{}, err
	}
	if principalFound && (issuer != request.Principal.SubjectIssuer || subject != request.Principal.Subject) {
		conflict, err := loadAnyPrincipalReconciliationState(ctx, transaction, request.Principal.ID)
		if err != nil {
			return ReconciliationResult{}, err
		}
		return quarantineReconciliation(ctx, transaction, request, result, ReasonPrincipalIdentifierConflict, conflict, now)
	}

	if err := upsertReconciledPrincipal(ctx, transaction, request.Principal, now); err != nil {
		return ReconciliationResult{}, err
	}
	if err := replaceReconciledFacts(ctx, transaction, request, now); err != nil {
		return ReconciliationResult{}, err
	}
	if err := upsertReconciliationState(ctx, transaction, request, inputHash, now); err != nil {
		return ReconciliationResult{}, err
	}
	result.Status = ReconciliationApplied
	result.ReasonCode = ReasonReconciliationApplied
	if err := insertReconciliationEvent(ctx, transaction, request, result, now); err != nil {
		return ReconciliationResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ReconciliationResult{}, fmt.Errorf("commit IAM reconciliation: %w", err)
	}
	return result, nil
}

type reconciliationState struct {
	sourceSystem string
	sourceKey    string
	version      int64
	inputHash    string
	principalID  string
}

func lockReconciliationKeys(ctx context.Context, transaction pgx.Tx, request ReconciliationRequest) error {
	keys := []string{
		reconciliationLockKey("identity", request.Principal.SubjectIssuer, request.Principal.Subject),
		reconciliationLockKey("principal", request.Principal.ID),
		reconciliationLockKey("source", request.SourceSystem, request.SourceKey),
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return fmt.Errorf("lock IAM reconciliation key: %w", err)
		}
	}
	return nil
}

func reconciliationLockKey(namespace string, parts ...string) string {
	digest := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(part))
	}
	return "iam-reconciliation-" + namespace + ":" + hex.EncodeToString(digest.Sum(nil))
}

func loadReconciliationState(ctx context.Context, transaction pgx.Tx, sourceSystem, sourceKey string) (reconciliationState, bool, error) {
	var state reconciliationState
	err := transaction.QueryRow(ctx, `
SELECT source_system, source_key, source_version, input_hash, principal_id::text
FROM iam.reconciliation_state
WHERE source_system = $1 AND source_key = $2
FOR UPDATE
`, sourceSystem, sourceKey).Scan(&state.sourceSystem, &state.sourceKey, &state.version, &state.inputHash, &state.principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return reconciliationState{}, false, nil
	}
	if err != nil {
		return reconciliationState{}, false, fmt.Errorf("load IAM reconciliation state: %w", err)
	}
	return state, true, nil
}

func loadPrincipalReconciliationState(ctx context.Context, transaction pgx.Tx, principalID, sourceSystem, sourceKey string) (reconciliationState, bool, error) {
	var state reconciliationState
	err := transaction.QueryRow(ctx, `
SELECT source_system, source_key, source_version, input_hash, principal_id::text
FROM iam.reconciliation_state
WHERE principal_id = $1::uuid
  AND (source_system, source_key) <> ($2, $3)
FOR UPDATE
`, principalID, sourceSystem, sourceKey).Scan(&state.sourceSystem, &state.sourceKey, &state.version, &state.inputHash, &state.principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return reconciliationState{}, false, nil
	}
	if err != nil {
		return reconciliationState{}, false, fmt.Errorf("load IAM principal reconciliation state: %w", err)
	}
	return state, true, nil
}

func loadAnyPrincipalReconciliationState(ctx context.Context, transaction pgx.Tx, principalID string) (reconciliationState, error) {
	state := reconciliationState{principalID: principalID}
	err := transaction.QueryRow(ctx, `
SELECT source_system, source_key, source_version, input_hash
FROM iam.reconciliation_state
WHERE principal_id = $1::uuid
FOR UPDATE
`, principalID).Scan(&state.sourceSystem, &state.sourceKey, &state.version, &state.inputHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return reconciliationState{}, fmt.Errorf("load existing IAM principal reconciliation state: %w", err)
	}
	return state, nil
}

func findPrincipalByIdentity(ctx context.Context, transaction pgx.Tx, issuer, subject string) (string, error) {
	var principalID string
	err := transaction.QueryRow(ctx, `
SELECT id::text FROM iam.principals
WHERE external_issuer = $1 AND external_subject = $2
`, issuer, subject).Scan(&principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find IAM principal by immutable identity: %w", err)
	}
	return principalID, nil
}

func findPrincipalIdentityByID(ctx context.Context, transaction pgx.Tx, principalID string) (string, string, bool, error) {
	var issuer, subject string
	err := transaction.QueryRow(ctx, `
SELECT external_issuer, external_subject FROM iam.principals WHERE id = $1::uuid
`, principalID).Scan(&issuer, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("find IAM principal identity by id: %w", err)
	}
	return issuer, subject, true, nil
}

func upsertReconciledPrincipal(ctx context.Context, transaction pgx.Tx, principal ReconciledPrincipal, now time.Time) error {
	_, err := transaction.Exec(ctx, `
INSERT INTO iam.principals (
  id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6, 1, $7, $7)
ON CONFLICT (id) DO UPDATE SET
  display_name = EXCLUDED.display_name,
  email = EXCLUDED.email,
  status = EXCLUDED.status,
  revision = iam.principals.revision + 1,
  updated_at = EXCLUDED.updated_at
`, principal.ID, principal.SubjectIssuer, principal.Subject, principal.DisplayName, principal.Email, principal.Status, now)
	if err != nil {
		return fmt.Errorf("upsert reconciled IAM principal: %w", err)
	}
	return nil
}

func replaceReconciledFacts(ctx context.Context, transaction pgx.Tx, request ReconciliationRequest, now time.Time) error {
	principalID := request.Principal.ID
	for _, table := range []string{"explicit_denies", "site_bindings", "role_bindings", "organization_memberships"} {
		if _, err := transaction.Exec(ctx, `DELETE FROM iam.`+table+` WHERE principal_id = $1::uuid`, principalID); err != nil {
			return fmt.Errorf("clear reconciled IAM %s: %w", table, err)
		}
	}
	for _, membership := range request.Memberships {
		id, err := newUUIDv7(now)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `
INSERT INTO iam.organization_memberships (
  id, organization_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $8)
`, id, membership.OrganizationID, principalID, membership.Status, membership.ValidFrom, membership.ValidTo, request.SourceVersion, now); err != nil {
			return fmt.Errorf("insert reconciled IAM membership: %w", err)
		}
	}
	for _, binding := range request.RoleBindings {
		id, err := newUUIDv7(now)
		if err != nil {
			return err
		}
		actions := make([]string, len(binding.Actions))
		for index, action := range binding.Actions {
			actions[index] = string(action)
		}
		if _, err := transaction.Exec(ctx, `
INSERT INTO iam.role_bindings (
  id, organization_id, principal_id, role_key, actions, effect, valid_from, valid_to, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $10)
`, id, binding.OrganizationID, principalID, binding.RoleKey, actions, binding.Effect, binding.ValidFrom, binding.ValidTo, request.SourceVersion, now); err != nil {
			return fmt.Errorf("insert reconciled IAM role binding: %w", err)
		}
	}
	for _, binding := range request.SiteBindings {
		id, err := newUUIDv7(now)
		if err != nil {
			return err
		}
		actions := make([]string, len(binding.Actions))
		for index, action := range binding.Actions {
			actions[index] = string(action)
		}
		if _, err := transaction.Exec(ctx, `
INSERT INTO iam.site_bindings (
  id, acting_organization_id, owning_organization_id, site_id, principal_id, actions, effect,
  valid_from, valid_to, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, $11)
`, id, binding.ActingOrganizationID, binding.OwningOrganizationID, binding.SiteID, principalID, actions, binding.Effect, binding.ValidFrom, binding.ValidTo, request.SourceVersion, now); err != nil {
			return fmt.Errorf("insert reconciled IAM site binding: %w", err)
		}
	}
	for _, deny := range request.ExplicitDenies {
		id, err := newUUIDv7(now)
		if err != nil {
			return err
		}
		var siteID any
		if deny.SiteID != "" {
			siteID = deny.SiteID
		}
		if _, err := transaction.Exec(ctx, `
INSERT INTO iam.explicit_denies (
  id, acting_organization_id, owning_organization_id, site_id, principal_id, action,
  reason_code, valid_from, valid_to, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, $11)
`, id, deny.ActingOrganizationID, deny.OwningOrganizationID, siteID, principalID, deny.Action, deny.ReasonCode, deny.ValidFrom, deny.ValidTo, request.SourceVersion, now); err != nil {
			return fmt.Errorf("insert reconciled IAM explicit deny: %w", err)
		}
	}
	return nil
}

func upsertReconciliationState(ctx context.Context, transaction pgx.Tx, request ReconciliationRequest, inputHash string, now time.Time) error {
	_, err := transaction.Exec(ctx, `
INSERT INTO iam.reconciliation_state (
  source_system, source_key, source_version, input_hash, principal_id, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5::uuid, $6, $6)
ON CONFLICT (source_system, source_key) DO UPDATE SET
  source_version = EXCLUDED.source_version,
  input_hash = EXCLUDED.input_hash,
  principal_id = EXCLUDED.principal_id,
  updated_at = EXCLUDED.updated_at
`, request.SourceSystem, request.SourceKey, request.SourceVersion, inputHash, request.Principal.ID, now)
	if err != nil {
		return fmt.Errorf("upsert IAM reconciliation state: %w", err)
	}
	return nil
}

func insertReconciliationEvent(ctx context.Context, transaction pgx.Tx, request ReconciliationRequest, result ReconciliationResult, now time.Time) error {
	_, err := transaction.Exec(ctx, `
INSERT INTO iam.reconciliation_events (
  id, source_system, source_key, source_version, input_hash, principal_id, status, reason_code,
  membership_count, role_binding_count, site_binding_count, explicit_deny_count, created_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8, $9, $10, $11, $12, $13)
`, result.EventID, request.SourceSystem, request.SourceKey, request.SourceVersion, result.InputHash,
		request.Principal.ID, result.Status, result.ReasonCode, len(request.Memberships), len(request.RoleBindings),
		len(request.SiteBindings), len(request.ExplicitDenies), now)
	if err != nil {
		return fmt.Errorf("insert IAM reconciliation audit event: %w", err)
	}
	return nil
}

func quarantineReconciliation(ctx context.Context, transaction pgx.Tx, request ReconciliationRequest, result ReconciliationResult, reason ReconciliationReason, current reconciliationState, now time.Time) (ReconciliationResult, error) {
	result.Status = ReconciliationQuarantined
	result.ReasonCode = reason
	if err := insertReconciliationEvent(ctx, transaction, request, result, now); err != nil {
		return ReconciliationResult{}, err
	}
	quarantineID, err := newUUIDv7(now)
	if err != nil {
		return ReconciliationResult{}, err
	}
	_, err = transaction.Exec(ctx, `
INSERT INTO iam.reconciliation_quarantine (
  id, reconciliation_event_id, source_system, source_key, source_version, input_hash,
  current_source_system, current_source_key, current_source_version, current_input_hash,
  requested_principal_id, current_principal_id, reason_code, quarantine_status, created_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''),
  NULLIF($9, 0), NULLIF($10, ''), $11::uuid, NULLIF($12, '')::uuid, $13, 'OPEN', $14)
`, quarantineID, result.EventID, request.SourceSystem, request.SourceKey, request.SourceVersion, result.InputHash,
		current.sourceSystem, current.sourceKey, current.version, current.inputHash,
		request.Principal.ID, current.principalID, reason, now)
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("insert IAM reconciliation quarantine: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return ReconciliationResult{}, fmt.Errorf("commit quarantined IAM reconciliation: %w", err)
	}
	return result, nil
}

var _ ReconciliationStore = (*PostgresReconciliationStore)(nil)
