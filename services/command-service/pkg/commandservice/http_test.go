package commandservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type fakeHTTPAuthority struct {
	submitted commandmodel.SubmitRequest
	approved  commandmodel.ApproveRequest
	intent    commandmodel.CommandIntent
	getOrg    string
	getID     string
	err       error
}

func (authority *fakeHTTPAuthority) Submit(_ context.Context, request commandmodel.SubmitRequest) (SubmitResult, error) {
	authority.submitted = request
	if authority.err != nil {
		return SubmitResult{}, authority.err
	}
	intent := authority.intent
	if intent.ID == "" {
		intent = commandmodel.CommandIntent{
			ID: "command-1", DeviceID: request.DeviceID, PointID: request.PointID, Capability: request.Capability,
			CapabilityRevision: setpointCapabilityRevision, Parameters: cloneParameters(request.Parameters), VerificationPointKey: request.VerificationPointKey, Status: commandmodel.IntentQueued,
			Risk: commandmodel.RiskLow, ApprovalPolicy: commandmodel.ApprovalNone,
			DeviceCommandSequence: 1, Version: 3, SnapshotRevision: request.CurrentState.BusinessRevision,
			CreatedAt: time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC),
		}
	}
	return SubmitResult{Intent: intent}, nil
}

func (authority *fakeHTTPAuthority) Get(_ context.Context, organizationID, commandID string) (commandmodel.CommandIntent, error) {
	authority.getOrg, authority.getID = organizationID, commandID
	if authority.err != nil {
		return commandmodel.CommandIntent{}, authority.err
	}
	return authority.intent, nil
}

func (authority *fakeHTTPAuthority) Approve(_ context.Context, request commandmodel.ApproveRequest) (commandmodel.CommandIntent, error) {
	authority.approved = request
	if authority.err != nil {
		return commandmodel.CommandIntent{}, authority.err
	}
	intent := authority.intent
	intent.Approvals = append(intent.Approvals, request.Approval)
	intent.Status = commandmodel.IntentQueued
	intent.Version++
	intent.UpdatedAt = request.Approval.IssuedAt
	return intent, nil
}

func TestCommandHTTPCreateRequiresExactIAMGrant(t *testing.T) {
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	iamSigner := newECDSASigner(t)
	gatewaySigner := newECDSASigner(t)
	authority := &fakeHTTPAuthority{}
	handler := newCommandHTTPTestHandler(t, authority, iamSigner, gatewaySigner, now)
	grant := signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandSubmit, "principal-1", "device-1")
	body := internalCreateCommandRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1", PrincipalID: "principal-1",
		IdempotencyKey: "idempotency-1", Capability: commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: internalCurrentState{
			EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD",
			BusinessRevision: 17, CurrentValue: testFloat64Pointer(23), ObservedAt: now.Add(-time.Second),
		},
	}
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, InternalCommandsPath, bytes.NewReader(encoded))
	request.Header.Set(commandGrantHeader, grant)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authority.submitted.TenantID != "tenant-1" || authority.submitted.Authorization.Purpose != commandmodel.AuthorizationCommandSubmit || authority.submitted.Authorization.PrincipalID != "principal-1" {
		t.Fatalf("unexpected submitted request %#v", authority.submitted)
	}
	if recorder.Header().Get("Location") != "/api/v1/commands/command-1" {
		t.Fatalf("location=%s", recorder.Header().Get("Location"))
	}
}

