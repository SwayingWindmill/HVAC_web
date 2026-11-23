package edgedaemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	commandSchemaVersion     = "2.0"
	commandWALRecordFence    = "fence"
	commandWALRecordEntry    = "record"
	commandLedgerMayExecute  = "MAY_EXECUTE"
	commandLedgerTerminal    = "TERMINAL"
	commandWALFileName       = "command-ledger.wal"
	commandWALMaxScanBytes   = 4 * 1024 * 1024
	commandMaxVerifyWindowMS = int64((10 * time.Minute) / time.Millisecond)
	commandMaxTTL            = 24 * time.Hour
)

// CommandDownlinkTopic returns the canonical MQTT topic for cloud commands targeting gatewayID.
func CommandDownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/down/command"
}

// CommandReplyUplinkTopic returns the canonical MQTT topic for command replies emitted by gatewayID.
func CommandReplyUplinkTopic(gatewayID string) string {
	return "hvac/v1/" + strings.TrimSpace(gatewayID) + "/up/reply"
}

// CommandEnvelopePolicy carries readback and verification policy attached to a cloud command.
type CommandEnvelopePolicy struct {
	RequiresReadback     bool  `json:"requiresReadback"`
	VerificationWindowMS int64 `json:"verificationWindowMs"`
}

// CommandEnvelopeWire mirrors modules/command/pkg/mqttconnector's v2.0 downlink command envelope.
type CommandEnvelopeWire struct {
	SchemaVersion   string                `json:"schemaVersion"`
	MessageID       string                `json:"messageId"`
	CommandID       string                `json:"commandId"`
	TraceID         string                `json:"traceId,omitempty"`
	IssuedAt        int64                 `json:"issuedAt"`
	ExpireAt        int64                 `json:"expireAt"`
	DeviceID        string                `json:"deviceId"`
	CommandCode     string                `json:"commandCode"`
	Params          map[string]float64    `json:"params"`
	Policy          CommandEnvelopePolicy `json:"policy"`
	ExecutionFence  uint64                `json:"executionFence"`
	PayloadHash     string                `json:"payloadHash"`
	PointCode       string                `json:"pointCode,omitempty"`
	ActuationMode   string                `json:"actuationMode,omitempty"`
	Channel         string                `json:"channel,omitempty"`
	CoilAddress     *int                  `json:"coilAddress,omitempty"`
	PulseDurationMS int                   `json:"pulseDurationMs,omitempty"`
}

// CommandReplyWire mirrors modules/command/pkg/mqttconnector's v2.0 command reply envelope.
type CommandReplyWire struct {
	SchemaVersion     string                              `json:"schemaVersion"`
	MessageID         string                              `json:"messageId"`
	CommandID         string                              `json:"commandId"`
	TraceID           string                              `json:"traceId,omitempty"`
	EventTime         int64                               `json:"eventTime"`
	EdgeStatus        string                              `json:"edgeStatus"`
	ExecutionEvidence *commandmodel.EdgeExecutionEvidence `json:"executionEvidence,omitempty"`
	ReasonCode        *string                             `json:"reasonCode"`
	ExecutionFence    uint64                              `json:"executionFence"`
}

type commandLedgerRecord struct {
	DeviceID       string            `json:"deviceId"`
	PayloadHash    string            `json:"payloadHash"`
	ExecutionFence uint64            `json:"executionFence"`
	State          string            `json:"state"`
	Reply          *CommandReplyWire `json:"reply,omitempty"`
}

type commandWALEntry struct {
	Type           string               `json:"type"`
	DeviceID       string               `json:"deviceId,omitempty"`
	ExecutionFence uint64               `json:"executionFence,omitempty"`
	CommandID      string               `json:"commandId,omitempty"`
	Record         *commandLedgerRecord `json:"record,omitempty"`
}

type pendingCommandEntry struct {
	commandID   string
	deviceID    string
	payloadHash string
	address     string
	requested   edgecontrol.Value
	keepLeased  bool
	expiresAt   time.Time
	base        CommandReplyWire
	done        chan CommandReplyWire
}

// CommandAdapter converts cloud MQTT command envelopes into leased Edge ControlIntents
// and emits schemaVersion 2.0 CommandReplyWire messages backed by a crash-safe WAL.
type CommandAdapter struct {
	gatewayID string
	host      *edgecontrol.Host
	now       func() time.Time
	logger    *slog.Logger

	walPath string
	walFile *os.File

	mu               sync.Mutex
	closed           bool
	closedCh         chan struct{}
	walEntries       int
	maxFenceByDevice map[string]uint64
	results          map[string]commandLedgerRecord
	lastIssuedAt     map[string]time.Time
	pendingByID      map[string]*pendingCommandEntry
	pendingByAddr    map[string]*pendingCommandEntry
	pendingSignal    chan struct{}
	seqFallback      uint64
}

