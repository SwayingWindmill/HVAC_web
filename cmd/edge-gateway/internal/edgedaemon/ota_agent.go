package edgedaemon

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	otaSlotA                   = "slot-a"
	otaSlotB                   = "slot-b"
	maxOTADesiredResponseBytes = 256 << 10
)

// OTAState tracks active slot, version, and pending boot health confirmation across restarts.
type OTAState struct {
	CurrentVersion        string                        `json:"currentVersion"`
	PreviousVersion       string                        `json:"previousVersion,omitempty"`
	ActiveSlot            string                        `json:"activeSlot,omitempty"`
	PreviousSlot          string                        `json:"previousSlot,omitempty"`
	Status                string                        `json:"status"`
	PendingStage          *edgecontrol.OTAStagePayload  `json:"pendingStage,omitempty"`
	HealthDeadlineUnixMs  int64                         `json:"healthDeadlineUnixMs,omitempty"`
	BootAttempts          int                           `json:"bootAttempts,omitempty"`
	ErrorReason           string                        `json:"errorReason,omitempty"`
	PendingRollbackReport *edgecontrol.OTAResultPayload `json:"pendingRollbackReport,omitempty"`
}

// OTAAgentOptions configures the A/B OTA download, cryptographic verification, and rollback agent.
type OTAAgentOptions struct {
	Dir             string
	CurrentVersion  string
	HardwareModel   string
	TargetOS        string
	TargetArch      string
	PlatformBaseURL string
	ServerName      string
	CAFile          string
	CertFile        string
	KeyFile         string
	TrustedKeys     map[string]string
	HTTPClient      *http.Client
	ProbeBinary     func(context.Context, string) error
	Now             func() time.Time
	Logger          *slog.Logger
}

// OTAAgent manages resumable package downloads, SHA-256/Ed25519 checks, and A/B binary swaps.
type OTAAgent struct {
	dir           string
	slotsDir      string
	downloadsDir  string
	statePath     string
	currentLink   string
	previousLink  string
	baseVersion   string
	hardwareModel string
	targetOS      string
	targetArch    string
	opts          OTAAgentOptions
	trustedKeys   map[string]ed25519.PublicKey
	now           func() time.Time
	logger        *slog.Logger
	mu            sync.Mutex
}

// NewOTAAgent validates trusted Ed25519 public keys and prepares A/B slot directories.
func NewOTAAgent(opts OTAAgentOptions) (*OTAAgent, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return nil, errors.New("OTA directory is required")
	}
	slotsDir := filepath.Join(dir, "slots")
	downloadsDir := filepath.Join(dir, "downloads")
	for _, sub := range []string{dir, slotsDir, downloadsDir} {
		if err := os.MkdirAll(sub, 0o750); err != nil {
			return nil, fmt.Errorf("create OTA directory %s: %w", sub, err)
		}
	}

	keys := make(map[string]ed25519.PublicKey, len(opts.TrustedKeys))
	for keyID, keyB64 := range opts.TrustedKeys {
		trimmedID := strings.TrimSpace(keyID)
		if trimmedID == "" {
			return nil, errors.New("OTA trusted key ID must not be empty")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid Ed25519 public key for %q", trimmedID)
		}
		keys[trimmedID] = ed25519.PublicKey(raw)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	targetOS := strings.TrimSpace(opts.TargetOS)
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	targetArch := edgecontrol.NormalizeTargetArch(firstNonEmpty(opts.TargetArch, runtime.GOARCH))
	baseVersion := firstNonEmpty(opts.CurrentVersion, "0.1.0")

	return &OTAAgent{
		dir:           dir,
		slotsDir:      slotsDir,
		downloadsDir:  downloadsDir,
		statePath:     filepath.Join(dir, "ota-state.json"),
		currentLink:   filepath.Join(dir, "current"),
		previousLink:  filepath.Join(dir, "previous"),
		baseVersion:   baseVersion,
		hardwareModel: strings.TrimSpace(opts.HardwareModel),
		targetOS:      targetOS,
		targetArch:    targetArch,
		opts:          opts,
		trustedKeys:   keys,
		now:           nowFn,
		logger:        logger,
	}, nil
}

