package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ATV630ProtocolStatus string

type ATV630RawDataType string

type ATV630ByteOrder string

type ATV630WordOrder string

type ATV630ProtocolAccess string

const (
	ATV630ProtocolCandidate ATV630ProtocolStatus = "RELEASE_CANDIDATE"

	ATV630RawBitString16   ATV630RawDataType = "BIT_STRING_16"
	ATV630RawSigned16      ATV630RawDataType = "SIGNED_16"
	ATV630RawEnumeration16 ATV630RawDataType = "ENUMERATION_16"

	ATV630ByteOrderBigEndian     ATV630ByteOrder      = "BIG_ENDIAN"
	ATV630WordOrderNotApplicable ATV630WordOrder      = "NOT_APPLICABLE"
	ATV630ProtocolReadOnly       ATV630ProtocolAccess = "R"
	ATV630ProtocolReadWrite      ATV630ProtocolAccess = "R/W"

	atv630ETARunningBit = 2
	atv630ETAFaultBit   = 3

	atv630StateMaskSwitchOnDisabled uint16 = 0x004F
	atv630StateSwitchOnDisabled     uint16 = 0x0040
	atv630StateMaskDriveCom         uint16 = 0x006F
	atv630StateReadyToSwitchOn      uint16 = 0x0021
	atv630StateSwitchedOn           uint16 = 0x0023
	atv630StateOperationEnabled     uint16 = 0x0027

	atv630CMDShutdown        uint16 = 6
	atv630CMDSwitchOn        uint16 = 7
	atv630CMDEnableOperation uint16 = 15
	atv630CMDFaultReset      uint16 = 128
	atv630CMDFaultResetClear uint16 = 0
)

type ATV630ProtocolParameter struct {
	Code              string               `json:"code"`
	LogicalAddress    uint16               `json:"logicalAddress"`
	RegisterCount     uint16               `json:"registerCount"`
	RawDataType       ATV630RawDataType    `json:"rawDataType"`
	Scale             float64              `json:"scale"`
	Unit              string               `json:"unit,omitempty"`
	ByteOrder         ATV630ByteOrder      `json:"byteOrder"`
	WordOrder         ATV630WordOrder      `json:"wordOrder"`
	Access            ATV630ProtocolAccess `json:"access"`
	ReadFunctionCode  ModbusFunctionCode   `json:"readFunctionCode"`
	WriteFunctionCode ModbusFunctionCode   `json:"writeFunctionCode,omitzero"`
}

type ATV630ProtocolCandidateDescriptor struct {
	Status                  ATV630ProtocolStatus      `json:"status"`
	HardwareCertified       bool                      `json:"hardwareCertified"`
	Manufacturer            string                    `json:"manufacturer"`
	Model                   string                    `json:"model"`
	Transport               string                    `json:"transport"`
	ControlProfile          string                    `json:"controlProfile"`
	EmbeddedEthernetManual  string                    `json:"embeddedEthernetManual"`
	CommunicationParameters string                    `json:"communicationParameters"`
	Parameters              []ATV630ProtocolParameter `json:"parameters"`
}

var (
	atv630ETA = ATV630ProtocolParameter{
		Code: "ETA", LogicalAddress: 3201, RegisterCount: 1, RawDataType: ATV630RawBitString16, Scale: 1,
		ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable,
		Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters,
	}
	atv630RFR = ATV630ProtocolParameter{
		Code: "RFR", LogicalAddress: 3202, RegisterCount: 1, RawDataType: ATV630RawSigned16, Scale: 0.1, Unit: "Hz",
		ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable,
		Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters,
	}
	atv630LFT = ATV630ProtocolParameter{
		Code: "LFT", LogicalAddress: 7121, RegisterCount: 1, RawDataType: ATV630RawEnumeration16, Scale: 1,
		ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable,
		Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters,
	}
	atv630CMD = ATV630ProtocolParameter{
		Code: "CMD", LogicalAddress: 8501, RegisterCount: 1, RawDataType: ATV630RawBitString16, Scale: 1,
		ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable,
		Access: ATV630ProtocolReadWrite, ReadFunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister,
	}
	atv630LFR = ATV630ProtocolParameter{
		Code: "LFR", LogicalAddress: 8502, RegisterCount: 1, RawDataType: ATV630RawSigned16, Scale: 0.1, Unit: "Hz",
		ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable,
		Access: ATV630ProtocolReadWrite, ReadFunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister,
	}
)

