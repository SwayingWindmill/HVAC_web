package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

var (
	sha256Pattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	ErrEdgeFleetStale = errors.New("edge fleet revision is stale")
)

type EdgeNodeInput struct {
	ID                    string
	SiteID                string
	IntegrationInstanceID string
	ExternalID            string
	HardwareIdentityHash  string
}

type EdgeEnrollmentInput struct {
	ID                   string
	EdgeNodeID           string
	HardwareIdentityHash string
	ChallengeHash        string
	ExpiresAt            time.Time
}

type EdgeEnrollmentConsumeInput struct {
	EnrollmentID         string
	HardwareIdentityHash string
	ChallengeHash        string
	CredentialRefID      string
	IdentityBindingID    string
}

type EdgeHandshakeInput struct {
	ID         string
	EdgeNodeID string
	SessionID  string
	Request    edgefleet.HandshakeRequest
	Policy     edgefleet.HandshakePolicy
}

type DesiredEdgeStateInput struct {
	EdgeNodeID       string
	ReleaseID        string
	DesiredRevision  uint64
	SnapshotRevision uint64
}

type ObservedEdgeStateInput struct {
	EdgeNodeID             string
	ActiveReleaseID        string
	StagedReleaseID        string
	PreviousReleaseID      string
	ActiveSnapshotRevision uint64
	DesiredRevision        uint64
	DeliveryCursor         uint64
	ReportedConfigRevision uint64
	RuntimeVersion         string
	ProtocolSchemaVersion  int
	ManifestDigest         string
	Health                 string
	CapacityState          edgefleet.CapacityState
	DriftStatus            string
	DriftReason            string
	BacklogBytes           int64
	QuarantineCount        uint64
	LastSeenAt             time.Time
}

type EdgeDeliveryInput struct {
	DeliveryID     string
	EdgeNodeID     string
	OwnerDomain    edgefleet.OwnerDomain
	OrderingKey    string
	SourceRevision uint64
	PayloadDigest  string
	Payload        json.RawMessage
	Tombstone      bool
	Priority       string
}

type EdgeDeliveryResult struct {
	Cursor               uint64
	CommittedCursor      uint64
	Status               string
	AppliedOwnerRevision uint64
}

type OTAArtifactInput struct {
	ArtifactID string
	Artifact   edgefleet.SignedOTAArtifact
}

type OTACampaignInput struct {
	CampaignID  string
	ArtifactID  string
	Waves       []int
	WindowStart time.Time
	WindowEnd   time.Time
}

