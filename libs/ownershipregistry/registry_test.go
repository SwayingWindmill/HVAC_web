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
