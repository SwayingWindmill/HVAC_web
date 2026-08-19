package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
)

const (
	AssuranceBasic = "urn:hvac:loa:1"
	AssuranceMFA   = "urn:hvac:loa:2"
)

type MFAProtector struct {
	keyID string
	aead  cipher.AEAD
}

type TOTPEnrollment struct {
	ProvisioningKey string `json:"provisioningKey"`
	ProvisioningURI string `json:"provisioningUri"`
}

type mfaRecord struct {
	KeyID           string
	Nonce           []byte
	Ciphertext      []byte
	Status          string
	FailedAttempts  int
	LockedUntil     *time.Time
	LastUsedCounter int64
}

func GenerateMFAEncryptionKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate identity MFA encryption key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

func LoadMFAProtectorFile(path string) (*MFAProtector, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("identity MFA encryption key file is required")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity MFA encryption key: %w", err)
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("identity MFA encryption key must be one base64url-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("open identity MFA encryption key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create identity MFA protector: %w", err)
	}
	digest := sha256.Sum256(key)
	return &MFAProtector{keyID: base64.RawURLEncoding.EncodeToString(digest[:12]), aead: aead}, nil
}

func (protector *MFAProtector) Protect(secret string) (nonce, ciphertext []byte, keyID string, err error) {
	if protector == nil || protector.aead == nil || strings.TrimSpace(secret) == "" {
		return nil, nil, "", errors.New("identity MFA protector input is invalid")
	}
	nonce = make([]byte, protector.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", fmt.Errorf("generate identity MFA nonce: %w", err)
	}
	ciphertext = protector.aead.Seal(nil, nonce, []byte(secret), []byte(protector.keyID))
	return nonce, ciphertext, protector.keyID, nil
}

func (protector *MFAProtector) Open(keyID string, nonce, ciphertext []byte) (string, error) {
	if protector == nil || protector.aead == nil || keyID != protector.keyID || len(nonce) != protector.aead.NonceSize() || len(ciphertext) == 0 {
		return "", errors.New("identity MFA secret cannot be opened")
	}
	plaintext, err := protector.aead.Open(nil, nonce, ciphertext, []byte(keyID))
	if err != nil {
		return "", errors.New("identity MFA secret cannot be opened")
	}
	secret := string(plaintext)
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("identity MFA secret is empty")
	}
	return secret, nil
}

