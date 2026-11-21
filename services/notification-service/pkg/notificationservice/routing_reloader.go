package notificationservice

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	defaultRoutingReloadInterval = 30 * time.Second

	ReloadErrorRecipientsRead    = "RECIPIENTS_READ_FAILED"
	ReloadErrorRecipientsInvalid = "RECIPIENTS_INVALID"
	ReloadErrorRoutingRead       = "ROUTING_READ_FAILED"
	ReloadErrorRoutingInvalid    = "ROUTING_INVALID"
	ReloadErrorRecipientMismatch = "ROUTING_RECIPIENT_MISMATCH"
)

var (
	errRecipientsReadFailed = errors.New("recipients file could not be read")
	errRoutingReadFailed    = errors.New("routing files could not be read")
)

// RoutingCatalog maps Site IDs to validated alarm routing configurations.
type RoutingCatalog = AlarmRoutingTable

// RoutingSnapshot holds one immutable, cross-validated routing and recipients state.
type RoutingSnapshot struct {
	Recipients    *RecipientDirectory
	Routing       *RoutingCatalog
	Digest        string
	LoadedAt      time.Time
	ReloadCount   uint64
	LastErrorCode string
}

// EscalationStepSummaryView exposes non-secret step routing metadata without recipient identifiers or contacts.
type EscalationStepSummaryView struct {
	AfterMinutes   int               `json:"afterMinutes"`
	Channels       []DeliveryChannel `json:"channels"`
	RecipientCount int               `json:"recipientCount"`
}

// AlarmRouteSummaryView exposes non-secret route settings for administrative inspection.
type AlarmRouteSummaryView struct {
	Name               string                      `json:"name"`
	MinSeverity        alarmmodel.Severity         `json:"minSeverity"`
	AlarmTypePrefixes  []string                    `json:"alarmTypePrefixes,omitempty"`
	CooldownMinutes    int                         `json:"cooldownMinutes,omitempty"`
	GroupWindowSeconds int                         `json:"groupWindowSeconds,omitempty"`
	GroupMaxItems      int                         `json:"groupMaxItems,omitempty"`
	GroupBy            string                      `json:"groupBy,omitempty"`
	NotifyAck          bool                        `json:"notifyAck"`
	NotifyRecovery     bool                        `json:"notifyRecovery"`
	Steps              []EscalationStepSummaryView `json:"steps"`
}

// SiteRoutingSummaryView is the secret-free administrative summary of a Site's alarm routing.
type SiteRoutingSummaryView struct {
	SiteID                string                  `json:"siteId"`
	SiteName              string                  `json:"siteName"`
	Timezone              string                  `json:"timezone"`
	HoldMinutes           int                     `json:"holdMinutes"`
	MaxAgeHours           int                     `json:"maxAgeHours"`
	RateLimitPerHour      int                     `json:"rateLimitPerHour"`
	QuietHours            *QuietHours             `json:"quietHours,omitempty"`
	RootCauseAlarmType    string                  `json:"rootCauseAlarmType,omitempty"`
	SuppressWhenRootCause []string                `json:"suppressWhenRootCause,omitempty"`
	RootCauseRules        []RootCauseRule         `json:"rootCauseRules,omitempty"`
	Routes                []AlarmRouteSummaryView `json:"routes"`
	ConfigDigest          string                  `json:"configDigest"`
	LoadedAt              time.Time               `json:"loadedAt"`
	ReloadCount           uint64                  `json:"reloadCount"`
	LastReloadErrorCode   string                  `json:"lastReloadErrorCode,omitempty"`
}

// AlarmRoutingReloadResponse reports the result of an explicit admin reload together with the active summary.
type AlarmRoutingReloadResponse struct {
	SiteRoutingSummaryView
	Changed bool `json:"changed"`
}

// RoutingReloader hot-reloads recipient and site routing files off the hot path and swaps snapshots atomically.
type RoutingReloader struct {
	recipientsFile string
	routingDir     string
	onReload       func(outcome string)
	now            func() time.Time

	reloadMu sync.Mutex
	snapshot atomic.Pointer[RoutingSnapshot]
	logger   atomic.Pointer[slog.Logger]
}

// NewRoutingReloader performs an initial strict fail-closed load of recipients and routing files.
func NewRoutingReloader(recipientsFile, routingDir string, onReload func(outcome string)) (*RoutingReloader, error) {
	recipientsFile = strings.TrimSpace(recipientsFile)
	routingDir = strings.TrimSpace(routingDir)
	if recipientsFile == "" || routingDir == "" {
		return nil, errors.New("recipients file and routing directory are required")
	}
	reloader := &RoutingReloader{
		recipientsFile: recipientsFile,
		routingDir:     routingDir,
		onReload:       onReload,
		now:            time.Now,
	}
	recipientsBytes, routingFiles, digest, err := readRoutingInputs(recipientsFile, routingDir)
	if err != nil {
		return nil, err
	}
	recipients, routing, err := validateRoutingInputs(recipientsBytes, routingFiles)
	if err != nil {
		return nil, err
	}
	reloader.snapshot.Store(&RoutingSnapshot{
		Recipients:  &recipients,
		Routing:     &routing,
		Digest:      digest,
		LoadedAt:    reloader.now().UTC(),
		ReloadCount: 0,
	})
	return reloader, nil
}

