package gateway

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

const ownerCallTenantID = "01910000-0000-7000-8000-000000000001"

type ownerCallRecord struct {
	decided, served, validated int
	serviceContext             string
}

func newOwnerCallHandler(t *testing.T) (*handler, *ecdsa.PublicKey) {
	t.Helper()
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	signer := commandTestSigner(t)
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: signer,
			TokenEncryptionKey: make([]byte, 32), SessionStore: sessionstore.NewMemoryStore(), SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
		},
	}).(*handler)
	return configured, &signer.PublicKey
}

func ownerCallCaller() ownerCaller {
	return ownerCaller{
		session: bffSession{Session: sessionstore.Session{
			ID: "owner-call-session", TenantID: ownerCallTenantID, ExpiresAt: time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC),
			Principal: identitycontext.UserPrincipal{Subject: "operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		}},
		presenter: "spiffe://hvac.local/operations-agent-service",
	}
}

func ownerCallScope() ownerScope {
	return ownerScope{tenantID: ownerCallTenantID, siteID: gatewayAlarmSiteID, resourceKind: "alarm", resourceID: gatewayAlarmID}
}

// testOwner answers with status and body; deny and invalid make its decision and contract fail.
func testOwner(record *ownerCallRecord, status int, body string, deny bool, invalid bool) domainOwner[string] {
	return domainOwner[string]{
		name: "Test Service", audience: "test-service",
		decide: func(_ context.Context, _ ownerCaller, _ string, _ ownerScope) (ownerDecision, *ownerFailure) {
			record.decided++
			if deny {
				return ownerDecision{}, &ownerFailure{status: http.StatusForbidden, code: "FORBIDDEN"}
			}
			return ownerDecision{principalID: "01910000-5000-7000-8000-000000000001", policyRevision: "test-policy-7"}, nil
		},
		serve: func(_ context.Context, _ string, serviceContext string) (ownerResponse, *ownerFailure) {
			record.served++
			record.serviceContext = serviceContext
			return ownerResponse{status: status, body: []byte(body)}, nil
		},
		validate: func(_ ownerScope, request string, response ownerResponse) (any, error) {
			record.validated++
			if invalid {
				return nil, errors.New("outside the contract")
			}
			return request + ":" + string(response.body), nil
		},
	}
}

func TestCallOwnerSignsTheDecidedActionAndScopeForTheOwner(t *testing.T) {
	h, key := newOwnerCallHandler(t)
	record := &ownerCallRecord{}
	response, failure := CallOwner(context.Background(), h, ownerCallCaller(), testOwner(record, http.StatusOK, "answer", false, false), "alarm:ack", ownerCallScope(), "request")
	if failure != nil || response.value != "request:answer" || record.decided != 1 || record.served != 1 || record.validated != 1 {
		t.Fatalf("response=%+v failure=%+v record=%+v", response, failure, record)
	}
	claims, err := identitycontext.VerifyDelegation(key, record.serviceContext)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Audience != "test-service" || claims.ExecutingService != "spiffe://hvac.local/operations-agent-service" ||
		claims.Issuer != "spiffe://hvac.local/platform-gateway" || claims.TenantID != ownerCallTenantID ||
		strings.Join(claims.Actions, ",") != "alarm:ack" ||
		strings.Join(claims.Scopes, ",") != "tenant:"+ownerCallTenantID+",site:"+gatewayAlarmSiteID+",alarm:"+gatewayAlarmID ||
		claims.PolicyRevision != "test-policy-7" || claims.PrincipalID != "01910000-5000-7000-8000-000000000001" ||
		claims.Subject != "operator" || claims.SessionID != "owner-call-session" {
		t.Fatalf("signed context does not carry the decision: %+v", claims)
	}
}

func TestCallOwnerStopsAtADenialBeforeTheOwner(t *testing.T) {
	h, _ := newOwnerCallHandler(t)
	record := &ownerCallRecord{}
	_, failure := CallOwner(context.Background(), h, ownerCallCaller(), testOwner(record, http.StatusOK, "answer", true, false), "alarm:read", ownerCallScope(), "request")
	if failure == nil || failure.status != http.StatusForbidden || record.served != 0 {
		t.Fatalf("failure=%+v record=%+v", failure, record)
	}
}

func TestCallOwnerRefusesAScopeOutsideTheCallersTenant(t *testing.T) {
	h, _ := newOwnerCallHandler(t)
	record := &ownerCallRecord{}
	scope := ownerCallScope()
	scope.tenantID = "01910000-0000-7000-8000-0000000000ff"
	_, failure := CallOwner(context.Background(), h, ownerCallCaller(), testOwner(record, http.StatusOK, "answer", false, false), "alarm:read", scope, "request")
	if failure == nil || failure.status != http.StatusServiceUnavailable || record.decided != 0 || record.served != 0 {
		t.Fatalf("failure=%+v record=%+v", failure, record)
	}
}

func TestCallOwnerWithholdsAnAnswerOutsideTheContract(t *testing.T) {
	h, _ := newOwnerCallHandler(t)
	record := &ownerCallRecord{}
	response, failure := CallOwner(context.Background(), h, ownerCallCaller(), testOwner(record, http.StatusOK, "secret-owner-body", false, true), "alarm:read", ownerCallScope(), "request")
	if failure == nil || failure.status != http.StatusServiceUnavailable || failure.code != "INTERNAL_ERROR" || !failure.retryable ||
		strings.Contains(failure.detail, "secret-owner-body") || response.body != nil {
		t.Fatalf("response=%+v failure=%+v", response, failure)
	}
}

func TestCallOwnerReturnsAnOwnerProblemUnvalidated(t *testing.T) {
	h, _ := newOwnerCallHandler(t)
	record := &ownerCallRecord{}
	response, failure := CallOwner(context.Background(), h, ownerCallCaller(), testOwner(record, http.StatusConflict, `{"code":"ALARM_VERSION_CONFLICT"}`, false, true), "alarm:assign", ownerCallScope(), "request")
	if failure != nil || response.status != http.StatusConflict || record.validated != 0 || response.value != nil {
		t.Fatalf("response=%+v failure=%+v record=%+v", response, failure, record)
	}
}
