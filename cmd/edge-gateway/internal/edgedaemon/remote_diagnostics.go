package edgedaemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const maxInMemoryLogLines = 1024

type daemonLogEntry struct {
	ts   time.Time
	line string
}

// RemoteDiagnosticsCollector keeps a bounded in-memory daemon log ring and samples host/bus/MQTT
// health metrics. It never spawns subshells or executes arbitrary remote commands.
type RemoteDiagnosticsCollector struct {
	mu          sync.Mutex
	startedAt   time.Time
	stateDir    string
	logs        []daemonLogEntry
	txFrames    int64
	rxErrors    int64
	reconnect   int64
	lastRTTMs   float64
	hasPrevCPU  bool
	prevCPUWall time.Time
	prevCPUTime time.Duration
}

type sharedDiagLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

type diagnosticsLogHandler struct {
	inner  slog.Handler
	text   slog.Handler
	shared *sharedDiagLogBuffer
	diag   *RemoteDiagnosticsCollector
}

// WrapLoggerWithDiagnostics returns an *slog.Logger that forwards all enabled log records
// to base while also capturing redacted-ready log lines into diag's bounded in-memory ring.
func WrapLoggerWithDiagnostics(base *slog.Logger, diag *RemoteDiagnosticsCollector) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	if diag == nil {
		return base
	}
	if _, already := base.Handler().(*diagnosticsLogHandler); already {
		return base
	}
	shared := &sharedDiagLogBuffer{}
	textHandler := slog.NewTextHandler(&shared.buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(&diagnosticsLogHandler{
		inner:  base.Handler(),
		text:   textHandler,
		shared: shared,
		diag:   diag,
	})
}

func (h *diagnosticsLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *diagnosticsLogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.shared.mu.Lock()
	h.shared.buf.Reset()
	_ = h.text.Handle(ctx, r)
	line := strings.TrimSpace(h.shared.buf.String())
	h.shared.mu.Unlock()

	ts := r.Time
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	h.diag.RecordLogLine(ts, line)
	return h.inner.Handle(ctx, r)
}

func (h *diagnosticsLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &diagnosticsLogHandler{
		inner:  h.inner.WithAttrs(attrs),
		text:   h.text.WithAttrs(attrs),
		shared: h.shared,
		diag:   h.diag,
	}
}

func (h *diagnosticsLogHandler) WithGroup(name string) slog.Handler {
	return &diagnosticsLogHandler{
		inner:  h.inner.WithGroup(name),
		text:   h.text.WithGroup(name),
		shared: h.shared,
		diag:   h.diag,
	}
}

func NewRemoteDiagnosticsCollector(stateDir string, startedAt time.Time) *RemoteDiagnosticsCollector {
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return &RemoteDiagnosticsCollector{
		startedAt: startedAt.UTC(),
		stateDir:  strings.TrimSpace(stateDir),
		logs:      make([]daemonLogEntry, 0, 128),
	}
}

func (c *RemoteDiagnosticsCollector) RecordLogLine(ts time.Time, line string) {
	if c == nil || strings.TrimSpace(line) == "" {
		return
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.logs) >= maxInMemoryLogLines {
		copy(c.logs, c.logs[1:])
		c.logs[len(c.logs)-1] = daemonLogEntry{ts: ts.UTC(), line: line}
		return
	}
	c.logs = append(c.logs, daemonLogEntry{ts: ts.UTC(), line: line})
}

func (c *RemoteDiagnosticsCollector) RecordCycleBusStats(txFrames, rxErrors int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.txFrames += max(txFrames, 0)
	c.rxErrors += max(rxErrors, 0)
	c.mu.Unlock()
}

func (c *RemoteDiagnosticsCollector) RecordMQTTStatus(rttMs float64, reconnected bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if rttMs >= 0 {
		c.lastRTTMs = rttMs
	}
	if reconnected {
		c.reconnect++
	}
	c.mu.Unlock()
}

func (c *RemoteDiagnosticsCollector) PullRedactedLogs(req edgecontrol.DiagnosticsDownlinkPayload, now time.Time) edgecontrol.DiagnosticsLogReplyPayload {
	ts := now.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	maxBytes, sinceMinutes := edgecontrol.ClampLogPullBounds(req.MaxBytes, req.SinceMinutes)
	cutoff := ts.Add(-time.Duration(sinceMinutes) * time.Minute)

	c.mu.Lock()
	var lines []string
	for _, entry := range c.logs {
		if !entry.ts.Before(cutoff) {
			lines = append(lines, entry.ts.Format(time.RFC3339)+" "+entry.line)
		}
	}
	c.mu.Unlock()

	if len(lines) == 0 && c != nil && c.stateDir != "" {
		logPath := c.stateDir + "/edge-gateway.log"
		if raw, err := os.ReadFile(logPath); err == nil && len(raw) > 0 {
			lines = append(lines, string(raw))
		}
	}

	joined := strings.Join(lines, "\n")
	if strings.TrimSpace(joined) == "" {
		joined = ts.Format(time.RFC3339) + " level=INFO msg=edge_gateway_diagnostics_ready"
	}
	clean, redactedCount, truncated := edgecontrol.RedactSensitiveLogText(joined, maxBytes)
	return edgecontrol.DiagnosticsLogReplyPayload{
		RequestID:     strings.TrimSpace(req.RequestID),
		Status:        "SUCCEEDED",
		Content:       clean,
		ReturnedBytes: len(clean),
		RedactedCount: redactedCount,
		Truncated:     truncated,
		CollectedAtMs: ts.UnixMilli(),
	}
}

