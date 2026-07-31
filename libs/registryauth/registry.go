package registryauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	GrantStatusPath         = "/internal/v1/registry-read/grant-status"
	MaximumGrantTokenIDSize = 256
)

type GrantStatusRequest struct {
	ActingOrganizationID string `json:"actingOrganizationId"`
	TokenID              string `json:"tokenId"`
}

func (request GrantStatusRequest) Validate() error {
	if !validUUIDv7(request.ActingOrganizationID) {
		return errors.New("acting organization must be a UUIDv7")
	}
	if len(request.TokenID) == 0 || len(request.TokenID) > MaximumGrantTokenIDSize {
		return errors.New("grant token identifier is invalid")
	}
	return nil
}

type GrantStatus struct {
	CurrentPolicyRevision string `json:"currentPolicyRevision"`
	Revoked               bool   `json:"revoked"`
}

type Action string

const (
	ActionRegistryRead      Action = "registry.read"
	ActionOrganizationList  Action = "organization.list"
	ActionOrganizationRead  Action = "organization.read"
	ActionSiteList          Action = "site.list"
	ActionSiteRead          Action = "site.read"
	ActionEquipmentList     Action = "equipment.list"
	ActionEquipmentRead     Action = "equipment.read"
	ActionDeviceList        Action = "device.list"
	ActionDeviceRead        Action = "device.read"
	ActionDeviceBindingList Action = "device-binding.list"
)

func (action Action) Valid() bool {
	switch action {
	case ActionRegistryRead,
		ActionOrganizationList,
		ActionOrganizationRead,
		ActionSiteList,
		ActionSiteRead,
		ActionEquipmentList,
		ActionEquipmentRead,
		ActionDeviceList,
		ActionDeviceRead,
		ActionDeviceBindingList:
		return true
	default:
		return false
	}
}

func (action Action) SiteScoped() bool {
	switch action {
	case ActionSiteList, ActionSiteRead, ActionEquipmentList, ActionEquipmentRead, ActionDeviceList, ActionDeviceRead, ActionDeviceBindingList:
		return true
	default:
		return false
	}
}

func ActionAllows(granted, requested Action) bool {
	return granted == ActionRegistryRead || granted == requested
}

type DecisionRequest struct {
	ActingOrganizationID string `json:"actingOrganizationId"`
	Action               Action `json:"action"`
}

func (request DecisionRequest) Validate() error {
	if !validUUIDv7(request.ActingOrganizationID) {
		return errors.New("acting organization must be a UUIDv7")
	}
	if !request.Action.Valid() || request.Action == ActionRegistryRead {
		return errors.New("a concrete registry read action is required")
	}
	return nil
}

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

type ReasonCode string

const (
	ReasonAllowOrganizationRole  ReasonCode = "ALLOW_ORGANIZATION_ROLE"
	ReasonAllowSiteRole          ReasonCode = "ALLOW_SITE_ROLE"
	ReasonAllowSiteBinding       ReasonCode = "ALLOW_SITE_BINDING"
	ReasonDenyExplicit           ReasonCode = "DENY_EXPLICIT"
	ReasonDenyPrincipalNotFound  ReasonCode = "DENY_PRINCIPAL_NOT_FOUND"
	ReasonDenyPrincipalInactive  ReasonCode = "DENY_PRINCIPAL_INACTIVE"
	ReasonDenyMembershipRequired ReasonCode = "DENY_ACTING_ORGANIZATION_MEMBERSHIP_REQUIRED"
	ReasonDenyMembershipRevoked  ReasonCode = "DENY_ACTING_ORGANIZATION_MEMBERSHIP_REVOKED"
	ReasonDenyActionNotGranted   ReasonCode = "DENY_ACTION_NOT_GRANTED"
)

func IsAllowReason(reason ReasonCode) bool {
	switch reason {
	case ReasonAllowOrganizationRole, ReasonAllowSiteRole, ReasonAllowSiteBinding:
		return true
	default:
		return false
	}
}

type Decision struct {
	Allowed                bool       `json:"allowed"`
	PrincipalID            string     `json:"principalId"`
	SubjectIssuer          string     `json:"subjectIssuer"`
	Subject                string     `json:"subject"`
	ActingOrganizationID   string     `json:"actingOrganizationId"`
	AllowedOrganizationIDs []string   `json:"allowedOrganizationIds"`
	AllowedSiteIDs         []string   `json:"allowedSiteIds"`
	DeniedOrganizationIDs  []string   `json:"deniedOrganizationIds"`
	DeniedSiteIDs          []string   `json:"deniedSiteIds"`
	Actions                []Action   `json:"actions"`
	PolicyRevision         string     `json:"policyRevision"`
	ReasonCode             ReasonCode `json:"reasonCode"`
	DecidedAt              string     `json:"decidedAt"`
}

type DecisionResponse struct {
	Decision        Decision `json:"decision"`
	DelegationGrant string   `json:"delegationGrant,omitempty"`
}

type GrantClaims struct {
	Version                int        `json:"version"`
	Issuer                 string     `json:"issuer"`
	Presenter              string     `json:"presenter"`
	Audience               string     `json:"audience"`
	PrincipalID            string     `json:"principalId"`
	SubjectIssuer          string     `json:"subjectIssuer"`
	Subject                string     `json:"subject"`
	ActingOrganizationID   string     `json:"actingOrganizationId"`
	AllowedOrganizationIDs []string   `json:"allowedOrganizationIds"`
	AllowedSiteIDs         []string   `json:"allowedSiteIds"`
	DeniedOrganizationIDs  []string   `json:"deniedOrganizationIds"`
	DeniedSiteIDs          []string   `json:"deniedSiteIds"`
	Actions                []Action   `json:"actions"`
	PolicyRevision         string     `json:"policyRevision"`
	DecisionReason         ReasonCode `json:"decisionReason"`
	SessionID              string     `json:"sessionId"`
	ParentTokenID          string     `json:"parentTokenId"`
	IssuedAt               int64      `json:"issuedAt"`
	ExpiresAt              int64      `json:"expiresAt"`
	TokenID                string     `json:"tokenId"`
	Transitive             bool       `json:"transitive"`
}

