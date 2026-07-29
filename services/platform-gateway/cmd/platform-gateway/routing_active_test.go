package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepositoryFixture(t *testing.T, relativePath string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", filepath.FromSlash(relativePath))
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

func TestParseActiveRouteRegistryRejectsHistoricalLegacyOwner(t *testing.T) {
	contents := readRepositoryFixture(t, "contracts/ownership/s1-registry-phases/01-legacy-primary-go-shadow.json")
	_, err := parseActiveRouteRegistry(contents)
	if err == nil || !strings.Contains(err.Error(), "retired Legacy owner") {
		t.Fatalf("historical Legacy owner was not rejected: %v", err)
	}
}
