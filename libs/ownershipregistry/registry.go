package ownershipregistry

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	OwnerGateway          = "platform-gateway"
	OwnerLegacy           = "legacy-hvac-backend"
	OwnerCore             = "platform-core-service"
	OwnerTelemetryRuntime = "telemetry-runtime-service"
	OwnerCommand          = "command-service"
	OwnerAnalyticsQuery   = "telemetry-query-service"

	PhaseLegacyPrimaryGoShadow       = "LEGACY_PRIMARY_GO_SHADOW"
	PhaseGoCanaryLegacyShadow        = "GO_CANARY_LEGACY_SHADOW"
	PhaseGoPrimaryLegacyReadFallback = "GO_PRIMARY_LEGACY_READ_FALLBACK"
	PhaseGoPrimary                   = "GO_PRIMARY"
	PhaseS2ContractOnly              = "R0-contract-only"
	PhaseS2DarkIngest                = "R1-dark-ingest"
	PhaseS2ShadowCompare             = "R2-shadow-compare"
	PhaseS2InternalCanary            = "R3-internal-canary"
	PhaseS2ExternalCanary            = "R4-external-canary-5"
	PhaseS2Ramp25                    = "R5-ramp-25"
	PhaseS2Ramp50                    = "R6-ramp-50"
	PhaseS2Primary                   = "R7-primary-100"
	PhaseS2LegacyRetired             = "R8-legacy-current-state-retired"
	PhaseS3ContractOnly              = "S3-R0-contract-only"
)

var (
	ErrRouteMissing  = errors.New("route ownership is missing")
	ErrRouteConflict = errors.New("route ownership is conflicting")
	ErrCohortKey     = errors.New("server-derived cohort key is required")
)

type Registry struct {
	RegistryVersion  int          `json:"registryVersion"`
	RegistryRevision int64        `json:"registryRevision"`
	Routes           []RouteEntry `json:"routes"`
}

type RouteEntry struct {
	Method                   string        `json:"method"`
	Path                     string        `json:"path"`
	Owner                    string        `json:"owner"`
	PublicIngress            string        `json:"publicIngress,omitempty"`
	ActivationStatus         string        `json:"activationStatus,omitempty"`
	Revision                 int64         `json:"revision"`
	Rollout                  RolloutPolicy `json:"rollout"`
	CompatibilityMode        string        `json:"compatibilityMode"`
	AllowedScopeDimensions   []string      `json:"allowedScopeDimensions"`
	MigrationPhase           string        `json:"migrationPhase,omitempty"`
	ShadowSideEffectPolicy   string        `json:"shadowSideEffectPolicy,omitempty"`
	ReadOnlyFallback         bool          `json:"readOnlyFallback,omitempty"`
	ReadFallbackOwner        string        `json:"readFallbackOwner,omitempty"`
	FallbackForbiddenResults []string      `json:"fallbackForbiddenResults,omitempty"`
	CohortGroup              string        `json:"cohortGroup,omitempty"`
}

type RolloutPolicy struct {
	Mode          string `json:"mode"`
	Percentage    int    `json:"percentage,omitempty"`
	FallbackOwner string `json:"fallbackOwner,omitempty"`
	CohortSalt    string `json:"cohortSalt,omitempty"`
}

type Decision struct {
	RouteKey                 string
	PathTemplate             string
	DeclaredOwner            string
	SelectedOwner            string
	RegistryRevision         int64
	RouteRevision            int64
	CompatibilityMode        string
	AllowedScopeDimensions   []string
	CohortBucket             *int
	MigrationPhase           string
	ShadowOwner              string
	ReadFallbackOwner        string
	FallbackForbiddenResults []string
	CohortGroup              string
}

type Snapshot struct {
	registry Registry
	routes   []compiledRoute
}

type compiledRoute struct {
	entry     RouteEntry
	segments  []string
	canonical string
}

type Manager struct {
	current atomic.Pointer[Snapshot]
	audit   AuditSink
	now     func() time.Time
}

