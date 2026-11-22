package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const (
	OTAPackageStatusActive  = "ACTIVE"
	OTAPackageStatusRetired = "RETIRED"

	OTACampaignStatusCanaryRunning  = "CANARY_RUNNING"
	OTACampaignStatusRolloutRunning = "ROLLOUT_RUNNING"
	OTACampaignStatusPaused         = "PAUSED"
	OTACampaignStatusCompleted      = "COMPLETED"
	OTACampaignStatusRolledBack     = "ROLLED_BACK"
	OTACampaignStatusFailed         = "FAILED"

	OTAPauseReasonManual                 = "MANUAL_PAUSE"
	OTAPauseReasonFailureRateExceeded    = "FAILURE_RATE_THRESHOLD_EXCEEDED"
	OTAPauseReasonCanaryAwaitingPromote  = "CANARY_AWAITING_PROMOTION"

	OTAWaveCanary  = "CANARY"
	OTAWaveRollout = "ROLLOUT"

	OTATaskStagePending    = "PENDING"
	OTATaskStageDispatched = "DISPATCHED"
	OTATaskStageCancelled  = "CANCELLED"

	DefaultOTAHealthCheckTimeoutSec = 120
	MinOTAHealthCheckTimeoutSec     = 10
	MaxOTAHealthCheckTimeoutSec     = 1800

	maxOTATagCount        = 64
	maxOTATagValueRunes   = 256
	maxOTASiteIDsCount    = 256
	maxOTAHardwareModels  = 32
	maxOTAReasonRunes     = 1000
	maxOTACampaignName    = 160
	maxOTAReleaseNotes    = 2000
	maxOTAErrorCodeRunes  = 128
	maxOTAErrorMsgRunes   = 1024
)

