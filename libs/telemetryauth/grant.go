package telemetryauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	GrantVersion            = 1
	MaximumGrantLifetime    = 30 * time.Second
	MaximumEncodedGrantSize = 64 << 10
)

type ReasonCode string

const (
	ReasonAllowExactScope       ReasonCode = "ALLOW_EXACT_DEVICE_KEY_SCOPE"
	ReasonResourceNotFound      ReasonCode = "RESOURCE_NOT_FOUND"
	ReasonTelemetryKeyInvalid   ReasonCode = "TELEMETRY_KEY_INVALID"
	ReasonDenyExplicit          ReasonCode = "DENY_EXPLICIT"
	ReasonDenyPrincipalNotFound ReasonCode = "DENY_PRINCIPAL_NOT_FOUND"
	ReasonDenyPrincipalInactive ReasonCode = "DENY_PRINCIPAL_INACTIVE"
	ReasonDenyMembership        ReasonCode = "DENY_ACTING_ORGANIZATION_MEMBERSHIP_REQUIRED"
	ReasonDenyActionNotGranted  ReasonCode = "DENY_ACTION_NOT_GRANTED"
)

type AuthorizedTarget struct {
	DeviceID             string   `json:"deviceId"`
	OwningOrganizationID string   `json:"owningOrganizationId"`
	SiteID               string   `json:"siteId"`
	Keys                 []string `json:"keys"`
}

type Decision struct {
	Allowed              bool               `json:"allowed"`
	PrincipalID          string             `json:"principalId"`
	SubjectIssuer        string             `json:"subjectIssuer"`
	Subject              string             `json:"subject"`
	ActingOrganizationID string             `json:"actingOrganizationId"`
	Action               Action             `json:"action"`
	Targets              []AuthorizedTarget `json:"targets"`
	ScopeDigest          string             `json:"scopeDigest"`
	PolicyRevision       string             `json:"policyRevision"`
	ReasonCode           ReasonCode         `json:"reasonCode"`
	DecidedAt            string             `json:"decidedAt"`
}

type DecisionResponse struct {
	Decision        Decision `json:"decision"`
	DelegationGrant string   `json:"delegationGrant,omitempty"`
}

type Actor struct {
	Service  string `json:"service"`
	SPIFFEID string `json:"spiffeId"`
}

type GrantClaims struct {
	Version              int     `json:"version"`
	Issuer               string  `json:"issuer"`
	Presenter            string  `json:"presenter"`
	Audience             string  `json:"audience"`
	PrincipalID          string  `json:"principalId"`
	SubjectIssuer        string  `json:"subjectIssuer"`
	Subject              string  `json:"subject"`
	ActingOrganizationID string  `json:"actingOrganizationId"`
	ActorChain           []Actor `json:"actorChain"`
	Action               Action  `json:"action"`
	ScopeDigest          string  `json:"scopeDigest"`
	TargetCount          int     `json:"targetCount"`
	KeyCount             int     `json:"keyCount"`
	PolicyRevision       string  `json:"policyRevision"`
	SessionID            string  `json:"sessionId"`
	ParentTokenID        string  `json:"parentTokenId"`
	RequestID            string  `json:"requestId"`
	TraceID              string  `json:"traceId"`
	Route                string  `json:"route"`
	IssuedAt             int64   `json:"issuedAt"`
	ExpiresAt            int64   `json:"expiresAt"`
	TokenID              string  `json:"tokenId"`
	Transitive           bool    `json:"transitive"`
}

type GrantUseStatus struct {
	CurrentPolicyRevision string
	Revoked               bool
	Replayed              bool
}

type GrantUseChecker func(GrantClaims) (GrantUseStatus, error)

type GrantValidation struct {
	Now                  time.Time
	Issuer               string
	Presenter            string
	Audience             string
	PrincipalID          string
	SessionID            string
	Action               Action
	ActingOrganizationID string
	Targets              []Target
	UseChecker           GrantUseChecker
}

