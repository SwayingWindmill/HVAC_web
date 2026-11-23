package simulator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type captureResetQoSPublisher struct {
	mu      sync.Mutex
	packets []*paho.Publish
}

func (c *captureResetQoSPublisher) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := &paho.Publish{
		Topic:   pub.Topic,
		QoS:     pub.QoS,
		Retain:  pub.Retain,
		Payload: append([]byte(nil), pub.Payload...),
	}
	c.packets = append(c.packets, copied)
	return &paho.PublishResponse{}, nil
}

func (c *captureResetQoSPublisher) Packets() []*paho.Publish {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*paho.Publish(nil), c.packets...)
}

func TestMQTTPublisherFactoryResetFullWipePublishesAckAndClearsState(t *testing.T) {
	t.Parallel()

	const gatewayID = "0195f300-3000-7000-8000-000000000777"
	const otherGatewayID = "0195f300-3000-7000-8000-000000000888"
	const resetID = "0195f300-6000-7000-8000-000000000777"
	const transferID = "0195f300-5000-7000-8000-000000000777"

	dir := t.TempDir()
	certPath := filepath.Join(dir, "gateway.crt")
	keyPath := filepath.Join(dir, "gateway.key")
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nSENSITIVE_CERT\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\nSENSITIVE_KEY\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	shadowPath := filepath.Join(dir, "config-shadow.v1.json")
	shadow := newEdgeConfigShadow(shadowPath)
	var (
		removedDrivers []string
		autonomyReset  bool
		rulesReset     bool
	)
	shadow.removeDriverHook = func(_ context.Context, deviceKey string) error {
		removedDrivers = append(removedDrivers, deviceKey)
		return nil
	}
	shadow.applyAutonomyHook = func(_ context.Context, policy *GatewayAutonomyPolicy) error {
		if policy == nil {
			autonomyReset = true
		}
		return nil
	}
	shadow.applyEdgeRulesHook = func(_ context.Context, bundle *edgecontrol.EdgeRuleBundle) error {
		if bundle == nil {
			rulesReset = true
		}
		return nil
	}
	if _, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{
		DesiredRevision: 4,
		PointMappings: []GatewayPointMapping{
			{SourceChannel: "ch-01:supply", PrimaryDeviceKey: "ch-01", PrimaryPointCode: "supply_temp_c"},
		},
		SamplingPolicies: []GatewaySamplingPolicy{
			{DeviceKey: "ch-01", PointCode: "supply_temp_c", SampleIntervalMS: 1000, PublishIntervalMS: 5000},
		},
	}); err != nil {
		t.Fatalf("applyDesired: %v", err)
	}
	shadow.mu.Lock()
	shadow.driverConfigs = []GatewayModbusDriverConfig{{DeviceKey: "PLC-AUX-01"}}
	shadow.mu.Unlock()
	if _, err := os.Stat(shadowPath); err != nil {
		t.Fatalf("expected persisted config shadow file before wipe: %v", err)
	}

	spool, err := newMQTTEvidenceSpool(filepath.Join(dir, "outbound"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Enqueue("msg-1", EvidenceTelemetryNormal, "hvac/v1/"+gatewayID+"/up/telemetry", []byte(`{"test":1}`)); err != nil {
		t.Fatal(err)
	}
	if spool.UsedBytes() == 0 {
		t.Fatal("expected non-zero spool bytes before wipe")
	}

	timedata, err := edgecontrol.OpenFileTimedata(filepath.Join(dir, "timedata"), edgecontrol.PriorityLow)
	if err != nil {
		t.Fatal(err)
	}
	rt := edgecontrol.NewRuntime()
	desc := edgecontrol.ChannelDescriptor{
		ComponentID:               "chiller01",
		ChannelID:                 "Power",
		PointID:                   "point-power",
		DataType:                  edgecontrol.DataTypeDouble,
		Access:                    edgecontrol.AccessReadOnly,
		Unit:                      "kW",
		Category:                  edgecontrol.ChannelCategoryOpenemsType,
		PollPriority:              edgecontrol.PriorityHigh,
		LocalPersistencePriority:  edgecontrol.PriorityHigh,
		RemotePersistencePriority: edgecontrol.PriorityHigh,
		AggregationPriority:       edgecontrol.PriorityMedium,
		ResendPriority:            edgecontrol.PriorityHigh,
	}
	if err := rt.Register(desc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	if err := rt.PublishNext(desc.Address(), edgecontrol.Sample{
		Value:      edgecontrol.DoubleValue(42.5),
		Quality:    edgecontrol.QualityGood,
		ObservedAt: now,
		Sequence:   1,
	}); err != nil {
		t.Fatal(err)
	}
	if written, err := timedata.RecordImage(rt.SwitchProcessImage(now)); err != nil || written != 1 {
		t.Fatalf("RecordImage written=%d err=%v", written, err)
	}
	if timedata.LatestSequence() == 0 {
		t.Fatal("expected timedata sequence > 0 before wipe")
	}

	cursorPath := filepath.Join(dir, "resend-cursor.json")
	resendQueue, err := NewTimedataResendQueue(TimedataResendConfig{
		GatewayID:  gatewayID,
		Timedata:   timedata,
		CursorPath: cursorPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := resendQueue.commitCursor(edgecontrol.PriorityHigh, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cursorPath); err != nil {
		t.Fatalf("expected resend cursor file before wipe: %v", err)
	}

	qosPub := &captureResetQoSPublisher{}
	connected := &atomic.Bool{}
	connected.Store(true)
	metrics := observability.NewRegistry()
	publisher := &MQTTPublisher{
		gatewayID:         gatewayID,
		config:            MQTTGatewayConfig{GatewayID: gatewayID, CertFile: certPath, KeyFile: keyPath, MaximumQueueBytes: 1 << 20},
		qosPub:            qosPub,
		evidenceSpool:     spool,
		resendQueue:       resendQueue,
		configShadow:      shadow,
		metrics:           metrics,
		connected:         connected,
		pendingObsByMsgID: map[string][]PublishedObservation{"msg-1": {{Address: desc.Address(), ObservedAt: now}}},
		lastPointStates:   map[string]lastEmittedPointState{"k": {}},
	}

	// 1. Envelope targeting another gateway is rejected without touching local files.
	wrongEnv, err := edgecontrol.EncodeFactoryReset(otherGatewayID, edgecontrol.FactoryResetPayload{
		ResetID:           resetID,
		TransferID:        transferID,
		WipeCredentials:   true,
		WipeConfigShadow:  true,
		WipeOfflineBuffer: true,
		Reason:            "wrong gateway",
		IssuedAt:          now.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.handleDownlinkFactoryResetMessage(context.Background(), wrongEnv); err == nil {
		t.Fatal("expected error for mismatched gatewayId FACTORY_RESET envelope")
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("wrong-gateway wipe removed cert: %v", err)
	}
	if spool.UsedBytes() == 0 || len(qosPub.Packets()) != 0 {
		t.Fatalf("wrong-gateway wipe modified state: spool=%d packets=%d", spool.UsedBytes(), len(qosPub.Packets()))
	}

	// 2. Valid FACTORY_RESET executes full wipe, emits FACTORY_RESET_ACK QoS 1, and clears connected flag.
	validEnv, err := edgecontrol.EncodeFactoryReset(gatewayID, edgecontrol.FactoryResetPayload{
		ResetID:           resetID,
		TransferID:        transferID,
		WipeCredentials:   true,
		WipeConfigShadow:  true,
		WipeOfflineBuffer: true,
		Reason:            "cross-tenant transfer cleanup",
		IssuedAt:          now.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.handleDownlinkFactoryResetMessage(context.Background(), validEnv); err != nil {
		t.Fatalf("handleDownlinkFactoryResetMessage: %v", err)
	}

	for _, p := range []string{certPath, keyPath, shadowPath, cursorPath} {
		if _, statErr := os.Stat(p); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("expected %s to be deleted after factory reset, stat err=%v", p, statErr)
		}
	}
	rev, mappings, policies, concs := publisher.ConfigShadowState()
	if rev != 0 || len(mappings) != 0 || len(policies) != 0 || len(concs) != 0 {
		t.Fatalf("expected cleared config shadow state, got rev=%d mappings=%d policies=%d concs=%d", rev, len(mappings), len(policies), len(concs))
	}
	if !slices.Equal(removedDrivers, []string{"PLC-AUX-01"}) || !autonomyReset || !rulesReset {
		t.Fatalf("expected driver/autonomy/rules unload on reset: drivers=%v autonomy=%v rules=%v", removedDrivers, autonomyReset, rulesReset)
	}
	if spool.UsedBytes() != 0 || timedata.LatestSequence() != 0 || len(publisher.pendingObsByMsgID) != 0 || len(publisher.lastPointStates) != 0 {
		t.Fatalf("expected offline buffers cleared: spool=%d seq=%d pending=%d pointStates=%d",
			spool.UsedBytes(), timedata.LatestSequence(), len(publisher.pendingObsByMsgID), len(publisher.lastPointStates))
	}
	if publisher.Ready() {
		t.Fatal("expected publisher Ready()==false after credential wipe")
	}

	packets := qosPub.Packets()
	if len(packets) != 1 {
		t.Fatalf("ACK packet count=%d, want 1", len(packets))
	}
	pkt := packets[0]
	if pkt.Topic != edgecontrol.LifecycleUplinkTopic(gatewayID) || pkt.QoS != 1 || pkt.Retain {
		t.Fatalf("unexpected ACK packet metadata: topic=%q qos=%d retain=%v", pkt.Topic, pkt.QoS, pkt.Retain)
	}
	_, ack, err := edgecontrol.DecodeFactoryResetAck(pkt.Payload, gatewayID)
	if err != nil {
		t.Fatalf("DecodeFactoryResetAck: %v", err)
	}
	wantArtifacts := []string{"credentials", "config_shadow", "offline_buffer"}
	if ack.ResetID != resetID || ack.Status != edgecontrol.FactoryResetAckStatusWiped || !slices.Equal(ack.WipedArtifacts, wantArtifacts) {
		t.Fatalf("unexpected decoded ACK: %+v", ack)
	}
}

func TestMQTTPublisherFactoryResetPartialWipePreservesUnselectedArtifacts(t *testing.T) {
	t.Parallel()

	const gatewayID = "0195f300-3000-7000-8000-000000000778"
	const resetID = "0195f300-6000-7000-8000-000000000778"

	dir := t.TempDir()
	certPath := filepath.Join(dir, "gateway.crt")
	keyPath := filepath.Join(dir, "gateway.key")
	if err := os.WriteFile(certPath, []byte("CERT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	shadowPath := filepath.Join(dir, "config-shadow.v1.json")
	shadow := newEdgeConfigShadow(shadowPath)
	if _, err := shadow.applyDesired(context.Background(), GatewayDesiredConfig{DesiredRevision: 2}); err != nil {
		t.Fatal(err)
	}
	spool, err := newMQTTEvidenceSpool(filepath.Join(dir, "outbound"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Enqueue("msg-keep", EvidenceTelemetryNormal, "hvac/v1/"+gatewayID+"/up/telemetry", []byte(`{"keep":true}`)); err != nil {
		t.Fatal(err)
	}

	qosPub := &captureResetQoSPublisher{}
	connected := &atomic.Bool{}
	connected.Store(true)
	publisher := &MQTTPublisher{
		gatewayID:     gatewayID,
		config:        MQTTGatewayConfig{GatewayID: gatewayID, CertFile: certPath, KeyFile: keyPath},
		qosPub:        qosPub,
		evidenceSpool: spool,
		configShadow:  shadow,
		connected:     connected,
	}

	now := time.Date(2026, 11, 23, 2, 10, 0, 0, time.UTC)
	envBytes, err := edgecontrol.EncodeFactoryReset(gatewayID, edgecontrol.FactoryResetPayload{
		ResetID:           resetID,
		WipeCredentials:   false,
		WipeConfigShadow:  true,
		WipeOfflineBuffer: false,
		Reason:            "shadow reset only",
		IssuedAt:          now.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.handleDownlinkFactoryResetMessage(context.Background(), envBytes); err != nil {
		t.Fatalf("partial reset failed: %v", err)
	}

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected certPath preserved when WipeCredentials=false: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected keyPath preserved when WipeCredentials=false: %v", err)
	}
	if _, err := os.Stat(shadowPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected shadowPath deleted, err=%v", err)
	}
	if spool.UsedBytes() == 0 || !publisher.Ready() {
		t.Fatalf("expected spool & connected state preserved, spool=%d ready=%v", spool.UsedBytes(), publisher.Ready())
	}
	packets := qosPub.Packets()
	if len(packets) != 1 {
		t.Fatalf("packets=%d, want 1", len(packets))
	}
	_, ack, err := edgecontrol.DecodeFactoryResetAck(packets[0].Payload, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ack.WipedArtifacts, []string{"config_shadow"}) || ack.Status != edgecontrol.FactoryResetAckStatusWiped {
		t.Fatalf("unexpected partial ACK: %+v", ack)
	}
}
