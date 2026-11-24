package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const loginErrorCookieName = "__Host-hvac_idp_error"

type loginContextPayload struct {
	Status    string `json:"status"`
	Step      string `json:"step,omitempty"`
	CSRFToken string `json:"csrfToken,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (server *Server) loginContext(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Vary", "Cookie")

	if !server.isAllowedBrowserOrigin(request) {
		writeLoginContextJSON(writer, http.StatusForbidden, loginContextPayload{Status: "forbidden"})
		return
	}

	flashError := ""
	if errorCookie, err := request.Cookie(loginErrorCookieName); err == nil {
		flashError = allowedLoginErrorCode(errorCookie.Value)
		clearLoginErrorCookie(writer)
	}
	if flashError == "expired" {
		if _, err := request.Cookie(loginCookieName); err == nil {
			clearLoginCookie(writer)
		}
		writeLoginContextJSON(writer, http.StatusOK, loginContextPayload{Status: "expired"})
		return
	}

	loginCookie, err := request.Cookie(loginCookieName)
	if err != nil || loginCookie.Value == "" {
		writeLoginContextJSON(writer, http.StatusOK, loginContextPayload{Status: "none"})
		return
	}

	if server.store == nil {
		writeLoginContextJSON(writer, http.StatusServiceUnavailable, loginContextPayload{
			Status: "unavailable",
			Error:  "unavailable",
		})
		return
	}

	step, err := server.store.ReadLoginContext(request.Context(), hashOpaque(loginCookie.Value), server.now().UTC())
	if errors.Is(err, ErrAuthorizationRequestExpired) {
		clearLoginCookie(writer)
		writeLoginContextJSON(writer, http.StatusOK, loginContextPayload{Status: "expired"})
		return
	}
	if err != nil {
		writeLoginContextJSON(writer, http.StatusServiceUnavailable, loginContextPayload{
			Status: "unavailable",
			Error:  "unavailable",
		})
		return
	}

	writeLoginContextJSON(writer, http.StatusOK, loginContextPayload{
		Status:    "ready",
		Step:      step,
		CSRFToken: deriveLoginCSRF(loginCookie.Value),
		Error:     flashError,
	})
}

func writeLoginContextJSON(writer http.ResponseWriter, statusCode int, payload loginContextPayload) {
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}

func deriveLoginCSRF(challenge string) string {
	mac := hmac.New(sha256.New, []byte(challenge))
	mac.Write([]byte("hvac-idp-login-csrf-v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func allowedLoginErrorCode(code string) string {
	switch code {
	case "invalid_credentials", "totp_invalid", "mfa_enrollment_required", "unavailable", "expired":
		return code
	default:
		return ""
	}
}

func expectedOrigin(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func (server *Server) isAllowedBrowserOrigin(request *http.Request) bool {
	if site := strings.ToLower(strings.TrimSpace(request.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if strings.EqualFold(origin, "null") {
		return false
	}
	expected := expectedOrigin(server.signInURL)
	if expected != "" && strings.EqualFold(origin, expected) {
		return true
	}
	if expected == "" && request.Host != "" {
		parsedOrigin, err := url.Parse(origin)
		if err != nil {
			return false
		}
		requestScheme := "http"
		if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
			requestScheme = "https"
		}
		return strings.EqualFold(parsedOrigin.Scheme, requestScheme) && strings.EqualFold(parsedOrigin.Host, request.Host)
	}
	return false
}

func setLoginCookie(writer http.ResponseWriter, challenge string) {
	http.SetCookie(writer, &http.Cookie{
		Name:     loginCookieName,
		Value:    challenge,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
}

func clearLoginCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     loginCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func setLoginErrorCookie(writer http.ResponseWriter, code string) {
	allowed := allowedLoginErrorCode(code)
	if allowed == "" {
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     loginErrorCookieName,
		Value:    allowed,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   120,
	})
}

func clearLoginErrorCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     loginErrorCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
