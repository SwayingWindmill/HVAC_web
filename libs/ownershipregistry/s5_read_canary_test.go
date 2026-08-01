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

func TestS5ReadCanarySelectsGroupedWorkOrderRoutesWithoutFallback(t *testing.T) {
	snapshot := mustParse(t, s5ReadRegistryJSON(16, 2, ownershipregistry.PhaseS5InternalReadOnly))
	listPath := "/api/v1/sites/01910000-0001-7000-8000-000000000001/work-orders"
	detailPath := listPath + "/01910000-1000-7000-8000-000000000001"
	selectedKey := ""
	rejectedKey := ""
	for index := 0; index < 10000 && (selectedKey == "" || rejectedKey == ""); index++ {
		key := fmt.Sprintf("01910000-0000-7000-8000-000000000001\x00principal-%d", index)
		listDecision, listErr := snapshot.Resolve("GET", listPath, key)
		detailDecision, detailErr := snapshot.Resolve("GET", detailPath, key)
		if listErr == nil {
			if detailErr != nil || listDecision.SelectedOwner != ownershipregistry.OwnerWorkOrder || detailDecision.SelectedOwner != ownershipregistry.OwnerWorkOrder ||
				listDecision.ReadFallbackOwner != "" || detailDecision.ReadFallbackOwner != "" || listDecision.ShadowOwner != "" || detailDecision.ShadowOwner != "" ||
				listDecision.CohortBucket == nil || detailDecision.CohortBucket == nil || *listDecision.CohortBucket != *detailDecision.CohortBucket || *listDecision.CohortBucket >= 1 {
				t.Fatalf("invalid selected S5 decisions: list=%#v detail=%#v detailErr=%v", listDecision, detailDecision, detailErr)
			}
			selectedKey = key
		} else if errors.Is(listErr, ownershipregistry.ErrRouteMissing) {
			if !errors.Is(detailErr, ownershipregistry.ErrRouteMissing) {
				t.Fatalf("S5 grouped routes disagreed for rejected cohort: listErr=%v detailErr=%v", listErr, detailErr)
			}
			rejectedKey = key
		} else {
			t.Fatal(listErr)
		}
	}
	if selectedKey == "" || rejectedKey == "" {
		t.Fatalf("could not prove both S5 canary outcomes: selected=%q rejected=%q", selectedKey, rejectedKey)
	}
	first, err := snapshot.Resolve("GET", listPath, selectedKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.Resolve("GET", listPath, selectedKey)
	if err != nil || first.CohortBucket == nil || second.CohortBucket == nil || *first.CohortBucket != *second.CohortBucket {
		t.Fatalf("S5 canary decision was not stable: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestS5ReadActivationRequiresReviewedPhaseAndRevision(t *testing.T) {
	initial := mustParse(t, s5ReadRegistryJSON(15, 1, ownershipregistry.PhaseS5ContractOnly))
	manager := ownershipregistry.NewManager(initial, ownershipregistry.NewMemoryAuditSink(), func() time.Time {
		return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	})
	change := ownershipregistry.PolicyChangeContext{ExecutingService: ownershipregistry.OwnerGateway, ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway"}
	if err := manager.Reload(context.Background(), []byte(s5ReadRegistryJSON(16, 1, ownershipregistry.PhaseS5InternalReadOnly)), change); err == nil {
		t.Fatal("S5 phase activation without route revision advance was accepted")
	}
	if err := manager.Reload(context.Background(), []byte(s5ReadRegistryJSON(16, 2, ownershipregistry.PhaseS5InternalReadOnly)), change); err != nil {
		t.Fatalf("reviewed S5 read activation was rejected: %v", err)
	}
	if err := manager.Reload(context.Background(), []byte(s5ReadRegistryJSON(17, 3, ownershipregistry.PhaseS5SiteCanary)), change); err == nil {
		t.Fatal("unreviewed S5 site canary phase was accepted")
	}
}

func TestS5ReadCanaryRejectsWritesAndMismatchedGroupedPolicy(t *testing.T) {
	valid := s5ReadRegistryJSON(16, 2, ownershipregistry.PhaseS5InternalReadOnly)
	if _, err := ownershipregistry.Parse([]byte(valid)); err != nil {
		t.Fatalf("valid S5 read canary was rejected: %v", err)
	}
	write := strings.Replace(valid, "\"method\":\"GET\"", "\"method\":\"POST\"", 1)
	if _, err := ownershipregistry.Parse([]byte(write)); err == nil {
		t.Fatal("S5 write route entered the read canary")
	}
	mismatched := strings.Replace(valid,
		"\"path\":\"/api/v1/sites/{siteId}/work-orders/{workOrderId}\",\"owner\":\"work-order-service\",\"publicIngress\":\"platform-gateway\",\"activationStatus\":\"internal-canary\",\"revision\":2",
		"\"path\":\"/api/v1/sites/{siteId}/work-orders/{workOrderId}\",\"owner\":\"work-order-service\",\"publicIngress\":\"platform-gateway\",\"activationStatus\":\"internal-canary\",\"revision\":3", 1)
	if _, err := ownershipregistry.Parse([]byte(mismatched)); err == nil {
		t.Fatal("S5 list and detail canary routes accepted mismatched revisions")
	}
}

func s5ReadRegistryJSON(registryRevision, routeRevision int64, phase string) string {
	activation := "expand-baseline"
	rollout := "{\"mode\":\"disabled\"}"
	switch phase {
	case ownershipregistry.PhaseS5InternalReadOnly:
		activation = "internal-canary"
		rollout = "{\"mode\":\"percentage\",\"percentage\":1,\"cohortSalt\":\"s5-work-order-read-canary-v1\"}"
	case ownershipregistry.PhaseS5SiteCanary:
		activation = "site-canary"
		rollout = "{\"mode\":\"percentage\",\"percentage\":5,\"cohortSalt\":\"s5-work-order-read-canary-v1\"}"
	}
	format := "{\"registryVersion\":1,\"registryRevision\":%d,\"routes\":[" +
		"{\"method\":\"GET\",\"path\":\"/api/v1/sites/{siteId}/work-orders\",\"owner\":\"work-order-service\",\"publicIngress\":\"platform-gateway\",\"activationStatus\":%q,\"revision\":%d,\"rollout\":%s,\"compatibilityMode\":\"native\",\"allowedScopeDimensions\":[\"organization\",\"site\",\"principal\"],\"migrationPhase\":%q,\"cohortGroup\":\"s5-work-order-read-v1\",\"shadowSideEffectPolicy\":\"NONE\",\"readOnlyFallback\":false,\"fallbackForbiddenResults\":[\"AUTHORIZATION_DENIED\",\"RESOURCE_NOT_FOUND\"]}," +
		"{\"method\":\"GET\",\"path\":\"/api/v1/sites/{siteId}/work-orders/{workOrderId}\",\"owner\":\"work-order-service\",\"publicIngress\":\"platform-gateway\",\"activationStatus\":%q,\"revision\":%d,\"rollout\":%s,\"compatibilityMode\":\"native\",\"allowedScopeDimensions\":[\"organization\",\"site\",\"principal\",\"work-order\"],\"migrationPhase\":%q,\"cohortGroup\":\"s5-work-order-read-v1\",\"shadowSideEffectPolicy\":\"NONE\",\"readOnlyFallback\":false,\"fallbackForbiddenResults\":[\"AUTHORIZATION_DENIED\",\"RESOURCE_NOT_FOUND\"]}]}"
	return fmt.Sprintf(format, registryRevision, activation, routeRevision, rollout, phase, activation, routeRevision, rollout, phase)
}
