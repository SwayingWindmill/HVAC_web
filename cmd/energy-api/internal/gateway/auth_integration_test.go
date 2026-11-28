package gateway_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/gateway"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/oidctest"
	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/modules/iam/pkg/iamserver"
)

const authFixtureTenantID = "018f3d00-0000-7000-8000-000000000001"
const authFixtureMFAACR = "urn:hvac:loa:2"

type authTestSyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *authTestSyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *authTestSyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type authHarness struct {
	gatewayServer *httptest.Server
	iamServer     *httptest.Server
	oidcServer    *httptest.Server
	provider      *oidctest.Provider
	gatewayURL    string
	logs          *authTestSyncBuffer
	transport     http.RoundTripper
	sessionStore  *sessionstore.MemoryStore
}

type fixedPrincipalCapabilityResolver struct {
	authorization identitycontext.EffectiveAuthorization
}

func (resolver fixedPrincipalCapabilityResolver) ResolvePrincipalCapabilities(_ context.Context, lookup iamserver.PrincipalCapabilityLookup) (identitycontext.EffectiveAuthorization, error) {
	authorization := resolver.authorization
	if lookup.Subject == "fixture-admin" || lookup.Subject == "fixture-other-admin" {
		authorization.Capabilities = append(append([]identitycontext.Capability{}, authorization.Capabilities...), identitycontext.CapabilitySessionRevoke)
	}
	return authorization, nil
}

