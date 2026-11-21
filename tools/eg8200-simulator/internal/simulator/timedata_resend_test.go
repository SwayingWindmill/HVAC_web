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
	gotCodesBeforeCrash := []string{
		publishedEnvelopes[0].Payload.Devices[0].Points[0].Code,
		publishedEnvelopes[1].Payload.Devices[0].Points[0].Code,
		publishedEnvelopes[2].Payload.Devices[0].Points[0].Code,
	}
	mu.Unlock()
	if !slices.Equal(gotCodesBeforeCrash, []string{"vh_pt", "vh_pt", "high_pt"}) {
		t.Fatalf("unexpected priority replay order before crash: %v", gotCodesBeforeCrash)
	}

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

	for _, prio := range ResendPriorityOrder {
		if got := metricCounterValue(t, registry, "hvac_edge_mqtt_replay_batches_total", "priority", string(prio)); got != 2 {
			t.Fatalf("metric %s{priority=%s} = %v, want 2", "hvac_edge_mqtt_replay_batches_total", prio, got)
		}
	}

	noopRes, err := restartedQueue.Drain(context.Background())
	if err != nil {
		t.Fatalf("idempotent Drain() error = %v", err)
	}
	if noopRes.BatchesReplayed != 0 || noopRes.RecordsReplayed != 0 {
		t.Fatalf("expected 0 batches on clean re-drain, got %+v", noopRes)
	}
}

