package edgefleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type replicaDiskState struct {
	Observed        ObservedEdgeState         `json:"observed"`
	Active          map[string]ProjectionItem `json:"active,omitempty"`
	AppliedRevision map[string]uint64         `json:"appliedRevision,omitempty"`
	DeliveryResults map[uint64]DeliveryAck    `json:"deliveryResults,omitempty"`
	DeliveryIDs     map[string]uint64         `json:"deliveryIds,omitempty"`
	Staging         *stagingSnapshot          `json:"staging,omitempty"`
	StagedRelease   *SignedEdgeRelease        `json:"stagedRelease,omitempty"`
}

type stagingSnapshot struct {
	Meta   SnapshotMeta          `json:"meta"`
	Chunks map[int]SnapshotChunk `json:"chunks"`
}

type Replica struct {
	mu          sync.Mutex
	directory   string
	runtime     RuntimeDescriptor
	state       replicaDiskState
	trustedKeys map[string][]byte
}

func OpenReplica(directory string, runtime RuntimeDescriptor) (*Replica, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("edge fleet replica directory is required")
	}
	runtime = runtime.normalized()
	if runtime.ProtocolSchemaVersion <= 0 || strings.TrimSpace(runtime.RuntimeVersion) == "" {
		return nil, errors.New("runtime version and protocol schema version are required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("create edge fleet replica directory: %w", err)
	}
	replica := &Replica{directory: directory, runtime: runtime, trustedKeys: map[string][]byte{}}
	replica.state = replicaDiskState{
		Observed:        ObservedEdgeState{CapacityState: CapacityNormal},
		Active:          map[string]ProjectionItem{},
		AppliedRevision: map[string]uint64{},
		DeliveryResults: map[uint64]DeliveryAck{},
		DeliveryIDs:     map[string]uint64{},
	}
	data, err := os.ReadFile(replica.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return replica, nil
		}
		return nil, fmt.Errorf("read edge fleet replica state: %w", err)
	}
	if err := json.Unmarshal(data, &replica.state); err != nil {
		return nil, fmt.Errorf("decode edge fleet replica state: %w", err)
	}
	replica.ensureMaps()
	return replica, nil
}

func (replica *Replica) statePath() string {
	return filepath.Join(replica.directory, "replica-state.json")
}

func (replica *Replica) ensureMaps() {
	if replica.state.Observed.CapacityState == "" {
		replica.state.Observed.CapacityState = CapacityNormal
	}
	if replica.state.Active == nil {
		replica.state.Active = map[string]ProjectionItem{}
	}
	if replica.state.AppliedRevision == nil {
		replica.state.AppliedRevision = map[string]uint64{}
	}
	if replica.state.DeliveryResults == nil {
		replica.state.DeliveryResults = map[uint64]DeliveryAck{}
	}
	if replica.state.DeliveryIDs == nil {
		replica.state.DeliveryIDs = map[string]uint64{}
	}
	if replica.state.Staging != nil && replica.state.Staging.Chunks == nil {
		replica.state.Staging.Chunks = map[int]SnapshotChunk{}
	}
}

func (replica *Replica) persistLocked() error {
	encoded, err := json.MarshalIndent(replica.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode edge fleet replica state: %w", err)
	}
	temporary := replica.statePath() + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open edge fleet replica temporary state: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write edge fleet replica state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync edge fleet replica state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close edge fleet replica state: %w", err)
	}
	if err := os.Rename(temporary, replica.statePath()); err != nil {
		return fmt.Errorf("activate edge fleet replica state: %w", err)
	}
	return nil
}