type PolicyChangeContext struct {
	CorrelationID     string
	TraceID           string
	ExecutingService  string
	ExecutingSPIFFEID string
}

func Parse(input []byte) (*Snapshot, error) {
	var registry Registry
	if err := json.Unmarshal(input, &registry); err != nil {
		return nil, fmt.Errorf("decode route registry: %w", err)
	}
	if registry.RegistryVersion != 1 || registry.RegistryRevision < 1 {
		return nil, errors.New("route registry version or revision is invalid")
	}
	if len(registry.Routes) == 0 {
		return nil, errors.New("route registry is empty")
	}
	seen := map[string]struct{}{}
	canonical := map[string]struct{}{}
	compiled := make([]compiledRoute, 0, len(registry.Routes))
	cohortGroups := map[string]RouteEntry{}
	for index := range registry.Routes {
		entry := registry.Routes[index]
		entry.Method = strings.ToUpper(strings.TrimSpace(entry.Method))
		entry.Path = strings.TrimSpace(entry.Path)
		entry.Owner = strings.TrimSpace(entry.Owner)
		if err := validateEntry(entry); err != nil {
			return nil, fmt.Errorf("route %s %s: %w", entry.Method, entry.Path, err)
		}
		key := entry.Method + " " + entry.Path
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: %s", ErrRouteConflict, key)
		}
		seen[key] = struct{}{}
		segments := splitPath(entry.Path)
		canonicalKey := entry.Method + " " + canonicalPath(segments)
		if _, exists := canonical[canonicalKey]; exists {
			return nil, fmt.Errorf("%w: %s", ErrRouteConflict, canonicalKey)
		}
		for _, existing := range compiled {
			if existing.entry.Method == entry.Method && templatesOverlap(existing.segments, segments) {
				return nil, fmt.Errorf("%w: %s overlaps %s", ErrRouteConflict, key, existing.entry.Method+" "+existing.entry.Path)
			}
		}
		canonical[canonicalKey] = struct{}{}
		if entry.CohortGroup != "" {
			if previous, exists := cohortGroups[entry.CohortGroup]; exists {
				if err := validateCohortGroup(previous, entry); err != nil {
					return nil, fmt.Errorf("cohort group %s: %w", entry.CohortGroup, err)
				}
			} else {
				cohortGroups[entry.CohortGroup] = entry
			}
		}
		entry.AllowedScopeDimensions = append([]string(nil), entry.AllowedScopeDimensions...)
		compiled = append(compiled, compiledRoute{entry: entry, segments: segments, canonical: canonicalKey})
		registry.Routes[index] = entry
	}
	return &Snapshot{registry: registry, routes: compiled}, nil
}

