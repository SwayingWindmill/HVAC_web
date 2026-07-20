package gateway_test

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/oidctest"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/services/iam-service/pkg/iamserver"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/internal/gateway"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

type authHarness struct {
	gatewayServer *httptest.Server
	iamServer     *httptest.Server
	oidcServer    *httptest.Server
	gatewayURL    string
	logs          *bytes.Buffer
	transport     http.RoundTripper
}

func TestAuthenticatedPrincipalLoop(t *testing.T) {
	harness := newAuthHarness(t)
	client := harness.browserClient(t)
	principal, response := loginAndReadPrincipal(t, client, harness.gatewayURL, "")

	if principal.Principal.Subject != "fixture-user" || principal.Context.ExecutingServicePrincipal.SPIFFEID != "spiffe://hvac.local/platform-gateway" {
		t.Fatalf("unexpected principal chain: %#v", principal)
	}
	if principal.Context.ActingOrganizationID != "org-fixture-01" || principal.Context.Audience != "iam-service" || principal.Context.PolicyRevision != "policy-v1" {
		t.Fatalf("identity context is incomplete: %#v", principal.Context)
	}
	if principal.Session.CSRFToken == "" || principal.Session.ID == "" || principal.Session.RevocationObjectiveMS > 1000 {
		t.Fatalf("session view is incomplete: %#v", principal.Session)
	}
	if response.Request.URL.RawQuery != "" || strings.Contains(response.Request.URL.String(), "code=") || strings.Contains(response.Request.URL.String(), "token") {
		t.Fatalf("OIDC material leaked into final URL: %s", response.Request.URL)
	}
	gatewayURL, _ := url.Parse(harness.gatewayURL)
	cookies := client.Jar.Cookies(gatewayURL)
	if len(cookies) != 1 || cookies[0].Name != "__Host-hvac_session" || strings.Contains(cookies[0].Value, ".") {
		t.Fatalf("browser did not receive one opaque BFF cookie: %#v", cookies)
	}

	forged, _ := http.NewRequest(http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, nil)
	forged.Header.Set("X-Principal", "forged-user")
	forgedResponse, err := client.Do(forged)
	if err != nil {
		t.Fatal(err)
	}
	defer forgedResponse.Body.Close()
	assertProblemCode(t, forgedResponse, http.StatusBadRequest, "FORGED_IDENTITY_HEADER")

	logoutWithoutOrigin, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.LogoutPath, nil)
	logoutWithoutOrigin.Header.Set("X-CSRF-Token", principal.Session.CSRFToken)
	originResponse, err := client.Do(logoutWithoutOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer originResponse.Body.Close()
	assertProblemCode(t, originResponse, http.StatusForbidden, "ORIGIN_NOT_ALLOWED")

	logout, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.LogoutPath, nil)
	logout.Header.Set("Origin", harness.gatewayURL)
	logout.Header.Set("X-CSRF-Token", principal.Session.CSRFToken)
	logoutResponse, err := client.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.StatusCode)
	}

	afterLogout, err := client.Get(harness.gatewayURL + platformapi.GetCurrentPrincipalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer afterLogout.Body.Close()
	assertProblemCode(t, afterLogout, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
}

func TestOIDCRejectedIdentityPaths(t *testing.T) {
	harness := newAuthHarness(t)
	cases := []struct{ hint, code string }{
		{"invalid-issuer", "OIDC_ISSUER_INVALID"},
		{"invalid-audience", "OIDC_AUDIENCE_INVALID"},
		{"invalid-token-type", "OIDC_TOKEN_TYPE_INVALID"},
		{"invalid-signature", "OIDC_SIGNATURE_INVALID"},
		{"nonce-mismatch", "OIDC_NONCE_INVALID"},
		{"expired", "OIDC_TOKEN_EXPIRED"},
		{"not-before", "OIDC_TOKEN_NOT_ACTIVE"},
		{"pkce-mismatch", "OIDC_PKCE_VALIDATION_FAILED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.hint, func(t *testing.T) {
			client := harness.browserClient(t)
			response, err := client.Get(harness.gatewayURL + platformapi.BeginLoginPath + "?returnTo=%2Fapi%2Fv1%2Fprincipal&login_hint=" + url.QueryEscape(testCase.hint))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			assertProblemCode(t, response, http.StatusUnauthorized, testCase.code)
		})
	}

	rotatedClient := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, rotatedClient, harness.gatewayURL, "rotated")
	if principal.Principal.Subject != "fixture-user" {
		t.Fatalf("JWKS rotation login failed: %#v", principal)
	}
}

