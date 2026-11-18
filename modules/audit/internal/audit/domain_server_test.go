package audit_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
)

type fakeDomainWriter struct {
	inserted bool
	err      error
	payloads [][]byte
	topics   []string
}

func (writer *fakeDomainWriter) ConsumeDomain(
	_ context.Context,
	payload []byte,
	metadata audit.MessageMetadata,
) (bool, error) {
	writer.payloads = append(writer.payloads, append([]byte(nil), payload...))
	writer.topics = append(writer.topics, metadata.Topic)
	return writer.inserted, writer.err
}

type fakeSearcher struct {
	records    []audit.SearchRecord
	lastTenant string
	lastFilter audit.SearchFilter
	err        error
}

func (searcher *fakeSearcher) SearchRecords(
	_ context.Context,
	tenantID string,
	filter audit.SearchFilter,
) ([]audit.SearchRecord, error) {
	searcher.lastTenant = tenantID
	searcher.lastFilter = filter
	if searcher.err != nil {
		return nil, searcher.err
	}
	return searcher.records, nil
}

func domainEventFixture(spiffeID string, now time.Time) audit.DomainEvent {
	return audit.DomainEvent{
		SchemaVersion:     audit.DomainEventSchemaVersion,
		MessageID:         "01a12370-0000-7000-8000-000000000777",
		TenantID:          "018f3d00-0000-7000-8000-000000000001",
		Category:          audit.AggregateTypeRemoteCommand,
		TargetID:          "command:cmd-001",
		AggregateVersion:  1,
		OccurredAt:        now,
		InitiatingSubject: "fixture-user",
		InitiatingIssuer:  "https://issuer.example.test",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: spiffeID,
		Action:            "COMMAND_SUBMITTED",
		Outcome:           "SUCCEEDED",
		PolicyRevision:    "policy-v1",
		CorrelationID:     "corr-001",
		CausationID:       "cause-001",
		TraceID:           "0123456789abcdef0123456789abcdef",
		ClientIP:          "10.10.0.12",
		ClientKind:        audit.ClientKindBrowser,
		ActorDisplay:      "张工",
		TargetLabel:       "1号冷水机组启动",
		ChangeSummary:     json.RawMessage(`{"action":"START","before":"STOPPED","after":"RUNNING"}`),
	}
}

func domainRequest(t *testing.T, harness auditHarness, event audit.DomainEvent, action, scope string) *http.Request {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	claims := identitycontext.DelegationClaims{
		Issuer:           harness.spiffeID,
		Subject:          event.InitiatingSubject,
		SubjectIssuer:    event.InitiatingIssuer,
		DisplayName:      "张工",
		Email:            "fixture@example.test",
		Roles:            []string{"operator"},
		ExecutingService: harness.spiffeID,
		Audience:         "audit-ledger-service",
		TenantID:         event.TenantID,
		Actions:          []string{action},
		Scopes:           []string{scope},
		PolicyRevision:   event.PolicyRevision,
		SessionID:        "session-01",
		IssuedAt:         harness.now.Unix(),
		ExpiresAt:        harness.now.Add(30 * time.Second).Unix(),
		TokenID:          "grant-domain-01",
	}
	grant, err := identitycontext.SignDelegation(harness.signer, claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, audit.DomainAuditPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", event.MessageID)
	request.Header.Set("X-Delegation-Grant", grant)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{harness.cert},
		VerifiedChains:   [][]*x509.Certificate{{harness.cert}},
	}
	return request
}