// NewCommandAdapter initializes the persistent MQTT command adapter backed by stateDir/command-ledger.wal.
func NewCommandAdapter(
	gatewayID string,
	stateDir string,
	host *edgecontrol.Host,
	nowFn func() time.Time,
	logger *slog.Logger,
) (*CommandAdapter, error) {
	if strings.TrimSpace(gatewayID) == "" {
		return nil, errors.New("gatewayID is required")
	}
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("stateDir is required")
	}
	if host == nil {
		return nil, errors.New("edgecontrol host is required")
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return nil, fmt.Errorf("create command WAL directory: %w", err)
	}

	adapter := &CommandAdapter{
		gatewayID:        strings.TrimSpace(gatewayID),
		host:             host,
		now:              nowFn,
		logger:           logger,
		walPath:          filepath.Join(stateDir, commandWALFileName),
		closedCh:         make(chan struct{}),
		maxFenceByDevice: map[string]uint64{},
		results:          map[string]commandLedgerRecord{},
		lastIssuedAt:     map[string]time.Time{},
		pendingByID:      map[string]*pendingCommandEntry{},
		pendingByAddr:    map[string]*pendingCommandEntry{},
		pendingSignal:    make(chan struct{}, 1),
	}
	if err := adapter.loadAndOpenWAL(); err != nil {
		return nil, err
	}
	return adapter, nil
}

// PendingSignal returns a channel notified non-blockingly whenever a command intent
// is queued for the next control cycle.
func (a *CommandAdapter) PendingSignal() <-chan struct{} {
	return a.pendingSignal
}

// Close closes the command WAL file and wakes any blocked Handle callers.
func (a *CommandAdapter) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	close(a.closedCh)
	if a.walFile != nil {
		err := a.walFile.Close()
		a.walFile = nil
		return err
	}
	return nil
}

func (a *CommandAdapter) loadAndOpenWAL() error {
	raw, err := os.ReadFile(a.walPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read command WAL: %w", err)
	}
	if len(raw) > 0 {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 0, 64*1024), commandWALMaxScanBytes)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var entry commandWALEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return fmt.Errorf("decode command WAL entry: %w", err)
			}
			if err := a.applyWALEntryLocked(entry); err != nil {
				return err
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan command WAL: %w", err)
		}
	}

	file, err := os.OpenFile(a.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open command WAL: %w", err)
	}
	a.walFile = file
	return nil
}

func (a *CommandAdapter) applyWALEntryLocked(entry commandWALEntry) error {
	switch entry.Type {
	case commandWALRecordFence:
		deviceID := strings.TrimSpace(entry.DeviceID)
		if deviceID == "" || entry.ExecutionFence == 0 || entry.CommandID != "" || entry.Record != nil {
			return errors.New("command WAL contains an invalid fence entry")
		}
		if entry.ExecutionFence > a.maxFenceByDevice[deviceID] {
			a.maxFenceByDevice[deviceID] = entry.ExecutionFence
		}
		a.walEntries++
		return nil
	case commandWALRecordEntry:
		cmdID := strings.TrimSpace(entry.CommandID)
		if cmdID == "" || entry.Record == nil {
			return errors.New("command WAL contains an invalid record entry")
		}
		rec := cloneLedgerRecord(*entry.Record)
		if strings.TrimSpace(rec.DeviceID) == "" || rec.ExecutionFence == 0 || strings.TrimSpace(rec.PayloadHash) == "" {
			return errors.New("command WAL record is missing required correlation fields")
		}
		a.results[cmdID] = rec
		if rec.ExecutionFence > a.maxFenceByDevice[rec.DeviceID] {
			a.maxFenceByDevice[rec.DeviceID] = rec.ExecutionFence
		}
		a.walEntries++
		return nil
	default:
		return fmt.Errorf("unsupported command WAL entry type %q", entry.Type)
	}
}

// HandlePayload decodes a downlink command envelope, executes through Handle, and serializes the reply.
func (a *CommandAdapter) HandlePayload(ctx context.Context, payload []byte) ([]byte, bool) {
	reply, ok := a.Handle(ctx, payload)
	if !ok {
		return nil, false
	}
	wire, err := json.Marshal(reply)
	if err != nil {
		a.logger.Warn("edge_command_reply_marshal_failed", "command_id", reply.CommandID)
		return nil, false
	}
	return wire, true
}

