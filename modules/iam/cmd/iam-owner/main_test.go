package main

import "testing"

func TestIAMOwnerRequiredEnvAndRunFailure(t *testing.T) {
	t.Setenv("IAM_TLS_CERT", "")
	if _, err := requiredEnv("IAM_TLS_CERT"); err == nil {
		t.Fatal("expected error for missing IAM_TLS_CERT")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when IAM_TLS_CERT is missing", code)
	}
}
