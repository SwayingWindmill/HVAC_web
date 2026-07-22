package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/logto-io/go/v2/core"
)

const maximumOIDCResponseSize = 1 << 20

var (
	errOIDCTokenIssuerInvalid   = errors.New("OIDC token issuer is invalid")
	errOIDCTokenAudienceInvalid = errors.New("OIDC token audience is invalid")
	errOIDCTokenExpired         = errors.New("OIDC token is expired")
	errOIDCTokenIssuedFuture    = errors.New("OIDC token was issued in the future")
	errOIDCTokenIssuedPast      = errors.New("OIDC token was issued too far in the past")
	errOIDCSignatureKeyUnknown  = errors.New("OIDC signature key is unknown")
	errOIDCJWKSInvalid          = errors.New("OIDC JWKS is invalid")
	errOIDCResponseTooLarge     = errors.New("OIDC response exceeds the accepted size")
)

type oidcProtocol interface {
	Discover(context.Context, *http.Client, string) (oidcDiscovery, error)
	AuthorizationURL(oidcDiscovery, oidcAuthorizationRequest) (string, error)
	ExchangeCode(context.Context, *http.Client, oidcDiscovery, oidcCodeExchangeRequest) (oidcTokenResponse, error)
	VerifyIDToken(context.Context, *http.Client, oidcDiscovery, string, string, string) error
}

type oidcAuthorizationRequest struct {
	ClientID     string
	RedirectURI  string
	CodeVerifier string
	State        string
	Nonce        string
	LoginHint    string
}

type oidcCodeExchangeRequest struct {
	ClientID     string
	RedirectURI  string
	Code         string
	CodeVerifier string
}

type logtoOIDCProtocol struct{}

func newLogtoOIDCProtocol() oidcProtocol {
	return logtoOIDCProtocol{}
}

func (logtoOIDCProtocol) Discover(ctx context.Context, client *http.Client, issuer string) (oidcDiscovery, error) {
	capture := &oidcResponseCapture{}
	document, err := core.FetchOidcConfig(oidcCallClient(ctx, client, capture), strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return oidcDiscovery{}, newOIDCProtocolError(err, capture)
	}
	return oidcDiscovery{document.Issuer, document.AuthorizationEndpoint, document.TokenEndpoint, document.JwksUri}, nil
}

func (logtoOIDCProtocol) AuthorizationURL(discovery oidcDiscovery, request oidcAuthorizationRequest) (string, error) {
	options := core.SignInUriGenerationOptions{}
	options.AuthorizationEndpoint = discovery.AuthorizationEndpoint
	options.ClientId = request.ClientID
	options.RedirectUri = request.RedirectURI
	options.CodeChallenge = core.GenerateCodeChallenge(request.CodeVerifier)
	options.State = request.State
	options.Scopes = []string{core.UserScopeEmail}
	options.LoginHint = request.LoginHint
	options.ExtraParams = map[string]string{}
	options.ExtraParams["nonce"] = request.Nonce
	generated, err := core.GenerateSignInUri(&options)
	if err != nil {
		return "", err
	}
	endpoint, err := url.Parse(generated)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Del(core.QueryKeyPrompt)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (logtoOIDCProtocol) ExchangeCode(ctx context.Context, client *http.Client, discovery oidcDiscovery, request oidcCodeExchangeRequest) (oidcTokenResponse, error) {
	capture := &oidcResponseCapture{}
	options := core.FetchTokenByAuthorizationCodeOptions{
		discovery.TokenEndpoint,
		request.Code,
		request.CodeVerifier,
		request.ClientID,
		"",
		request.RedirectURI,
		"",
	}
	response, err := core.FetchTokenByAuthorizationCode(oidcCallClient(ctx, client, capture), &options)
	if err != nil {
		return oidcTokenResponse{"", "", "", "", 0, "", capture.ErrorDescription}, newOIDCProtocolError(err, capture)
	}
	return oidcTokenResponse{response.AccessToken, response.RefreshToken, response.IdToken, capture.TokenType, response.ExpireIn, "", ""}, nil
}

func (logtoOIDCProtocol) VerifyIDToken(ctx context.Context, client *http.Client, discovery oidcDiscovery, clientID, keyID, token string) error {
	capture := &oidcResponseCapture{}
	response, err := core.FetchJwks(oidcCallClient(ctx, client, capture), discovery.JWKSURI)
	if err != nil {
		return newOIDCProtocolError(err, capture)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return errOIDCJWKSInvalid
	}
	var keySet jose.JSONWebKeySet
	if err := json.Unmarshal(encoded, &keySet); err != nil {
		return errOIDCJWKSInvalid
	}
	if len(keySet.Keys) == 0 {
		return errOIDCJWKSInvalid
	}
	if len(keySet.Key(keyID)) == 0 {
		return errOIDCSignatureKeyUnknown
	}
	if err := core.VerifyIdToken(token, clientID, discovery.Issuer, &keySet); err != nil {
		switch {
		case errors.Is(err, core.ErrTokenIssuerNotMatch):
			return errOIDCTokenIssuerInvalid
		case errors.Is(err, core.ErrTokenAudienceNotMatch):
			return errOIDCTokenAudienceInvalid
		case errors.Is(err, core.ErrTokenExpired):
			return errOIDCTokenExpired
		case errors.Is(err, core.ErrTokenIssuedInTheFuture):
			return errOIDCTokenIssuedFuture
		case errors.Is(err, core.ErrTokenIssuedInThePast):
			return errOIDCTokenIssuedPast
		default:
			return err
		}
	}
	return nil
}

type oidcProtocolError struct {
	StatusCode  int
	Description string
	cause       error
}

func (failure *oidcProtocolError) Error() string {
	return failure.cause.Error()
}

func (failure *oidcProtocolError) Unwrap() error {
	return failure.cause
}

func newOIDCProtocolError(err error, capture *oidcResponseCapture) error {
	if err == nil {
		return nil
	}
	if capture == nil {
		return &oidcProtocolError{cause: err}
	}
	return &oidcProtocolError{StatusCode: capture.StatusCode, Description: capture.ErrorDescription, cause: err}
}

func oidcProtocolErrorDetails(err error) (int, string) {
	var failure *oidcProtocolError
	if errors.As(err, &failure) {
		return failure.StatusCode, failure.Description
	}
	return 0, ""
}

type oidcResponseCapture struct {
	StatusCode       int
	TokenType        string
	ErrorDescription string
}

type boundedOIDCTransport struct {
	base    http.RoundTripper
	ctx     context.Context
	capture *oidcResponseCapture
}

func (transport boundedOIDCTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request.Clone(transport.ctx))
	if err != nil {
		return nil, err
	}
	if transport.capture != nil {
		transport.capture.StatusCode = response.StatusCode
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumOIDCResponseSize+1))
	_ = response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if len(body) > maximumOIDCResponseSize {
		return nil, errOIDCResponseTooLarge
	}
	if transport.capture != nil {
		var envelope struct {
			TokenType        string `json:"token_type"`
			ErrorDescription string `json:"error_description"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			transport.capture.TokenType = envelope.TokenType
			transport.capture.ErrorDescription = envelope.ErrorDescription
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

func oidcCallClient(ctx context.Context, client *http.Client, capture *oidcResponseCapture) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	base := cloned.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cloned.Transport = boundedOIDCTransport{base: base, ctx: ctx, capture: capture}
	return &cloned
}
