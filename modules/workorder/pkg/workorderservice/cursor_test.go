package workorderservice

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workordermodel"
)

const (
	cursorOrganizationID = "01920000-0000-7000-8000-000000000001"
	cursorSiteID         = "01920000-0001-7000-8000-000000000001"
	cursorWorkOrderID    = "01920000-1000-7000-8000-000000000001"
)

func TestCursorCodecBindsScopeFiltersAndPosition(t *testing.T) {
	codec, err := newCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	filter := Filter{Status: workordermodel.StatusOpen, Priority: workordermodel.PriorityHigh, AssigneeID: "principal:operator", Limit: 25}
	updatedAt := time.Date(2026, 8, 1, 10, 30, 0, 123, time.UTC)
	token, err := codec.Encode(cursorOrganizationID, cursorSiteID, filter, updatedAt, cursorWorkOrderID)
	if err != nil {
		t.Fatal(err)
	}
	position, err := codec.Decode(token, cursorOrganizationID, cursorSiteID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if !position.UpdatedAt.Equal(updatedAt) || position.WorkOrderID != cursorWorkOrderID {
		t.Fatalf("unexpected cursor position: %#v", position)
	}

	cases := map[string]struct {
		token          string
		organizationID string
		siteID         string
		filter         Filter
	}{
		"tampered":           {token: token[:len(token)-1] + "A", organizationID: cursorOrganizationID, siteID: cursorSiteID, filter: filter},
		"organization drift": {token: token, organizationID: "01920000-0000-7000-8000-000000000002", siteID: cursorSiteID, filter: filter},
		"site drift":         {token: token, organizationID: cursorOrganizationID, siteID: "01920000-0001-7000-8000-000000000002", filter: filter},
		"status drift":       {token: token, organizationID: cursorOrganizationID, siteID: cursorSiteID, filter: Filter{Status: workordermodel.StatusBlocked, Priority: filter.Priority, AssigneeID: filter.AssigneeID, Limit: 25}},
		"assignee drift":     {token: token, organizationID: cursorOrganizationID, siteID: cursorSiteID, filter: Filter{Status: filter.Status, Priority: filter.Priority, AssigneeID: "principal:other", Limit: 25}},
		"oversized":          {token: strings.Repeat("x", 513), organizationID: cursorOrganizationID, siteID: cursorSiteID, filter: filter},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(test.token, test.organizationID, test.siteID, test.filter); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("expected invalid cursor, got %v", err)
			}
		})
	}
}

func TestCursorCodecRequiresStrongSecret(t *testing.T) {
	if _, err := newCursorCodec([]byte("too-short")); err == nil {
		t.Fatal("weak cursor secret was accepted")
	}
}
