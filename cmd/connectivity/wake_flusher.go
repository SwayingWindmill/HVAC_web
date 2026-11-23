package main

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	defaultWakeDebounce           = 25 * time.Millisecond
	defaultWakeRPCTimeout         = 5 * time.Second
	maxWakePendingTenants         = 64
	maxWakePendingDevicesPerTenant = 512
	maxWakePendingGateways        = 128
)

type deferredCommandWakeFunc func(ctx context.Context, tenantID string, deviceIDs []string) (int, error)
type wakeGatewayDevicesFunc func(ctx context.Context, gatewayID string) (string, []string, error)

type deferredCommandWakeFlusher struct {
	logger   *slog.Logger
	metrics  *observability.Registry
	debounce time.Duration

	enabled  atomic.Bool
	notifyCh chan struct{}

	mu              sync.Mutex
	started         bool
	closed          bool
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	wakeFn          deferredCommandWakeFunc
	gatewayDevices  wakeGatewayDevicesFunc
	pendingByTenant map[string]map[string]struct{}
	pendingGateways map[string]struct{}
}

func newDeferredCommandWakeFlusher(logger *slog.Logger, metrics *observability.Registry, debounce time.Duration) *deferredCommandWakeFlusher {
	if debounce <= 0 {
		debounce = defaultWakeDebounce
	}
	return &deferredCommandWakeFlusher{
		logger:          logger,
		metrics:         metrics,
		debounce:        debounce,
		notifyCh:        make(chan struct{}, 1),
		pendingByTenant: make(map[string]map[string]struct{}),
		pendingGateways: make(map[string]struct{}),
	}
}

func (f *deferredCommandWakeFlusher) Start(parentCtx context.Context, wakeFn deferredCommandWakeFunc, gatewayDevicesFn wakeGatewayDevicesFunc) {
	if f == nil || wakeFn == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started || f.closed {
		return
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	workerCtx, cancel := context.WithCancel(parentCtx)
	f.started = true
	f.cancel = cancel
	f.wakeFn = wakeFn
	f.gatewayDevices = gatewayDevicesFn
	f.enabled.Store(true)
	f.wg.Add(1)
	go f.run(workerCtx)
}

func (f *deferredCommandWakeFlusher) NotifyDevices(_ context.Context, tenantID string, deviceIDs []string) {
	if f == nil || !f.enabled.Load() || len(deviceIDs) == 0 {
		return
	}
	trimmedTenant := strings.TrimSpace(tenantID)
	if trimmedTenant == "" {
		return
	}
	f.mu.Lock()
	if f.closed || !f.started {
		f.mu.Unlock()
		return
	}
	added := false
	deviceSet, exists := f.pendingByTenant[trimmedTenant]
	if !exists {
		if len(f.pendingByTenant) >= maxWakePendingTenants {
			f.mu.Unlock()
			return
		}
		deviceSet = make(map[string]struct{}, len(deviceIDs))
		f.pendingByTenant[trimmedTenant] = deviceSet
	}
	for _, rawID := range deviceIDs {
		trimmedID := strings.TrimSpace(rawID)
		if trimmedID == "" {
			continue
		}
		if _, seen := deviceSet[trimmedID]; seen {
			continue
		}
		if len(deviceSet) >= maxWakePendingDevicesPerTenant {
			break
		}
		deviceSet[trimmedID] = struct{}{}
		added = true
	}
	if len(deviceSet) == 0 {
		delete(f.pendingByTenant, trimmedTenant)
	}
	f.mu.Unlock()
	if added {
		f.signal()
	}
}

func (f *deferredCommandWakeFlusher) NotifyGateway(gatewayID string) {
	if f == nil || !f.enabled.Load() {
		return
	}
	trimmedGateway := strings.TrimSpace(gatewayID)
	if trimmedGateway == "" {
		return
	}
	f.mu.Lock()
	if f.closed || !f.started {
		f.mu.Unlock()
		return
	}
	if _, exists := f.pendingGateways[trimmedGateway]; exists || len(f.pendingGateways) >= maxWakePendingGateways {
		f.mu.Unlock()
		return
	}
	f.pendingGateways[trimmedGateway] = struct{}{}
	f.mu.Unlock()
	f.signal()
}

func (f *deferredCommandWakeFlusher) signal() {
	select {
	case f.notifyCh <- struct{}{}:
	default:
	}
}

func (f *deferredCommandWakeFlusher) Close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		f.wg.Wait()
		return
	}
	f.closed = true
	f.enabled.Store(false)
	cancel := f.cancel
	f.pendingByTenant = nil
	f.pendingGateways = nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	f.wg.Wait()
}

