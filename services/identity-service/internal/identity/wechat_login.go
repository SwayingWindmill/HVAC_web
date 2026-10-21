package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// WeChat one-tap sign-in in the mini program (#664, docs/specs/miniprogram-wechat.md section 四).
// The Gateway sends the code from wx.login with the same client, redirect, state, nonce and PKCE
// challenge as /app/login; the account bound to that WeChat signs in like after a password,
// including the one-time code. A WeChat bound to no account signs nobody in: there is no sign-up.

var ErrAccountUnavailable = errors.New("the account is disabled or locked")

type appWeChatLoginRequest struct {
	appClient
	Code string `json:"code"`
}

func (server *Server) appWeChatLogin(writer http.ResponseWriter, request *http.Request) {
	var input appWeChatLoginRequest
	if !decodeAppRequest(writer, request, 16<<10, &input) || !server.validAppClient(writer, input.appClient) {
		return
	}
	openID, ok := server.exchangeWeChatCode(writer, request, input.Code)
	if !ok {
		return
	}
	challenge, now, ok := server.startAppLogin(writer, request, input.appClient)
	if !ok {
		return
	}
	result, err := server.store.CompleteWeChatLogin(request.Context(), hashOpaque(challenge), server.wechat.AppID, openID, now)
	switch {
	case errors.Is(err, ErrWeChatNotBound):
		writeOAuthError(writer, http.StatusUnauthorized, "wechat_not_bound", "this WeChat is bound to no account")
	case errors.Is(err, ErrAccountUnavailable):
		writeOAuthError(writer, http.StatusUnauthorized, "account_unavailable", "the account bound to this WeChat is disabled or locked")
	default:
		writeAppLoginResult(writer, challenge, result, err)
	}
}

// CompleteWeChatLogin signs in the account bound to the WeChat. Disabled and locked accounts are
// refused, and the refusal recorded, as for a password.
func (store *Store) CompleteWeChatLogin(ctx context.Context, challengeHash []byte, appID, openID string, now time.Time) (LoginResult, error) {
	now = resolveIdentityTime(now)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LoginResult{}, fmt.Errorf("begin WeChat login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The handler kept this request a moment ago; it is read for the code it will carry.
	request, _, err := readAuthorizationRequest(ctx, tx, challengeHash)
	if err != nil {
		return LoginResult{}, err
	}
	var userID, username, status string
	var lockedUntil *time.Time
	err = tx.QueryRow(ctx, `
SELECT u.id::text, u.username, u.status, u.locked_until
FROM identity.wechat_bindings b
JOIN identity.users u ON u.id = b.user_id
WHERE b.app_id = $1 AND b.open_id = $2
FOR UPDATE OF u
`, appID, openID).Scan(&userID, &username, &status, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginResult{}, ErrWeChatNotBound
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("read the account bound to WeChat: %w", err)
	}
	if status != "ACTIVE" || (lockedUntil != nil && lockedUntil.After(now)) {
		reason := "ACCOUNT_DISABLED"
		if status == "ACTIVE" {
			reason = "ACCOUNT_LOCKED"
		}
		if err := insertSecurityAudit(ctx, tx, "LOGIN_FAILED", userID, username, "FAILED", reason, now, map[string]any{"amr": []string{"wechat"}}); err != nil {
			return LoginResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return LoginResult{}, fmt.Errorf("commit refused WeChat login: %w", err)
		}
		return LoginResult{}, ErrAccountUnavailable
	}
	return finishFirstFactor(ctx, tx, challengeHash, request, userID, username, "wechat", "WECHAT_AUTHENTICATED", now)
}