func TestAuthenticatedPrincipalLoop(t *testing.T) {
	harness := newAuthHarness(t)
	client := harness.browserClient(t)
	principal, response := loginAndReadPrincipal(t, client, harness.gatewayURL, "")

	if principal.Principal.Subject != "fixture-user" || principal.Context.ExecutingServicePrincipal.SPIFFEID != "spiffe://hvac.local/platform-gateway" {
		t.Fatalf("unexpected principal chain: %#v", principal)
	}
	if principal.Context.TenantID != authFixtureTenantID || principal.Context.Audience != "iam-service" || principal.Context.PolicyRevision != "policy-v1" {
		t.Fatalf("identity context is incomplete: %#v", principal.Context)
	}
	if principal.Authorization.CapabilitySetVersion != identitycontext.CapabilitySetVersion || principal.Authorization.PolicyRevision != "registry-read:test" {
		t.Fatalf("effective authorization is incomplete: %#v", principal.Authorization)
	}
	if len(principal.Authorization.Capabilities) != 2 || principal.Authorization.Capabilities[0] != platformapi.CapabilitySiteRead || principal.Authorization.Capabilities[1] != platformapi.CapabilityDeviceRead {
		t.Fatalf("Gateway did not transport IAM capabilities exactly: %#v", principal.Authorization.Capabilities)
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
	defer logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.StatusCode)
	}
	providerLogoutURL, err := url.Parse(logoutResponse.Header.Get("Location"))
	if err != nil || providerLogoutURL.Scheme == "" || providerLogoutURL.Host == "" {
		t.Fatalf("logout did not return a valid provider redirect: %q (%v)", logoutResponse.Header.Get("Location"), err)
	}
	if providerLogoutURL.Scheme+"://"+providerLogoutURL.Host != harness.oidcServer.URL || providerLogoutURL.Path != "/session/end" {
		t.Fatalf("logout provider endpoint = %s", providerLogoutURL)
	}
	if providerLogoutURL.Query().Get("client_id") != "hvac-web-s0" {
		t.Fatalf("logout client_id = %q", providerLogoutURL.Query().Get("client_id"))
	}
	if providerLogoutURL.Query().Get("post_logout_redirect_uri") != harness.gatewayURL+"/?logged_out=1" {
		t.Fatalf("logout post_logout_redirect_uri = %q", providerLogoutURL.Query().Get("post_logout_redirect_uri"))
	}
	if providerLogoutURL.Query().Has("id_token_hint") || strings.Contains(providerLogoutURL.RawQuery, "token") {
		t.Fatalf("logout URL leaked token material: %s", providerLogoutURL)
	}

	afterLogout, err := client.Get(harness.gatewayURL + platformapi.GetCurrentPrincipalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer afterLogout.Body.Close()
	assertProblemCode(t, afterLogout, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
}

func TestOIDCIDTokenWithoutAssuranceIsRejected(t *testing.T) {
	harness := newAuthHarness(t)
	client := harness.browserClient(t)
	loginURL := harness.gatewayURL + platformapi.BeginLoginPath + "?returnTo=%2Fapi%2Fv1%2Fprincipal&login_hint=minimal-oidc"
	request, err := http.NewRequest(http.MethodPost, loginURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertProblemCode(t, response, http.StatusUnauthorized, "OIDC_ASSURANCE_INVALID")
}

func TestHighAssuranceLoginPersistsMFASessionEvidence(t *testing.T) {
	harness := newAuthHarness(t)
	client := harness.browserClient(t)
	loginURL := harness.gatewayURL + platformapi.BeginLoginPath + "?returnTo=%2Fapi%2Fv1%2Fprincipal&assurance=high"
	request, err := http.NewRequest(http.MethodPost, loginURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var problem platformapi.ProblemDetails
		if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
			t.Fatalf("high-assurance login status = %d; decode problem: %v", response.StatusCode, err)
		}
		t.Fatalf("high-assurance login status = %d; problem=%#v", response.StatusCode, problem)
	}
	var principal platformapi.CurrentPrincipalResponse
	if err := json.NewDecoder(response.Body).Decode(&principal); err != nil {
		t.Fatal(err)
	}
	stored, err := harness.sessionStore.GetSession(context.Background(), principal.Session.ID)
	if err != nil {
		t.Fatalf("read high-assurance BFF session: %v", err)
	}
	if stored.AuthenticationACR != authFixtureMFAACR || !containsFixtureValue(stored.AuthenticationAMR, "pwd") || !containsFixtureValue(stored.AuthenticationAMR, "otp") || stored.AuthenticationTime.IsZero() {
		t.Fatalf("high-assurance session evidence was not persisted: %#v", stored)
	}
}

func TestGatewayRejectsMalformedIAMCapabilityResponse(t *testing.T) {
	harness := newAuthHarnessWithIAMFactory(t, func(_ string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
				t.Fatal("IAM fixture did not receive the Gateway workload certificate")
			}
			claims, err := identitycontext.VerifyDelegation(request.TLS.PeerCertificates[0].PublicKey, request.Header.Get("X-Delegation-Grant"))
			if err != nil {
				t.Fatalf("verify Gateway delegation: %v", err)
			}
			principal := identitycontext.UserPrincipal{
				Subject: claims.Subject, Issuer: claims.SubjectIssuer, DisplayName: claims.DisplayName, Email: claims.Email, Roles: append([]string(nil), claims.Roles...),
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(identitycontext.InternalPrincipalResponse{
				Principal: principal,
				Context: identitycontext.PrincipalContext{
					InitiatingPrincipal:       principal,
					ExecutingServicePrincipal: identitycontext.ServicePrincipal{Service: "platform-gateway", SPIFFEID: claims.ExecutingService},
					TenantID:                  claims.TenantID,
					Audience:                  claims.Audience,
					PolicyRevision:            claims.PolicyRevision,
					DelegationExpiresAt:       time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
				},
				Authorization: identitycontext.EffectiveAuthorization{
					CapabilitySetVersion: identitycontext.CapabilitySetVersion,
					PolicyRevision:       "registry-read:test",
					Capabilities:         []identitycontext.Capability{identitycontext.CapabilitySiteRead, identitycontext.CapabilitySiteRead},
				},
			})
		})
	})
	client := harness.browserClient(t)
	response := beginLogin(t, client, harness.gatewayURL, "?returnTo=%2Fapi%2Fv1%2Fprincipal")
	defer response.Body.Close()
	assertProblemCode(t, response, http.StatusServiceUnavailable, "IAM_RESPONSE_INVALID")
	gatewayURL, _ := url.Parse(harness.gatewayURL)
	if cookies := client.Jar.Cookies(gatewayURL); len(cookies) != 0 {
		t.Fatalf("malformed IAM response created a browser Session: %#v", cookies)
	}
}

