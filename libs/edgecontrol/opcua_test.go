package edgecontrol

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

func TestOPCUANodeIDCodecSecurityAndValidation(t *testing.T) {
	t.Parallel()

	// 1. NodeID formatting and binary encoding roundtrip across numeric/string/guid/opaque.
	nodeIDs := []string{
		"ns=2;i=1001",
		"ns=3;s=Chiller.SupplyTemp",
		"ns=1;g=550e8400-e29b-41d4-a716-446655440000",
		"ns=4;b=AQIDBA==",
	}
	for _, raw := range nodeIDs {
		nid, err := ParseOPCUANodeID(raw)
		if err != nil {
			t.Fatalf("ParseOPCUANodeID(%q): %v", raw, err)
		}
		encoded := appendOPCUANodeIDBinary(nil, nid)
		decoded, _, err := readOPCUANodeIDBinary(encoded, 0)
		if err != nil || decoded.String() != nid.String() {
			t.Fatalf("NodeId binary roundtrip %q got %q (err=%v)", raw, decoded.String(), err)
		}
	}

	// 2. Basic256Sha256 SignAndEncrypt key derivation, roundtrip, and HMAC tamper rejection.
	cNonce := make([]byte, 32)
	sNonce := make([]byte, 32)
	_, _ = rand.Read(cNonce)
	_, _ = rand.Read(sNonce)
	cKeys, sKeys := DeriveOPCUABasic256Sha256Keys(cNonce, sNonce)

	n1, _ := ParseOPCUANodeID("ns=2;s=Plant.CHW_Setpoint")
	body := EncodeOPCUAReadRequest([]OPCUANodeID{n1})
	wire, err := EncodeOPCUASecureMessage(OPCUAMsgMessage, 10, 1, 5, 9, body, OPCUASecurityModeSignAndEncrypt, &cKeys)
	if err != nil {
		t.Fatalf("EncodeOPCUASecureMessage: %v", err)
	}
	_, _, seq, req, gotBody, err := DecodeOPCUASecureMessage(wire, OPCUASecurityModeSignAndEncrypt, &cKeys)
	if err != nil || seq != 5 || req != 9 || len(gotBody) != len(body) {
		t.Fatalf("DecodeOPCUASecureMessage failed: seq=%d req=%d err=%v", seq, req, err)
	}
	_ = sKeys

	// Flip 1 ciphertext bit -> HMAC verification must fail closed.
	tampered := append([]byte(nil), wire...)
	tampered[len(tampered)-5] ^= 0x01
	if _, _, _, _, _, err := DecodeOPCUASecureMessage(tampered, OPCUASecurityModeSignAndEncrypt, &cKeys); err == nil {
		t.Fatal("expected HMAC-SHA256 verification failure on tampered Basic256Sha256 frame")
	}

	// 3. SecurityPolicy/SecurityMode coherence and EDGE-003 rejection.
	ep := "opc.tcp://127.0.0.1:4840"
	tmo, ret := 1000, 1
	if _, err := ValidateOPCUADriverInput(ModbusDriverInput{
		DeviceKey:  "bms-1",
		DriverType: DriverTypeOPCUA,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		OPCUA: &OPCUADriverSpecInput{
			SecurityPolicy: OPCUASecurityPolicyNone,
			SecurityMode:   OPCUASecurityModeSignAndEncrypt,
			Nodes:          []OPCUANodeInput{{PointCode: "sup_temp", NodeID: "ns=2;i=1", PollPriority: "HIGH"}},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true}); err == nil {
		t.Fatal("expected securityPolicy=None with securityMode=SignAndEncrypt error")
	}

	_, err = ValidateOPCUADriverInput(ModbusDriverInput{
		DeviceKey:  "bms-1",
		DriverType: DriverTypeOPCUA,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		OPCUA: &OPCUADriverSpecInput{
			SecurityPolicy: OPCUASecurityPolicyBasic256Sha256,
			SecurityMode:   OPCUASecurityModeSignAndEncrypt,
			Nodes: []OPCUANodeInput{{
				PointCode:      "fire_interlock_trip",
				SafetyBoundary: "HARD_REALTIME_SAFETY",
				NodeID:         "ns=2;i=99",
				Writable:       true,
				PollPriority:   "VERY_HIGH",
			}},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if !errors.Is(err, ErrHardRealtimeSafetyBoundary) {
		t.Fatalf("expected ErrHardRealtimeSafetyBoundary, got %v", err)
	}
}

func TestOPCUABridgeLoopbackCryptoReadWriteSubscribeAndReconnect(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	var mu sync.Mutex
	values := map[string]OPCUADataValue{
		"ns=2;i=10": {HasValue: true, Value: DoubleValue(24.5), StatusCode: OPCUAStatusGood},
		"ns=2;i=11": {HasValue: true, Value: DoubleValue(7.0), StatusCode: OPCUAStatusGood},
	}
	dropNextConn := false
	serverNonce := make([]byte, 32)
	_, _ = rand.Read(serverNonce)

	serveConn := func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		secMode := OPCUASecurityModeNone
		var cKeys, sKeys OPCUACryptoKeys
		hasKeys := false
		subItems := map[uint32]OPCUANodeID{}

		for {
			mType, frame, rErr := ReadOPCUAFrame(conn)
			if rErr != nil {
				return
			}
			switch mType {
			case OPCUAMsgHello:
				_, _ = conn.Write(EncodeOPCUAAcknowledge())
			case OPCUAMsgOpenChannel:
				opn, oErr := DecodeOPCUAOpenRequest(frame)
				if oErr != nil {
					return
				}
				switch opn.SecurityMode {
				case 3:
					secMode = OPCUASecurityModeSignAndEncrypt
				case 2:
					secMode = OPCUASecurityModeSign
				}
				if opn.SecurityPolicy == OPCUASecurityPolicyBasic256Sha256 {
					cKeys, sKeys = DeriveOPCUABasic256Sha256Keys(opn.ClientNonce, serverNonce)
					hasKeys = true
				}
				resp := EncodeOPCUAOpenResponse(OPCUAOpenChannelFrame{
					SecurityPolicy:  opn.SecurityPolicy,
					SeqNum:          opn.SeqNum,
					RequestID:       opn.RequestID,
					TokenChannelID:  77,
					TokenID:         1,
					RevisedLifetime: 3600000,
					ServerNonce:     serverNonce,
				})
				_, _ = conn.Write(resp)
			case OPCUAMsgMessage:
				var ckPtr, skPtr *OPCUACryptoKeys
				if hasKeys {
					ckPtr, skPtr = &cKeys, &sKeys
				}
				chID, tokID, seq, reqID, body, dErr := DecodeOPCUASecureMessage(frame, secMode, ckPtr)
				if dErr != nil {
					return
				}
				svcID, _ := PeekOPCUAServiceID(body)
				var respBody []byte
				switch svcID {
				case OPCUAServiceCreateSessionReq:
					respBody = EncodeOPCUACreateSessionResponse(OPCUAStatusGood, 88, serverNonce)
				case OPCUAServiceActivateSessionReq:
					respBody = EncodeOPCUAActivateSessionResponse(OPCUAStatusGood)
				case OPCUAServiceReadReq:
					nids, _ := DecodeOPCUAReadRequest(body)
					mu.Lock()
					dvs := make([]OPCUADataValue, len(nids))
					for i, nid := range nids {
						if dv, ok := values[nid.String()]; ok {
							dvs[i] = dv
						} else {
							dvs[i] = OPCUADataValue{StatusCode: OPCUAStatusBadNodeID}
						}
					}
					mu.Unlock()
					respBody = EncodeOPCUAReadResponse(OPCUAStatusGood, dvs)
				case OPCUAServiceWriteReq:
					wrs, _ := DecodeOPCUAWriteRequest(body)
					mu.Lock()
					rcs := make([]uint32, len(wrs))
					for i, w := range wrs {
						values[w.NodeID.String()] = OPCUADataValue{HasValue: true, Value: w.Value, StatusCode: OPCUAStatusGood}
						rcs[i] = OPCUAStatusGood
					}
					mu.Unlock()
					respBody = EncodeOPCUAWriteResponse(OPCUAStatusGood, rcs)
				case OPCUAServiceCreateSubscriptionReq:
					respBody = EncodeOPCUACreateSubscriptionResponse(OPCUAStatusGood, 501, 200)
				case OPCUAServiceCreateMonitoredItemsReq:
					_, items, _ := DecodeOPCUACreateMonitoredItemsRequest(body)
					rcs := make([]uint32, len(items))
					for i, it := range items {
						subItems[it.ClientHandle] = it.NodeID
						rcs[i] = OPCUAStatusGood
					}
					respBody = EncodeOPCUACreateMonitoredItemsResponse(OPCUAStatusGood, rcs)
				case OPCUAServicePublishReq:
					mu.Lock()
					var notifs []OPCUAMonitoredNotification
					for handle, nid := range subItems {
						if dv, ok := values[nid.String()]; ok {
							notifs = append(notifs, OPCUAMonitoredNotification{ClientHandle: handle, Value: dv})
						}
					}
					drop := dropNextConn
					dropNextConn = false
					mu.Unlock()
					if drop {
						return
					}
					respBody = EncodeOPCUAPublishResponse(OPCUAStatusGood, 501, notifs)
				}
				outWire, _ := EncodeOPCUASecureMessage(OPCUAMsgMessage, chID, tokID, seq, reqID, respBody, secMode, skPtr)
				_, _ = conn.Write(outWire)
			}
		}
	}

	go func() {
		for {
			c, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			go serveConn(c)
		}
	}()

	ep := "opc.tcp://" + ln.Addr().String()
	tmo, ret := 500, 1
	prof, err := ValidateOPCUADriverInput(ModbusDriverInput{
		DeviceKey:  "bms-opcua-1",
		DriverType: DriverTypeOPCUA,
		Endpoint:   &ep,
		TimeoutMS:  &tmo,
		Retries:    &ret,
		OPCUA: &OPCUADriverSpecInput{
			SecurityPolicy:         OPCUASecurityPolicyBasic256Sha256,
			SecurityMode:           OPCUASecurityModeSignAndEncrypt,
			AuthMode:               OPCUAAuthAnonymous,
			SubscriptionIntervalMS: 200,
			Nodes: []OPCUANodeInput{
				{PointCode: "oat_temp", NodeID: "ns=2;i=10", DataType: "DOUBLE", Subscribe: true, PollPriority: "HIGH"},
				{PointCode: "chw_sp", NodeID: "ns=2;i=11", DataType: "DOUBLE", Writable: true, PollPriority: "HIGH"},
			},
		},
	}, ModbusDriverValidationOptions{RequireDeviceKey: true, RequireGatewayBindings: true})
	if err != nil {
		t.Fatalf("ValidateOPCUADriverInput: %v", err)
	}

	adapter, err := NewDeclarativeOPCUAAdapter(prof, nil)
	if err != nil {
		t.Fatalf("NewDeclarativeOPCUAAdapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	t0 := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	updates, err := adapter.Poll(context.Background(), t0)
	if err != nil || len(updates) != 2 {
		t.Fatalf("initial OPC-UA Poll: len=%d err=%v", len(updates), err)
	}

	// Simulate server disconnect during next Publish -> adapter's OPCUABridge retries, reconnects & re-subscribes cleanly!
	mu.Lock()
	values["ns=2;i=10"] = OPCUADataValue{HasValue: true, Value: DoubleValue(26.75), StatusCode: OPCUAStatusGood}
	dropNextConn = true
	mu.Unlock()

	updates2, err := adapter.Poll(context.Background(), t0.Add(time.Second))
	if err != nil || len(updates2) != 2 {
		t.Fatalf("reconnect Poll failed: len=%d err=%v", len(updates2), err)
	}
	var gotOAT float64
	for _, u := range updates2 {
		if u.Address == "bms-opcua-1/oat_temp" {
			gotOAT = u.Sample.Value.Double
		}
	}
	if math.Abs(gotOAT-26.75) > 1e-6 {
		t.Fatalf("expected updated subscribed oat_temp 26.75 after reconnect, got %v", gotOAT)
	}

	// WriteNodes + immediate ReadNodes readback verification + read-only channel protection.
	wrRes, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{
		{Address: "bms-opcua-1/chw_sp", ControllerID: "test", Accepted: true, Effective: &Value{Type: DataTypeDouble, Double: 6.2}},
		{Address: "bms-opcua-1/oat_temp", ControllerID: "test", Accepted: true, Effective: &Value{Type: DataTypeDouble, Double: 10}},
	})
	if err != nil || len(wrRes) != 2 {
		t.Fatalf("OPC-UA Apply err=%v results=%+v", err, wrRes)
	}
	if !wrRes[0].Success || !wrRes[0].ReadbackVerified || wrRes[0].Code != "APPLIED" || math.Abs(wrRes[0].ReadbackValue.Double-6.2) > 1e-6 {
		t.Fatalf("unexpected writable OPC-UA Apply result: %+v", wrRes[0])
	}
	if wrRes[1].Success || wrRes[1].Code != "CHANNEL_READ_ONLY" {
		t.Fatalf("expected CHANNEL_READ_ONLY for subscribed read-only node, got %+v", wrRes[1])
	}
}
