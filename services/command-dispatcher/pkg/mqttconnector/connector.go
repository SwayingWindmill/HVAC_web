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
	"strconv"
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
	BrokerURL             string
	ClientID              string
	CAFile                string
	CertFile              string
	KeyFile               string
	ServerName            string
	IntegrationInstanceID string
	TenantID              string
	SiteID                string
	GatewayID             string
	OwnerID               string
	OwnerGeneration       uint64
	TransportState        TransportState
	EvidenceStore         EvidenceStore
	LateResultSink        LateResultSink
	ReplyTimeout          time.Duration
	Now                   func() time.Time
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

type Connector struct {
	rootContext  context.Context
	config       Config
	manager      *autopaho.ConnectionManager
	commandTopic string
	replyTopic   string

	mu      sync.Mutex
	waiters map[string]chan CommandCorrelation
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
		rootContext:  ctx,
		config:       config,
		commandTopic: topic(config, "command"),
		replyTopic:   topic(config, "command/reply"),
		waiters:      make(map[string]chan CommandCorrelation),
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
		if _, subscribeErr := manager.Subscribe(subscribeContext, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: connector.replyTopic, QoS: 1}}}); subscribeErr == nil {
			go connector.recoverReplies(ctx)
		}
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
	if err := connector.config.TransportState.AssertConnectorOwnership(ctx, connector.config.IntegrationInstanceID, connector.config.OwnerID, connector.config.OwnerGeneration); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command connector ownership is not active")
	}
	if envelope.TenantID != connector.config.TenantID || envelope.SiteID != connector.config.SiteID {
		return commandmodel.ConnectorResult{}, errors.New("command scope is outside MQTT integration")
	}
	route, err := connector.config.TransportState.ResolveCommandRoute(ctx, connector.config.IntegrationInstanceID, envelope.TenantID, envelope.SiteID, connector.config.GatewayID, envelope.DeviceID)
	if err != nil {
		return commandmodel.ConnectorResult{}, errors.New("command Device has no active MQTT binding")
	}
	externalDeviceID := strings.TrimSpace(route.ExternalDeviceID)
	if externalDeviceID == "" || route.BindingRevision == 0 {
		return commandmodel.ConnectorResult{}, errors.New("command Device has invalid MQTT binding")
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
		PayloadHash:      envelope.PayloadHash,
		MappingRevision:  "mqtt-command-v1:" + envelope.CapabilityRevision,
		BindingRevision:  "connectivity:" + strconv.FormatUint(route.BindingRevision, 10),
		ProviderEndpoint: connector.commandTopic,
		ProviderMethod:   method,
		RequestSHA256:    sha256Hex(body),
		PreparedAt:       now,
	}
	if err := connector.config.EvidenceStore.Prepare(ctx, prepared); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT connector evidence preparation failed")
	}
	correlation, err := connector.config.TransportState.PrepareCommandCorrelation(ctx, CommandCorrelation{
		Envelope: envelope, IntegrationInstanceID: connector.config.IntegrationInstanceID,
		ExternalDeviceID: externalDeviceID, OwnerGeneration: connector.config.OwnerGeneration,
		MappingRevision: prepared.MappingRevision, BindingRevision: prepared.BindingRevision,
		ProviderEndpoint: prepared.ProviderEndpoint, ProviderMethod: prepared.ProviderMethod,
		RequestSHA256: prepared.RequestSHA256, PreparedAt: prepared.PreparedAt,
		State: CorrelationPrepared,
	})
	if err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command correlation preparation failed")
	}
	if correlation.Envelope.PayloadHash != envelope.PayloadHash {
		return commandmodel.ConnectorResult{}, ErrPayloadMismatch
	}
	switch resumeActionForCorrelation(correlation.State) {
	case resumeUseReply:
		return connector.completeRecoveredReply(ctx, correlation)
	case resumeOutcomeUnknown:
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_RECOVERED_MAY_COMMIT")
	case resumePublish:
		// PREPARED is the only state from which a physical publish is permitted.
	default:
		return commandmodel.ConnectorResult{}, errors.New("MQTT command correlation state is invalid")
	}

	waiterKey := replyKey(envelope.CommandID, envelope.ExecutionFence)
	waiter := make(chan CommandCorrelation, 1)
	connector.mu.Lock()
	connector.waiters[waiterKey] = waiter
	connector.mu.Unlock()
	defer func() {
		connector.mu.Lock()
		delete(connector.waiters, waiterKey)
		connector.mu.Unlock()
	}()

	armedAt := connector.config.Now().UTC()
	if err := connector.config.TransportState.ArmCommandCorrelation(ctx, envelope.AttemptID, envelope.ExecutionFence, connector.config.OwnerGeneration, armedAt); err != nil {
		return commandmodel.ConnectorResult{}, errors.New("MQTT command publish commit-point arm failed")
	}

	publishContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, publishErr := connector.manager.Publish(publishContext, &paho.Publish{QoS: 1, Retain: false, Topic: connector.commandTopic, Payload: body})
	cancel()
	if publishErr != nil {
		// The durable correlation is armed before calling the MQTT client. A process
		// crash or publish error after this point is not provably unsent, so retrying
		// the physical write would be unsafe.
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_PUBLISH_OUTCOME_UNKNOWN")
	}

	wait := connector.config.ReplyTimeout
	if remaining := time.Until(expireAt); remaining < wait {
		wait = remaining
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_CONTEXT_DONE")
	case <-timer.C:
		return connector.completeOutcomeUnknown(ctx, prepared, "MQTT_COMMAND_REPLY_TIMEOUT")
	case replyCorrelation := <-waiter:
		return connector.completeRecoveredReply(ctx, replyCorrelation)
	}
}

