package edgedaemon

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

// FieldPlantMirrorAdapter exposes read-only FieldPlantRunner telemetry for a single
// deviceKey onto edgecontrol.Host so Cycle consumers observe live plant readings.
type FieldPlantMirrorAdapter struct {
	mu             sync.Mutex
	component      edgecontrol.ComponentDescriptor
	channels       []edgecontrol.ChannelDescriptor
	channelByPoint map[string]edgecontrol.ChannelDescriptor
	samples        map[string]edgecontrol.Sample
	seq            uint64
}

func newFieldPlantMirrorAdapter(deviceID string, descriptors []edgecontrol.ChannelDescriptor) *FieldPlantMirrorAdapter {
	sorted := append([]edgecontrol.ChannelDescriptor(nil), descriptors...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ChannelID < sorted[j].ChannelID
	})
	byPoint := make(map[string]edgecontrol.ChannelDescriptor, len(sorted))
	for _, d := range sorted {
		byPoint[d.ChannelID] = d
	}
	return &FieldPlantMirrorAdapter{
		component: edgecontrol.ComponentDescriptor{
			ID:        deviceID,
			Enabled:   true,
			Kind:      edgecontrol.ComponentDeviceDriver,
			Type:      "FIELD_PLANT_MIRROR",
			FactoryID: edgecontrol.DeclarativeModbusFactoryID,
			Version:   "1.0.0",
		},
		channels:       sorted,
		channelByPoint: byPoint,
		samples:        make(map[string]edgecontrol.Sample, len(sorted)),
	}
}

func (a *FieldPlantMirrorAdapter) Component() edgecontrol.ComponentDescriptor {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.component
}

func (a *FieldPlantMirrorAdapter) Channels() []edgecontrol.ChannelDescriptor {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]edgecontrol.ChannelDescriptor, len(a.channels))
	copy(out, a.channels)
	return out
}

func (a *FieldPlantMirrorAdapter) Poll(_ context.Context, _ time.Time) ([]edgecontrol.ChannelUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.samples) == 0 {
		return nil, nil
	}
	updates := make([]edgecontrol.ChannelUpdate, 0, len(a.samples))
	for _, ch := range a.channels {
		addr := ch.Address()
		if sample, ok := a.samples[addr]; ok {
			updates = append(updates, edgecontrol.ChannelUpdate{
				Address: addr,
				Sample:  sample,
			})
		}
	}
	return updates, nil
}

func (a *FieldPlantMirrorAdapter) Apply(_ context.Context, _ edgecontrol.ProcessImage, decisions []edgecontrol.Decision) ([]edgecontrol.DeviceWriteResult, error) {
	results := make([]edgecontrol.DeviceWriteResult, 0, len(decisions))
	for _, d := range decisions {
		results = append(results, edgecontrol.DeviceWriteResult{
			Address: d.Address,
			Success: false,
			Code:    "CHANNEL_READ_ONLY",
		})
	}
	return results, nil
}

func (a *FieldPlantMirrorAdapter) UpdatePoints(ts time.Time, points []mqttUplinkPoint) {
	if a == nil || len(points) == 0 {
		return
	}
	observedAt := ts.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, pt := range points {
		desc, ok := a.channelByPoint[pt.Code]
		if !ok {
			continue
		}
		a.seq++
		sample, ok := toMirrorSample(desc, pt, observedAt, a.seq)
		if !ok {
			continue
		}
		a.samples[desc.Address()] = sample
	}
}

