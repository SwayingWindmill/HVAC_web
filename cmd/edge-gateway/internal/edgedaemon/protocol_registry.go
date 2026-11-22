package edgedaemon

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const DriverTypeBACnet = "BACNET"

// ErrBACnetBridgeNotRegistered indicates BACnet driver config was requested before a
// site BACnet protocol bridge plugin was registered in ProtocolBridgeRegistry.
var ErrBACnetBridgeNotRegistered = errors.New("bacnet protocol bridge is not registered")

// SouthboundDriverFactory creates a DeviceAdapter for an extension southbound protocol (e.g. BACnet).
type SouthboundDriverFactory func(input edgecontrol.ModbusDriverInput) (edgecontrol.DeviceAdapter, error)

// ProtocolBridgeRegistry manages shared protocol transports (notably serialized RS-485
// Modbus RTU buses such as /dev/ttyAS3) and extensible southbound protocol factories.
type ProtocolBridgeRegistry struct {
	mu          sync.Mutex
	serialBuses map[string]SerialBusConfig
	rtuBridges  map[string]*edgecontrol.ModbusTCPBridge
	factories   map[string]SouthboundDriverFactory
}

// NewProtocolBridgeRegistry constructs a bridge registry pre-seeded with serial bus parameters.
func NewProtocolBridgeRegistry(serialBuses map[string]SerialBusConfig) *ProtocolBridgeRegistry {
	buses := make(map[string]SerialBusConfig, len(serialBuses))
	for endpoint, cfg := range serialBuses {
		buses[strings.TrimSpace(endpoint)] = cfg
	}
	return &ProtocolBridgeRegistry{
		serialBuses: buses,
		rtuBridges:  make(map[string]*edgecontrol.ModbusTCPBridge),
		factories:   make(map[string]SouthboundDriverFactory),
	}
}

// RegisterFactory registers an extension protocol factory (for instance BACNET) keyed by normalized driverType.
func (r *ProtocolBridgeRegistry) RegisterFactory(driverType string, factory SouthboundDriverFactory) error {
	if r == nil {
		return errors.New("protocol bridge registry is nil")
	}
	key := strings.ToUpper(strings.TrimSpace(driverType))
	if key == "" || factory == nil {
		return errors.New("driverType and factory are required")
	}
	r.mu.Lock()
	r.factories[key] = factory
	r.mu.Unlock()
	return nil
}

// BuildAdapter validates input and constructs an edgecontrol.DeviceAdapter backed by shared transports.
func (r *ProtocolBridgeRegistry) BuildAdapter(input edgecontrol.ModbusDriverInput) (edgecontrol.DeviceAdapter, error) {
	if r == nil {
		return nil, errors.New("protocol bridge registry is nil")
	}
	driverType := strings.ToUpper(strings.TrimSpace(input.DriverType))

	r.mu.Lock()
	customFactory := r.factories[driverType]
	r.mu.Unlock()
	if customFactory != nil {
		return customFactory(input)
	}
	if driverType == DriverTypeBACnet {
		return nil, fmt.Errorf("%w: device %q cannot attach without BACnet bridge factory", ErrBACnetBridgeNotRegistered, input.DeviceKey)
	}

	profile, err := edgecontrol.ValidateModbusDriverInput(input, edgecontrol.ModbusDriverValidationOptions{
		RequireDeviceKey:       true,
		RequireGatewayBindings: true,
	})
	if err != nil {
		return nil, err
	}

	switch profile.DriverType {
	case edgecontrol.ModbusDriverRTU:
		transport, err := r.sharedRTUBridge(profile)
		if err != nil {
			return nil, err
		}
		return edgecontrol.NewDeclarativeModbusAdapter(profile, transport)
	case edgecontrol.ModbusDriverTCP:
		return edgecontrol.NewDeclarativeModbusAdapter(profile, nil)
	default:
		return nil, fmt.Errorf("unsupported southbound driverType %q", profile.DriverType)
	}
}

func (r *ProtocolBridgeRegistry) sharedRTUBridge(profile edgecontrol.DeclarativeModbusProfile) (*edgecontrol.ModbusTCPBridge, error) {
	endpoint := strings.TrimSpace(profile.Endpoint)
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.rtuBridges[endpoint]; ok {
		return existing, nil
	}
	bus := r.serialBuses[endpoint]
	timeout := profile.Timeout
	if timeout <= 0 && profile.TimeoutMS > 0 {
		timeout = time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	bridge, err := edgecontrol.NewModbusRTUBridge(edgecontrol.ModbusRTUBridgeConfig{
		Endpoint: endpoint,
		Timeout:  timeout,
		Retries:  profile.Retries,
		Speed:    firstNonZeroUint(bus.Speed, defaultRTUBaudRate),
		DataBits: firstNonZeroUint(bus.DataBits, defaultRTUDataBits),
		Parity:   bus.Parity,
		StopBits: firstNonZeroUint(bus.StopBits, defaultRTUStopBits),
	})
	if err != nil {
		return nil, err
	}
	r.rtuBridges[endpoint] = bridge
	return bridge, nil
}

// SharedRTUBridgeCount returns how many unique serial RTU transport bridges are currently open.
func (r *ProtocolBridgeRegistry) SharedRTUBridgeCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rtuBridges)
}

// Close closes all shared protocol transports owned by the registry.
func (r *ProtocolBridgeRegistry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	bridges := make([]*edgecontrol.ModbusTCPBridge, 0, len(r.rtuBridges))
	for k, b := range r.rtuBridges {
		delete(r.rtuBridges, k)
		bridges = append(bridges, b)
	}
	r.mu.Unlock()

	var errs []error
	for _, b := range bridges {
		if err := b.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func firstNonZeroUint(v, fallback uint) uint {
	if v != 0 {
		return v
	}
	return fallback
}
