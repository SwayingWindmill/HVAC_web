package commandservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type recordingControlAlarmExec struct {
	mu       sync.Mutex
	sqls     []string
	argsList [][]any
}

func (e *recordingControlAlarmExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sqls = append(e.sqls, sql)
	e.argsList = append(e.argsList, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (e *recordingControlAlarmExec) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &fakePgxRows{}, nil
}

func (e *recordingControlAlarmExec) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

type fakeControlAlarmRow struct {
	rec           ControlAlarmOutboxRecord
	lastFailError string
	lastBackoff   time.Duration
}

type memoryControlAlarmOutboxStore struct {
	mu      sync.Mutex
	now     func() time.Time
	rows    []*fakeControlAlarmRow
	failErr error
}

func (s *memoryControlAlarmOutboxStore) ListTenantsWithPendingControlAlarms(_ context.Context, readyAt time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tenants []string
	seen := make(map[string]struct{})
	for _, row := range s.rows {
		r := row.rec
		if r.Status != ControlAlarmStatusPending || r.AvailableAt.After(readyAt) {
			continue
		}
		if r.LeaseUntil != nil && r.LeaseUntil.After(readyAt) {
			continue
		}
		if _, ok := seen[r.TenantID]; !ok {
			seen[r.TenantID] = struct{}{}
			tenants = append(tenants, r.TenantID)
		}
	}
	slices.Sort(tenants)
	return tenants, nil
}

func (s *memoryControlAlarmOutboxStore) ClaimControlAlarmOutbox(_ context.Context, tenantID, leaseOwner string, leaseFor time.Duration, limit int) ([]ControlAlarmOutboxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	var out []ControlAlarmOutboxRecord
	for _, row := range s.rows {
		if len(out) >= limit {
			break
		}
		r := &row.rec
		if r.TenantID != tenantID || r.Status != ControlAlarmStatusPending || r.AvailableAt.After(now) {
			continue
		}
		if r.LeaseUntil != nil && r.LeaseUntil.After(now) {
			continue
		}
		leaseUntil := now.Add(leaseFor)
		r.LeaseOwner = leaseOwner
		r.LeaseUntil = &leaseUntil
		r.Attempts++
		r.UpdatedAt = now
		out = append(out, *r)
	}
	return out, nil
}

func (s *memoryControlAlarmOutboxStore) MarkControlAlarmDelivered(_ context.Context, tenantID, outboxID, leaseOwner string, attempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	for _, row := range s.rows {
		r := &row.rec
		if r.TenantID == tenantID && r.OutboxID == outboxID && r.Status == ControlAlarmStatusPending && r.LeaseOwner == leaseOwner && r.Attempts == attempts {
			r.Status = ControlAlarmStatusDelivered
			r.DeliveredAt = &now
			r.LeaseOwner = ""
			r.LeaseUntil = nil
			r.LastErrorCode = ""
			r.UpdatedAt = now
			return nil
		}
	}
	return ErrControlAlarmLeaseLost
}

func (s *memoryControlAlarmOutboxStore) FailControlAlarmOutbox(_ context.Context, tenantID, outboxID, leaseOwner string, attempts int, errorCode string, maxAttempts int, backoff time.Duration) error {
	if s.failErr != nil {
		return s.failErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	for _, row := range s.rows {
		r := &row.rec
		if r.TenantID == tenantID && r.OutboxID == outboxID && r.Status == ControlAlarmStatusPending && r.LeaseOwner == leaseOwner && r.Attempts == attempts {
			row.lastFailError = errorCode
			row.lastBackoff = backoff
			r.LastErrorCode = sanitizeControlAlarmErrorCode(errorCode)
			r.LeaseOwner = ""
			r.LeaseUntil = nil
			r.UpdatedAt = now
			if r.Attempts >= maxAttempts {
				r.Status = ControlAlarmStatusDeadLetter
			} else {
				r.Status = ControlAlarmStatusPending
				r.AvailableAt = now.Add(backoff)
			}
			return nil
		}
	}
	return ErrControlAlarmLeaseLost
}

func TestInsertControlAlarmOutboxPersistsIdempotentPayload(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store := NewPostgresStore(nil, func() time.Time { return now }, func(time.Time) (string, error) {
		return "019a9000-0000-7000-8000-000000000099", nil
	})
	exec := &recordingControlAlarmExec{}

	event := ControlFailureEvent{
		Kind:       string(commandmodel.IntentOutcomeUnknown),
		TenantID:   commandTenantA,
		SiteID:     commandSiteA,
		DeviceID:   commandDeviceA,
		PointID:    commandPointA,
		CommandID:  "019a9000-2222-7000-8000-000000000001",
		Capability: commandmodel.CapabilityStart,
		ReasonCode: "MQTT_COMMAND_REPLY_TIMEOUT",
		Detail:     "reply timeout",
		OccurredAt: now,
		ActorType:  "COMMAND_RUNTIME",
		ActorID:    "command-owner",
	}

	if err := store.insertControlAlarmOutbox(t.Context(), exec, event, now); err != nil {
		t.Fatalf("insertControlAlarmOutbox: %v", err)
	}
	if len(exec.sqls) != 1 {
		t.Fatalf("exec count=%d want 1", len(exec.sqls))
	}
	sqlText := exec.sqls[0]
	if !strings.Contains(sqlText, "INSERT INTO command_runtime.control_alarm_outbox") ||
		!strings.Contains(sqlText, "ON CONFLICT (command_id, event_kind) DO NOTHING") {
		t.Fatalf("unexpected outbox insert SQL: %s", sqlText)
	}
	rawPayload, ok := exec.argsList[0][7].([]byte)
	if !ok {
		t.Fatalf("expected raw JSON payload arg, got %T", exec.argsList[0][7])
	}
	var decoded controlAlarmOutboxPayload
	if err := json.Unmarshal(rawPayload, &decoded); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if decoded.SchemaVersion != 1 || decoded.EventKind != "OUTCOME_UNKNOWN" ||
		decoded.PointID != commandPointA || decoded.Capability != "START" ||
		decoded.ReasonCode != "MQTT_COMMAND_REPLY_TIMEOUT" {
		t.Fatalf("unexpected payload: %+v", decoded)
	}

	if err := store.insertControlAlarmOutbox(t.Context(), exec, ControlFailureEvent{Kind: "OUTCOME_UNKNOWN"}, now); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest on incomplete event, got %v", err)
	}
}

func TestPostgresReconcileExecutesControlAlarmOutboxInsertInTransaction(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	seq := 0
	store := NewPostgresStore(nil, func() time.Time { return now }, func(time.Time) (string, error) {
		seq++
		return fmt.Sprintf("019a9000-0000-7000-8000-%012d", seq), nil
	})
	tx := &fakePostgresExecutor{
		queryHandlers: []func(string) pgx.Rows{
			func(string) pgx.Rows {
				return &fakePgxRows{
					rows: [][]any{{
						"019a9000-1111-7000-8000-000000000001",
						"019a9000-2222-7000-8000-000000000001",
						commandSiteA,
						commandDeviceA,
						commandPointA,
						string(commandmodel.CapabilityStop),
						"hash-prepared",
						"dispatcher-a",
						"019a9000-3333-7000-8000-000000000001",
						uint64(1),
						uint64(2),
						uint64(3),
					}},
				}
			},
		},
	}
	events, err := store.reconcileExpiredPreparedAttempts(t.Context(), tx, commandTenantA, now)
	if err != nil {
		t.Fatalf("reconcileExpiredPreparedAttempts: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	foundOutboxInsert := false
	for _, sql := range tx.execSQLs {
		if strings.Contains(sql, "INSERT INTO command_runtime.control_alarm_outbox") {
			foundOutboxInsert = true
			break
		}
	}
	if !foundOutboxInsert {
		t.Fatalf("expected atomic INSERT INTO command_runtime.control_alarm_outbox inside reconcile tx, execSQLs=%v", tx.execSQLs)
	}
}

func TestControlAlarmRelayRetryBackoffDeliveredAndDeadLetter(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) { now = now.Add(d) }

	store := &memoryControlAlarmOutboxStore{
		now: func() time.Time { return now },
		rows: []*fakeControlAlarmRow{
			{
				rec: ControlAlarmOutboxRecord{
					OutboxID:    "019a9000-aaaa-7000-8000-000000000001",
					TenantID:    commandTenantA,
					CommandID:   "019a9000-bbbb-7000-8000-000000000001",
					SiteID:      commandSiteA,
					DeviceID:    commandDeviceA,
					EventKind:   "OUTCOME_UNKNOWN",
					ReasonCode:  "LEASE_EXPIRED_WITHOUT_SEND_PROOF",
					Status:      ControlAlarmStatusPending,
					AvailableAt: now,
					CreatedAt:   now,
					UpdatedAt:   now,
					Event: ControlFailureEvent{
						Kind:       "OUTCOME_UNKNOWN",
						TenantID:   commandTenantA,
						SiteID:     commandSiteA,
						DeviceID:   commandDeviceA,
						CommandID:  "019a9000-bbbb-7000-8000-000000000001",
						ReasonCode: "LEASE_EXPIRED_WITHOUT_SEND_PROOF",
						OccurredAt: now,
					},
				},
			},
			{
				rec: ControlAlarmOutboxRecord{
					OutboxID:    "019a9000-aaaa-7000-8000-000000000002",
					TenantID:    commandTenantB,
					CommandID:   "019a9000-bbbb-7000-8000-000000000002",
					SiteID:      commandSiteA,
					DeviceID:    commandDeviceA,
					EventKind:   "EMERGENCY_DO_RELEASE",
					ReasonCode:  "EMERGENCY_DO_RELEASE",
					Status:      ControlAlarmStatusPending,
					AvailableAt: now,
					CreatedAt:   now,
					UpdatedAt:   now,
					Event: ControlFailureEvent{
						Kind:       "EMERGENCY_DO_RELEASE",
						TenantID:   commandTenantB,
						SiteID:     commandSiteA,
						DeviceID:   commandDeviceA,
						CommandID:  "019a9000-bbbb-7000-8000-000000000002",
						ReasonCode: "EMERGENCY_DO_RELEASE",
						OccurredAt: now,
					},
				},
			},
		},
	}

	attemptsByCmd := map[string]int{}
	relay, err := NewControlAlarmRelay(ControlAlarmRelayConfig{
		Store:           store,
		LeaseOwner:      "test-relay",
		PollInterval:    time.Second,
		LeaseDuration:   15 * time.Second,
		DeliveryTimeout: time.Second,
		BatchSize:       10,
		MaxAttempts:     3,
		BaseBackoff:     2 * time.Second,
		MaxBackoff:      5 * time.Second,
		Now:             func() time.Time { return now },
		Sender: func(_ context.Context, evt ControlFailureEvent) error {
			attemptsByCmd[evt.CommandID]++
			if evt.CommandID == "019a9000-bbbb-7000-8000-000000000001" {
				if attemptsByCmd[evt.CommandID] == 1 {
					return &ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_FAILED"}
				}
				return nil
			}
			if attemptsByCmd[evt.CommandID] == 1 {
				panic("simulated transient panic with secret://dsn")
			}
			return errors.New("raw internal URL https://secret-host:9999 failed")
		},
	})
	if err != nil {
		t.Fatalf("NewControlAlarmRelay: %v", err)
	}

	if got := relay.NextRetryDelay(1); got != 2*time.Second {
		t.Fatalf("attempt 1 delay=%v want 2s", got)
	}
	if got := relay.NextRetryDelay(2); got != 4*time.Second {
		t.Fatalf("attempt 2 delay=%v want 4s", got)
	}
	if got := relay.NextRetryDelay(3); got != 5*time.Second {
		t.Fatalf("attempt 3 capped delay=%v want 5s", got)
	}

	delivered, err := relay.ProcessOnce(t.Context())
	if err != nil {
		t.Fatalf("ProcessOnce 1: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("delivered=%d want 0 after first failing sweep", delivered)
	}
	if store.rows[0].rec.LastErrorCode != "CONTROL_ALARM_HTTP_FAILED" || store.rows[0].lastBackoff != 2*time.Second {
		t.Fatalf("unexpected row 0 failure state: %+v", store.rows[0])
	}
	if store.rows[1].rec.LastErrorCode != "CONTROL_ALARM_SENDER_PANIC" {
		t.Fatalf("expected sanitized panic code on row 1, got %q", store.rows[1].rec.LastErrorCode)
	}

	// Not due yet before backoff elapses.
	delivered, err = relay.ProcessOnce(t.Context())
	if err != nil || delivered != 0 {
		t.Fatalf("expected 0 delivered before backoff, got delivered=%d err=%v", delivered, err)
	}

	// After 2s backoff: cmd 1 succeeds -> DELIVERED; cmd 2 fails -> attempt 2 with raw error sanitized -> 4s backoff.
	advance(2 * time.Second)
	delivered, err = relay.ProcessOnce(t.Context())
	if err != nil {
		t.Fatalf("ProcessOnce 2: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("delivered=%d want 1", delivered)
	}
	if store.rows[0].rec.Status != ControlAlarmStatusDelivered || store.rows[0].rec.DeliveredAt == nil {
		t.Fatalf("expected row 0 DELIVERED, got %+v", store.rows[0].rec)
	}
	if store.rows[1].rec.Status != ControlAlarmStatusPending || store.rows[1].rec.LastErrorCode != "CONTROL_ALARM_DELIVERY_FAILED" || store.rows[1].lastBackoff != 4*time.Second {
		t.Fatalf("expected row 1 sanitized retry after 4s, got %+v", store.rows[1])
	}

	// After 4s backoff: cmd 2 hits attempt 3 == MaxAttempts -> DEAD_LETTER.
	advance(4 * time.Second)
	delivered, err = relay.ProcessOnce(t.Context())
	if err != nil {
		t.Fatalf("ProcessOnce 3: %v", err)
	}
	if delivered != 0 || store.rows[1].rec.Status != ControlAlarmStatusDeadLetter {
		t.Fatalf("expected row 1 DEAD_LETTER after exhausting retries, status=%s", store.rows[1].rec.Status)
	}

	// Subsequent sweeps do not pick up DELIVERED or DEAD_LETTER records.
	advance(time.Hour)
	if _, err := relay.ProcessOnce(t.Context()); err != nil {
		t.Fatalf("ProcessOnce 4: %v", err)
	}
	if attemptsByCmd["019a9000-bbbb-7000-8000-000000000001"] != 2 || attemptsByCmd["019a9000-bbbb-7000-8000-000000000002"] != 3 {
		t.Fatalf("unexpected attempt counts: %+v", attemptsByCmd)
	}
}

func TestControlAlarmRelayRunWakesOnNotifyAndStopsOnCancel(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store := &memoryControlAlarmOutboxStore{now: func() time.Time { return now }}
	deliveredCh := make(chan string, 1)
	relay, err := NewControlAlarmRelay(ControlAlarmRelayConfig{
		Store:        store,
		LeaseOwner:   "notify-relay",
		PollInterval: time.Hour,
		MaxAttempts:  3,
		BaseBackoff:  time.Millisecond,
		MaxBackoff:   time.Millisecond,
		Now:          func() time.Time { return now },
		Sender: func(_ context.Context, evt ControlFailureEvent) error {
			deliveredCh <- evt.CommandID
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	store.mu.Lock()
	store.rows = append(store.rows, &fakeControlAlarmRow{
		rec: ControlAlarmOutboxRecord{
			OutboxID:    "019a9000-cccc-7000-8000-000000000001",
			TenantID:    commandTenantA,
			CommandID:   "019a9000-dddd-7000-8000-000000000001",
			SiteID:      commandSiteA,
			DeviceID:    commandDeviceA,
			EventKind:   "OUTCOME_UNKNOWN",
			Status:      ControlAlarmStatusPending,
			AvailableAt: now,
			CreatedAt:   now,
			UpdatedAt:   now,
			Event: ControlFailureEvent{
				Kind:      "OUTCOME_UNKNOWN",
				TenantID:  commandTenantA,
				SiteID:    commandSiteA,
				DeviceID:  commandDeviceA,
				CommandID: "019a9000-dddd-7000-8000-000000000001",
			},
		},
	})
	store.mu.Unlock()

	relay.Notify()
	select {
	case cmdID := <-deliveredCh:
		if cmdID != "019a9000-dddd-7000-8000-000000000001" {
			t.Fatalf("unexpected delivered command %s", cmdID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Notify-triggered relay delivery")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay.Run did not stop on context cancel")
	}
}

func TestPostgresControlAlarmOutboxPersistenceLeaseAndDeadLetter(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()

	clock := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	store := NewPostgresStore(opened.pool, func() time.Time { return clock }, nil)

	wakeups := 0
	store.SetControlAlarmOutboxWakeup(func() { wakeups++ })

	// Create two commands on independent devices and resolve both into OUTCOME_UNKNOWN.
	req1 := postgresCommandRequest("outbox-pg-1", 24)
	sub1, err := store.Submit(ctx, req1)
	if err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	commandDeviceB := "018f3e00-3000-7000-8000-000000000002"
	req2 := postgresCommandRequest("outbox-pg-2", 23.5)
	req2.DeviceID = commandDeviceB
	req2.PointID = "018f3e00-4000-7000-8000-000000000002"
	req2.Authorization.DeviceID = commandDeviceB
	req2.Authorization.GrantID = "018f3e00-9000-7000-8000-000000000002"
	sub2, err := store.Submit(ctx, req2)
	if err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// Claim and expire lease on command 1, plus resolve command 2 into OUTCOME_UNKNOWN.
	env1, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-1", 10*time.Second)
	if err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if err := store.PrepareConnectorEvidence(ctx, commandmodel.PreparedConnectorEvidence{
		AttemptID: env1.AttemptID, CommandID: env1.CommandID,
		TenantID: env1.TenantID, SiteID: env1.SiteID, DeviceID: env1.DeviceID,
		ExternalDeviceID: "CHILLER-01", ExecutionFence: env1.ExecutionFence, PayloadHash: env1.PayloadHash,
		MappingRevision: "mqtt-command-v1", BindingRevision: "registry-source-key:1",
		ProviderEndpoint: "hvac/v1/018f3e00-4000-7000-8000-000000000100/down/command", ProviderMethod: string(env1.Capability),
		RequestSHA256: strings.Repeat("a", 64), PreparedAt: clock,
	}); err != nil {
		t.Fatalf("prepare 1: %v", err)
	}
	if err := store.ResolveDispatch(ctx, env1, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorRequestCommitted, EvidenceID: "ev-outbox-1", FailureCode: "MQTT_TIMEOUT",
	}); err != nil {
		t.Fatalf("resolve 1: %v", err)
	}

	env2, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-2", 10*time.Second)
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	if err := store.PrepareConnectorEvidence(ctx, commandmodel.PreparedConnectorEvidence{
		AttemptID: env2.AttemptID, CommandID: env2.CommandID,
		TenantID: env2.TenantID, SiteID: env2.SiteID, DeviceID: env2.DeviceID,
		ExternalDeviceID: "CHILLER-01", ExecutionFence: env2.ExecutionFence, PayloadHash: env2.PayloadHash,
		MappingRevision: "mqtt-command-v1", BindingRevision: "registry-source-key:1",
		ProviderEndpoint: "hvac/v1/018f3e00-4000-7000-8000-000000000100/down/command", ProviderMethod: string(env2.Capability),
		RequestSHA256: strings.Repeat("a", 64), PreparedAt: clock,
	}); err != nil {
		t.Fatalf("prepare 2: %v", err)
	}
	if err := store.ResolveDispatch(ctx, env2, commandmodel.ConnectorResult{
		Phase: commandmodel.ConnectorRequestCommitted, EvidenceID: "ev-outbox-2", FailureCode: "MQTT_DISCONNECT",
	}); err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if wakeups < 2 {
		t.Fatalf("wakeups=%d want >= 2", wakeups)
	}

	// Idempotent EnqueueControlAlarm must succeed via ON CONFLICT DO NOTHING without duplicate rows.
	if err := store.EnqueueControlAlarm(ctx, ControlFailureEvent{
		Kind:       string(commandmodel.IntentOutcomeUnknown),
		TenantID:   commandTenantA,
		SiteID:     commandSiteA,
		DeviceID:   commandDeviceA,
		PointID:    commandPointA,
		CommandID:  sub1.Intent.ID,
		Capability: commandmodel.CapabilitySetTemperatureSetpoint,
		ReasonCode: "REQUEST_COMMITTED_OUTCOME_UNKNOWN",
		OccurredAt: clock,
	}); err != nil {
		t.Fatalf("idempotent EnqueueControlAlarm: %v", err)
	}

	tenants, err := store.ListTenantsWithPendingControlAlarms(ctx, clock)
	if err != nil || len(tenants) != 1 || tenants[0] != commandTenantA {
		t.Fatalf("pending tenants=%v err=%v", tenants, err)
	}

	claimed, err := store.ClaimControlAlarmOutbox(ctx, commandTenantA, "relay-1", 15*time.Second, 10)
	if err != nil {
		t.Fatalf("ClaimControlAlarmOutbox: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed=%d want 2", len(claimed))
	}
	byCmd := map[string]ControlAlarmOutboxRecord{}
	for _, rec := range claimed {
		byCmd[rec.CommandID] = rec
	}
	r1 := byCmd[sub1.Intent.ID]
	r2 := byCmd[sub2.Intent.ID]
	if r1.Attempts != 1 || r1.Event.PointID != commandPointA || r1.Event.Capability != commandmodel.CapabilitySetTemperatureSetpoint {
		t.Fatalf("unexpected claimed record 1: %+v", r1)
	}

	if err := store.MarkControlAlarmDelivered(ctx, commandTenantA, r1.OutboxID, "relay-1", r1.Attempts); err != nil {
		t.Fatalf("MarkControlAlarmDelivered: %v", err)
	}
	// Stale attempt count must be rejected even with matching lease owner.
	if err := store.FailControlAlarmOutbox(ctx, commandTenantA, r2.OutboxID, "relay-1", r2.Attempts+1, "CONTROL_ALARM_HTTP_FAILED", 2, 5*time.Second); !errors.Is(err, ErrControlAlarmLeaseLost) {
		t.Fatalf("expected ErrControlAlarmLeaseLost on mismatched attempts, got %v", err)
	}
	if err := store.FailControlAlarmOutbox(ctx, commandTenantA, r2.OutboxID, "relay-1", r2.Attempts, "CONTROL_ALARM_HTTP_FAILED", 2, 5*time.Second); err != nil {
		t.Fatalf("FailControlAlarmOutbox attempt 1: %v", err)
	}

	clock = clock.Add(5 * time.Second)
	claimed2, err := store.ClaimControlAlarmOutbox(ctx, commandTenantA, "relay-2", 15*time.Second, 10)
	if err != nil || len(claimed2) != 1 || claimed2[0].OutboxID != r2.OutboxID || claimed2[0].Attempts != 2 {
		t.Fatalf("reclaim after backoff=%+v err=%v", claimed2, err)
	}
	if err := store.FailControlAlarmOutbox(ctx, commandTenantA, r2.OutboxID, "relay-2", claimed2[0].Attempts, "CONTROL_ALARM_HTTP_REJECTED", 2, 5*time.Second); err != nil {
		t.Fatalf("FailControlAlarmOutbox dead letter: %v", err)
	}

	var status1, status2, errCode2 string
	if err := admin.QueryRow(ctx, `SELECT status FROM command_runtime.control_alarm_outbox WHERE outbox_id = $1::uuid`, r1.OutboxID).Scan(&status1); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT status, last_error_code FROM command_runtime.control_alarm_outbox WHERE outbox_id = $1::uuid`, r2.OutboxID).Scan(&status2, &errCode2); err != nil {
		t.Fatal(err)
	}
	if status1 != "DELIVERED" || status2 != "DEAD_LETTER" || errCode2 != "CONTROL_ALARM_HTTP_REJECTED" {
		t.Fatalf("unexpected final states: status1=%s status2=%s errCode2=%s", status1, status2, errCode2)
	}

	// Verify dead-letter list, cross-tenant state count, and atomic replay with wakeup.
	deadLetters, err := store.ListControlAlarmDeadLetters(ctx, commandTenantA, commandSiteA, 10)
	if err != nil {
		t.Fatalf("ListControlAlarmDeadLetters: %v", err)
	}
	if len(deadLetters) != 1 || deadLetters[0].OutboxID != r2.OutboxID || deadLetters[0].LastErrorCode != "CONTROL_ALARM_HTTP_REJECTED" {
		t.Fatalf("unexpected Postgres dead letters: %+v", deadLetters)
	}
	countsBefore, err := store.CountControlAlarmOutboxStates(ctx)
	if err != nil || countsBefore.Pending != 0 || countsBefore.DeadLetter != 1 {
		t.Fatalf("unexpected outbox counts before replay: %+v err=%v", countsBefore, err)
	}

	wakeupsBefore := wakeups
	replayed, err := store.ReplayControlAlarmDeadLetter(ctx, commandTenantA, commandSiteA, r2.OutboxID)
	if err != nil || !replayed || wakeups != wakeupsBefore+1 {
		t.Fatalf("first Postgres replay replayed=%v wakeups=%d/%d err=%v", replayed, wakeups, wakeupsBefore+1, err)
	}
	replayedAgain, err := store.ReplayControlAlarmDeadLetter(ctx, commandTenantA, commandSiteA, r2.OutboxID)
	if err != nil || replayedAgain || wakeups != wakeupsBefore+1 {
		t.Fatalf("second idempotent Postgres replay replayed=%v wakeups=%d err=%v", replayedAgain, wakeups, err)
	}
	if _, err := store.ReplayControlAlarmDeadLetter(ctx, commandTenantA, commandSiteA, "019a9000-ffff-7000-8000-000000000099"); !errors.Is(err, ErrControlAlarmNotFound) {
		t.Fatalf("expected ErrControlAlarmNotFound for missing outboxId, got %v", err)
	}
	countsAfter, err := store.CountControlAlarmOutboxStates(ctx)
	if err != nil || countsAfter.Pending != 1 || countsAfter.DeadLetter != 0 {
		t.Fatalf("unexpected outbox counts after replay: %+v err=%v", countsAfter, err)
	}
}

func TestControlAlarmRelayDeadLetterMetricsAndReasonFamily(t *testing.T) {
	allowedFamilies := map[string]bool{
		"transport": true, "dependency": true, "authorization": true,
		"scope": true, "capacity": true, "rate_limited": true, "unknown": true,
	}
	cases := []struct {
		code string
		want string
	}{
		{"CONTROL_ALARM_HTTP_FAILED", "transport"},
		{"CONTROL_ALARM_DELIVERY_TIMEOUT", "transport"},
		{"CONTROL_ALARM_HTTP_REJECTED", "dependency"},
		{"CONTROL_ALARM_SENDER_PANIC", "dependency"},
		{"CONTROL_ALARM_PAYLOAD_INVALID", "scope"},
		{"UPSTREAM_UNAUTHORIZED_403", "authorization"},
		{"HTTP_429_RATE_LIMITED", "rate_limited"},
		{"QUEUE_CAPACITY_EXCEEDED", "capacity"},
		{"CONTROL_ALARM_DELIVERY_CANCELED", "unknown"},
		{"UNCLASSIFIED_FOO", "unknown"},
	}
	for _, tc := range cases {
		got := ControlAlarmErrorReasonFamily(tc.code)
		if !allowedFamilies[got] || got != tc.want {
			t.Fatalf("ControlAlarmErrorReasonFamily(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}

	now := time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC)
	store := &memoryControlAlarmOutboxStore{
		now: func() time.Time { return now },
		rows: []*fakeControlAlarmRow{
			{
				rec: ControlAlarmOutboxRecord{
					OutboxID: "019a9000-cccc-7000-8000-000000000001", TenantID: commandTenantA,
					CommandID: "019a9000-bbbb-7000-8000-000000000011", SiteID: commandSiteA, DeviceID: commandDeviceA,
					EventKind: "OUTCOME_UNKNOWN", Status: ControlAlarmStatusPending, AvailableAt: now,
					Event: ControlFailureEvent{Kind: "OUTCOME_UNKNOWN", TenantID: commandTenantA, SiteID: commandSiteA, DeviceID: commandDeviceA, CommandID: "019a9000-bbbb-7000-8000-000000000011"},
				},
			},
			{
				rec: ControlAlarmOutboxRecord{
					OutboxID: "019a9000-cccc-7000-8000-000000000002", TenantID: commandTenantA,
					CommandID: "019a9000-bbbb-7000-8000-000000000012", SiteID: commandSiteA, DeviceID: commandDeviceA,
					EventKind: "EMERGENCY_DO_RELEASE", Status: ControlAlarmStatusPending, AvailableAt: now,
					Event: ControlFailureEvent{Kind: "EMERGENCY_DO_RELEASE", TenantID: commandTenantA, SiteID: commandSiteA, DeviceID: commandDeviceA, CommandID: "019a9000-bbbb-7000-8000-000000000012"},
				},
			},
		},
	}
	metrics := observability.NewRegistry()
	relay, err := NewControlAlarmRelay(ControlAlarmRelayConfig{
		Store:       store,
		Metrics:     metrics,
		LeaseOwner:  "metrics-relay",
		MaxAttempts: 1,
		BaseBackoff: time.Second,
		MaxBackoff:  time.Second,
		Now:         func() time.Time { return now },
		Sender: func(_ context.Context, evt ControlFailureEvent) error {
			if evt.Kind == "OUTCOME_UNKNOWN" {
				return &ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_FAILED"}
			}
			return &ControlAlarmDeliveryError{Code: "CONTROL_ALARM_HTTP_REJECTED"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.ProcessOnce(t.Context()); err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}

	scrapeRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrapeRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrapeRec.Body.String()
	if !strings.Contains(body, `hvac_command_control_alarm_dead_letter_total{reason_family="transport"} 1`) {
		t.Fatalf("missing transport dead-letter counter in metrics:\n%s", body)
	}
	if !strings.Contains(body, `hvac_command_control_alarm_dead_letter_total{reason_family="dependency"} 1`) {
		t.Fatalf("missing dependency dead-letter counter in metrics:\n%s", body)
	}
}

func TestControlAuthorityAndHTTPControlAlarmDeadLetters(t *testing.T) {
	now := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	h, _ := seedPlantHarness(t, now)

	otherSite := "018f3e00-1000-7000-8000-000000000099"
	dlOld := "019a9000-dddd-7000-8000-000000000001"
	dlNew := "019a9000-dddd-7000-8000-000000000002"
	dlMid := "019a9000-dddd-7000-8000-000000000003"
	pendingID := "019a9000-dddd-7000-8000-000000000004"
	otherSiteID := "019a9000-dddd-7000-8000-000000000005"

	h.Store.SeedControlAlarmOutbox(ControlAlarmOutboxRecord{
		OutboxID: dlOld, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
		CommandID: "019a9000-eeee-7000-8000-000000000001", EventKind: "OUTCOME_UNKNOWN",
		ReasonCode: "LEASE_EXPIRED", Status: ControlAlarmStatusDeadLetter, Attempts: 10,
		LastErrorCode: "CONTROL_ALARM_HTTP_FAILED\nsecret", CreatedAt: now.Add(-3 * time.Minute), UpdatedAt: now.Add(-3 * time.Minute),
		Event: ControlFailureEvent{Detail: "Bearer secret-token-should-never-leak"},
	})
	h.Store.SeedControlAlarmOutbox(ControlAlarmOutboxRecord{
		OutboxID: dlMid, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
		CommandID: "019a9000-eeee-7000-8000-000000000002", EventKind: "PRE_SEND_REJECTED",
		ReasonCode: "INTERLOCK_FAILED", Status: ControlAlarmStatusDeadLetter, Attempts: 10,
		LastErrorCode: "CONTROL_ALARM_HTTP_REJECTED", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute),
	})
	h.Store.SeedControlAlarmOutbox(ControlAlarmOutboxRecord{
		OutboxID: dlNew, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
		CommandID: "019a9000-eeee-7000-8000-000000000003", EventKind: "EMERGENCY_DO_RELEASE",
		ReasonCode: "EMERGENCY_DO_RELEASE", Status: ControlAlarmStatusDeadLetter, Attempts: 10,
		LastErrorCode: "CONTROL_ALARM_DELIVERY_TIMEOUT", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	})
	h.Store.SeedControlAlarmOutbox(ControlAlarmOutboxRecord{
		OutboxID: pendingID, TenantID: caTenantID, SiteID: caSiteID, DeviceID: caChillerDev,
		CommandID: "019a9000-eeee-7000-8000-000000000004", EventKind: "OUTCOME_UNKNOWN",
		Status: ControlAlarmStatusPending, CreatedAt: now, UpdatedAt: now,
	})
	h.Store.SeedControlAlarmOutbox(ControlAlarmOutboxRecord{
		OutboxID: otherSiteID, TenantID: caTenantID, SiteID: otherSite, DeviceID: caChillerDev,
		CommandID: "019a9000-eeee-7000-8000-000000000005", EventKind: "OUTCOME_UNKNOWN",
		Status: ControlAlarmStatusDeadLetter, CreatedAt: now, UpdatedAt: now,
	})

	signCtx := func() string {
		claims := identitycontext.DelegationClaims{
			Issuer: "spiffe://hvac.local/platform-gateway", Subject: "op-1", SubjectIssuer: "https://issuer.test",
			ExecutingService: "spiffe://hvac.local/platform-gateway", Audience: "command-service",
			TenantID: caTenantID, Actions: []string{OperatorControlContextAction}, Scopes: []string{"tenant:" + caTenantID, "session:sess-1"},
			PolicyRevision: "policy-v1", SessionID: "sess-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(),
			TokenID: "ctx-dlq-1",
		}
		signed, err := identitycontext.SignDelegation(h.GatewaySigner, claims)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	listPath := InternalOperatorPrefix + "/sites/" + caSiteID + "/control-alarms/dead-letters"
	replayPath := listPath + "/" + dlNew + "/replay"

	if got := ObservabilityRoute(httptest.NewRequest(http.MethodGet, listPath, nil)); got != "control.state" {
		t.Fatalf("ObservabilityRoute list = %q want control.state", got)
	}
	if got := ObservabilityRoute(httptest.NewRequest(http.MethodPost, replayPath, nil)); got != "control.state" {
		t.Fatalf("ObservabilityRoute replay = %q want control.state", got)
	}

	// List bounded by limit=2, newest first, sanitized without payload/secret leakage.
	listReq := httptest.NewRequest(http.MethodGet, listPath+"?limit=2", nil)
	listReq.Header.Set("X-Command-Control-Context", signCtx())
	listRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list dead-letters status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	rawBody := listRec.Body.String()
	if strings.Contains(rawBody, "secret-token") || strings.Contains(rawBody, `"payload"`) {
		t.Fatalf("dead-letter response must not contain raw payload or secrets: %s", rawBody)
	}
	var listPayload struct {
		Data []ControlAlarmDeadLetterView `json:"data"`
	}
	if json.Unmarshal(listRec.Body.Bytes(), &listPayload) != nil || len(listPayload.Data) != 2 {
		t.Fatalf("unexpected list payload: %s", rawBody)
	}
	if listPayload.Data[0].OutboxID != dlNew || listPayload.Data[1].OutboxID != dlMid {
		t.Fatalf("expected newest-first order [dlNew, dlMid], got %+v", listPayload.Data)
	}

	// Invalid limit=51 -> 400.
	badLimitReq := httptest.NewRequest(http.MethodGet, listPath+"?limit=51", nil)
	badLimitReq.Header.Set("X-Command-Control-Context", signCtx())
	badLimitRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(badLimitRec, badLimitReq)
	if badLimitRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=51, got %d", badLimitRec.Code)
	}

	// Unauthorized site or missing permission -> 403.
	h.IAM.DenyScheduleManage = true
	forbidReq := httptest.NewRequest(http.MethodGet, listPath, nil)
	forbidReq.Header.Set("X-Command-Control-Context", signCtx())
	forbidRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(forbidRec, forbidReq)
	if forbidRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when ActionScheduleManage denied, got %d", forbidRec.Code)
	}
	h.IAM.DenyScheduleManage = false

	// Replay with non-empty invalid JSON body -> 400.
	badBodyReq := httptest.NewRequest(http.MethodPost, replayPath, bytes.NewBufferString(`{"unexpected":true}`))
	badBodyReq.Header.Set("X-Command-Control-Context", signCtx())
	badBodyRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(badBodyRec, badBodyReq)
	if badBodyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unexpected replay JSON body, got %d", badBodyRec.Code)
	}

	// Missing / other-site outboxId -> 404.
	missingReq := httptest.NewRequest(http.MethodPost, listPath+"/"+otherSiteID+"/replay", bytes.NewBufferString(`{}`))
	missingReq.Header.Set("X-Command-Control-Context", signCtx())
	missingRec := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-site outboxId replay, got %d", missingRec.Code)
	}

	// First replay moves DEAD_LETTER -> PENDING and fires wakeup.
	repReq1 := httptest.NewRequest(http.MethodPost, replayPath, bytes.NewBufferString(`{}`))
	repReq1.Header.Set("X-Command-Control-Context", signCtx())
	repRec1 := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(repRec1, repReq1)
	var rep1 ControlAlarmDeadLetterReplayResponse
	if repRec1.Code != http.StatusOK || json.NewDecoder(repRec1.Body).Decode(&rep1) != nil || !rep1.Replayed || rep1.OutboxID != dlNew {
		t.Fatalf("first replay status=%d resp=%+v", repRec1.Code, rep1)
	}
	if h.Store.AlarmWakeups != 1 {
		t.Fatalf("AlarmWakeups=%d want 1", h.Store.AlarmWakeups)
	}

	// Second replay with empty body is idempotent with replayed=false and no extra wakeup.
	repReq2 := httptest.NewRequest(http.MethodPost, replayPath, nil)
	repReq2.Header.Set("X-Command-Control-Context", signCtx())
	repRec2 := httptest.NewRecorder()
	h.OperatorHandler.ServeHTTP(repRec2, repReq2)
	var rep2 ControlAlarmDeadLetterReplayResponse
	if repRec2.Code != http.StatusOK || json.NewDecoder(repRec2.Body).Decode(&rep2) != nil || rep2.Replayed || rep2.OutboxID != dlNew {
		t.Fatalf("second idempotent replay status=%d resp=%+v", repRec2.Code, rep2)
	}
	if h.Store.AlarmWakeups != 1 {
		t.Fatalf("AlarmWakeups=%d want 1 after idempotent replay", h.Store.AlarmWakeups)
	}
}
