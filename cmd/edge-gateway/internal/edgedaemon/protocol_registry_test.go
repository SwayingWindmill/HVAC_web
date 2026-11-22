package edgedaemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type stubBACnetAdapter struct {
	id string
}

func (a *stubBACnetAdapter) Component() edgecontrol.ComponentDescriptor {
	return edgecontrol.ComponentDescriptor{ID: a.id, Enabled: true, Kind: edgecontrol.ComponentDeviceDriver, Type: "BACNET"}
}

func (a *stubBACnetAdapter) Channels() []edgecontrol.ChannelDescriptor {
	return []edgecontrol.ChannelDescriptor{{
		ComponentID:  a.id,
		ChannelID:    "supply_temp",
		PointID:      a.id + ":supply_temp",
		DataType:     edgecontrol.DataTypeDouble,
		Access:       edgecontrol.AccessReadOnly,
		PollPriority: edgecontrol.PriorityHigh,
	}}
}

func (a *stubBACnetAdapter) Poll(_ context.Context, at time.Time) ([]edgecontrol.ChannelUpdate, error) {
	return []edgecontrol.ChannelUpdate{{
		Address: a.id + "/supply_temp",
		Sample:  edgecontrol.Sample{Value: edgecontrol.DoubleValue(7.2), Quality: edgecontrol.QualityGood, ObservedAt: at, Sequence: 1},
	}}, nil
}

func (a *stubBACnetAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, _ []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	return nil, nil
}

