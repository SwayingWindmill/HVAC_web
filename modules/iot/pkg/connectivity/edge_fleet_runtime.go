package connectivity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func edgeFleetPayloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

type EdgeSyncSessionInput struct {
	ID                    string
	EdgeNodeID            string
	ConnectivitySessionID string
	Status                string
	SnapshotRevision      uint64
	SnapshotResumeChunk   *int
	DeliveryCursor        uint64
}

func (store *Store) OpenEdgeSyncSession(ctx context.Context, input EdgeSyncSessionInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) ||
		!uuidV7Pattern.MatchString(strings.TrimSpace(input.ConnectivitySessionID)) || (input.Status != "ACTIVE" && input.Status != "READ_ONLY" && input.Status != "UPGRADE_REQUIRED") {
		return errors.New("invalid Edge sync session")
	}
	if input.SnapshotResumeChunk != nil && *input.SnapshotResumeChunk < 0 {
		return errors.New("invalid Edge snapshot resume chunk")
	}
	now := store.clock().UTC()
	syncSessionID := strings.TrimSpace(input.ID)
	if syncSessionID == "" {
		var err error
		syncSessionID, err = newUUIDv7(now)
		if err != nil {
			return err
		}
	} else if !uuidV7Pattern.MatchString(syncSessionID) {
		return errors.New("invalid Edge sync session identity")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active bool
	err = tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM connectivity.edge_nodes n
  JOIN connectivity.sessions s ON s.tenant_id=n.tenant_id AND s.integration_instance_id=n.integration_instance_id
  WHERE n.tenant_id=$1::uuid AND n.id=$2::uuid AND n.status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED')
    AND s.id=$3::uuid AND s.status='ACTIVE' AND s.opened_at <= $4 AND s.expires_at > $4
)
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.TrimSpace(input.ConnectivitySessionID), now).Scan(&active)
	if err != nil {
		return fmt.Errorf("authorize Edge sync session: %w", err)
	}
	if !active {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.edge_sync_sessions
SET status='CLOSED', closed_at=$3, close_reason='REPLACED_SESSION', revision=revision+1, updated_at=$3
WHERE tenant_id=$1::uuid AND edge_node_id=$2::uuid AND closed_at IS NULL
`, store.tenantID, strings.TrimSpace(input.EdgeNodeID), now); err != nil {
		return fmt.Errorf("close replaced Edge sync session: %w", err)
	}
	var snapshotRevision any
	if input.SnapshotRevision > 0 {
		snapshotRevision = input.SnapshotRevision
	}
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_sync_sessions (
  id, tenant_id, edge_node_id, connectivity_session_id, status, snapshot_revision,
  snapshot_resume_chunk, delivery_cursor, opened_at, closed_at, close_reason, revision, updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,NULL,NULL,1,$9)
`, syncSessionID, store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.TrimSpace(input.ConnectivitySessionID), input.Status,
		snapshotRevision, input.SnapshotResumeChunk, input.DeliveryCursor, now)
	if err != nil {
		return fmt.Errorf("open Edge sync session: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) CloseEdgeSyncSession(ctx context.Context, syncSessionID, reason string) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(syncSessionID)) || strings.TrimSpace(reason) == "" {
		return errors.New("invalid Edge sync-session close")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_sync_sessions
SET status='CLOSED', closed_at=$3, close_reason=$4, revision=revision+1, updated_at=$3
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED') AND closed_at IS NULL
`, store.tenantID, strings.TrimSpace(syncSessionID), now, strings.TrimSpace(reason))
	if err != nil {
		return fmt.Errorf("close Edge sync session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

type OTACampaignAction string

const (
	OTACampaignStart   OTACampaignAction = "START"
	OTACampaignPause   OTACampaignAction = "PAUSE"
	OTACampaignResume  OTACampaignAction = "RESUME"
	OTACampaignAdvance OTACampaignAction = "ADVANCE"
	OTACampaignAbort   OTACampaignAction = "ABORT"
)

type OTACampaignState struct {
	Status    string
	WaveIndex int
	Revision  uint64
}

func (store *Store) TransitionOTACampaign(ctx context.Context, campaignID string, expectedRevision uint64, action OTACampaignAction) (OTACampaignState, error) {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(campaignID)) || expectedRevision == 0 {
		return OTACampaignState{}, errors.New("invalid OTA campaign transition")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return OTACampaignState{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	var wavesJSON []byte
	var waveIndex int
	var revision uint64
	var windowStart, windowEnd time.Time
	err = tx.QueryRow(ctx, `
SELECT status, waves, wave_index, revision, campaign_window_start, campaign_window_end
FROM connectivity.edge_ota_campaigns
WHERE tenant_id=$1::uuid AND id=$2::uuid
FOR UPDATE
`, store.tenantID, strings.TrimSpace(campaignID)).Scan(&status, &wavesJSON, &waveIndex, &revision, &windowStart, &windowEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return OTACampaignState{}, ErrNotFound
	}
	if err != nil {
		return OTACampaignState{}, fmt.Errorf("load OTA campaign: %w", err)
	}
	if revision != expectedRevision {
		return OTACampaignState{}, ErrEdgeFleetStale
	}
	var waves []int
	if err := json.Unmarshal(wavesJSON, &waves); err != nil || len(waves) == 0 || waveIndex < 0 || waveIndex >= len(waves) {
		return OTACampaignState{}, errors.New("stored OTA campaign wave state is invalid")
	}
	now := store.clock().UTC()
	nextStatus := status
	nextWave := waveIndex
	switch action {
	case OTACampaignStart:
		if status != "DRAFT" || now.Before(windowStart) || !now.Before(windowEnd) {
			return OTACampaignState{}, errors.New("OTA campaign cannot start in current state/window")
		}
		nextStatus = "RUNNING"
	case OTACampaignPause:
		if status != "RUNNING" {
			return OTACampaignState{}, errors.New("OTA campaign is not running")
		}
		nextStatus = "PAUSED"
	case OTACampaignResume:
		if status != "PAUSED" || !now.Before(windowEnd) {
			return OTACampaignState{}, errors.New("OTA campaign cannot resume")
		}
		nextStatus = "RUNNING"
	case OTACampaignAdvance:
		if status != "RUNNING" {
			return OTACampaignState{}, errors.New("OTA campaign is not running")
		}
		if waveIndex+1 >= len(waves) {
			nextStatus = "COMPLETED"
		} else {
			nextWave++
		}
	case OTACampaignAbort:
		if status == "COMPLETED" || status == "ABORTED" {
			return OTACampaignState{}, errors.New("OTA campaign is terminal")
		}
		nextStatus = "ABORTED"
	default:
		return OTACampaignState{}, errors.New("unknown OTA campaign action")
	}
	tag, err := tx.Exec(ctx, `
UPDATE connectivity.edge_ota_campaigns
SET status=$4, wave_index=$5, revision=revision+1, updated_at=$6
WHERE tenant_id=$1::uuid AND id=$2::uuid AND revision=$3
`, store.tenantID, strings.TrimSpace(campaignID), expectedRevision, nextStatus, nextWave, now)
	if err != nil {
		return OTACampaignState{}, fmt.Errorf("transition OTA campaign: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return OTACampaignState{}, ErrEdgeFleetStale
	}
	if err := tx.Commit(ctx); err != nil {
		return OTACampaignState{}, err
	}
	return OTACampaignState{Status: nextStatus, WaveIndex: nextWave, Revision: revision + 1}, nil
}

type OTAAssignmentInput struct {
	CampaignID string
	EdgeNodeID string
	TargetWave int
}

func (store *Store) AssignEdgeToOTACampaign(ctx context.Context, input OTAAssignmentInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.CampaignID)) || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) || input.TargetWave < 0 {
		return errors.New("invalid OTA assignment")
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := store.clock().UTC()
	tag, err := tx.Exec(ctx, `
INSERT INTO connectivity.edge_ota_assignments (campaign_id,edge_node_id,tenant_id,status,target_wave,revision,updated_at)
SELECT c.id,n.id,$1::uuid,'PENDING',$4,1,$5
FROM connectivity.edge_ota_campaigns c
JOIN connectivity.edge_nodes n ON n.tenant_id=c.tenant_id
WHERE c.tenant_id=$1::uuid AND c.id=$2::uuid AND n.id=$3::uuid
  AND c.status IN ('DRAFT','RUNNING','PAUSED') AND n.status IN ('ACTIVE','READ_ONLY','UPGRADE_REQUIRED')
  AND c.waves @> to_jsonb(ARRAY[$4]::int[])
`, store.tenantID, strings.TrimSpace(input.CampaignID), strings.TrimSpace(input.EdgeNodeID), input.TargetWave, now)
	if err != nil {
		return fmt.Errorf("assign Edge to OTA campaign: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

type EdgeFleetEventInput struct {
	EdgeNodeID     string
	EventType      string
	SubjectID      string
	EvidenceDigest string
	Evidence       map[string]any
	OccurredAt     time.Time
}

func (store *Store) AppendEdgeFleetEvent(ctx context.Context, input EdgeFleetEventInput) error {
	if store == nil || !uuidV7Pattern.MatchString(strings.TrimSpace(input.EdgeNodeID)) || strings.TrimSpace(input.EventType) == "" ||
		strings.TrimSpace(input.SubjectID) == "" || !sha256Pattern.MatchString(strings.ToLower(strings.TrimSpace(input.EvidenceDigest))) || input.OccurredAt.IsZero() {
		return errors.New("invalid Edge fleet event")
	}
	evidence, err := json.Marshal(input.Evidence)
	if err != nil {
		return err
	}
	eventID, err := newUUIDv7(input.OccurredAt.UTC())
	if err != nil {
		return err
	}
	tx, err := store.beginTenant(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
INSERT INTO connectivity.edge_fleet_events (event_id,tenant_id,edge_node_id,event_type,subject_id,evidence_digest_sha256,evidence,occurred_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8)
`, eventID, store.tenantID, strings.TrimSpace(input.EdgeNodeID), strings.TrimSpace(input.EventType), strings.TrimSpace(input.SubjectID),
		strings.ToLower(strings.TrimSpace(input.EvidenceDigest)), string(evidence), input.OccurredAt.UTC())
	if err != nil {
		return fmt.Errorf("append Edge fleet event: %w", err)
	}
	return tx.Commit(ctx)
}
