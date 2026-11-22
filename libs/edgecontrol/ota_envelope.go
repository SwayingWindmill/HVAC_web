package edgecontrol

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ShadowReplicationTypeOTAStage  = "OTA_STAGE"
	ShadowReplicationTypeOTAResult = "OTA_RESULT"

	OTAActionApply    = "APPLY"
	OTAActionRollback = "ROLLBACK"

	OTAComponentEdgeGateway = "EDGE_GATEWAY"
	OTAComponentFirmware    = "FIRMWARE"

	OTASignatureAlgEd25519 = "ED25519"
	otaSignaturePrefix     = "HVAC-OTA-V1"

	OTAStagePending        = "PENDING"
	OTAStageDownloading    = "DOWNLOADING"
	OTAStageVerifying      = "VERIFYING"
	OTAStageApplying       = "APPLYING"
	OTAStageHealthChecking = "HEALTH_CHECKING"
	OTAStageSucceeded      = "SUCCEEDED"
	OTAStageRolledBack     = "ROLLED_BACK"
	OTAStageFailed         = "FAILED"
)

// OTADownlinkTopic returns the per-gateway OTA rollout stage directive topic.
func OTADownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/ota"
}

// OTAUplinkTopic returns the per-gateway OTA execution progress and result topic.
func OTAUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/ota"
}

// OTAStagePayload mirrors the OTA_STAGE payload in edge-replication-envelope.v1.schema.json.
type OTAStagePayload struct {
	CampaignID            string   `json:"campaignId"`
	TaskID                string   `json:"taskId"`
	PackageID             string   `json:"packageId"`
	Version               string   `json:"version"`
	Component             string   `json:"component"`
	TargetOS              string   `json:"targetOs"`
	TargetArch            string   `json:"targetArch"`
	HardwareModels        []string `json:"hardwareModels,omitempty"`
	MinCompatibleVersion  string   `json:"minCompatibleVersion,omitempty"`
	DownloadURL           string   `json:"downloadUrl"`
	FileSizeBytes         int64    `json:"fileSizeBytes"`
	SHA256                string   `json:"sha256"`
	SignatureAlg          string   `json:"signatureAlg"`
	SignatureB64          string   `json:"signatureB64"`
	SigningKeyID          string   `json:"signingKeyId"`
	HealthCheckTimeoutSec int      `json:"healthCheckTimeoutSec"`
	Action                string   `json:"action"`
}

