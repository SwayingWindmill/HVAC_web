package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type resendStubDevice struct {
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	samples   map[string]edgecontrol.Sample
}

func (d *resendStubDevice) Component() edgecontrol.ComponentDescriptor {
	return d.component
}

func (d *resendStubDevice) Channels() []edgecontrol.ChannelDescriptor {
	return slices.Clone(d.channels)
}

func (d *resendStubDevice) Poll(_ context.Context, _ time.Time) ([]edgecontrol.ChannelUpdate, error) {
	updates := make([]edgecontrol.ChannelUpdate, 0, len(d.channels))
	for _, ch := range d.channels {
		if s, ok := d.samples[ch.Address()]; ok {
			updates = append(updates, edgecontrol.ChannelUpdate{Address: ch.Address(), Sample: s})
		}
	}
	return updates, nil
}

func (d *resendStubDevice) Apply(_ context.Context, _ edgecontrol.ProcessImage, _ []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	return nil, nil
}

func metricCounterValue(t *testing.T, reg *observability.Registry, metricName, labelKey, labelVal string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(rec, req)
	prefix := metricName + "{" + labelKey + `="` + labelVal + `"} `
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, prefix) {
			val, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 64)
			if err != nil {
				t.Fatalf("parse metric line %q: %v", line, err)
			}
			return val
		}
	}
	return 0
}

