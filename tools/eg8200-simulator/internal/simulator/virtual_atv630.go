package simulator

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	virtualATV630UnitID uint8 = 1

	modbusFunctionReadHoldingRegisters byte = 3
	modbusFunctionWriteSingleRegister  byte = 6
	modbusExceptionIllegalFunction     byte = 1
	modbusExceptionIllegalDataAddress  byte = 2
	modbusExceptionIllegalDataValue    byte = 3
	modbusExceptionServerDeviceFailure byte = 4
	modbusTCPMaxLengthField                 = 254
)

type virtualATV630DriveState uint8

const (
	virtualATV630SwitchOnDisabled virtualATV630DriveState = iota
	virtualATV630ReadyToSwitchOn
	virtualATV630SwitchedOn
	virtualATV630OperationEnabled
)

const (
	virtualATV630ETASwitchOnDisabled uint16 = 0x0250
	virtualATV630ETAReadyToSwitchOn  uint16 = 0x0231
	virtualATV630ETASwitchedOn       uint16 = 0x0233
	virtualATV630ETAOperationEnabled uint16 = 0x0237
	virtualATV630ETAFault            uint16 = 0x0008
)

type virtualATV630Mapping struct {
	eta edgecontrol.ATV630ProtocolParameter
	rfr edgecontrol.ATV630ProtocolParameter
	lft edgecontrol.ATV630ProtocolParameter
	cmd edgecontrol.ATV630ProtocolParameter
	lfr edgecontrol.ATV630ProtocolParameter
}

type VirtualATV630Server struct {
	mu                    sync.Mutex
	plant                 *Plant
	mapping               virtualATV630Mapping
	endpoint              string
	driveState            virtualATV630DriveState
	commandWord           uint16
	frequencyReferenceRaw uint16
	lastFaultRaw          uint16

	lifecycleMu sync.Mutex
	listener    net.Listener
	connections map[net.Conn]struct{}
	workers     sync.WaitGroup
}

func NewVirtualATV630Server(endpoint string, plant *Plant) (*VirtualATV630Server, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("virtual ATV630 Modbus TCP endpoint is required")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return nil, fmt.Errorf("invalid virtual ATV630 Modbus TCP endpoint %q: %w", endpoint, err)
	}
	if plant == nil {
		return nil, errors.New("virtual ATV630 Plant is required")
	}

	mapping := virtualATV630ReleaseCandidateMapping()
	snapshot := plant.Snapshot()
	pump := snapshot.Devices[plant.config.ChilledWaterPump.ID]
	driveState := virtualATV630SwitchOnDisabled
	commandWord := uint16(0)
	if pump["runState"] == "RUNNING" {
		driveState = virtualATV630OperationEnabled
		commandWord = 15
	}
	frequencyReferenceRaw := uint16(int16(math.Round(plant.config.ChilledWaterPump.InitialFrequencyHz / mapping.lfr.Scale)))

	return &VirtualATV630Server{
		plant:                 plant,
		mapping:               mapping,
		endpoint:              endpoint,
		driveState:            driveState,
		commandWord:           commandWord,
		frequencyReferenceRaw: frequencyReferenceRaw,
		connections:           make(map[net.Conn]struct{}),
	}, nil
}

func (server *VirtualATV630Server) Start() error {
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	if server.listener != nil {
		return errors.New("virtual ATV630 Modbus TCP server is already started")
	}
	listener, err := net.Listen("tcp", server.endpoint)
	if err != nil {
		return err
	}
	server.listener = listener
	server.workers.Go(func() { server.accept(listener) })
	return nil
}

func (server *VirtualATV630Server) Stop() error {
	server.lifecycleMu.Lock()
	listener := server.listener
	if listener == nil {
		server.lifecycleMu.Unlock()
		return nil
	}
	server.listener = nil
	connections := make([]net.Conn, 0, len(server.connections))
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.lifecycleMu.Unlock()

	closeErr := listener.Close()
	for _, connection := range connections {
		_ = connection.Close()
	}
	server.workers.Wait()
	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return closeErr
	}
	return nil
}

func (server *VirtualATV630Server) accept(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}

		server.lifecycleMu.Lock()
		if server.listener != listener {
			server.lifecycleMu.Unlock()
			_ = connection.Close()
			return
		}
		server.connections[connection] = struct{}{}
		server.lifecycleMu.Unlock()

		server.workers.Go(func() {
			server.serveConnection(connection)
			server.lifecycleMu.Lock()
			delete(server.connections, connection)
			server.lifecycleMu.Unlock()
			_ = connection.Close()
		})
	}
}

