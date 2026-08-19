package ownershipregistry_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
)

func TestPostgresRouteAuditIsAppendOnlyAndCredentialFree(t *testing.T) {
	gatewayDSN := requiredOwnershipEnv(t, "S0_GATEWAY_DATABASE_URL")
	admin, err := pgxpool.New(context.Background(), requiredOwnershipEnv(t, "S0_ADMIN_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(context.Background(), `TRUNCATE gateway.route_audit_records`); err != nil {
		t.Fatal(err)
	}

	sink, err := ownershipregistry.OpenPostgresAudit(context.Background(), gatewayDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	record := ownershipregistry.AuditRecord{
		MessageID:         "route-audit-message-01",
		EventType:         "ROUTE_DECIDED",
		RouteKey:          "GET /api/v1/sites",
		Method:            "GET",
		PathTemplate:      "/api/v1/sites",
		SelectedOwner:     ownershipregistry.OwnerCore,
		RegistryRevision:  24,
		RouteRevision:     6,
		CompatibilityMode: "native",
		TenantID:          "tenant-01",
		InitiatingSubject: "fixture-user",
		InitiatingIssuer:  "https://issuer.example.test",
		ExecutingService:  ownershipregistry.OwnerGateway,
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		PolicyRevision:    "policy-v1",
		CorrelationID:     "request-01",
		TraceID:           "0123456789abcdef0123456789abcdef",
		OutcomeCode:       "ROUTED",
		OccurredAt:        time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC),
	}
	if err := sink.Record(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	var count int
	var serialized string
	if err := admin.QueryRow(context.Background(), `SELECT count(*), string_agg(row_to_json(route_audit_records)::text, '') FROM gateway.route_audit_records`).Scan(&count, &serialized); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("route audit count = %d", count)
	}
	for _, forbidden := range []string{"opaque-session-cookie-value", "access_token", "refresh_token", "authorization_code", "delegation"} {
		if strings.Contains(strings.ToLower(serialized), strings.ToLower(forbidden)) {
			t.Fatalf("route audit leaked %q: %s", forbidden, serialized)
		}
	}
	if _, err := admin.Exec(context.Background(), `UPDATE gateway.route_audit_records SET selected_owner='forged' WHERE message_id='route-audit-message-01'`); err == nil {
		t.Fatal("append-only route audit accepted update")
	}
	if _, err := admin.Exec(context.Background(), `DELETE FROM gateway.route_audit_records WHERE message_id='route-audit-message-01'`); err == nil {
		t.Fatal("append-only route audit accepted delete")
	}
}

func TestRouteAuditRuntimeCannotWriteAuditLedger(t *testing.T) {
	gateway, err := pgxpool.New(context.Background(), requiredOwnershipEnv(t, "S0_GATEWAY_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	_, err = gateway.Exec(context.Background(), `INSERT INTO audit_ledger.organization_heads (organization_id,last_record_hash,updated_at) VALUES ('org-forged',$1,clock_timestamp())`, strings.Repeat("0", 64))
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("gateway runtime cross-wrote Audit Ledger: %v", err)
	}
}

func requiredOwnershipEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not configured", name)
	}
	return value
}