// Handle decodes a v2.0 downlink command payload, enforces idempotency/fence/safety gates,
// registers a leased ControlIntent on Host, and waits for the next control cycle outcome.
func (a *CommandAdapter) Handle(ctx context.Context, payload []byte) (CommandReplyWire, bool) {
	if a == nil {
		return CommandReplyWire{}, false
	}
	env, ok := decodeStrictCommandEnvelope(payload)
	if !ok {
		a.logger.Warn("edge_command_envelope_invalid")
		return CommandReplyWire{}, false
	}
	if env.CommandID == "" {
		a.logger.Warn("edge_command_missing_id")
		return CommandReplyWire{}, false
	}

	now := a.now().UTC()
	base := a.newBaseReply(env, now)
	if !validCommandEnvelope(env) {
		return rejectedReply(base, "ENVELOPE_INVALID"), true
	}
	if env.Policy.VerificationWindowMS < 0 || env.Policy.VerificationWindowMS > commandMaxVerifyWindowMS {
		return rejectedReply(base, "POLICY_INVALID"), true
	}

	if existingReply, handled := a.checkExistingOrStale(env, base); handled {
		return existingReply, true
	}

	expiresAt := time.UnixMilli(env.ExpireAt).UTC()
	if !now.Before(expiresAt) {
		reply := expiredReply(base, "COMMAND_EXPIRED")
		return a.persistImmediateTerminal(env, reply), true
	}
	issuedAt := time.UnixMilli(env.IssuedAt).UTC()
	if expiresAt.Sub(issuedAt) > commandMaxTTL || expiresAt.Sub(now) > commandMaxTTL {
		reply := expiredReply(base, "TTL_EXCEEDS_MAX")
		return a.persistImmediateTerminal(env, reply), true
	}
	if _, protected := edgecontrol.ClassifyHardRealtimeProtection(env.PointCode, env.CommandCode, nil); protected {
		reply := rejectedReply(base, edgecontrol.ReasonHardRealtimeSafetyBoundary)
		return a.persistImmediateTerminal(env, reply), true
	}

	addr, requested, keepLeased, targetReason := a.resolveCommandTarget(now, env)
	if targetReason != "" {
		reply := rejectedReply(base, targetReason)
		return a.persistImmediateTerminal(env, reply), true
	}

	done, immediateReply, ok := a.enqueuePendingCommand(env, base, addr, requested, keepLeased, expiresAt)
	if !ok {
		return immediateReply, true
	}

	select {
	case reply := <-done:
		return reply, true
	case <-ctx.Done():
		a.abandonPendingOnCancel(env.CommandID, addr)
		return CommandReplyWire{}, false
	case <-a.closedCh:
		return CommandReplyWire{}, false
	}
}

func (a *CommandAdapter) checkExistingOrStale(env CommandEnvelopeWire, base CommandReplyWire) (CommandReplyWire, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, exists := a.results[env.CommandID]; exists {
		return replyForExistingRecord(existing, env, base), true
	}
	if env.ExecutionFence <= a.maxFenceByDevice[env.DeviceID] {
		return rejectedReply(base, "STALE_FENCE"), true
	}
	return CommandReplyWire{}, false
}

func replyForExistingRecord(existing commandLedgerRecord, env CommandEnvelopeWire, base CommandReplyWire) CommandReplyWire {
	if existing.PayloadHash != env.PayloadHash || existing.ExecutionFence != env.ExecutionFence || existing.DeviceID != env.DeviceID {
		return rejectedReply(base, "PAYLOAD_MISMATCH")
	}
	if existing.State == commandLedgerTerminal && existing.Reply != nil {
		return cloneReplyWire(*existing.Reply)
	}
	return failedReply(base, "EDGE_OUTCOME_UNKNOWN")
}

func (a *CommandAdapter) persistImmediateTerminal(env CommandEnvelopeWire, reply CommandReplyWire) CommandReplyWire {
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, exists := a.results[env.CommandID]; exists {
		return replyForExistingRecord(existing, env, reply)
	}
	if a.closed {
		return failedReply(reply, "EDGE_LEDGER_UNAVAILABLE")
	}
	if env.ExecutionFence <= a.maxFenceByDevice[env.DeviceID] {
		return rejectedReply(reply, "STALE_FENCE")
	}
	if err := a.persistTerminalLocked(env.CommandID, env.DeviceID, env.PayloadHash, reply); err != nil {
		return rejectedReply(reply, "EDGE_LEDGER_UNAVAILABLE")
	}
	return reply
}

