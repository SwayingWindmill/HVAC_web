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
	var firstFactor *string
	err := tx.QueryRow(ctx, `
SELECT client_id, redirect_uri, state, nonce, code_challenge, scope, required_acr,
       user_id::text, first_factor_at, first_factor, expires_at, attempt_count
FROM identity.authorization_requests
WHERE challenge_hash = $1
FOR UPDATE
`, challengeHash).Scan(
		&request.ClientID, &request.RedirectURI, &request.State, &request.Nonce,
		&request.CodeChallenge, &request.Scope, &request.RequiredACR,
		&userID, &firstFactorAt, &firstFactor, &request.ExpiresAt, &attemptCount,
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
	if firstFactor != nil {
		request.FirstFactor = *firstFactor
	}
	return request, attemptCount, nil
}

// finishFirstFactor carries on a sign-in whose first factor ('pwd' or 'wechat') the account just
// passed: an account with one-time codes waits for its code, any other gets its code for /token.
func finishFirstFactor(ctx context.Context, tx pgx.Tx, challengeHash []byte, request AuthorizationRequest, userID, username, firstFactor, reason string, now time.Time) (LoginResult, error) {
	var mfaStatus string
	mfaErr := tx.QueryRow(ctx, `SELECT status FROM identity.user_mfa WHERE user_id = $1::uuid`, userID).Scan(&mfaStatus)
	if mfaErr != nil && !errors.Is(mfaErr, pgx.ErrNoRows) {
		return LoginResult{}, fmt.Errorf("read identity MFA status: %w", mfaErr)
	}
	hasActiveMFA := mfaErr == nil && mfaStatus == "ACTIVE"
	if request.RequiredACR == AssuranceMFA && !hasActiveMFA {
		if err := insertSecurityAudit(ctx, tx, "STEP_UP_FAILED", userID, username, "FAILED", "MFA_NOT_ENROLLED", now, map[string]any{}); err != nil {
			return LoginResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginResult{}, fmt.Errorf("commit rejected step-up: %w", err)
		}
		return LoginResult{}, ErrMFAEnrollmentRequired
	}
	if hasActiveMFA {
		if _, err := tx.Exec(ctx, `
UPDATE identity.authorization_requests
SET user_id = $2::uuid, first_factor_at = $3, first_factor = $4
WHERE challenge_hash = $1
`, challengeHash, userID, now, firstFactor); err != nil {
			return LoginResult{}, fmt.Errorf("record identity first factor: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginResult{}, fmt.Errorf("commit identity first factor: %w", err)
		}
		return LoginResult{MFARequired: true}, ErrMFARequired
	}

	grant, err := issueAuthorizationCode(ctx, tx, challengeHash, request, userID, AssuranceBasic, []string{firstFactor}, now)
	if err != nil {
		return LoginResult{}, err
	}
	if err := recordSuccessfulLogin(ctx, tx, userID, now); err != nil {
		return LoginResult{}, err
	}
	if err := insertSecurityAudit(ctx, tx, "LOGIN_SUCCEEDED", userID, username, "SUCCEEDED", reason, now, map[string]any{"acr": AssuranceBasic}); err != nil {
		return LoginResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginResult{}, fmt.Errorf("commit successful login: %w", err)
	}
	return LoginResult{Grant: grant}, nil
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
