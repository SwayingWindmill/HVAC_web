package edgedaemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"math"
	"path/filepath"
	"sync"
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

type writableChillerTestAdapter struct {
	mu      sync.Mutex
	comp    edgecontrol.ComponentDescriptor
	chans   []edgecontrol.ChannelDescriptor
	samples map[string]edgecontrol.Sample
	seq     uint64
}

func newWritableChillerTestAdapter(now time.Time, initialLoadPct, initialSupplySp float64) *writableChillerTestAdapter {
	makeDesc := func(compID, chID, unit string, acc edgecontrol.AccessMode) edgecontrol.ChannelDescriptor {
		return edgecontrol.ChannelDescriptor{
			ComponentID:               compID,
			ChannelID:                 chID,
			PointID:                   compID + ":" + chID,
			DataType:                  edgecontrol.DataTypeDouble,
			Access:                    acc,
			Unit:                      unit,
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              edgecontrol.PriorityHigh,
			LocalPersistencePriority:  edgecontrol.PriorityHigh,
			RemotePersistencePriority: edgecontrol.PriorityHigh,
			AggregationPriority:       edgecontrol.PriorityHigh,
			ResendPriority:            edgecontrol.PriorityHigh,
		}
	}
	runDesc := edgecontrol.ChannelDescriptor{
		ComponentID:               "ch-1",
		ChannelID:                 "RunState",
		PointID:                   "ch-1:RunState",
		DataType:                  edgecontrol.DataTypeString,
		Access:                    edgecontrol.AccessReadOnly,
		Category:                  edgecontrol.ChannelCategoryOpenemsType,
		PollPriority:              edgecontrol.PriorityHigh,
		LocalPersistencePriority:  edgecontrol.PriorityHigh,
		RemotePersistencePriority: edgecontrol.PriorityHigh,
		AggregationPriority:       edgecontrol.PriorityHigh,
		ResendPriority:            edgecontrol.PriorityHigh,
	}
	faultDesc := edgecontrol.ChannelDescriptor{
		ComponentID:               "ch-1",
		ChannelID:                 "Fault",
		PointID:                   "ch-1:Fault",
		DataType:                  edgecontrol.DataTypeBoolean,
		Access:                    edgecontrol.AccessReadOnly,
		Category:                  edgecontrol.ChannelCategoryOpenemsType,
		PollPriority:              edgecontrol.PriorityHigh,
		LocalPersistencePriority:  edgecontrol.PriorityHigh,
		RemotePersistencePriority: edgecontrol.PriorityHigh,
		AggregationPriority:       edgecontrol.PriorityHigh,
		ResendPriority:            edgecontrol.PriorityHigh,
	}
	chans := []edgecontrol.ChannelDescriptor{
		makeDesc("ch-1", "SupplySp", "Cel", edgecontrol.AccessReadWrite),
		makeDesc("ch-1", "cooling_load_pct", "%", edgecontrol.AccessReadOnly),
		runDesc,
		faultDesc,
	}
	return &writableChillerTestAdapter{
		comp: edgecontrol.ComponentDescriptor{
			ID:        "ch-1",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "CHILLER_STUB",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		chans: chans,
		samples: map[string]edgecontrol.Sample{
			"ch-1/SupplySp":         {Value: edgecontrol.DoubleValue(initialSupplySp), Quality: edgecontrol.QualityGood, ObservedAt: now, Sequence: 1},
			"ch-1/cooling_load_pct": {Value: edgecontrol.DoubleValue(initialLoadPct), Quality: edgecontrol.QualityGood, ObservedAt: now, Sequence: 1},
			"ch-1/RunState":         {Value: edgecontrol.StringValue("RUNNING"), Quality: edgecontrol.QualityGood, ObservedAt: now, Sequence: 1},
			"ch-1/Fault":            {Value: edgecontrol.BooleanValue(false), Quality: edgecontrol.QualityGood, ObservedAt: now, Sequence: 1},
		},
		seq: 1,
	}
}

func (a *writableChillerTestAdapter) Component() edgecontrol.ComponentDescriptor { return a.comp }
func (a *writableChillerTestAdapter) Channels() []edgecontrol.ChannelDescriptor  { return a.chans }
func (a *writableChillerTestAdapter) Poll(_ context.Context, _ time.Time) ([]edgecontrol.ChannelUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]edgecontrol.ChannelUpdate, 0, len(a.samples))
	for _, ch := range a.chans {
		addr := ch.Address()
		if s, ok := a.samples[addr]; ok {
			out = append(out, edgecontrol.ChannelUpdate{Address: addr, Sample: s})
		}
	}
	return out, nil
}
func (a *writableChillerTestAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	res := make([]edgecontrol.DeviceWriteResult, 0, len(decisions))
	for _, d := range decisions {
		if d.Address == "ch-1/SupplySp" {
			val := d.Requested
			if d.Effective != nil {
				val = *d.Effective
			}
			a.seq++
			a.samples[d.Address] = edgecontrol.Sample{
				Value:      val,
				Quality:    edgecontrol.QualityGood,
				ObservedAt: time.Now().UTC(),
				Sequence:   a.seq,
			}
			res = append(res, edgecontrol.DeviceWriteResult{Address: d.Address, Success: true})
			continue
		}
		res = append(res, edgecontrol.DeviceWriteResult{Address: d.Address, Success: false, Code: "READ_ONLY"})
	}
	return res, nil
}
func (a *writableChillerTestAdapter) SupplySp() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.samples["ch-1/SupplySp"].Value.Double
}

