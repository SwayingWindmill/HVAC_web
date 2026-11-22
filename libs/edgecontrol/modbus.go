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
	ModbusFunctionReadCoils              ModbusFunctionCode = 1
	ModbusFunctionReadDiscreteInputs     ModbusFunctionCode = 2
	ModbusFunctionReadHoldingRegisters   ModbusFunctionCode = 3
	ModbusFunctionReadInputRegisters     ModbusFunctionCode = 4
	ModbusFunctionWriteSingleCoil        ModbusFunctionCode = 5
	ModbusFunctionWriteSingleRegister    ModbusFunctionCode = 6
	ModbusFunctionWriteMultipleCoils     ModbusFunctionCode = 15
	ModbusFunctionWriteMultipleRegisters ModbusFunctionCode = 16

	defaultModbusRTUSpeed           uint          = 9600
	defaultModbusRTUDataBits        uint          = 8
	defaultModbusRTUStopBits        uint          = 1
	defaultModbusRTUTurnaroundDelay time.Duration = 45 * time.Millisecond
)

type ModbusTCPBridgeConfig struct {
	Endpoint string
	Timeout  time.Duration
	Retries  int
}

type ModbusRTUBridgeConfig struct {
	Endpoint               string
	Timeout                time.Duration
	Retries                int
	Speed                  uint          // defaults to 9600 when 0
	DataBits               uint          // defaults to 8 when 0 (7 or 8)
	Parity                 string        // "", "N", "E", "O" (defaults to "N")
	StopBits               uint          // defaults to 1 when 0 (1 or 2)
	TurnaroundDelay        time.Duration // defaults to 45ms when 0 unless DisableTurnaroundDelay is set
	DisableTurnaroundDelay bool
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

type busWaiter struct {
	ch chan struct{}
}

type priorityBusLock struct {
	mu            sync.Mutex
	held          bool
	highWaiters   []*busWaiter
	normalWaiters []*busWaiter
}

func (l *priorityBusLock) lock(ctx context.Context, highPriority bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	if !l.held {
		l.held = true
		l.mu.Unlock()
		return nil
	}
	w := &busWaiter{ch: make(chan struct{}, 1)}
	if highPriority {
		l.highWaiters = append(l.highWaiters, w)
	} else {
		l.normalWaiters = append(l.normalWaiters, w)
	}
	l.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		l.mu.Lock()
		var removed bool
		if highPriority {
			l.highWaiters, removed = removeBusWaiter(l.highWaiters, w)
		} else {
			l.normalWaiters, removed = removeBusWaiter(l.normalWaiters, w)
		}
		if removed {
			l.mu.Unlock()
			return ctx.Err()
		}
		l.unlockLocked()
		l.mu.Unlock()
		return ctx.Err()
	}
}

func (l *priorityBusLock) unlock() {
	l.mu.Lock()
	l.unlockLocked()
	l.mu.Unlock()
}

func (l *priorityBusLock) unlockLocked() {
	if len(l.highWaiters) > 0 {
		next := l.highWaiters[0]
		l.highWaiters[0] = nil
		l.highWaiters = l.highWaiters[1:]
		next.ch <- struct{}{}
		return
	}
	if len(l.normalWaiters) > 0 {
		next := l.normalWaiters[0]
		l.normalWaiters[0] = nil
		l.normalWaiters = l.normalWaiters[1:]
		next.ch <- struct{}{}
		return
	}
	l.held = false
}

func removeBusWaiter(waiters []*busWaiter, target *busWaiter) ([]*busWaiter, bool) {
	for i, w := range waiters {
		if w == target {
			copy(waiters[i:], waiters[i+1:])
			waiters[len(waiters)-1] = nil
			return waiters[:len(waiters)-1], true
		}
	}
	return waiters, false
}

