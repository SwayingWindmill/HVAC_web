package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	modbus "github.com/simonvetter/modbus"
)

type ModbusFunctionCode uint8

const (
	ModbusFunctionReadHoldingRegisters   ModbusFunctionCode = 3
	ModbusFunctionReadInputRegisters     ModbusFunctionCode = 4
	ModbusFunctionWriteSingleRegister    ModbusFunctionCode = 6
	ModbusFunctionWriteMultipleRegisters ModbusFunctionCode = 16

	defaultModbusRTUSpeed    uint = 9600
	defaultModbusRTUDataBits uint = 8
	defaultModbusRTUStopBits uint = 1
)

type ModbusTCPBridgeConfig struct {
	Endpoint string
	Timeout  time.Duration
	Retries  int
}

type ModbusRTUBridgeConfig struct {
	Endpoint string
	Timeout  time.Duration
	Retries  int
	Speed    uint   // defaults to 9600 when 0
	DataBits uint   // defaults to 8 when 0 (7 or 8)
	Parity   string // "", "N", "E", "O" (defaults to "N")
	StopBits uint   // defaults to 1 when 0 (1 or 2)
}

type ModbusReadTask struct {
	Name         string
	UnitID       uint8
	FunctionCode ModbusFunctionCode
	Address      uint16
	Quantity     uint16
}

type ModbusWriteTask struct {
	Name         string
	UnitID       uint8
	FunctionCode ModbusFunctionCode
	Address      uint16
	Values       []uint16
}

// ModbusTCPBridge owns one Modbus/TCP or Modbus/RTU master connection and serializes
// raw register transactions across DeviceAdapters. DeviceAdapters retain register
// meaning, scaling and semantic Channel conversion; the Bridge owns transport,
// bounded request retries and reconnects after failed transactions.
type ModbusTCPBridge struct {
	mu          sync.Mutex
	scheme      string
	label       string
	endpoint    string
	timeout     time.Duration
	retries     int
	rtuSpeed    uint
	rtuDataBits uint
	rtuParity   uint
	rtuStopBits uint
	client      *modbus.ModbusClient
	open        bool
}

func NewModbusTCPBridge(config ModbusTCPBridgeConfig) (*ModbusTCPBridge, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, errors.New("modbus TCP endpoint is required")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return nil, fmt.Errorf("invalid Modbus TCP endpoint %q: %w", endpoint, err)
	}
	if config.Timeout <= 0 {
		return nil, errors.New("modbus TCP request timeout must be positive")
	}
	if config.Retries < 0 {
		return nil, errors.New("modbus TCP retries cannot be negative")
	}
	return &ModbusTCPBridge{scheme: "tcp://", label: "modbus TCP", endpoint: endpoint, timeout: config.Timeout, retries: config.Retries}, nil
}

func NewModbusRTUBridge(config ModbusRTUBridgeConfig) (*ModbusTCPBridge, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, errors.New("modbus RTU endpoint is required")
	}
	if config.Timeout <= 0 {
		return nil, errors.New("modbus RTU request timeout must be positive")
	}
	if config.Retries < 0 {
		return nil, errors.New("modbus RTU retries cannot be negative")
	}
	speed := config.Speed
	if speed == 0 {
		speed = defaultModbusRTUSpeed
	}
	dataBits := config.DataBits
	if dataBits == 0 {
		dataBits = defaultModbusRTUDataBits
	}
	if dataBits != 7 && dataBits != 8 {
		return nil, fmt.Errorf("modbus RTU dataBits must be 7 or 8, got %d", dataBits)
	}
	stopBits := config.StopBits
	if stopBits == 0 {
		stopBits = defaultModbusRTUStopBits
	}
	if stopBits != 1 && stopBits != 2 {
		return nil, fmt.Errorf("modbus RTU stopBits must be 1 or 2, got %d", stopBits)
	}
	parity, err := parseModbusRTUParity(config.Parity)
	if err != nil {
		return nil, err
	}
	return &ModbusTCPBridge{
		scheme:      "rtu://",
		label:       "modbus RTU",
		endpoint:    endpoint,
		timeout:     config.Timeout,
		retries:     config.Retries,
		rtuSpeed:    speed,
		rtuDataBits: dataBits,
		rtuParity:   parity,
		rtuStopBits: stopBits,
	}, nil
}

func parseModbusRTUParity(raw string) (uint, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "N", "NONE":
		return modbus.PARITY_NONE, nil
	case "E", "EVEN":
		return modbus.PARITY_EVEN, nil
	case "O", "ODD":
		return modbus.PARITY_ODD, nil
	default:
		return 0, fmt.Errorf("unsupported modbus RTU parity %q", raw)
	}
}

func (bridge *ModbusTCPBridge) Close() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.closeLocked()
}