// State returns the persisted OTA state (or an initialized idle baseline).
func (a *OTAAgent) State() (OTAState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadStateLocked()
}

// SeedSlotA populates slot-a and points the current symlink at it if no active symlink exists yet.
func (a *OTAAgent) SeedSlotA(binaryContent []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	slotPath := filepath.Join(a.slotsDir, otaSlotA)
	if err := writeFileAtomic(slotPath, binaryContent, 0o755); err != nil {
		return err
	}
	if err := swapSymlinkAtomic(a.currentLink, filepath.Join("slots", otaSlotA)); err != nil {
		return err
	}
	state, err := a.loadStateLocked()
	if err != nil {
		return err
	}
	state.ActiveSlot = otaSlotA
	if state.Status == "" {
		state.Status = edgecontrol.OTAStageSucceeded
	}
	return a.saveStateLocked(state)
}

// ApplyStage executes a cloud OTA_STAGE directive (APPLY or ROLLBACK) fail-closed.
func (a *OTAAgent) ApplyStage(ctx context.Context, stage edgecontrol.OTAStagePayload, report func(edgecontrol.OTAResultPayload)) error {
	if err := stage.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.loadStateLocked()
	if err != nil {
		return err
	}
	if stage.Action == edgecontrol.OTAActionApply &&
		state.Status == edgecontrol.OTAStageHealthChecking &&
		state.PendingStage != nil &&
		state.PendingStage.TaskID == stage.TaskID {
		return nil
	}
	curVer := firstNonEmpty(state.CurrentVersion, a.baseVersion)

	emit := func(stageName string, pct int, errCode, errMsg string) {
		if report == nil {
			return
		}
		report(edgecontrol.OTAResultPayload{
			CampaignID:     stage.CampaignID,
			TaskID:         stage.TaskID,
			PackageID:      stage.PackageID,
			Stage:          stageName,
			ProgressPct:    pct,
			CurrentVersion: curVer,
			TargetVersion:  stage.Version,
			ErrorCode:      errCode,
			ErrorMessage:   clampRunes(errMsg, 1024),
			OccurredAt:     max(a.now().UTC().UnixMilli(), 1),
		})
	}

	if stage.Action == edgecontrol.OTAActionRollback {
		return a.rollbackToPreviousLocked(&state, emit)
	}

	if err := a.checkCompatibility(stage, curVer); err != nil {
		emit(edgecontrol.OTAStageFailed, 0, "INCOMPATIBLE_TARGET", err.Error())
		return err
	}

	emit(edgecontrol.OTAStageDownloading, 10, "", "")
	partPath, err := a.downloadWithResume(ctx, stage)
	if err != nil {
		emit(edgecontrol.OTAStageFailed, 10, "DOWNLOAD_FAILED", err.Error())
		return err
	}

	emit(edgecontrol.OTAStageVerifying, 60, "", "")
	if code, verifyErr := a.verifyDownloadedFile(stage, partPath); verifyErr != nil {
		_ = os.Remove(partPath)
		emit(edgecontrol.OTAStageFailed, 60, code, verifyErr.Error())
		return verifyErr
	}

	emit(edgecontrol.OTAStageApplying, 80, "", "")
	prevSlot := a.detectActiveSlotLocked(state)
	nextSlot := otaSlotA
	if prevSlot == otaSlotA {
		nextSlot = otaSlotB
	}
	nextSlotPath := filepath.Join(a.slotsDir, nextSlot)
	if err := copyFileAtomic(partPath, nextSlotPath, 0o755); err != nil {
		emit(edgecontrol.OTAStageFailed, 80, "SLOT_WRITE_FAILED", err.Error())
		return err
	}
	_ = os.Remove(partPath)

	if a.opts.ProbeBinary != nil {
		if err := a.opts.ProbeBinary(ctx, nextSlotPath); err != nil {
			_ = os.Remove(nextSlotPath)
			emit(edgecontrol.OTAStageFailed, 85, "PRE_SWITCH_PROBE_FAILED", err.Error())
			return fmt.Errorf("pre-switch probe failed for %s: %w", nextSlotPath, err)
		}
	}

	if prevSlot != "" {
		if err := swapSymlinkAtomic(a.previousLink, filepath.Join("slots", prevSlot)); err != nil {
			emit(edgecontrol.OTAStageFailed, 88, "SYMLINK_FAILED", err.Error())
			return err
		}
	}

	timeoutSec := stage.HealthCheckTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultOTAHealthTimeoutSec
	}
	stageCopy := stage
	nextState := OTAState{
		CurrentVersion:       curVer,
		PreviousVersion:      curVer,
		ActiveSlot:           nextSlot,
		PreviousSlot:         prevSlot,
		Status:               edgecontrol.OTAStageHealthChecking,
		PendingStage:         &stageCopy,
		HealthDeadlineUnixMs: a.now().UTC().Add(time.Duration(timeoutSec) * time.Second).UnixMilli(),
		BootAttempts:         0,
	}
	if err := a.saveStateLocked(nextState); err != nil {
		emit(edgecontrol.OTAStageFailed, 90, "STATE_SAVE_FAILED", err.Error())
		return err
	}
	if err := swapSymlinkAtomic(a.currentLink, filepath.Join("slots", nextSlot)); err != nil {
		emit(edgecontrol.OTAStageFailed, 90, "SYMLINK_FAILED", err.Error())
		return err
	}

	emit(edgecontrol.OTAStageHealthChecking, 95, "", "")
	return nil
}

