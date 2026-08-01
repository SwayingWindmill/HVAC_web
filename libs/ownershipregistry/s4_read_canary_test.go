package ownershipregistry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	if err := manager.Reload(context.Background(), []byte(s4ReadRegistryJSON(15, 2, ownershipregistry.PhaseS4SiteCanary)), change); err == nil {
		t.Fatal("S4 site canary promotion without route revision advance was accepted")
	}
	if err := manager.Reload(context.Background(), []byte(s4ReadRegistryJSON(15, 3, ownershipregistry.PhaseS4SiteCanary)), change); err != nil {
		t.Fatalf("adjacent S4 site canary promotion was rejected: %v", err)
	}
}

func TestS4ReadSiteCanaryMonotonicallyExpandsTheStableCohort(t *testing.T) {
	source := mustParse(t, s4ReadRegistryJSON(14, 2, ownershipregistry.PhaseS4InternalReadOnly))
	target := mustParse(t, s4ReadRegistryJSON(15, 3, ownershipregistry.PhaseS4SiteCanary))
	path := "/api/v1/sites/01910000-0001-7000-8000-000000000001/alarms"
	sourceSelected := 0
	targetOnlySelected := 0
	for index := 0; index < 10000; index++ {
		key := fmt.Sprintf("01910000-0000-7000-8000-000000000001\x00principal-%d", index)
		sourceDecision, sourceErr := source.Resolve("GET", path, key)
		targetDecision, targetErr := target.Resolve("GET", path, key)
		if sourceErr == nil {
			sourceSelected++
			if targetErr != nil || sourceDecision.CohortBucket == nil || targetDecision.CohortBucket == nil || *sourceDecision.CohortBucket != *targetDecision.CohortBucket {
				t.Fatalf("source cohort member was not preserved by the 5%% expansion: source=%#v target=%#v err=%v", sourceDecision, targetDecision, targetErr)
			}
		}
		if errors.Is(sourceErr, ownershipregistry.ErrRouteMissing) && targetErr == nil {
			targetOnlySelected++
		}
	}
	if sourceSelected == 0 || targetOnlySelected == 0 {
		t.Fatalf("cohort expansion was not observable: source=%d target-only=%d", sourceSelected, targetOnlySelected)
	}
}

func TestS4LifecycleWriteCannotEnterReadCanaryPhase(t *testing.T) {
	for _, input := range []string{
		`{"registryVersion":1,"registryRevision":14,"routes":[{"method":"POST","path":"/api/v1/sites/{siteId}/alarms/{alarmId}:acknowledge","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"internal-canary","revision":2,"rollout":{"mode":"percentage","percentage":1,"cohortSalt":"s4-alarm-read-canary-v1"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","alarm","principal","key"],"migrationPhase":"S4-R1-internal-read-only","cohortGroup":"s4-alarm-lifecycle-v1","shadowSideEffectPolicy":"SYNTHETIC_ONLY","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","VERSION_CONFLICT","IDEMPOTENCY_CONFLICT"]}]}`,
		`{"registryVersion":1,"registryRevision":15,"routes":[{"method":"POST","path":"/api/v1/sites/{siteId}/alarms/{alarmId}:acknowledge","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"site-canary","revision":3,"rollout":{"mode":"percentage","percentage":5,"cohortSalt":"s4-alarm-read-canary-v1"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","alarm","principal","key"],"migrationPhase":"S4-R2-site-canary","cohortGroup":"s4-alarm-lifecycle-v1","shadowSideEffectPolicy":"SYNTHETIC_ONLY","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND","VERSION_CONFLICT","IDEMPOTENCY_CONFLICT"]}]}`,
	} {
		if _, err := ownershipregistry.Parse([]byte(input)); err == nil {
			t.Fatal("S4 lifecycle write entered an Alarm read canary phase")
		}
	}
}

func TestS4ReadSiteCanaryRequiresGroupedListAndDetailPolicy(t *testing.T) {
	valid := `{"registryVersion":1,"registryRevision":15,"routes":[{"method":"GET","path":"/api/v1/sites/{siteId}/alarms","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"site-canary","revision":3,"rollout":{"mode":"percentage","percentage":5,"cohortSalt":"s4-alarm-read-canary-v1"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal"],"migrationPhase":"S4-R2-site-canary","cohortGroup":"s4-alarm-read-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"]},{"method":"GET","path":"/api/v1/sites/{siteId}/alarms/{alarmId}","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"site-canary","revision":3,"rollout":{"mode":"percentage","percentage":5,"cohortSalt":"s4-alarm-read-canary-v1"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal","alarm"],"migrationPhase":"S4-R2-site-canary","cohortGroup":"s4-alarm-read-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"]}]}`
	if _, err := ownershipregistry.Parse([]byte(valid)); err != nil {
		t.Fatalf("grouped S4 site canary was rejected: %v", err)
	}
	mismatched := strings.Replace(valid, `"path":"/api/v1/sites/{siteId}/alarms/{alarmId}","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"site-canary","revision":3`, `"path":"/api/v1/sites/{siteId}/alarms/{alarmId}","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":"site-canary","revision":4`, 1)
	if _, err := ownershipregistry.Parse([]byte(mismatched)); err == nil {
		t.Fatal("S4 list and detail site canary routes accepted mismatched revisions")
	}
}

func s4ReadRegistryJSON(registryRevision, routeRevision int64, phase string) string {
	activation := "expand-baseline"
	rollout := `{"mode":"disabled"}`
	switch phase {
	case ownershipregistry.PhaseS4InternalReadOnly:
		activation = "internal-canary"
		rollout = `{"mode":"percentage","percentage":1,"cohortSalt":"s4-alarm-read-canary-v1"}`
	case ownershipregistry.PhaseS4SiteCanary:
		activation = "site-canary"
		rollout = `{"mode":"percentage","percentage":5,"cohortSalt":"s4-alarm-read-canary-v1"}`
	}
	return fmt.Sprintf(`{"registryVersion":1,"registryRevision":%d,"routes":[{"method":"GET","path":"/api/v1/sites/{siteId}/alarms","owner":"alarm-service","publicIngress":"platform-gateway","activationStatus":%q,"revision":%d,"rollout":%s,"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal"],"migrationPhase":%q,"cohortGroup":"s4-alarm-read-v1","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"]}]}`, registryRevision, activation, routeRevision, rollout, phase)
}
