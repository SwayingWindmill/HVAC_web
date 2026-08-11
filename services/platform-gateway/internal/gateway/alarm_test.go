package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/services/platform-gateway/pkg/platformapi"
)

const (
	gatewayAlarmTenantID       = "0190f000-0000-7000-8000-000000000001"
	gatewayAlarmOrganizationID = "01910000-0000-7000-8000-000000000001"
	gatewayAlarmSiteID         = "01910000-0001-7000-8000-000000000001"
	gatewayAlarmOtherSiteID    = "01910000-0002-7000-8000-000000000001"
	gatewayAlarmID             = "01910000-1000-7000-8000-000000000001"
)

func TestGatewayAlarmListUsesIAMAndExactSignedReadContext(t *testing.T) {
	fixture := newAlarmGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayAlarmSiteID+"/alarms?status=OPEN&limit=25", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchAlarmRoute(fixture.handler, recorder, request, publicAlarmRoute{
		template: "/api/v1/sites/{siteId}/alarms", siteID: gatewayAlarmSiteID, action: alarmauth.ActionList,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.iamCalls.Load() != 2 || fixture.alarmCalls.Load() != 1 {
		t.Fatalf("calls iam=%d alarm=%d", fixture.iamCalls.Load(), fixture.alarmCalls.Load())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control=%q", recorder.Header().Get("Cache-Control"))
	}
	var response alarmmodel.ListResponse
	if json.NewDecoder(recorder.Body).Decode(&response) != nil || len(response.Items) != 1 || response.Items[0].AlarmID != gatewayAlarmID {
		t.Fatalf("unexpected Alarm list: %#v", response)
	}
}

func TestGatewayAlarmDetailRejectsCrossSiteProjection(t *testing.T) {
	fixture := newAlarmGatewayFixture(t)
	fixture.crossSite.Store(true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayAlarmSiteID+"/alarms/"+gatewayAlarmID, nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchAlarmRoute(fixture.handler, recorder, request, publicAlarmRoute{
		template: "/api/v1/sites/{siteId}/alarms/{alarmId}", siteID: gatewayAlarmSiteID, alarmID: gatewayAlarmID, action: alarmauth.ActionRead,
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), gatewayAlarmOtherSiteID) {
		t.Fatal("cross-Site Alarm projection leaked its scope")
	}
}

func TestGatewayAlarmDenialAndInvalidFilterStopBeforeAlarmService(t *testing.T) {
	fixture := newAlarmGatewayFixture(t)
	fixture.deny.Store(true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayAlarmSiteID+"/alarms", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder := httptest.NewRecorder()
	dispatchAlarmRoute(fixture.handler, recorder, request, publicAlarmRoute{siteID: gatewayAlarmSiteID, action: alarmauth.ActionList})
	if recorder.Code != http.StatusForbidden || fixture.alarmCalls.Load() != 0 {
		t.Fatalf("denial status=%d alarmCalls=%d body=%s", recorder.Code, fixture.alarmCalls.Load(), recorder.Body.String())
	}

	fixture.deny.Store(false)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayAlarmSiteID+"/alarms?deviceId=caller-supplied", nil)
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, fixture.session))
	recorder = httptest.NewRecorder()
	dispatchAlarmRoute(fixture.handler, recorder, request, publicAlarmRoute{siteID: gatewayAlarmSiteID, action: alarmauth.ActionList})
	if recorder.Code != http.StatusBadRequest || fixture.iamCalls.Load() != 1 || fixture.alarmCalls.Load() != 0 {
		t.Fatalf("invalid filter reached upstream: status=%d iam=%d alarm=%d", recorder.Code, fixture.iamCalls.Load(), fixture.alarmCalls.Load())
	}
}

func TestGatewayRejectsBrowserAlarmAuthorityHeaders(t *testing.T) {
	handler := NewHandler(Config{Now: time.Now})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/"+gatewayAlarmSiteID+"/alarms", nil)
	request.Header.Set("X-Alarm-Read-Context", "caller-supplied")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type alarmGatewayFixture struct {
	handler    *handler
	session    bffSession
	iamCalls   atomic.Int64
	alarmCalls atomic.Int64
	deny       atomic.Bool
	crossSite  atomic.Bool
}

func newAlarmGatewayFixture(t *testing.T) *alarmGatewayFixture {
	t.Helper()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &alarmGatewayFixture{}
	fixture.session = bffSession{Session: sessionstore.Session{
		ID:                   "session-alarm-1",
		Principal:            identitycontext.UserPrincipal{Subject: "subject-alarm", Issuer: "https://issuer.example", DisplayName: "Alarm Operator", Email: "alarm@example.test", Roles: []string{"operator"}},
		ActingOrganizationID: gatewayAlarmOrganizationID,
		ExpiresAt:            now.Add(15 * time.Minute),
	}}
	iamServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.iamCalls.Add(1)
		claims, verifyErr := identitycontext.VerifyDelegation(signer.Public(), request.Header.Get("X-Delegation-Grant"))
		if verifyErr != nil {
			http.Error(writer, "invalid IAM delegation", http.StatusForbidden)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case alarmDecisionPath:
			if identitycontext.ValidateDelegation(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "alarm:authorize", "session:"+fixture.session.ID) != nil {
				http.Error(writer, "invalid Alarm IAM delegation", http.StatusForbidden)
				return
			}
			var input alarmauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.Validate() != nil {
				http.Error(writer, "invalid decision request", http.StatusBadRequest)
				return
			}
			decision := alarmauth.Decision{
				Allowed: !fixture.deny.Load(), PrincipalID: "principal-alarm-1",
				SubjectIssuer: fixture.session.Principal.Issuer, Subject: fixture.session.Principal.Subject,
				ActingOrganizationID: gatewayAlarmOrganizationID, SiteID: input.SiteID, AlarmID: input.AlarmID, Action: input.Action,
				PolicyRevision: "alarm-policy-1", ReasonCode: alarmauth.ReasonAllowExactScope, DecidedAt: now.Format(time.RFC3339Nano),
			}
			if fixture.deny.Load() {
				decision.ReasonCode = alarmauth.ReasonDenyScope
			}
			_ = json.NewEncoder(writer).Encode(alarmauth.DecisionResponse{Decision: decision})
		case "/internal/v1/registry-read/decision":
			if identitycontext.ValidateDelegation(claims, now, "spiffe://hvac.local/platform-gateway", "iam-service", "registry:authorize", "session:"+fixture.session.ID) != nil {
				http.Error(writer, "invalid Registry IAM delegation", http.StatusForbidden)
				return
			}
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.Validate() != nil || input.Action != registryauth.ActionSiteRead {
				http.Error(writer, "invalid Registry decision request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(registryauth.DecisionResponse{
				Decision: registryauth.Decision{
					Allowed: true, PrincipalID: "principal-alarm-1", SubjectIssuer: fixture.session.Principal.Issuer, Subject: fixture.session.Principal.Subject,
					ActingOrganizationID: gatewayAlarmOrganizationID, AllowedOrganizationIDs: []string{gatewayAlarmOrganizationID}, AllowedSiteIDs: []string{gatewayAlarmSiteID},
					Actions: []registryauth.Action{registryauth.ActionSiteRead}, PolicyRevision: "gateway-policy-1", ReasonCode: registryauth.ReasonAllowOrganizationRole,
				},
				DelegationGrant: "e30.c2ln",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(iamServer.Close)

	registryServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/v1/registry/sites/"+gatewayAlarmSiteID || request.Header.Get("X-Delegation-Grant") == "" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(platformapi.Site{
			ID: gatewayAlarmSiteID, TenantID: gatewayAlarmTenantID, OwningOrganizationID: gatewayAlarmOrganizationID,
			Code: "alarm-site", DisplayName: "Alarm Site", Timezone: "UTC", Status: "ACTIVE", Revision: 1,
			CreatedAt: "2026-08-01T00:00:00.000Z", UpdatedAt: "2026-08-01T00:00:00.000Z",
		})
	}))
	t.Cleanup(registryServer.Close)

	alarmServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.alarmCalls.Add(1)
		claims, verifyErr := identitycontext.VerifyDelegation(signer.Public(), request.Header.Get(alarmReadContextHeader))
		action := string(alarmauth.ActionList)
		scopes := []string{"organization:" + gatewayAlarmOrganizationID, "site:" + gatewayAlarmSiteID}
		if strings.HasSuffix(request.URL.Path, "/"+gatewayAlarmID) {
			action = string(alarmauth.ActionRead)
			scopes = append(scopes, "alarm:"+gatewayAlarmID)
		}
		if verifyErr != nil || identitycontext.ValidateDelegationAnyScope(claims, now, "spiffe://hvac.local/platform-gateway", "alarm-service", action, scopes) != nil || claims.TenantID != gatewayAlarmTenantID || claims.PrincipalID != "principal-alarm-1" || claims.PolicyRevision != "alarm-policy-1" {
			http.Error(writer, `{"code":"ALARM_FORBIDDEN"}`, http.StatusForbidden)
			return
		}
		alarm := validGatewayAlarm(gatewayAlarmSiteID)
		if fixture.crossSite.Load() {
			alarm.SiteID = gatewayAlarmOtherSiteID
		}
		writer.Header().Set("Content-Type", "application/json")
		if action == string(alarmauth.ActionList) {
			_ = json.NewEncoder(writer).Encode(alarmmodel.ListResponse{SchemaVersion: 1, Items: []alarmmodel.Alarm{alarm}, HasMore: false})
			return
		}
		_ = json.NewEncoder(writer).Encode(alarm)
	}))
	t.Cleanup(alarmServer.Close)

	fixture.handler = &handler{
		identity: &identityController{config: IdentityConfig{
			IAMURL: iamServer.URL, IAMAudience: "iam-service", IAMHTTPClient: iamServer.Client(),
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "gateway-policy-1",
			DelegationSigner: signer, DelegationTTL: 30 * time.Second,
		}, now: func() time.Time { return now }},
		registry: newRegistryController(&RegistryConfig{CoreBaseURL: registryServer.URL, CoreHTTPClient: registryServer.Client(), CoreTimeout: time.Second}),
		alarm:    newAlarmController(&AlarmConfig{BackendBaseURL: alarmServer.URL, BackendHTTPClient: alarmServer.Client(), BackendAudience: "alarm-service"}),
	}
	return fixture
}

