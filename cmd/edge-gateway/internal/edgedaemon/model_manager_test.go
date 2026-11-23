package edgedaemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func signBundleForTest(t *testing.T, keyID string, priv ed25519.PrivateKey, b edgecontrol.EdgeModelBundle) edgecontrol.EdgeModelBundle {
	t.Helper()
	sha, err := edgecontrol.ComputeEdgeModelHyperparamsSHA256(b.Hyperparams)
	if err != nil {
		t.Fatal(err)
	}
	b.ArtifactSHA256 = sha
	if err := edgecontrol.SignEdgeModelBundleEd25519(&b, keyID, priv); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEdgeModelManagerSignatureVerificationShadowEvaluationAndAutoRollback(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{
		"edge-k1": base64.StdEncoding.EncodeToString(pub),
	}
	mgr := NewEdgeModelManager(keys, nil)

	min1 := 6.5
	max1 := 10.5
	activeV1 := signBundleForTest(t, "edge-k1", priv, edgecontrol.EdgeModelBundle{
		BundleID:           "019a0000-0001-7000-8000-000000000001",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "1.0.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutActive,
		MaxShadowDeviation: 1.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			SupplyTempSetpointMinC: &min1,
			SupplyTempSetpointMaxC: &max1,
		},
	})
	if _, drifts, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{activeV1}); err != nil || len(drifts) != 0 {
		t.Fatalf("ApplyBundles valid active failed: drifts=%v err=%v", drifts, err)
	}

	// Unsigned or forged bundle must be rejected without mutating running v1.
	forged := activeV1.Clone()
	forged.Version = "1.1.0"
	forged.Signature = ""
	if _, drifts, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{forged}); err == nil || len(drifts) != 1 {
		t.Fatalf("expected signature rejection, got drifts=%v err=%v", drifts, err)
	}
	cur := mgr.ActiveBundles()
	if len(cur) != 1 || cur[0].Version != "1.0.0" {
		t.Fatalf("rejected update mutated running bundles: %+v", cur)
	}

	// Load a SHADOW candidate within deviation tolerance, then another that breaches MaxShadowDeviation.
	minS1 := 7.0
	maxS1 := 10.6
	shadowOK := signBundleForTest(t, "edge-k1", priv, edgecontrol.EdgeModelBundle{
		BundleID:           "019a0000-0001-7000-8000-000000000002",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "1.1.0-shadow",
		RolloutMode:        edgecontrol.EdgeModelRolloutShadow,
		MaxShadowDeviation: 1.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			SupplyTempSetpointMinC: &minS1,
			SupplyTempSetpointMaxC: &maxS1,
		},
	})
	if _, _, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{activeV1, shadowOK}); err != nil {
		t.Fatalf("apply active+shadow failed: %v", err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	st1 := mgr.EvaluateCycle(now, edgecontrol.ProcessImage{})
	if len(st1) != 1 || st1[0].RollbackCount != 0 || st1[0].ShadowSampleCount != 1 || st1[0].ShadowBundleID != shadowOK.BundleID {
		t.Fatalf("unexpected non-breaching status: %+v", st1)
	}

	// Shadow candidate with > MaxShadowDeviation automatically rolls back!
	minBad := 13.0
	maxBad := 15.0
	shadowBreach := signBundleForTest(t, "edge-k1", priv, edgecontrol.EdgeModelBundle{
		BundleID:           "019a0000-0001-7000-8000-000000000003",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "1.2.0-breach",
		RolloutMode:        edgecontrol.EdgeModelRolloutShadow,
		MaxShadowDeviation: 1.5,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			SupplyTempSetpointMinC: &minBad,
			SupplyTempSetpointMaxC: &maxBad,
		},
	})
	if _, _, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{activeV1, shadowBreach}); err != nil {
		t.Fatalf("apply active+breaching shadow failed: %v", err)
	}
	st2 := mgr.EvaluateCycle(now.Add(time.Second), edgecontrol.ProcessImage{})
	if len(st2) != 1 || st2[0].RollbackCount != 1 || st2[0].ShadowBundleID != "" || st2[0].ActiveBundleID != activeV1.BundleID {
		t.Fatalf("expected automatic shadow rollback keeping activeV1, got %+v", st2)
	}
}

