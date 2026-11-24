package edgedaemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

type fakeConcModbus struct {
	holding map[uint8]map[uint16]uint16
	input   map[uint8]map[uint16]uint16
	writes  []string
}

func newFakeConcModbus(unitID uint8) *fakeConcModbus {
	return &fakeConcModbus{
		holding: map[uint8]map[uint16]uint16{unitID: {}},
		input:   map[uint8]map[uint16]uint16{unitID: {}},
	}
}

func (f *fakeConcModbus) ReadHoldingRegisters(_ context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	out := make([]uint16, quantity)
	regs := f.holding[unitID]
	for i := range quantity {
		out[i] = regs[address+i]
	}
	return out, nil
}

func (f *fakeConcModbus) ReadInputRegisters(_ context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	out := make([]uint16, quantity)
	regs := f.input[unitID]
	for i := range quantity {
		out[i] = regs[address+i]
	}
	return out, nil
}

func (f *fakeConcModbus) WriteSingleRegister(_ context.Context, unitID uint8, address, value uint16) error {
	if f.holding[unitID] == nil {
		f.holding[unitID] = map[uint16]uint16{}
	}
	f.holding[unitID][address] = value
	f.writes = append(f.writes, fmt.Sprintf("fc06:%d:0x%x=%d", unitID, address, value))
	return nil
}

func (f *fakeConcModbus) WriteMultipleRegisters(_ context.Context, unitID uint8, address uint16, values []uint16) error {
	if f.holding[unitID] == nil {
		f.holding[unitID] = map[uint16]uint16{}
	}
	for i, v := range values {
		f.holding[unitID][address+uint16(i)] = v
	}
	f.writes = append(f.writes, fmt.Sprintf("fc16:%d:0x%x+%d", unitID, address, len(values)))
	return nil
}

func TestConcentratorRPCExecutorAllOperationsAndGuards(t *testing.T) {
	gwID := "01a14000-4000-7000-8000-000000000100"
	reqID := "0194f000-0000-7000-8000-000000000010"
	fixedNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

	bus := newFakeConcModbus(20)
	bus.holding[20][0x9c43] = 20
	bus.holding[20][0x9c45] = 2
	bus.holding[20][0x9c48] = 60
	bus.holding[20][0x9c49] = 30
	bus.holding[20][0x9d0f] = 1
	bus.holding[20][0x9d15] = 0
	bus.holding[20][0x9d6d] = 20
	bus.holding[20][0x9d6e] = 433
	bus.holding[20][0x9d6f] = 1
	bus.holding[20][0x9d70] = 7
	bus.holding[20][0x9d71] = 8
	bus.holding[20][0x9d73] = 100
	bus.holding[20][0x9d74] = 100
	bus.holding[20][0x9d76] = 1
	bus.holding[20][concRegEIDBase+7*2] = 0
	bus.holding[20][concRegEIDBase+7*2+1] = 50
	bus.input[20][concRegSensorTypeBase+7] = 0x35
	bus.input[20][concRegVoltageBase+7] = 3250
	bus.input[20][concRegTempBase+7] = uint16(0x10000 - 55)
	bus.input[20][concRegRSSIBase+7] = uint16(0x10000 - 62)

	exec, err := NewConcentratorRPCExecutor(ConcentratorExecutorOptions{
		GatewayID:     gwID,
		Bus:           bus,
		SettleMS:      1,
		ProtectedUIDs: []uint8{2, 3, 4, 5, 10, 31},
		Now:           func() time.Time { return fixedNow },
		Sleep:         func(_ context.Context, _ time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewConcentratorRPCExecutor: %v", err)
	}

	nowMs := fixedNow.UnixMilli()
	protResp := exec.Execute(t.Context(), ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 31, Operation: "READ_STATUS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
	})
	if protResp.Status != "REJECTED" || protResp.ErrorCode != "CONCENTRATOR_UID_PROTECTED" {
		t.Fatalf("expected protected UID rejection, got %+v", protResp)
	}
	wpProt := exec.Execute(t.Context(), ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 20, Operation: "WRITE_PARAMS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
		Params: map[string]int{"modbusId": 10},
	})
	if wpProt.Status != "REJECTED" || wpProt.ErrorCode != "CONCENTRATOR_UID_PROTECTED" {
		t.Fatalf("expected WRITE_PARAMS modbusId=10 protected UID rejection, got %+v", wpProt)
	}

	reqBytes, _ := json.Marshal(ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 20, Operation: "READ_STATUS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
	})
	replyBytes, ok := exec.HandlePayload(t.Context(), ConcentratorDownlinkTopic(gwID), reqBytes)
	if !ok {
		t.Fatal("expected HandlePayload to accept valid downlink")
	}
	var statusResp ConcentratorRPCResponse
	if err := json.Unmarshal(replyBytes, &statusResp); err != nil {
		t.Fatal(err)
	}
	if statusResp.Status != "OK" || len(statusResp.Channels) != 1 || statusResp.Channels[0].Channel != 8 || *statusResp.Channels[0].TemperatureC != -5.5 || *statusResp.Channels[0].RSSIDBm != -62 {
		t.Fatalf("unexpected READ_STATUS response: %+v", statusResp)
	}

	wpResp := exec.Execute(t.Context(), ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 20, Operation: "WRITE_PARAMS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
		Params: map[string]int{"rfTxPower": 22, "rssiOut": 0},
	})
	if wpResp.Status != "OK" || wpResp.WrittenCount != 2 || wpResp.Params.RfTxPower != 22 || wpResp.Params.RssiOut != 0 {
		t.Fatalf("unexpected WRITE_PARAMS response: %+v", wpResp)
	}

	slots := make([]ConcentratorShadowEIDSlot, 200)
	for i := range 200 {
		slots[i] = ConcentratorShadowEIDSlot{Slot: i + 1}
	}
	for i := range 25 {
		slots[i].EID = uint32(1000 + i)
	}
	slots[7].EID = 50
	weResp := exec.Execute(t.Context(), ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 20, Operation: "WRITE_EIDS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
		Slots: slots,
	})
	if weResp.Status != "OK" || weResp.UpdatedSlots != 24 || weResp.ChunkCount != 2 {
		t.Fatalf("unexpected WRITE_EIDS response: %+v", weResp)
	}

	slots[100].EID = 1000
	dupResp := exec.Execute(t.Context(), ConcentratorRPCRequest{
		SchemaVersion: "1.0", RequestID: reqID, GatewayID: gwID, UnitID: 20, Operation: "WRITE_EIDS", IssuedAt: nowMs - 10, ExpireAt: nowMs + 10000,
		Slots: slots,
	})
	if dupResp.Status != "REJECTED" || dupResp.ErrorCode != "CONCENTRATOR_EID_DUPLICATE" {
		t.Fatalf("expected CONCENTRATOR_EID_DUPLICATE, got %+v", dupResp)
	}
}
