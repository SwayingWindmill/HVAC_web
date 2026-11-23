package simulator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

const (
	mqttCommandSchemaVersion = "2.0"
	mqttCommandMaxTTL        = 24 * time.Hour
)

type mqttCommandPolicy struct {
	RequiresReadback     bool  `json:"requiresReadback"`
	VerificationWindowMS int64 `json:"verificationWindowMs"`
}

type mqttCommandEnvelope struct {
	SchemaVersion   string             `json:"schemaVersion"`
	MessageID       string             `json:"messageId"`
	CommandID       string             `json:"commandId"`
	TraceID         string             `json:"traceId,omitempty"`
	IssuedAt        int64              `json:"issuedAt"`
	ExpireAt        int64              `json:"expireAt"`
	DeviceID        string             `json:"deviceId"`
	CommandCode     string             `json:"commandCode"`
	Params          map[string]float64 `json:"params"`
	Policy          mqttCommandPolicy  `json:"policy"`
	ExecutionFence  uint64             `json:"executionFence"`
	PayloadHash     string             `json:"payloadHash"`
	PointCode       string             `json:"pointCode,omitempty"`
	ActuationMode   string             `json:"actuationMode,omitempty"`
	Channel         string             `json:"channel,omitempty"`
	CoilAddress     *int               `json:"coilAddress,omitempty"`
	PulseDurationMs *int               `json:"pulseDurationMs,omitempty"`
}

