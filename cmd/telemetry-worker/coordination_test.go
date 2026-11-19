package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedisEntry struct {
	value     string
	expiresAt time.Time
}

type fakeRESPServer struct {
	listener net.Listener
	mu       sync.Mutex
	store    map[string]fakeRedisEntry
	evalOps  atomic.Int32
	wg       sync.WaitGroup
}

func startFakeRESPServer(t *testing.T) *fakeRESPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fakeRESPServer{
		listener: listener,
		store:    make(map[string]fakeRedisEntry),
	}
	srv.wg.Add(1)
	go func() {
		defer srv.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			srv.wg.Add(1)
			go func(c net.Conn) {
				defer srv.wg.Done()
				defer func() { _ = c.Close() }()
				srv.handleConn(c)
			}(conn)
		}
	}()
	t.Cleanup(func() {
		_ = srv.listener.Close()
		srv.wg.Wait()
	})
	return srv
}

func (s *fakeRESPServer) addr() string {
	return s.listener.Addr().String()
}

func (s *fakeRESPServer) handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		reply := s.exec(args)
		if _, err := io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("unexpected RESP header %q", line)
	}
	count, err := strconv.Atoi(strings.TrimPrefix(line, "*"))
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for range count {
		sizeLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeLine = strings.TrimSuffix(sizeLine, "\r\n")
		size, err := strconv.Atoi(strings.TrimPrefix(sizeLine, "$"))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func (s *fakeRESPServer) exec(args []string) string {
	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "HELLO":
		return "-ERR unknown command 'HELLO'\r\n"
	case "PING":
		return "+PONG\r\n"
	case "CLIENT", "SELECT":
		return "+OK\r\n"
	case "SET":
		return s.execSet(args[1:])
	case "GET":
		s.mu.Lock()
		defer s.mu.Unlock()
		entry, ok := s.getLocked(args[1])
		if !ok {
			return "$-1\r\n"
		}
		return fmt.Sprintf("$%d\r\n%s\r\n", len(entry.value), entry.value)
	case "DEL":
		s.mu.Lock()
		defer s.mu.Unlock()
		var removed int
		for _, key := range args[1:] {
			if _, ok := s.getLocked(key); ok {
				delete(s.store, key)
				removed++
			}
		}
		return fmt.Sprintf(":%d\r\n", removed)
	case "EVALSHA":
		return "-NOSCRIPT No matching script\r\n"
	case "EVAL":
		return s.execEval(args[1:])
	default:
		return "-ERR unknown command\r\n"
	}
}

func (s *fakeRESPServer) getLocked(key string) (fakeRedisEntry, bool) {
	entry, ok := s.store[key]
	if !ok {
		return fakeRedisEntry{}, false
	}
	if !entry.expiresAt.IsZero() && !entry.expiresAt.After(time.Now()) {
		delete(s.store, key)
		return fakeRedisEntry{}, false
	}
	return entry, true
}

func (s *fakeRESPServer) execSet(args []string) string {
	if len(args) < 2 {
		return "-ERR wrong number of arguments\r\n"
	}
	key, val := args[0], args[1]
	nx := false
	var ttl time.Duration
	for i := 2; i < len(args); i++ {
		opt := strings.ToUpper(args[i])
		switch opt {
		case "NX":
			nx = true
		case "PX":
			i++
			ms, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return "-ERR invalid PX\r\n"
			}
			ttl = time.Duration(ms) * time.Millisecond
		case "EX":
			i++
			sec, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return "-ERR invalid EX\r\n"
			}
			ttl = time.Duration(sec) * time.Second
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if nx {
		if _, exists := s.getLocked(key); exists {
			return "$-1\r\n"
		}
	}
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.store[key] = fakeRedisEntry{value: val, expiresAt: exp}
	return "+OK\r\n"
}

func (s *fakeRESPServer) execEval(args []string) string {
	s.evalOps.Add(1)
	if len(args) < 4 {
		return "-ERR eval args\r\n"
	}
	script, key, token := args[0], args[2], args[3]
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.getLocked(key)
	if !exists || entry.value != token {
		return ":0\r\n"
	}
	if strings.Contains(script, "DEL") {
		delete(s.store, key)
		return ":1\r\n"
	}
	if strings.Contains(script, "PEXPIRE") && len(args) >= 5 {
		ms, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return "-ERR invalid PEXPIRE\r\n"
		}
		entry.expiresAt = time.Now().Add(time.Duration(ms) * time.Millisecond)
		s.store[key] = entry
		return ":1\r\n"
	}
	return ":0\r\n"
}

