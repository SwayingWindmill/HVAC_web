package commandservice

import (
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestSubmitRejectsExpiredOrScopeDriftedAuthorization(t *testing.T) {
	service := New(fixedClock())
	request := validRequest()
	request.Authorization.ExpiresAt = fixedClock()().Add(-time.Second)
	if _, err := service.Submit(request); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expired authorization err=%v", err)
	}
	request = validRequest()
	request.Authorization.DeviceID = "device-other"
	if _, err := service.Submit(request); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("scope drift authorization err=%v", err)
	}
}

func TestMediumRiskRequiresBoundIndependentApproval(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 24.5
	request.IdempotencyKey = "medium-risk"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Intent.Risk != commandmodel.RiskMedium || created.Intent.ApprovalPolicy != commandmodel.ApprovalSingleApprover || created.Intent.Status != commandmodel.IntentAwaitingApproval {
		t.Fatalf("governance result=%#v", created.Intent)
	}
	if _, err := service.PrepareDispatch(created.Intent.ID, "dispatcher-a", clock().Add(time.Minute)); !errors.Is(err, ErrCommandNotDispatchable) {
		t.Fatalf("unapproved command became dispatchable: %v", err)
	}

	approval := commandmodel.ApprovalEvidence{
		ApprovalID: "approval-1", ApproverID: "principal-2", ApproverRole: "SITE_OPERATOR",
		Policy: commandmodel.ApprovalSingleApprover, PayloadHash: created.Intent.PayloadHash,
		CapabilityRevision: created.Intent.CapabilityRevision, Risk: created.Intent.Risk,
		RiskRuleRevision: created.Intent.RiskSnapshot.RuleRevision,
		Authorization:    freshApprovalAuthorization(created.Intent, "principal-2", clock(), "approval-grant-1"),
		IssuedAt:         clock(), ExpiresAt: clock().Add(10 * time.Minute),
	}
	selfApproval := approval
	selfApproval.ApproverID = created.Intent.PrincipalID
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: selfApproval}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("self approval err=%v", err)
	}
	wrongBinding := approval
	wrongBinding.PayloadHash = "wrong"
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: wrongBinding}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("wrong binding approval err=%v", err)
	}
	wrongPurpose := approval
	wrongPurpose.Authorization.Purpose = commandmodel.AuthorizationCommandSubmit
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: wrongPurpose}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("submit-purpose approval err=%v", err)
	}
	wrongPrincipal := approval
	wrongPrincipal.Authorization.PrincipalID = "principal-other"
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: wrongPrincipal}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("approver principal mismatch err=%v", err)
	}
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: approval})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != commandmodel.IntentQueued || len(approved.Approvals) != 1 {
		t.Fatalf("approved intent=%#v", approved)
	}
}

func TestFreshApprovalAuthorizationReplacesExpiredSubmitAuthorization(t *testing.T) {
	clockNow := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return clockNow }
	service := New(clock)
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 24.5
	request.IdempotencyKey = "fresh-approval-auth"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	clockNow = clockNow.Add(26 * time.Second)
	approval := commandmodel.ApprovalEvidence{
		ApprovalID: "approval-fresh", ApproverID: "principal-2", ApproverRole: "SITE_OPERATOR",
		Policy: commandmodel.ApprovalSingleApprover, PayloadHash: created.Intent.PayloadHash,
		CapabilityRevision: created.Intent.CapabilityRevision, Risk: created.Intent.Risk,
		RiskRuleRevision: created.Intent.RiskSnapshot.RuleRevision,
		Authorization:    freshApprovalAuthorization(created.Intent, "principal-2", clockNow, "approval-grant-fresh"),
		IssuedAt:         clockNow, ExpiresAt: clockNow.Add(10 * time.Minute),
	}
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: approval})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Authorization.GrantID != "approval-grant-fresh" || len(approved.Authorizations) != 2 {
		t.Fatalf("authorization history=%#v current=%#v", approved.Authorizations, approved.Authorization)
	}
	if _, err := service.PrepareDispatch(approved.ID, "dispatcher-a", clockNow.Add(time.Minute)); err != nil {
		t.Fatalf("fresh approval authorization did not permit dispatch: %v", err)
	}
}

