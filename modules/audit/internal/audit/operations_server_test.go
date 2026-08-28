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

	"github.com/quanlaihe/hvac-web/libs/operationsauditevent"
	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
)

type fakeOperationsWriter struct {
	payloads [][]byte
	metadata []audit.MessageMetadata
	inserted bool
	err      error
}

func (writer *fakeOperationsWriter) ConsumeOperations(
	_ context.Context,
	payload []byte,
	metadata audit.MessageMetadata,
) (bool, error) {
	writer.payloads = append(writer.payloads, append([]byte(nil), payload...))
	writer.metadata = append(writer.metadata, metadata)
	return writer.inserted, writer.err
}

func operationsEvent(spiffeID string, now time.Time) operationsauditevent.EventV1 {
	investigationID := "investigation-001"
	runID := "run-001"
	revision := uint64(7)
	return operationsauditevent.EventV1{
		EventID:               "operations-audit-v1:tenant-001:site-001:investigation-001:run-001:7:COMMIT_EFFECT:SUCCEEDED:evidence-001",
		SchemaVersion:         operationsauditevent.SchemaVersion,
		MessageType:           operationsauditevent.MessageType,
		Producer:              operationsauditevent.Producer,
		TenantID:              "tenant-001",
		SiteID:                "site-001",
		InvestigationID:       &investigationID,
		RunID:                 &runID,
		InvestigationRevision: &revision,
		Actor: operationsauditevent.Actor{
			ActorType:         "OPERATOR",
			ActorID:           "operator-001",
			ActorIssuer:       "https://issuer.example.test",
			ExecutingService:  operationsauditevent.Producer,
			ExecutingSPIFFEID: spiffeID,
		},
		AuthorizationDecisionID: "decision-001",
		PolicyRevision:          "policy-v17",
		Action:                  "COMMIT_EFFECT",
		Operation:               "COMMIT_EFFECT",
		Outcome:                 "SUCCEEDED",
		OccurredAt:              now.UnixMilli(),
		RecordReferences: []operationsauditevent.RecordReference{{
			RecordType: "EVIDENCE",
			RecordID:   "evidence-001",
		}},
	}
}

func operationsRequest(
	t *testing.T,
	harness auditHarness,
	event operationsauditevent.EventV1,
) *http.Request {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, audit.OperationsAuditPath, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", event.EventID)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{harness.cert},
		VerifiedChains:   [][]*x509.Certificate{{harness.cert}},
	}
	return request
}

func TestOperationsAuditIngestRequiresExactMTLSProducerAndPersistsBoundedEvent(t *testing.T) {
	harness := newAuditHarness(t)
	writer := &fakeOperationsWriter{inserted: true}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                           &fakeRecordStore{},
		OperationsWriter:                writer,
		AllowedWorkloadSPIFFE:           harness.spiffeID,
		AllowedOperationsProducerSPIFFE: harness.spiffeID,
		Now:                             func() time.Time { return harness.now },
	})
	event := operationsEvent(harness.spiffeID, harness.now)
	request := operationsRequest(t, harness, event)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(writer.payloads) != 1 || len(writer.metadata) != 1 {
		t.Fatalf("unexpected writes: payloads=%d metadata=%d", len(writer.payloads), len(writer.metadata))
	}
	if writer.metadata[0].Topic != "operations-http" || !writer.metadata[0].ReceivedAt.Equal(harness.now) {
		t.Fatalf("unexpected metadata: %#v", writer.metadata[0])
	}
	decoded, err := operationsauditevent.Decode(writer.payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventID != event.EventID || decoded.Actor.ExecutingSPIFFEID != harness.spiffeID {
		t.Fatalf("unexpected event: %#v", decoded)
	}

	writer.inserted = false
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, operationsRequest(t, harness, event))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("exact duplicate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOperationsAuditIngestRejectsConflictForgedHeadersAndInvalidContent(t *testing.T) {
	harness := newAuditHarness(t)
	writer := &fakeOperationsWriter{err: audit.ErrEnvelopeConflict}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                           &fakeRecordStore{},
		OperationsWriter:                writer,
		AllowedWorkloadSPIFFE:           harness.spiffeID,
		AllowedOperationsProducerSPIFFE: harness.spiffeID,
		Now:                             func() time.Time { return harness.now },
	})
	event := operationsEvent(harness.spiffeID, harness.now)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, operationsRequest(t, harness, event))
	assertAuditProblem(t, recorder, http.StatusConflict, "AUDIT_EVENT_CONFLICT")

	forged := operationsRequest(t, harness, event)
	forged.Header.Set("X-Tenant-ID", "tenant-forged")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, forged)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_FORGED_IDENTITY_HEADER")

	mismatched := operationsRequest(t, harness, event)
	mismatched.Header.Set("Idempotency-Key", "different-event")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, mismatched)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_EVENT_INVALID")

	invalidEvent := operationsRequest(t, harness, event)
	var value map[string]any
	if err := json.NewDecoder(invalidEvent.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	value["prompt"] = "secret prompt"
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	invalidEvent.Body = ioNopCloser(bytes.NewReader(payload))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, invalidEvent)
	assertAuditProblem(t, recorder, http.StatusBadRequest, "AUDIT_EVENT_INVALID")
}

func TestOperationsAuditIngestFailsClosedWhenProducerOrWriterIsUnavailable(t *testing.T) {
	harness := newAuditHarness(t)
	event := operationsEvent(harness.spiffeID, harness.now)

	disabled := audit.NewHandler(audit.ServerConfig{Store: &fakeRecordStore{}, AllowedWorkloadSPIFFE: harness.spiffeID})
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, operationsRequest(t, harness, event))
	assertAuditProblem(t, recorder, http.StatusNotFound, "AUDIT_ROUTE_NOT_FOUND")

	writer := &fakeOperationsWriter{err: errors.New("postgres unavailable with secret detail")}
	handler := audit.NewHandler(audit.ServerConfig{
		Store:                           &fakeRecordStore{},
		OperationsWriter:                writer,
		AllowedWorkloadSPIFFE:           harness.spiffeID,
		AllowedOperationsProducerSPIFFE: harness.spiffeID,
		Now:                             func() time.Time { return harness.now },
	})
	withoutTLS := operationsRequest(t, harness, event)
	withoutTLS.TLS = nil
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, withoutTLS)
	assertAuditProblem(t, recorder, http.StatusUnauthorized, "AUDIT_WORKLOAD_IDENTITY_INVALID")

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, operationsRequest(t, harness, event))
	assertAuditProblem(t, recorder, http.StatusServiceUnavailable, "AUDIT_INGEST_UNAVAILABLE")
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret detail")) {
		t.Fatalf("store error detail leaked: %s", recorder.Body.String())
	}
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error                   { return nil }
func ioNopCloser(reader *bytes.Reader) readCloser { return readCloser{Reader: reader} }