var (
	ErrOTAInvalid   = errors.New("OTA request is invalid")
	ErrOTAConflict  = errors.New("OTA resource state conflict")
	ErrOTANoTargets = errors.New("no active gateways matched the OTA campaign target filters")

	otaTagKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,63}$`)
)

// OTAPackage represents an immutable signed software artifact registered for edge gateways.
type OTAPackage struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenantId"`
	Component            string    `json:"component"`
	Version              string    `json:"version"`
	TargetOS             string    `json:"targetOs"`
	TargetArch           string    `json:"targetArch"`
	HardwareModels       []string  `json:"hardwareModels"`
	MinCompatibleVersion *string   `json:"minCompatibleVersion,omitempty"`
	FileName             string    `json:"fileName"`
	FileSizeBytes        int64     `json:"fileSizeBytes"`
	SHA256               string    `json:"sha256"`
	SignatureAlg         string    `json:"signatureAlg"`
	SigningKeyID         string    `json:"signingKeyId"`
	SignatureB64         string    `json:"signatureB64"`
	StorageRelPath       string    `json:"-"`
	ReleaseNotes         string    `json:"releaseNotes"`
	Status               string    `json:"status"`
	UploadedBy           string    `json:"uploadedBy"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// OTACampaignSummary summarizes an OTA campaign configuration and live per-stage counters.
type OTACampaignSummary struct {
	ID                       string            `json:"id"`
	TenantID                 string            `json:"tenantId"`
	Name                     string            `json:"name"`
	PackageID                string            `json:"packageId"`
	PackageVersion           string            `json:"packageVersion"`
	Component                string            `json:"component"`
	TargetOS                 string            `json:"targetOs"`
	TargetArch               string            `json:"targetArch"`
	TargetSiteIDs            []string          `json:"targetSiteIds"`
	TargetTags               map[string]string `json:"targetTags"`
	CanaryPercent            int               `json:"canaryPercent"`
	FailureRateThresholdPct  int               `json:"failureRateThresholdPct"`
	HealthCheckTimeoutSec    int               `json:"healthCheckTimeoutSec"`
	AutoPromoteRollout       bool              `json:"autoPromoteRollout"`
	Status                   string            `json:"status"`
	PauseReason              *string           `json:"pauseReason,omitempty"`
	Reason                   string            `json:"reason"`
	TotalCount               int               `json:"totalCount"`
	PendingCount             int               `json:"pendingCount"`
	InProgressCount          int               `json:"inProgressCount"`
	SucceededCount           int               `json:"succeededCount"`
	FailedCount              int               `json:"failedCount"`
	RolledBackCount          int               `json:"rolledBackCount"`
	CancelledCount           int               `json:"cancelledCount"`
	FailureRatePct           int               `json:"failureRatePct"`
	CreatedBy                string            `json:"createdBy"`
	UpdatedBy                string            `json:"updatedBy"`
	CreatedAt                time.Time         `json:"createdAt"`
	UpdatedAt                time.Time         `json:"updatedAt"`
	CompletedAt              *time.Time        `json:"completedAt,omitempty"`
}

// OTADeviceTask represents the execution state of one gateway within an OTA campaign.
type OTADeviceTask struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenantId"`
	CampaignID     string     `json:"campaignId"`
	PackageID      string     `json:"packageId"`
	GatewayID      string     `json:"gatewayId"`
	SiteID         string     `json:"siteId"`
	GatewayCode    string     `json:"gatewayCode"`
	GatewayName    string     `json:"gatewayName"`
	GatewayOnline  bool       `json:"gatewayOnline"`
	Wave           string     `json:"wave"`
	DesiredAction  string     `json:"desiredAction"`
	Stage          string     `json:"stage"`
	ProgressPct    int        `json:"progressPct"`
	CurrentVersion string     `json:"currentVersion"`
	TargetVersion  string     `json:"targetVersion"`
	ErrorCode      *string    `json:"errorCode,omitempty"`
	ErrorMessage   *string    `json:"errorMessage,omitempty"`
	DispatchedAt   *time.Time `json:"dispatchedAt,omitempty"`
	LastReportedAt *time.Time `json:"lastReportedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// OTACampaignDetail returns full campaign state together with its package metadata and per-gateway tasks.
type OTACampaignDetail struct {
	OTACampaignSummary
	Package OTAPackage      `json:"package"`
	Tasks   []OTADeviceTask `json:"tasks"`
}

// OTAStageDispatch pairs a target gateway UUID with the OTA_STAGE payload to deliver over MQTT.
type OTAStageDispatch struct {
	GatewayID string
	Stage     edgecontrol.OTAStagePayload
}

type CreateOTAPackageInput struct {
	PackageID            string
	Component            string
	Version              string
	TargetOS             string
	TargetArch           string
	HardwareModels       []string
	MinCompatibleVersion *string
	FileName             string
	FileSizeBytes        int64
	SHA256               string
	SigningKeyID         string
	SignatureB64         string
	StorageRelPath       string
	ReleaseNotes         string
}

type ListOTAPackagesFilter struct {
	Component  string
	TargetArch string
	Status     string
	Limit      int
}

type CreateOTACampaignInput struct {
	Name                    string            `json:"name"`
	PackageID               string            `json:"packageId"`
	TargetSiteIDs           []string          `json:"targetSiteIds,omitempty"`
	TargetTags              map[string]string `json:"targetTags,omitempty"`
	CanaryPercent           int               `json:"canaryPercent"`
	FailureRateThresholdPct int               `json:"failureRateThresholdPct"`
	HealthCheckTimeoutSec   *int              `json:"healthCheckTimeoutSec,omitempty"`
	AutoPromoteRollout      *bool             `json:"autoPromoteRollout,omitempty"`
	Reason                  string            `json:"reason"`
}

type ListOTACampaignsFilter struct {
	Status string
	Limit  int
}

type OTACampaignActionInput struct {
	Reason string `json:"reason"`
}

type OTAStore interface {
	ListOTAPackages(ctx context.Context, claims registryauth.GrantClaims, filter ListOTAPackagesFilter) ([]OTAPackage, error)
	CreateOTAPackage(ctx context.Context, claims registryauth.GrantClaims, input CreateOTAPackageInput) (OTAPackage, error)
	GetOTAPackageForGateway(ctx context.Context, gatewayID, packageID string) (OTAPackage, error)

	ListOTACampaigns(ctx context.Context, claims registryauth.GrantClaims, filter ListOTACampaignsFilter) ([]OTACampaignSummary, error)
	GetOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID string) (OTACampaignDetail, error)
	CreateOTACampaign(ctx context.Context, claims registryauth.GrantClaims, input CreateOTACampaignInput) (OTACampaignDetail, []OTAStageDispatch, error)
	PauseOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, error)
	ResumeOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, []OTAStageDispatch, error)
	RollbackOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, []OTAStageDispatch, error)

	GetGatewayDesiredOTAStage(ctx context.Context, gatewayID string) (*edgecontrol.OTAStagePayload, error)
	ApplyGatewayOTAResult(ctx context.Context, gatewayID string, result edgecontrol.OTAResultPayload) (OTADeviceTask, []OTAStageDispatch, error)
}

// OTACampaignTransitionDecision captures the deterministic campaign state transition computed from task states.
type OTACampaignTransitionDecision struct {
	Changed             bool
	NewStatus           string
	NewPauseReason      *string
	CompletedAt         *time.Time
	PromoteTaskIDs      []string
	ShouldClearComplete bool
}

// BuildOTAStageDownloadURL builds the mTLS gateway OTA binary download URL for packageID.
func BuildOTAStageDownloadURL(publicBaseURL, packageID string) string {
	pkgID := strings.ToLower(strings.TrimSpace(packageID))
	rel := "/gateway/v1/ota/packages/" + url.PathEscape(pkgID) + "/download"
	base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if base == "" {
		return rel
	}
	return base + rel
}

// NormalizeOTAPackageInput validates and canonicalizes OTA package metadata prior to persistence.
func NormalizeOTAPackageInput(input CreateOTAPackageInput) (CreateOTAPackageInput, error) {
	out := input
	if strings.TrimSpace(out.PackageID) != "" {
		out.PackageID = strings.ToLower(strings.TrimSpace(out.PackageID))
		if !gatewayIDPattern.MatchString(out.PackageID) {
			return CreateOTAPackageInput{}, fmt.Errorf("%w: packageId must be UUIDv7", ErrOTAInvalid)
		}
	}
	out.Component = strings.ToUpper(strings.TrimSpace(out.Component))
	switch out.Component {
	case edgecontrol.OTAComponentEdgeGateway, edgecontrol.OTAComponentFirmware:
	default:
		return CreateOTAPackageInput{}, fmt.Errorf("%w: unsupported component %q", ErrOTAInvalid, input.Component)
	}
	out.Version = strings.TrimSpace(out.Version)
	if out.Version == "" || utf8.RuneCountInString(out.Version) > 64 {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: version must be 1..64 characters", ErrOTAInvalid)
	}
	if _, err := edgecontrol.CompareSemver(out.Version, out.Version); err != nil {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: version %q is not valid SemVer 2.0", ErrOTAInvalid, out.Version)
	}
	out.TargetOS = strings.ToLower(strings.TrimSpace(out.TargetOS))
	if out.TargetOS == "" {
		out.TargetOS = "linux"
	}
	if out.TargetOS != "linux" {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: unsupported targetOs %q (must be linux)", ErrOTAInvalid, input.TargetOS)
	}
	out.TargetArch = edgecontrol.NormalizeTargetArch(out.TargetArch)
	switch out.TargetArch {
	case "armv7l", "arm64", "amd64":
	default:
		return CreateOTAPackageInput{}, fmt.Errorf("%w: unsupported targetArch %q (allowed: armv7l, arm64, amd64)", ErrOTAInvalid, input.TargetArch)
	}
	if len(out.HardwareModels) > maxOTAHardwareModels {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: at most %d hardwareModels allowed", ErrOTAInvalid, maxOTAHardwareModels)
	}
	seenModels := make(map[string]struct{}, len(out.HardwareModels))
	cleanedModels := make([]string, 0, len(out.HardwareModels))
	for _, m := range out.HardwareModels {
		trimmed := strings.TrimSpace(m)
		if trimmed == "" || len(trimmed) > 64 {
			return CreateOTAPackageInput{}, fmt.Errorf("%w: hardwareModel must be 1..64 characters", ErrOTAInvalid)
		}
		norm := strings.ToLower(trimmed)
		if _, dup := seenModels[norm]; dup {
			continue
		}
		seenModels[norm] = struct{}{}
		cleanedModels = append(cleanedModels, trimmed)
	}
	out.HardwareModels = cleanedModels

	if out.MinCompatibleVersion != nil {
		minVer := strings.TrimSpace(*out.MinCompatibleVersion)
		if minVer == "" {
			out.MinCompatibleVersion = nil
		} else {
			if _, err := edgecontrol.CompareSemver(minVer, minVer); err != nil {
				return CreateOTAPackageInput{}, fmt.Errorf("%w: minCompatibleVersion %q is not valid SemVer 2.0", ErrOTAInvalid, minVer)
			}
			cmp, err := edgecontrol.CompareSemver(out.Version, minVer)
			if err != nil || cmp < 0 {
				return CreateOTAPackageInput{}, fmt.Errorf("%w: minCompatibleVersion %q must not exceed package version %q", ErrOTAInvalid, minVer, out.Version)
			}
			out.MinCompatibleVersion = &minVer
		}
	}

	out.FileName = strings.TrimSpace(out.FileName)
	if out.FileName == "" || utf8.RuneCountInString(out.FileName) > 255 {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: fileName must be 1..255 characters", ErrOTAInvalid)
	}
	if out.FileSizeBytes <= 0 {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: fileSizeBytes must be positive", ErrOTAInvalid)
	}
	out.SHA256 = strings.ToLower(strings.TrimSpace(out.SHA256))
	if !edgecontrol.IsLowerSHA256Hex(out.SHA256) {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: sha256 must be 64 lowercase hex characters", ErrOTAInvalid)
	}
	out.SigningKeyID = strings.TrimSpace(out.SigningKeyID)
	out.SignatureB64 = strings.TrimSpace(out.SignatureB64)
	if out.SigningKeyID == "" || out.SignatureB64 == "" {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: signingKeyId and signatureB64 are required", ErrOTAInvalid)
	}
	out.StorageRelPath = strings.TrimSpace(out.StorageRelPath)
	if out.StorageRelPath == "" {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: storageRelPath is required", ErrOTAInvalid)
	}
	out.ReleaseNotes = strings.TrimSpace(out.ReleaseNotes)
	if utf8.RuneCountInString(out.ReleaseNotes) > maxOTAReleaseNotes {
		return CreateOTAPackageInput{}, fmt.Errorf("%w: releaseNotes must not exceed %d characters", ErrOTAInvalid, maxOTAReleaseNotes)
	}
	return out, nil
}

// ComputeCanaryTargetCount computes ceil(total * canaryPercent / 100), clamped to [1, total].
func ComputeCanaryTargetCount(total, canaryPercent int) int {
	if total <= 0 {
		return 0
	}
	if canaryPercent <= 0 {
		return 1
	}
	if canaryPercent >= 100 {
		return total
	}
	count := (total*canaryPercent + 99) / 100
	if count < 1 {
		return 1
	}
	if count > total {
		return total
	}
	return count
}

// IsOTATaskFailureOrRollbackStage reports whether stage represents a failure or rollback outcome for failure-rate accounting.
func IsOTATaskFailureOrRollbackStage(stage string) bool {
	return stage == edgecontrol.OTAStageFailed || stage == edgecontrol.OTAStageRolledBack
}

// IsOTATaskTerminal reports whether a task has reached a terminal state for its current desiredAction.
func IsOTATaskTerminal(desiredAction, stage string) bool {
	switch stage {
	case OTATaskStageCancelled:
		return true
	case edgecontrol.OTAStageFailed, edgecontrol.OTAStageRolledBack:
		return true
	case edgecontrol.OTAStageSucceeded:
		return desiredAction != edgecontrol.OTAActionRollback
	default:
		return false
	}
}

func otaApplyStageRank(stage string) int {
	switch stage {
	case OTATaskStagePending:
		return 0
	case OTATaskStageDispatched:
		return 1
	case edgecontrol.OTAStageDownloading:
		return 2
	case edgecontrol.OTAStageVerifying:
		return 3
	case edgecontrol.OTAStageApplying:
		return 4
	case edgecontrol.OTAStageHealthChecking:
		return 5
	case edgecontrol.OTAStageSucceeded, edgecontrol.OTAStageRolledBack, edgecontrol.OTAStageFailed, OTATaskStageCancelled:
		return 6
	default:
		return -1
	}
}

// EvaluateOTATaskProgress applies an uplink/HTTP OTAResultPayload to a single task row,
// returning updated=false if the report is stale, terminal-duplicate, or out of order.
func EvaluateOTATaskProgress(task OTADeviceTask, res edgecontrol.OTAResultPayload, now time.Time) (OTADeviceTask, bool, error) {
	if err := res.Validate(); err != nil {
		return task, false, fmt.Errorf("%w: %v", ErrOTAInvalid, err)
	}
	if !strings.EqualFold(task.ID, res.TaskID) || !strings.EqualFold(task.CampaignID, res.CampaignID) || !strings.EqualFold(task.PackageID, res.PackageID) {
		return task, false, fmt.Errorf("%w: OTA result task/campaign/package mismatch", ErrOTAInvalid)
	}
	if IsOTATaskTerminal(task.DesiredAction, task.Stage) {
		return task, false, nil
	}

	next := task
	nowUTC := now.UTC()
	reportedAt := time.UnixMilli(res.OccurredAt).UTC()
	if reportedAt.IsZero() {
		reportedAt = nowUTC
	}

	if task.DesiredAction == edgecontrol.OTAActionRollback {
		switch res.Stage {
		case edgecontrol.OTAStageRolledBack, edgecontrol.OTAStageFailed:
			fin := nowUTC
			next.Stage = res.Stage
			next.ProgressPct = 100
			next.CurrentVersion = strings.TrimSpace(res.CurrentVersion)
			next.ErrorCode = optionalTrimmedPtr(res.ErrorCode, maxOTAErrorCodeRunes)
			next.ErrorMessage = optionalTrimmedPtr(res.ErrorMessage, maxOTAErrorMsgRunes)
			next.LastReportedAt = &reportedAt
			next.FinishedAt = &fin
			next.UpdatedAt = nowUTC
			return next, true, nil
		default:
			return task, false, nil
		}
	}

	// APPLY flow: ignore out-of-order lower-ranked stages (or non-advancing progress).
	curRank := otaApplyStageRank(task.Stage)
	newRank := otaApplyStageRank(res.Stage)
	if newRank < curRank {
		return task, false, nil
	}
	if newRank == curRank && res.ProgressPct <= task.ProgressPct {
		return task, false, nil
	}

	next.Stage = res.Stage
	next.ProgressPct = res.ProgressPct
	if cur := strings.TrimSpace(res.CurrentVersion); cur != "" {
		next.CurrentVersion = cur
	}
	next.ErrorCode = optionalTrimmedPtr(res.ErrorCode, maxOTAErrorCodeRunes)
	next.ErrorMessage = optionalTrimmedPtr(res.ErrorMessage, maxOTAErrorMsgRunes)
	next.LastReportedAt = &reportedAt
	next.UpdatedAt = nowUTC
	if IsOTATaskTerminal(edgecontrol.OTAActionApply, res.Stage) {
		fin := nowUTC
		next.FinishedAt = &fin
	}
	return next, true, nil
}

// EvaluateOTACampaignTransition computes whether an active campaign must trip its circuit breaker,
// promote rollout wave tasks, pause for manual promotion, or complete based on current task states.
func EvaluateOTACampaignTransition(
	campaignStatus string,
	failureRateThresholdPct int,
	autoPromoteRollout bool,
	tasks []OTADeviceTask,
	now time.Time,
) OTACampaignTransitionDecision {
	if campaignStatus != OTACampaignStatusCanaryRunning && campaignStatus != OTACampaignStatusRolloutRunning {
		return OTACampaignTransitionDecision{}
	}

	activeWave := OTAWaveCanary
	if campaignStatus == OTACampaignStatusRolloutRunning {
		activeWave = OTAWaveRollout
	}

	var (
		allTotal           int
		allFailed          int
		allTerminal        = true
		waveTotal          int
		waveFailed         int
		canaryTerminal     = true
		canaryCount        int
		pendingRolloutIDs  []string
	)

	for _, task := range tasks {
		if task.Stage == OTATaskStageCancelled {
			continue
		}
		allTotal++
		if IsOTATaskFailureOrRollbackStage(task.Stage) {
			allFailed++
		}
		term := IsOTATaskTerminal(task.DesiredAction, task.Stage)
		if !term {
			allTerminal = false
		}
		if task.Wave == activeWave {
			waveTotal++
			if IsOTATaskFailureOrRollbackStage(task.Stage) {
				waveFailed++
			}
		}
		if task.Wave == OTAWaveCanary {
			canaryCount++
			if !term {
				canaryTerminal = false
			}
		}
		if task.Wave == OTAWaveRollout && task.Stage == OTATaskStagePending {
			pendingRolloutIDs = append(pendingRolloutIDs, task.ID)
		}
	}

	if allTotal == 0 {
		return OTACampaignTransitionDecision{}
	}

	// Exact integer failure-rate circuit breaker check on active wave and whole campaign.
	waveTripped := waveFailed > 0 && waveTotal > 0 && waveFailed*100 >= waveTotal*failureRateThresholdPct
	allTripped := allFailed > 0 && allTotal > 0 && allFailed*100 >= allTotal*failureRateThresholdPct
	if waveTripped || allTripped {
		reason := OTAPauseReasonFailureRateExceeded
		return OTACampaignTransitionDecision{
			Changed:        true,
			NewStatus:      OTACampaignStatusPaused,
			NewPauseReason: &reason,
		}
	}

	if campaignStatus == OTACampaignStatusCanaryRunning && canaryCount > 0 && canaryTerminal {
		if len(pendingRolloutIDs) > 0 {
			if autoPromoteRollout {
				return OTACampaignTransitionDecision{
					Changed:        true,
					NewStatus:      OTACampaignStatusRolloutRunning,
					PromoteTaskIDs: pendingRolloutIDs,
				}
			}
			reason := OTAPauseReasonCanaryAwaitingPromote
			return OTACampaignTransitionDecision{
				Changed:        true,
				NewStatus:      OTACampaignStatusPaused,
				NewPauseReason: &reason,
			}
		}
		doneAt := now.UTC()
		return OTACampaignTransitionDecision{
			Changed:     true,
			NewStatus:   OTACampaignStatusCompleted,
			CompletedAt: &doneAt,
		}
	}

	if allTerminal {
		doneAt := now.UTC()
		return OTACampaignTransitionDecision{
			Changed:     true,
			NewStatus:   OTACampaignStatusCompleted,
			CompletedAt: &doneAt,
		}
	}

	return OTACampaignTransitionDecision{}
}

// BuildOTAStageDispatches turns package metadata and tasks into validated OTAStagePayload dispatches.
func BuildOTAStageDispatches(
	pkg OTAPackage,
	tasks []OTADeviceTask,
	healthCheckTimeoutSec int,
	publicBaseURL string,
) []OTAStageDispatch {
	if len(tasks) == 0 {
		return nil
	}
	timeoutSec := healthCheckTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = DefaultOTAHealthCheckTimeoutSec
	}
	downloadURL := BuildOTAStageDownloadURL(publicBaseURL, pkg.ID)
	minVer := ""
	if pkg.MinCompatibleVersion != nil {
		minVer = *pkg.MinCompatibleVersion
	}
	models := slices.Clone(pkg.HardwareModels)
	dispatches := make([]OTAStageDispatch, 0, len(tasks))
	for _, task := range tasks {
		action := task.DesiredAction
		if action == "" {
			action = edgecontrol.OTAActionApply
		}
		dispatches = append(dispatches, OTAStageDispatch{
			GatewayID: task.GatewayID,
			Stage: edgecontrol.OTAStagePayload{
				CampaignID:            task.CampaignID,
				TaskID:                task.ID,
				PackageID:             pkg.ID,
				Version:               pkg.Version,
				Component:             pkg.Component,
				TargetOS:              pkg.TargetOS,
				TargetArch:            pkg.TargetArch,
				HardwareModels:        models,
				MinCompatibleVersion:  minVer,
				DownloadURL:           downloadURL,
				FileSizeBytes:         pkg.FileSizeBytes,
				SHA256:                pkg.SHA256,
				SignatureAlg:          pkg.SignatureAlg,
				SignatureB64:          pkg.SignatureB64,
				SigningKeyID:          pkg.SigningKeyID,
				HealthCheckTimeoutSec: timeoutSec,
				Action:                action,
			},
		})
	}
	return dispatches
}

func computeCampaignTaskCounters(summary *OTACampaignSummary, tasks []OTADeviceTask) {
	summary.TotalCount = len(tasks)
	var nonCancelled, failedOrRolledBack int
	for _, task := range tasks {
		switch task.Stage {
		case OTATaskStagePending:
			summary.PendingCount++
			nonCancelled++
		case OTATaskStageDispatched,
			edgecontrol.OTAStageDownloading,
			edgecontrol.OTAStageVerifying,
			edgecontrol.OTAStageApplying,
			edgecontrol.OTAStageHealthChecking:
			summary.InProgressCount++
			nonCancelled++
		case edgecontrol.OTAStageSucceeded:
			summary.SucceededCount++
			nonCancelled++
		case edgecontrol.OTAStageFailed:
			summary.FailedCount++
			nonCancelled++
			failedOrRolledBack++
		case edgecontrol.OTAStageRolledBack:
			summary.RolledBackCount++
			nonCancelled++
			failedOrRolledBack++
		case OTATaskStageCancelled:
			summary.CancelledCount++
		}
	}
	if nonCancelled > 0 && failedOrRolledBack > 0 {
		summary.FailureRatePct = (failedOrRolledBack * 100) / nonCancelled
	}
}

func (store *Store) beginTenantWithSites(ctx context.Context, claims registryauth.GrantClaims, readOnly bool) (pgx.Tx, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(claims.TenantID) {
		return nil, ErrCredentialForbidden
	}
	opts := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	if readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := store.pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := setTenant(ctx, tx, claims.TenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	authorized := effectiveAuthorizedSites(claims)
	siteArray := "{}"
	if len(authorized) > 0 {
		siteArray = "{" + strings.Join(authorized, ",") + "}"
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.authorized_site_ids', $1, true)`, siteArray); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set authorized sites context: %w", err)
	}
	return tx, nil
}

