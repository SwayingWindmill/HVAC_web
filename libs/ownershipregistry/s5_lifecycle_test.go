package ownershipregistry_test

import (
	"fmt"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

func TestS5LifecycleCanaryAcceptsOnlyReviewedRoutesAndPhase(t *testing.T) {
	for _, action := range []string{"plan", "start", "block", "resume", "complete", "cancel", "reopen"} {
		if _, err := ownershipregistry.Parse([]byte(s5LifecycleRegistryJSON(action, ownershipregistry.PhaseS5InternalLifecycle))); err != nil {
			t.Fatalf("action %s rejected: %v", action, err)
		}
	}
	for _, action := range []string{"open", "draft", "link-alarm", "add-note"} {
		if _, err := ownershipregistry.Parse([]byte(s5LifecycleRegistryJSON(action, ownershipregistry.PhaseS5InternalLifecycle))); err == nil {
			t.Fatalf("unreviewed action %s was accepted", action)
		}
	}
	if _, err := ownershipregistry.Parse([]byte(s5LifecycleRegistryJSON("start", "S5-R1-unreviewed-lifecycle"))); err == nil {
		t.Fatal("unreviewed lifecycle phase was accepted")
	}
}

func s5LifecycleRegistryJSON(action, phase string) string {
	format := "{\"registryVersion\":1,\"registryRevision\":18,\"routes\":[{\"method\":\"POST\",\"path\":\"/api/v1/sites/{siteId}/work-orders/{workOrderId}:%s\",\"owner\":\"work-order-service\",\"publicIngress\":\"platform-gateway\",\"activationStatus\":\"internal-canary\",\"revision\":1,\"rollout\":{\"mode\":\"percentage\",\"percentage\":1,\"cohortSalt\":\"s5-work-order-lifecycle-canary-v1\"},\"compatibilityMode\":\"native\",\"allowedScopeDimensions\":[\"organization\",\"site\",\"principal\",\"work-order\",\"key\"],\"migrationPhase\":%q,\"cohortGroup\":\"s5-work-order-lifecycle-v1\",\"shadowSideEffectPolicy\":\"NONE\",\"readOnlyFallback\":false,\"fallbackForbiddenResults\":[\"AUTHORIZATION_DENIED\",\"RESOURCE_NOT_FOUND\",\"VERSION_CONFLICT\",\"IDEMPOTENCY_CONFLICT\"]}]}"
	return fmt.Sprintf(format, action, phase)
}
