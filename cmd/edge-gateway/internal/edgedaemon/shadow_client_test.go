package edgedaemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func testModbusDriver(key, endpoint string, unitID int) edgecontrol.ModbusDriverInput {
	ep := endpoint
	uid := unitID
	timeoutMS := 500
	retries := 1
	return edgecontrol.ModbusDriverInput{
		DeviceKey:  key,
		DriverType: "MODBUS_RTU",
		Endpoint:   &ep,
		UnitID:     &uid,
		TimeoutMS:  &timeoutMS,
		Retries:    &retries,
		Registers: []edgecontrol.ModbusRegisterInput{{
			PointCode:     "supply_temp",
			FunctionCode:  3,
			Address:       100,
			RegisterCount: 1,
			RawType:       "UINT16",
			ByteOrder:     "BIG_ENDIAN",
			WordOrder:     "BIG_ENDIAN",
			Scale:         0.1,
			Offset:        0,
			PollPriority:  "HIGH",
		}},
	}
}

func TestShadowManagerHotReloadsDriversRulesAndMQTTBatch(t *testing.T) {
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

	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy: edgecontrol.DefaultAutonomyPolicyConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := edgecontrol.NewAutonomousFallbackController(edgecontrol.AutonomousFallbackConfig{
		Policy: edgecontrol.DefaultAutonomyPolicyConfig(),
	})
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

	if err := mgr.LoadOrBootstrap(Config{Drivers: []edgecontrol.ModbusDriverInput{testModbusDriver("meter-01", "/dev/ttyAS3", 2)}}); err != nil {
		t.Fatalf("LoadOrBootstrap: %v", err)
	}
	if _, ok := host.Adapter("meter-01"); !ok {
		t.Fatal("expected initial meter-01 adapter on host")
	}

	// Apply revision 1 via MQTT CHANGE_BATCH removing meter-01 and adding meter-02 + rules + autonomy
	bundle := &edgecontrol.EdgeRuleBundle{
		SchemaVersion:  1,
		SiteID:         "0193f000-2000-7000-8000-000000000041",
		BundleRevision: 3,
		CompiledAt:     now.Format(time.RFC3339),
		Rules:          []edgecontrol.CompiledEdgeRule{},
	}
	digest, err := bundle.ComputeDigest()
	if err != nil {
		t.Fatal(err)
	}
	bundle.EdgeBundleDigest = digest
	autonomy := edgecontrol.DefaultAutonomyPolicyConfig()
	autonomy.Enabled = true
	autonomy.OfflineGraceSeconds = 180

	desired := GatewayDesiredConfig{
		DesiredRevision: 1,
		DriverConfigs:   []edgecontrol.ModbusDriverInput{testModbusDriver("meter-02", "/dev/ttyAS3", 3)},
		AutonomyPolicy:  &autonomy,
		EdgeRuleBundle:  bundle,
	}
	batchWire, _, err := edgecontrol.EncodeShadowChangeBatch(gwID, 1, desired, now)
	if err != nil {
		t.Fatal(err)
	}
	ackWire, err := mgr.HandleMQTTChangeBatch(batchWire)
	if err != nil {
		t.Fatalf("HandleMQTTChangeBatch: %v", err)
	}
	_, ack, err := edgecontrol.DecodeShadowChangeAck(ackWire, gwID)
	if err != nil || ack.Status != edgecontrol.ShadowAckStatusAcked {
		t.Fatalf("expected ACKED, got %+v err=%v", ack, err)
	}
	if _, ok := host.Adapter("meter-01"); ok {
		t.Fatal("meter-01 should have been unregistered")
	}
	if _, ok := host.Adapter("meter-02"); !ok {
		t.Fatal("meter-02 should be registered")
	}
	if !interlock.Policy().Enabled || interlock.Policy().OfflineGraceSeconds != 180 {
		t.Fatalf("autonomy policy did not hot-update: %+v", interlock.Policy())
	}
	if ruleEngine.Status().BundleRevision != 3 {
		t.Fatalf("ruleEngine BundleRevision = %d, want 3", ruleEngine.Status().BundleRevision)
	}

	// Reject malformed driver without dropping existing meter-02
	badDriver := testModbusDriver("bad-drv", "/dev/ttyAS3", 0)
	badReport, stale, err := mgr.ApplyDesired(GatewayDesiredConfig{
		DesiredRevision: 2,
		DriverConfigs:   []edgecontrol.ModbusDriverInput{badDriver},
	})
	if err != nil || stale || badReport.AppliedStatus != shadowStatusRejected {
		t.Fatalf("expected REJECTED report for invalid driver, got %+v stale=%v err=%v", badReport, stale, err)
	}
	if _, ok := host.Adapter("meter-02"); !ok {
		t.Fatal("existing meter-02 should remain active after rejected update")
	}

	// Tampered payloadDigest -> QUARANTINED
	badBatchBytes, err := json.Marshal(edgecontrol.ShadowReplicationEnvelope{
		SchemaVersion: edgecontrol.ShadowReplicationSchemaVersion,
		MessageID:     "0193f000-2000-7000-8000-000000000099",
		EdgeID:        gwID,
		SentAt:        now.UnixMilli(),
		Type:          edgecontrol.ShadowReplicationTypeBatch,
		Payload: json.RawMessage(mustJSON(t, edgecontrol.ShadowChangeBatchPayload{
			Items: []edgecontrol.ShadowChangeBatchItem{{
				Cursor:         2,
				DeliveryID:     "0193f000-2000-7000-8000-000000000098",
				OwnerDomain:    edgecontrol.ShadowOwnerDomainDriverConfig,
				OrderingKey:    "gateway-shadow:" + gwID,
				SourceRevision: 2,
				PayloadDigest:  hex.EncodeToString(sha256.New().Sum(nil))[:64],
				Payload:        json.RawMessage(`{"desiredRevision":2}`),
			}},
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	qAckWire, err := mgr.HandleMQTTChangeBatch(badBatchBytes)
	if err != nil {
		t.Fatalf("HandleMQTTChangeBatch tampered: %v", err)
	}
	_, qAck, err := edgecontrol.DecodeShadowChangeAck(qAckWire, gwID)
	if err != nil || qAck.Status != edgecontrol.ShadowAckStatusQuarantined {
		t.Fatalf("expected QUARANTINED ack on bad digest, got %+v err=%v", qAck, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
