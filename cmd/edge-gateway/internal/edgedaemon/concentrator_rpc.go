package edgedaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	concentratorRPCSchemaVersion = "1.0"
	concentratorEIDSlotCount     = 200
	concentratorMaxEIDsPerFC16   = 20
	concentratorDefaultSettleMS  = 300

	concRegCommit         uint16 = 0x9c41
	concCommitWriteParams uint16 = 6
	concCommitWriteEIDs   uint16 = 8
	concRegBlockAStart    uint16 = 0x9c43
	concRegBlockACount    uint16 = 7
	concRegBlockBStart    uint16 = 0x9d0f
	concRegBlockBCount    uint16 = 7
	concRegBlockCStart    uint16 = 0x9d6d
	concRegBlockCCount    uint16 = 10
	concRegEIDBase        uint16 = 0xb799
	concRegSensorTypeBase uint16 = 0x7531
	concRegVoltageBase    uint16 = 0x75f9
	concRegTempBase       uint16 = 0x76c1
	concRegRSSIBase       uint16 = 0x9089
)

// ConcentratorDownlinkTopic returns the canonical MQTT RPC downlink topic for UID 20 concentrator operations.
func ConcentratorDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/concentrator"
}

// ConcentratorUplinkTopic returns the canonical MQTT RPC reply topic for UID 20 concentrator operations.
func ConcentratorUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/concentrator"
}

// ConcentratorModbusBus abstracts the Modbus RTU register read/write operations required by concentrator RPC.
type ConcentratorModbusBus interface {
	ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error)
	WriteSingleRegister(ctx context.Context, unitID uint8, address, value uint16) error
	WriteMultipleRegisters(ctx context.Context, unitID uint8, address uint16, values []uint16) error
}

// ModbusConcentratorBus adapts *edgecontrol.ModbusTCPBridge to ConcentratorModbusBus.
type ModbusConcentratorBus struct {
	Bridge *edgecontrol.ModbusTCPBridge
}

func (b ModbusConcentratorBus) ReadHoldingRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("concentrator modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name: fmt.Sprintf("conc-fc03-%d-%x", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadHoldingRegisters, Address: address, Quantity: quantity,
	})
}

func (b ModbusConcentratorBus) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	if b.Bridge == nil {
		return nil, errors.New("concentrator modbus bridge is nil")
	}
	return b.Bridge.Read(ctx, edgecontrol.ModbusReadTask{
		Name: fmt.Sprintf("conc-fc04-%d-%x", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionReadInputRegisters, Address: address, Quantity: quantity,
	})
}