func TestProtocolBridgeRegistrySharesRTUBusAndExtendsBACnet(t *testing.T) {
	reg := NewProtocolBridgeRegistry(map[string]SerialBusConfig{
		"/dev/ttyAS3": {Speed: 9600, DataBits: 8, Parity: "N", StopBits: 1},
	})
	t.Cleanup(func() { _ = reg.Close() })

	ep := "/dev/ttyAS3"
	timeoutMS := 500
	retries := 1
	uid2, uid3 := 2, 3
	makeRTUInput := func(key string, uid *int) edgecontrol.ModbusDriverInput {
		return edgecontrol.ModbusDriverInput{
			DeviceKey:  key,
			DriverType: "MODBUS_RTU",
			Endpoint:   &ep,
			UnitID:     uid,
			TimeoutMS:  &timeoutMS,
			Retries:    &retries,
			Registers: []edgecontrol.ModbusRegisterInput{{
				PointCode:     "active_power_kw",
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

	if _, err := reg.BuildAdapter(makeRTUInput("meter-01", &uid2)); err != nil {
		t.Fatalf("build first RTU adapter failed: %v", err)
	}
	if _, err := reg.BuildAdapter(makeRTUInput("meter-02", &uid3)); err != nil {
		t.Fatalf("build second RTU adapter failed: %v", err)
	}
	if got := reg.SharedRTUBridgeCount(); got != 1 {
		t.Fatalf("SharedRTUBridgeCount = %d, want 1 shared bridge for /dev/ttyAS3", got)
	}

	// BACnet returns explicit extension sentinel until registered.
	_, err := reg.BuildAdapter(edgecontrol.ModbusDriverInput{DeviceKey: "ahu-01", DriverType: DriverTypeBACnet})
	if !errors.Is(err, ErrBACnetBridgeNotRegistered) {
		t.Fatalf("expected ErrBACnetBridgeNotRegistered, got %v", err)
	}

	if err := reg.RegisterFactory(DriverTypeBACnet, func(input edgecontrol.ModbusDriverInput) (edgecontrol.DeviceAdapter, error) {
		return &stubBACnetAdapter{id: input.DeviceKey}, nil
	}); err != nil {
		t.Fatalf("RegisterFactory failed: %v", err)
	}
	adapter, err := reg.BuildAdapter(edgecontrol.ModbusDriverInput{DeviceKey: "ahu-01", DriverType: DriverTypeBACnet})
	if err != nil {
		t.Fatalf("expected registered BACnet factory to build adapter: %v", err)
	}
	if adapter.Component().ID != "ahu-01" || adapter.Component().Type != "BACNET" {
		t.Fatalf("unexpected BACnet component: %+v", adapter.Component())
	}

	cfg := Config{
		GatewayID: "gw-bus",
		StateDir:  t.TempDir(),
		SerialBuses: map[string]SerialBusConfig{
			"/dev/ttyAS3": {Speed: 9600, DataBits: 8, Parity: "N", StopBits: 1, TurnaroundDelayMS: 45},
		},
	}
	cfg.ApplyDefaults("0.1.2")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid turnaroundDelayMs rejected: %v", err)
	}
	cfg.SerialBuses["/dev/ttyAS3"] = SerialBusConfig{TurnaroundDelayMS: -1}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative turnaroundDelayMs to fail validation")
	}
}

func TestProtocolBridgeRegistryBuildsAndPoolsBACnetIPAndOPCUA(t *testing.T) {
	reg := NewProtocolBridgeRegistry(nil)
	t.Cleanup(func() { _ = reg.Close() })

	tmo, ret := 800, 1
	bacEP := "127.0.0.1:47808"
	dev1, dev2 := 101, 102
	for _, tc := range []struct {
		key  string
		inst int
	}{
		{"ahu-bac-1", dev1},
		{"ahu-bac-2", dev2},
	} {
		inst := tc.inst
		ad, err := reg.BuildAdapter(edgecontrol.ModbusDriverInput{
			DeviceKey:  tc.key,
			DriverType: edgecontrol.DriverTypeBACnetIP,
			Endpoint:   &bacEP,
			TimeoutMS:  &tmo,
			Retries:    &ret,
			BACnet: &edgecontrol.BACnetDriverSpecInput{
				DeviceInstance: &inst,
				Objects: []edgecontrol.BACnetObjectInput{{
					PointCode:      "supply_temp",
					ObjectType:     "ANALOG_INPUT",
					ObjectInstance: 1,
					PollPriority:   "HIGH",
				}},
			},
		})
		if err != nil {
			t.Fatalf("BuildAdapter BACNET_IP %s: %v", tc.key, err)
		}
		if ad.Component().FactoryID != edgecontrol.DeclarativeBACnetFactoryID {
			t.Fatalf("unexpected FactoryID %s", ad.Component().FactoryID)
		}
	}
	if got := reg.SharedBACnetBridgeCount(); got != 1 {
		t.Fatalf("SharedBACnetBridgeCount = %d, want 1", got)
	}

	uaEP := "opc.tcp://127.0.0.1:4840/plant"
	for _, key := range []string{"bms-ua-1", "bms-ua-2"} {
		ad, err := reg.BuildAdapter(edgecontrol.ModbusDriverInput{
			DeviceKey:  key,
			DriverType: edgecontrol.DriverTypeOPCUA,
			Endpoint:   &uaEP,
			TimeoutMS:  &tmo,
			Retries:    &ret,
			OPCUA: &edgecontrol.OPCUADriverSpecInput{
				SecurityPolicy: edgecontrol.OPCUASecurityPolicyBasic256Sha256,
				SecurityMode:   edgecontrol.OPCUASecurityModeSignAndEncrypt,
				Nodes: []edgecontrol.OPCUANodeInput{{
					PointCode:    "chilled_flow",
					NodeID:       "ns=2;i=10",
					PollPriority: "HIGH",
				}},
			},
		})
		if err != nil {
			t.Fatalf("BuildAdapter OPC_UA %s: %v", key, err)
		}
		if ad.Component().FactoryID != edgecontrol.DeclarativeOPCUAFactoryID {
			t.Fatalf("unexpected OPC-UA FactoryID %s", ad.Component().FactoryID)
		}
	}
	if got := reg.SharedOPCUABridgeCount(); got != 1 {
		t.Fatalf("SharedOPCUABridgeCount = %d, want 1 pooled OPC-UA session", got)
	}
	if err := reg.Close(); err != nil {
		t.Fatalf("reg.Close(): %v", err)
	}
	if reg.SharedBACnetBridgeCount() != 0 || reg.SharedOPCUABridgeCount() != 0 {
		t.Fatal("expected pooled BACnet/OPC-UA bridges cleared on Close()")
	}
}
