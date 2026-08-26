package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

const (
	testTenantID       = "018f3d00-0000-7000-8000-000000000001"
	testSiteID         = "018f3e00-1000-7000-8000-000000000001"
	testDeviceID       = "018f3e00-3000-7000-8000-000000000001"
	testCommandPointID = "018f3e00-4000-7000-8000-000000000001"
	testPrincipalID    = "018f3e00-5000-7000-8000-000000000001"
	testApproverID     = "018f3e00-5000-7000-8000-000000000002"
	testCommandID      = "018f3e00-4000-4000-8000-000000000001"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLocalWebGatewayCreatesExactShortLivedCommandGrant(t *testing.T) {
	grantKey := testRSAKey(t)
	delegationKey := testRSAKey(t)
	now := time.Date(2026, 7, 27, 7, 0, 0, 0, time.UTC)
	called := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.Method != http.MethodPost || request.URL.String() != "https://command.local/internal/v1/commands" {
			t.Fatalf("upstream request=%s %s", request.Method, request.URL)
		}
		claims, err := commandauth.VerifyGrant(&grantKey.PublicKey, request.Header.Get("X-Command-Grant"))
		if err != nil {
			t.Fatal(err)
		}
		if claims.Purpose != commandmodel.AuthorizationCommandSubmit || claims.PrincipalID != testPrincipalID ||
			claims.TenantID != testTenantID || claims.SiteID != testSiteID || claims.DeviceID != testDeviceID ||
			claims.Presenter != "spiffe://hvac.local/platform-gateway" || claims.Issuer != "spiffe://hvac.local/iam-service" ||
			claims.Audience != "command-service" || claims.ExpiresAt-claims.IssuedAt != 25 || claims.Transitive {
			t.Fatalf("grant claims=%#v", claims)
		}
		var body internalCreateCommandRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TenantID != testTenantID || body.SiteID != testSiteID ||
			body.DeviceID != testDeviceID || body.PointID != testCommandPointID || body.IdempotencyKey != "hvac-web-test-1" ||
			body.Parameters[commandmodel.ParameterSetpointC] != 24 || body.VerificationPointKey != "zone.temperature_setpoint" ||
			body.CurrentState.BusinessRevision != 21 || body.CurrentState.CurrentValue == nil || *body.CurrentState.CurrentValue != 23 {
			t.Fatalf("upstream body=%#v", body)
		}
		return testResponse(http.StatusAccepted, `{"schemaVersion":1}`), nil
	})}
	gateway := testGateway(grantKey, delegationKey, client, now)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"deviceId":"`+testDeviceID+`","capability":"SET_TEMPERATURE_SETPOINT","parameters":{"setpointC":24}}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", "local-csrf")
	request.Header.Set("Idempotency-Key", "hvac-web-test-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || called != 1 {
		t.Fatalf("status=%d called=%d body=%s", response.Code, called, response.Body.String())
	}
}

func TestLocalWebGatewayReadUsesExactDelegation(t *testing.T) {
	grantKey := testRSAKey(t)
	delegationKey := testRSAKey(t)
	now := time.Date(2026, 7, 27, 7, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		claims, err := identitycontext.VerifyDelegation(&delegationKey.PublicKey, request.Header.Get("X-Command-Read-Context"))
		if err != nil {
			t.Fatal(err)
		}
		if claims.TenantID != testTenantID || claims.Actions[0] != "command:read" ||
			len(claims.Scopes) != 2 || claims.Scopes[0] != "tenant:"+testTenantID || claims.Scopes[1] != "command:"+testCommandID ||
			claims.ExpiresAt-claims.IssuedAt != 30 {
			t.Fatalf("delegation claims=%#v", claims)
		}
		return testResponse(http.StatusOK, `{"schemaVersion":1}`), nil
	})}
	gateway := testGateway(grantKey, delegationKey, client, now)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/commands/"+testCommandID, nil)
	response := httptest.NewRecorder()
	gateway.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLocalWebGatewayRejectsMutationBeforeUpstream(t *testing.T) {
	grantKey := testRSAKey(t)
	delegationKey := testRSAKey(t)
	called := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		return testResponse(http.StatusInternalServerError, `{}`), nil
	})}
	gateway := testGateway(grantKey, delegationKey, client, time.Now().UTC())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{}`))
	request.Header.Set("Origin", "http://evil.invalid")
	request.Header.Set("X-CSRF-Token", "local-csrf")
	response := httptest.NewRecorder()
	gateway.handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("status=%d called=%d", response.Code, called)
	}
}

func TestLocalDeviceProjectionDoesNotExposeAuthorityMetadata(t *testing.T) {
	gateway := testGateway(testRSAKey(t), testRSAKey(t), &http.Client{}, time.Now().UTC())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/local/devices", nil)
	response := httptest.NewRecorder()
	gateway.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "commandPointId") || strings.Contains(body, "verificationPointKey") || !strings.Contains(body, testTenantID) || !strings.Contains(body, testSiteID) || !strings.Contains(body, testDeviceID) {
		t.Fatalf("local Device projection leaked authority metadata: %s", body)
	}
}

func TestLoadDeviceCatalogRequiresV2TenantAndPointAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-catalog.json")
	body := `{"schemaVersion":2,"devices":[{"tenantId":"` + testTenantID + `","siteId":"` + testSiteID + `","deviceId":"` + testDeviceID + `","commandPointId":"` + testCommandPointID + `","verificationPointKey":"zone.temperature_setpoint","name":"AHU-01","type":"AHU"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	devices, err := loadDeviceCatalog(path, testTenantID, testSiteID)
	if err != nil || len(devices) != 1 || devices[0].TenantID != testTenantID || devices[0].CommandPointID != testCommandPointID {
		t.Fatalf("catalog=%#v err=%v", devices, err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(body, `"schemaVersion":2`, `"schemaVersion":1`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeviceCatalog(path, testTenantID, testSiteID); err == nil {
		t.Fatal("schemaVersion 1 catalog must be rejected")
	}
}

func testGateway(grantKey, delegationKey *rsa.PrivateKey, client *http.Client, now time.Time) *gateway {
	return &gateway{config: config{
		publicOrigin:      "http://127.0.0.1:5173",
		commandServiceURL: "https://command.local",
		tenantID:          testTenantID,
		siteID:            testSiteID,
		deviceCatalog: []localDevice{{
			TenantID: testTenantID, SiteID: testSiteID,
			DeviceID: testDeviceID, CommandPointID: testCommandPointID, VerificationPointKey: "zone.temperature_setpoint",
			Name: "AHU-01", Type: "AHU",
		}},
		principalID:        testPrincipalID,
		approverID:         testApproverID,
		csrfToken:          "local-csrf",
		policyRevision:     "s3-local-policy-v1",
		revocationRevision: 1,
		gatewaySPIFFE:      "spiffe://hvac.local/platform-gateway",
		iamIssuer:          "spiffe://hvac.local/iam-service",
		commandAudience:    "command-service",
		client:             client,
		grantSigner:        grantKey,
		delegationSigner:   delegationKey,
		now:                func() time.Time { return now },
	}}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