func TestShadowManagerHotReloadsAndRejectsInvalidEdgeModels(t *testing.T) {
	now := time.Unix(1_760_100_000, 0).UTC()
	dir := t.TempDir()
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	reg := NewProtocolBridgeRegistry(map[string]SerialBusConfig{
		"/dev/ttyAS3": {Speed: 9600, DataBits: 8, Parity: "N", StopBits: 1},
	})
	t.Cleanup(func() { _ = reg.Close() })

	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{Policy: edgecontrol.DefaultAutonomyPolicyConfig()})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{Policy: edgecontrol.DefaultAutonomyPolicyConfig()})
	if err != nil {
		t.Fatal(err)
	}
	ruleEngine := edgecontrol.NewEdgeRuleController(edgecontrol.EdgeRuleControllerConfig{})
	if err := host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 20, Controller: fallback},
		{Priority: 30, Controller: ruleEngine},
	}); err != nil {
		t.Fatal(err)
	}

	gwID := "0193f000-2000-7000-8000-000000000040"
	mgr := NewShadowManager(gwID, dir, host, reg, interlock, fallback, ruleEngine, nil)
	mgr.now = func() time.Time { return now }
	mgr.AttachModelManager(NewEdgeModelManager(nil, nil))

	minC := 7.0
	maxC := 11.0
	hp := edgecontrol.EdgeModelHyperparams{SupplyTempSetpointMinC: &minC, SupplyTempSetpointMaxC: &maxC}
	sha, err := edgecontrol.ComputeEdgeModelHyperparamsSHA256(hp)
	if err != nil {
		t.Fatal(err)
	}
	bundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019a0000-0001-7000-8000-000000000010",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "2.0.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutActive,
		ArtifactSHA256:     sha,
		MaxShadowDeviation: 1.5,
		Hyperparams:        hp,
	}

	desired := GatewayDesiredConfig{
		DesiredRevision: 1,
		EdgeModels:      []edgecontrol.EdgeModelBundle{bundle},
	}
	batchBytes, _, err := edgecontrol.EncodeShadowChangeBatch(gwID, 1, desired, now)
	if err != nil {
		t.Fatal(err)
	}
	ackBytes, err := mgr.HandleMQTTChangeBatch(batchBytes)
	if err != nil {
		t.Fatalf("HandleMQTTChangeBatch: %v", err)
	}
	_, ack, err := edgecontrol.DecodeShadowChangeAck(ackBytes, gwID)
	if err != nil || ack.Status != edgecontrol.ShadowAckStatusAcked {
		t.Fatalf("expected ACKED, got %+v err=%v", ack, err)
	}

	// Reject out-of-bounds update while keeping 2.0.0 running.
	badMin := 1.0
	badHP := edgecontrol.EdgeModelHyperparams{SupplyTempSetpointMinC: &badMin}
	badSHA, _ := edgecontrol.ComputeEdgeModelHyperparamsSHA256(badHP)
	report, _, err := mgr.ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 2,
		EdgeModels: []edgecontrol.EdgeModelBundle{{
			BundleID:           "019a0000-0001-7000-8000-000000000011",
			Domain:             edgecontrol.EdgeModelDomainControl,
			ModelKey:           "chiller-cop-opt",
			Version:            "2.1.0",
			RolloutMode:        edgecontrol.EdgeModelRolloutActive,
			ArtifactSHA256:     badSHA,
			MaxShadowDeviation: 1.0,
			Hyperparams:        badHP,
		}},
	})
	if err != nil || report.AppliedStatus != shadowStatusRejected || len(report.EdgeModels) != 1 || report.EdgeModels[0].Version != "2.0.0" {
		rawReport, _ := json.Marshal(report)
		t.Fatalf("expected REJECTED preserving 2.0.0, got %s err=%v", rawReport, err)
	}
}