func (server *VirtualATV630Server) serveConnection(connection net.Conn) {
	for {
		var header [7]byte
		if _, err := io.ReadFull(connection, header[:]); err != nil {
			return
		}
		if binary.BigEndian.Uint16(header[2:4]) != 0 {
			return
		}
		length := binary.BigEndian.Uint16(header[4:6])
		if length < 2 || length > modbusTCPMaxLengthField {
			return
		}

		pdu := make([]byte, int(length)-1)
		if _, err := io.ReadFull(connection, pdu); err != nil {
			return
		}
		responsePDU, ok := server.handlePDU(header[6], pdu)
		if !ok {
			return
		}

		response := make([]byte, 7+len(responsePDU))
		copy(response[0:2], header[0:2])
		binary.BigEndian.PutUint16(response[4:6], uint16(1+len(responsePDU)))
		response[6] = header[6]
		copy(response[7:], responsePDU)
		if err := writeAll(connection, response); err != nil {
			return
		}
	}
}

func (server *VirtualATV630Server) handlePDU(unitID byte, pdu []byte) ([]byte, bool) {
	if len(pdu) == 0 || unitID != virtualATV630UnitID {
		return nil, false
	}
	functionCode := pdu[0]
	switch functionCode {
	case modbusFunctionReadHoldingRegisters:
		if len(pdu) != 5 {
			return modbusException(functionCode, modbusExceptionIllegalDataValue), true
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		quantity := binary.BigEndian.Uint16(pdu[3:5])
		if quantity == 0 || quantity > 125 {
			return modbusException(functionCode, modbusExceptionIllegalDataValue), true
		}
		values, exceptionCode := server.readHoldingRegisters(address, quantity)
		if exceptionCode != 0 {
			return modbusException(functionCode, exceptionCode), true
		}
		response := make([]byte, 2+2*len(values))
		response[0] = functionCode
		response[1] = byte(2 * len(values))
		for index, value := range values {
			binary.BigEndian.PutUint16(response[2+2*index:4+2*index], value)
		}
		return response, true
	case modbusFunctionWriteSingleRegister:
		if len(pdu) != 5 {
			return modbusException(functionCode, modbusExceptionIllegalDataValue), true
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		value := binary.BigEndian.Uint16(pdu[3:5])
		if exceptionCode := server.writeHoldingRegister(address, value); exceptionCode != 0 {
			return modbusException(functionCode, exceptionCode), true
		}
		return append([]byte(nil), pdu...), true
	default:
		return modbusException(functionCode, modbusExceptionIllegalFunction), true
	}
}

func (server *VirtualATV630Server) readHoldingRegisters(address, quantity uint16) ([]uint16, byte) {
	server.mu.Lock()
	defer server.mu.Unlock()

	snapshot := server.plant.Snapshot()
	pump := snapshot.Devices[server.plant.config.ChilledWaterPump.ID]
	values := make([]uint16, quantity)
	for index := range quantity {
		value, exceptionCode := server.readRegisterLocked(address+uint16(index), pump)
		if exceptionCode != 0 {
			return nil, exceptionCode
		}
		values[index] = value
	}
	return values, 0
}

func (server *VirtualATV630Server) readRegisterLocked(address uint16, pump DeviceTelemetry) (uint16, byte) {
	switch address {
	case server.mapping.eta.LogicalAddress:
		if faultCode, _ := pump["faultCode"].(string); faultCode != "" {
			return virtualATV630ETAFault, 0
		}
		return server.etaLocked(), 0
	case server.mapping.rfr.LogicalAddress:
		frequencyHz, ok := pump["frequencyHz"].(float64)
		if !ok {
			return 0, modbusExceptionServerDeviceFailure
		}
		return uint16(int16(math.Round(frequencyHz / server.mapping.rfr.Scale))), 0
	case server.mapping.lft.LogicalAddress:
		if exceptionCode := server.rememberCurrentFaultLocked(pump); exceptionCode != 0 {
			return 0, exceptionCode
		}
		return server.lastFaultRaw, 0
	case server.mapping.cmd.LogicalAddress:
		return server.commandWord, 0
	case server.mapping.lfr.LogicalAddress:
		return server.frequencyReferenceRaw, 0
	default:
		return 0, modbusExceptionIllegalDataAddress
	}
}

func (server *VirtualATV630Server) rememberCurrentFaultLocked(pump DeviceTelemetry) byte {
	faultCode, _ := pump["faultCode"].(string)
	if faultCode == "" {
		return 0
	}
	value, err := strconv.ParseUint(faultCode, 10, 16)
	if err != nil {
		return modbusExceptionServerDeviceFailure
	}
	server.lastFaultRaw = uint16(value)
	return 0
}

func (server *VirtualATV630Server) writeHoldingRegister(address, value uint16) byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	switch address {
	case server.mapping.cmd.LogicalAddress:
		return server.applyCommandWordLocked(value)
	case server.mapping.lfr.LogicalAddress:
		return server.applyFrequencyReferenceLocked(value)
	default:
		return modbusExceptionIllegalDataAddress
	}
}

func (server *VirtualATV630Server) applyFrequencyReferenceLocked(raw uint16) byte {
	frequencyHz := float64(int16(raw)) * server.mapping.lfr.Scale
	result := server.plant.ApplyCommand(Command{
		DeviceID: server.plant.config.ChilledWaterPump.ID,
		Method:   "setFrequency",
		Params:   map[string]float64{"frequencyHz": frequencyHz},
	})
	if !result.Success {
		return modbusExceptionIllegalDataValue
	}
	server.frequencyReferenceRaw = raw
	return 0
}

func (server *VirtualATV630Server) applyCommandWordLocked(value uint16) byte {
	switch value {
	case 6:
		if server.driveState != virtualATV630SwitchOnDisabled {
			return modbusExceptionIllegalDataValue
		}
		server.driveState = virtualATV630ReadyToSwitchOn
	case 7:
		switch server.driveState {
		case virtualATV630ReadyToSwitchOn:
			server.driveState = virtualATV630SwitchedOn
		case virtualATV630SwitchedOn:
		case virtualATV630OperationEnabled:
			result := server.plant.ApplyCommand(Command{DeviceID: server.plant.config.ChilledWaterPump.ID, Method: "stop"})
			if !result.Success {
				return modbusExceptionServerDeviceFailure
			}
			server.driveState = virtualATV630SwitchedOn
		default:
			return modbusExceptionIllegalDataValue
		}
	case 15:
		if server.driveState != virtualATV630SwitchedOn && server.driveState != virtualATV630OperationEnabled {
			return modbusExceptionIllegalDataValue
		}
		result := server.plant.ApplyCommand(Command{DeviceID: server.plant.config.ChilledWaterPump.ID, Method: "start"})
		if !result.Success {
			return modbusExceptionServerDeviceFailure
		}
		server.driveState = virtualATV630OperationEnabled
	case 128:
		snapshot := server.plant.Snapshot()
		if exceptionCode := server.rememberCurrentFaultLocked(snapshot.Devices[server.plant.config.ChilledWaterPump.ID]); exceptionCode != 0 {
			return exceptionCode
		}
		result := server.plant.ApplyCommand(Command{DeviceID: server.plant.config.ChilledWaterPump.ID, Method: "resetFault"})
		if !result.Success {
			return modbusExceptionServerDeviceFailure
		}
		server.driveState = virtualATV630SwitchOnDisabled
	case 0:
		if server.driveState != virtualATV630SwitchOnDisabled {
			return modbusExceptionIllegalDataValue
		}
	default:
		return modbusExceptionIllegalDataValue
	}
	server.commandWord = value
	return 0
}

func (server *VirtualATV630Server) etaLocked() uint16 {
	switch server.driveState {
	case virtualATV630ReadyToSwitchOn:
		return virtualATV630ETAReadyToSwitchOn
	case virtualATV630SwitchedOn:
		return virtualATV630ETASwitchedOn
	case virtualATV630OperationEnabled:
		return virtualATV630ETAOperationEnabled
	default:
		return virtualATV630ETASwitchOnDisabled
	}
}

func virtualATV630ReleaseCandidateMapping() virtualATV630Mapping {
	var mapping virtualATV630Mapping
	for _, parameter := range edgecontrol.ATV630ProtocolReleaseCandidate().Parameters {
		switch parameter.Code {
		case "ETA":
			mapping.eta = parameter
		case "RFR":
			mapping.rfr = parameter
		case "LFT":
			mapping.lft = parameter
		case "CMD":
			mapping.cmd = parameter
		case "LFR":
			mapping.lfr = parameter
		}
	}
	return mapping
}

func modbusException(functionCode, exceptionCode byte) []byte {
	return []byte{functionCode | 0x80, exceptionCode}
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[written:]
	}
	return nil
}
