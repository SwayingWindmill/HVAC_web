package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestGatewayCreateCommandDerivesAuthorityAndCurrentState(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":23.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-1")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var view commandView
	if json.NewDecoder(recorder.Body).Decode(&view) != nil || view.CommandID == "" ||
		view.TenantID != fixture.tenantID || view.SiteID != fixture.siteID || view.SnapshotRevision != 17 {
		t.Fatalf("unexpected view %#v", view)
	}
	if recorder.Header().Get("Location") != "/api/v1/commands/"+view.CommandID {
		t.Fatalf("location=%s", recorder.Header().Get("Location"))
	}
	if fixture.commandCalls.Load() != 1 || fixture.control.IAM.CommandAuthorizeCalls != 1 {
		t.Fatalf("calls command=%d iamGrant=%d", fixture.commandCalls.Load(), fixture.control.IAM.CommandAuthorizeCalls)
	}
}

func TestGatewayCreateCommandFailsBeforeUpstreamsWithoutCSRF(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":23.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, false)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-2")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("upstream called before CSRF validation")
	}
}

func TestGatewayCreateCommandUsesReportedSettingAsFeedback(t *testing.T) {
	fixture := newCommandGatewayFixture(t, "SETTING")
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":23.5}}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "reported-setpoint-command")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("reported setpoint feedback rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.feedbackPointID+`","parameters":{"setpointC":23.5}}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "direct-reported-setpoint")
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || fixture.control.IAM.CommandAuthorizeCalls != 1 {
		t.Fatalf("reported SETTING became a command target: status=%d iamGrants=%d", recorder.Code, fixture.control.IAM.CommandAuthorizeCalls)
	}
}

func TestGatewayRejectsBrowserCommandAuthorityHeaders(t *testing.T) {
	for _, header := range []string{"X-Command-Grant", "X-Command-Read-Context", "X-Command-Control-Context", "X-Acting-Organization-ID"} {
		t.Run(header, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(`{}`))
			fixture.authenticate(request, true)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "command-request-forged")
			request.Header.Set(header, "caller-supplied")
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if fixture.totalUpstreamCalls() != 0 {
				t.Fatalf("caller-supplied header %s reached upstream", header)
			}
		})
	}
}

func TestGatewayRejectsBrowserForbiddenCommandBodyFields(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "tenantId", body: `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"tenantId":"` + fixture.tenantID + `"}`},
		{name: "providerMethod", body: `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"providerMethod":"rpc.set"}`},
		{name: "providerParams", body: `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"providerParams":{"x":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(tc.body))
			fixture.authenticate(request, true)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "forbid-field-"+tc.name)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || fixture.totalUpstreamCalls() != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, fixture.totalUpstreamCalls(), recorder.Body.String())
			}
		})
	}
}

func TestGatewayUnsafeCurrentStateStopsBeforeCommandAuthorization(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	fixture.setFeedbackObservation(&commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE",
		Presence:               "ONLINE",
		Readiness:              "CURRENT",
		Freshness:              "STALE",
		Quality:                "STALE",
		BusinessRevision:       17,
		ObservedAt:             fixture.now,
	})
	body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":23.5}}`
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "command-request-4")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.control.IAM.CommandAuthorizeCalls != 0 || fixture.identityCalls.Load() != 0 {
		t.Fatalf("expected 0 IAM command grant / identity calls on unsafe state, got grant=%d identity=%d",
			fixture.control.IAM.CommandAuthorizeCalls, fixture.identityCalls.Load())
	}
}

func TestGatewayGetCommandUsesScopedReadContext(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitMediumCommand(t, "get-cmd-seed")
	fixture.commandCalls.Store(0)

	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"/"+cmdID, nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.commandCalls.Load() != 1 || fixture.iamCalls.Load() != 0 || fixture.identityCalls.Load() != 0 {
		t.Fatalf("unexpected calls on get command: command=%d iam=%d identity=%d",
			fixture.commandCalls.Load(), fixture.iamCalls.Load(), fixture.identityCalls.Load())
	}
	claims, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, fixture.lastControlContext)
	if err != nil {
		t.Fatalf("verify X-Command-Control-Context: %v", err)
	}
	if claims.TenantID != fixture.tenantID ||
		!commandTestContains(claims.Actions, commandservice.OperatorControlContextAction) ||
		!commandTestContains(claims.Scopes, "tenant:"+fixture.tenantID) ||
		!commandTestContains(claims.Scopes, "session:"+fixture.sessionID) {
		t.Fatalf("unexpected delegation claims: %+v", claims)
	}
}

