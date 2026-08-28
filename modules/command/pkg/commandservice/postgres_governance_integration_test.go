package commandservice

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestPostgresApprovalUsesFreshAuthorizationAndCreatesOutboxOnlyAfterThreshold(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	store := NewPostgresStore(opened.pool, func() time.Time { return now }, nil)

	request := postgresCommandRequest("postgres-medium-approval", 24.5)
	created, err := store.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Intent.Status != commandmodel.IntentAwaitingApproval || created.Intent.ApprovalPolicy != commandmodel.ApprovalSingleApprover {
		t.Fatalf("medium-risk command=%#v", created.Intent)
	}
	evidence, err := store.SubmissionEvidence(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.OutboxCount != 0 || evidence.ApprovalCount != 0 || evidence.AuthorizationCount != 1 || evidence.RiskCount != 1 {
		t.Fatalf("pre-approval evidence=%#v", evidence)
	}

	now = now.Add(26 * time.Second)
	approval := postgresApproval(created.Intent, now,
		"018f3e00-a000-7000-8000-000000000001",
		"018f3e00-5000-7000-8000-000000000002",
		"approval-grant-medium")
	bad := approval
	bad.PayloadHash = "wrong"
	if _, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: bad}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("wrong binding approval err=%v", err)
	}
	wrongPurpose := approval
	wrongPurpose.Authorization.Purpose = commandmodel.AuthorizationCommandSubmit
	if _, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: wrongPurpose}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("submit-purpose approval err=%v", err)
	}
	wrongPrincipal := approval
	wrongPrincipal.Authorization.PrincipalID = commandPrincipalA
	if _, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: wrongPrincipal}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("approver principal mismatch err=%v", err)
	}

	approved, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: approval})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != commandmodel.IntentQueued || len(approved.Approvals) != 1 || len(approved.Authorizations) != 2 {
		t.Fatalf("approved command=%#v", approved)
	}
	if approved.Authorization.GrantID != "approval-grant-medium" {
		t.Fatalf("latest authorization=%#v", approved.Authorization)
	}
	evidence, err = store.SubmissionEvidence(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ApprovalCount != 1 || evidence.OutboxCount != 1 || evidence.TransitionCount != 5 || evidence.AuditIntentCount != 2 {
		t.Fatalf("post-approval evidence=%#v", evidence)
	}
}

func TestPostgresHighRiskRequiresTwoDistinctApprovals(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	store := NewPostgresStore(opened.pool, func() time.Time { return now }, nil)

	request := postgresCommandRequest("postgres-high-approval", 25.5)
	created, err := store.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Intent.Risk != commandmodel.RiskHigh || created.Intent.ApprovalPolicy != commandmodel.ApprovalTwoPerson {
		t.Fatalf("high-risk command=%#v", created.Intent)
	}

	first := postgresApproval(created.Intent, now,
		"018f3e00-a000-7000-8000-000000000011",
		"018f3e00-5000-7000-8000-000000000011",
		"approval-grant-high-1")
	partial, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: first})
	if !errors.Is(err, ErrApprovalRequired) || partial.Status != commandmodel.IntentAwaitingApproval || len(partial.Approvals) != 1 {
		t.Fatalf("first approval result=%#v err=%v", partial, err)
	}
	evidence, err := store.SubmissionEvidence(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ApprovalCount != 1 || evidence.OutboxCount != 0 || evidence.TransitionCount != 4 {
		t.Fatalf("partial approval evidence=%#v", evidence)
	}

	now = now.Add(2 * time.Second)
	second := postgresApproval(partial, now,
		"018f3e00-a000-7000-8000-000000000012",
		"018f3e00-5000-7000-8000-000000000012",
		"approval-grant-high-2")
	approved, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: second})
	if err != nil || approved.Status != commandmodel.IntentQueued || len(approved.Approvals) != 2 {
		t.Fatalf("second approval result=%#v err=%v", approved, err)
	}
	evidence, err = store.SubmissionEvidence(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ApprovalCount != 2 || evidence.OutboxCount != 1 || evidence.TransitionCount != 6 || evidence.AuditIntentCount != 3 {
		t.Fatalf("two-person approval evidence=%#v", evidence)
	}
}

func TestPostgresExecutionExpiresWhenAnyApprovalAuthorizationExpires(t *testing.T) {
	runtimeURL, adminURL := commandPostgresTestURLs(t)
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	resetCommandFixture(t, admin)

	opened, err := OpenPostgresStore(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	store := NewPostgresStore(opened.pool, func() time.Time { return now }, nil)

	created, err := store.Submit(ctx, postgresCommandRequest("postgres-expired-earlier-approval", 25.5))
	if err != nil {
		t.Fatal(err)
	}
	first := postgresApproval(created.Intent, now,
		"018f3e00-a000-7000-8000-000000000021",
		"018f3e00-5000-7000-8000-000000000021",
		"approval-grant-expiring")
	first.Authorization.ExpiresAt = now.Add(5 * time.Second)
	partial, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: first})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("first approval result=%#v err=%v", partial, err)
	}
	now = now.Add(2 * time.Second)
	second := postgresApproval(partial, now,
		"018f3e00-a000-7000-8000-000000000022",
		"018f3e00-5000-7000-8000-000000000022",
		"approval-grant-current")
	approved, err := store.Approve(ctx, commandmodel.ApproveRequest{TenantID: commandTenantA, CommandID: created.Intent.ID, Approval: second})
	if err != nil || approved.Status != commandmodel.IntentQueued {
		t.Fatalf("second approval result=%#v err=%v", approved, err)
	}
	now = now.Add(4 * time.Second)
	if _, err := store.ClaimDispatch(ctx, commandTenantA, "dispatcher-a", 10*time.Second); !errors.Is(err, ErrNoDispatchAvailable) {
		t.Fatalf("expired approval authorization became dispatchable: %v", err)
	}
	readBack, err := store.Get(ctx, commandTenantA, created.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.Status != commandmodel.IntentExpired {
		t.Fatalf("expected expired intent, got %s", readBack.Status)
	}
}

func postgresApproval(intent commandmodel.CommandIntent, now time.Time, approvalID, approverID, grantID string) commandmodel.ApprovalEvidence {
	return commandmodel.ApprovalEvidence{
		ApprovalID:         approvalID,
		ApproverID:         approverID,
		ApproverRole:       "CONTROL_APPROVER",
		Policy:             intent.ApprovalPolicy,
		PayloadHash:        intent.PayloadHash,
		CapabilityRevision: intent.CapabilityRevision,
		Risk:               intent.Risk,
		RiskRuleRevision:   intent.RiskSnapshot.RuleRevision,
		Authorization: commandmodel.AuthorizationSnapshot{
			GrantID:                     grantID,
			PolicyRevision:              "command-policy-approval-1",
			Purpose:                     commandmodel.AuthorizationCommandApprove,
			PrincipalID:                 approverID,
			TenantID:                    intent.TenantID,
			SiteID:                      intent.SiteID,
			DeviceID:                    intent.DeviceID,
			Capability:                  intent.Capability,
			MaximumRisk:                 intent.Risk,
			CapabilityRevision:          intent.CapabilityRevision,
			EmergencyRevocationRevision: 2,
			IssuedAt:                    now.Add(-time.Second),
			ExpiresAt:                   now.Add(25 * time.Second),
		},
		IssuedAt:  now,
		ExpiresAt: now.Add(10 * time.Minute),
	}
}
