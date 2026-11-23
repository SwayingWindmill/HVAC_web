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
	mu               sync.Mutex
	serialBuses      map[string]SerialBusConfig
	rtuBridges       map[string]*edgecontrol.ModbusTCPBridge
	bacnetBridge     *edgecontrol.BACnetIPBridge
	opcuaBridges     map[string]*edgecontrol.OPCUABridge
	factories        map[string]SouthboundDriverFactory
	credentialLookup func(credentialRef string) (string, error)
}

// NewProtocolBridgeRegistry constructs a bridge registry pre-seeded with serial bus parameters.
func NewProtocolBridgeRegistry(serialBuses map[string]SerialBusConfig) *ProtocolBridgeRegistry {
	buses := make(map[string]SerialBusConfig, len(serialBuses))
	for endpoint, cfg := range serialBuses {
		buses[strings.TrimSpace(endpoint)] = cfg
	}
	return &ProtocolBridgeRegistry{
		serialBuses:  buses,
		rtuBridges:   make(map[string]*edgecontrol.ModbusTCPBridge),
		opcuaBridges: make(map[string]*edgecontrol.OPCUABridge),
		factories:    make(map[string]SouthboundDriverFactory),
	}
}

// SetOPCUACredentialLookup configures secret lookup by credentialRef for USERNAME OPC-UA sessions.
func (r *ProtocolBridgeRegistry) SetOPCUACredentialLookup(fn func(credentialRef string) (string, error)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.credentialLookup = fn
	r.mu.Unlock()
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

	southbound, err := edgecontrol.ValidateSouthboundDriverInput(input, edgecontrol.ModbusDriverValidationOptions{
		RequireDeviceKey:       true,
		RequireGatewayBindings: true,
	})
	if err != nil {
		return nil, err
	}

	switch southbound.Kind {
	case edgecontrol.SouthboundKindModbus:
		profile := *southbound.Modbus
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
	case edgecontrol.SouthboundKindBACnetIP:
		bridge, err := r.sharedBACnetBridge(*southbound.BACnet)
		if err != nil {
			return nil, err
		}
		return edgecontrol.NewDeclarativeBACnetAdapter(*southbound.BACnet, bridge)
	case edgecontrol.SouthboundKindOPCUA:
		bridge, err := r.sharedOPCUABridge(*southbound.OPCUA)
		if err != nil {
			return nil, err
		}
		return edgecontrol.NewDeclarativeOPCUAAdapter(*southbound.OPCUA, bridge)
	default:
		return nil, fmt.Errorf("unsupported southbound driverType %q", input.DriverType)
	}
}

func (r *ProtocolBridgeRegistry) sharedBACnetBridge(profile edgecontrol.DeclarativeBACnetProfile) (*edgecontrol.BACnetIPBridge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bacnetBridge != nil {
		return r.bacnetBridge, nil
	}
	timeout := profile.Timeout
	if timeout <= 0 && profile.TimeoutMS > 0 {
		timeout = time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	bridge, err := edgecontrol.NewBACnetIPBridge(edgecontrol.BACnetIPBridgeConfig{
		Timeout: timeout,
		Retries: profile.Retries,
	})
	if err != nil {
		return nil, err
	}
	r.bacnetBridge = bridge
	return bridge, nil
}

func (r *ProtocolBridgeRegistry) sharedOPCUABridge(profile edgecontrol.DeclarativeOPCUAProfile) (*edgecontrol.OPCUABridge, error) {
	key := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		strings.TrimSpace(profile.Endpoint),
		profile.SecurityPolicy,
		profile.SecurityMode,
		profile.AuthMode,
		profile.Username,
		profile.CredentialRef,
	)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.opcuaBridges[key]; ok {
		return existing, nil
	}
	timeout := profile.Timeout
	if timeout <= 0 && profile.TimeoutMS > 0 {
		timeout = time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	bridge, err := edgecontrol.NewOPCUABridge(edgecontrol.OPCUABridgeConfig{
		Endpoint:         profile.Endpoint,
		SecurityPolicy:   profile.SecurityPolicy,
		SecurityMode:     profile.SecurityMode,
		AuthMode:         profile.AuthMode,
		Username:         profile.Username,
		CredentialRef:    profile.CredentialRef,
		CredentialLookup: r.credentialLookup,
		Timeout:          timeout,
		Retries:          profile.Retries,
	})
	if err != nil {
		return nil, err
	}
	r.opcuaBridges[key] = bridge
	return bridge, nil
}

// SharedRTUBridge returns the pooled Modbus RTU transport bridge for the given serial endpoint.
func (r *ProtocolBridgeRegistry) SharedRTUBridge(endpoint string, timeout time.Duration, retries int) (*edgecontrol.ModbusTCPBridge, error) {
	if r == nil {
		return nil, errors.New("protocol bridge registry is nil")
	}
	if retries < 0 {
		retries = 0
	}
	return r.sharedRTUBridge(edgecontrol.DeclarativeModbusProfile{
		Endpoint: strings.TrimSpace(endpoint),
		Timeout:  timeout,
		Retries:  retries,
	})
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
		Endpoint:        endpoint,
		Timeout:         timeout,
		Retries:         profile.Retries,
		Speed:           firstNonZeroUint(bus.Speed, defaultRTUBaudRate),
		DataBits:        firstNonZeroUint(bus.DataBits, defaultRTUDataBits),
		Parity:          bus.Parity,
		StopBits:        firstNonZeroUint(bus.StopBits, defaultRTUStopBits),
		TurnaroundDelay: time.Duration(bus.TurnaroundDelayMS) * time.Millisecond,
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

// SharedBACnetBridgeCount returns 1 if the shared UDP BACnet/IP bridge is open, else 0.
func (r *ProtocolBridgeRegistry) SharedBACnetBridgeCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bacnetBridge != nil {
		return 1
	}
	return 0
}

// SharedOPCUABridgeCount returns the count of pooled OPC-UA TCP session bridges.
func (r *ProtocolBridgeRegistry) SharedOPCUABridgeCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.opcuaBridges)
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
	bacBridge := r.bacnetBridge
	r.bacnetBridge = nil
	uaBridges := make([]*edgecontrol.OPCUABridge, 0, len(r.opcuaBridges))
	for k, b := range r.opcuaBridges {
		delete(r.opcuaBridges, k)
		uaBridges = append(uaBridges, b)
	}
	r.mu.Unlock()

	var errs []error
	for _, b := range bridges {
		if err := b.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if bacBridge != nil {
		if err := bacBridge.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, b := range uaBridges {
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