func (a *CommandAdapter) enqueuePendingCommand(
	env CommandEnvelopeWire,
	base CommandReplyWire,
	addr string,
	requested edgecontrol.Value,
	keepLeased bool,
	expiresAt time.Time,
) (<-chan CommandReplyWire, CommandReplyWire, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if existing, exists := a.results[env.CommandID]; exists {
		return nil, replyForExistingRecord(existing, env, base), false
	}
	if a.closed {
		return nil, failedReply(base, "EDGE_LEDGER_UNAVAILABLE"), false
	}
	if env.ExecutionFence <= a.maxFenceByDevice[env.DeviceID] {
		return nil, rejectedReply(base, "STALE_FENCE"), false
	}
	if busy, exists := a.pendingByAddr[addr]; exists && busy.commandID != env.CommandID {
		return nil, rejectedReply(base, "COMMAND_BUSY"), false
	}

	issuedAt := time.UnixMilli(env.IssuedAt).UTC()
	if last, hasLast := a.lastIssuedAt[addr]; hasLast && !issuedAt.After(last) {
		issuedAt = last.Add(time.Nanosecond)
	}
	if !expiresAt.After(issuedAt) {
		reply := expiredReply(base, "COMMAND_EXPIRED")
		if err := a.persistTerminalLocked(env.CommandID, env.DeviceID, env.PayloadHash, reply); err != nil {
			return nil, rejectedReply(base, "EDGE_LEDGER_UNAVAILABLE"), false
		}
		return nil, reply, false
	}

	mayRecord := commandLedgerRecord{
		DeviceID:       env.DeviceID,
		PayloadHash:    env.PayloadHash,
		ExecutionFence: env.ExecutionFence,
		State:          commandLedgerMayExecute,
	}
	if err := a.appendWALEntryLocked(commandWALEntry{
		Type:      commandWALRecordEntry,
		CommandID: env.CommandID,
		Record:    &mayRecord,
	}); err != nil {
		return nil, rejectedReply(base, "EDGE_LEDGER_UNAVAILABLE"), false
	}
	a.results[env.CommandID] = mayRecord
	if env.ExecutionFence > a.maxFenceByDevice[env.DeviceID] {
		a.maxFenceByDevice[env.DeviceID] = env.ExecutionFence
	}

	if _, err := a.host.IntentStore().Put(edgecontrol.ControlIntent{
		ID:        env.CommandID,
		Address:   addr,
		Requested: requested,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
		Source:    "CLOUD_COMMAND",
	}); err != nil {
		reason := "CONTROL_INTENT_REJECTED"
		if errors.Is(err, edgecontrol.ErrHardRealtimeSafetyBoundary) {
			reason = edgecontrol.ReasonHardRealtimeSafetyBoundary
		}
		reply := rejectedReply(base, reason)
		_ = a.persistTerminalLocked(env.CommandID, env.DeviceID, env.PayloadHash, reply)
		return nil, reply, false
	}
	a.lastIssuedAt[addr] = issuedAt

	done := make(chan CommandReplyWire, 1)
	entry := &pendingCommandEntry{
		commandID:   env.CommandID,
		deviceID:    env.DeviceID,
		payloadHash: env.PayloadHash,
		address:     addr,
		requested:   requested,
		keepLeased:  keepLeased,
		expiresAt:   expiresAt,
		base:        base,
		done:        done,
	}
	a.pendingByID[env.CommandID] = entry
	a.pendingByAddr[addr] = entry
	select {
	case a.pendingSignal <- struct{}{}:
	default:
	}
	return done, CommandReplyWire{}, true
}

func (a *CommandAdapter) abandonPendingOnCancel(commandID, addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, stillPending := a.pendingByID[commandID]; !stillPending {
		return
	}
	delete(a.pendingByID, commandID)
	if active, ok := a.pendingByAddr[addr]; ok && active.commandID == commandID {
		delete(a.pendingByAddr, addr)
	}
	if store := a.host.IntentStore(); store != nil {
		_ = store.Revoke(commandID)
	}
}