func TestEdgeModelManagerActiveControlSetpointBehavior(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "test-model-key"
	mgr := NewEdgeModelManager(map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)}, nil)

	if sp, ok := mgr.ActiveControlSetpointC(); ok || sp != 0 {
		t.Fatalf("expected (0, false) before EvaluateCycle, got (%v, %v)", sp, ok)
	}

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	adapter := newWritableChillerTestAdapter(now, 50.0, 7.0)
	if err := host.RegisterAdapter(adapter); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatal(err)
	}
	res, err := host.RunCycle(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	minC, maxC := 6.0, 14.0
	shadowBundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000301",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-sp",
		Version:            "1.0.0-shadow",
		RolloutMode:        edgecontrol.EdgeModelRolloutShadow,
		MaxShadowDeviation: 2.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			COPCoefficients:        []float64{10.0, -2.0},
			SupplyTempSetpointMinC: &minC,
			SupplyTempSetpointMaxC: &maxC,
		},
		UpdatedAt: now,
	}
	if err := edgecontrol.SignEdgeModelBundleEd25519(&shadowBundle, keyID, priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{shadowBundle}); err != nil {
		t.Fatalf("ApplyBundles shadow: %v", err)
	}
	mgr.EvaluateCycle(now, res.Cycle.Image)
	if sp, ok := mgr.ActiveControlSetpointC(); ok {
		t.Fatalf("SHADOW bundle must never expose ActiveControlSetpointC, got %v", sp)
	}
	if sp, ok := mgr.ActiveControlSetpointCForImage(res.Cycle.Image); ok {
		t.Fatalf("SHADOW bundle must never expose ActiveControlSetpointCForImage, got %v", sp)
	}

	// Install ACTIVE EDGE_CONTROL bundle (10.0 - 3.0 * (50/100) = 8.5°C).
	activeBundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000302",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-sp",
		Version:            "1.0.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutActive,
		MaxShadowDeviation: 2.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			COPCoefficients:        []float64{10.0, -3.0},
			SupplyTempSetpointMinC: &minC,
			SupplyTempSetpointMaxC: &maxC,
		},
		UpdatedAt: now,
	}
	if err := edgecontrol.SignEdgeModelBundleEd25519(&activeBundle, keyID, priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{activeBundle}); err != nil {
		t.Fatalf("ApplyBundles active: %v", err)
	}
	mgr.EvaluateCycle(now, res.Cycle.Image)
	if sp, ok := mgr.ActiveControlSetpointC(); !ok || math.Abs(sp-8.5) > 1e-6 {
		t.Fatalf("ActiveControlSetpointC = (%v, %v), want (8.5, true)", sp, ok)
	}

	// Replace with an ACTIVE bundle whose COPCoefficients evaluate out of [4, 18] bounds -> rolled back!
	badActive := activeBundle.Clone()
	badActive.BundleID = "019b0000-1000-7000-8000-000000000303"
	badActive.Version = "1.1.0-bad"
	badActive.Hyperparams.COPCoefficients = []float64{2.0, 0.0}
	badActive = signBundleForTest(t, keyID, priv, badActive)
	if _, _, err := mgr.ApplyBundles([]edgecontrol.EdgeModelBundle{badActive}); err != nil {
		t.Fatalf("ApplyBundles badActive: %v", err)
	}
	mgr.EvaluateCycle(now.Add(time.Second), res.Cycle.Image)
	if sp, ok := mgr.ActiveControlSetpointC(); !ok || math.Abs(sp-8.5) > 1e-6 {
		t.Fatalf("expected rollback to previous ACTIVE setpoint 8.5°C, got (%v, %v)", sp, ok)
	}
}

