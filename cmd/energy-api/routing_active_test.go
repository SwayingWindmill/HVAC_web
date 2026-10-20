package main

import (
	"os"
	"path/filepath"
	"testing"
)

func readRepositoryFixture(t *testing.T, relativePath string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", filepath.FromSlash(relativePath))
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repository fixture %s: %v", relativePath, err)
	}
	return contents
}

func TestParseActiveRouteRegistryAcceptsGoOnlyRegistry(t *testing.T) {
	contents := readRepositoryFixture(t, "contracts/ownership/route-ownership.v1.json")
	if _, err := parseActiveRouteRegistry(contents); err != nil {
		t.Fatalf("active Go-only registry was rejected: %v", err)
	}
}

// The Gateway answers only routes the registry gives an owner (#664: the mini program sign-in
// answered 404 on the server until it was registered).
func TestMiniProgramSignInRoutesHaveTheGatewayAsOwner(t *testing.T) {
	snapshot, err := parseActiveRouteRegistry(readRepositoryFixture(t, "contracts/ownership/route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/auth/app/sessions", "/api/v1/auth/app/sessions/totp"} {
		decision, err := snapshot.Resolve("POST", path, "")
		if err != nil || decision.SelectedOwner != "platform-gateway" {
			t.Fatalf("POST %s resolves to %+v (%v), want platform-gateway", path, decision, err)
		}
	}
}