func (a *FieldPlantMirrorAdapter) missingDescriptors(deviceID string, points []mqttUplinkPoint) []edgecontrol.ChannelDescriptor {
	a.mu.Lock()
	defer a.mu.Unlock()
	var added []edgecontrol.ChannelDescriptor
	seen := make(map[string]struct{})
	for _, pt := range points {
		code := strings.TrimSpace(pt.Code)
		if code == "" || strings.Contains(code, "/") {
			continue
		}
		if _, exists := a.channelByPoint[code]; exists {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		added = append(added, inferMirrorChannelDescriptor(deviceID, pt))
	}
	return added
}

func (a *FieldPlantMirrorAdapter) cloneWithAddedChannels(extra []edgecontrol.ChannelDescriptor) *FieldPlantMirrorAdapter {
	a.mu.Lock()
	merged := make([]edgecontrol.ChannelDescriptor, 0, len(a.channels)+len(extra))
	merged = append(merged, a.channels...)
	merged = append(merged, extra...)
	next := newFieldPlantMirrorAdapter(a.component.ID, merged)
	next.seq = a.seq
	for addr, sample := range a.samples {
		next.samples[addr] = sample
	}
	a.mu.Unlock()
	return next
}

// FieldPlantMirrorRegistry owns read-only mirror DeviceAdapters registered onto Host
// and keeps them synchronized as FieldPlantRunner emits telemetry.
type FieldPlantMirrorRegistry struct {
	mu              sync.RWMutex
	host            *edgecontrol.Host
	adapters        map[string]*FieldPlantMirrorAdapter
	skippedExternal map[string]bool
}

// NewFieldPlantMirrorRegistry builds FieldPlantMirrorAdapter instances from cfg and registers
// them onto host, preserving any device IDs already owned by explicit driver adapters.
func NewFieldPlantMirrorRegistry(cfg FieldPlantConfig, routes []PointCopyRouteConfig, host *edgecontrol.Host) (*FieldPlantMirrorRegistry, error) {
	adapters := BuildFieldPlantMirrorAdapters(cfg, routes)
	reg := &FieldPlantMirrorRegistry{
		host:            host,
		adapters:        make(map[string]*FieldPlantMirrorAdapter, len(adapters)),
		skippedExternal: make(map[string]bool),
	}
	for _, ad := range adapters {
		id := ad.Component().ID
		if host != nil {
			if _, exists := host.Adapter(id); exists {
				reg.skippedExternal[id] = true
				continue
			}
			if err := host.RegisterAdapter(ad); err != nil {
				return nil, fmt.Errorf("register field plant mirror %q: %w", id, err)
			}
		}
		reg.adapters[id] = ad
	}
	return reg, nil
}

// HasDevice reports whether deviceID is managed by a FieldPlant mirror adapter.
func (r *FieldPlantMirrorRegistry) HasDevice(deviceID string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	_, ok := r.adapters[strings.TrimSpace(deviceID)]
	r.mu.RUnlock()
	return ok
}

// Observe applies newly polled FieldPlant readings to matching mirror adapters so the next
// Host.RunCycle switches them into ProcessImage.
func (r *FieldPlantMirrorRegistry) Observe(ts time.Time, devices []mqttUplinkDevice) {
	if r == nil || len(devices) == 0 {
		return
	}
	for _, dev := range devices {
		devID := strings.TrimSpace(dev.DeviceID)
		if devID == "" || strings.Contains(devID, "/") || len(dev.Points) == 0 {
			continue
		}
		sampleTS := ts.UTC()
		if sampleTS.IsZero() && dev.DeviceTimestamp > 0 {
			sampleTS = time.UnixMilli(dev.DeviceTimestamp).UTC()
		}

		r.mu.RLock()
		if r.skippedExternal[devID] {
			r.mu.RUnlock()
			continue
		}
		ad := r.adapters[devID]
		r.mu.RUnlock()

		if ad == nil || len(ad.missingDescriptors(devID, dev.Points)) > 0 {
			ad = r.ensureAdapterChannels(devID, dev.Points)
			if ad == nil {
				continue
			}
		}
		ad.UpdatePoints(sampleTS, dev.Points)
	}
}

func (r *FieldPlantMirrorRegistry) ensureAdapterChannels(deviceID string, points []mqttUplinkPoint) *FieldPlantMirrorAdapter {
	r.mu.Lock()
	if r.skippedExternal[deviceID] {
		r.mu.Unlock()
		return nil
	}
	existing := r.adapters[deviceID]
	var next *FieldPlantMirrorAdapter
	var changed bool
	if existing == nil {
		if r.host != nil {
			if _, ext := r.host.Adapter(deviceID); ext {
				r.skippedExternal[deviceID] = true
				r.mu.Unlock()
				return nil
			}
		}
		var descs []edgecontrol.ChannelDescriptor
		seen := make(map[string]struct{}, len(points))
		for _, pt := range points {
			code := strings.TrimSpace(pt.Code)
			if code == "" || strings.Contains(code, "/") {
				continue
			}
			if _, dup := seen[code]; dup {
				continue
			}
			seen[code] = struct{}{}
			descs = append(descs, inferMirrorChannelDescriptor(deviceID, pt))
		}
		if len(descs) == 0 {
			r.mu.Unlock()
			return nil
		}
		next = newFieldPlantMirrorAdapter(deviceID, descs)
		changed = true
	} else {
		extra := existing.missingDescriptors(deviceID, points)
		if len(extra) == 0 {
			r.mu.Unlock()
			return existing
		}
		next = existing.cloneWithAddedChannels(extra)
		changed = true
	}
	r.adapters[deviceID] = next
	r.mu.Unlock()

	if changed && r.host != nil {
		_ = r.host.ReplaceOrRegisterDevice(next)
	}
	return next
}

// BuildFieldPlantMirrorAdapters constructs sorted read-only mirror DeviceAdapters for all
// devices and channels declared by cfg (plus effective copy routes).
func BuildFieldPlantMirrorAdapters(cfg FieldPlantConfig, effectiveRoutes []PointCopyRouteConfig) []*FieldPlantMirrorAdapter {
	cfg.ApplyDefaults()
	byDev := make(map[string]map[string]edgecontrol.ChannelDescriptor)
	addCh := func(deviceID, pointCode string, dataType edgecontrol.DataType, unit string, prio edgecontrol.DataPriority) {
		dev := strings.TrimSpace(deviceID)
		pt := strings.TrimSpace(pointCode)
		if dev == "" || pt == "" || strings.Contains(dev, "/") || strings.Contains(pt, "/") {
			return
		}
		pts := byDev[dev]
		if pts == nil {
			pts = make(map[string]edgecontrol.ChannelDescriptor)
			byDev[dev] = pts
		}
		if _, exists := pts[pt]; exists {
			return
		}
		pts[pt] = newMirrorChannelDescriptor(dev, pt, dataType, unit, prio)
	}

	if cfg.Meters != nil {
		for _, m := range cfg.Meters.Devices {
			addCh(m.DeviceKey, "power_total", edgecontrol.DataTypeDouble, "kW", edgecontrol.PriorityHigh)
			addCh(m.DeviceKey, "active_energy_total", edgecontrol.DataTypeDouble, "kWh", edgecontrol.PriorityHigh)
			addCh(m.DeviceKey, "run_state", edgecontrol.DataTypeString, "", edgecontrol.PriorityHigh)
			if m.ControllerDeviceKey != "" {
				addCh(m.ControllerDeviceKey, "run_state", edgecontrol.DataTypeString, "", edgecontrol.PriorityHigh)
			}
		}
		if cfg.Meters.SynthesizedTotal != nil {
			addCh(cfg.Meters.SynthesizedTotal.DeviceKey, "power_total", edgecontrol.DataTypeDouble, "kW", edgecontrol.PriorityHigh)
			addCh(cfg.Meters.SynthesizedTotal.DeviceKey, "active_energy_total", edgecontrol.DataTypeDouble, "kWh", edgecontrol.PriorityHigh)
		}
	}

	if cfg.FlowMeter != nil {
		f := cfg.FlowMeter
		addCh(f.DeviceKey, "flow_meter", edgecontrol.DataTypeDouble, "m3/h", edgecontrol.PriorityHigh)
		if f.VelocityRegisterOffset != nil {
			addCh(f.DeviceKey, "flow_velocity", edgecontrol.DataTypeDouble, "m/s", edgecontrol.PriorityHigh)
		}
		if f.PumpMirror != nil {
			addCh(f.PumpMirror.DeviceKey, f.PumpMirror.FlowPointCode, edgecontrol.DataTypeDouble, "m3/h", edgecontrol.PriorityHigh)
			addCh(f.PumpMirror.DeviceKey, "run_state", edgecontrol.DataTypeString, "", edgecontrol.PriorityHigh)
		}
	}

	if cfg.Concentrator != nil {
		conc := cfg.Concentrator
		for _, ch := range conc.ActiveChannels {
			addCh(conc.TempDeviceKey, fmt.Sprintf("t%d", ch), edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh)
			addCh(conc.VoltageDeviceKey, fmt.Sprintf("v%d", ch), edgecontrol.DataTypeInteger, "mV", edgecontrol.PriorityMedium)
			addCh(conc.RSSIDeviceKey, fmt.Sprintf("rssi%d", ch), edgecontrol.DataTypeInteger, "dBm", edgecontrol.PriorityMedium)
			addCh(conc.StatusDeviceKey, fmt.Sprintf("status%d", ch), edgecontrol.DataTypeString, "", edgecontrol.PriorityMedium)
		}
		addCh(conc.StatusDeviceKey, "online_count", edgecontrol.DataTypeInteger, "", edgecontrol.PriorityMedium)
		addCh(conc.StatusDeviceKey, "offline_count", edgecontrol.DataTypeInteger, "", edgecontrol.PriorityMedium)
		addCh(conc.StatusDeviceKey, "low_battery_count", edgecontrol.DataTypeInteger, "", edgecontrol.PriorityMedium)
		addCh(conc.StatusDeviceKey, "min_voltage_mv", edgecontrol.DataTypeInteger, "mV", edgecontrol.PriorityMedium)
		addCh(conc.StatusDeviceKey, "min_rssi_dbm", edgecontrol.DataTypeInteger, "dBm", edgecontrol.PriorityMedium)

		routes := effectiveRoutes
		if routes == nil {
			routes = conc.CopyRoutes
		}
		for _, route := range routes {
			for _, tgt := range route.Targets {
				addCh(tgt.DeviceKey, tgt.PointCode, edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh)
			}
		}
	}

	if cfg.BTUCalculator != nil {
		btu := cfg.BTUCalculator
		addCh(btu.DeviceKey, "supply_water_temperature", edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh)
		addCh(btu.DeviceKey, "return_water_temperature", edgecontrol.DataTypeDouble, "Cel", edgecontrol.PriorityHigh)
		addCh(btu.DeviceKey, "temperature_difference", edgecontrol.DataTypeDouble, "K", edgecontrol.PriorityHigh)
		addCh(btu.DeviceKey, "flow_rate", edgecontrol.DataTypeDouble, "m3/h", edgecontrol.PriorityHigh)
		addCh(btu.DeviceKey, "instant_cooling_capacity", edgecontrol.DataTypeDouble, "kW", edgecontrol.PriorityHigh)
		addCh(btu.DeviceKey, "accumulated_cooling_energy", edgecontrol.DataTypeDouble, "kWh", edgecontrol.PriorityHigh)
	}

	if cfg.IsDOControlEnabled() {
		doCfg := cfg.DOControl
		for _, coil := range doCfg.Coils {
			addCh(doCfg.DeviceKey, coil.Channel, edgecontrol.DataTypeInteger, "", edgecontrol.PriorityHigh)
		}
		addCh(doCfg.DeviceKey, "run_state", edgecontrol.DataTypeString, "", edgecontrol.PriorityHigh)
		for _, u := range doCfg.Units {
			for _, aux := range u.Auxiliaries {
				addCh(aux.DeviceKey, "run_state", edgecontrol.DataTypeString, "", edgecontrol.PriorityHigh)
			}
		}
	}

	devIDs := make([]string, 0, len(byDev))
	for id := range byDev {
		devIDs = append(devIDs, id)
	}
	sort.Strings(devIDs)

	out := make([]*FieldPlantMirrorAdapter, 0, len(devIDs))
	for _, devID := range devIDs {
		pts := byDev[devID]
		descs := make([]edgecontrol.ChannelDescriptor, 0, len(pts))
		for _, d := range pts {
			descs = append(descs, d)
		}
		out = append(out, newFieldPlantMirrorAdapter(devID, descs))
	}
	return out
}

func newMirrorChannelDescriptor(deviceID, pointCode string, dataType edgecontrol.DataType, unit string, prio edgecontrol.DataPriority) edgecontrol.ChannelDescriptor {
	if prio == "" {
		prio = edgecontrol.PriorityHigh
	}
	return edgecontrol.ChannelDescriptor{
		ComponentID:               deviceID,
		ChannelID:                 pointCode,
		PointID:                   deviceID + ":" + pointCode,
		DataType:                  dataType,
		Access:                    edgecontrol.AccessReadOnly,
		Unit:                      unit,
		Category:                  edgecontrol.ChannelCategoryOpenemsType,
		PollPriority:              prio,
		LocalPersistencePriority:  prio,
		RemotePersistencePriority: prio,
		AggregationPriority:       prio,
		ResendPriority:            prio,
	}
}

func inferMirrorChannelDescriptor(deviceID string, pt mqttUplinkPoint) edgecontrol.ChannelDescriptor {
	dt := edgecontrol.DataTypeDouble
	switch pt.Value.(type) {
	case string:
		dt = edgecontrol.DataTypeString
	case bool:
		dt = edgecontrol.DataTypeBoolean
	case int, int32, int64:
		dt = edgecontrol.DataTypeInteger
	}
	unit := ""
	if pt.Unit != nil {
		unit = strings.TrimSpace(*pt.Unit)
	}
	return newMirrorChannelDescriptor(deviceID, strings.TrimSpace(pt.Code), dt, unit, edgecontrol.PriorityHigh)
}

func toMirrorSample(desc edgecontrol.ChannelDescriptor, pt mqttUplinkPoint, observedAt time.Time, seq uint64) (edgecontrol.Sample, bool) {
	q := edgecontrol.QualityGood
	if pt.Quality != 0 {
		q = edgecontrol.QualityUnavailable
	}
	val, ok := toMirrorEdgeValue(pt.Value, desc.DataType)
	if !ok {
		return edgecontrol.Sample{}, false
	}
	return edgecontrol.Sample{
		Value:      val,
		Quality:    q,
		ObservedAt: observedAt,
		Sequence:   seq,
	}, true
}

func toMirrorEdgeValue(raw any, expected edgecontrol.DataType) (edgecontrol.Value, bool) {
	switch expected {
	case edgecontrol.DataTypeDouble:
		switch v := raw.(type) {
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return edgecontrol.Value{}, false
			}
			return edgecontrol.DoubleValue(v), true
		case float32:
			f := float64(v)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return edgecontrol.Value{}, false
			}
			return edgecontrol.DoubleValue(f), true
		case int:
			return edgecontrol.DoubleValue(float64(v)), true
		case int32:
			return edgecontrol.DoubleValue(float64(v)), true
		case int64:
			return edgecontrol.DoubleValue(float64(v)), true
		case uint:
			return edgecontrol.DoubleValue(float64(v)), true
		case uint16:
			return edgecontrol.DoubleValue(float64(v)), true
		case uint32:
			return edgecontrol.DoubleValue(float64(v)), true
		case uint64:
			return edgecontrol.DoubleValue(float64(v)), true
		case bool:
			if v {
				return edgecontrol.DoubleValue(1), true
			}
			return edgecontrol.DoubleValue(0), true
		default:
			return edgecontrol.Value{}, false
		}
	case edgecontrol.DataTypeInteger:
		switch v := raw.(type) {
		case int:
			return edgecontrol.IntegerValue(int32(v)), true
		case int32:
			return edgecontrol.IntegerValue(v), true
		case int64:
			return edgecontrol.IntegerValue(int32(v)), true
		case uint16:
			return edgecontrol.IntegerValue(int32(v)), true
		case uint32:
			return edgecontrol.IntegerValue(int32(v)), true
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return edgecontrol.Value{}, false
			}
			return edgecontrol.IntegerValue(int32(v)), true
		default:
			return edgecontrol.Value{}, false
		}
	case edgecontrol.DataTypeString:
		s, ok := raw.(string)
		if !ok {
			s = fmt.Sprint(raw)
		}
		return edgecontrol.StringValue(s), true
	case edgecontrol.DataTypeBoolean:
		if b, ok := raw.(bool); ok {
			return edgecontrol.BooleanValue(b), true
		}
		return edgecontrol.Value{}, false
	default:
		return edgecontrol.Value{}, false
	}
}