func SignGrant(signer crypto.Signer, claims GrantClaims) (string, error) {
	if signer == nil {
		return "", errors.New("telemetry grant signer is required")
	}
	claims.Version = GrantVersion
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal telemetry grant claims: %w", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sign telemetry grant claims: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyGrant(publicKey crypto.PublicKey, token string) (GrantClaims, error) {
	if len(token) == 0 || len(token) > MaximumEncodedGrantSize {
		return GrantClaims{}, errors.New("telemetry grant size is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return GrantClaims{}, errors.New("telemetry grant format is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return GrantClaims{}, errors.New("telemetry grant payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return GrantClaims{}, errors.New("telemetry grant signature encoding is invalid")
	}
	digest := sha256.Sum256(payload)
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			err = errors.New("ecdsa telemetry grant signature is invalid")
		}
	default:
		err = fmt.Errorf("unsupported telemetry grant public key %T", publicKey)
	}
	if err != nil {
		return GrantClaims{}, errors.New("telemetry grant signature is invalid")
	}
	var claims GrantClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return GrantClaims{}, errors.New("telemetry grant payload is invalid")
	}
	if claims.Version != GrantVersion {
		return GrantClaims{}, errors.New("telemetry grant version is unsupported")
	}
	return claims, nil
}

func ValidateGrant(claims GrantClaims, validation GrantValidation) error {
	if validation.Now.IsZero() {
		return errors.New("telemetry grant validation time is required")
	}
	if claims.Issuer != validation.Issuer || strings.TrimSpace(validation.Issuer) == "" {
		return errors.New("telemetry grant issuer is invalid")
	}
	if claims.Presenter != validation.Presenter || strings.TrimSpace(validation.Presenter) == "" {
		return errors.New("telemetry grant presenter is invalid")
	}
	if claims.Audience != validation.Audience || strings.TrimSpace(validation.Audience) == "" {
		return errors.New("telemetry grant audience is invalid")
	}
	if claims.Transitive {
		return errors.New("telemetry grant is transitive")
	}
	if claims.IssuedAt > validation.Now.Add(5*time.Second).Unix() {
		return errors.New("telemetry grant is not active")
	}
	if claims.ExpiresAt <= validation.Now.Unix() || claims.ExpiresAt <= claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > MaximumGrantLifetime {
		return errors.New("telemetry grant is expired or too long-lived")
	}
	if claims.PrincipalID == "" || claims.SubjectIssuer == "" || claims.Subject == "" || claims.ActingOrganizationID == "" || claims.SessionID == "" || claims.ParentTokenID == "" || claims.TokenID == "" {
		return errors.New("telemetry grant identity fields are incomplete")
	}
	if claims.RequestID == "" || claims.TraceID == "" || claims.Route == "" || len(claims.ActorChain) == 0 {
		return errors.New("telemetry grant correlation fields are incomplete")
	}
	if validation.PrincipalID == "" || claims.PrincipalID != validation.PrincipalID {
		return errors.New("telemetry grant principal is invalid")
	}
	if validation.SessionID == "" || claims.SessionID != validation.SessionID {
		return errors.New("telemetry grant session is invalid")
	}
	if !validation.Action.Valid() || claims.Action != validation.Action {
		return errors.New("telemetry grant action is invalid")
	}
	if claims.ActingOrganizationID != validation.ActingOrganizationID {
		return errors.New("telemetry grant acting organization is invalid")
	}
	expectedDigest, err := ScopeDigest(validation.Action, validation.ActingOrganizationID, validation.Targets)
	if err != nil || claims.ScopeDigest != expectedDigest {
		return errors.New("telemetry grant scope is invalid")
	}
	canonical, _ := CanonicalTargets(validation.Targets)
	keyCount := 0
	for _, target := range canonical {
		keyCount += len(target.Keys)
	}
	if claims.TargetCount != len(canonical) || claims.KeyCount != keyCount {
		return errors.New("telemetry grant scope counts are invalid")
	}
	if validation.UseChecker == nil {
		return errors.New("telemetry grant use check is required")
	}
	status, err := validation.UseChecker(claims)
	if err != nil {
		return errors.New("telemetry grant use check failed")
	}
	if status.CurrentPolicyRevision == "" || claims.PolicyRevision != status.CurrentPolicyRevision {
		return errors.New("telemetry grant policy revision is stale")
	}
	if status.Revoked {
		return errors.New("telemetry grant is revoked")
	}
	if status.Replayed {
		return errors.New("telemetry grant was replayed")
	}
	return nil
}
