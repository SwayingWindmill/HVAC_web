package commandauth

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type ReasonCode string

const (
	ReasonAllowExactCapability ReasonCode = "ALLOW_EXACT_CAPABILITY"
	ReasonAllowAutoLease       ReasonCode = "ALLOW_AUTO_LEASE"
	ReasonDenyPrincipal        ReasonCode = "DENY_PRINCIPAL"
	ReasonDenyMembership       ReasonCode = "DENY_MEMBERSHIP"
	ReasonDenyExplicit         ReasonCode = "DENY_EXPLICIT"
	ReasonDenyScope            ReasonCode = "DENY_SCOPE"
	ReasonDenyCapability       ReasonCode = "DENY_CAPABILITY"
	ReasonDenyPurpose          ReasonCode = "DENY_PURPOSE"
	ReasonDenyAutoLease        ReasonCode = "DENY_AUTO_LEASE"
	ReasonDenyControlLock      ReasonCode = "DENY_CONTROL_LOCK"
	ReasonDenyRateLimit        ReasonCode = "DENY_RATE_LIMIT"
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
	if _, supported := commandmodel.CapabilityProfileFor(request.Capability); !supported {
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
	LeaseID                     string                            `json:"leaseId,omitempty"`
	ArmedBy                     string                            `json:"armedBy,omitempty"`
	Detail                      string                            `json:"detail,omitempty"`
}

type WorkloadDecisionRequest struct {
	TenantID           string                            `json:"tenantId"`
	SiteID             string                            `json:"siteId"`
	DeviceID           string                            `json:"deviceId"`
	Capability         commandmodel.Capability           `json:"capability"`
	CapabilityRevision string                            `json:"capabilityRevision"`
	Purpose            commandmodel.AuthorizationPurpose `json:"purpose"`
	Parameters         commandmodel.CommandParameters    `json:"parameters,omitempty"`
	CurrentValue       *float64                          `json:"currentValue,omitempty"`
	IsRollback         bool                              `json:"isRollback,omitempty"`
}

func (request WorkloadDecisionRequest) Validate() error {
	for _, value := range []string{request.TenantID, request.SiteID, request.DeviceID, request.CapabilityRevision} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return errors.New("workload command decision scope is invalid")
		}
	}
	profile, supported := commandmodel.CapabilityProfileFor(request.Capability)
	if !supported || request.CapabilityRevision != profile.Revision {
		return errors.New("workload command decision capability is invalid")
	}
	if request.Purpose != commandmodel.AuthorizationCommandSubmit {
		return errors.New("workload command decision purpose must be submit")
	}
	if !commandmodel.ParametersValid(request.Capability, request.Parameters) {
		return errors.New("workload command decision parameters are invalid")
	}
	if profile.ParameterKey != "" {
		if request.CurrentValue == nil || math.IsNaN(*request.CurrentValue) || math.IsInf(*request.CurrentValue, 0) {
			return errors.New("workload command decision current value is required")
		}
	}
	return nil
}

type DecisionResponse struct {
	Decision        Decision `json:"decision"`
	DelegationGrant string   `json:"delegationGrant,omitempty"`
}

func IsAllowReason(reason ReasonCode) bool {
	return reason == ReasonAllowExactCapability || reason == ReasonAllowAutoLease
}

func FormatInstant(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
