package iam

import (
	"strings"
	"testing"
)

func TestReconciliationLockKeyIsPostgresTextSafeAndUnambiguous(t *testing.T) {
	left := reconciliationLockKey("identity", "a", "bc")
	right := reconciliationLockKey("identity", "ab", "c")
	withNUL := reconciliationLockKey("source", "identity", "user\x00subject")

	if left == right {
		t.Fatal("different reconciliation identities produced the same advisory lock key")
	}
	for _, key := range []string{left, right, withNUL} {
		if strings.ContainsRune(key, '\x00') {
			t.Fatalf("advisory lock key contains a PostgreSQL text NUL byte: %q", key)
		}
		if !strings.HasPrefix(key, "iam-reconciliation-") {
			t.Fatalf("unexpected advisory lock namespace: %q", key)
		}
	}
}
