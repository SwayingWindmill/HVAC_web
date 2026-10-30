package fdd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// FDD reads a window as each device's last observation of each key per minute, scoped by Tenant,
// Site and the devices the loop and its gates name; replay fixtures are such responses.
func TestClickHouseEvidenceReadsTheWindowPerMinuteForTheLoopAndItsGates(t *testing.T) {
	rows, err := os.ReadFile("testdata/songyaxuan-low-delta-t-2026-10-16T0030Z.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sql = string(body)
		if user, _, ok := request.BasicAuth(); !ok || user != "fdd_service_reader" {
			t.Errorf("reader account=%q", user)
		}
		_, _ = writer.Write(rows)
	}))
	defer server.Close()
	evidence, err := NewClickHouseEvidence(server.URL, "fdd_service_reader", "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(2026, 10, 16, 0, 30, 0, 0, time.UTC)
	query := WindowQuery{TenantID: songyaxuanTenant, SiteID: songyaxuanSite, LoopDeviceID: songyaxuanLoop, GateDeviceIDs: songyaxuanTopology().DeviceIDs(), From: to.Add(-25 * time.Minute), To: to}
	samples, err := evidence.WindowSamples(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 115 || samples[0].Minute.Before(query.From) || !samples[len(samples)-1].Minute.Before(to) {
		t.Fatalf("samples=%d first=%v last=%v", len(samples), samples[0].Minute, samples[len(samples)-1].Minute)
	}
	var runState Sample
	for _, sample := range samples {
		if sample.Key == runStateKey && sample.DeviceID == "01a14000-4000-7000-8000-000000000005" {
			runState = sample
		}
	}
	if runState.Text != "RUNNING" || runState.HasNumber || runState.ValueType != "STRING" {
		t.Fatalf("a run state keeps its text and has no number: %#v", runState)
	}
	for _, scope := range []string{
		"tenant_id=toUUID('" + songyaxuanTenant + "')", "site_id=toUUID('" + songyaxuanSite + "')",
		"device_id=toUUID('" + songyaxuanLoop + "')", "toUUID('01a14000-4000-7000-8000-000000000006')",
		"sampled_at >= parseDateTime64BestEffort('2026-10-16T00:05:00Z'", "sampled_at < parseDateTime64BestEffort('2026-10-16T00:30:00Z'",
		"received_at <= parseDateTime64BestEffort('2026-10-16T00:30:00Z'",
	} {
		if !strings.Contains(sql, scope) {
			t.Fatalf("evidence query is not scoped by %s:\n%s", scope, sql)
		}
	}

	query.GateDeviceIDs = []string{"not-a-uuid"}
	if _, err = evidence.WindowSamples(t.Context(), query); err == nil {
		t.Fatal("a gate device that is not a UUID reached the query")
	}
}
