package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/sessionevent"
	"github.com/quanlaihe/hvac-web/services/audit-ledger-service/internal/audit"
)

func TestTransactionalInboxDeduplicatesAcrossRestartAndPreservesHashChain(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	firstPayload := marshalEvent(t, fixtureAuditEvent(now, "message-01", "session-01", "org-01", 1, "SESSION_CREATED"))
	inserted, err := store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 0, Offset: 10, ReceivedAt: now.Add(time.Second)})
	if err != nil || !inserted {
		t.Fatalf("first consume inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 0, Offset: 11, ReceivedAt: now.Add(2 * time.Second)})
	if err != nil || inserted {
		t.Fatalf("duplicate consume inserted=%v err=%v", inserted, err)
	}
	count, err := store.CountRecords(context.Background(), "org-01", "message-01")
	if err != nil || count != 1 {
		t.Fatalf("duplicate produced %d records: %v", count, err)
	}
	firstRecord, err := store.GetRecord(context.Background(), "org-01", "message-01")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	store, err = audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inserted, err = store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 0, Offset: 12, ReceivedAt: now.Add(3 * time.Second)})
	if err != nil || inserted {
		t.Fatalf("restart duplicate inserted=%v err=%v", inserted, err)
	}

	secondEvent := fixtureAuditEvent(now.Add(time.Second), "message-02", "session-01", "org-01", 2, "SESSION_REVOKED")
	secondEvent.CausationID = "message-01"
	secondPayload := marshalEvent(t, secondEvent)
	inserted, err = store.Consume(context.Background(), secondPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 0, Offset: 13, ReceivedAt: now.Add(4 * time.Second)})
	if err != nil || !inserted {
		t.Fatalf("second event inserted=%v err=%v", inserted, err)
	}
	secondRecord, err := store.GetRecord(context.Background(), "org-01", "message-02")
	if err != nil {
		t.Fatal(err)
	}
	if secondRecord.PreviousRecordHash != firstRecord.RecordHash || secondRecord.RecordHash == firstRecord.RecordHash {
		t.Fatalf("hash chain did not advance: first=%#v second=%#v", firstRecord, secondRecord)
	}
}

func TestDuplicateMessageWithDifferentProtobufIsRejected(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original := fixtureAuditEvent(now, "message-conflict", "session-01", "org-01", 1, "SESSION_CREATED")
	if _, err := store.Consume(context.Background(), marshalEvent(t, original), audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 1, Offset: 20, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Action = "SESSION_REVOKED"
	if _, err := store.Consume(context.Background(), marshalEvent(t, changed), audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 1, Offset: 21, ReceivedAt: now.Add(time.Second)}); !errors.Is(err, audit.ErrEnvelopeConflict) {
		t.Fatalf("different protobuf under same message ID was accepted: %v", err)
	}
}

