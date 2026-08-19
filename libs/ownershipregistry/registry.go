package ownershipregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
)

const (
	OwnerGateway          = "platform-gateway"
	OwnerCore             = "platform-core-service"
	OwnerTelemetryRuntime = "telemetry-runtime-service"
	OwnerCommand          = "command-service"
	OwnerAnalyticsQuery   = "telemetry-query-service"
	OwnerOperationsAgent  = "operations-agent-service"
	OwnerAlarm            = "alarm-service"
	OwnerWorkOrder        = "work-order-service"
	OwnerPresentation     = "presentation-service"
)

var (
	ErrRouteMissing  = errors.New("route ownership is missing")
	ErrRouteConflict = errors.New("route ownership is conflicting")
)

type Registry struct {
	RegistryVersion  int          `json:"registryVersion"`
	RegistryRevision int64        `json:"registryRevision"`
	Routes           []RouteEntry `json:"routes"`
}

type RouteEntry struct {
	Method                 string        `json:"method"`
	Path                   string        `json:"path"`
	Owner                  string        `json:"owner"`
	PublicIngress          string        `json:"publicIngress,omitempty"`
	Revision               int64         `json:"revision"`
	Rollout                RolloutPolicy `json:"rollout"`
	CompatibilityMode      string        `json:"compatibilityMode"`
	AllowedScopeDimensions []string      `json:"allowedScopeDimensions"`
}

type RolloutPolicy struct {
	Mode string `json:"mode"`
}

type Decision struct {
	RouteKey               string
	PathTemplate           string
	SelectedOwner          string
	RegistryRevision       int64
	RouteRevision          int64
	CompatibilityMode      string
	AllowedScopeDimensions []string
}

type Snapshot struct {
	registry Registry
	routes   []compiledRoute
}

type compiledRoute struct {
	entry    RouteEntry
	segments []string
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
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return nil, fmt.Errorf("decode route registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("route registry contains trailing JSON")
		}
		return nil, fmt.Errorf("decode route registry trailing data: %w", err)
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
	for index := range registry.Routes {
		entry := registry.Routes[index]
		entry.Method = strings.ToUpper(strings.TrimSpace(entry.Method))
		entry.Path = strings.TrimSpace(entry.Path)
		entry.Owner = strings.TrimSpace(entry.Owner)
		entry.PublicIngress = strings.TrimSpace(entry.PublicIngress)
		entry.CompatibilityMode = strings.TrimSpace(entry.CompatibilityMode)
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

		entry.AllowedScopeDimensions = append([]string(nil), entry.AllowedScopeDimensions...)
		compiled = append(compiled, compiledRoute{entry: entry, segments: segments})
		registry.Routes[index] = entry
	}
	return &Snapshot{registry: registry, routes: compiled}, nil
}

func validateEntry(entry RouteEntry) error {
	if entry.Method == "" || !strings.HasPrefix(entry.Path, "/api/v1/") || entry.Revision < 1 {
		return errors.New("method, public path and positive revision are required")
	}
	if !isCurrentOwner(entry.Owner) {
		return errors.New("owner is unsupported")
	}
	if entry.Owner == OwnerGateway {
		if entry.PublicIngress != "" && entry.PublicIngress != OwnerGateway {
			return errors.New("gateway-owned route has invalid public ingress")
		}
	} else if entry.PublicIngress != OwnerGateway {
		return errors.New("non-gateway public route must enter through platform-gateway")
	}
	if entry.CompatibilityMode != "native" {
		return errors.New("only native compatibility mode is supported")
	}
	if entry.Rollout.Mode != "all" && entry.Rollout.Mode != "disabled" {
		return errors.New("only deterministic all-or-disabled activation is supported")
	}

	allowedScopes := map[string]bool{
		"tenant":     true,
		"principal":  true,
		"site":       true,
		"space":      true,
		"asset":      true,
		"device":     true,
		"point":      true,
		"command":    true,
		"key":        true,
		"alarm":      true,
		"work-order": true,
	}
	seenScopes := map[string]bool{}
	for _, scope := range entry.AllowedScopeDimensions {
		if !allowedScopes[scope] || seenScopes[scope] {
			return errors.New("scope dimensions are invalid")
		}
		seenScopes[scope] = true
	}
	return nil
}

func isCurrentOwner(owner string) bool {
	switch owner {
	case OwnerGateway, OwnerCore, OwnerTelemetryRuntime, OwnerCommand, OwnerAnalyticsQuery, OwnerOperationsAgent, OwnerAlarm, OwnerWorkOrder, OwnerPresentation:
		return true
	default:
		return false
	}
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
	candidate, err := Parse(input)
	if err != nil {
		return err
	}
	current := manager.current.Load()
	if err := validateRevisionTransition(current, candidate); err != nil {
		return err
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
	}
	return nil
}

func routePolicyChanged(old, next RouteEntry) bool {
	return old.Owner != next.Owner ||
		old.PublicIngress != next.PublicIngress ||
		old.CompatibilityMode != next.CompatibilityMode ||
		old.Rollout != next.Rollout ||
		strings.Join(old.AllowedScopeDimensions, "\x00") != strings.Join(next.AllowedScopeDimensions, "\x00")
}

func (snapshot *Snapshot) Resolve(method, requestPath, _ string) (Decision, error) {
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
	if matched == nil || matched.entry.Rollout.Mode == "disabled" {
		return Decision{}, ErrRouteMissing
	}
	entry := matched.entry
	return Decision{
		RouteKey:               entry.Method + " " + entry.Path,
		PathTemplate:           entry.Path,
		SelectedOwner:          entry.Owner,
		RegistryRevision:       snapshot.registry.RegistryRevision,
		RouteRevision:          entry.Revision,
		CompatibilityMode:      "native",
		AllowedScopeDimensions: append([]string(nil), entry.AllowedScopeDimensions...),
	}, nil
}

func (snapshot *Snapshot) AllowedMethods(requestPath string) []string {
	pathSegments := splitPath(requestPath)
	seen := map[string]struct{}{}
	var methods []string
	for index := range snapshot.routes {
		candidate := &snapshot.routes[index]
		if candidate.entry.Rollout.Mode == "disabled" || !matches(candidate.segments, pathSegments) {
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
		if route.Owner == owner {
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
	copyValue.Routes = make([]RouteEntry, len(snapshot.registry.Routes))
	for index, route := range snapshot.registry.Routes {
		copyValue.Routes[index] = route
		copyValue.Routes[index].AllowedScopeDimensions = append([]string(nil), route.AllowedScopeDimensions...)
	}
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

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
