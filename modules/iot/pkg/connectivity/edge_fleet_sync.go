package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

type EdgeRuntimeDescriptor struct {
	EdgeNodeID         string
	EdgeExternalID     string
	SessionID          string
	CredentialRevision uint64
}

type EdgeSyncBundle struct {
	Mode         edgefleet.ReconnectMode
	Release      edgefleet.SignedEdgeRelease
	Meta         edgefleet.SnapshotMeta
	Chunks       []edgefleet.SnapshotChunk
	Dispositions []edgefleet.QuarantineDispositionPayload
	Items        []edgefleet.DeliveryItem
}

func (store *Store) LoadEdgeRuntimeDescriptor(ctx context.Context, integrationInstanceID string) (EdgeRuntimeDescriptor, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(integrationInstanceID)) {
		return EdgeRuntimeDescriptor{}, ErrNotFound
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return EdgeRuntimeDescriptor{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var descriptor EdgeRuntimeDescriptor
	err = tx.QueryRow(ctx, `
SELECT n.id::text, n.edge_external_id, s.id::text, s.credential_revision
FROM connectivity.edge_nodes n
JOIN connectivity.edge_identity_bindings b
  ON b.tenant_id=n.tenant_id AND b.edge_node_id=n.id AND b.status='ACTIVE' AND b.valid_until IS NULL
JOIN connectivity.sessions s
  ON s.tenant_id=n.tenant_id AND s.integration_instance_id=n.integration_instance_id
 AND s.credential_ref_id=b.credential_ref_id AND s.status='ACTIVE'
WHERE n.tenant_id=$1::uuid AND n.integration_instance_id=$2::uuid
  AND n.status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED')
  AND s.gateway_external_id=n.edge_external_id AND s.opened_at <= $3 AND s.expires_at > $3
`, store.tenantID, strings.TrimSpace(integrationInstanceID), now).Scan(
		&descriptor.EdgeNodeID, &descriptor.EdgeExternalID, &descriptor.SessionID, &descriptor.CredentialRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeRuntimeDescriptor{}, ErrNotFound
	}
	if err != nil {
		return EdgeRuntimeDescriptor{}, fmt.Errorf("load Edge runtime descriptor: %w", err)
	}
	return descriptor, nil
}

func (store *Store) LoadEdgeSyncBundle(ctx context.Context, edgeNodeID string, status edgefleet.SyncStatus, maxItems int) (EdgeSyncBundle, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) || maxItems < 1 || maxItems > 1024 {
		return EdgeSyncBundle{}, errors.New("invalid Edge sync bundle request")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return EdgeSyncBundle{}, err
	}
	defer tx.Rollback(ctx)

	var bundle EdgeSyncBundle
	var release edgefleet.SignedEdgeRelease
	var capabilitiesJSON string
	var retainedFloor uint64
	err = tx.QueryRow(ctx, `
SELECT d.release_id::text, d.desired_revision, d.snapshot_revision,
       r.release_digest_sha256, r.signer_key_id, r.signature_ed25519_hex,
       r.runtime_revision, r.manifest_revision, r.registry_projection_revision, r.driver_revision,
       COALESCE(r.rule_revision,''), COALESCE(r.schedule_revision,''), r.safety_policy_revision,
       r.desired_config_revision, r.min_runtime_version, r.max_runtime_version,
       r.required_capabilities::text,
       c.retained_floor
FROM connectivity.desired_edge_states d
JOIN connectivity.edge_releases r ON r.tenant_id=d.tenant_id AND r.id=d.release_id
JOIN connectivity.edge_delivery_cursors c ON c.tenant_id=d.tenant_id AND c.edge_node_id=d.edge_node_id
WHERE d.tenant_id=$1::uuid AND d.edge_node_id=$2::uuid
`, store.tenantID, strings.TrimSpace(edgeNodeID)).Scan(
		&release.Payload.ReleaseID, &release.Payload.DesiredConfigRevision, &bundle.Meta.SnapshotRevision,
		&release.Digest, &release.SignerKeyID, &release.Signature,
		&release.Payload.RuntimeRevision, &release.Payload.ManifestRevision, &release.Payload.RegistryProjectionRevision, &release.Payload.DriverRevision,
		&release.Payload.RuleRevision, &release.Payload.ScheduleRevision, &release.Payload.SafetyPolicyRevision,
		&release.Payload.DesiredConfigRevision, &release.Payload.MinRuntimeVersion, &release.Payload.MaxRuntimeVersion,
		&capabilitiesJSON, &retainedFloor,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeSyncBundle{}, ErrNotFound
	}
	if err != nil {
		return EdgeSyncBundle{}, fmt.Errorf("load DesiredEdgeState: %w", err)
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &release.Payload.RequiredCapabilities); err != nil {
		return EdgeSyncBundle{}, fmt.Errorf("decode EdgeRelease required capabilities: %w", err)
	}
	bundle.Release = release

	if status.StagingSnapshotRevision == bundle.Meta.SnapshotRevision {
		bundle.Mode = edgefleet.ReconnectSnapshotResume
	} else if status.Observed.ActiveSnapshotRevision != bundle.Meta.SnapshotRevision || status.Observed.DeliveryCursor < retainedFloor {
		bundle.Mode = edgefleet.ReconnectSnapshot
	} else {
		bundle.Mode = edgefleet.ReconnectDelta
	}

	if bundle.Mode == edgefleet.ReconnectDelta {
		dispositions, err := loadEdgeDeliveryDispositions(ctx, tx, store.tenantID, strings.TrimSpace(edgeNodeID), status.Observed.DeliveryCursor, maxItems)
		if err != nil {
			return EdgeSyncBundle{}, err
		}
		items, err := loadPendingEdgeDeliveries(ctx, tx, store.tenantID, strings.TrimSpace(edgeNodeID), status.Observed.DeliveryCursor, maxItems)
		if err != nil {
			return EdgeSyncBundle{}, err
		}
		bundle.Dispositions = dispositions
		bundle.Items = items
		return bundle, nil
	}

	err = tx.QueryRow(ctx, `
SELECT s.snapshot_revision, s.desired_revision, s.release_id::text, s.chunk_count, s.final_digest_sha256, s.base_delivery_cursor
FROM connectivity.edge_snapshots s
WHERE s.tenant_id=$1::uuid AND s.edge_node_id=$2::uuid AND s.snapshot_revision=$3
`, store.tenantID, strings.TrimSpace(edgeNodeID), bundle.Meta.SnapshotRevision).Scan(
		&bundle.Meta.SnapshotRevision, &bundle.Meta.DesiredRevision, &bundle.Meta.ReleaseID, &bundle.Meta.ChunkCount, &bundle.Meta.FinalDigest, &bundle.Meta.BaseDeliveryCursor,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeSyncBundle{}, ErrNotFound
	}
	if err != nil {
		return EdgeSyncBundle{}, fmt.Errorf("load Edge snapshot: %w", err)
	}
	if bundle.Meta.ReleaseID != bundle.Release.Payload.ReleaseID || bundle.Meta.DesiredRevision != bundle.Release.Payload.DesiredConfigRevision {
		return EdgeSyncBundle{}, errors.New("DesiredEdgeState release/snapshot revision mismatch")
	}

	startChunk := 0
	if bundle.Mode == edgefleet.ReconnectSnapshotResume {
		startChunk = status.ResumeChunk
		if startChunk < 0 || startChunk > bundle.Meta.ChunkCount {
			return EdgeSyncBundle{}, errors.New("Edge snapshot resume chunk is invalid")
		}
	}
	rows, err := tx.Query(ctx, `
SELECT chunk_index, chunk_digest_sha256, payload::text
FROM connectivity.edge_snapshot_chunks
WHERE tenant_id=$1::uuid AND edge_node_id=$2::uuid AND snapshot_revision=$3 AND chunk_index >= $4
ORDER BY chunk_index
`, store.tenantID, strings.TrimSpace(edgeNodeID), bundle.Meta.SnapshotRevision, startChunk)
	if err != nil {
		return EdgeSyncBundle{}, fmt.Errorf("load Edge snapshot chunks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var chunk edgefleet.SnapshotChunk
		var payloadJSON string
		chunk.SnapshotRevision = bundle.Meta.SnapshotRevision
		if err := rows.Scan(&chunk.Index, &chunk.Digest, &payloadJSON); err != nil {
			return EdgeSyncBundle{}, fmt.Errorf("scan Edge snapshot chunk: %w", err)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &chunk.Items); err != nil {
			return EdgeSyncBundle{}, fmt.Errorf("decode Edge snapshot chunk: %w", err)
		}
		bundle.Chunks = append(bundle.Chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return EdgeSyncBundle{}, err
	}
	if startChunk+len(bundle.Chunks) != bundle.Meta.ChunkCount {
		return EdgeSyncBundle{}, errors.New("Edge snapshot chunk set is incomplete")
	}
	return bundle, nil
}

func loadEdgeDeliveryDispositions(ctx context.Context, tx pgx.Tx, tenantID, edgeNodeID string, afterCursor uint64, maxItems int) ([]edgefleet.QuarantineDispositionPayload, error) {
	rows, err := tx.Query(ctx, `
SELECT delivery_cursor, disposition_evidence_sha256
FROM connectivity.edge_delivery_items
WHERE tenant_id=$1::uuid AND edge_node_id=$2::uuid AND delivery_cursor > $3
  AND state='DISPOSED' AND disposition_evidence_sha256 IS NOT NULL
ORDER BY delivery_cursor
LIMIT $4
`, tenantID, edgeNodeID, afterCursor, maxItems)
	if err != nil {
		return nil, fmt.Errorf("load Edge delivery dispositions: %w", err)
	}
	defer rows.Close()
	items := make([]edgefleet.QuarantineDispositionPayload, 0, maxItems)
	for rows.Next() {
		var item edgefleet.QuarantineDispositionPayload
		if err := rows.Scan(&item.Cursor, &item.EvidenceDigest); err != nil {
			return nil, fmt.Errorf("scan Edge delivery disposition: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func loadPendingEdgeDeliveries(ctx context.Context, tx pgx.Tx, tenantID, edgeNodeID string, afterCursor uint64, maxItems int) ([]edgefleet.DeliveryItem, error) {
	rows, err := tx.Query(ctx, `
SELECT delivery_cursor, delivery_id::text, owner_domain, ordering_key, source_revision,
       payload_digest_sha256, tombstone, COALESCE(payload::text,'')
FROM connectivity.edge_delivery_items
WHERE tenant_id=$1::uuid AND edge_node_id=$2::uuid AND delivery_cursor > $3
  AND state IN ('PENDING','IN_FLIGHT')
ORDER BY delivery_cursor
LIMIT $4
`, tenantID, edgeNodeID, afterCursor, maxItems)
	if err != nil {
		return nil, fmt.Errorf("load pending Edge deliveries: %w", err)
	}
	defer rows.Close()
	items := make([]edgefleet.DeliveryItem, 0, maxItems)
	for rows.Next() {
		var item edgefleet.DeliveryItem
		var ownerDomain string
		var payloadJSON string
		if err := rows.Scan(&item.Cursor, &item.DeliveryID, &ownerDomain, &item.OrderingKey, &item.SourceRevision, &item.PayloadDigest, &item.Deleted, &payloadJSON); err != nil {
			return nil, fmt.Errorf("scan pending Edge delivery: %w", err)
		}
		item.OwnerDomain = edgefleet.OwnerDomain(ownerDomain)
		if !item.Deleted {
			item.Payload = json.RawMessage(payloadJSON)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
