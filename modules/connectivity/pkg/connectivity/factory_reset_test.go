package connectivity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestFactoryResetAndTransferDecommissionNilGuards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	claims := registryauth.GrantClaims{
		TenantID:    "0193f000-0000-7000-8000-000000000001",
		PrincipalID: "0193f000-1000-7000-8000-000000000001",
	}
	const gatewayID = "0193f000-2000-7000-8000-000000000001"
	const transferID = "0193f000-3000-7000-8000-000000000001"
	const resetID = "0193f000-4000-7000-8000-000000000001"

	var nilSvc *CredentialService
	if _, err := nilSvc.IssueFactoryReset(ctx, claims, gatewayID, IssueFactoryResetInput{Reason: "wipe"}); err == nil {
		t.Fatal("expected nil CredentialService.IssueFactoryReset error")
	}
	if _, err := nilSvc.ListFactoryResets(ctx, claims, gatewayID); err == nil {
		t.Fatal("expected nil CredentialService.ListFactoryResets error")
	}
	if _, err := nilSvc.DecommissionTransferredGateway(ctx, gatewayID, DecommissionTransferInput{
		TenantID:   claims.TenantID,
		TransferID: transferID,
		Reason:     "transfer",
	}); err == nil {
		t.Fatal("expected nil CredentialService.DecommissionTransferredGateway error")
	}

	emptySvc := &CredentialService{}
	if _, err := emptySvc.IssueFactoryReset(ctx, claims, gatewayID, IssueFactoryResetInput{Reason: "wipe"}); err == nil {
		t.Fatal("expected unconfigured CredentialService.IssueFactoryReset error")
	}
	if _, err := emptySvc.ListFactoryResets(ctx, claims, gatewayID); err == nil {
		t.Fatal("expected unconfigured CredentialService.ListFactoryResets error")
	}
	if _, err := emptySvc.DecommissionTransferredGateway(ctx, gatewayID, DecommissionTransferInput{
		TenantID:   claims.TenantID,
		TransferID: transferID,
		Reason:     "transfer",
	}); err == nil {
		t.Fatal("expected unconfigured CredentialService.DecommissionTransferredGateway error")
	}

	var nilStore *Store
	ack := edgecontrol.FactoryResetAckPayload{
		ResetID:        resetID,
		Status:         edgecontrol.FactoryResetAckStatusWiped,
		WipedArtifacts: []string{"credentials"},
		OccurredAt:     1760000000000,
	}
	if _, err := nilStore.RecordFactoryResetAck(ctx, gatewayID, ack); err == nil {
		t.Fatal("expected nil Store.RecordFactoryResetAck error")
	}
	if _, err := (&Store{}).RecordFactoryResetAck(ctx, gatewayID, ack); err == nil {
		t.Fatal("expected unconfigured Store.RecordFactoryResetAck error")
	}
}

func TestCredentialHTTPFactoryResetAndTransferDecommissionAuthAndValidation(t *testing.T) {
	t.Parallel()

	gwURI, _ := url.Parse("spiffe://hvac.local/platform-gateway")
	gwPeer := &x509.Certificate{URIs: []*url.URL{gwURI}}
	coreURI, _ := url.Parse("spiffe://hvac.local/platform-core-service")
	corePeer := &x509.Certificate{URIs: []*url.URL{coreURI}}
	otherURI, _ := url.Parse("spiffe://hvac.local/telemetry-worker")
	otherPeer := &x509.Certificate{URIs: []*url.URL{otherURI}}

	const gatewayID = "0193f000-2000-7000-8000-000000000001"
	const tenantID = "0193f000-0000-7000-8000-000000000001"
	const principalID = "0193f000-1000-7000-8000-000000000001"

	var authErr error
	handler := NewCredentialHandler(&CredentialService{}, func(_ *http.Request, _ registryauth.Action) (registryauth.GrantClaims, error) {
		if authErr != nil {
			return registryauth.GrantClaims{}, authErr
		}
		return registryauth.GrantClaims{
			TenantID:    tenantID,
			PrincipalID: principalID,
		}, nil
	})

	// 1. Unverified TLS peer rejected on internal routes.
	unverifiedReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gatewayID+":transfer-decommission", nil)
	unverifiedReq.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{corePeer}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unverifiedReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unverified transfer-decommission status=%d, want 401", rec.Code)
	}

	// 2. Non-core/non-gateway workload rejected on transfer-decommission.
	otherReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gatewayID+":transfer-decommission", bytes.NewBufferString(`{}`))
	otherReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{otherPeer}}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, otherReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("telemetry-worker transfer-decommission status=%d, want 401", rec.Code)
	}

	// 3. platform-core-service is denied from operator factory-reset / factory-resets routes.
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/internal/v1/gateways/" + gatewayID + "/factory-reset"},
		{http.MethodGet, "/internal/v1/gateways/" + gatewayID + "/factory-resets"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("core-service on %s %s status=%d, want 401", tc.method, tc.path, r.Code)
		}
	}

	// 4. Forged X-Gateway-Client-Cert header rejected on internal transfer-decommission.
	forgedReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gatewayID+":transfer-decommission", bytes.NewBufferString(`{}`))
	forgedReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	forgedReq.Header.Set("X-Gateway-Client-Cert", "forged")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, forgedReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged header status=%d, want 403", rec.Code)
	}

	// 5. platform-core-service reaching transfer-decommission rejects malformed JSON with 400 ENROLLMENT_REJECTED.
	badJSONReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gatewayID+":transfer-decommission", bytes.NewBufferString(`{"unknown":true}`))
	badJSONReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{corePeer}}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, badJSONReq)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field transfer-decommission status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp["code"] != "ENROLLMENT_REJECTED" {
		t.Fatalf("unexpected error response: %s (%v)", rec.Body.String(), err)
	}

	// 6. Operator factory-reset rejects malformed JSON with 400 and authorizer denial with 403.
	badResetReq := httptest.NewRequest(http.MethodPost, "/internal/v1/gateways/"+gatewayID+"/factory-reset", bytes.NewBufferString(`{not-json`))
	badResetReq.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, badResetReq)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON factory-reset status=%d, want 400", rec.Code)
	}

	authErr = ErrCredentialForbidden
	for _, tc := range []struct {
		method, path string
		body         string
	}{
		{http.MethodPost, "/internal/v1/gateways/" + gatewayID + "/factory-reset", `{"reason":"manual wipe"}`},
		{http.MethodGet, "/internal/v1/gateways/" + gatewayID + "/factory-resets", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{gwPeer}}}
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		if r.Code != http.StatusForbidden {
			t.Fatalf("unauthorized %s %s status=%d, want 403", tc.method, tc.path, r.Code)
		}
	}
	authErr = nil
	_ = time.Second
}
