package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
)

func TestLoginDoesNotRevealWhetherUsernameExists(t *testing.T) {
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	runtimeURL := os.Getenv("IDENTITY_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("IDENTITY_TEST_ADMIN_DATABASE_URL and IDENTITY_TEST_RUNTIME_DATABASE_URL are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	adminStore, err := OpenStore(ctx, adminURL)
	if err != nil {
		t.Fatalf("open identity admin store: %v", err)
	}
	defer adminStore.Close()
	runtimeStore, err := OpenStore(ctx, runtimeURL)
	if err != nil {
		t.Fatalf("open identity runtime store: %v", err)
	}
	defer runtimeStore.Close()

	now := time.Now().UTC()
	username := "enumeration-" + strings.ReplaceAll(now.Format("20060102T150405.000000000"), ".", "")
	password := randomToken(24)
	if _, err := adminStore.CreateUser(ctx, CreateUserInput{
		Username: username, DisplayName: "Enumeration Test", Email: username + "@example.invalid", Password: password, Now: now,
	}); err != nil {
		t.Fatalf("create enumeration test user: %v", err)
	}

	attempt := func(attemptedUsername, attemptedPassword string) error {
		challenge := hashOpaque(randomToken(24))
		if err := runtimeStore.CreateAuthorizationRequest(ctx, AuthorizationRequest{
			ChallengeHash: challenge,
			ClientID:      "hvac-web-s0", RedirectURI: "https://example.invalid/auth/callback", State: randomToken(12), Nonce: randomToken(12),
			CodeChallenge: randomToken(32), Scope: "openid profile email", RequiredACR: AssuranceBasic, ExpiresAt: now.Add(2 * time.Minute),
		}); err != nil {
			t.Fatalf("create authorization request: %v", err)
		}
		_, err := runtimeStore.CompleteLogin(ctx, challenge, attemptedUsername, attemptedPassword, now)
		return err
	}

	unknownErr := attempt(username+"-missing", randomToken(24))
	wrongPasswordErr := attempt(username, randomToken(24))
	if !errors.Is(unknownErr, ErrInvalidCredentials) || !errors.Is(wrongPasswordErr, ErrInvalidCredentials) {
		t.Fatalf("login enumeration boundary diverged: unknown=%v wrong-password=%v", unknownErr, wrongPasswordErr)
	}
}

func TestStartTOTPEnrollmentCannotDowngradeActiveMFA(t *testing.T) {
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	if adminURL == "" {
		t.Skip("IDENTITY_TEST_ADMIN_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := OpenStore(ctx, adminURL)
	if err != nil {
		t.Fatalf("open identity admin store: %v", err)
	}
	defer store.Close()

	encodedKey, err := GenerateMFAEncryptionKey()
	if err != nil {
		t.Fatalf("generate MFA encryption key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "mfa-encryption.key")
	if err := os.WriteFile(keyPath, []byte(encodedKey+"\n"), 0o600); err != nil {
		t.Fatalf("write MFA encryption key: %v", err)
	}
	protector, err := LoadMFAProtectorFile(keyPath)
	if err != nil {
		t.Fatalf("load MFA protector: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	username := "mfa-active-" + strings.ReplaceAll(now.Format("20060102T150405.000000000"), ".", "")
	if _, err := store.CreateUser(ctx, CreateUserInput{
		Username:    username,
		DisplayName: "MFA Active",
		Email:       username + "@example.invalid",
		Password:    randomToken(24),
		Now:         now,
	}); err != nil {
		t.Fatalf("create identity user: %v", err)
	}
	enrollment, err := store.StartTOTPEnrollment(ctx, username, "HVAC", protector, now)
	if err != nil {
		t.Fatalf("start TOTP enrollment: %v", err)
	}
	code, err := totp.GenerateCode(enrollment.ProvisioningKey, now)
	if err != nil {
		t.Fatalf("generate TOTP activation code: %v", err)
	}
	if err := store.ActivateTOTP(ctx, username, code, protector, now); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	if _, err := store.StartTOTPEnrollment(ctx, username, "HVAC", protector, now.Add(time.Second)); err == nil {
		t.Fatal("expected active TOTP re-enrollment to be rejected instead of downgrading the account")
	}

	var status string
	if err := store.pool.QueryRow(ctx, `
SELECT m.status
FROM identity.user_mfa m
JOIN identity.users u ON u.id = m.user_id
WHERE u.username_normalized = $1
`, normalizeUsername(username)).Scan(&status); err != nil {
		t.Fatalf("read TOTP status after rejected re-enrollment: %v", err)
	}
	if status != "ACTIVE" {
		t.Fatalf("expected TOTP to remain ACTIVE after rejected re-enrollment, got %q", status)
	}
}

func TestCreateUserRollsBackWhenSecurityOutboxWriteFails(t *testing.T) {
	migratorURL := os.Getenv("IDENTITY_TEST_MIGRATOR_DATABASE_URL")
	adminURL := os.Getenv("IDENTITY_TEST_ADMIN_DATABASE_URL")
	if migratorURL == "" || adminURL == "" {
		t.Skip("IDENTITY_TEST_MIGRATOR_DATABASE_URL and IDENTITY_TEST_ADMIN_DATABASE_URL are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	migrator, err := pgxpool.New(ctx, migratorURL)
	if err != nil {
		t.Fatalf("open identity migrator database: %v", err)
	}
	defer migrator.Close()
	if _, err := migrator.Exec(ctx, `REVOKE INSERT ON identity.security_outbox FROM identity_admin`); err != nil {
		t.Fatalf("revoke identity security outbox insert: %v", err)
	}
	defer func() {
		if _, err := migrator.Exec(context.Background(), `GRANT INSERT ON identity.security_outbox TO identity_admin`); err != nil {
			t.Errorf("restore identity security outbox insert: %v", err)
		}
	}()

	store, err := OpenStore(ctx, adminURL)
	if err != nil {
		t.Fatalf("open identity admin store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	username := "audit-rollback-" + strings.ReplaceAll(now.Format("20060102T150405.000000000"), ".", "")
	_, err = store.CreateUser(ctx, CreateUserInput{
		Username:    username,
		DisplayName: "Audit Rollback",
		Email:       username + "@example.invalid",
		Password:    randomToken(24),
		Now:         now,
	})
	if err == nil {
		t.Fatal("expected user creation to fail when the durable security outbox cannot be written")
	}

	var userCount int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM identity.users WHERE username_normalized = $1`, normalizeUsername(username)).Scan(&userCount); err != nil {
		t.Fatalf("count identity user after rollback: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("expected user state to roll back with audit failure, found %d row(s)", userCount)
	}
	var auditCount int
	if err := migrator.QueryRow(ctx, `SELECT count(*) FROM identity.security_audit_intents WHERE subject_ref = $1`, securitySubjectRef(username)).Scan(&auditCount); err != nil {
		t.Fatalf("count identity audit intent after rollback: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("expected audit intent to roll back with outbox failure, found %d row(s)", auditCount)
	}
}
