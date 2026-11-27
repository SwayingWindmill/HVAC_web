package edgedaemon

import (
	"context"
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

type shadowStubTowerAdapter struct {
	component edgecontrol.ComponentDescriptor
	channels  []edgecontrol.ChannelDescriptor
	mode      string
	fan       float64
}

func (a *shadowStubTowerAdapter) Component() edgecontrol.ComponentDescriptor { return a.component }
func (a *shadowStubTowerAdapter) Channels() []edgecontrol.ChannelDescriptor  { return a.channels }
func (a *shadowStubTowerAdapter) Poll(_ context.Context, at time.Time) ([]edgecontrol.ChannelUpdate, error) {
	return []edgecontrol.ChannelUpdate{
		{Address: a.component.ID + "/run_state", Sample: edgecontrol.Sample{Value: edgecontrol.StringValue("RUNNING"), Quality: edgecontrol.QualityGood, ObservedAt: at}},
		{Address: a.component.ID + "/fault_code", Sample: edgecontrol.Sample{Value: edgecontrol.StringValue(""), Quality: edgecontrol.QualityGood, ObservedAt: at}},
		{Address: a.component.ID + "/local_remote_mode", Sample: edgecontrol.Sample{Value: edgecontrol.StringValue(a.mode), Quality: edgecontrol.QualityGood, ObservedAt: at}},
		{Address: a.component.ID + "/fan_speed", Sample: edgecontrol.Sample{Value: edgecontrol.DoubleValue(a.fan), Quality: edgecontrol.QualityGood, ObservedAt: at}},
	}, nil
}
func (a *shadowStubTowerAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	var out []edgecontrol.DeviceWriteResult
	for _, d := range decisions {
		if !d.Accepted || d.Effective == nil {
			continue
		}
		if d.Address == a.component.ID+"/fan_speed_sp" {
			a.fan = d.Effective.Double
		}
		val := *d.Effective
		out = append(out, edgecontrol.DeviceWriteResult{Address: d.Address, Success: true, Code: "APPLIED", AppliedValue: &val})
	}
	return out, nil
}

type shadowTowerIntentController struct {
	target float64
}

func (c *shadowTowerIntentController) ID() string { return "test-tower-intent" }
func (c *shadowTowerIntentController) Run(_ context.Context, _ edgecontrol.ProcessImage, plan *edgecontrol.ControlPlan) error {
	_, _, _ = plan.Request("ct-01/fan_speed_sp", c.ID(), edgecontrol.DoubleValue(c.target))
	return nil
}

func TestShadowManagerWiresCoolingTowerLocalRemoteInterlock(t *testing.T) {
	now := time.Unix(1_760_200_000, 0).UTC()
	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	tower := &shadowStubTowerAdapter{
		component: edgecontrol.ComponentDescriptor{
			ID: "ct-01", Enabled: true, Kind: edgecontrol.ComponentDeviceDriver, Type: "COOLING_TOWER",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID, Version: "1.0.0",
			Profiles: []edgecontrol.CapabilityProfileID{edgecontrol.ProfileCoolingTower},
			ChannelBindings: map[edgecontrol.SemanticChannel]string{
				edgecontrol.SemanticRunState:         "ct-01/run_state",
				edgecontrol.SemanticFaultCode:        "ct-01/fault_code",
				edgecontrol.SemanticLocalRemoteMode:  "ct-01/local_remote_mode",
				edgecontrol.SemanticFanSpeed:         "ct-01/fan_speed",
				edgecontrol.SemanticFanSpeedSetpoint: "ct-01/fan_speed_sp",
			},
		},
		channels: []edgecontrol.ChannelDescriptor{
			{ComponentID: "ct-01", ChannelID: "run_state", PointID: "ct:run", DataType: edgecontrol.DataTypeString, Access: edgecontrol.AccessReadOnly, Category: edgecontrol.ChannelCategoryOpenemsType, PollPriority: edgecontrol.PriorityHigh, LocalPersistencePriority: edgecontrol.PriorityHigh, RemotePersistencePriority: edgecontrol.PriorityHigh, AggregationPriority: edgecontrol.PriorityHigh, ResendPriority: edgecontrol.PriorityHigh},
			{ComponentID: "ct-01", ChannelID: "fault_code", PointID: "ct:fault", DataType: edgecontrol.DataTypeString, Access: edgecontrol.AccessReadOnly, Category: edgecontrol.ChannelCategoryOpenemsType, PollPriority: edgecontrol.PriorityHigh, LocalPersistencePriority: edgecontrol.PriorityHigh, RemotePersistencePriority: edgecontrol.PriorityHigh, AggregationPriority: edgecontrol.PriorityHigh, ResendPriority: edgecontrol.PriorityHigh},
			{ComponentID: "ct-01", ChannelID: "local_remote_mode", PointID: "ct:mode", DataType: edgecontrol.DataTypeString, Access: edgecontrol.AccessReadOnly, Category: edgecontrol.ChannelCategoryOpenemsType, PollPriority: edgecontrol.PriorityHigh, LocalPersistencePriority: edgecontrol.PriorityHigh, RemotePersistencePriority: edgecontrol.PriorityHigh, AggregationPriority: edgecontrol.PriorityHigh, ResendPriority: edgecontrol.PriorityHigh},
			{ComponentID: "ct-01", ChannelID: "fan_speed", PointID: "ct:fan", DataType: edgecontrol.DataTypeDouble, Access: edgecontrol.AccessReadOnly, Category: edgecontrol.ChannelCategoryOpenemsType, PollPriority: edgecontrol.PriorityHigh, LocalPersistencePriority: edgecontrol.PriorityHigh, RemotePersistencePriority: edgecontrol.PriorityHigh, AggregationPriority: edgecontrol.PriorityHigh, ResendPriority: edgecontrol.PriorityHigh},
			{ComponentID: "ct-01", ChannelID: "fan_speed_sp", PointID: "ct:fansp", DataType: edgecontrol.DataTypeDouble, Access: edgecontrol.AccessReadWrite, Category: edgecontrol.ChannelCategoryOpenemsType, PollPriority: edgecontrol.PriorityHigh, LocalPersistencePriority: edgecontrol.PriorityHigh, RemotePersistencePriority: edgecontrol.PriorityHigh, AggregationPriority: edgecontrol.PriorityHigh, ResendPriority: edgecontrol.PriorityHigh},
		},
		mode: "LOCAL",
		fan:  72.0,
	}
	if err := host.RegisterAdapter(tower); err != nil {
		t.Fatal(err)
	}
	interlock, err := edgecontrol.NewHydraulicInterlockController(edgecontrol.HydraulicInterlockConfig{
		Policy: edgecontrol.DefaultAutonomyPolicyConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{
		{Priority: 10, Critical: true, Controller: interlock},
		{Priority: 100, Controller: &shadowTowerIntentController{target: 40.0}},
	}); err != nil {
		t.Fatal(err)
	}

	mgr := NewShadowManager("0193f000-2000-7000-8000-000000000045", t.TempDir(), host, nil, interlock, nil, nil, nil)
	mgr.now = func() time.Time { return now }
	mgr.refreshControllerBindings()

	res, err := host.RunCycle(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cycle.Decisions) != 1 || res.Cycle.Decisions[0].Accepted ||
		res.Cycle.Decisions[0].ConstraintReasons[0].Reason != "CABINET_LOCAL_MANUAL" {
		t.Fatalf("expected CABINET_LOCAL_MANUAL block on wired tower, got %+v", res.Cycle.Decisions)
	}
	if tower.fan != 72.0 {
		t.Fatalf("expected local tower fan 72.0 preserved, got %v", tower.fan)
	}
}
