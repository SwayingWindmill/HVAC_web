package edgefleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrAuthorityViolation  = errors.New("edge fleet authority violation")
	ErrDeliveryQuarantined = errors.New("edge fleet delivery quarantined")
	ErrSignatureInvalid    = errors.New("edge fleet signature invalid")
	ErrRuntimeIncompatible = errors.New("edge fleet runtime incompatible")
	ErrCapabilityMissing   = errors.New("edge fleet required capability missing")
	ErrPreflightFailed     = errors.New("edge fleet local preflight failed")
	ErrOfflineCapacity     = errors.New("edge fleet offline capacity exhausted")
)

type Authority string

const (
	AuthorityCloud Authority = "CLOUD"
	AuthorityEdge  Authority = "EDGE"
)

type OwnerDomain string

const (
	OwnerRegistry         OwnerDomain = "REGISTRY"
	OwnerProfile          OwnerDomain = "PROFILE"
	OwnerRule             OwnerDomain = "RULE"
	OwnerSchedule         OwnerDomain = "SCHEDULE"
	OwnerSafetyPolicy     OwnerDomain = "SAFETY_POLICY"
	OwnerDriverConfig     OwnerDomain = "DRIVER_CONFIG"
	OwnerObservedManifest OwnerDomain = "OBSERVED_MANIFEST"
	OwnerTelemetry        OwnerDomain = "TELEMETRY"
	OwnerControlEvidence  OwnerDomain = "CONTROL_EVIDENCE"
	OwnerAuditEvidence    OwnerDomain = "AUDIT_EVIDENCE"
)

func OwnerAuthority(domain OwnerDomain) (Authority, bool) {
	switch domain {
	case OwnerRegistry, OwnerProfile, OwnerRule, OwnerSchedule, OwnerSafetyPolicy, OwnerDriverConfig:
		return AuthorityCloud, true
	case OwnerObservedManifest, OwnerTelemetry, OwnerControlEvidence, OwnerAuditEvidence:
		return AuthorityEdge, true
	default:
		return "", false
	}
}

type RuntimeDescriptor struct {
	RuntimeVersion        string   `json:"runtimeVersion"`
	ProtocolSchemaVersion int      `json:"protocolSchemaVersion"`
	Capabilities          []string `json:"capabilities,omitempty"`
	MaxPayloadBytes       int      `json:"maxPayloadBytes,omitempty"`
}

func (descriptor RuntimeDescriptor) normalized() RuntimeDescriptor {
	descriptor.RuntimeVersion = strings.TrimSpace(descriptor.RuntimeVersion)
	descriptor.Capabilities = normalizedStrings(descriptor.Capabilities)
	return descriptor
}

func (descriptor RuntimeDescriptor) hasCapability(capability string) bool {
	capability = strings.TrimSpace(capability)
	index := sort.SearchStrings(descriptor.Capabilities, capability)
	return index < len(descriptor.Capabilities) && descriptor.Capabilities[index] == capability
}

