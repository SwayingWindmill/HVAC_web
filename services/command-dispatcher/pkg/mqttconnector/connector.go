package mqttconnector

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const commandSchemaVersion = "1.0"

var (
	ErrPayloadMismatch = errors.New("MQTT command attempt payload mismatch")
	ErrOldFence        = errors.New("MQTT command execution fence is stale")
)

type EvidenceStore interface {
	Prepare(context.Context, commandmodel.PreparedConnectorEvidence) error
	Complete(context.Context, commandmodel.CompletedConnectorEvidence) error
}

type Config struct {
	BrokerURL                  string
	ClientID                   string
	CAFile                     string
	CertFile                   string
	KeyFile                    string
	ServerName                 string
	TenantID                   string
	SiteID                     string
	GatewayID                  string
	DeviceExternalIDByDeviceID map[string]string
	EvidenceStore              EvidenceStore
	ReplyTimeout               time.Duration
	Now                        func() time.Time
}

type commandPolicy struct {
	RequiresReadback     bool  `json:"requiresReadback"`
	VerificationWindowMS int64 `json:"verificationWindowMs"`
}

type commandEnvelope struct {
	SchemaVersion  string                         `json:"schemaVersion"`
	MessageID      string                         `json:"messageId"`
	CommandID      string                         `json:"commandId"`
	TraceID        string                         `json:"traceId,omitempty"`
	IssuedAt       int64                          `json:"issuedAt"`
	ExpireAt       int64                          `json:"expireAt"`
	DeviceID       string                         `json:"deviceId"`
	CommandCode    string                         `json:"commandCode"`
	Params         commandmodel.CommandParameters `json:"params"`
	Policy         commandPolicy                  `json:"policy"`
	ExecutionFence uint64                         `json:"executionFence"`
	PayloadHash    string                         `json:"payloadHash"`
}

type commandReply struct {
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

type record struct {
	payloadHash string
	result      commandmodel.ConnectorResult
}

type Connector struct {
	config        Config
	manager       *autopaho.ConnectionManager
	commandTopic  string
	replyTopic    string

	mu               sync.Mutex
	results          map[string]record
	maxFenceByDevice map[string]uint64
	waiters          map[string]chan commandReply
}

func New(ctx context.Context, config Config) (*Connector, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	brokerURL, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := newTLSConfig(config)
	if err != nil {
		return nil, err
	}
	if config.ReplyTimeout == 0 {
		config.ReplyTimeout = 15 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	connector := &Connector{
		config:            config,
		commandTopic:      topic(config, "command"),
		replyTopic:        topic(config, "command/reply"),
		results:           make(map[string]record),
		maxFenceByDevice: make(map[string]uint64),
		waiters:           make(map[string]chan commandReply),
	}
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         24 * 60 * 60,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		ClientConfig: paho.ClientConfig{
			ClientID: strings.TrimSpace(config.ClientID),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				connector.onReply,
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		subscribeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, _ = manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: connector.replyTopic, QoS: 1}}})
	}
	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("create MQTT command connection: %w", err)
	}
	connector.manager = manager
	return connector, nil
}