func effectiveAuthorizedSites(claims registryauth.GrantClaims) []string {
	if len(claims.AllowedSiteIDs) == 0 {
		return nil
	}
	denied := make(map[string]struct{}, len(claims.DeniedSiteIDs))
	for _, s := range claims.DeniedSiteIDs {
		denied[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}
	out := make([]string, 0, len(claims.AllowedSiteIDs))
	seen := make(map[string]struct{}, len(claims.AllowedSiteIDs))
	for _, raw := range claims.AllowedSiteIDs {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !gatewayIDPattern.MatchString(id) {
			continue
		}
		if _, isDenied := denied[id]; isDenied {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func validateOTATagFilterMap(input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return map[string]string{}, nil
	}
	if len(input) > maxOTATagCount {
		return nil, fmt.Errorf("%w: targetTags cannot exceed %d entries", ErrOTAInvalid, maxOTATagCount)
	}
	out := make(map[string]string, len(input))
	for rawK, rawV := range input {
		key := strings.TrimSpace(rawK)
		val := strings.TrimSpace(rawV)
		if !otaTagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("%w: invalid tag key %q", ErrOTAInvalid, key)
		}
		valLen := utf8.RuneCountInString(val)
		if valLen == 0 || valLen > maxOTATagValueRunes {
			return nil, fmt.Errorf("%w: tag %q value must be 1..%d characters", ErrOTAInvalid, key, maxOTATagValueRunes)
		}
		out[key] = val
	}
	return out, nil
}

func validateOTAReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	n := utf8.RuneCountInString(trimmed)
	if n == 0 || n > maxOTAReasonRunes {
		return "", fmt.Errorf("%w: reason must be 1..%d characters", ErrOTAInvalid, maxOTAReasonRunes)
	}
	return trimmed, nil
}

func campaignScopeAllows(claims registryauth.GrantClaims, targetSiteIDs []string, tasks []OTADeviceTask) bool {
	if !gatewayIDPattern.MatchString(claims.TenantID) {
		return false
	}
	for _, siteID := range targetSiteIDs {
		if !registryauth.ScopeAllows(claims, siteID) {
			return false
		}
	}
	for _, task := range tasks {
		if !registryauth.ScopeAllows(claims, task.SiteID) {
			return false
		}
	}
	return true
}

func (store *Store) ListOTAPackages(ctx context.Context, claims registryauth.GrantClaims, filter ListOTAPackagesFilter) ([]OTAPackage, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(claims.TenantID) {
		return nil, ErrCredentialForbidden
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	component := strings.ToUpper(strings.TrimSpace(filter.Component))
	if component != "" && component != edgecontrol.OTAComponentEdgeGateway && component != edgecontrol.OTAComponentFirmware {
		return nil, fmt.Errorf("%w: invalid component filter %q", ErrOTAInvalid, filter.Component)
	}
	arch := ""
	if strings.TrimSpace(filter.TargetArch) != "" {
		arch = edgecontrol.NormalizeTargetArch(filter.TargetArch)
		if arch != "armv7l" && arch != "arm64" && arch != "amd64" {
			return nil, fmt.Errorf("%w: invalid targetArch filter %q", ErrOTAInvalid, filter.TargetArch)
		}
	}
	status := strings.ToUpper(strings.TrimSpace(filter.Status))
	if status != "" && status != OTAPackageStatusActive && status != OTAPackageStatusRetired {
		return nil, fmt.Errorf("%w: invalid status filter %q", ErrOTAInvalid, filter.Status)
	}

	items := []OTAPackage{}
	if err := store.readAsTenant(ctx, claims.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, component, version, target_os, target_arch,
       hardware_models, min_compatible_version, file_name, file_size_bytes,
       sha256, signature_alg, signing_key_id, signature_b64, storage_rel_path,
       release_notes, status, uploaded_by::text, created_at, updated_at
FROM connectivity.ota_packages
WHERE tenant_id = $1::uuid
  AND ($2 = '' OR component = $2)
  AND ($3 = '' OR target_arch = $3)
  AND ($4 = '' OR status = $4)
ORDER BY created_at DESC, id DESC
LIMIT $5
`, claims.TenantID, component, arch, status, limit)
		if err != nil {
			return fmt.Errorf("list OTA packages: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			pkg, scanErr := scanOTAPackage(rows)
			if scanErr != nil {
				return scanErr
			}
			items = append(items, pkg)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	return items, nil
}

func (store *Store) CreateOTAPackage(ctx context.Context, claims registryauth.GrantClaims, input CreateOTAPackageInput) (OTAPackage, error) {
	if store == nil || store.pool == nil {
		return OTAPackage{}, errors.New("connectivity store is closed")
	}
	if !gatewayIDPattern.MatchString(claims.TenantID) || !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return OTAPackage{}, ErrCredentialForbidden
	}
	normalized, err := NormalizeOTAPackageInput(input)
	if err != nil {
		return OTAPackage{}, err
	}
	now := store.clock().UTC()
	pkgID := normalized.PackageID
	if pkgID == "" {
		var uuidErr error
		pkgID, uuidErr = newUUIDv7(now)
		if uuidErr != nil {
			return OTAPackage{}, fmt.Errorf("generate OTA package UUIDv7: %w", uuidErr)
		}
	}
	modelsJSON, err := json.Marshal(normalized.HardwareModels)
	if err != nil {
		return OTAPackage{}, fmt.Errorf("%w: encode hardwareModels: %v", ErrOTAInvalid, err)
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OTAPackage{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, claims.TenantID); err != nil {
		return OTAPackage{}, err
	}

	row := tx.QueryRow(ctx, `
INSERT INTO connectivity.ota_packages (
  id, tenant_id, component, version, target_os, target_arch, hardware_models,
  min_compatible_version, file_name, file_size_bytes, sha256, signature_alg,
  signing_key_id, signature_b64, storage_rel_path, release_notes, status,
  uploaded_by, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12,
  $13, $14, $15, $16, $17, $18::uuid, $19, $19
)
RETURNING id::text, tenant_id::text, component, version, target_os, target_arch,
          hardware_models, min_compatible_version, file_name, file_size_bytes,
          sha256, signature_alg, signing_key_id, signature_b64, storage_rel_path,
          release_notes, status, uploaded_by::text, created_at, updated_at
`, pkgID, claims.TenantID, normalized.Component, normalized.Version, normalized.TargetOS,
		normalized.TargetArch, string(modelsJSON), normalized.MinCompatibleVersion,
		normalized.FileName, normalized.FileSizeBytes, normalized.SHA256, edgecontrol.OTASignatureAlgEd25519,
		normalized.SigningKeyID, normalized.SignatureB64, normalized.StorageRelPath,
		normalized.ReleaseNotes, OTAPackageStatusActive, claims.PrincipalID, now)

	pkg, err := scanOTAPackage(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return OTAPackage{}, fmt.Errorf("%w: package %s v%s (%s/%s) already exists", ErrOTAConflict, normalized.Component, normalized.Version, normalized.TargetOS, normalized.TargetArch)
		}
		return OTAPackage{}, fmt.Errorf("insert OTA package: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OTAPackage{}, err
	}
	return pkg, nil
}

func (store *Store) GetOTAPackageForGateway(ctx context.Context, gatewayID, packageID string) (OTAPackage, error) {
	if !gatewayIDPattern.MatchString(packageID) {
		return OTAPackage{}, fmt.Errorf("%w: packageId must be UUIDv7", ErrOTAInvalid)
	}
	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return OTAPackage{}, err
	}
	var pkg OTAPackage
	if err := store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var loadErr error
		pkg, loadErr = loadOTAPackageInTx(ctx, tx, tenantID, packageID)
		return loadErr
	}); err != nil {
		return OTAPackage{}, err
	}
	return pkg, nil
}

func (store *Store) ListOTACampaigns(ctx context.Context, claims registryauth.GrantClaims, filter ListOTACampaignsFilter) ([]OTACampaignSummary, error) {
	tx, err := store.beginTenantWithSites(ctx, claims, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	statusFilter := strings.ToUpper(strings.TrimSpace(filter.Status))
	if statusFilter != "" {
		switch statusFilter {
		case OTACampaignStatusCanaryRunning, OTACampaignStatusRolloutRunning, OTACampaignStatusPaused,
			OTACampaignStatusCompleted, OTACampaignStatusRolledBack, OTACampaignStatusFailed:
		default:
			return nil, fmt.Errorf("%w: invalid campaign status filter %q", ErrOTAInvalid, filter.Status)
		}
	}

	rows, err := tx.Query(ctx, `
SELECT c.id::text, c.tenant_id::text, c.name, c.package_id::text, p.version, p.component, p.target_os, p.target_arch,
       c.target_site_ids, c.target_tags, c.canary_percent, c.failure_rate_threshold_pct,
       c.health_check_timeout_sec, c.auto_promote_rollout, c.status, c.pause_reason,
       c.reason, c.created_by::text, c.updated_by::text, c.created_at, c.updated_at, c.completed_at
FROM connectivity.ota_campaigns AS c
JOIN connectivity.ota_packages AS p
  ON p.tenant_id = c.tenant_id AND p.id = c.package_id
WHERE c.tenant_id = $1::uuid
  AND ($2 = '' OR c.status = $2)
ORDER BY c.created_at DESC, c.id DESC
LIMIT $3
`, claims.TenantID, statusFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("list OTA campaigns: %w", err)
	}
	var summaries []OTACampaignSummary
	campaignIDs := make([]string, 0, limit)
	for rows.Next() {
		s, scanErr := scanOTACampaignSummary(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		summaries = append(summaries, s)
		campaignIDs = append(campaignIDs, s.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	tasksByCampaign := make(map[string][]OTADeviceTask, len(campaignIDs))
	if len(campaignIDs) > 0 {
		tRows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, campaign_id::text, package_id::text, gateway_id::text, site_id::text,
       '', '', wave, desired_action, stage, progress_pct, current_version, target_version,
       error_code, error_message, dispatched_at, last_reported_at, finished_at, created_at, updated_at
FROM connectivity.ota_device_tasks
WHERE tenant_id = $1::uuid AND campaign_id = ANY($2::uuid[])
`, claims.TenantID, campaignIDs)
		if err != nil {
			return nil, fmt.Errorf("load campaign task counters: %w", err)
		}
		for tRows.Next() {
			t, scanErr := scanOTADeviceTask(tRows)
			if scanErr != nil {
				tRows.Close()
				return nil, scanErr
			}
			tasksByCampaign[t.CampaignID] = append(tasksByCampaign[t.CampaignID], t)
		}
		if err := tRows.Err(); err != nil {
			tRows.Close()
			return nil, err
		}
		tRows.Close()
	}

	visible := make([]OTACampaignSummary, 0, len(summaries))
	for _, s := range summaries {
		tasks := tasksByCampaign[s.ID]
		if !campaignScopeAllows(claims, s.TargetSiteIDs, tasks) {
			continue
		}
		computeCampaignTaskCounters(&s, tasks)
		visible = append(visible, s)
	}
	return visible, nil
}

func (store *Store) GetOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID string) (OTACampaignDetail, error) {
	if !gatewayIDPattern.MatchString(campaignID) {
		return OTACampaignDetail{}, fmt.Errorf("%w: campaignId must be UUIDv7", ErrOTAInvalid)
	}
	tx, err := store.beginTenantWithSites(ctx, claims, true)
	if err != nil {
		return OTACampaignDetail{}, err
	}
	defer tx.Rollback(ctx)
	return loadOTACampaignDetailForOperatorInTx(ctx, tx, claims, campaignID, false)
}