func (b ModbusConcentratorBus) WriteSingleRegister(ctx context.Context, unitID uint8, address, value uint16) error {
	if b.Bridge == nil {
		return errors.New("concentrator modbus bridge is nil")
	}
	return b.Bridge.Write(ctx, edgecontrol.ModbusWriteTask{
		Name: fmt.Sprintf("conc-fc06-%d-%x", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionWriteSingleRegister, Address: address, Values: []uint16{value},
	})
}

func (b ModbusConcentratorBus) WriteMultipleRegisters(ctx context.Context, unitID uint8, address uint16, values []uint16) error {
	if b.Bridge == nil {
		return errors.New("concentrator modbus bridge is nil")
	}
	return b.Bridge.Write(ctx, edgecontrol.ModbusWriteTask{
		Name: fmt.Sprintf("conc-fc16-%d-%x", unitID, address), UnitID: unitID,
		FunctionCode: edgecontrol.ModbusFunctionWriteMultipleRegisters, Address: address, Values: values,
	})
}

// ConcentratorChannelReading mirrors modules/connectivity/pkg/concentrator.ChannelReading.
type ConcentratorChannelReading struct {
	Channel      int      `json:"channel"`
	EID          uint32   `json:"eid"`
	Online       bool     `json:"online"`
	SensorType   int      `json:"sensorType"`
	SensorModel  string   `json:"sensorModel"`
	TemperatureC *float64 `json:"temperatureC"`
	VoltageMV    *int     `json:"voltageMv"`
	RSSIDBm      *int     `json:"rssiDbm"`
}

// ConcentratorRPCRequest matches the cloud-to-edge concentrator RPC envelope.
type ConcentratorRPCRequest struct {
	SchemaVersion string                      `json:"schemaVersion"`
	RequestID     string                      `json:"requestId"`
	GatewayID     string                      `json:"gatewayId"`
	UnitID        int                         `json:"unitId"`
	Operation     string                      `json:"operation"`
	IssuedAt      int64                       `json:"issuedAt"`
	ExpireAt      int64                       `json:"expireAt"`
	Params        map[string]int              `json:"params,omitempty"`
	Slots         []ConcentratorShadowEIDSlot `json:"slots,omitempty"`
}

// ConcentratorRPCResponse matches modules/connectivity/pkg/concentrator.RPCResponse.
type ConcentratorRPCResponse struct {
	SchemaVersion string                       `json:"schemaVersion"`
	RequestID     string                       `json:"requestId"`
	GatewayID     string                       `json:"gatewayId"`
	UnitID        int                          `json:"unitId"`
	Operation     string                       `json:"operation"`
	Status        string                       `json:"status"`
	ErrorCode     string                       `json:"errorCode,omitempty"`
	ErrorMessage  string                       `json:"errorMessage,omitempty"`
	RespondedAt   int64                        `json:"respondedAt"`
	Params        *ConcentratorShadowParams    `json:"params,omitempty"`
	Channels      []ConcentratorChannelReading `json:"channels,omitempty"`
	Slots         []ConcentratorShadowEIDSlot  `json:"slots,omitempty"`
	WrittenCount  int                          `json:"writtenCount,omitempty"`
	UpdatedSlots  int                          `json:"updatedSlots,omitempty"`
	ChunkCount    int                          `json:"chunkCount,omitempty"`
}

type concParamSpec struct {
	key     string
	address uint16
	min     int
	max     int
}

var concParamSpecList = []concParamSpec{
	{key: "modbusId", address: 0x9c43, min: 1, max: 247},
	{key: "baudRateCode", address: 0x9c45, min: 0, max: 5},
	{key: "offLineTime", address: 0x9c48, min: 1, max: 65535},
	{key: "dataUplinkCycle", address: 0x9c49, min: 1, max: 65535},
	{key: "localNet", address: 0x9d0f, min: 0, max: 65535},
	{key: "timeMark", address: 0x9d15, min: 0, max: 3},
	{key: "rfTxPower", address: 0x9d6d, min: 0, max: 30},
	{key: "rfFrequency", address: 0x9d6e, min: 100, max: 1000},
	{key: "rfBandWidth", address: 0x9d6f, min: 0, max: 2},
	{key: "rfSf", address: 0x9d70, min: 5, max: 12},
	{key: "rfLeadCode", address: 0x9d71, min: 1, max: 65535},
	{key: "txTimeout", address: 0x9d73, min: 1, max: 65535},
	{key: "rxTimeout", address: 0x9d74, min: 1, max: 65535},
	{key: "rssiOut", address: 0x9d76, min: 0, max: 1},
}

var concParamSpecMap = func() map[string]concParamSpec {
	m := make(map[string]concParamSpec, len(concParamSpecList))
	for _, spec := range concParamSpecList {
		m[spec.key] = spec
	}
	return m
}()

type concDirtyEIDChunk struct {
	startSlot    int
	slotCount    int
	startAddress uint16
	registers    []uint16
	eids         []uint32
}

// ConcentratorRPCExecutor executes wireless concentrator RPC operations on RS-485.
type ConcentratorRPCExecutor struct {
	gatewayID     string
	bus           ConcentratorModbusBus
	settle        time.Duration
	protectedUIDs map[uint8]struct{}
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
}

// ConcentratorExecutorOptions configures a ConcentratorRPCExecutor.
type ConcentratorExecutorOptions struct {
	GatewayID     string
	Bus           ConcentratorModbusBus
	SettleMS      int
	ProtectedUIDs []uint8
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
}

// NewConcentratorRPCExecutor constructs a ConcentratorRPCExecutor.
func NewConcentratorRPCExecutor(opts ConcentratorExecutorOptions) (*ConcentratorRPCExecutor, error) {
	gwID := strings.TrimSpace(opts.GatewayID)
	if gwID == "" {
		return nil, errors.New("concentrator executor requires gatewayId")
	}
	if opts.Bus == nil {
		return nil, errors.New("concentrator executor requires modbus bus")
	}
	settleMS := opts.SettleMS
	if settleMS <= 0 {
		settleMS = concentratorDefaultSettleMS
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = defaultEdgeSleep
	}
	protected := make(map[uint8]struct{}, len(opts.ProtectedUIDs))
	for _, uid := range opts.ProtectedUIDs {
		if uid > 0 {
			protected[uid] = struct{}{}
		}
	}
	return &ConcentratorRPCExecutor{
		gatewayID:     gwID,
		bus:           opts.Bus,
		settle:        time.Duration(settleMS) * time.Millisecond,
		protectedUIDs: protected,
		now:           nowFn,
		sleep:         sleepFn,
	}, nil
}

// HandlePayload decodes a JSON RPC request targeting gatewayID and returns the JSON reply envelope.
func (e *ConcentratorRPCExecutor) HandlePayload(ctx context.Context, topic string, payload []byte) ([]byte, bool) {
	if e == nil {
		return nil, false
	}
	if topic != ConcentratorDownlinkTopic(e.gatewayID) {
		return nil, false
	}
	var req ConcentratorRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, false
	}
	if strings.TrimSpace(req.GatewayID) != e.gatewayID {
		return nil, false
	}
	resp := e.Execute(ctx, req)
	wire, err := json.Marshal(resp)
	if err != nil {
		return nil, false
	}
	return wire, true
}