func (store *Store) RegisterEdgeNode(ctx context.Context, input EdgeNodeInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.ID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.SiteID)) ||
		!uuidV7Pattern.MatchString(strings.TrimSpace(input.IntegrationInstanceID)) || strings.TrimSpace(input.ExternalID) == "" || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash))) {
		return errors.New("invalid EdgeNode input")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
INSERT INTO connectivity.edge_nodes (
  id, tenant_id, site_id, integration_instance_id, edge_external_id, hardware_identity_sha256,
  status, revision, created_at, updated_at
)
SELECT $1::uuid, $2::uuid, i.site_id, i.id, $4, $5, 'SUSPENDED', 1, $6, $6
FROM connectivity.integration_instances i
WHERE i.tenant_id = $2::uuid AND i.id = $3::uuid AND i.site_id = $7::uuid AND i.status = 'ACTIVE'
`, strings.TrimSpace(input.ID), store.tenantID, strings.TrimSpace(input.IntegrationInstanceID), strings.TrimSpace(input.ExternalID),
		strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash)), now, strings.TrimSpace(input.SiteID))
	if err != nil {
		return fmt.Errorf("register EdgeNode: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_delivery_cursors (edge_node_id, tenant_id, committed_cursor, retained_floor, revision, updated_at)
VALUES ($1::uuid, $2::uuid, 0, 0, 1, $3)
`, strings.TrimSpace(input.ID), store.tenantID, now)
	if err != nil {
		return fmt.Errorf("initialize Edge delivery cursor: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) CreateEdgeEnrollment(ctx context.Context, input EdgeEnrollmentInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.ID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) ||
		!sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash))) || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.ChallengeHash))) || input.ExpiresAt.IsZero() {
		return errors.New("invalid Edge enrollment input")
	}
	now := store.clock().UTC()
	if !input.ExpiresAt.After(now) {
		return errors.New("Edge enrollment must expire in the future")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_enrollments (
  id, tenant_id, edge_node_id, hardware_identity_sha256, challenge_hash_sha256,
  expires_at, consumed_at, credential_ref_id, revision, created_at, updated_at
)
SELECT $1::uuid, $2::uuid, n.id, $4, $5, $6, NULL, NULL, 1, $7, $7
FROM connectivity.edge_nodes n
WHERE n.tenant_id = $2::uuid AND n.id = $3::uuid AND n.status IN ('SUSPENDED','ACTIVE','READ_ONLY','UPGRADE_REQUIRED')
`, strings.TrimSpace(input.ID), store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash)),
		strings.ToLower(strings.TrimSpace(input.ChallengeHash)), input.ExpiresAt.UTC(), now)
	if err != nil {
		return fmt.Errorf("create Edge enrollment: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) ConsumeEdgeEnrollment(ctx context.Context, input EdgeEnrollmentConsumeInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EnrollmentID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.CredentialRefID)) ||
		!uuidV7Pattern.MatchString(strings.TrimSpace(input.IdentityBindingID)) || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash))) ||
		!sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.ChallengeHash))) {
		return ErrEnrollmentInvalid
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var edgeNodeID string
	var identityRevision uint64
	err = tx.QueryRow(ctx, `
SELECT e.edge_node_id::text,
       COALESCE((SELECT max(b.identity_revision) + 1 FROM connectivity.edge_identity_bindings b WHERE b.tenant_id = e.tenant_id AND b.edge_node_id = e.edge_node_id), 1)
FROM connectivity.edge_enrollments e
JOIN connectivity.edge_nodes n ON n.tenant_id = e.tenant_id AND n.id = e.edge_node_id
JOIN connectivity.credential_refs c ON c.tenant_id = e.tenant_id AND c.id = $6::uuid AND c.integration_instance_id = n.integration_instance_id
WHERE e.tenant_id = $1::uuid AND e.id = $2::uuid AND e.consumed_at IS NULL AND e.expires_at > $3
  AND e.hardware_identity_sha256 = $4 AND e.challenge_hash_sha256 = $5
  AND c.status = 'ACTIVE' AND c.valid_from <= $3 AND c.valid_until > $3
FOR UPDATE OF e
`, store.tenantID, strings.TrimSpace(input.EnrollmentID), now, strings.ToLower(strings.TrimSpace(input.HardwareIdentityHash)),
		strings.ToLower(strings.TrimSpace(input.ChallengeHash)), strings.TrimSpace(input.CredentialRefID)).Scan(&edgeNodeID, &identityRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEnrollmentInvalid
	}
	if err != nil {
		return fmt.Errorf("load Edge enrollment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.edge_identity_bindings
SET status = 'ROTATED', valid_until = $3
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid AND status = 'ACTIVE' AND valid_until IS NULL
`, store.tenantID, edgeNodeID, now); err != nil {
		return fmt.Errorf("rotate prior Edge identity binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.edge_identity_bindings (
  id, tenant_id, edge_node_id, credential_ref_id, identity_revision, valid_from, valid_until, status, created_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, NULL, 'ACTIVE', $6)
`, strings.TrimSpace(input.IdentityBindingID), store.tenantID, edgeNodeID, strings.TrimSpace(input.CredentialRefID), identityRevision, now); err != nil {
		return fmt.Errorf("insert Edge identity binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.edge_enrollments
SET consumed_at = $3, credential_ref_id = $4::uuid, revision = revision + 1, updated_at = $3
WHERE tenant_id = $1::uuid AND id = $2::uuid AND consumed_at IS NULL
`, store.tenantID, strings.TrimSpace(input.EnrollmentID), now, strings.TrimSpace(input.CredentialRefID)); err != nil {
		return fmt.Errorf("consume Edge enrollment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.edge_nodes
SET status = 'ACTIVE', revision = revision + 1, updated_at = $3
WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'SUSPENDED'
`, store.tenantID, edgeNodeID, now); err != nil {
		return fmt.Errorf("activate enrolled EdgeNode: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) RecordEdgeHandshake(ctx context.Context, input EdgeHandshakeInput) (edgefleet.HandshakeResult, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.SessionID)) {
		return edgefleet.HandshakeResult{}, errors.New("invalid Edge handshake input")
	}
	now := store.clock().UTC()
	handshakeID := strings.TrimSpace(input.ID)
	if handshakeID == "" {
		var err error
		handshakeID, err = newUUIDv7(now)
		if err != nil {
			return edgefleet.HandshakeResult{}, err
		}
	} else if !uuidV7Pattern.MatchString(handshakeID) {
		return edgefleet.HandshakeResult{}, errors.New("invalid Edge handshake identity")
	}
	result := edgefleet.NegotiateHandshake(input.Request, input.Policy)
	capabilities, err := json.Marshal(input.Request.Capabilities)
	if err != nil {
		return edgefleet.HandshakeResult{}, err
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return edgefleet.HandshakeResult{}, err
	}
	defer tx.Rollback(ctx)
	var authorized bool
	err = tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM connectivity.edge_nodes n
  JOIN connectivity.edge_identity_bindings b ON b.tenant_id = n.tenant_id AND b.edge_node_id = n.id AND b.status = 'ACTIVE' AND b.valid_until IS NULL
  JOIN connectivity.sessions s ON s.tenant_id = n.tenant_id AND s.id = $3::uuid AND s.integration_instance_id = n.integration_instance_id
  WHERE n.tenant_id = $1::uuid AND n.id = $2::uuid AND n.edge_external_id = $4
    AND n.status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED')
    AND s.status = 'ACTIVE' AND s.credential_ref_id = b.credential_ref_id AND s.credential_revision = $6
    AND s.opened_at <= $5 AND s.expires_at > $5
)
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.TrimSpace(input.SessionID), strings.TrimSpace(input.Request.EdgeID), now, input.Request.CredentialRevision).Scan(&authorized)
	if err != nil {
		return edgefleet.HandshakeResult{}, fmt.Errorf("authorize Edge handshake: %w", err)
	}
	if !authorized {
		result = edgefleet.HandshakeResult{Status: edgefleet.HandshakeRejected, Reason: "IDENTITY_OR_SESSION_INACTIVE"}
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_handshakes (
  id, tenant_id, edge_node_id, session_id, runtime_version, protocol_schema_version, capabilities,
  max_payload_bytes, negotiated_max_payload_bytes, status, reason, occurred_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::jsonb, $8, NULLIF($9, 0), $10, NULLIF($11, ''), $12)
`, handshakeID, store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.TrimSpace(input.SessionID),
		strings.TrimSpace(input.Request.RuntimeVersion), input.Request.ProtocolSchemaVersion, string(capabilities), input.Request.MaxPayloadBytes,
		result.NegotiatedMaxBytes, string(result.Status), strings.TrimSpace(result.Reason), now)
	if err != nil {
		return edgefleet.HandshakeResult{}, fmt.Errorf("record Edge handshake: %w", err)
	}
	nodeStatus := "ACTIVE"
	switch result.Status {
	case edgefleet.HandshakeReadOnly:
		nodeStatus = "READ_ONLY"
	case edgefleet.HandshakeUpgradeRequired:
		nodeStatus = "UPGRADE_REQUIRED"
	case edgefleet.HandshakeRejected:
		nodeStatus = "SUSPENDED"
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.edge_nodes SET status = $3, revision = revision + 1, updated_at = $4
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID), nodeStatus, now); err != nil {
		return edgefleet.HandshakeResult{}, fmt.Errorf("update Edge handshake status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return edgefleet.HandshakeResult{}, err
	}
	return result, nil
}

