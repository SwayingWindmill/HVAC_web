package simulator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	rpcPollTimeout = 20 * time.Second
	rpcRetryDelay  = time.Second
)

type Runtime struct {
	plant        *Plant
	client       *ThingsBoardClient
	scheduler    *MeasurementScheduler
	interval     time.Duration
	controllable []string
	logger       *slog.Logger

	mu                        sync.RWMutex
	lastPublished             time.Time
	lastError                 string
	publishedDeviceIDs        map[string]struct{}
	expectedPublishingDevices int

	rpcMu           sync.Mutex
	lastRPCByDevice map[string]cachedRPCResult
}

type cachedRPCResult struct {
	ID     int64
	Result CommandResult
}

func NewRuntime(config Config, plant *Plant, client *ThingsBoardClient, logger *slog.Logger) (*Runtime, error) {
	if plant == nil || client == nil {
		return nil, errors.New("simulator runtime dependencies are incomplete")
	}
	scheduler, err := NewMeasurementScheduler(config)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	publishingDevices := make(map[string]struct{}, len(config.Points))
	for _, point := range config.Points {
		publishingDevices[point.DeviceID] = struct{}{}
	}
	return &Runtime{
		plant:     plant,
		client:    client,
		scheduler: scheduler,
		interval:  config.Interval(),
		controllable: []string{
			config.Plant.Chiller.ID,
			config.Plant.ChilledWaterPump.ID,
			config.Plant.CoolingWaterPump.ID,
			config.Plant.CoolingTower.ID,
		},
		logger:                    logger,
		publishedDeviceIDs:        make(map[string]struct{}, len(publishingDevices)),
		expectedPublishingDevices: len(publishingDevices),
		lastRPCByDevice:           make(map[string]cachedRPCResult, len(config.Plant.DeviceIDs())),
	}, nil
}

func (runtime *Runtime) Run(ctx context.Context) error {
	var pollers sync.WaitGroup
	for _, deviceID := range runtime.controllable {
		deviceID := deviceID
		pollers.Add(1)
		go func() {
			defer pollers.Done()
			runtime.pollCommands(ctx, deviceID)
		}()
	}

	if err := runtime.publish(ctx, runtime.plant.Tick(runtime.interval)); err != nil {
		runtime.logger.Warn("eg8200_initial_publish_failed", "error", err.Error())
	}
	ticker := time.NewTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			pollers.Wait()
			return nil
		case <-ticker.C:
			if err := runtime.publish(ctx, runtime.plant.Tick(runtime.interval)); err != nil {
				runtime.logger.Warn("eg8200_telemetry_publish_failed", "error", err.Error())
			}
		}
	}
}

func (runtime *Runtime) Ready() bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return !runtime.lastPublished.IsZero() &&
		runtime.lastError == "" &&
		len(runtime.publishedDeviceIDs) == runtime.expectedPublishingDevices
}

func (runtime *Runtime) publish(ctx context.Context, snapshot Snapshot) error {
	measurements, err := runtime.scheduler.Observe(snapshot)
	if err != nil {
		runtime.recordPublishFailure(err)
		return err
	}
	if len(measurements) == 0 {
		return nil
	}
	publishContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := runtime.client.PublishMeasurements(publishContext, measurements); err != nil {
		runtime.recordPublishFailure(err)
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	for _, measurement := range measurements {
		runtime.publishedDeviceIDs[measurement.DeviceID] = struct{}{}
		if measurement.ObservedAt.After(runtime.lastPublished) {
			runtime.lastPublished = measurement.ObservedAt
		}
	}
	runtime.lastError = ""
	return nil
}

func (runtime *Runtime) recordPublishFailure(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.lastError = err.Error()
}

func (runtime *Runtime) pollCommands(ctx context.Context, deviceID string) {
	for ctx.Err() == nil {
		command, err := runtime.client.PollRPC(ctx, deviceID, rpcPollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			runtime.logger.Warn("eg8200_rpc_poll_failed", "device_id", deviceID, "error", err.Error())
			if !sleepContext(ctx, rpcRetryDelay) {
				return
			}
			continue
		}
		if command == nil {
			continue
		}
		result := runtime.applyRPCCommand(*command)
		replyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		replyErr := runtime.client.ReplyRPC(replyContext, command.DeviceID, command.ID, result)
		cancel()
		if replyErr != nil {
			runtime.logger.Warn("eg8200_rpc_reply_failed", "device_id", deviceID, "rpc_id", command.ID, "error", replyErr.Error())
			continue
		}
		runtime.logger.Info(
			"eg8200_command_processed",
			"device_id", deviceID,
			"rpc_id", command.ID,
			"method", command.Method,
			"success", result.Success,
			"code", result.Code,
			"business_revision", result.BusinessRevision,
		)
	}
}

func (runtime *Runtime) applyRPCCommand(command RPCCommand) CommandResult {
	runtime.rpcMu.Lock()
	cached, exists := runtime.lastRPCByDevice[command.DeviceID]
	if exists && cached.ID == command.ID {
		runtime.rpcMu.Unlock()
		return cached.Result
	}
	runtime.rpcMu.Unlock()

	result := runtime.plant.ApplyCommand(Command{
		DeviceID: command.DeviceID,
		Method:   command.Method,
		Params:   command.Params,
	})
	runtime.rpcMu.Lock()
	runtime.lastRPCByDevice[command.DeviceID] = cachedRPCResult{ID: command.ID, Result: result}
	runtime.rpcMu.Unlock()
	return result
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