func (store *Store) CreateOTACampaign(ctx context.Context, claims registryauth.GrantClaims, input CreateOTACampaignInput) (OTACampaignDetail, []OTAStageDispatch, error) {
	if !gatewayIDPattern.MatchString(claims.TenantID) || !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return OTACampaignDetail{}, nil, ErrCredentialForbidden
	}
	name := strings.TrimSpace(input.Name)
	nameRunes := utf8.RuneCountInString(name)
	if nameRunes == 0 || nameRunes > maxOTACampaignName {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: name must be 1..%d characters", ErrOTAInvalid, maxOTACampaignName)
	}
	pkgID := strings.ToLower(strings.TrimSpace(input.PackageID))
	if !gatewayIDPattern.MatchString(pkgID) {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: packageId must be UUIDv7", ErrOTAInvalid)
	}
	if input.CanaryPercent < 1 || input.CanaryPercent > 100 {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: canaryPercent must be between 1 and 100", ErrOTAInvalid)
	}
	if input.FailureRateThresholdPct < 1 || input.FailureRateThresholdPct > 100 {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: failureRateThresholdPct must be between 1 and 100", ErrOTAInvalid)
	}
	timeoutSec := DefaultOTAHealthCheckTimeoutSec
	if input.HealthCheckTimeoutSec != nil {
		timeoutSec = *input.HealthCheckTimeoutSec
		if timeoutSec < MinOTAHealthCheckTimeoutSec || timeoutSec > MaxOTAHealthCheckTimeoutSec {
			return OTACampaignDetail{}, nil, fmt.Errorf("%w: healthCheckTimeoutSec must be between %d and %d", ErrOTAInvalid, MinOTAHealthCheckTimeoutSec, MaxOTAHealthCheckTimeoutSec)
		}
	}
	autoPromote := true
	if input.AutoPromoteRollout != nil {
		autoPromote = *input.AutoPromoteRollout
	}
	reason, err := validateOTAReason(input.Reason)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	cleanedTags, err := validateOTATagFilterMap(input.TargetTags)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	if len(input.TargetSiteIDs) > maxOTASiteIDsCount {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: targetSiteIds cannot exceed %d sites", ErrOTAInvalid, maxOTASiteIDsCount)
	}

	querySiteIDs := effectiveAuthorizedSites(claims)
	var requestedSites []string
	if len(input.TargetSiteIDs) > 0 {
		seen := make(map[string]struct{}, len(input.TargetSiteIDs))
		for _, raw := range input.TargetSiteIDs {
			sID := strings.ToLower(strings.TrimSpace(raw))
			if !gatewayIDPattern.MatchString(sID) {
				return OTACampaignDetail{}, nil, fmt.Errorf("%w: targetSiteIds entry %q must be UUIDv7", ErrOTAInvalid, raw)
			}
			if !registryauth.ScopeAllows(claims, sID) {
				return OTACampaignDetail{}, nil, ErrCredentialForbidden
			}
			if _, dup := seen[sID]; dup {
				continue
			}
			seen[sID] = struct{}{}
			requestedSites = append(requestedSites, sID)
		}
		querySiteIDs = requestedSites
	} else {
		requestedSites = []string{}
	}
	if len(querySiteIDs) == 0 {
		return OTACampaignDetail{}, nil, ErrCredentialForbidden
	}

	tagsJSON, err := json.Marshal(cleanedTags)
	if err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: encode targetTags: %v", ErrOTAInvalid, err)
	}
	siteIDsJSON, err := json.Marshal(requestedSites)
	if err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: encode targetSiteIds: %v", ErrOTAInvalid, err)
	}

	tx, err := store.beginTenantWithSites(ctx, claims, false)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	defer tx.Rollback(ctx)

	pkg, err := loadOTAPackageInTx(ctx, tx, claims.TenantID, pkgID)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	if pkg.Status != OTAPackageStatusActive {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: package %s is %s", ErrOTAConflict, pkg.ID, pkg.Status)
	}

	rows, err := tx.Query(ctx, `
SELECT gateway_id::text, site_id::text, gateway_code, gateway_name
FROM core_registry.gateway_ota_targets_v1
WHERE status = 'ACTIVE'
  AND site_id = ANY($1::uuid[])
  AND ($2::jsonb = '{}'::jsonb OR effective_tags @> $2::jsonb)
ORDER BY gateway_id ASC
`, querySiteIDs, string(tagsJSON))
	if err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("query target gateways: %w", err)
	}
	type matchedTarget struct {
		gatewayID string
		siteID    string
		code      string
		name      string
	}
	var targets []matchedTarget
	for rows.Next() {
		var item matchedTarget
		if scanErr := rows.Scan(&item.gatewayID, &item.siteID, &item.code, &item.name); scanErr != nil {
			rows.Close()
			return OTACampaignDetail{}, nil, fmt.Errorf("scan target gateway: %w", scanErr)
		}
		targets = append(targets, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return OTACampaignDetail{}, nil, err
	}
	rows.Close()

	if len(targets) == 0 {
		return OTACampaignDetail{}, nil, ErrOTANoTargets
	}

	now := store.clock().UTC()
	campaignID, err := newUUIDv7(now)
	if err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("generate OTA campaign UUIDv7: %w", err)
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.ota_campaigns (
  id, tenant_id, name, package_id, target_site_ids, target_tags, canary_percent,
  failure_rate_threshold_pct, health_check_timeout_sec, auto_promote_rollout,
  status, pause_reason, reason, created_by, updated_by, created_at, updated_at, completed_at
) VALUES (
  $1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, $6::jsonb, $7, $8, $9, $10,
  $11, NULL, $12, $13::uuid, $13::uuid, $14, $14, NULL
)
`, campaignID, claims.TenantID, name, pkg.ID, string(siteIDsJSON), string(tagsJSON),
		input.CanaryPercent, input.FailureRateThresholdPct, timeoutSec, autoPromote,
		OTACampaignStatusCanaryRunning, reason, claims.PrincipalID, now); err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("insert OTA campaign: %w", err)
	}

	canaryCount := ComputeCanaryTargetCount(len(targets), input.CanaryPercent)
	tasks := make([]OTADeviceTask, 0, len(targets))
	var canaryTasks []OTADeviceTask
	for i, target := range targets {
		taskID, idErr := newUUIDv7(now.Add(time.Duration(i) * time.Millisecond))
		if idErr != nil {
			return OTACampaignDetail{}, nil, fmt.Errorf("generate OTA task UUIDv7: %w", idErr)
		}
		wave := OTAWaveRollout
		stage := OTATaskStagePending
		var dispatchedAt *time.Time
		if i < canaryCount {
			wave = OTAWaveCanary
			stage = OTATaskStageDispatched
			dAt := now
			dispatchedAt = &dAt
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO connectivity.ota_device_tasks (
  id, tenant_id, campaign_id, package_id, gateway_id, site_id, wave, desired_action,
  stage, progress_pct, current_version, target_version, error_code, error_message,
  dispatched_at, last_reported_at, finished_at, created_at, updated_at
) VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8,
  $9, 0, '', $10, NULL, NULL, $11, NULL, NULL, $12, $12
)
`, taskID, claims.TenantID, campaignID, pkg.ID, target.gatewayID, target.siteID, wave,
			edgecontrol.OTAActionApply, stage, pkg.Version, dispatchedAt, now); err != nil {
			return OTACampaignDetail{}, nil, fmt.Errorf("insert OTA device task: %w", err)
		}
		task := OTADeviceTask{
			ID:            taskID,
			TenantID:      claims.TenantID,
			CampaignID:    campaignID,
			PackageID:     pkg.ID,
			GatewayID:     target.gatewayID,
			SiteID:        target.siteID,
			GatewayCode:   target.code,
			GatewayName:   target.name,
			Wave:          wave,
			DesiredAction: edgecontrol.OTAActionApply,
			Stage:         stage,
			TargetVersion: pkg.Version,
			DispatchedAt:  dispatchedAt,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		tasks = append(tasks, task)
		if wave == OTAWaveCanary {
			canaryTasks = append(canaryTasks, task)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return OTACampaignDetail{}, nil, err
	}

	detail := OTACampaignDetail{
		OTACampaignSummary: OTACampaignSummary{
			ID:                      campaignID,
			TenantID:                claims.TenantID,
			Name:                    name,
			PackageID:               pkg.ID,
			PackageVersion:          pkg.Version,
			Component:               pkg.Component,
			TargetOS:                pkg.TargetOS,
			TargetArch:              pkg.TargetArch,
			TargetSiteIDs:           requestedSites,
			TargetTags:              cleanedTags,
			CanaryPercent:           input.CanaryPercent,
			FailureRateThresholdPct: input.FailureRateThresholdPct,
			HealthCheckTimeoutSec:   timeoutSec,
			AutoPromoteRollout:      autoPromote,
			Status:                  OTACampaignStatusCanaryRunning,
			Reason:                  reason,
			CreatedBy:               claims.PrincipalID,
			UpdatedBy:               claims.PrincipalID,
			CreatedAt:               now,
			UpdatedAt:               now,
		},
		Package: pkg,
		Tasks:   tasks,
	}
	computeCampaignTaskCounters(&detail.OTACampaignSummary, tasks)
	return detail, BuildOTAStageDispatches(pkg, canaryTasks, timeoutSec, ""), nil
}

