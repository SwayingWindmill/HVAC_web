package ownershipregistry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

const s2CohortGroup = "s2-current-state-v1"

var s2RouteSurfaces = []struct {
	method string
	path   string
	actual string
}{
	{method: "GET", path: "/api/v1/devices/{deviceId}/observation-snapshot", actual: "/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot"},
	{method: "POST", path: "/api/v1/telemetry/observation-snapshots:batchGet", actual: "/api/v1/telemetry/observation-snapshots:batchGet"},
	{method: "POST", path: "/api/v1/telemetry/subscriptions:bootstrap", actual: "/api/v1/telemetry/subscriptions:bootstrap"},
	{method: "POST", path: "/api/v1/telemetry/recovery-cursors:checkpoint", actual: "/api/v1/telemetry/recovery-cursors:checkpoint"},
}

type s2PhasePolicy struct {
	phase         string
	owner         string
	activation    string
	compatibility string
	rollout       ownershipregistry.RolloutPolicy
	registryRev   int64
	routeRevision int64
}

func TestS2CurrentStateCohortIsConsistentAndRevisionBound(t *testing.T) {
	canary := s2PhasePolicy{
		phase:         ownershipregistry.PhaseS2InternalCanary,
		owner:         ownershipregistry.OwnerTelemetryRuntime,
		activation:    "canary",
		compatibility: "native",
		rollout: ownershipregistry.RolloutPolicy{
			Mode: "percentage", Percentage: 1,
			FallbackOwner: ownershipregistry.OwnerLegacy,
			CohortSalt:    "s2-current-state-rollout-v1",
		},
		registryRev:   10,
		routeRevision: 4,
	}
	first := mustParseS2(t, canary)
	secondPolicy := canary
	secondPolicy.registryRev = 11
	secondPolicy.routeRevision = 5
	second := mustParseS2(t, secondPolicy)

	var sawS2, sawLegacy, sawRevisionChange bool
	for index := 0; index < 20000; index++ {
		businessKey := fmt.Sprintf("018f2e00-1000-7000-8000-%012d\x00principal-%d", index, index)
		firstDecisions := resolveS2Group(t, first, businessKey)
		secondDecisions := resolveS2Group(t, second, businessKey)
		if firstDecisions[0].SelectedOwner == ownershipregistry.OwnerTelemetryRuntime {
			sawS2 = true
		} else if firstDecisions[0].SelectedOwner == ownershipregistry.OwnerLegacy {
			sawLegacy = true
		}
		if *firstDecisions[0].CohortBucket != *secondDecisions[0].CohortBucket {
			sawRevisionChange = true
		}
		if sawS2 && sawLegacy && sawRevisionChange {
			break
		}
	}
	if !sawS2 || !sawLegacy {
		t.Fatalf("1%% cohort did not produce both owners: s2=%v legacy=%v", sawS2, sawLegacy)
	}
	if !sawRevisionChange {
		t.Fatal("route revision did not re-bind the S2 cohort assignment")
	}
}

