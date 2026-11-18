package main

import "testing"

func TestRegistryOwnerRequiredEnvAndRunFailure(t *testing.T) {
	t.Setenv("CORE_TLS_CERT", "")
	if _, err := requiredEnv("CORE_TLS_CERT"); err == nil {
		t.Fatal("expected error for missing CORE_TLS_CERT")
	}
	if code := run(); code != 1 {
		t.Fatalf("run() = %d, want 1 when CORE_TLS_CERT is missing", code)
	}
}