// ATV630ProtocolReleaseCandidate returns the exact minimal protocol mapping that
// #339 may later promote after real-TCP conformance. It is not a Registry
// RELEASED revision and does not claim real-hardware certification.
func ATV630ProtocolReleaseCandidate() ATV630ProtocolCandidateDescriptor {
	return ATV630ProtocolCandidateDescriptor{
		Status:                  ATV630ProtocolCandidate,
		HardwareCertified:       false,
		Manufacturer:            "Schneider Electric",
		Model:                   "ATV630",
		Transport:               "EMBEDDED_MODBUS_TCP",
		ControlProfile:          "CIA402_DRIVECOM",
		EmbeddedEthernetManual:  "EAV64327 v03",
		CommunicationParameters: "EAV64332 v4.6 (2026-05-01)",
		Parameters:              []ATV630ProtocolParameter{atv630ETA, atv630RFR, atv630LFT, atv630CMD, atv630LFR},
	}
}

// ModbusRegisterTransport is the raw-register consumer boundary required by a
// production Modbus DeviceAdapter. ModbusTCPBridge satisfies this interface;
// vendor mapping and semantic conversion remain owned by the adapter.
type ModbusRegisterTransport interface {
	Read(context.Context, ModbusReadTask) ([]uint16, error)
	Write(context.Context, ModbusWriteTask) error
}

type ATV630PointIDs struct {
	RunState          string
	FaultCode         string
	StartCommand      string
	StopCommand       string
	ResetFaultCommand string
	Frequency         string
	FrequencySetpoint string
}

type ATV630DeviceAdapterConfig struct {
	ComponentID string
	Alias       string
	UnitID      uint8
	Transport   ModbusRegisterTransport
	PointIDs    ATV630PointIDs
}

type ATV630DeviceAdapter struct {
	mu        sync.Mutex
	component ComponentDescriptor
	channels  []ChannelDescriptor
	transport ModbusRegisterTransport
	unitID    uint8
	sequence  uint64
	lastETA   uint16
}

func NewATV630DeviceAdapter(config ATV630DeviceAdapterConfig) (*ATV630DeviceAdapter, error) {
	if strings.TrimSpace(config.ComponentID) == "" {
		return nil, errors.New("ATV630 component ID is required")
	}
	if config.Transport == nil {
		return nil, errors.New("ATV630 Modbus transport is required")
	}
	pointIDs := []string{
		config.PointIDs.RunState, config.PointIDs.FaultCode, config.PointIDs.StartCommand,
		config.PointIDs.StopCommand, config.PointIDs.ResetFaultCommand, config.PointIDs.Frequency,
		config.PointIDs.FrequencySetpoint,
	}
	for _, pointID := range pointIDs {
		if strings.TrimSpace(pointID) == "" {
			return nil, errors.New("ATV630 canonical Point IDs are required")
		}
	}

	channels := []ChannelDescriptor{
		atv630Channel(config.ComponentID, "RunState", config.PointIDs.RunState, DataTypeString, "", AccessReadOnly, "ATV630 CiA402 run state"),
		atv630Channel(config.ComponentID, "FaultCode", config.PointIDs.FaultCode, DataTypeString, "", AccessReadOnly, "ATV630 current fault code"),
		atv630Channel(config.ComponentID, "StartCommand", config.PointIDs.StartCommand, DataTypeBoolean, "", AccessWriteOnly, "ATV630 start command"),
		atv630Channel(config.ComponentID, "StopCommand", config.PointIDs.StopCommand, DataTypeBoolean, "", AccessWriteOnly, "ATV630 stop command"),
		atv630Channel(config.ComponentID, "ResetFaultCommand", config.PointIDs.ResetFaultCommand, DataTypeBoolean, "", AccessWriteOnly, "ATV630 fault reset command"),
		atv630Channel(config.ComponentID, "Frequency", config.PointIDs.Frequency, DataTypeDouble, "Hz", AccessReadOnly, "ATV630 motor frequency"),
		atv630Channel(config.ComponentID, "FrequencySetpoint", config.PointIDs.FrequencySetpoint, DataTypeDouble, "Hz", AccessWriteOnly, "ATV630 frequency reference"),
	}
	bindings := map[SemanticChannel]string{
		SemanticRunState: channels[0].Address(), SemanticFaultCode: channels[1].Address(),
		SemanticStartCommand: channels[2].Address(), SemanticStopCommand: channels[3].Address(),
		SemanticResetFaultCommand: channels[4].Address(), SemanticFrequency: channels[5].Address(),
		SemanticFrequencySetpoint: channels[6].Address(),
	}
	return &ATV630DeviceAdapter{
		component: ComponentDescriptor{
			ID: config.ComponentID, Alias: config.Alias, Enabled: true,
			Kind: ComponentDeviceDriver, Type: "SCHNEIDER_ATV630_CIA402_MODBUS_TCP",
			FactoryID: "SCHNEIDER_ATV630_CIA402_MODBUS_TCP", Version: "EAV64332-v4.6-rc1",
			Profiles: []CapabilityProfileID{ProfileVariableSpeedPump}, ChannelBindings: bindings,
		},
		channels: channels, transport: config.Transport, unitID: config.UnitID,
	}, nil
}