func TestGatewayListCommandsReadsAVisibleSitesCommands(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitMediumCommand(t, "list-cmd-seed")

	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId="+fixture.siteID+"&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data []commandView `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].CommandID != cmdID {
		t.Fatalf("list body=%s err=%v", recorder.Body.String(), err)
	}
}

func TestGatewayListCommandsHidesASiteOutsideTheSession(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId=018f3e00-2000-7000-8000-000000000002&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || len(fixture.control.Twin.Models) == 0 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayListCommandsRejectsCrossTenantProjection(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	_ = fixture.mustSubmitMediumCommand(t, "cross-tenant-list-seed")
	fixture.crossTenantView.Store(true)

	request := httptest.NewRequest(http.MethodGet, publicCommandsPath+"?siteId="+fixture.siteID+"&limit=20", nil)
	fixture.authenticate(request, false)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for cross-tenant projection, got status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayApprovalRoutesAndStepUpEnforcement(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitMediumCommand(t, "approve-medium-seed")
	fixture.control.IAM.PrincipalID = "018f3e00-5000-7000-8000-000000000002"

	// Approve medium-risk awaiting command with empty `{}` from another operator succeeds and moves to QUEUED.
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve medium status=%d body=%s", rec.Code, rec.Body.String())
	}
	var approved commandView
	if json.NewDecoder(rec.Body).Decode(&approved) != nil || approved.Status != commandmodel.IntentQueued || approved.ApprovalCount != 1 {
		t.Fatalf("unexpected approved view: %#v", approved)
	}
}

func TestGatewayHighRiskApprovalRequiresStepUpBeforeAuthorizationUpstreams(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitMediumCommand(t, "approve-high-seed")
	fixture.control.Store.SetAwaitingHighRiskApprovalForTest(cmdID)
	fixture.control.IAM.PrincipalID = "018f3e00-5000-7000-8000-000000000002"
	fixture.control.IAM.CommandAuthorizeCalls = 0

	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil || problem.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("unexpected step-up problem %#v err=%v", problem, err)
	}
	if fixture.control.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("high-risk step-up refusal must consume 0 IAM command grants, got %d", fixture.control.IAM.CommandAuthorizeCalls)
	}

	// Retry with fresh session MFA assurance succeeds.
	fixture.setSessionMFA(t)
	reqMFA := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(reqMFA, true)
	reqMFA.Header.Set("Content-Type", "application/json")
	recMFA := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recMFA, reqMFA)
	if recMFA.Code != http.StatusOK {
		t.Fatalf("fresh MFA approval failed: status=%d body=%s", recMFA.Code, recMFA.Body.String())
	}
}

func TestGatewayApprovalRejectsBrowserAuthorityBeforeUpstreams(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{"principalId":"caller-supplied"}`))
	fixture.authenticate(request, true)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("browser approval authority reached an upstream")
	}
}

func TestGatewayApprovalRequiresCSRFBeforeCommandRead(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	request := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+fixture.commandID+"/approve", strings.NewReader(`{}`))
	fixture.authenticate(request, false)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if fixture.totalUpstreamCalls() != 0 {
		t.Fatal("approval read occurred before CSRF validation")
	}
}