func validateEntry(entry RouteEntry) error {
	if entry.Method == "" || !strings.HasPrefix(entry.Path, "/api/v1/") || entry.Revision < 1 {
		return errors.New("method, public path and positive revision are required")
	}
	if !isCandidateOwner(entry.Owner) {
		return errors.New("owner is unsupported")
	}
	if entry.CompatibilityMode != "native" && entry.CompatibilityMode != "legacy-read" {
		return errors.New("compatibility mode is unsupported")
	}
	if entry.Owner == OwnerLegacy && entry.CompatibilityMode != "legacy-read" {
		return errors.New("Legacy ownership requires legacy-read compatibility")
	}
	allowed := map[string]bool{"organization": true, "principal": true, "site": true, "device": true, "key": true}
	seenScopes := map[string]bool{}
	for _, scope := range entry.AllowedScopeDimensions {
		if !allowed[scope] || seenScopes[scope] {
			return errors.New("scope dimensions are invalid")
		}
		seenScopes[scope] = true
	}
	switch entry.Rollout.Mode {
	case "all":
		if entry.Rollout.Percentage != 0 || entry.Rollout.FallbackOwner != "" || entry.Rollout.CohortSalt != "" {
			return errors.New("all rollout contains cohort fields")
		}
	case "percentage":
		if entry.Rollout.Percentage < 0 || entry.Rollout.Percentage > 100 {
			return errors.New("rollout percentage is outside 0..100")
		}
		if entry.Rollout.FallbackOwner == "" || entry.Rollout.FallbackOwner == entry.Owner || !isCandidateOwner(entry.Rollout.FallbackOwner) {
			return errors.New("fallback owner is invalid")
		}
		if len(entry.Rollout.CohortSalt) < 8 || !seenScopes["organization"] || !seenScopes["principal"] {
			return errors.New("percentage rollout requires salt, organization and principal")
		}
	case "disabled":
		if entry.Rollout.Percentage != 0 || entry.Rollout.FallbackOwner != "" || entry.Rollout.CohortSalt != "" {
			return errors.New("disabled rollout contains cohort fields")
		}
	default:
		return errors.New("rollout mode is unsupported")
	}
	if entry.MigrationPhase != "" {
		var err error
		if isS2Phase(entry.MigrationPhase) {
			err = validateS2Phase(entry, seenScopes)
		} else if isS3Phase(entry.MigrationPhase) {
			err = validateS3Phase(entry, seenScopes)
		} else {
			err = validateMigrationPhase(entry)
		}
		if err != nil {
			return err
		}
	}
	if entry.Owner == OwnerTelemetryRuntime && !isS2Phase(entry.MigrationPhase) {
		return errors.New("Telemetry Runtime ownership requires an S2 migration phase")
	}
	if entry.Owner == OwnerCommand && !isS3Phase(entry.MigrationPhase) {
		return errors.New("Command ownership requires an S3 migration phase")
	}
	return nil
}

func validateS2Phase(entry RouteEntry, seenScopes map[string]bool) error {
	if entry.PublicIngress != OwnerGateway || entry.ShadowSideEffectPolicy != "NONE" || entry.ReadOnlyFallback || entry.ReadFallbackOwner != "" {
		return errors.New("S2 route must use Gateway ingress, side-effect-free shadowing and no request fallback")
	}
	for _, required := range []string{"organization", "site", "device", "principal", "key"} {
		if !seenScopes[required] {
			return errors.New("S2 scope dimensions are incomplete")
		}
	}
	for _, required := range []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND", "REVISION_GAP", "RECOVERY_FAILED"} {
		if !containsString(entry.FallbackForbiddenResults, required) {
			return errors.New("S2 forbidden results are incomplete")
		}
	}
	if entry.CohortGroup == "" {
		return errors.New("S2 route requires a cohort group")
	}
	switch entry.MigrationPhase {
	case PhaseS2ContractOnly:
		if entry.Owner != OwnerTelemetryRuntime || entry.ActivationStatus != "expand-baseline" || entry.Rollout.Mode != "disabled" || entry.CompatibilityMode != "native" {
			return errors.New("S2 contract-only policy is invalid")
		}
	case PhaseS2DarkIngest:
		if entry.Owner != OwnerLegacy || entry.ActivationStatus != "dark-ingest" || entry.Rollout.Mode != "all" || entry.CompatibilityMode != "legacy-read" {
			return errors.New("S2 dark-ingest policy is invalid")
		}
	case PhaseS2ShadowCompare:
		if entry.Owner != OwnerLegacy || entry.ActivationStatus != "shadow-compare" || entry.Rollout.Mode != "all" || entry.CompatibilityMode != "legacy-read" {
			return errors.New("S2 shadow-compare policy is invalid")
		}
	case PhaseS2InternalCanary:
		if err := validateS2PercentagePhase(entry, 1); err != nil {
			return err
		}
	case PhaseS2ExternalCanary:
		if err := validateS2PercentagePhase(entry, 5); err != nil {
			return err
		}
	case PhaseS2Ramp25:
		if err := validateS2PercentagePhase(entry, 25); err != nil {
			return err
		}
	case PhaseS2Ramp50:
		if err := validateS2PercentagePhase(entry, 50); err != nil {
			return err
		}
	case PhaseS2Primary:
		if entry.Owner != OwnerTelemetryRuntime || entry.ActivationStatus != "primary" || entry.Rollout.Mode != "all" || entry.CompatibilityMode != "native" {
			return errors.New("S2 primary policy is invalid")
		}
	case PhaseS2LegacyRetired:
		if entry.Owner != OwnerTelemetryRuntime || entry.ActivationStatus != "legacy-retired" || entry.Rollout.Mode != "all" || entry.CompatibilityMode != "native" {
			return errors.New("S2 Legacy-retired policy is invalid")
		}
	default:
		return errors.New("S2 migration phase is unsupported")
	}
	return nil
}

