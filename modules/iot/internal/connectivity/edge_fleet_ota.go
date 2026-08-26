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

type EdgeOTADispatch struct {
	CampaignID string
	Artifact   edgefleet.SignedOTAArtifact
}

func (store *Store) LoadDispatchableOTA(ctx context.Context, edgeNodeID string) (EdgeOTADispatch, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) {
		return EdgeOTADispatch{}, ErrNotFound
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return EdgeOTADispatch{}, err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	var dispatch EdgeOTADispatch
	var capabilitiesJSON string
	var assignmentStatus string
	err = tx.QueryRow(ctx, `
SELECT x.campaign_id::text, x.status,
       a.id::text, a.version, a.package_ref, a.package_sha256, a.artifact_digest_sha256,
       a.signer_key_id, a.signature_ed25519_hex, a.min_runtime_version, a.max_runtime_version,
       a.required_capabilities::text, a.rollback_artifact_id::text
FROM connectivity.edge_ota_assignments x
JOIN connectivity.edge_ota_campaigns c
  ON c.tenant_id=x.tenant_id AND c.id=x.campaign_id
JOIN connectivity.edge_ota_artifacts a
  ON a.tenant_id=c.tenant_id AND a.id=c.artifact_id
WHERE x.tenant_id=$1::uuid AND x.edge_node_id=$2::uuid
  AND x.status IN ('PENDING','STAGING')
  AND c.status='RUNNING' AND c.campaign_window_start <= $3 AND c.campaign_window_end > $3
  AND x.target_wave <= (c.waves ->> c.wave_index)::integer
ORDER BY c.created_at, x.target_wave, x.campaign_id
LIMIT 1
FOR UPDATE OF x
`, store.tenantID, strings.TrimSpace(edgeNodeID), now).Scan(
		&dispatch.CampaignID, &assignmentStatus,
		&dispatch.Artifact.Payload.ArtifactID, &dispatch.Artifact.Payload.Version, &dispatch.Artifact.Payload.PackageRef,
		&dispatch.Artifact.Payload.PackageSHA256, &dispatch.Artifact.Digest, &dispatch.Artifact.SignerKeyID,
		&dispatch.Artifact.Signature, &dispatch.Artifact.Payload.MinRuntimeVersion, &dispatch.Artifact.Payload.MaxRuntimeVersion,
		&capabilitiesJSON, &dispatch.Artifact.Payload.RollbackArtifactID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EdgeOTADispatch{}, ErrNotFound
	}
	if err != nil {
		return EdgeOTADispatch{}, fmt.Errorf("load dispatchable Edge OTA: %w", err)
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &dispatch.Artifact.Payload.RequiredCapabilities); err != nil {
		return EdgeOTADispatch{}, fmt.Errorf("decode OTA required capabilities: %w", err)
	}
	if assignmentStatus == "PENDING" {
		tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_ota_assignments
SET status='STAGING', staged_at=$4, revision=revision+1, updated_at=$4
WHERE tenant_id=$1::uuid AND campaign_id=$2::uuid AND edge_node_id=$3::uuid AND status='PENDING'
`, store.tenantID, dispatch.CampaignID, strings.TrimSpace(edgeNodeID), now)
		if err != nil {
			return EdgeOTADispatch{}, fmt.Errorf("stage Edge OTA assignment: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return EdgeOTADispatch{}, ErrEdgeFleetStale
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return EdgeOTADispatch{}, err
	}
	return dispatch, nil
}

func (store *Store) RecordOTAResult(ctx context.Context, edgeNodeID string, result edgefleet.OTAActivationResult) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(edgeNodeID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(result.ArtifactID)) {
		return errors.New("invalid Edge OTA result")
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	evidenceDigest := edgeFleetPayloadDigest(resultJSON)
	status := "SUCCEEDED"
	if result.RolledBack {
		status = "ROLLED_BACK"
	} else if strings.TrimSpace(result.Reason) != "" {
		status = "QUARANTINED"
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_ota_assignments x
SET status=$4, completed_at=$5, evidence_digest_sha256=$6, reason=NULLIF($7,''), revision=x.revision+1, updated_at=$5
FROM connectivity.edge_ota_campaigns c
WHERE x.tenant_id=$1::uuid AND x.edge_node_id=$2::uuid AND x.campaign_id=c.id AND c.tenant_id=x.tenant_id
  AND c.artifact_id=$3::uuid AND x.status IN ('STAGING','ACTIVATING','VERIFYING')
`, store.tenantID, strings.TrimSpace(edgeNodeID), strings.TrimSpace(result.ArtifactID), status, now, evidenceDigest, strings.TrimSpace(result.Reason))
	if err != nil {
		return fmt.Errorf("record Edge OTA result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var storedStatus, storedDigest string
		err := tx.QueryRow(ctx, `
SELECT x.status, COALESCE(x.evidence_digest_sha256,'')
FROM connectivity.edge_ota_assignments x
JOIN connectivity.edge_ota_campaigns c ON c.tenant_id=x.tenant_id AND c.id=x.campaign_id
WHERE x.tenant_id=$1::uuid AND x.edge_node_id=$2::uuid AND c.artifact_id=$3::uuid
`, store.tenantID, strings.TrimSpace(edgeNodeID), strings.TrimSpace(result.ArtifactID)).Scan(&storedStatus, &storedDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load existing Edge OTA result: %w", err)
		}
		if storedStatus != status || storedDigest != evidenceDigest {
			return ErrEdgeFleetStale
		}
	}
	return tx.Commit(ctx)
}