func (store *Store) PublishEdgeRelease(ctx context.Context, release edgefleet.SignedEdgeRelease) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(release.Payload.ReleaseID)) || !sha256Pattern.MatchString(release.Digest) || strings.TrimSpace(release.SignerKeyID) == "" || len(release.Signature) != 128 {
		return errors.New("invalid signed EdgeRelease")
	}
	capabilities, err := json.Marshal(release.Payload.RequiredCapabilities)
	if err != nil {
		return err
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_releases (
  id, tenant_id, release_digest_sha256, signer_key_id, signature_ed25519_hex,
  runtime_revision, manifest_revision, registry_projection_revision, driver_revision, rule_revision,
  schedule_revision, safety_policy_revision, desired_config_revision, min_runtime_version, max_runtime_version,
  required_capabilities, published_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), $12, $13, $14, $15, $16::jsonb, $17)
`, release.Payload.ReleaseID, store.tenantID, strings.ToLower(release.Digest), strings.TrimSpace(release.SignerKeyID), strings.ToLower(release.Signature),
		release.Payload.RuntimeRevision, release.Payload.ManifestRevision, release.Payload.RegistryProjectionRevision, release.Payload.DriverRevision,
		release.Payload.RuleRevision, release.Payload.ScheduleRevision, release.Payload.SafetyPolicyRevision, release.Payload.DesiredConfigRevision,
		release.Payload.MinRuntimeVersion, release.Payload.MaxRuntimeVersion, string(capabilities), store.clock().UTC())
	if err != nil {
		return fmt.Errorf("publish EdgeRelease: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) PublishEdgeSnapshot(ctx context.Context, edgeNodeID string, meta edgefleet.SnapshotMeta, chunks []edgefleet.SnapshotChunk) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(meta.ReleaseID)) || meta.SnapshotRevision == 0 || meta.DesiredRevision == 0 || meta.ChunkCount != len(chunks) || !sha256Pattern.MatchString(meta.FinalDigest) {
		return errors.New("invalid Edge snapshot")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_snapshots (
  edge_node_id, snapshot_revision, tenant_id, release_id, desired_revision, base_delivery_cursor, chunk_count, final_digest_sha256, created_at
) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9)
`, strings.TrimSpace(edgeNodeID), meta.SnapshotRevision, store.tenantID, meta.ReleaseID, meta.DesiredRevision, meta.BaseDeliveryCursor, meta.ChunkCount, strings.ToLower(meta.FinalDigest), now)
	if err != nil {
		return fmt.Errorf("publish Edge snapshot: %w", err)
	}
	for index, chunk := range chunks {
		if chunk.SnapshotRevision != meta.SnapshotRevision || chunk.Index != index || !sha256Pattern.MatchString(chunk.Digest) {
			return errors.New("Edge snapshot chunks are not contiguous or digest-bound")
		}
		payload, marshalErr := json.Marshal(chunk.Items)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.edge_snapshot_chunks (
  edge_node_id, snapshot_revision, chunk_index, tenant_id, chunk_digest_sha256, payload, created_at
) VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6::jsonb, $7)
`, strings.TrimSpace(edgeNodeID), meta.SnapshotRevision, chunk.Index, store.tenantID, strings.ToLower(chunk.Digest), string(payload), now); err != nil {
			return fmt.Errorf("publish Edge snapshot chunk %d: %w", chunk.Index, err)
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) SetDesiredEdgeState(ctx context.Context, input DesiredEdgeStateInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.ReleaseID)) || input.DesiredRevision == 0 || input.SnapshotRevision == 0 {
		return errors.New("invalid DesiredEdgeState")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
INSERT INTO connectivity.desired_edge_states (edge_node_id, tenant_id, release_id, desired_revision, snapshot_revision, revision, published_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 1, $6, $6)
ON CONFLICT (edge_node_id) DO UPDATE
SET release_id = EXCLUDED.release_id, desired_revision = EXCLUDED.desired_revision, snapshot_revision = EXCLUDED.snapshot_revision,
    revision = connectivity.desired_edge_states.revision + 1, published_at = EXCLUDED.published_at, updated_at = EXCLUDED.updated_at
WHERE connectivity.desired_edge_states.tenant_id = EXCLUDED.tenant_id
  AND EXCLUDED.desired_revision > connectivity.desired_edge_states.desired_revision
`, strings.TrimSpace(input.EdgeNodeID), store.tenantID, strings.TrimSpace(input.ReleaseID), input.DesiredRevision, input.SnapshotRevision, now)
	if err != nil {
		return fmt.Errorf("set DesiredEdgeState: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrEdgeFleetStale
	}
	return tx.Commit(ctx)
}

