package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrUsernameTaken  = errors.New("identity username is already taken")
	ErrUserNotFound   = errors.New("identity user not found")
	ErrProfileInvalid = errors.New("identity username, display name or email is invalid")
)

// DirectoryUser is what user administration shows about an account; it never carries the
// password hash.
type DirectoryUser struct {
	ID          string
	Username    string
	DisplayName string
	Email       string
	Status      string
	LockedUntil *time.Time
	LastLoginAt *time.Time
}

const directoryColumns = `id::text, username, display_name, email, status, locked_until, last_login_at`

func scanDirectoryUser(row pgx.Row) (DirectoryUser, error) {
	var user DirectoryUser
	err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Status, &user.LockedUntil, &user.LastLoginAt)
	return user, err
}

// UsersByID returns the accounts among ids that exist, in no particular order.
func (store *Store) UsersByID(ctx context.Context, ids []string) ([]DirectoryUser, error) {
	if len(ids) == 0 {
		return []DirectoryUser{}, nil
	}
	rows, err := store.pool.Query(ctx, `SELECT `+directoryColumns+` FROM identity.users WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("read identity users: %w", err)
	}
	defer rows.Close()
	users := []DirectoryUser{}
	for rows.Next() {
		user, err := scanDirectoryUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan identity user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// SetPassword replaces an account's password on an administrator's behalf and clears any lockout.
func (store *Store) SetPassword(ctx context.Context, administrator, userID, password string, now time.Time) error {
	return store.updateUser(ctx, administrator, userID, now, func(ctx context.Context, tx pgx.Tx, user lockedUser) (string, error) {
		if err := ValidatePassword(user.username, password); err != nil {
			return "", err
		}
		passwordPHC, err := HashPassword(password)
		if err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `UPDATE identity.users SET password_phc = $2, failed_attempts = 0, locked_until = NULL, revision = revision + 1, updated_at = $3 WHERE id = $1::uuid`, userID, passwordPHC, user.now)
		return "PASSWORD_RESET", err
	})
}

// SetStatus enables (ACTIVE) or disables (DISABLED) sign-in for an account.
func (store *Store) SetStatus(ctx context.Context, administrator, userID, status string, now time.Time) error {
	event := map[string]string{"ACTIVE": "USER_ENABLED", "DISABLED": "USER_DISABLED"}[status]
	if event == "" {
		return fmt.Errorf("unsupported identity user status %q", status)
	}
	return store.updateUser(ctx, administrator, userID, now, func(ctx context.Context, tx pgx.Tx, user lockedUser) (string, error) {
		_, err := tx.Exec(ctx, `UPDATE identity.users SET status = $2, failed_attempts = 0, locked_until = NULL, revision = revision + 1, updated_at = $3 WHERE id = $1::uuid`, userID, status, user.now)
		return event, err
	})
}

func (store *Store) UpdateProfile(ctx context.Context, administrator, userID, displayName, email string, now time.Time) error {
	displayName, email = strings.TrimSpace(displayName), strings.TrimSpace(email)
	if err := validateProfile(displayName, email); err != nil {
		return err
	}
	return store.updateUser(ctx, administrator, userID, now, func(ctx context.Context, tx pgx.Tx, user lockedUser) (string, error) {
		_, err := tx.Exec(ctx, `UPDATE identity.users SET display_name = $2, email = $3, revision = revision + 1, updated_at = $4 WHERE id = $1::uuid`, userID, displayName, email, user.now)
		return "PROFILE_UPDATED", err
	})
}

// ChangePassword lets a signed-in user replace their own password. A wrong current password
// counts toward the same lockout as a failed sign-in.
func (store *Store) ChangePassword(ctx context.Context, userID, current, next string, now time.Time) error {
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin identity password change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var username, passwordPHC, status string
	var failedAttempts int
	var lockedUntil *time.Time
	err = tx.QueryRow(ctx, `SELECT username, status, failed_attempts, locked_until FROM identity.users WHERE id = $1::uuid FOR UPDATE`, userID).
		Scan(&username, &status, &failedAttempts, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("read identity user for password change: %w", err)
	}
	if passwordPHC, err = readPasswordHash(ctx, tx, userID); err != nil {
		return err
	}
	locked := lockedUntil != nil && lockedUntil.After(now)
	if status != "ACTIVE" || locked || !VerifyPassword(current, passwordPHC) {
		if status == "ACTIVE" && !locked {
			failedAttempts++
			var nextLockedUntil *time.Time
			if failedAttempts >= 5 {
				value := now.Add(15 * time.Minute)
				nextLockedUntil = &value
			}
			if _, err := tx.Exec(ctx, `UPDATE identity.users SET failed_attempts = $2, locked_until = $3, revision = revision + 1, updated_at = $4 WHERE id = $1::uuid`, userID, failedAttempts, nextLockedUntil, now); err != nil {
				return fmt.Errorf("record identity password change failure: %w", err)
			}
		}
		if err := insertSecurityAudit(ctx, tx, "PASSWORD_CHANGED", userID, username, "FAILED", "INVALID_CREDENTIALS", now, map[string]any{}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit failed identity password change: %w", err)
		}
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(username, next); err != nil {
		return err
	}
	nextPHC, err := HashPassword(next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.users SET password_phc = $2, failed_attempts = 0, locked_until = NULL, revision = revision + 1, updated_at = $3 WHERE id = $1::uuid`, userID, nextPHC, now); err != nil {
		return fmt.Errorf("change identity password: %w", err)
	}
	if err := insertSecurityAudit(ctx, tx, "PASSWORD_CHANGED", userID, username, "SUCCEEDED", "PASSWORD_CHANGED", now, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity password change: %w", err)
	}
	return nil
}

type lockedUser struct {
	username string
	now      time.Time
}

// updateUser locks one account, applies change and records its audit event, naming the
// administrator who made it, in one transaction.
func (store *Store) updateUser(ctx context.Context, administrator, userID string, now time.Time, change func(context.Context, pgx.Tx, lockedUser) (string, error)) error {
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin identity user update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user := lockedUser{now: now}
	if err := tx.QueryRow(ctx, `SELECT username FROM identity.users WHERE id = $1::uuid FOR UPDATE`, userID).Scan(&user.username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("read identity user for update: %w", err)
	}
	event, err := change(ctx, tx, user)
	if err != nil {
		return err
	}
	if err := insertSecurityAudit(ctx, tx, event, userID, user.username, "SUCCEEDED", event, now, map[string]any{"administeredBy": administrator}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit identity user update: %w", err)
	}
	return nil
}

func validateProfile(displayName, email string) error {
	if displayName == "" || len(displayName) > 256 || email == "" || len(email) > 320 || !strings.Contains(email, "@") {
		return ErrProfileInvalid
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}