func TestOIDCRejectedIdentityPaths(t *testing.T) {
	harness := newAuthHarness(t)
	cases := []struct{ hint, code string }{
		{"invalid-issuer", "OIDC_ISSUER_INVALID"},
		{"callback-issuer-mismatch", "OIDC_ISSUER_INVALID"},
		{"invalid-audience", "OIDC_AUDIENCE_INVALID"},
		{"invalid-token-type", "OIDC_TOKEN_TYPE_INVALID"},
		{"invalid-signature", "OIDC_SIGNATURE_INVALID"},
		{"unknown-signing-key", "OIDC_SIGNATURE_KEY_UNKNOWN"},
		{"nonce-mismatch", "OIDC_NONCE_INVALID"},
		{"expired", "OIDC_TOKEN_EXPIRED"},
		{"not-before", "OIDC_TOKEN_NOT_ACTIVE"},
		{"pkce-mismatch", "OIDC_PKCE_VALIDATION_FAILED"},
		{"disabled-user", "OIDC_CODE_EXCHANGE_FAILED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.hint, func(t *testing.T) {
			client := harness.browserClient(t)
			response := beginLogin(t, client, harness.gatewayURL, "?returnTo=%2Fapi%2Fv1%2Fprincipal&login_hint="+url.QueryEscape(testCase.hint))
			defer response.Body.Close()
			assertProblemCode(t, response, http.StatusUnauthorized, testCase.code)
			gatewayURL, _ := url.Parse(harness.gatewayURL)
			if cookies := client.Jar.Cookies(gatewayURL); len(cookies) != 0 {
				t.Fatalf("rejected identity created browser Session cookies: %#v", cookies)
			}
		})
	}

	rotatedClient := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, rotatedClient, harness.gatewayURL, "rotated")
	if principal.Principal.Subject != "fixture-user" {
		t.Fatalf("JWKS rotation login failed: %#v", principal)
	}
}

func TestOIDCOutagePreservesCommittedSessionAndBlocksNewLogin(t *testing.T) {
	harness := newAuthHarness(t)
	committedClient := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, committedClient, harness.gatewayURL, "")
	if principal.Session.ID == "" {
		t.Fatal("login did not commit a BFF Session before the outage")
	}

	harness.oidcServer.Close()

	current, err := committedClient.Get(harness.gatewayURL + platformapi.GetCurrentPrincipalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Body.Close()
	if current.StatusCode != http.StatusOK {
		t.Fatalf("committed Session depended on OIDC provider availability: status=%d", current.StatusCode)
	}

	newClient := harness.browserClient(t)
	login := beginLogin(t, newClient, harness.gatewayURL, "?returnTo=%2Fapi%2Fv1%2Fprincipal")
	defer login.Body.Close()
	assertProblemCode(t, login, http.StatusServiceUnavailable, "OIDC_PROVIDER_UNAVAILABLE")
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

func TestCrossTenantAdminCannotRevokeSession(t *testing.T) {
	harness := newAuthHarness(t)
	userClient := harness.browserClient(t)
	userPrincipal, _ := loginAndReadPrincipal(t, userClient, harness.gatewayURL, "")
	otherAdminClient := harness.browserClient(t)
	otherAdminPrincipal, _ := loginAndReadPrincipal(t, otherAdminClient, harness.gatewayURL, "admin-other-tenant")

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
		t.Fatalf("cross-Tenant revocation changed target Session status to %d", stillActive.StatusCode)
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
	request, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.BeginLoginPath+"?returnTo=%2Fapi%2Fv1%2Fprincipal&login_hint=seeded-sensitive-login-hint", nil)
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
	return newAuthHarnessWithIAMFactory(t, nil)
}