func (store *Store) RecordObservedEdgeState(ctx context.Context, input ObservedEdgeStateInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) || strings.TrimSpace(input.RuntimeVersion) == "" || input.ProtocolSchemaVersion <= 0 || input.LastSeenAt.IsZero() || input.BacklogBytes < 0 {
		return errors.New("invalid ObservedEdgeState")
	}
	for _, optionalID := range []string{input.ActiveReleaseID, input.StagedReleaseID, input.PreviousReleaseID} {
		if optionalID != "" && !uuidV7Pattern.MatchString(strings.TrimSpace(optionalID)) {
			return errors.New("invalid ObservedEdgeState release identity")
		}
	}
	if input.ManifestDigest != "" && !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.ManifestDigest))) {
		return errors.New("invalid ObservedEdgeState manifest digest")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
INSERT INTO connectivity.observed_edge_states (
  edge_node_id, tenant_id, active_release_id, staged_release_id, previous_release_id,
  active_snapshot_revision, desired_revision, delivery_cursor, reported_config_revision,
  runtime_version, protocol_schema_version, manifest_digest_sha256, health, capacity_state,
  drift_status, drift_reason, backlog_bytes, quarantine_count, last_seen_at, revision, updated_at
) VALUES (
  $1::uuid, $2::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid,
  $6, $7, $8, $9, $10, $11, NULLIF($12, ''), $13, $14, $15, NULLIF($16, ''), $17, $18, $19, 1, $20
)
ON CONFLICT (edge_node_id) DO UPDATE
SET active_release_id = EXCLUDED.active_release_id, staged_release_id = EXCLUDED.staged_release_id, previous_release_id = EXCLUDED.previous_release_id,
    active_snapshot_revision = EXCLUDED.active_snapshot_revision, desired_revision = EXCLUDED.desired_revision,
    delivery_cursor = EXCLUDED.delivery_cursor, reported_config_revision = EXCLUDED.reported_config_revision,
    runtime_version = EXCLUDED.runtime_version, protocol_schema_version = EXCLUDED.protocol_schema_version,
    manifest_digest_sha256 = EXCLUDED.manifest_digest_sha256, health = EXCLUDED.health, capacity_state = EXCLUDED.capacity_state,
    drift_status = EXCLUDED.drift_status, drift_reason = EXCLUDED.drift_reason, backlog_bytes = EXCLUDED.backlog_bytes,
    quarantine_count = EXCLUDED.quarantine_count, last_seen_at = EXCLUDED.last_seen_at,
    revision = connectivity.observed_edge_states.revision + 1, updated_at = EXCLUDED.updated_at
WHERE connectivity.observed_edge_states.tenant_id = EXCLUDED.tenant_id
  AND EXCLUDED.last_seen_at >= connectivity.observed_edge_states.last_seen_at
`, strings.TrimSpace(input.EdgeNodeID), store.tenantID, strings.TrimSpace(input.ActiveReleaseID), strings.TrimSpace(input.StagedReleaseID),
		strings.TrimSpace(input.PreviousReleaseID), input.ActiveSnapshotRevision, input.DesiredRevision, input.DeliveryCursor, input.ReportedConfigRevision,
		strings.TrimSpace(input.RuntimeVersion), input.ProtocolSchemaVersion, strings.ToLower(strings.TrimSpace(input.ManifestDigest)), strings.TrimSpace(input.Health),
		string(input.CapacityState), strings.TrimSpace(input.DriftStatus), strings.TrimSpace(input.DriftReason), input.BacklogBytes, input.QuarantineCount,
		input.LastSeenAt.UTC(), now)
	if err != nil {
		return fmt.Errorf("record ObservedEdgeState: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrEdgeFleetStale
	}
	return tx.Commit(ctx)
}

func (store *Store) AppendEdgeDelivery(ctx context.Context, input EdgeDeliveryInput) (edgefleet.DeliveryItem, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.DeliveryID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) ||
		strings.TrimSpace(input.OrderingKey) == "" || input.SourceRevision == 0 || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.PayloadDigest))) ||
		(input.Tombstone && len(input.Payload) != 0) || (!input.Tombstone && (len(input.Payload) == 0 || !json.Valid(input.Payload))) {
		return edgefleet.DeliveryItem{}, errors.New("invalid Edge delivery input")
	}
	payloadDigest := edgeFleetPayloadDigest(input.Payload)
	if strings.ToLower(strings.TrimSpace(input.PayloadDigest)) != payloadDigest {
		return edgefleet.DeliveryItem{}, errors.New("Edge delivery payload digest mismatch")
	}
	authority, known := edgefleet.OwnerAuthority(input.OwnerDomain)
	if !known || authority != edgefleet.AuthorityCloud {
		return edgefleet.DeliveryItem{}, edgefleet.ErrAuthorityViolation
	}
	if input.Priority != "CONFIG_CRITICAL" && input.Priority != "CONFIG_NORMAL" {
		return edgefleet.DeliveryItem{}, errors.New("invalid Edge delivery priority")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return edgefleet.DeliveryItem{}, err
	}
	defer tx.Rollback(ctx)
	var committed uint64
	err = tx.QueryRow(ctx, `
SELECT committed_cursor
FROM connectivity.edge_delivery_cursors
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid
FOR UPDATE
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID)).Scan(&committed)
	if errors.Is(err, pgx.ErrNoRows) {
		return edgefleet.DeliveryItem{}, ErrNotFound
	}
	if err != nil {
		return edgefleet.DeliveryItem{}, fmt.Errorf("lock Edge delivery cursor: %w", err)
	}
	var nextCursor uint64
	err = tx.QueryRow(ctx, `
SELECT COALESCE(max(delivery_cursor), $3) + 1
FROM connectivity.edge_delivery_items
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID), committed).Scan(&nextCursor)
	if err != nil {
		return edgefleet.DeliveryItem{}, fmt.Errorf("allocate Edge delivery cursor: %w", err)
	}
	now := store.clock().UTC()
	var payload any
	if !input.Tombstone {
		payload = string(input.Payload)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_delivery_items (
  edge_node_id, delivery_cursor, tenant_id, delivery_id, owner_domain, ordering_key, source_revision,
  payload_digest_sha256, payload, tombstone, priority, state, attempt_count, created_at, updated_at
) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9::jsonb, $10, $11, 'PENDING', 0, $12, $12)
`, strings.TrimSpace(input.EdgeNodeID), nextCursor, store.tenantID, strings.TrimSpace(input.DeliveryID), string(input.OwnerDomain),
		strings.TrimSpace(input.OrderingKey), input.SourceRevision, strings.ToLower(strings.TrimSpace(input.PayloadDigest)), payload, input.Tombstone, input.Priority, now)
	if err != nil {
		return edgefleet.DeliveryItem{}, fmt.Errorf("append Edge delivery: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return edgefleet.DeliveryItem{}, err
	}
	return edgefleet.DeliveryItem{
		Cursor: nextCursor, DeliveryID: strings.TrimSpace(input.DeliveryID), OwnerDomain: input.OwnerDomain, OrderingKey: strings.TrimSpace(input.OrderingKey),
		SourceRevision: input.SourceRevision, PayloadDigest: strings.ToLower(strings.TrimSpace(input.PayloadDigest)), Deleted: input.Tombstone, Payload: input.Payload,
	}, nil
}

