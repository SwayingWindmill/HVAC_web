package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestRunReplayReusesCanonicalPlantAndProducesDeterministicRequests(t *testing.T) {
	plantConfig, mqttConfig, err := loadReplayConfigs("../../configs/central-plant.local.json", "../../configs/central-plant.mqtt.local.example.json")
	if err != nil {
		t.Fatal(err)
	}
	const (
		integrationID = "018f3e00-0000-7000-8000-000000000101"
		datasetID     = "01991f00-0000-7000-8000-000000000001"
	)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	duration := 2 * plantConfig.Interval()

	run := func() []replayObservationRequest {
		requests := make([]replayObservationRequest, 0)
		count, err := runReplay(t.Context(), plantConfig, mqttConfig, integrationID, datasetID, from, duration, func(_ context.Context, request replayObservationRequest) error {
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
		if request.IntegrationInstanceID != integrationID || request.ReplayDatasetID != datasetID || request.DeviceExternalID == "" || request.TelemetryKey == "" || len(request.Value) == 0 || request.SampledAt.Before(from) || request.SampledAt.After(from.Add(duration)) {
			t.Fatalf("request=%#v", request)
		}
		if request.Offset != nextOffset[request.DeviceExternalID] {
			t.Fatalf("device %s offset=%d want=%d", request.DeviceExternalID, request.Offset, nextOffset[request.DeviceExternalID])
		}
		nextOffset[request.DeviceExternalID]++
	}
}