func GenerateTOTP(issuer, accountName string) (secret, provisioningURI string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      strings.TrimSpace(issuer),
		AccountName: strings.TrimSpace(accountName),
		SecretSize:  32,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate identity TOTP: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

func (store *Store) StartTOTPEnrollment(ctx context.Context, username, issuer string, protector *MFAProtector, now time.Time) (TOTPEnrollment, error) {
	normalized := normalizeUsername(username)
	if normalized == "" {
		return TOTPEnrollment{}, errors.New("identity username is required")
	}
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("begin identity TOTP enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, storedUsername, email string
	if err := tx.QueryRow(ctx, `SELECT id::text, username, email FROM identity.users WHERE username_normalized = $1 AND status = 'ACTIVE' FOR UPDATE`, normalized).Scan(&userID, &storedUsername, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TOTPEnrollment{}, errors.New("active identity user not found")
		}
		return TOTPEnrollment{}, fmt.Errorf("read identity user for TOTP enrollment: %w", err)
	}
	keyMaterial, uri, err := GenerateTOTP(issuer, email)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	nonce, ciphertext, keyID, err := protector.Protect(keyMaterial)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	commandTag, err := tx.Exec(ctx, `
INSERT INTO identity.user_mfa (
  user_id, method, key_id, cipher_nonce, ciphertext, status, failed_attempts, locked_until,
  last_used_counter, activated_at, last_verified_at, revision, created_at, updated_at
) VALUES ($1::uuid, 'TOTP', $2, $3, $4, 'PENDING', 0, NULL, -1, NULL, NULL, 1, $5, $5)
ON CONFLICT (user_id) DO UPDATE SET
  method = 'TOTP', key_id = EXCLUDED.key_id, cipher_nonce = EXCLUDED.cipher_nonce,
  ciphertext = EXCLUDED.ciphertext, status = 'PENDING', failed_attempts = 0,
  locked_until = NULL, last_used_counter = -1, activated_at = NULL, last_verified_at = NULL,
  revision = identity.user_mfa.revision + 1, updated_at = EXCLUDED.updated_at
WHERE identity.user_mfa.status = 'PENDING'
`, userID, keyID, nonce, ciphertext, now)
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("persist identity TOTP enrollment: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return TOTPEnrollment{}, errors.New("active TOTP is already configured")
	}
	if err := insertSecurityAudit(ctx, tx, "MFA_ENROLLMENT_STARTED", userID, storedUsername, "SUCCEEDED", "TOTP_ENROLLMENT_STARTED", now, map[string]any{"method": "TOTP"}); err != nil {
		return TOTPEnrollment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TOTPEnrollment{}, fmt.Errorf("commit identity TOTP enrollment: %w", err)
	}
	return TOTPEnrollment{ProvisioningKey: keyMaterial, ProvisioningURI: uri}, nil
}

func (store *Store) ActivateTOTP(ctx context.Context, username, passcode string, protector *MFAProtector, now time.Time) error {
	normalized := normalizeUsername(username)
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin identity TOTP activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, storedUsername string
	var record mfaRecord
	if err := tx.QueryRow(ctx, `
SELECT u.id::text, u.username, m.key_id, m.cipher_nonce, m.ciphertext, m.status,
       m.failed_attempts, m.locked_until, m.last_used_counter
FROM identity.users u
JOIN identity.user_mfa m ON m.user_id = u.id
WHERE u.username_normalized = $1 AND u.status = 'ACTIVE'
FOR UPDATE OF u, m
`, normalized).Scan(&userID, &storedUsername, &record.KeyID, &record.Nonce, &record.Ciphertext, &record.Status, &record.FailedAttempts, &record.LockedUntil, &record.LastUsedCounter); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("pending TOTP enrollment not found")
		}
		return fmt.Errorf("read identity TOTP enrollment: %w", err)
	}
	if record.Status != "PENDING" {
		return errors.New("pending TOTP enrollment not found")
	}
	keyMaterial, err := protector.Open(record.KeyID, record.Nonce, record.Ciphertext)
	if err != nil {
		return err
	}
	counter, ok := validateTOTP(keyMaterial, passcode, now, record.LastUsedCounter)
	if !ok {
		if err := insertSecurityAudit(ctx, tx, "MFA_FAILED", userID, storedUsername, "FAILED", "TOTP_ACTIVATION_CODE_INVALID", now, map[string]any{"method": "TOTP"}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit failed TOTP activation: %w", err)
		}
		return ErrMFAInvalid
	}
	if _, err := tx.Exec(ctx, `
UPDATE identity.user_mfa
SET status = 'ACTIVE', failed_attempts = 0, locked_until = NULL, last_used_counter = $2,
    activated_at = $3, last_verified_at = $3, revision = revision + 1, updated_at = $3
WHERE user_id = $1::uuid
`, userID, counter, now); err != nil {
		return fmt.Errorf("activate identity TOTP: %w", err)
	}
	if err := insertSecurityAudit(ctx, tx, "MFA_ENABLED", userID, storedUsername, "SUCCEEDED", "TOTP_ENABLED", now, map[string]any{"method": "TOTP"}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity TOTP activation: %w", err)
	}
	return nil
}

func (store *Store) DisableTOTP(ctx context.Context, username string, now time.Time) error {
	normalized := normalizeUsername(username)
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin identity TOTP disable: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, storedUsername string
	if err := tx.QueryRow(ctx, `
SELECT u.id::text, u.username
FROM identity.users u JOIN identity.user_mfa m ON m.user_id = u.id
WHERE u.username_normalized = $1 AND u.status = 'ACTIVE'
FOR UPDATE OF u, m
`, normalized).Scan(&userID, &storedUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("identity TOTP enrollment not found")
		}
		return fmt.Errorf("read identity TOTP enrollment: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.user_mfa WHERE user_id = $1::uuid`, userID); err != nil {
		return fmt.Errorf("disable identity TOTP: %w", err)
	}
	if err := insertSecurityAudit(ctx, tx, "MFA_DISABLED", userID, storedUsername, "SUCCEEDED", "TOTP_DISABLED", now, map[string]any{"method": "TOTP"}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity TOTP disable: %w", err)
	}
	return nil
}