// ResolveCycle reconciles accepted/rejected cycle decisions and immediate local write
// readback results into terminal CommandReplyWire outcomes for pending commands.
func (a *CommandAdapter) ResolveCycle(now time.Time, cycle edgecontrol.CycleResult, writeResults []edgecontrol.DeviceWriteResult) {
	if a == nil {
		return
	}
	if now.IsZero() {
		now = a.now().UTC()
	} else {
		now = now.UTC()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pendingByID) == 0 {
		return
	}

	cycleNum := cycle.Image.Cycle()
	if cycleNum == 0 {
		a.seqFallback++
		cycleNum = a.seqFallback
	} else if cycleNum > a.seqFallback {
		a.seqFallback = cycleNum
	}

	decisionsByAddr := make(map[string]edgecontrol.Decision, len(cycle.Decisions))
	for _, decision := range cycle.Decisions {
		decisionsByAddr[decision.Address] = decision
	}
	writesByAddr := make(map[string]edgecontrol.DeviceWriteResult, len(writeResults))
	for _, wr := range writeResults {
		writesByAddr[wr.Address] = wr
	}

	ids := make([]string, 0, len(a.pendingByID))
	for id := range a.pendingByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		entry := a.pendingByID[id]
		if !now.Before(entry.expiresAt) && !hasWriteResult(writesByAddr, entry.address) {
			reply := expiredReply(entry.base, "COMMAND_EXPIRED")
			reply.EventTime = now.UnixMilli()
			a.completePendingLocked(entry, reply, true)
			continue
		}
		decision, hasDecision := decisionsByAddr[entry.address]
		if !hasDecision {
			continue
		}

		reply, revoke, ready := buildCycleReply(now, cycle, cycleNum, entry, decision, writesByAddr[entry.address], hasWriteResult(writesByAddr, entry.address))
		if !ready {
			continue
		}
		a.completePendingLocked(entry, reply, revoke)
	}
}

func hasWriteResult(m map[string]edgecontrol.DeviceWriteResult, addr string) bool {
	_, ok := m[addr]
	return ok
}

func buildCycleReply(
	now time.Time,
	cycle edgecontrol.CycleResult,
	cycleNum uint64,
	entry *pendingCommandEntry,
	decision edgecontrol.Decision,
	wr edgecontrol.DeviceWriteResult,
	hasWrite bool,
) (CommandReplyWire, bool, bool) {
	reply := entry.base
	reply.EventTime = now.UnixMilli()

	winner := strings.TrimSpace(decision.ControllerID)
	if winner == "" && len(decision.ConstraintReasons) > 0 {
		winner = strings.TrimSpace(decision.ConstraintReasons[0].ControllerID)
	}
	if winner == "" {
		winner = "edge-control-arbiter"
	}

	leaseExpMs := entry.expiresAt.UnixMilli()
	evidence := &commandmodel.EdgeExecutionEvidence{
		Requested:          scalarFromEdgeValue(entry.requested),
		WinnerControllerID: winner,
		Cycle:              cycleNum,
		LeaseExpiresAt:     &leaseExpMs,
	}
	if decision.Effective != nil {
		eff := scalarFromEdgeValue(*decision.Effective)
		evidence.Effective = &eff
	}
	for _, c := range decision.ConstraintReasons {
		evidence.Constraints = append(evidence.Constraints, commandmodel.EdgeConstraintEvidence{
			ControllerID: c.ControllerID,
			Reason:       c.Reason,
		})
	}
	if evidence.Valid() {
		reply.ExecutionEvidence = evidence
	}

	if !decision.Accepted {
		reason := "CONTROL_DENIED"
		if len(decision.ConstraintReasons) > 0 && strings.TrimSpace(decision.ConstraintReasons[0].Reason) != "" {
			reason = strings.TrimSpace(decision.ConstraintReasons[0].Reason)
		}
		reply.EdgeStatus = "REJECTED"
		reply.ReasonCode = &reason
		return reply, true, true
	}

	if !hasWrite {
		reason := "DEVICE_WRITE_RESULT_MISSING"
		if cycle.OutputError != nil {
			reason = "DEVICE_WRITE_FAILED"
		}
		reply.EdgeStatus = "FAILED"
		reply.ReasonCode = &reason
		return reply, true, true
	}

	if wr.Success && wr.Code == "IN_PROGRESS" {
		return CommandReplyWire{}, false, false
	}

	if wr.AppliedValue != nil {
		applied := scalarFromEdgeValue(*wr.AppliedValue)
		evidence.Applied = &applied
	}
	if wr.ReadbackValue != nil {
		rb := scalarFromEdgeValue(*wr.ReadbackValue)
		evidence.Readback = &rb
	}
	if wr.ReadbackVerified || !wr.ReadbackAt.IsZero() || strings.HasPrefix(wr.Code, "LOCAL_READBACK_") {
		verified := wr.ReadbackVerified
		evidence.LocalReadbackVerified = &verified
	}
	if !wr.ReadbackAt.IsZero() {
		rbAtMs := wr.ReadbackAt.UTC().UnixMilli()
		evidence.LocalReadbackAt = &rbAtMs
	}
	if evidence.Valid() {
		reply.ExecutionEvidence = evidence
	} else {
		reply.ExecutionEvidence = nil
	}

	if !wr.Success {
		status := "FAILED"
		if wr.Code == edgecontrol.ReasonHardRealtimeSafetyBoundary || wr.Code == "CHANNEL_READ_ONLY" {
			status = "REJECTED"
		}
		reason := strings.TrimSpace(wr.Code)
		if reason == "" {
			reason = "DEVICE_WRITE_FAILED"
		}
		reply.EdgeStatus = status
		reply.ReasonCode = &reason
		return reply, true, true
	}

	reply.EdgeStatus = "EXECUTED"
	reply.ReasonCode = nil
	return reply, !entry.keepLeased, true
}