func TestS2AdjacentPromotionAndRollbackAreAccepted(t *testing.T) {
	policies := []s2PhasePolicy{
		{
			phase:         ownershipregistry.PhaseS2ContractOnly,
			owner:         ownershipregistry.OwnerTelemetryRuntime,
			activation:    "expand-baseline",
			compatibility: "native",
			rollout:       ownershipregistry.RolloutPolicy{Mode: "disabled"},
			registryRev:   7,
			routeRevision: 1,
		},
		{
			phase:         ownershipregistry.PhaseS2DarkIngest,
			owner:         ownershipregistry.OwnerLegacy,
			activation:    "dark-ingest",
			compatibility: "legacy-read",
			rollout:       ownershipregistry.RolloutPolicy{Mode: "all"},
			registryRev:   8,
			routeRevision: 2,
		},
		{
			phase:         ownershipregistry.PhaseS2ShadowCompare,
			owner:         ownershipregistry.OwnerLegacy,
			activation:    "shadow-compare",
			compatibility: "legacy-read",
			rollout:       ownershipregistry.RolloutPolicy{Mode: "all"},
			registryRev:   9,
			routeRevision: 3,
		},
		{
			phase:         ownershipregistry.PhaseS2InternalCanary,
			owner:         ownershipregistry.OwnerTelemetryRuntime,
			activation:    "canary",
			compatibility: "native",
			rollout:       ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 1, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"},
			registryRev:   10,
			routeRevision: 4,
		},
		{
			phase:         ownershipregistry.PhaseS2ShadowCompare,
			owner:         ownershipregistry.OwnerLegacy,
			activation:    "shadow-compare",
			compatibility: "legacy-read",
			rollout:       ownershipregistry.RolloutPolicy{Mode: "all"},
			registryRev:   11,
			routeRevision: 5,
		},
	}

	manager := ownershipregistry.NewManager(mustParseS2(t, policies[0]), ownershipregistry.NewMemoryAuditSink(), fixedNow)
	invalidator := &recordingS2Invalidator{}
	for _, policy := range policies[1:] {
		if _, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, policy), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}, invalidator); err != nil {
			t.Fatalf("reload phase %s: %v", policy.phase, err)
		}
	}
	if len(invalidator.commands) != 1 || !invalidator.commands[0].Rollback {
		t.Fatalf("phase sequence invalidations = %#v", invalidator.commands)
	}
	decision, err := manager.Current().Resolve("GET", s2RouteSurfaces[0].actual, "org\x00principal")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerLegacy || decision.MigrationPhase != ownershipregistry.PhaseS2ShadowCompare || decision.RouteRevision != 5 {
		t.Fatalf("rollback decision = %#v", decision)
	}

	skipped := policies[0]
	skipped.registryRev = 12
	skipped.routeRevision = 6
	skipped.phase = ownershipregistry.PhaseS2ExternalCanary
	skipped.owner = ownershipregistry.OwnerTelemetryRuntime
	skipped.activation = "canary"
	skipped.compatibility = "native"
	skipped.rollout = ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 5, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"}
	if err := manager.Reload(context.Background(), mustMarshalS2(t, skipped), ownershipregistry.PolicyChangeContext{}); err == nil {
		t.Fatal("R2 to R4 non-adjacent transition was accepted")
	}
}

func TestS2FullPromotionSequenceAndR8RetirementAccepted(t *testing.T) {
	policies := []s2PhasePolicy{
		{phase: ownershipregistry.PhaseS2ContractOnly, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "expand-baseline", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "disabled"}, registryRev: 11, routeRevision: 5},
		{phase: ownershipregistry.PhaseS2DarkIngest, owner: ownershipregistry.OwnerLegacy, activation: "dark-ingest", compatibility: "legacy-read", rollout: ownershipregistry.RolloutPolicy{Mode: "all"}, registryRev: 12, routeRevision: 6},
		{phase: ownershipregistry.PhaseS2ShadowCompare, owner: ownershipregistry.OwnerLegacy, activation: "shadow-compare", compatibility: "legacy-read", rollout: ownershipregistry.RolloutPolicy{Mode: "all"}, registryRev: 13, routeRevision: 7},
		{phase: ownershipregistry.PhaseS2InternalCanary, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "canary", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 1, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"}, registryRev: 14, routeRevision: 8},
		{phase: ownershipregistry.PhaseS2ExternalCanary, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "canary", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 5, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"}, registryRev: 15, routeRevision: 9},
		{phase: ownershipregistry.PhaseS2Ramp25, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "canary", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 25, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"}, registryRev: 16, routeRevision: 10},
		{phase: ownershipregistry.PhaseS2Ramp50, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "canary", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 50, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"}, registryRev: 17, routeRevision: 11},
		{phase: ownershipregistry.PhaseS2Primary, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "primary", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "all"}, registryRev: 18, routeRevision: 12},
		{phase: ownershipregistry.PhaseS2LegacyRetired, owner: ownershipregistry.OwnerTelemetryRuntime, activation: "legacy-retired", compatibility: "native", rollout: ownershipregistry.RolloutPolicy{Mode: "all"}, registryRev: 19, routeRevision: 13},
	}

	manager := ownershipregistry.NewManager(mustParseS2(t, policies[0]), ownershipregistry.NewMemoryAuditSink(), fixedNow)
	invalidator := &recordingS2Invalidator{}
	for _, policy := range policies[1:] {
		result, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, policy), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}, invalidator)
		if err != nil {
			t.Fatalf("promote to %s: %v", policy.phase, err)
		}
		if result.MigrationPhase != policy.phase || result.RegistryRevision != policy.registryRev || result.RouteRevision != policy.routeRevision {
			t.Fatalf("promotion result for %s = %#v", policy.phase, result)
		}
	}

	if len(invalidator.commands) != 5 {
		t.Fatalf("R3 through R8 session invalidations = %d, want 5", len(invalidator.commands))
	}
	for _, command := range invalidator.commands {
		if command.Rollback || !command.DisconnectOrExpire || !command.FreshSnapshotRequired || command.DatabaseAction != "EXPAND_ONLY_NO_DOWN_MIGRATION" {
			t.Fatalf("promotion invalidation = %#v", command)
		}
	}
	for _, route := range s2RouteSurfaces {
		decision, err := manager.Current().Resolve(route.method, route.actual, "organization-1\x00principal-1")
		if err != nil {
			t.Fatalf("resolve R8 %s %s: %v", route.method, route.actual, err)
		}
		if decision.SelectedOwner != ownershipregistry.OwnerTelemetryRuntime || decision.MigrationPhase != ownershipregistry.PhaseS2LegacyRetired || decision.RouteRevision != 13 || decision.CohortGroup != s2CohortGroup || decision.CohortBucket != nil {
			t.Fatalf("R8 final decision = %#v", decision)
		}
	}
}

