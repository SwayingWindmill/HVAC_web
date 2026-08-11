package identitycontext

import (
	"context"
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

const DelegationVersion = 1

type UserPrincipal struct {
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles"`
}

type ServicePrincipal struct {
	Service  string `json:"service"`
	SPIFFEID string `json:"spiffeId"`
}

type DelegationClaims struct {
	Version              int      `json:"version"`
	Issuer               string   `json:"issuer"`
	Subject              string   `json:"subject"`
	SubjectIssuer        string   `json:"subjectIssuer"`
	PrincipalID          string   `json:"principalId,omitempty"`
	DisplayName          string   `json:"displayName"`
	Email                string   `json:"email"`
	Roles                []string `json:"roles"`
	ExecutingService     string   `json:"executingService"`
	Audience             string   `json:"audience"`
	ActingOrganizationID string   `json:"actingOrganizationId"`
	TenantID             string   `json:"tenantId,omitempty"`
	Actions              []string `json:"actions"`
	Scopes               []string `json:"scopes"`
	PolicyRevision       string   `json:"policyRevision"`
	SessionID            string   `json:"sessionId"`
	IssuedAt             int64    `json:"issuedAt"`
	ExpiresAt            int64    `json:"expiresAt"`
	TokenID              string   `json:"tokenId"`
}

type PrincipalContext struct {
	InitiatingPrincipal       UserPrincipal    `json:"initiatingPrincipal"`
	ExecutingServicePrincipal ServicePrincipal `json:"executingServicePrincipal"`
	ActingOrganizationID      string           `json:"actingOrganizationId"`
	Audience                  string           `json:"audience"`
	PolicyRevision            string           `json:"policyRevision"`
	DelegationExpiresAt       string           `json:"delegationExpiresAt"`
}

type InternalPrincipalResponse struct {
	Principal     UserPrincipal          `json:"principal"`
	Context       PrincipalContext       `json:"context"`
	Authorization EffectiveAuthorization `json:"authorization"`
}

type tenantContextKey struct{}

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, strings.TrimSpace(tenantID))
}

func TenantIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	tenantID, ok := ctx.Value(tenantContextKey{}).(string)
	tenantID = strings.TrimSpace(tenantID)
	return tenantID, ok && tenantID != ""
}

func (response InternalPrincipalResponse) Validate() error {
	if strings.TrimSpace(response.Principal.Subject) == "" || strings.TrimSpace(response.Principal.Issuer) == "" {
		return errors.New("principal identity is required")
	}
	if strings.TrimSpace(response.Context.ActingOrganizationID) == "" || strings.TrimSpace(response.Context.PolicyRevision) == "" {
		return errors.New("principal context is incomplete")
	}
	return response.Authorization.Validate()
}

func SignDelegation(signer crypto.Signer, claims DelegationClaims) (string, error) {
	if signer == nil {
		return "", errors.New("delegation signer is required")
	}
	claims.Version = DelegationVersion
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal delegation claims: %w", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sign delegation claims: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyDelegation(publicKey crypto.PublicKey, token string) (DelegationClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return DelegationClaims{}, errors.New("delegation grant format is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return DelegationClaims{}, errors.New("delegation payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return DelegationClaims{}, errors.New("delegation signature encoding is invalid")
	}
	digest := sha256.Sum256(payload)
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			err = errors.New("ecdsa delegation signature is invalid")
		}
	default:
		err = fmt.Errorf("unsupported delegation public key %T", publicKey)
	}
	if err != nil {
		return DelegationClaims{}, errors.New("delegation signature is invalid")
	}
	var claims DelegationClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return DelegationClaims{}, errors.New("delegation payload is invalid")
	}
	if claims.Version != DelegationVersion {
		return DelegationClaims{}, errors.New("delegation version is unsupported")
	}
	return claims, nil
}

func ValidateDelegation(claims DelegationClaims, now time.Time, executingService, audience, action, scope string) error {
	return ValidateDelegationFromIssuer(
		claims,
		now,
		executingService,
		executingService,
		audience,
		action,
		scope,
	)
}

func ValidateDelegationFromIssuer(
	claims DelegationClaims,
	now time.Time,
	issuer,
	executingService,
	audience,
	action,
	scope string,
) error {
	if len(claims.Scopes) != 1 {
		return errors.New("delegation scope is invalid")
	}
	return ValidateDelegationFromIssuerAnyScope(
		claims,
		now,
		issuer,
		executingService,
		audience,
		action,
		[]string{scope},
	)
}

func ValidateDelegationAnyScope(claims DelegationClaims, now time.Time, executingService, audience, action string, acceptableScopes []string) error {
	return ValidateDelegationFromIssuerAnyScope(
		claims,
		now,
		executingService,
		executingService,
		audience,
		action,
		acceptableScopes,
	)
}

func ValidateDelegationFromIssuerAnyScope(
	claims DelegationClaims,
	now time.Time,
	issuer,
	executingService,
	audience,
	action string,
	acceptableScopes []string,
) error {
	if claims.Issuer != issuer {
		return errors.New("delegation issuer is invalid")
	}
	if claims.ExecutingService != executingService {
		return errors.New("delegation executing service is invalid")
	}
	if claims.Audience != audience {
		return errors.New("delegation audience is invalid")
	}
	if claims.IssuedAt > now.Add(5*time.Second).Unix() {
		return errors.New("delegation is not active")
	}
	if claims.ExpiresAt <= now.Unix() || claims.ExpiresAt-claims.IssuedAt > 60 {
		return errors.New("delegation is expired or too long-lived")
	}
	if claims.Subject == "" || claims.SubjectIssuer == "" || claims.SessionID == "" || claims.TokenID == "" || claims.PolicyRevision == "" {
		return errors.New("delegation identity fields are incomplete")
	}
	if !containsExact(claims.Actions, action) || len(claims.Actions) != 1 {
		return errors.New("delegation action is invalid")
	}
	if len(claims.Scopes) == 0 || len(claims.Scopes) > 256 || len(acceptableScopes) == 0 {
		return errors.New("delegation scope is invalid")
	}
	seen := make(map[string]struct{}, len(claims.Scopes))
	for _, scope := range claims.Scopes {
		if scope == "" || !containsExact(acceptableScopes, scope) {
			return errors.New("delegation scope is invalid")
		}
		if _, duplicate := seen[scope]; duplicate {
			return errors.New("delegation scope is invalid")
		}
		seen[scope] = struct{}{}
	}
	return nil
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
