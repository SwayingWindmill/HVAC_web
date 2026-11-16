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
		ApprovalID: "approval-1", ApproverID: "principal-2",
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
		ApprovalID: "approval-fresh", ApproverID: "principal-2",
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

func TestHighRiskActuationAndDeltaSkipApprovalsAndEnforceAuthority(t *testing.T) {
	clock := fixedClock()
	service := New(clock)

	// Large setpoint delta (25.5 vs 23 => RiskHigh) and discrete actuation commands enter QUEUED directly.
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 25.5
	request.IdempotencyKey = "high-risk-delta"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Intent.Risk != commandmodel.RiskHigh || created.Intent.ApprovalPolicy != commandmodel.ApprovalNone || created.Intent.Status != commandmodel.IntentQueued {
		t.Fatalf("risk/policy/status=%s/%s/%s", created.Intent.Risk, created.Intent.ApprovalPolicy, created.Intent.Status)
	}

	for _, capability := range []commandmodel.Capability{
		commandmodel.CapabilityStart,
		commandmodel.CapabilityStop,
		commandmodel.CapabilityStartUnitSequence,
		commandmodel.CapabilityStopUnitSequence,
	} {
		profile, ok := commandmodel.CapabilityProfileFor(capability)
		if !ok {
			t.Fatalf("missing capability profile for %s", capability)
		}
		actuation := validRequest()
		actuation.IdempotencyKey = "actuation-" + string(capability)
		actuation.DeviceID = "device-" + string(capability)
		actuation.Capability = capability
		actuation.Parameters = nil
		actuation.Authorization.DeviceID = actuation.DeviceID
		actuation.Authorization.Capability = capability
		actuation.Authorization.CapabilityRevision = profile.Revision
		actuation.Authorization.MaximumRisk = commandmodel.RiskMedium
		if _, err := service.Submit(actuation); !errors.Is(err, ErrAuthorizationDenied) {
			t.Fatalf("operator-scoped grant submitted HIGH actuation %s: err=%v", capability, err)
		}
		actuation.Authorization.MaximumRisk = commandmodel.RiskHigh
		queued, err := service.Submit(actuation)
		if err != nil {
			t.Fatalf("high-risk submit %s failed: %v", capability, err)
		}
		if queued.Intent.Risk != commandmodel.RiskHigh || queued.Intent.ApprovalPolicy != commandmodel.ApprovalNone || queued.Intent.Status != commandmodel.IntentQueued {
			t.Fatalf("actuation %s risk/policy/status=%s/%s/%s", capability, queued.Intent.Risk, queued.Intent.ApprovalPolicy, queued.Intent.Status)
		}
	}
}

func TestCriticalRiskPolicyRequiresTwoDistinctApprovers(t *testing.T) {
	if got := approvalPolicyForRisk(commandmodel.RiskCritical); got != commandmodel.ApprovalTwoPerson {
		t.Fatalf("approvalPolicyForRisk(CRITICAL)=%s want TWO_PERSON", got)
	}
	clock := fixedClock()
	service := New(clock)
	request := validRequest()
	request.IdempotencyKey = "critical-two-person"
	request.Parameters[commandmodel.ParameterSetpointC] = 24.5
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	// Elevate intent to CRITICAL/TWO_PERSON in package test to exercise two-person approval enforcement.
	service.mu.Lock()
	intent := service.intents[created.Intent.ID]
	intent.Risk = commandmodel.RiskCritical
	intent.ApprovalPolicy = commandmodel.ApprovalTwoPerson
	service.intents[created.Intent.ID] = intent
	service.mu.Unlock()
	updated, err := service.Get(created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := func(id, approver string) commandmodel.ApprovalEvidence {
		return commandmodel.ApprovalEvidence{
			ApprovalID: id, ApproverID: approver,
			Policy: commandmodel.ApprovalTwoPerson, PayloadHash: updated.PayloadHash,
			CapabilityRevision: updated.CapabilityRevision, Risk: commandmodel.RiskCritical,
			RiskRuleRevision: updated.RiskSnapshot.RuleRevision,
			Authorization:    freshApprovalAuthorization(updated, approver, clock(), "approval-grant-"+id),
			IssuedAt:         clock(), ExpiresAt: clock().Add(10 * time.Minute),
		}
	}
	partial, err := service.Approve(commandmodel.ApproveRequest{TenantID: updated.TenantID, CommandID: updated.ID, Approval: approval("approval-a", "principal-2")})
	if !errors.Is(err, ErrApprovalRequired) || partial.Status != commandmodel.IntentAwaitingApproval || len(partial.Approvals) != 1 {
		t.Fatalf("first approval result=%#v err=%v", partial, err)
	}
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: updated.TenantID, CommandID: updated.ID, Approval: approval("approval-dup", "principal-2")}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("duplicate approver accepted: err=%v", err)
	}
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: updated.TenantID, CommandID: updated.ID, Approval: approval("approval-b", "principal-3")})
	if err != nil || approved.Status != commandmodel.IntentQueued || len(approved.Approvals) != 2 {
		t.Fatalf("two-person approval result=%#v err=%v", approved, err)
	}
}

