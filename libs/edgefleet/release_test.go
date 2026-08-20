package edgefleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

func TestHandshakeNegotiatesVersionCapabilitiesAndMaxPayload(t *testing.T) {
	result := NegotiateHandshake(HandshakeRequest{
		EdgeID: "edge-a", RuntimeVersion: "2.3.0", ProtocolSchemaVersion: 1,
		Capabilities: []string{"config.v1", "registry.v1", "ota.v1"}, MaxPayloadBytes: 2 << 20, CredentialRevision: 4,
	}, HandshakePolicy{
		ProtocolSchemaVersion: 1, MinRuntimeVersion: "2.0.0", MaxRuntimeVersion: "2.9.9",
		RequiredCapabilities: []string{"registry.v1", "config.v1"}, MaxPayloadBytes: 1 << 20,
	})
	if result.Status != HandshakeAccepted || result.NegotiatedMaxBytes != 1<<20 {
		t.Fatalf("handshake=%+v", result)
	}
}

func TestSignedReleaseHealthFailureRollsBackToPreviousRelease(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := OpenReplica(t.TempDir(), RuntimeDescriptor{RuntimeVersion: "2.3.0", ProtocolSchemaVersion: 1, Capabilities: []string{"config.v1", "rollback.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	replica.SetTrustedKey("release-key", publicKey)

	activate := func(id string, configRevision uint64, healthErr error) ReleaseActivationResult {
		t.Helper()
		release, signErr := SignEdgeRelease(EdgeReleasePayload{
			ReleaseID: id, RuntimeRevision: "runtime-2.3", ManifestRevision: "manifest-1", RegistryProjectionRevision: configRevision,
			DriverRevision: "driver-1", SafetyPolicyRevision: "safety-1", DesiredConfigRevision: configRevision,
			MinRuntimeVersion: "2.0.0", MaxRuntimeVersion: "2.9.9", RequiredCapabilities: []string{"config.v1", "rollback.v1"},
		}, "release-key", privateKey)
		if signErr != nil {
			t.Fatal(signErr)
		}
		meta, chunks, buildErr := BuildSnapshot(configRevision, configRevision, id, []ProjectionItem{
			cloudProjection(OwnerRegistry, "device-a", configRevision, `{"configRevision":`+fmt.Sprint(configRevision)+`}`),
		}, 1)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err := replica.StageRelease(release, func(EdgeReleasePayload) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := replica.BeginSnapshot(meta); err != nil {
			t.Fatal(err)
		}
		if err := replica.StageSnapshotChunk(chunks[0]); err != nil {
			t.Fatal(err)
		}
		result, err := replica.ActivateSnapshotRelease(meta, release, sha256Hex([]byte(id+"-manifest")), func(EdgeReleasePayload) error { return healthErr })
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	if result := activate("release-a", 1, nil); result.RolledBack || result.ActiveReleaseID != "release-a" {
		t.Fatalf("initial activation=%+v", result)
	}
	before := replica.ObservedState()
	if result := activate("release-b", 2, errors.New("readback unhealthy")); !result.RolledBack || result.ActiveReleaseID != "release-a" || result.PreviousReleaseID != "release-b" {
		t.Fatalf("rollback=%+v", result)
	}
	after := replica.ObservedState()
	if after.ReportedConfigRevision != before.ReportedConfigRevision || after.ManifestDigest != before.ManifestDigest {
		t.Fatalf("rollback did not restore prior config/manifest: before=%+v after=%+v", before, after)
	}
}

func TestOTACampaignSupportsCanaryWavesAndPause(t *testing.T) {
	campaign, err := NewOTACampaign("campaign-a", "artifact-a", []int{1, 10, 30, 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := campaign.Start(); err != nil {
		t.Fatal(err)
	}
	if err := campaign.AdvanceWave(); err != nil || campaign.Waves[campaign.WaveIndex] != 10 {
		t.Fatalf("advance err=%v campaign=%+v", err, campaign)
	}
	if err := campaign.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := campaign.Resume(); err != nil {
		t.Fatal(err)
	}
}