func TestRedisLeaseLockerAcquireContentionRenewalAndSafeRelease(t *testing.T) {
	srv := startFakeRESPServer(t)
	clientA := redis.NewClient(&redis.Options{Addr: srv.addr(), Protocol: 2})
	defer func() { _ = clientA.Close() }()
	clientB := redis.NewClient(&redis.Options{Addr: srv.addr(), Protocol: 2})
	defer func() { _ = clientB.Close() }()

	lockerA := newRedisLeaseLocker(clientA, "hvac:telemetry:lock:")
	lockerB := newRedisLeaseLocker(clientB, "hvac:telemetry:lock:")

	ctx := context.Background()
	releaseA, acquiredA, err := lockerA.Acquire(ctx, "total_submeter_projection", 60*time.Millisecond)
	if err != nil || !acquiredA {
		t.Fatalf("replica A acquire failed: acquired=%v err=%v", acquiredA, err)
	}

	if _, acquiredB, err := lockerB.Acquire(ctx, "total_submeter_projection", 60*time.Millisecond); err != nil || acquiredB {
		t.Fatalf("replica B should be blocked by contention: acquired=%v err=%v", acquiredB, err)
	}

	// Keep the lock across longer than initial TTL to verify background renewal extends it.
	time.Sleep(90 * time.Millisecond)
	if _, acquiredB, err := lockerB.Acquire(ctx, "total_submeter_projection", 60*time.Millisecond); err != nil || acquiredB {
		t.Fatalf("replica B should still see lock held due to renewal: acquired=%v err=%v", acquiredB, err)
	}

	releaseA(ctx)

	releaseB, acquiredB, err := lockerB.Acquire(ctx, "total_submeter_projection", 60*time.Millisecond)
	if err != nil || !acquiredB {
		t.Fatalf("replica B should acquire after A released: acquired=%v err=%v", acquiredB, err)
	}
	// Stale second release from A must be idempotent and never delete B's lease.
	releaseA(ctx)
	if _, acquiredA2, err := lockerA.Acquire(ctx, "total_submeter_projection", 60*time.Millisecond); err != nil || acquiredA2 {
		t.Fatalf("replica A stole or deleted B's lease: acquired=%v err=%v", acquiredA2, err)
	}
	releaseB(ctx)
}

func TestNextSettledTickWaitsWatermarkBufferAfterBoundary(t *testing.T) {
	hour := time.Hour
	buffer := 30 * time.Second

	// Just before boundary -> waits for 10:00:30, boundary 10:00:00.
	fireAt, boundary := nextSettledTick(time.Date(2026, 8, 1, 9, 59, 55, 0, time.UTC), hour, buffer)
	if !fireAt.Equal(time.Date(2026, 8, 1, 10, 0, 30, 0, time.UTC)) || !boundary.Equal(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("pre-boundary fireAt=%v boundary=%v", fireAt, boundary)
	}

	// Inside settling buffer past boundary -> still waits remaining buffer until 10:00:30.
	fireAt, boundary = nextSettledTick(time.Date(2026, 8, 1, 10, 0, 10, 0, time.UTC), hour, buffer)
	if !fireAt.Equal(time.Date(2026, 8, 1, 10, 0, 30, 0, time.UTC)) || !boundary.Equal(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("in-buffer fireAt=%v boundary=%v", fireAt, boundary)
	}

	// Exactly at settled watermark -> schedules next boundary + buffer (11:00:30).
	fireAt, boundary = nextSettledTick(time.Date(2026, 8, 1, 10, 0, 30, 0, time.UTC), hour, buffer)
	if !fireAt.Equal(time.Date(2026, 8, 1, 11, 0, 30, 0, time.UTC)) || !boundary.Equal(time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("at-buffer fireAt=%v boundary=%v", fireAt, boundary)
	}
}

