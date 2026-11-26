package edgedaemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type mirrorPlantModbus struct {
	mu      sync.Mutex
	inner   *fakePlantModbus
	concUID uint8
	concRaw uint16
	failUID uint8
	errOn   bool
}

func (m *mirrorPlantModbus) ReadCoils(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	m.mu.Lock()
	fail := m.errOn && unitID == m.failUID
	m.mu.Unlock()
	if fail {
		return nil, errors.New("simulated RS485 timeout")
	}
	return m.inner.ReadCoils(ctx, unitID, address, quantity)
}

func (m *mirrorPlantModbus) ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	m.mu.Lock()
	fail := m.errOn && unitID == m.failUID
	concUID := m.concUID
	concRaw := m.concRaw
	m.mu.Unlock()
	if fail {
		return nil, errors.New("simulated RS485 timeout")
	}
	if concUID != 0 && unitID == concUID {
		out := make([]uint16, quantity)
		for i := range out {
			out[i] = concRaw
		}
		return out, nil
	}
	return m.inner.ReadHoldingRegisters(ctx, unitID, address, quantity)
}

func (m *mirrorPlantModbus) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	m.mu.Lock()
	fail := m.errOn && unitID == m.failUID
	concUID := m.concUID
	concRaw := m.concRaw
	m.mu.Unlock()
	if fail {
		return nil, errors.New("simulated RS485 timeout")
	}
	if concUID != 0 && unitID == concUID {
		out := make([]uint16, quantity)
		for i := range out {
			out[i] = concRaw
		}
		return out, nil
	}
	return m.inner.ReadInputRegisters(ctx, unitID, address, quantity)
}

func (m *mirrorPlantModbus) setConcRaw(v uint16) {
	m.mu.Lock()
	m.concRaw = v
	m.mu.Unlock()
}

func (m *mirrorPlantModbus) setFail(uid uint8, on bool) {
	m.mu.Lock()
	m.failUID = uid
	m.errOn = on
	m.mu.Unlock()
}