// VerifyPendingUpdateOnBoot checks whether an OTA update is awaiting health confirmation.
// If healthy, it commits SUCCEEDED; if healthCheck fails or the deadline elapsed, it swaps
// current back to the previous slot, persists ROLLED_BACK, and returns rolledBack=true.
func (a *OTAAgent) VerifyPendingUpdateOnBoot(
	ctx context.Context,
	healthCheck func(context.Context) error,
	report func(edgecontrol.OTAResultPayload),
) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, err := a.loadStateLocked()
	if err != nil {
		return false, err
	}
	if state.Status == edgecontrol.OTAStageRolledBack && (state.PendingRollbackReport != nil || state.PendingStage != nil) {
		var pendingReport edgecontrol.OTAResultPayload
		if state.PendingRollbackReport != nil {
			pendingReport = *state.PendingRollbackReport
			if pendingReport.OccurredAt < 1 {
				pendingReport.OccurredAt = max(a.now().UTC().UnixMilli(), 1)
			}
			if strings.TrimSpace(pendingReport.CurrentVersion) == "" {
				pendingReport.CurrentVersion = firstNonEmpty(state.CurrentVersion, a.baseVersion)
			}
		} else if state.PendingStage != nil {
			pending := *state.PendingStage
			pendingReport = edgecontrol.OTAResultPayload{
				CampaignID:     pending.CampaignID,
				TaskID:         pending.TaskID,
				PackageID:      pending.PackageID,
				Stage:          edgecontrol.OTAStageRolledBack,
				ProgressPct:    100,
				CurrentVersion: firstNonEmpty(state.CurrentVersion, a.baseVersion),
				TargetVersion:  pending.Version,
				ErrorCode:      "BOOT_GUARD_ROLLBACK",
				ErrorMessage:   clampRunes(firstNonEmpty(state.ErrorReason, "boot guard triggered OTA rollback"), 1024),
				OccurredAt:     max(a.now().UTC().UnixMilli(), 1),
			}
		}
		state.PendingRollbackReport = nil
		state.PendingStage = nil
		state.HealthDeadlineUnixMs = 0
		state.BootAttempts = 0
		if err := a.saveStateLocked(state); err != nil {
			return false, err
		}
		if report != nil {
			report(pendingReport)
		}
		return false, nil
	}
	if state.Status != edgecontrol.OTAStageHealthChecking || state.PendingStage == nil {
		return false, nil
	}

	pending := *state.PendingStage
	now := a.now().UTC()
	emit := func(stageName string, pct int, errCode, errMsg string) {
		if report == nil {
			return
		}
		report(edgecontrol.OTAResultPayload{
			CampaignID:     pending.CampaignID,
			TaskID:         pending.TaskID,
			PackageID:      pending.PackageID,
			Stage:          stageName,
			ProgressPct:    pct,
			CurrentVersion: firstNonEmpty(state.CurrentVersion, a.baseVersion),
			TargetVersion:  pending.Version,
			ErrorCode:      errCode,
			ErrorMessage:   clampRunes(errMsg, 1024),
			OccurredAt:     max(a.now().UTC().UnixMilli(), 1),
		})
	}

	var checkErr error
	remaining := time.UnixMilli(state.HealthDeadlineUnixMs).Sub(now)
	if remaining <= 0 {
		checkErr = errors.New("OTA startup health check deadline exceeded")
	} else if healthCheck != nil {
		checkCtx, cancel := context.WithTimeout(ctx, remaining)
		checkErr = healthCheck(checkCtx)
		cancel()
	}

	if checkErr != nil {
		if rbErr := a.restorePreviousOnHealthFailureLocked(&state, emit, checkErr.Error()); rbErr != nil {
			return false, rbErr
		}
		return true, nil
	}

	state.CurrentVersion = pending.Version
	state.Status = edgecontrol.OTAStageSucceeded
	state.PendingStage = nil
	state.HealthDeadlineUnixMs = 0
	state.BootAttempts = 0
	state.ErrorReason = ""
	state.PendingRollbackReport = nil
	if err := a.saveStateLocked(state); err != nil {
		return false, err
	}
	emit(edgecontrol.OTAStageSucceeded, 100, "", "")
	return false, nil
}