func newAuthHarnessWithIAMFactory(t *testing.T, factory func(clientSPIFFEID string) http.Handler, configure ...func(*gateway.IdentityConfig)) *authHarness {
	t.Helper()
	return buildAuthHarness(t, factory, nil, configure...)
}

// newAuthHarnessWithNotification also serves Notification Service's handler, built with the
// Gateway's delegation key, behind the Gateway.
func newAuthHarnessWithNotification(t *testing.T, notification func(gatewayKey crypto.PublicKey) http.Handler) *authHarness {
	t.Helper()
	return buildAuthHarness(t, nil, notification)
}

func buildAuthHarness(t *testing.T, factory func(clientSPIFFEID string) http.Handler, notification func(gatewayKey crypto.PublicKey) http.Handler, configure ...func(*gateway.IdentityConfig)) *authHarness {
	return buildAuthHarnessWithGatewayConfig(t, factory, notification, nil, configure...)
}

func buildAuthHarnessWithGatewayConfig(t *testing.T, factory func(clientSPIFFEID string) http.Handler, notification func(gatewayKey crypto.PublicKey) http.Handler, configureGateway func(*gateway.Config), configure ...func(*gateway.IdentityConfig)) *authHarness {
	t.Helper()
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", "spiffe://hvac.local/platform-gateway", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := bundle.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	iamHandler := http.Handler(iamserver.NewHandler(iamserver.Config{
		AllowedWorkloadSPIFFE: bundle.ClientSPIFFEID,
		Audience:              "iam-service",
		PrincipalCapabilityResolver: fixedPrincipalCapabilityResolver{authorization: identitycontext.EffectiveAuthorization{
			CapabilitySetVersion: identitycontext.CapabilitySetVersion,
			PolicyRevision:       "registry-read:test",
			Capabilities: []identitycontext.Capability{
				identitycontext.CapabilitySiteRead,
				identitycontext.CapabilityDeviceRead,
			},
		}},
	}))
	if factory != nil {
		iamHandler = factory(bundle.ClientSPIFFEID)
	}
	iamServer := httptest.NewUnstartedServer(iamHandler)
	iamServer.TLS = serverTLS
	iamServer.StartTLS()
	t.Cleanup(iamServer.Close)

	gatewayServer := httptest.NewUnstartedServer(nil)
	gatewayURL := "https://" + gatewayServer.Listener.Addr().String()
	redirectURI := gatewayURL + platformapi.CompleteLoginPath
	provider, err := oidctest.New(oidctest.Config{Issuer: "http://placeholder.invalid", ClientID: "hvac-web-s0", RedirectURI: redirectURI, DefaultTenantID: authFixtureTenantID})
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
	provider.TrustGatewayGrants(signer.Public(), bundle.ClientSPIFFEID)
	var logs authTestSyncBuffer
	sessionStore := sessionstore.NewMemoryStore()
	identity := &gateway.IdentityConfig{
		OIDCIssuer: oidcServer.URL, OIDCClientID: "hvac-web-s0", OIDCRedirectURI: redirectURI, PublicOrigin: gatewayURL,
		DefaultTenantID: authFixtureTenantID,
		IAMURL:          iamServer.URL, IAMAudience: "iam-service", ExecutingWorkloadSPIFFE: bundle.ClientSPIFFEID, PolicyRevision: "policy-v1",
		OIDCHTTPClient: oidcServer.Client(), IAMHTTPClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: clientTLS}},
		DelegationSigner: signer, TokenEncryptionKey: bytes.Repeat([]byte{0x42}, 32), SessionStore: sessionStore, LoginStateStore: gateway.NewMemoryLoginStateStoreForTest(), SessionTTL: 10 * time.Minute, StateTTL: time.Minute, DelegationTTL: 30 * time.Second, RevocationObjective: time.Second,
	}
	for _, apply := range configure {
		apply(identity)
	}
	var notificationConfig *gateway.NotificationConfig
	if notification != nil {
		notificationServer := httptest.NewServer(notification(signer.Public()))
		t.Cleanup(notificationServer.Close)
		notificationConfig = &gateway.NotificationConfig{BackendBaseURL: notificationServer.URL, BackendHTTPClient: notificationServer.Client()}
	}
	gatewayCfg := gateway.Config{
		Logger:       slog.New(slog.NewJSONHandler(&logs, nil)),
		Build:        platformapi.BuildInfo{Service: "platform-gateway", Version: "test", Commit: "test", BuiltAt: "test"},
		Identity:     identity,
		Notification: notificationConfig,
	}
	if configureGateway != nil {
		configureGateway(&gatewayCfg)
	}
	handler := gateway.NewHandler(gatewayCfg)
	gatewayServer.Config.Handler = handler
	gatewayServer.StartTLS()
	t.Cleanup(gatewayServer.Close)
	return &authHarness{gatewayServer: gatewayServer, iamServer: iamServer, oidcServer: oidcServer, provider: provider, gatewayURL: gatewayURL, logs: &logs, transport: gatewayServer.Client().Transport, sessionStore: sessionStore}
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
	query := "?returnTo=%2Fapi%2Fv1%2Fprincipal"
	if loginHint != "" {
		query += "&login_hint=" + url.QueryEscape(loginHint)
	}
	response := beginLogin(t, client, gatewayURL, query)
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

