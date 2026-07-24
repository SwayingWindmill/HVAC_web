package telemetry

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
)

var (
	ErrGrantRejected            = errors.New("telemetry grant rejected")
	ErrAuthorizationUnavailable = errors.New("telemetry authorization unavailable")
)

type AccessContext struct {
	TokenID              string
	PrincipalID          string
	Subject              string
	SubjectIssuer        string
	SessionID            string
	ActingOrganizationID string
	PolicyRevision       string
}

type GrantAuthorizer interface {
	Authorize(context.Context, string, string, telemetryauth.Action, []telemetryauth.Target) (AccessContext, error)
}

type HTTPGrantAuthorizer struct {
	endpoint  string
	client    *http.Client
	publicKey crypto.PublicKey
	issuer    string
	audience  string
	now       func() time.Time
}

type grantConsumeRequest struct {
	DelegationGrant      string                 `json:"delegationGrant"`
	PrincipalID          string                 `json:"principalId"`
	SessionID            string                 `json:"sessionId"`
	ActingOrganizationID string                 `json:"actingOrganizationId"`
	Action               telemetryauth.Action   `json:"action"`
	Targets              []telemetryauth.Target `json:"targets"`
}

type grantAcceptance struct {
	TokenID              string               `json:"tokenId"`
	PrincipalID          string               `json:"principalId"`
	SessionID            string               `json:"sessionId"`
	ActingOrganizationID string               `json:"actingOrganizationId"`
	Action               telemetryauth.Action `json:"action"`
	ScopeDigest          string               `json:"scopeDigest"`
	PolicyRevision       string               `json:"policyRevision"`
	ExpiresAt            int64                `json:"expiresAt"`
}

func NewHTTPGrantAuthorizer(endpoint string, client *http.Client, publicKey crypto.PublicKey, issuer, audience string) (*HTTPGrantAuthorizer, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return nil, errors.New("IAM telemetry authorization endpoint must use HTTPS or loopback HTTP")
	}
	if client == nil || publicKey == nil || strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, errors.New("IAM telemetry authorization configuration is incomplete")
	}
	return &HTTPGrantAuthorizer{
		endpoint: strings.TrimRight(parsed.String(), "/") + "/internal/v1/telemetry/grants:consume",
		client:   client, publicKey: publicKey, issuer: issuer, audience: audience, now: time.Now,
	}, nil
}

func (authorizer *HTTPGrantAuthorizer) Authorize(ctx context.Context, peerSPIFFE, grant string, action telemetryauth.Action, targets []telemetryauth.Target) (AccessContext, error) {
	if authorizer == nil || authorizer.client == nil || authorizer.publicKey == nil {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	claims, err := telemetryauth.VerifyGrant(authorizer.publicKey, grant)
	if err != nil || !authorizer.preflight(claims, peerSPIFFE, action, targets) {
		return AccessContext{}, ErrGrantRejected
	}
	payload, err := json.Marshal(grantConsumeRequest{
		DelegationGrant: grant, PrincipalID: claims.PrincipalID, SessionID: claims.SessionID,
		ActingOrganizationID: claims.ActingOrganizationID, Action: action, Targets: targets,
	})
	if err != nil {
		return AccessContext{}, ErrGrantRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, authorizer.endpoint, bytes.NewReader(payload))
	if err != nil {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := authorizer.client.Do(request)
	if err != nil {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, telemetryauth.MaximumEncodedGrantSize+1))
	if err != nil || len(body) > telemetryauth.MaximumEncodedGrantSize {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized {
		return AccessContext{}, ErrGrantRejected
	}
	if response.StatusCode != http.StatusOK {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	var accepted grantAcceptance
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&accepted); err != nil || ensureJSONEOF(decoder) != nil {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	if accepted.TokenID != claims.TokenID || accepted.PrincipalID != claims.PrincipalID || accepted.SessionID != claims.SessionID ||
		accepted.ActingOrganizationID != claims.ActingOrganizationID || accepted.Action != claims.Action || accepted.ScopeDigest != claims.ScopeDigest ||
		accepted.PolicyRevision != claims.PolicyRevision || accepted.ExpiresAt != claims.ExpiresAt {
		return AccessContext{}, ErrAuthorizationUnavailable
	}
	return AccessContext{
		TokenID: accepted.TokenID, PrincipalID: accepted.PrincipalID,
		Subject: claims.Subject, SubjectIssuer: claims.SubjectIssuer, SessionID: accepted.SessionID,
		ActingOrganizationID: accepted.ActingOrganizationID, PolicyRevision: accepted.PolicyRevision,
	}, nil
}

func (authorizer *HTTPGrantAuthorizer) preflight(claims telemetryauth.GrantClaims, peerSPIFFE string, action telemetryauth.Action, targets []telemetryauth.Target) bool {
	now := authorizer.now().UTC()
	if strings.TrimSpace(peerSPIFFE) == "" || claims.Presenter != peerSPIFFE || claims.Issuer != authorizer.issuer || claims.Audience != authorizer.audience || claims.Transitive {
		return false
	}
	if claims.Action != action || !action.Valid() || claims.PrincipalID == "" || claims.SessionID == "" || claims.ActingOrganizationID == "" || claims.TokenID == "" {
		return false
	}
	if claims.IssuedAt > now.Add(5*time.Second).Unix() || claims.ExpiresAt <= now.Unix() || claims.ExpiresAt <= claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > telemetryauth.MaximumGrantLifetime {
		return false
	}
	digest, err := telemetryauth.ScopeDigest(action, claims.ActingOrganizationID, targets)
	if err != nil || digest != claims.ScopeDigest {
		return false
	}
	canonical, err := telemetryauth.CanonicalTargets(targets)
	if err != nil || len(canonical) != claims.TargetCount {
		return false
	}
	keyCount := 0
	for _, target := range canonical {
		keyCount += len(target.Keys)
	}
	return keyCount == claims.KeyCount && claims.PolicyRevision != "" && len(claims.ActorChain) > 0 && claims.RequestID != "" && claims.TraceID != "" && claims.Route != ""
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

var _ GrantAuthorizer = (*HTTPGrantAuthorizer)(nil)
