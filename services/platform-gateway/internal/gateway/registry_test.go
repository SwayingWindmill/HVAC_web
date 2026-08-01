package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	registryTestOrganizationID = "018f1e00-1000-7000-8000-000000000001"
	registryTestSiteID         = "018f1e00-1000-7000-8000-000000000002"
	registryTestEquipmentID    = "018f1e00-1000-7000-8000-000000000003"
	registryTestDeviceID       = "018f1e00-1000-7000-8000-000000000004"
	registryTestBindingID      = "018f1e00-1000-7000-8000-000000000005"
)

type eventFailingAuditSink struct {
	delegate  ownershipregistry.AuditSink
	failEvent string
}

func (sink eventFailingAuditSink) Record(ctx context.Context, record ownershipregistry.AuditRecord) error {
	if record.EventType == sink.failEvent {
		return errors.New("forced route audit failure")
	}
	return sink.delegate.Record(ctx, record)
}

func TestGatewayRegistryRejectsDeviceBindingScopeDrift(t *testing.T) {
	binding := registryDeviceBinding()
	binding.SiteID = "018f1e00-1000-7000-8000-000000000006"
	raw, err := json.Marshal(platformapi.DeviceBindingCollection{Items: []platformapi.DeviceBinding{binding}, NextCursor: nil, HasMore: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRegistrySuccess(registryauth.ActionDeviceBindingList, registryTestSiteID, raw); err == nil {
		t.Fatal("cross-Site DeviceBinding response was accepted")
	}
}

func TestGatewayRegistryRoutesAuthorizeAndForwardOnlyGrant(t *testing.T) {
	var mu sync.Mutex
	var actions []registryauth.Action
	var corePaths []string
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoPrimary, 0,
		func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Cookie") != "" || request.Header.Get("X-Principal") != "" || request.Header.Get("X-Organization-ID") != "" {
				t.Fatalf("Core request leaked browser identity headers: %#v", request.Header)
			}
			if request.Header.Get("X-Delegation-Grant") != "e30.c2ln" || request.Header.Get("X-Route-Policy-Revision") == "" || request.Header.Get("X-Request-ID") == "" {
				t.Fatalf("Core request missing trusted routing headers: %#v", request.Header)
			}
			mu.Lock()
			corePaths = append(corePaths, request.URL.RequestURI())
			mu.Unlock()
			return registrySuccessResponse(request.URL.Path), nil
		}, nil, func(action registryauth.Action) {
			mu.Lock()
			actions = append(actions, action)
			mu.Unlock()
		})

	tests := []struct {
		path   string
		action registryauth.Action
	}{
		{platformapi.ListOrganizationsPath + "?limit=20", registryauth.ActionOrganizationList},
		{"/api/v1/organizations/" + registryTestOrganizationID, registryauth.ActionOrganizationRead},
		{"/api/v1/organizations/" + registryTestOrganizationID + "/sites?limit=10", registryauth.ActionSiteList},
		{"/api/v1/sites/" + registryTestSiteID, registryauth.ActionSiteRead},
		{"/api/v1/sites/" + registryTestSiteID + "/equipment", registryauth.ActionEquipmentList},
		{"/api/v1/equipment/" + registryTestEquipmentID, registryauth.ActionEquipmentRead},
		{"/api/v1/sites/" + registryTestSiteID + "/devices", registryauth.ActionDeviceList},
		{"/api/v1/sites/" + registryTestSiteID + "/device-bindings", registryauth.ActionDeviceBindingList},
		{"/api/v1/devices/" + registryTestDeviceID, registryauth.ActionDeviceRead},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("X-Route-Policy-Revision") != "4" {
			t.Fatalf("%s route revision header=%q", test.path, recorder.Header().Get("X-Route-Policy-Revision"))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(actions) != len(tests) || len(corePaths) != len(tests) {
		t.Fatalf("actions=%#v paths=%#v", actions, corePaths)
	}
	for index, test := range tests {
		if actions[index] != test.action {
			t.Fatalf("action[%d]=%q want=%q", index, actions[index], test.action)
		}
	}
}

func TestGatewayRegistryFinalRouteDecisionIncludesActorEvidence(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseGoPrimary, 0,
		func(request *http.Request) (*http.Response, error) {
			return registrySuccessResponse(request.URL.Path), nil
		}, nil, nil, audit)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("actor evidence status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	records := audit.Records()
	if len(records) != 1 || records[0].EventType != "ROUTE_DECIDED" || records[0].OrganizationID != registryTestOrganizationID || records[0].InitiatingSubject != "fixture-user" || records[0].InitiatingIssuer != "https://issuer.example.test" {
		t.Fatalf("route decision actor evidence=%#v", records)
	}
}

func TestGatewayRegistryRejectsMalformedInputBeforeAuthorization(t *testing.T) {
	var iamCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoPrimary, 0,
		func(request *http.Request) (*http.Response, error) {
			t.Fatal("Core was called for malformed public input")
			return nil, nil
		}, nil, func(registryauth.Action) { iamCalls.Add(1) })

	for _, path := range []string{
		"/api/v1/organizations/018F1E00-1000-7000-8000-000000000001",
		platformapi.ListOrganizationsPath + "?limit=0",
		platformapi.ListOrganizationsPath + "?limit=10&limit=20",
		platformapi.ListOrganizationsPath + "?unknown=value",
		platformapi.ListOrganizationsPath + "?cursor=not-a-cursor",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	if iamCalls.Load() != 0 {
		t.Fatalf("IAM was called %d times for malformed input", iamCalls.Load())
	}
}

func TestGatewayRegistryLegacyProjectionPreservesSiteOnlyIAMScope(t *testing.T) {
	var gatewayHandler *handler
	var observedClaims identitycontext.DelegationClaims
	h, sessionID, _ := newRegistryTestHandlerWithDecision(t, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100,
		func(request *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusOK, registrySite()), nil
		}, func(request *http.Request) (*http.Response, error) {
			claims, err := identitycontext.VerifyDelegation(gatewayHandler.identity.config.DelegationSigner.Public(), request.Header.Get("X-Delegation-Grant"))
			if err != nil {
				t.Fatalf("verify Legacy delegation: %v", err)
			}
			observedClaims = claims
			return jsonHTTPResponse(http.StatusOK, registrySite()), nil
		}, func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
			response := allowedRegistryDecision(request)
			response.Decision.AllowedOrganizationIDs = nil
			response.Decision.AllowedSiteIDs = []string{registryTestSiteID}
			return response
		})
	gatewayHandler = h
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+registryTestSiteID, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("site-only status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(observedClaims.Scopes) != 1 || observedClaims.Scopes[0] != "site:"+registryTestSiteID {
		t.Fatalf("Legacy delegation expanded IAM scope: %#v", observedClaims.Scopes)
	}
}