func beginLogin(t *testing.T, client *http.Client, gatewayURL, query string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, gatewayURL+platformapi.BeginLoginPath+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func containsFixtureValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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

type recordingSessionStore struct {
	sessionstore.Store
	touchCalls atomic.Int32
}

func (store *recordingSessionStore) TouchSession(ctx context.Context, sessionID string, at time.Time) (sessionstore.Session, error) {
	store.touchCalls.Add(1)
	return store.Store.TouchSession(ctx, sessionID, at)
}

func TestLogoutAndRevokeSessionTriggerTelemetryUnsubscribe(t *testing.T) {
	type revokeEvent struct {
		Path      string
		TenantID  string `json:"tenantId"`
		SessionID string `json:"sessionId"`
		Reason    string `json:"reason"`
	}
	var mu sync.Mutex
	var events []revokeEvent
	var failTelemetry atomic.Bool

	telemetryServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body revokeEvent
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		body.Path = request.URL.Path
		mu.Lock()
		events = append(events, body)
		mu.Unlock()
		if failTelemetry.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schemaVersion":1,"revokedSubscriptions":1}`))
	}))
	t.Cleanup(telemetryServer.Close)

	harness := buildAuthHarnessWithGatewayConfig(t, nil, nil, func(cfg *gateway.Config) {
		cfg.Telemetry = &gateway.TelemetryConfig{
			RuntimeBaseURL:    telemetryServer.URL,
			RuntimeHTTPClient: telemetryServer.Client(),
			RuntimeAudience:   "telemetry-runtime-service",
			Timeout:           time.Second,
		}
	})

	userClient := harness.browserClient(t)
	userPrincipal, _ := loginAndReadPrincipal(t, userClient, harness.gatewayURL, "")

	logout, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.LogoutPath, nil)
	logout.Header.Set("Origin", harness.gatewayURL)
	logout.Header.Set("X-CSRF-Token", userPrincipal.Session.CSRFToken)
	logoutResp, err := userClient.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", logoutResp.StatusCode)
	}

	user2Client := harness.browserClient(t)
	user2Principal, _ := loginAndReadPrincipal(t, user2Client, harness.gatewayURL, "")
	adminClient := harness.browserClient(t)
	adminPrincipal, _ := loginAndReadPrincipal(t, adminClient, harness.gatewayURL, "admin")

	failTelemetry.Store(true)
	revokePath := strings.Replace(platformapi.RevokeSessionPathTemplate, "{sessionId}", url.PathEscape(user2Principal.Session.ID), 1)
	revokeReq, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+revokePath, nil)
	revokeReq.Header.Set("Origin", harness.gatewayURL)
	revokeReq.Header.Set("X-CSRF-Token", adminPrincipal.Session.CSRFToken)
	revokeResp, err := adminClient.Do(revokeReq)
	if err != nil {
		t.Fatal(err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status=%d", revokeResp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 telemetry revoke events, got %#v", events)
	}
	if events[0].Path != "/internal/v1/telemetry/subscriptions:revoke" ||
		events[0].TenantID != authFixtureTenantID ||
		events[0].SessionID != userPrincipal.Session.ID ||
		events[0].Reason != "SESSION_LOGGED_OUT" {
		t.Fatalf("unexpected logout revoke event: %#v", events[0])
	}
	if events[1].Path != "/internal/v1/telemetry/subscriptions:revoke" ||
		events[1].TenantID != authFixtureTenantID ||
		events[1].SessionID != user2Principal.Session.ID ||
		events[1].Reason != "SESSION_REVOKED" {
		t.Fatalf("unexpected admin revoke event: %#v", events[1])
	}
}

