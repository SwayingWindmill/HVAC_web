package mv

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHourlySamplesReadBothSiteMetricsInTheWindow(t *testing.T) {
	var sql string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql = string(body)
		if user, _, ok := request.BasicAuth(); !ok || user != "mv_service_reader" {
			t.Errorf("basic auth user = %q", user)
		}
		_, _ = writer.Write([]byte(`{"period_start":"2026-10-11T16:00:00Z","energy_kwh":210.5,"cooling_kwh":740}` + "\n" +
			`{"period_start":"2026-10-11T17:00:00Z","energy_kwh":95,"cooling_kwh":300}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseHours(server.URL, "mv_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC)
	samples, err := reader.HourlySamples(t.Context(), testTenant, testSite, from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || !samples[0].Start.Equal(from) || samples[0].EnergyKWh != 210.5 || samples[1].CoolingKWh != 300 {
		t.Fatalf("samples = %+v", samples)
	}
	for _, scope := range []string{
		"site_id=toUUID('" + testSite + "')", "tenant_id=toUUID('" + testTenant + "')",
		"metric_code IN ('hourly_energy','hourly_cooling')", "quality='GOOD'", "HAVING count()=2",
		"period_start >= parseDateTime64BestEffort('2026-10-11T16:00:00Z'", "period_start < parseDateTime64BestEffort('2026-10-12T16:00:00Z'",
	} {
		if !strings.Contains(sql, scope) {
			t.Fatalf("query lacks %q:\n%s", scope, sql)
		}
	}
	if _, err := reader.HourlySamples(t.Context(), "not-a-tenant", testSite, from, from.Add(time.Hour)); err == nil {
		t.Fatal("a malformed tenant reached ClickHouse")
	}
}
