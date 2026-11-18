package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

func newMiniProgramCommandFixture(t *testing.T) (*commandGatewayFixture, string) {
	t.Helper()
	fixture := newCommandGatewayFixture(t)
	snapshotRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatalf("read route-ownership.v1.json: %v", err)
	}
	snapshot, err := ownershipregistry.Parse(snapshotRaw)
	if err != nil {
		t.Fatalf("parse route-ownership.v1.json: %v", err)
	}
	now := fixture.now
	fixture.handler.routeManager = ownershipregistry.NewManager(snapshot, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now })

	miniToken := "mini-command-bearer-session-1"
	csrfCiphertext, err := fixture.handler.identity.encryptBytes([]byte("mini-command-csrf"))
	if err != nil {
		t.Fatalf("encrypt mini csrf: %v", err)
	}
	if _, err := fixture.store.CreateSession(context.Background(), sessionstore.Session{
		Client:              sessionstore.ClientMiniProgram,
		ID:                  miniToken,
		Principal:           identitycontext.UserPrincipal{Subject: "mini-operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID:            fixture.tenantID,
		CSRFTokenCiphertext: csrfCiphertext,
		ExpiresAt:           now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action:            "SESSION_CREATED",
		Result:            "SUCCEEDED",
		PolicyRevision:    "identity-policy-1",
		CorrelationID:     "mini-command-fixture",
		TraceID:           strings.Repeat("a", 32),
		Traceparent:       "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now,
	}); err != nil {
		t.Fatalf("create mini program session: %v", err)
	}
	return fixture, miniToken
}

func doMiniCommandRequest(h *handler, method, path, bearerToken, idempotencyKey, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMiniProgramBearerControlReadSubmitIdempotencyAndPolling(t *testing.T) {
	fixture, miniToken := newMiniProgramCommandFixture(t)

	// 1. Mini Program Bearer session reads site control-state without Cookie/Origin/CSRF.
	stateRec := doMiniCommandRequest(fixture.handler, http.MethodGet, "/api/v1/sites/"+fixture.siteID+"/control-state", miniToken, "", "")
	if stateRec.Code != http.StatusOK {
		t.Fatalf("GET control-state status=%d body=%s", stateRec.Code, stateRec.Body.String())
	}
	var state siteControlStateView
	if err := json.NewDecoder(stateRec.Body).Decode(&state); err != nil {
		t.Fatalf("decode control-state: %v", err)
	}
	if state.ControlMode != siteControlModeRemoteAuto || state.SiteID != fixture.siteID {
		t.Fatalf("unexpected control-state %#v", state)
	}

	// 2. Submit setpoint command via Bearer + Idempotency-Key (no Cookie, no Origin, no X-CSRF-Token).
	payload := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{"setpointC":23.5}}`
	cmdRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-setpoint-1", payload)
	if cmdRec.Code != http.StatusAccepted {
		t.Fatalf("POST command status=%d body=%s", cmdRec.Code, cmdRec.Body.String())
	}
	var created commandView
	if err := json.NewDecoder(cmdRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created command: %v", err)
	}
	if created.CommandID == "" || created.DeviceID != fixture.deviceID {
		t.Fatalf("unexpected created command %#v", created)
	}

	// 3. Replaying the exact Idempotency-Key returns the same commandId without issuing a second command.
	replayRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-setpoint-1", payload)
	if replayRec.Code != http.StatusAccepted {
		t.Fatalf("replay POST command status=%d body=%s", replayRec.Code, replayRec.Body.String())
	}
	var replayed commandView
	if err := json.NewDecoder(replayRec.Body).Decode(&replayed); err != nil {
		t.Fatalf("decode replayed command: %v", err)
	}
	if replayed.CommandID != created.CommandID {
		t.Fatalf("expected identical commandId on idempotent retry: first=%q replay=%q", created.CommandID, replayed.CommandID)
	}

	// 4. List recent commands and poll GET /api/v1/commands/{commandId}.
	listRec := doMiniCommandRequest(fixture.handler, http.MethodGet, publicCommandsPath+"?siteId="+fixture.siteID+"&limit=20", miniToken, "", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET commands status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Data []commandView `json:"data"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil || len(listResp.Data) == 0 {
		t.Fatalf("unexpected commands list err=%v body=%s", err, listRec.Body.String())
	}

	pollRec := doMiniCommandRequest(fixture.handler, http.MethodGet, publicCommandsPath+"/"+created.CommandID, miniToken, "", "")
	if pollRec.Code != http.StatusOK {
		t.Fatalf("GET command by id status=%d body=%s", pollRec.Code, pollRec.Body.String())
	}
	var polled commandView
	if err := json.NewDecoder(pollRec.Body).Decode(&polled); err != nil || polled.CommandID != created.CommandID {
		t.Fatalf("unexpected polled command err=%v body=%s", err, pollRec.Body.String())
	}
}