// Snapshot returns the currently active immutable routing snapshot.
func (reloader *RoutingReloader) Snapshot() *RoutingSnapshot {
	if reloader == nil {
		return nil
	}
	return reloader.snapshot.Load()
}

// ReloadNow checks the SHA-256 digest of recipients and routing files, validates changes off the hot path,
// and atomically swaps the active snapshot on success or retains the previous known-good snapshot on error.
func (reloader *RoutingReloader) ReloadNow(ctx context.Context) (changed bool, digest string, err error) {
	if reloader == nil {
		return false, "", errors.New("routing reloader is nil")
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			prev := reloader.snapshot.Load()
			if prev != nil {
				return false, prev.Digest, ctxErr
			}
			return false, "", ctxErr
		}
	}

	var outcome string
	var onReload func(string)

	reloader.reloadMu.Lock()
	prev := reloader.snapshot.Load()
	prevDigest := ""
	if prev != nil {
		prevDigest = prev.Digest
	}

	recipientsBytes, routingFiles, newDigest, readErr := readRoutingInputs(reloader.recipientsFile, reloader.routingDir)
	if readErr != nil {
		code := classifyReloadError(readErr)
		reloader.storeErrorCodeLocked(prev, code)
		reloader.log().Warn("alarm_routing_reload_failed", "error_code", code, "error", "[REDACTED]")
		outcome = "failed"
		onReload = reloader.onReload
		reloader.reloadMu.Unlock()
		if onReload != nil {
			onReload(outcome)
		}
		return false, prevDigest, readErr
	}

	if prev != nil && newDigest == prev.Digest {
		reloader.reloadMu.Unlock()
		return false, prev.Digest, nil
	}

	recipients, routing, valErr := validateRoutingInputs(recipientsBytes, routingFiles)
	if valErr != nil {
		code := classifyReloadError(valErr)
		reloader.storeErrorCodeLocked(prev, code)
		reloader.log().Warn("alarm_routing_reload_failed", "error_code", code, "error", "[REDACTED]")
		outcome = "failed"
		onReload = reloader.onReload
		reloader.reloadMu.Unlock()
		if onReload != nil {
			onReload(outcome)
		}
		return false, prevDigest, valErr
	}

	var nextCount uint64 = 1
	if prev != nil {
		nextCount = prev.ReloadCount + 1
	}
	reloader.snapshot.Store(&RoutingSnapshot{
		Recipients:    &recipients,
		Routing:       &routing,
		Digest:        newDigest,
		LoadedAt:      reloader.now().UTC(),
		ReloadCount:   nextCount,
		LastErrorCode: "",
	})
	outcome = "success"
	onReload = reloader.onReload
	reloader.reloadMu.Unlock()

	if onReload != nil {
		onReload(outcome)
	}
	return true, newDigest, nil
}

// Run polls for routing and recipients changes on interval (default 30s) until ctx is cancelled.
func (reloader *RoutingReloader) Run(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if reloader == nil || ctx == nil {
		return
	}
	if interval <= 0 {
		interval = defaultRoutingReloadInterval
	}
	if logger != nil {
		reloader.logger.Store(logger)
	}
	activeLogger := reloader.log()
	defer observability.Recover(activeLogger, nil, "notification.routing_reloader")

	if ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				defer observability.Recover(activeLogger, nil, "notification.routing_reloader")
				_, _, _ = reloader.ReloadNow(ctx)
			}()
		}
	}
}

// SiteRoutingSummary returns a secret-free summary of the named Site's active routing configuration.
func (reloader *RoutingReloader) SiteRoutingSummary(siteID string) (SiteRoutingSummaryView, bool) {
	snap := reloader.Snapshot()
	if snap == nil {
		return SiteRoutingSummaryView{}, false
	}
	return snap.SiteRoutingSummary(siteID)
}

