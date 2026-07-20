package sessionevent_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/sessionevent"
)

func TestSessionAuditEventRoundTripIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	event := fixtureEvent(now)
	first, err := event.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	second, err := event.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("protobuf encoding is not deterministic")
	}
	decoded, err := sessionevent.UnmarshalBinary(first)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != event {
		t.Fatalf("round trip mismatch: %#v", decoded)
	}
}

func TestSessionAuditEventRejectsCredentialsAndGrants(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, seeded := range []string{"Bearer seeded-token", "access_token=seeded", "refresh_token=seeded", "id_token=seeded", "authorization_code=seeded", "X-Delegation-Grant=seeded", "Cookie=seeded"} {
		t.Run(seeded, func(t *testing.T) {
			event := fixtureEvent(now)
			event.CorrelationID = seeded
			if _, err := event.MarshalBinary(); err == nil {
				t.Fatal("sensitive marker was accepted")
			}
		})
	}
}

func TestAuditAggregateIDIsDeterministicAndIrreversibleInPayload(t *testing.T) {
	first := sessionevent.AuditAggregateID("opaque-session-cookie-value")
	second := sessionevent.AuditAggregateID("opaque-session-cookie-value")
	other := sessionevent.AuditAggregateID("different-session-cookie-value")
	if first != second || first == other {
		t.Fatalf("unexpected aggregate IDs: first=%q second=%q other=%q", first, second, other)
	}
	if len(first) != 64 || bytes.Contains([]byte(first), []byte("opaque-session-cookie-value")) {
		t.Fatalf("aggregate ID is not a one-way SHA-256 identifier: %q", first)
	}
}

func TestSessionAuditEventRejectsRawOrMismatchedAggregateIdentifier(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	raw := fixtureEvent(now)
	raw.AggregateID = "opaque-session-cookie-value"
	raw.PartitionKey = sessionevent.AggregateType + ":" + raw.AggregateID
	if _, err := raw.MarshalBinary(); err == nil {
		t.Fatal("raw Session identifier was accepted as an Audit aggregate")
	}

	mismatched := fixtureEvent(now)
	mismatched.PartitionKey = sessionevent.AggregateType + ":" + sessionevent.AuditAggregateID("other-session")
	if _, err := mismatched.MarshalBinary(); err == nil {
		t.Fatal("mismatched Audit partition key was accepted")
	}
}

func fixtureEvent(now time.Time) sessionevent.SessionAuditEventV1 {
	auditAggregateID := sessionevent.AuditAggregateID("session-01")
	return sessionevent.SessionAuditEventV1{
		MessageID:         "message-01",
		SchemaVersion:     sessionevent.SchemaVersion,
		MessageType:       sessionevent.MessageType,
		Producer:          sessionevent.Producer,
		OrganizationID:    "org-01",
		PartitionKey:      "bff-session:" + auditAggregateID,
		AggregateType:     sessionevent.AggregateType,
		AggregateID:       auditAggregateID,
		AggregateVersion:  1,
		OccurredAtUnixMS:  now.UnixMilli(),
		PublishedAtUnixMS: now.UnixMilli(),
		CorrelationID:     "request-01",
		TraceID:           "0123456789abcdef0123456789abcdef",
		Actor: sessionevent.ActorChainV1{
			InitiatingSubject:    "fixture-user",
			InitiatingIssuer:     "https://issuer.example.test",
			ExecutingService:     "platform-gateway",
			ExecutingSPIFFEID:    "spiffe://hvac.local/platform-gateway",
			ActingOrganizationID: "org-01",
		},
		Action:         "SESSION_CREATED",
		Result:         "SUCCEEDED",
		PolicyRevision: "policy-v1",
		PayloadSHA256:  sessionevent.SafePayloadHash("session-01", "ACTIVE", now.UnixMilli()),
		SessionState:   "ACTIVE",
	}
}