func TestRunPeriodicLoopWaitsBoundaryBufferAndCoordinatesReplicas(t *testing.T) {
	locker := newLocalLeaseLocker(func() time.Time { return time.Date(2026, 8, 1, 10, 0, 30, 0, time.UTC) })
	var mu sync.Mutex
	var recorded []time.Time

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Replica 1 starts inside the settling buffer at 10:00:05 and waits until 10:00:30 before running.
	clock1 := time.Date(2026, 8, 1, 10, 0, 5, 0, time.UTC)
	run1 := 0
	runPeriodicLoop(ctx, periodicLoopConfig{
		LockKey:        "hourly_rollup",
		LockTTL:        time.Minute,
		Interval:       time.Hour,
		SettlingBuffer: 30 * time.Second,
		AlignToWindow:  true,
		Locker:         locker,
		Now:            func() time.Time { return clock1 },
		WaitUntil: func(_ context.Context, target time.Time) bool {
			if target.Before(time.Date(2026, 8, 1, 10, 0, 30, 0, time.UTC)) {
				t.Errorf("fired before settling watermark: %v", target)
			}
			clock1 = target
			run1++
			return run1 == 1
		},
	}, func(_ context.Context, boundary time.Time) {
		mu.Lock()
		recorded = append(recorded, boundary)
		mu.Unlock()
		// While replica 1 holds the lock at 10:00:30, replica 2 fails to acquire and skips.
		_, acquired2, err := locker.Acquire(context.Background(), "hourly_rollup", time.Minute)
		if err != nil || acquired2 {
			t.Errorf("contending replica acquired lock simultaneously: acquired=%v err=%v", acquired2, err)
		}
	})

	if len(recorded) != 1 || !recorded[0].Equal(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("recorded boundaries = %v, want [2026-08-01T10:00:00Z]", recorded)
	}

	// Lock was released cleanly on return so another replica can now acquire.
	rel, acquiredAfter, err := locker.Acquire(context.Background(), "hourly_rollup", time.Minute)
	if err != nil || !acquiredAfter {
		t.Fatalf("lock was not released cleanly: acquired=%v err=%v", acquiredAfter, err)
	}
	rel(context.Background())
}

func TestLoadTelemetryCoordinationLocksSelectsRedisOrLocalFallback(t *testing.T) {
	locker, closeFn, err := loadTelemetryCoordinationLocks(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if _, ok := locker.(*localLeaseLocker); !ok {
		t.Fatalf("expected localLeaseLocker without redis URL, got %T", locker)
	}

	locker, closeFn, err = loadTelemetryCoordinationLocks(func(name string) string {
		if name == "TELEMETRY_LATEST_CACHE_REDIS_URL" {
			return "redis://127.0.0.1:6379/0"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if _, ok := locker.(*redisLeaseLocker); !ok {
		t.Fatalf("expected redisLeaseLocker when fallback redis URL present, got %T", locker)
	}

	if _, _, err := loadTelemetryCoordinationLocks(func(name string) string {
		if name == "TELEMETRY_COORDINATION_REDIS_URL" {
			return "://bad-url"
		}
		return ""
	}); err == nil {
		t.Fatal("expected invalid redis URL error")
	}
}

type stubConditionAnchorRunner struct {
	calls atomic.Int32
	fn    func(context.Context, int32) (int, error)
}

func (s *stubConditionAnchorRunner) SyncOnce(ctx context.Context) (int, error) {
	call := s.calls.Add(1)
	if s.fn != nil {
		return s.fn(ctx, call)
	}
	return 0, nil
}

func TestRunConditionAnchorProjectionCoordinatedUsesLeaseLockAndRecoversFailures(t *testing.T) {
	locker := newLocalLeaseLocker(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runner := &stubConditionAnchorRunner{
		fn: func(_ context.Context, call int32) (int, error) {
			// While running under lease lock, a second replica must not acquire analytics_condition_anchors.
			if _, acquired, err := locker.Acquire(context.Background(), "analytics_condition_anchors", time.Minute); err != nil || acquired {
				t.Errorf("concurrent replica acquired analytics_condition_anchors lock: acquired=%v err=%v", acquired, err)
			}
			if call == 1 {
				return 3, nil
			}
			cancel()
			return 0, fmt.Errorf("transient clickhouse failure")
		},
	}

	runConditionAnchorProjectionCoordinated(ctx, runner, 5*time.Millisecond, nil, 0, locker, logger)

	if got := runner.calls.Load(); got < 2 {
		t.Fatalf("SyncOnce calls=%d, want >= 2", got)
	}
	out := logs.String()
	if !strings.Contains(out, `"analytics_condition_anchors_synced"`) || !strings.Contains(out, `"anchor_count":3`) {
		t.Fatalf("missing synced debug log: %s", out)
	}
	if !strings.Contains(out, `"ANALYTICS_CONDITION_ANCHOR_PROJECTION_FAILED"`) {
		t.Fatalf("missing failure warning log: %s", out)
	}
	// Lease must be released cleanly on exit.
	rel, acquiredAfter, err := locker.Acquire(context.Background(), "analytics_condition_anchors", time.Minute)
	if err != nil || !acquiredAfter {
		t.Fatalf("expected analytics_condition_anchors lock to be released, acquired=%v err=%v", acquiredAfter, err)
	}
	rel(context.Background())
}
