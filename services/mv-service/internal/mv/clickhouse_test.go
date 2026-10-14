package mv

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHourlySamplesReadTheBoundaryAndThePlantInTheWindow(t *testing.T) {
	var sql string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql = string(body)
		if user, _, ok := request.BasicAuth(); !ok || user != "mv_service_reader" {
			t.Errorf("basic auth user = %q", user)
		}
		_, _ = writer.Write([]byte(`{"period_start":"2026-10-11T16:00:00Z","energy_kwh":21.5,"cooling_kwh":740,"plant_kwh":210.5}` + "\n" +
			`{"period_start":"2026-10-11T17:00:00Z","energy_kwh":9,"cooling_kwh":300,"plant_kwh":95}` + "\n"))
	}))
	defer server.Close()

	reader, err := NewClickHouseHours(server.URL, "mv_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC)
	device := testDevice
	samples, err := reader.HourlySamples(t.Context(), testTenant, testSite, &device, from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || !samples[0].Start.Equal(from) || samples[0].EnergyKWh != 21.5 || samples[0].PlantKWh != 210.5 || samples[1].CoolingKWh != 300 {
		t.Fatalf("samples = %+v", samples)
	}
	for _, scope := range []string{
		"site_id=toUUID('" + testSite + "')", "tenant_id=toUUID('" + testTenant + "')",
		"(subject_type='DEVICE' AND subject_id=toUUID('" + testDevice + "') AND metric_code='device_hourly_energy'),'device'",
		"(subject_type='SITE' AND subject_id=toUUID('" + testSite + "') AND metric_code='hourly_cooling'),'cooling'",
		"sumIf(value, key='device') AS energy_kwh", "quality='GOOD'", "HAVING count()=3",
		"period_start >= parseDateTime64BestEffort('2026-10-11T16:00:00Z'", "period_start < parseDateTime64BestEffort('2026-10-12T16:00:00Z'",
	} {
		if !strings.Contains(sql, scope) {
			t.Fatalf("query lacks %q:\n%s", scope, sql)
		}
	}
	// The plant's total meter is its own boundary: its electricity is the plant's.
	if plant := hourlySQL(testTenant, testSite, nil, from, from.Add(time.Hour)); !strings.Contains(plant, "sumIf(value, key='plant') AS energy_kwh") || !strings.Contains(plant, "HAVING count()=2") || strings.Contains(plant, "DEVICE") {
		t.Fatalf("plant boundary query:\n%s", plant)
	}
	if _, err := reader.HourlySamples(t.Context(), "not-a-tenant", testSite, nil, from, from.Add(time.Hour)); err == nil {
		t.Fatal("a malformed tenant reached ClickHouse")
	}
}
