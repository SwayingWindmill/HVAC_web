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
	ErrCommandTTLExpired   = errors.New("command TTL expired")
)

func evaluateGovernance(request commandmodel.SubmitRequest, now time.Time) (commandmodel.RiskSnapshot, commandmodel.ApprovalPolicy, error) {
	level, reasons, ok := commandmodel.EvaluateRiskLevel(request.Capability, request.Parameters, request.CurrentState.CurrentValue)
	if !ok {
		priorityTier, commandSource, err := commandmodel.ResolveCommandPriority(request)
		preconditionMode, bypassReason := normalizedPrecondition(request)
		deliveryMode := commandmodel.NormalizeDeliveryMode(request.DeliveryMode)
		if err == nil {
			if (preconditionMode == commandmodel.PreconditionEmergencyBypass &&
				request.CurrentState.CurrentValue == nil &&
				commandmodel.AllowsEmergencyPreconditionBypass(request.Capability, priorityTier, commandSource, request.IdempotencyKey, bypassReason)) ||
				(priorityTier == commandmodel.PriorityProtectionRule &&
					commandSource == commandmodel.SourceProtectionRule &&
					request.Capability == commandmodel.CapabilitySetChilledWaterTemperatureSetpoint &&
					strings.HasPrefix(request.IdempotencyKey, "auto-rollback:")) {
				return evaluateBypassFallbackGovernance(request, now, "EMERGENCY_PRECONDITION_BYPASS")
			}
			if deliveryMode == commandmodel.DeliveryModeQueueIfOffline &&
				request.CurrentState.CurrentValue == nil &&
				commandmodel.CapabilityAllowsOfflineQueue(request.Capability) &&
				commandmodel.ParametersValid(request.Capability, request.Parameters) {
				return evaluateBypassFallbackGovernance(request, now, "OFFLINE_QUEUE_DEFERRED_PRECONDITION")
			}
		}
		return commandmodel.RiskSnapshot{}, "", ErrCapabilityDenied
	}
	return finalizeRiskGovernance(request, commandmodel.RiskSnapshot{
		Level:        level,
		RuleRevision: riskRuleRevision,
		Reasons:      reasons,
		EvaluatedAt:  now,
	}, now)
}

func evaluateBypassFallbackGovernance(request commandmodel.SubmitRequest, now time.Time, reasonCode string) (commandmodel.RiskSnapshot, commandmodel.ApprovalPolicy, error) {
	profile, ok := commandmodel.CapabilityProfileFor(request.Capability)
	if !ok {
		return commandmodel.RiskSnapshot{}, "", ErrCapabilityDenied
	}
	if strings.TrimSpace(reasonCode) == "" {
		reasonCode = "EMERGENCY_PRECONDITION_BYPASS"
	}
	return finalizeRiskGovernance(request, commandmodel.RiskSnapshot{
		Level:        profile.BaseRisk,
		RuleRevision: riskRuleRevision,
		Reasons:      []string{reasonCode},
		EvaluatedAt:  now,
	}, now)
}

func finalizeRiskGovernance(request commandmodel.SubmitRequest, risk commandmodel.RiskSnapshot, now time.Time) (commandmodel.RiskSnapshot, commandmodel.ApprovalPolicy, error) {
	if err := validateAuthorizationSnapshot(request, risk.Level, now); err != nil {
		return commandmodel.RiskSnapshot{}, "", err
	}
	// Automated commands backed by a human-armed lease skip redundant medium-risk approval
	// because the operator already completed TOTP step-up when arming the lease and
	// validateAuthorizationSnapshot verifies risk.Level <= auth.MaximumRisk.
	if risk.Level != commandmodel.RiskCritical &&
		strings.TrimSpace(request.Authorization.LeaseID) != "" &&
		strings.TrimSpace(request.Authorization.WorkloadSPIFFE) != "" &&
		strings.TrimSpace(request.Authorization.ArmedBy) != "" {
		return risk, commandmodel.ApprovalNone, nil
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
	case commandmodel.RiskHigh:
		return commandmodel.ApprovalNone
	case commandmodel.RiskCritical:
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

// ApprovalWindow bounds how long a command may wait for approval after submission. Risk
// was assessed against the state at submission, and an unapproved command must not hold
// the Device's command sequence indefinitely.
const ApprovalWindow = 5 * time.Minute

func validateApproval(intent commandmodel.CommandIntent, approval commandmodel.ApprovalEvidence, now time.Time) error {
	if !now.Before(intent.CreatedAt.Add(ApprovalWindow)) {
		return ErrApprovalInvalid
	}
	if strings.TrimSpace(approval.ApprovalID) == "" || strings.TrimSpace(approval.ApproverID) == "" ||
		approval.ApproverID == intent.PrincipalID {
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
	nowUTC := now.UTC()
	if intent.ExpiresAt != nil && !nowUTC.Before(intent.ExpiresAt.UTC()) {
		return ErrCommandTTLExpired
	}
	if intent.ApprovalPolicy == commandmodel.ApprovalNone {
		authNow := nowUTC
		if commandmodel.NormalizeDeliveryMode(intent.DeliveryMode) == commandmodel.DeliveryModeQueueIfOffline &&
			intent.ExpiresAt != nil &&
			!intent.Authorization.IssuedAt.IsZero() &&
			intent.Authorization.ExpiresAt.After(intent.Authorization.IssuedAt) &&
			nowUTC.Before(intent.ExpiresAt.UTC()) {
			authNow = intent.Authorization.IssuedAt
		}
		return validateAuthorizationScope(intent.Authorization, commandmodel.AuthorizationCommandSubmit,
			intent.PrincipalID, intent.TenantID, intent.SiteID, intent.DeviceID,
			intent.Capability, intent.CapabilityRevision, intent.Risk, authNow)
	}
	if len(intent.Approvals) < requiredApprovalCount(intent.ApprovalPolicy) {
		return ErrApprovalRequired
	}
	for _, approval := range intent.Approvals {
		if approval.Policy != intent.ApprovalPolicy || approval.PayloadHash != intent.PayloadHash ||
			approval.CapabilityRevision != intent.CapabilityRevision || approval.Risk != intent.Risk ||
			approval.RiskRuleRevision != intent.RiskSnapshot.RuleRevision || !approval.ExpiresAt.After(nowUTC) {
			return ErrApprovalInvalid
		}
		if err := validateAuthorizationScope(approval.Authorization, commandmodel.AuthorizationCommandApprove,
			approval.ApproverID, intent.TenantID, intent.SiteID, intent.DeviceID,
			intent.Capability, intent.CapabilityRevision, intent.Risk, nowUTC); err != nil {
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