type mqttCommandReply struct {
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

const (
	edgeCommandMayExecute             = "MAY_EXECUTE"
	edgeCommandTerminal               = "TERMINAL"
	edgeCommandWALRecordTypeFence     = "fence"
	edgeCommandWALRecordTypeRecord    = "record"
	maxRetainedTerminalCommandRecords = 1024
	edgeCommandWALCompactThreshold    = 2048
)

type edgeCommandRecord struct {
	DeviceID       string            `json:"deviceId"`
	PayloadHash    string            `json:"payloadHash"`
	ExecutionFence uint64            `json:"executionFence"`
	State          string            `json:"state"`
	Reply          *mqttCommandReply `json:"reply,omitempty"`
}

type edgeCommandLedger struct {
	Records map[string]edgeCommandRecord `json:"records"`
}

type edgeCommandWALEntry struct {
	Type           string             `json:"type"`
	DeviceID       string             `json:"deviceId,omitempty"`
	ExecutionFence uint64             `json:"executionFence,omitempty"`
	CommandID      string             `json:"commandId,omitempty"`
	Record         *edgeCommandRecord `json:"record,omitempty"`
}

type seqCommand struct {
	commandID string
	seq       uint64
}

type edgeCommandHandler struct {
	edgeRuntime    *EdgeControlRuntime
	deviceByWireID map[string]string
	replyTopic     string
	now            func() time.Time
	ledgerPath     string
	walPath        string
	spool          *mqttEvidenceSpool

	mu               sync.Mutex
	results          map[string]edgeCommandRecord
	recordSeq        map[string]uint64
	terminalQueue    []seqCommand
	terminalCount    int
	nextSeq          uint64
	walLines         int
	compactThreshold int
	maxFenceByDevice map[string]uint64
}

// newEdgeCommandHandler accepts commands for the plant's devices, which commands name by
// the same source keys the Gateway uses on the wire.
func newEdgeCommandHandler(edgeRuntime *EdgeControlRuntime, config MQTTGatewayConfig, deviceIDs []string, spool *mqttEvidenceSpool) (*edgeCommandHandler, error) {
	if edgeRuntime == nil || spool == nil {
		return nil, errors.New("MQTT command handler requires Edge Control Runtime and evidence spool")
	}
	deviceByWireID := make(map[string]string, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		deviceByWireID[deviceID] = deviceID
	}
	ledgerPath := filepath.Join(config.QueueDirectory, "command-execution-records.json")
	walPath := filepath.Join(config.QueueDirectory, "command-execution-records.wal")
	handler := &edgeCommandHandler{
		edgeRuntime:      edgeRuntime,
		deviceByWireID:   deviceByWireID,
		replyTopic:       mqttReplyTopic(config),
		now:              time.Now,
		ledgerPath:       ledgerPath,
		walPath:          walPath,
		spool:            spool,
		results:          make(map[string]edgeCommandRecord),
		recordSeq:        make(map[string]uint64),
		compactThreshold: edgeCommandWALCompactThreshold,
		maxFenceByDevice: make(map[string]uint64),
	}
	if err := handler.loadEdgeCommandLedger(); err != nil {
		return nil, err
	}
	return handler, nil
}

func validateEdgeCommandRecord(record edgeCommandRecord) error {
	if strings.TrimSpace(record.DeviceID) == "" || record.ExecutionFence == 0 ||
		(record.State != edgeCommandMayExecute && record.State != edgeCommandTerminal) {
		return errors.New("edge command execution ledger contains an invalid record")
	}
	if record.State == edgeCommandTerminal && record.Reply == nil {
		return errors.New("edge command execution ledger terminal record has no reply")
	}
	if record.State == edgeCommandMayExecute && record.Reply != nil {
		return errors.New("edge command execution ledger non-terminal record must not have a reply")
	}
	return nil
}

func (handler *edgeCommandHandler) loadEdgeCommandLedger() error {
	_, statErr := os.Stat(handler.walPath)
	if statErr == nil {
		return handler.replayEdgeCommandWAL()
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat Edge command WAL: %w", statErr)
	}
	legacyRecords, err := loadLegacyEdgeCommandLedger(handler.ledgerPath)
	if err != nil {
		return err
	}
	if len(legacyRecords) == 0 {
		return nil
	}
	commandIDs := make([]string, 0, len(legacyRecords))
	for commandID, record := range legacyRecords {
		if strings.TrimSpace(commandID) == "" {
			return errors.New("edge command execution ledger contains an empty commandId")
		}
		if err := validateEdgeCommandRecord(record); err != nil {
			return err
		}
		commandIDs = append(commandIDs, commandID)
	}
	sort.Strings(commandIDs)
	for _, commandID := range commandIDs {
		handler.recordEntryLocked(commandID, legacyRecords[commandID])
	}
	return handler.compactWALLocked()
}

func (handler *edgeCommandHandler) replayEdgeCommandWAL() error {
	file, err := os.Open(handler.walPath)
	if err != nil {
		return fmt.Errorf("open Edge command WAL: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	lineCount := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry edgeCommandWALEntry
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			return fmt.Errorf("edge command WAL is corrupt: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return errors.New("edge command WAL line contains trailing data")
		}
		switch entry.Type {
		case edgeCommandWALRecordTypeFence:
			if strings.TrimSpace(entry.DeviceID) == "" || entry.ExecutionFence == 0 || entry.CommandID != "" || entry.Record != nil {
				return errors.New("edge command WAL contains an invalid fence entry")
			}
			if entry.ExecutionFence > handler.maxFenceByDevice[entry.DeviceID] {
				handler.maxFenceByDevice[entry.DeviceID] = entry.ExecutionFence
			}
		case edgeCommandWALRecordTypeRecord:
			if strings.TrimSpace(entry.CommandID) == "" || entry.Record == nil || entry.DeviceID != "" || entry.ExecutionFence != 0 {
				return errors.New("edge command WAL contains an invalid record entry")
			}
			if err := validateEdgeCommandRecord(*entry.Record); err != nil {
				return err
			}
			handler.recordEntryLocked(entry.CommandID, *entry.Record)
		default:
			return errors.New("edge command WAL contains an unknown entry type")
		}
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Edge command WAL: %w", err)
	}
	handler.walLines = lineCount
	if handler.walLines >= handler.compactThreshold {
		handler.compactThreshold = handler.walLines + edgeCommandWALCompactThreshold
	}
	return nil
}

func loadLegacyEdgeCommandLedger(path string) (map[string]edgeCommandRecord, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]edgeCommandRecord), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Edge command execution ledger: %w", err)
	}
	var ledger edgeCommandLedger
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil || ledger.Records == nil {
		return nil, errors.New("edge command execution ledger is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("edge command execution ledger contains trailing data")
	}
	return ledger.Records, nil
}