func TestOperationsAuditPersistsExactlyOnceAndAdvancesOrganizationHashChain(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	firstEvent := operationsEvent("spiffe://hvac.local/operations-agent-service", now)
	firstPayload, err := json.Marshal(firstEvent)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := store.ConsumeOperations(context.Background(), firstPayload, audit.MessageMetadata{
		Topic: "operations-http", Partition: 0, Offset: 0, ReceivedAt: now.Add(time.Second),
	})
	if err != nil || !inserted {
		t.Fatalf("first operations consume inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.ConsumeOperations(context.Background(), firstPayload, audit.MessageMetadata{
		Topic: "operations-http", Partition: 0, Offset: 1, ReceivedAt: now.Add(2 * time.Second),
	})
	if err != nil || inserted {
		t.Fatalf("duplicate operations consume inserted=%v err=%v", inserted, err)
	}
	firstRecord, err := store.GetRecord(context.Background(), firstEvent.OrganizationID, firstEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRecord.AggregateType != "operations-investigation" || firstRecord.Result != firstEvent.Outcome {
		t.Fatalf("unexpected operations record: %#v", firstRecord)
	}
	if strings.Contains(firstRecord.AggregateID, "investigation-001") || len(firstRecord.AggregateID) != 64 {
		t.Fatalf("operations aggregate identity was not hashed: %q", firstRecord.AggregateID)
	}
	if firstRecord.CausationID != firstEvent.AuthorizationDecisionID || firstRecord.TraceID != "" || firstRecord.Traceparent != "" {
		t.Fatalf("operations Audit and Trace boundaries were not separated: %#v", firstRecord)
	}

	changed := firstEvent
	changed.Outcome = "FAILED"
	changedPayload, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeOperations(context.Background(), changedPayload, audit.MessageMetadata{
		Topic: "operations-http", Partition: 0, Offset: 2, ReceivedAt: now.Add(3 * time.Second),
	}); !errors.Is(err, audit.ErrEnvelopeConflict) {
		t.Fatalf("different Operations event under same event ID was accepted: %v", err)
	}

	secondEvent := firstEvent
	secondEvent.EventID += ":plan"
	secondEvent.Action = "PLAN_READS"
	secondEvent.Operation = "PLAN_READS"
	secondEvent.RecordReferences = nil
	secondPayload, err := json.Marshal(secondEvent)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err = store.ConsumeOperations(context.Background(), secondPayload, audit.MessageMetadata{
		Topic: "operations-http", Partition: 0, Offset: 3, ReceivedAt: now.Add(4 * time.Second),
	})
	if err != nil || !inserted {
		t.Fatalf("second same-revision Operations event inserted=%v err=%v", inserted, err)
	}
	secondRecord, err := store.GetRecord(context.Background(), secondEvent.OrganizationID, secondEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if secondRecord.PreviousRecordHash != firstRecord.RecordHash || secondRecord.RecordHash == firstRecord.RecordHash {
		t.Fatalf("operations hash chain did not advance: first=%#v second=%#v", firstRecord, secondRecord)
	}
	if _, err := store.GetRecord(context.Background(), "org-other", firstEvent.EventID); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("cross-Organization operations query disclosed existence: %v", err)
	}
	if _, err := harness.admin.Exec(context.Background(), `UPDATE audit_ledger.records SET result='FORGED' WHERE message_id=$1`, firstEvent.EventID); err == nil {
		t.Fatal("append-only Audit Ledger allowed Operations record UPDATE")
	}
}

func TestAuditQueryIsOrganizationScopedAndLedgerIsAppendOnly(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := fixtureAuditEvent(now, "message-org-01", "session-01", "org-01", 1, "SESSION_CREATED")
	if _, err := store.Consume(context.Background(), marshalEvent(t, event), audit.MessageMetadata{Topic: sessionevent.ControlTopic, Partition: 2, Offset: 30, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRecord(context.Background(), "org-02", event.MessageID); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("cross-Organization query disclosed existence: %v", err)
	}
	if _, err := harness.admin.Exec(context.Background(), `UPDATE audit_ledger.records SET action='FORGED' WHERE message_id=$1`, event.MessageID); err == nil {
		t.Fatal("append-only Audit Ledger allowed UPDATE")
	}
}

func TestAuditRuntimeRolesCannotWriteOtherSchemas(t *testing.T) {
	harness := newLedgerHarness(t)
	consumer, err := pgxpool.New(context.Background(), harness.consumerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if _, err := consumer.Exec(context.Background(), `INSERT INTO gateway.sessions (session_id) VALUES ('forged')`); err == nil {
		t.Fatal("audit_consumer_runtime wrote Gateway schema")
	}
	query, err := pgxpool.New(context.Background(), harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	if _, err := query.Exec(context.Background(), `INSERT INTO audit_ledger.inbox (message_id) VALUES ('forged')`); err == nil {
		t.Fatal("audit_query_runtime wrote Audit Inbox")
	}
}

type ledgerHarness struct {
	consumerDSN string
	queryDSN    string
	admin       *pgxpool.Pool
}

func newLedgerHarness(t *testing.T) ledgerHarness {
	t.Helper()
	admin, err := pgxpool.New(context.Background(), requiredLedgerEnv(t, "S0_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	return ledgerHarness{consumerDSN: requiredLedgerEnv(t, "S0_AUDIT_CONSUMER_DATABASE_URL"), queryDSN: requiredLedgerEnv(t, "S0_AUDIT_QUERY_DATABASE_URL"), admin: admin}
}

func (h ledgerHarness) reset(t *testing.T) {
	t.Helper()
	_, err := h.admin.Exec(context.Background(), `TRUNCATE audit_ledger.records, audit_ledger.organization_heads, audit_ledger.inbox RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureAuditEvent(now time.Time, messageID, sessionID, organizationID string, version uint64, action string) sessionevent.SessionAuditEventV1 {
	auditAggregateID := sessionevent.AuditAggregateID(sessionID)
	return sessionevent.SessionAuditEventV1{
		MessageID: messageID, SchemaVersion: sessionevent.SchemaVersion, MessageType: sessionevent.MessageType, Producer: sessionevent.Producer,
		OrganizationID: organizationID, PartitionKey: sessionevent.AggregateType + ":" + auditAggregateID, AggregateType: sessionevent.AggregateType,
		AggregateID: auditAggregateID, AggregateVersion: version, OccurredAtUnixMS: now.UnixMilli(), PublishedAtUnixMS: now.UnixMilli(),
		CorrelationID: "request-01", TraceID: "0123456789abcdef0123456789abcdef",
		Traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		Actor:       sessionevent.ActorChainV1{InitiatingSubject: "fixture-user", InitiatingIssuer: "https://issuer.example.test", ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", ActingOrganizationID: organizationID},
		Action:      action, Result: "SUCCEEDED", PolicyRevision: "policy-v1", PayloadSHA256: sessionevent.SafePayloadHash(sessionID, strings.TrimPrefix(action, "SESSION_"), now.UnixMilli()), SessionState: strings.TrimPrefix(action, "SESSION_"),
	}
}

func marshalEvent(t *testing.T, event sessionevent.SessionAuditEventV1) []byte {
	t.Helper()
	payload, err := event.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func requiredLedgerEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not configured", name)
	}
	return value
}
