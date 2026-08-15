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
)

const mqttCommandSchemaVersion = "1.0"

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
	SchemaVersion  string             `json:"schemaVersion"`
	MessageID      string             `json:"messageId"`
	CommandID      string             `json:"commandId"`
	TraceID        string             `json:"traceId,omitempty"`
	EventTime      int64              `json:"eventTime"`
	EdgeStatus     string             `json:"edgeStatus"`
	Reported       map[string]float64 `json:"reported,omitempty"`
	ReasonCode     *string            `json:"reasonCode"`
	ExecutionFence uint64             `json:"executionFence"`
}

type edgeCommandRecord struct {
	PayloadHash string           `json:"payloadHash"`
	Reply       mqttCommandReply `json:"reply"`
}

type edgeCommandLedger struct {
	Records map[string]edgeCommandRecord `json:"records"`
}

type edgeCommandHandler struct {
	plant              *Plant
	deviceByWireID     map[string]string
	replyTopic         string
	now                func() time.Time
	ledgerPath         string

	mu               sync.Mutex
	results          map[string]edgeCommandRecord
	maxFenceByDevice map[string]uint64
}

func newEdgeCommandHandler(plant *Plant, config MQTTGatewayConfig, gatewayID string) (*edgeCommandHandler, error) {
	if plant == nil {
		return nil, errors.New("MQTT command handler requires Plant")
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
		if record.Reply.ExecutionFence == 0 {
			continue
		}
		// The persisted reply does not expose the internal Plant id. Fence recovery
		// remains conservative: commandId idempotency survives restart, while new
		// physical commands receive a fresh Cloud execution fence.
	}
	return &edgeCommandHandler{
		plant:              plant,
		deviceByWireID:     deviceByWireID,
		replyTopic:         "energy/v1/" + config.TenantID + "/" + config.SiteID + "/" + gatewayID + "/command/reply",
		now:                time.Now,
		ledgerPath:         ledgerPath,
		results:            results,
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = received.Client.Publish(ctx, &paho.Publish{QoS: 1, Retain: false, Topic: handler.replyTopic, Payload: payload})
	return true, err
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

	handler.mu.Lock()
	if existing, ok := handler.results[base.CommandID]; ok {
		if existing.PayloadHash != request.PayloadHash {
			handler.mu.Unlock()
			return edgeRejected(base, "PAYLOAD_MISMATCH")
		}
		reply := existing.Reply
		handler.mu.Unlock()
		return reply
	}
	deviceID := handler.deviceByWireID[strings.TrimSpace(request.DeviceID)]
	if deviceID == "" {
		handler.mu.Unlock()
		return edgeRejected(base, "DEVICE_NOT_FOUND")
	}
	if request.ExecutionFence < handler.maxFenceByDevice[deviceID] {
		handler.mu.Unlock()
		return edgeRejected(base, "STALE_FENCE")
	}
	if request.ExecutionFence > handler.maxFenceByDevice[deviceID] {
		handler.maxFenceByDevice[deviceID] = request.ExecutionFence
	}
	handler.mu.Unlock()

	if request.ExpireAt <= now.UnixMilli() {
		base.EdgeStatus = "EXPIRED"
		reason := "EXPIRED"
		base.ReasonCode = &reason
		handler.remember(request.PayloadHash, base)
		return base
	}
	if request.Policy.VerificationWindowMS < 0 || request.Policy.VerificationWindowMS > int64((10*time.Minute)/time.Millisecond) {
		return edgeRejectedAndRemember(handler, request.PayloadHash, base, "POLICY_INVALID")
	}
	method, ok := nativeCommandMethod(strings.TrimSpace(request.CommandCode))
	if !ok {
		return edgeRejectedAndRemember(handler, request.PayloadHash, base, "COMMAND_MAPPING_INVALID")
	}

	result := handler.plant.ApplyCommand(Command{DeviceID: deviceID, Method: method, Params: request.Params})
	if !result.Success {
		base.EdgeStatus = "FAILED"
		reason := strings.TrimSpace(result.Code)
		if reason == "" {
			reason = "DEVICE_WRITE_FAILED"
		}
		base.ReasonCode = &reason
		handler.remember(request.PayloadHash, base)
		return base
	}
	base.EdgeStatus = "EXECUTED"
	if request.Policy.RequiresReadback {
		// The simulator applies the command synchronously and exposes the resulting
		// local value as Edge-local readback evidence. This does not make Cloud
		// Control VERIFIED; Cloud still performs its independent verification.
		base.EdgeStatus = "VERIFIED"
	}
	if result.AppliedValue != 0 {
		base.Reported = map[string]float64{"value": result.AppliedValue}
	}
	handler.remember(request.PayloadHash, base)
	return base
}

func edgeRejected(base mqttCommandReply, reasonCode string) mqttCommandReply {
	base.EdgeStatus = "REJECTED"
	reason := reasonCode
	base.ReasonCode = &reason
	return base
}

func edgeRejectedAndRemember(handler *edgeCommandHandler, payloadHash string, base mqttCommandReply, reasonCode string) mqttCommandReply {
	reply := edgeRejected(base, reasonCode)
	handler.remember(payloadHash, reply)
	return reply
}

func (handler *edgeCommandHandler) remember(payloadHash string, reply mqttCommandReply) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.results[reply.CommandID] = edgeCommandRecord{PayloadHash: payloadHash, Reply: reply}
	_ = persistEdgeCommandLedger(handler.ledgerPath, handler.results)
}

func nativeCommandMethod(commandCode string) (string, bool) {
	method := map[string]string{
		"START":                                 "start",
		"STOP":                                  "stop",
		"RESET_FAULT":                           "resetFault",
		"SET_TEMPERATURE_SETPOINT":              "setTemperatureSetpoint",
		"SET_CHILLED_WATER_TEMPERATURE_SETPOINT": "setChilledWaterTemperatureSetpoint",
		"SET_FREQUENCY":                         "setFrequency",
		"SET_FAN_SPEED":                         "setFanSpeed",
		"SET_LOAD_LIMIT":                        "setLoadLimit",
		"SET_OPENING":                           "setOpening",
	}[commandCode]
	return method, method != ""
}