func (connector *Connector) Disconnect(ctx context.Context) error {
	if connector == nil || connector.manager == nil {
		return nil
	}
	return connector.manager.Disconnect(ctx)
}

func (connector *Connector) FinalizeDispatch(ctx context.Context, envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error {
	if connector == nil || !replyBackedResult(result) {
		return nil
	}
	return connector.config.TransportState.MarkCommandCorrelationResolved(ctx, envelope.AttemptID, envelope.ExecutionFence, connector.config.Now().UTC())
}

func replyBackedResult(result commandmodel.ConnectorResult) bool {
	return result.Acknowledged || strings.HasPrefix(result.FailureCode, "MQTT_EDGE_") || result.FailureCode == "MQTT_COMMAND_REPLY_INVALID"
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
		return true, nil
	}
	if strings.TrimSpace(reply.CommandID) == "" || reply.ExecutionFence == 0 {
		return true, nil
	}
	replyBody, _ := json.Marshal(reply)
	reason := ""
	if reply.ReasonCode != nil {
		reason = strings.ToUpper(strings.TrimSpace(*reply.ReasonCode))
	}
	eventTime := time.UnixMilli(reply.EventTime).UTC()
	if reply.SchemaVersion != commandSchemaVersion || strings.TrimSpace(reply.MessageID) == "" || reply.EventTime <= 0 {
		status = "INVALID"
		reason = ""
		eventTime = time.Time{}
	}
	ctx, cancel := context.WithTimeout(connector.rootContext, 5*time.Second)
	defer cancel()
	correlation, err := connector.config.TransportState.RecordCommandReply(
		ctx, connector.config.IntegrationInstanceID, reply.CommandID, reply.ExecutionFence,
		sha256Hex(replyBody), status, eventTime, reason, connector.config.Now().UTC(),
	)
	if err != nil {
		return true, nil
	}
	key := replyKey(reply.CommandID, reply.ExecutionFence)
	connector.mu.Lock()
	waiter := connector.waiters[key]
	connector.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- correlation:
		default:
		}
		return true, nil
	}
	_ = connector.resolveRecoveredReply(ctx, correlation)
	return true, nil
}

func (connector *Connector) recoverReplies(ctx context.Context) {
	if connector.config.LateResultSink == nil {
		return
	}
	recoveryContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	correlations, err := connector.config.TransportState.RecoverCommandReplies(recoveryContext, connector.config.IntegrationInstanceID, 50)
	if err != nil {
		return
	}
	for _, correlation := range correlations {
		if recoveryContext.Err() != nil {
			return
		}
		_ = connector.resolveRecoveredReply(recoveryContext, correlation)
	}
}

func (connector *Connector) resolveRecoveredReply(ctx context.Context, correlation CommandCorrelation) error {
	if connector.config.LateResultSink == nil || correlation.State != CorrelationReplied {
		return nil
	}
	result, err := connector.completeRecoveredReply(ctx, correlation)
	if err != nil {
		return err
	}
	if err := connector.config.LateResultSink.ResolveDispatch(ctx, correlation.Envelope, result); err != nil {
		return err
	}
	return connector.config.TransportState.MarkCommandCorrelationResolved(ctx, correlation.Envelope.AttemptID, correlation.Envelope.ExecutionFence, connector.config.Now().UTC())
}

func (connector *Connector) completeRecoveredReply(ctx context.Context, correlation CommandCorrelation) (commandmodel.ConnectorResult, error) {
	completed := completedEvidenceFromCorrelation(correlation)
	result := connectorResult(completed)
	if err := connector.config.EvidenceStore.Complete(ctx, completed); err != nil {
		result.Phase = commandmodel.ConnectorRequestCommitted
		result.Acknowledged = false
		result.FailureCode = "CONNECTOR_EVIDENCE_COMPLETION_FAILED"
		return result, nil
	}
	return result, nil
}

