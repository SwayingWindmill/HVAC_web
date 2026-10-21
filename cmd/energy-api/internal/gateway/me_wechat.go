package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

// The signed-in person's WeChat, bound in the mini program (#664, docs/specs/miniprogram-wechat.md).
// identity-service trades the wx.login code for the OpenID and keeps the binding; the Gateway
// names the account of the Session, never one from the request. A browser has no WeChat, so only
// mini program Sessions reach this.

const publicMeWeChatPath = "/api/v1/me/wechat"

type weChatBindRequest struct {
	Code string `json:"code"`
}

// identityWeChatBind is identity-service's /app/wechat/binding request.
type identityWeChatBind struct {
	UserID string `json:"userId"`
	Code   string `json:"code"`
}

var weChatMiniProgramOnly = identityFailure{403, "WECHAT_MINI_PROGRAM_ONLY", "WeChat unavailable", "WeChat is bound and removed only in the mini program.", false}

func (h *handler) MeWeChat(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writeMethodNotAllowedFor(writer, request, "GET, POST, DELETE")
		return
	}
	session, failure := h.identitySession(request)
	if failure == nil && session.Client != sessionstore.ClientMiniProgram {
		failure = &weChatMiniProgramOnly
	}
	if failure == nil && session.Principal.Issuer != h.identity.config.OIDCIssuer {
		failure = &identityFailure{403, "WECHAT_FORBIDDEN", "WeChat unavailable", "This account is not managed by the platform.", false}
	}
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	account := url.Values{"userId": {session.Principal.Subject}}.Encode()
	var status int
	var reply []byte
	switch request.Method {
	case http.MethodGet:
		status, reply, failure = h.identity.callIdentityWeChat(request, http.MethodGet, "/app/wechat/binding?"+account, nil)
	case http.MethodDelete:
		status, reply, failure = h.identity.callIdentityWeChat(request, http.MethodDelete, "/app/wechat/binding?"+account, nil)
	case http.MethodPost:
		var input weChatBindRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || strings.TrimSpace(input.Code) == "" {
			writeIdentityFailure(writer, request, identityFailure{400, "REQUEST_INVALID", "Request invalid", "Send the code from wx.login, and nothing else.", false})
			return
		}
		status, reply, failure = h.identity.callIdentityWeChat(request, http.MethodPost, "/app/wechat/binding", identityWeChatBind{UserID: session.Principal.Subject, Code: input.Code})
	}
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNoContent {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(reply)
}

// callIdentityWeChat sends one WeChat request to identity-service over the backchannel and turns
// its refusals into the problems the mini program shows.
func (controller *identityController) callIdentityWeChat(request *http.Request, method, path string, body any) (int, []byte, *identityFailure) {
	unavailable := &identityFailure{503, "IDENTITY_UNAVAILABLE", "WeChat unavailable", "The identity service could not be reached.", true}
	base := strings.TrimSpace(controller.config.OIDCBackchannelBaseURL)
	if base == "" {
		base = controller.config.OIDCIssuer
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, unavailable
		}
		payload = bytes.NewReader(encoded)
	}
	outbound, err := http.NewRequestWithContext(request.Context(), method, strings.TrimRight(base, "/")+path, payload)
	if err != nil {
		return 0, nil, unavailable
	}
	if body != nil {
		outbound.Header.Set("Content-Type", "application/json")
	}
	response, err := controller.config.OIDCHTTPClient.Do(outbound)
	if err != nil {
		return 0, nil, unavailable
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		return 0, nil, unavailable
	}
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent {
		return response.StatusCode, reply, nil
	}
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(reply, &refusal)
	switch refusal.Error {
	case "wechat_code_invalid":
		return 0, nil, &identityFailure{400, "WECHAT_CODE_INVALID", "WeChat code invalid", "The WeChat code is invalid or already used; try again.", false}
	case "wechat_already_bound":
		return 0, nil, &identityFailure{409, "WECHAT_ALREADY_BOUND", "WeChat already bound", "This WeChat is bound to another account; remove it there first.", false}
	case "wechat_not_configured":
		return 0, nil, &identityFailure{503, "WECHAT_NOT_CONFIGURED", "WeChat not set up", "WeChat is not set up on this server yet.", false}
	default:
		return 0, nil, unavailable
	}
}
