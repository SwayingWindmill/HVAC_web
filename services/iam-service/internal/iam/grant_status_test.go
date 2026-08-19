package iam_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

func TestRegistryGrantStatusRequiresCoreWorkloadAndReturnsCurrentState(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	bundle, err := testpki.Generate("spiffe://hvac.local/iam-service", "spiffe://hvac.local/platform-core-service", now)
	if err != nil {
		t.Fatal(err)
	}
	clientPair, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientCertificate, err := x509.ParseCertificate(clientPair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	identifier := "id-1"
	handler := iam.NewHandler(iam.Config{
		AllowedWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway",
		CoreWorkloadSPIFFE:    bundle.ClientSPIFFEID,
		Audience:              "iam-service",
		Now:                   func() time.Time { return now },
		RegistryGrantStatus: iam.StaticRegistryGrantStatusStore{
			PolicyRevision:  "registry-read:1",
			RevokedTokenIDs: map[string]struct{}{identifier: {}},
		},
	})
	payload, err := json.Marshal(map[string]string{
		"tenantId": iam.S1FixtureTenantAID,
		"tokenId":  identifier,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, iam.RegistryGrantStatusPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientCertificate},
		VerifiedChains:   [][]*x509.Certificate{{clientCertificate}},
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response iam.RegistryGrantStatus
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.CurrentPolicyRevision != "registry-read:1" || !response.Revoked {
		t.Fatalf("response = %#v", response)
	}

	unverified := httptest.NewRequest(http.MethodPost, iam.RegistryGrantStatusPath, bytes.NewReader(payload))
	unverifiedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unverifiedRecorder, unverified)
	if unverifiedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unverified status = %d; body=%s", unverifiedRecorder.Code, unverifiedRecorder.Body.String())
	}
}