func (connector *Connector) Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error) {
	if connector == nil || connector.manager == nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command connector is unavailable")
	}
	key := resultKey(envelope.AttemptID, envelope.ExecutionFence)
	connector.mu.Lock()
	if existing, ok := connector.results[key]; ok {
		if existing.payloadHash != envelope.PayloadHash {
			connector.mu.Unlock()
			return commandmodel.ConnectorResult{}, ErrPayloadMismatch
		}
		result := existing.result
		connector.mu.Unlock()
		return result, nil
	}
	if envelope.ExecutionFence < connector.maxFenceByDevice[envelope.DeviceID] {
		connector.mu.Unlock()
		return commandmodel.ConnectorResult{}, ErrOldFence
	}
	if envelope.ExecutionFence > connector.maxFenceByDevice[envelope.DeviceID] {
		connector.maxFenceByDevice[envelope.DeviceID] = envelope.ExecutionFence
	}
	connector.mu.Unlock()

	if envelope.SiteID != connector.config.SiteID {
		return commandmodel.ConnectorResult{}, errors.New("command Site is outside MQTT routing scope")
	}
	externalDeviceID := strings.TrimSpace(connector.config.DeviceExternalIDByDeviceID[envelope.DeviceID])
	if externalDeviceID == "" {
		return commandmodel.ConnectorResult{}, errors.New("command Device has no MQTT routing binding")
	}
	method, err := capabilityMethod(envelope.Capability)
	if err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	now := connector.config.Now().UTC()
	expireAt := envelope.LeaseUntil.UTC()
	if expireAt.IsZero() || !expireAt.After(now) {
		return commandmodel.ConnectorResult{}, errors.New("command dispatch lease is expired")
	}
	verificationWindow := connector.config.ReplyTimeout
	if verificationWindow <= 0 {
		verificationWindow = 15 * time.Second
	}
	request := commandEnvelope{
		SchemaVersion:  commandSchemaVersion,
		MessageID:      envelope.AttemptID,
		CommandID:      envelope.CommandID,
		IssuedAt:       now.UnixMilli(),
		ExpireAt:       expireAt.UnixMilli(),
		DeviceID:       externalDeviceID,
		CommandCode:    string(envelope.Capability),
		Params:         envelope.Parameters,
		Policy:         commandPolicy{RequiresReadback: true, VerificationWindowMS: verificationWindow.Milliseconds()},
		ExecutionFence: envelope.ExecutionFence,
		PayloadHash:    envelope.PayloadHash,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return commandmodel.ConnectorResult{}, err
	}
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: envelope.AttemptID, CommandID: envelope.CommandID,
		TenantID: envelope.TenantID, SiteID: envelope.SiteID, DeviceID: envelope.DeviceID,
		ExternalDeviceID: externalDeviceID, ExecutionFence: envelope.ExecutionFence,
		PayloadHash: envelope.PayloadHash,
		MappingRevision: "mqtt-command-v1:" + envelope.CapabilityRevision,
		BindingRevision: "mqtt-routing-v1",
		ProviderEndpoint: connector.commandTopic,
		ProviderMethod: method,
		RequestSHA256: sha256Hex(body),
		PreparedAt: now,
	}
	if err := connector.config.EvidenceStore.Prepare(ctx, prepared); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT connector evidence preparation failed")
	}

	waiterKey := replyKey(envelope.CommandID)
	waiter := make(chan commandReply, 1)
	connector.mu.Lock()
	connector.waiters[waiterKey] = waiter
	connector.mu.Unlock()
	defer func() {
		connector.mu.Lock()
		delete(connector.waiters, waiterKey)
		connector.mu.Unlock()
	}()

	publishContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, publishErr := connector.manager.Publish(publishContext, &paho.Publish{QoS: 1, Retain: false, Topic: connector.commandTopic, Payload: body})
	cancel()
	if publishErr != nil {
		return connector.complete(ctx, key, envelope.PayloadHash, commandmodel.CompletedConnectorEvidence{
			PreparedConnectorEvidence: prepared,
			RequestWritten: false,
			ConnectorPhase: commandmodel.ConnectorPreSendRejected,
			FailureCode: "MQTT_COMMAND_PUBLISH_FAILED",
			CompletedAt: connector.config.Now().UTC(),
		})
	}

	wait := connector.config.ReplyTimeout
	if remaining := time.Until(expireAt); remaining < wait {
		wait = remaining
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return connector.complete(ctx, key, envelope.PayloadHash, commandmodel.CompletedConnectorEvidence{
			PreparedConnectorEvidence: prepared, RequestWritten: true,
			ConnectorPhase: commandmodel.ConnectorRequestCommitted,
			FailureCode: "MQTT_COMMAND_CONTEXT_DONE", CompletedAt: connector.config.Now().UTC(),
		})
	case <-timer.C:
		return connector.complete(ctx, key, envelope.PayloadHash, commandmodel.CompletedConnectorEvidence{
			PreparedConnectorEvidence: prepared, RequestWritten: true,
			ConnectorPhase: commandmodel.ConnectorRequestCommitted,
			FailureCode: "MQTT_COMMAND_REPLY_TIMEOUT", CompletedAt: connector.config.Now().UTC(),
		})
	case reply := <-waiter:
		replyBody, _ := json.Marshal(reply)
		completed := commandmodel.CompletedConnectorEvidence{
			PreparedConnectorEvidence: prepared, RequestWritten: true,
			ResponseSHA256: sha256Hex(replyBody), CompletedAt: connector.config.Now().UTC(),
		}
		status := strings.ToUpper(strings.TrimSpace(reply.EdgeStatus))
		if reply.SchemaVersion != commandSchemaVersion || strings.TrimSpace(reply.MessageID) == "" || reply.ExecutionFence != envelope.ExecutionFence || reply.EventTime <= 0 {
			completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
			completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
			return connector.complete(ctx, key, envelope.PayloadHash, completed)
		}
		switch status {
		case "DEVICE_ACK", "EXECUTED", "VERIFIED":
			// Edge VERIFIED is execution evidence only. Cloud Control VERIFIED is
			// produced later by the independent readback verifier.
			completed.ConnectorPhase = commandmodel.ConnectorAcknowledged
			return connector.complete(ctx, key, envelope.PayloadHash, completed)
		case "REJECTED", "FAILED", "TIMEOUT", "EXPIRED":
			completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
			reason := status
			if reply.ReasonCode != nil && strings.TrimSpace(*reply.ReasonCode) != "" {
				reason = strings.ToUpper(strings.TrimSpace(*reply.ReasonCode))
			}
			completed.FailureCode = "MQTT_EDGE_" + reason
			return connector.complete(ctx, key, envelope.PayloadHash, completed)
		default:
			completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
			completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
			return connector.complete(ctx, key, envelope.PayloadHash, completed)
		}
	}
}

