package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/schemagate"
)

var (
	ErrAuthorizationRequestExpired = errors.New("authorization request is missing or expired")
	ErrInvalidCredentials          = errors.New("invalid credentials")
	ErrAuthorizationCodeInvalid    = errors.New("authorization code is invalid")
	ErrMFARequired                 = errors.New("multi-factor authentication is required")
	ErrMFAEnrollmentRequired       = errors.New("multi-factor authentication enrollment is required")
	ErrMFAInvalid                  = errors.New("multi-factor authentication code is invalid")
)

type Store struct {
	pool      *pgxpool.Pool
	dummyHash string
}

type AuthorizationRequest struct {
	ChallengeHash []byte
	ClientID      string
	RedirectURI   string
	State         string
	Nonce         string
	CodeChallenge string
	Scope         string
	RequiredACR   string
	UserID        string
	FirstFactorAt *time.Time
	ExpiresAt     time.Time
}

type LoginGrant struct {
	Code        string
	RedirectURI string
	State       string
}

type LoginResult struct {
	Grant       LoginGrant
	MFARequired bool
}

type TokenSubject struct {
	UserID      string
	DisplayName string
	Email       string
	Nonce       string
	ACR         string
	AMR         []string
	AuthTime    time.Time
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Email       string
	Password    string
	Now         time.Time
}

type CreatedUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("open identity database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping identity database: %w", err)
	}
	return &Store{pool: pool, dummyHash: dummyPasswordHash()}, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *Store) VerifyProductSchema(ctx context.Context, expectedVersion string) error {
	return schemagate.VerifyProductSchema(ctx, store.pool, expectedVersion)
}

