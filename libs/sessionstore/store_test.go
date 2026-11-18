package sessionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

func TestMemoryStoreSwitchTenantUpdatesSessionAndAppendsAuditEvent(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	store := sessionstore.NewMemoryStore()
	mutation := sessionstore.MutationContext{
		Action:            "SESSION_CREATED",
		Result:            "SUCCEEDED",
		PolicyRevision:    "policy-v1",
		CorrelationID:     "req-1",
		TraceID:           "0123456789abcdef0123456789abcdef",
		Traceparent:       "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now,
	}
	created, err := store.CreateSession(context.Background(), sessionstore.Session{
		ID:                 "session-switch-01",
		Client:             sessionstore.ClientBrowser,
		Principal:          identitycontext.UserPrincipal{Subject: "operator-1", Issuer: "https://issuer.example.test", DisplayName: "Operator", Email: "operator@example.test"},
		TenantID:           "018f3d00-0000-7000-8000-000000000001",
		AuthenticationACR:  "urn:hvac:loa:1",
		AuthenticationTime: now,
		ExpiresAt:          now.Add(time.Hour),
	}, mutation)
	if err != nil {
		t.Fatal(err)
	}

	switchMutation := mutation
	switchMutation.Action = "SESSION_TENANT_SWITCHED"
	switchMutation.OccurredAt = now.Add(time.Minute)
	switched, err := store.SwitchTenant(context.Background(), created.ID, "018f3d00-0000-7000-8000-000000000002", switchMutation)
	if err != nil {
		t.Fatal(err)
	}
	if switched.TenantID != "018f3d00-0000-7000-8000-000000000002" {
		t.Fatalf("switched TenantID = %q", switched.TenantID)
	}
	if switched.AggregateVersion != 2 || switched.LastAuditMessageID == created.LastAuditMessageID {
		t.Fatalf("unexpected aggregate metadata after switch: %#v", switched)
	}
	if !switched.UpdatedAt.Equal(switchMutation.OccurredAt) {
		t.Fatalf("UpdatedAt = %v, want %v", switched.UpdatedAt, switchMutation.OccurredAt)
	}

	event, ok := store.Event(switched.LastAuditMessageID)
	if !ok {
		t.Fatalf("missing audit event %q", switched.LastAuditMessageID)
	}
	if event.Action != "SESSION_TENANT_SWITCHED" || event.TenantID != switched.TenantID || event.CausationID != created.LastAuditMessageID {
		t.Fatalf("unexpected audit event: %#v", event)
	}

	if _, err := store.RevokeSession(context.Background(), created.ID, sessionstore.MutationContext{
		Action: "SESSION_REVOKED", Result: "SUCCEEDED", PolicyRevision: "policy-v1",
		CorrelationID: "req-2", TraceID: mutation.TraceID, Traceparent: mutation.Traceparent,
		ExecutingService: mutation.ExecutingService, ExecutingSPIFFEID: mutation.ExecutingSPIFFEID,
		OccurredAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SwitchTenant(context.Background(), created.ID, "018f3d00-0000-7000-8000-000000000001", switchMutation); !errors.Is(err, sessionstore.ErrSessionRevoked) {
		t.Fatalf("switching revoked session returned %v, want ErrSessionRevoked", err)
	}
}