func (a *OTAAgent) rollbackToPreviousLocked(state *OTAState, emit func(string, int, string, string)) error {
	prevSlot := firstNonEmpty(state.PreviousSlot, slotNameFromSymlink(a.previousLink))
	if prevSlot == "" {
		err := errors.New("no previous OTA slot available for rollback")
		emit(edgecontrol.OTAStageFailed, 0, "NO_PREVIOUS_SLOT", err.Error())
		return err
	}
	prevPath := filepath.Join(a.slotsDir, prevSlot)
	if _, err := os.Stat(prevPath); err != nil {
		emit(edgecontrol.OTAStageFailed, 0, "PREVIOUS_SLOT_MISSING", err.Error())
		return err
	}
	if err := swapSymlinkAtomic(a.currentLink, filepath.Join("slots", prevSlot)); err != nil {
		emit(edgecontrol.OTAStageFailed, 0, "SYMLINK_FAILED", err.Error())
		return err
	}
	curActive := state.ActiveSlot
	state.ActiveSlot = prevSlot
	state.PreviousSlot = curActive
	if curActive != "" {
		_ = swapSymlinkAtomic(a.previousLink, filepath.Join("slots", curActive))
	}
	if strings.TrimSpace(state.PreviousVersion) != "" {
		state.CurrentVersion = state.PreviousVersion
	}
	state.Status = edgecontrol.OTAStageRolledBack
	state.PendingStage = nil
	state.HealthDeadlineUnixMs = 0
	state.BootAttempts = 0
	state.ErrorReason = ""
	state.PendingRollbackReport = nil
	if err := a.saveStateLocked(*state); err != nil {
		return err
	}
	emit(edgecontrol.OTAStageRolledBack, 100, "", "")
	return nil
}

func (a *OTAAgent) restorePreviousOnHealthFailureLocked(state *OTAState, emit func(string, int, string, string), reason string) error {
	prevSlot := firstNonEmpty(state.PreviousSlot, slotNameFromSymlink(a.previousLink))
	if prevSlot == "" {
		return errors.New("cannot roll back: previous OTA slot missing")
	}
	if err := swapSymlinkAtomic(a.currentLink, filepath.Join("slots", prevSlot)); err != nil {
		return err
	}
	state.ActiveSlot = prevSlot
	if strings.TrimSpace(state.PreviousVersion) != "" {
		state.CurrentVersion = state.PreviousVersion
	}
	state.Status = edgecontrol.OTAStageRolledBack
	state.PendingStage = nil
	state.HealthDeadlineUnixMs = 0
	state.BootAttempts = 0
	state.ErrorReason = clampRunes(strings.TrimSpace(reason), 1024)
	state.PendingRollbackReport = nil
	if err := a.saveStateLocked(*state); err != nil {
		return err
	}
	emit(edgecontrol.OTAStageRolledBack, 100, "HEALTH_CHECK_FAILED", reason)
	return nil
}