func TestHighRiskRequiresTwoDistinctApprovers(t *testing.T) {
	clock := fixedClock()
	service := New(clock)
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 25.5
	request.IdempotencyKey = "high-risk"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Intent.Risk != commandmodel.RiskHigh || created.Intent.ApprovalPolicy != commandmodel.ApprovalTwoPerson {
		t.Fatalf("risk/policy=%s/%s", created.Intent.Risk, created.Intent.ApprovalPolicy)
	}
	approval := func(id, approver string) commandmodel.ApprovalEvidence {
		return commandmodel.ApprovalEvidence{
			ApprovalID: id, ApproverID: approver, ApproverRole: "CONTROL_APPROVER",
			Policy: commandmodel.ApprovalTwoPerson, PayloadHash: created.Intent.PayloadHash,
			CapabilityRevision: created.Intent.CapabilityRevision, Risk: created.Intent.Risk,
			RiskRuleRevision: created.Intent.RiskSnapshot.RuleRevision,
			Authorization:    freshApprovalAuthorization(created.Intent, approver, clock(), "approval-grant-"+id),
			IssuedAt:         clock(), ExpiresAt: clock().Add(10 * time.Minute),
		}
	}
	partial, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: approval("approval-a", "principal-2")})
	if !errors.Is(err, ErrApprovalRequired) || partial.Status != commandmodel.IntentAwaitingApproval || len(partial.Approvals) != 1 {
		t.Fatalf("first approval result=%#v err=%v", partial, err)
	}
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: approval("approval-b", "principal-3")})
	if err != nil || approved.Status != commandmodel.IntentQueued || len(approved.Approvals) != 2 {
		t.Fatalf("two-person approval result=%#v err=%v", approved, err)
	}
}

func TestExecutionRejectsExpiredEarlierApprovalAuthorization(t *testing.T) {
	clockNow := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return clockNow }
	service := New(clock)
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 25.5
	request.IdempotencyKey = "expired-earlier-approval"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	approval := func(id, approver string) commandmodel.ApprovalEvidence {
		return commandmodel.ApprovalEvidence{
			ApprovalID: id, ApproverID: approver, ApproverRole: "CONTROL_APPROVER",
			Policy: commandmodel.ApprovalTwoPerson, PayloadHash: created.Intent.PayloadHash,
			CapabilityRevision: created.Intent.CapabilityRevision, Risk: created.Intent.Risk,
			RiskRuleRevision: created.Intent.RiskSnapshot.RuleRevision,
			Authorization:    freshApprovalAuthorization(created.Intent, approver, clockNow, "grant-"+id),
			IssuedAt:         clockNow, ExpiresAt: clockNow.Add(10 * time.Minute),
		}
	}
	first := approval("approval-expiring", "principal-2")
	first.Authorization.ExpiresAt = clockNow.Add(5 * time.Second)
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: first}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("first approval err=%v", err)
	}
	clockNow = clockNow.Add(2 * time.Second)
	second := approval("approval-current", "principal-3")
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: created.Intent.TenantID, CommandID: created.Intent.ID, Approval: second})
	if err != nil || approved.Status != commandmodel.IntentQueued {
		t.Fatalf("second approval result=%#v err=%v", approved, err)
	}
	clockNow = clockNow.Add(4 * time.Second)
	if _, err := service.PrepareDispatch(approved.ID, "dispatcher-a", clockNow.Add(time.Minute)); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expired earlier approval authorization err=%v", err)
	}
}

func freshApprovalAuthorization(intent commandmodel.CommandIntent, approverID string, now time.Time, grantID string) commandmodel.AuthorizationSnapshot {
	return commandmodel.AuthorizationSnapshot{
		GrantID: grantID, PolicyRevision: "command-policy-approval-1", Purpose: commandmodel.AuthorizationCommandApprove,
		PrincipalID: approverID, TenantID: intent.TenantID, SiteID: intent.SiteID, DeviceID: intent.DeviceID,
		Capability: intent.Capability, MaximumRisk: intent.Risk, CapabilityRevision: intent.CapabilityRevision,
		EmergencyRevocationRevision: 2, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(25 * time.Second),
	}
}