func TestGatewayReconcileCommandRoutesAndValidatesAuthority(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitLowCommand(t, "reconcile-seed")
	env, err := fixture.control.Store.ClaimDispatch(context.Background(), fixture.tenantID, "dispatcher-1", 30*time.Second)
	if err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}
	if err := fixture.control.Store.ResolveDispatch(context.Background(), env, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorRequestCommitted, FailureCode: "ACK_TIMEOUT", EvidenceID: "ev-timeout",
	}); err != nil {
		t.Fatalf("resolve dispatch timeout: %v", err)
	}

	// Reject browser-supplied authority fields before calling upstream.
	forgedReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/reconcile", strings.NewReader(`{"outcome":"APPLIED","principalId":"caller-supplied"}`))
	fixture.authenticate(forgedReq, true)
	forgedReq.Header.Set("Content-Type", "application/json")
	forgedRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(forgedRec, forgedReq)
	if forgedRec.Code != http.StatusBadRequest || fixture.commandCalls.Load() != 1 {
		t.Fatalf("forged reconcile status=%d calls=%d body=%s", forgedRec.Code, fixture.commandCalls.Load(), forgedRec.Body.String())
	}

	fixture.control.IAM.CommandAuthorizeCalls = 0
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/reconcile", strings.NewReader(`{"outcome":"APPLIED"}`))
	fixture.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fixture.control.IAM.CommandAuthorizeCalls != 1 {
		t.Fatalf("reconcile status=%d grants=%d body=%s", rec.Code, fixture.control.IAM.CommandAuthorizeCalls, rec.Body.String())
	}
	var view commandView
	if json.NewDecoder(rec.Body).Decode(&view) != nil || view.Status != commandmodel.IntentSucceeded {
		t.Fatalf("unexpected reconciled view: %#v", view)
	}
}

func TestGatewayHighRiskSubmitRequiresValidTOTP(t *testing.T) {
	t.Run("missing or malformed code fails with 428 before IAM command grant", func(t *testing.T) {
		for _, code := range []string{"", "12345", "abcdef"} {
			fixture := newCommandGatewayFixture(t)
			fixture.setCommandPointCapability(commandmodel.CapabilityStart, "", nil)
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}`
			if code != "" {
				body += `,"verificationCode":"` + code + `"`
			}
			body += `}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "missing-totp-"+code)
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusPreconditionRequired {
				t.Fatalf("code=%q status=%d body=%s", code, rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "STEP_UP_REQUIRED" {
				t.Fatalf("unexpected problem %#v", problem)
			}
			if fixture.identityCalls.Load() != 0 || fixture.control.IAM.CommandAuthorizeCalls != 0 {
				t.Fatalf("expected 0 identity/grant calls, got identity=%d grant=%d", fixture.identityCalls.Load(), fixture.control.IAM.CommandAuthorizeCalls)
			}
		}
	})

	t.Run("locked asset rejects 409 before 428 when code omitted", func(t *testing.T) {
		fixture := newCommandGatewayFixture(t)
		fixture.setCommandPointCapability(commandmodel.CapabilityStart, "", nil)
		fixture.control.Twin.ControlStates[fixture.siteID] = commandservice.SiteControlStateView{
			TenantID: fixture.tenantID, SiteID: fixture.siteID, ControlMode: siteControlModeRemoteAuto,
			AssetLocks: []assetControlLockView{{AssetID: fixture.assetID, SiteID: fixture.siteID, LockMode: assetControlLockMaintenance}},
		}
		body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}}`
		req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
		fixture.authenticate(req, true)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "locked-before-stepup")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, req)
		var problem platformapi.ProblemDetails
		if rec.Code != http.StatusConflict || json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CONTROL_LOCKED" {
			t.Fatalf("expected 409 COMMAND_CONTROL_LOCKED before step-up, got %d body=%s", rec.Code, rec.Body.String())
		}
		if fixture.identityCalls.Load() != 0 || fixture.control.IAM.CommandAuthorizeCalls != 0 {
			t.Fatalf("expected 0 identity/grant calls, got identity=%d grant=%d", fixture.identityCalls.Load(), fixture.control.IAM.CommandAuthorizeCalls)
		}
	})

	t.Run("identity rejections map to 428 STEP_UP_REQUIRED without reaching command backend", func(t *testing.T) {
		cases := []struct {
			name   string
			status int
			errKey string
		}{
			{name: "wrong_code", status: http.StatusUnauthorized, errKey: "mfa_code_invalid"},
			{name: "replayed_code", status: http.StatusConflict, errKey: "mfa_code_replayed"},
			{name: "mfa_not_configured", status: http.StatusPreconditionFailed, errKey: "mfa_not_configured"},
			{name: "mfa_locked", status: http.StatusLocked, errKey: "mfa_locked"},
		}
		for _, tc := range cases {
			fixture := newCommandGatewayFixture(t)
			fixture.setCommandPointCapability(commandmodel.CapabilityStartUnitSequence, "", nil)
			fixture.totpResponder = func(code string) (int, map[string]any) {
				return tc.status, map[string]any{"error": tc.errKey}
			}
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"654321"}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "totp-"+tc.name)
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusPreconditionRequired {
				t.Fatalf("%s status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "STEP_UP_REQUIRED" {
				t.Fatalf("%s unexpected problem %#v", tc.name, problem)
			}
			if fixture.identityCalls.Load() != 1 || fixture.commandCalls.Load() != 0 {
				t.Fatalf("%s calls identity=%d command=%d", tc.name, fixture.identityCalls.Load(), fixture.commandCalls.Load())
			}
		}
	})

	t.Run("valid TOTP submits high-risk discrete and large-delta setpoint commands", func(t *testing.T) {
		for _, capability := range []commandmodel.Capability{
			commandmodel.CapabilityStart,
			commandmodel.CapabilityStop,
			commandmodel.CapabilityStartUnitSequence,
			commandmodel.CapabilityStopUnitSequence,
		} {
			fixture := newCommandGatewayFixture(t)
			fixture.setCommandPointCapability(capability, "", nil)
			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "totp-ok-"+strings.ToLower(string(capability)))
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("%s status=%d body=%s", capability, rec.Code, rec.Body.String())
			}
		}

		fixture := newCommandGatewayFixture(t)
		withCodeReq := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(
			`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":25.5},"verificationCode":"123456"}`,
		))
		fixture.authenticate(withCodeReq, true)
		withCodeReq.Header.Set("Content-Type", "application/json")
		withCodeReq.Header.Set("Idempotency-Key", "high-delta-with-totp")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, withCodeReq)
		if rec.Code != http.StatusAccepted || fixture.identityCalls.Load() != 1 || fixture.commandCalls.Load() != 1 {
			t.Fatalf("high delta with totp status=%d identity=%d command=%d body=%s", rec.Code, fixture.identityCalls.Load(), fixture.commandCalls.Load(), rec.Body.String())
		}
	})
}

