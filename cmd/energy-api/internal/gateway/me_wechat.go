package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
)

// The signed-in person's WeChat, bound in the mini program (#664, docs/specs/miniprogram-wechat.md).
// identity-service trades the wx.login code for the OpenID and keeps the binding. The Gateway
// names the account in a grant it signs for the Session, never one from the request. A browser has
// no WeChat, so only mini program Sessions reach this.

const (
	publicMeWeChatPath = "/api/v1/me/wechat"
	identityWeChatPath = "/app/wechat/binding"
	// identity-service accepts a WeChat call only with a grant naming this audience and action.
	identityWeChatAudience = "identity-service"
	identityWeChatAction   = "wechat:manage"
)

type weChatBindRequest struct {
	Code string `json:"code"`
}

// weChatSession is the mini program Session of an account the platform manages, or false after
// answering the refusal.
func (h *handler) weChatSession(writer http.ResponseWriter, request *http.Request) (bffSession, bool) {
	session, failure := h.identitySession(request)
	if failure == nil && session.Client != sessionstore.ClientMiniProgram {
		failure = &identityFailure{403, "WECHAT_MINI_PROGRAM_ONLY", "WeChat unavailable", "WeChat is bound and removed only in the mini program.", false}
	}
	if failure == nil && session.Principal.Issuer != h.identity.config.OIDCIssuer {
		failure = &identityFailure{403, "WECHAT_FORBIDDEN", "WeChat unavailable", "This account is not managed by the platform.", false}
	}
	if failure != nil {
		writeIdentityFailure(writer, request, *failure)
		return bffSession{}, false
	}
	return session, true
}

func (h *handler) MeWeChat(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writeMethodNotAllowedFor(writer, request, "GET, POST, DELETE")
		return
	}
	session, ok := h.weChatSession(writer, request)
	if !ok {
		return
	}
	var body any
	if request.Method == http.MethodPost {
		var input weChatBindRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || strings.TrimSpace(input.Code) == "" {
			writeIdentityFailure(writer, request, identityFailure{400, "REQUEST_INVALID", "Request invalid", "Send the code from wx.login, and nothing else.", false})
			return
		}
		body = input
	}
	grant, err := h.identityWeChatGrant(session)
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "DELEGATION_UNAVAILABLE", "WeChat unavailable", "The Gateway could not sign the request for identity-service.", true})
		return
	}
	status, reply, err := h.identity.backchannel(request, request.Method, identityWeChatPath, body, map[string]string{"X-Delegation-Grant": grant})
	if err != nil {
		writeIdentityFailure(writer, request, identityFailure{503, "IDENTITY_UNAVAILABLE", "WeChat unavailable", "The identity service could not be reached.", true})
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	switch status {
	case http.StatusNoContent:
		// Removing WeChat also forgets the reminders it allowed (docs/specs/miniprogram-wechat.md).
		if request.Method == http.MethodDelete {
			if status, _, err := h.callWeChatReminders(request, session, http.MethodDelete, nil); err != nil || status != http.StatusNoContent {
				writeProblem(writer, request, http.StatusServiceUnavailable, "NOTIFICATION_UNAVAILABLE", "Notification unavailable", "WeChat was removed, but its reminders could not be cleared; try again.", true, nil)
				return
			}
		}
		writer.WriteHeader(http.StatusNoContent)
	case http.StatusOK:
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(reply)
	default:
		writeIdentityFailure(writer, request, weChatRefusal(reply))
	}
}

// identityWeChatGrant names the Session's account for identity-service, for one WeChat call.
func (h *handler) identityWeChatGrant(session bffSession) (string, error) {
	now := h.identity.now().UTC()
	expiresAt := now.Add(h.identity.config.DelegationTTL)
	if expiresAt.After(session.ExpiresAt) {
		expiresAt = session.ExpiresAt
	}
	return identitycontext.SignDelegation(h.identity.config.DelegationSigner, identitycontext.DelegationClaims{
		Issuer: h.identity.config.ExecutingWorkloadSPIFFE, ExecutingService: h.identity.config.ExecutingWorkloadSPIFFE,
		Subject: session.Principal.Subject, SubjectIssuer: session.Principal.Issuer,
		Audience: identityWeChatAudience, TenantID: session.TenantID, Actions: []string{identityWeChatAction},
		Scopes: []string{"session:" + session.ID}, PolicyRevision: h.identity.config.PolicyRevision,
		SessionID: session.ID, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: randomURLToken(16),
	})
}

// weChatRefusal is identity-service's refusal as the problem the mini program shows.
func weChatRefusal(reply []byte) identityFailure {
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(reply, &refusal)
	switch refusal.Error {
	case "wechat_code_invalid":
		return identityFailure{400, "WECHAT_CODE_INVALID", "WeChat code invalid", "The WeChat code is invalid or already used; try again.", false}
	case "wechat_already_bound":
		return identityFailure{409, "WECHAT_ALREADY_BOUND", "WeChat already bound", "This WeChat is bound to another account; remove it there first.", false}
	case "wechat_not_configured":
		return identityFailure{503, "WECHAT_NOT_CONFIGURED", "WeChat not set up", "WeChat is not set up on this server yet.", false}
	default:
		return identityFailure{503, "IDENTITY_UNAVAILABLE", "WeChat unavailable", "The identity service could not complete the request.", true}
	}
}