func (store *Store) PauseOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, error) {
	if !gatewayIDPattern.MatchString(campaignID) {
		return OTACampaignDetail{}, fmt.Errorf("%w: campaignId must be UUIDv7", ErrOTAInvalid)
	}
	if !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return OTACampaignDetail{}, ErrCredentialForbidden
	}
	cleanedReason := OTAPauseReasonManual
	if strings.TrimSpace(reason) != "" {
		var err error
		cleanedReason, err = validateOTAReason(reason)
		if err != nil {
			return OTACampaignDetail{}, err
		}
	}

	tx, err := store.beginTenantWithSites(ctx, claims, false)
	if err != nil {
		return OTACampaignDetail{}, err
	}
	defer tx.Rollback(ctx)

	detail, err := loadOTACampaignDetailForOperatorInTx(ctx, tx, claims, campaignID, true)
	if err != nil {
		return OTACampaignDetail{}, err
	}
	if detail.Status != OTACampaignStatusCanaryRunning && detail.Status != OTACampaignStatusRolloutRunning {
		return OTACampaignDetail{}, fmt.Errorf("%w: cannot pause campaign in status %s", ErrOTAConflict, detail.Status)
	}

	now := store.clock().UTC()
	pauseReason := OTAPauseReasonManual
	if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_campaigns