func (connector *Connector) completeOutcomeUnknown(ctx context.Context, prepared commandmodel.PreparedConnectorEvidence, failureCode string) (commandmodel.ConnectorResult, error) {
	completed := commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared,
		RequestWritten:            true,
		ConnectorPhase:            commandmodel.ConnectorRequestCommitted,
		FailureCode:               failureCode,
		CompletedAt:               connector.config.Now().UTC(),
	}
	result := connectorResult(completed)
	if err := connector.config.EvidenceStore.Complete(ctx, completed); err != nil {
		result.FailureCode = "CONNECTOR_EVIDENCE_COMPLETION_FAILED"
	}
	return result, nil
}

func completedEvidenceFromCorrelation(correlation CommandCorrelation) commandmodel.CompletedConnectorEvidence {
	prepared := commandmodel.PreparedConnectorEvidence{
		AttemptID: correlation.Envelope.AttemptID, CommandID: correlation.Envelope.CommandID,
		TenantID: correlation.Envelope.TenantID, SiteID: correlation.Envelope.SiteID, DeviceID: correlation.Envelope.DeviceID,
		ExternalDeviceID: correlation.ExternalDeviceID, ExecutionFence: correlation.Envelope.ExecutionFence,
		PayloadHash:     correlation.Envelope.PayloadHash,
		MappingRevision: correlation.MappingRevision, BindingRevision: correlation.BindingRevision,
		ProviderEndpoint: correlation.ProviderEndpoint, ProviderMethod: correlation.ProviderMethod,
		RequestSHA256: correlation.RequestSHA256, PreparedAt: correlation.PreparedAt,
	}
	completed := commandmodel.CompletedConnectorEvidence{
		PreparedConnectorEvidence: prepared,
		RequestWritten:            true,
		ResponseSHA256:            correlation.ReplySHA256,
		CompletedAt:               correlation.RepliedAt,
	}
	status := strings.ToUpper(strings.TrimSpace(correlation.ReplyStatus))
	if correlation.ReplyEventTime.IsZero() {
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
		return completed
	}
	switch status {
	case "DEVICE_ACK", "EXECUTED", "VERIFIED":
		completed.ProviderStatusCode = 200
		completed.ConnectorPhase = commandmodel.ConnectorAcknowledged
	case "REJECTED", "FAILED", "TIMEOUT", "EXPIRED":
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		reason := status
		if strings.TrimSpace(correlation.ReplyReasonCode) != "" {
			reason = strings.ToUpper(strings.TrimSpace(correlation.ReplyReasonCode))
		}
		completed.FailureCode = "MQTT_EDGE_" + reason
	default:
		completed.ConnectorPhase = commandmodel.ConnectorRequestCommitted
		completed.FailureCode = "MQTT_COMMAND_REPLY_INVALID"
	}
	return completed
}

func connectorResult(evidence commandmodel.CompletedConnectorEvidence) commandmodel.ConnectorResult {
	return commandmodel.ConnectorResult{
		Phase:        evidence.ConnectorPhase,
		FailureCode:  evidence.FailureCode,
		EvidenceID:   "mqtt:" + evidence.AttemptID + ":" + fmt.Sprint(evidence.ExecutionFence),
		Acknowledged: evidence.ConnectorPhase == commandmodel.ConnectorAcknowledged,
		Verified:     false,
	}
}

func validateConfig(config Config) error {
	parsed, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil || parsed.Scheme != "tls" || parsed.Host == "" {
		return errors.New("MQTT command brokerUrl must be a tls:// endpoint")
	}
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.IntegrationInstanceID) == "" || strings.TrimSpace(config.TenantID) == "" || strings.TrimSpace(config.SiteID) == "" || strings.TrimSpace(config.GatewayID) == "" || strings.TrimSpace(config.OwnerID) == "" || config.OwnerGeneration == 0 {
		return errors.New("MQTT command identity is incomplete")
	}
	if strings.TrimSpace(config.CAFile) == "" || strings.TrimSpace(config.CertFile) == "" || strings.TrimSpace(config.KeyFile) == "" || strings.TrimSpace(config.ServerName) == "" {
		return errors.New("MQTT command TLS configuration is incomplete")
	}
	if config.TransportState == nil || config.EvidenceStore == nil {
		return errors.New("MQTT command durable dependencies are incomplete")
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

type resumeAction uint8

const (
	resumeInvalid resumeAction = iota
	resumePublish
	resumeOutcomeUnknown
	resumeUseReply
)

func resumeActionForCorrelation(state CorrelationState) resumeAction {
	switch state {
	case CorrelationPrepared:
		return resumePublish
	case CorrelationMayCommit:
		return resumeOutcomeUnknown
	case CorrelationReplied, CorrelationResolved:
		return resumeUseReply
	default:
		return resumeInvalid
	}
}

func replyKey(commandID string, fence uint64) string {
	return strings.TrimSpace(commandID) + "|" + strconv.FormatUint(fence, 10)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
