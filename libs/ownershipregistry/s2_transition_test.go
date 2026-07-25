package ownershipregistry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

type recordingS2Invalidator struct {
	commands []ownershipregistry.S2SessionInvalidation
	err      error
}

func (invalidator *recordingS2Invalidator) InvalidateS2Sessions(_ context.Context, command ownershipregistry.S2SessionInvalidation) error {
	invalidator.commands = append(invalidator.commands, command)
	return invalidator.err
}

func TestReloadS2RequiresSuccessfulInvalidationForLiveRollback(t *testing.T) {
	r3 := s2PhasePolicy{
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
	r2 := s2PhasePolicy{
		phase:         ownershipregistry.PhaseS2ShadowCompare,
		owner:         ownershipregistry.OwnerLegacy,
		activation:    "shadow-compare",
		compatibility: "legacy-read",
		rollout:       ownershipregistry.RolloutPolicy{Mode: "all"},
		registryRev:   11,
		routeRevision: 5,
	}
	manager := ownershipregistry.NewManager(mustParseS2(t, r3), ownershipregistry.NewMemoryAuditSink(), fixedNow)

	if err := manager.Reload(context.Background(), mustMarshalS2(t, r2), ownershipregistry.PolicyChangeContext{}); err == nil {
		t.Fatal("ordinary Reload bypassed required S2 session invalidation")
	}
	if manager.Current().RegistryRevision() != r3.registryRev {
		t.Fatal("failed ordinary reload changed the active S2 policy")
	}

	if _, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, r2), ownershipregistry.PolicyChangeContext{}, nil); err == nil {
		t.Fatal("live rollback without invalidator was accepted")
	}
	if manager.Current().RegistryRevision() != r3.registryRev {
		t.Fatal("missing invalidator changed the active S2 policy")
	}

	failing := &recordingS2Invalidator{err: errors.New("transport unavailable")}
	if _, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, r2), ownershipregistry.PolicyChangeContext{}, failing); err == nil {
		t.Fatal("failed session invalidation still changed S2 route ownership")
	}
	if manager.Current().RegistryRevision() != r3.registryRev || len(failing.commands) != 1 {
		t.Fatalf("failed invalidation state revision=%d commands=%d", manager.Current().RegistryRevision(), len(failing.commands))
	}

	successful := &recordingS2Invalidator{}
	result, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, r2), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}, successful)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Current().RegistryRevision() != r2.registryRev || result.RegistryRevision != r2.registryRev || result.RouteRevision != r2.routeRevision || result.MigrationPhase != r2.phase {
		t.Fatalf("successful rollback result=%#v active=%d", result, manager.Current().RegistryRevision())
	}
	if len(successful.commands) != 1 || result.Invalidation == nil {
		t.Fatalf("successful rollback commands=%d result=%#v", len(successful.commands), result)
	}
	command := successful.commands[0]
	if !command.Rollback || !command.DisconnectOrExpire || !command.FreshSnapshotRequired || command.DatabaseAction != "EXPAND_ONLY_NO_DOWN_MIGRATION" ||
		command.PreviousRouteRevision != r3.routeRevision || command.NextRouteRevision != r2.routeRevision || command.PreviousOwner != ownershipregistry.OwnerTelemetryRuntime || command.NextOwner != ownershipregistry.OwnerLegacy {
		t.Fatalf("rollback invalidation command=%#v", command)
	}
}

func TestReloadS2PromotionFromShadowDoesNotInvalidateInactiveLiveSessions(t *testing.T) {
	r2 := s2PhasePolicy{
		phase:         ownershipregistry.PhaseS2ShadowCompare,
		owner:         ownershipregistry.OwnerLegacy,
		activation:    "shadow-compare",
		compatibility: "legacy-read",
		rollout:       ownershipregistry.RolloutPolicy{Mode: "all"},
		registryRev:   9,
		routeRevision: 3,
	}
	r3 := s2PhasePolicy{
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
	manager := ownershipregistry.NewManager(mustParseS2(t, r2), ownershipregistry.NewMemoryAuditSink(), fixedNow)
	invalidator := &recordingS2Invalidator{}
	result, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, r3), ownershipregistry.PolicyChangeContext{}, invalidator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalidation != nil || len(invalidator.commands) != 0 {
		t.Fatalf("R2 to R3 invalidated sessions before any S2 live cohort existed: %#v", result)
	}
}

func TestReloadS2LivePromotionRequiresInvalidation(t *testing.T) {
	r3 := s2PhasePolicy{
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
	r4 := r3
	r4.phase = ownershipregistry.PhaseS2ExternalCanary
	r4.rollout.Percentage = 5
	r4.registryRev = 11
	r4.routeRevision = 5
	manager := ownershipregistry.NewManager(mustParseS2(t, r3), ownershipregistry.NewMemoryAuditSink(), fixedNow)
	invalidator := &recordingS2Invalidator{}
	result, err := manager.ReloadS2(context.Background(), mustMarshalS2(t, r4), ownershipregistry.PolicyChangeContext{}, invalidator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Invalidation == nil || len(invalidator.commands) != 1 || invalidator.commands[0].Rollback {
		t.Fatalf("R3 to R4 invalidation=%#v commands=%#v", result.Invalidation, invalidator.commands)
	}
}
