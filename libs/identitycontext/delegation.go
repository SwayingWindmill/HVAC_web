package identitycontext

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
	DisplayName          string   `json:"displayName"`
	Email                string   `json:"email"`
	Roles                []string `json:"roles"`
	ExecutingService     string   `json:"executingService"`
	Audience             string   `json:"audience"`
	ActingOrganizationID string   `json:"actingOrganizationId"`
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
	Principal UserPrincipal    `json:"principal"`
	Context   PrincipalContext `json:"context"`
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
	if claims.ExecutingService != executingService || claims.Issuer != executingService {
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
	if !containsExact(claims.Scopes, scope) || len(claims.Scopes) != 1 {
		return errors.New("delegation scope is invalid")
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
