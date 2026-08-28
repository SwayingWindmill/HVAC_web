package edgecontrol

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	modbus "github.com/simonvetter/modbus"
)

const (
	testFrequencyReadRegister  uint16 = 100
	testFrequencyWriteRegister uint16 = 101
)

func TestModbusTCPBridgeRunsProductionAdapterThroughRealTCPCycle(t *testing.T) {
	endpoint, handler := startModbusTestServer(t)
	handler.set(testFrequencyReadRegister, 400)

	bridge, err := NewModbusTCPBridge(ModbusTCPBridgeConfig{
		Endpoint: endpoint,
		Timeout:  250 * time.Millisecond,
		Retries:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	driver := newModbusPumpDriver(bridge)
	if err := host.RegisterAdapter(driver); err != nil {
		t.Fatal(err)
	}
	intentController, err := NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]ControllerBinding{{Priority: 100, Controller: intentController}}); err != nil {
		t.Fatal(err)
	}

	at := time.Unix(6_000, 0).UTC()
	_, err = host.IntentStore().Put(ControlIntent{
		ID:        "set-frequency-45",
		Address:   "chwp01/FrequencySetpoint",
		Requested: DoubleValue(45),
		IssuedAt:  at,
		ExpiresAt: at.Add(time.Minute),
		Source:    "CLOUD_COMMAND",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := host.RunCycle(t.Context(), at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PollResults) != 1 || first.PollResults[0].Error != nil {
		t.Fatalf("Modbus production adapter poll failed: %#v", first.PollResults)
	}
	frequency, ok := first.Cycle.Image.Get("chwp01/Frequency")
	if !ok || !frequency.HasValue || frequency.Sample.Value.Double != 40 {
		t.Fatalf("real TCP read was not promoted before controller evaluation: %#v", frequency)
	}
	if len(first.WriteResults) != 1 || !first.WriteResults[0].Success {
		t.Fatalf("governed write did not cross the Modbus bridge: %#v", first.WriteResults)
	}
	if got := handler.get(testFrequencyWriteRegister); got != 450 {
		t.Fatalf("execute-write did not reach the TCP endpoint: got register=%d want=450", got)
	}

	handler.set(testFrequencyReadRegister, handler.get(testFrequencyWriteRegister))
	second, err := host.RunCycle(t.Context(), at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	frequency, _ = second.Cycle.Image.Get("chwp01/Frequency")
	if !frequency.HasValue || frequency.Sample.Value.Double != 45 {
		t.Fatalf("later independent Modbus readback did not observe the governed write: %#v", frequency)
	}
}

func TestModbusTCPBridgeDoesNotFabricateDeviceValuesOnReadFailure(t *testing.T) {
	endpoint, handler := startModbusTestServer(t)
	handler.setReadFailureCount(1)

	bridge, err := NewModbusTCPBridge(ModbusTCPBridgeConfig{
		Endpoint: endpoint,
		Timeout:  250 * time.Millisecond,
		Retries:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(newModbusPumpDriver(bridge)); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatal(err)
	}

	result, err := host.RunCycle(t.Context(), time.Unix(6_100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PollResults) != 1 || result.PollResults[0].Error == nil {
		t.Fatalf("expected the raw Modbus read failure to surface: %#v", result.PollResults)
	}
	if !strings.Contains(result.PollResults[0].Error.Error(), endpoint) {
		t.Fatalf("poll error omitted endpoint context: %v", result.PollResults[0].Error)
	}
	frequency, ok := result.Cycle.Image.Get("chwp01/Frequency")
	if !ok || frequency.HasValue {
		t.Fatalf("failed Modbus read fabricated a process-image value: %#v", frequency)
	}
}

func TestModbusTCPBridgeWriteFailureHaltsGovernedCycle(t *testing.T) {
	endpoint, handler := startModbusTestServer(t)
	handler.set(testFrequencyReadRegister, 400)
	handler.setWriteFailureCount(1)

	bridge, err := NewModbusTCPBridge(ModbusTCPBridgeConfig{
		Endpoint: endpoint,
		Timeout:  250 * time.Millisecond,
		Retries:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(newModbusPumpDriver(bridge)); err != nil {
		t.Fatal(err)
	}
	intentController, err := NewIntentController("cloud-command", host.IntentStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start([]ControllerBinding{{Priority: 100, Controller: intentController}}); err != nil {
		t.Fatal(err)
	}

	at := time.Unix(6_150, 0).UTC()
	_, err = host.IntentStore().Put(ControlIntent{
		ID:        "set-frequency-45",
		Address:   "chwp01/FrequencySetpoint",
		Requested: DoubleValue(45),
		IssuedAt:  at,
		ExpiresAt: at.Add(time.Minute),
		Source:    "CLOUD_COMMAND",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := host.RunCycle(t.Context(), at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cycle.Halted || result.Cycle.OutputError == nil {
		t.Fatalf("failed Modbus write did not halt the governed cycle: %#v", result.Cycle)
	}
	message := result.Cycle.OutputError.Error()
	for _, want := range []string{endpoint, "frequency-write", "unit=1", "function=6", "address=101", "attempts=1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Modbus write error omitted operational context %q: %v", want, result.Cycle.OutputError)
		}
	}
	if got := handler.get(testFrequencyWriteRegister); got != 0 {
		t.Fatalf("failed governed write mutated the remote register: got=%d", got)
	}
}

func TestModbusTCPBridgeRetriesBoundedlyAndSurfacesTransactionContext(t *testing.T) {
	endpoint, handler := startModbusTestServer(t)
	handler.set(testFrequencyReadRegister, 400)
	handler.setReadFailureCount(1)

	bridge, err := NewModbusTCPBridge(ModbusTCPBridgeConfig{
		Endpoint: endpoint,
		Timeout:  250 * time.Millisecond,
		Retries:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })

	registers, err := bridge.Read(t.Context(), ModbusReadTask{
		Name:         "frequency-read",
		UnitID:       1,
		FunctionCode: ModbusFunctionReadHoldingRegisters,
		Address:      testFrequencyReadRegister,
		Quantity:     1,
	})
	if err != nil {
		t.Fatalf("transient protocol failure was not retried: %v", err)
	}
	attempts := handler.attemptCount()
	if len(registers) != 1 || registers[0] != 400 || attempts != 2 {
		t.Fatalf("unexpected retry result: registers=%v attempts=%d", registers, attempts)
	}

	handler.setReadFailureCount(2)
	_, err = bridge.Read(t.Context(), ModbusReadTask{
		Name:         "frequency-read",
		UnitID:       7,
		FunctionCode: ModbusFunctionReadHoldingRegisters,
		Address:      4321,
		Quantity:     1,
	})
	if err == nil {
		t.Fatal("expected bounded Modbus failure")
	}
	message := err.Error()
	for _, want := range []string{endpoint, "frequency-read", "unit=7", "function=3", "address=4321", "attempts=2"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Modbus error omitted operational context %q: %v", want, err)
		}
	}
}

type modbusPumpDriver struct {
	component ComponentDescriptor
	channels  []ChannelDescriptor
	bridge    *ModbusTCPBridge
	sequence  uint64
}

func newModbusPumpDriver(bridge *ModbusTCPBridge) *modbusPumpDriver {
	base := newFakePumpDriver(ComponentDeviceDriver)
	return &modbusPumpDriver{component: base.component, channels: base.channels, bridge: bridge}
}

func (driver *modbusPumpDriver) Component() ComponentDescriptor {
	return cloneComponent(driver.component)
}

func (driver *modbusPumpDriver) Channels() []ChannelDescriptor {
	return slices.Clone(driver.channels)
}

func (driver *modbusPumpDriver) Poll(ctx context.Context, at time.Time) ([]ChannelUpdate, error) {
	registers, err := driver.bridge.Read(ctx, ModbusReadTask{
		Name:         "frequency-read",
		UnitID:       1,
		FunctionCode: ModbusFunctionReadHoldingRegisters,
		Address:      testFrequencyReadRegister,
		Quantity:     1,
	})
	if err != nil {
		return nil, err
	}
	driver.sequence++
	return []ChannelUpdate{
		{Address: driver.channels[0].Address(), Sample: Sample{Value: StringValue("RUNNING"), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
		{Address: driver.channels[1].Address(), Sample: Sample{Value: StringValue(""), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
		{Address: driver.channels[5].Address(), Sample: Sample{Value: DoubleValue(float64(registers[0]) / 10), Quality: QualityGood, ObservedAt: at, Sequence: driver.sequence}},
	}, nil
}

func (driver *modbusPumpDriver) Apply(ctx context.Context, _ ProcessImage, decisions []Decision) ([]DeviceWriteResult, error) {
	results := make([]DeviceWriteResult, 0, len(decisions))
	for _, decision := range decisions {
		if decision.Address != driver.channels[6].Address() || decision.Effective == nil {
			results = append(results, DeviceWriteResult{Address: decision.Address, Success: false, Code: "UNSUPPORTED"})
			continue
		}
		if err := driver.bridge.Write(ctx, ModbusWriteTask{
			Name:         "frequency-write",
			UnitID:       1,
			FunctionCode: ModbusFunctionWriteSingleRegister,
			Address:      testFrequencyWriteRegister,
			Values:       []uint16{uint16(decision.Effective.Double * 10)},
		}); err != nil {
			return results, err
		}
		value := *decision.Effective
		results = append(results, DeviceWriteResult{Address: decision.Address, Success: true, Code: "APPLIED", AppliedValue: &value})
	}
	return results, nil
}

type modbusTestHandler struct {
	mu           sync.Mutex
	registers    map[uint16]uint16
	failReads    int
	failWrites   int
	readAttempts int
}

func startModbusTestServer(t *testing.T) (string, *modbusTestHandler) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	handler := &modbusTestHandler{registers: map[uint16]uint16{}}
	server, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        "tcp://" + address,
		Timeout:    time.Second,
		MaxClients: 1,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return address, handler
}

func (handler *modbusTestHandler) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (handler *modbusTestHandler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

func (handler *modbusTestHandler) HandleHoldingRegisters(request *modbus.HoldingRegistersRequest) ([]uint16, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if request.IsWrite {
		if handler.failWrites > 0 {
			handler.failWrites--
			return nil, modbus.ErrServerDeviceFailure
		}
		for index, value := range request.Args {
			handler.registers[request.Addr+uint16(index)] = value
		}
		return nil, nil
	}
	handler.readAttempts++
	if handler.failReads > 0 {
		handler.failReads--
		return nil, modbus.ErrServerDeviceFailure
	}
	values := make([]uint16, request.Quantity)
	for index := range request.Quantity {
		values[index] = handler.registers[request.Addr+uint16(index)]
	}
	return values, nil
}

func (handler *modbusTestHandler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func (handler *modbusTestHandler) set(address uint16, value uint16) {
	handler.mu.Lock()
	handler.registers[address] = value
	handler.mu.Unlock()
}

func (handler *modbusTestHandler) get(address uint16) uint16 {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.registers[address]
}

func (handler *modbusTestHandler) setReadFailureCount(count int) {
	handler.mu.Lock()
	handler.failReads = count
	handler.mu.Unlock()
}

func (handler *modbusTestHandler) setWriteFailureCount(count int) {
	handler.mu.Lock()
	handler.failWrites = count
	handler.mu.Unlock()
}

func (handler *modbusTestHandler) attemptCount() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.readAttempts
}