SET status = $3,
    pause_reason = $4,
    reason = $5,
    updated_by = $6::uuid,
    updated_at = $7
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, claims.TenantID, campaignID, OTACampaignStatusPaused, pauseReason, cleanedReason, claims.PrincipalID, now); err != nil {
		return OTACampaignDetail{}, fmt.Errorf("pause OTA campaign: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return OTACampaignDetail{}, err
	}
	detail.Status = OTACampaignStatusPaused
	detail.PauseReason = &pauseReason
	detail.Reason = cleanedReason
	detail.UpdatedBy = claims.PrincipalID
	detail.UpdatedAt = now
	return detail, nil
}

func (store *Store) ResumeOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, []OTAStageDispatch, error) {
	if !gatewayIDPattern.MatchString(campaignID) {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: campaignId must be UUIDv7", ErrOTAInvalid)
	}
	if !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return OTACampaignDetail{}, nil, ErrCredentialForbidden
	}
	cleanedReason := "RESUME_CAMPAIGN"
	if strings.TrimSpace(reason) != "" {
		var err error
		cleanedReason, err = validateOTAReason(reason)
		if err != nil {
			return OTACampaignDetail{}, nil, err
		}
	}

	tx, err := store.beginTenantWithSites(ctx, claims, false)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	defer tx.Rollback(ctx)

	detail, err := loadOTACampaignDetailForOperatorInTx(ctx, tx, claims, campaignID, true)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	if detail.Status != OTACampaignStatusPaused {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: cannot resume campaign in status %s", ErrOTAConflict, detail.Status)
	}

	var activeCanary []OTADeviceTask
	var pendingRolloutIDs []string
	var activeRollout []OTADeviceTask
	for _, t := range detail.Tasks {
		term := IsOTATaskTerminal(t.DesiredAction, t.Stage)
		if t.Wave == OTAWaveCanary && !term && t.Stage != OTATaskStagePending {
			activeCanary = append(activeCanary, t)
		}
		if t.Wave == OTAWaveRollout {
			if t.Stage == OTATaskStagePending {
				pendingRolloutIDs = append(pendingRolloutIDs, t.ID)
			} else if !term {
				activeRollout = append(activeRollout, t)
			}
		}
	}

	now := store.clock().UTC()
	var nextStatus string
	var toDispatch []OTADeviceTask
	switch {
	case len(activeCanary) > 0:
		nextStatus = OTACampaignStatusCanaryRunning
		toDispatch = activeCanary
	case len(pendingRolloutIDs) > 0:
		nextStatus = OTACampaignStatusRolloutRunning
		if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_device_tasks
SET stage = $3,
    dispatched_at = COALESCE(dispatched_at, $4),
    updated_at = $4
WHERE tenant_id = $1::uuid AND id = ANY($2::uuid[])
`, claims.TenantID, pendingRolloutIDs, OTATaskStageDispatched, now); err != nil {
			return OTACampaignDetail{}, nil, fmt.Errorf("promote rollout tasks on resume: %w", err)
		}
		promotedSet := make(map[string]struct{}, len(pendingRolloutIDs))
		for _, id := range pendingRolloutIDs {
			promotedSet[id] = struct{}{}
		}
		for i := range detail.Tasks {
			if _, ok := promotedSet[detail.Tasks[i].ID]; ok {
				dAt := now
				detail.Tasks[i].Stage = OTATaskStageDispatched
				detail.Tasks[i].DispatchedAt = &dAt
				detail.Tasks[i].UpdatedAt = now
				toDispatch = append(toDispatch, detail.Tasks[i])
			}
		}
		toDispatch = append(toDispatch, activeRollout...)
	case len(activeRollout) > 0:
		nextStatus = OTACampaignStatusRolloutRunning
		toDispatch = activeRollout
	default:
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: campaign has no pending or active tasks to resume", ErrOTAConflict)
	}

	if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_campaigns
SET status = $3,
    pause_reason = NULL,
    reason = $4,
    updated_by = $5::uuid,
    updated_at = $6,
    completed_at = NULL
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, claims.TenantID, campaignID, nextStatus, cleanedReason, claims.PrincipalID, now); err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("resume OTA campaign: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return OTACampaignDetail{}, nil, err
	}
	detail.Status = nextStatus
	detail.PauseReason = nil
	detail.Reason = cleanedReason
	detail.UpdatedBy = claims.PrincipalID
	detail.UpdatedAt = now
	detail.CompletedAt = nil
	computeCampaignTaskCounters(&detail.OTACampaignSummary, detail.Tasks)
	return detail, BuildOTAStageDispatches(detail.Package, toDispatch, detail.HealthCheckTimeoutSec, ""), nil
}

func (store *Store) RollbackOTACampaign(ctx context.Context, claims registryauth.GrantClaims, campaignID, reason string) (OTACampaignDetail, []OTAStageDispatch, error) {
	if !gatewayIDPattern.MatchString(campaignID) {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: campaignId must be UUIDv7", ErrOTAInvalid)
	}
	if !gatewayIDPattern.MatchString(claims.PrincipalID) {
		return OTACampaignDetail{}, nil, ErrCredentialForbidden
	}
	cleanedReason := "MANUAL_ROLLBACK"
	if strings.TrimSpace(reason) != "" {
		var err error
		cleanedReason, err = validateOTAReason(reason)
		if err != nil {
			return OTACampaignDetail{}, nil, err
		}
	}

	tx, err := store.beginTenantWithSites(ctx, claims, false)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	defer tx.Rollback(ctx)

	detail, err := loadOTACampaignDetailForOperatorInTx(ctx, tx, claims, campaignID, true)
	if err != nil {
		return OTACampaignDetail{}, nil, err
	}
	if detail.Status == OTACampaignStatusRolledBack {
		return OTACampaignDetail{}, nil, fmt.Errorf("%w: campaign is already rolled back", ErrOTAConflict)
	}

	var cancelIDs, rollbackIDs []string
	for _, t := range detail.Tasks {
		switch t.Stage {
		case OTATaskStagePending:
			cancelIDs = append(cancelIDs, t.ID)
		case OTATaskStageCancelled, edgecontrol.OTAStageRolledBack:
		default:
			rollbackIDs = append(rollbackIDs, t.ID)
		}
	}

	now := store.clock().UTC()
	if len(cancelIDs) > 0 {
		if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_device_tasks
SET stage = $3,
    finished_at = $4,
    updated_at = $4
WHERE tenant_id = $1::uuid AND id = ANY($2::uuid[])
`, claims.TenantID, cancelIDs, OTATaskStageCancelled, now); err != nil {
			return OTACampaignDetail{}, nil, fmt.Errorf("cancel pending OTA tasks: %w", err)
		}
	}
	if len(rollbackIDs) > 0 {
		if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_device_tasks
SET desired_action = $3,
    stage = $4,
    progress_pct = 0,
    dispatched_at = $5,
    finished_at = NULL,
    updated_at = $5
WHERE tenant_id = $1::uuid AND id = ANY($2::uuid[])
`, claims.TenantID, rollbackIDs, edgecontrol.OTAActionRollback, OTATaskStageDispatched, now); err != nil {
			return OTACampaignDetail{}, nil, fmt.Errorf("dispatch OTA rollback tasks: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_campaigns
SET status = $3,
    pause_reason = NULL,
    reason = $4,
    updated_by = $5::uuid,
    updated_at = $6,
    completed_at = $6
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, claims.TenantID, campaignID, OTACampaignStatusRolledBack, cleanedReason, claims.PrincipalID, now); err != nil {
		return OTACampaignDetail{}, nil, fmt.Errorf("rollback OTA campaign: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return OTACampaignDetail{}, nil, err
	}

	cancelSet := make(map[string]struct{}, len(cancelIDs))
	for _, id := range cancelIDs {
		cancelSet[id] = struct{}{}
	}
	rollbackSet := make(map[string]struct{}, len(rollbackIDs))
	for _, id := range rollbackIDs {
		rollbackSet[id] = struct{}{}
	}
	var rollbackTasks []OTADeviceTask
	for i := range detail.Tasks {
		if _, ok := cancelSet[detail.Tasks[i].ID]; ok {
			fin := now
			detail.Tasks[i].Stage = OTATaskStageCancelled
			detail.Tasks[i].FinishedAt = &fin
			detail.Tasks[i].UpdatedAt = now
		} else if _, ok := rollbackSet[detail.Tasks[i].ID]; ok {
			dAt := now
			detail.Tasks[i].DesiredAction = edgecontrol.OTAActionRollback
			detail.Tasks[i].Stage = OTATaskStageDispatched
			detail.Tasks[i].ProgressPct = 0
			detail.Tasks[i].DispatchedAt = &dAt
			detail.Tasks[i].FinishedAt = nil
			detail.Tasks[i].UpdatedAt = now
			rollbackTasks = append(rollbackTasks, detail.Tasks[i])
		}
	}
	detail.Status = OTACampaignStatusRolledBack
	detail.PauseReason = nil
	detail.Reason = cleanedReason
	detail.UpdatedBy = claims.PrincipalID
	detail.UpdatedAt = now
	detail.CompletedAt = &now
	computeCampaignTaskCounters(&detail.OTACampaignSummary, detail.Tasks)
	return detail, BuildOTAStageDispatches(detail.Package, rollbackTasks, detail.HealthCheckTimeoutSec, ""), nil
}

func (store *Store) GetGatewayDesiredOTAStage(ctx context.Context, gatewayID string) (*edgecontrol.OTAStagePayload, error) {
	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return nil, err
	}
	var stage *edgecontrol.OTAStagePayload
	err = store.readAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var (
			task       OTADeviceTask
			timeoutSec int
			row        = tx.QueryRow(ctx, `
SELECT t.id::text, t.tenant_id::text, t.campaign_id::text, t.package_id::text, t.gateway_id::text, t.site_id::text,
       '', '', t.wave, t.desired_action, t.stage, t.progress_pct, t.current_version, t.target_version,
       t.error_code, t.error_message, t.dispatched_at, t.last_reported_at, t.finished_at, t.created_at, t.updated_at,
       c.health_check_timeout_sec
FROM connectivity.ota_device_tasks AS t
JOIN connectivity.ota_campaigns AS c
  ON c.tenant_id = t.tenant_id AND c.id = t.campaign_id
WHERE t.tenant_id = $1::uuid
  AND t.gateway_id = $2::uuid
  AND t.stage IN ('DISPATCHED', 'DOWNLOADING', 'VERIFYING', 'APPLYING', 'HEALTH_CHECKING')
  AND (
    c.status IN ('CANARY_RUNNING', 'ROLLOUT_RUNNING')
    OR (c.status = 'ROLLED_BACK' AND t.desired_action = 'ROLLBACK')
  )
ORDER BY t.created_at DESC, t.id DESC
LIMIT 1
`, tenantID, gatewayID)
		)
		err := row.Scan(
			&task.ID, &task.TenantID, &task.CampaignID, &task.PackageID, &task.GatewayID, &task.SiteID,
			&task.GatewayCode, &task.GatewayName, &task.Wave, &task.DesiredAction, &task.Stage, &task.ProgressPct,
			&task.CurrentVersion, &task.TargetVersion, &task.ErrorCode, &task.ErrorMessage,
			&task.DispatchedAt, &task.LastReportedAt, &task.FinishedAt, &task.CreatedAt, &task.UpdatedAt,
			&timeoutSec,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load desired gateway OTA stage: %w", err)
		}
		pkg, pkgErr := loadOTAPackageInTx(ctx, tx, tenantID, task.PackageID)
		if pkgErr != nil {
			return pkgErr
		}
		dispatches := BuildOTAStageDispatches(pkg, []OTADeviceTask{task}, timeoutSec, "")
		if len(dispatches) > 0 {
			s := dispatches[0].Stage
			stage = &s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stage, nil
}

func (store *Store) ApplyGatewayOTAResult(ctx context.Context, gatewayID string, result edgecontrol.OTAResultPayload) (OTADeviceTask, []OTAStageDispatch, error) {
	if err := result.Validate(); err != nil {
		return OTADeviceTask{}, nil, fmt.Errorf("%w: %v", ErrOTAInvalid, err)
	}
	tenantID, err := store.resolveActiveGatewayTenant(ctx, gatewayID)
	if err != nil {
		return OTADeviceTask{}, nil, err
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OTADeviceTask{}, nil, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return OTADeviceTask{}, nil, err
	}

	var (
		campaignStatus          string
		failureRateThresholdPct int
		healthCheckTimeoutSec   int
		autoPromoteRollout      bool
		pkgID                   string
	)
	err = tx.QueryRow(ctx, `
SELECT status, failure_rate_threshold_pct, health_check_timeout_sec, auto_promote_rollout, package_id::text
FROM connectivity.ota_campaigns
WHERE tenant_id = $1::uuid AND id = $2::uuid
FOR UPDATE
`, tenantID, result.CampaignID).Scan(&campaignStatus, &failureRateThresholdPct, &healthCheckTimeoutSec, &autoPromoteRollout, &pkgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OTADeviceTask{}, nil, ErrNotFound
	}
	if err != nil {
		return OTADeviceTask{}, nil, fmt.Errorf("lock OTA campaign: %w", err)
	}

	tasks, err := loadCampaignTasksRawInTx(ctx, tx, tenantID, result.CampaignID)
	if err != nil {
		return OTADeviceTask{}, nil, err
	}
	targetIdx := -1
	for i := range tasks {
		if strings.EqualFold(tasks[i].ID, result.TaskID) && strings.EqualFold(tasks[i].GatewayID, gatewayID) {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return OTADeviceTask{}, nil, ErrNotFound
	}

	now := store.clock().UTC()
	nextTask, updated, err := EvaluateOTATaskProgress(tasks[targetIdx], result, now)
	if err != nil {
		return OTADeviceTask{}, nil, err
	}
	if !updated {
		return tasks[targetIdx], nil, nil
	}

	if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_device_tasks
SET stage = $3,
    progress_pct = $4,
    current_version = $5,
    error_code = $6,
    error_message = $7,
    last_reported_at = $8,
    finished_at = $9,
    updated_at = $10
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, tenantID, nextTask.ID, nextTask.Stage, nextTask.ProgressPct, nextTask.CurrentVersion,
		nextTask.ErrorCode, nextTask.ErrorMessage, nextTask.LastReportedAt, nextTask.FinishedAt, nextTask.UpdatedAt); err != nil {
		return OTADeviceTask{}, nil, fmt.Errorf("update OTA device task: %w", err)
	}
	tasks[targetIdx] = nextTask

	decision := EvaluateOTACampaignTransition(campaignStatus, failureRateThresholdPct, autoPromoteRollout, tasks, now)
	var promotedTasks []OTADeviceTask
	if decision.Changed {
		if len(decision.PromoteTaskIDs) > 0 {
			if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_device_tasks
SET stage = $3,
    dispatched_at = COALESCE(dispatched_at, $4),
    updated_at = $4
WHERE tenant_id = $1::uuid AND id = ANY($2::uuid[])
`, tenantID, decision.PromoteTaskIDs, OTATaskStageDispatched, now); err != nil {
				return OTADeviceTask{}, nil, fmt.Errorf("promote rollout OTA tasks: %w", err)
			}
			promoteSet := make(map[string]struct{}, len(decision.PromoteTaskIDs))
			for _, id := range decision.PromoteTaskIDs {
				promoteSet[id] = struct{}{}
			}
			for i := range tasks {
				if _, ok := promoteSet[tasks[i].ID]; ok {
					dAt := now
					tasks[i].Stage = OTATaskStageDispatched
					tasks[i].DispatchedAt = &dAt
					tasks[i].UpdatedAt = now
					promotedTasks = append(promotedTasks, tasks[i])
				}
			}
		}
		if _, err := tx.Exec(ctx, `
UPDATE connectivity.ota_campaigns
SET status = $3,
    pause_reason = $4,
    updated_at = $5,
    completed_at = COALESCE($6, completed_at)
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, tenantID, result.CampaignID, decision.NewStatus, decision.NewPauseReason, now, decision.CompletedAt); err != nil {
			return OTADeviceTask{}, nil, fmt.Errorf("update OTA campaign status: %w", err)
		}
	}

	var pkg OTAPackage
	if len(promotedTasks) > 0 {
		pkg, err = loadOTAPackageInTx(ctx, tx, tenantID, pkgID)
		if err != nil {
			return OTADeviceTask{}, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return OTADeviceTask{}, nil, err
	}
	return nextTask, BuildOTAStageDispatches(pkg, promotedTasks, healthCheckTimeoutSec, ""), nil
}

func loadOTAPackageInTx(ctx context.Context, tx pgx.Tx, tenantID, packageID string) (OTAPackage, error) {
	row := tx.QueryRow(ctx, `
SELECT id::text, tenant_id::text, component, version, target_os, target_arch,
       hardware_models, min_compatible_version, file_name, file_size_bytes,
       sha256, signature_alg, signing_key_id, signature_b64, storage_rel_path,
       release_notes, status, uploaded_by::text, created_at, updated_at
FROM connectivity.ota_packages
WHERE tenant_id = $1::uuid AND id = $2::uuid
`, tenantID, packageID)
	pkg, err := scanOTAPackage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return OTAPackage{}, ErrNotFound
	}
	if err != nil {
		return OTAPackage{}, fmt.Errorf("load OTA package: %w", err)
	}
	return pkg, nil
}

func loadOTACampaignDetailForOperatorInTx(
	ctx context.Context,
	tx pgx.Tx,
	claims registryauth.GrantClaims,
	campaignID string,
	forUpdate bool,
) (OTACampaignDetail, error) {
	query := `
SELECT c.id::text, c.tenant_id::text, c.name, c.package_id::text, p.version, p.component, p.target_os, p.target_arch,
       c.target_site_ids, c.target_tags, c.canary_percent, c.failure_rate_threshold_pct,
       c.health_check_timeout_sec, c.auto_promote_rollout, c.status, c.pause_reason,
       c.reason, c.created_by::text, c.updated_by::text, c.created_at, c.updated_at, c.completed_at
FROM connectivity.ota_campaigns AS c
JOIN connectivity.ota_packages AS p
  ON p.tenant_id = c.tenant_id AND p.id = c.package_id
WHERE c.tenant_id = $1::uuid AND c.id = $2::uuid
`
	if forUpdate {
		query += "FOR UPDATE OF c\n"
	}
	summary, err := scanOTACampaignSummary(tx.QueryRow(ctx, query, claims.TenantID, campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OTACampaignDetail{}, ErrNotFound
	}
	if err != nil {
		return OTACampaignDetail{}, fmt.Errorf("load OTA campaign: %w", err)
	}
	pkg, err := loadOTAPackageInTx(ctx, tx, claims.TenantID, summary.PackageID)
	if err != nil {
		return OTACampaignDetail{}, err
	}
	tRows, err := tx.Query(ctx, `
SELECT t.id::text, t.tenant_id::text, t.campaign_id::text, t.package_id::text, t.gateway_id::text, t.site_id::text,
       COALESCE(g.gateway_code, ''), COALESCE(g.gateway_name, ''),
       t.wave, t.desired_action, t.stage, t.progress_pct, t.current_version, t.target_version,
       t.error_code, t.error_message, t.dispatched_at, t.last_reported_at, t.finished_at, t.created_at, t.updated_at
FROM connectivity.ota_device_tasks AS t
LEFT JOIN core_registry.gateway_ota_targets_v1 AS g
  ON g.tenant_id = t.tenant_id AND g.gateway_id = t.gateway_id
WHERE t.tenant_id = $1::uuid AND t.campaign_id = $2::uuid
ORDER BY CASE WHEN t.wave = 'CANARY' THEN 0 ELSE 1 END, t.gateway_id ASC
`, claims.TenantID, campaignID)
	if err != nil {
		return OTACampaignDetail{}, fmt.Errorf("load OTA campaign tasks: %w", err)
	}
	tasks := []OTADeviceTask{}
	for tRows.Next() {
		t, scanErr := scanOTADeviceTask(tRows)
		if scanErr != nil {
			tRows.Close()
			return OTACampaignDetail{}, scanErr
		}
		tasks = append(tasks, t)
	}
	if err := tRows.Err(); err != nil {
		tRows.Close()
		return OTACampaignDetail{}, err
	}
	tRows.Close()

	if !campaignScopeAllows(claims, summary.TargetSiteIDs, tasks) {
		return OTACampaignDetail{}, ErrCredentialForbidden
	}
	computeCampaignTaskCounters(&summary, tasks)
	return OTACampaignDetail{
		OTACampaignSummary: summary,
		Package:            pkg,
		Tasks:              tasks,
	}, nil
}

func loadCampaignTasksRawInTx(ctx context.Context, tx pgx.Tx, tenantID, campaignID string) ([]OTADeviceTask, error) {
	rows, err := tx.Query(ctx, `
SELECT id::text, tenant_id::text, campaign_id::text, package_id::text, gateway_id::text, site_id::text,
       '', '', wave, desired_action, stage, progress_pct, current_version, target_version,
       error_code, error_message, dispatched_at, last_reported_at, finished_at, created_at, updated_at
FROM connectivity.ota_device_tasks
WHERE tenant_id = $1::uuid AND campaign_id = $2::uuid
ORDER BY gateway_id ASC
`, tenantID, campaignID)
	if err != nil {
		return nil, fmt.Errorf("load campaign OTA tasks: %w", err)
	}
	defer rows.Close()
	var tasks []OTADeviceTask
	for rows.Next() {
		t, scanErr := scanOTADeviceTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func scanOTAPackage(row rowScanner) (OTAPackage, error) {
	var pkg OTAPackage
	var modelsJSON []byte
	err := row.Scan(
		&pkg.ID, &pkg.TenantID, &pkg.Component, &pkg.Version, &pkg.TargetOS, &pkg.TargetArch,
		&modelsJSON, &pkg.MinCompatibleVersion, &pkg.FileName, &pkg.FileSizeBytes,
		&pkg.SHA256, &pkg.SignatureAlg, &pkg.SigningKeyID, &pkg.SignatureB64, &pkg.StorageRelPath,
		&pkg.ReleaseNotes, &pkg.Status, &pkg.UploadedBy, &pkg.CreatedAt, &pkg.UpdatedAt,
	)
	if err != nil {
		return OTAPackage{}, err
	}
	pkg.HardwareModels = []string{}
	if len(modelsJSON) > 0 {
		if err := json.Unmarshal(modelsJSON, &pkg.HardwareModels); err != nil {
			return OTAPackage{}, fmt.Errorf("decode OTA hardwareModels: %w", err)
		}
		if pkg.HardwareModels == nil {
			pkg.HardwareModels = []string{}
		}
	}
	pkg.CreatedAt = pkg.CreatedAt.UTC()
	pkg.UpdatedAt = pkg.UpdatedAt.UTC()
	return pkg, nil
}

func scanOTACampaignSummary(row rowScanner) (OTACampaignSummary, error) {
	var s OTACampaignSummary
	var siteIDsJSON, tagsJSON []byte
	var completedAt *time.Time
	err := row.Scan(
		&s.ID, &s.TenantID, &s.Name, &s.PackageID, &s.PackageVersion, &s.Component, &s.TargetOS, &s.TargetArch,
		&siteIDsJSON, &tagsJSON, &s.CanaryPercent, &s.FailureRateThresholdPct,
		&s.HealthCheckTimeoutSec, &s.AutoPromoteRollout, &s.Status, &s.PauseReason,
		&s.Reason, &s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt, &completedAt,
	)
	if err != nil {
		return OTACampaignSummary{}, err
	}
	s.TargetSiteIDs = []string{}
	if len(siteIDsJSON) > 0 {
		if err := json.Unmarshal(siteIDsJSON, &s.TargetSiteIDs); err != nil {
			return OTACampaignSummary{}, fmt.Errorf("decode OTA targetSiteIds: %w", err)
		}
		if s.TargetSiteIDs == nil {
			s.TargetSiteIDs = []string{}
		}
	}
	s.TargetTags = map[string]string{}
	if len(tagsJSON) > 0 {
		if err := json.Unmarshal(tagsJSON, &s.TargetTags); err != nil {
			return OTACampaignSummary{}, fmt.Errorf("decode OTA targetTags: %w", err)
		}
		if s.TargetTags == nil {
			s.TargetTags = map[string]string{}
		}
	}
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = s.UpdatedAt.UTC()
	if completedAt != nil {
		t := completedAt.UTC()
		s.CompletedAt = &t
	}
	return s, nil
}

func scanOTADeviceTask(row rowScanner) (OTADeviceTask, error) {
	var t OTADeviceTask
	var dispatchedAt, lastReportedAt, finishedAt *time.Time
	err := row.Scan(
		&t.ID, &t.TenantID, &t.CampaignID, &t.PackageID, &t.GatewayID, &t.SiteID,
		&t.GatewayCode, &t.GatewayName, &t.Wave, &t.DesiredAction, &t.Stage, &t.ProgressPct,
		&t.CurrentVersion, &t.TargetVersion, &t.ErrorCode, &t.ErrorMessage,
		&dispatchedAt, &lastReportedAt, &finishedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return OTADeviceTask{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if dispatchedAt != nil {
		v := dispatchedAt.UTC()
		t.DispatchedAt = &v
	}
	if lastReportedAt != nil {
		v := lastReportedAt.UTC()
		t.LastReportedAt = &v
	}
	if finishedAt != nil {
		v := finishedAt.UTC()
		t.FinishedAt = &v
	}
	return t, nil
}

func optionalTrimmedPtr(raw string, maxRunes int) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	clamped := clampUTF8Runes(trimmed, maxRunes)
	return &clamped
}
