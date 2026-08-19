package simulator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func loadGeneratedCentralPlantConfig(t *testing.T) Config {
	t.Helper()
	file, err := os.Open("../../configs/central-plant.local.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, err := DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestGeneratedCentralPlantBuildsCapabilityDrivenEdgeDrivers(t *testing.T) {
	config := loadGeneratedCentralPlantConfig(t)
	plant := NewPlant(config.Plant, time.Unix(2000, 0).UTC())
	runtime := edgecontrol.NewRuntime()
	capabilities, err := edgecontrol.NewStandardCapabilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	components, err := edgecontrol.NewComponentRegistry(runtime, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	host, err := edgecontrol.NewDirectDeviceHost(runtime, components)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := newSimulatedDeviceAdapters(config, plant)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 7 {
		t.Fatalf("expected 7 simulated Device Adapters, got %d", len(adapters))
	}
	for _, adapter := range adapters {
		if err := host.RegisterAdapter(adapter); err != nil {
			t.Fatalf("register %s: %v", adapter.Component().ID, err)
		}
	}
	manifest, err := components.Manifest(config.GatewayID, "central-plant:v1", time.Unix(2001, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Components) != 7 || len(manifest.Channels) != 65 || len(manifest.CapabilityProfiles) != 6 {
		t.Fatalf("unexpected Edge Manifest sizes: components=%d channels=%d profiles=%d", len(manifest.Components), len(manifest.Channels), len(manifest.CapabilityProfiles))
	}

	plant.Tick(time.Second)
	results := host.PollOnce(context.Background(), time.Unix(2001, 0).UTC())
	updates := 0
	for _, result := range results {
		if result.Error != nil {
			t.Fatalf("poll %s: %v", result.AdapterID, result.Error)
		}
		updates += result.Updates
	}
	if updates != 48 {
		t.Fatalf("expected 48 observed Channel updates, got %d", updates)
	}
	image := runtime.SwitchProcessImage(time.Unix(2001, 0).UTC())
	available := 0
	for _, channel := range image.Channels() {
		if channel.HasValue {
			available++
		}
	}
	if available != 48 {
		t.Fatalf("expected 48 values in Process Image, got %d", available)
	}
}
