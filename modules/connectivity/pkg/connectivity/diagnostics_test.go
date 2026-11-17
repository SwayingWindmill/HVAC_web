package connectivity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/packettap"
)

type fakeDiagnosticsStore struct {
	quarantineItems []GatewayQuarantineRecord
	quarantineErr   error
	scopeErr        error
	lastGatewayID   string
	lastLimit       int
	lastAction      string
}

func (f *fakeDiagnosticsStore) ListGatewayQuarantineForOperator(_ context.Context, _ registryauth.GrantClaims, gatewayID string, limit int) ([]GatewayQuarantineRecord, error) {
	f.lastAction = "list_quarantine"
	f.lastGatewayID = gatewayID
	f.lastLimit = limit
	if f.quarantineErr != nil {
		return nil, f.quarantineErr
	}
	return f.quarantineItems, nil
}

func (f *fakeDiagnosticsStore) CheckGatewayOperatorScope(_ context.Context, _ registryauth.GrantClaims, gatewayID string) error {
	f.lastAction = "check_scope"
	f.lastGatewayID = gatewayID
	return f.scopeErr
}

func TestDiagnosticsHTTPHandlersAndContract(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	gwID := "0194f000-2000-7000-8000-000000000001"
	tenantID := "0194f000-0000-7000-8000-000000000001"
	principalID := "0194f000-4000-7000-8000-000000000001"

	store := &fakeDiagnosticsStore{
		quarantineItems: []GatewayQuarantineRecord{
			{
				ID:               "0194f000-9000-7000-8000-000000000001",
				GatewayID:        gwID,
				Topic:            "hvac/v1/" + gwID + "/up/telemetry",
				ReasonCode:       "MESSAGE_INVALID",
				Detail:           "bad payload",
				PayloadSHA256:    strings.Repeat("a", 64),
				PayloadBytes:     12,
				PayloadPreview:   `{"bad":true}`,
				PayloadEncoding:  "utf8",
				PayloadTruncated: false,
				OccurrenceCount:  4,
				ReceivedAt:       now.Add(-time.Minute),
				LastSeenAt:       now,
			},
		},
	}
	tap := packettap.NewManager(packettap.WithNow(func() time.Time { return now }))

	var gotAction registryauth.Action
	authorize := func(_ *http.Request, action registryauth.Action) (registryauth.GrantClaims, error) {
		gotAction = action
		return registryauth.GrantClaims{TenantID: tenantID, PrincipalID: principalID}, nil
	}

	diagHandler := NewDiagnosticsHandler(store, tap, authorize)
	wrapped := NewCredentialHandlerWithDiagnostics(nil, authorize, nil, nil, nil, diagHandler)

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}

	unauthReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine", nil)
	unauthRec := httptest.NewRecorder()
	wrapped.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without SPIFFE TLS, got %d", unauthRec.Code)
	}

	forgedReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/packet-capture", nil)
	forgedReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	forgedReq.Header.Set("X-Gateway-Client-Cert", "forged")
	forgedRec := httptest.NewRecorder()
	wrapped.ServeHTTP(forgedRec, forgedReq)
	if forgedRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on forged X-Gateway-Client-Cert, got %d", forgedRec.Code)
	}

	shadowOnly := NewCredentialHandlerWithShadow(nil, authorize, nil, nil, nil)
	missingReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine", nil)
	missingReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	missingRec := httptest.NewRecorder()
	shadowOnly.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when diagnosticsHandler is nil, got %d", missingRec.Code)
	}

	qReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine", nil)
	qReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	qRec := httptest.NewRecorder()
	wrapped.ServeHTTP(qRec, qReq)
	if qRec.Code != http.StatusOK || gotAction != registryauth.ActionDeviceRead || store.lastLimit != 20 {
		t.Fatalf("GET quarantine status=%d action=%s limit=%d body=%s", qRec.Code, gotAction, store.lastLimit, qRec.Body.String())
	}
	if qRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control=no-store, got %q", qRec.Header().Get("Cache-Control"))
	}
	var qPayload map[string]any
	if err := json.Unmarshal(qRec.Body.Bytes(), &qPayload); err != nil {
		t.Fatalf("unmarshal quarantine response: %v", err)
	}
	items := qPayload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 quarantine item, got %d", len(items))
	}
	firstItem := items[0].(map[string]any)
	for _, field := range []string{"id", "gatewayId", "topic", "reasonCode", "detail", "payloadSha256", "payloadBytes", "payloadPreview", "payloadEncoding", "payloadTruncated", "occurrenceCount", "receivedAt", "lastSeenAt"} {
		if _, ok := firstItem[field]; !ok {
			t.Fatalf("missing quarantine JSON field %q in %s", field, qRec.Body.String())
		}
	}

	for _, badLimit := range []string{"", "0", "-5", "51", "abc"} {
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine?limit="+badLimit, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q got status=%d, want 400", badLimit, rec.Code)
		}
	}

	store.quarantineItems = nil
	limReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine?limit=50", nil)
	limReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	limRec := httptest.NewRecorder()
	wrapped.ServeHTTP(limRec, limReq)
	if limRec.Code != http.StatusOK || store.lastLimit != 50 || strings.TrimSpace(limRec.Body.String()) != `{"items":[]}` {
		t.Fatalf("limit=50 empty result status=%d limit=%d body=%s", limRec.Code, store.lastLimit, limRec.Body.String())
	}

	for _, tc := range []struct {
		err      error
		wantCode int
		wantStr  string
	}{
		{ErrCredentialForbidden, http.StatusForbidden, "CREDENTIAL_FORBIDDEN"},
		{ErrNotFound, http.StatusNotFound, "GATEWAY_NOT_FOUND"},
		{ErrEnrollmentRejected, http.StatusBadRequest, "DIAGNOSTICS_REQUEST_INVALID"},
		{errors.New("postgres://secret:pass@db/hvac"), http.StatusInternalServerError, "DIAGNOSTICS_SERVICE_UNAVAILABLE"},
	} {
		store.quarantineErr = tc.err
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine", nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantStr) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("err=%v status=%d body=%s", tc.err, rec.Code, rec.Body.String())
		}
	}
	store.quarantineErr = nil

	startReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/packet-capture/start", nil)
	startReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	startRec := httptest.NewRecorder()
	wrapped.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK || gotAction != registryauth.ActionDeviceWrite {
		t.Fatalf("start empty body status=%d action=%s body=%s", startRec.Code, gotAction, startRec.Body.String())
	}
	var snap packettap.Snapshot
	if err := json.Unmarshal(startRec.Body.Bytes(), &snap); err != nil || !snap.Active || snap.DurationSeconds == nil || *snap.DurationSeconds != 300 {
		t.Fatalf("unexpected start snapshot: %+v err=%v", snap, err)
	}

	tap.Record(packettap.DirectionUplink, packettap.ChannelTelemetry, gwID, "hvac/v1/"+gwID+"/up/telemetry", "tr-1", []byte(`{"ok":1}`))

	getCapReq := httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/packet-capture", nil)
	getCapReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	getCapRec := httptest.NewRecorder()
	wrapped.ServeHTTP(getCapRec, getCapReq)
	if getCapRec.Code != http.StatusOK || gotAction != registryauth.ActionDeviceRead {
		t.Fatalf("GET packet-capture status=%d action=%s", getCapRec.Code, gotAction)
	}
	if err := json.Unmarshal(getCapRec.Body.Bytes(), &snap); err != nil || !snap.Active || len(snap.Items) != 1 {
		t.Fatalf("unexpected GET snapshot: %+v err=%v", snap, err)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/packet-capture/stop", nil)
	stopReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	stopRec := httptest.NewRecorder()
	wrapped.ServeHTTP(stopRec, stopReq)
	if stopRec.Code != http.StatusOK || gotAction != registryauth.ActionDeviceWrite {
		t.Fatalf("POST stop status=%d action=%s", stopRec.Code, gotAction)
	}
	if err := json.Unmarshal(stopRec.Body.Bytes(), &snap); err != nil || snap.Active || len(snap.Items) != 1 {
		t.Fatalf("unexpected stop snapshot: %+v err=%v", snap, err)
	}

	for _, badBody := range []string{`{"durationSeconds":0}`, `{"durationSeconds":29}`, `{"durationSeconds":901}`, `{"unknown":true}`, `{`, `{"durationSeconds":60}{}`} {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gwID+"/packet-capture/start", bytes.NewBufferString(badBody))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad body %q status=%d, want 400 body=%s", badBody, rec.Code, rec.Body.String())
		}
	}

	for _, tc := range []struct {
		err      error
		wantCode int
	}{
		{ErrCredentialForbidden, http.StatusForbidden},
		{ErrNotFound, http.StatusNotFound},
	} {
		store.scopeErr = tc.err
		for _, ep := range []struct {
			method, suffix string
		}{
			{http.MethodGet, "/packet-capture"},
			{http.MethodPost, "/packet-capture/start"},
			{http.MethodPost, "/packet-capture/stop"},
		} {
			req := httptest.NewRequest(ep.method, "/internal/v1/gateways/"+gwID+ep.suffix, nil)
			req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("%s %s err=%v status=%d, want %d", ep.method, ep.suffix, tc.err, rec.Code, tc.wantCode)
			}
		}
	}
	store.scopeErr = nil

	for i := 1; i <= packettap.MaxActiveCaptureSessions; i++ {
		id := fmt.Sprintf("0194f000-2000-7000-8000-%012x", 100+i)
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+id+"/packet-capture/start", bytes.NewBufferString(`{"durationSeconds":60}`))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("session %d start status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	overReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/0194f000-2000-7000-8000-000000000999/packet-capture/start", bytes.NewBufferString(`{"durationSeconds":60}`))
	overReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	overRec := httptest.NewRecorder()
	wrapped.ServeHTTP(overRec, overReq)
	if overRec.Code != http.StatusTooManyRequests || !strings.Contains(overRec.Body.String(), "PACKET_CAPTURE_CAPACITY_EXCEEDED") {
		t.Fatalf("expected 429 PACKET_CAPTURE_CAPACITY_EXCEEDED, got %d body=%s", overRec.Code, overRec.Body.String())
	}

	noAuthHandler := NewDiagnosticsHandler(store, tap, nil)
	noAuthRec := httptest.NewRecorder()
	noAuthHandler.ServeHTTP(noAuthRec, httptest.NewRequest(http.MethodGet, "/internal/v1/gateways/"+gwID+"/quarantine", nil))
	if noAuthRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on nil authorizer, got %d", noAuthRec.Code)
	}
}
