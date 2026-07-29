package oidctest

import "testing"

func TestConfiguredDefaultActingOrganizationID(t *testing.T) {
	provider, err := New(Config{
		Issuer:                      "https://identity.example.test",
		ClientID:                    "hvac-web-test",
		RedirectURI:                 "https://web.example.test/api/v1/auth/callback",
		DefaultActingOrganizationID: "018f3e00-0000-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.defaultActingOrganizationID != "018f3e00-0000-7000-8000-000000000001" {
		t.Fatalf("default acting organization = %q", provider.defaultActingOrganizationID)
	}
}

func TestDefaultActingOrganizationIDRemainsBackwardCompatible(t *testing.T) {
	provider, err := New(Config{
		Issuer:      "https://identity.example.test",
		ClientID:    "hvac-web-test",
		RedirectURI: "https://web.example.test/api/v1/auth/callback",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.defaultActingOrganizationID != "org-fixture-01" {
		t.Fatalf("default acting organization = %q", provider.defaultActingOrganizationID)
	}
}