func TestGatewayCancelCommandRoutesAndEnforcesLifecycleAndAuthority(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	cmdID := fixture.mustSubmitMediumCommand(t, "cancel-awaiting-seed")

	// Valid empty-body cancel of an AWAITING_APPROVAL command succeeds.
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/cancel", nil)
	fixture.authenticate(req, true)
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fixture.control.IAM.CommandAuthorizeCalls != 2 {
		t.Fatalf("cancel awaiting failed: status=%d grants=%d body=%s", rec.Code, fixture.control.IAM.CommandAuthorizeCalls, rec.Body.String())
	}
	var view commandView
	if json.NewDecoder(rec.Body).Decode(&view) != nil || view.Status != commandmodel.IntentCancelled {
		t.Fatalf("unexpected cancelled view %#v", view)
	}

	// Browser authority fields in cancel JSON body are rejected with 400 before upstreams.
	badBodyReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+cmdID+"/cancel", strings.NewReader(`{"principalId":"spoofed"}`))
	fixture.authenticate(badBodyReq, true)
	badBodyReq.Header.Set("Content-Type", "application/json")
	badBodyRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(badBodyRec, badBodyReq)
	if badBodyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on browser authority field in cancel, got %d body=%s", badBodyRec.Code, badBodyRec.Body.String())
	}

	// DISPATCHING commands return 409 COMMAND_CANCEL_INVALID before minting an IAM grant.
	dispFixture := newCommandGatewayFixture(t)
	dispID := dispFixture.mustSubmitLowCommand(t, "cancel-disp-seed")
	if _, err := dispFixture.control.Store.ClaimDispatch(context.Background(), dispFixture.tenantID, "dispatcher-1", 30*time.Second); err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}
	dispFixture.control.IAM.CommandAuthorizeCalls = 0
	dispReq := httptest.NewRequest(http.MethodPost, publicCommandsPath+"/"+dispID+"/cancel", strings.NewReader(`{"reason":"TOO_LATE"}`))
	dispFixture.authenticate(dispReq, true)
	dispReq.Header.Set("Content-Type", "application/json")
	dispRec := httptest.NewRecorder()
	dispFixture.handler.ServeHTTP(dispRec, dispReq)
	if dispRec.Code != http.StatusConflict || dispFixture.control.IAM.CommandAuthorizeCalls != 0 {
		t.Fatalf("expected 409 COMMAND_CANCEL_INVALID with 0 iam grants, got %d grants=%d body=%s", dispRec.Code, dispFixture.control.IAM.CommandAuthorizeCalls, dispRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if json.NewDecoder(dispRec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_CANCEL_INVALID" {
		t.Fatalf("unexpected conflict problem %#v", problem)
	}
}

func TestGatewayRejectsEmergencyDoReleaseDirectCommand(t *testing.T) {
	for _, tc := range []struct {
		name          string
		actuationMode string
	}{
		{name: "exact", actuationMode: "EMERGENCY_DO_RELEASE"},
		{name: "trimmed_lowercase", actuationMode: " emergency_do_release "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCommandGatewayFixture(t)
			fixture.setCommandPointCapability(commandmodel.CapabilityStop, tc.actuationMode, nil)

			body := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
			req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(body))
			fixture.authenticate(req, true)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "cmd-block-emergency-do-"+tc.name)
			rec := httptest.NewRecorder()
			fixture.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			var problem platformapi.ProblemDetails
			if json.NewDecoder(rec.Body).Decode(&problem) != nil || problem.Code != "COMMAND_REQUEST_INVALID" ||
				!strings.Contains(problem.Detail, "/control-shutdown") {
				t.Fatalf("unexpected problem %#v", problem)
			}
			if fixture.control.IAM.CommandAuthorizeCalls != 0 {
				t.Fatalf("expected 0 IAM command grant calls, got %d", fixture.control.IAM.CommandAuthorizeCalls)
			}
		})
	}
}

