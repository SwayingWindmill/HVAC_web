package alarmservice

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type panickingDueEvaluationRunner struct {
	dueCalls   atomic.Int32
	watchCalls atomic.Int32
	pruneCalls atomic.Int32
	tick2Done  chan struct{}
	once       sync.Once
}

func (r *panickingDueEvaluationRunner) RunDueEvaluations(context.Context, string, time.Time) (DueEvaluationResult, error) {
	if r.dueCalls.Add(1) == 1 {
		panic("secret-alarm-database-dsn")
	}
	return DueEvaluationResult{Evaluated: 1}, nil
}

func (r *panickingDueEvaluationRunner) WatchUnevaluableRules(context.Context, time.Time) (int, int, error) {
	r.watchCalls.Add(1)
	return 0, 0, nil
}

func (r *panickingDueEvaluationRunner) PruneExpiredEvaluationEvidence(context.Context) (int64, error) {
	r.pruneCalls.Add(1)
	r.once.Do(func() {
		if r.tick2Done != nil {
			close(r.tick2Done)
		}
	})
	return 0, nil
}

func TestRunDueEvaluationPassRecoversPanicAndContinues(t *testing.T) {
	var logs syncLogBuffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	runner := &panickingDueEvaluationRunner{}

	runDueEvaluationPass(context.Background(), runner, "alarm-due-evaluations:test", logger)
	runDueEvaluationPass(context.Background(), runner, "alarm-due-evaluations:test", logger)

	if runner.dueCalls.Load() != 2 || runner.watchCalls.Load() != 1 || runner.pruneCalls.Load() != 1 {
		t.Fatalf("unexpected call counts: due=%d watch=%d prune=%d", runner.dueCalls.Load(), runner.watchCalls.Load(), runner.pruneCalls.Load())
	}
	output := logs.String()
	if strings.Contains(output, "secret-alarm-database-dsn") || !strings.Contains(output, `"error":"[REDACTED]"`) || !strings.Contains(output, `"component":"alarm.due_evaluations"`) {
		t.Fatalf("unexpected log output: %s", output)
	}
}

func TestRunScheduledEvaluationsRecoversTickPanicAndRunsNextTick(t *testing.T) {
	var logs syncLogBuffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	runner := &panickingDueEvaluationRunner{tick2Done: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runScheduledEvaluations(ctx, runner, logger, 5*time.Millisecond)

	select {
	case <-runner.tick2Done:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for tick N+1 after tick N panic; dueCalls=%d logs=%s", runner.dueCalls.Load(), logs.String())
	}

	output := logs.String()
	if strings.Contains(output, "secret-alarm-database-dsn") || !strings.Contains(output, `"error":"[REDACTED]"`) || !strings.Contains(output, `"component":"alarm.due_evaluations"`) {
		t.Fatalf("unexpected log output: %s", output)
	}
	if runner.dueCalls.Load() < 2 || runner.watchCalls.Load() < 1 || runner.pruneCalls.Load() < 1 {
		t.Fatalf("expected tick N+1 to complete after tick N panic: due=%d watch=%d prune=%d", runner.dueCalls.Load(), runner.watchCalls.Load(), runner.pruneCalls.Load())
	}
}