func (store *Store) CompleteTOTP(ctx context.Context, challengeHash []byte, passcode string, protector *MFAProtector, now time.Time) (LoginGrant, error) {
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LoginGrant{}, fmt.Errorf("begin TOTP login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	request, attemptCount, err := readAuthorizationRequest(ctx, tx, challengeHash)
	if err != nil {
		return LoginGrant{}, err
	}
	if !request.ExpiresAt.After(now) || attemptCount >= 10 || request.UserID == "" || request.FirstFactorAt == nil || now.Sub(*request.FirstFactorAt) > 5*time.Minute {
		return LoginGrant{}, ErrAuthorizationRequestExpired
	}
	var storedUsername, userStatus string
	var record mfaRecord
	if err := tx.QueryRow(ctx, `
SELECT u.username, u.status, m.key_id, m.cipher_nonce, m.ciphertext, m.status,
       m.failed_attempts, m.locked_until, m.last_used_counter
FROM identity.users u
JOIN identity.user_mfa m ON m.user_id = u.id
WHERE u.id = $1::uuid
FOR UPDATE OF u, m
`, request.UserID).Scan(&storedUsername, &userStatus, &record.KeyID, &record.Nonce, &record.Ciphertext, &record.Status, &record.FailedAttempts, &record.LockedUntil, &record.LastUsedCounter); err != nil {
		return LoginGrant{}, ErrMFAInvalid
	}
	if userStatus != "ACTIVE" || record.Status != "ACTIVE" || (record.LockedUntil != nil && record.LockedUntil.After(now)) {
		return LoginGrant{}, ErrMFAInvalid
	}
	keyMaterial, err := protector.Open(record.KeyID, record.Nonce, record.Ciphertext)
	if err != nil {
		return LoginGrant{}, err
	}
	counter, ok := validateTOTP(keyMaterial, passcode, now, record.LastUsedCounter)
	if !ok {
		record.FailedAttempts++
		var nextLockedUntil *time.Time
		lockedNow := false
		if record.FailedAttempts >= 5 {
			value := now.Add(15 * time.Minute)
			nextLockedUntil = &value
			lockedNow = true
		}
		if _, err := tx.Exec(ctx, `
UPDATE identity.user_mfa
SET failed_attempts = $2, locked_until = $3, revision = revision + 1, updated_at = $4
WHERE user_id = $1::uuid
`, request.UserID, record.FailedAttempts, nextLockedUntil, now); err != nil {
			return LoginGrant{}, fmt.Errorf("record identity MFA failure: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE identity.authorization_requests SET attempt_count = attempt_count + 1 WHERE challenge_hash = $1`, challengeHash); err != nil {
			return LoginGrant{}, fmt.Errorf("record identity MFA request failure: %w", err)
		}
		eventType := "MFA_FAILED"
		if request.RequiredACR == AssuranceMFA {
			eventType = "STEP_UP_FAILED"
		}
		if err := insertSecurityAudit(ctx, tx, eventType, request.UserID, storedUsername, "FAILED", "TOTP_CODE_INVALID", now, map[string]any{"method": "TOTP"}); err != nil {
			return LoginGrant{}, err
		}
		if lockedNow {
			if err := insertSecurityAudit(ctx, tx, "MFA_LOCKED", request.UserID, storedUsername, "FAILED", "MFA_FAILED_ATTEMPT_LIMIT", now, map[string]any{"lockSeconds": 900}); err != nil {
				return LoginGrant{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginGrant{}, fmt.Errorf("commit identity MFA failure: %w", err)
		}
		return LoginGrant{}, ErrMFAInvalid
	}
	if _, err := tx.Exec(ctx, `
UPDATE identity.user_mfa
SET failed_attempts = 0, locked_until = NULL, last_used_counter = $2, last_verified_at = $3,
    revision = revision + 1, updated_at = $3
WHERE user_id = $1::uuid
`, request.UserID, counter, now); err != nil {
		return LoginGrant{}, fmt.Errorf("record identity MFA success: %w", err)
	}
	grant, err := issueAuthorizationCode(ctx, tx, challengeHash, request, request.UserID, AssuranceMFA, []string{"pwd", "otp"}, now)
	if err != nil {
		return LoginGrant{}, err
	}
	if err := recordSuccessfulLogin(ctx, tx, request.UserID, now); err != nil {
		return LoginGrant{}, err
	}
	eventType, reason := "LOGIN_SUCCEEDED", "MFA_AUTHENTICATED"
	if request.RequiredACR == AssuranceMFA {
		eventType, reason = "STEP_UP_SUCCEEDED", "STEP_UP_AUTHENTICATED"
	}
	if err := insertSecurityAudit(ctx, tx, eventType, request.UserID, storedUsername, "SUCCEEDED", reason, now, map[string]any{"acr": AssuranceMFA, "amr": []string{"pwd", "otp"}}); err != nil {
		return LoginGrant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginGrant{}, fmt.Errorf("commit identity MFA success: %w", err)
	}
	return grant, nil
}

func validateTOTP(secret, passcode string, now time.Time, lastUsedCounter int64) (int64, bool) {
	passcode = strings.TrimSpace(passcode)
	if len(passcode) != 6 {
		return 0, false
	}
	for _, value := range passcode {
		if value < '0' || value > '9' {
			return 0, false
		}
	}
	base := now.UTC().Unix() / 30
	for _, offset := range []int64{-1, 0, 1} {
		counter := base + offset
		if counter <= lastUsedCounter || counter < 0 {
			continue
		}
		code, err := totp.GenerateCode(secret, time.Unix(counter*30, 0).UTC())
		if err == nil && subtleStringCompare(code, passcode) {
			return counter, true
		}
	}
	return 0, false
}
