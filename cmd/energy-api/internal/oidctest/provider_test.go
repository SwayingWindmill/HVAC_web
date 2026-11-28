package oidctest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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

func TestEndSessionReturnsOnlyToRegisteredAddress(t *testing.T) {
	provider, err := New(Config{
		Issuer:      "https://identity.example.test",
		ClientID:    "hvac-web-test",
		RedirectURI: "https://web.example.test/api/v1/auth/callback",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	cases := []struct {
		name     string
		redirect string
		status   int
	}{
		{"registered", "https://web.example.test/?logged_out=1", http.StatusFound},
		{"other host", "https://evil.example.test/?logged_out=1", http.StatusBadRequest},
		{"same host other path", "https://web.example.test//evil.example.test/", http.StatusBadRequest},
		{"missing", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"client_id": {"hvac-web-test"}, "post_logout_redirect_uri": {tc.redirect}}
			recorder := httptest.NewRecorder()
			provider.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/session/end?"+query.Encode(), nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			if tc.status == http.StatusFound && recorder.Header().Get("Location") != "https://web.example.test/?logged_out=1" {
				t.Fatalf("Location = %q", recorder.Header().Get("Location"))
			}
		})
	}
}