type ProjectionItem struct {
	OwnerDomain   OwnerDomain     `json:"ownerDomain"`
	EntityID      string          `json:"entityId"`
	OwnerRevision uint64          `json:"ownerRevision"`
	Deleted       bool            `json:"deleted,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

func (item ProjectionItem) validateDownlink() error {
	authority, known := OwnerAuthority(item.OwnerDomain)
	if !known || authority != AuthorityCloud {
		return ErrAuthorityViolation
	}
	if strings.TrimSpace(item.EntityID) == "" || item.OwnerRevision == 0 {
		return errors.New("projection item identity and owner revision are required")
	}
	if item.Deleted {
		if len(item.Payload) != 0 && string(item.Payload) != "null" {
			return errors.New("tombstone must not carry a replacement payload")
		}
		return nil
	}
	if len(item.Payload) == 0 || !json.Valid(item.Payload) {
		return errors.New("projection payload must be valid JSON")
	}
	return nil
}

func projectionKey(domain OwnerDomain, entityID string) string {
	return string(domain) + "\x00" + strings.TrimSpace(entityID)
}

type SnapshotMeta struct {
	SnapshotRevision   uint64 `json:"snapshotRevision"`
	DesiredRevision    uint64 `json:"desiredRevision"`
	ReleaseID          string `json:"releaseId"`
	ChunkCount         int    `json:"chunkCount"`
	FinalDigest        string `json:"finalDigest"`
	BaseDeliveryCursor uint64 `json:"baseDeliveryCursor"`
}

type SnapshotChunk struct {
	SnapshotRevision uint64           `json:"snapshotRevision"`
	Index            int              `json:"index"`
	Digest           string           `json:"digest"`
	Items            []ProjectionItem `json:"items"`
}

type DeliveryStatus string

const (
	DeliveryAcked       DeliveryStatus = "ACKED"
	DeliveryQuarantined DeliveryStatus = "QUARANTINED"
	DeliveryDisposed    DeliveryStatus = "DISPOSED"
)

type DeliveryItem struct {
	Cursor         uint64          `json:"cursor"`
	DeliveryID     string          `json:"deliveryId"`
	OwnerDomain    OwnerDomain     `json:"ownerDomain"`
	OrderingKey    string          `json:"orderingKey"`
	SourceRevision uint64          `json:"sourceRevision"`
	PayloadDigest  string          `json:"payloadDigest"`
	Deleted        bool            `json:"deleted,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

type DeliveryAck struct {
	Cursor               uint64         `json:"cursor"`
	DeliveryID           string         `json:"deliveryId"`
	Status               DeliveryStatus `json:"status"`
	AppliedOwnerRevision uint64         `json:"appliedOwnerRevision,omitempty"`
	Reason               string         `json:"reason,omitempty"`
	DispositionEvidence  string         `json:"dispositionEvidence,omitempty"`
	PayloadDigest        string         `json:"payloadDigest"`
}

type ObservedEdgeState struct {
	ActiveSnapshotRevision uint64        `json:"activeSnapshotRevision"`
	DesiredRevision        uint64        `json:"desiredRevision"`
	DeliveryCursor         uint64        `json:"deliveryCursor"`
	ActiveReleaseID        string        `json:"activeReleaseId,omitempty"`
	StagedReleaseID        string        `json:"stagedReleaseId,omitempty"`
	PreviousReleaseID      string        `json:"previousReleaseId,omitempty"`
	ActiveOTAArtifactID    string        `json:"activeOtaArtifactId,omitempty"`
	StagedOTAArtifactID    string        `json:"stagedOtaArtifactId,omitempty"`
	PreviousOTAArtifactID  string        `json:"previousOtaArtifactId,omitempty"`
	ReportedConfigRevision uint64        `json:"reportedConfigRevision"`
	ManifestDigest         string        `json:"manifestDigest,omitempty"`
	CapacityState          CapacityState `json:"capacityState,omitempty"`
}

type ReconnectMode string

const (
	ReconnectSnapshot       ReconnectMode = "SNAPSHOT"
	ReconnectSnapshotResume ReconnectMode = "SNAPSHOT_RESUME"
	ReconnectDelta          ReconnectMode = "DELTA"
)

type ReconnectPlan struct {
	Mode             ReconnectMode `json:"mode"`
	SnapshotRevision uint64        `json:"snapshotRevision,omitempty"`
	ResumeChunk      int           `json:"resumeChunk,omitempty"`
	DeliveryCursor   uint64        `json:"deliveryCursor,omitempty"`
}

type HandshakeRequest struct {
	EdgeID                string   `json:"edgeId"`
	RuntimeVersion        string   `json:"runtimeVersion"`
	ProtocolSchemaVersion int      `json:"protocolSchemaVersion"`
	Capabilities          []string `json:"capabilities"`
	MaxPayloadBytes       int      `json:"maxPayloadBytes"`
	CredentialRevision    uint64   `json:"credentialRevision"`
}

type HandshakePolicy struct {
	ProtocolSchemaVersion int
	MinRuntimeVersion     string
	MaxRuntimeVersion     string
	RequiredCapabilities  []string
	MaxPayloadBytes       int
}

type HandshakeStatus string

const (
	HandshakeAccepted        HandshakeStatus = "ACCEPTED"
	HandshakeReadOnly        HandshakeStatus = "READ_ONLY"
	HandshakeUpgradeRequired HandshakeStatus = "UPGRADE_REQUIRED"
	HandshakeRejected        HandshakeStatus = "REJECTED"
)

type HandshakeResult struct {
	Status             HandshakeStatus `json:"status"`
	NegotiatedMaxBytes int             `json:"negotiatedMaxBytes"`
	Reason             string          `json:"reason,omitempty"`
}

func NegotiateHandshake(request HandshakeRequest, policy HandshakePolicy) HandshakeResult {
	if strings.TrimSpace(request.EdgeID) == "" || request.CredentialRevision == 0 || request.MaxPayloadBytes <= 0 {
		return HandshakeResult{Status: HandshakeRejected, Reason: "INVALID_IDENTITY_OR_LIMIT"}
	}
	if request.ProtocolSchemaVersion != policy.ProtocolSchemaVersion {
		return HandshakeResult{Status: HandshakeUpgradeRequired, Reason: "PROTOCOL_SCHEMA_INCOMPATIBLE"}
	}
	if !versionInRange(request.RuntimeVersion, policy.MinRuntimeVersion, policy.MaxRuntimeVersion) {
		return HandshakeResult{Status: HandshakeUpgradeRequired, Reason: "RUNTIME_INCOMPATIBLE"}
	}
	capabilities := normalizedStrings(request.Capabilities)
	for _, required := range normalizedStrings(policy.RequiredCapabilities) {
		index := sort.SearchStrings(capabilities, required)
		if index >= len(capabilities) || capabilities[index] != required {
			return HandshakeResult{Status: HandshakeReadOnly, Reason: "CAPABILITY_MISSING:" + required}
		}
	}
	negotiated := request.MaxPayloadBytes
	if policy.MaxPayloadBytes > 0 && policy.MaxPayloadBytes < negotiated {
		negotiated = policy.MaxPayloadBytes
	}
	return HandshakeResult{Status: HandshakeAccepted, NegotiatedMaxBytes: negotiated}
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func versionInRange(current, minimum, maximum string) bool {
	currentParts, ok := parseVersion(current)
	if !ok {
		return false
	}
	if strings.TrimSpace(minimum) != "" {
		minimumParts, valid := parseVersion(minimum)
		if !valid || compareVersion(currentParts, minimumParts) < 0 {
			return false
		}
	}
	if strings.TrimSpace(maximum) != "" {
		maximumParts, valid := parseVersion(maximum)
		if !valid || compareVersion(currentParts, maximumParts) > 0 {
			return false
		}
	}
	return true
}

func parseVersion(value string) ([3]int, bool) {
	var parsed [3]int
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(value, "v")), ".")
	if len(parts) != 3 {
		return parsed, false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return parsed, false
		}
		parsed[index] = number
	}
	return parsed, true
}

func compareVersion(left, right [3]int) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical edge fleet payload: %w", err)
	}
	return encoded, nil
}
