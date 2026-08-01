package ownershipregistry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

func TestS4ReadCanarySelectsAlarmWithoutFallback(t *testing.T) {
	snapshot := mustParse(t, s4ReadRegistryJSON(14, 2, ownershipregistry.PhaseS4InternalReadOnly))
	path := "/api/v1/sites/01910000-0001-7000-8000-000000000001/alarms"
	selectedKey := ""
	rejectedKey := ""
	for index := 0; index < 10000 && (selectedKey == "" || rejectedKey == ""); index++ {
		key := fmt.Sprintf("01910000-0000-7000-8000-000000000001\x00principal-%d", index)
		decision, err := snapshot.Resolve("GET", path, key)
		if err == nil {
			if decision.SelectedOwner != ownershipregistry.OwnerAlarm || decision.ReadFallbackOwner != "" || decision.CohortBucket == nil || *decision.CohortBucket >= 1 {
				t.Fatalf("invalid selected S4 decision: %#v", decision)
			}
			selectedKey = key
		} else if errors.Is(err, ownershipregistry.ErrRouteMissing) {
			rejectedKey = key
		} else {
			t.Fatal(err)
		}
	}
	if selectedKey == "" || rejectedKey == "" {
		t.Fatalf("could not prove both S4 canary outcomes: selected=%q rejected=%q", selectedKey, rejectedKey)
	}
	first, err := snapshot.Resolve("GET", path, selectedKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.Resolve("GET", path, selectedKey)
	if err != nil || second.CohortBucket == nil || first.CohortBucket == nil || *second.CohortBucket != *first.CohortBucket {
		t.Fatalf("S4 canary decision was not stable: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestS4ReadActivationRequiresAdjacentPhaseAndRevision(t *testing.T) {
	initial := mustParse(t, s4ReadRegistryJSON(13, 1, ownershipregistry.PhaseS4ContractOnly))
	manager := ownershipregistry.NewManager(initial, ownershipregistry.NewMemoryAuditSink(), func() time.Time {
		return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	})
	change := ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway, ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway"}
	if err := manager.Reload(context.Background(), []byte(s4ReadRegistryJSON(14, 1, ownershipregistry.PhaseS4InternalReadOnly)), change); err == nil {
		t.Fatal("S4 phase activation without route revision advance was accepted")
	}
	if err := manager.Reload(context.Background(), []byte(s4ReadRegistryJSON(14, 2, ownershipregistry.PhaseS4InternalReadOnly)), change); err != nil {
		t.Fatalf("adjacent S4 read activation was rejected: %v", err)
	}
}

func TestS4LifecycleWriteCannotEnterReadCanaryPhase(t *testing.T) {
	input := `{"registryVersion":1,"registryRevision":14,"routes":[{"method":"POST","path":"/api/v1/sites/{siteId}/alarms/{alarmId}:acknowledge","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"internal-canary","revision":2,"rollout":{"mode":"percentage","percentage":1,"cohortSalt":"s4-alarm-read-canary-v1"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","alarm","principal","key"],"migrationPhase":"S4-R1-internal-read-only","cohortGroup":"s4-alarm-lifecycle-v1","shadowSideEffectPolicy":"SYNTHETIC_ONLY","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","VERSION_CONFLICT","IDEMPOTENCY_CONFLICT"]}]}`
	if _, err := ownershipregistry.Parse([]byte(input)); err == nil {
		t.Fatal("S4 lifecycle write entered the read-only canary phase")
	}
}

func s4ReadRegistryJSON(registryRevision, routeRevision int64, phase string) string {
	activation := "expand-baseline"
	rollout := `{"mode":"disabled"}`
	if phase == ownershipregistry.PhaseS4InternalReadOnly {
		activation = "internal-canary"
		rollout = `{"mode":"percentage","percentage":1,"cohortSalt":"s4-alarm-read-canary-v1"}`
	}
	return fmt.Sprintf(`{"registryVersion":1,"registryRevision":%d,"routes":[{"method":"GET","path":"/api/v1/sites/{siteId}/alarms","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":%q,"revision":%d,"rollout":%s,"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal"],"migrationPhase":%q,"cohortGroup":"s4-alarm-read-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"]}]}`, registryRevision, activation, routeRevision, rollout, phase)
}
