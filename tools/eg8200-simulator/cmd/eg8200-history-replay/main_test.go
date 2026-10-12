package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/tools/eg8200-simulator/internal/simulator"
)

func TestRunReplayReusesCanonicalPlantAndProducesDeterministicRequests(t *testing.T) {
	plantConfig, deviceIDs, err := loadReplayConfigs("../../configs/central-plant.local.json", "../../configs/central-plant.device-ids.local.json")
	if err != nil {
		t.Fatal(err)
	}
	const (
		datasetID = "01991f00-0000-7000-8000-000000000001"
	)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	duration := 2 * plantConfig.Interval()

	run := func() []replayObservationRequest {
		requests := make([]replayObservationRequest, 0)
		count, err := runReplay(t.Context(), plantConfig, deviceIDs, replayPlan{DatasetID: datasetID, From: from, Duration: duration}, func(_ context.Context, request replayObservationRequest) error {
			requests = append(requests, request)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 || count != len(requests) {
			t.Fatalf("count=%d requests=%d", count, len(requests))
		}
		return requests
	}

	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same replay dataset and canonical Plant/Scenario did not produce deterministic request sequence")
	}

	nextOffset := map[string]int64{}
	for _, request := range first {
		if request.ReplayDatasetID != datasetID || request.DeviceID == "" || request.TelemetryKey == "" || len(request.Value) == 0 || request.SampledAt.Before(from) || request.SampledAt.After(from.Add(duration)) {
			t.Fatalf("request=%#v", request)
		}
		if request.Offset != nextOffset[request.DeviceID] {
			t.Fatalf("device %s offset=%d want=%d", request.DeviceID, request.Offset, nextOffset[request.DeviceID])
		}
		nextOffset[request.DeviceID]++
	}
}

// Local savings acceptance replays weeks of the two counters M&V reads, on minute steps, with the
// chilled water setpoint raised partway through as an operator would.
func TestAPlannedReplayKeepsItsPointsStepsAndScheduledCommands(t *testing.T) {
	plantConfig, deviceIDs, err := loadReplayConfigs("../../configs/central-plant.local.json", "../../configs/central-plant.device-ids.local.json")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	raised := from.Add(12 * time.Hour)
	plan := replayPlan{
		DatasetID: "01991f00-0000-7000-8000-000000000002",
		From:      from,
		Duration:  24 * time.Hour,
		Step:      time.Minute,
		Points:    map[string]bool{"METER-HVAC-TOTAL/hvac_meter.energy": true, "CHILLER-01/chiller.chilled_water_temperature_setpoint": true},
		Commands: []scheduledCommand{{At: raised, Command: simulator.Command{
			DeviceID: "CHILLER-01", Method: "setChilledWaterTemperatureSetpoint", Params: map[string]float64{"setpointC": 8},
		}}},
	}
	var requests []replayObservationRequest
	if _, err := runReplay(t.Context(), plantConfig, deviceIDs, plan, func(_ context.Context, request replayObservationRequest) error {
		requests = append(requests, request)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	keys := map[string]int{}
	var setpointBefore, setpointAfter string
	for _, request := range requests {
		keys[request.TelemetryKey]++
		if request.TelemetryKey == "chilled_water_temperature_setpoint" {
			if request.SampledAt.Before(raised) {
				setpointBefore = string(request.Value)
			} else {
				setpointAfter = string(request.Value)
			}
		}
	}
	if len(keys) != 2 || keys["energy"] != 24*60+1 {
		t.Fatalf("replayed keys = %v", keys)
	}
	if setpointBefore != "7" || setpointAfter != "8" {
		t.Fatalf("setpoint before %s after %s", setpointBefore, setpointAfter)
	}
}

// Telemetry Runtime refuses a burst it cannot take as retryable; the replay waits and sends the same
// observation again instead of abandoning the window.
func TestARateLimitedObservationIsSentAgain(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			writer.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := &replayClient{endpoint: server.URL, httpClient: server.Client(), retryWait: func(int) time.Duration { return 0 }}
	if err := client.Admit(t.Context(), replayObservationRequest{}); err != nil || calls != 3 {
		t.Fatalf("err = %v after %d calls", err, calls)
	}

	refused := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusBadRequest) }))
	defer refused.Close()
	client = &replayClient{endpoint: refused.URL, httpClient: refused.Client(), retryWait: func(int) time.Duration { return 0 }}
	if err := client.Admit(t.Context(), replayObservationRequest{}); err == nil {
		t.Fatal("an invalid observation was not refused")
	}
}

func TestAReplayRefusesAPointThePlantDoesNotHave(t *testing.T) {
	plantConfig, deviceIDs, err := loadReplayConfigs("../../configs/central-plant.local.json", "../../configs/central-plant.device-ids.local.json")
	if err != nil {
		t.Fatal(err)
	}
	plan := replayPlan{
		DatasetID: "01991f00-0000-7000-8000-000000000003", From: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), Duration: time.Hour, Step: time.Minute,
		Points: map[string]bool{"METER-HVAC-TOTAL/hvac_meter.energy": true, "METER-HVAC-TOTAL/hvac_meter.enrgy": true},
	}
	if _, err := runReplay(t.Context(), plantConfig, deviceIDs, plan, func(context.Context, replayObservationRequest) error { return nil }); err == nil {
		t.Fatal("a misspelled point replayed nothing for it without saying so")
	}
}