func TestTimedataResendQueueMarkPublishedContiguousAndRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	timedataDir := filepath.Join(dir, "timedata")
	cursorPath := filepath.Join(dir, "resend-cursor.json")
	const gatewayID = "0193f000-2000-7000-8000-000000000088"

	timedata, err := edgecontrol.OpenFileTimedata(timedataDir, edgecontrol.PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata() error = %v", err)
	}
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	makeCh := func(id string) edgecontrol.ChannelDescriptor {
		return edgecontrol.ChannelDescriptor{
			ComponentID:               "DEV-ACK",
			ChannelID:                 id,
			PointID:                   "DEV-ACK:" + id,
			DataType:                  edgecontrol.DataTypeDouble,
			Access:                    edgecontrol.AccessReadOnly,
			Unit:                      "degC",
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              edgecontrol.PriorityHigh,
			LocalPersistencePriority:  edgecontrol.PriorityHigh,
			RemotePersistencePriority: edgecontrol.PriorityHigh,
			AggregationPriority:       edgecontrol.PriorityHigh,
			ResendPriority:            edgecontrol.PriorityHigh,
		}
	}
	stub := &resendStubDevice{
		component: edgecontrol.ComponentDescriptor{
			ID:        "DEV-ACK",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "DECLARATIVE_MODBUS_TCP",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		channels: []edgecontrol.ChannelDescriptor{makeCh("p1"), makeCh("p2")},
		samples:  map[string]edgecontrol.Sample{},
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

	t1 := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)
	stub.samples["DEV-ACK/p1"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(11), Quality: edgecontrol.QualityGood, ObservedAt: t1}
	stub.samples["DEV-ACK/p2"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(22), Quality: edgecontrol.QualityGood, ObservedAt: t1}
	if _, err := host.RunCycle(context.Background(), t1); err != nil {
		t.Fatalf("RunCycle error = %v", err)
	}

	var mu sync.Mutex
	var replayed []mqttTelemetryEnvelope
	queueCfg := TimedataResendConfig{
		GatewayID:  gatewayID,
		Timedata:   timedata,
		CursorPath: cursorPath,
		BatchSize:  10,
		PointMetadata: func(addr string) TimedataResendPointMeta {
			dev, code, _ := strings.Cut(addr, "/")
			return TimedataResendPointMeta{DeviceID: dev, PointCode: code, Priority: edgecontrol.PriorityHigh}
		},
		Publish: func(_ context.Context, _ string, payload []byte) error {
			var env mqttTelemetryEnvelope
			if err := json.Unmarshal(payload, &env); err == nil {
				mu.Lock()
				replayed = append(replayed, env)
				mu.Unlock()
			}
			return nil
		},
	}
	q1, err := NewTimedataResendQueue(queueCfg)
	if err != nil {
		t.Fatalf("NewTimedataResendQueue() error = %v", err)
	}
	if err := q1.MarkPublishedObservations([]PublishedObservation{{Address: "DEV-ACK/p2", ObservedAt: t1}}); err != nil {
		t.Fatalf("MarkPublishedObservations() error = %v", err)
	}
	if got := q1.CursorSnapshot().ByPriority[edgecontrol.PriorityHigh]; got != 0 {
		t.Fatalf("expected HIGH cursor 0 before un-acked p1, got %d", got)
	}

	q2, err := NewTimedataResendQueue(queueCfg)
	if err != nil {
		t.Fatalf("reopened NewTimedataResendQueue() error = %v", err)
	}
	res, err := q2.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if res.RecordsReplayed != 1 {
		t.Fatalf("expected exactly 1 un-acked record replayed (p1), got %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(replayed) != 1 || len(replayed[0].Payload.Devices) != 1 || len(replayed[0].Payload.Devices[0].Points) != 1 || replayed[0].Payload.Devices[0].Points[0].Code != "p1" {
		t.Fatalf("expected only DEV-ACK/p1 replayed, got %+v", replayed)
	}
}

func TestTimedataResendDrainUpToPreservesLiveRecordsAfterWatermark(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	timedataDir := filepath.Join(dir, "timedata")
	cursorPath := filepath.Join(dir, "resend-cursor.json")
	timedata, err := edgecontrol.OpenFileTimedata(timedataDir, edgecontrol.PriorityLow)
	if err != nil {
		t.Fatalf("OpenFileTimedata() error = %v", err)
	}

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	makeCh := func(id string) edgecontrol.ChannelDescriptor {
		return edgecontrol.ChannelDescriptor{
			ComponentID:               "DEV-WM",
			ChannelID:                 id,
			PointID:                   "DEV-WM:" + id,
			DataType:                  edgecontrol.DataTypeDouble,
			Access:                    edgecontrol.AccessReadOnly,
			Unit:                      "degC",
			Category:                  edgecontrol.ChannelCategoryOpenemsType,
			PollPriority:              edgecontrol.PriorityHigh,
			LocalPersistencePriority:  edgecontrol.PriorityHigh,
			RemotePersistencePriority: edgecontrol.PriorityHigh,
			AggregationPriority:       edgecontrol.PriorityHigh,
			ResendPriority:            edgecontrol.PriorityHigh,
		}
	}
	stub := &resendStubDevice{
		component: edgecontrol.ComponentDescriptor{
			ID:        "DEV-WM",
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "DECLARATIVE_MODBUS_TCP",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		channels: []edgecontrol.ChannelDescriptor{makeCh("p1"), makeCh("p2")},
		samples:  map[string]edgecontrol.Sample{},
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

	t1 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	stub.samples["DEV-WM/p1"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(10), Quality: edgecontrol.QualityGood, ObservedAt: t1}
	stub.samples["DEV-WM/p2"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(20), Quality: edgecontrol.QualityGood, ObservedAt: t1}
	if _, err := host.RunCycle(context.Background(), t1); err != nil {
		t.Fatalf("RunCycle t1 error = %v", err)
	}

	var mu sync.Mutex
	var replayed []mqttTelemetryEnvelope
	q, err := NewTimedataResendQueue(TimedataResendConfig{
		GatewayID:  "01950000-0000-7000-8000-000000000088",
		Timedata:   timedata,
		CursorPath: cursorPath,
		BatchSize:  10,
		PointMetadata: func(addr string) TimedataResendPointMeta {
			dev, code, _ := strings.Cut(addr, "/")
			return TimedataResendPointMeta{DeviceID: dev, PointCode: code, Priority: edgecontrol.PriorityHigh}
		},
		Publish: func(_ context.Context, _ string, payload []byte) error {
			var env mqttTelemetryEnvelope
			if err := json.Unmarshal(payload, &env); err == nil {
				mu.Lock()
				replayed = append(replayed, env)
				mu.Unlock()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewTimedataResendQueue() error = %v", err)
	}

	zeroRes, err := q.DrainUpTo(context.Background(), 0)
	if err != nil {
		t.Fatalf("DrainUpTo(0) error = %v", err)
	}
	if zeroRes.RecordsReplayed != 0 || !q.IsSynced() {
		t.Fatalf("expected DrainUpTo(0) to mark synced without replaying t1 records, got res=%+v synced=%v", zeroRes, q.IsSynced())
	}

	q.SetOnlineSynced(false)
	offlineWatermark := q.LatestTimedataSequence()

	t2 := t1.Add(time.Second)
	stub.samples["DEV-WM/p1"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(15), Quality: edgecontrol.QualityGood, ObservedAt: t2}
	stub.samples["DEV-WM/p2"] = edgecontrol.Sample{Value: edgecontrol.DoubleValue(25), Quality: edgecontrol.QualityGood, ObservedAt: t2}
	if _, err := host.RunCycle(context.Background(), t2); err != nil {
		t.Fatalf("RunCycle t2 error = %v", err)
	}

	boundedRes, err := q.DrainUpTo(context.Background(), offlineWatermark)
	if err != nil {
		t.Fatalf("DrainUpTo(offlineWatermark) error = %v", err)
	}
	if boundedRes.RecordsReplayed != 2 || !q.IsSynced() {
		t.Fatalf("expected DrainUpTo(offlineWatermark) to replay only 2 t1 records, got res=%+v synced=%v", boundedRes, q.IsSynced())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(replayed) != 1 || replayed[0].Timestamp != t1.UnixMilli() {
		t.Fatalf("expected only t1 (%d) replayed, got %+v", t1.UnixMilli(), replayed)
	}
	if got := q.CursorSnapshot().ByPriority[edgecontrol.PriorityHigh]; got != offlineWatermark {
		t.Fatalf("expected HIGH cursor=%d after bounded drain, got %d", offlineWatermark, got)
	}
}