func TestTimedataResendQueuePriorityDrainCrashRecoveryAndMetrics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	timedataDir := filepath.Join(dir, "timedata")
	cursorPath := filepath.Join(dir, "resend-cursor.json")
	const gatewayID = "0193f000-2000-7000-8000-000000000077"

	timedata, err := edgecontrol.OpenFileTimedata(timedataDir, edgecontrol.PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata() error = %v", err)
	}
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	makeCh := func(id string, priority edgecontrol.DataPriority) edgecontrol.ChannelDescriptor {
		return edgecontrol.ChannelDescriptor{
			ComponentID:               "DEV-REPLAY",
			ChannelID:                 id,
			PointID:                   "DEV-REPLAY:" + id,
			DataType:                  edgecontrol.DataTypeDouble,
			Access:                    edgecontrol.AccessReadOnly,
			Unit:                      "kW",
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              priority,
			LocalPersistencePriority:  priority,
			RemotePersistencePriority: priority,
			AggregationPriority:       priority,
			ResendPriority:            priority,
		}
	}

	stub := &resendStubDevice{
		component: edgecontrol.ComponentDescriptor{
			ID:        "DEV-REPLAY",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "DECLARATIVE_MODBUS_TCP",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		channels: []edgecontrol.ChannelDescriptor{
			makeCh("low_pt", edgecontrol.PriorityLow),
			makeCh("med_pt", edgecontrol.PriorityMedium),
			makeCh("high_pt", edgecontrol.PriorityHigh),
			makeCh("vh_pt", edgecontrol.PriorityVeryHigh),
		},
		samples: map[string]edgecontrol.Sample{},
	}
	if err := host.RegisterAdapter(stub); err != nil {
		t.Fatalf("RegisterAdapter() error = %v", err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}
	if err := host.AttachTimedata(timedata); err != nil {
		t.Fatalf("AttachTimedata() error = %v", err)
	}

	metaLookup := func(addr string) TimedataResendPointMeta {
		desc, ok := host.ChannelDescriptor(addr)
		if !ok {
			return TimedataResendPointMeta{Priority: edgecontrol.PriorityMedium}
		}
		return TimedataResendPointMeta{
			DeviceID:  desc.ComponentID,
			PointCode: desc.ChannelID,
			Unit:      desc.Unit,
			Priority:  desc.ResendPriority,
		}
	}

	// Phase 1: Record 1 cycle online and sync online baseline cursor so it is NOT replayed.
	t0 := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	for _, name := range []string{"low_pt", "med_pt", "high_pt", "vh_pt"} {
		stub.samples["DEV-REPLAY/"+name] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(1), Quality: edgecontrol.QualityGood, ObservedAt: t0}
	}
	if _, err := host.RunCycle(context.Background(), t0); err != nil {
		t.Fatalf("online RunCycle error = %v", err)
	}

	registry := observability.NewRegistry()
	var mu sync.Mutex
	var publishedEnvelopes []mqttTelemetryEnvelope
	var failAfterCount int

	makePublish := func() func(context.Context, string, []byte) error {
		return func(_ context.Context, topic string, payload []byte) error {
			if topic != "hvac/v1/"+gatewayID+"/up/telemetry" {
				return errors.New("unexpected topic: " + topic)
			}
			var env mqttTelemetryEnvelope
			if err := json.Unmarshal(payload, &env); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if failAfterCount > 0 && len(publishedEnvelopes) >= failAfterCount {
				return errors.New("simulated broker drop mid-replay")
			}
			publishedEnvelopes = append(publishedEnvelopes, env)
			return nil
		}
	}

	queue, err := NewTimedataResendQueue(TimedataResendConfig{
		GatewayID:     gatewayID,
		Timedata:      timedata,
		CursorPath:    cursorPath,
		BatchSize:     10,
		Metrics:       registry,
		PointMetadata: metaLookup,
		Publish:       makePublish(),
	})
	if err != nil {
		t.Fatalf("NewTimedataResendQueue() error = %v", err)
	}
	queue.SetOnlineSynced(true)
	if err := queue.SyncOnlineBaseline(); err != nil {
		t.Fatalf("SyncOnlineBaseline() error = %v", err)
	}

	// Phase 2: Disconnect, record 2 offline cycles per priority (notice LOW appears before VERY_HIGH in file).
	queue.SetOnlineSynced(false)
	for cycle := 1; cycle <= 2; cycle++ {
		at := t0.Add(time.Duration(cycle*5) * time.Second)
		v := float64(100 + cycle)
		for _, name := range []string{"low_pt", "med_pt", "high_pt", "vh_pt"} {
			stub.samples["DEV-REPLAY/"+name] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(v), Quality: edgecontrol.QualityGood, ObservedAt: at}
		}
		if _, err := host.RunCycle(context.Background(), at); err != nil {
			t.Fatalf("offline RunCycle %d error = %v", cycle, err)
		}
	}

	// Phase 3: Reconnect and simulate crash after 3 batches (2 VERY_HIGH batches + 1 HIGH batch),
	// failing on the 4th publish (2nd HIGH batch).
	mu.Lock()
	failAfterCount = 3
	mu.Unlock()
	if _, err := queue.Drain(context.Background()); err == nil {
		t.Fatal("expected simulated crash failure on 4th publish")
	}

	mu.Lock()
	if len(publishedEnvelopes) != 3 {
		t.Fatalf("expected 3 published replay batches before simulated crash, got %d", len(publishedEnvelopes))
	}
	// Verify strict priority order of the first 3 batches: vh_pt, vh_pt, high_pt
	gotCodesBeforeCrash := []string{
		publishedEnvelopes[0].Payload.Devices[0].Points[0].Code,
		publishedEnvelopes[1].Payload.Devices[0].Points[0].Code,
		publishedEnvelopes[2].Payload.Devices[0].Points[0].Code,
	}
	mu.Unlock()
	if !slices.Equal(gotCodesBeforeCrash, []string{"vh_pt", "vh_pt", "high_pt"}) {
		t.Fatalf("unexpected priority replay order before crash: %v", gotCodesBeforeCrash)
	}

	// Phase 4: Reopen queue from disk (crash recovery), clear failure injection, drain remaining.
	restartedQueue, err := NewTimedataResendQueue(TimedataResendConfig{
		GatewayID:     gatewayID,
		Timedata:      timedata,
		CursorPath:    cursorPath,
		BatchSize:     10,
		Metrics:       registry,
		PointMetadata: metaLookup,
		Publish:       makePublish(),
	})
	if err != nil {
		t.Fatalf("restarted NewTimedataResendQueue() error = %v", err)
	}

	mu.Lock()
	failAfterCount = 0
	mu.Unlock()

	res, err := restartedQueue.Drain(context.Background())
	if err != nil {
		t.Fatalf("recovery Drain() error = %v", err)
	}
	if res.BatchesReplayed != 5 || res.RecordsReplayed != 5 {
		t.Fatalf("expected 5 remaining replay batches/records (1 HIGH + 2 MEDIUM + 2 LOW), got %+v", res)
	}

	mu.Lock()
	allEnvs := append([]mqttTelemetryEnvelope(nil), publishedEnvelopes...)
	mu.Unlock()

	if len(allEnvs) != 8 {
		t.Fatalf("expected total 8 envelopes across crash + recovery, got %d", len(allEnvs))
	}
	var allCodes []string
	for i, env := range allEnvs {
		if !env.Replay {
			t.Fatalf("envelope %d missing Replay=true", i)
		}
		allCodes = append(allCodes, env.Payload.Devices[0].Points[0].Code)
	}
	wantOrder := []string{"vh_pt", "vh_pt", "high_pt", "high_pt", "med_pt", "med_pt", "low_pt", "low_pt"}
	if !slices.Equal(allCodes, wantOrder) {
		t.Fatalf("expected strict VERY_HIGH->HIGH->MEDIUM->LOW replay order %v without duplicates, got %v", wantOrder, allCodes)
	}

	// Verify metric hvac_edge_mqtt_replay_batches_total{priority=...} == 2 for each priority.
	for _, prio := range ResendPriorityOrder {
		if got := metricCounterValue(t, registry, "hvac_edge_mqtt_replay_batches_total", "priority", string(prio)); got != 2 {
			t.Fatalf("metric %s{priority=%s} = %v, want 2", "hvac_edge_mqtt_replay_batches_total", prio, got)
		}
	}

	// Re-draining after completion should emit 0 batches.
	noopRes, err := restartedQueue.Drain(context.Background())
	if err != nil {
		t.Fatalf("idempotent Drain() error = %v", err)
	}
	if noopRes.BatchesReplayed != 0 || noopRes.RecordsReplayed != 0 {
		t.Fatalf("expected 0 batches on clean re-drain, got %+v", noopRes)
	}
}
