package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

func TestDefaultCommandMQTTClientID(t *testing.T) {
	t.Run("explicit env overrides hostname default", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_CLIENT_ID", "  custom-command-client  ")
		if got := defaultCommandMQTTClientID(); got != "custom-command-client" {
			t.Fatalf("defaultCommandMQTTClientID() = %q, want custom-command-client", got)
		}
	})

	t.Run("unset env derives sanitized hostname suffix", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_CLIENT_ID", "")
		hostname, err := os.Hostname()
		got := defaultCommandMQTTClientID()
		if err != nil || sanitizeClientID(hostname) == "" {
			if got != "connectivity-commands" {
				t.Fatalf("defaultCommandMQTTClientID() = %q, want fallback connectivity-commands", got)
			}
		} else {
			want := "connectivity-commands-" + sanitizeClientID(hostname)
			if got != want {
				t.Fatalf("defaultCommandMQTTClientID() = %q, want %q", got, want)
			}
		}
	})

	t.Run("sanitizeClientID normalizes special characters and empty fallback", func(t *testing.T) {
		for _, tc := range []struct {
			hostname string
			want     string
		}{
			{hostname: "connectivity-0", want: "connectivity-commands-connectivity-0"},
			{hostname: "pod_1.prod.internal", want: "connectivity-commands-pod_1-prod-internal"},
			{hostname: "  !!!  ", want: "connectivity-commands"},
			{hostname: "", want: "connectivity-commands"},
		} {
			if got := defaultCommandMQTTClientIDForHost(tc.hostname); got != tc.want {
				t.Fatalf("defaultCommandMQTTClientIDForHost(%q) = %q, want %q", tc.hostname, got, tc.want)
			}
		}
		longHost := strings.Repeat("a", 80)
		gotLong := defaultCommandMQTTClientIDForHost(longHost)
		if gotLong != "connectivity-commands-"+strings.Repeat("a", 64) {
			t.Fatalf("expected 64-char sanitized suffix, got %q", gotLong)
		}
	})
}

func TestCommandSharedSubscriptionGroup(t *testing.T) {
	t.Run("unset defaults to command-dispatcher", func(t *testing.T) {
		if prev, had := os.LookupEnv("COMMAND_MQTT_SHARE_GROUP"); had {
			t.Cleanup(func() { _ = os.Setenv("COMMAND_MQTT_SHARE_GROUP", prev) })
			_ = os.Unsetenv("COMMAND_MQTT_SHARE_GROUP")
		}
		if got := commandSharedSubscriptionGroup(); got != "command-dispatcher" {
			t.Fatalf("commandSharedSubscriptionGroup() = %q, want command-dispatcher", got)
		}
	})

	for _, disabled := range []string{"", "   ", "off", "OFF", "none", "NONE"} {
		t.Run("disabled_"+disabled, func(t *testing.T) {
			t.Setenv("COMMAND_MQTT_SHARE_GROUP", disabled)
			if got := commandSharedSubscriptionGroup(); got != "" {
				t.Fatalf("commandSharedSubscriptionGroup(%q) = %q, want empty", disabled, got)
			}
		})
	}

	t.Run("custom group is trimmed and preserved", func(t *testing.T) {
		t.Setenv("COMMAND_MQTT_SHARE_GROUP", " command-pool_2 ")
		if got := commandSharedSubscriptionGroup(); got != "command-pool_2" {
			t.Fatalf("commandSharedSubscriptionGroup() = %q, want command-pool_2", got)
		}
	})
}