func validateS3Phase(entry RouteEntry, seenScopes map[string]bool) error {
	if entry.PublicIngress != OwnerGateway || entry.ShadowSideEffectPolicy != "SYNTHETIC_ONLY" || entry.ReadOnlyFallback || entry.ReadFallbackOwner != "" {
		return errors.New("S3 route must use Gateway ingress, Synthetic-only shadowing and no request fallback")
	}
	for _, required := range []string{"organization", "site", "device", "principal"} {
		if !seenScopes[required] {
			return errors.New("S3 scope dimensions are incomplete")
		}
	}
	for _, required := range []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND", "CURRENT_STATE_UNSAFE", "OUTCOME_UNKNOWN"} {
		if !containsString(entry.FallbackForbiddenResults, required) {
			return errors.New("S3 forbidden results are incomplete")
		}
	}
	if entry.CohortGroup == "" {
		return errors.New("S3 route requires a cohort group")
	}
	if entry.MigrationPhase != PhaseS3ContractOnly || entry.Owner != OwnerCommand || entry.ActivationStatus != "expand-baseline" || entry.Rollout.Mode != "disabled" || entry.CompatibilityMode != "native" {
		return errors.New("S3 contract-only policy is invalid")
	}
	return nil
}

func validateS2PercentagePhase(entry RouteEntry, percentage int) error {
	if entry.Owner != OwnerTelemetryRuntime || entry.ActivationStatus != "canary" || entry.CompatibilityMode != "native" ||
		entry.Rollout.Mode != "percentage" || entry.Rollout.Percentage != percentage || entry.Rollout.FallbackOwner != OwnerLegacy {
		return errors.New("S2 percentage rollout policy is invalid")
	}
	return nil
}

func validateCohortGroup(previous, next RouteEntry) error {
	if previous.Revision != next.Revision || previous.MigrationPhase != next.MigrationPhase || previous.Owner != next.Owner ||
		previous.ActivationStatus != next.ActivationStatus || previous.CompatibilityMode != next.CompatibilityMode || previous.Rollout != next.Rollout {
		return errors.New("route revision, phase, owner or rollout is inconsistent")
	}
	return nil
}

func isS2Phase(phase string) bool {
	_, ok := s2PhaseRank(phase)
	return ok
}

func isS3Phase(phase string) bool {
	return phase == PhaseS3ContractOnly
}

func validateMigrationPhase(entry RouteEntry) error {
	if entry.Method != http.MethodGet || entry.ShadowSideEffectPolicy != "NONE" {
		return errors.New("migration routes must be GET routes with side-effect-free shadowing")
	}
	for _, required := range []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND"} {
		if !containsString(entry.FallbackForbiddenResults, required) {
			return errors.New("migration fallback forbidden results are incomplete")
		}
	}
	switch entry.MigrationPhase {
	case PhaseLegacyPrimaryGoShadow:
		if !entry.ReadOnlyFallback || entry.Owner != OwnerLegacy || entry.CompatibilityMode != "legacy-read" || entry.Rollout.Mode != "percentage" || entry.Rollout.Percentage != 100 || entry.Rollout.FallbackOwner != OwnerCore || entry.ReadFallbackOwner != "" {
			return errors.New("legacy-primary shadow phase policy is invalid")
		}
	case PhaseGoCanaryLegacyShadow:
		if !entry.ReadOnlyFallback || entry.Owner != OwnerCore || entry.CompatibilityMode != "native" || entry.Rollout.Mode != "percentage" || entry.Rollout.Percentage <= 0 || entry.Rollout.Percentage >= 100 || entry.Rollout.FallbackOwner != OwnerLegacy || entry.ReadFallbackOwner != "" {
			return errors.New("Go canary shadow phase policy is invalid")
		}
	case PhaseGoPrimaryLegacyReadFallback:
		if !entry.ReadOnlyFallback || entry.Owner != OwnerCore || entry.CompatibilityMode != "native" || entry.Rollout.Mode != "all" || entry.ReadFallbackOwner != OwnerLegacy {
			return errors.New("Go-primary Legacy fallback phase policy is invalid")
		}
	case PhaseGoPrimary:
		if entry.ReadOnlyFallback || entry.Owner != OwnerCore || entry.CompatibilityMode != "native" || entry.Rollout.Mode != "all" || entry.ReadFallbackOwner != "" {
			return errors.New("Go-primary phase policy is invalid")
		}
	default:
		return errors.New("migration phase is unsupported")
	}
	return nil
}

