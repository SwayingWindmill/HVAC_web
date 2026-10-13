package fdd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClickHouseEvidenceReadsTheLatestSupplyAndReturnOfTheDevice(t *testing.T) {
	request := validEvaluation()
	rows := `{"telemetry_key":"return_water_temperature","observation_id":"01990000-6200-7000-8000-000000000001","point_type":"TELEMETRY","value_type":"NUMBER","unit":"Cel","quality":"GOOD","value":10.4,"sampled_at":"2026-08-19T10:14:58.000Z"}
{"telemetry_key":"supply_water_temperature","observation_id":"01990000-6200-7000-8000-000000000002","point_type":"TELEMETRY","value_type":"NUMBER","unit":"Cel","quality":"GOOD","value":7.1,"sampled_at":"2026-08-19T10:14:59.000Z"}
`
	var sql string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql = string(body)
		if user, _, ok := request.BasicAuth(); !ok || user != "fdd_service_reader" {
			t.Errorf("reader account=%q", user)
		}
		_, _ = writer.Write([]byte(rows))
	}))
	defer server.Close()
	evidence, err := NewClickHouseEvidence(server.URL, "fdd_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	query := LoopQuery{TenantID: request.TenantID, SiteID: request.SiteID, DeviceID: request.DeviceID, From: request.At.Add(-15 * time.Minute), To: request.At}
	readings, err := evidence.LatestLoopReadings(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if readings.Supply.Value != 7.1 || readings.Return.Value != 10.4 || readings.Supply.ObservationID != "01990000-6200-7000-8000-000000000002" ||
		!readings.Return.SampledAt.Equal(time.Date(2026, 8, 19, 10, 14, 58, 0, time.UTC)) {
		t.Fatalf("readings=%#v", readings)
	}
	for _, scope := range []string{"tenant_id=toUUID('" + request.TenantID + "')", "site_id=toUUID('" + request.SiteID + "')", "device_id=toUUID('" + request.DeviceID + "')"} {
		if !strings.Contains(sql, scope) {
			t.Fatalf("evidence query is not scoped by %s:\n%s", scope, sql)
		}
	}

	rows = rows[:strings.Index(rows, "\n")+1]
	if _, err = evidence.LatestLoopReadings(t.Context(), query); !errors.Is(err, ErrNoLoopEvidence) {
		t.Fatalf("a window without a supply reading err=%v", err)
	}
}
