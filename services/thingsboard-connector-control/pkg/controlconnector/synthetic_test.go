package controlconnector

import (
	"context"
	"errors"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestSyntheticConnectorIsIdempotent(t *testing.T) {
	connector := NewSynthetic(ModeVerifiedSuccess)
	envelope := validEnvelope(1)
	first, err := connector.Execute(context.Background(), envelope)
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}
	second, err := connector.Execute(context.Background(), envelope)
	if err != nil {
		t.Fatalf("second execute failed: %v", err)
	}
	if first != second {
		t.Fatalf("idempotent result drifted: %#v != %#v", first, second)
	}
}

func TestSyntheticConnectorRejectsPayloadMutation(t *testing.T) {
	connector := NewSynthetic(ModeVerifiedSuccess)
	envelope := validEnvelope(1)
	if _, err := connector.Execute(context.Background(), envelope); err != nil {
		t.Fatalf("first execute failed: %v", err)
	}
	envelope.PayloadHash = "different"
	if _, err := connector.Execute(context.Background(), envelope); !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("expected payload mismatch, got %v", err)
	}
}

func TestSyntheticConnectorRejectsOldFence(t *testing.T) {
	connector := NewSynthetic(ModeVerifiedSuccess)
	newer := validEnvelope(2)
	newer.AttemptID = "attempt-2"
	if _, err := connector.Execute(context.Background(), newer); err != nil {
		t.Fatalf("newer execute failed: %v", err)
	}
	older := validEnvelope(1)
	if _, err := connector.Execute(context.Background(), older); !errors.Is(err, ErrOldFence) {
		t.Fatalf("expected old fence rejection, got %v", err)
	}
}

func validEnvelope(fence uint64) commandmodel.DispatchEnvelope {
	return commandmodel.DispatchEnvelope{
		CommandID:             "command-1",
		AttemptID:             "attempt-1",
		OrganizationID:        "org-1",
		SiteID:                "site-1",
		DeviceID:              "device-1",
		Capability:            commandmodel.CapabilitySetTemperatureSetpoint,
		CapabilityRevision:    "capability:set-temperature-setpoint:v1",
		SetpointC:             24,
		PayloadHash:           "payload-hash",
		ExecutionFence:        fence,
		DeviceCommandSequence: 1,
	}
}