func (replica *Replica) ObservedState() ObservedEdgeState {
	if replica == nil {
		return ObservedEdgeState{}
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	return replica.state.Observed
}

func (replica *Replica) BeginSnapshot(meta SnapshotMeta) error {
	if replica == nil {
		return errors.New("edge fleet replica is unavailable")
	}
	if meta.SnapshotRevision == 0 || meta.DesiredRevision == 0 || strings.TrimSpace(meta.ReleaseID) == "" || meta.ChunkCount < 0 || !isSHA256(meta.FinalDigest) {
		return errors.New("invalid snapshot metadata")
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	if replica.state.Staging != nil && replica.state.Staging.Meta.SnapshotRevision == meta.SnapshotRevision {
		if replica.state.Staging.Meta.FinalDigest != meta.FinalDigest || replica.state.Staging.Meta.DesiredRevision != meta.DesiredRevision {
			return errors.New("snapshot revision already staged with different metadata")
		}
		return nil
	}
	replica.state.Staging = &stagingSnapshot{Meta: meta, Chunks: map[int]SnapshotChunk{}}
	return replica.persistLocked()
}

func (replica *Replica) StageSnapshotChunk(chunk SnapshotChunk) error {
	if replica == nil {
		return errors.New("edge fleet replica is unavailable")
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	staging := replica.state.Staging
	if staging == nil || chunk.SnapshotRevision != staging.Meta.SnapshotRevision {
		return errors.New("snapshot chunk does not match staged snapshot")
	}
	if chunk.Index < 0 || chunk.Index >= staging.Meta.ChunkCount {
		return errors.New("snapshot chunk index is outside staged snapshot")
	}
	for _, item := range chunk.Items {
		if err := item.validateDownlink(); err != nil {
			return err
		}
	}
	digest, err := snapshotChunkDigest(chunk)
	if err != nil {
		return err
	}
	if digest != chunk.Digest {
		return errors.New("snapshot chunk digest mismatch")
	}
	if existing, exists := staging.Chunks[chunk.Index]; exists {
		if existing.Digest != chunk.Digest {
			return errors.New("snapshot chunk index already staged with different digest")
		}
		return nil
	}
	staging.Chunks[chunk.Index] = chunk
	return replica.persistLocked()
}

func (replica *Replica) ActivateSnapshotRelease(meta SnapshotMeta, release SignedEdgeRelease, manifestDigest string, healthCheck func(EdgeReleasePayload) error) (ReleaseActivationResult, error) {
	if replica == nil {
		return ReleaseActivationResult{}, errors.New("edge fleet replica is unavailable")
	}
	if err := replica.verifyEdgeRelease(release); err != nil {
		return ReleaseActivationResult{}, err
	}
	if !isSHA256(manifestDigest) {
		return ReleaseActivationResult{}, errors.New("observed manifest digest is required")
	}
	if release.Payload.ReleaseID != meta.ReleaseID || release.Payload.DesiredConfigRevision != meta.DesiredRevision {
		return ReleaseActivationResult{}, errors.New("signed release does not match staged snapshot revision")
	}

	replica.mu.Lock()
	staging := replica.state.Staging
	if staging == nil || staging.Meta.SnapshotRevision != meta.SnapshotRevision || staging.Meta.FinalDigest != meta.FinalDigest {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, errors.New("snapshot activation does not match staged snapshot")
	}
	if replica.state.StagedRelease == nil || replica.state.StagedRelease.Digest != release.Digest || replica.state.Observed.StagedReleaseID != release.Payload.ReleaseID {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, errors.New("signed release must be staged before snapshot activation")
	}
	if len(staging.Chunks) != meta.ChunkCount {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, errors.New("snapshot activation is incomplete")
	}
	chunks := make([]SnapshotChunk, meta.ChunkCount)
	for index := 0; index < meta.ChunkCount; index++ {
		chunk, exists := staging.Chunks[index]
		if !exists {
			replica.mu.Unlock()
			return ReleaseActivationResult{}, fmt.Errorf("snapshot activation missing chunk %d", index)
		}
		chunks[index] = chunk
	}
	finalDigest, err := snapshotFinalDigest(meta, chunks)
	if err != nil {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, err
	}
	if finalDigest != meta.FinalDigest {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, errors.New("snapshot final digest mismatch")
	}

	rollbackBytes, err := json.Marshal(replica.state)
	if err != nil {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, fmt.Errorf("snapshot rollback state encode: %w", err)
	}
	var rollback replicaDiskState
	if err := json.Unmarshal(rollbackBytes, &rollback); err != nil {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, fmt.Errorf("snapshot rollback state decode: %w", err)
	}

	active := make(map[string]ProjectionItem)
	appliedRevision := make(map[string]uint64)
	for _, chunk := range chunks {
		for _, item := range chunk.Items {
			key := projectionKey(item.OwnerDomain, item.EntityID)
			if item.Deleted {
				continue
			}
			active[key] = item
			appliedRevision[key] = item.OwnerRevision
		}
	}
	previousRelease := replica.state.Observed.ActiveReleaseID
	replica.state.Active = active
	replica.state.AppliedRevision = appliedRevision
	replica.state.DeliveryResults = map[uint64]DeliveryAck{}
	replica.state.DeliveryIDs = map[string]uint64{}
	replica.state.Observed.PreviousReleaseID = previousRelease
	replica.state.Observed.ActiveReleaseID = release.Payload.ReleaseID
	replica.state.Observed.StagedReleaseID = ""
	replica.state.Observed.ActiveSnapshotRevision = meta.SnapshotRevision
	replica.state.Observed.DesiredRevision = meta.DesiredRevision
	replica.state.Observed.DeliveryCursor = meta.BaseDeliveryCursor
	replica.state.Observed.ReportedConfigRevision = release.Payload.DesiredConfigRevision
	replica.state.Observed.ManifestDigest = manifestDigest
	replica.state.Staging = nil
	replica.state.StagedRelease = nil
	if err := replica.persistLocked(); err != nil {
		replica.mu.Unlock()
		return ReleaseActivationResult{}, err
	}
	replica.mu.Unlock()

	if healthCheck != nil {
		if err := healthCheck(release.Payload); err != nil {
			replica.mu.Lock()
			replica.state = rollback
			replica.ensureMaps()
			replica.state.Staging = nil
			replica.state.StagedRelease = nil
			replica.state.Observed.StagedReleaseID = ""
			replica.state.Observed.PreviousReleaseID = release.Payload.ReleaseID
			persistErr := replica.persistLocked()
			activeRelease := replica.state.Observed.ActiveReleaseID
			replica.mu.Unlock()
			if persistErr != nil {
				return ReleaseActivationResult{}, persistErr
			}
			return ReleaseActivationResult{ActiveReleaseID: activeRelease, PreviousReleaseID: release.Payload.ReleaseID, RolledBack: true, Reason: err.Error()}, nil
		}
	}
	return ReleaseActivationResult{ActiveReleaseID: release.Payload.ReleaseID, PreviousReleaseID: previousRelease}, nil
}

func (replica *Replica) ReconnectPlan(desiredSnapshotRevision, retainedDeliveryFloor uint64) ReconnectPlan {
	if replica == nil {
		return ReconnectPlan{Mode: ReconnectSnapshot, SnapshotRevision: desiredSnapshotRevision}
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	if staging := replica.state.Staging; staging != nil && staging.Meta.SnapshotRevision == desiredSnapshotRevision {
		return ReconnectPlan{
			Mode:             ReconnectSnapshotResume,
			SnapshotRevision: desiredSnapshotRevision,
			ResumeChunk:      firstMissingChunk(staging.Meta.ChunkCount, staging.Chunks),
		}
	}
	if replica.state.Observed.ActiveSnapshotRevision != desiredSnapshotRevision || replica.state.Observed.DeliveryCursor < retainedDeliveryFloor {
		return ReconnectPlan{Mode: ReconnectSnapshot, SnapshotRevision: desiredSnapshotRevision}
	}
	return ReconnectPlan{Mode: ReconnectDelta, SnapshotRevision: desiredSnapshotRevision, DeliveryCursor: replica.state.Observed.DeliveryCursor}
}

func firstMissingChunk(count int, chunks map[int]SnapshotChunk) int {
	for index := 0; index < count; index++ {
		if _, exists := chunks[index]; !exists {
			return index
		}
	}
	return count
}

func (replica *Replica) ApplyDelivery(item DeliveryItem) (DeliveryAck, error) {
	if replica == nil {
		return DeliveryAck{}, errors.New("edge fleet replica is unavailable")
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	if item.Cursor == 0 || strings.TrimSpace(item.DeliveryID) == "" || strings.TrimSpace(item.OrderingKey) == "" || item.SourceRevision == 0 {
		return replica.quarantineLocked(item, "INVALID_DELIVERY", ErrDeliveryQuarantined)
	}
	if existingCursor, exists := replica.state.DeliveryIDs[item.DeliveryID]; exists {
		ack := replica.state.DeliveryResults[existingCursor]
		if existingCursor != item.Cursor || ack.PayloadDigest != item.PayloadDigest {
			return replica.quarantineLocked(item, "DELIVERY_ID_CONFLICT", ErrDeliveryQuarantined)
		}
		return ack, nil
	}
	if existing, exists := replica.state.DeliveryResults[item.Cursor]; exists {
		if existing.DeliveryID != item.DeliveryID || existing.PayloadDigest != item.PayloadDigest {
			return replica.quarantineLocked(item, "CURSOR_CONFLICT", ErrDeliveryQuarantined)
		}
		return existing, nil
	}
	authority, known := OwnerAuthority(item.OwnerDomain)
	if !known || authority != AuthorityCloud {
		return replica.quarantineLocked(item, "CLOUD_NOT_AUTHORITY", ErrAuthorityViolation)
	}
	if !isSHA256(item.PayloadDigest) || sha256Hex(item.Payload) != item.PayloadDigest {
		return replica.quarantineLocked(item, "PAYLOAD_DIGEST_MISMATCH", ErrDeliveryQuarantined)
	}
	projection := ProjectionItem{OwnerDomain: item.OwnerDomain, EntityID: item.OrderingKey, OwnerRevision: item.SourceRevision, Deleted: item.Deleted, Payload: item.Payload}
	if err := projection.validateDownlink(); err != nil {
		return replica.quarantineLocked(item, "INVALID_PROJECTION", ErrDeliveryQuarantined)
	}
	key := projectionKey(item.OwnerDomain, item.OrderingKey)
	currentRevision := replica.state.AppliedRevision[key]
	if currentRevision > item.SourceRevision {
		return replica.quarantineLocked(item, "OWNER_REVISION_REGRESSION", ErrDeliveryQuarantined)
	}
	if currentRevision < item.SourceRevision {
		if item.Deleted {
			delete(replica.state.Active, key)
		} else {
			replica.state.Active[key] = projection
		}
		replica.state.AppliedRevision[key] = item.SourceRevision
	}
	ack := DeliveryAck{Cursor: item.Cursor, DeliveryID: item.DeliveryID, Status: DeliveryAcked, AppliedOwnerRevision: item.SourceRevision, PayloadDigest: item.PayloadDigest}
	replica.state.DeliveryResults[item.Cursor] = ack
	replica.state.DeliveryIDs[item.DeliveryID] = item.Cursor
	replica.advanceCursorLocked()
	if err := replica.persistLocked(); err != nil {
		return DeliveryAck{}, err
	}
	return ack, nil
}

func (replica *Replica) quarantineLocked(item DeliveryItem, reason string, resultErr error) (DeliveryAck, error) {
	ack := DeliveryAck{Cursor: item.Cursor, DeliveryID: item.DeliveryID, Status: DeliveryQuarantined, Reason: reason, PayloadDigest: item.PayloadDigest}
	if item.Cursor > 0 {
		replica.state.DeliveryResults[item.Cursor] = ack
	}
	if strings.TrimSpace(item.DeliveryID) != "" && item.Cursor > 0 {
		replica.state.DeliveryIDs[item.DeliveryID] = item.Cursor
	}
	if err := replica.persistLocked(); err != nil {
		return DeliveryAck{}, err
	}
	return ack, resultErr
}

func (replica *Replica) DisposeQuarantine(cursor uint64, evidenceDigest string) error {
	if replica == nil {
		return errors.New("edge fleet replica is unavailable")
	}
	if cursor == 0 || !isSHA256(evidenceDigest) {
		return errors.New("quarantine cursor and disposition evidence digest are required")
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	ack, exists := replica.state.DeliveryResults[cursor]
	if !exists || ack.Status != DeliveryQuarantined {
		return errors.New("delivery cursor is not quarantined")
	}
	ack.Status = DeliveryDisposed
	ack.DispositionEvidence = evidenceDigest
	replica.state.DeliveryResults[cursor] = ack
	replica.advanceCursorLocked()
	return replica.persistLocked()
}

func (replica *Replica) advanceCursorLocked() {
	for next := replica.state.Observed.DeliveryCursor + 1; ; next++ {
		ack, exists := replica.state.DeliveryResults[next]
		if !exists || (ack.Status != DeliveryAcked && ack.Status != DeliveryDisposed) {
			return
		}
		replica.state.Observed.DeliveryCursor = next
	}
}

func (replica *Replica) ProjectionItems() []ProjectionItem {
	if replica == nil {
		return nil
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	keys := make([]string, 0, len(replica.state.Active))
	for key := range replica.state.Active {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]ProjectionItem, 0, len(keys))
	for _, key := range keys {
		items = append(items, replica.state.Active[key])
	}
	return items
}