// Execute validates and runs READ_STATUS, WRITE_PARAMS, READ_EIDS, or WRITE_EIDS.
func (e *ConcentratorRPCExecutor) Execute(ctx context.Context, req ConcentratorRPCRequest) ConcentratorRPCResponse {
	base := func(status, errCode, errMsg string) ConcentratorRPCResponse {
		return ConcentratorRPCResponse{
			SchemaVersion: concentratorRPCSchemaVersion,
			RequestID:     req.RequestID,
			GatewayID:     req.GatewayID,
			UnitID:        req.UnitID,
			Operation:     req.Operation,
			Status:        status,
			ErrorCode:     errCode,
			ErrorMessage:  errMsg,
			RespondedAt:   e.now().UTC().UnixMilli(),
		}
	}

	if req.SchemaVersion != concentratorRPCSchemaVersion {
		return base("REJECTED", "CONCENTRATOR_SCHEMA_UNSUPPORTED", "unsupported RPC schemaVersion")
	}
	if !uuidv7.IsValid(req.RequestID) || !uuidv7.IsValid(req.GatewayID) {
		return base("REJECTED", "CONCENTRATOR_REQUEST_INVALID", "invalid gatewayId or requestId")
	}
	if req.UnitID < 1 || req.UnitID > 247 {
		return base("REJECTED", "CONCENTRATOR_REQUEST_INVALID", fmt.Sprintf("unitId=%d out of [1,247]", req.UnitID))
	}
	unitID := uint8(req.UnitID)
	if e.isProtectedUID(unitID) {
		return base("REJECTED", "CONCENTRATOR_UID_PROTECTED", fmt.Sprintf("unitId %d is protected on RS485 bus", req.UnitID))
	}
	if req.ExpireAt <= 0 || (req.IssuedAt > 0 && req.ExpireAt <= req.IssuedAt) || e.now().UTC().UnixMilli() >= req.ExpireAt {
		return base("FAILED", "CONCENTRATOR_RPC_EXPIRED", "request expired before execution")
	}

	switch req.Operation {
	case "WRITE_PARAMS":
		entries, code, msg := e.validateParamsPatch(req.Params)
		if code != "" {
			return base("REJECTED", code, msg)
		}
		params, count, err := e.writeParams(ctx, unitID, entries)
		if err != nil {
			return base("FAILED", "CONCENTRATOR_WRITE_FAILED", err.Error())
		}
		rep := base("OK", "", "")
		rep.Params = &params
		rep.WrittenCount = count
		return rep
	case "WRITE_EIDS":
		desired, code, msg := validateConcentratorEIDSlots(req.Slots)
		if code != "" {
			return base("REJECTED", code, msg)
		}
		slots, updated, chunks, err := e.writeEIDs(ctx, unitID, desired)
		if err != nil {
			return base("FAILED", "CONCENTRATOR_WRITE_FAILED", err.Error())
		}
		rep := base("OK", "", "")
		rep.Slots = slots
		rep.UpdatedSlots = updated
		rep.ChunkCount = chunks
		return rep
	case "READ_EIDS":
		slots, err := e.readAllEIDs(ctx, unitID)
		if err != nil {
			return base("FAILED", "CONCENTRATOR_READ_FAILED", err.Error())
		}
		rep := base("OK", "", "")
		rep.Slots = slots
		return rep
	case "READ_STATUS":
		params, channels, err := e.readStatus(ctx, unitID)
		if err != nil {
			return base("FAILED", "CONCENTRATOR_READ_FAILED", err.Error())
		}
		rep := base("OK", "", "")
		rep.Params = &params
		rep.Channels = channels
		return rep
	default:
		return base("REJECTED", "CONCENTRATOR_OPERATION_UNSUPPORTED", fmt.Sprintf("unsupported operation %s", req.Operation))
	}
}

