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
	"github.com/quanlaihe/hvac-web/modules/audit/internal/audit"
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
	inserted, err := store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now.Add(time.Second)})
	if err != nil || !inserted {
		t.Fatalf("first consume inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now.Add(2 * time.Second)})
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
	inserted, err = store.Consume(context.Background(), firstPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now.Add(3 * time.Second)})
	if err != nil || inserted {
		t.Fatalf("restart duplicate inserted=%v err=%v", inserted, err)
	}

	secondEvent := fixtureAuditEvent(now.Add(time.Second), "message-02", "session-01", "org-01", 2, "SESSION_REVOKED")
	secondEvent.CausationID = "message-01"
	secondPayload := marshalEvent(t, secondEvent)
	inserted, err = store.Consume(context.Background(), secondPayload, audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now.Add(4 * time.Second)})
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
	if _, err := store.Consume(context.Background(), marshalEvent(t, original), audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Action = "SESSION_REVOKED"
	if _, err := store.Consume(context.Background(), marshalEvent(t, changed), audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now.Add(time.Second)}); !errors.Is(err, audit.ErrEnvelopeConflict) {
		t.Fatalf("different protobuf under same message ID was accepted: %v", err)
	}
}

func TestOperationsAuditPersistsExactlyOnceAndAdvancesTenantHashChain(t *testing.T) {
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
		Topic: "operations-http", ReceivedAt: now.Add(time.Second),
	})
	if err != nil || !inserted {
		t.Fatalf("first operations consume inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.ConsumeOperations(context.Background(), firstPayload, audit.MessageMetadata{
		Topic: "operations-http", ReceivedAt: now.Add(2 * time.Second),
	})
	if err != nil || inserted {
		t.Fatalf("duplicate operations consume inserted=%v err=%v", inserted, err)
	}
	firstRecord, err := store.GetRecord(context.Background(), firstEvent.TenantID, firstEvent.EventID)
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
		Topic: "operations-http", ReceivedAt: now.Add(3 * time.Second),
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
		Topic: "operations-http", ReceivedAt: now.Add(4 * time.Second),
	})
	if err != nil || !inserted {
		t.Fatalf("second same-revision Operations event inserted=%v err=%v", inserted, err)
	}
	secondRecord, err := store.GetRecord(context.Background(), secondEvent.TenantID, secondEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if secondRecord.PreviousRecordHash != firstRecord.RecordHash || secondRecord.RecordHash == firstRecord.RecordHash {
		t.Fatalf("operations hash chain did not advance: first=%#v second=%#v", firstRecord, secondRecord)
	}
	if _, err := store.GetRecord(context.Background(), "tenant-other", firstEvent.EventID); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("cross-Tenant operations query disclosed existence: %v", err)
	}
	if _, err := harness.admin.Exec(context.Background(), `UPDATE audit_ledger.records SET result='FORGED' WHERE message_id=$1`, firstEvent.EventID); err == nil {
		t.Fatal("append-only Audit Ledger allowed Operations record UPDATE")
	}
}

