package ownershipregistry_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

func currentRoute(method, path, owner string, scopes ...string) ownershipregistry.RouteEntry {
	return ownershipregistry.RouteEntry{
		Method:                 method,
		Path:                   path,
		Owner:                  owner,
		PublicIngress:          ownershipregistry.OwnerGateway,
		Revision:               1,
		Rollout:                ownershipregistry.RolloutPolicy{Mode: "all"},
		CompatibilityMode:      "native",
		AllowedScopeDimensions: append([]string(nil), scopes...),
	}
}

func registryBytes(t *testing.T, revision int64, routes ...ownershipregistry.RouteEntry) []byte {
	t.Helper()
	value, err := json.Marshal(ownershipregistry.Registry{RegistryVersion: 1, RegistryRevision: revision, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustParseRegistry(t *testing.T, revision int64, routes ...ownershipregistry.RouteEntry) *ownershipregistry.Snapshot {
	t.Helper()
	snapshot, err := ownershipregistry.Parse(registryBytes(t, revision, routes...))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestResolveUsesExactlyOneDeclaredOwner(t *testing.T) {
	snapshot := mustParseRegistry(t, 7, currentRoute("GET", "/api/v1/assets/{assetId}", ownershipregistry.OwnerCore, "tenant", "site", "asset", "principal"))
	decision, err := snapshot.Resolve("GET", "/api/v1/assets/0198a36e-4c9d-7b5a-8f2d-4c5e6f708194", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedOwner != ownershipregistry.OwnerCore || decision.RegistryRevision != 7 || decision.RouteRevision != 1 {
		t.Fatalf("unexpected ownership decision: %+v", decision)
	}
}

func TestParseRejectsMigrationEraFields(t *testing.T) {
	input := `{"registryVersion":1,"registryRevision":1,"routes":[{"method":"GET","path":"/api/v1/sites","owner":"platform-core-service","publicIngress":"platform-gateway","revision":1,"rollout":{"mode":"all","percentage":1},"compatibilityMode":"native","allowedScopeDimensions":["tenant","principal"],"migrationPhase":"GO_PRIMARY"}]}`
	if _, err := ownershipregistry.Parse([]byte(input)); err == nil {
		t.Fatal("migration-era route metadata was accepted")
	}
}

func TestParseRejectsLegacyOwnerAndOrganizationScope(t *testing.T) {
	legacy := currentRoute("GET", "/api/v1/sites", "legacy-hvac-backend", "tenant", "principal")
	if _, err := ownershipregistry.Parse(registryBytes(t, 1, legacy)); err == nil {
		t.Fatal("Legacy owner was accepted")
	}
	organization := currentRoute("GET", "/api/v1/sites", ownershipregistry.OwnerCore, "tenant", "organization", "principal")
	if _, err := ownershipregistry.Parse(registryBytes(t, 1, organization)); err == nil {
		t.Fatal("Organization scope was accepted")
	}
}

func TestDisabledRouteIsNotDiscoverable(t *testing.T) {
	route := currentRoute("GET", "/api/v1/sites/{siteId}/alarms", ownershipregistry.OwnerAlarm, "tenant", "site", "principal")
	route.Rollout.Mode = "disabled"
	snapshot := mustParseRegistry(t, 1, route)
	if _, err := snapshot.Resolve("GET", "/api/v1/sites/site-1/alarms", ""); !errors.Is(err, ownershipregistry.ErrRouteMissing) {
		t.Fatalf("disabled route resolved: %v", err)
	}
	if methods := snapshot.AllowedMethods("/api/v1/sites/site-1/alarms"); len(methods) != 0 {
		t.Fatalf("disabled route leaked methods: %v", methods)
	}
}

func TestReloadAllowsRouteRemovalAndRequiresRegistryRevisionAdvance(t *testing.T) {
	keep := currentRoute("GET", "/api/v1/sites", ownershipregistry.OwnerCore, "tenant", "principal")
	obsolete := currentRoute("GET", "/api/v1/obsolete", ownershipregistry.OwnerCore, "tenant", "principal")
	manager := ownershipregistry.NewManager(mustParseRegistry(t, 1, keep, obsolete), ownershipregistry.NewMemoryAuditSink(), func() time.Time { return time.Unix(10, 0).UTC() })
	if err := manager.Reload(context.Background(), registryBytes(t, 2, keep), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}); err != nil {
		t.Fatalf("obsolete route removal failed: %v", err)
	}
	if manager.Current().RegistryRevision() != 2 {
		t.Fatal("new registry revision was not installed")
	}
	if err := manager.Reload(context.Background(), registryBytes(t, 2, keep), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}); err == nil {
		t.Fatal("non-advancing registry revision was accepted")
	}
}

func TestReloadRequiresRouteRevisionForPolicyChange(t *testing.T) {
	initial := currentRoute("GET", "/api/v1/sites", ownershipregistry.OwnerCore, "tenant", "principal")
	manager := ownershipregistry.NewManager(mustParseRegistry(t, 1, initial), ownershipregistry.NewMemoryAuditSink(), time.Now)
	changed := initial
	changed.AllowedScopeDimensions = []string{"tenant", "site", "principal"}
	if err := manager.Reload(context.Background(), registryBytes(t, 2, changed), ownershipregistry.PolicyChangeContext{}); err == nil || !strings.Contains(err.Error(), "route policy changed without revision advance") {
		t.Fatalf("policy change without route revision was not rejected: %v", err)
	}
}

func TestReloadKeepsCurrentSnapshotWhenAuditCannotPersist(t *testing.T) {
	initial := mustParseRegistry(t, 1, currentRoute("GET", "/api/v1/sites", ownershipregistry.OwnerCore, "tenant", "principal"))
	audit := ownershipregistry.NewMemoryAuditSink()
	audit.SetFailure(errors.New("audit unavailable"))
	manager := ownershipregistry.NewManager(initial, audit, func() time.Time { return time.Unix(10, 0).UTC() })
	nextRoute := currentRoute("GET", "/api/v1/sites", ownershipregistry.OwnerCore, "tenant", "principal")
	nextRoute.Revision = 2
	if err := manager.Reload(context.Background(), registryBytes(t, 2, nextRoute), ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway}); err == nil {
		t.Fatal("reload succeeded without durable audit")
	}
	if manager.Current().RegistryRevision() != 1 {
		t.Fatal("failed reload replaced the authoritative snapshot")
	}
}