func TestGatewayRegistryRejectsOverlappingIAMScopeProjection(t *testing.T) {
	var backendCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandlerWithDecision(t, ownershipregistry.PhaseGoPrimary, 0,
		func(*http.Request) (*http.Response, error) {
			backendCalls.Add(1)
			return jsonHTTPResponse(http.StatusOK, registrySite()), nil
		}, nil, func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
			response := allowedRegistryDecision(request)
			response.Decision.AllowedSiteIDs = []string{registryTestSiteID}
			response.Decision.DeniedSiteIDs = []string{registryTestSiteID}
			return response
		})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+registryTestSiteID, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || backendCalls.Load() != 0 {
		t.Fatalf("overlapping projection status=%d backendCalls=%d body=%s", recorder.Code, backendCalls.Load(), recorder.Body.String())
	}
}

func TestGatewayRegistryRejectsStaleIAMPolicyProjection(t *testing.T) {
	var backendCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandlerWithDecision(t, ownershipregistry.PhaseGoPrimary, 0,
		func(*http.Request) (*http.Response, error) {
			backendCalls.Add(1)
			return registrySuccessResponse("/internal/v1/registry/organizations"), nil
		}, nil, func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
			response := allowedRegistryDecision(request)
			response.Decision.PolicyRevision = "policy-stale"
			return response
		})
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || backendCalls.Load() != 0 {
		t.Fatalf("stale policy status=%d backendCalls=%d body=%s", recorder.Code, backendCalls.Load(), recorder.Body.String())
	}
}