func TestDomainAuditIngestRequiresWorkloadMTLSDelegationAndValidEnvelope(t *testing.T) {
	harness := newAuditHarness(t)
	writer := &fakeDomainWriter{inserted: true}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                 &fakeRecordStore{},
		DomainWriter:          writer,
		AllowedWorkloadSPIFFE: harness.spiffeID,
		Now:                   func() time.Time { return harness.now },
	})
	event := domainEventFixture(harness.spiffeID, harness.now)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(writer.payloads) != 1 || writer.topics[0] != audit.GatewayDomainTopic {
		t.Fatalf("unexpected domain write call: payloads=%d topics=%v", len(writer.payloads), writer.topics)
	}
	decoded, err := audit.DecodeDomainEvent(writer.payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ClientIP != "10.10.0.12" || decoded.ClientKind != audit.ClientKindBrowser || decoded.ActorDisplay != "张工" {
		t.Fatalf("unexpected decoded metadata: %#v", decoded)
	}
	if len(decoded.AggregateID()) != 64 {
		t.Fatalf("aggregate id length=%d", len(decoded.AggregateID()))
	}

	readOnlyGrant := domainRequest(t, harness, event, "audit:read", "tenant:"+event.TenantID)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, readOnlyGrant)
	assertAuditProblem(t, recorder, http.StatusForbidden, "AUDIT_DELEGATION_REJECTED")

	forged := domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID)
	forged.Header.Set("X-Principal", "forged")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, forged)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_FORGED_IDENTITY_HEADER")

	noTLS := domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID)
	noTLS.TLS = nil
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, noTLS)
	assertAuditProblem(t, recorder, http.StatusUnauthorized, "AUDIT_WORKLOAD_IDENTITY_INVALID")

	wrongIdempotency := domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID)
	wrongIdempotency.Header.Set("Idempotency-Key", "other-id")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, wrongIdempotency)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_EVENT_INVALID")

	conflictWriter := &fakeDomainWriter{err: audit.ErrEnvelopeConflict}
	conflictHandler := audit.NewHandler(audit.ServerConfig{
		Store:                 &fakeRecordStore{},
		DomainWriter:          conflictWriter,
		AllowedWorkloadSPIFFE: harness.spiffeID,
		Now:                   func() time.Time { return harness.now },
	})
	recorder = httptest.NewRecorder()
	conflictHandler.ServeHTTP(recorder, domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID))
	assertAuditProblem(t, recorder, http.StatusConflict, "AUDIT_EVENT_CONFLICT")

	unavailableWriter := &fakeDomainWriter{err: errors.New("secret postgres failure")}
	unavailableHandler := audit.NewHandler(audit.ServerConfig{
		Store:                 &fakeRecordStore{},
		DomainWriter:          unavailableWriter,
		AllowedWorkloadSPIFFE: harness.spiffeID,
		Now:                   func() time.Time { return harness.now },
	})
	recorder = httptest.NewRecorder()
	unavailableHandler.ServeHTTP(recorder, domainRequest(t, harness, event, "audit:write", "tenant:"+event.TenantID))
	assertAuditProblem(t, recorder, http.StatusServiceUnavailable, "AUDIT_INGEST_UNAVAILABLE")
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret postgres")) {
		t.Fatalf("internal store detail leaked: %s", recorder.Body.String())
	}
}