// RollbackPendingOTAOnEarlyFailure reverts an unconfirmed HEALTH_CHECKING OTA slot back to
// the previous slot when startup fails before Config/Daemon/OTAAgent initialization completes.
func RollbackPendingOTAOnEarlyFailure(otaDir, reason string) (bool, error) {
	dir := strings.TrimSpace(otaDir)
	if dir == "" {
		return false, nil
	}
	statePath := filepath.Join(dir, "ota-state.json")
	raw, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read OTA state %s: %w", statePath, err)
	}

	var state OTAState
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, fmt.Errorf("decode OTA state %s: %w", statePath, err)
	}
	if state.Status != edgecontrol.OTAStageHealthChecking {
		return false, nil
	}

	prevSlot := firstNonEmpty(state.PreviousSlot, slotNameFromSymlink(filepath.Join(dir, "previous")))
	if prevSlot == "" {
		return false, errors.New("cannot roll back: previous OTA slot missing")
	}
	prevPath := filepath.Join(dir, "slots", prevSlot)
	if _, err := os.Stat(prevPath); err != nil {
		return false, fmt.Errorf("cannot roll back: previous OTA slot %s unavailable: %w", prevSlot, err)
	}
	if err := swapSymlinkAtomic(filepath.Join(dir, "current"), filepath.Join("slots", prevSlot)); err != nil {
		return false, fmt.Errorf("swap OTA current symlink to %s: %w", prevSlot, err)
	}

	trimmedReason := clampRunes(firstNonEmpty(strings.TrimSpace(reason), "early startup failure triggered OTA rollback"), 1024)
	state.ActiveSlot = prevSlot
	if strings.TrimSpace(state.PreviousVersion) != "" {
		state.CurrentVersion = state.PreviousVersion
	}
	state.Status = edgecontrol.OTAStageRolledBack
	state.BootAttempts = 0
	state.HealthDeadlineUnixMs = 0
	state.ErrorReason = trimmedReason
	if state.PendingStage != nil {
		pending := *state.PendingStage
		state.PendingRollbackReport = &edgecontrol.OTAResultPayload{
			CampaignID:     pending.CampaignID,
			TaskID:         pending.TaskID,
			PackageID:      pending.PackageID,
			Stage:          edgecontrol.OTAStageRolledBack,
			ProgressPct:    100,
			CurrentVersion: firstNonEmpty(state.CurrentVersion, state.PreviousVersion, "0.1.0"),
			TargetVersion:  pending.Version,
			ErrorCode:      "EARLY_BOOT_ROLLBACK",
			ErrorMessage:   trimmedReason,
			OccurredAt:     max(time.Now().UTC().UnixMilli(), 1),
		}
		state.PendingStage = nil
	}

	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(statePath, append(encoded, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// PollDesired fetches any desired pending OTA stage from GET /gateway/v1/ota/desired using mTLS + PoP.
func (a *OTAAgent) PollDesired(ctx context.Context) (*edgecontrol.OTAStagePayload, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(a.opts.PlatformBaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("OTA platformBaseUrl is required")
	}
	client, id, err := a.httpClientAndIdentity()
	if err != nil {
		return nil, err
	}
	if a.opts.HTTPClient == nil {
		defer client.CloseIdleConnections()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/gateway/v1/ota/desired", nil)
	if err != nil {
		return nil, err
	}
	if err := signHTTPPoPIfPresent(req, id, a.now().UTC()); err != nil {
		return nil, fmt.Errorf("sign OTA desired poll PoP: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll OTA desired stage: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOTADesiredResponseBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read OTA desired stage response: %w", readErr)
	}
	if int64(len(body)) > maxOTADesiredResponseBytes {
		return nil, errors.New("OTA desired stage response exceeds maximum size")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll OTA desired stage failed with HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		GatewayID string                       `json:"gatewayId"`
		Stage     *edgecontrol.OTAStagePayload `json:"stage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode OTA desired stage response: %w", err)
	}
	if parsed.Stage == nil {
		return nil, nil
	}
	if err := parsed.Stage.Validate(); err != nil {
		return nil, fmt.Errorf("invalid OTA desired stage: %w", err)
	}
	return parsed.Stage, nil
}

func (a *OTAAgent) checkCompatibility(stage edgecontrol.OTAStagePayload, currentVer string) error {
	if !strings.EqualFold(strings.TrimSpace(stage.TargetOS), strings.TrimSpace(a.targetOS)) {
		return fmt.Errorf("OTA targetOs %q incompatible with gateway OS %q", stage.TargetOS, a.targetOS)
	}
	if edgecontrol.NormalizeTargetArch(stage.TargetArch) != edgecontrol.NormalizeTargetArch(a.targetArch) {
		return fmt.Errorf("OTA targetArch %q incompatible with gateway arch %q", stage.TargetArch, a.targetArch)
	}
	if len(stage.HardwareModels) > 0 && a.hardwareModel != "" {
		matched := false
		for _, model := range stage.HardwareModels {
			if strings.EqualFold(strings.TrimSpace(model), a.hardwareModel) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("OTA hardwareModels %v do not include gateway model %q", stage.HardwareModels, a.hardwareModel)
		}
	}
	if strings.TrimSpace(stage.MinCompatibleVersion) != "" {
		cmp, err := edgecontrol.CompareSemver(currentVer, stage.MinCompatibleVersion)
		if err != nil {
			return fmt.Errorf("compare currentVersion %q against minCompatibleVersion %q: %w", currentVer, stage.MinCompatibleVersion, err)
		}
		if cmp < 0 {
			return fmt.Errorf("currentVersion %q is older than minCompatibleVersion %q", currentVer, stage.MinCompatibleVersion)
		}
	}
	return nil
}

func (a *OTAAgent) downloadWithResume(ctx context.Context, stage edgecontrol.OTAStagePayload) (string, error) {
	partPath := filepath.Join(a.downloadsDir, stage.PackageID+".part")
	client, id, err := a.httpClientAndIdentity()
	if err != nil {
		return "", err
	}

	for attempt := 0; attempt < 2; attempt++ {
		var existingSize int64
		if info, statErr := os.Stat(partPath); statErr == nil {
			if info.Size() == stage.FileSizeBytes {
				return partPath, nil
			}
			if info.Size() > 0 && info.Size() < stage.FileSizeBytes {
				existingSize = info.Size()
			} else {
				_ = os.Remove(partPath)
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, stage.DownloadURL, nil)
		if err != nil {
			return "", err
		}
		if existingSize > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(existingSize, 10)+"-")
		}
		if err := signHTTPPoPIfPresent(req, id, a.now().UTC()); err != nil {
			return "", fmt.Errorf("sign OTA download PoP: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("download OTA package: %w", err)
		}
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && existingSize > 0 {
			_ = resp.Body.Close()
			_ = os.Remove(partPath)
			continue
		}

		var openFlags int
		var startOffset int64
		switch resp.StatusCode {
		case http.StatusPartialContent:
			if existingSize == 0 {
				_ = resp.Body.Close()
				return "", errors.New("unexpected 206 Partial Content without Range request")
			}
			if cr := strings.TrimSpace(resp.Header.Get("Content-Range")); cr != "" {
				wantPrefix := fmt.Sprintf("bytes %d-", existingSize)
				if !strings.HasPrefix(cr, wantPrefix) {
					_ = resp.Body.Close()
					_ = os.Remove(partPath)
					continue
				}
			}
			openFlags = os.O_WRONLY | os.O_APPEND
			startOffset = existingSize
		case http.StatusOK:
			openFlags = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
			startOffset = 0
		default:
			_ = resp.Body.Close()
			return "", fmt.Errorf("OTA package download failed with HTTP %d", resp.StatusCode)
		}

		outFile, err := os.OpenFile(partPath, openFlags, 0o644)
		if err != nil {
			_ = resp.Body.Close()
			return "", err
		}
		remaining := stage.FileSizeBytes - startOffset
		written, copyErr := io.Copy(outFile, io.LimitReader(resp.Body, remaining+1))
		syncErr := outFile.Sync()
		closeErr := outFile.Close()
		_ = resp.Body.Close()
		if copyErr != nil {
			return "", fmt.Errorf("write OTA partial file: %w", copyErr)
		}
		if syncErr != nil {
			return "", syncErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		totalSize := startOffset + written
		if totalSize != stage.FileSizeBytes {
			_ = os.Remove(partPath)
			return "", fmt.Errorf("downloaded OTA file size %d != expected %d", totalSize, stage.FileSizeBytes)
		}
		return partPath, nil
	}
	return "", errors.New("OTA package download failed after range reset")
}

func (a *OTAAgent) verifyDownloadedFile(stage edgecontrol.OTAStagePayload, partPath string) (string, error) {
	f, err := os.Open(partPath)
	if err != nil {
		return "FILE_READ_FAILED", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		_ = f.Close()
		return "FILE_READ_FAILED", err
	}
	_ = f.Close()
	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA != strings.ToLower(stage.SHA256) {
		return "CHECKSUM_MISMATCH", fmt.Errorf("OTA package sha256 mismatch: got %s, want %s", actualSHA, stage.SHA256)
	}

	pub, ok := a.trustedKeys[strings.TrimSpace(stage.SigningKeyID)]
	if !ok {
		return "UNKNOWN_SIGNING_KEY", fmt.Errorf("unknown OTA signingKeyId %q", stage.SigningKeyID)
	}
	if err := edgecontrol.VerifyOTASignatureEd25519(
		pub,
		stage.PackageID,
		stage.Version,
		stage.Component,
		stage.TargetOS,
		stage.TargetArch,
		actualSHA,
		stage.FileSizeBytes,
		stage.SignatureB64,
	); err != nil {
		return "SIGNATURE_INVALID", err
	}
	return "", nil
}

func (a *OTAAgent) httpClientAndIdentity() (*http.Client, tlsIdentity, error) {
	if a.opts.HTTPClient != nil {
		if strings.TrimSpace(a.opts.CAFile) != "" {
			id, err := loadTLSIdentity(a.opts.CAFile, a.opts.CertFile, a.opts.KeyFile)
			if err == nil {
				return a.opts.HTTPClient, id, nil
			}
		}
		return a.opts.HTTPClient, tlsIdentity{}, nil
	}
	if strings.TrimSpace(a.opts.CAFile) == "" {
		return &http.Client{Timeout: 60 * time.Second}, tlsIdentity{}, nil
	}
	return newMTLSHTTPClient(a.opts.CAFile, a.opts.CertFile, a.opts.KeyFile, a.opts.ServerName, 60*time.Second)
}

func (a *OTAAgent) detectActiveSlotLocked(state OTAState) string {
	if fromLink := slotNameFromSymlink(a.currentLink); fromLink != "" {
		return fromLink
	}
	return strings.TrimSpace(state.ActiveSlot)
}

func (a *OTAAgent) loadStateLocked() (OTAState, error) {
	raw, err := os.ReadFile(a.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return OTAState{
			CurrentVersion: a.baseVersion,
			Status:         edgecontrol.OTAStageSucceeded,
		}, nil
	}
	if err != nil {
		return OTAState{}, fmt.Errorf("read OTA state: %w", err)
	}
	var state OTAState
	if err := json.Unmarshal(raw, &state); err != nil {
		return OTAState{}, fmt.Errorf("decode OTA state: %w", err)
	}
	if strings.TrimSpace(state.CurrentVersion) == "" {
		state.CurrentVersion = a.baseVersion
	}
	return state, nil
}

func (a *OTAAgent) saveStateLocked(state OTAState) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(a.statePath, append(encoded, '\n'), 0o600)
}

func slotNameFromSymlink(linkPath string) string {
	target, err := os.Readlink(linkPath)
	if err != nil {
		return ""
	}
	base := filepath.Base(target)
	if base == otaSlotA || base == otaSlotB {
		return base
	}
	return ""
}

func swapSymlinkAtomic(linkPath, target string) error {
	tmpLink := linkPath + ".tmp"
	_ = os.Remove(tmpLink)
	if err := os.Symlink(target, tmpLink); err != nil {
		return err
	}
	if err := os.Rename(tmpLink, linkPath); err != nil {
		_ = os.Remove(tmpLink)
		return err
	}
	return nil
}

func copyFileAtomic(srcPath, dstPath string, mode os.FileMode) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return writeFileAtomic(dstPath, content, mode)
}
