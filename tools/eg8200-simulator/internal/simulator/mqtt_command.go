package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

const mqttCommandSchemaVersion = "2.0"

type mqttCommandPolicy struct {
	RequiresReadback     bool  `json:"requiresReadback"`
	VerificationWindowMS int64 `json:"verificationWindowMs"`
}

type mqttCommandEnvelope struct {
	SchemaVersion  string             `json:"schemaVersion"`
	MessageID      string             `json:"messageId"`
	CommandID      string             `json:"commandId"`
	TraceID        string             `json:"traceId,omitempty"`
	IssuedAt       int64              `json:"issuedAt"`
	ExpireAt       int64              `json:"expireAt"`
	DeviceID       string             `json:"deviceId"`
	CommandCode    string             `json:"commandCode"`
	Params         map[string]float64 `json:"params"`
	Policy         mqttCommandPolicy  `json:"policy"`
	ExecutionFence uint64             `json:"executionFence"`
	PayloadHash    string             `json:"payloadHash"`
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
	edgeCommandMayExecute = "MAY_EXECUTE"
	edgeCommandTerminal   = "TERMINAL"
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

type edgeCommandHandler struct {
	edgeRuntime    *EdgeControlRuntime
	deviceByWireID map[string]string
	replyTopic     string
	now            func() time.Time
	ledgerPath     string
	spool          *mqttEvidenceSpool

	mu               sync.Mutex
	results          map[string]edgeCommandRecord
	maxFenceByDevice map[string]uint64
}

func newEdgeCommandHandler(edgeRuntime *EdgeControlRuntime, config MQTTGatewayConfig, gatewayID string, spool *mqttEvidenceSpool) (*edgeCommandHandler, error) {
	if edgeRuntime == nil || spool == nil {
		return nil, errors.New("MQTT command handler requires Edge Control Runtime and evidence spool")
	}
	deviceByWireID := make(map[string]string, len(config.DeviceExternalIDByDeviceID))
	for deviceID, wireID := range config.DeviceExternalIDByDeviceID {
		wireID = strings.TrimSpace(wireID)
		if wireID == "" {
			return nil, errors.New("MQTT command routing contains an empty device id")
		}
		if _, duplicate := deviceByWireID[wireID]; duplicate {
			return nil, errors.New("MQTT command routing contains duplicate device ids")
		}
		deviceByWireID[wireID] = deviceID
	}
	ledgerPath := filepath.Join(config.QueueDirectory, "command-execution-records.json")
	results, err := loadEdgeCommandLedger(ledgerPath)
	if err != nil {
		return nil, err
	}
	maxFenceByDevice := make(map[string]uint64)
	for _, record := range results {
		if strings.TrimSpace(record.DeviceID) == "" || record.ExecutionFence == 0 ||
			(record.State != edgeCommandMayExecute && record.State != edgeCommandTerminal) {
			return nil, errors.New("Edge command execution ledger contains an invalid record")
		}
		if record.State == edgeCommandTerminal && record.Reply == nil {
			return nil, errors.New("Edge command execution ledger terminal record has no reply")
		}
		if record.ExecutionFence > maxFenceByDevice[record.DeviceID] {
			maxFenceByDevice[record.DeviceID] = record.ExecutionFence
		}
	}
	return &edgeCommandHandler{
		edgeRuntime:      edgeRuntime,
		deviceByWireID:   deviceByWireID,
		replyTopic:       "energy/v1/" + config.TenantID + "/" + config.SiteID + "/" + gatewayID + "/command/reply",
		now:              time.Now,
		ledgerPath:       ledgerPath,
		spool:            spool,
		results:          results,
		maxFenceByDevice: maxFenceByDevice,
	}, nil
}

func loadEdgeCommandLedger(path string) (map[string]edgeCommandRecord, error) {
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
		return nil, errors.New("Edge command execution ledger is invalid")
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

func (handler *edgeCommandHandler) Handle(received paho.PublishReceived) (bool, error) {
	if handler == nil || received.Packet == nil || received.Client == nil {
		return false, nil
	}
	var request mqttCommandEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(received.Packet.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return true, nil
	}
	reply := handler.evaluate(request)
	payload, err := json.Marshal(reply)
	if err != nil {
		return true, err
	}
	if _, err := handler.spool.Enqueue(reply.MessageID, edgefleet.EvidenceControl, handler.replyTopic, payload); err != nil {
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
	if handler.spool.State() == edgefleet.CapacityReadOnlySafety {
		handler.mu.Unlock()
		return edgeRejected(base, "EDGE_CAPACITY_READ_ONLY")
	}
	if request.ExecutionFence <= handler.maxFenceByDevice[deviceID] {
		handler.mu.Unlock()
		return edgeRejected(base, "STALE_FENCE")
	}
	handler.results[base.CommandID] = edgeCommandRecord{
		DeviceID: deviceID, PayloadHash: request.PayloadHash, ExecutionFence: request.ExecutionFence, State: edgeCommandMayExecute,
	}
	if err := persistEdgeCommandLedger(handler.ledgerPath, handler.results); err != nil {
		delete(handler.results, base.CommandID)
		handler.mu.Unlock()
		return edgeRejected(base, "EDGE_LEDGER_UNAVAILABLE")
	}
	handler.maxFenceByDevice[deviceID] = request.ExecutionFence
	handler.mu.Unlock()

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
		if strings.Contains(err.Error(), "does not expose command capability") {
			reason = "COMMAND_MAPPING_INVALID"
		} else if strings.Contains(err.Error(), "in-flight") {
			reason = "COMMAND_BUSY"
		}
		return handler.finish(deviceID, request.PayloadHash, edgeRejected(base, reason))
	}

	waitDuration := time.UnixMilli(request.ExpireAt).Sub(now)
	if waitDuration > 10*time.Second {
		waitDuration = 10 * time.Second
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
			if strings.HasPrefix(outcome.Code, "DEVICE_WRITE_") {
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
	handler.results[reply.CommandID] = edgeCommandRecord{
		DeviceID: deviceID, PayloadHash: payloadHash, ExecutionFence: reply.ExecutionFence,
		State: edgeCommandTerminal, Reply: &replyCopy,
	}
	_ = persistEdgeCommandLedger(handler.ledgerPath, handler.results)
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

func nativeCommandMethod(commandCode string) (string, bool) {
	method := map[string]string{
		"START":                                  "start",
		"STOP":                                   "stop",
		"RESET_FAULT":                            "resetFault",
		"SET_TEMPERATURE_SETPOINT":               "setTemperatureSetpoint",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT": "setChilledWaterTemperatureSetpoint",
		"SET_FREQUENCY":                          "setFrequency",
		"SET_FAN_SPEED":                          "setFanSpeed",
		"SET_LOAD_LIMIT":                         "setLoadLimit",
		"SET_OPENING":                            "setOpening",
	}[commandCode]
	return method, method != ""
}
