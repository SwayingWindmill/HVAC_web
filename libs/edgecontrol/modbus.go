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
)

type ModbusTCPBridgeConfig struct {
	Endpoint string
	Timeout  time.Duration
	Retries  int
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

// ModbusTCPBridge owns one Modbus/TCP master connection and serializes raw
// register transactions across DeviceAdapters. DeviceAdapters retain register
// meaning, scaling and semantic Channel conversion; the Bridge owns transport,
// bounded request retries and reconnects after failed transactions.
type ModbusTCPBridge struct {
	mu       sync.Mutex
	endpoint string
	timeout  time.Duration
	retries  int
	client   *modbus.ModbusClient
	open     bool
}

func NewModbusTCPBridge(config ModbusTCPBridgeConfig) (*ModbusTCPBridge, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, errors.New("Modbus TCP endpoint is required")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return nil, fmt.Errorf("invalid Modbus TCP endpoint %q: %w", endpoint, err)
	}
	if config.Timeout <= 0 {
		return nil, errors.New("Modbus TCP request timeout must be positive")
	}
	if config.Retries < 0 {
		return nil, errors.New("Modbus TCP retries cannot be negative")
	}
	return &ModbusTCPBridge{endpoint: endpoint, timeout: config.Timeout, retries: config.Retries}, nil
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
			"Modbus TCP read endpoint=%s task=%s unit=%d function=%d address=%d quantity=%d attempts=%d: %w",
			bridge.endpoint, task.Name, task.UnitID, task.FunctionCode, task.Address, task.Quantity, bridge.retries+1, err,
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
			"Modbus TCP write endpoint=%s task=%s unit=%d function=%d address=%d quantity=%d attempts=%d: %w",
			bridge.endpoint, task.Name, task.UnitID, task.FunctionCode, task.Address, len(task.Values), bridge.retries+1, err,
		)
	}
	return nil
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
	client, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:     "tcp://" + bridge.endpoint,
		Timeout: bridge.timeout,
	})
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
		return errors.New("Modbus read task name is required")
	}
	if task.Quantity == 0 {
		return fmt.Errorf("Modbus read task %s quantity must be positive", task.Name)
	}
	switch task.FunctionCode {
	case ModbusFunctionReadHoldingRegisters, ModbusFunctionReadInputRegisters:
		return nil
	default:
		return fmt.Errorf("Modbus read task %s uses unsupported function code %d", task.Name, task.FunctionCode)
	}
}

func validateModbusWriteTask(task ModbusWriteTask) error {
	if strings.TrimSpace(task.Name) == "" {
		return errors.New("Modbus write task name is required")
	}
	if len(task.Values) == 0 {
		return fmt.Errorf("Modbus write task %s requires at least one register value", task.Name)
	}
	switch task.FunctionCode {
	case ModbusFunctionWriteSingleRegister:
		if len(task.Values) != 1 {
			return fmt.Errorf("Modbus write task %s function code 6 requires exactly one register value", task.Name)
		}
	case ModbusFunctionWriteMultipleRegisters:
	default:
		return fmt.Errorf("Modbus write task %s uses unsupported function code %d", task.Name, task.FunctionCode)
	}
	return nil
}
