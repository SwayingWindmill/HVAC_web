package workordermodel

import (
	"errors"
	"testing"
	"time"
)

const (
	mutationOrganizationID = "01930000-0000-7000-8000-000000000001"
	mutationSiteID         = "01930000-0001-7000-8000-000000000001"
	mutationWorkOrderID    = "01930000-1000-7000-8000-000000000001"
	mutationAlarmID        = "01930000-2000-7000-8000-000000000001"
)

func TestCreateBuildsServerOwnedOpenProjection(t *testing.T) {
	assignee := " principal:operator-a "
	team := " team:mechanical "
	start := "2026-08-02T01:00:00+00:00"
	due := "2026-08-02T04:00:00Z"
	created, err := Create(CreateInput{
		WorkOrderID: mutationWorkOrderID, OrganizationID: mutationOrganizationID, SiteID: mutationSiteID,
		Title: " Inspect AHU fan ", Description: " Validate the vibration. ", Priority: PriorityHigh,
		SourceReferences: []SourceReference{{Domain: SourceAlarm, ResourceID: mutationAlarmID, Relationship: RelationshipOrigin}},
		AssigneeID:       &assignee, TeamID: &team, ScheduledStart: &start, DueAt: &due,
		ActorType: "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "idem-create-0001",
		OccurredAt: "2026-08-01T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != StatusOpen || created.Version != 1 || created.AssigneeID == nil || *created.AssigneeID != "principal:operator-a" || created.TeamID == nil || *created.TeamID != "team:mechanical" {
		t.Fatalf("unexpected create projection: %#v", created)
	}
	if len(created.Timeline) != 1 || created.Timeline[0].Operation != OperationCreate || created.Timeline[0].AssigneeID == nil || created.Timeline[0].TeamID == nil {
		t.Fatalf("create timeline lacks ownership evidence: %#v", created.Timeline)
	}
	if created.ScheduledStart == nil || *created.ScheduledStart != "2026-08-02T01:00:00Z" || created.DueAt == nil || *created.DueAt != "2026-08-02T04:00:00Z" {
		t.Fatalf("schedule was not normalized: %#v", created)
	}
}

func TestCreateRejectsInvalidDueWindowAndSourceAuthority(t *testing.T) {
	base := CreateInput{
		WorkOrderID: mutationWorkOrderID, OrganizationID: mutationOrganizationID, SiteID: mutationSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: PriorityHigh,
		SourceReferences: []SourceReference{{Domain: SourceAlarm, ResourceID: mutationAlarmID, Relationship: RelationshipOrigin}},
		ActorType:        "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "idem-create-0001",
		OccurredAt: "2026-08-01T12:00:00Z",
	}
	past := "2026-08-01T11:59:59Z"
	tooFar := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Add(MaximumScheduleHorizon + time.Second).Format(time.RFC3339Nano)
	for name, mutate := range map[string]func(*CreateInput){
		"past due":            func(input *CreateInput) { input.DueAt = &past },
		"unbounded due":       func(input *CreateInput) { input.DueAt = &tooFar },
		"forged alarm source": func(input *CreateInput) { input.SourceReferences[0].ResourceID = "alarm:fixture" },
		"related-only source": func(input *CreateInput) { input.SourceReferences[0].Relationship = RelationshipRelated },
		"multiple sources": func(input *CreateInput) {
			input.SourceReferences = append(input.SourceReferences, SourceReference{Domain: SourceAsset, ResourceID: "01930000-2000-7000-8000-000000000002", Relationship: RelationshipRelated})
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.SourceReferences = append([]SourceReference(nil), base.SourceReferences...)
			mutate(&input)
			if _, err := Create(input); !errors.Is(err, ErrInvalidCreate) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestApplyAssignmentUsesExpectedVersionAndExplicitTuple(t *testing.T) {
	created, err := Create(CreateInput{
		WorkOrderID: mutationWorkOrderID, OrganizationID: mutationOrganizationID, SiteID: mutationSiteID,
		Title: "Inspect AHU fan", Description: "Validate vibration.", Priority: PriorityHigh,
		SourceReferences: []SourceReference{{Domain: SourceAlarm, ResourceID: mutationAlarmID, Relationship: RelationshipOrigin}},
		ActorType:        "PRINCIPAL", ActorID: "principal:creator", PolicyRevision: "policy-7", CorrelationID: "idem-create-0001",
		OccurredAt: "2026-08-01T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	assignee := "principal:operator-b"
	team := "team:controls"
	assigned, err := ApplyAssignment(created, AssignmentInput{
		ExpectedVersion: 1, AssigneeID: &assignee, TeamID: &team, Reason: "route to controls",
		ActorType: "PRINCIPAL", ActorID: "principal:dispatcher", PolicyRevision: "policy-8", CorrelationID: "idem-assign-0001",
		OccurredAt: "2026-08-01T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Version != 2 || assigned.Status != StatusOpen || assigned.AssigneeID == nil || assigned.TeamID == nil || assigned.Timeline[1].Operation != OperationAssign {
		t.Fatalf("unexpected assignment: %#v", assigned)
	}
	if _, err := ApplyAssignment(assigned, AssignmentInput{ExpectedVersion: 1}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error=%v", err)
	}
	cleared, err := ApplyAssignment(assigned, AssignmentInput{
		ExpectedVersion: 2, Reason: "return to unassigned queue", ActorType: "PRINCIPAL", ActorID: "principal:dispatcher",
		PolicyRevision: "policy-8", CorrelationID: "idem-assign-0002", OccurredAt: "2026-08-01T12:02:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.AssigneeID != nil || cleared.TeamID != nil || cleared.Timeline[2].Operation != OperationUnassign || cleared.Version != 3 {
		t.Fatalf("unexpected clear assignment: %#v", cleared)
	}
}
