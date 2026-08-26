package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
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
	SignOutURL(oidcDiscovery, oidcSignOutRequest) (string, error)
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
	ACRValues    string
}

type oidcSignOutRequest struct {
	ClientID              string
	PostLogoutRedirectURI string
}

type oidcCodeExchangeRequest struct {
	ClientID     string
	RedirectURI  string
	Code         string
	CodeVerifier string
}

type standardOIDCProtocol struct{}

func newStandardOIDCProtocol() oidcProtocol { return standardOIDCProtocol{} }

func (standardOIDCProtocol) Discover(ctx context.Context, client *http.Client, issuer string) (oidcDiscovery, error) {
	capture := &oidcResponseCapture{}
	endpoint := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := oidcCallClient(ctx, client, capture).Do(request)
	if err != nil {
		return oidcDiscovery{}, newOIDCProtocolError(err, capture)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return oidcDiscovery{}, newOIDCProtocolError(fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode), capture)
	}
	var document oidcDiscovery
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return oidcDiscovery{}, newOIDCProtocolError(fmt.Errorf("decode OIDC discovery: %w", err), capture)
	}
	return document, nil
}

func (standardOIDCProtocol) AuthorizationURL(discovery oidcDiscovery, request oidcAuthorizationRequest) (string, error) {
	endpoint, err := url.Parse(discovery.AuthorizationEndpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("OIDC authorization endpoint is invalid")
	}
	verifierDigest := sha256.Sum256([]byte(request.CodeVerifier))
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", request.ClientID)
	query.Set("redirect_uri", request.RedirectURI)
	query.Set("scope", "openid profile email")
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(verifierDigest[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("state", request.State)
	query.Set("nonce", request.Nonce)
	if request.LoginHint != "" {
		query.Set("login_hint", request.LoginHint)
	}
	if request.ACRValues != "" {
		query.Set("acr_values", request.ACRValues)
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (standardOIDCProtocol) SignOutURL(discovery oidcDiscovery, request oidcSignOutRequest) (string, error) {
	endpoint, err := url.Parse(discovery.EndSessionEndpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("OIDC end-session endpoint is invalid")
	}
	query := endpoint.Query()
	query.Set("client_id", request.ClientID)
	query.Set("post_logout_redirect_uri", request.PostLogoutRedirectURI)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (standardOIDCProtocol) ExchangeCode(ctx context.Context, client *http.Client, discovery oidcDiscovery, request oidcCodeExchangeRequest) (oidcTokenResponse, error) {
	capture := &oidcResponseCapture{}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", request.ClientID)
	form.Set("redirect_uri", request.RedirectURI)
	form.Set("code", request.Code)
	form.Set("code_verifier", request.CodeVerifier)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("Accept", "application/json")
	response, err := oidcCallClient(ctx, client, capture).Do(httpRequest)
	if err != nil {
		return oidcTokenResponse{}, newOIDCProtocolError(err, capture)
	}
	defer response.Body.Close()
	var payload oidcTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return oidcTokenResponse{}, newOIDCProtocolError(fmt.Errorf("decode OIDC token response: %w", err), capture)
	}
	if response.StatusCode != http.StatusOK {
		if payload.Description == "" {
			payload.Description = capture.ErrorDescription
		}
		return payload, newOIDCProtocolError(fmt.Errorf("OIDC token endpoint returned HTTP %d", response.StatusCode), capture)
	}
	return payload, nil
}

func (standardOIDCProtocol) VerifyIDToken(ctx context.Context, client *http.Client, discovery oidcDiscovery, _ string, keyID, token string) error {
	capture := &oidcResponseCapture{}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discovery.JWKSURI, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := oidcCallClient(ctx, client, capture).Do(request)
	if err != nil {
		return newOIDCProtocolError(err, capture)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return newOIDCProtocolError(fmt.Errorf("OIDC JWKS endpoint returned HTTP %d", response.StatusCode), capture)
	}
	var keySet jose.JSONWebKeySet
	if err := json.NewDecoder(response.Body).Decode(&keySet); err != nil || len(keySet.Keys) == 0 {
		return errOIDCJWKSInvalid
	}
	keys := keySet.Key(keyID)
	if len(keys) == 0 {
		return errOIDCSignatureKeyUnknown
	}
	signed, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256, jose.ES384})
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := signed.Verify(key.Key); err == nil {
			return nil
		}
	}
	return errors.New("OIDC signature verification failed")
}

type oidcProtocolError struct {
	StatusCode  int
	Description string
	cause       error
}

func (failure *oidcProtocolError) Error() string { return failure.cause.Error() }
func (failure *oidcProtocolError) Unwrap() error { return failure.cause }

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
