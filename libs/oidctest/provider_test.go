package oidctest

import "testing"

func TestConfiguredDefaultTenantID(t *testing.T) {
	provider, err := New(Config{
		Issuer:          "https://identity.example.test",
		ClientID:        "hvac-web-test",
		RedirectURI:     "https://web.example.test/api/v1/auth/callback",
		DefaultTenantID: "018f3e00-0000-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.defaultTenantID != "018f3e00-0000-7000-8000-000000000001" {
		t.Fatalf("default tenant = %q", provider.defaultTenantID)
	}
}

func TestDefaultTenantID(t *testing.T) {
	provider, err := New(Config{
		Issuer:      "https://identity.example.test",
		ClientID:    "hvac-web-test",
		RedirectURI: "https://web.example.test/api/v1/auth/callback",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.defaultTenantID != "018f3d00-0000-7000-8000-000000000001" {
		t.Fatalf("default tenant = %q", provider.defaultTenantID)
	}
}