// OTAResultPayload mirrors the OTA_RESULT payload in edge-replication-envelope.v1.schema.json.
type OTAResultPayload struct {
	CampaignID     string `json:"campaignId"`
	TaskID         string `json:"taskId"`
	PackageID      string `json:"packageId"`
	Stage          string `json:"stage"`
	ProgressPct    int    `json:"progressPct"`
	CurrentVersion string `json:"currentVersion"`
	TargetVersion  string `json:"targetVersion"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	OccurredAt     int64  `json:"occurredAt"`
}

// NormalizeTargetArch maps uname / Go GOARCH tokens onto canonical edge architecture strings.
func NormalizeTargetArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "arm", "armv7", "armv7l", "armhf":
		return "armv7l"
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x86_64", "x64":
		return "amd64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

// Validate checks all OTA_STAGE invariants against edge-replication-envelope.v1.schema.json.
func (p OTAStagePayload) Validate() error {
	if !IsReplicationUUID(p.CampaignID) || !IsReplicationUUID(p.TaskID) || !IsReplicationUUID(p.PackageID) {
		return errors.New("OTA_STAGE requires valid campaignId, taskId, and packageId UUIDs")
	}
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("OTA_STAGE version is required")
	}
	switch p.Component {
	case OTAComponentEdgeGateway, OTAComponentFirmware:
	default:
		return fmt.Errorf("unsupported OTA component %q", p.Component)
	}
	if strings.TrimSpace(p.TargetOS) == "" || strings.TrimSpace(p.TargetArch) == "" {
		return errors.New("OTA_STAGE targetOs and targetArch are required")
	}
	if strings.TrimSpace(p.DownloadURL) == "" {
		return errors.New("OTA_STAGE downloadUrl is required")
	}
	if p.FileSizeBytes < 1 {
		return errors.New("OTA_STAGE fileSizeBytes must be >= 1")
	}
	if !IsLowerSHA256Hex(p.SHA256) {
		return errors.New("OTA_STAGE sha256 must be 64 lowercase hex characters")
	}
	if p.SignatureAlg != OTASignatureAlgEd25519 {
		return fmt.Errorf("unsupported OTA signatureAlg %q", p.SignatureAlg)
	}
	if strings.TrimSpace(p.SignatureB64) == "" || strings.TrimSpace(p.SigningKeyID) == "" {
		return errors.New("OTA_STAGE signatureB64 and signingKeyId are required")
	}
	if p.HealthCheckTimeoutSec < 1 || p.HealthCheckTimeoutSec > 3600 {
		return fmt.Errorf("OTA_STAGE healthCheckTimeoutSec %d must be in [1,3600]", p.HealthCheckTimeoutSec)
	}
	switch p.Action {
	case OTAActionApply, OTAActionRollback:
	default:
		return fmt.Errorf("unsupported OTA action %q", p.Action)
	}
	return nil
}

// Validate checks all OTA_RESULT invariants against edge-replication-envelope.v1.schema.json.
func (p OTAResultPayload) Validate() error {
	if !IsReplicationUUID(p.CampaignID) || !IsReplicationUUID(p.TaskID) || !IsReplicationUUID(p.PackageID) {
		return errors.New("OTA_RESULT requires valid campaignId, taskId, and packageId UUIDs")
	}
	switch p.Stage {
	case OTAStageDownloading, OTAStageVerifying, OTAStageApplying, OTAStageHealthChecking,
		OTAStageSucceeded, OTAStageRolledBack, OTAStageFailed:
	default:
		return fmt.Errorf("unsupported OTA_RESULT stage %q", p.Stage)
	}
	if p.ProgressPct < 0 || p.ProgressPct > 100 {
		return fmt.Errorf("OTA_RESULT progressPct %d must be in [0,100]", p.ProgressPct)
	}
	if strings.TrimSpace(p.CurrentVersion) == "" || strings.TrimSpace(p.TargetVersion) == "" {
		return errors.New("OTA_RESULT currentVersion and targetVersion are required")
	}
	if p.OccurredAt < 1 {
		return errors.New("OTA_RESULT occurredAt must be >= 1")
	}
	return nil
}

// CanonicalOTASignaturePayload returns the domain-separated byte string signed with Ed25519.
func CanonicalOTASignaturePayload(packageID, version, component, targetOS, targetArch, sha256Hex string, fileSizeBytes int64) []byte {
	return []byte(strings.Join([]string{
		otaSignaturePrefix,
		strings.ToLower(strings.TrimSpace(packageID)),
		strings.TrimSpace(version),
		strings.TrimSpace(component),
		strings.ToLower(strings.TrimSpace(targetOS)),
		NormalizeTargetArch(targetArch),
		strings.ToLower(strings.TrimSpace(sha256Hex)),
		strconv.FormatInt(fileSizeBytes, 10),
	}, "\n"))
}

// SignOTAPackageEd25519 produces the base64 Ed25519 signature over CanonicalOTASignaturePayload.
func SignOTAPackageEd25519(priv ed25519.PrivateKey, packageID, version, component, targetOS, targetArch, sha256Hex string, fileSizeBytes int64) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("OTA signing requires a valid Ed25519 private key")
	}
	msg := CanonicalOTASignaturePayload(packageID, version, component, targetOS, targetArch, sha256Hex, fileSizeBytes)
	sig := ed25519.Sign(priv, msg)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyOTASignatureEd25519 verifies a base64 Ed25519 signature against CanonicalOTASignaturePayload.
func VerifyOTASignatureEd25519(pub ed25519.PublicKey, packageID, version, component, targetOS, targetArch, sha256Hex string, fileSizeBytes int64, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("OTA verification requires a valid Ed25519 public key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("decode OTA signature base64: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return errors.New("OTA signature has invalid length")
	}
	msg := CanonicalOTASignaturePayload(packageID, version, component, targetOS, targetArch, sha256Hex, fileSizeBytes)
	if !ed25519.Verify(pub, msg, sig) {
		return errors.New("OTA package Ed25519 signature verification failed")
	}
	return nil
}

// EncodeOTAStage encodes an OTA_STAGE downlink replication envelope.
func EncodeOTAStage(gatewayID string, stage OTAStagePayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("OTA_STAGE requires valid edgeId")
	}
	if err := stage.Validate(); err != nil {
		return nil, err
	}
	return encodeReplicationEnvelope(gwID, ShadowReplicationTypeOTAStage, stage, now)
}

// DecodeOTAStage decodes and validates an OTA_STAGE downlink replication envelope.
func DecodeOTAStage(payload []byte, expectedGatewayID string) (ShadowReplicationEnvelope, OTAStagePayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, expectedGatewayID, ShadowReplicationTypeOTAStage)
	if err != nil {
		return ShadowReplicationEnvelope{}, OTAStagePayload{}, err
	}
	var stage OTAStagePayload
	if err := DecodeStrictShadowObject(env.Payload, &stage, "OTA_STAGE payload"); err != nil {
		return ShadowReplicationEnvelope{}, OTAStagePayload{}, err
	}
	if err := stage.Validate(); err != nil {
		return ShadowReplicationEnvelope{}, OTAStagePayload{}, err
	}
	return env, stage, nil
}

// EncodeOTAResult encodes an OTA_RESULT uplink replication envelope.
func EncodeOTAResult(gatewayID string, res OTAResultPayload, now time.Time) ([]byte, error) {
	gwID := strings.TrimSpace(gatewayID)
	if gwID == "" || len(gwID) > 128 {
		return nil, errors.New("OTA_RESULT requires valid edgeId")
	}
	if err := res.Validate(); err != nil {
		return nil, err
	}
	return encodeReplicationEnvelope(gwID, ShadowReplicationTypeOTAResult, res, now)
}

// DecodeOTAResult decodes and validates an OTA_RESULT uplink replication envelope.
func DecodeOTAResult(payload []byte, expectedGatewayID string) (ShadowReplicationEnvelope, OTAResultPayload, error) {
	env, err := DecodeShadowReplicationEnvelope(payload, expectedGatewayID, ShadowReplicationTypeOTAResult)
	if err != nil {
		return ShadowReplicationEnvelope{}, OTAResultPayload{}, err
	}
	var res OTAResultPayload
	if err := DecodeStrictShadowObject(env.Payload, &res, "OTA_RESULT payload"); err != nil {
		return ShadowReplicationEnvelope{}, OTAResultPayload{}, err
	}
	if err := res.Validate(); err != nil {
		return ShadowReplicationEnvelope{}, OTAResultPayload{}, err
	}
	return env, res, nil
}

func encodeReplicationEnvelope(edgeID, msgType string, body any, now time.Time) ([]byte, error) {
	nowUTC := now.UTC()
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	msgID, err := NewReplicationUUIDv7(nowUTC)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", msgType, err)
	}
	wireBytes, err := json.Marshal(ShadowReplicationEnvelope{
		SchemaVersion: ShadowReplicationSchemaVersion,
		MessageID:     msgID,
		EdgeID:        edgeID,
		SentAt:        max(nowUTC.UnixMilli(), 1),
		Type:          msgType,
		Payload:       json.RawMessage(bodyBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s envelope: %w", msgType, err)
	}
	if len(wireBytes) > MaximumShadowReplicationBytes {
		return nil, fmt.Errorf("%s envelope exceeds maximum MQTT payload size", msgType)
	}
	return wireBytes, nil
}

type parsedSemver struct {
	major int
	minor int
	patch int
	pre   []string
}

func parseSemver(raw string) (parsedSemver, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if plusIdx := strings.IndexByte(trimmed, '+'); plusIdx >= 0 {
		trimmed = trimmed[:plusIdx]
	}
	var pre []string
	if dashIdx := strings.IndexByte(trimmed, '-'); dashIdx >= 0 {
		prePart := trimmed[dashIdx+1:]
		if prePart == "" {
			return parsedSemver{}, fmt.Errorf("invalid semver %q: empty prerelease", raw)
		}
		pre = strings.Split(prePart, ".")
		trimmed = trimmed[:dashIdx]
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return parsedSemver{}, fmt.Errorf("invalid semver %q: want MAJOR.MINOR.PATCH", raw)
	}
	nums := make([]int, 3)
	for i, part := range parts {
		if part == "" {
			return parsedSemver{}, fmt.Errorf("invalid semver %q", raw)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsedSemver{}, fmt.Errorf("invalid semver %q: %w", raw, err)
		}
		nums[i] = n
	}
	return parsedSemver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, nil
}

// CompareSemver compares two SemVer 2.0 version strings (with optional leading 'v').
// Returns -1 if a < b, 0 if equal (ignoring build metadata), and +1 if a > b.
func CompareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	if pa.major != pb.major {
		return cmpInt(pa.major, pb.major), nil
	}
	if pa.minor != pb.minor {
		return cmpInt(pa.minor, pb.minor), nil
	}
	if pa.patch != pb.patch {
		return cmpInt(pa.patch, pb.patch), nil
	}
	if len(pa.pre) == 0 && len(pb.pre) == 0 {
		return 0, nil
	}
	if len(pa.pre) == 0 {
		return 1, nil
	}
	if len(pb.pre) == 0 {
		return -1, nil
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		if pa.pre[i] == pb.pre[i] {
			continue
		}
		ai, aErr := strconv.Atoi(pa.pre[i])
		bi, bErr := strconv.Atoi(pb.pre[i])
		switch {
		case aErr == nil && bErr == nil:
			return cmpInt(ai, bi), nil
		case aErr == nil:
			return -1, nil
		case bErr == nil:
			return 1, nil
		case pa.pre[i] < pb.pre[i]:
			return -1, nil
		default:
			return 1, nil
		}
	}
	return cmpInt(len(pa.pre), len(pb.pre)), nil
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
