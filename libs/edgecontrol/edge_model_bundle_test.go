package edgecontrol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func TestValidateEdgeModelBundleSignatureAndBounds(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]string{
		"edge-mlops-v1": base64.StdEncoding.EncodeToString(pub),
	}
	bundle := EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000001",
		Domain:             EdgeModelDomainControl,
		ModelKey:           "chiller-pid-v2",
		Version:            "1.2.0",
		RolloutMode:        EdgeModelRolloutShadow,
		MaxShadowDeviation: 2.5,
		Hyperparams: EdgeModelHyperparams{
			COPCoefficients:        []float64{3.2, 0.14, -0.02},
			ChillerStartPowerKW:    f64(15),
			ChillerStopPowerKW:     f64(5),
			SupplyTempSetpointMinC: f64(6),
			SupplyTempSetpointMaxC: f64(12),
			PumpFrequencyMinHz:     f64(30),
			PumpFrequencyMaxHz:     f64(50),
			PIDKp:                  f64(1.4),
			PIDKi:                  f64(0.25),
			TargetPointCodes:       []string{"chw_supply_temp_sp", "chw_pump_freq"},
		},
		UpdatedAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC),
	}
	if err := SignEdgeModelBundleEd25519(&bundle, "edge-mlops-v1", priv); err != nil {
		t.Fatalf("SignEdgeModelBundleEd25519: %v", err)
	}
	if err := ValidateEdgeModelBundle(bundle, trusted, true); err != nil {
		t.Fatalf("ValidateEdgeModelBundle valid: %v", err)
	}

	tampered := bundle.Clone()
	tampered.Hyperparams.PIDKp = f64(1.8)
	if err := ValidateEdgeModelBundle(tampered, trusted, true); err == nil {
		t.Fatal("expected tampered hyperparams to fail validation")
	}

	tampered.ArtifactSHA256, _ = ComputeEdgeModelHyperparamsSHA256(tampered.Hyperparams)
	if err := ValidateEdgeModelBundle(tampered, trusted, true); !errors.Is(err, ErrEdgeModelSignature) {
		t.Fatalf("expected ErrEdgeModelSignature, got %v", err)
	}

	badBounds := bundle.Clone()
	badBounds.Hyperparams.SupplyTempSetpointMinC = f64(1.5)
	badBounds.ArtifactSHA256, _ = ComputeEdgeModelHyperparamsSHA256(badBounds.Hyperparams)
	if err := ValidateEdgeModelBundle(badBounds, trusted, false); !errors.Is(err, ErrEdgeModelValidation) {
		t.Fatalf("expected ErrEdgeModelValidation for low chilled water setpoint, got %v", err)
	}

	hardSafety := bundle.Clone()
	hardSafety.Hyperparams.TargetPointCodes = []string{"plc_estop_trip"}
	hardSafety.ArtifactSHA256, _ = ComputeEdgeModelHyperparamsSHA256(hardSafety.Hyperparams)
	if err := ValidateEdgeModelBundle(hardSafety, trusted, false); err == nil || !strings.Contains(err.Error(), ReasonHardRealtimeSafetyBoundary) {
		t.Fatalf("expected %s rejection, got %v", ReasonHardRealtimeSafetyBoundary, err)
	}
}