func TestFieldPlantMirrorFeedsHostWithoutDuplicateRawTelemetry(t *testing.T) {
	plantPath := filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.edge-plant.json")
	plantCfg, err := LoadFieldPlantConfigFile(plantPath)
	if err != nil {
		t.Fatalf("LoadFieldPlantConfigFile: %v", err)
	}
	plantCfg.ApplyDefaults()
	if err := plantCfg.Validate(); err != nil {
		t.Fatalf("Validate songyaxuan.edge-plant.json: %v", err)
	}
	if plantCfg.Concentrator == nil || len(plantCfg.Concentrator.ActiveChannels) == 0 || plantCfg.Meters == nil || len(plantCfg.Meters.Devices) == 0 {
		t.Fatalf("songyaxuan.edge-plant.json missing concentrator or meter definitions")
	}

	tempDev := plantCfg.Concentrator.TempDeviceKey
	tempPt := fmt.Sprintf("t%d", plantCfg.Concentrator.ActiveChannels[0])
	tempAddr := tempDev + "/" + tempPt
	aggDev := plantCfg.Concentrator.VoltageDeviceKey
	aggPt := fmt.Sprintf("v%d", plantCfg.Concentrator.ActiveChannels[0])
	if len(plantCfg.Concentrator.ActiveChannels) >= 2 {
		aggDev = tempDev
		aggPt = fmt.Sprintf("t%d", plantCfg.Concentrator.ActiveChannels[1])
	}

	curTime := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	stateDir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "mirror-test-key"
	const gwID = "0193f000-2000-7000-8000-000000000081"

	aggPolicy := edgecontrol.EdgeAggregationPolicy{
		Enabled: true,
		Points: []edgecontrol.AggregationPointPolicy{{
			DeviceKey:     aggDev,
			PointCode:     aggPt,
			WindowSeconds: edgecontrol.AggregationWindow1Min,
			EmitMinMax:    true,
		}},
	}

	d, err := NewDaemon(Config{
		GatewayID:         gwID,
		StateDir:          stateDir,
		CycleIntervalMS:   1000,
		SpoolMaxBytes:     4 << 20,
		RuntimeVersion:    "1.0.0-test",
		HardwareModel:     "EG8200",
		TargetOS:          "linux",
		TargetArch:        "arm",
		OTA:               OTAConfig{Dir: filepath.Join(stateDir, "ota"), TrustedPublicKeys: map[string]string{keyID: base64.StdEncoding.EncodeToString(pub)}},
		SerialBuses:       map[string]SerialBusConfig{plantCfg.SerialPort: {Speed: 9600, DataBits: 8, Parity: "N", StopBits: 1}},
		FieldPlant:        &plantCfg,
		AggregationPolicy: &aggPolicy,
	}, DaemonOptions{
		Now: func() time.Time { return curTime },
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	innerBus := newFakePlantModbus()
	innerBus.ensureUnit(plantCfg.Concentrator.UnitID)
	innerBus.ensureUnit(plantCfg.Meters.Devices[0].UnitID)
	bus := &mirrorPlantModbus{
		inner:   innerBus,
		concUID: plantCfg.Concentrator.UnitID,
		concRaw: 100, // 10.0°C when scaled by 0.1
	}
	d.fieldPlant.bus = statsFieldPlantBus{inner: bus, onStats: d.fieldPlant.onBusStats}

	// Install an ACTIVE + SHADOW EDGE_CONTROL model pair to verify ModelManager evaluates on Host.RunCycle.
	minActive, maxActive := 6.0, 12.0
	minShadow, maxShadow := 7.0, 13.0
	activeBundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000101",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-opt",
		Version:            "1.0.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutActive,
		MaxShadowDeviation: 2.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			SupplyTempSetpointMinC: &minActive,
			SupplyTempSetpointMaxC: &maxActive,
		},
		UpdatedAt: curTime,
	}
	shadowBundle := edgecontrol.EdgeModelBundle{
		BundleID:           "019b0000-1000-7000-8000-000000000102",
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-opt",
		Version:            "1.1.0",
		RolloutMode:        edgecontrol.EdgeModelRolloutShadow,
		MaxShadowDeviation: 2.0,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			SupplyTempSetpointMinC: &minShadow,
			SupplyTempSetpointMaxC: &maxShadow,
		},
		UpdatedAt: curTime,
	}
	if err := edgecontrol.SignEdgeModelBundleEd25519(&activeBundle, keyID, priv); err != nil {
		t.Fatal(err)
	}
	if err := edgecontrol.SignEdgeModelBundleEd25519(&shadowBundle, keyID, priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.ModelManager().ApplyBundles([]edgecontrol.EdgeModelBundle{activeBundle, shadowBundle}); err != nil {
		t.Fatalf("ApplyBundles: %v", err)
	}

	seenSpool := make(map[string]bool)
	drainNewSpool := func() []SpoolItem {
		all := d.Spool().Pending()
		var out []SpoolItem
		for _, it := range all {
			if seenSpool[it.ID] {
				continue
			}
			seenSpool[it.ID] = true
			out = append(out, it)
		}
		return out
	}

	ctx := context.Background()

	// Step 1: FieldPlantRunner polls hardware -> emits 1 raw up/telemetry AND populates mirror adapters.
	if err := d.fieldPlant.PollTemperaturesOnce(ctx); err != nil {
		t.Fatalf("PollTemperaturesOnce baseline: %v", err)
	}
	pollItems := drainNewSpool()
	var pollTelemetryCount int
	for _, it := range pollItems {
		if strings.HasSuffix(it.Topic, "/up/telemetry") {
			pollTelemetryCount++
		}
	}
	if pollTelemetryCount == 0 {
		t.Fatal("expected FieldPlantRunner.PollTemperaturesOnce to publish raw up/telemetry")
	}

	// Step 2: Host.RunCycle switches mirrored FieldPlant readings into ProcessImage without duplicate raw up/telemetry.
	res1, err := d.StepCycle(ctx)
	if err != nil {
		t.Fatalf("StepCycle baseline: %v", err)
	}
	snap1, ok := res1.Cycle.Image.Get(tempAddr)
	if !ok || !snap1.HasValue {
		t.Fatalf("expected mirrored channel %s in ProcessImage after StepCycle", tempAddr)
	}
	val1 := snap1.Sample.Value.Double

	cycleItems1 := drainNewSpool()
	for _, it := range cycleItems1 {
		if strings.HasSuffix(it.Topic, "/up/telemetry") {
			t.Fatalf("unexpected duplicate up/telemetry from StepCycle on mirrored FieldPlant data: %s", string(it.Payload))
		}
	}

	// Verify FileTimedata recorded the mirrored sample and EdgeModelManager evaluated on ProcessImage.
	if rec, ok := d.Timedata().Latest(tempAddr); !ok || math.Abs(rec.Sample.Value.Double-val1) > 1e-6 {
		t.Fatalf("FileTimedata.Latest(%s) = %+v ok=%v, want %v", tempAddr, rec, ok, val1)
	}
	statuses := d.ModelManager().Statuses()
	if len(statuses) != 1 || statuses[0].ShadowSampleCount < 1 || math.Abs(statuses[0].ShadowMeanAbsDeviation-1.0) > 1e-6 {
		t.Fatalf("unexpected EdgeModelManager statuses after mirrored cycle: %+v", statuses)
	}
	if sp, ok := d.ModelManager().ActiveControlSetpointC(); !ok || math.Abs(sp-9.0) > 1e-6 {
		t.Fatalf("ActiveControlSetpointC() = (%v, %v), want (9.0, true)", sp, ok)
	}

	// Step 3: Install WaveformRule + EdgeRule targeting tempAddr, plus AggregationPolicy on aggDev/aggPt,
	// and reapply EdgeModelManager bundles afterwards so ApplyDesired doesn't overwrite them.
	triggerThreshold := val1 + 1.0
	if err := d.WaveformRing().SetRules([]edgecontrol.WaveformRuleConfig{{
		RuleID:          "wf-mirror-temp",
		RuleName:        "Mirrored temp spike",
		Enabled:         true,
		DeviceCode:      tempDev,
		PointCode:       tempPt,
		TriggerType:     edgecontrol.WaveformTriggerThresholdHigh,
		ThresholdValue:  triggerThreshold,
		PreSeconds:      5,
		PostSeconds:     5,
		SampleRateHz:    1,
		PointCodes:      []string{tempPt},
		CooldownSeconds: 5,
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	ruleBundle := edgecontrol.EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         "0193f000-2000-7000-8000-000000000082",
		BundleRevision: 1,
		CompiledAt:     curTime.Format(time.RFC3339),
		Rules: []edgecontrol.CompiledEdgeRule{{
			RuleID:           "0193f000-2000-7000-8000-000000000083",
			AlarmType:        "mirror-temp-high",
			Title:            "Mirrored temp high",
			Severity:         "WARNING",
			ExecutionTarget:  edgecontrol.EdgeTargetCloudAndEdge,
			QualityPolicy:    edgecontrol.EdgeQualityValidOnly,
			FreshnessSeconds: 300,
			TriggerMode:      edgecontrol.EdgeTriggerSimple,
			Raise: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    tempAddr,
				Operator: edgecontrol.EdgeCompareGT,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: triggerThreshold},
			},
			Clear: edgecontrol.EdgeCondition{
				Kind:     edgecontrol.EdgeConditionCompare,
				Input:    tempAddr,
				Operator: edgecontrol.EdgeCompareLTE,
				Value:    &edgecontrol.EdgeTypedValue{Type: edgecontrol.EdgeValueNumber, Number: val1 + 0.2},
			},
		}},
	}
	digest, err := ruleBundle.ComputeDigest()
	if err != nil {
		t.Fatal(err)
	}
	ruleBundle.EdgeBundleDigest = digest
	report, stale, err := d.Shadow().ApplyDesired(GatewayDesiredConfig{
		DesiredRevision:   1,
		EdgeRuleBundle:    &ruleBundle,
		AggregationPolicy: &aggPolicy,
	})
	if err != nil || stale || report.AppliedStatus != shadowStatusApplied {
		t.Fatalf("ApplyDesired rules: report=%+v stale=%v err=%v", report, stale, err)
	}
	if _, _, err := d.ModelManager().ApplyBundles([]edgecontrol.EdgeModelBundle{activeBundle, shadowBundle}); err != nil {
		t.Fatalf("ApplyBundles after ApplyDesired: %v", err)
	}

	// Accumulate a few pre-trigger ring samples at val1, then bump Modbus value & inject one RX error.
	for i := 1; i <= 5; i++ {
		curTime = curTime.Add(time.Second)
		if _, err := d.StepCycle(ctx); err != nil {
			t.Fatalf("StepCycle pre-trigger %d: %v", i, err)
		}
	}
	drainNewSpool()

	bus.setConcRaw(300) // 30.0°C when scaled by 0.1 (>> val1 + 1.0)
	curTime = curTime.Add(time.Second)
	if err := d.fieldPlant.PollTemperaturesOnce(ctx); err != nil {
		t.Fatalf("PollTemperaturesOnce high: %v", err)
	}
	bus.setFail(plantCfg.Meters.Devices[0].UnitID, true)
	_ = d.fieldPlant.PollMeterOnce(ctx, plantCfg.Meters.Devices[0].DeviceKey)
	bus.setFail(plantCfg.Meters.Devices[0].UnitID, false)
	drainNewSpool()

	// Step through post-trigger capture window (6 cycles @ 1s) so WaveformRingBuffer completes & emits burst.
	var sawRuleEvent, sawWaveformBurst bool
	var observedWaveforms []string
	for i := 0; i <= 6; i++ {
		if i > 0 {
			curTime = curTime.Add(time.Second)
		}
		if _, err := d.StepCycle(ctx); err != nil {
			t.Fatalf("StepCycle post-trigger %d: %v", i, err)
		}
		for _, it := range drainNewSpool() {
			switch {
			case strings.HasSuffix(it.Topic, "/up/telemetry"):
				t.Fatalf("unexpected duplicate raw up/telemetry during post-trigger cycles: %s", string(it.Payload))
			case strings.HasSuffix(it.Topic, "/up/event"):
				sawRuleEvent = true
			case strings.HasSuffix(it.Topic, "/up/waveform"):
				observedWaveforms = append(observedWaveforms, string(it.Payload))
				var env edgecontrol.ShadowReplicationEnvelope
				if err := json.Unmarshal(it.Payload, &env); err == nil {
					var burst edgecontrol.WaveformBurstPayload
					if err := json.Unmarshal(env.Payload, &burst); err == nil && len(burst.Samples) >= 5 {
						sawWaveformBurst = true
					}
				}
			}
		}
	}
	if !sawRuleEvent {
		t.Fatal("expected EdgeRuleController to emit alarm event from mirrored ProcessImage point")
	}
	if !sawWaveformBurst {
		t.Fatalf("expected WaveformRingBuffer to emit captured burst from mirrored ProcessImage point; curTime=%s val1=%v observedWaveforms=%v", curTime.Format(time.RFC3339), val1, observedWaveforms)
	}

	// Step 4: Advance past the 1-minute aggregation boundary so WindowAggregator flushes the aggregated window ONLY.
	curTime = curTime.Add(time.Minute)
	if _, err := d.StepCycle(ctx); err != nil {
		t.Fatalf("StepCycle agg flush: %v", err)
	}
	var aggTelemetry *mqttUplinkTelemetryEnvelope
	for _, it := range drainNewSpool() {
		if strings.HasSuffix(it.Topic, "/up/telemetry") {
			var env mqttUplinkTelemetryEnvelope
			if err := json.Unmarshal(it.Payload, &env); err != nil {
				t.Fatalf("decode aggregated telemetry: %v", err)
			}
			aggTelemetry = &env
		}
	}
	if aggTelemetry == nil || len(aggTelemetry.Payload.Devices) != 1 || aggTelemetry.Payload.Devices[0].DeviceID != aggDev {
		t.Fatalf("expected only aggregated WindowAggregator telemetry for %s, got %+v", aggDev, aggTelemetry)
	}
	ptCodes := make(map[string]float64)
	for _, pt := range aggTelemetry.Payload.Devices[0].Points {
		if v, ok := pt.Value.(float64); ok {
			ptCodes[pt.Code] = v
		}
	}
	if _, ok := ptCodes[aggPt]; !ok || ptCodes[aggPt+"_max"] <= ptCodes[aggPt+"_min"] {
		t.Fatalf("expected mean/min/max in aggregated telemetry for %s/%s, got %+v", aggDev, aggPt, ptCodes)
	}

	// Step 5: Verify FieldPlantRunner.OnBusStats propagated TX frames and simulated RX errors into RemoteDiagnostics.
	metrics := d.RemoteDiagnostics().SampleHostMetrics(curTime, d.WaveformRing())
	if metrics.RS485TxFrames == 0 || metrics.RS485RxErrors == 0 || metrics.RS485ErrorRatePPM <= 0 {
		t.Fatalf("expected non-zero RS485TxFrames and RS485RxErrors via OnBusStats, got %+v", metrics)
	}
}

