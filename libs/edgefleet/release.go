package edgefleet

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
)

type EdgeReleasePayload struct {
	ReleaseID                  string   `json:"releaseId"`
	RuntimeRevision            string   `json:"runtimeRevision"`
	ManifestRevision           string   `json:"manifestRevision"`
	RegistryProjectionRevision uint64   `json:"registryProjectionRevision"`
	DriverRevision             string   `json:"driverRevision"`
	RuleRevision               string   `json:"ruleRevision,omitempty"`
	ScheduleRevision           string   `json:"scheduleRevision,omitempty"`
	SafetyPolicyRevision       string   `json:"safetyPolicyRevision"`
	DesiredConfigRevision      uint64   `json:"desiredConfigRevision"`
	MinRuntimeVersion          string   `json:"minRuntimeVersion"`
	MaxRuntimeVersion          string   `json:"maxRuntimeVersion"`
	RequiredCapabilities       []string `json:"requiredCapabilities,omitempty"`
}

type SignedEdgeRelease struct {
	Payload     EdgeReleasePayload `json:"payload"`
	Digest      string             `json:"digest"`
	SignerKeyID string             `json:"signerKeyId"`
	Signature   string             `json:"signature"`
}

func SignEdgeRelease(payload EdgeReleasePayload, signerKeyID string, privateKey ed25519.PrivateKey) (SignedEdgeRelease, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(signerKeyID) == "" {
		return SignedEdgeRelease{}, errors.New("release signer key ID and Ed25519 private key are required")
	}
	payload.RequiredCapabilities = normalizedStrings(payload.RequiredCapabilities)
	if err := validateEdgeReleasePayload(payload); err != nil {
		return SignedEdgeRelease{}, err
	}
	digest, err := edgeReleaseDigest(payload)
	if err != nil {
		return SignedEdgeRelease{}, err
	}
	signature := ed25519.Sign(privateKey, []byte(digest))
	return SignedEdgeRelease{
		Payload: payload, Digest: digest, SignerKeyID: strings.TrimSpace(signerKeyID), Signature: hex.EncodeToString(signature),
	}, nil
}

