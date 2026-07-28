package simulator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeReadinessTracksLatestPublishOutcome(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if fail.Load() {
			http.Error(writer, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	plantConfig := testPlantConfig()
	tokens := make(map[string]string, len(plantConfig.DeviceIDs()))
	for _, deviceID := range plantConfig.DeviceIDs() {
		tokens[deviceID] = "test-token"
	}
	client, err := NewThingsBoardClient(server.URL, tokens, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	config := Config{PublishInterval: "5s", Plant: plantConfig}
	plant := NewPlant(plantConfig, time.Now())
	runtime, err := NewRuntime(config, plant, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.publish(context.Background(), plant.Tick(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !runtime.Ready() {
		t.Fatal("expected runtime to become ready after successful publish")
	}

	fail.Store(true)
	if err := runtime.publish(context.Background(), plant.Tick(time.Second)); err == nil {
		t.Fatal("expected provider publish failure")
	}
	if runtime.Ready() {
		t.Fatal("runtime remained ready after latest publish failed")
	}
}

func TestRuntimeDoesNotReapplyRetriedRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	plantConfig := testPlantConfig()
	tokens := make(map[string]string, len(plantConfig.DeviceIDs()))
	for _, deviceID := range plantConfig.DeviceIDs() {
		tokens[deviceID] = "test-token"
	}
	client, err := NewThingsBoardClient(server.URL, tokens, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	plant := NewPlant(plantConfig, time.Now())
	runtime, err := NewRuntime(Config{PublishInterval: "5s", Plant: plantConfig}, plant, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	command := RPCCommand{
		ID:       42,
		DeviceID: "CHILLER-01",
		Method:   "setChilledWaterTemperatureSetpoint",
		Params:   map[string]float64{"setpointC": 8.5},
	}
	first := runtime.applyRPCCommand(command)
	second := runtime.applyRPCCommand(command)
	if first != second || first.BusinessRevision != 2 {
		t.Fatalf("retried RPC changed result: first=%#v second=%#v", first, second)
	}
	if got := plant.Snapshot().Devices["CHILLER-01"]["businessRevision"]; got != uint64(2) {
		t.Fatalf("retried RPC changed plant revision: %v", got)
	}
}
