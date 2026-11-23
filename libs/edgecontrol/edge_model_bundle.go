package edgecontrol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type EdgeModelDomain string

const (
	EdgeModelDomainControl EdgeModelDomain = "EDGE_CONTROL"
	EdgeModelDomainFDD     EdgeModelDomain = "EDGE_FDD"
)

type EdgeModelRolloutMode string

const (
	EdgeModelRolloutShadow EdgeModelRolloutMode = "SHADOW"
	EdgeModelRolloutActive EdgeModelRolloutMode = "ACTIVE"

	edgeModelSignaturePrefix = "HVAC-EDGE-MODEL-V1"
	maxEdgeModelTargetPoints = 64
	maxCOPCoefficientCount   = 12
)

var (
	ErrEdgeModelValidation = errors.New("edge model bundle validation failed")
	ErrEdgeModelSignature  = errors.New("edge model bundle signature verification failed")

	edgeModelKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	sha256HexPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// EdgeModelHyperparams defines physical-bounded parameters for edge control and FDD bundles.
type EdgeModelHyperparams struct {
	COPCoefficients        []float64 `json:"copCoefficients,omitempty"`
	ChillerStartPowerKW    *float64  `json:"chillerStartPowerKw,omitempty"`
	ChillerStopPowerKW     *float64  `json:"chillerStopPowerKw,omitempty"`
	SupplyTempSetpointMinC *float64  `json:"supplyTempSetpointMinC,omitempty"`
	SupplyTempSetpointMaxC *float64  `json:"supplyTempSetpointMaxC,omitempty"`
	PumpFrequencyMinHz     *float64  `json:"pumpFrequencyMinHz,omitempty"`
	PumpFrequencyMaxHz     *float64  `json:"pumpFrequencyMaxHz,omitempty"`
	TowerFanSpeedMinPct    *float64  `json:"towerFanSpeedMinPct,omitempty"`
	TowerFanSpeedMaxPct    *float64  `json:"towerFanSpeedMaxPct,omitempty"`
	PIDKp                  *float64  `json:"pidKp,omitempty"`
	PIDKi                  *float64  `json:"pidKi,omitempty"`
	PIDKd                  *float64  `json:"pidKd,omitempty"`
	StageUpLoadPct         *float64  `json:"stageUpLoadPct,omitempty"`
	StageDownLoadPct       *float64  `json:"stageDownLoadPct,omitempty"`
	FDDDeltaTMinK          *float64  `json:"fddDeltaTMinK,omitempty"`
	FDDAnomalyZScore       *float64  `json:"fddAnomalyZScore,omitempty"`
	TargetPointCodes       []string  `json:"targetPointCodes,omitempty"`
}

// EdgeModelBundle is the signed unit synchronized from cloud MLOps metadata through config shadow.
type EdgeModelBundle struct {
	BundleID           string               `json:"bundleId"`
	Domain             EdgeModelDomain      `json:"domain"`
	ModelKey           string               `json:"modelKey"`
	Version            string               `json:"version"`
	RolloutMode        EdgeModelRolloutMode `json:"rolloutMode"`
	ArtifactSHA256     string               `json:"artifactSha256"`
	KeyID              string               `json:"keyId,omitempty"`
	Signature          string               `json:"signature,omitempty"`
	MaxShadowDeviation float64              `json:"maxShadowDeviation"`
	Hyperparams        EdgeModelHyperparams `json:"hyperparams"`
	UpdatedAt          time.Time            `json:"updatedAt,omitempty"`
}

// EdgeModelRuntimeStatus captures live active/shadow status and auto-rollback telemetry from the gateway.
type EdgeModelRuntimeStatus struct {
	ModelKey               string          `json:"modelKey"`
	Domain                 EdgeModelDomain `json:"domain"`
	ActiveBundleID         string          `json:"activeBundleId,omitempty"`
	ActiveVersion          string          `json:"activeVersion,omitempty"`
	ShadowBundleID         string          `json:"shadowBundleId,omitempty"`
	ShadowVersion          string          `json:"shadowVersion,omitempty"`
	ShadowSampleCount      int64           `json:"shadowSampleCount"`
	ShadowMeanAbsDeviation float64         `json:"shadowMeanAbsDeviation"`
	ShadowMaxAbsDeviation  float64         `json:"shadowMaxAbsDeviation"`
	RollbackCount          int             `json:"rollbackCount"`
	LastRollbackReason     string          `json:"lastRollbackReason,omitempty"`
	LastEvaluatedAt        time.Time       `json:"lastEvaluatedAt,omitempty"`
}

// ValidEdgeModelDomain reports whether domain is supported by edge model governance.
func ValidEdgeModelDomain(domain EdgeModelDomain) bool {
	switch domain {
	case EdgeModelDomainControl, EdgeModelDomainFDD:
		return true
	default:
		return false
	}
}

// ValidEdgeModelRolloutMode reports whether rollout mode is SHADOW or ACTIVE.
func ValidEdgeModelRolloutMode(mode EdgeModelRolloutMode) bool {
	switch mode {
	case EdgeModelRolloutShadow, EdgeModelRolloutActive:
		return true
	default:
		return false
	}
}

// Clone returns a deep copy of EdgeModelHyperparams.
func (h EdgeModelHyperparams) Clone() EdgeModelHyperparams {
	out := EdgeModelHyperparams{
		ChillerStartPowerKW:    cloneFloat64Ptr(h.ChillerStartPowerKW),
		ChillerStopPowerKW:     cloneFloat64Ptr(h.ChillerStopPowerKW),
		SupplyTempSetpointMinC: cloneFloat64Ptr(h.SupplyTempSetpointMinC),
		SupplyTempSetpointMaxC: cloneFloat64Ptr(h.SupplyTempSetpointMaxC),
		PumpFrequencyMinHz:     cloneFloat64Ptr(h.PumpFrequencyMinHz),
		PumpFrequencyMaxHz:     cloneFloat64Ptr(h.PumpFrequencyMaxHz),
		TowerFanSpeedMinPct:    cloneFloat64Ptr(h.TowerFanSpeedMinPct),
		TowerFanSpeedMaxPct:    cloneFloat64Ptr(h.TowerFanSpeedMaxPct),
		PIDKp:                  cloneFloat64Ptr(h.PIDKp),
		PIDKi:                  cloneFloat64Ptr(h.PIDKi),
		PIDKd:                  cloneFloat64Ptr(h.PIDKd),
		StageUpLoadPct:         cloneFloat64Ptr(h.StageUpLoadPct),
		StageDownLoadPct:       cloneFloat64Ptr(h.StageDownLoadPct),
		FDDDeltaTMinK:          cloneFloat64Ptr(h.FDDDeltaTMinK),
		FDDAnomalyZScore:       cloneFloat64Ptr(h.FDDAnomalyZScore),
	}
	if h.COPCoefficients != nil {
		out.COPCoefficients = append([]float64(nil), h.COPCoefficients...)
	}
	if h.TargetPointCodes != nil {
		out.TargetPointCodes = append([]string(nil), h.TargetPointCodes...)
	}
	return out
}

// Clone returns a deep copy of EdgeModelBundle.
func (b EdgeModelBundle) Clone() EdgeModelBundle {
	out := b
	out.Hyperparams = b.Hyperparams.Clone()
	return out
}

// ComputeEdgeModelHyperparamsSHA256 computes a canonical SHA-256 hex digest for hyperparams.
func ComputeEdgeModelHyperparamsSHA256(hp EdgeModelHyperparams) (string, error) {
	raw, err := json.Marshal(hp)
	if err != nil {
		return "", fmt.Errorf("marshal hyperparams: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalEdgeModelSignaturePayload returns the domain-separated payload covered by Ed25519 signatures.
func CanonicalEdgeModelSignaturePayload(b EdgeModelBundle) ([]byte, error) {
	hpJSON, err := json.Marshal(b.Hyperparams)
	if err != nil {
		return nil, fmt.Errorf("marshal hyperparams for signature: %w", err)
	}
	parts := []string{
		edgeModelSignaturePrefix,
		strings.TrimSpace(b.BundleID),
		string(b.Domain),
		strings.TrimSpace(b.ModelKey),
		strings.TrimSpace(b.Version),
		string(b.RolloutMode),
		strings.ToLower(strings.TrimSpace(b.ArtifactSHA256)),
		strconv.FormatFloat(b.MaxShadowDeviation, 'g', -1, 64),
		string(hpJSON),
	}
	return []byte(strings.Join(parts, "\n")), nil
}

// SignEdgeModelBundleEd25519 computes and attaches Signature using privateKey for keyID.
func SignEdgeModelBundleEd25519(b *EdgeModelBundle, keyID string, privateKey ed25519.PrivateKey) error {
	if b == nil {
		return fmt.Errorf("%w: bundle is nil", ErrEdgeModelSignature)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: invalid ed25519 private key length", ErrEdgeModelSignature)
	}
	trimmedKeyID := strings.TrimSpace(keyID)
	if trimmedKeyID == "" {
		return fmt.Errorf("%w: keyId is required", ErrEdgeModelSignature)
	}
	if strings.TrimSpace(b.ArtifactSHA256) == "" {
		sum, err := ComputeEdgeModelHyperparamsSHA256(b.Hyperparams)
		if err != nil {
			return err
		}
		b.ArtifactSHA256 = sum
	}
	b.KeyID = trimmedKeyID
	msg, err := CanonicalEdgeModelSignaturePayload(*b)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(privateKey, msg)
	b.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// VerifyEdgeModelBundleEd25519 verifies b.Signature against trustedKeys[b.KeyID].
func VerifyEdgeModelBundleEd25519(b EdgeModelBundle, trustedKeys map[string]string) error {
	keyID := strings.TrimSpace(b.KeyID)
	sigRaw := strings.TrimSpace(b.Signature)
	if keyID == "" || sigRaw == "" {
		return fmt.Errorf("%w: keyId and signature are required", ErrEdgeModelSignature)
	}
	pubB64, ok := trustedKeys[keyID]
	if !ok {
		return fmt.Errorf("%w: unknown keyId %q", ErrEdgeModelSignature, keyID)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid trusted public key for %q", ErrEdgeModelSignature, keyID)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigRaw)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("%w: malformed base64 signature", ErrEdgeModelSignature)
	}
	msg, err := CanonicalEdgeModelSignaturePayload(b)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), msg, sigBytes) {
		return fmt.Errorf("%w: ed25519 signature mismatch", ErrEdgeModelSignature)
	}
	return nil
}

// ValidateEdgeModelHyperparams enforces hard equipment physical bounds and EDGE-003 point protection.
func ValidateEdgeModelHyperparams(domain EdgeModelDomain, hp EdgeModelHyperparams) error {
	hasParam := len(hp.COPCoefficients) > 0
	if len(hp.COPCoefficients) > maxCOPCoefficientCount {
		return fmt.Errorf("%w: copCoefficients count %d exceeds %d", ErrEdgeModelValidation, len(hp.COPCoefficients), maxCOPCoefficientCount)
	}
	for i, coef := range hp.COPCoefficients {
		if math.IsNaN(coef) || math.IsInf(coef, 0) || coef < -1000 || coef > 1000 {
			return fmt.Errorf("%w: copCoefficients[%d]=%v out of [-1000,1000]", ErrEdgeModelValidation, i, coef)
		}
	}

	checkRange := func(name string, ptr *float64, minVal, maxVal float64) error {
		if ptr == nil {
			return nil
		}
		hasParam = true
		v := *ptr
		if math.IsNaN(v) || math.IsInf(v, 0) || v < minVal || v > maxVal {
			return fmt.Errorf("%w: %s=%v out of physical bounds [%g,%g]", ErrEdgeModelValidation, name, v, minVal, maxVal)
		}
		return nil
	}

	if err := checkRange("chillerStartPowerKw", hp.ChillerStartPowerKW, 5, 200); err != nil {
		return err
	}
	if err := checkRange("chillerStopPowerKw", hp.ChillerStopPowerKW, 0.5, 100); err != nil {
		return err
	}
	if hp.ChillerStartPowerKW != nil && hp.ChillerStopPowerKW != nil && *hp.ChillerStopPowerKW >= *hp.ChillerStartPowerKW {
		return fmt.Errorf("%w: chillerStopPowerKw (%g) must be less than chillerStartPowerKw (%g)", ErrEdgeModelValidation, *hp.ChillerStopPowerKW, *hp.ChillerStartPowerKW)
	}
	if err := checkRange("supplyTempSetpointMinC", hp.SupplyTempSetpointMinC, 4, 18); err != nil {
		return err
	}
	if err := checkRange("supplyTempSetpointMaxC", hp.SupplyTempSetpointMaxC, 4, 18); err != nil {
		return err
	}
	if hp.SupplyTempSetpointMinC != nil && hp.SupplyTempSetpointMaxC != nil && *hp.SupplyTempSetpointMinC > *hp.SupplyTempSetpointMaxC {
		return fmt.Errorf("%w: supplyTempSetpointMinC > supplyTempSetpointMaxC", ErrEdgeModelValidation)
	}
	if err := checkRange("pumpFrequencyMinHz", hp.PumpFrequencyMinHz, 15, 50); err != nil {
		return err
	}
	if err := checkRange("pumpFrequencyMaxHz", hp.PumpFrequencyMaxHz, 15, 50); err != nil {
		return err
	}
	if hp.PumpFrequencyMinHz != nil && hp.PumpFrequencyMaxHz != nil && *hp.PumpFrequencyMinHz > *hp.PumpFrequencyMaxHz {
		return fmt.Errorf("%w: pumpFrequencyMinHz > pumpFrequencyMaxHz", ErrEdgeModelValidation)
	}
	if err := checkRange("towerFanSpeedMinPct", hp.TowerFanSpeedMinPct, 15, 100); err != nil {
		return err
	}
	if err := checkRange("towerFanSpeedMaxPct", hp.TowerFanSpeedMaxPct, 15, 100); err != nil {
		return err
	}
	if hp.TowerFanSpeedMinPct != nil && hp.TowerFanSpeedMaxPct != nil && *hp.TowerFanSpeedMinPct > *hp.TowerFanSpeedMaxPct {
		return fmt.Errorf("%w: towerFanSpeedMinPct > towerFanSpeedMaxPct", ErrEdgeModelValidation)
	}
	if err := checkRange("pidKp", hp.PIDKp, 0, 20); err != nil {
		return err
	}
	if err := checkRange("pidKi", hp.PIDKi, 0, 5); err != nil {
		return err
	}
	if err := checkRange("pidKd", hp.PIDKd, 0, 5); err != nil {
		return err
	}
	if hp.StageUpLoadPct != nil {
		hasParam = true
		v := *hp.StageUpLoadPct
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 50 || v > 100 {
			return fmt.Errorf("%w: stageUpLoadPct=%v out of physical bounds (50,100]", ErrEdgeModelValidation, v)
		}
	}
	if hp.StageDownLoadPct != nil {
		hasParam = true
		v := *hp.StageDownLoadPct
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 5 || v >= 80 {
			return fmt.Errorf("%w: stageDownLoadPct=%v out of physical bounds [5,80)", ErrEdgeModelValidation, v)
		}
	}
	if hp.StageUpLoadPct != nil && hp.StageDownLoadPct != nil && (*hp.StageUpLoadPct-*hp.StageDownLoadPct) < 10 {
		return fmt.Errorf("%w: stageUpLoadPct - stageDownLoadPct must be >= 10%%", ErrEdgeModelValidation)
	}
	if err := checkRange("fddDeltaTMinK", hp.FDDDeltaTMinK, 0.5, 15); err != nil {
		return err
	}
	if err := checkRange("fddAnomalyZScore", hp.FDDAnomalyZScore, 1.5, 10); err != nil {
		return err
	}

	if len(hp.TargetPointCodes) > maxEdgeModelTargetPoints {
		return fmt.Errorf("%w: targetPointCodes count %d exceeds %d", ErrEdgeModelValidation, len(hp.TargetPointCodes), maxEdgeModelTargetPoints)
	}
	for _, rawCode := range hp.TargetPointCodes {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			return fmt.Errorf("%w: empty targetPointCode is not allowed", ErrEdgeModelValidation)
		}
		if category, protected := ClassifyHardRealtimeProtection(code, "", nil); protected {
			return fmt.Errorf("%w (%s): targetPointCode %q is hardware-protected (%s)", ErrEdgeModelValidation, ReasonHardRealtimeSafetyBoundary, code, category)
		}
	}

	if !hasParam {
		return fmt.Errorf("%w: at least one %s hyperparameter must be specified", ErrEdgeModelValidation, domain)
	}
	return nil
}

// ValidateEdgeModelBundleEnvelope validates bundle metadata, physical bounds, EDGE-003 point protection,
// artifact SHA-256 checksum, and signature envelope syntax without requiring trusted Ed25519 keys.
func ValidateEdgeModelBundleEnvelope(b EdgeModelBundle) error {
	if strings.TrimSpace(b.BundleID) == "" || len(strings.TrimSpace(b.BundleID)) > 128 {
		return fmt.Errorf("%w: bundleId is required", ErrEdgeModelValidation)
	}
	if !ValidEdgeModelDomain(b.Domain) {
		return fmt.Errorf("%w: unsupported domain %q", ErrEdgeModelValidation, b.Domain)
	}
	if !edgeModelKeyPattern.MatchString(strings.TrimSpace(b.ModelKey)) {
		return fmt.Errorf("%w: invalid modelKey %q", ErrEdgeModelValidation, b.ModelKey)
	}
	version := strings.TrimSpace(b.Version)
	if version == "" || len(version) > 64 {
		return fmt.Errorf("%w: invalid version", ErrEdgeModelValidation)
	}
	if !ValidEdgeModelRolloutMode(b.RolloutMode) {
		return fmt.Errorf("%w: unsupported rolloutMode %q", ErrEdgeModelValidation, b.RolloutMode)
	}
	if math.IsNaN(b.MaxShadowDeviation) || math.IsInf(b.MaxShadowDeviation, 0) || b.MaxShadowDeviation < 0 || b.MaxShadowDeviation > 50 {
		return fmt.Errorf("%w: maxShadowDeviation=%v out of [0,50]", ErrEdgeModelValidation, b.MaxShadowDeviation)
	}
	if err := ValidateEdgeModelHyperparams(b.Domain, b.Hyperparams); err != nil {
		return err
	}
	computedSHA, err := ComputeEdgeModelHyperparamsSHA256(b.Hyperparams)
	if err != nil {
		return err
	}
	digest := strings.ToLower(strings.TrimSpace(b.ArtifactSHA256))
	if !sha256HexPattern.MatchString(digest) || digest != computedSHA {
		return fmt.Errorf("%w: artifactSha256 mismatch (got %q want %q)", ErrEdgeModelValidation, digest, computedSHA)
	}
	if strings.TrimSpace(b.Signature) != "" {
		if strings.TrimSpace(b.KeyID) == "" {
			return fmt.Errorf("%w: keyId is required when signature is set", ErrEdgeModelSignature)
		}
		sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b.Signature))
		if err != nil || len(sigBytes) != ed25519.SignatureSize {
			return fmt.Errorf("%w: malformed base64 signature", ErrEdgeModelSignature)
		}
	}
	return nil
}

// ValidateEdgeModelBundle verifies metadata, physical bounds, artifact digest, and optional Ed25519 signature.
func ValidateEdgeModelBundle(b EdgeModelBundle, trustedKeys map[string]string, requireSignature bool) error {
	if err := ValidateEdgeModelBundleEnvelope(b); err != nil {
		return err
	}
	if requireSignature || strings.TrimSpace(b.Signature) != "" {
		return VerifyEdgeModelBundleEd25519(b, trustedKeys)
	}
	return nil
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	cpy := *v
	return &cpy
}