func (e *ConcentratorRPCExecutor) readParams(ctx context.Context, unitID uint8) (ConcentratorShadowParams, error) {
	blockA, err := e.bus.ReadHoldingRegisters(ctx, unitID, concRegBlockAStart, concRegBlockACount)
	if err != nil {
		return ConcentratorShadowParams{}, err
	}
	blockB, err := e.bus.ReadHoldingRegisters(ctx, unitID, concRegBlockBStart, concRegBlockBCount)
	if err != nil {
		return ConcentratorShadowParams{}, err
	}
	blockC, err := e.bus.ReadHoldingRegisters(ctx, unitID, concRegBlockCStart, concRegBlockCCount)
	if err != nil {
		return ConcentratorShadowParams{}, err
	}
	if len(blockA) < int(concRegBlockACount) || len(blockB) < int(concRegBlockBCount) || len(blockC) < int(concRegBlockCCount) {
		return ConcentratorShadowParams{}, errors.New("incomplete holding register blocks returned by concentrator")
	}
	return ConcentratorShadowParams{
		ModbusID:        int(blockA[0]),
		BaudRateCode:    int(blockA[2]),
		OffLineTime:     int(blockA[5]),
		DataUplinkCycle: int(blockA[6]),
		LocalNet:        int(blockB[0]),
		TimeMark:        int(blockB[6]),
		RfTxPower:       int(blockC[0]),
		RfFrequency:     int(blockC[1]),
		RfBandWidth:     int(blockC[2]),
		RfSf:            int(blockC[3]),
		RfLeadCode:      int(blockC[4]),
		TxTimeout:       int(blockC[6]),
		RxTimeout:       int(blockC[7]),
		RssiOut:         int(blockC[9]),
	}, nil
}

func (e *ConcentratorRPCExecutor) writeParams(ctx context.Context, unitID uint8, entries []concParamEntry) (ConcentratorShadowParams, int, error) {
	targetUnitID := unitID
	for _, entry := range entries {
		if err := e.bus.WriteSingleRegister(ctx, unitID, entry.address, uint16(entry.value)); err != nil {
			return ConcentratorShadowParams{}, 0, err
		}
		if entry.key == "modbusId" {
			targetUnitID = uint8(entry.value)
		}
	}
	if err := e.bus.WriteSingleRegister(ctx, targetUnitID, concRegCommit, concCommitWriteParams); err != nil {
		return ConcentratorShadowParams{}, 0, err
	}
	if err := e.sleep(ctx, e.settle); err != nil {
		return ConcentratorShadowParams{}, 0, err
	}
	readback, err := e.readParams(ctx, targetUnitID)
	if err != nil {
		return ConcentratorShadowParams{}, 0, err
	}
	for _, entry := range entries {
		got := paramFieldValue(readback, entry.key)
		if got != entry.value {
			return ConcentratorShadowParams{}, 0, fmt.Errorf("readback mismatch on %s: got %d, want %d", entry.key, got, entry.value)
		}
	}
	return readback, len(entries), nil
}

