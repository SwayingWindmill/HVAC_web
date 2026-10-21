package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

// Sending a WeChat subscription message to an account (#664, docs/specs/miniprogram-wechat.md).
// Notification Service decides who gets which reminder; identity-service, the only holder of the
// OpenIDs and the AppSecret, turns an account into its OpenID and sends.

// WeChatSendAction is what a Gateway workload grant to send a message names.
const WeChatSendAction = "wechat:send"

var (
	ErrWeChatNotBound = errors.New("this account has no WeChat bound")
	// ErrWeChatOutcomeUnknown is a message WeChat may have delivered without saying so clearly
	// (no answer in time, or an answer that is not WeChat's): it must not be sent again.
	ErrWeChatOutcomeUnknown = errors.New("WeChat may have sent the message")
	// errWeChatUnclear is an exchange where the request may have reached WeChat.
	errWeChatUnclear = errors.New("WeChat did not answer clearly")
)

// WeChatMessage is a subscription message: a template the administrator chose in 微信公众平台,
// the mini program page it opens, and the value of each of the template's keywords.
type WeChatMessage struct {
	TemplateID string            `json:"templateId"`
	Page       string            `json:"page"`
	Data       map[string]string `json:"data"`
}

func (message WeChatMessage) valid() bool {
	return strings.TrimSpace(message.TemplateID) != "" && strings.TrimSpace(message.Page) != "" && len(message.Data) > 0 && len(message.Data) <= 10
}

// accessToken is WeChat's interface credential (stable_token), kept until shortly before it expires.
func (weChat *WeChat) accessToken(ctx context.Context, now time.Time) (string, error) {
	weChat.tokenMu.Lock()
	defer weChat.tokenMu.Unlock()
	if weChat.token != "" && now.Before(weChat.tokenExpiresAt) {
		return weChat.token, nil
	}
	body, _ := json.Marshal(map[string]any{"grant_type": "client_credential", "appid": weChat.AppID, "secret": weChat.Secret, "force_refresh": false})
	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
	}
	// No message goes out while fetching the credential, so any failure here is plainly "not sent".
	if err := weChat.post(ctx, "/cgi-bin/stable_token", body, &answer); err != nil {
		return "", ErrWeChatUnavailable
	}
	if answer.ErrCode != 0 {
		return "", WeChatRefusal{Code: answer.ErrCode}
	}
	if answer.AccessToken == "" || answer.ExpiresIn <= 0 {
		return "", ErrWeChatUnavailable
	}
	// Five minutes early, so a credential never expires between reading and using it.
	weChat.token, weChat.tokenExpiresAt = answer.AccessToken, now.Add(time.Duration(answer.ExpiresIn)*time.Second-5*time.Minute)
	return weChat.token, nil
}

// Send sends the message to the OpenID (subscribeMessage.send).
func (weChat *WeChat) Send(ctx context.Context, openID string, message WeChatMessage, now time.Time) error {
	token, err := weChat.accessToken(ctx, now)
	if err != nil {
		return err
	}
	data := make(map[string]map[string]string, len(message.Data))
	for keyword, value := range message.Data {
		data[keyword] = map[string]string{"value": value}
	}
	body, _ := json.Marshal(map[string]any{
		"touser": openID, "template_id": message.TemplateID, "page": message.Page, "data": data,
		"miniprogram_state": weChat.State, "lang": "zh_CN",
	})
	var answer struct {
		ErrCode int `json:"errcode"`
	}
	if err := weChat.post(ctx, "/cgi-bin/message/subscribe/send?access_token="+url.QueryEscape(token), body, &answer); err != nil {
		if errors.Is(err, errWeChatUnclear) {
			return ErrWeChatOutcomeUnknown
		}
		return err
	}
	switch answer.ErrCode {
	case 0:
		return nil
	// 40001, 42001: the credential was replaced or expired early; the next message fetches a new one.
	case 40001, 42001:
		weChat.tokenMu.Lock()
		weChat.token = ""
		weChat.tokenMu.Unlock()
	}
	return WeChatRefusal{Code: answer.ErrCode}
}

