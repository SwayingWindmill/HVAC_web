package commandauth

import (
	"crypto"
	"errors"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/grant"
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
	TenantID                    string                            `json:"tenantId"`
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
	TenantID           string
	SiteID             string
	DeviceID           string
	Capability         commandmodel.Capability
	CapabilityRevision string
	Risk               commandmodel.RiskLevel
	UseChecker         func(GrantClaims) (UseStatus, error)
}

var grantFormat = grant.Format{Name: "command grant", MaxEncodedSize: MaximumEncodedGrantSize}

func SignGrant(signer crypto.Signer, claims GrantClaims) (string, error) {
	claims.Version = GrantVersion
	return grant.Sign(grantFormat, signer, claims)
}

func VerifyGrant(publicKey crypto.PublicKey, token string) (GrantClaims, error) {
	claims, err := grant.Verify[GrantClaims](grantFormat, publicKey, token)
	if err != nil {
		return GrantClaims{}, err
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
		TenantID:                    claims.TenantID,
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
