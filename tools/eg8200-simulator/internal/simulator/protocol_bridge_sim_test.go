package simulator

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func TestSimulatorSIM004BACnetIPAndOPCUABridgesEndToEnd(t *testing.T) {
	t.Parallel()

	bacSrv, err := NewVirtualBACnetServer("127.0.0.1:0", 401)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bacSrv.Close() }()
	bacSrv.SetObjectValue(edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjAnalogInput, Instance: 1}, edgecontrol.BACnetAppValue{Type: edgecontrol.DataTypeFloat, Real: 11.5})
	bacSrv.SetObjectValue(edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjAnalogOutput, Instance: 2}, edgecontrol.BACnetAppValue{Type: edgecontrol.DataTypeFloat, Real: 7.0})

	uaSrv, err := NewVirtualOPCUAServer("127.0.0.1:0", "edge-user", "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uaSrv.Close() }()
	uaSrv.SetNodeValue("ns=2;i=10", edgecontrol.OPCUADataValue{HasValue: true, Value: edgecontrol.DoubleValue(28.0), StatusCode: edgecontrol.OPCUAStatusGood}, false)
	uaSrv.SetNodeValue("ns=2;i=11", edgecontrol.OPCUADataValue{HasValue: true, Value: edgecontrol.DoubleValue(40.0), StatusCode: edgecontrol.OPCUAStatusGood}, true)

	bacBridge, err := edgecontrol.NewBACnetIPBridge(edgecontrol.BACnetIPBridgeConfig{
		LocalAddr: "127.0.0.1:0",
		Timeout:   200 * time.Millisecond,
		Retries:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bacBridge.Close() }()

	// Discovery via Who-Is/I-Am against VirtualBACnetServer.
	found, err := bacBridge.DiscoverDevices(context.Background(), bacSrv.Endpoint(), nil, nil)
	if err != nil || len(found) != 1 || found[0].DeviceID.Instance != 401 {
		t.Fatalf("DiscoverDevices got %+v err=%v", found, err)
	}

	bacEP := bacSrv.Endpoint()
	devInst, writePrio := 401, 8
	tmo, ret := 400, 1
	bacProfile, err := edgecontrol.ValidateBACnetDriverInput(edgecontrol.ModbusDriverInput{
		DeviceKey:  "ahu-sim-bacnet",
		DriverType: edgecontrol.DriverTypeBACnetIP,
		Endpoint:   &bacEP,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		BACnet: &edgecontrol.BACnetDriverSpecInput{
			DeviceInstance: &devInst,
			WritePriority:  &writePrio,
			Objects: []edgecontrol.BACnetObjectInput{
				{PointCode: "sa_temp", ObjectType: "ANALOG_INPUT", ObjectInstance: 1, DataType: "DOUBLE", COVSubscribe: true, PollPriority: "HIGH"},
				{PointCode: "sa_sp", ObjectType: "ANALOG_OUTPUT", ObjectInstance: 2, DataType: "DOUBLE", Writable: true, PollPriority: "HIGH"},
			},
		},
	}, edgecontrol.ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if err != nil {
		t.Fatal(err)
	}

	bacAdapter, err := edgecontrol.NewDeclarativeBACnetAdapter(bacProfile, bacBridge)
	if err != nil {
		t.Fatal(err)
	}

	uaBridge, err := edgecontrol.NewOPCUABridge(edgecontrol.OPCUABridgeConfig{
		Endpoint:       uaSrv.Endpoint(),
		SecurityPolicy: edgecontrol.OPCUASecurityPolicyBasic256Sha256,
		SecurityMode:   edgecontrol.OPCUASecurityModeSignAndEncrypt,
		AuthMode:       edgecontrol.OPCUAAuthUsername,
		Username:       "edge-user",
		CredentialRef:  "sim://opcua",
		CredentialLookup: func(ref string) (string, error) {
			if ref != "sim://opcua" {
				return "", errors.New("unknown ref")
			}
			return "secret-key", nil
		},
		Timeout: 400 * time.Millisecond,
		Retries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uaBridge.Close() }()

	uaEP := uaSrv.Endpoint()
	uaProfile, err := edgecontrol.ValidateOPCUADriverInput(edgecontrol.ModbusDriverInput{
		DeviceKey:  "bms-sim-opcua",
		DriverType: edgecontrol.DriverTypeOPCUA,
		Endpoint:   &uaEP,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		OPCUA: &edgecontrol.OPCUADriverSpecInput{
			SecurityPolicy:         edgecontrol.OPCUASecurityPolicyBasic256Sha256,
			SecurityMode:           edgecontrol.OPCUASecurityModeSignAndEncrypt,
			AuthMode:               edgecontrol.OPCUAAuthUsername,
			Username:               "edge-user",
			CredentialRef:          "sim://opcua",
			SubscriptionIntervalMS: 200,
			Nodes: []edgecontrol.OPCUANodeInput{
				{PointCode: "oat_temp", NodeID: "ns=2;i=10", DataType: "DOUBLE", Subscribe: true, PollPriority: "HIGH"},
				{PointCode: "pump_freq", NodeID: "ns=2;i=11", DataType: "DOUBLE", Writable: true, PollPriority: "HIGH"},
			},
		},
	}, edgecontrol.ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if err != nil {
		t.Fatal(err)
	}

	uaAdapter, err := edgecontrol.NewDeclarativeOPCUAAdapter(uaProfile, uaBridge)
	if err != nil {
		t.Fatal(err)
	}

	host, err := edgecontrol.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range []edgecontrol.DeviceAdapter{bacAdapter, uaAdapter} {
		if err := host.RegisterAdapter(ad); err != nil {
			t.Fatal(err)
		}
	}
	intentCtrl, err := edgecontrol.NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]edgecontrol.ControllerBinding{{Priority: 40, Controller: intentCtrl}}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	if _, err := host.IntentStore().Put(edgecontrol.ControlIntent{
		ID: "cmd-bac", Address: "ahu-sim-bacnet/sa_sp", Requested: edgecontrol.DoubleValue(8.2),
		IssuedAt: t0, ExpiresAt: t0.Add(time.Minute), Source: "CLOUD_COMMAND",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.IntentStore().Put(edgecontrol.ControlIntent{
		ID: "cmd-ua", Address: "bms-sim-opcua/pump_freq", Requested: edgecontrol.DoubleValue(43.5),
		IssuedAt: t0, ExpiresAt: t0.Add(time.Minute), Source: "CLOUD_COMMAND",
	}); err != nil {
		t.Fatal(err)
	}

	// Cycle 1: polls BACnet & OPC-UA, executes leased writes with EDGE-013 readback.
	res1, err := host.RunCycle(context.Background(), t0)
	if err != nil || len(res1.WriteResults) != 2 {
		t.Fatalf("Cycle 1 failed: res=%+v err=%v", res1, err)
	}
	for _, wr := range res1.WriteResults {
		if !wr.Success || !wr.ReadbackVerified {
			t.Fatalf("expected ReadbackVerified=true, got %+v", wr)
		}
	}
	if gotBac, ok := bacSrv.GetObjectValue(edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjAnalogOutput, Instance: 2}); !ok || math.Abs(float64(gotBac.Real-8.2)) > 1e-2 {
		t.Fatalf("BACnet server did not retain written value 8.2: %+v", gotBac)
	}
	if gotUA, ok := uaSrv.GetNodeValue("ns=2;i=11"); !ok || math.Abs(gotUA.Value.Double-43.5) > 1e-6 {
		t.Fatalf("OPC-UA server did not retain written value 43.5: %+v", gotUA)
	}

	// Simulate OPC-UA drop + value change, and BACnet COV push -> next cycle reconnects OPC-UA and ingests BACnet COV!
	uaSrv.DropAllClients()
	uaSrv.SetNodeValue("ns=2;i=10", edgecontrol.OPCUADataValue{HasValue: true, Value: edgecontrol.DoubleValue(31.25), StatusCode: edgecontrol.OPCUAStatusGood}, false)
	bacSrv.SetObjectValue(edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjAnalogInput, Instance: 1}, edgecontrol.BACnetAppValue{Type: edgecontrol.DataTypeFloat, Real: 13.75})

	// Allow UDP COV packet to arrive on bacBridge socket cleanly without sleep by polling ReadProperty once on unrelated object.
	_, _ = bacBridge.ReadProperty(context.Background(), bacSrv.Endpoint(), 0, edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjAnalogOutput, Instance: 2}, edgecontrol.BACnetPropPresentValue)

	res2, err := host.RunCycle(context.Background(), t0.Add(time.Second))
	if err != nil {
		t.Fatalf("Cycle 2 reconnect/COV failed: %v", err)
	}
	snapUA, _ := res2.Cycle.Image.Get("bms-sim-opcua/oat_temp")
	snapBac, _ := res2.Cycle.Image.Get("ahu-sim-bacnet/sa_temp")
	if math.Abs(snapUA.Sample.Value.Double-31.25) > 1e-6 {
		t.Fatalf("expected reconnected OPC-UA subscription oat_temp=31.25, got %+v", snapUA.Sample)
	}
	if math.Abs(snapBac.Sample.Value.Double-13.75) > 1e-2 {
		t.Fatalf("expected BACnet COV sa_temp=13.75, got %+v", snapBac.Sample)
	}

	// Network timeout fault isolation on BACnet server keeps OPC-UA running cleanly and triggers FaultStatus on BACnet only.
	bacSrv.SetDropRequests(true)
	resFault, err := host.RunCycle(context.Background(), t0.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Cycle 3 fault-isolation RunCycle errored: %v", err)
	}
	if !bacAdapter.FaultStatus().FaultActive || uaAdapter.FaultStatus().FaultActive {
		t.Fatalf("expected fault isolated to BACnet: bac=%+v ua=%+v", bacAdapter.FaultStatus(), uaAdapter.FaultStatus())
	}
	_ = resFault
}
