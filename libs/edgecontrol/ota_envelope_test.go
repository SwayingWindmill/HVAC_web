package edgecontrol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestOTAEnvelopeRoundTripAndValidation(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	gatewayID := "0193f000-2000-7000-8000-000000000010"
	campaignID := "0193f000-2000-7000-8000-000000000011"
	taskID := "0193f000-2000-7000-8000-000000000012"
	packageID := "0193f000-2000-7000-8000-000000000013"

	if got := OTADownlinkTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/down/ota" {
		t.Fatalf("OTADownlinkTopic = %q", got)
	}
	if got := OTAUplinkTopic(gatewayID); got != "hvac/v1/"+gatewayID+"/up/ota" {
		t.Fatalf("OTAUplinkTopic = %q", got)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("edge-gateway-binary-v1.2.0"))
	shaHex := hex.EncodeToString(sum[:])

	sigB64, err := SignOTAPackageEd25519(priv, packageID, "1.2.0", OTAComponentEdgeGateway, "linux", "arm", shaHex, 1024)
	if err != nil {
		t.Fatalf("sign OTA package: %v", err)
	}

	stage := OTAStagePayload{
		CampaignID:            campaignID,
		TaskID:                taskID,
		PackageID:             packageID,
		Version:               "1.2.0",
		Component:             OTAComponentEdgeGateway,
		TargetOS:              "linux",
		TargetArch:            "armv7l",
		HardwareModels:        []string{"EG8200"},
		MinCompatibleVersion:  "1.0.0",
		DownloadURL:           "https://hvac.example.com/gateway/v1/ota/packages/" + packageID + "/download",
		FileSizeBytes:         1024,
		SHA256:                shaHex,
		SignatureAlg:          OTASignatureAlgEd25519,
		SignatureB64:          sigB64,
		SigningKeyID:          "release-key-2026",
		HealthCheckTimeoutSec: 60,
		Action:                OTAActionApply,
	}

	wire, err := EncodeOTAStage(gatewayID, stage, now)
	if err != nil {
		t.Fatalf("EncodeOTAStage failed: %v", err)
	}
	env, decoded, err := DecodeOTAStage(wire, gatewayID)
	if err != nil {
		t.Fatalf("DecodeOTAStage failed: %v", err)
	}
	if env.Type != ShadowReplicationTypeOTAStage || decoded.Version != "1.2.0" || decoded.SHA256 != shaHex {
		t.Fatalf("unexpected decoded stage: env=%+v stage=%+v", env, decoded)
	}

	if err := VerifyOTASignatureEd25519(pub, decoded.PackageID, decoded.Version, decoded.Component, decoded.TargetOS, decoded.TargetArch, decoded.SHA256, decoded.FileSizeBytes, decoded.SignatureB64); err != nil {
		t.Fatalf("expected signature verification to pass across canonicalized arch: %v", err)
	}
	if err := VerifyOTASignatureEd25519(pub, decoded.PackageID, "1.2.1", decoded.Component, decoded.TargetOS, decoded.TargetArch, decoded.SHA256, decoded.FileSizeBytes, decoded.SignatureB64); err == nil {
		t.Fatal("expected modified version to fail signature verification")
	}

	resWire, err := EncodeOTAResult(gatewayID, OTAResultPayload{
		CampaignID:     campaignID,
		TaskID:         taskID,
		PackageID:      packageID,
		Stage:          OTAStageHealthChecking,
		ProgressPct:    95,
		CurrentVersion: "1.1.0",
		TargetVersion:  "1.2.0",
		OccurredAt:     now.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatalf("EncodeOTAResult failed: %v", err)
	}
	resEnv, resDecoded, err := DecodeOTAResult(resWire, gatewayID)
	if err != nil {
		t.Fatalf("DecodeOTAResult failed: %v", err)
	}
	if resEnv.Type != ShadowReplicationTypeOTAResult || resDecoded.Stage != OTAStageHealthChecking || resDecoded.ProgressPct != 95 {
		t.Fatalf("unexpected decoded result: %+v", resDecoded)
	}

	if _, _, err := DecodeOTAStage(wire, "0193f000-2000-7000-8000-000000000099"); err == nil {
		t.Fatal("expected gateway ID mismatch to fail decoding")
	}
	badStage := stage
	badStage.SHA256 = strings.Repeat("z", 64)
	if _, err := EncodeOTAStage(gatewayID, badStage, now); err == nil {
		t.Fatal("expected invalid SHA256 hex to fail validation")
	}
}

func TestNormalizeTargetArchAndCompareSemver(t *testing.T) {
	if NormalizeTargetArch("arm") != "armv7l" || NormalizeTargetArch("ARMV7L") != "armv7l" {
		t.Fatal("arm/ARMV7L should normalize to armv7l")
	}
	if NormalizeTargetArch("aarch64") != "arm64" || NormalizeTargetArch("x86_64") != "amd64" {
		t.Fatal("aarch64/x86_64 normalization mismatch")
	}

	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "1.2.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.10.0", "1.2.9", 1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0-rc.2", "1.2.0-rc.10", -1},
		{"2.0.0+build1", "2.0.0+build2", 0},
	}
	for _, tc := range cases {
		got, err := CompareSemver(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("CompareSemver(%q, %q) = (%d, %v), want (%d, nil)", tc.a, tc.b, got, err, tc.want)
		}
	}
}
