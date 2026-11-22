package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresEdgeAlarmEventRelayFIFOOutboxAndRetentionGuard(t *testing.T) {
	runtimeURL, adminURL := postgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	store, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	msg1 := "0195a000-0000-7000-8000-000000000001"
	msg2 := "0195a000-0000-7000-8000-000000000002"
	msgNonEdge := "0195a000-0000-7000-8000-000000000003"
	if _, err := admin.Exec(ctx, `DELETE FROM telemetry_runtime.mqtt_runtime_events WHERE message_id = ANY($1::uuid[])`, []string{msg1, msg2, msgNonEdge}); err != nil {
		t.Fatal(err)
	}

	receivedAt := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: tenantA, SiteID: siteA, GatewayID: "gw-edge-950", MessageID: msg2, Sequence: 2,
		EventType: "EDGE_RULE_ALARM_CLEARED", SourceType: "EDGE_RULE", SourceID: "0195a000-1111-7000-8000-000000000001",
		EventTime: receivedAt.Add(-1 * time.Minute), Severity: "INFO", Data: json.RawMessage(`{"eventId":"edge-rule:0195a000-1111-7000-8000-000000000001:2"}`),
		ReceivedAt: receivedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: tenantA, SiteID: siteA, GatewayID: "gw-edge-950", MessageID: msg1, Sequence: 1,
		EventType: "EDGE_RULE_ALARM_RAISED", SourceType: "EDGE_RULE", SourceID: "0195a000-1111-7000-8000-000000000001",
		EventTime: receivedAt.Add(-5 * time.Minute), Severity: "CRITICAL", Data: json.RawMessage(`{"eventId":"edge-rule:0195a000-1111-7000-8000-000000000001:1"}`),
		ReceivedAt: receivedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptRuntimeEvent(ctx, RuntimeEventEvidence{
		TenantID: tenantA, SiteID: siteA, GatewayID: "gw-edge-950", MessageID: msgNonEdge, Sequence: 3,
		EventType: "GATEWAY_HEARTBEAT", SourceType: "GATEWAY", SourceID: "gw-edge-950",
		EventTime: receivedAt, Severity: "INFO", Data: json.RawMessage(`{"ok":true}`),
		ReceivedAt: receivedAt,
	}); err != nil {
		t.Fatal(err)
	}

	var nonEdgeState *string
	if err := admin.QueryRow(ctx, `SELECT alarm_delivery_state FROM telemetry_runtime.mqtt_runtime_events WHERE message_id = $1::uuid`, msgNonEdge).Scan(&nonEdgeState); err != nil {
		t.Fatal(err)
	}
	if nonEdgeState != nil {
		t.Fatalf("non-edge event initialized alarm_delivery_state=%q", *nonEdgeState)
	}

	if _, err := store.PruneRuntimeHistory(ctx, receivedAt.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var remainingEdge int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.mqtt_runtime_events WHERE message_id = ANY($1::uuid[])`, []string{msg1, msg2}).Scan(&remainingEdge); err != nil {
		t.Fatal(err)
	}
	if remainingEdge != 2 {
		t.Fatalf("retention deleted PENDING edge alarm events: remaining=%d", remainingEdge)
	}

	var deliveredOrder []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MessageID string `json:"messageId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		deliveredOrder = append(deliveredOrder, req.MessageID)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr, err := NewHTTPEdgeAlarmEventTransport(HTTPEdgeAlarmEventTransportConfig{
		Endpoint: srv.URL + InternalEdgeAlarmEventsPath, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewEdgeAlarmEventRelay(EdgeAlarmEventRelayConfig{
		Repository: store, Transport: tr, WorkerID: "edge-pg-test", LeaseDuration: 30 * time.Second, RetryDelay: time.Second,
		Now: func() time.Time { return receivedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := relay.RelayOnce(ctx, 10)
	if err != nil || n != 2 {
		t.Fatalf("RelayOnce() = (%d, %v), want (2, nil)", n, err)
	}
	if len(deliveredOrder) != 2 || deliveredOrder[0] != msg1 || deliveredOrder[1] != msg2 {
		t.Fatalf("edge events did not relay chronologically: %v (want [%s %s])", deliveredOrder, msg1, msg2)
	}

	if _, err := store.PruneRuntimeHistory(ctx, receivedAt.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM telemetry_runtime.mqtt_runtime_events WHERE message_id = ANY($1::uuid[])`, []string{msg1, msg2}).Scan(&remainingEdge); err != nil {
		t.Fatal(err)
	}
	if remainingEdge != 0 {
		t.Fatalf("expected published edge events to be prunable after retention window, remaining=%d", remainingEdge)
	}
}