func (store *Store) CreateUser(ctx context.Context, input CreateUserInput) (CreatedUser, error) {
	username := strings.TrimSpace(input.Username)
	displayName := strings.TrimSpace(input.DisplayName)
	email := strings.TrimSpace(input.Email)
	if username == "" || len(username) > 128 {
		return CreatedUser{}, errors.New("identity username is required and must not exceed 128 bytes")
	}
	if displayName == "" || len(displayName) > 256 {
		return CreatedUser{}, errors.New("identity display name is required and must not exceed 256 bytes")
	}
	if email == "" || len(email) > 320 || !strings.Contains(email, "@") {
		return CreatedUser{}, errors.New("identity email is invalid")
	}
	passwordPHC, err := HashPassword(input.Password)
	if err != nil {
		return CreatedUser{}, err
	}
	now := resolveIdentityTime(input.Now)
	userID, err := newUUIDv7(now)
	if err != nil {
		return CreatedUser{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CreatedUser{}, fmt.Errorf("begin identity user creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
INSERT INTO identity.users (
  id, username, username_normalized, display_name, email, password_phc, status,
  failed_attempts, locked_until, revision, created_at, updated_at
) VALUES ($1::uuid, $2, $3, $4, $5, $6, 'ACTIVE', 0, NULL, 1, $7, $7)
`, userID, username, normalizeUsername(username), displayName, email, passwordPHC, now)
	if err != nil {
		return CreatedUser{}, fmt.Errorf("create identity user: %w", err)
	}
	if err := insertSecurityAudit(ctx, tx, "USER_CREATED", userID, username, "SUCCEEDED", "USER_CREATED", now, map[string]any{}); err != nil {
		return CreatedUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatedUser{}, fmt.Errorf("commit identity user creation: %w", err)
	}
	return CreatedUser{ID: userID, Username: username, DisplayName: displayName, Email: email}, nil
}

func (store *Store) ResetPassword(ctx context.Context, username, password string, now time.Time) error {
	normalized := normalizeUsername(username)
	if normalized == "" {
		return errors.New("identity username is required")
	}
	passwordPHC, err := HashPassword(password)
	if err != nil {
		return err
	}
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin identity password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, storedUsername string
	if err := tx.QueryRow(ctx, `SELECT id::text, username FROM identity.users WHERE username_normalized = $1 AND status = 'ACTIVE' FOR UPDATE`, normalized).Scan(&userID, &storedUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("active identity user not found")
		}
		return fmt.Errorf("read identity user for password reset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE identity.users
SET password_phc = $1,
    failed_attempts = 0,
    locked_until = NULL,
    revision = revision + 1,
    updated_at = $2
WHERE id = $3::uuid
`, passwordPHC, now, userID); err != nil {
		return fmt.Errorf("reset identity password: %w", err)
	}
	if err := insertSecurityAudit(ctx, tx, "PASSWORD_RESET", userID, storedUsername, "SUCCEEDED", "PASSWORD_RESET", now, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity password reset: %w", err)
	}
	return nil
}

func (store *Store) CreateAuthorizationRequest(ctx context.Context, request AuthorizationRequest) error {
	requiredACR := request.RequiredACR
	if requiredACR == "" {
		requiredACR = AssuranceBasic
	}
	_, err := store.pool.Exec(ctx, `
INSERT INTO identity.authorization_requests (
  challenge_hash, client_id, redirect_uri, state, nonce, code_challenge, scope,
  required_acr, attempt_count, expires_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, now())
`, request.ChallengeHash, request.ClientID, request.RedirectURI, request.State, request.Nonce, request.CodeChallenge, request.Scope, requiredACR, request.ExpiresAt)
	if err != nil {
		return fmt.Errorf("persist authorization request: %w", err)
	}
	return nil
}

func (store *Store) CompleteLogin(ctx context.Context, challengeHash []byte, username, credential string, now time.Time) (LoginResult, error) {
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LoginResult{}, fmt.Errorf("begin login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	request, attemptCount, err := readAuthorizationRequest(ctx, tx, challengeHash)
	if err != nil {
		return LoginResult{}, err
	}
	if !request.ExpiresAt.After(now) || attemptCount >= 10 || request.UserID != "" {
		return LoginResult{}, ErrAuthorizationRequestExpired
	}

	normalizedUsername := normalizeUsername(username)
	var userID, storedUsername, passwordPHC, status string
	var lockedUntil *time.Time
	var failedAttempts int
	err = tx.QueryRow(ctx, `
SELECT id::text, username, password_phc, status, failed_attempts, locked_until
FROM identity.users
WHERE username_normalized = $1
FOR UPDATE
`, normalizedUsername).Scan(&userID, &storedUsername, &passwordPHC, &status, &failedAttempts, &lockedUntil)
	userFound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, fmt.Errorf("read identity user: %w", err)
	}
	if !userFound {
		passwordPHC = store.dummyHash
		storedUsername = username
	}
	credentialOK := VerifyPassword(credential, passwordPHC)
	locked := lockedUntil != nil && lockedUntil.After(now)
	if !userFound || !credentialOK || status != "ACTIVE" || locked {
		if _, err := tx.Exec(ctx, `UPDATE identity.authorization_requests SET attempt_count = attempt_count + 1 WHERE challenge_hash = $1`, challengeHash); err != nil {
			return LoginResult{}, fmt.Errorf("record failed login attempt: %w", err)
		}
		lockedNow := false
		if userFound && status == "ACTIVE" && !locked && !credentialOK {
			failedAttempts++
			var nextLockedUntil *time.Time
			if failedAttempts >= 5 {
				value := now.Add(15 * time.Minute)
				nextLockedUntil = &value
				lockedNow = true
			}
			if _, err := tx.Exec(ctx, `
UPDATE identity.users
SET failed_attempts = $2, locked_until = $3, revision = revision + 1, updated_at = $4
WHERE id = $1::uuid
`, userID, failedAttempts, nextLockedUntil, now); err != nil {
				return LoginResult{}, fmt.Errorf("record identity login failure: %w", err)
			}
		}
		reason := "INVALID_CREDENTIALS"
		if locked {
			reason = "ACCOUNT_LOCKED"
		}
		auditUserID := ""
		if userFound {
			auditUserID = userID
		}
		if err := insertSecurityAudit(ctx, tx, "LOGIN_FAILED", auditUserID, storedUsername, "FAILED", reason, now, map[string]any{}); err != nil {
			return LoginResult{}, err
		}
		if lockedNow {
			if err := insertSecurityAudit(ctx, tx, "LOGIN_LOCKED", userID, storedUsername, "FAILED", "FAILED_ATTEMPT_LIMIT", now, map[string]any{"lockSeconds": 900}); err != nil {
				return LoginResult{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginResult{}, fmt.Errorf("commit failed login: %w", err)
		}
		return LoginResult{}, ErrInvalidCredentials
	}

	var mfaStatus string
	mfaErr := tx.QueryRow(ctx, `SELECT status FROM identity.user_mfa WHERE user_id = $1::uuid`, userID).Scan(&mfaStatus)
	if mfaErr != nil && !errors.Is(mfaErr, pgx.ErrNoRows) {
		return LoginResult{}, fmt.Errorf("read identity MFA status: %w", mfaErr)
	}
	hasActiveMFA := mfaErr == nil && mfaStatus == "ACTIVE"
	if request.RequiredACR == AssuranceMFA && !hasActiveMFA {
		if err := insertSecurityAudit(ctx, tx, "STEP_UP_FAILED", userID, storedUsername, "FAILED", "MFA_NOT_ENROLLED", now, map[string]any{}); err != nil {
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
SET user_id = $2::uuid, first_factor_at = $3
WHERE challenge_hash = $1
`, challengeHash, userID, now); err != nil {
			return LoginResult{}, fmt.Errorf("record identity first factor: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginResult{}, fmt.Errorf("commit identity first factor: %w", err)
		}
		return LoginResult{MFARequired: true}, ErrMFARequired
	}

	grant, err := issueAuthorizationCode(ctx, tx, challengeHash, request, userID, AssuranceBasic, []string{"pwd"}, now)
	if err != nil {
		return LoginResult{}, err
	}
	if err := recordSuccessfulLogin(ctx, tx, userID, now); err != nil {
		return LoginResult{}, err
	}
	if err := insertSecurityAudit(ctx, tx, "LOGIN_SUCCEEDED", userID, storedUsername, "SUCCEEDED", "PASSWORD_AUTHENTICATED", now, map[string]any{"acr": AssuranceBasic}); err != nil {
		return LoginResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginResult{}, fmt.Errorf("commit successful login: %w", err)
	}
	return LoginResult{Grant: grant}, nil
}

func (store *Store) ExchangeAuthorizationCode(ctx context.Context, rawCode, verifier, clientID, redirectURI string, now time.Time) (TokenSubject, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TokenSubject{}, fmt.Errorf("begin token transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now = resolveIdentityTime(now)
	codeHash := sha256.Sum256([]byte(rawCode))
	var userID, storedClientID, storedRedirectURI, nonce, codeChallenge, acr string
	var amr []string
	var expiresAt, authTime time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
SELECT user_id::text, client_id, redirect_uri, nonce, code_challenge, expires_at, consumed_at, acr, amr, auth_time
FROM identity.authorization_codes
WHERE code_hash = $1
FOR UPDATE
`, codeHash[:]).Scan(&userID, &storedClientID, &storedRedirectURI, &nonce, &codeChallenge, &expiresAt, &consumedAt, &acr, &amr, &authTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenSubject{}, ErrAuthorizationCodeInvalid
	}
	if err != nil {
		return TokenSubject{}, fmt.Errorf("read authorization code: %w", err)
	}
	if consumedAt != nil || !expiresAt.After(now) || storedClientID != clientID || storedRedirectURI != redirectURI {
		return TokenSubject{}, ErrAuthorizationCodeInvalid
	}
	verifierDigest := sha256.Sum256([]byte(verifier))
	actualChallenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
	if !subtleStringCompare(actualChallenge, codeChallenge) {
		return TokenSubject{}, ErrAuthorizationCodeInvalid
	}

	var subject TokenSubject
	var status string
	err = tx.QueryRow(ctx, `SELECT id::text, display_name, email, status FROM identity.users WHERE id = $1::uuid`, userID).Scan(&subject.UserID, &subject.DisplayName, &subject.Email, &status)
	if err != nil || status != "ACTIVE" {
		return TokenSubject{}, ErrAuthorizationCodeInvalid
	}
	subject.Nonce = nonce
	subject.ACR = acr
	subject.AMR = append([]string(nil), amr...)
	subject.AuthTime = authTime.UTC()
	if _, err := tx.Exec(ctx, `UPDATE identity.authorization_codes SET consumed_at = $2 WHERE code_hash = $1`, codeHash[:], now); err != nil {
		return TokenSubject{}, fmt.Errorf("consume authorization code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenSubject{}, fmt.Errorf("commit token exchange: %w", err)
	}
	return subject, nil
}

func randomToken(byteCount int) string {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func hashOpaque(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func subtleStringCompare(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func newUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	milliseconds := uint64(now.UTC().UnixMilli())
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	if _, err := rand.Read(value[6:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