func TestAuditSearchPassesCategoryFilterAndReturnsDisplayMetadata(t *testing.T) {
	harness := newAuditHarness(t)
	searcher := &fakeSearcher{
		records: []audit.SearchRecord{{
			LedgerSequence: 9,
			MessageID:      "msg-9",
			TenantID:       "018f3d00-0000-7000-8000-000000000001",
			Actor:          "fixture-user",
			ActorDisplay:   "张工",
			ClientKind:     audit.ClientKindMiniProgram,
			Action:         "SITE_CONTROL_MODE_UPDATED",
			ResourceType:   audit.AggregateTypeControlLock,
			ResourceID:     "abcd",
			TargetLabel:    "松雅轩冷站",
			Outcome:        "SUCCEEDED",
			PolicyRevision: "policy-v1",
			CorrelationID:  "corr-9",
			ChangeSummary:  json.RawMessage(`{"before":"LOCAL_MANUAL","after":"REMOTE_AUTO"}`),
			OccurredAt:     harness.now,
		}},
	}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                 &fakeRecordStore{},
		Searcher:              searcher,
		AllowedWorkloadSPIFFE: harness.spiffeID,
		Now:                   func() time.Time { return harness.now },
	})
	req := harness.request(t, "018f3d00-0000-7000-8000-000000000001", "ignored", []string{"admin"}, "audit-ledger-service", "audit:read", "tenant:018f3d00-0000-7000-8000-000000000001")
	req.Method = http.MethodPost
	req.URL.Path = audit.AuditSearchPath
	req.Body = ioNopCloser(bytes.NewReader([]byte(`{"category":"control-schedule","limit":50}`)))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if searcher.lastFilter.Category != audit.CategoryControlSchedule || searcher.lastFilter.Limit != 50 {
		t.Fatalf("unexpected filter passed to searcher: %#v", searcher.lastFilter)
	}
	var payload struct {
		Items []audit.SearchRecord `json:"items"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ClientKind != audit.ClientKindMiniProgram || payload.Items[0].TargetLabel != "松雅轩冷站" {
		t.Fatalf("unexpected search payload: %#v", payload.Items)
	}
}

func TestDomainEventValidationRejectsInvalidIPClientKindAndSummary(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := domainEventFixture("spiffe://hvac.local/platform-gateway", now)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid domain event failed: %v", err)
	}

	badIP := base
	badIP.ClientIP = "not-an-ip:8080"
	if err := badIP.Validate(); err == nil {
		t.Fatal("expected invalid clientIp error")
	}

	badClient := base
	badClient.ClientKind = "DESKTOP"
	if err := badClient.Validate(); err == nil {
		t.Fatal("expected invalid clientKind error")
	}

	badSummary := base
	badSummary.ChangeSummary = json.RawMessage(`["not-an-object"]`)
	if err := badSummary.Validate(); err == nil {
		t.Fatal("expected non-object changeSummary error")
	}

	badCategory := base
	badCategory.Category = "unknown-category"
	if err := badCategory.Validate(); err == nil {
		t.Fatal("expected unsupported category error")
	}
}

func TestDomainAuditIngestAcceptsSystemWorkloadDelegationOnlyForSystemEvents(t *testing.T) {
	harness := newAuditHarness(t)
	writer := &fakeDomainWriter{inserted: true}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                 &fakeRecordStore{},
		DomainWriter:          writer,
		AllowedWorkloadSPIFFE: harness.spiffeID,
		Now:                   func() time.Time { return harness.now },
	})

	event := domainEventFixture(harness.spiffeID, harness.now)
	event.InitiatingSubject = "principal-opt-workload"
	event.ClientKind = audit.ClientKindSystem
	event.ActorDisplay = "AI 自动优化"

	buildReq := func(ev audit.DomainEvent) *http.Request {
		payload, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		claims := identitycontext.DelegationClaims{
			Issuer:           harness.spiffeID,
			Subject:          harness.spiffeID,
			SubjectIssuer:    ev.InitiatingIssuer,
			PrincipalID:      "principal-opt-workload",
			DisplayName:      "AI 自动优化",
			ExecutingService: harness.spiffeID,
			Audience:         "audit-ledger-service",
			TenantID:         ev.TenantID,
			Actions:          []string{"audit:write"},
			Scopes:           []string{"tenant:" + ev.TenantID},
			PolicyRevision:   ev.PolicyRevision,
			IssuedAt:         harness.now.Unix(),
			ExpiresAt:        harness.now.Add(30 * time.Second).Unix(),
			TokenID:          "grant-domain-system-01",
		}
		grant, err := identitycontext.SignDelegation(harness.signer, claims)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, audit.DomainAuditPath, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", ev.MessageID)
		req.Header.Set("X-Delegation-Grant", grant)
		req.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{harness.cert},
			VerifiedChains:   [][]*x509.Certificate{{harness.cert}},
		}
		return req
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, buildReq(event))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for workload delegation SYSTEM event, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	spoofBrowser := event
	spoofBrowser.ClientKind = audit.ClientKindBrowser
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, buildReq(spoofBrowser))
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_EVENT_INVALID")
}
