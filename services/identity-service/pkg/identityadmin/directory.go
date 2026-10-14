// Package identityadmin is the user-administration surface of the platform identity directory.
// The Platform Gateway embeds it to create accounts, set passwords and enable or disable
// sign-in from 系统管理; authorization stays with IAM. It connects only as identity_admin.
package identityadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/identity-service/internal/identity"
)

const databaseRole = "identity_admin"

type User = identity.DirectoryUser

var (
	ErrUsernameTaken           = identity.ErrUsernameTaken
	ErrUserNotFound            = identity.ErrUserNotFound
	ErrInvalidCredentials      = identity.ErrInvalidCredentials
	ErrPasswordTooShort        = identity.ErrPasswordTooShort
	ErrPasswordTooLong         = identity.ErrPasswordTooLong
	ErrPasswordCommon          = identity.ErrPasswordCommon
	ErrPasswordMatchesUsername = identity.ErrPasswordMatchesUsername
)

// PasswordRejected reports whether err is one of the password rules, which the caller shows to
// the person choosing the password.
func PasswordRejected(err error) bool {
	return errors.Is(err, ErrPasswordTooShort) || errors.Is(err, ErrPasswordTooLong) ||
		errors.Is(err, ErrPasswordCommon) || errors.Is(err, ErrPasswordMatchesUsername)
}

type NewUser struct {
	Username    string
	DisplayName string
	Email       string
	Password    string
}

type Directory struct {
	store *identity.Store
	now   func() time.Time
}

func Open(ctx context.Context, databaseURL string) (*Directory, error) {
	pool, err := pgxpool.New(ctx, strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("open identity directory: %w", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
		pool.Close()
		return nil, fmt.Errorf("read identity directory role: %w", err)
	}
	if role != databaseRole {
		pool.Close()
		return nil, fmt.Errorf("identity directory role %q is not allowed", role)
	}
	pool.Close()
	store, err := identity.OpenStore(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Directory{store: store, now: time.Now}, nil
}

func (directory *Directory) Close() { directory.store.Close() }

func (directory *Directory) CreateUser(ctx context.Context, user NewUser) (User, error) {
	created, err := directory.store.CreateUser(ctx, identity.CreateUserInput{
		Username: user.Username, DisplayName: user.DisplayName, Email: user.Email, Password: user.Password, Now: directory.now(),
	})
	if err != nil {
		return User{}, err
	}
	return User{ID: created.ID, Username: created.Username, DisplayName: created.DisplayName, Email: created.Email, Status: "ACTIVE"}, nil
}

func (directory *Directory) UsersByID(ctx context.Context, ids []string) ([]User, error) {
	return directory.store.UsersByID(ctx, ids)
}

func (directory *Directory) UserByUsername(ctx context.Context, username string) (User, error) {
	return directory.store.UserByUsername(ctx, username)
}

func (directory *Directory) SetPassword(ctx context.Context, userID, password string) error {
	return directory.store.SetPassword(ctx, userID, password, directory.now())
}

// SetEnabled lets an account sign in again, or stops it from signing in.
func (directory *Directory) SetEnabled(ctx context.Context, userID string, enabled bool) error {
	status := "DISABLED"
	if enabled {
		status = "ACTIVE"
	}
	return directory.store.SetStatus(ctx, userID, status, directory.now())
}

func (directory *Directory) UpdateProfile(ctx context.Context, userID, displayName, email string) error {
	return directory.store.UpdateProfile(ctx, userID, displayName, email, directory.now())
}

func (directory *Directory) ChangePassword(ctx context.Context, userID, current, next string) error {
	return directory.store.ChangePassword(ctx, userID, current, next, directory.now())
}