func TestCommandRuntimeAnyWorkerFastNoWorkGuard(t *testing.T) {
	claimCtx, cancelClaim := context.WithCancel(context.Background())
	defer cancelClaim()

	var noWorkCalls atomic.Int32
	var timestamps [4]int64
	doneObserved := make(chan struct{})
	rt := &commandRuntime{
		dispatchWorkers: 1,
		dispatchAnyFn: func(_ context.Context, _ time.Duration) error {
			n := int(noWorkCalls.Add(1))
			if n <= len(timestamps) {
				timestamps[n-1] = time.Now().UnixNano()
				if n == len(timestamps) {
					close(doneObserved)
				}
			}
			return commandservice.ErrNoDispatchAvailable
		},
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go rt.RunWithDrain(claimCtx, context.Background(), logger)

	select {
	case <-doneObserved:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for fast noWork cycles; calls=%d", noWorkCalls.Load())
	}
	cancelClaim()
	if err := rt.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	// Fast noWork spin guard sleeps commandTenantBaseBackoff (100ms) across 3 waits.
	totalElapsed := time.Duration(timestamps[3] - timestamps[0])
	if totalElapsed < 220*time.Millisecond {
		t.Fatalf("expected fast noWork guard (~300ms across 3 waits), got %s", totalElapsed)
	}
}

func TestCommandRuntimeRunWithDrainFinishesInFlightCommand(t *testing.T) {
	claimCtx, cancelClaim := context.WithCancel(context.Background())
	execCtx, cancelExec := context.WithCancel(context.Background())
	defer cancelExec()

	started := make(chan struct{})
	release := make(chan error, 1)
	var dispatchCalls atomic.Int32
	var verifyCalls atomic.Int32
	var seenExecErrDuringFlight atomic.Bool

	rt := &commandRuntime{
		dispatchWorkers:     1,
		verificationWorkers: 1,
		dispatchAnyFn: func(ctx context.Context, _ time.Duration) error {
			dispatchCalls.Add(1)
			close(started)
			err := <-release
			if ctx.Err() != nil {
				seenExecErrDuringFlight.Store(true)
			}
			return err
		},
		verifyAnyFn: func(_ context.Context, _ time.Duration) error {
			verifyCalls.Add(1)
			return commandservice.ErrVerificationNotAvailable
		},
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go rt.RunWithDrain(claimCtx, execCtx, logger)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight dispatch did not start")
	}

	cancelClaim()

	waitPrematureCtx, cancelPremature := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelPremature()
	if err := rt.Wait(waitPrematureCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait while in-flight turn active returned %v, want DeadlineExceeded", err)
	}

	release <- nil

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()
	if err := rt.Wait(waitCtx); err != nil {
		t.Fatalf("Wait after releasing in-flight command failed: %v", err)
	}
	if seenExecErrDuringFlight.Load() {
		t.Fatal("in-flight execution context was canceled when claimCtx stopped")
	}
	if dispatchCalls.Load() != 1 {
		t.Fatalf("dispatchCalls = %d, want 1 (no new claim allowed after claimCtx cancel)", dispatchCalls.Load())
	}
}

func TestCommandRuntimeDecoupledWorkerPoolsExecuteConcurrently(t *testing.T) {
	t.Parallel()

	claimCtx, cancelClaim := context.WithCancel(context.Background())
	defer cancelClaim()

	var dispActive, dispMax atomic.Int32
	var verCalls atomic.Int32
	allDispEntered := make(chan struct{})
	firstVerEntered := make(chan struct{})
	releaseWorkers := make(chan struct{})
	var closeAllDispOnce sync.Once
	var closeFirstVerOnce sync.Once

	rt := &commandRuntime{
		dispatchWorkers:     4,
		verificationWorkers: 2,
		dispatchAnyFn: func(_ context.Context, _ time.Duration) error {
			cur := dispActive.Add(1)
			for {
				prev := dispMax.Load()
				if cur <= prev || dispMax.CompareAndSwap(prev, cur) {
					break
				}
			}
			if cur == 4 {
				closeAllDispOnce.Do(func() { close(allDispEntered) })
			}
			<-releaseWorkers
			dispActive.Add(-1)
			return commandservice.ErrNoDispatchAvailable
		},
		verifyAnyFn: func(_ context.Context, _ time.Duration) error {
			verCalls.Add(1)
			closeFirstVerOnce.Do(func() { close(firstVerEntered) })
			<-releaseWorkers
			return commandservice.ErrVerificationNotAvailable
		},
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go rt.RunWithDrain(claimCtx, context.Background(), logger)

	select {
	case <-allDispEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected 4 concurrent dispatch workers, max observed=%d", dispMax.Load())
	}
	select {
	case <-firstVerEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected verification worker to execute while dispatch workers were busy")
	}
	close(releaseWorkers)
	cancelClaim()
	if err := rt.Wait(context.Background()); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if dispMax.Load() < 4 || verCalls.Load() < 1 {
		t.Fatalf("unexpected concurrency stats dispMax=%d verCalls=%d", dispMax.Load(), verCalls.Load())
	}
}

func TestDeferredCommandWakeFlusherDebouncesDevicesAndResolvesGateways(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	flusher := newDeferredCommandWakeFlusher(logger, nil, 15*time.Millisecond)
	defer flusher.Close()

	// No-op before Start (e.g. when command runtime is disabled).
	flusher.NotifyDevices(context.Background(), "tenant-1", []string{"dev-before"})
	flusher.NotifyGateway("gw-before")

	type wakeCall struct {
		tenantID  string
		deviceIDs []string
	}
	var (
		mu    sync.Mutex
		calls []wakeCall
	)
	flushed := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flusher.Start(
		ctx,
		func(_ context.Context, tenantID string, deviceIDs []string) (int, error) {
			mu.Lock()
			calls = append(calls, wakeCall{tenantID: tenantID, deviceIDs: append([]string(nil), deviceIDs...)})
			mu.Unlock()
			flushed <- struct{}{}
			return len(deviceIDs), nil
		},
		func(_ context.Context, gatewayID string) (string, []string, error) {
			if gatewayID == "gw-1" {
				return "tenant-1", []string{"dev-2", "dev-3"}, nil
			}
			return "", nil, errors.New("missing")
		},
	)

	flusher.NotifyDevices(context.Background(), "tenant-1", []string{"dev-1", "dev-2"})
	flusher.NotifyDevices(context.Background(), "tenant-1", []string{"dev-1"})
	flusher.NotifyGateway("gw-1")

	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for debounced wake flush")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("calls len=%d, want 1 coalesced call; calls=%+v", len(calls), calls)
	}
	if calls[0].tenantID != "tenant-1" || strings.Join(calls[0].deviceIDs, ",") != "dev-1,dev-2,dev-3" {
		t.Fatalf("unexpected wake call=%+v", calls[0])
	}
}
