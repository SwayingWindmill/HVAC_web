package commandservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type fakePgxRows struct {
	rows   [][]any
	idx    int
	closed bool
	err    error
}

func (r *fakePgxRows) Close()                                       { r.closed = true }
func (r *fakePgxRows) Err() error                                   { return r.err }
func (r *fakePgxRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakePgxRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakePgxRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakePgxRows) RawValues() [][]byte                          { return nil }
func (r *fakePgxRows) Conn() *pgx.Conn                              { return nil }
func (r *fakePgxRows) Next() bool {
	if r.closed || r.idx >= len(r.rows) {
		r.closed = true
		return false
	}
	r.idx++
	return true
}

func (r *fakePgxRows) Scan(dest ...any) error {
	row := r.rows[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("column count mismatch: got %d dests, %d values", len(dest), len(row))
	}
	for i := range row {
		dv := reflect.ValueOf(dest[i])
		if dv.Kind() != reflect.Pointer || dv.IsNil() {
			return fmt.Errorf("dest[%d] must be non-nil pointer", i)
		}
		rv := reflect.ValueOf(row[i])
		if !rv.IsValid() {
			dv.Elem().SetZero()
			continue
		}
		dv.Elem().Set(rv)
	}
	return nil
}

type fakePostgresExecutor struct {
	queryHandlers []func(sql string) pgx.Rows
	execSQLs      []string
	commitErr     error
	committed     bool
}

func (f *fakePostgresExecutor) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.execSQLs = append(f.execSQLs, sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *fakePostgresExecutor) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	for _, fn := range f.queryHandlers {
		if rows := fn(sql); rows != nil {
			return rows, nil
		}
	}
	return &fakePgxRows{}, nil
}

func (f *fakePostgresExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

func (f *fakePostgresExecutor) Commit(_ context.Context) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.committed = true
	return nil
}