func (e *ConcentratorRPCExecutor) readAllEIDs(ctx context.Context, unitID uint8) ([]ConcentratorShadowEIDSlot, error) {
	slots := make([]ConcentratorShadowEIDSlot, 0, concentratorEIDSlotCount)
	for offset := 0; offset < concentratorEIDSlotCount; offset += concentratorMaxEIDsPerFC16 {
		words, err := e.bus.ReadHoldingRegisters(ctx, unitID, concRegEIDBase+uint16(offset*2), uint16(concentratorMaxEIDsPerFC16*2))
		if err != nil {
			return nil, err
		}
		if len(words) < concentratorMaxEIDsPerFC16*2 {
			return nil, fmt.Errorf("incomplete EID block at offset %d", offset)
		}
		for i := range concentratorMaxEIDsPerFC16 {
			eid := (uint32(words[i*2]) << 16) | uint32(words[i*2+1])
			slots = append(slots, ConcentratorShadowEIDSlot{
				Slot: offset + i + 1,
				EID:  eid,
			})
		}
	}
	return slots, nil
}

func (e *ConcentratorRPCExecutor) writeEIDs(ctx context.Context, unitID uint8, desired []ConcentratorShadowEIDSlot) ([]ConcentratorShadowEIDSlot, int, int, error) {
	current, err := e.readAllEIDs(ctx, unitID)
	if err != nil {
		return nil, 0, 0, err
	}
	chunks := computeDirtyConcEIDChunks(current, desired, concentratorMaxEIDsPerFC16)
	if len(chunks) == 0 {
		return current, 0, 0, nil
	}
	updatedSlots := 0
	for _, ch := range chunks {
		if err := e.bus.WriteMultipleRegisters(ctx, unitID, ch.startAddress, ch.registers); err != nil {
			return nil, 0, 0, err
		}
		updatedSlots += ch.slotCount
	}
	if err := e.bus.WriteSingleRegister(ctx, unitID, concRegCommit, concCommitWriteEIDs); err != nil {
		return nil, 0, 0, err
	}
	if err := e.sleep(ctx, e.settle); err != nil {
		return nil, 0, 0, err
	}
	verified := slices.Clone(current)
	for _, ch := range chunks {
		words, err := e.bus.ReadHoldingRegisters(ctx, unitID, ch.startAddress, uint16(ch.slotCount*2))
		if err != nil {
			return nil, 0, 0, err
		}
		if len(words) < ch.slotCount*2 {
			return nil, 0, 0, fmt.Errorf("incomplete EID readback starting at slot %d", ch.startSlot)
		}
		for i := range ch.slotCount {
			got := (uint32(words[i*2]) << 16) | uint32(words[i*2+1])
			if got != ch.eids[i] {
				return nil, 0, 0, fmt.Errorf("EID readback mismatch at slot %d: got %d, want %d", ch.startSlot+i, got, ch.eids[i])
			}
			idx := ch.startSlot - 1 + i
			verified[idx] = ConcentratorShadowEIDSlot{Slot: idx + 1, EID: got}
		}
	}
	return verified, updatedSlots, len(chunks), nil
}