// ModbusTCPBridge owns one Modbus/TCP or Modbus/RTU master connection and serializes
// raw register/coil transactions across DeviceAdapters with priority arbitration so
// control writes preempt queued telemetry reads on shared RS-485 serial buses.
type ModbusTCPBridge struct {
	bus             priorityBusLock
	scheme          string
	label           string
	endpoint        string
	timeout         time.Duration
	retries         int
	rtuSpeed        uint
	rtuDataBits     uint
	rtuParity       uint
	rtuStopBits     uint
	turnaroundDelay time.Duration
	lastTxEnd       time.Time
	nowFn           func() time.Time
	waitFn          func(context.Context, time.Duration) error
	client          *modbus.ModbusClient
	open            bool
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
	if config.TurnaroundDelay < 0 {
		return nil, fmt.Errorf("modbus RTU turnaroundDelay %s cannot be negative", config.TurnaroundDelay)
	}
	turnaroundDelay := config.TurnaroundDelay
	if turnaroundDelay == 0 && !config.DisableTurnaroundDelay {
		turnaroundDelay = defaultModbusRTUTurnaroundDelay
	}
	return &ModbusTCPBridge{
		scheme:          "rtu://",
		label:           "modbus RTU",
		endpoint:        endpoint,
		timeout:         config.Timeout,
		retries:         config.Retries,
		rtuSpeed:        speed,
		rtuDataBits:     dataBits,
		rtuParity:       parity,
		rtuStopBits:     stopBits,
		turnaroundDelay: turnaroundDelay,
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
	_ = bridge.bus.lock(context.Background(), true)
	defer bridge.bus.unlock()
	return bridge.closeLocked()
}

func (bridge *ModbusTCPBridge) Read(ctx context.Context, task ModbusReadTask) ([]uint16, error) {
	if err := validateModbusReadTask(task); err != nil {
		return nil, err
	}
	if err := bridge.bus.lock(ctx, false); err != nil {
		return nil, fmt.Errorf("%s read wait endpoint=%s task=%s: %w", bridge.bridgeLabel(), bridge.endpoint, task.Name, err)
	}
	defer bridge.bus.unlock()

	var registers []uint16
	err := bridge.runLocked(ctx, task.UnitID, func(client *modbus.ModbusClient) error {
		switch task.FunctionCode {
		case ModbusFunctionReadCoils:
			bits, err := client.ReadCoils(task.Address, task.Quantity)
			if err != nil {
				return err
			}
			registers = boolsToUint16(bits)
			return nil
		case ModbusFunctionReadDiscreteInputs:
			bits, err := client.ReadDiscreteInputs(task.Address, task.Quantity)
			if err != nil {
				return err
			}
			registers = boolsToUint16(bits)
			return nil
		case ModbusFunctionReadInputRegisters:
			regs, err := client.ReadRegisters(task.Address, task.Quantity, modbus.INPUT_REGISTER)
			registers = regs
			return err
		default:
			regs, err := client.ReadRegisters(task.Address, task.Quantity, modbus.HOLDING_REGISTER)
			registers = regs
			return err
		}
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
	if err := bridge.bus.lock(ctx, true); err != nil {
		return fmt.Errorf("%s write wait endpoint=%s task=%s: %w", bridge.bridgeLabel(), bridge.endpoint, task.Name, err)
	}
	defer bridge.bus.unlock()

	operation := func(client *modbus.ModbusClient) error {
		switch task.FunctionCode {
		case ModbusFunctionWriteSingleCoil:
			return client.WriteCoil(task.Address, task.Values[0] != 0)
		case ModbusFunctionWriteMultipleCoils:
			return client.WriteCoils(task.Address, uint16ToBools(task.Values))
		case ModbusFunctionWriteSingleRegister:
			return client.WriteRegister(task.Address, task.Values[0])
		default:
			return client.WriteRegisters(task.Address, task.Values)
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

func (bridge *ModbusTCPBridge) clockNow() time.Time {
	if bridge.nowFn != nil {
		return bridge.nowFn()
	}
	return time.Now()
}

func (bridge *ModbusTCPBridge) waitDelay(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if bridge.waitFn != nil {
		return bridge.waitFn(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (bridge *ModbusTCPBridge) runLocked(ctx context.Context, unitID uint8, operation func(*modbus.ModbusClient) error) error {
	var lastErr error
	for attempt := 0; attempt <= bridge.retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bridge.turnaroundDelay > 0 && !bridge.lastTxEnd.IsZero() {
			if remaining := bridge.turnaroundDelay - bridge.clockNow().Sub(bridge.lastTxEnd); remaining > 0 {
				if err := bridge.waitDelay(ctx, remaining); err != nil {
					return err
				}
			}
		}
		if err := bridge.openLocked(); err != nil {
			lastErr = err
			continue
		}
		if err := bridge.client.SetUnitId(unitID); err != nil {
			lastErr = err
			_ = bridge.closeLocked()
			bridge.lastTxEnd = bridge.clockNow()
			continue
		}
		err := operation(bridge.client)
		bridge.lastTxEnd = bridge.clockNow()
		if err != nil {
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
	case ModbusFunctionReadCoils, ModbusFunctionReadDiscreteInputs, ModbusFunctionReadHoldingRegisters, ModbusFunctionReadInputRegisters:
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
	case ModbusFunctionWriteSingleCoil:
		if len(task.Values) != 1 {
			return fmt.Errorf("modbus write task %s function code 5 requires exactly one coil value", task.Name)
		}
	case ModbusFunctionWriteSingleRegister:
		if len(task.Values) != 1 {
			return fmt.Errorf("modbus write task %s function code 6 requires exactly one register value", task.Name)
		}
	case ModbusFunctionWriteMultipleCoils, ModbusFunctionWriteMultipleRegisters:
	default:
		return fmt.Errorf("modbus write task %s uses unsupported function code %d", task.Name, task.FunctionCode)
	}
	return nil
}

func boolsToUint16(bits []bool) []uint16 {
	out := make([]uint16, len(bits))
	for i, b := range bits {
		if b {
			out[i] = 1
		}
	}
	return out
}

func uint16ToBools(values []uint16) []bool {
	out := make([]bool, len(values))
	for i, v := range values {
		out[i] = v != 0
	}
	return out
}
