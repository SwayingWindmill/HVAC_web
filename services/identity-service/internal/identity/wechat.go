package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// WeChat for the platform's own mini program (#664, docs/specs/miniprogram-wechat.md).
// identity-service is the only part of the platform that holds the AppSecret, keeps OpenIDs and
// reaches WeChat; other services name an account, never an OpenID.

const WeChatAPIBase = "https://api.weixin.qq.com"

var (
	ErrWeChatCodeInvalid  = errors.New("the WeChat sign-in code is invalid or already used")
	ErrWeChatUnavailable  = errors.New("WeChat is unavailable")
	ErrWeChatAlreadyBound = errors.New("this WeChat is bound to another account")
)

type WeChat struct {
	AppID   string
	Secret  string
	BaseURL string
	Client  *http.Client
}

// LoadWeChat reads the AppSecret the administrator put on the server. Without the file WeChat is
// not set up yet, and every WeChat request says so.
func LoadWeChat(appID, secretFile string) (*WeChat, error) {
	appID, secretFile = strings.TrimSpace(appID), strings.TrimSpace(secretFile)
	if appID == "" || secretFile == "" {
		return nil, nil
	}
	secret, err := os.ReadFile(secretFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read WeChat AppSecret: %w", err)
	}
	if strings.TrimSpace(string(secret)) == "" {
		return nil, errors.New("the WeChat AppSecret file is empty")
	}
	return &WeChat{AppID: appID, Secret: strings.TrimSpace(string(secret)), BaseURL: WeChatAPIBase, Client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// OpenID trades a code from wx.login for the person's OpenID (code2Session). The session key
// WeChat sends with it is dropped here.
func (weChat *WeChat) OpenID(ctx context.Context, code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", ErrWeChatCodeInvalid
	}
	query := url.Values{"appid": {weChat.AppID}, "secret": {weChat.Secret}, "js_code": {code}, "grant_type": {"authorization_code"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, weChat.BaseURL+"/sns/jscode2session?"+query.Encode(), nil)
	if err != nil {
		return "", ErrWeChatUnavailable
	}
	response, err := weChat.Client.Do(request)
	if err != nil {
		return "", ErrWeChatUnavailable
	}
	defer response.Body.Close()
	var answer struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&answer) != nil {
		return "", ErrWeChatUnavailable
	}
	switch {
	// 40029: invalid code; 40163: code already used; 40226: WeChat blocked this sign-in as risky.
	case answer.ErrCode == 40029 || answer.ErrCode == 40163 || answer.ErrCode == 40226:
		return "", ErrWeChatCodeInvalid
	case answer.ErrCode != 0 || answer.OpenID == "":
		return "", ErrWeChatUnavailable
	}
	return answer.OpenID, nil
}

type WeChatBinding struct {
	Bound   bool       `json:"bound"`
	BoundAt *time.Time `json:"boundAt,omitempty"`
}

// BindWeChat binds the WeChat to the account, replacing a WeChat bound before; a WeChat already
// bound to another account is refused.
func (store *Store) BindWeChat(ctx context.Context, userID, appID, openID string, now time.Time) error {
	_, err := store.pool.Exec(ctx, `
INSERT INTO identity.wechat_bindings (user_id, app_id, open_id, bound_at) VALUES ($1::uuid, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET app_id = EXCLUDED.app_id, open_id = EXCLUDED.open_id, bound_at = EXCLUDED.bound_at
`, userID, appID, openID, now.UTC())
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.ConstraintName == "wechat_bindings_one_account_per_wechat" {
		return ErrWeChatAlreadyBound
	}
	if err != nil {
		return fmt.Errorf("bind WeChat: %w", err)
	}
	return nil
}

func (store *Store) UnbindWeChat(ctx context.Context, userID string) error {
	if _, err := store.pool.Exec(ctx, `DELETE FROM identity.wechat_bindings WHERE user_id = $1::uuid`, userID); err != nil {
		return fmt.Errorf("remove WeChat binding: %w", err)
	}
	return nil
}

func (store *Store) WeChatBinding(ctx context.Context, userID string) (WeChatBinding, error) {
	var boundAt time.Time
	err := store.pool.QueryRow(ctx, `SELECT bound_at FROM identity.wechat_bindings WHERE user_id = $1::uuid`, userID).Scan(&boundAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WeChatBinding{}, nil
	}
	if err != nil {
		return WeChatBinding{}, fmt.Errorf("read WeChat binding: %w", err)
	}
	boundAt = boundAt.UTC()
	return WeChatBinding{Bound: true, BoundAt: &boundAt}, nil
}

// The Gateway calls these for the signed-in account of a mini program Session; like /app/login
// they are reached only on the internal network.

type wechatBindRequest struct {
	UserID string `json:"userId"`
	Code   string `json:"code"`
}

func (server *Server) bindWeChat(writer http.ResponseWriter, request *http.Request) {
	var input wechatBindRequest
	if !decodeAppRequest(writer, request, 4<<10, &input) {
		return
	}
	if !validUserID(input.UserID) {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "userId is required")
		return
	}
	if server.wechat == nil {
		writeWeChatNotConfigured(writer)
		return
	}
	openID, err := server.wechat.OpenID(request.Context(), input.Code)
	if err == nil {
		err = server.store.BindWeChat(request.Context(), input.UserID, server.wechat.AppID, openID, server.now())
	}
	switch {
	case errors.Is(err, ErrWeChatCodeInvalid):
		writeOAuthError(writer, http.StatusBadRequest, "wechat_code_invalid", "the WeChat code is invalid or already used; ask for a new one")
	case errors.Is(err, ErrWeChatAlreadyBound):
		writeOAuthError(writer, http.StatusConflict, "wechat_already_bound", "this WeChat is bound to another account")
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "WeChat or the identity service is unavailable")
	default:
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (server *Server) unbindWeChat(writer http.ResponseWriter, request *http.Request) {
	userID := request.URL.Query().Get("userId")
	if !validUserID(userID) {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "userId is required")
		return
	}
	if err := server.store.UnbindWeChat(request.Context(), userID); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) readWeChatBinding(writer http.ResponseWriter, request *http.Request) {
	userID := request.URL.Query().Get("userId")
	if !validUserID(userID) {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "userId is required")
		return
	}
	binding, err := server.store.WeChatBinding(request.Context(), userID)
	if err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, binding)
}

var userIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validUserID(value string) bool { return userIDPattern.MatchString(value) }

func writeWeChatNotConfigured(writer http.ResponseWriter) {
	writeOAuthError(writer, http.StatusServiceUnavailable, "wechat_not_configured", "WeChat is not set up on this server yet")
}