func persistEdgeCommandLedger(path string, records map[string]edgeCommandRecord) error {
	raw, err := json.Marshal(edgeCommandLedger{Records: records})
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return fmt.Errorf("write Edge command execution ledger: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("commit Edge command execution ledger: %w", err)
	}
	return nil
}

func appendEdgeCommandWALEntry(walPath string, entry edgeCommandWALEntry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	file, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open Edge command WAL: %w", err)
	}
	_, writeErr := file.Write(line)
	syncErr := writeErr
	if syncErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("append Edge command WAL: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync Edge command WAL: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Edge command WAL: %w", closeErr)
	}
	return nil
}

func (handler *edgeCommandHandler) recordEntryLocked(commandID string, record edgeCommandRecord) {
	prev, hadPrev := handler.results[commandID]
	if hadPrev && prev.State == edgeCommandTerminal {
		handler.terminalCount -= 1
	}
	handler.results[commandID] = record
	handler.nextSeq++
	handler.recordSeq[commandID] = handler.nextSeq
	if record.ExecutionFence > handler.maxFenceByDevice[record.DeviceID] {
		handler.maxFenceByDevice[record.DeviceID] = record.ExecutionFence
	}
	if record.State == edgeCommandTerminal {
		handler.terminalCount++
		handler.terminalQueue = append(handler.terminalQueue, seqCommand{commandID: commandID, seq: handler.nextSeq})
		handler.pruneTerminalRecordsLocked()
	}
}

func (handler *edgeCommandHandler) pruneTerminalRecordsLocked() {
	for handler.terminalCount > maxRetainedTerminalCommandRecords && len(handler.terminalQueue) > 0 {
		oldest := handler.terminalQueue[0]
		handler.terminalQueue = handler.terminalQueue[1:]
		cur, ok := handler.results[oldest.commandID]
		if ok && cur.State == edgeCommandTerminal && handler.recordSeq[oldest.commandID] == oldest.seq {
			delete(handler.results, oldest.commandID)
			delete(handler.recordSeq, oldest.commandID)
			handler.terminalCount -= 1
		}
	}
	if cap(handler.terminalQueue) > 2*maxRetainedTerminalCommandRecords && len(handler.terminalQueue) <= maxRetainedTerminalCommandRecords {
		compacted := make([]seqCommand, len(handler.terminalQueue))
		copy(compacted, handler.terminalQueue)
		handler.terminalQueue = compacted
	}
}

func (handler *edgeCommandHandler) appendRecordLocked(commandID string, record edgeCommandRecord) error {
	if err := appendEdgeCommandWALEntry(handler.walPath, edgeCommandWALEntry{
		Type:      edgeCommandWALRecordTypeRecord,
		CommandID: commandID,
		Record:    &record,
	}); err != nil {
		return err
	}
	handler.recordEntryLocked(commandID, record)
	handler.walLines++
	if handler.walLines >= handler.compactThreshold {
		_ = handler.compactWALLocked()
	}
	return nil
}