func TestAdministrativeSessionRevocation(t *testing.T) {
	harness := newAuthHarness(t)
	userClient := harness.browserClient(t)
	userPrincipal, _ := loginAndReadPrincipal(t, userClient, harness.gatewayURL, "")
	adminClient := harness.browserClient(t)
	adminPrincipal, _ := loginAndReadPrincipal(t, adminClient, harness.gatewayURL, "admin")

	revokePath := strings.Replace(platformapi.RevokeSessionPathTemplate, "{sessionId}", url.PathEscape(userPrincipal.Session.ID), 1)
	revoke, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+revokePath, nil)
	revoke.Header.Set("Origin", harness.gatewayURL)
	revoke.Header.Set("X-CSRF-Token", adminPrincipal.Session.CSRFToken)
	response, err := adminClient.Do(revoke)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", response.StatusCode)
	}
	var result platformapi.SessionRevocationResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID != userPrincipal.Session.ID || result.ObjectiveMS > 1000 {
		t.Fatalf("unexpected revocation result: %#v", result)
	}

	afterRevocation, err := userClient.Get(harness.gatewayURL + platformapi.GetCurrentPrincipalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer afterRevocation.Body.Close()
	assertProblemCode(t, afterRevocation, http.StatusUnauthorized, "SESSION_INVALID")
}

func TestCrossOrganizationAdminCannotRevokeSession(t *testing.T) {
	harness := newAuthHarness(t)
	userClient := harness.browserClient(t)
	userPrincipal, _ := loginAndReadPrincipal(t, userClient, harness.gatewayURL, "")
	otherAdminClient := harness.browserClient(t)
	otherAdminPrincipal, _ := loginAndReadPrincipal(t, otherAdminClient, harness.gatewayURL, "admin-other-organization")

	revokePath := strings.Replace(platformapi.RevokeSessionPathTemplate, "{sessionId}", url.PathEscape(userPrincipal.Session.ID), 1)
	revoke, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+revokePath, nil)
	revoke.Header.Set("Origin", harness.gatewayURL)
	revoke.Header.Set("X-CSRF-Token", otherAdminPrincipal.Session.CSRFToken)
	response, err := otherAdminClient.Do(revoke)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertProblemCode(t, response, http.StatusNotFound, "SESSION_NOT_FOUND")

	stillActive, err := userClient.Get(harness.gatewayURL + platformapi.GetCurrentPrincipalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stillActive.Body.Close()
	if stillActive.StatusCode != http.StatusOK {
		t.Fatalf("cross-Organization revocation changed target Session status to %d", stillActive.StatusCode)
	}
}

func TestDirectIAMAccessWithoutWorkloadIdentityFails(t *testing.T) {
	harness := newAuthHarness(t)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	request, _ := http.NewRequest(http.MethodPost, harness.iamServer.URL+iamserver.CurrentPrincipalPath, nil)
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("IAM accepted a TLS client without a workload certificate")
	}
}