func TestDaemonModelSetpointWiresIntoEnergyScheduleController(t *testing.T) {
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	curTime := day.Add(9 * time.Hour)
	stateDir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "daemon-sched-key"
	const gwID = "0193f000-2000-7000-8000-000000000095"

	schedule := &edgecontrol.EdgeEnergySchedule{
		ScheduleID:                "sched-ai-model",
		Version:                   1,
		IssuedAt:                  day,
		ValidFrom:                 day,
		ValidUntil:                day.Add(24 * time.Hour),
		ContinueOfflineWhileValid: true,
		Slots: []edgecontrol.EnergyScheduleSlot{{
			SlotID:      "ai-eco",
			StartMinute: 8 * 60,
			EndMinute:   12 * 60,
			Mode:        edgecontrol.ScheduleModeEco,
		}},
	}

	d, err := NewDaemon(Config{
		GatewayID:       gwID,
		StateDir:        stateDir,
		CycleIntervalMS: 1000,
		SpoolMaxBytes:   4 << 20,
		RuntimeVersion:  "1.0.0-test",
		HardwareModel:   "EG8200",
		TargetOS:        "linux",
		TargetArch:      "arm",
		OTA:             OTAConfig{Dir: filepath.Join(stateDir, "ota"), TrustedPublicKeys: map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)}},
		EnergySchedule:  schedule,
	}, DaemonOptions{
		Now: func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	defer func() { _ = d.Close() }()

	chiller := newWritableChillerTestAdapter(curTime, 80.0, 7.0)
	if err := d.Host().RegisterAdapter(chiller); err != nil {
		t.Fatal(err)
	}
	d.ScheduleController().UpdateTargets([]edgecontrol.ChillerFallbackTarget{{
		ComponentID:        "ch-1",
		SupplySetpointAddr: "ch-1/SupplySp",
		RunStateAddr:       "ch-1/RunState",
		FaultAddr:          "ch-1/Fault",
	}}, nil, nil)

	// No active model yet -> setpoint remains untouched on initial StepCycle.
	if _, err := d.StepCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sp := chiller.SupplySp(); math.Abs(sp-7.0) > 1e-6 {
		t.Fatalf("expected initial SupplySp 7.0 without active model, got %v", sp)
	}

	minC, maxC := 5.0, 15.0
	bundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000310",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-sp",
		Version:            "2.0.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutActive,
		MaxShadowDeviation: 2.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			COPCoefficients:        []float64{12.0, -4.0}, // 12.0 - 4.0 * 0.8 = 8.8°C
			SupplyTempSetpointMinC: &minC,
			SupplyTempSetpointMaxC: &maxC,
		},
		UpdatedAt: curTime,
	}
	if err := edgecontrol.SignEdgeModelBundleEd25519(&bundle, keyID, priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.ModelManager().ApplyBundles([]edgecontrol.EdgeModelBundle{bundle}); err != nil {
		t.Fatal(err)
	}

	curTime = curTime.Add(time.Second)
	if _, err := d.StepCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sp := chiller.SupplySp(); math.Abs(sp-8.8) > 1e-6 {
		t.Fatalf("expected EnergyScheduleController to drive SupplySp from active EdgeModel (8.8°C), got %v", sp)
	}
}