func (handler *edgeCommandHandler) compactWALLocked() error {
	deviceIDs := make([]string, 0, len(handler.maxFenceByDevice))
	for deviceID, fence := range handler.maxFenceByDevice {
		if strings.TrimSpace(deviceID) != "" && fence > 0 {
			deviceIDs = append(deviceIDs, deviceID)
		}
	}
	sort.Strings(deviceIDs)

	commandIDs := make([]string, 0, len(handler.results))
	for commandID := range handler.results {
		commandIDs = append(commandIDs, commandID)
	}
	sort.Slice(commandIDs, func(i, j int) bool {
		seqI, seqJ := handler.recordSeq[commandIDs[i]], handler.recordSeq[commandIDs[j]]
		if seqI != seqJ {
			return seqI < seqJ
		}
		return commandIDs[i] < commandIDs[j]
	})

	var buf bytes.Buffer
	lineCount := 0
	for _, deviceID := range deviceIDs {
		encoded, err := json.Marshal(edgeCommandWALEntry{
			Type:           edgeCommandWALRecordTypeFence,
			DeviceID:       deviceID,
			ExecutionFence: handler.maxFenceByDevice[deviceID],
		})
		if err != nil {
			return err
		}
		buf.Write(encoded)
		buf.WriteByte('\n')
		lineCount++
	}
	for _, commandID := range commandIDs {
		record := handler.results[commandID]
		encoded, err := json.Marshal(edgeCommandWALEntry{
			Type:      edgeCommandWALRecordTypeRecord,
			CommandID: commandID,
			Record:    &record,
		})
		if err != nil {
			return err
		}
		buf.Write(encoded)
		buf.WriteByte('\n')
		lineCount++
	}

	tmpPath := handler.walPath + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create compacted Edge command WAL: %w", err)
	}
	_, writeErr := file.Write(buf.Bytes())
	syncErr := writeErr
	if syncErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write compacted Edge command WAL: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync compacted Edge command WAL: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close compacted Edge command WAL: %w", closeErr)
	}
	if err := os.Rename(tmpPath, handler.walPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit compacted Edge command WAL: %w", err)
	}
	handler.walLines = lineCount
	handler.compactThreshold = edgeCommandWALCompactThreshold
	if handler.walLines >= handler.compactThreshold {
		handler.compactThreshold = handler.walLines + edgeCommandWALCompactThreshold
	}
	return nil
}

func decodeMQTTCommandEnvelope(payload []byte) (mqttCommandEnvelope, error) {
	var request mqttCommandEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return mqttCommandEnvelope{}, err
	}
	return request, nil
}