func isActiveOwner(owner string) bool {
	return owner == OwnerGateway || owner == OwnerLegacy
}

func isCandidateOwner(owner string) bool {
	return isActiveOwner(owner) || owner == OwnerCore || owner == OwnerTelemetryRuntime || owner == OwnerCommand || owner == OwnerAnalyticsQuery
}

func NewManager(snapshot *Snapshot, audit AuditSink, now func() time.Time) *Manager {
	if snapshot == nil {
		panic("route ownership snapshot is required")
	}
	if audit == nil {
		audit = NewMemoryAuditSink()
	}
	if now == nil {
		now = time.Now
	}
	manager := &Manager{audit: audit, now: now}
	manager.current.Store(snapshot)
	return manager
}

func (manager *Manager) Current() *Snapshot {
	return manager.current.Load()
}

func (manager *Manager) Reload(ctx context.Context, input []byte, meta PolicyChangeContext) error {
	return manager.reload(ctx, input, meta, false)
}

func (manager *Manager) reload(ctx context.Context, input []byte, meta PolicyChangeContext, allowS2SessionInvalidation bool) error {
	candidate, err := Parse(input)
	if err != nil {
		return err
	}
	current := manager.current.Load()
	if err := validateRevisionTransition(current, candidate); err != nil {
		return err
	}
	if !allowS2SessionInvalidation && s2SnapshotsTransitionInvalidatesSessions(current, candidate) {
		return errors.New("S2 route transition requires ReloadS2 session invalidation")
	}
	if err := manager.audit.Record(ctx, AuditRecord{
		EventType:         "ROUTE_POLICY_CHANGED",
		RouteKey:          "registry",
		Method:            "SYSTEM",
		PathTemplate:      "route-ownership-registry",
		SelectedOwner:     OwnerGateway,
		PreviousOwner:     OwnerGateway,
		RegistryRevision:  candidate.registry.RegistryRevision,
		PreviousRevision:  current.registry.RegistryRevision,
		RouteRevision:     candidate.registry.RegistryRevision,
		CompatibilityMode: "native",
		ExecutingService:  defaultString(meta.ExecutingService, OwnerGateway),
		ExecutingSPIFFEID: meta.ExecutingSPIFFEID,
		CorrelationID:     meta.CorrelationID,
		TraceID:           meta.TraceID,
		OccurredAt:        manager.now().UTC(),
	}); err != nil {
		return fmt.Errorf("persist route policy audit: %w", err)
	}
	manager.current.Store(candidate)
	return nil
}