func TestFieldPlantMirrorPreservesExplicitDriverAdapters(t *testing.T) {
	plantPath := filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.edge-plant.json")
	plantCfg, err := LoadFieldPlantConfigFile(plantPath)
	if err != nil {
		t.Fatalf("LoadFieldPlantConfigFile: %v", err)
	}
	plantCfg.ApplyDefaults()
	existingID := plantCfg.Concentrator.TempDeviceKey

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	explicit := newFieldPlantMirrorAdapter(existingID, []edgecontrol.ChannelDescriptor{
		newMirrorChannelDescriptor(existingID, "custom_point", edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh),
	})
	explicit.component.Type = "EXPLICIT_DRIVER"
	if err := host.RegisterAdapter(explicit); err != nil {
		t.Fatal(err)
	}

	reg, err := NewFieldPlantMirrorRegistry(plantCfg, nil, host)
	if err != nil {
		t.Fatalf("NewFieldPlantMirrorRegistry: %v", err)
	}
	if reg.HasDevice(existingID) {
		t.Fatalf("mirror registry must not claim explicit driver ID %q", existingID)
	}
	if ad, ok := host.Adapter(existingID); !ok || ad.Component().Type != "EXPLICIT_DRIVER" {
		t.Fatalf("explicit driver adapter %q was overwritten: ok=%v comp=%+v", existingID, ok, ad)
	}
}

