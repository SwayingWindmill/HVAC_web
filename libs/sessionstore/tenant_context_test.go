package sessionstore

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestSwitchTenantContextRotatesContextAndAuditRevision(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	created, err := store.CreateSession(context.Background(), Session{
		ID: "session-1",
		Principal: identitycontext.UserPrincipal{
			Subject: "user-1",
			Issuer:  "https://identity.example",
		},
		TenantID:            "tenant-a",
		CSRFTokenCiphertext: []byte("old-csrf"),
		ExpiresAt:           now.Add(time.Hour),
	}, MutationContext{
		Action:            "SESSION_CREATED",
		PolicyRevision:    "policy:1",
		CorrelationID:     "corr-1",
		TraceID:           "0123456789abcdef0123456789abcdef",
		Traceparent:       "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}

	switched, err := store.SwitchTenantContext(context.Background(), created.ID, "tenant-b", []byte("new-csrf"), MutationContext{
		Action:            "SESSION_TENANT_CONTEXT_SWITCHED",
		PolicyRevision:    "policy:2",
		CorrelationID:     "corr-2",
		TraceID:           "0123456789abcdef0123456789abcdef",
		Traceparent:       "00-0123456789abcdef0123456789abcdef-fedcba9876543210-01",
		ExecutingService:  "platform-gateway",
		ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway",
		OccurredAt:        now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if switched.TenantID != "tenant-b" {
		t.Fatalf("tenant = %q, want tenant-b", switched.TenantID)
	}
	if bytes.Equal(switched.CSRFTokenCiphertext, created.CSRFTokenCiphertext) {
		t.Fatal("CSRF ciphertext was not rotated")
	}
	if switched.AggregateVersion != created.AggregateVersion+1 {
		t.Fatalf("aggregate version = %d, want %d", switched.AggregateVersion, created.AggregateVersion+1)
	}
	if switched.LastAuditMessageID == created.LastAuditMessageID {
		t.Fatal("tenant switch did not create a new audit message")
	}
	event, ok := store.Event(switched.LastAuditMessageID)
	if !ok {
		t.Fatal("tenant switch audit event is missing")
	}
	if event.TenantID != "tenant-b" || event.Action != "SESSION_TENANT_CONTEXT_SWITCHED" {
		t.Fatalf("unexpected tenant switch audit event: tenant=%q action=%q", event.TenantID, event.Action)
	}
}