func validateRevisionTransition(current, candidate *Snapshot) error {
	if candidate.registry.RegistryRevision <= current.registry.RegistryRevision {
		return errors.New("route registry revision did not advance")
	}
	previous := map[string]RouteEntry{}
	for _, route := range current.registry.Routes {
		previous[route.Method+" "+route.Path] = route
	}
	for _, route := range candidate.registry.Routes {
		key := route.Method + " " + route.Path
		old, exists := previous[key]
		if !exists {
			continue
		}
		if route.Revision < old.Revision {
			return fmt.Errorf("route owner revision regressed for %s", key)
		}
		if routePolicyChanged(old, route) && route.Revision <= old.Revision {
			return fmt.Errorf("route policy changed without revision advance for %s", key)
		}
		if err := validatePhaseTransition(old.MigrationPhase, route.MigrationPhase); err != nil {
			return fmt.Errorf("route phase transition invalid for %s: %w", key, err)
		}
		delete(previous, key)
	}
	if len(previous) != 0 {
		return errors.New("route policy removed an existing public route")
	}
	return nil
}

func routePolicyChanged(old, next RouteEntry) bool {
	return old.Owner != next.Owner || old.PublicIngress != next.PublicIngress || old.ActivationStatus != next.ActivationStatus ||
		old.CompatibilityMode != next.CompatibilityMode || old.Rollout != next.Rollout ||
		old.MigrationPhase != next.MigrationPhase || old.ShadowSideEffectPolicy != next.ShadowSideEffectPolicy ||
		old.ReadOnlyFallback != next.ReadOnlyFallback || old.ReadFallbackOwner != next.ReadFallbackOwner || old.CohortGroup != next.CohortGroup ||
		strings.Join(old.AllowedScopeDimensions, "\x00") != strings.Join(next.AllowedScopeDimensions, "\x00") ||
		strings.Join(old.FallbackForbiddenResults, "\x00") != strings.Join(next.FallbackForbiddenResults, "\x00")
}

func validatePhaseTransition(old, next string) error {
	if old == next {
		return nil
	}
	if old == "" && next == PhaseLegacyPrimaryGoShadow {
		return nil
	}
	if old == "" || next == "" {
		return errors.New("migration phase cannot be removed or entered outside phase one")
	}
	oldRank, oldOK := migrationPhaseRank(old)
	nextRank, nextOK := migrationPhaseRank(next)
	if !oldOK || !nextOK {
		oldRank, oldOK = s2PhaseRank(old)
		nextRank, nextOK = s2PhaseRank(next)
	}
	if !oldOK || !nextOK || nextRank-oldRank > 1 || oldRank-nextRank > 1 {
		return errors.New("migration phase skipped a required adjacent state")
	}
	return nil
}

func migrationPhaseRank(phase string) (int, bool) {
	switch phase {
	case PhaseLegacyPrimaryGoShadow:
		return 1, true
	case PhaseGoCanaryLegacyShadow:
		return 2, true
	case PhaseGoPrimaryLegacyReadFallback:
		return 3, true
	case PhaseGoPrimary:
		return 4, true
	default:
		return 0, false
	}
}

func s2PhaseRank(phase string) (int, bool) {
	switch phase {
	case PhaseS2ContractOnly:
		return 0, true
	case PhaseS2DarkIngest:
		return 1, true
	case PhaseS2ShadowCompare:
		return 2, true
	case PhaseS2InternalCanary:
		return 3, true
	case PhaseS2ExternalCanary:
		return 4, true
	case PhaseS2Ramp25:
		return 5, true
	case PhaseS2Ramp50:
		return 6, true
	case PhaseS2Primary:
		return 7, true
	case PhaseS2LegacyRetired:
		return 8, true
	default:
		return 0, false
	}
}

