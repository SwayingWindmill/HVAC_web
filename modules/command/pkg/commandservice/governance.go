package commandservice

import (
	"errors"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const riskRuleRevision = "command-risk:equipment-capability:v1"

var (
	ErrAuthorizationDenied = errors.New("command authorization snapshot is invalid")
	ErrApprovalRequired    = errors.New("command approval is required")
	ErrApprovalInvalid     = errors.New("command approval evidence is invalid")
)

func evaluateGovernance(request commandmodel.SubmitRequest, now time.Time) (commandmodel.RiskSnapshot, commandmodel.ApprovalPolicy, error) {
	profile, ok := commandmodel.CapabilityProfileFor(request.Capability)
	if !ok {
		return commandmodel.RiskSnapshot{}, "", ErrCapabilityDenied
	}
	risk := commandmodel.RiskSnapshot{
		Level:        profile.BaseRisk,
		RuleRevision: riskRuleRevision,
		Reasons:      []string{"CAPABILITY_BASE_RISK"},
		EvaluatedAt:  now,
	}
	if profile.ParameterKey != "" {
		value, valid := commandmodel.ParameterValue(request.Capability, request.Parameters)
		if !valid || request.CurrentState.CurrentValue == nil {
			return commandmodel.RiskSnapshot{}, "", ErrCapabilityDenied
		}
		delta := absolute(value - *request.CurrentState.CurrentValue)
		switch {
		case delta <= profile.LowRiskDelta:
			risk.Level = commandmodel.RiskLow
			risk.Reasons = []string{"CAPABILITY_DELTA_LOW"}
		case delta <= profile.MediumRiskDelta:
			risk.Level = commandmodel.RiskMedium
			risk.Reasons = []string{"CAPABILITY_DELTA_MEDIUM"}
		case delta <= profile.MaximumDelta:
			risk.Level = commandmodel.RiskHigh
			risk.Reasons = []string{"CAPABILITY_DELTA_HIGH"}
		default:
			return commandmodel.RiskSnapshot{}, "", ErrCapabilityDenied
		}
	}

	if err := validateAuthorizationSnapshot(request, risk.Level, now); err != nil {
		return commandmodel.RiskSnapshot{}, "", err
	}
	return risk, approvalPolicyForRisk(risk.Level), nil
}

func validateAuthorizationSnapshot(request commandmodel.SubmitRequest, risk commandmodel.RiskLevel, now time.Time) error {
	capabilityRevision, _, _, ok := commandCapabilityProfile(request.Capability)
	if !ok {
		return ErrAuthorizationDenied
	}
	return validateAuthorizationScope(request.Authorization, commandmodel.AuthorizationCommandSubmit,
		request.PrincipalID, request.TenantID, request.SiteID, request.DeviceID,
		request.Capability, capabilityRevision, risk, now)
}

func validateAuthorizationScope(auth commandmodel.AuthorizationSnapshot, purpose commandmodel.AuthorizationPurpose, principalID, tenantID, siteID, deviceID string, capability commandmodel.Capability, capabilityRevision string, risk commandmodel.RiskLevel, now time.Time) error {
	if strings.TrimSpace(auth.GrantID) == "" || strings.TrimSpace(auth.PolicyRevision) == "" ||
		auth.Purpose != purpose || auth.PrincipalID != principalID || auth.TenantID != tenantID ||
		auth.SiteID != siteID || auth.DeviceID != deviceID ||
		auth.Capability != capability || auth.CapabilityRevision != capabilityRevision {
		return ErrAuthorizationDenied
	}
	if auth.IssuedAt.IsZero() || auth.ExpiresAt.IsZero() || auth.IssuedAt.After(now.Add(5*time.Second)) ||
		!auth.ExpiresAt.After(now) || !auth.ExpiresAt.After(auth.IssuedAt) || auth.ExpiresAt.Sub(auth.IssuedAt) > 30*time.Second {
		return ErrAuthorizationDenied
	}
	if riskOrdinal(risk) > riskOrdinal(auth.MaximumRisk) {
		return ErrAuthorizationDenied
	}
	return nil
}

func approvalPolicyForRisk(risk commandmodel.RiskLevel) commandmodel.ApprovalPolicy {
	switch risk {
	case commandmodel.RiskLow:
		return commandmodel.ApprovalNone
	case commandmodel.RiskMedium:
		return commandmodel.ApprovalSingleApprover
	case commandmodel.RiskHigh, commandmodel.RiskCritical:
		return commandmodel.ApprovalTwoPerson
	default:
		return commandmodel.ApprovalTwoPerson
	}
}

func requiredApprovalCount(policy commandmodel.ApprovalPolicy) int {
	switch policy {
	case commandmodel.ApprovalNone:
		return 0
	case commandmodel.ApprovalSingleApprover:
		return 1
	case commandmodel.ApprovalTwoPerson:
		return 2
	default:
		return 99
	}
}

func validateApproval(intent commandmodel.CommandIntent, approval commandmodel.ApprovalEvidence, now time.Time) error {
	if strings.TrimSpace(approval.ApprovalID) == "" || strings.TrimSpace(approval.ApproverID) == "" ||
		strings.TrimSpace(approval.ApproverRole) == "" || approval.ApproverID == intent.PrincipalID {
		return ErrApprovalInvalid
	}
	if approval.Policy != intent.ApprovalPolicy || approval.PayloadHash != intent.PayloadHash ||
		approval.CapabilityRevision != intent.CapabilityRevision || approval.Risk != intent.Risk ||
		approval.RiskRuleRevision != intent.RiskSnapshot.RuleRevision {
		return ErrApprovalInvalid
	}
	if approval.IssuedAt.IsZero() || approval.ExpiresAt.IsZero() || approval.IssuedAt.After(now.Add(5*time.Second)) ||
		!approval.ExpiresAt.After(now) || !approval.ExpiresAt.After(approval.IssuedAt) {
		return ErrApprovalInvalid
	}
	if err := validateAuthorizationScope(approval.Authorization, commandmodel.AuthorizationCommandApprove,
		approval.ApproverID, intent.TenantID, intent.SiteID, intent.DeviceID,
		intent.Capability, intent.CapabilityRevision, intent.Risk, now); err != nil {
		return ErrApprovalInvalid
	}
	for _, existing := range intent.Approvals {
		if existing.ApprovalID == approval.ApprovalID || existing.ApproverID == approval.ApproverID {
			return ErrApprovalInvalid
		}
	}
	return nil
}

func validateExecutionGovernance(intent commandmodel.CommandIntent, now time.Time) error {
	if intent.ApprovalPolicy == commandmodel.ApprovalNone {
		return validateAuthorizationScope(intent.Authorization, commandmodel.AuthorizationCommandSubmit,
			intent.PrincipalID, intent.TenantID, intent.SiteID, intent.DeviceID,
			intent.Capability, intent.CapabilityRevision, intent.Risk, now)
	}
	if len(intent.Approvals) < requiredApprovalCount(intent.ApprovalPolicy) {
		return ErrApprovalRequired
	}
	for _, approval := range intent.Approvals {
		if approval.Policy != intent.ApprovalPolicy || approval.PayloadHash != intent.PayloadHash ||
			approval.CapabilityRevision != intent.CapabilityRevision || approval.Risk != intent.Risk ||
			approval.RiskRuleRevision != intent.RiskSnapshot.RuleRevision || !approval.ExpiresAt.After(now) {
			return ErrApprovalInvalid
		}
		if err := validateAuthorizationScope(approval.Authorization, commandmodel.AuthorizationCommandApprove,
			approval.ApproverID, intent.TenantID, intent.SiteID, intent.DeviceID,
			intent.Capability, intent.CapabilityRevision, intent.Risk, now); err != nil {
			return ErrAuthorizationDenied
		}
	}
	return nil
}

func riskOrdinal(value commandmodel.RiskLevel) int {
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