func (a *CommandAdapter) completePendingLocked(entry *pendingCommandEntry, reply CommandReplyWire, revoke bool) {
	delete(a.pendingByID, entry.commandID)
	if active, ok := a.pendingByAddr[entry.address]; ok && active.commandID == entry.commandID {
		delete(a.pendingByAddr, entry.address)
	}
	if revoke {
		if store := a.host.IntentStore(); store != nil {
			_ = store.Revoke(entry.commandID)
		}
	}
	if err := a.persistTerminalLocked(entry.commandID, entry.deviceID, entry.payloadHash, reply); err != nil {
		reply = failedReply(reply, "EDGE_LEDGER_UNAVAILABLE")
	}
	select {
	case entry.done <- reply:
	default:
	}
}

func (a *CommandAdapter) persistTerminalLocked(commandID, deviceID, payloadHash string, reply CommandReplyWire) error {
	replyCopy := cloneReplyWire(reply)
	rec := commandLedgerRecord{
		DeviceID:       deviceID,
		PayloadHash:    payloadHash,
		ExecutionFence: reply.ExecutionFence,
		State:          commandLedgerTerminal,
		Reply:          &replyCopy,
	}
	if err := a.appendWALEntryLocked(commandWALEntry{
		Type:      commandWALRecordEntry,
		CommandID: commandID,
		Record:    &rec,
	}); err != nil {
		return err
	}
	a.results[commandID] = rec
	if reply.ExecutionFence > a.maxFenceByDevice[deviceID] {
		a.maxFenceByDevice[deviceID] = reply.ExecutionFence
	}
	if a.walEntries > len(a.results)*2 && a.walEntries > 200 {
		_ = a.compactWALLocked()
	}
	return nil
}