func TestGatewayRegistryRejectsOversizedIAMDecision(t *testing.T) {
	var backendCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoPrimary, 0,
		func(*http.Request) (*http.Response, error) {
			backendCalls.Add(1)
			return registrySuccessResponse("/internal/v1/registry/organizations"), nil
		}, nil, nil)
	encoded, err := json.Marshal(allowedRegistryDecision(registryauth.DecisionRequest{ActingOrganizationID: registryTestOrganizationID, Action: registryauth.ActionOrganizationList}))
	if err != nil {
		t.Fatal(err)
	}
	oversized := append(encoded, bytes.Repeat([]byte(" "), int(defaultRegistryAuthorizationBody)+1)...)
	h.identity.config.IAMHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(oversized))}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || backendCalls.Load() != 0 {
		t.Fatalf("oversized IAM decision status=%d backendCalls=%d body=%s", recorder.Code, backendCalls.Load(), recorder.Body.String())
	}
}

func TestGatewayRegistryDenialFailsClosedWithoutBackend(t *testing.T) {
	var coreCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandlerWithDecision(t, ownershipregistry.PhaseGoPrimary, 0,
		func(request *http.Request) (*http.Response, error) {
			coreCalls.Add(1)
			return registrySuccessResponse(request.URL.Path), nil
		}, nil, func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
			return registryauth.DecisionResponse{Decision: registryauth.Decision{
				Allowed: false, ActingOrganizationID: request.ActingOrganizationID, Actions: []registryauth.Action{request.Action},
				ReasonCode: registryauth.ReasonDenyActionNotGranted,
			}}
		})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+registryTestDeviceID, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("denial status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil || problem.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("denial problem=%#v err=%v", problem, err)
	}
	if coreCalls.Load() != 0 {
		t.Fatalf("denial reached Core %d times", coreCalls.Load())
	}
}

func TestGatewayRegistryFallbackOnlyForRetryableCoreFailure(t *testing.T) {
	var legacyCalls atomic.Int32
	audit := ownershipregistry.NewMemoryAuditSink()
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"CORE_PRIVATE_FAILURE","detail":"secret"}`))}, nil
		}, func(request *http.Request) (*http.Response, error) {
			legacyCalls.Add(1)
			return registrySuccessResponse(strings.Replace(request.URL.Path, "/api/v1", "/internal/v1/registry", 1)), nil
		}, nil, audit)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || legacyCalls.Load() != 1 {
		t.Fatalf("fallback status=%d legacyCalls=%d body=%s", recorder.Code, legacyCalls.Load(), recorder.Body.String())
	}
	if records := audit.Records(); len(records) != 2 || records[1].EventType != "ROUTE_FALLBACK_EXECUTED" || records[1].PrimaryBodySHA256 == "" || records[1].SecondaryBodySHA256 == "" {
		t.Fatalf("fallback audit=%#v", records)
	}
}

func TestGatewayRegistryInvalidLegacyFallbackDoesNotReplaceCoreFailure(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"CORE_PRIVATE_FAILURE"}`))}, nil
		}, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"unexpected":true}`))}, nil
		}, nil, audit)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid fallback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "REGISTRY_UNAVAILABLE" || !problem.Retryable {
		t.Fatalf("Core failure was replaced by invalid Legacy response: %#v", problem)
	}
	if records := audit.Records(); len(records) != 2 || records[1].EventType != "ROUTE_FALLBACK_EXECUTED" {
		t.Fatalf("invalid fallback audit=%#v", records)
	}
}

func TestGatewayRegistryFallbackFailsClosedWhenAuditIsUnavailable(t *testing.T) {
	memoryAudit := ownershipregistry.NewMemoryAuditSink()
	audit := eventFailingAuditSink{delegate: memoryAudit, failEvent: "ROUTE_FALLBACK_EXECUTED"}
	var legacyCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"CORE_PRIVATE_FAILURE"}`))}, nil
		}, func(request *http.Request) (*http.Response, error) {
			legacyCalls.Add(1)
			return registrySuccessResponse(strings.Replace(request.URL.Path, "/api/v1", "/internal/v1/registry", 1)), nil
		}, nil, audit)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || legacyCalls.Load() != 1 {
		t.Fatalf("unaudited fallback status=%d legacyCalls=%d body=%s", recorder.Code, legacyCalls.Load(), recorder.Body.String())
	}
	if records := memoryAudit.Records(); len(records) != 1 || records[0].EventType != "ROUTE_DECIDED" {
		t.Fatalf("unaudited fallback records=%#v", records)
	}
}

