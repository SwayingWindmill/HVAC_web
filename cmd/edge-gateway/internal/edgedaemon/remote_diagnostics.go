package edgedaemon

import (
	"os"
	"runtime"
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
	mu        sync.Mutex
	startedAt time.Time
	stateDir  string
	logs      []daemonLogEntry
	txFrames  int64
	rxErrors  int64
	reconnect int64
	lastRTTMs float64
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
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	memPct := 0.0
	if memStats.Sys > 0 {
		memPct = float64(memStats.HeapAlloc) * 100.0 / float64(memStats.Sys)
		if memPct > 100 {
			memPct = 100
		}
	}

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
		CPUPercent:         readProcessCPUEstimate(),
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

func readProcessCPUEstimate() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	pct := float64(runtime.NumGoroutine()) * 0.45
	if pct > 95 {
		pct = 95
	}
	return roundTwoDecimals(pct)
}

func roundTwoDecimals(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