func TestCommandRegistryDecisionUsesRegistryRouteOwnership(t *testing.T) {
	fixture := newCommandGatewayFixture(t)
	snapshot, err := ownershipregistry.Parse([]byte(`{
		"registryVersion":1,
		"registryRevision":49,
		"routes":[{
			"method":"GET",
			"path":"/api/v1/sites/{siteId}/control-state",
			"owner":"command-service",
			"publicIngress":"platform-gateway",
			"revision":2,
			"rollout":{"mode":"all"},
			"compatibilityMode":"native",
			"allowedScopeDimensions":["tenant","site","principal"]
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := snapshot.Resolve(http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", "")
	if err != nil || decision.SelectedOwner != ownershipregistry.OwnerCommand || decision.RouteRevision != 2 {
		t.Fatalf("unexpected ownership decision: %+v err=%v", decision, err)
	}
}

type commandGatewayFixture struct {
	mu                 sync.Mutex
	feedbackPointType  string
	commandCapability  commandmodel.Capability
	now                time.Time
	handler            *handler
	sessionID          string
	tenantID           string
	siteID             string
	assetID            string
	deviceID           string
	commandPointID     string
	feedbackPointID    string
	commandID          string
	principalID        string
	gatewaySigner      *ecdsa.PrivateKey
	iamSigner          *ecdsa.PrivateKey
	store              *sessionstore.MemoryStore
	control            *commandservice.TestControlHarness
	iamCalls           atomic.Int32
	registryCalls      atomic.Int32
	telemetryCalls     atomic.Int32
	commandCalls       atomic.Int32
	identityCalls      atomic.Int32
	crossTenantView    atomic.Bool
	totpResponder      func(code string) (int, map[string]any)
	lastControlContext string
}

func newCommandGatewayFixture(t *testing.T, feedbackTypes ...string) *commandGatewayFixture {
	t.Helper()
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	control, err := commandservice.NewControlAuthorityForTest(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	gatewaySigner := control.GatewaySigner.(*ecdsa.PrivateKey)
	fixture := &commandGatewayFixture{
		now:             now,
		tenantID:        "018f3d00-1000-7000-8000-000000000001",
		siteID:          "018f3e00-2000-7000-8000-000000000001",
		deviceID:        "018f3e00-3000-7000-8000-000000000001",
		commandID:       "50f5012a-fe6b-47d6-b3df-62ca69a98885",
		principalID:     control.IAM.PrincipalID,
		assetID:         "018f3e00-6000-7000-8000-000000000001",
		commandPointID:  "018f3e00-7000-7000-8000-000000000001",
		feedbackPointID: "018f3e00-8000-7000-8000-000000000001",
		gatewaySigner:   gatewaySigner,
		iamSigner:       control.GrantSigner.(*ecdsa.PrivateKey),
		control:         control,
	}
	if len(feedbackTypes) > 0 {
		fixture.feedbackPointType = feedbackTypes[0]
	}

	fixture.seedDefaultPlant()

	store := sessionstore.NewMemoryStore()
	fixture.store = store
	configured := NewHandler(Config{
		Now: func() time.Time { return now },
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: gatewaySigner,
			TokenEncryptionKey: make([]byte, 32), SessionStore: store, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.commandIAMClient(t), OIDCHTTPClient: fixture.commandIdentityClient(t),
		},
		Registry:  &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreTimeout: time.Second},
		Telemetry: &TelemetryConfig{RuntimeBaseURL: "https://telemetry.example.test", RuntimeAudience: "telemetry-runtime-service", Timeout: time.Second},
		Command:   &CommandConfig{BackendBaseURL: "https://command.example.test", BackendHTTPClient: fixture.commandBackendClient(t), BackendAudience: "command-service", IAMGrantIssuer: "spiffe://hvac.local/iam-service", Timeout: time.Second},
	}).(*handler)
	fixture.handler = configured
	csrfCiphertext, err := configured.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser,
		ID:     "command-session", Principal: identitycontext.UserPrincipal{Subject: "command-user", Issuer: "https://issuer.example.test", Roles: []string{"site-manager"}},
		TenantID: fixture.tenantID, CSRFTokenCiphertext: csrfCiphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "command-fixture",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *commandGatewayFixture) seedDefaultPlant() {
	spProfile, _ := commandmodel.CapabilityProfileFor(commandmodel.CapabilitySetTemperatureSetpoint)
	fbType := fixture.feedbackPointType
	if fbType == "" {
		fbType = "TELEMETRY"
	}
	ts := fixture.now.Format(time.RFC3339)
	fixture.control.IAM.AllowedSites = []string{fixture.siteID}
	fixture.control.Twin.BoundDevices[fixture.assetID] = []string{fixture.deviceID}
	asset := commandservice.AssetDTO{ID: fixture.assetID, TenantID: fixture.tenantID, SiteID: fixture.siteID, Code: "chiller-a", DisplayName: "Chiller A", AssetType: "CHILLER", Status: "ACTIVE"}
	fixture.control.Twin.Assets[fixture.assetID] = asset
	fixture.control.Twin.Models[fixture.siteID] = commandservice.SiteAssetModelDTO{
		SchemaVersion: 2,
		TenantID:      fixture.tenantID,
		SiteID:        fixture.siteID,
		Assets:        []commandservice.AssetDTO{asset},
		Devices:       []commandservice.DeviceDTO{{ID: fixture.deviceID, TenantID: fixture.tenantID, SiteID: fixture.siteID, Code: "ctrl-a", Status: "ACTIVE"}},
		TelemetryPoints: []commandservice.TelemetryPointDTO{
			{
				ID: fixture.feedbackPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
				PointCode: "zone_temperature", PointType: fbType, ValueType: "NUMBER", Status: "ACTIVE",
			},
			{
				ID: "018f3e00-8000-7000-8000-000000000002", TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
				PointCode: "run_state", PointType: "STATE", ValueType: "STRING", Status: "ACTIVE",
			},
			{
				ID: fixture.commandPointID, TenantID: fixture.tenantID, SiteID: fixture.siteID, ReportingDeviceID: fixture.deviceID,
				PointCode: "zone_temperature_setpoint", PointType: "COMMAND", ValueType: "NUMBER", Writable: true, Status: "ACTIVE",
				SourceMetadata: map[string]any{"capability": string(commandmodel.CapabilitySetTemperatureSetpoint), "capabilityRevision": spProfile.Revision, "feedbackPointKey": "zone_temperature"},
			},
		},
		Relationships: []commandservice.AssetRelationshipDTO{
			{ID: "rel-default", TenantID: fixture.tenantID, SiteID: fixture.siteID, FromType: "POINT", FromID: fixture.commandPointID, ToType: "ASSET", ToID: fixture.assetID, Role: "CONTROLS", Status: "ACTIVE", ValidFrom: ts},
		},
	}
	num := 23.0
	fixture.setFeedbackObservation(&commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 17, CurrentValue: &num, ReportedValue: commandmodel.NumberScalar(23.0), ObservedAt: fixture.now,
	})
	fixture.control.Live.Set(fixture.deviceID, "run_state", &commandmodel.LivePointObservation{
		EvaluationAvailability: "AVAILABLE", Presence: "ONLINE", Readiness: "CURRENT", Freshness: "FRESH", Quality: "GOOD",
		BusinessRevision: 18, ReportedValue: commandmodel.TextScalar("STOPPED"), ObservedAt: fixture.now,
	}, "s2:sha256:"+strings.Repeat("2", 64))
}

func (fixture *commandGatewayFixture) setFeedbackObservation(obs *commandmodel.LivePointObservation) {
	fixture.control.Live.Set(fixture.deviceID, "zone_temperature", obs, "s2:sha256:"+strings.Repeat("1", 64))
}

func (fixture *commandGatewayFixture) setCommandPointCapability(capability commandmodel.Capability, actuationMode string, interlocks []any) {
	profile, _ := commandmodel.CapabilityProfileFor(capability)
	model := fixture.control.Twin.Models[fixture.siteID]
	for i := range model.TelemetryPoints {
		if model.TelemetryPoints[i].ID == fixture.commandPointID {
			meta := map[string]any{
				"capability":         string(capability),
				"capabilityRevision": profile.Revision,
				"feedbackPointKey":   "run_state",
			}
			if actuationMode != "" {
				meta["actuationMode"] = actuationMode
			}
			if len(interlocks) > 0 {
				meta["interlockChecks"] = interlocks
			}
			model.TelemetryPoints[i].SourceMetadata = meta
		}
	}
	fixture.control.Twin.Models[fixture.siteID] = model
}

func (fixture *commandGatewayFixture) setSessionMFA(t *testing.T) {
	t.Helper()
	csrfCiphertext, err := fixture.handler.identity.encryptBytes([]byte(commandTestCSRFValue()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.store.CreateSession(context.Background(), sessionstore.Session{
		Client:              sessionstore.ClientBrowser,
		ID:                  "command-session-mfa",
		Principal:           identitycontext.UserPrincipal{Subject: "command-user", Issuer: "https://issuer.example.test", Roles: []string{"site-manager"}},
		TenantID:            fixture.tenantID,
		CSRFTokenCiphertext: csrfCiphertext,
		AuthenticationACR:   authenticationACRMFA,
		AuthenticationAMR:   []string{"pwd", "otp"},
		AuthenticationTime:  fixture.now.Add(-time.Minute),
		ExpiresAt:           fixture.now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "command-fixture-mfa",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: fixture.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
}

func (fixture *commandGatewayFixture) mustSubmitLowCommand(t *testing.T, key string) string {
	t.Helper()
	return fixture.mustSubmitSetpointCommand(t, key, 23.5)
}

func (fixture *commandGatewayFixture) mustSubmitMediumCommand(t *testing.T, key string) string {
	t.Helper()
	return fixture.mustSubmitSetpointCommand(t, key, 24.5)
}

func (fixture *commandGatewayFixture) mustSubmitSetpointCommand(t *testing.T, key string, value float64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, publicCommandsPath, strings.NewReader(
		`{"assetId":"`+fixture.assetID+`","commandPointId":"`+fixture.commandPointID+`","parameters":{"setpointC":`+jsonNumber(value)+`}}`,
	))
	fixture.authenticate(req, true)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("seed command failed (%s): status=%d body=%s", key, rec.Code, rec.Body.String())
	}
	var view commandView
	if json.NewDecoder(rec.Body).Decode(&view) != nil || view.CommandID == "" {
		t.Fatalf("decode seed command failed: %s", rec.Body.String())
	}
	return view.CommandID
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (fixture *commandGatewayFixture) totalUpstreamCalls() int32 {
	return fixture.iamCalls.Load() + fixture.registryCalls.Load() + fixture.telemetryCalls.Load() + fixture.commandCalls.Load() + fixture.identityCalls.Load()
}

func (fixture *commandGatewayFixture) authenticate(request *http.Request, includeCSRF bool) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	if includeCSRF {
		request.Header.Set("Origin", "https://web.example.test")
		request.Header.Set("X-CSRF-Token", commandTestCSRFValue())
	}
}

func commandTestCSRFValue() string {
	return strings.Join([]string{"command", "csrf", "fixture"}, "-")
}

func (fixture *commandGatewayFixture) commandIAMClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.iamCalls.Add(1)
		if request.URL.Path == "/internal/v1/registry/decision" {
			parent, err := identitycontext.VerifyDelegation(&fixture.gatewaySigner.PublicKey, request.Header.Get("X-Delegation-Grant"))
			if err != nil {
				return jsonTestResponse(http.StatusUnauthorized, map[string]any{"code": "IAM_DELEGATION_INVALID"}), nil
			}
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_REQUEST_INVALID"}), nil
			}
			switch input.Action {
			case registryauth.ActionAssetRead, registryauth.ActionAssetModelRead, registryauth.ActionDeviceRead, registryauth.ActionSiteRead, registryauth.ActionScheduleManage:
			default:
				return jsonTestResponse(http.StatusBadRequest, map[string]any{"code": "IAM_ACTION_INVALID"}), nil
			}
			now := fixture.now.UTC()
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: fixture.principalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID},
				Actions: []registryauth.Action{input.Action}, PolicyRevision: "registry-read:1/iam:1",
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return jsonTestResponse(http.StatusOK, registryauth.DecisionResponse{
				Decision:        decision,
				DelegationGrant: commandUnsignedRegistryGrant(claims),
			}), nil
		}
		return nil, context.DeadlineExceeded
	})}
}

func (fixture *commandGatewayFixture) commandIdentityClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.identityCalls.Add(1)
		if request.URL.Path != identityTOTPVerifyPath {
			return jsonTestResponse(http.StatusNotFound, map[string]any{"error": "not_found"}), nil
		}
		var payload struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			return jsonTestResponse(http.StatusBadRequest, map[string]any{"error": "invalid_request"}), nil
		}
		if fixture.totpResponder != nil {
			status, body := fixture.totpResponder(payload.Code)
			return jsonTestResponse(status, body), nil
		}
		if payload.Code == "123456" {
			return jsonTestResponse(http.StatusOK, map[string]any{"verified": true}), nil
		}
		return jsonTestResponse(http.StatusUnauthorized, map[string]any{"error": "mfa_code_invalid"}), nil
	})}
}

func (fixture *commandGatewayFixture) commandBackendClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture.commandCalls.Add(1)
		fixture.mu.Lock()
		fixture.lastControlContext = request.Header.Get(commandControlContextHeader)
		fixture.mu.Unlock()

		rec := httptest.NewRecorder()
		fixture.control.OperatorHandler.ServeHTTP(rec, request)
		res := rec.Result()
		if fixture.crossTenantView.Load() && res.StatusCode == http.StatusOK && strings.HasPrefix(request.URL.Path, internalOperatorCommandsPath) {
			raw, _ := io.ReadAll(res.Body)
			tampered := strings.ReplaceAll(string(raw), fixture.tenantID, "018f3d00-9999-7000-8000-999999999999")
			res.Body = io.NopCloser(strings.NewReader(tampered))
		}
		return res, nil
	})}
}

func jsonTestResponse(status int, value any) *http.Response {
	encoded, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(encoded)),
	}
}

func commandTestSigner(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func commandUnsignedRegistryGrant(claims registryauth.GrantClaims) string {
	claims.Version = registryauth.GrantVersion
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("test-proof"))
}

func commandTestContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