func TestTouchSessionThrottledWithinOneMinute(t *testing.T) {
	var mu sync.Mutex
	now := time.Now().UTC()
	currentNow := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	recordingStore := &recordingSessionStore{Store: sessionstore.NewMemoryStore()}
	harness := buildAuthHarnessWithGatewayConfig(t, func(clientSPIFFEID string) http.Handler {
		return iamserver.NewHandler(iamserver.Config{
			AllowedWorkloadSPIFFE: clientSPIFFEID,
			Audience:              "iam-service",
			Now:                   currentNow,
			PrincipalCapabilityResolver: fixedPrincipalCapabilityResolver{authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				PolicyRevision:       "registry-read:test",
				Capabilities: []identitycontext.Capability{
					identitycontext.CapabilitySiteRead,
					identitycontext.CapabilityDeviceRead,
				},
			}},
		})
	}, nil, func(cfg *gateway.Config) {
		cfg.Now = currentNow
	}, func(cfg *gateway.IdentityConfig) {
		cfg.SessionStore = recordingStore
	})

	client := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, client, harness.gatewayURL, "")

	callWithActivity := func(active bool) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, harness.gatewayURL+platformapi.GetCurrentPrincipalPath, nil)
		if active {
			req.Header.Set("X-HVAC-User-Activity", "1")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	}

	advance(30 * time.Second)
	callWithActivity(true)
	if got := recordingStore.touchCalls.Load(); got != 0 {
		t.Fatalf("expected 0 TouchSession calls at +30s, got %d", got)
	}

	advance(29 * time.Second)
	callWithActivity(true)
	if got := recordingStore.touchCalls.Load(); got != 0 {
		t.Fatalf("expected 0 TouchSession calls at +59s, got %d", got)
	}

	advance(time.Second)
	callWithActivity(true)
	if got := recordingStore.touchCalls.Load(); got != 1 {
		t.Fatalf("expected 1 TouchSession call at +60s, got %d", got)
	}

	stored, err := recordingStore.GetSession(context.Background(), principal.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.LastActivityAt.Equal(currentNow().UTC()) {
		t.Fatalf("LastActivityAt=%s, want %s", stored.LastActivityAt, currentNow().UTC())
	}

	advance(30 * time.Second)
	callWithActivity(true)
	if got := recordingStore.touchCalls.Load(); got != 1 {
		t.Fatalf("expected TouchSession throttled for 1m after last touch, got %d", got)
	}

	advance(90 * time.Second)
	callWithActivity(false)
	if got := recordingStore.touchCalls.Load(); got != 1 {
		t.Fatalf("expected no TouchSession without activity header, got %d", got)
	}
}

