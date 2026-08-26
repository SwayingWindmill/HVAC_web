package audit_test

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/testpki"
	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
)

func TestAuditQueryRequiresWorkloadAndTenantScopedDelegation(t *testing.T) {
	harness := newAuditHarness(t)
	record := audit.Record{
		LedgerSequence: 1, MessageID: "message-01", SchemaVersion: 1,
		TenantID: "tenant-01",
		AggregateType: "bff-session", AggregateID: "session-01", AggregateVersion: 1,
		OccurredAt: harness.now, InitiatingSubject: "fixture-user", InitiatingIssuer: "https://issuer.example.test",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: harness.spiffeID,
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "policy-v1",
		CorrelationID: "request-01", TraceID: "0123456789abcdef0123456789abcdef",
		Traceparent:   "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		PayloadSHA256: stringOf('a', 64), PreviousRecordHash: stringOf('0', 64), RecordHash: stringOf('b', 64), RecordedAt: harness.now,
	}
	store := &fakeRecordStore{records: map[string]audit.Record{"tenant-01/message-01": record}}
	handler := audit.NewHandler(audit.ServerConfig{Store: store, AllowedWorkloadSPIFFE: harness.spiffeID, Audience: "audit-ledger-service", Now: func() time.Time { return harness.now }})

	request := harness.request(t, "tenant-01", "message-01", []string{"audit-reader"}, "audit-ledger-service", "audit:read", "tenant:tenant-01")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response audit.Record
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.MessageID != record.MessageID || response.TenantID != "tenant-01" {
		t.Fatalf("unexpected audit record: %#v", response)
	}

	crossTenant := harness.request(t, "tenant-02", "message-01", []string{"audit-reader"}, "audit-ledger-service", "audit:read", "tenant:tenant-02")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, crossTenant)
	assertAuditProblem(t, recorder, http.StatusNotFound, "AUDIT_RECORD_NOT_FOUND")

	wrongAudience := harness.request(t, "tenant-01", "message-01", []string{"audit-reader"}, "iam-service", "audit:read", "tenant:tenant-01")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, wrongAudience)
	assertAuditProblem(t, recorder, http.StatusForbidden, "AUDIT_DELEGATION_REJECTED")

	wrongAction := harness.request(t, "tenant-01", "message-01", []string{"audit-reader"}, "audit-ledger-service", "principal:read", "tenant:tenant-01")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, wrongAction)
	assertAuditProblem(t, recorder, http.StatusForbidden, "AUDIT_DELEGATION_REJECTED")
}

func TestAuditQueryRejectsForgedHeadersAndMissingVerifiedTLS(t *testing.T) {
	harness := newAuditHarness(t)
	handler := audit.NewHandler(audit.ServerConfig{Store: &fakeRecordStore{}, AllowedWorkloadSPIFFE: harness.spiffeID, Now: func() time.Time { return harness.now }})

	forged := harness.request(t, "tenant-01", "message-01", []string{"audit-reader"}, "audit-ledger-service", "audit:read", "tenant:tenant-01")
	forged.Header.Set("X-Tenant-ID", "tenant-forged")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, forged)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_FORGED_IDENTITY_HEADER")

	withoutTLS := httptest.NewRequest(http.MethodGet, audit.SessionAuditPathPrefix+"message-01", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, withoutTLS)
	assertAuditProblem(t, recorder, http.StatusUnauthorized, "AUDIT_WORKLOAD_IDENTITY_INVALID")
}

type auditHarness struct {
	now      time.Time
	spiffeID string
	cert     *x509.Certificate
	signer   crypto.Signer
}

func newAuditHarness(t *testing.T) auditHarness {
	t.Helper()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	bundle, err := testpki.GenerateWithAudit("spiffe://hvac.local/iam-service", "spiffe://hvac.local/audit-ledger-service", "spiffe://hvac.local/platform-gateway", now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(bundle.ClientCertPEM, bundle.ClientKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatal("test workload key is not a signer")
	}
	return auditHarness{now: now, spiffeID: bundle.ClientSPIFFEID, cert: certificate, signer: signer}
}

func (h auditHarness) request(t *testing.T, tenantID, messageID string, roles []string, audience, action, scope string) *http.Request {
	t.Helper()
	claims := identitycontext.DelegationClaims{
		Issuer: h.spiffeID, Subject: "fixture-user", SubjectIssuer: "https://issuer.example.test",
		DisplayName: "Fixture User", Email: "fixture@example.test", Roles: roles,
		ExecutingService: h.spiffeID, Audience: audience, TenantID: tenantID,
		Actions: []string{action}, Scopes: []string{scope}, PolicyRevision: "policy-v1",
		SessionID: "session-01", IssuedAt: h.now.Unix(), ExpiresAt: h.now.Add(30 * time.Second).Unix(), TokenID: "grant-01",
	}
	grant, err := identitycontext.SignDelegation(h.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, audit.SessionAuditPathPrefix+messageID, nil)
	request.Header.Set("X-Delegation-Grant", grant)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{h.cert}, VerifiedChains: [][]*x509.Certificate{{h.cert}}}
	return request
}

type fakeRecordStore struct {
	records map[string]audit.Record
}

func (store *fakeRecordStore) GetRecord(_ context.Context, tenantID, messageID string) (audit.Record, error) {
	if record, exists := store.records[tenantID+"/"+messageID]; exists {
		return record, nil
	}
	return audit.Record{}, audit.ErrRecordNotFound
}

func assertAuditProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != code {
		t.Fatalf("code=%q want=%q", problem.Code, code)
	}
}

func stringOf(character rune, count int) string {
	result := make([]rune, count)
	for index := range result {
		result[index] = character
	}
	return string(result)
}
