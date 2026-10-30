package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// insertSecurityAudit records an event about an existing account. username must be the
// account's stored username, never unverified sign-in input: for a name that matches no
// account use insertSecurityAuditForUnknownSubject instead.
func insertSecurityAudit(ctx context.Context, tx pgx.Tx, eventType, userID, username, outcome, reasonCode string, now time.Time, details map[string]any) error {
	return insertSecurityAuditWithRef(ctx, tx, eventType, userID, securitySubjectRef(username), outcome, reasonCode, now, details)
}

// insertSecurityAuditForUnknownSubject records an event for sign-in input that matched no
// account. The typed name is only ever stored as a keyed digest (see unknownSubjectRef).
func insertSecurityAuditForUnknownSubject(ctx context.Context, tx pgx.Tx, eventType, typedUsername, outcome, reasonCode string, now time.Time, details map[string]any) error {
	return insertSecurityAuditWithRef(ctx, tx, eventType, "", unknownSubjectRef(typedUsername), outcome, reasonCode, now, details)
}

func insertSecurityAuditWithRef(ctx context.Context, tx pgx.Tx, eventType, userID, subjectRef, outcome, reasonCode string, now time.Time, details map[string]any) error {
	eventID, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"schemaVersion": 1,
		"eventId":       eventID,
		"eventType":     eventType,
		"subjectRef":    subjectRef,
		"outcome":       outcome,
		"reasonCode":    reasonCode,
		"occurredAt":    now.UTC().Format(time.RFC3339Nano),
		"details":       details,
	}
	if userID != "" {
		payload["userId"] = userID
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode identity security audit: %w", err)
	}
	detailsBytes, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode identity security audit details: %w", err)
	}
	var userValue any
	if userID != "" {
		userValue = userID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO identity.security_audit_intents
  (event_id, event_type, user_id, subject_ref, outcome, reason_code, occurred_at, details)
VALUES
  ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8::jsonb)
`, eventID, eventType, userValue, subjectRef, outcome, reasonCode, now, detailsBytes); err != nil {
		return fmt.Errorf("persist identity security audit intent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO identity.security_outbox
  (event_id, payload, status, attempt_count, available_at, created_at)
VALUES
  ($1::uuid, $2::jsonb, 'PENDING', 0, $3, $3)
`, eventID, payloadBytes, now); err != nil {
		return fmt.Errorf("persist identity security outbox: %w", err)
	}
	return nil
}

// securitySubjectRef is the stable audit reference for an existing account: the SHA-256 of
// its normalized stored username. Usernames are not secret and existing audit rows keep
// matching, so this stays an unkeyed digest.
func securitySubjectRef(username string) string {
	digest := sha256.Sum256([]byte(normalizeUsername(username)))
	return hex.EncodeToString(digest[:])
}

// unknownSubjectKey keys unknownSubjectRef. It is random per process and never stored, so
// an audit row for an unknown name cannot be reversed by guessing offline. People
// sometimes type a password into the username box; an unkeyed SHA-256 of that input
// would let anyone holding the audit table try candidate passwords against it.
var unknownSubjectKey = mustRandomKey()

func mustRandomKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("identity: generate audit subject key: %v", err))
	}
	return key
}

// unknownSubjectRef is the audit reference for sign-in input that matched no account: an
// HMAC-SHA256 of the normalized input under unknownSubjectKey. Repeated attempts with the
// same name share a reference while this process runs; after a restart the reference for
// the same name changes. It has the same 64-hex shape as securitySubjectRef, and these rows
// have no user_id.
func unknownSubjectRef(typedUsername string) string {
	mac := hmac.New(sha256.New, unknownSubjectKey)
	mac.Write([]byte(normalizeUsername(typedUsername)))
	return hex.EncodeToString(mac.Sum(nil))
}

func resolveIdentityTime(now time.Time) time.Time {
	now = now.UTC()
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now
}