func TestWaveformRingCanonicalStorageAndRulePriorityAtScale(t *testing.T) {
	// 1. Songyaxuan mirror scale must keep BufferStats compact (1 canonical series per numeric channel)
	//    and capture non-empty pre/post samples deterministically on every invocation.
	plantPath := filepath.Join("..", "..", "..", "..", "deploy", "sites", "songyaxuan.edge-plant.json")
	plantCfg, err := LoadFieldPlantConfigFile(plantPath)
	if err != nil {
		t.Fatalf("LoadFieldPlantConfigFile: %v", err)
	}
	plantCfg.ApplyDefaults()
	adapters := BuildFieldPlantMirrorAdapters(plantCfg, nil)

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	var numericChannels int
	t0 := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	for _, ad := range adapters {
		var pts []mqttUplinkPoint
		for _, ch := range ad.Channels() {
			switch ch.DataType {
			case edgecontrol.DataTypeDouble:
				pts = append(pts, mqttUplinkPoint{Code: ch.ChannelID, Value: 10.0, Quality: 0})
				numericChannels++
			case edgecontrol.DataTypeInteger:
				pts = append(pts, mqttUplinkPoint{Code: ch.ChannelID, Value: 100, Quality: 0})
				numericChannels++
			}
		}
		ad.UpdatePoints(t0, pts)
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}
	if err := host.Start(nil); err != nil {
		t.Fatal(err)
	}

	ring := NewWaveformRingBuffer(defaultWaveformRingHorizon)
	res, err := host.RunCycle(context.Background(), t0)
	if err != nil {
		t.Fatal(err)
	}
	ring.ObserveSnapshots(t0, res.Cycle.Image)
	activePts, _ := ring.BufferStats()
	if activePts != numericChannels {
		t.Fatalf("expected 1 canonical ring series per numeric channel (%d), got %d", numericChannels, activePts)
	}
	if activePts >= maxWaveformRingPoints {
		t.Fatalf("expected Songyaxuan canonical points (%d) well under maxWaveformRingPoints (%d)", activePts, maxWaveformRingPoints)
	}

	// 2. When total channels exceed maxWaveformRingPoints (300 > 256), a rule added afterwards targeting
	//    the tail channel dev-299/temp_tail still evicts a non-rule channel and captures non-empty bursts.
	scaleHost, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	var tailAdapter *FieldPlantMirrorAdapter
	for i := 0; i < maxWaveformRingPoints+44; i++ {
		devID := fmt.Sprintf("dev-%03d", i)
		ptCode := "val"
		if i == maxWaveformRingPoints+43 {
			ptCode = "temp_tail"
		}
		ad := newFieldPlantMirrorAdapter(devID, []edgecontrol.ChannelDescriptor{
			newMirrorChannelDescriptor(devID, ptCode, edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh),
		})
		ad.UpdatePoints(t0, []mqttUplinkPoint{{Code: ptCode, Value: 8.0, Quality: 0}})
		if i == maxWaveformRingPoints+43 {
			tailAdapter = ad
		}
		if err := scaleHost.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}
	if err := scaleHost.Start(nil); err != nil {
		t.Fatal(err)
	}

	scaleRing := NewWaveformRingBuffer(defaultWaveformRingHorizon)
	// Saturate r.points up to maxWaveformRingPoints BEFORE configuring any rule.
	for i := 0; i < 3; i++ {
		ts := t0.Add(time.Duration(i) * time.Second)
		r, err := scaleHost.RunCycle(context.Background(), ts)
		if err != nil {
			t.Fatal(err)
		}
		scaleRing.ObserveSnapshots(ts, r.Cycle.Image)
	}
	tailDev := tailAdapter.Component().ID
	if err := scaleRing.SetRules([]edgecontrol.WaveformRuleConfig{{
		RuleID:          "wf-tail-priority",
		RuleName:        "Tail priority rule",
		Enabled:         true,
		DeviceCode:      tailDev,
		PointCode:       "temp_tail",
		TriggerType:     edgecontrol.WaveformTriggerThresholdHigh,
		ThresholdValue:  15.0,
		PreSeconds:      5,
		PostSeconds:     5,
		SampleRateHz:    1,
		PointCodes:      []string{"temp_tail"},
		CooldownSeconds: 5,
	}}); err != nil {
		t.Fatal(err)
	}

	// Accumulate baseline samples after rule installation, then trigger above 15.0°C and complete post window.
	for i := 3; i <= 5; i++ {
		ts := t0.Add(time.Duration(i) * time.Second)
		r, err := scaleHost.RunCycle(context.Background(), ts)
		if err != nil {
			t.Fatal(err)
		}
		scaleRing.ObserveSnapshots(ts, r.Cycle.Image)
	}
	tailAdapter.UpdatePoints(t0.Add(6*time.Second), []mqttUplinkPoint{{Code: "temp_tail", Value: 22.0, Quality: 0}})
	var tailBursts []edgecontrol.WaveformBurstPayload
	for i := 6; i <= 12; i++ {
		ts := t0.Add(time.Duration(i) * time.Second)
		r, err := scaleHost.RunCycle(context.Background(), ts)
		if err != nil {
			t.Fatal(err)
		}
		tailBursts = append(tailBursts, scaleRing.ObserveSnapshots(ts, r.Cycle.Image)...)
	}
	if len(tailBursts) != 1 || len(tailBursts[0].Samples) < 6 {
		t.Fatalf("expected rule-prioritized tail channel burst with >=6 samples, got %+v", tailBursts)
	}
}