type RevocationChecker func(tokenID string) (bool, error)

type GrantValidation struct {
	Now                   time.Time
	Issuer                string
	Presenter             string
	Audience              string
	Action                Action
	CurrentPolicyRevision string
	IsRevoked             RevocationChecker
}

func SignGrant(signer crypto.Signer, claims GrantClaims) (string, error) {
	if signer == nil {
		return "", errors.New("registry grant signer is required")
	}
	claims.Version = GrantVersion
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal registry grant claims: %w", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sign registry grant claims: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyGrant(publicKey crypto.PublicKey, token string) (GrantClaims, error) {
	if len(token) == 0 || len(token) > MaximumEncodedGrantSize {
		return GrantClaims{}, errors.New("registry grant size is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return GrantClaims{}, errors.New("registry grant format is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return GrantClaims{}, errors.New("registry grant payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return GrantClaims{}, errors.New("registry grant signature encoding is invalid")
	}
	digest := sha256.Sum256(payload)
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			err = errors.New("ecdsa registry grant signature is invalid")
		}
	default:
		err = fmt.Errorf("unsupported registry grant public key %T", publicKey)
	}
	if err != nil {
		return GrantClaims{}, errors.New("registry grant signature is invalid")
	}
	var claims GrantClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return GrantClaims{}, errors.New("registry grant payload is invalid")
	}
	if claims.Version != GrantVersion {
		return GrantClaims{}, errors.New("registry grant version is unsupported")
	}
	return claims, nil
}

func ValidateGrant(claims GrantClaims, validation GrantValidation) error {
	if validation.Now.IsZero() {
		return errors.New("registry grant validation time is required")
	}
	if claims.Issuer != validation.Issuer || strings.TrimSpace(validation.Issuer) == "" {
		return errors.New("registry grant issuer is invalid")
	}
	if claims.Presenter != validation.Presenter || strings.TrimSpace(validation.Presenter) == "" {
		return errors.New("registry grant presenter is invalid")
	}
	if claims.Audience != validation.Audience || strings.TrimSpace(validation.Audience) == "" {
		return errors.New("registry grant audience is invalid")
	}
	if claims.Transitive {
		return errors.New("registry grant is transitive")
	}
	if claims.IssuedAt > validation.Now.Add(5*time.Second).Unix() {
		return errors.New("registry grant is not active")
	}
	if claims.ExpiresAt <= validation.Now.Unix() || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > MaximumGrantLifetime {
		return errors.New("registry grant is expired or too long-lived")
	}
	if claims.PrincipalID == "" || claims.SubjectIssuer == "" || claims.Subject == "" || claims.ActingOrganizationID == "" || claims.SessionID == "" || claims.ParentTokenID == "" || claims.TokenID == "" {
		return errors.New("registry grant identity fields are incomplete")
	}
	if len(claims.TokenID) > MaximumGrantTokenIDSize {
		return errors.New("registry grant token identifier is too large")
	}
	if validation.CurrentPolicyRevision == "" || claims.PolicyRevision != validation.CurrentPolicyRevision {
		return errors.New("registry grant policy revision is stale")
	}
	if !validation.Action.Valid() || validation.Action == ActionRegistryRead || len(claims.Actions) != 1 || claims.Actions[0] != validation.Action {
		return errors.New("registry grant action is invalid")
	}
	if !IsAllowReason(claims.DecisionReason) {
		return errors.New("registry grant decision reason is invalid")
	}
	if err := validateScope(claims); err != nil {
		return err
	}
	if validation.IsRevoked == nil {
		return errors.New("registry grant revocation check is required")
	}
	revoked, err := validation.IsRevoked(claims.TokenID)
	if err != nil {
		return errors.New("registry grant revocation check failed")
	}
	if revoked {
		return errors.New("registry grant is revoked")
	}
	return nil
}

func ScopeAllows(claims GrantClaims, owningOrganizationID, siteID string) bool {
	if owningOrganizationID == "" {
		return false
	}
	if contains(claims.DeniedOrganizationIDs, owningOrganizationID) {
		return false
	}
	if siteID != "" && contains(claims.DeniedSiteIDs, siteID) {
		return false
	}
	if contains(claims.AllowedOrganizationIDs, owningOrganizationID) {
		return true
	}
	return siteID != "" && contains(claims.AllowedSiteIDs, siteID)
}

func validateScope(claims GrantClaims) error {
	for _, values := range [][]string{claims.AllowedOrganizationIDs, claims.AllowedSiteIDs, claims.DeniedOrganizationIDs, claims.DeniedSiteIDs} {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("registry grant scope contains an empty identifier")
			}
			if _, exists := seen[value]; exists {
				return errors.New("registry grant scope contains duplicate identifiers")
			}
			seen[value] = struct{}{}
		}
	}
	for _, value := range claims.AllowedOrganizationIDs {
		if contains(claims.DeniedOrganizationIDs, value) {
			return errors.New("registry grant organization scope is contradictory")
		}
	}
	for _, value := range claims.AllowedSiteIDs {
		if contains(claims.DeniedSiteIDs, value) {
			return errors.New("registry grant site scope is contradictory")
		}
	}
	if len(claims.AllowedOrganizationIDs) == 0 && len(claims.AllowedSiteIDs) == 0 {
		return errors.New("registry grant contains no allowed scope")
	}
	return nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
