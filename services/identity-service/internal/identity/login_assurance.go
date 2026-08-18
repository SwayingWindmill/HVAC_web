package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func readAuthorizationRequest(ctx context.Context, tx pgx.Tx, challengeHash []byte) (AuthorizationRequest, int, error) {
	var request AuthorizationRequest
	var attemptCount int
	var userID *string
	var firstFactorAt *time.Time
	err := tx.QueryRow(ctx, `
SELECT client_id, redirect_uri, state, nonce, code_challenge, scope, required_acr,
       user_id::text, first_factor_at, expires_at, attempt_count
FROM identity.authorization_requests
WHERE challenge_hash = $1
FOR UPDATE
`, challengeHash).Scan(
		&request.ClientID, &request.RedirectURI, &request.State, &request.Nonce,
		&request.CodeChallenge, &request.Scope, &request.RequiredACR,
		&userID, &firstFactorAt, &request.ExpiresAt, &attemptCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationRequest{}, 0, ErrAuthorizationRequestExpired
	}
	if err != nil {
		return AuthorizationRequest{}, 0, fmt.Errorf("read authorization request: %w", err)
	}
	if userID != nil {
		request.UserID = *userID
	}
	request.FirstFactorAt = firstFactorAt
	return request, attemptCount, nil
}

func issueAuthorizationCode(ctx context.Context, tx pgx.Tx, challengeHash []byte, request AuthorizationRequest, userID, acr string, amr []string, authTime time.Time) (LoginGrant, error) {
	code := randomToken(32)
	codeHash := sha256.Sum256([]byte(code))
	if _, err := tx.Exec(ctx, `
INSERT INTO identity.authorization_codes (
  code_hash, user_id, client_id, redirect_uri, nonce, code_challenge, scope,
  expires_at, created_at, acr, amr, auth_time
) VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
`, codeHash[:], userID, request.ClientID, request.RedirectURI, request.Nonce,
		request.CodeChallenge, request.Scope, authTime.Add(2*time.Minute), authTime, acr, amr, authTime); err != nil {
		return LoginGrant{}, fmt.Errorf("persist authorization code: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.authorization_requests WHERE challenge_hash = $1`, challengeHash); err != nil {
		return LoginGrant{}, fmt.Errorf("consume authorization request: %w", err)
	}
	return LoginGrant{Code: code, RedirectURI: request.RedirectURI, State: request.State}, nil
}

func recordSuccessfulLogin(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
UPDATE identity.users
SET failed_attempts = 0, locked_until = NULL, last_login_at = $2, revision = revision + 1, updated_at = $2
WHERE id = $1::uuid
`, userID, now); err != nil {
		return fmt.Errorf("record successful login: %w", err)
	}
	return nil
}
