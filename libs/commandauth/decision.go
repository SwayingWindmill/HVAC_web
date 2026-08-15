package commandauth

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type ReasonCode string

const (
	ReasonAllowExactCapability ReasonCode = "ALLOW_EXACT_CAPABILITY"
	ReasonDenyPrincipal        ReasonCode = "DENY_PRINCIPAL"
	ReasonDenyMembership       ReasonCode = "DENY_MEMBERSHIP"
	ReasonDenyExplicit         ReasonCode = "DENY_EXPLICIT"
	ReasonDenyScope            ReasonCode = "DENY_SCOPE"
	ReasonDenyCapability       ReasonCode = "DENY_CAPABILITY"
	ReasonDenyPurpose          ReasonCode = "DENY_PURPOSE"
)

type DecisionRequest struct {
	TenantID           string                            `json:"tenantId"`
	SiteID             string                            `json:"siteId"`
	DeviceID           string                            `json:"deviceId"`
	Capability         commandmodel.Capability           `json:"capability"`
	CapabilityRevision string                            `json:"capabilityRevision"`
	Purpose            commandmodel.AuthorizationPurpose `json:"purpose"`
}

func (request DecisionRequest) Validate() error {
	for _, value := range []string{request.TenantID, request.SiteID, request.DeviceID, request.CapabilityRevision} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return errors.New("command decision scope is invalid")
		}
	}
	if request.Capability != commandmodel.CapabilitySetTemperatureSetpoint {
		return errors.New("command decision capability is invalid")
	}
	if request.Purpose != commandmodel.AuthorizationCommandSubmit && request.Purpose != commandmodel.AuthorizationCommandApprove {
		return errors.New("command decision purpose is invalid")
	}
	return nil
}

type Decision struct {
	Allowed                     bool                              `json:"allowed"`
	PrincipalID                 string                            `json:"principalId"`
	SubjectIssuer               string                            `json:"subjectIssuer"`
	Subject                     string                            `json:"subject"`
	TenantID                    string                            `json:"tenantId"`
	SiteID                      string                            `json:"siteId"`
	DeviceID                    string                            `json:"deviceId"`
	Capability                  commandmodel.Capability           `json:"capability"`
	CapabilityRevision          string                            `json:"capabilityRevision"`
	Purpose                     commandmodel.AuthorizationPurpose `json:"purpose"`
	MaximumRisk                 commandmodel.RiskLevel            `json:"maximumRisk"`
	PolicyRevision              string                            `json:"policyRevision"`
	EmergencyRevocationRevision uint64                            `json:"emergencyRevocationRevision"`
	ReasonCode                  ReasonCode                        `json:"reasonCode"`
	DecidedAt                   string                            `json:"decidedAt"`
}

type DecisionResponse struct {
	Decision        Decision `json:"decision"`
	DelegationGrant string   `json:"delegationGrant,omitempty"`
}

func IsAllowReason(reason ReasonCode) bool {
	return reason == ReasonAllowExactCapability
}

func FormatInstant(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
