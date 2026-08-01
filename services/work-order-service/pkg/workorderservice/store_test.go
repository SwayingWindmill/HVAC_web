package workorderservice

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	testOrganizationID = "01910000-0000-7000-8000-000000000001"
	testOtherOrgID     = "01910000-0000-7000-8000-000000000002"
	testSiteID         = "01910000-0001-7000-8000-000000000001"
	testOtherSiteID    = "01910000-0001-7000-8000-000000000002"
	testWorkOrderID    = "01910000-5000-7000-8000-000000000001"
)

func TestMemoryStoreFiltersAndPaginatesDeterministically(t *testing.T) {
	first := validWorkOrder(testWorkOrderID, testOrganizationID, testSiteID, "2026-08-01T02:00:00Z")
	second := validWorkOrder("01910000-5000-7000-8000-000000000002", testOrganizationID, testSiteID, "2026-08-01T01:00:00Z")
	third := validWorkOrder("01910000-5000-7000-8000-000000000003", testOrganizationID, testSiteID, "2026-08-01T00:00:00Z")
	assignee := "principal:operator-1"
	second.AssigneeID = &assignee
	second.Priority = workordermodel.PriorityUrgent
	store, err := NewMemoryStore([]workordermodel.WorkOrder{third, first, second})
	if err != nil {
		t.Fatal(err)
	}

	page, err := store.List(context.Background(), testOrganizationID, testSiteID, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].WorkOrderID != first.WorkOrderID || page.Items[1].WorkOrderID != second.WorkOrderID || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("unexpected first page: %#v", page)
	}
	next, err := store.List(context.Background(), testOrganizationID, testSiteID, Filter{Limit: 2, Cursor: *page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].WorkOrderID != third.WorkOrderID || next.HasMore || next.NextCursor != nil {
		t.Fatalf("unexpected second page: %#v", next)
	}
	filtered, err := store.List(context.Background(), testOrganizationID, testSiteID, Filter{Priority: workordermodel.PriorityUrgent, AssigneeID: assignee, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].WorkOrderID != second.WorkOrderID {
		t.Fatalf("unexpected filtered result: %#v", filtered)
	}
}

func TestMemoryStoreRejectsInvalidCursorAndHidesCrossScopeDetail(t *testing.T) {
	store, err := NewMemoryStore([]workordermodel.WorkOrder{validWorkOrder(testWorkOrderID, testOrganizationID, testSiteID, "2026-08-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(context.Background(), testOrganizationID, testSiteID, Filter{Cursor: "not-a-cursor", Limit: 10}); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("invalid cursor error=%v", err)
	}
	if _, err := store.Get(context.Background(), testOrganizationID, testOtherSiteID, testWorkOrderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Site detail error=%v", err)
	}
	if _, err := store.Get(context.Background(), testOtherOrgID, testSiteID, testWorkOrderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Organization detail error=%v", err)
	}
}

func validWorkOrder(id, organizationID, siteID, updatedAt string) workordermodel.WorkOrder {
	status := workordermodel.StatusOpen
	return workordermodel.WorkOrder{
		SchemaVersion: workordermodel.SchemaVersion,
		WorkOrderID:   id, OrganizationID: organizationID, SiteID: siteID,
		Title: "Inspect air handler", Description: "Authoritative maintenance work order.",
		Priority: workordermodel.PriorityHigh, Status: status,
		SourceReferences: []workordermodel.SourceReference{{Domain: workordermodel.SourceAlarm, ResourceID: "01910000-4000-7000-8000-000000000001", Relationship: workordermodel.RelationshipOrigin}},
		Tasks:            workordermodel.TaskSummary{}, Timeline: []workordermodel.TimelineEvent{{Operation: workordermodel.OperationCreate, ToStatus: status, Reason: "WORK_ORDER_CREATED", ActorType: "WORKLOAD", ActorID: "work-order-service", OccurredAt: "2026-08-01T00:00:00Z", Version: 1}},
		Version: 1, CreatedAt: "2026-08-01T00:00:00Z", UpdatedAt: updatedAt,
	}
}
