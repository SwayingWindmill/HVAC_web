package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type stubPresenceSweeper struct {
	calls atomic.Int32
	fn    func(call int32) (int, error)
}

func (stub *stubPresenceSweeper) SweepDuePresence(_ context.Context, _ time.Time, _ int) (int, error) {
	call := stub.calls.Add(1)
	return stub.fn(call)
}

func TestRunPresenceSweepTicksAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	sweeper := &stubPresenceSweeper{
		fn: func(call int32) (int, error) {
			switch call {
			case 1:
				return 2, nil
			case 2:
				return 0, errors.New("temporary db hiccup")
			default:
				cancel()
				return 0, nil
			}
		},
	}

	runPresenceSweep(ctx, sweeper, 5*time.Millisecond, logger)

	if got := sweeper.calls.Load(); got < 3 {
		t.Fatalf("calls=%d, want >= 3", got)
	}
	output := logs.String()
	if !strings.Contains(output, `"telemetry_presence_swept"`) || !strings.Contains(output, `"device_count":2`) {
		t.Fatalf("missing swept log in %s", output)
	}
	if !strings.Contains(output, `"telemetry_presence_sweep_failed"`) {
		t.Fatalf("missing failure log in %s", output)
	}
}
