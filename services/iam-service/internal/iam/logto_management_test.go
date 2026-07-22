package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLogtoManagementClientUsesM2MAndCachesToken(t *testing.T) {
	var tokenRequests atomic.Int32
	credential := strings.Repeat("c", 12)
	token := strings.Repeat("t", 12)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oidc/token":
			tokenRequests.Add(1)
			clientID, suppliedCredential, ok := request.BasicAuth()
			if !ok || clientID != "m2m-client" || suppliedCredential != credential {
				t.Fatalf("unexpected M2M authentication")
			}
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			expected := url.Values{
				"grant_type": {"client_credentials"},
				"resource":   {"https://tenant.logto.app/api"},
				"scope":      {"all"},
			}
			if request.Form.Encode() != expected.Encode() {
				t.Fatalf("unexpected token form: %s", request.Form.Encode())
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token": token,
				"token_type":   "Bearer",
				"expires_in":   300,
			})
		case "/api/users/user-123":
			if request.Header.Get("Authorization") != "Bearer "+token {
				t.Fatalf("unexpected authorization header")
			}
			_ = json.NewEncoder(writer).Encode(LogtoUser{
				ID: "user-123", PrimaryEmail: "provider@example.test", Name: "Provider Name", UpdatedAt: 42,
			})
		case "/api/users/user-123/organizations":
			if request.Header.Get("Authorization") != "Bearer "+token {
				t.Fatalf("unexpected authorization header")
			}
			_ = json.NewEncoder(writer).Encode([]LogtoOrganization{{
				ID: "logto-org-1", Name: "Owner A",
				OrganizationRoles: []LogtoOrganizationRole{{ID: "role-1", Name: "registry-reader", Type: "User"}},
			}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	config := LogtoManagementConfig{
		Endpoint: server.URL, ClientID: "m2m-client", Resource: "https://tenant.logto.app/api",
		Scope: "all", HTTPClient: server.Client(), allowInsecureHTTPForTests: true,
	}
	config.ClientSecret = credential
	client, err := NewLogtoManagementClient(config)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) }
	user, err := client.User(context.Background(), "user-123")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "user-123" || user.PrimaryEmail != "provider@example.test" || user.Name != "Provider Name" {
		t.Fatalf("unexpected user: %#v", user)
	}
	for range 2 {
		organizations, err := client.UserOrganizations(context.Background(), "user-123")
		if err != nil {
			t.Fatal(err)
		}
		if len(organizations) != 1 || organizations[0].OrganizationRoles[0].Name != "registry-reader" {
			t.Fatalf("unexpected organizations: %#v", organizations)
		}
	}
	if tokenRequests.Load() != 1 {
		t.Fatalf("expected one cached token request, got %d", tokenRequests.Load())
	}
}

func TestLogtoManagementClientRejectsInsecureProductionEndpoint(t *testing.T) {
	config := LogtoManagementConfig{
		Endpoint: "http://127.0.0.1:3001", ClientID: "m2m-client",
		Resource: "https://tenant.logto.app/api", Scope: "all",
	}
	config.ClientSecret = strings.Repeat("c", 12)
	if _, err := NewLogtoManagementClient(config); err == nil || err.Error() != "Logto Management endpoint must use HTTPS" {
		t.Fatalf("insecure production endpoint was accepted: %v", err)
	}
}

func TestLogtoManagementClientDoesNotExposeProviderErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid_client","detail":"provider-sensitive-detail"}`))
	}))
	defer server.Close()

	config := LogtoManagementConfig{
		Endpoint: server.URL, ClientID: "m2m-client", Resource: "https://tenant.logto.app/api",
		Scope: "all", HTTPClient: server.Client(), allowInsecureHTTPForTests: true,
	}
	config.ClientSecret = strings.Repeat("c", 12)
	client, err := NewLogtoManagementClient(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UserOrganizations(context.Background(), "user-123")
	if err == nil || err.Error() != "Logto M2M token endpoint returned HTTP 401" {
		t.Fatalf("provider response body escaped the adapter boundary: %v", err)
	}
}

func TestLogtoManagementClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer server.Close()

	config := LogtoManagementConfig{
		Endpoint: server.URL, ClientID: "m2m-client", Resource: "https://tenant.logto.app/api",
		Scope: "all", HTTPClient: server.Client(), allowInsecureHTTPForTests: true,
	}
	config.ClientSecret = strings.Repeat("c", 12)
	client, err := NewLogtoManagementClient(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UserOrganizations(context.Background(), "user-123"); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect was not rejected safely: %v", err)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatal("management credentials were sent to a redirect target")
	}
}
