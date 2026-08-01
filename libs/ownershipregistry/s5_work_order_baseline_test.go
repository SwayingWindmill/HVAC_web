package ownershipregistry_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

const s5WorkOrderRegistry = `{"registryVersion":1,"registryRevision":15,"routes":[{"method":"GET","path":"/api/v1/sites/{siteId}/work-orders","owner":"work-order-service","publicIngress":"platform-gateway","activationStatus":"expand-baseline","revision":1,"rollout":{"mode":"disabled"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal"],"migrationPhase":"S5-R0-contract-only","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"],"cohortGroup":"s5-work-order-read-v1"},{"method":"GET","path":"/api/v1/sites/{siteId}/work-orders/{workOrderId}","owner":"work-order-service","publicIngress":"platform-gateway","activationStatus":"expand-baseline","revision":1,"rollout":{"mode":"disabled"},"compatibilityMode":"native","allowedScopeDimensions":["organization","site","principal","work-order"],"migrationPhase":"S5-R0-contract-only","shadowSideEffectPolicy":"NONE","readOnlyFallback":false,"fallbackForbiddenResults":["AUTHORIZATION_DENIED","RESOURCE_NOT_FOUND"],"cohortGroup":"s5-work-order-read-v1"}]}`

func TestS5WorkOrderContractOnlyRoutesLoadButAreNotDiscoverable(t *testing.T) {
	snapshot, err := ownershipregistry.Parse([]byte(s5WorkOrderRegistry))
	if err != nil {
		t.Fatalf("S5 Work Order registry rejected: %v", err)
	}
	for _, path := range []string{
		"/api/v1/sites/01910000-0001-7000-8000-000000000001/work-orders",
		"/api/v1/sites/01910000-0001-7000-8000-000000000001/work-orders/01910000-0002-7000-8000-000000000001",
	} {
		if _, err := snapshot.Resolve("GET", path, ""); !errors.Is(err, ownershipregistry.ErrRouteMissing) {
			t.Fatalf("disabled S5 Work Order route became discoverable: %v", err)
		}
	}
}

func TestS5WorkOrderBaselineRejectsActivationFallbackAndScopeDrift(t *testing.T) {
	tests := map[string]string{
		"active rollout":       strings.Replace(s5WorkOrderRegistry, `"rollout":{"mode":"disabled"}`, `"rollout":{"mode":"all"}`, 1),
		"wrong owner":          strings.Replace(s5WorkOrderRegistry, `"owner":"work-order-service"`, `"owner":"alarm-service"`, 1),
		"fallback":             strings.Replace(s5WorkOrderRegistry, `"readOnlyFallback":false`, `"readOnlyFallback":true,"readFallbackOwner":"legacy-hvac-backend"`, 1),
		"missing detail scope": strings.Replace(s5WorkOrderRegistry, `,"work-order"`, ``, 1),
		"write route":          strings.Replace(s5WorkOrderRegistry, `"method":"GET"`, `"method":"POST"`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ownershipregistry.Parse([]byte(input)); err == nil {
				t.Fatal("invalid S5 Work Order ownership was accepted")
			}
		})
	}
}