func TestSwitchActiveTenantValidatesMembershipCSRFAndRevokesTelemetry(t *testing.T) {
	const tenantB = "018f3d00-0000-7000-8000-000000000002"
	const nonMemberTenant = "018f3d00-0000-7000-8000-000000000099"
	now := time.Now().UTC()

	var revokedMu sync.Mutex
	var revokedEvents []struct {
		TenantID  string `json:"tenantId"`
		SessionID string `json:"sessionId"`
		Reason    string `json:"reason"`
	}
	telemetryServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/v1/telemetry/subscriptions:revoke" {
			http.NotFound(writer, request)
			return
		}
		var event struct {
			TenantID  string `json:"tenantId"`
			SessionID string `json:"sessionId"`
			Reason    string `json:"reason"`
		}
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		revokedMu.Lock()
		revokedEvents = append(revokedEvents, event)
		revokedMu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(telemetryServer.Close)

	store := sessionstore.NewMemoryStore()
	harness := buildAuthHarnessWithGatewayConfig(t, func(_ string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			claims, err := identitycontext.VerifyDelegation(request.TLS.PeerCertificates[0].PublicKey, request.Header.Get("X-Delegation-Grant"))
			if err != nil {
				t.Fatalf("verify delegation: %v", err)
			}
			roles := []string{"operator"}
			caps := []identitycontext.Capability{identitycontext.CapabilitySiteList, identitycontext.CapabilitySiteRead}
			if claims.TenantID == tenantB {
				roles = []string{"admin"}
				caps = append(caps, identitycontext.CapabilityIAMAdmin)
			}
			user := identitycontext.UserPrincipal{
				Subject: claims.Subject, Issuer: claims.SubjectIssuer, DisplayName: claims.DisplayName, Email: claims.Email, Roles: roles,
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(identitycontext.InternalPrincipalResponse{
				PrincipalID: "018f3e00-2000-7000-8000-000000000001",
				Principal:   user,
				Context: identitycontext.PrincipalContext{
					InitiatingPrincipal:       user,
					ExecutingServicePrincipal: identitycontext.ServicePrincipal{Service: "platform-gateway", SPIFFEID: claims.ExecutingService},
					TenantID:                  claims.TenantID,
					Audience:                  claims.Audience,
					PolicyRevision:            claims.PolicyRevision,
					DelegationExpiresAt:       time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339),
				},
				Authorization: identitycontext.EffectiveAuthorization{
					CapabilitySetVersion: identitycontext.CapabilitySetVersion,
					PolicyRevision:       "capability-v7:test-" + claims.TenantID,
					Capabilities:         caps,
				},
				AvailableTenants: []identitycontext.TenantMembershipSummary{
					{TenantID: authFixtureTenantID, Code: "alpha", DisplayName: "松雅轩"},
					{TenantID: tenantB, Code: "beta", DisplayName: "星沙冷站"},
				},
			})
		})
	}, nil, func(cfg *gateway.Config) {
		cfg.Telemetry = &gateway.TelemetryConfig{
			RuntimeBaseURL: telemetryServer.URL, RuntimeHTTPClient: telemetryServer.Client(),
			RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second,
		}
	}, func(cfg *gateway.IdentityConfig) {
		cfg.SessionStore = store
	})

	browser := harness.browserClient(t)
	initial, _ := loginAndReadPrincipal(t, browser, harness.gatewayURL, "")
	if len(initial.AvailableTenants) != 2 {
		t.Fatalf("availableTenants = %#v, want 2", initial.AvailableTenants)
	}

	postSwitch := func(client *http.Client, origin, csrf, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.SwitchTenantPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	missingCSRF := postSwitch(browser, harness.gatewayURL, "", `{"tenantId":"`+tenantB+`"}`)
	assertProblemCode(t, missingCSRF, http.StatusForbidden, "CSRF_TOKEN_INVALID")
	missingCSRF.Body.Close()

	forbiddenTenant := postSwitch(browser, harness.gatewayURL, initial.Session.CSRFToken, `{"tenantId":"`+nonMemberTenant+`"}`)
	assertProblemCode(t, forbiddenTenant, http.StatusForbidden, "TENANT_SWITCH_FORBIDDEN")
	forbiddenTenant.Body.Close()

	badField := postSwitch(browser, harness.gatewayURL, initial.Session.CSRFToken, `{"tenantId":"`+tenantB+`","extra":true}`)
	assertProblemCode(t, badField, http.StatusBadRequest, "TENANT_SWITCH_REQUEST_INVALID")
	badField.Body.Close()

	storedBefore, err := store.GetSession(context.Background(), initial.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBefore.TenantID != authFixtureTenantID {
		t.Fatalf("rejected switch mutated session tenant to %q", storedBefore.TenantID)
	}

	okResp := postSwitch(browser, harness.gatewayURL, initial.Session.CSRFToken, `{"tenantId":"`+tenantB+`"}`)
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d", okResp.StatusCode)
	}
	var switched platformapi.CurrentPrincipalResponse
	if err := json.NewDecoder(okResp.Body).Decode(&switched); err != nil {
		t.Fatal(err)
	}
	if switched.Context.TenantID != tenantB || !containsCapability(switched.Authorization.Capabilities, platformapi.CapabilityIAMAdmin) {
		t.Fatalf("switched principal = %#v", switched)
	}

	revokedMu.Lock()
	defer revokedMu.Unlock()
	if len(revokedEvents) != 1 || revokedEvents[0].TenantID != authFixtureTenantID || revokedEvents[0].SessionID != initial.Session.ID || revokedEvents[0].Reason != "SESSION_TENANT_SWITCHED" {
		t.Fatalf("telemetry revocation events = %#v", revokedEvents)
	}

	t.Run("mini-program bearer switches tenant without CSRF", func(t *testing.T) {
		appSessionToken := "app-switch-session-token"
		block, err := aes.NewCipher(bytes.Repeat([]byte{0x42}, 32))
		if err != nil {
			t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce := make([]byte, gcm.NonceSize())
		ciphertext := gcm.Seal(nonce, nonce, []byte("csrf-secret"), nil)
		if _, err := store.CreateSession(context.Background(), sessionstore.Session{
			ID: appSessionToken, Client: sessionstore.ClientMiniProgram,
			Principal: identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", DisplayName: "App Engineer", Email: "app@example.test"},
			TenantID:  authFixtureTenantID, CSRFTokenCiphertext: ciphertext,
			AuthenticationACR: "urn:hvac:loa:1", AuthenticationAMR: []string{"pwd"}, AuthenticationTime: now, ExpiresAt: now.Add(time.Hour),
		}, sessionstore.MutationContext{
			Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "policy-v1",
			CorrelationID: "req-app", TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
			ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		appReq, err := http.NewRequest(http.MethodPost, harness.gatewayURL+platformapi.SwitchTenantPath, strings.NewReader(`{"tenantId":"`+tenantB+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		appReq.Header.Set("Content-Type", "application/json")
		appReq.Header.Set("Authorization", "Bearer "+appSessionToken)
		appResp, err := harness.gatewayServer.Client().Do(appReq)
		if err != nil {
			t.Fatal(err)
		}
		defer appResp.Body.Close()
		if appResp.StatusCode != http.StatusOK {
			t.Fatalf("mini program switch status = %d", appResp.StatusCode)
		}
	})
}

func containsCapability(values []platformapi.Capability, target platformapi.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