func TestGatewayRegistryShadowDoesNotMutateLegacyCircuitBreaker(t *testing.T) {
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoCanaryLegacyShadow, 50,
		func(request *http.Request) (*http.Response, error) {
			return registrySuccessResponse(request.URL.Path), nil
		},
		func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }, nil)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	session, failure := h.identitySession(request)
	if failure != nil {
		t.Fatalf("load test session: %#v", failure)
	}
	route := publicRegistryRoute{template: platformapi.ListOrganizationsPath, internalPath: "/internal/v1/registry/organizations", action: registryauth.ActionOrganizationList, list: true}
	decision := ownershipregistry.Decision{RegistryRevision: 2, RouteRevision: 3, PathTemplate: route.template}
	for range h.legacy.config.FailureThreshold + 1 {
		result := h.executeLegacyRegistry(context.Background(), route, "", []string{"organization:" + session.ActingOrganizationID}, h.identity.config.PolicyRevision, session, decision, false)
		if !result.retryable {
			t.Fatalf("shadow failure result=%#v", result)
		}
	}
	if h.legacy.isOpen(h.identity.now()) {
		t.Fatal("Legacy shadow failures opened the circuit breaker")
	}
	for range h.legacy.config.FailureThreshold {
		_ = h.executeLegacyRegistry(context.Background(), route, "", []string{"organization:" + session.ActingOrganizationID}, h.identity.config.PolicyRevision, session, decision, true)
	}
	if !h.legacy.isOpen(h.identity.now()) {
		t.Fatal("primary Legacy failures did not open the circuit breaker")
	}
}

func TestGatewayRegistryDoesNotFallbackAfterNonRetryableCoreResponse(t *testing.T) {
	invalidStatus := registryOrganization("core")
	invalidStatus.Status = "UNKNOWN"
	invalidStatusBody, err := json.Marshal(platformapi.OrganizationCollection{Items: []platformapi.Organization{invalidStatus}, NextCursor: nil, HasMore: false})
	if err != nil {
		t.Fatal(err)
	}
	invalidInstant := registryOrganization("core")
	invalidInstant.UpdatedAt = "2026-07-22T12:00:00Z"
	invalidInstantBody, err := json.Marshal(platformapi.OrganizationCollection{Items: []platformapi.Organization{invalidInstant}, NextCursor: nil, HasMore: false})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		status        int
		body          string
		wantStatus    int
		wantCode      string
		wantRetryable bool
	}{
		{name: "authorization denied", status: http.StatusForbidden, body: `{"code":"AUTHORIZATION_DENIED"}`, wantStatus: http.StatusNotFound, wantCode: "RESOURCE_NOT_FOUND"},
		{name: "unknown client failure", status: http.StatusBadRequest, body: `{"code":"UNEXPECTED_CLIENT_FAILURE"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "REGISTRY_UNAVAILABLE"},
		{name: "invalid success contract", status: http.StatusOK, body: `{"unexpected":true}`, wantStatus: http.StatusServiceUnavailable, wantCode: "REGISTRY_UNAVAILABLE"},
		{name: "invalid status enum", status: http.StatusOK, body: string(invalidStatusBody), wantStatus: http.StatusServiceUnavailable, wantCode: "REGISTRY_UNAVAILABLE"},
		{name: "invalid instant format", status: http.StatusOK, body: string(invalidInstantBody), wantStatus: http.StatusServiceUnavailable, wantCode: "REGISTRY_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var legacyCalls atomic.Int32
			h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0,
				func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
				}, func(*http.Request) (*http.Response, error) {
					legacyCalls.Add(1)
					return registrySuccessResponse("/internal/v1/registry/organizations"), nil
				}, nil)
			request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || legacyCalls.Load() != 0 {
				t.Fatalf("status=%d legacyCalls=%d body=%s", recorder.Code, legacyCalls.Load(), recorder.Body.String())
			}
			var problem platformapi.ProblemDetails
			if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != test.wantCode || problem.Retryable != test.wantRetryable {
				t.Fatalf("problem=%#v", problem)
			}
		})
	}
}

func TestGatewayRegistryDoesNotFallbackAfterResourceNotFound(t *testing.T) {
	var legacyCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseGoPrimaryLegacyReadFallback, 0,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"RESOURCE_NOT_FOUND"}`))}, nil
		}, func(*http.Request) (*http.Response, error) {
			legacyCalls.Add(1)
			return registrySuccessResponse("/internal/v1/registry/devices/" + registryTestDeviceID), nil
		}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+registryTestDeviceID, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || legacyCalls.Load() != 0 {
		t.Fatalf("not-found fallback status=%d legacyCalls=%d", recorder.Code, legacyCalls.Load())
	}
}