func (bridge *ModbusTCPBridge) Read(ctx context.Context, task ModbusReadTask) ([]uint16, error) {
	if err := validateModbusReadTask(task); err != nil {
		return nil, err
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	registerType := modbus.HOLDING_REGISTER
	if task.FunctionCode == ModbusFunctionReadInputRegisters {
		registerType = modbus.INPUT_REGISTER
	}
	var registers []uint16
	err := bridge.runLocked(ctx, task.UnitID, func(client *modbus.ModbusClient) error {
		var err error
		registers, err = client.ReadRegisters(task.Address, task.Quantity, registerType)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf(
			"%s read endpoint=%s task=%s unit=%d function=%d address=%d quantity=%d attempts=%d: %w",
			bridge.bridgeLabel(), bridge.endpoint, task.Name, task.UnitID, task.FunctionCode, task.Address, task.Quantity, bridge.retries+1, err,
		)
	}
	return registers, nil
}

func (bridge *ModbusTCPBridge) Write(ctx context.Context, task ModbusWriteTask) error {
	if err := validateModbusWriteTask(task); err != nil {
		return err
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	operation := func(client *modbus.ModbusClient) error {
		return client.WriteRegisters(task.Address, task.Values)
	}
	if task.FunctionCode == ModbusFunctionWriteSingleRegister {
		operation = func(client *modbus.ModbusClient) error {
			return client.WriteRegister(task.Address, task.Values[0])
		}
	}
	err := bridge.runLocked(ctx, task.UnitID, operation)
	if err != nil {
		return fmt.Errorf(
			"%s write endpoint=%s task=%s unit=%d function=%d address=%d quantity=%d attempts=%d: %w",
			bridge.bridgeLabel(), bridge.endpoint, task.Name, task.UnitID, task.FunctionCode, task.Address, len(task.Values), bridge.retries+1, err,
		)
	}
	return nil
}

func (bridge *ModbusTCPBridge) bridgeLabel() string {
	if bridge.label == "" {
		return "modbus TCP"
	}
	return bridge.label
}

func (bridge *ModbusTCPBridge) bridgeScheme() string {
	if bridge.scheme == "" {
		return "tcp://"
	}
	return bridge.scheme
}

func (bridge *ModbusTCPBridge) runLocked(ctx context.Context, unitID uint8, operation func(*modbus.ModbusClient) error) error {
	var lastErr error
	for attempt := 0; attempt <= bridge.retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := bridge.openLocked(); err != nil {
			lastErr = err
			continue
		}
		if err := bridge.client.SetUnitId(unitID); err != nil {
			lastErr = err
			_ = bridge.closeLocked()
			continue
		}
		if err := operation(bridge.client); err != nil {
			lastErr = err
			_ = bridge.closeLocked()
			continue
		}
		return nil
	}
	return lastErr
}

func (bridge *ModbusTCPBridge) openLocked() error {
	if bridge.open {
		return nil
	}
	cfg := &modbus.ClientConfiguration{
		URL:     bridge.bridgeScheme() + bridge.endpoint,
		Timeout: bridge.timeout,
	}
	if bridge.bridgeScheme() == "rtu://" {
		cfg.Speed = bridge.rtuSpeed
		cfg.DataBits = bridge.rtuDataBits
		cfg.Parity = bridge.rtuParity
		cfg.StopBits = bridge.rtuStopBits
	}
	client, err := modbus.NewClient(cfg)
	if err != nil {
		return err
	}
	if err := client.Open(); err != nil {
		return err
	}
	bridge.client = client
	bridge.open = true
	return nil
}

func (bridge *ModbusTCPBridge) closeLocked() error {
	if !bridge.open {
		return nil
	}
	err := bridge.client.Close()
	bridge.client = nil
	bridge.open = false
	return err
}

func validateModbusReadTask(task ModbusReadTask) error {
	if strings.TrimSpace(task.Name) == "" {
		return errors.New("modbus read task name is required")
	}
	if task.Quantity == 0 {
		return fmt.Errorf("modbus read task %s quantity must be positive", task.Name)
	}
	switch task.FunctionCode {
	case ModbusFunctionReadHoldingRegisters, ModbusFunctionReadInputRegisters:
		return nil
	default:
		return fmt.Errorf("modbus read task %s uses unsupported function code %d", task.Name, task.FunctionCode)
	}
}

func validateModbusWriteTask(task ModbusWriteTask) error {
	if strings.TrimSpace(task.Name) == "" {
		return errors.New("modbus write task name is required")
	}
	if len(task.Values) == 0 {
		return fmt.Errorf("modbus write task %s requires at least one register value", task.Name)
	}
	switch task.FunctionCode {
	case ModbusFunctionWriteSingleRegister:
		if len(task.Values) != 1 {
			return fmt.Errorf("modbus write task %s function code 6 requires exactly one register value", task.Name)
		}
	case ModbusFunctionWriteMultipleRegisters:
	default:
		return fmt.Errorf("modbus write task %s uses unsupported function code %d", task.Name, task.FunctionCode)
	}
	return nil
}
