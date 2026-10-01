package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPGrantStatusProviderUsesBoundedNonRedirectingHTTPS(t *testing.T) {
	if _, err := NewHTTPGrantStatusProvider("http://iam.example.test", &http.Client{}); err == nil {
		t.Fatal("insecure IAM endpoint was accepted")
	}
	if _, err := NewHTTPGrantStatusProvider("https://iam.example.test/prefix", &http.Client{}); err == nil {
		t.Fatal("IAM endpoint with a path was accepted")
	}
	if _, err := NewHTTPGrantStatusProvider("https://user@example.test", &http.Client{}); err == nil {
		t.Fatal("IAM endpoint with user information was accepted")
	}

	var targetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"currentPolicyRevision":"unexpected","revoked":false}`))
	}))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	provider, err := NewHTTPGrantStatusProvider(redirect.URL, redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Lookup(context.Background(), testGrantClaims("organization.list")); !errors.Is(err, ErrStatusFailed) {
		t.Fatalf("redirect error = %v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("IAM redirect target was called")
	}

	oversized := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"currentPolicyRevision":"registry-read:1","revoked":false,"padding":"` + strings.Repeat("x", maximumGrantStatusResponse) + `"}`))
	}))
	defer oversized.Close()
	provider, err = NewHTTPGrantStatusProvider(oversized.URL, oversized.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Lookup(context.Background(), testGrantClaims("organization.list")); !errors.Is(err, ErrStatusFailed) {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestHTTPGrantStatusProviderReadsOnlySafeStatusFields(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != IAMGrantStatusPath {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"currentPolicyRevision":"registry-read:1","revoked":true}`))
	}))
	defer server.Close()
	provider, err := NewHTTPGrantStatusProvider(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	status, err := provider.Lookup(context.Background(), testGrantClaims("organization.list"))
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentPolicyRevision != testPolicy || !status.Revoked {
		t.Fatalf("status = %#v", status)
	}
}