func (adapter *ATV630DeviceAdapter) Component() ComponentDescriptor {
	return cloneComponent(adapter.component)
}

func (adapter *ATV630DeviceAdapter) Channels() []ChannelDescriptor {
	return slices.Clone(adapter.channels)
}

func (adapter *ATV630DeviceAdapter) Poll(ctx context.Context, at time.Time) ([]ChannelUpdate, error) {
	status, err := adapter.transport.Read(ctx, ModbusReadTask{
		Name: "ATV630 ETA/RFR", UnitID: adapter.unitID, FunctionCode: atv630ETA.ReadFunctionCode,
		Address: atv630ETA.LogicalAddress, Quantity: 2,
	})
	if err != nil {
		return nil, fmt.Errorf("read ATV630 ETA/RFR: %w", err)
	}

	eta := status[0]
	faultActive := eta&(1<<atv630ETAFaultBit) != 0
	runState := "STOPPED"
	if faultActive {
		runState = "FAULT"
	} else if eta&(1<<atv630ETARunningBit) != 0 {
		runState = "RUNNING"
	}
	faultCode := ""
	if faultActive {
		fault, err := adapter.transport.Read(ctx, ModbusReadTask{
			Name: "ATV630 LFT", UnitID: adapter.unitID, FunctionCode: atv630LFT.ReadFunctionCode,
			Address: atv630LFT.LogicalAddress, Quantity: 1,
		})
		if err != nil {
			return nil, fmt.Errorf("read ATV630 LFT: %w", err)
		}
		faultCode = strconv.FormatUint(uint64(fault[0]), 10)
	}
	frequencyHz := float64(int16(status[1])) * atv630RFR.Scale

	adapter.mu.Lock()
	adapter.lastETA = eta
	adapter.sequence++
	sequence := adapter.sequence
	adapter.mu.Unlock()
	return []ChannelUpdate{
		{Address: adapter.channels[0].Address(), Sample: Sample{Value: StringValue(runState), Quality: QualityGood, ObservedAt: at, Sequence: sequence}},
		{Address: adapter.channels[1].Address(), Sample: Sample{Value: StringValue(faultCode), Quality: QualityGood, ObservedAt: at, Sequence: sequence}},
		{Address: adapter.channels[5].Address(), Sample: Sample{Value: DoubleValue(frequencyHz), Quality: QualityGood, ObservedAt: at, Sequence: sequence}},
	}, nil
}