func (c *RemoteDiagnosticsCollector) SampleHostMetrics(now time.Time, ring *WaveformRingBuffer) edgecontrol.GatewayHostMetricsPayload {
	ts := now.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	memPct := sampleMemoryUsedPercent()
	cpuPct := c.sampleProcessCPUPercent(ts)

	diskPct := 0.0
	if c != nil && c.stateDir != "" {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(c.stateDir, &stat); err == nil && stat.Blocks > 0 {
			used := stat.Blocks - stat.Bavail
			diskPct = float64(used) * 100.0 / float64(stat.Blocks)
			if diskPct > 100 {
				diskPct = 100
			}
		}
	}

	activePts, ringBytes := 0, int64(0)
	if ring != nil {
		activePts, ringBytes = ring.BufferStats()
	}

	c.mu.Lock()
	uptime := int64(ts.Sub(c.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}
	txFrames := c.txFrames
	rxErrors := c.rxErrors
	reconnects := c.reconnect
	rttMs := c.lastRTTMs
	c.mu.Unlock()

	errPPM := 0.0
	if txFrames > 0 {
		errPPM = float64(rxErrors) * 1_000_000.0 / float64(txFrames)
	}
	return edgecontrol.GatewayHostMetricsPayload{
		CollectedAtMs:      ts.UnixMilli(),
		CPUPercent:         cpuPct,
		MemoryUsedPercent:  roundTwoDecimals(memPct),
		DiskUsedPercent:    roundTwoDecimals(diskPct),
		UptimeSeconds:      uptime,
		RS485TxFrames:      txFrames,
		RS485RxErrors:      rxErrors,
		RS485ErrorRatePPM:  roundTwoDecimals(errPPM),
		MQTTRTTMs:          roundTwoDecimals(rttMs),
		MQTTTLSVersion:     "TLSv1.3",
		MQTTReconnectCount: reconnects,
		ActiveRingPoints:   activePts,
		RingBufferBytes:    ringBytes,
	}
}

func (c *RemoteDiagnosticsCollector) sampleProcessCPUPercent(now time.Time) float64 {
	if c == nil {
		return 0
	}
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	cpuTotal := timevalToDuration(ru.Utime) + timevalToDuration(ru.Stime)

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPrevCPU {
		c.hasPrevCPU = true
		c.prevCPUWall = now
		c.prevCPUTime = cpuTotal
		return 0
	}
	wallDelta := now.Sub(c.prevCPUWall)
	cpuDelta := cpuTotal - c.prevCPUTime
	c.prevCPUWall = now
	c.prevCPUTime = cpuTotal
	if wallDelta <= 0 || cpuDelta <= 0 {
		return 0
	}
	numCPU := runtime.NumCPU()
	if numCPU < 1 {
		numCPU = 1
	}
	pct := (cpuDelta.Seconds() / (wallDelta.Seconds() * float64(numCPU))) * 100.0
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	return roundTwoDecimals(pct)
}

func timevalToDuration(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}

func sampleMemoryUsedPercent() float64 {
	if runtime.GOOS == "linux" {
		if pct, ok := readLinuxProcMemoryPercent("/proc/self/statm", "/proc/meminfo", os.Getpagesize()); ok {
			return pct
		}
	}
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	if memStats.Sys == 0 {
		return 0
	}
	memPct := float64(memStats.HeapAlloc) * 100.0 / float64(memStats.Sys)
	if memPct > 100 {
		memPct = 100
	}
	return memPct
}

func readLinuxProcMemoryPercent(statmPath, meminfoPath string, pageSize int) (float64, bool) {
	if pageSize <= 0 {
		return 0, false
	}
	statmRaw, err := os.ReadFile(statmPath)
	if err != nil {
		return 0, false
	}
	statmFields := strings.Fields(string(statmRaw))
	if len(statmFields) < 2 {
		return 0, false
	}
	rssPages, err := strconv.ParseInt(statmFields[1], 10, 64)
	if err != nil || rssPages < 0 {
		return 0, false
	}

	meminfoRaw, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0, false
	}
	var totalKB int64
	for _, line := range strings.Split(string(meminfoRaw), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if parsed, convErr := strconv.ParseInt(fields[1], 10, 64); convErr == nil {
				totalKB = parsed
			}
		}
		break
	}
	if totalKB <= 0 {
		return 0, false
	}
	totalBytes := float64(totalKB) * 1024.0
	rssBytes := float64(rssPages) * float64(pageSize)
	pct := rssBytes * 100.0 / totalBytes
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	return pct, true
}

func roundTwoDecimals(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return float64(int64(v*100+0.5)) / 100
}
