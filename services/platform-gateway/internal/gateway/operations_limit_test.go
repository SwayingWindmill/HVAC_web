package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOperationsLimitPolicyRequiresVersionedRedisFailClosedContract(t *testing.T) {
	valid := `{"schemaVersion":1,"policyId":"phase1-high-risk-limits","revision":3,"limits":{"operationsAgent":{"backend":"redis","failureMode":"fail-closed","scope":"session","windowSeconds":60,"maxRequests":30,"keyPrefix":"hvac:v1:limit:operations-agent"}}}`
	path := filepath.Join(t.TempDir(), "limit-policy.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := loadOperationsLimitPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 3 || policy.Limits.OperationsAgent.MaxRequests != 30 {
		t.Fatalf("unexpected policy: %+v", policy)
	}

	invalid := `{"schemaVersion":1,"policyId":"phase1-high-risk-limits","revision":3,"limits":{"operationsAgent":{"backend":"memory","failureMode":"fail-open","scope":"session","windowSeconds":60,"maxRequests":30,"keyPrefix":"hvac:v1:limit:operations-agent"}}}`
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOperationsLimitPolicy(path); err == nil {
		t.Fatal("fail-open in-process Operations limit policy was accepted")
	}
}
