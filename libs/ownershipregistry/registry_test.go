package ownershipregistry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

func TestResolveUsesStableServerDerivedCohort(t *testing.T) {
	snapshot := mustParse(t, registryJSON(1, 1, ownershipregistry.OwnerLegacy, 50))
	businessKey := "org-01\x00fixture-user"
	first, err := snapshot.Resolve("GET", "/api/v1/platform/status", businessKey)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		next, err := snapshot.Resolve("GET", "/api/v1/platform/status", businessKey)
		if err != nil {
			t.Fatal(err)
		}
		if next.SelectedOwner != first.SelectedOwner || next.CohortBucket == nil || *next.CohortBucket != *first.CohortBucket {
			t.Fatalf("cohort decision changed: first=%#v next=%#v", first, next)
		}
	}
	if _, err := snapshot.Resolve("GET", "/api/v1/platform/status", ""); !errors.Is(err, ownershipregistry.ErrCohortKey) {
		t.Fatalf("missing server business key error = %v", err)
	}
}

func TestRegistryReloadRequiresAdjacentMigrationPhaseAndPreservesRoutes(t *testing.T) {
	initial := mustParse(t, phaseRegistryJSON(3, 2, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100))
	audit := ownershipregistry.NewMemoryAuditSink()
	manager := ownershipregistry.NewManager(initial, audit, time.Now)
	changeContext := ownershipregistry.PolicyChangeContext{ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway"}
	if err := manager.Reload(context.Background(), []byte(phaseRegistryJSON(5, 4, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0)), changeContext); err == nil {
		t.Fatal("phase skip was accepted")
	}
	if manager.Current().RegistryRevision() != 3 {
		t.Fatal("rejected phase changed the active snapshot")
	}
	if err := manager.Reload(context.Background(), []byte(phaseRegistryJSON(4, 3, ownershipregistry.PhaseGoCanaryLegacyShadow, 50)), changeContext); err != nil {
		t.Fatalf("adjacent phase was rejected: %v", err)
	}
	if err := manager.Reload(context.Background(), []byte(phaseRegistryJSON(5, 4, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100)), changeContext); err != nil {
		t.Fatalf("adjacent rollback with monotonic revisions was rejected: %v", err)
	}
	removed := `{"registryVersion":1,"registryRevision":6,"routes":[{"method":"GET","path":"/api/v1/health","owner":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":[]}]}`
	if err := manager.Reload(context.Background(), []byte(removed), changeContext); err == nil {
		t.Fatal("route removal was accepted")
	}
}

func TestRegistryResolvesS1MigrationPhases(t *testing.T) {
	phaseOne := mustParse(t, phaseRegistryJSON(1, 1, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100))
	decision, err := phaseOne.Resolve("GET", "/api/v1/organizations", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerLegacy || decision.ShadowOwner != ownershipregistry.OwnerCore || decision.ReadFallbackOwner != "" {
		t.Fatalf("phase one decision = %#v", decision)
	}

	phaseTwo := mustParse(t, phaseRegistryJSON(2, 2, ownershipregistry.PhaseGoCanaryLegacyShadow, 50))
	decision, err = phaseTwo.Resolve("GET", "/api/v1/organizations", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner == decision.ShadowOwner || decision.ShadowOwner == "" {
		t.Fatalf("phase two did not choose opposite primary/shadow owners: %#v", decision)
	}

	phaseThree := mustParse(t, phaseRegistryJSON(3, 3, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0))
	decision, err = phaseThree.Resolve("GET", "/api/v1/organizations", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore || decision.ReadFallbackOwner != ownershipregistry.OwnerLegacy || decision.ShadowOwner != "" {
		t.Fatalf("phase three decision = %#v", decision)
	}

	phaseFour := mustParse(t, phaseRegistryJSON(4, 4, ownershipregistry.PhaseGoPrimary, 0))
	decision, err = phaseFour.Resolve("GET", "/api/v1/organizations", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore || decision.ReadFallbackOwner != "" || decision.ShadowOwner != "" {
		t.Fatalf("phase four decision = %#v", decision)
	}
}

func TestRegistryRejectsMissingConflictingAndRegressedOwnership(t *testing.T) {
	snapshot := mustParse(t, registryJSON(1, 1, ownershipregistry.OwnerLegacy, 100))
	if _, err := snapshot.Resolve("GET", "/api/v1/unknown", "org\x00user"); !errors.Is(err, ownershipregistry.ErrRouteMissing) {
		t.Fatalf("missing route error = %v", err)
	}
	conflict := `{"registryVersion":1,"registryRevision":1,"routes":[` +
		routeJSON(1, ownershipregistry.OwnerGateway, 100) + `,` +
		`{"method":"GET","path":"/api/v1/platform/{value}","owner":"platform-gateway","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":[]}]}`
	if _, err := ownershipregistry.Parse([]byte(conflict)); !errors.Is(err, ownershipregistry.ErrRouteConflict) {
		t.Fatalf("conflicting registry error = %v", err)
	}

	audit := ownershipregistry.NewMemoryAuditSink()
	manager := ownershipregistry.NewManager(snapshot, audit, fixedNow)
	if err := manager.Reload(context.Background(), []byte(registryJSON(2, 1, ownershipregistry.OwnerGateway, 0)), ownershipregistry.PolicyChangeContext{}); err == nil {
		t.Fatal("owner change without route revision advance was accepted")
	}
	if manager.Current().RegistryRevision() != 1 {
		t.Fatal("failed reload changed active snapshot")
	}
}

func TestDisabledS2BaselineLoadsButIsNotDiscoverable(t *testing.T) {
	input := `{"registryVersion":1,"registryRevision":7,"routes":[{"method":"GET","path":"/api/v1/devices/{deviceId}/observation-snapshot","owner":"telemetry-runtime-service","publicIngress":"platform-gateway","activationStatus":"expand-baseline","revision":1,"rollout":{"mode":"disabled"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","device","principal","key"],"migrationPhase":"R0-contract-only","cohortGroup":"s2-current-state-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","REVISION_GAP","RECOVERY_FAILED"]}]}`
	snapshot := mustParse(t, input)
	if !snapshot.ContainsOwner(ownershipregistry.OwnerTelemetryRuntime) {
		t.Fatal("Telemetry Runtime owner was not retained in the parsed baseline")
	}
	if _, err := snapshot.Resolve("GET", "/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot", "org\x00user"); !errors.Is(err, ownershipregistry.ErrRouteMissing) {
		t.Fatalf("disabled S2 route became resolvable: %v", err)
	}
	if methods := snapshot.AllowedMethods("/api/v1/devices/018f2e00-3000-7000-8000-000000000001/observation-snapshot"); len(methods) != 0 {
		t.Fatalf("disabled S2 route leaked allowed methods: %v", methods)
	}

	active := `{"registryVersion":1,"registryRevision":7,"routes":[{"method":"GET","path":"/api/v1/devices/{deviceId}/observation-snapshot","owner":"telemetry-runtime-service","publicIngress":"platform-gateway","activationStatus":"expand-baseline","revision":1,"rollout":{"mode":"all"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","device","principal","key"],"migrationPhase":"R0-contract-only","cohortGroup":"s2-current-state-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","REVISION_GAP","RECOVERY_FAILED"]}]}`
	if _, err := ownershipregistry.Parse([]byte(active)); err == nil {
		t.Fatal("S2 contract-only route accepted active traffic")
	}
}

func TestDisabledS3BaselineLoadsButIsNotDiscoverable(t *testing.T) {
	input := `{"registryVersion":1,"registryRevision":10,"routes":[{"method":"POST","path":"/api/v1/commands","owner":"command-service","publicIngress":"platform-gateway","activationStatus":"expand-baseline","revision":1,"rollout":{"mode":"disabled"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","device","principal"],"migrationPhase":"S3-R0-contract-only","cohortGroup":"s3-command-v1","shadowSideEffectPolicy":"SYNTHETIC_ONLY","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","CURRENT_STATE_UNSAFE","OUTCOME_UNKNOWN"]}]}`
	snapshot := mustParse(t, input)
	if !snapshot.ContainsOwner(ownershipregistry.OwnerCommand) {
		t.Fatal("Command owner was not retained in the parsed baseline")
	}
	if _, err := snapshot.Resolve("POST", "/api/v1/commands", "org\x00user"); !errors.Is(err, ownershipregistry.ErrRouteMissing) {
		t.Fatalf("disabled S3 route became resolvable: %v", err)
	}
	if methods := snapshot.AllowedMethods("/api/v1/commands"); len(methods) != 0 {
		t.Fatalf("disabled S3 route leaked allowed methods: %v", methods)
	}
}

func TestPolicyRollbackOnlyChangesFutureDecisions(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	manager := ownershipregistry.NewManager(mustParse(t, registryJSON(1, 1, ownershipregistry.OwnerLegacy, 100)), audit, fixedNow)
	before, err := manager.Current().Resolve("GET", "/api/v1/platform/status", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if before.SelectedOwner != ownershipregistry.OwnerLegacy {
		t.Fatalf("initial owner = %q", before.SelectedOwner)
	}
	if err := manager.Reload(context.Background(), []byte(registryJSON(2, 2, ownershipregistry.OwnerGateway, 100)), ownershipregistry.PolicyChangeContext{ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway"}); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Current().Resolve("GET", "/api/v1/platform/status", "org-01\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	if before.SelectedOwner != ownershipregistry.OwnerLegacy || after.SelectedOwner != ownershipregistry.OwnerGateway {
		t.Fatalf("rollback decisions before=%q after=%q", before.SelectedOwner, after.SelectedOwner)
	}
	if len(audit.Records()) != 1 || audit.Records()[0].EventType != "ROUTE_POLICY_CHANGED" {
		t.Fatalf("policy audit records = %#v", audit.Records())
	}
}

func TestPolicyAuditFailureFailsClosed(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	audit.SetFailure(errors.New("seeded audit failure"))
	manager := ownershipregistry.NewManager(mustParse(t, registryJSON(1, 1, ownershipregistry.OwnerLegacy, 100)), audit, fixedNow)
	if err := manager.Reload(context.Background(), []byte(registryJSON(2, 2, ownershipregistry.OwnerGateway, 100)), ownershipregistry.PolicyChangeContext{}); err == nil {
		t.Fatal("policy reload succeeded without durable audit")
	}
	decision, err := manager.Current().Resolve("GET", "/api/v1/platform/status", "org\x00user")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerLegacy {
		t.Fatalf("audit failure changed active owner to %q", decision.SelectedOwner)
	}
}

func registryJSON(registryRevision, routeRevision int64, owner string, percentage int) string {
	return fmt.Sprintf(`{"registryVersion":1,"registryRevision":%d,"routes":[%s]}`, registryRevision, routeJSON(routeRevision, owner, percentage))
}

func phaseRegistryJSON(registryRevision, routeRevision int64, phase string, percentage int) string {
	owner := ownershipregistry.OwnerCore
	compatibility := "native"
	rollout := `{"mode":"all"}`
	readFallback := ""
	readOnlyFallback := true
	switch phase {
	case ownershipregistry.PhaseLegacyPrimaryGoShadow:
		owner = ownershipregistry.OwnerLegacy
		compatibility = "legacy-read"
		rollout = `{"mode":"percentage","percentage":100,"fallbackOwner":"platform-core-service","cohortSalt":"s1-organizations-v1"}`
	case ownershipregistry.PhaseGoCanaryLegacyShadow:
		rollout = fmt.Sprintf(`{"mode":"percentage","percentage":%d,"fallbackOwner":"legacy-hvac-backend","cohortSalt":"s1-organizations-v1"}`, percentage)
	case ownershipregistry.PhaseGoPrimaryLegacyReadFallback:
		readFallback = `,"readFallbackOwner":"legacy-hvac-backend"`
	case ownershipregistry.PhaseGoPrimary:
		readOnlyFallback = false
	}
	return fmt.Sprintf(`{"registryVersion":1,"registryRevision":%d,"routes":[{"method":"GET","path":"/api/v1/organizations","owner":%q,"revision":%d,"rollout":%s,"compatibilityMode":%q,"allowedScopeDimensions":["organization","principal"],"migrationPhase":%q,"shadowSideEffectPolicy":"NONE","readOnlyFallback":%t%s,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"]}]}`, registryRevision, owner, routeRevision, rollout, compatibility, phase, readOnlyFallback, readFallback)
}

func routeJSON(routeRevision int64, owner string, percentage int) string {
	fallback := ownershipregistry.OwnerGateway
	compatibility := "legacy-read"
	if owner == ownershipregistry.OwnerGateway {
		fallback = ownershipregistry.OwnerLegacy
		compatibility = "native"
	}
	return fmt.Sprintf(`{"method":"GET","path":"/api/v1/platform/status","owner":%q,"revision":%d,"rollout":{"mode":"percentage","percentage":%d,"fallbackOwner":%q,"cohortSalt":"status-salt-v1"},"compatibilityMode":%q,"allowedScopeDimensions":["organization","principal"]}`, owner, routeRevision, percentage, fallback, compatibility)
}

func mustParse(t *testing.T, value string) *ownershipregistry.Snapshot {
	t.Helper()
	snapshot, err := ownershipregistry.Parse([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
}
