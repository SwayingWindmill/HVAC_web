package edgecontrol

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

func TestBACnetCodecRoundtripAndValidation(t *testing.T) {
	t.Parallel()

	// 1. Who-Is + I-Am codec roundtrip.
	low, high := uint32(100), uint32(500)
	whoFrame, err := EncodeBACnetPacket(BACnetPacket{
		BVLCFunction: BVLCFuncOriginalBroadcastNPDU,
		PDUType:      BACnetAPDUTypeUnconfirmedRequest,
		Service:      BACnetServiceWhoIs,
		Payload:      EncodeBACnetWhoIsPayload(&low, &high),
	})
	if err != nil {
		t.Fatalf("Encode Who-Is: %v", err)
	}
	decWho, err := DecodeBACnetPacket(whoFrame)
	if err != nil {
		t.Fatalf("Decode Who-Is: %v", err)
	}
	gotLow, gotHigh, err := DecodeBACnetWhoIsPayload(decWho.Payload)
	if err != nil || gotLow == nil || gotHigh == nil || *gotLow != 100 || *gotHigh != 500 {
		t.Fatalf("Who-Is mismatch: low=%v high=%v err=%v", gotLow, gotHigh, err)
	}

	iamPayload, err := EncodeBACnetIAmPayload(BACnetIAMAnnouncement{
		DeviceID:     BACnetObjectID{Type: BACnetObjDevice, Instance: 301},
		MaxAPDU:      1476,
		Segmentation: 3,
		VendorID:     42,
	})
	if err != nil {
		t.Fatal(err)
	}
	iamFrame, err := EncodeBACnetPacket(BACnetPacket{
		PDUType: BACnetAPDUTypeUnconfirmedRequest,
		Service: BACnetServiceIAm,
		Payload: iamPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	decIAmPkt, err := DecodeBACnetPacket(iamFrame)
	if err != nil {
		t.Fatal(err)
	}
	ann, err := DecodeBACnetIAmPayload(decIAmPkt.Payload)
	if err != nil || ann.DeviceID.Instance != 301 || ann.VendorID != 42 {
		t.Fatalf("I-Am mismatch: %+v err=%v", ann, err)
	}

	// 2. WriteProperty + COV notification roundtrip.
	prio := uint8(8)
	wpPayload, err := EncodeBACnetWritePropertyRequest(
		BACnetObjectID{Type: BACnetObjAnalogOutput, Instance: 2},
		BACnetPropPresentValue,
		BACnetAppValue{Type: DataTypeFloat, Real: 7.5},
		&prio,
	)
	if err != nil {
		t.Fatal(err)
	}
	objID, propID, val, gotPrio, err := DecodeBACnetWritePropertyRequest(wpPayload)
	if err != nil || objID.Type != BACnetObjAnalogOutput || objID.Instance != 2 || propID != BACnetPropPresentValue || math.Abs(float64(val.Real-7.5)) > 1e-4 || gotPrio == nil || *gotPrio != 8 {
		t.Fatalf("WriteProperty decode mismatch: obj=%+v prop=%d val=%+v prio=%v err=%v", objID, propID, val, gotPrio, err)
	}

	covPayload, err := EncodeBACnetUnconfirmedCOVNotification(BACnetCOVNotification{
		SubscriberProcessID: 1,
		InitiatingDeviceID:  BACnetObjectID{Type: BACnetObjDevice, Instance: 301},
		MonitoredObjectID:   BACnetObjectID{Type: BACnetObjAnalogInput, Instance: 4},
		TimeRemainingSec:    120,
		Properties: []BACnetPropertyValue{
			{PropertyID: BACnetPropPresentValue, Value: BACnetAppValue{Type: DataTypeFloat, Real: 12.25}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	covDec, err := DecodeBACnetUnconfirmedCOVNotification(covPayload)
	if err != nil || covDec.MonitoredObjectID.Instance != 4 || len(covDec.Properties) != 1 || math.Abs(float64(covDec.Properties[0].Value.Real-12.25)) > 1e-4 {
		t.Fatalf("COV decode mismatch: %+v err=%v", covDec, err)
	}

	// 3. Validation enforces EDGE-003, input-only read-only, and endpoint checks.
	ep := "127.0.0.1:47808"
	devInst := 301
	tmo, ret := 500, 1
	_, err = ValidateBACnetDriverInput(ModbusDriverInput{
		DeviceKey:  "ahu-1",
		DriverType: DriverTypeBACnetIP,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		BACnet: &BACnetDriverSpecInput{
			DeviceInstance: &devInst,
			Objects: []BACnetObjectInput{{
				PointCode:      "chw_valve_cmd",
				ObjectType:     "ANALOG_INPUT",
				ObjectInstance: 1,
				Writable:       true,
				PollPriority:   "HIGH",
			}},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if err == nil {
		t.Fatal("expected ANALOG_INPUT writable rejection")
	}

	_, err = ValidateBACnetDriverInput(ModbusDriverInput{
		DeviceKey:  "chiller-1",
		DriverType: DriverTypeBACnetIP,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		BACnet: &BACnetDriverSpecInput{
			DeviceInstance: &devInst,
			Objects: []BACnetObjectInput{{
				PointCode:      "emergency_stop_cmd",
				SafetyBoundary: "HARD_REALTIME_SAFETY",
				ObjectType:     "BINARY_OUTPUT",
				ObjectInstance: 1,
				Writable:       true,
				PollPriority:   "VERY_HIGH",
			}},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("expected ErrHardRealtimeSafetyBoundary for writable safety point, got %v", err)
	}
}

func TestBACnetIPBridgeAndAdapterLoopbackUDP(t *testing.T) {
	t.Parallel()

	// Lightweight UDP responder implementing Who-Is/I-Am, ReadProperty, WriteProperty, SubscribeCOV, and COV push.
	srvConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srvConn.Close() }()

	var mu sync.Mutex
	values := map[BACnetObjectID]BACnetAppValue{
		{Type: BACnetObjAnalogInput, Instance: 1}:  {Type: DataTypeFloat, Real: 6.8},
		{Type: BACnetObjAnalogOutput, Instance: 2}: {Type: DataTypeFloat, Real: 7.0},
	}
	var dropRequests bool
	var lastSubClient *net.UDPAddr

	done := make(chan struct{})
	defer close(done)
	go func() {
		buf := make([]byte, maxBACnetPacketBytes)
		for {
			n, raddr, rErr := srvConn.ReadFromUDP(buf)
			if rErr != nil {
				return
			}
			pkt, err := DecodeBACnetPacket(buf[:n])
			if err != nil {
				continue
			}
			mu.Lock()
			drop := dropRequests
			mu.Unlock()
			if drop {
				continue
			}
			switch pkt.PDUType {
			case BACnetAPDUTypeUnconfirmedRequest:
				if pkt.Service == BACnetServiceWhoIs {
					iam, _ := EncodeBACnetIAmPayload(BACnetIAMAnnouncement{
						DeviceID:     BACnetObjectID{Type: BACnetObjDevice, Instance: 1001},
						MaxAPDU:      1476,
						Segmentation: 3,
						VendorID:     7,
					})
					resp, _ := EncodeBACnetPacket(BACnetPacket{
						PDUType: BACnetAPDUTypeUnconfirmedRequest,
						Service: BACnetServiceIAm,
						Payload: iam,
					})
					_, _ = srvConn.WriteToUDP(resp, raddr)
				}
			case BACnetAPDUTypeConfirmedRequest:
				switch pkt.Service {
				case BACnetServiceReadProperty:
					objID, propID, _ := DecodeBACnetReadPropertyRequest(pkt.Payload)
					mu.Lock()
					val, ok := values[objID]
					mu.Unlock()
					if !ok {
						resp, _ := EncodeBACnetPacket(BACnetPacket{
							PDUType: BACnetAPDUTypeError, InvokeID: pkt.InvokeID, Service: pkt.Service,
							Payload: EncodeBACnetErrorPayload(1, 31),
						})
						_, _ = srvConn.WriteToUDP(resp, raddr)
						continue
					}
					ack, _ := EncodeBACnetReadPropertyACK(objID, propID, val)
					resp, _ := EncodeBACnetPacket(BACnetPacket{
						PDUType: BACnetAPDUTypeComplexACK, InvokeID: pkt.InvokeID, Service: pkt.Service, Payload: ack,
					})
					_, _ = srvConn.WriteToUDP(resp, raddr)
				case BACnetServiceWriteProperty:
					objID, _, val, _, _ := DecodeBACnetWritePropertyRequest(pkt.Payload)
					mu.Lock()
					values[objID] = val
					mu.Unlock()
					resp, _ := EncodeBACnetPacket(BACnetPacket{
						PDUType: BACnetAPDUTypeSimpleACK, InvokeID: pkt.InvokeID, Service: pkt.Service,
					})
					_, _ = srvConn.WriteToUDP(resp, raddr)
				case BACnetServiceSubscribeCOV:
					mu.Lock()
					lastSubClient = raddr
					mu.Unlock()
					resp, _ := EncodeBACnetPacket(BACnetPacket{
						PDUType: BACnetAPDUTypeSimpleACK, InvokeID: pkt.InvokeID, Service: pkt.Service,
					})
					_, _ = srvConn.WriteToUDP(resp, raddr)
				}
			}
		}
	}()

	ep := srvConn.LocalAddr().String()
	bridge, err := NewBACnetIPBridge(BACnetIPBridgeConfig{
		LocalAddr: "127.0.0.1:0",
		Timeout:   150 * time.Millisecond,
		Retries:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bridge.Close() }()

	// Discovery finds device 1001.
	found, err := bridge.DiscoverDevices(context.Background(), ep, nil, nil)
	if err != nil || len(found) != 1 || found[0].DeviceID.Instance != 1001 {
		t.Fatalf("DiscoverDevices got %+v, err=%v", found, err)
	}

	devInst, prio := 1001, 8
	tmo, ret := 500, 1
	prof, err := ValidateBACnetDriverInput(ModbusDriverInput{
		DeviceKey:  "bacnet-ahu-1",
		DriverType: DriverTypeBACnetIP,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		BACnet: &BACnetDriverSpecInput{
			DeviceInstance: &devInst,
			WritePriority:  &prio,
			Objects: []BACnetObjectInput{
				{PointCode: "supply_temp", ObjectType: "ANALOG_INPUT", ObjectInstance: 1, DataType: "DOUBLE", COVSubscribe: true, PollPriority: "HIGH"},
				{PointCode: "supply_temp_sp", ObjectType: "ANALOG_OUTPUT", ObjectInstance: 2, DataType: "DOUBLE", Writable: true, PollPriority: "HIGH"},
			},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if err != nil {
		t.Fatal(err)
	}

	adapter, err := NewDeclarativeBACnetAdapter(prof, bridge)
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	updates, err := adapter.Poll(context.Background(), t0)
	if err != nil || len(updates) != 2 {
		t.Fatalf("initial Poll: len=%d err=%v", len(updates), err)
	}

	// Push COV update while keeping adapter deterministic (poll on next cycle picks up COV value without waiting on ReadProperty change).
	mu.Lock()
	subAddr := lastSubClient
	mu.Unlock()
	if subAddr == nil {
		t.Fatal("expected SubscribeCOV registered client address")
	}
	covPayload, _ := EncodeBACnetUnconfirmedCOVNotification(BACnetCOVNotification{
		SubscriberProcessID: 1,
		InitiatingDeviceID:  BACnetObjectID{Type: BACnetObjDevice, Instance: 1001},
		MonitoredObjectID:   BACnetObjectID{Type: BACnetObjAnalogInput, Instance: 1},
		TimeRemainingSec:    120,
		Properties: []BACnetPropertyValue{
			{PropertyID: BACnetPropPresentValue, Value: BACnetAppValue{Type: DataTypeFloat, Real: 9.25}},
		},
	})
	covFrame, _ := EncodeBACnetPacket(BACnetPacket{
		PDUType: BACnetAPDUTypeUnconfirmedRequest,
		Service: BACnetServiceUnconfirmedCOVNotification,
		Payload: covPayload,
	})
	_, _ = srvConn.WriteToUDP(covFrame, subAddr)

	// Poll briefly until UDP goroutine ingests COV frame, or verify COV delivery deterministically via Wait loop <= 50ms.
	deadline := time.Now().Add(200 * time.Millisecond)
	var covSeen bool
	for time.Now().Before(deadline) {
		adapter.mu.Lock()
		_, covSeen = adapter.covLatest["bacnet-ahu-1/supply_temp"]
		adapter.mu.Unlock()
		if covSeen {
			break
		}
		_, _ = bridge.ReadProperty(context.Background(), ep, 0, BACnetObjectID{Type: BACnetObjAnalogOutput, Instance: 2}, BACnetPropPresentValue)
	}
	if !covSeen {
		t.Fatal("expected COV notification delivered to bridge")
	}
	updatesCOV, err := adapter.Poll(context.Background(), t0.Add(time.Second))
	if err != nil {
		t.Fatalf("COV Poll err: %v", err)
	}
	var gotSupply float64
	for _, u := range updatesCOV {
		if u.Address == "bacnet-ahu-1/supply_temp" {
			gotSupply = u.Sample.Value.Double
		}
	}
	if math.Abs(gotSupply-9.25) > 1e-3 {
		t.Fatalf("expected COV supply_temp 9.25, got %v", gotSupply)
	}

	// WriteProperty + immediate ReadProperty readback verification (EDGE-013) + read-only rejection.
	wrRes, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{
		{Address: "bacnet-ahu-1/supply_temp_sp", ControllerID: "test", Accepted: true, Effective: &Value{Type: DataTypeDouble, Double: 8.5}},
		{Address: "bacnet-ahu-1/supply_temp", ControllerID: "test", Accepted: true, Effective: &Value{Type: DataTypeDouble, Double: 5.0}},
	})
	if err != nil || len(wrRes) != 2 {
		t.Fatalf("Apply err=%v results=%+v", err, wrRes)
	}
	if !wrRes[0].Success || !wrRes[0].ReadbackVerified || wrRes[0].Code != "APPLIED" || math.Abs(wrRes[0].ReadbackValue.Double-8.5) > 1e-3 {
		t.Fatalf("unexpected writable BACnet Apply result: %+v", wrRes[0])
	}
	if wrRes[1].Success || wrRes[1].Code != "CHANNEL_READ_ONLY" {
		t.Fatalf("expected CHANNEL_READ_ONLY on input object, got %+v", wrRes[1])
	}

	// Timeout -> fault backoff + recovery.
	mu.Lock()
	dropRequests = true
	mu.Unlock()
	if _, err := adapter.Poll(context.Background(), t0.Add(2*time.Second)); !errors.Is(err, ErrCommunicationFault) {
		t.Fatalf("expected ErrCommunicationFault on drop, got %v", err)
	}
	if !adapter.FaultStatus().FaultActive {
		t.Fatal("expected FaultStatus active after timeout")
	}
	mu.Lock()
	dropRequests = false
	mu.Unlock()
	if _, err := adapter.Poll(context.Background(), t0.Add(10*time.Second)); err != nil {
		t.Fatalf("expected clean recovery after backoff window, got %v", err)
	}
}