func TestS2CohortGroupRejectsRouteDrift(t *testing.T) {
	policy := s2PhasePolicy{
		phase:         ownershipregistry.PhaseS2InternalCanary,
		owner:         ownershipregistry.OwnerTelemetryRuntime,
		activation:    "canary",
		compatibility: "native",
		rollout:       ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 1, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "s2-current-state-rollout-v1"},
		registryRev:   10,
		routeRevision: 4,
	}
	registry := buildS2Registry(policy)
	registry.Routes[1].Rollout.Percentage = 5
	input, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownershipregistry.Parse(input); err == nil {
		t.Fatal("inconsistent S2 cohort group was accepted")
	}
}

func resolveS2Group(t *testing.T, snapshot *ownershipregistry.Snapshot, businessKey string) []ownershipregistry.Decision {
	t.Helper()
	decisions := make([]ownershipregistry.Decision, 0, len(s2RouteSurfaces))
	for _, route := range s2RouteSurfaces {
		decision, err := snapshot.Resolve(route.method, route.actual, businessKey)
		if err != nil {
			t.Fatalf("resolve %s %s: %v", route.method, route.actual, err)
		}
		decisions = append(decisions, decision)
	}
	first := decisions[0]
	for _, decision := range decisions[1:] {
		if decision.SelectedOwner != first.SelectedOwner || decision.CohortBucket == nil || first.CohortBucket == nil ||
			*decision.CohortBucket != *first.CohortBucket || decision.RouteRevision != first.RouteRevision || decision.CohortGroup != s2CohortGroup {
			t.Fatalf("S2 route group drifted: first=%#v next=%#v", first, decision)
		}
	}
	return decisions
}

func mustParseS2(t *testing.T, policy s2PhasePolicy) *ownershipregistry.Snapshot {
	t.Helper()
	input := mustMarshalS2(t, policy)
	snapshot, err := ownershipregistry.Parse(input)
	if err != nil {
		t.Fatalf("parse S2 phase %s: %v", policy.phase, err)
	}
	return snapshot
}

func mustMarshalS2(t *testing.T, policy s2PhasePolicy) []byte {
	t.Helper()
	input, err := json.Marshal(buildS2Registry(policy))
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func buildS2Registry(policy s2PhasePolicy) ownershipregistry.Registry {
	routes := make([]ownershipregistry.RouteEntry, 0, len(s2RouteSurfaces))
	for _, surface := range s2RouteSurfaces {
		routes = append(routes, ownershipregistry.RouteEntry{
			Method:                   surface.method,
			Path:                     surface.path,
			Owner:                    policy.owner,
			PublicIngress:            ownershipregistry.OwnerGateway,
			ActivationStatus:         policy.activation,
			Revision:                 policy.routeRevision,
			Rollout:                  policy.rollout,
			CompatibilityMode:        policy.compatibility,
			AllowedScopeDimensions:   []string{"organization", "site", "device", "principal", "key"},
			MigrationPhase:           policy.phase,
			ShadowSideEffectPolicy:   "NONE",
			ReadOnlyFallback:         false,
			FallbackForbiddenResults: []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND", "REVISION_GAP", "RECOVERY_FAILED"},
			CohortGroup:              s2CohortGroup,
		})
	}
	return ownershipregistry.Registry{RegistryVersion: 1, RegistryRevision: policy.registryRev, Routes: routes}
}
