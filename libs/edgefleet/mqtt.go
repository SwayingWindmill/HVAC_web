package edgefleet

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const ReplicationSchemaVersion = "1.0"

type ReplicationType string

const (
	ReplicationHandshake             ReplicationType = "HANDSHAKE"
	ReplicationHandshakeResult       ReplicationType = "HANDSHAKE_RESULT"
	ReplicationSnapshotBegin         ReplicationType = "SNAPSHOT_BEGIN"
	ReplicationSnapshotChunk         ReplicationType = "SNAPSHOT_CHUNK"
	ReplicationSnapshotCommit        ReplicationType = "SNAPSHOT_COMMIT"
	ReplicationChangeBatch           ReplicationType = "CHANGE_BATCH"
	ReplicationChangeAck             ReplicationType = "CHANGE_ACK"
	ReplicationQuarantineDisposition ReplicationType = "QUARANTINE_DISPOSITION"
	ReplicationObservedState         ReplicationType = "OBSERVED_STATE"
	ReplicationReleaseStage          ReplicationType = "RELEASE_STAGE"
	ReplicationReleaseResult         ReplicationType = "RELEASE_RESULT"
	ReplicationOTAStage              ReplicationType = "OTA_STAGE"
	ReplicationOTAResult             ReplicationType = "OTA_RESULT"
)

var messageIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ReplicationEnvelope struct {
	SchemaVersion string          `json:"schemaVersion"`
	MessageID     string          `json:"messageId"`
	EdgeID        string          `json:"edgeId"`
	SentAt        int64           `json:"sentAt"`
	Type          ReplicationType `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

type SyncStatus struct {
	Observed                ObservedEdgeState `json:"observed"`
	Runtime                 RuntimeDescriptor `json:"runtime"`
	Health                  string            `json:"health"`
	DriftStatus             string            `json:"driftStatus"`
	DriftReason             string            `json:"driftReason,omitempty"`
	BacklogBytes            int64             `json:"backlogBytes"`
	QuarantineCount         uint64            `json:"quarantineCount"`
	StagingSnapshotRevision uint64            `json:"stagingSnapshotRevision,omitempty"`
	ResumeChunk             int               `json:"resumeChunk,omitempty"`
}

type HandshakeResultPayload struct {
	Result HandshakeResult `json:"result"`
}

type SnapshotCommitPayload struct {
	Meta    SnapshotMeta      `json:"meta"`
	Release SignedEdgeRelease `json:"release"`
}

type ChangeBatchPayload struct {
	Items []DeliveryItem `json:"items"`
}

type QuarantineDispositionPayload struct {
	Cursor         uint64 `json:"cursor"`
	EvidenceDigest string `json:"evidenceDigest"`
}

type ReleaseResultPayload struct {
	Result ReleaseActivationResult `json:"result"`
}

type OTAStagePayload struct {
	Artifact SignedOTAArtifact `json:"artifact"`
}

type OTAResultPayload struct {
	Result OTAActivationResult `json:"result"`
}

func NewReplicationEnvelope(edgeID string, messageType ReplicationType, payload any, now time.Time) (ReplicationEnvelope, error) {
	edgeID = strings.TrimSpace(edgeID)
	if edgeID == "" || now.IsZero() || !validReplicationType(messageType) {
		return ReplicationEnvelope{}, errors.New("edge replication envelope identity, type and timestamp are required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ReplicationEnvelope{}, fmt.Errorf("encode edge replication payload: %w", err)
	}
	if len(encoded) == 0 || encoded[0] != '{' {
		return ReplicationEnvelope{}, errors.New("edge replication payload must be a JSON object")
	}
	messageID, err := newMessageUUIDv7(now)
	if err != nil {
		return ReplicationEnvelope{}, err
	}
	return ReplicationEnvelope{
		SchemaVersion: ReplicationSchemaVersion,
		MessageID:     messageID,
		EdgeID:        edgeID,
		SentAt:        now.UTC().UnixMilli(),
		Type:          messageType,
		Payload:       encoded,
	}, nil
}

func EncodeReplicationEnvelope(envelope ReplicationEnvelope) ([]byte, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode edge replication envelope: %w", err)
	}
	return encoded, nil
}

func DecodeReplicationEnvelope(data []byte, maxBytes int) (ReplicationEnvelope, error) {
	if maxBytes <= 0 || len(data) == 0 || len(data) > maxBytes {
		return ReplicationEnvelope{}, errors.New("edge replication envelope size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(data), int64(maxBytes)))
	decoder.DisallowUnknownFields()
	var envelope ReplicationEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ReplicationEnvelope{}, fmt.Errorf("decode edge replication envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ReplicationEnvelope{}, errors.New("edge replication envelope contains trailing JSON")
	}
	if err := envelope.validate(); err != nil {
		return ReplicationEnvelope{}, err
	}
	return envelope, nil
}

func DecodeReplicationPayload[T any](envelope ReplicationEnvelope) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s payload: %w", envelope.Type, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("decode %s payload: trailing JSON", envelope.Type)
	}
	return value, nil
}

func (replica *Replica) SyncStatus() SyncStatus {
	if replica == nil {
		return SyncStatus{}
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	status := SyncStatus{Observed: replica.state.Observed, Runtime: replica.runtime, Health: "UNKNOWN", DriftStatus: "UNKNOWN"}
	if replica.state.Staging != nil {
		status.StagingSnapshotRevision = replica.state.Staging.Meta.SnapshotRevision
		status.ResumeChunk = firstMissingChunk(replica.state.Staging.Meta.ChunkCount, replica.state.Staging.Chunks)
	}
	return status
}

func (envelope ReplicationEnvelope) validate() error {
	if envelope.SchemaVersion != ReplicationSchemaVersion || !messageIDPattern.MatchString(strings.TrimSpace(envelope.MessageID)) ||
		strings.TrimSpace(envelope.EdgeID) == "" || envelope.SentAt <= 0 || !validReplicationType(envelope.Type) || len(envelope.Payload) == 0 || !json.Valid(envelope.Payload) {
		return errors.New("edge replication envelope is invalid")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Payload, &object); err != nil || object == nil {
		return errors.New("edge replication payload must be a JSON object")
	}
	return nil
}

func validReplicationType(value ReplicationType) bool {
	switch value {
	case ReplicationHandshake, ReplicationHandshakeResult, ReplicationSnapshotBegin, ReplicationSnapshotChunk,
		ReplicationSnapshotCommit, ReplicationChangeBatch, ReplicationChangeAck, ReplicationQuarantineDisposition,
		ReplicationObservedState, ReplicationReleaseStage, ReplicationReleaseResult, ReplicationOTAStage, ReplicationOTAResult:
		return true
	default:
		return false
	}
}

func newMessageUUIDv7(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	millis := uint64(now.UTC().UnixMilli())
	raw[0] = byte(millis >> 40)
	raw[1] = byte(millis >> 32)
	raw[2] = byte(millis >> 24)
	raw[3] = byte(millis >> 16)
	raw[4] = byte(millis >> 8)
	raw[5] = byte(millis)
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