func TestAuthLogsExcludeQueryIdentityAndCredentials(t *testing.T) {
	harness := newAuthHarness(t)
	client := harness.browserClient(t)
	request, _ := http.NewRequest(http.MethodGet, harness.gatewayURL+platformapi.BeginLoginPath+"?returnTo=%2Fapi%2Fv1%2Fprincipal&login_hint=seeded-sensitive-login-hint", nil)
	request.Header.Set("Authorization", "Bearer seeded-sensitive-token")
	request.Header.Set("Cookie", "seeded-sensitive-cookie=value")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	output := harness.logs.String()
	for _, forbidden := range []string{"seeded-sensitive-login-hint", "seeded-sensitive-token", "seeded-sensitive-cookie", "Authorization", "Cookie", "X-Delegation-Grant"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("auth logs leaked %q: %s", forbidden, output)
		}
	}
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", "spiffe://hvac.local/platform-gateway", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := bundle.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	iamServer := httptest.NewUnstartedServer(iamserver.NewHandler(iamserver.Config{AllowedWorkloadSPIFFE: bundle.ClientSPIFFEID, Audience: "iam-service"}))
	iamServer.TLS = serverTLS
	iamServer.StartTLS()
	t.Cleanup(iamServer.Close)

	gatewayServer := httptest.NewUnstartedServer(nil)
	gatewayURL := "https://" + gatewayServer.Listener.Addr().String()
	redirectURI := gatewayURL + platformapi.CompleteLoginPath
	provider, err := oidctest.New(oidctest.Config{Issuer: "http://placeholder.invalid", ClientID: "hvac-web-s0", RedirectURI: redirectURI})
	if err != nil {
		t.Fatal(err)
	}
	oidcServer := httptest.NewServer(provider)
	provider.SetIssuer(oidcServer.URL)
	t.Cleanup(oidcServer.Close)

	clientTLS, err := bundle.ClientTLSConfig("localhost")
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("client key is not a signer")
	}
	var logs bytes.Buffer
	handler := gateway.NewHandler(gateway.Config{
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		Build:  platformapi.BuildInfo{Service: "platform-gateway", Version: "test", Commit: "test", BuiltAt: "test"},
		Identity: &gateway.IdentityConfig{
			OIDCIssuer: oidcServer.URL, OIDCClientID: "hvac-web-s0", OIDCRedirectURI: redirectURI, PublicOrigin: gatewayURL,
			IAMURL: iamServer.URL, IAMAudience: "iam-service", ExecutingWorkloadSPIFFE: bundle.ClientSPIFFEID, PolicyRevision: "policy-v1",
			OIDCHTTPClient: oidcServer.Client(), IAMHTTPClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: clientTLS}},
			DelegationSigner: signer, TokenEncryptionKey: bytes.Repeat([]byte{0x42}, 32), SessionTTL: 10 * time.Minute, StateTTL: time.Minute, DelegationTTL: 30 * time.Second, RevocationObjective: time.Second,
		},
	})
	gatewayServer.Config.Handler = handler
	gatewayServer.StartTLS()
	t.Cleanup(gatewayServer.Close)
	return &authHarness{gatewayServer: gatewayServer, iamServer: iamServer, oidcServer: oidcServer, gatewayURL: gatewayURL, logs: &logs, transport: gatewayServer.Client().Transport}
}

func (h *authHarness) browserClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: h.transport, Jar: jar, Timeout: 15 * time.Second}
}

func loginAndReadPrincipal(t *testing.T, client *http.Client, gatewayURL, loginHint string) (platformapi.CurrentPrincipalResponse, *http.Response) {
	t.Helper()
	loginURL := gatewayURL + platformapi.BeginLoginPath + "?returnTo=%2Fapi%2Fv1%2Fprincipal"
	if loginHint != "" {
		loginURL += "&login_hint=" + url.QueryEscape(loginHint)
	}
	response, err := client.Get(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		assertProblemCode(t, response, http.StatusOK, "")
	}
	var principal platformapi.CurrentPrincipalResponse
	if err := json.NewDecoder(response.Body).Decode(&principal); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	return principal, response
}

func assertProblemCode(t *testing.T, response *http.Response, expectedStatus int, expectedCode string) {
	t.Helper()
	if response.StatusCode != expectedStatus {
		t.Fatalf("status = %d, want %d", response.StatusCode, expectedStatus)
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != expectedCode {
		t.Fatalf("problem code = %q, want %q; problem=%#v", problem.Code, expectedCode, problem)
	}
}