func TestGatewayRegistryShadowMismatchIsAuditedWithoutChangingPrimary(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	shadowObserved := make(chan struct{}, 1)
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100,
		func(*http.Request) (*http.Response, error) {
			shadowObserved <- struct{}{}
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("core")}, NextCursor: nil, HasMore: false}), nil
		}, func(*http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("legacy")}, NextCursor: nil, HasMore: false}), nil
		}, nil, audit)
	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "legacy") {
		t.Fatalf("primary response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-shadowObserved:
	case <-time.After(time.Second):
		t.Fatal("Core shadow was not called")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		records := audit.Records()
		if len(records) >= 2 {
			if records[1].EventType != "ROUTE_SHADOW_COMPARED" || records[1].OutcomeCode != "MISMATCH" || records[1].SemanticEqual == nil || *records[1].SemanticEqual {
				t.Fatalf("shadow audit=%#v", records[1])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("shadow comparison audit was not recorded")
}

func TestGatewayRegistryShadowTimeoutStillRecordsComparisonEvidence(t *testing.T) {
	audit := ownershipregistry.NewMemoryAuditSink()
	h, sessionID, _ := newRegistryTestHandlerWithAudit(t, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100,
		func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}, func(*http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("legacy")}, NextCursor: nil, HasMore: false}), nil
		}, nil, audit)
	h.registry.shadowTimeout = 10 * time.Millisecond

	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("primary status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		records := audit.Records()
		if len(records) >= 2 {
			if records[1].EventType != "ROUTE_SHADOW_COMPARED" || records[1].SecondaryStatus != http.StatusGatewayTimeout {
				t.Fatalf("timeout shadow audit=%#v", records[1])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed-out shadow comparison audit was not recorded")
}

func TestGatewayRegistryShadowCapacitySkipsWithoutChangingPrimary(t *testing.T) {
	var coreCalls atomic.Int32
	h, sessionID, _ := newRegistryTestHandler(t, ownershipregistry.PhaseLegacyPrimaryGoShadow, 100,
		func(request *http.Request) (*http.Response, error) {
			coreCalls.Add(1)
			return registrySuccessResponse(request.URL.Path), nil
		}, func(request *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("legacy")}, NextCursor: nil, HasMore: false}), nil
		}, nil)
	h.registry.shadowSlots = make(chan struct{}, 1)
	h.registry.shadowSlots <- struct{}{}

	request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "legacy") {
		t.Fatalf("capacity-bound primary status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if coreCalls.Load() != 0 {
		t.Fatalf("capacity-bound shadow called Core %d times", coreCalls.Load())
	}
}