func (store *Store) RecordEdgeDeliveryAck(ctx context.Context, edgeNodeID string, ack edgefleet.DeliveryAck) (EdgeDeliveryResult, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) || ack.Cursor == 0 || !uuidV7Pattern.MatchString(strings.TrimSpace(ack.DeliveryID)) ||
		(ack.Status != edgefleet.DeliveryAcked && ack.Status != edgefleet.DeliveryQuarantined) {
		return EdgeDeliveryResult{}, errors.New("invalid Edge delivery acknowledgement")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return EdgeDeliveryResult{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	state := "ACKED"
	if ack.Status == edgefleet.DeliveryQuarantined {
		state = "QUARANTINED"
	}
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_delivery_items
SET state = $5, applied_owner_revision = NULLIF($6, 0), failure_reason = NULLIF($7, ''), updated_at = $8
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid AND delivery_cursor = $3 AND delivery_id = $4::uuid
  AND payload_digest_sha256 = $9 AND state IN ('PENDING','IN_FLIGHT')
`, store.tenantID, strings.TrimSpace(edgeNodeID), ack.Cursor, strings.TrimSpace(ack.DeliveryID), state, ack.AppliedOwnerRevision,
		strings.TrimSpace(ack.Reason), now, strings.ToLower(strings.TrimSpace(ack.PayloadDigest)))
	if err != nil {
		return EdgeDeliveryResult{}, fmt.Errorf("record Edge delivery acknowledgement: %w", err)
	}
	if tag.RowsAffected() != 1 {
		matches, matchErr := existingEdgeDeliveryAckMatches(ctx, tx, store.tenantID, strings.TrimSpace(edgeNodeID), ack, state)
		if matchErr != nil {
			return EdgeDeliveryResult{}, matchErr
		}
		if !matches {
			return EdgeDeliveryResult{}, ErrNotFound
		}
	}
	committed, err := advanceEdgeDeliveryCursor(ctx, tx, store.tenantID, strings.TrimSpace(edgeNodeID), now)
	if err != nil {
		return EdgeDeliveryResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EdgeDeliveryResult{}, err
	}
	return EdgeDeliveryResult{Cursor: ack.Cursor, CommittedCursor: committed, Status: state, AppliedOwnerRevision: ack.AppliedOwnerRevision}, nil
}

func existingEdgeDeliveryAckMatches(ctx context.Context, tx pgx.Tx, tenantID, edgeNodeID string, ack edgefleet.DeliveryAck, state string) (bool, error) {
	var storedState, storedReason string
	var storedApplied uint64
	err := tx.QueryRow(ctx, `
SELECT state, COALESCE(applied_owner_revision, 0), COALESCE(failure_reason, '')
FROM connectivity.edge_delivery_items
WHERE tenant_id=$1::uuid AND edge_node_id=$2::uuid AND delivery_cursor=$3 AND delivery_id=$4::uuid AND payload_digest_sha256=$5
`, tenantID, edgeNodeID, ack.Cursor, strings.TrimSpace(ack.DeliveryID), strings.ToLower(strings.TrimSpace(ack.PayloadDigest))).Scan(&storedState, &storedApplied, &storedReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load existing Edge delivery acknowledgement: %w", err)
	}
	return storedState == state && storedApplied == ack.AppliedOwnerRevision && storedReason == strings.TrimSpace(ack.Reason), nil
}

func (store *Store) DisposeEdgeDelivery(ctx context.Context, edgeNodeID string, cursor uint64, evidenceDigest string) (uint64, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) || cursor == 0 || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(evidenceDigest))) {
		return 0, errors.New("invalid Edge delivery disposition")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_delivery_items
SET state = 'DISPOSED', disposition_evidence_sha256 = $4, updated_at = $5
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid AND delivery_cursor = $3 AND state = 'QUARANTINED'
`, store.tenantID, strings.TrimSpace(edgeNodeID), cursor, strings.ToLower(strings.TrimSpace(evidenceDigest)), now)
	if err != nil {
		return 0, fmt.Errorf("dispose Edge delivery: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrNotFound
	}
	committed, err := advanceEdgeDeliveryCursor(ctx, tx, store.tenantID, strings.TrimSpace(edgeNodeID), now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return committed, nil
}

func advanceEdgeDeliveryCursor(ctx context.Context, tx pgx.Tx, tenantID, edgeNodeID string, at time.Time) (uint64, error) {
	var current uint64
	err := tx.QueryRow(ctx, `
SELECT committed_cursor FROM connectivity.edge_delivery_cursors
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid
FOR UPDATE
`, tenantID, edgeNodeID).Scan(&current)
	if err != nil {
		return 0, fmt.Errorf("lock committed Edge delivery cursor: %w", err)
	}
	var candidate uint64
	err = tx.QueryRow(ctx, `
WITH tail AS (
  SELECT delivery_cursor, state
  FROM connectivity.edge_delivery_items
  WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid AND delivery_cursor > $3
), bounds AS (
  SELECT min(delivery_cursor) FILTER (WHERE state NOT IN ('ACKED','DISPOSED')) AS first_blocked,
         max(delivery_cursor) AS maximum
  FROM tail
)
SELECT CASE
  WHEN first_blocked IS NOT NULL THEN GREATEST($3, first_blocked - 1)
  WHEN maximum IS NOT NULL THEN maximum
  ELSE $3
END
FROM bounds
`, tenantID, edgeNodeID, current).Scan(&candidate)
	if err != nil {
		return 0, fmt.Errorf("calculate committed Edge delivery cursor: %w", err)
	}
	if candidate > current {
		_, err = tx.Exec(ctx, `
UPDATE connectivity.edge_delivery_cursors
SET committed_cursor = $3, revision = revision + 1, updated_at = $4
WHERE tenant_id = $1::uuid AND edge_node_id = $2::uuid
`, tenantID, edgeNodeID, candidate, at)
		if err != nil {
			return 0, fmt.Errorf("advance committed Edge delivery cursor: %w", err)
		}
	}
	return candidate, nil
}

func (store *Store) PublishOTAArtifact(ctx context.Context, input OTAArtifactInput) error {
	artifact := input.Artifact
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.ArtifactID)) || artifact.Payload.ArtifactID != strings.TrimSpace(input.ArtifactID) ||
		!uuidV7Pattern.MatchString(strings.TrimSpace(artifact.Payload.RollbackArtifactID)) || strings.TrimSpace(artifact.Payload.PackageRef) == "" ||
		!sha256Pattern.MatchString(artifact.Payload.PackageSHA256) || !sha256Pattern.MatchString(artifact.Digest) || len(artifact.Signature) != 128 {
		return errors.New("invalid signed OTA artifact")
	}
	capabilities, err := json.Marshal(artifact.Payload.RequiredCapabilities)
	if err != nil {
		return err
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_ota_artifacts (
  id, tenant_id, version, package_ref, package_sha256, artifact_digest_sha256, signer_key_id, signature_ed25519_hex,
  min_runtime_version, max_runtime_version, required_capabilities, rollback_artifact_id, published_at
) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::uuid, $13)
`, strings.TrimSpace(input.ArtifactID), store.tenantID, artifact.Payload.Version, strings.TrimSpace(artifact.Payload.PackageRef), artifact.Payload.PackageSHA256, artifact.Digest,
		artifact.SignerKeyID, strings.ToLower(artifact.Signature), artifact.Payload.MinRuntimeVersion, artifact.Payload.MaxRuntimeVersion,
		string(capabilities), strings.TrimSpace(artifact.Payload.RollbackArtifactID), store.clock().UTC())
	if err != nil {
		return fmt.Errorf("publish OTA artifact: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) CreateOTACampaign(ctx context.Context, input OTACampaignInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.CampaignID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.ArtifactID)) || input.WindowStart.IsZero() || !input.WindowEnd.After(input.WindowStart) {
		return errors.New("invalid OTA campaign")
	}
	campaign, err := edgefleet.NewOTACampaign(input.CampaignID, input.ArtifactID, input.Waves)
	if err != nil {
		return err
	}
	waves, err := json.Marshal(campaign.Waves)
	if err != nil {
		return err
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_ota_campaigns (
  id, tenant_id, artifact_id, status, waves, wave_index, campaign_window_start, campaign_window_end, revision, created_at, updated_at
) VALUES ($1::uuid, $2::uuid, $3::uuid, 'DRAFT', $4::jsonb, 0, $5, $6, 1, $7, $7)
`, strings.TrimSpace(input.CampaignID), store.tenantID, strings.TrimSpace(input.ArtifactID), string(waves), input.WindowStart.UTC(), input.WindowEnd.UTC(), now)
	if err != nil {
		return fmt.Errorf("create OTA campaign: %w", err)
	}
	return tx.Commit(ctx)
}