func validGatewayAlarm(siteID string) alarmmodel.Alarm {
	return alarmmodel.Alarm{
		SchemaVersion: 1, AlarmID: gatewayAlarmID, OrganizationID: gatewayAlarmOrganizationID, SiteID: siteID,
		SourceType: alarmmodel.SourceSiteRule, SourceReference: "rule:gateway-alarm:v1", Title: "Gateway Alarm", Summary: "Alarm read certification",
		Severity: alarmmodel.SeverityMajor, Status: alarmmodel.StatusOpen, OccurrenceCount: 1,
		FirstOccurredAt: "2026-08-01T00:00:00Z", LastOccurredAt: "2026-08-01T00:00:00Z",
		Evidence:    []alarmmodel.EvidenceReference{{Kind: "RULE", Reference: "rule:gateway-alarm:v1", CapturedAt: "2026-08-01T00:00:00Z"}},
		Transitions: []alarmmodel.Transition{{ToStatus: alarmmodel.StatusOpen, Operation: alarmmodel.OperationPublish, Reason: "published", ActorType: "SYSTEM", OccurredAt: "2026-08-01T00:00:00Z", Version: 1}},
		Version:     1, CreatedAt: "2026-08-01T00:00:00Z", UpdatedAt: "2026-08-01T00:00:00Z",
	}
}