func TestGatewayRegistryCanaryIsStickyForSessionBusinessKey(t *testing.T) {
	var coreCalls atomic.Int32
	var legacyCalls atomic.Int32
	h, sessionID, snapshot := newRegistryTestHandler(t, ownershipregistry.PhaseGoCanaryLegacyShadow, 50,
		func(request *http.Request) (*http.Response, error) {
			coreCalls.Add(1)
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("core")}, NextCursor: nil, HasMore: false}), nil
		}, func(request *http.Request) (*http.Response, error) {
			legacyCalls.Add(1)
			return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("legacy")}, NextCursor: nil, HasMore: false}), nil
		}, nil)
	decision, err := snapshot.Resolve(http.MethodGet, platformapi.ListOrganizationsPath, registryTestOrganizationID+"\x00fixture-user")
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, platformapi.ListOrganizationsPath, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("canary status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		bodies = append(bodies, recorder.Body.String())
	}
	expectedLabel := "legacy"
	if decision.SelectedOwner == ownershipregistry.OwnerCore {
		expectedLabel = "core"
	}
	if bodies[0] != bodies[1] || !strings.Contains(bodies[0], expectedLabel) {
		t.Fatalf("canary was not sticky: owner=%s bodies=%#v", decision.SelectedOwner, bodies)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (coreCalls.Load() < 2 || legacyCalls.Load() < 2) {
		time.Sleep(5 * time.Millisecond)
	}
	if coreCalls.Load() < 2 || legacyCalls.Load() < 2 {
		t.Fatalf("canary primary/shadow calls core=%d legacy=%d", coreCalls.Load(), legacyCalls.Load())
	}
}

func newRegistryTestHandler(t *testing.T, phase string, percentage int, core, legacy func(*http.Request) (*http.Response, error), onIAM func(registryauth.Action)) (*handler, string, *ownershipregistry.Snapshot) {
	t.Helper()
	return newRegistryTestHandlerWithDecision(t, phase, percentage, core, legacy, func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
		if onIAM != nil {
			onIAM(request.Action)
		}
		return allowedRegistryDecision(request)
	})
}

func newRegistryTestHandlerWithDecision(t *testing.T, phase string, percentage int, core, legacy func(*http.Request) (*http.Response, error), decision func(registryauth.DecisionRequest) registryauth.DecisionResponse) (*handler, string, *ownershipregistry.Snapshot) {
	t.Helper()
	return newRegistryTestHandlerWithAudit(t, phase, percentage, core, legacy, decision, ownershipregistry.NewMemoryAuditSink())
}