func (handler *edgeCommandHandler) Handle(received paho.PublishReceived) (bool, error) {
	if handler == nil || received.Packet == nil || received.Client == nil {
		return false, nil
	}
	request, err := decodeMQTTCommandEnvelope(received.Packet.Payload)
	if err != nil {
		return true, nil
	}
	reply := handler.evaluate(request)
	payload, err := json.Marshal(reply)
	if err != nil {
		return true, err
	}
	if _, err := handler.spool.Enqueue(reply.MessageID, EvidenceControl, handler.replyTopic, payload); err != nil {
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = handler.spool.Flush(ctx, received.Client)
	return true, nil
}

func (handler *edgeCommandHandler) evaluate(request mqttCommandEnvelope) mqttCommandReply {
	now := handler.now().UTC()
	messageID, _ := newUUIDV7(now)
	base := mqttCommandReply{
		SchemaVersion:  mqttCommandSchemaVersion,
		MessageID:      messageID,
		CommandID:      strings.TrimSpace(request.CommandID),
		TraceID:        strings.TrimSpace(request.TraceID),
		EventTime:      now.UnixMilli(),
		ExecutionFence: request.ExecutionFence,
	}
	if request.SchemaVersion != mqttCommandSchemaVersion ||
		strings.TrimSpace(request.MessageID) == "" || base.CommandID == "" ||
		strings.TrimSpace(request.DeviceID) == "" || strings.TrimSpace(request.PayloadHash) == "" ||
		request.ExecutionFence == 0 || request.IssuedAt <= 0 || request.ExpireAt <= request.IssuedAt {
		return edgeRejected(base, "COMMAND_INVALID")
	}
	deviceID := handler.deviceByWireID[strings.TrimSpace(request.DeviceID)]
	if deviceID == "" {
		return edgeRejected(base, "DEVICE_NOT_FOUND")
	}
	if request.ExpireAt <= now.UnixMilli() {
		base.EdgeStatus = "EXPIRED"
		reason := "EXPIRED"
		base.ReasonCode = &reason
		return base
	}
	issuedAt := time.UnixMilli(request.IssuedAt).UTC()
	expiresAt := time.UnixMilli(request.ExpireAt).UTC()
	if expiresAt.Sub(issuedAt) > mqttCommandMaxTTL || expiresAt.Sub(now) > mqttCommandMaxTTL {
		base.EdgeStatus = "EXPIRED"
		reason := "TTL_EXCEEDS_MAX"
		base.ReasonCode = &reason
		return base
	}
	if request.Policy.VerificationWindowMS < 0 || request.Policy.VerificationWindowMS > int64((10*time.Minute)/time.Millisecond) {
		return edgeRejected(base, "POLICY_INVALID")
	}

	handler.mu.Lock()
	if existing, ok := handler.results[base.CommandID]; ok {
		if existing.PayloadHash != request.PayloadHash || existing.ExecutionFence != request.ExecutionFence || existing.DeviceID != deviceID {
			handler.mu.Unlock()
			return edgeRejected(base, "PAYLOAD_MISMATCH")
		}
		if existing.State == edgeCommandTerminal && existing.Reply != nil {
			reply := *existing.Reply
			handler.mu.Unlock()
			return reply
		}
		handler.mu.Unlock()
		return edgeFailed(base, "EDGE_OUTCOME_UNKNOWN")
	}
	if handler.spool.State() == CapacityReadOnlySafety {
		handler.mu.Unlock()
		return edgeRejected(base, "EDGE_CAPACITY_READ_ONLY")
	}
	if request.ExecutionFence <= handler.maxFenceByDevice[deviceID] {
		handler.mu.Unlock()
		return edgeRejected(base, "STALE_FENCE")
	}
	record := edgeCommandRecord{
		DeviceID: deviceID, PayloadHash: request.PayloadHash, ExecutionFence: request.ExecutionFence, State: edgeCommandMayExecute,
	}
	if err := handler.appendRecordLocked(base.CommandID, record); err != nil {
		handler.mu.Unlock()
		return edgeRejected(base, "EDGE_LEDGER_UNAVAILABLE")
	}
	handler.mu.Unlock()

	if _, protected := edgecontrol.ClassifyHardRealtimeProtection(request.PointCode, request.CommandCode, nil); protected {
		return handler.finish(deviceID, request.PayloadHash, edgeRejected(base, edgecontrol.ReasonHardRealtimeSafetyBoundary))
	}

	outcomeCh, err := handler.edgeRuntime.SubmitCommand(EdgeCommandIntentRequest{
		CommandID:   base.CommandID,
		DeviceID:    deviceID,
		CommandCode: strings.TrimSpace(request.CommandCode),
		Params:      request.Params,
		IssuedAt:    time.UnixMilli(request.IssuedAt).UTC(),
		ExpiresAt:   time.UnixMilli(request.ExpireAt).UTC(),
	})
	if err != nil {
		reason := "COMMAND_INVALID"
		if errors.Is(err, edgecontrol.ErrHardRealtimeSafetyBoundary) {
			reason = edgecontrol.ReasonHardRealtimeSafetyBoundary
		} else if strings.Contains(err.Error(), "does not expose command capability") {
			reason = "COMMAND_MAPPING_INVALID"
		} else if strings.Contains(err.Error(), "in-flight") {
			reason = "COMMAND_BUSY"
		}
		return handler.finish(deviceID, request.PayloadHash, edgeRejected(base, reason))
	}

	waitDuration := time.UnixMilli(request.ExpireAt).Sub(handler.now().UTC())
	if waitDuration > 25*time.Second {
		waitDuration = 25 * time.Second
	}
	if waitDuration <= 0 {
		handler.edgeRuntime.CancelCommand(base.CommandID, "EXPIRED")
		base.EdgeStatus = "EXPIRED"
		reason := "EXPIRED"
		base.ReasonCode = &reason
		return handler.finish(deviceID, request.PayloadHash, base)
	}
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	select {
	case outcome := <-outcomeCh:
		base.ExecutionEvidence = edgeExecutionEvidence(outcome)
		if !outcome.Accepted {
			base.EdgeStatus = "REJECTED"
			if strings.HasPrefix(outcome.Code, "DEVICE_WRITE_") || strings.HasPrefix(outcome.Code, "LOCAL_READBACK_") {
				base.EdgeStatus = "FAILED"
			}
			reason := strings.TrimSpace(outcome.Code)
			if reason == "" {
				reason = "CONTROL_DENIED"
			}
			base.ReasonCode = &reason
			return handler.finish(deviceID, request.PayloadHash, base)
		}
		base.EdgeStatus = "EXECUTED"
		return handler.finish(deviceID, request.PayloadHash, base)
	case <-timer.C:
		handler.edgeRuntime.CancelCommand(base.CommandID, "TIMEOUT")
		base.EdgeStatus = "TIMEOUT"
		reason := "TIMEOUT"
		base.ReasonCode = &reason
		return handler.finish(deviceID, request.PayloadHash, base)
	}
}

func edgeRejected(base mqttCommandReply, reasonCode string) mqttCommandReply {
	base.EdgeStatus = "REJECTED"
	reason := reasonCode
	base.ReasonCode = &reason
	return base
}

func edgeFailed(base mqttCommandReply, reasonCode string) mqttCommandReply {
	base.EdgeStatus = "FAILED"
	reason := reasonCode
	base.ReasonCode = &reason
	return base
}

func (handler *edgeCommandHandler) finish(deviceID, payloadHash string, reply mqttCommandReply) mqttCommandReply {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	replyCopy := reply
	record := edgeCommandRecord{
		DeviceID: deviceID, PayloadHash: payloadHash, ExecutionFence: reply.ExecutionFence,
		State: edgeCommandTerminal, Reply: &replyCopy,
	}
	if err := handler.appendRecordLocked(reply.CommandID, record); err != nil {
		handler.recordEntryLocked(reply.CommandID, record)
	}
	return reply
}

func edgeExecutionEvidence(outcome EdgeCommandOutcome) *commandmodel.EdgeExecutionEvidence {
	evidence := &commandmodel.EdgeExecutionEvidence{
		Requested:          commandScalar(outcome.Requested),
		WinnerControllerID: strings.TrimSpace(outcome.WinnerControllerID),
		Cycle:              outcome.Cycle,
	}
	if outcome.Effective != nil {
		value := commandScalar(*outcome.Effective)
		evidence.Effective = &value
	}
	if outcome.AppliedValue != nil {
		value := commandScalar(*outcome.AppliedValue)
		evidence.Applied = &value
	}
	if outcome.ReadbackValue != nil {
		value := commandScalar(*outcome.ReadbackValue)
		evidence.Readback = &value
	}
	if outcome.ReadbackVerified || !outcome.ReadbackAt.IsZero() || strings.HasPrefix(outcome.Code, "LOCAL_READBACK_") {
		verified := outcome.ReadbackVerified
		evidence.LocalReadbackVerified = &verified
	}
	if !outcome.ReadbackAt.IsZero() {
		rbAt := outcome.ReadbackAt.UTC().UnixMilli()
		evidence.LocalReadbackAt = &rbAt
	}
	if !outcome.LeaseExpiresAt.IsZero() {
		leaseExp := outcome.LeaseExpiresAt.UTC().UnixMilli()
		evidence.LeaseExpiresAt = &leaseExp
	}
	for _, constraint := range outcome.ConstraintReasons {
		evidence.Constraints = append(evidence.Constraints, commandmodel.EdgeConstraintEvidence{
			ControllerID: constraint.ControllerID, Reason: constraint.Reason,
		})
	}
	if !evidence.Valid() {
		return nil
	}
	return evidence
}

func commandScalar(value edgecontrol.Value) commandmodel.ScalarValue {
	if number, ok := value.NumericFloat64(); ok {
		return commandmodel.NumberScalar(number)
	}
	switch value.Type {
	case edgecontrol.DataTypeBoolean:
		return commandmodel.BooleanScalar(value.Boolean)
	case edgecontrol.DataTypeString:
		return commandmodel.TextScalar(value.String)
	default:
		return commandmodel.ScalarValue{}
	}
}
