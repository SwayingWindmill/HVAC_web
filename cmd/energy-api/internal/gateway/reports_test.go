package gateway

import (
	"net/http"
	"testing"
)

func TestReportRoutesReadAMonthOrAYearOfSnapshots(t *testing.T) {
	siteID := "01990000-5000-7000-8000-000000000001"
	base := "/api/v1/sites/" + siteID + "/reports/monthly"
	for path, internalPath := range map[string]string{
		base:              "/monthly",
		base + "/2026-09": "/monthly/2026-09",
	} {
		route, ok := matchPublicReportRoute(http.MethodGet, path)
		if !ok || route.siteID != siteID || route.internalPath != internalPath {
			t.Fatalf("%s resolved as %#v ok=%t", path, route, ok)
		}
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, base + "/2026-09"},
		{http.MethodGet, base + "/"},
		{http.MethodGet, base + "/2026-09/versions"},
		{http.MethodGet, "/api/v1/sites/" + siteID + "/reports/yearly"},
	} {
		if _, ok := matchPublicReportRoute(test.method, test.path); ok {
			t.Fatalf("%s %s unexpectedly matched a report route", test.method, test.path)
		}
	}
}
