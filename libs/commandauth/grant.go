package commandauth

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

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	GrantVersion            = 1
	MaximumGrantLifetime    = 30 * time.Second
	MaximumEncodedGrantSize = 32 << 10
)

type GrantClaims struct {
	Version                     int                               `json:"version"`
	Issuer                      string                            `json:"issuer"`
	Presenter                   string                            `json:"presenter"`
	Audience                    string                            `json:"audience"`
	GrantID                     string                            `json:"grantId"`
	Purpose                     commandmodel.AuthorizationPurpose `json:"purpose"`
	PrincipalID                 string                            `json:"principalId"`
	TenantID              string                            `json:"tenantId"`
	SiteID                      string                            `json:"siteId"`
	DeviceID                    string                            `json:"deviceId"`
	Capability                  commandmodel.Capability           `json:"capability"`
	MaximumRisk                 commandmodel.RiskLevel            `json:"maximumRisk"`
	CapabilityRevision          string                            `json:"capabilityRevision"`
	PolicyRevision              string                            `json:"policyRevision"`
	EmergencyRevocationRevision uint64                            `json:"emergencyRevocationRevision"`
	IssuedAt                    int64                             `json:"issuedAt"`
	ExpiresAt                   int64                             `json:"expiresAt"`
	TokenID                     string                            `json:"tokenId"`
	Transitive                  bool                              `json:"transitive"`
}

type UseStatus struct {
	CurrentPolicyRevision     string
	CurrentRevocationRevision uint64
	Revoked                   bool
	Replayed                  bool
}

type Validation struct {
	Now                time.Time
	Issuer             string
	Presenter          string
	Audience           string
	Purpose            commandmodel.AuthorizationPurpose
	PrincipalID        string
	TenantID     string
	SiteID             string
	DeviceID           string
	Capability         commandmodel.Capability
	CapabilityRevision string
	Risk               commandmodel.RiskLevel
	UseChecker         func(GrantClaims) (UseStatus, error)
}

func SignGrant(signer crypto.Signer, claims GrantClaims) (string, error) {
	if signer == nil {
		return "", errors.New("command grant signer is required")
	}
	claims.Version = GrantVersion
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal command grant: %w", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sign command grant: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyGrant(publicKey crypto.PublicKey, token string) (GrantClaims, error) {
	if len(token) == 0 || len(token) > MaximumEncodedGrantSize {
		return GrantClaims{}, errors.New("command grant size is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return GrantClaims{}, errors.New("command grant format is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return GrantClaims{}, errors.New("command grant payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return GrantClaims{}, errors.New("command grant signature encoding is invalid")
	}
	digest := sha256.Sum256(payload)
	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			err = errors.New("ecdsa command grant signature is invalid")
		}
	default:
		err = fmt.Errorf("unsupported command grant public key %T", publicKey)
	}
	if err != nil {
		return GrantClaims{}, errors.New("command grant signature is invalid")
	}
	var claims GrantClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return GrantClaims{}, errors.New("command grant payload is invalid")
	}
	if claims.Version != GrantVersion {
		return GrantClaims{}, errors.New("command grant version is unsupported")
	}
	return claims, nil
}

func ValidateGrant(claims GrantClaims, validation Validation) error {
	if validation.Now.IsZero() {
		return errors.New("command grant validation time is required")
	}
	if claims.Issuer != validation.Issuer || validation.Issuer == "" || claims.Presenter != validation.Presenter || validation.Presenter == "" || claims.Audience != validation.Audience || validation.Audience == "" {
		return errors.New("command grant trust chain is invalid")
	}
	if claims.Transitive {
		return errors.New("command grant is transitive")
	}
	if claims.IssuedAt > validation.Now.Add(5*time.Second).Unix() || claims.ExpiresAt <= validation.Now.Unix() || claims.ExpiresAt <= claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > MaximumGrantLifetime {
		return errors.New("command grant is expired or too long-lived")
	}
	if claims.GrantID == "" || claims.TokenID == "" || claims.PolicyRevision == "" || claims.CapabilityRevision == "" || claims.Purpose == "" {
		return errors.New("command grant metadata is incomplete")
	}
	if claims.Purpose != validation.Purpose || validation.Purpose == "" || claims.PrincipalID != validation.PrincipalID || claims.TenantID != validation.TenantID || claims.SiteID != validation.SiteID || claims.DeviceID != validation.DeviceID || claims.Capability != validation.Capability || claims.CapabilityRevision != validation.CapabilityRevision {
		return errors.New("command grant scope is invalid")
	}
	if RiskOrdinal(validation.Risk) > RiskOrdinal(claims.MaximumRisk) {
		return errors.New("command grant risk ceiling is exceeded")
	}
	if validation.UseChecker == nil {
		return errors.New("command grant use check is required")
	}
	status, err := validation.UseChecker(claims)
	if err != nil {
		return errors.New("command grant use check failed")
	}
	if status.CurrentPolicyRevision == "" || status.CurrentPolicyRevision != claims.PolicyRevision {
		return errors.New("command grant policy revision is stale")
	}
	if status.CurrentRevocationRevision != claims.EmergencyRevocationRevision {
		return errors.New("command grant emergency revocation revision is stale")
	}
	if status.Revoked || status.Replayed {
		return errors.New("command grant is revoked or replayed")
	}
	return nil
}

func Snapshot(claims GrantClaims) commandmodel.AuthorizationSnapshot {
	return commandmodel.AuthorizationSnapshot{
		GrantID:                     claims.GrantID,
		PolicyRevision:              claims.PolicyRevision,
		Purpose:                     claims.Purpose,
		PrincipalID:                 claims.PrincipalID,
		TenantID:              claims.TenantID,
		SiteID:                      claims.SiteID,
		DeviceID:                    claims.DeviceID,
		Capability:                  claims.Capability,
		MaximumRisk:                 claims.MaximumRisk,
		CapabilityRevision:          claims.CapabilityRevision,
		EmergencyRevocationRevision: claims.EmergencyRevocationRevision,
		IssuedAt:                    time.Unix(claims.IssuedAt, 0).UTC(),
		ExpiresAt:                   time.Unix(claims.ExpiresAt, 0).UTC(),
	}
}

func RiskOrdinal(value commandmodel.RiskLevel) int {
	switch value {
	case commandmodel.RiskLow:
		return 1
	case commandmodel.RiskMedium:
		return 2
	case commandmodel.RiskHigh:
		return 3
	case commandmodel.RiskCritical:
		return 4
	default:
		return 99
	}
}