func TestAuditQueryIsTenantScopedAndLedgerIsAppendOnly(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := fixtureAuditEvent(now, "message-tenant-01", "session-01", "tenant-01", 1, "SESSION_CREATED")
	if _, err := store.Consume(context.Background(), marshalEvent(t, event), audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRecord(context.Background(), "tenant-02", event.MessageID); !errors.Is(err, audit.ErrRecordNotFound) {
		t.Fatalf("cross-Tenant query disclosed existence: %v", err)
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

func TestDomainAuditPersistsWithHashChainAndSearchCategoryFiltering(t *testing.T) {
	harness := newLedgerHarness(t)
	harness.reset(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store, err := audit.OpenStore(context.Background(), harness.consumerDSN, harness.queryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tenantID := "018f3d00-0000-7000-8000-000000000001"
	sessionEvent := fixtureAuditEvent(now, "msg-session-01", "session-01", tenantID, 1, "SESSION_CREATED")
	if _, err := store.Consume(context.Background(), marshalEvent(t, sessionEvent), audit.MessageMetadata{Topic: sessionevent.ControlTopic, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}

	controlPayload := []byte(`{"schemaVersion":1,"messageId":"msg-control-01","tenantId":"018f3d00-0000-7000-8000-000000000001","category":"control-lock","targetId":"site:songyaxuan","aggregateVersion":1,"occurredAt":"2026-07-19T12:01:00Z","initiatingSubject":"operator-01","initiatingIssuer":"https://issuer.example.test","executingService":"platform-gateway","executingSpiffeId":"spiffe://hvac.local/platform-gateway","action":"SITE_CONTROL_MODE_UPDATED","outcome":"SUCCEEDED","policyRevision":"policy-v1","correlationId":"corr-01","causationId":"msg-control-01","traceId":"0123456789abcdef0123456789abcdef","clientIp":"10.20.30.40","clientKind":"MINI_PROGRAM","actorDisplay":"李值班","targetLabel":"松雅轩冷站","changeSummary":{"before":"LOCAL_MANUAL","after":"REMOTE_AUTO"}}`)
	inserted, err := store.ConsumeDomain(context.Background(), controlPayload, audit.MessageMetadata{Topic: audit.GatewayDomainTopic, ReceivedAt: now.Add(time.Minute)})
	if err != nil || !inserted {
		t.Fatalf("ConsumeDomain inserted=%v err=%v", inserted, err)
	}

	schedulePayload := []byte(`{"schemaVersion":1,"messageId":"msg-schedule-01","tenantId":"018f3d00-0000-7000-8000-000000000001","category":"operating-schedule","targetId":"site:songyaxuan","aggregateVersion":1,"occurredAt":"2026-07-19T12:02:00Z","initiatingSubject":"operator-01","initiatingIssuer":"https://issuer.example.test","executingService":"platform-gateway","executingSpiffeId":"spiffe://hvac.local/platform-gateway","action":"OPERATING_SCHEDULE_UPDATED","outcome":"SUCCEEDED","policyRevision":"policy-v1","correlationId":"corr-02","causationId":"msg-schedule-01","traceId":"0123456789abcdef0123456789abcdee","clientIp":"10.20.30.41","clientKind":"BROWSER","actorDisplay":"张工","targetLabel":"松雅轩冷站运行计划","changeSummary":{"entriesCount":2}}`)
	if _, err := store.ConsumeDomain(context.Background(), schedulePayload, audit.MessageMetadata{Topic: audit.GatewayDomainTopic, ReceivedAt: now.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	first, err := store.GetRecord(context.Background(), tenantID, "msg-session-01")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetRecord(context.Background(), tenantID, "msg-control-01")
	if err != nil {
		t.Fatal(err)
	}
	if second.PreviousRecordHash != first.RecordHash || second.ClientIP != "10.20.30.40" || second.ClientKind != audit.ClientKindMiniProgram {
		t.Fatalf("domain record hash chain or metadata mismatch: first=%#v second=%#v", first, second)
	}

	grouped, err := store.SearchRecords(context.Background(), tenantID, audit.SearchFilter{Category: audit.CategoryControlSchedule, Limit: 10})
	if err != nil || len(grouped) != 2 {
		t.Fatalf("SearchRecords(control-schedule) len=%d err=%v", len(grouped), err)
	}
	all, err := store.SearchRecords(context.Background(), tenantID, audit.SearchFilter{Limit: 10})
	if err != nil || len(all) != 3 {
		t.Fatalf("SearchRecords(all) len=%d err=%v", len(all), err)
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
	_, err := h.admin.Exec(context.Background(), `TRUNCATE audit_ledger.records, audit_ledger.tenant_heads, audit_ledger.inbox RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureAuditEvent(now time.Time, messageID, sessionID, tenantID string, version uint64, action string) sessionevent.SessionAuditEventV1 {
	auditAggregateID := sessionevent.AuditAggregateID(sessionID)
	return sessionevent.SessionAuditEventV1{
		MessageID: messageID, SchemaVersion: sessionevent.SchemaVersion, MessageType: sessionevent.MessageType, Producer: sessionevent.Producer,
		TenantID: tenantID, PartitionKey: sessionevent.AggregateType + ":" + auditAggregateID, AggregateType: sessionevent.AggregateType,
		AggregateID: auditAggregateID, AggregateVersion: version, OccurredAtUnixMS: now.UnixMilli(), PublishedAtUnixMS: now.UnixMilli(),
		CorrelationID: "request-01", TraceID: "0123456789abcdef0123456789abcdef",
		Traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		Actor:       sessionevent.ActorChainV1{InitiatingSubject: "fixture-user", InitiatingIssuer: "https://issuer.example.test", ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", TenantID: tenantID},
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
