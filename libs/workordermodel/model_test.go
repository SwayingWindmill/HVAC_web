package workordermodel_test

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	organizationID = "01910000-0000-7000-8000-000000000001"
	siteID         = "01910000-0001-7000-8000-000000000001"
	workOrderID    = "01910000-0002-7000-8000-000000000001"
	alarmID        = "01910000-0003-7000-8000-000000000001"
)

func TestWorkOrderValidateAcceptsAuthoritativeProjection(t *testing.T) {
	item := validWorkOrder()
	if err := item.Validate(); err != nil {
		t.Fatalf("valid Work Order rejected: %v", err)
	}
}

func TestWorkOrderValidateRequiresCompletionEvidence(t *testing.T) {
	item := validWorkOrder()
	item.Status = workordermodel.StatusCompleted
	item.Timeline = append(item.Timeline, workordermodel.TimelineEvent{
		Operation:  workordermodel.OperationComplete,
		FromStatus: ptrStatus(workordermodel.StatusOpen),
		ToStatus:   workordermodel.StatusCompleted,
		Reason:     "verified repair",
		ActorType:  "PRINCIPAL",
		ActorID:    "principal:operator",
		OccurredAt: "2026-08-01T02:00:00Z",
		Version:    2,
	})
	item.Version = 2
	item.UpdatedAt = "2026-08-01T02:00:00Z"
	if err := item.Validate(); err == nil {
		t.Fatal("completed Work Order without completion evidence was accepted")
	}
	item.CompletionEvidence = []workordermodel.EvidenceReference{{Kind: "verification-report", Reference: "evidence://verification/1", CapturedAt: "2026-08-01T02:00:00Z"}}
	if err := item.Validate(); err != nil {
		t.Fatalf("completed Work Order with evidence was rejected: %v", err)
	}
}

func TestWorkOrderValidateRejectsProjectionDrift(t *testing.T) {
	tests := map[string]func(*workordermodel.WorkOrder){
		"duplicate source": func(item *workordermodel.WorkOrder) {
			item.SourceReferences = append(item.SourceReferences, item.SourceReferences[0])
		},
		"task counts":            func(item *workordermodel.WorkOrder) { item.Tasks.Completed = item.Tasks.Total + 1 },
		"timeline version":       func(item *workordermodel.WorkOrder) { item.Timeline[0].Version = 2 },
		"invalid source":         func(item *workordermodel.WorkOrder) { item.SourceReferences[0].Domain = "TELEMETRY" },
		"updated before created": func(item *workordermodel.WorkOrder) { item.UpdatedAt = "2026-07-31T23:59:59Z" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			item := validWorkOrder()
			mutate(&item)
			if err := item.Validate(); err == nil {
				t.Fatal("invalid Work Order was accepted")
			}
		})
	}
}

func TestListResponseRejectsCrossScopeAndDuplicateItems(t *testing.T) {
	item := validWorkOrder()
	response := workordermodel.ListResponse{SchemaVersion: 1, Items: []workordermodel.WorkOrder{item}, HasMore: false}
	if err := response.Validate(organizationID, siteID, 50); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}

	crossScope := response
	crossScope.Items = append([]workordermodel.WorkOrder(nil), response.Items...)
	crossScope.Items[0].SiteID = "01910000-0001-7000-8000-000000000002"
	if err := crossScope.Validate(organizationID, siteID, 50); err == nil {
		t.Fatal("cross-Site Work Order list was accepted")
	}

	duplicate := response
	duplicate.Items = []workordermodel.WorkOrder{item, item}
	if err := duplicate.Validate(organizationID, siteID, 50); err == nil {
		t.Fatal("duplicate Work Order list was accepted")
	}

	blankCursor := response
	blank := "   "
	blankCursor.NextCursor = &blank
	blankCursor.HasMore = true
	if err := blankCursor.Validate(organizationID, siteID, 50); err == nil {
		t.Fatal("blank Work Order cursor was accepted")
	}
}

func validWorkOrder() workordermodel.WorkOrder {
	return workordermodel.WorkOrder{
		SchemaVersion:      1,
		WorkOrderID:        workOrderID,
		OrganizationID:     organizationID,
		SiteID:             siteID,
		Title:              "Inspect AHU-1 fan vibration",
		Description:        "Verify the reported vibration and record the maintenance outcome.",
		Priority:           workordermodel.PriorityHigh,
		Status:             workordermodel.StatusOpen,
		SourceReferences:   []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: alarmID, Relationship: workordermodel.RelationshipOrigin}},
		Tasks:              workordermodel.TaskSummary{Total: 2, Completed: 0, Blocked: 0},
		NoteCount:          0,
		AttachmentCount:    0,
		CompletionEvidence: []workordermodel.EvidenceReference{},
		Timeline: []workordermodel.TimelineEvent{{
			Operation:  workordermodel.OperationCreate,
			ToStatus:   workordermodel.StatusOpen,
			Reason:     "created from authoritative Alarm",
			ActorType:  "PRINCIPAL",
			ActorID:    "principal:operator",
			OccurredAt: "2026-08-01T00:00:00Z",
			Version:    1,
		}},
		Version:   1,
		CreatedAt: "2026-08-01T00:00:00Z",
		UpdatedAt: "2026-08-01T00:00:00Z",
	}
}

func ptrStatus(value workordermodel.Status) *workordermodel.Status { return &value }
