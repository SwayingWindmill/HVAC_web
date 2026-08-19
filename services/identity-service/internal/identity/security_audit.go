package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func insertSecurityAudit(ctx context.Context, tx pgx.Tx, eventType, userID, username, outcome, reasonCode string, now time.Time, details map[string]any) error {
	eventID, err := newUUIDv7(now)
	if err != nil {
		return err
	}
	subjectRef := securitySubjectRef(username)
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

func securitySubjectRef(username string) string {
	digest := sha256.Sum256([]byte(normalizeUsername(username)))
	return hex.EncodeToString(digest[:])
}

func resolveIdentityTime(now time.Time) time.Time {
	now = now.UTC()
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now
}
