package workorderservice

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	postgresOrganizationID      = "01920000-0000-7000-8000-000000000001"
	postgresOtherOrganizationID = "01920000-0000-7000-8000-000000000002"
	postgresSiteID              = "01920000-0001-7000-8000-000000000001"
	postgresOtherSiteID         = "01920000-0001-7000-8000-000000000002"
	postgresForeignSiteID       = "01920000-0001-7000-8000-000000000003"
	postgresWorkOrderOne        = "01920000-1000-7000-8000-000000000001"
	postgresWorkOrderTwo        = "01920000-1000-7000-8000-000000000002"
	postgresCompletedWorkOrder  = "01920000-1000-7000-8000-000000000003"
)

func TestPostgresReadsAreScopedFilteredPaginatedAndConvergent(t *testing.T) {
	databaseURL := os.Getenv("S5_WORK_ORDER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("S5_WORK_ORDER_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	store, err := OpenPostgresStore(ctx, databaseURL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	firstPage, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 1 || firstPage.Items[0].WorkOrderID != postgresWorkOrderTwo || !firstPage.HasMore || firstPage.NextCursor == nil {
		t.Fatalf("unexpected first page: %#v", firstPage)
	}
	secondPage, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{Limit: 1, Cursor: *firstPage.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Items) != 1 || secondPage.Items[0].WorkOrderID != postgresWorkOrderOne || secondPage.HasMore || secondPage.NextCursor != nil {
		t.Fatalf("unexpected second page: %#v", secondPage)
	}
	if _, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{Status: workordermodel.StatusOpen, Limit: 1, Cursor: *firstPage.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor filter binding was not enforced: %v", err)
	}

	openOnly, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{Status: workordermodel.StatusOpen, Limit: 50})
	if err != nil || len(openOnly.Items) != 1 || openOnly.Items[0].WorkOrderID != postgresWorkOrderOne {
		t.Fatalf("unexpected status filter: %#v err=%v", openOnly, err)
	}
	urgentOnly, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{Priority: workordermodel.PriorityUrgent, Limit: 50})
	if err != nil || len(urgentOnly.Items) != 1 || urgentOnly.Items[0].WorkOrderID != postgresWorkOrderTwo {
		t.Fatalf("unexpected priority filter: %#v err=%v", urgentOnly, err)
	}
	assignedOnly, err := store.List(ctx, postgresOrganizationID, postgresSiteID, Filter{AssigneeID: "principal:operator-a", Limit: 50})
	if err != nil || len(assignedOnly.Items) != 1 || assignedOnly.Items[0].WorkOrderID != postgresWorkOrderOne {
		t.Fatalf("unexpected assignee filter: %#v err=%v", assignedOnly, err)
	}

	detail, err := store.Get(ctx, postgresOrganizationID, postgresSiteID, postgresWorkOrderOne)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Tasks.Total != 2 || detail.Tasks.Completed != 1 || detail.NoteCount != 1 || detail.AttachmentCount != 1 || len(detail.SourceReferences) != 1 || len(detail.Timeline) != 1 {
		t.Fatalf("authoritative detail did not converge: %#v", detail)
	}
	completed, err := store.Get(ctx, postgresOrganizationID, postgresOtherSiteID, postgresCompletedWorkOrder)
	if err != nil || completed.Status != workordermodel.StatusCompleted || len(completed.CompletionEvidence) != 1 {
		t.Fatalf("completed Work Order evidence missing: %#v err=%v", completed, err)
	}

	crossOrganization, err := store.List(ctx, postgresOtherOrganizationID, postgresSiteID, Filter{Limit: 50})
	if err != nil || len(crossOrganization.Items) != 0 {
		t.Fatalf("cross-Organization rows were visible in list: %#v err=%v", crossOrganization, err)
	}
	crossSite, err := store.List(ctx, postgresOrganizationID, postgresForeignSiteID, Filter{Limit: 50})
	if err != nil || len(crossSite.Items) != 0 {
		t.Fatalf("cross-Site rows were visible in list: %#v err=%v", crossSite, err)
	}
	if _, err := store.Get(ctx, postgresOtherOrganizationID, postgresSiteID, postgresWorkOrderOne); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Organization detail was visible: %v", err)
	}
	if _, err := store.Get(ctx, postgresOrganizationID, postgresOtherSiteID, postgresWorkOrderOne); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-Site detail was visible: %v", err)
	}
}

func TestPostgresMalformedProjectionFailsClosed(t *testing.T) {
	databaseURL := os.Getenv("S5_WORK_ORDER_TEST_DATABASE_URL")
	adminURL := os.Getenv("S5_WORK_ORDER_ADMIN_DATABASE_URL")
	if databaseURL == "" || adminURL == "" {
		t.Skip("S5 Work Order PostgreSQL URLs are not configured")
	}
	ctx := context.Background()
	store, err := OpenPostgresStore(ctx, databaseURL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, `UPDATE work_order_runtime.work_order_current SET task_total = 3 WHERE work_order_id = $1`, postgresWorkOrderOne); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(ctx, `UPDATE work_order_runtime.work_order_current SET task_total = 2 WHERE work_order_id = $1`, postgresWorkOrderOne)
	}()
	if _, err := store.Get(ctx, postgresOrganizationID, postgresSiteID, postgresWorkOrderOne); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nonconvergent Work Order projection did not fail closed: %v", err)
	}
}
