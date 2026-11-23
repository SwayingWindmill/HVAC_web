package edgecontrol

import "testing"
import "time"

func TestFactoryResetEnvelopeRoundTripAndValidation(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	gatewayID := "0193f000-2000-7000-8000-000000000010"
	resetID := "0193f000-2000-7000-8000-000000000020"
	transferID := "0193f000-2000-7000-8000-000000000030"

	if got := LifecycleDownlinkTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/down/lifecycle" {
		t.Fatalf("LifecycleDownlinkTopic = %q", got)
	}
	if got := LifecycleUplinkTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/up/lifecycle" {
		t.Fatalf("LifecycleUplinkTopic = %q", got)
	}

	reset := FactoryResetPayload{
		ResetID:           resetID,
		TransferID:        transferID,
		WipeCredentials:   true,
		WipeConfigShadow:  true,
		WipeOfflineBuffer: true,
		Reason:            "Device ownership transferred across sites",
		IssuedAt:          now.UnixMilli(),
	}
	wire, err := EncodeFactoryReset(gatewayID, reset, now)
	if err != nil {
		t.Fatalf("EncodeFactoryReset failed: %v", err)
	}
	env, decoded, err := DecodeFactoryReset(wire, gatewayID)
	if err != nil {
		t.Fatalf("DecodeFactoryReset failed: %v", err)
	}
	if env.Type != ShadowReplicationTypeFactoryReset || decoded.ResetID != resetID || decoded.TransferID != transferID || !decoded.WipeCredentials {
		t.Fatalf("unexpected decoded reset: env=%+v reset=%+v", env, decoded)
	}
	if _, _, err := DecodeFactoryReset(wire, "0193f000-2000-7000-8000-000000000099"); err == nil {
		t.Fatal("expected gateway ID mismatch to fail FACTORY_RESET decode")
	}

	noWipe := reset
	noWipe.WipeCredentials = false
	noWipe.WipeConfigShadow = false
	noWipe.WipeOfflineBuffer = false
	if _, err := EncodeFactoryReset(gatewayID, noWipe, now); err == nil {
		t.Fatal("expected zero wipe flags to fail FACTORY_RESET validation")
	}

	ack := FactoryResetAckPayload{
		ResetID:        resetID,
		Status:         FactoryResetAckStatusWiped,
		WipedArtifacts: []string{"credentials", "config_shadow", "offline_buffer"},
		OccurredAt:     now.Add(2 * time.Second).UnixMilli(),
	}
	ackWire, err := EncodeFactoryResetAck(gatewayID, ack, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("EncodeFactoryResetAck failed: %v", err)
	}
	ackEnv, decodedAck, err := DecodeFactoryResetAck(ackWire, gatewayID)
	if err != nil {
		t.Fatalf("DecodeFactoryResetAck failed: %v", err)
	}
	if ackEnv.Type != ShadowReplicationTypeFactoryResetAck || decodedAck.Status != FactoryResetAckStatusWiped || len(decodedAck.WipedArtifacts) != 3 {
		t.Fatalf("unexpected decoded ack: env=%+v ack=%+v", ackEnv, decodedAck)
	}

	badAck := ack
	badAck.WipedArtifacts = []string{"credentials", "credentials"}
	if _, err := EncodeFactoryResetAck(gatewayID, badAck, now); err == nil {
		t.Fatal("expected duplicate wipedArtifacts to fail validation")
	}
}