func (e *ConcentratorRPCExecutor) readStatus(ctx context.Context, unitID uint8) (ConcentratorShadowParams, []ConcentratorChannelReading, error) {
	params, err := e.readParams(ctx, unitID)
	if err != nil {
		return ConcentratorShadowParams{}, nil, err
	}
	eids, err := e.readAllEIDs(ctx, unitID)
	if err != nil {
		return ConcentratorShadowParams{}, nil, err
	}

	sensorTypes := make([]uint16, 0, concentratorEIDSlotCount)
	for offset := 0; offset < concentratorEIDSlotCount; offset += 40 {
		block, err := e.bus.ReadInputRegisters(ctx, unitID, concRegSensorTypeBase+uint16(offset), 40)
		if err != nil {
			return ConcentratorShadowParams{}, nil, err
		}
		if len(block) < 40 {
			return ConcentratorShadowParams{}, nil, fmt.Errorf("incomplete sensorType block at offset %d", offset)
		}
		sensorTypes = append(sensorTypes, block[:40]...)
	}

	var activeChannels []int
	spanSet := map[int]struct{}{}
	for i := range concentratorEIDSlotCount {
		if eids[i].EID > 0 || sensorTypes[i] != 0 {
			ch := i + 1
			activeChannels = append(activeChannels, ch)
			spanSet[i/40] = struct{}{}
		}
	}

	spans := make([]int, 0, len(spanSet))
	for s := range spanSet {
		spans = append(spans, s)
	}
	sort.Ints(spans)

	voltMap := make(map[int]int, len(spans)*40)
	tempMap := make(map[int]float64, len(spans)*40)
	rssiMap := make(map[int]int, len(spans)*40)
	for _, span := range spans {
		start := uint16(span * 40)
		volts, err := e.bus.ReadInputRegisters(ctx, unitID, concRegVoltageBase+start, 40)
		if err != nil {
			return ConcentratorShadowParams{}, nil, err
		}
		temps, err := e.bus.ReadInputRegisters(ctx, unitID, concRegTempBase+start, 40)
		if err != nil {
			return ConcentratorShadowParams{}, nil, err
		}
		rssis, err := e.bus.ReadInputRegisters(ctx, unitID, concRegRSSIBase+start, 40)
		if err != nil {
			return ConcentratorShadowParams{}, nil, err
		}
		for i := range 40 {
			ch := int(start) + i + 1
			voltMap[ch] = int(volts[i])
			tempMap[ch] = float64(int16(temps[i])) / 10.0
			rssiMap[ch] = int(int16(rssis[i]))
		}
	}

	channels := make([]ConcentratorChannelReading, 0, len(activeChannels))
	for _, ch := range activeChannels {
		sType := int(sensorTypes[ch-1])
		online := sType != 0
		row := ConcentratorChannelReading{
			Channel:     ch,
			EID:         eids[ch-1].EID,
			Online:      online,
			SensorType:  sType,
			SensorModel: concSensorModel(sType),
		}
		if online {
			if tVal, ok := tempMap[ch]; ok {
				row.TemperatureC = &tVal
			}
			if vVal, ok := voltMap[ch]; ok {
				row.VoltageMV = &vVal
			}
			if rVal, ok := rssiMap[ch]; ok {
				row.RSSIDBm = &rVal
			}
		}
		channels = append(channels, row)
	}
	return params, channels, nil
}

func concSensorModel(sensorType int) string {
	switch sensorType {
	case 0:
		return "离线"
	case 0x35:
		return "PT100/无线测温"
	case 0x45:
		return "温湿度传感器"
	case 0x55:
		return "无线温度变送器"
	default:
		return fmt.Sprintf("0x%X", sensorType)
	}
}

type concParamEntry struct {
	key     string
	address uint16
	value   int
}

func (e *ConcentratorRPCExecutor) isProtectedUID(unitID uint8) bool {
	if e == nil || len(e.protectedUIDs) == 0 {
		return false
	}
	_, protected := e.protectedUIDs[unitID]
	return protected
}

func (e *ConcentratorRPCExecutor) validateParamsPatch(patch map[string]int) ([]concParamEntry, string, string) {
	var protected map[uint8]struct{}
	if e != nil {
		protected = e.protectedUIDs
	}
	return validateConcentratorParamsPatch(patch, protected)
}