func TestMiniProgramBearerStepUpTOTPAndConflictGuards(t *testing.T) {
	fixture, miniToken := newMiniProgramCommandFixture(t)
	fixture.setCommandPointCapability(commandmodel.CapabilityStart, "", nil)

	startPayloadNoCode := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{}}`
	// 1. High-risk command without verificationCode fails with 428 STEP_UP_REQUIRED.
	stepUpRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-start-1", startPayloadNoCode)
	if stepUpRec.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing code status=%d want 428 body=%s", stepUpRec.Code, stepUpRec.Body.String())
	}
	var problem platformapi.ProblemDetails
	if err := json.NewDecoder(stepUpRec.Body).Decode(&problem); err != nil || problem.Code != "STEP_UP_REQUIRED" {
		t.Fatalf("unexpected 428 problem err=%v body=%s", err, stepUpRec.Body.String())
	}

	// 2. Wrong TOTP returns 428 STEP_UP_REQUIRED with CODE_INVALID.
	badCodePayload := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"000000"}`
	badRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-start-1", badCodePayload)
	if badRec.Code != http.StatusPreconditionRequired {
		t.Fatalf("wrong code status=%d want 428 body=%s", badRec.Code, badRec.Body.String())
	}

	// 3. Reusing the exact same Idempotency-Key with valid TOTP succeeds cleanly (202).
	goodCodePayload := `{"assetId":"` + fixture.assetID + `","commandPointId":"` + fixture.commandPointID + `","parameters":{},"verificationCode":"123456"}`
	okRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-start-1", goodCodePayload)
	if okRec.Code != http.StatusAccepted {
		t.Fatalf("valid TOTP status=%d want 202 body=%s", okRec.Code, okRec.Body.String())
	}

	// 4. Asset maintenance lock returns 409 COMMAND_CONTROL_LOCKED for Bearer session too.
	lockReq := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+fixture.assetID+"/control-lock", strings.NewReader(`{"lockMode":"MAINTENANCE_LOCK","reason":"现场维修挂牌"}`))
	fixture.authenticate(lockReq, true)
	lockReq.Header.Set("Content-Type", "application/json")
	lockRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("PUT control-lock status=%d body=%s", lockRec.Code, lockRec.Body.String())
	}

	lockedCmdRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-start-locked", goodCodePayload)
	lockedRaw := lockedCmdRec.Body.String()
	var lockedProblem platformapi.ProblemDetails
	if lockedCmdRec.Code != http.StatusConflict || json.Unmarshal([]byte(lockedRaw), &lockedProblem) != nil || lockedProblem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("locked asset status=%d want 409 COMMAND_CONTROL_LOCKED body=%s", lockedCmdRec.Code, lockedRaw)
	}

	// 5. Site MANUAL_OVERRIDE returns 409 COMMAND_CONTROL_LOCKED for Bearer session too.
	overrideReq := httptest.NewRequest(http.MethodPut, "/api/v1/sites/"+fixture.siteID+"/control-mode", strings.NewReader(`{"controlMode":"MANUAL_OVERRIDE","reason":"切到本地控制"}`))
	fixture.authenticate(overrideReq, true)
	overrideReq.Header.Set("Content-Type", "application/json")
	overrideRec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(overrideRec, overrideReq)
	if overrideRec.Code != http.StatusOK {
		t.Fatalf("PUT control-mode status=%d body=%s", overrideRec.Code, overrideRec.Body.String())
	}

	manualRec := doMiniCommandRequest(fixture.handler, http.MethodPost, publicCommandsPath, miniToken, "mini-intent-start-manual", goodCodePayload)
	manualRaw := manualRec.Body.String()
	var manualProblem platformapi.ProblemDetails
	if manualRec.Code != http.StatusConflict || json.Unmarshal([]byte(manualRaw), &manualProblem) != nil || manualProblem.Code != "COMMAND_CONTROL_LOCKED" {
		t.Fatalf("manual mode status=%d want 409 COMMAND_CONTROL_LOCKED body=%s", manualRec.Code, manualRaw)
	}
}