// post sends to WeChat: ErrWeChatUnavailable when the request never reached it, errWeChatUnclear
// when it may have. Its errors never carry the request, whose URL or body holds the AppSecret or
// the credential.
func (weChat *WeChat) post(ctx context.Context, path string, body []byte, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, weChat.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return ErrWeChatUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := weChat.Client.Do(request)
	var opError *net.OpError
	if err != nil && errors.As(err, &opError) && opError.Op == "dial" {
		return ErrWeChatUnavailable
	}
	if err != nil {
		return errWeChatUnclear
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(into) != nil {
		return errWeChatUnclear
	}
	return nil
}

func (store *Store) WeChatOpenID(ctx context.Context, userID, appID string) (string, error) {
	var openID string
	err := store.pool.QueryRow(ctx, `SELECT open_id FROM identity.wechat_bindings WHERE user_id = $1::uuid AND app_id = $2`, userID, appID).Scan(&openID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrWeChatNotBound
	}
	if err != nil {
		return "", fmt.Errorf("read WeChat binding: %w", err)
	}
	return openID, nil
}

// sendWeChatMessage serves POST /app/wechat/messages: a grant the Gateway's workload signed for
// itself names the one account to send to.
func (server *Server) sendWeChatMessage(writer http.ResponseWriter, request *http.Request) {
	claims, err := identitycontext.VerifyDelegation(server.gatewayKey, request.Header.Get("X-Delegation-Grant"))
	account := ""
	if err == nil && len(claims.Scopes) == 1 {
		account = strings.TrimPrefix(claims.Scopes[0], "account:")
		err = identitycontext.ValidateWorkloadDelegation(claims, server.now(), server.gatewaySPIFFE, server.gatewaySPIFFE, WeChatAudience, WeChatSendAction, "account:"+account)
	}
	if err != nil || !validUserID(account) {
		writeOAuthError(writer, http.StatusUnauthorized, "access_denied", "a Gateway workload grant to send to one account is required")
		return
	}
	var message WeChatMessage
	if !decodeAppRequest(writer, request, 8<<10, &message) {
		return
	}
	if !message.valid() {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "a template, a page and the keyword values are required")
		return
	}
	if server.wechat == nil {
		writeWeChatNotConfigured(writer)
		return
	}
	openID, err := server.store.WeChatOpenID(request.Context(), account, server.wechat.AppID)
	if err == nil {
		err = server.wechat.Send(request.Context(), openID, message, server.now())
	}
	var refusal WeChatRefusal
	if errors.As(err, &refusal) {
		server.log().Warn("wechat_refused", "error_code", "WECHAT_REFUSED", "wechat_errcode", refusal.Code)
	}
	switch {
	case err == nil:
		writer.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrWeChatNotBound):
		writeOAuthError(writer, http.StatusNotFound, "wechat_not_bound", "this account has no WeChat bound")
	case errors.As(err, &refusal) && refusal.Code == 43101:
		writeOAuthError(writer, http.StatusConflict, "wechat_reminder_refused", "the person has no reminder of this kind left")
	case errors.Is(err, ErrWeChatOutcomeUnknown):
		server.log().Warn("wechat_send_outcome_unknown", "error_code", "WECHAT_SEND_OUTCOME_UNKNOWN")
		writeOAuthError(writer, http.StatusBadGateway, "wechat_outcome_unknown", "WeChat may have sent the message; do not send it again")
	case errors.Is(err, ErrWeChatNotSetUp):
		writeOAuthError(writer, http.StatusServiceUnavailable, "wechat_not_configured", "WeChat refuses this server: check the AppID, the AppSecret and the IP allowlist")
	default:
		server.log().Warn("wechat_send_failed", "error_code", "WECHAT_SEND_FAILED")
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable", "WeChat or the identity service is unavailable")
	}
}