func newRegistryTestHandlerWithAudit(t *testing.T, phase string, percentage int, core, legacy func(*http.Request) (*http.Response, error), decision func(registryauth.DecisionRequest) registryauth.DecisionResponse, audit ownershipregistry.AuditSink) (*handler, string, *ownershipregistry.Snapshot) {
	t.Helper()
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if decision == nil {
		decision = func(request registryauth.DecisionRequest) registryauth.DecisionResponse {
			return allowedRegistryDecision(request)
		}
	}
	iamClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/v1/registry-read/decision" || request.Header.Get("X-Delegation-Grant") == "" {
			t.Fatalf("invalid IAM request: %s %s %#v", request.Method, request.URL.Path, request.Header)
		}
		var input registryauth.DecisionRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		return jsonHTTPResponse(http.StatusOK, decision(input)), nil
	})}
	store := sessionstore.NewMemoryStore()
	snapshot := registryPhaseSnapshot(t, phase, percentage)
	config := Config{
		Build:        platformapi.BuildInfo{Version: "test", Commit: "test", BuiltAt: now.Format(time.RFC3339)},
		Now:          func() time.Time { return now },
		RouteManager: ownershipregistry.NewManager(snapshot, audit, func() time.Time { return now }),
		RouteAudit:   audit,
		Identity: &IdentityConfig{
			OIDCIssuer:              "https://issuer.example.test",
			OIDCClientID:            "client",
			OIDCRedirectURI:         "https://web.example.test/api/v1/auth/callback",
			PublicOrigin:            "https://web.example.test",
			IAMURL:                  "https://iam.example.test",
			IAMAudience:             "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
			DelegationSigner:        signer,
			TokenEncryptionKey:      []byte("0123456789abcdef0123456789abcdef"),
			SessionStore:            store,
			PolicyRevision:          "policy-v1",
			DelegationTTL:           30 * time.Second,
			IAMHTTPClient:           iamClient,
		},
		Registry: &RegistryConfig{
			CoreBaseURL: "https://core.example.test",
			CoreHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if core == nil {
					return nil, context.DeadlineExceeded
				}
				return core(request)
			})},
			CoreTimeout:   50 * time.Millisecond,
			ShadowTimeout: 100 * time.Millisecond,
		},
	}
	if legacy != nil {
		config.Legacy = &LegacyConfig{
			BaseURL:  "https://legacy.example.test",
			Audience: "legacy-hvac-backend",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return legacy(request)
			})},
			Timeout:          50 * time.Millisecond,
			FailureThreshold: 3,
			OpenDuration:     time.Second,
		}
	}
	h := NewHandler(config).(*handler)
	ciphertext, err := h.identity.encryptBytes([]byte("csrf"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID:                   "opaque-registry-session",
		Principal:            identitycontext.UserPrincipal{Subject: "fixture-user", Issuer: "https://issuer.example.test", DisplayName: "Fixture", Email: "fixture@example.test", Roles: []string{"operator"}},
		ActingOrganizationID: registryTestOrganizationID,
		CSRFTokenCiphertext:  ciphertext,
		ExpiresAt:            now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "policy-v1",
		CorrelationID: "create", TraceID: strings.Repeat("a", 32),
		Traceparent:      "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, created.ID, snapshot
}

func registryPhaseSnapshot(t *testing.T, phase string, percentage int) *ownershipregistry.Snapshot {
	t.Helper()
	paths := []string{
		platformapi.ListOrganizationsPath,
		platformapi.GetOrganizationPathTemplate,
		platformapi.ListOrganizationSitesPathTemplate,
		platformapi.GetSitePathTemplate,
		platformapi.ListSiteEquipmentPathTemplate,
		platformapi.GetEquipmentPathTemplate,
		platformapi.ListSiteDevicesPathTemplate,
		platformapi.ListSiteDeviceBindingsPathTemplate,
		platformapi.GetDevicePathTemplate,
	}
	owner := ownershipregistry.OwnerCore
	compatibility := "native"
	rollout := ownershipregistry.RolloutPolicy{Mode: "all"}
	readFallback := ""
	readOnlyFallback := true
	registryRevision := int64(4)
	routeRevision := int64(4)
	switch phase {
	case ownershipregistry.PhaseLegacyPrimaryGoShadow:
		owner = ownershipregistry.OwnerLegacy
		compatibility = "legacy-read"
		rollout = ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: 100, FallbackOwner: ownershipregistry.OwnerCore, CohortSalt: "ticket05-test"}
		registryRevision, routeRevision = 1, 1
	case ownershipregistry.PhaseGoCanaryLegacyShadow:
		rollout = ownershipregistry.RolloutPolicy{Mode: "percentage", Percentage: percentage, FallbackOwner: ownershipregistry.OwnerLegacy, CohortSalt: "ticket05-test"}
		registryRevision, routeRevision = 2, 2
	case ownershipregistry.PhaseGoPrimaryLegacyReadFallback:
		readFallback = ownershipregistry.OwnerLegacy
		registryRevision, routeRevision = 3, 3
	case ownershipregistry.PhaseGoPrimary:
		readOnlyFallback = false
	}
	entries := make([]ownershipregistry.RouteEntry, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, ownershipregistry.RouteEntry{
			Method: http.MethodGet, Path: path, Owner: owner, Revision: routeRevision, Rollout: rollout,
			CompatibilityMode: compatibility, AllowedScopeDimensions: []string{"organization", "principal"},
			MigrationPhase: phase, ShadowSideEffectPolicy: "NONE", ReadOnlyFallback: readOnlyFallback,
			ReadFallbackOwner: readFallback, FallbackForbiddenResults: []string{"AUTHORIZATION_DENIED", "RESOURCE_NOT_FOUND"},
		})
	}
	encoded, err := json.Marshal(ownershipregistry.Registry{RegistryVersion: 1, RegistryRevision: registryRevision, Routes: entries})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ownershipregistry.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func allowedRegistryDecision(request registryauth.DecisionRequest) registryauth.DecisionResponse {
	return registryauth.DecisionResponse{
		Decision: registryauth.Decision{
			Allowed: true, PrincipalID: "principal-1", SubjectIssuer: "https://issuer.example.test", Subject: "fixture-user",
			ActingOrganizationID: request.ActingOrganizationID, AllowedOrganizationIDs: []string{request.ActingOrganizationID},
			Actions: []registryauth.Action{request.Action}, PolicyRevision: "policy-v1", ReasonCode: registryauth.ReasonAllowOrganizationRole,
		},
		DelegationGrant: "e30.c2ln",
	}
}