func (snapshot *Snapshot) Resolve(method, requestPath, businessKey string) (Decision, error) {
	method = strings.ToUpper(method)
	pathSegments := splitPath(requestPath)
	var matched *compiledRoute
	for index := range snapshot.routes {
		candidate := &snapshot.routes[index]
		if candidate.entry.Method != method || !matches(candidate.segments, pathSegments) {
			continue
		}
		if matched != nil {
			return Decision{}, ErrRouteConflict
		}
		matched = candidate
	}
	if matched == nil {
		return Decision{}, ErrRouteMissing
	}
	entry := matched.entry
	if entry.Rollout.Mode == "disabled" {
		return Decision{}, ErrRouteMissing
	}
	decision := Decision{
		RouteKey:                 entry.Method + " " + entry.Path,
		PathTemplate:             entry.Path,
		DeclaredOwner:            entry.Owner,
		SelectedOwner:            entry.Owner,
		RegistryRevision:         snapshot.registry.RegistryRevision,
		RouteRevision:            entry.Revision,
		CompatibilityMode:        entry.CompatibilityMode,
		AllowedScopeDimensions:   append([]string(nil), entry.AllowedScopeDimensions...),
		MigrationPhase:           entry.MigrationPhase,
		ReadFallbackOwner:        entry.ReadFallbackOwner,
		FallbackForbiddenResults: append([]string(nil), entry.FallbackForbiddenResults...),
		CohortGroup:              entry.CohortGroup,
	}
	if entry.Rollout.Mode == "percentage" {
		if businessKey == "" {
			return Decision{}, ErrCohortKey
		}
		cohortMaterial := entry.Rollout.CohortSalt + "\x00" + businessKey
		if entry.CohortGroup != "" {
			cohortMaterial = fmt.Sprintf("%s\x00%s\x00%d\x00%s", entry.Rollout.CohortSalt, entry.CohortGroup, entry.Revision, businessKey)
		}
		digest := sha256.Sum256([]byte(cohortMaterial))
		bucket := int(binary.BigEndian.Uint64(digest[:8]) % 100)
		decision.CohortBucket = &bucket
		if bucket >= entry.Rollout.Percentage {
			decision.SelectedOwner = entry.Rollout.FallbackOwner
		}
	}
	switch entry.MigrationPhase {
	case PhaseLegacyPrimaryGoShadow:
		decision.ShadowOwner = OwnerCore
	case PhaseGoCanaryLegacyShadow:
		if decision.SelectedOwner == OwnerCore {
			decision.ShadowOwner = OwnerLegacy
		} else {
			decision.ShadowOwner = OwnerCore
		}
	}
	return decision, nil
}

func (snapshot *Snapshot) AllowedMethods(requestPath string) []string {
	pathSegments := splitPath(requestPath)
	seen := map[string]struct{}{}
	var methods []string
	for index := range snapshot.routes {
		candidate := &snapshot.routes[index]
		if candidate.entry.Rollout.Mode == "disabled" {
			continue
		}
		if !matches(candidate.segments, pathSegments) {
			continue
		}
		if _, exists := seen[candidate.entry.Method]; exists {
			continue
		}
		seen[candidate.entry.Method] = struct{}{}
		methods = append(methods, candidate.entry.Method)
	}
	return methods
}

func (snapshot *Snapshot) ContainsOwner(owner string) bool {
	for _, route := range snapshot.registry.Routes {
		if route.Owner == owner || route.Rollout.FallbackOwner == owner || route.ReadFallbackOwner == owner {
			return true
		}
	}
	return false
}

func (snapshot *Snapshot) RegistryRevision() int64 {
	return snapshot.registry.RegistryRevision
}

func (snapshot *Snapshot) Registry() Registry {
	copyValue := snapshot.registry
	copyValue.Routes = append([]RouteEntry(nil), snapshot.registry.Routes...)
	return copyValue
}

func splitPath(value string) []string {
	trimmed := strings.Trim(value, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func canonicalPath(segments []string) string {
	output := make([]string, len(segments))
	for index, segment := range segments {
		if isPlaceholder(segment) {
			output[index] = "{}"
		} else {
			output[index] = segment
		}
	}
	return "/" + strings.Join(output, "/")
}

func matches(template, actual []string) bool {
	if len(template) != len(actual) {
		return false
	}
	for index := range template {
		if isPlaceholder(template[index]) {
			if actual[index] == "" {
				return false
			}
			continue
		}
		if template[index] != actual[index] {
			return false
		}
	}
	return true
}

func templatesOverlap(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if isPlaceholder(left[index]) || isPlaceholder(right[index]) {
			continue
		}
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func isPlaceholder(segment string) bool {
	return len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