// SiteRoutingSummary builds a secret-free administrative summary for siteID from this snapshot.
func (snapshot RoutingSnapshot) SiteRoutingSummary(siteID string) (SiteRoutingSummaryView, bool) {
	if snapshot.Routing == nil {
		return SiteRoutingSummaryView{}, false
	}
	routing, ok := snapshot.Routing.Site(siteID)
	if !ok {
		return SiteRoutingSummaryView{}, false
	}
	view := SiteRoutingSummaryView{
		SiteID:                routing.SiteID,
		SiteName:              routing.SiteName,
		Timezone:              routing.Timezone,
		HoldMinutes:           routing.HoldMinutes,
		MaxAgeHours:           routing.MaxAgeHours,
		RateLimitPerHour:      routing.RateLimitPerHour,
		RootCauseAlarmType:    routing.RootCauseAlarmType,
		SuppressWhenRootCause: slices.Clone(routing.SuppressWhenRootCause),
		ConfigDigest:          snapshot.Digest,
		LoadedAt:              snapshot.LoadedAt,
		ReloadCount:           snapshot.ReloadCount,
		LastReloadErrorCode:   snapshot.LastErrorCode,
	}
	if routing.QuietHours != nil {
		qh := *routing.QuietHours
		view.QuietHours = &qh
	}
	if len(routing.RootCauseRules) > 0 {
		view.RootCauseRules = make([]RootCauseRule, len(routing.RootCauseRules))
		for i, rule := range routing.RootCauseRules {
			view.RootCauseRules[i] = RootCauseRule{
				WhenActivePrefix:  rule.WhenActivePrefix,
				WhenDeviceID:      rule.WhenDeviceID,
				ScopeSameDevice:   rule.ScopeSameDevice,
				SuppressPrefixes:  slices.Clone(rule.SuppressPrefixes),
				SuppressDeviceIDs: slices.Clone(rule.SuppressDeviceIDs),
			}
		}
	}
	view.Routes = make([]AlarmRouteSummaryView, len(routing.Routes))
	for i, route := range routing.Routes {
		clamped := clampRouteGrouping(route)
		steps := make([]EscalationStepSummaryView, len(route.Steps))
		for stepIndex, step := range route.Steps {
			steps[stepIndex] = EscalationStepSummaryView{
				AfterMinutes:   step.AfterMinutes,
				Channels:       slices.Clone(step.Channels),
				RecipientCount: len(step.Recipients),
			}
		}
		view.Routes[i] = AlarmRouteSummaryView{
			Name:               route.Name,
			MinSeverity:        route.MinSeverity,
			AlarmTypePrefixes:  slices.Clone(route.AlarmTypePrefixes),
			CooldownMinutes:    route.CooldownMinutes,
			GroupWindowSeconds: clamped.GroupWindowSeconds,
			GroupMaxItems:      clamped.GroupMaxItems,
			GroupBy:            clamped.GroupBy,
			NotifyAck:          route.NotifyAck,
			NotifyRecovery:     route.NotifyRecovery,
			Steps:              steps,
		}
	}
	return view, true
}

func (reloader *RoutingReloader) storeErrorCodeLocked(prev *RoutingSnapshot, code string) {
	if prev == nil {
		return
	}
	retained := *prev
	retained.LastErrorCode = code
	reloader.snapshot.Store(&retained)
}

func (reloader *RoutingReloader) log() *slog.Logger {
	if reloader != nil {
		if logger := reloader.logger.Load(); logger != nil {
			return logger
		}
	}
	return slog.Default()
}

func readRoutingInputs(recipientsFile, routingDir string) ([]byte, []namedRoutingFile, string, error) {
	recipientsBytes, err := os.ReadFile(recipientsFile)
	if err != nil {
		return nil, nil, "", errRecipientsReadFailed
	}
	dirInfo, err := os.Stat(routingDir)
	if err != nil || !dirInfo.IsDir() {
		return nil, nil, "", errRoutingReadFailed
	}
	paths, err := filepath.Glob(filepath.Join(routingDir, "*.alarm-routing.json"))
	if err != nil {
		return nil, nil, "", errRoutingReadFailed
	}
	sort.Strings(paths)
	files := make([]namedRoutingFile, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, "", errRoutingReadFailed
		}
		files = append(files, namedRoutingFile{base: filepath.Base(path), body: body})
	}
	return recipientsBytes, files, computeRoutingDigest(recipientsBytes, files), nil
}

func validateRoutingInputs(recipientsBytes []byte, files []namedRoutingFile) (RecipientDirectory, AlarmRoutingTable, error) {
	recipients, err := parseRecipientDirectory(recipientsBytes)
	if err != nil {
		return RecipientDirectory{}, AlarmRoutingTable{}, err
	}
	routing, err := loadAlarmRoutingFiles(files, recipients)
	if err != nil {
		return RecipientDirectory{}, AlarmRoutingTable{}, err
	}
	return recipients, routing, nil
}

func computeRoutingDigest(recipientsBytes []byte, files []namedRoutingFile) string {
	hasher := sha256.New()
	writeChunk := func(tag string, data []byte) {
		var lenBuf [8]byte
		_, _ = hasher.Write([]byte(tag))
		_, _ = hasher.Write([]byte{0})
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
		_, _ = hasher.Write(lenBuf[:])
		_, _ = hasher.Write(data)
	}
	writeChunk("recipients", recipientsBytes)
	for _, file := range files {
		writeChunk("routing-name", []byte(file.base))
		writeChunk("routing-body", file.body)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func classifyReloadError(err error) string {
	switch {
	case errors.Is(err, errRecipientsReadFailed):
		return ReloadErrorRecipientsRead
	case errors.Is(err, errRecipientsInvalid):
		return ReloadErrorRecipientsInvalid
	case errors.Is(err, errRoutingReadFailed):
		return ReloadErrorRoutingRead
	case errors.Is(err, errRoutingRecipientInvalid):
		return ReloadErrorRecipientMismatch
	case errors.Is(err, errRoutingInvalid):
		return ReloadErrorRoutingInvalid
	default:
		return ReloadErrorRoutingInvalid
	}
}