func (adapter *ATV630DeviceAdapter) Apply(ctx context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		var err error
		resultCode := "APPLIED"
		switch decision.Address {
		case adapter.channels[2].Address():
			if !decision.Effective.Boolean {
				results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "ACTION_TRIGGER_REQUIRED"})
				continue
			}
			var command uint16
			var writeCommand, complete bool
			command, writeCommand, complete, err = adapter.nextStartCommand()
			if err == nil && writeCommand {
				err = adapter.writeCMDSequence(ctx, "START", command)
			}
			if err == nil && !complete {
				resultCode = "IN_PROGRESS"
			}
		case adapter.channels[3].Address():
			if !decision.Effective.Boolean {
				results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "ACTION_TRIGGER_REQUIRED"})
				continue
			}
			err = adapter.writeCMDSequence(ctx, "STOP", atv630CMDSwitchOn)
		case adapter.channels[4].Address():
			if !decision.Effective.Boolean {
				results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "ACTION_TRIGGER_REQUIRED"})
				continue
			}
			err = adapter.writeCMDSequence(ctx, "RESET_FAULT", atv630CMDFaultReset, atv630CMDFaultResetClear)
		case adapter.channels[6].Address():
			raw := uint16(int16(math.Round(decision.Effective.Double / atv630LFR.Scale)))
			err = adapter.transport.Write(ctx, ModbusWriteTask{
				Name: "ATV630 SET_FREQUENCY LFR", UnitID: adapter.unitID, FunctionCode: atv630LFR.WriteFunctionCode,
				Address: atv630LFR.LogicalAddress, Values: []uint16{raw},
			})
		default:
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "COMMAND_CHANNEL_INVALID"})
			continue
		}
		if err != nil {
			return results, err
		}
		value := *decision.Effective
		results = append(results, DeviceWriteResult{Address: decision.Address, Success: true, Code: resultCode, AppliedValue: &value})
	}
	return results, nil
}

func (adapter *ATV630DeviceAdapter) nextStartCommand() (command uint16, writeCommand, complete bool, err error) {
	adapter.mu.Lock()
	eta := adapter.lastETA
	adapter.mu.Unlock()

	switch {
	case eta&atv630StateMaskSwitchOnDisabled == atv630StateSwitchOnDisabled:
		return atv630CMDShutdown, true, false, nil
	case eta&atv630StateMaskDriveCom == atv630StateReadyToSwitchOn:
		return atv630CMDSwitchOn, true, false, nil
	case eta&atv630StateMaskDriveCom == atv630StateSwitchedOn:
		return atv630CMDEnableOperation, true, false, nil
	case eta&atv630StateMaskDriveCom == atv630StateOperationEnabled:
		return 0, false, true, nil
	default:
		return 0, false, false, fmt.Errorf("ATV630 START cannot advance from ETA=0x%04X", eta)
	}
}

func (adapter *ATV630DeviceAdapter) writeCMDSequence(ctx context.Context, command string, values ...uint16) error {
	for _, value := range values {
		if err := adapter.transport.Write(ctx, ModbusWriteTask{
			Name: "ATV630 " + command + " CMD", UnitID: adapter.unitID, FunctionCode: atv630CMD.WriteFunctionCode,
			Address: atv630CMD.LogicalAddress, Values: []uint16{value},
		}); err != nil {
			return fmt.Errorf("apply ATV630 %s: %w", command, err)
		}
	}
	return nil
}

func atv630Channel(componentID, channelID, pointID string, dataType DataType, unit string, access AccessMode, description string) ChannelDescriptor {
	return ChannelDescriptor{
		ComponentID: componentID, ChannelID: channelID, PointID: pointID,
		DataType: dataType, Access: access, Description: description, Unit: unit,
		Category: ChannelCategoryOpenemsType, PollPriority: PriorityVeryHigh,
		LocalPersistencePriority: PriorityVeryHigh, RemotePersistencePriority: PriorityVeryHigh,
		AggregationPriority: PriorityLow, ResendPriority: PriorityVeryHigh,
	}
}