func TestExecutionRejectsExpiredEarlierApprovalAuthorization(t *testing.T) {
	clockNow := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return clockNow }
	service := New(clock)
	request := validRequest()
	request.Parameters[commandmodel.ParameterSetpointC] = 24.5
	request.IdempotencyKey = "expired-earlier-approval"
	created, err := service.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	intent := service.intents[created.Intent.ID]
	intent.Risk = commandmodel.RiskCritical
	intent.ApprovalPolicy = commandmodel.ApprovalTwoPerson
	service.intents[created.Intent.ID] = intent
	service.mu.Unlock()
	updated, err := service.Get(created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := func(id, approver string) commandmodel.ApprovalEvidence {
		return commandmodel.ApprovalEvidence{
			ApprovalID: id, ApproverID: approver,
			Policy: commandmodel.ApprovalTwoPerson, PayloadHash: updated.PayloadHash,
			CapabilityRevision: updated.CapabilityRevision, Risk: commandmodel.RiskCritical,
			RiskRuleRevision: updated.RiskSnapshot.RuleRevision,
			Authorization:    freshApprovalAuthorization(updated, approver, clockNow, "grant-"+id),
			IssuedAt:         clockNow, ExpiresAt: clockNow.Add(10 * time.Minute),
		}
	}
	first := approval("approval-expiring", "principal-2")
	first.Authorization.ExpiresAt = clockNow.Add(5 * time.Second)
	if _, err := service.Approve(commandmodel.ApproveRequest{TenantID: updated.TenantID, CommandID: updated.ID, Approval: first}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("first approval err=%v", err)
	}
	clockNow = clockNow.Add(2 * time.Second)
	second := approval("approval-current", "principal-3")
	approved, err := service.Approve(commandmodel.ApproveRequest{TenantID: updated.TenantID, CommandID: updated.ID, Approval: second})
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

func TestLeasedMediumRiskEntersQueuedDirectlyWhileHumanRequiresApproval(t *testing.T) {
	clock := fixedClock()
	service := New(clock)

	humanReq := validRequest()
	humanReq.IdempotencyKey = "human-medium-step"
	humanReq.Capability = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	humanReq.Parameters = commandmodel.CommandParameters{commandmodel.ParameterSetpointC: 8.5}
	humanReq.CurrentState.CurrentValue = testFloat64Pointer(7.0)
	humanReq.Authorization.Capability = commandmodel.CapabilitySetChilledWaterTemperatureSetpoint
	humanReq.Authorization.CapabilityRevision = "capability:set-chilled-water-temperature-setpoint:v1"
	humanReq.Authorization.MaximumRisk = commandmodel.RiskMedium

	humanCreated, err := service.Submit(humanReq)
	if err != nil {
		t.Fatalf("human medium submit failed: %v", err)
	}
	if humanCreated.Intent.Risk != commandmodel.RiskMedium ||
		humanCreated.Intent.ApprovalPolicy != commandmodel.ApprovalSingleApprover ||
		humanCreated.Intent.Status != commandmodel.IntentAwaitingApproval {
		t.Fatalf("expected human 1.5C command to await approval, got %#v", humanCreated.Intent)
	}

	leasedReq := humanReq
	leasedReq.IdempotencyKey = "auto-opt:leased-medium-step"
	leasedReq.Authorization.WorkloadSPIFFE = "spiffe://hvac.local/optimization-service"
	leasedReq.Authorization.LeaseID = "01990000-8800-7000-8000-000000000011"
	leasedReq.Authorization.ArmedBy = "operator-1"

	leasedCreated, err := service.Submit(leasedReq)
	if err != nil {
		t.Fatalf("leased medium submit failed: %v", err)
	}
	if leasedCreated.Intent.Risk != commandmodel.RiskMedium ||
		leasedCreated.Intent.ApprovalPolicy != commandmodel.ApprovalNone ||
		leasedCreated.Intent.Status != commandmodel.IntentQueued {
		t.Fatalf("expected leased 1.5C command to queue directly with ApprovalNone, got %#v", leasedCreated.Intent)
	}
	if _, err := service.PrepareDispatch(leasedCreated.Intent.ID, "dispatcher-a", clock().Add(time.Minute)); err != nil {
		t.Fatalf("expected queued leased command to be dispatchable, err=%v", err)
	}
}