func (a *CommandAdapter) appendWALEntryLocked(entry commandWALEntry) error {
	if a.walFile == nil {
		return errors.New("command WAL is closed")
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if _, err := a.walFile.Write(raw); err != nil {
		return err
	}
	if err := a.walFile.Sync(); err != nil {
		return err
	}
	a.walEntries++
	return nil
}

func (a *CommandAdapter) compactWALLocked() error {
	tmpPath := a.walPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	count := 0
	cleanup := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return cause
	}
	devices := make([]string, 0, len(a.maxFenceByDevice))
	for deviceID := range a.maxFenceByDevice {
		devices = append(devices, deviceID)
	}
	sort.Strings(devices)
	for _, deviceID := range devices {
		fence := a.maxFenceByDevice[deviceID]
		if fence == 0 {
			continue
		}
		line, err := json.Marshal(commandWALEntry{Type: commandWALRecordFence, DeviceID: deviceID, ExecutionFence: fence})
		if err != nil {
			return cleanup(err)
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			return cleanup(err)
		}
		count++
	}
	cmdIDs := make([]string, 0, len(a.results))
	for cmdID := range a.results {
		cmdIDs = append(cmdIDs, cmdID)
	}
	sort.Strings(cmdIDs)
	for _, cmdID := range cmdIDs {
		rec := cloneLedgerRecord(a.results[cmdID])
		line, err := json.Marshal(commandWALEntry{Type: commandWALRecordEntry, CommandID: cmdID, Record: &rec})
		if err != nil {
			return cleanup(err)
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			return cleanup(err)
		}
		count++
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, a.walPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if a.walFile != nil {
		_ = a.walFile.Close()
	}
	reopened, err := os.OpenFile(a.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	a.walFile = reopened
	a.walEntries = count
	return nil
}

func (a *CommandAdapter) resolveCommandTarget(now time.Time, env CommandEnvelopeWire) (string, edgecontrol.Value, bool, string) {
	manifest, err := a.host.Manifest(a.gatewayID, "runtime", now)
	if err != nil {
		return "", edgecontrol.Value{}, false, "MANIFEST_UNAVAILABLE"
	}
	var comp *edgecontrol.ManifestComponent
	for i := range manifest.Components {
		if manifest.Components[i].ID == env.DeviceID && manifest.Components[i].Enabled {
			comp = &manifest.Components[i]
			break
		}
	}
	if comp == nil {
		return "", edgecontrol.Value{}, false, "DEVICE_NOT_FOUND"
	}

	var addr string
	var paramKey string
	var isAction bool
	if env.PointCode != "" {
		addr = env.DeviceID + "/" + env.PointCode
	} else {
		var semantic edgecontrol.SemanticChannel
		var ok bool
		semantic, paramKey, isAction, ok = semanticTargetForCommand(env.CommandCode)
		if !ok {
			return "", edgecontrol.Value{}, false, "COMMAND_UNSUPPORTED"
		}
		addr = comp.ChannelBindings[semantic]
		if addr == "" {
			return "", edgecontrol.Value{}, false, "COMMAND_UNSUPPORTED"
		}
	}

	desc, ok := a.host.ChannelDescriptor(addr)
	if !ok {
		return "", edgecontrol.Value{}, false, "COMMAND_CHANNEL_INVALID"
	}
	if edgecontrol.IsHardRealtimeChannel(desc) {
		return "", edgecontrol.Value{}, false, edgecontrol.ReasonHardRealtimeSafetyBoundary
	}
	if desc.Access != edgecontrol.AccessReadWrite && desc.Access != edgecontrol.AccessWriteOnly {
		return "", edgecontrol.Value{}, false, "CHANNEL_READ_ONLY"
	}

	requested, keepLeased, reason := buildRequestedValue(desc, paramKey, isAction, env.Params)
	if reason != "" {
		return "", edgecontrol.Value{}, false, reason
	}
	return addr, requested, keepLeased, ""
}

func semanticTargetForCommand(commandCode string) (edgecontrol.SemanticChannel, string, bool, bool) {
	switch commandCode {
	case "START":
		return edgecontrol.SemanticStartCommand, "", true, true
	case "STOP":
		return edgecontrol.SemanticStopCommand, "", true, true
	case "RESET_FAULT":
		return edgecontrol.SemanticResetFaultCommand, "", true, true
	case "SET_FREQUENCY":
		return edgecontrol.SemanticFrequencySetpoint, "frequencyHz", false, true
	case "SET_CHILLED_WATER_TEMPERATURE_SETPOINT":
		return edgecontrol.SemanticTemperatureSetpoint, "setpointC", false, true
	case "SET_LOAD_LIMIT":
		return edgecontrol.SemanticLoadLimit, "loadLimitPct", false, true
	case "SET_FAN_SPEED":
		return edgecontrol.SemanticFanSpeedSetpoint, "fanSpeedPct", false, true
	default:
		return "", "", false, false
	}
}

func buildRequestedValue(desc edgecontrol.ChannelDescriptor, paramKey string, isAction bool, params map[string]float64) (edgecontrol.Value, bool, string) {
	if desc.DataType == edgecontrol.DataTypeBoolean {
		if len(params) != 0 {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		return edgecontrol.BooleanValue(true), false, ""
	}
	if isAction {
		return edgecontrol.Value{}, false, "COMMAND_CHANNEL_TYPE_MISMATCH"
	}
	var rawNum float64
	if paramKey != "" {
		if len(params) != 1 {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		v, exists := params[paramKey]
		if !exists {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		rawNum = v
	} else {
		if len(params) != 1 {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		for _, v := range params {
			rawNum = v
		}
	}
	if math.IsNaN(rawNum) || math.IsInf(rawNum, 0) {
		return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
	}

	switch desc.DataType {
	case edgecontrol.DataTypeDouble:
		return edgecontrol.DoubleValue(rawNum), true, ""
	case edgecontrol.DataTypeFloat:
		return edgecontrol.FloatValue(float32(rawNum)), true, ""
	case edgecontrol.DataTypeInteger:
		if rawNum < math.MinInt32 || rawNum > math.MaxInt32 || math.Trunc(rawNum) != rawNum {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		return edgecontrol.IntegerValue(int32(rawNum)), true, ""
	case edgecontrol.DataTypeLong:
		if rawNum < math.MinInt64 || rawNum > math.MaxInt64 || math.Trunc(rawNum) != rawNum {
			return edgecontrol.Value{}, false, "INVALID_PARAMETERS"
		}
		return edgecontrol.LongValue(int64(rawNum)), true, ""
	default:
		return edgecontrol.Value{}, false, "COMMAND_CHANNEL_TYPE_MISMATCH"
	}
}

func decodeStrictCommandEnvelope(payload []byte) (CommandEnvelopeWire, bool) {
	var env CommandEnvelopeWire
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return CommandEnvelopeWire{}, false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return CommandEnvelopeWire{}, false
	}
	env.SchemaVersion = strings.TrimSpace(env.SchemaVersion)
	env.MessageID = strings.TrimSpace(env.MessageID)
	env.CommandID = strings.TrimSpace(env.CommandID)
	env.TraceID = strings.TrimSpace(env.TraceID)
	env.DeviceID = strings.TrimSpace(env.DeviceID)
	env.CommandCode = strings.TrimSpace(env.CommandCode)
	env.PayloadHash = strings.TrimSpace(env.PayloadHash)
	env.PointCode = strings.TrimSpace(env.PointCode)
	env.ActuationMode = strings.TrimSpace(env.ActuationMode)
	env.Channel = strings.TrimSpace(env.Channel)
	return env, true
}

func validCommandEnvelope(env CommandEnvelopeWire) bool {
	return env.SchemaVersion == commandSchemaVersion &&
		env.MessageID != "" &&
		env.CommandID != "" &&
		env.DeviceID != "" &&
		env.CommandCode != "" &&
		env.PayloadHash != "" &&
		env.ExecutionFence > 0 &&
		env.IssuedAt > 0 &&
		env.ExpireAt > env.IssuedAt
}

func (a *CommandAdapter) newBaseReply(env CommandEnvelopeWire, now time.Time) CommandReplyWire {
	msgID, err := edgecontrol.NewReplicationUUIDv7(now)
	if err != nil {
		msgID = "reply-" + env.CommandID
	}
	return CommandReplyWire{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      msgID,
		CommandID:      env.CommandID,
		TraceID:        env.TraceID,
		EventTime:      now.UnixMilli(),
		ExecutionFence: env.ExecutionFence,
	}
}

func rejectedReply(base CommandReplyWire, reason string) CommandReplyWire {
	base.EdgeStatus = "REJECTED"
	r := reason
	base.ReasonCode = &r
	return base
}

func failedReply(base CommandReplyWire, reason string) CommandReplyWire {
	base.EdgeStatus = "FAILED"
	r := reason
	base.ReasonCode = &r
	return base
}

func expiredReply(base CommandReplyWire, reason string) CommandReplyWire {
	base.EdgeStatus = "EXPIRED"
	r := reason
	base.ReasonCode = &r
	return base
}

func scalarFromEdgeValue(value edgecontrol.Value) commandmodel.ScalarValue {
	if num, ok := value.NumericFloat64(); ok {
		return commandmodel.NumberScalar(num)
	}
	switch value.Type {
	case edgecontrol.DataTypeBoolean:
		return commandmodel.BooleanScalar(value.Boolean)
	case edgecontrol.DataTypeString:
		return commandmodel.TextScalar(value.String)
	default:
		return commandmodel.TextScalar("")
	}
}

func cloneLedgerRecord(rec commandLedgerRecord) commandLedgerRecord {
	if rec.Reply != nil {
		r := cloneReplyWire(*rec.Reply)
		rec.Reply = &r
	}
	return rec
}

func cloneReplyWire(reply CommandReplyWire) CommandReplyWire {
	if reply.ReasonCode != nil {
		r := *reply.ReasonCode
		reply.ReasonCode = &r
	}
	if reply.ExecutionEvidence != nil {
		ev := *reply.ExecutionEvidence
		if ev.Effective != nil {
			v := cloneScalarValue(*ev.Effective)
			ev.Effective = &v
		}
		if ev.Applied != nil {
			v := cloneScalarValue(*ev.Applied)
			ev.Applied = &v
		}
		if ev.Readback != nil {
			v := cloneScalarValue(*ev.Readback)
			ev.Readback = &v
		}
		if ev.LocalReadbackVerified != nil {
			v := *ev.LocalReadbackVerified
			ev.LocalReadbackVerified = &v
		}
		if ev.LocalReadbackAt != nil {
			v := *ev.LocalReadbackAt
			ev.LocalReadbackAt = &v
		}
		if ev.LeaseExpiresAt != nil {
			v := *ev.LeaseExpiresAt
			ev.LeaseExpiresAt = &v
		}
		ev.Requested = cloneScalarValue(ev.Requested)
		ev.Constraints = append([]commandmodel.EdgeConstraintEvidence(nil), ev.Constraints...)
		reply.ExecutionEvidence = &ev
	}
	return reply
}

func cloneScalarValue(v commandmodel.ScalarValue) commandmodel.ScalarValue {
	if v.Number != nil {
		n := *v.Number
		v.Number = &n
	}
	if v.Text != nil {
		s := *v.Text
		v.Text = &s
	}
	if v.Boolean != nil {
		b := *v.Boolean
		v.Boolean = &b
	}
	return v
}