func TestPostgresStoreExpiredReconciliationCollectsAndNotifiesPostCommit(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	seq := 0
	store := NewPostgresStore(nil, func() time.Time { return now }, func(time.Time) (string, error) {
		seq++
		return fmt.Sprintf("019a9000-0000-7000-8000-%012d", seq), nil
	})
	events := make(chan ControlFailureEvent, 8)
	store.SetControlFailureNotifier(func(_ context.Context, evt ControlFailureEvent) {
		events <- evt
	})

	t.Run("expired_prepared_dispatch_notifies_only_after_commit", func(t *testing.T) {
		var selectedSQL string
		tx := &fakePostgresExecutor{
			queryHandlers: []func(string) pgx.Rows{
				func(sql string) pgx.Rows {
					selectedSQL = sql
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

		collected, err := store.reconcileExpiredPreparedAttempts(t.Context(), tx, commandTenantA, now)
		if err != nil {
			t.Fatalf("reconcileExpiredPreparedAttempts: %v", err)
		}
		if !strings.Contains(selectedSQL, "i.point_id::text") || !strings.Contains(selectedSQL, "i.capability_name") {
			t.Fatalf("expected point_id and capability_name in SQL projection, got %s", selectedSQL)
		}
		if len(collected) != 1 {
			t.Fatalf("collected=%d want 1", len(collected))
		}
		select {
		case premature := <-events:
			t.Fatalf("notification fired before tx.Commit: %+v", premature)
		case <-time.After(60 * time.Millisecond):
		}

		// Commit failure must suppress notification.
		tx.commitErr = errors.New("40001 serialization failure")
		if err := store.commitAndNotifyFailures(t.Context(), tx, collected); err == nil {
			t.Fatal("expected commit failure")
		}
		select {
		case premature := <-events:
			t.Fatalf("notification fired after failed commit: %+v", premature)
		case <-time.After(60 * time.Millisecond):
		}

		// Commit success emits full metadata asynchronously.
		tx.commitErr = nil
		if err := store.commitAndNotifyFailures(t.Context(), tx, collected); err != nil {
			t.Fatalf("commitAndNotifyFailures: %v", err)
		}
		select {
		case evt := <-events:
			if evt.Kind != string(commandmodel.IntentOutcomeUnknown) ||
				evt.ReasonCode != "LEASE_EXPIRED_WITHOUT_SEND_PROOF" ||
				evt.CommandID != "019a9000-2222-7000-8000-000000000001" ||
				evt.PointID != commandPointA ||
				evt.Capability != commandmodel.CapabilityStop ||
				evt.DeviceID != commandDeviceA ||
				evt.SiteID != commandSiteA ||
				evt.TenantID != commandTenantA {
				t.Fatalf("unexpected prepared expiry event: %+v", evt)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for post-commit prepared expiry event")
		}
	})

	t.Run("expired_acknowledged_verification_covers_level_and_pulse", func(t *testing.T) {
		levelParams, _ := json.Marshal(commandmodel.CommandParameters{})
		levelText := "u11:1"
		levelEdge, _ := json.Marshal(&commandmodel.EdgeExecutionEvidence{
			Requested:          commandmodel.TextScalar("START"),
			Effective:          &commandmodel.ScalarValue{Text: &levelText},
			Applied:            &commandmodel.ScalarValue{Text: &levelText},
			WinnerControllerID: "cloud-command-intent",
			Cycle:              1,
		})
		pulseText := "u14:pulse(500ms)->0"
		pulseEdge, _ := json.Marshal(&commandmodel.EdgeExecutionEvidence{
			Requested:          commandmodel.TextScalar("START"),
			Effective:          &commandmodel.ScalarValue{Text: &pulseText},
			Applied:            &commandmodel.ScalarValue{Text: &pulseText},
			WinnerControllerID: "cloud-command-intent",
			Cycle:              1,
		})

		tx := &fakePostgresExecutor{
			queryHandlers: []func(string) pgx.Rows{
				func(string) pgx.Rows {
					return &fakePgxRows{
						rows: [][]any{
							{
								"019a9000-1111-7000-8000-000000000002",
								"019a9000-2222-7000-8000-000000000002",
								commandSiteA,
								commandDeviceA,
								commandPointA,
								"4bf92f3577b34da6a3ce929d0e0e4736",
								string(commandmodel.CapabilityStart),
								"capability:start:v1",
								levelParams,
								"run_state",
								uint64(4),
								"hash-level",
								"ack-ev-level",
								uint64(1),
								uint64(3),
								uint64(2),
								now.Add(-2 * time.Minute),
								now.Add(-time.Second),
								levelEdge,
							},
							{
								"019a9000-1111-7000-8000-000000000003",
								"019a9000-2222-7000-8000-000000000003",
								commandSiteA,
								commandDeviceA,
								commandPointA,
								"",
								string(commandmodel.CapabilityStart),
								"capability:start:v1",
								levelParams,
								"run_state",
								uint64(5),
								"hash-pulse",
								"ack-ev-pulse",
								uint64(2),
								uint64(3),
								uint64(2),
								now.Add(-2 * time.Minute),
								now.Add(-time.Second),
								pulseEdge,
							},
						},
					}
				},
			},
		}

		collected, err := store.reconcileExpiredAcknowledgedAttempts(t.Context(), tx, commandTenantA, now)
		if err != nil {
			t.Fatalf("reconcileExpiredAcknowledgedAttempts: %v", err)
		}
		if len(collected) != 2 {
			t.Fatalf("collected=%d want 2", len(collected))
		}
		if err := store.commitAndNotifyFailures(t.Context(), tx, collected); err != nil {
			t.Fatalf("commitAndNotifyFailures: %v", err)
		}
		seen := map[string]ControlFailureEvent{}
		for i := 0; i < 2; i++ {
			select {
			case evt := <-events:
				seen[evt.CommandID] = evt
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for expired verification event %d", i+1)
			}
		}
		levelEvt := seen["019a9000-2222-7000-8000-000000000002"]
		if levelEvt.Kind != string(commandmodel.IntentOutcomeUnknown) || levelEvt.ReasonCode != "REPORTED_STATE_VERIFICATION_DEADLINE_EXPIRED" || levelEvt.PointID != commandPointA {
			t.Fatalf("unexpected level verification expiry event: %+v", levelEvt)
		}
		pulseEvt := seen["019a9000-2222-7000-8000-000000000003"]
		if pulseEvt.Kind != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || pulseEvt.ReasonCode != commandmodel.ReasonPulseDeliveredPowerUnconfirmed || pulseEvt.PointID != commandPointA {
			t.Fatalf("unexpected pulse verification expiry event: %+v", pulseEvt)
		}
	})
}

type storeBackedRuntimeStub struct {
	*PostgresStore
}

func (s *storeBackedRuntimeStub) ResolveDispatch(context.Context, commandmodel.DispatchEnvelope, commandmodel.ConnectorResult) error {
	return nil
}

func (s *storeBackedRuntimeStub) ResolveVerification(context.Context, commandmodel.VerificationEnvelope, commandmodel.VerificationResult) error {
	return nil
}

func TestRuntimeHTTPHandlerAvoidsDoubleNotificationWhenStoreNotifies(t *testing.T) {
	pgStore := NewPostgresStore(nil, time.Now, nil)
	if pgStore.hasControlFailureNotifier() {
		t.Fatal("expected hasControlFailureNotifier=false before SetControlFailureNotifier")
	}
	pgStore.SetControlFailureNotifier(func(context.Context, ControlFailureEvent) {})
	if !pgStore.hasControlFailureNotifier() {
		t.Fatal("expected hasControlFailureNotifier=true after SetControlFailureNotifier")
	}

	httpCalls := make(chan ControlFailureEvent, 2)
	handler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store:            &storeBackedRuntimeStub{PostgresStore: pgStore},
		DispatcherSPIFFE: "spiffe://hvac.local/command-dispatcher",
		VerifierSPIFFE:   "spiffe://hvac.local/command-verifier",
		ControlFailureNotifier: func(_ context.Context, event ControlFailureEvent) {
			httpCalls <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(runtimeDispatchResolveRequest{
		Envelope: commandmodel.DispatchEnvelope{CommandID: "cmd-no-dup", TenantID: "t1", SiteID: "s1", DeviceID: "d1", PointID: "p1", Capability: commandmodel.CapabilityStart},
		Result:   commandmodel.ConnectorResult{Phase: commandmodel.ConnectorRequestCommitted, EvidenceID: "ev-1"},
	})
	uri, _ := url.Parse("spiffe://hvac.local/command-dispatcher")
	req := httptest.NewRequest(http.MethodPost, InternalDispatchResolvePath, strings.NewReader(string(body)))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{uri}}}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case dup := <-httpCalls:
		t.Fatalf("expected HTTP layer not to double-notify when PostgresStore handles notifications: %+v", dup)
	case <-time.After(100 * time.Millisecond):
	}
}