func TestCommandHTTPCreateRejectsApprovalGrantAndScopeDrift(t *testing.T) {
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	iamSigner := newECDSASigner(t)
	gatewaySigner := newECDSASigner(t)
	handler := newCommandHTTPTestHandler(t, &fakeHTTPAuthority{}, iamSigner, gatewaySigner, now)
	body := internalCreateCommandRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1", PrincipalID: "principal-1",
		IdempotencyKey: "idempotency-1", Capability: commandmodel.CapabilitySetTemperatureSetpoint,
		Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, VerificationPointKey: "zone.temperature_setpoint",
		CurrentState: internalCurrentState{EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Quality: "GOOD", BusinessRevision: 17, CurrentValue: testFloat64Pointer(23), ObservedAt: now},
	}
	encoded, _ := json.Marshal(body)
	for _, testCase := range []struct {
		name  string
		grant string
	}{
		{name: "approval purpose", grant: signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandApprove, "principal-1", "device-1")},
		{name: "device drift", grant: signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandSubmit, "principal-1", "device-other")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, InternalCommandsPath, bytes.NewReader(encoded))
			request.Header.Set(commandGrantHeader, testCase.grant)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCommandHTTPApprovalUsesExactGrantAndServerDerivedEvidence(t *testing.T) {
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	iamSigner := newECDSASigner(t)
	gatewaySigner := newECDSASigner(t)
	authority := &fakeHTTPAuthority{intent: commandmodel.CommandIntent{
		ID: "command-1", TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1", PointID: "point-1", PrincipalID: "initiator-1",
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, CapabilityRevision: setpointCapabilityRevision,
		Status: commandmodel.IntentAwaitingApproval, Risk: commandmodel.RiskMedium,
		RiskSnapshot:   commandmodel.RiskSnapshot{Level: commandmodel.RiskMedium, RuleRevision: "risk-v1", EvaluatedAt: now},
		ApprovalPolicy: commandmodel.ApprovalSingleApprover, Parameters: commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 24}, VerificationPointKey: "zone.temperature_setpoint", PayloadHash: "payload-hash",
		DeviceCommandSequence: 2, Version: 3, SnapshotRevision: 18, CreatedAt: now, UpdatedAt: now,
	}}
	handler := newCommandHTTPTestHandler(t, authority, iamSigner, gatewaySigner, now)
	body, _ := json.Marshal(internalApproveCommandRequest{
		TenantID: "tenant-1", SiteID: "site-1", DeviceID: "device-1",
		PrincipalID: "approver-2", ApproverRole: "operations-approver",
	})
	request := httptest.NewRequest(http.MethodPost, InternalCommandsPath+"/command-1/approve", bytes.NewReader(body))
	request.Header.Set(commandGrantHeader, signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandApprove, "approver-2", "device-1"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	approval := authority.approved.Approval
	if authority.approved.TenantID != "tenant-1" || authority.approved.CommandID != "command-1" ||
		approval.ApproverID != "approver-2" || approval.ApproverRole != "operations-approver" ||
		approval.Policy != commandmodel.ApprovalSingleApprover || approval.PayloadHash != "payload-hash" ||
		approval.Risk != commandmodel.RiskMedium || approval.RiskRuleRevision != "risk-v1" ||
		approval.Authorization.Purpose != commandmodel.AuthorizationCommandApprove {
		t.Fatalf("approval evidence drifted %#v", authority.approved)
	}

	wrongPurpose := httptest.NewRequest(http.MethodPost, InternalCommandsPath+"/command-1/approve", bytes.NewReader(body))
	wrongPurpose.Header.Set(commandGrantHeader, signCommandTestGrant(t, iamSigner, now, commandmodel.AuthorizationCommandSubmit, "approver-2", "device-1"))
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrongPurpose)
	if wrongRecorder.Code != http.StatusForbidden {
		t.Fatalf("submit grant approved command status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
}

func TestCommandHTTPReadRequiresSignedOrganizationAndCommandScopes(t *testing.T) {
	now := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	iamSigner := newECDSASigner(t)
	gatewaySigner := newECDSASigner(t)
	authority := &fakeHTTPAuthority{intent: commandmodel.CommandIntent{
		ID: "command-1", DeviceID: "device-1", PointID: "point-1", Capability: commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision: setpointCapabilityRevision, Status: commandmodel.IntentAwaitingApproval,
		Risk: commandmodel.RiskMedium, ApprovalPolicy: commandmodel.ApprovalSingleApprover,
		DeviceCommandSequence: 2, Version: 3, SnapshotRevision: 18, CreatedAt: now, UpdatedAt: now,
	}}
	handler := newCommandHTTPTestHandler(t, authority, iamSigner, gatewaySigner, now)
	readContext, err := identitycontext.SignDelegation(gatewaySigner, identitycontext.DelegationClaims{
		Issuer: "spiffe://hvac.local/platform-gateway", ExecutingService: "spiffe://hvac.local/platform-gateway",
		Subject: "user-1", SubjectIssuer: "https://issuer.example.test", Audience: "command-service",
		TenantID: "org-1", Actions: []string{"command:read"},
		Scopes: []string{"tenant:org-1", "command:command-1"}, PolicyRevision: "policy-1",
		SessionID: "session-1", TokenID: "read-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, InternalCommandsPath+"/command-1", nil)
	request.Header.Set(tenantHeader, "org-1")
	request.Header.Set(commandReadContextHeader, readContext)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authority.getOrg != "org-1" || authority.getID != "command-1" {
		t.Fatalf("unexpected authority read %s %s", authority.getOrg, authority.getID)
	}

	forged := httptest.NewRequest(http.MethodGet, InternalCommandsPath+"/command-1", nil)
	forged.Header.Set(tenantHeader, "org-2")
	forged.Header.Set(commandReadContextHeader, readContext)
	forgedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusForbidden {
		t.Fatalf("forged status=%d body=%s", forgedRecorder.Code, forgedRecorder.Body.String())
	}
}

func newCommandHTTPTestHandler(t *testing.T, authority HTTPAuthority, iamSigner, gatewaySigner *ecdsa.PrivateKey, now time.Time) *HTTPHandler {
	t.Helper()
	handler, err := NewHTTPHandler(HTTPConfig{
		Authority: authority, CommandGrantPublicKey: &iamSigner.PublicKey,
		CommandGrantIssuer: "spiffe://hvac.local/iam-service", GatewaySPIFFE: "spiffe://hvac.local/platform-gateway",
		CommandGrantAudience: "command-service", GatewayDelegationPublicKey: &gatewaySigner.PublicKey,
		GatewayReadAudience: "command-service", Now: func() time.Time { return now },
		CommandGrantUseChecker: func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
			return commandauth.UseStatus{CurrentPolicyRevision: claims.PolicyRevision, CurrentRevocationRevision: claims.EmergencyRevocationRevision}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func signCommandTestGrant(t *testing.T, signer *ecdsa.PrivateKey, now time.Time, purpose commandmodel.AuthorizationPurpose, principalID, deviceID string) string {
	t.Helper()
	grant, err := commandauth.SignGrant(signer, commandauth.GrantClaims{
		Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
		GrantID: "grant-1", Purpose: purpose, PrincipalID: principalID, TenantID: "tenant-1", SiteID: "site-1", DeviceID: deviceID,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint, MaximumRisk: commandmodel.RiskHigh,
		CapabilityRevision: setpointCapabilityRevision, PolicyRevision: "command-policy-1", EmergencyRevocationRevision: 2,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(25 * time.Second).Unix(), TokenID: "grant-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func newECDSASigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

var _ HTTPAuthority = (*fakeHTTPAuthority)(nil)
var _ = errors.Is