func (connector *Connector) Disconnect(ctx context.Context) error {
	if connector == nil || connector.manager == nil {
		return nil
	}
	return connector.manager.Disconnect(ctx)
}

func (connector *Connector) onReply(received paho.PublishReceived) (bool, error) {
	if received.Packet == nil || received.Packet.Topic != connector.replyTopic {
		return false, nil
	}
	var reply commandReply
	decoder := json.NewDecoder(strings.NewReader(string(received.Packet.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return true, nil
	}
	status := strings.ToUpper(strings.TrimSpace(reply.EdgeStatus))
	if status == "RECEIVED" || status == "VALIDATING" || status == "WRITING" {
		// Intermediate Edge evidence does not complete the Cloud dispatch attempt.
		return true, nil
	}
	key := replyKey(reply.CommandID)
	connector.mu.Lock()
	waiter := connector.waiters[key]
	connector.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- reply:
		default:
		}
	}
	return true, nil
}

func (connector *Connector) complete(ctx context.Context, key, payloadHash string, evidence commandmodel.CompletedConnectorEvidence) (commandmodel.ConnectorResult, error) {
	result := commandmodel.ConnectorResult{
		Phase: evidence.ConnectorPhase,
		FailureCode: evidence.FailureCode,
		EvidenceID: "mqtt:" + evidence.AttemptID + ":" + fmt.Sprint(evidence.ExecutionFence),
		Acknowledged: evidence.ConnectorPhase == commandmodel.ConnectorAcknowledged,
		Verified: false,
	}
	if err := connector.config.EvidenceStore.Complete(ctx, evidence); err != nil {
		if evidence.ConnectorPhase == commandmodel.ConnectorPreSendRejected {
			return commandmodel.ConnectorResult{}, errors.New("MQTT connector evidence completion failed")
		}
		result.Phase = commandmodel.ConnectorRequestCommitted
		result.Acknowledged = false
		result.FailureCode = "CONNECTOR_EVIDENCE_COMPLETION_FAILED"
	}
	connector.mu.Lock()
	connector.results[key] = record{payloadHash: payloadHash, result: result}
	connector.mu.Unlock()
	return result, nil
}

func validateConfig(config Config) error {
	parsed, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil || parsed.Scheme != "tls" || parsed.Host == "" {
		return errors.New("MQTT command brokerUrl must be a tls:// endpoint")
	}
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.TenantID) == "" || strings.TrimSpace(config.SiteID) == "" || strings.TrimSpace(config.GatewayID) == "" {
		return errors.New("MQTT command identity is incomplete")
	}
	if strings.TrimSpace(config.CAFile) == "" || strings.TrimSpace(config.CertFile) == "" || strings.TrimSpace(config.KeyFile) == "" || strings.TrimSpace(config.ServerName) == "" {
		return errors.New("MQTT command TLS configuration is incomplete")
	}
	if config.EvidenceStore == nil || len(config.DeviceExternalIDByDeviceID) == 0 {
		return errors.New("MQTT command dependencies are incomplete")
	}
	if config.ReplyTimeout < 0 || config.ReplyTimeout > 30*time.Second {
		return errors.New("MQTT command reply timeout is invalid")
	}
	return nil
}

func newTLSConfig(config Config) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, errors.New("load MQTT command client identity failed")
	}
	caContent, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, errors.New("read MQTT command CA failed")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caContent) {
		return nil, errors.New("MQTT command CA is invalid")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: rootCAs, Certificates: []tls.Certificate{certificate}, ServerName: strings.TrimSpace(config.ServerName)}, nil
}

func capabilityMethod(capability commandmodel.Capability) (string, error) {
	switch capability {
	case commandmodel.CapabilityStart:
		return "start", nil
	case commandmodel.CapabilityStop:
		return "stop", nil
	case commandmodel.CapabilityResetFault:
		return "resetFault", nil
	case commandmodel.CapabilitySetTemperatureSetpoint:
		return "setTemperatureSetpoint", nil
	case commandmodel.CapabilitySetChilledWaterTemperatureSetpoint:
		return "setChilledWaterTemperatureSetpoint", nil
	case commandmodel.CapabilitySetFrequency:
		return "setFrequency", nil
	case commandmodel.CapabilitySetFanSpeed:
		return "setFanSpeed", nil
	case commandmodel.CapabilitySetLoadLimit:
		return "setLoadLimit", nil
	case commandmodel.CapabilitySetOpening:
		return "setOpening", nil
	default:
		return "", errors.New("command capability has no native MQTT method")
	}
}

func topic(config Config, suffix string) string {
	return "energy/v1/" + strings.TrimSpace(config.TenantID) + "/" + strings.TrimSpace(config.SiteID) + "/" + strings.TrimSpace(config.GatewayID) + "/" + suffix
}

func resultKey(attemptID string, fence uint64) string {
	return strings.TrimSpace(attemptID) + "|" + fmt.Sprint(fence)
}

func replyKey(commandID string) string {
	return strings.TrimSpace(commandID)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