func edgeReleaseDigest(payload EdgeReleasePayload) (string, error) {
	payload.RequiredCapabilities = normalizedStrings(payload.RequiredCapabilities)
	encoded, err := canonicalJSON(payload)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

func validateEdgeReleasePayload(payload EdgeReleasePayload) error {
	if strings.TrimSpace(payload.ReleaseID) == "" || strings.TrimSpace(payload.RuntimeRevision) == "" || strings.TrimSpace(payload.ManifestRevision) == "" ||
		payload.RegistryProjectionRevision == 0 || strings.TrimSpace(payload.DriverRevision) == "" || strings.TrimSpace(payload.SafetyPolicyRevision) == "" || payload.DesiredConfigRevision == 0 {
		return errors.New("edge release requires immutable release, runtime, manifest, registry, driver, safety and desired-config revisions")
	}
	if !versionInRange(payload.MinRuntimeVersion, payload.MinRuntimeVersion, payload.MaxRuntimeVersion) {
		return errors.New("edge release runtime compatibility range is invalid")
	}
	return nil
}

type ReleaseActivationResult struct {
	ActiveReleaseID   string `json:"activeReleaseId"`
	PreviousReleaseID string `json:"previousReleaseId,omitempty"`
	RolledBack        bool   `json:"rolledBack"`
	Reason            string `json:"reason,omitempty"`
}

func (replica *Replica) SetTrustedKey(keyID string, publicKey ed25519.PublicKey) {
	if replica == nil || strings.TrimSpace(keyID) == "" || len(publicKey) != ed25519.PublicKeySize {
		return
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	replica.trustedKeys[strings.TrimSpace(keyID)] = append([]byte(nil), publicKey...)
}

func (replica *Replica) StageRelease(release SignedEdgeRelease, preflight func(EdgeReleasePayload) error) error {
	if replica == nil {
		return errors.New("edge fleet replica is unavailable")
	}
	if err := replica.verifyEdgeRelease(release); err != nil {
		return err
	}
	if preflight != nil {
		if err := preflight(release.Payload); err != nil {
			return fmt.Errorf("%w: %v", ErrPreflightFailed, err)
		}
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	releaseCopy := release
	replica.state.StagedRelease = &releaseCopy
	replica.state.Observed.StagedReleaseID = release.Payload.ReleaseID
	return replica.persistLocked()
}

func (replica *Replica) verifyEdgeRelease(release SignedEdgeRelease) error {
	if err := validateEdgeReleasePayload(release.Payload); err != nil {
		return err
	}
	digest, err := edgeReleaseDigest(release.Payload)
	if err != nil {
		return err
	}
	if digest != release.Digest || !isSHA256(release.Digest) {
		return ErrSignatureInvalid
	}
	replica.mu.Lock()
	key := append([]byte(nil), replica.trustedKeys[strings.TrimSpace(release.SignerKeyID)]...)
	replica.mu.Unlock()
	if len(key) != ed25519.PublicKeySize {
		return ErrSignatureInvalid
	}
	signature, err := hex.DecodeString(release.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), []byte(release.Digest), signature) {
		return ErrSignatureInvalid
	}
	if !versionInRange(replica.runtime.RuntimeVersion, release.Payload.MinRuntimeVersion, release.Payload.MaxRuntimeVersion) {
		return ErrRuntimeIncompatible
	}
	for _, capability := range normalizedStrings(release.Payload.RequiredCapabilities) {
		if !replica.runtime.hasCapability(capability) {
			return fmt.Errorf("%w: %s", ErrCapabilityMissing, capability)
		}
	}
	return nil
}

type OTAArtifactPayload struct {
	ArtifactID           string   `json:"artifactId"`
	Version              string   `json:"version"`
	PackageRef           string   `json:"packageRef"`
	PackageSHA256        string   `json:"packageSha256"`
	MinRuntimeVersion    string   `json:"minRuntimeVersion"`
	MaxRuntimeVersion    string   `json:"maxRuntimeVersion"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
	RollbackArtifactID   string   `json:"rollbackArtifactId"`
}

type SignedOTAArtifact struct {
	Payload     OTAArtifactPayload `json:"payload"`
	Digest      string             `json:"digest"`
	SignerKeyID string             `json:"signerKeyId"`
	Signature   string             `json:"signature"`
}

func SignOTAArtifact(payload OTAArtifactPayload, signerKeyID string, privateKey ed25519.PrivateKey) (SignedOTAArtifact, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(signerKeyID) == "" {
		return SignedOTAArtifact{}, errors.New("OTA signer key ID and Ed25519 private key are required")
	}
	payload.RequiredCapabilities = normalizedStrings(payload.RequiredCapabilities)
	if err := validateOTAArtifactPayload(payload); err != nil {
		return SignedOTAArtifact{}, err
	}
	digest := otaArtifactDigest(payload)
	signature := ed25519.Sign(privateKey, []byte(digest))
	return SignedOTAArtifact{Payload: payload, Digest: digest, SignerKeyID: strings.TrimSpace(signerKeyID), Signature: hex.EncodeToString(signature)}, nil
}

func otaArtifactDigest(payload OTAArtifactPayload) string {
	payload.RequiredCapabilities = normalizedStrings(payload.RequiredCapabilities)
	encoded, err := canonicalJSON(payload)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func validateOTAArtifactPayload(payload OTAArtifactPayload) error {
	if strings.TrimSpace(payload.ArtifactID) == "" || strings.TrimSpace(payload.Version) == "" || !isSHA256(payload.PackageSHA256) || strings.TrimSpace(payload.RollbackArtifactID) == "" {
		return errors.New("OTA artifact requires identity, version, package digest and rollback artifact")
	}
	packageRef, err := url.Parse(strings.TrimSpace(payload.PackageRef))
	if err != nil || packageRef.Scheme != "artifact" || packageRef.Host == "" || packageRef.Path == "" || packageRef.User != nil || packageRef.RawQuery != "" || packageRef.Fragment != "" ||
		packageRef.EscapedPath() != packageRef.Path || pathpkg.Clean(packageRef.Path) != packageRef.Path {
		return errors.New("OTA artifact packageRef must be a canonical non-secret artifact:// reference")
	}
	if !versionInRange(payload.MinRuntimeVersion, payload.MinRuntimeVersion, payload.MaxRuntimeVersion) {
		return errors.New("OTA runtime compatibility range is invalid")
	}
	return nil
}

func (replica *Replica) StageOTA(artifact SignedOTAArtifact, downloadedPackageSHA256 string, preflight func() error) error {
	if replica == nil {
		return errors.New("edge fleet replica is unavailable")
	}
	if err := replica.verifyOTAArtifact(artifact); err != nil {
		return err
	}
	if downloadedPackageSHA256 != artifact.Payload.PackageSHA256 || !isSHA256(downloadedPackageSHA256) {
		return errors.New("OTA downloaded package digest mismatch")
	}
	replica.mu.Lock()
	alreadyActive := replica.state.Observed.ActiveOTAArtifactID == artifact.Payload.ArtifactID
	replica.mu.Unlock()
	if alreadyActive {
		return nil
	}
	if preflight != nil {
		if err := preflight(); err != nil {
			return fmt.Errorf("%w: %v", ErrPreflightFailed, err)
		}
	}
	replica.mu.Lock()
	defer replica.mu.Unlock()
	replica.state.Observed.StagedOTAArtifactID = artifact.Payload.ArtifactID
	return replica.persistLocked()
}

type OTAActivationResult struct {
	ArtifactID         string `json:"artifactId"`
	ActiveArtifactID   string `json:"activeArtifactId,omitempty"`
	PreviousArtifactID string `json:"previousArtifactId,omitempty"`
	RolledBack         bool   `json:"rolledBack"`
	Reason             string `json:"reason,omitempty"`
}

func (replica *Replica) ActivateOTA(artifact SignedOTAArtifact, startupHealthCheck func() error) (OTAActivationResult, error) {
	if replica == nil {
		return OTAActivationResult{}, errors.New("edge fleet replica is unavailable")
	}
	if err := replica.verifyOTAArtifact(artifact); err != nil {
		return OTAActivationResult{}, err
	}
	replica.mu.Lock()
	if replica.state.Observed.ActiveOTAArtifactID == artifact.Payload.ArtifactID && replica.state.Observed.StagedOTAArtifactID == "" {
		replica.mu.Unlock()
		return OTAActivationResult{ArtifactID: artifact.Payload.ArtifactID, ActiveArtifactID: artifact.Payload.ArtifactID}, nil
	}
	if replica.state.Observed.StagedOTAArtifactID != artifact.Payload.ArtifactID {
		replica.mu.Unlock()
		return OTAActivationResult{}, errors.New("OTA artifact must be staged before activation")
	}
	previous := replica.state.Observed.ActiveOTAArtifactID
	if previous != "" && artifact.Payload.RollbackArtifactID != previous {
		replica.mu.Unlock()
		return OTAActivationResult{}, errors.New("OTA rollback artifact does not match active artifact")
	}
	replica.state.Observed.PreviousOTAArtifactID = previous
	replica.state.Observed.ActiveOTAArtifactID = artifact.Payload.ArtifactID
	replica.state.Observed.StagedOTAArtifactID = ""
	if err := replica.persistLocked(); err != nil {
		replica.mu.Unlock()
		return OTAActivationResult{}, err
	}
	replica.mu.Unlock()

	if startupHealthCheck != nil {
		if err := startupHealthCheck(); err != nil {
			replica.mu.Lock()
			replica.state.Observed.ActiveOTAArtifactID = previous
			replica.state.Observed.PreviousOTAArtifactID = artifact.Payload.ArtifactID
			persistErr := replica.persistLocked()
			replica.mu.Unlock()
			if persistErr != nil {
				return OTAActivationResult{}, persistErr
			}
			return OTAActivationResult{ArtifactID: artifact.Payload.ArtifactID, ActiveArtifactID: previous, PreviousArtifactID: artifact.Payload.ArtifactID, RolledBack: true, Reason: err.Error()}, nil
		}
	}
	return OTAActivationResult{ArtifactID: artifact.Payload.ArtifactID, ActiveArtifactID: artifact.Payload.ArtifactID, PreviousArtifactID: previous}, nil
}

func (replica *Replica) verifyOTAArtifact(artifact SignedOTAArtifact) error {
	if err := validateOTAArtifactPayload(artifact.Payload); err != nil {
		return err
	}
	digest := otaArtifactDigest(artifact.Payload)
	if digest == "" || digest != artifact.Digest {
		return ErrSignatureInvalid
	}
	replica.mu.Lock()
	key := append([]byte(nil), replica.trustedKeys[strings.TrimSpace(artifact.SignerKeyID)]...)
	replica.mu.Unlock()
	if len(key) != ed25519.PublicKeySize {
		return ErrSignatureInvalid
	}
	signature, err := hex.DecodeString(artifact.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), []byte(artifact.Digest), signature) {
		return ErrSignatureInvalid
	}
	if !versionInRange(replica.runtime.RuntimeVersion, artifact.Payload.MinRuntimeVersion, artifact.Payload.MaxRuntimeVersion) {
		return ErrRuntimeIncompatible
	}
	for _, capability := range normalizedStrings(artifact.Payload.RequiredCapabilities) {
		if !replica.runtime.hasCapability(capability) {
			return fmt.Errorf("%w: %s", ErrCapabilityMissing, capability)
		}
	}
	return nil
}

type OTACampaignStatus string

const (
	OTACampaignDraft     OTACampaignStatus = "DRAFT"
	OTACampaignRunning   OTACampaignStatus = "RUNNING"
	OTACampaignPaused    OTACampaignStatus = "PAUSED"
	OTACampaignCompleted OTACampaignStatus = "COMPLETED"
	OTACampaignAborted   OTACampaignStatus = "ABORTED"
)

type OTACampaign struct {
	CampaignID string            `json:"campaignId"`
	ArtifactID string            `json:"artifactId"`
	Waves      []int             `json:"waves"`
	WaveIndex  int               `json:"waveIndex"`
	Status     OTACampaignStatus `json:"status"`
}

func NewOTACampaign(campaignID, artifactID string, waves []int) (OTACampaign, error) {
	campaignID = strings.TrimSpace(campaignID)
	artifactID = strings.TrimSpace(artifactID)
	if campaignID == "" || artifactID == "" || len(waves) == 0 {
		return OTACampaign{}, errors.New("OTA campaign identity, artifact and waves are required")
	}
	copyWaves := append([]int(nil), waves...)
	if !sort.IntsAreSorted(copyWaves) || copyWaves[len(copyWaves)-1] != 100 {
		return OTACampaign{}, errors.New("OTA campaign waves must be increasing and end at 100 percent")
	}
	previous := 0
	for _, wave := range copyWaves {
		if wave <= previous || wave > 100 {
			return OTACampaign{}, errors.New("OTA campaign waves must be unique percentages between 1 and 100")
		}
		previous = wave
	}
	return OTACampaign{CampaignID: campaignID, ArtifactID: artifactID, Waves: copyWaves, Status: OTACampaignDraft}, nil
}

func (campaign *OTACampaign) Start() error {
	if campaign == nil || campaign.Status != OTACampaignDraft {
		return errors.New("only a draft OTA campaign can start")
	}
	campaign.Status = OTACampaignRunning
	return nil
}

func (campaign *OTACampaign) Pause() error {
	if campaign == nil || campaign.Status != OTACampaignRunning {
		return errors.New("only a running OTA campaign can pause")
	}
	campaign.Status = OTACampaignPaused
	return nil
}

func (campaign *OTACampaign) Resume() error {
	if campaign == nil || campaign.Status != OTACampaignPaused {
		return errors.New("only a paused OTA campaign can resume")
	}
	campaign.Status = OTACampaignRunning
	return nil
}

func (campaign *OTACampaign) AdvanceWave() error {
	if campaign == nil || campaign.Status != OTACampaignRunning {
		return errors.New("only a running OTA campaign can advance")
	}
	if campaign.WaveIndex+1 >= len(campaign.Waves) {
		campaign.Status = OTACampaignCompleted
		return nil
	}
	campaign.WaveIndex++
	return nil
}

func (campaign *OTACampaign) Abort() error {
	if campaign == nil || campaign.Status == OTACampaignCompleted || campaign.Status == OTACampaignAborted {
		return errors.New("completed or aborted OTA campaign cannot abort again")
	}
	campaign.Status = OTACampaignAborted
	return nil
}