func (f *deferredCommandWakeFlusher) run(ctx context.Context) {
	defer f.wg.Done()
	defer observability.Recover(f.logger, f.metrics, "connectivity.deferred_command_wake_flusher")

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.notifyCh:
		}
		timer := time.NewTimer(f.debounce)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		f.flushOnce(ctx)
	}
}

func (f *deferredCommandWakeFlusher) flushOnce(ctx context.Context) {
	f.mu.Lock()
	if f.closed || f.wakeFn == nil {
		f.mu.Unlock()
		return
	}
	byTenant := f.pendingByTenant
	gateways := f.pendingGateways
	f.pendingByTenant = make(map[string]map[string]struct{})
	f.pendingGateways = make(map[string]struct{})
	wakeFn := f.wakeFn
	gatewayDevices := f.gatewayDevices
	f.mu.Unlock()

	if len(gateways) > 0 && gatewayDevices != nil {
		gatewayIDs := make([]string, 0, len(gateways))
		for gatewayID := range gateways {
			gatewayIDs = append(gatewayIDs, gatewayID)
		}
		sort.Strings(gatewayIDs)
		for _, gatewayID := range gatewayIDs {
			lookupCtx, cancel := context.WithTimeout(ctx, defaultWakeRPCTimeout)
			tenantID, deviceIDs, err := gatewayDevices(lookupCtx, gatewayID)
			cancel()
			if err != nil {
				if ctx.Err() == nil && f.logger != nil {
					f.logger.Debug("wake_gateway_devices_lookup_failed", "gateway_id", gatewayID, "error", err.Error())
				}
				continue
			}
			tenantID = strings.TrimSpace(tenantID)
			if tenantID == "" || len(deviceIDs) == 0 {
				continue
			}
			set := byTenant[tenantID]
			if set == nil {
				set = make(map[string]struct{}, len(deviceIDs))
				byTenant[tenantID] = set
			}
			for _, id := range deviceIDs {
				if trimmed := strings.TrimSpace(id); trimmed != "" {
					set[trimmed] = struct{}{}
				}
			}
		}
	}

	if len(byTenant) == 0 {
		return
	}
	tenants := make([]string, 0, len(byTenant))
	for tenantID := range byTenant {
		tenants = append(tenants, tenantID)
	}
	sort.Strings(tenants)
	for _, tenantID := range tenants {
		deviceSet := byTenant[tenantID]
		if len(deviceSet) == 0 {
			continue
		}
		deviceIDs := make([]string, 0, len(deviceSet))
		for id := range deviceSet {
			deviceIDs = append(deviceIDs, id)
		}
		sort.Strings(deviceIDs)
		rpcCtx, cancel := context.WithTimeout(ctx, defaultWakeRPCTimeout)
		wokenCount, err := wakeFn(rpcCtx, tenantID, deviceIDs)
		cancel()
		if err != nil {
			if ctx.Err() == nil && f.logger != nil {
				f.logger.Warn("wake_deferred_commands_failed", "tenant_id", tenantID, "device_count", len(deviceIDs), "error", err.Error())
			}
			continue
		}
		if wokenCount > 0 && f.logger != nil {
			f.logger.Info("wake_deferred_commands_dispatched", "tenant_id", tenantID, "device_count", len(deviceIDs), "woken_count", wokenCount)
		}
	}
}
