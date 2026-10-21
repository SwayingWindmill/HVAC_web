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
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// WeChat for the platform's own mini program (#664, docs/specs/miniprogram-wechat.md).
// identity-service is the only part of the platform that holds the AppSecret, keeps OpenIDs and
// reaches WeChat; other services name an account, never an OpenID.

const WeChatAPIBase = "https://api.weixin.qq.com"

var (
	ErrWeChatCodeInvalid  = errors.New("the WeChat sign-in code is invalid or already used")
	ErrWeChatUnavailable  = errors.New("WeChat is unavailable")
	ErrWeChatNotSetUp     = errors.New("WeChat refuses this server's AppID, AppSecret or IP")
	ErrWeChatAlreadyBound = errors.New("this WeChat is bound to another account")
)

// WeChatRefusal is an errcode WeChat answered with. It carries nothing else of the request, whose
// URL holds the AppSecret, so it is safe to log.
type WeChatRefusal struct{ Code int }

func (refusal WeChatRefusal) Error() string {
	return fmt.Sprintf("WeChat answered errcode %d", refusal.Code)
}

func (refusal WeChatRefusal) Is(target error) bool {
	switch refusal.Code {
	// 40029: invalid code; 40163: code already used.
	case 40029, 40163:
		return target == ErrWeChatCodeInvalid
	// 40013: invalid AppID; 40125: invalid AppSecret; 40164: this server is not on the IP allowlist.
	case 40013, 40125, 40164:
		return target == ErrWeChatNotSetUp
	default:
		return target == ErrWeChatUnavailable
	}
}

type WeChat struct {
	AppID   string
	Secret  string
	BaseURL string
	Client  *http.Client
	// Which version of the mini program a message opens: developer, trial or formal.
	State string

	tokenMu        sync.Mutex
	token          string
	tokenExpiresAt time.Time
}

// LoadWeChat reads the AppSecret the administrator put on the server. Without the file WeChat is
// not set up yet, and every WeChat request says so.
func LoadWeChat(appID, secretFile, state string) (*WeChat, error) {
	appID, secretFile = strings.TrimSpace(appID), strings.TrimSpace(secretFile)
	if state != "developer" && state != "trial" && state != "formal" {
		return nil, errors.New("the WeChat mini program state must be developer, trial or formal")
	}
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
	return &WeChat{AppID: appID, Secret: strings.TrimSpace(string(secret)), BaseURL: WeChatAPIBase, Client: &http.Client{Timeout: 5 * time.Second}, State: state}, nil
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
	if answer.ErrCode != 0 {
		return "", WeChatRefusal{Code: answer.ErrCode}
	}
	if answer.OpenID == "" {
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

// The Gateway calls these for the signed-in account of a mini program Session. Each call carries a
// grant the Gateway signed for that account (X-Delegation-Grant); the account is read from the
// grant alone, so nothing else on the internal network can bind a WeChat to someone's account.

const (
	// WeChatAudience and WeChatAction are what a Gateway grant for these calls names.
	WeChatAudience = "identity-service"
	WeChatAction   = "wechat:manage"
)

type wechatBindRequest struct {
	Code string `json:"code"`
}

// wechatAccount is the account the Gateway's grant names, or false after answering the refusal.
func (server *Server) wechatAccount(writer http.ResponseWriter, request *http.Request) (string, bool) {
	claims, err := identitycontext.VerifyDelegation(server.gatewayKey, request.Header.Get("X-Delegation-Grant"))
	if err == nil {
		err = identitycontext.ValidateDelegationFromIssuer(claims, server.now(), server.gatewaySPIFFE, server.gatewaySPIFFE, WeChatAudience, WeChatAction, "session:"+claims.SessionID)
	}
	if err == nil && (claims.SubjectIssuer != server.issuer || !validUserID(claims.Subject)) {
		err = errors.New("the grant names an account this identity service does not keep")
	}
	if err != nil {
		writeOAuthError(writer, http.StatusUnauthorized, "access_denied", "a Gateway grant for the signed-in account is required")
		return "", false
	}
	return claims.Subject, true
}

func (server *Server) bindWeChat(writer http.ResponseWriter, request *http.Request) {
	userID, ok := server.wechatAccount(writer, request)
	if !ok {
		return
	}
	var input wechatBindRequest
	if !decodeAppRequest(writer, request, 4<<10, &input) {
		return
	}
	openID, ok := server.openID(writer, request, input.Code)
	if !ok {
		return
	}
	err := server.store.BindWeChat(request.Context(), userID, server.wechat.AppID, openID, server.now())
	switch {
	case errors.Is(err, ErrWeChatAlreadyBound):
		writeOAuthError(writer, http.StatusConflict, "wechat_already_bound", "this WeChat is bound to another account")
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
	default:
		writer.WriteHeader(http.StatusNoContent)
	}
}

// openID trades the code from wx.login for the person's OpenID, or answers why WeChat would not.
func (server *Server) openID(writer http.ResponseWriter, request *http.Request, code string) (string, bool) {
	if server.wechat == nil {
		writeWeChatNotConfigured(writer)
		return "", false
	}
	openID, err := server.wechat.OpenID(request.Context(), code)
	var refusal WeChatRefusal
	if errors.As(err, &refusal) {
		server.log().Warn("wechat_refused", "error_code", "WECHAT_REFUSED", "wechat_errcode", refusal.Code)
	}
	switch {
	case errors.Is(err, ErrWeChatCodeInvalid):
		writeOAuthError(writer, http.StatusBadRequest, "wechat_code_invalid", "the WeChat code is invalid or already used; ask for a new one")
	case errors.Is(err, ErrWeChatNotSetUp):
		writeOAuthError(writer, http.StatusServiceUnavailable, "wechat_not_configured", "WeChat refuses this server: check the AppID, the AppSecret and the IP allowlist")
	case err != nil:
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "WeChat is unavailable")
	default:
		return openID, true
	}
	return "", false
}

func (server *Server) unbindWeChat(writer http.ResponseWriter, request *http.Request) {
	userID, ok := server.wechatAccount(writer, request)
	if !ok {
		return
	}
	if err := server.store.UnbindWeChat(request.Context(), userID); err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "identity service is unavailable")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) readWeChatBinding(writer http.ResponseWriter, request *http.Request) {
	userID, ok := server.wechatAccount(writer, request)
	if !ok {
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