func validateConcentratorParamsPatch(patch map[string]int, protectedUIDs map[uint8]struct{}) ([]concParamEntry, string, string) {
	if len(patch) == 0 {
		return nil, "CONCENTRATOR_PARAMS_INVALID", "at least one parameter must be provided"
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "modbusId" {
			return false
		}
		if keys[j] == "modbusId" {
			return true
		}
		return keys[i] < keys[j]
	})
	entries := make([]concParamEntry, 0, len(keys))
	for _, k := range keys {
		spec, ok := concParamSpecMap[k]
		if !ok {
			return nil, "CONCENTRATOR_PARAMS_INVALID", fmt.Sprintf("unknown concentrator parameter %s", k)
		}
		v := patch[k]
		if v < spec.min || v > spec.max {
			return nil, "CONCENTRATOR_PARAMS_INVALID", fmt.Sprintf("%s=%d out of range [%d,%d]", k, v, spec.min, spec.max)
		}
		if k == "modbusId" {
			if _, protected := protectedUIDs[uint8(v)]; protected {
				return nil, "CONCENTRATOR_UID_PROTECTED", fmt.Sprintf("modbusId %d is protected on RS485 bus", v)
			}
		}
		entries = append(entries, concParamEntry{key: k, address: spec.address, value: v})
	}
	return entries, "", ""
}

func paramFieldValue(p ConcentratorShadowParams, key string) int {
	switch key {
	case "modbusId":
		return p.ModbusID
	case "baudRateCode":
		return p.BaudRateCode
	case "offLineTime":
		return p.OffLineTime
	case "dataUplinkCycle":
		return p.DataUplinkCycle
	case "localNet":
		return p.LocalNet
	case "timeMark":
		return p.TimeMark
	case "rfTxPower":
		return p.RfTxPower
	case "rfFrequency":
		return p.RfFrequency
	case "rfBandWidth":
		return p.RfBandWidth
	case "rfSf":
		return p.RfSf
	case "rfLeadCode":
		return p.RfLeadCode
	case "txTimeout":
		return p.TxTimeout
	case "rxTimeout":
		return p.RxTimeout
	case "rssiOut":
		return p.RssiOut
	default:
		return -1
	}
}

func validateConcentratorEIDSlots(slots []ConcentratorShadowEIDSlot) ([]ConcentratorShadowEIDSlot, string, string) {
	if len(slots) != concentratorEIDSlotCount {
		return nil, "CONCENTRATOR_EIDS_INVALID", fmt.Sprintf("expected %d EID slots", concentratorEIDSlotCount)
	}
	ordered := make([]ConcentratorShadowEIDSlot, concentratorEIDSlotCount)
	seenSlot := make([]bool, concentratorEIDSlotCount)
	seenEID := map[uint32]int{}
	for _, s := range slots {
		if s.Slot < 1 || s.Slot > concentratorEIDSlotCount || seenSlot[s.Slot-1] {
			return nil, "CONCENTRATOR_EIDS_INVALID", fmt.Sprintf("invalid EID slot entry at slot %d", s.Slot)
		}
		seenSlot[s.Slot-1] = true
		if s.EID > 0 {
			if prev, dup := seenEID[s.EID]; dup {
				return nil, "CONCENTRATOR_EID_DUPLICATE", fmt.Sprintf("duplicate EID %d in slots %d and %d", s.EID, prev, s.Slot)
			}
			seenEID[s.EID] = s.Slot
		}
		ordered[s.Slot-1] = ConcentratorShadowEIDSlot{Slot: s.Slot, EID: s.EID}
	}
	return ordered, "", ""
}

func computeDirtyConcEIDChunks(current, desired []ConcentratorShadowEIDSlot, maxPerChunk int) []concDirtyEIDChunk {
	var chunks []concDirtyEIDChunk
	for idx := 0; idx < concentratorEIDSlotCount; {
		if current[idx].EID == desired[idx].EID {
			idx++
			continue
		}
		startIdx := idx
		var eids []uint32
		var regs []uint16
		for idx < concentratorEIDSlotCount && len(eids) < maxPerChunk && current[idx].EID != desired[idx].EID {
			eid := desired[idx].EID
			eids = append(eids, eid)
			regs = append(regs, uint16(eid>>16), uint16(eid&0xffff))
			idx++
		}
		chunks = append(chunks, concDirtyEIDChunk{
			startSlot:    startIdx + 1,
			slotCount:    len(eids),
			startAddress: concRegEIDBase + uint16(startIdx*2),
			registers:    regs,
			eids:         eids,
		})
	}
	return chunks
}