func registrySuccessResponse(path string) *http.Response {
	switch {
	case path == "/internal/v1/registry/organizations":
		return jsonHTTPResponse(http.StatusOK, platformapi.OrganizationCollection{Items: []platformapi.Organization{registryOrganization("core")}, NextCursor: nil, HasMore: false})
	case strings.HasPrefix(path, "/internal/v1/registry/organizations/") && strings.HasSuffix(path, "/sites"):
		return jsonHTTPResponse(http.StatusOK, platformapi.SiteCollection{Items: []platformapi.Site{registrySite()}, NextCursor: nil, HasMore: false})
	case strings.HasPrefix(path, "/internal/v1/registry/organizations/"):
		return jsonHTTPResponse(http.StatusOK, registryOrganization("core"))
	case strings.HasPrefix(path, "/internal/v1/registry/sites/") && strings.HasSuffix(path, "/equipment"):
		return jsonHTTPResponse(http.StatusOK, platformapi.EquipmentCollection{Items: []platformapi.Equipment{registryEquipment()}, NextCursor: nil, HasMore: false})
	case strings.HasPrefix(path, "/internal/v1/registry/sites/") && strings.HasSuffix(path, "/devices"):
		return jsonHTTPResponse(http.StatusOK, platformapi.DeviceCollection{Items: []platformapi.Device{registryDevice()}, NextCursor: nil, HasMore: false})
	case strings.HasPrefix(path, "/internal/v1/registry/sites/") && strings.HasSuffix(path, "/device-bindings"):
		return jsonHTTPResponse(http.StatusOK, platformapi.DeviceBindingCollection{Items: []platformapi.DeviceBinding{registryDeviceBinding()}, NextCursor: nil, HasMore: false})
	case strings.HasPrefix(path, "/internal/v1/registry/sites/"):
		return jsonHTTPResponse(http.StatusOK, registrySite())
	case strings.HasPrefix(path, "/internal/v1/registry/equipment/"):
		return jsonHTTPResponse(http.StatusOK, registryEquipment())
	case strings.HasPrefix(path, "/internal/v1/registry/devices/"):
		return jsonHTTPResponse(http.StatusOK, registryDevice())
	default:
		return jsonHTTPResponse(http.StatusNotFound, map[string]any{"code": "RESOURCE_NOT_FOUND"})
	}
}

func registryOrganization(source string) platformapi.Organization {
	return platformapi.Organization{ID: registryTestOrganizationID, Code: "org", DisplayName: source, Status: "ACTIVE", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z"}
}

func registrySite() platformapi.Site {
	return platformapi.Site{ID: registryTestSiteID, OwningOrganizationID: registryTestOrganizationID, Code: "site", DisplayName: "Site", Timezone: "UTC", Status: "ACTIVE", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z"}
}

func registryEquipment() platformapi.Equipment {
	return platformapi.Equipment{ID: registryTestEquipmentID, OwningOrganizationID: registryTestOrganizationID, SiteID: registryTestSiteID, Code: "equipment", DisplayName: "Equipment", EquipmentType: "AHU", Status: "ACTIVE", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z"}
}

func registryDevice() platformapi.Device {
	return platformapi.Device{ID: registryTestDeviceID, OwningOrganizationID: registryTestOrganizationID, SiteID: registryTestSiteID, Code: "device", DisplayName: "Device", DeviceType: "CONTROLLER", Status: "ACTIVE", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z"}
}

func registryDeviceBinding() platformapi.DeviceBinding {
	return platformapi.DeviceBinding{ID: registryTestBindingID, OwningOrganizationID: registryTestOrganizationID, SiteID: registryTestSiteID, DeviceID: registryTestDeviceID, EquipmentID: registryTestEquipmentID, BindingRole: "PRIMARY_CONTROLLER", Status: "ACTIVE", ValidFrom: "2026-07-22T12:00:00.000Z", Revision: 1, CreatedAt: "2026-07-22T12:00:00.000Z", UpdatedAt: "2026-07-22T12:00:00.000Z"}
}

func jsonHTTPResponse(status int, value any) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}
