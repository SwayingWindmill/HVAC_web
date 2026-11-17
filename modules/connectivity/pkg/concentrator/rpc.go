package concentrator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	rpcSchemaVersion    = "1.0"
	defaultRPCTimeout   = 18 * time.Second
	ReplyTopicFilter    = "hvac/v1/+/up/concentrator"
	maxReplyPayloadSize = 256 << 10
	mqttConnectTimeout  = 10 * time.Second
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type TransportConfig struct {
	BrokerURL  string
	ClientID   string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
	RPCTimeout time.Duration
	Logger     *slog.Logger
	Metrics    *observability.Registry
	PacketHook func(direction, channel, gatewayID, topic, traceID string, payload []byte)
}

func DownlinkTopic(gatewayID string) string {
	return "hvac/v1/" + gatewayID + "/down/concentrator"
}

func ParseReplyTopic(topic string) (string, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) != 5 || parts[0] != "hvac" || parts[1] != "v1" || parts[3] != "up" || parts[4] != "concentrator" {
		return "", false
	}
	if !uuidV7Pattern.MatchString(parts[2]) {
		return "", false
	}
	return parts[2], true
}

type waiterKey struct {
	gatewayID string
	requestID string
}

var sharedRouteCounter atomic.Uint64

type MQTTTransport struct {
	rootContext       context.Context
	runContext        context.Context
	stopCancel        context.CancelFunc
	timeout           time.Duration
	logger            *slog.Logger
	metrics           *observability.Registry
	packetHook        func(direction, channel, gatewayID, topic, traceID string, payload []byte)
	manager           *autopaho.ConnectionManager
	sharedSession     *mqttclient.SharedSession
	unregisterRoute   func(context.Context) error
	removeObserver    func()
	connTracker       *mqttclient.ConnTracker
	subscribeBackoffs []time.Duration

	subWG      sync.WaitGroup
	subMu      sync.Mutex
	subscribed bool
	closed     bool
	subGen     uint64
	subCancel  context.CancelFunc

	readyMu   sync.Mutex
	readyOnce bool
	readyCh   chan struct{}

	mu           sync.Mutex
	waiters      map[waiterKey]chan RPCResponse
	waiterTraces map[waiterKey]string
}

func newBaseTransport(ctx context.Context, cfg TransportConfig, subscribeBackoffs []time.Duration) *MQTTTransport {
	timeout := cfg.RPCTimeout
	if timeout <= 0 {
		timeout = defaultRPCTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	runContext, stopCancel := context.WithCancel(ctx)
	return &MQTTTransport{
		rootContext:       ctx,
		runContext:        runContext,
		stopCancel:        stopCancel,
		timeout:           timeout,
		logger:            logger,
		metrics:           cfg.Metrics,
		packetHook:        cfg.PacketHook,
		connTracker:       mqttclient.NewConnTracker(),
		subscribeBackoffs: subscribeBackoffs,
		readyCh:           make(chan struct{}),
		waiters:           map[waiterKey]chan RPCResponse{},
		waiterTraces:      map[waiterKey]string{},
	}
}

func NewMQTTTransport(ctx context.Context, cfg TransportConfig) (*MQTTTransport, error) {
	return newMQTTTransport(ctx, cfg, nil, nil)
}

// NewSharedMQTTTransport builds a concentrator RPC transport over an existing
// mqttclient.SharedSession. Close unregisters the reply route without closing session.
func NewSharedMQTTTransport(_ context.Context, session *mqttclient.SharedSession, cfg TransportConfig) (*MQTTTransport, error) {
	if session == nil {
		return nil, errors.New("concentrator shared MQTT session is required")
	}
	transport := newBaseTransport(context.Background(), cfg, nil)
	transport.sharedSession = session

	routeID := fmt.Sprintf("concentrator-rpc-%d", sharedRouteCounter.Add(1))
	unregister, err := session.RegisterRoute(routeID, []string{ReplyTopicFilter}, func(pkt *paho.Publish) {
		_ = transport.handleReplyPacket(pkt)
	})
	if err != nil {
		transport.stopCancel()
		return nil, err
	}
	transport.unregisterRoute = unregister

	transport.removeObserver = session.AddObserver(func(state mqttclient.SessionState) {
		transport.subMu.Lock()
		active := !transport.closed && state.Healthy()
		transport.subscribed = active
		transport.subMu.Unlock()
		if active {
			transport.markReady()
		}
	})
	return transport, nil
}

func newMQTTTransport(ctx context.Context, cfg TransportConfig, reconnectBackoff func(int) time.Duration, subscribeBackoffs []time.Duration) (*MQTTTransport, error) {
	if err := validateTransportConfig(cfg); err != nil {
		return nil, err
	}
	brokerURL, err := url.Parse(strings.TrimSpace(cfg.BrokerURL))
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	if reconnectBackoff == nil {
		reconnectBackoff = autopaho.DefaultExponentialBackoff()
	}

	transport := newBaseTransport(ctx, cfg, subscribeBackoffs)

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ConnectTimeout:                mqttConnectTimeout,
		ReconnectBackoff:              reconnectBackoff,
		AttemptConnection: func(attemptCtx context.Context, _ autopaho.ClientConfig, server *url.URL) (net.Conn, error) {
			return transport.dial(attemptCtx, tlsConfig, server)
		},
		OnConnectError: func(connectErr error) {
			transport.logger.Warn("concentrator_mqtt_connect_failed", "error", connectErr.Error())
		},
		ClientConfig: paho.ClientConfig{
			ClientID: strings.TrimSpace(cfg.ClientID),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				transport.onReply,
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		gen := transport.connTracker.CurrentGen()
		connection := transport.connTracker.CurrentClosed()
		transport.subMu.Lock()
		if transport.subCancel != nil {
			transport.subCancel()
		}
		subCtx, subCancel := context.WithCancel(transport.runContext)
		transport.subCancel = subCancel
		transport.subGen = gen
		transport.subscribed = false
		transport.subMu.Unlock()

		transport.subWG.Add(1)
		go func() {
			defer transport.subWG.Done()
			defer subCancel()
			transport.subscribeWithRetry(subCtx, manager, connection, gen)
		}()
	}

	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		transport.stopCancel()
		return nil, fmt.Errorf("create concentrator MQTT connection: %w", err)
	}
	transport.manager = manager
	return transport, nil
}

func (t *MQTTTransport) dial(ctx context.Context, tlsConfig *tls.Config, server *url.URL) (net.Conn, error) {
	conn, gen, _, err := t.connTracker.DialTLS(ctx, tlsConfig, server.Host, mqttConnectTimeout, t.onConnectionClosed)
	if err != nil {
		return nil, err
	}
	t.subMu.Lock()
	t.subGen = gen
	t.subscribed = false
	t.subMu.Unlock()
	return conn, nil
}

func (t *MQTTTransport) onConnectionClosed(gen uint64) {
	t.subMu.Lock()
	var cancel context.CancelFunc
	if t.subGen == gen {
		t.subscribed = false
		cancel = t.subCancel
		t.subCancel = nil
	}
	t.subMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *MQTTTransport) closeCurrentConnection() {
	t.subMu.Lock()
	t.subscribed = false
	t.subMu.Unlock()
	_ = t.connTracker.CloseCurrent()
}

func (t *MQTTTransport) setSubscribedForGen(gen uint64, connection <-chan struct{}, subscribed bool) bool {
	if mqttclient.IsClosedSignal(connection) {
		return false
	}
	t.subMu.Lock()
	defer t.subMu.Unlock()
	if t.connTracker.CurrentGen() != gen || t.subGen != gen || mqttclient.IsClosedSignal(t.connTracker.CurrentClosed()) {
		return false
	}
	t.subscribed = subscribed
	return true
}

func (t *MQTTTransport) Subscribed() bool {
	if t == nil {
		return false
	}
	t.subMu.Lock()
	defer t.subMu.Unlock()
	return t.subscribed
}

func (t *MQTTTransport) subscribeWithRetry(ctx context.Context, manager *autopaho.ConnectionManager, connection <-chan struct{}, gen uint64) {
	backoffs := t.subscribeBackoffs
	if backoffs == nil {
		backoffs = mqttclient.DefaultSubscribeRetryDelays
	}
	sub := &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: ReplyTopicFilter, QoS: 1}},
	}
	if err := mqttclient.SubscribeWithRetry(
		ctx,
		manager,
		sub,
		mqttConnectTimeout,
		backoffs,
		func() bool { return mqttclient.IsClosedSignal(connection) },
	); err != nil {
		if ctx.Err() != nil || mqttclient.IsClosedSignal(connection) {
			return
		}
		t.setSubscribedForGen(gen, connection, false)
		t.logger.Warn("concentrator_mqtt_subscribe_failed", "error", err.Error())
		if t.connTracker.CurrentGen() == gen {
			t.closeCurrentConnection()
		}
		return
	}
	if t.setSubscribedForGen(gen, connection, true) {
		t.markReady()
	}
}

func (t *MQTTTransport) markReady() {
	t.readyMu.Lock()
	if !t.readyOnce {
		t.readyOnce = true
		close(t.readyCh)
	}
	t.readyMu.Unlock()
}

func (t *MQTTTransport) WaitReady(ctx context.Context) error {
	if t == nil {
		return ErrTransportUnavailable
	}
	select {
	case <-t.readyCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrTransportUnavailable, ctx.Err())
	}
}

func (t *MQTTTransport) recordPacket(direction, channel, gatewayID, topic, traceID string, payload []byte) {
	if t == nil || t.packetHook == nil {
		return
	}
	t.packetHook(direction, channel, gatewayID, topic, traceID, payload)
}

func (t *MQTTTransport) recordRPCOutcome(outcome string, started time.Time) {
	if t == nil || t.metrics == nil {
		return
	}
	labels := map[string]string{"outcome": outcome}
	_ = t.metrics.AddCounter("hvac_mqtt_concentrator_rpc_total", "Concentrator MQTT RPC calls by outcome.", labels, 1)
	_ = t.metrics.ObserveHistogram("hvac_mqtt_concentrator_rpc_duration_seconds", "Concentrator MQTT RPC duration.", labels, time.Since(started).Seconds(), nil)
}

func (t *MQTTTransport) publish(ctx context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	if t.sharedSession != nil {
		return t.sharedSession.Publish(ctx, pub)
	}
	return t.manager.Publish(ctx, pub)
}

func (t *MQTTTransport) Call(ctx context.Context, req RPCRequest) (RPCResponse, error) {
	started := time.Now()
	if t == nil || (t.manager == nil && t.sharedSession == nil) {
		t.recordRPCOutcome("failed", started)
		return RPCResponse{}, ErrTransportUnavailable
	}
	if ctx.Err() != nil {
		t.recordRPCOutcome("timed_out", started)
		return RPCResponse{}, ErrRPCTimeout
	}
	timeout := t.timeout
	if timeout <= 0 {
		timeout = defaultRPCTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.recordRPCOutcome("timed_out", started)
			return RPCResponse{}, ErrRPCTimeout
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	if !t.Subscribed() {
		t.recordRPCOutcome("failed", started)
		return RPCResponse{}, fmt.Errorf("%w: reply subscription inactive", ErrTransportUnavailable)
	}
	req.ExpireAt = time.Now().UTC().Add(timeout).UnixMilli()
	payload, err := json.Marshal(req)
	if err != nil {
		t.recordRPCOutcome("failed", started)
		return RPCResponse{}, fmt.Errorf("encode concentrator RPC request: %w", err)
	}

	traceID := observability.TraceID(ctx)
	key := waiterKey{gatewayID: req.GatewayID, requestID: req.RequestID}
	ch := make(chan RPCResponse, 1)
	t.mu.Lock()
	if t.waiters == nil {
		t.waiters = map[waiterKey]chan RPCResponse{}
	}
	if t.waiterTraces == nil {
		t.waiterTraces = map[waiterKey]string{}
	}
	t.waiters[key] = ch
	if traceID != "" {
		t.waiterTraces[key] = traceID
	}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.waiters, key)
		delete(t.waiterTraces, key)
		t.mu.Unlock()
	}()

	topic := DownlinkTopic(req.GatewayID)
	expirySeconds := concentratorMessageExpirySeconds(timeout)
	t.recordPacket("DOWNLINK", "CONCENTRATOR", req.GatewayID, topic, traceID, payload)
	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	_, pubErr := t.publish(pubCtx, &paho.Publish{
		QoS:        1,
		Retain:     false,
		Topic:      topic,
		Payload:    payload,
		Properties: &paho.PublishProperties{MessageExpiry: &expirySeconds},
	})
	pubCancel()
	if pubErr != nil {
		t.recordRPCOutcome("failed", started)
		return RPCResponse{}, fmt.Errorf("%w: %v", ErrTransportUnavailable, pubErr)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		t.recordRPCOutcome("success", started)
		return resp, nil
	case <-timer.C:
		t.recordRPCOutcome("timed_out", started)
		return RPCResponse{}, ErrRPCTimeout
	case <-ctx.Done():
		t.recordRPCOutcome("timed_out", started)
		return RPCResponse{}, ErrRPCTimeout
	}
}

func concentratorMessageExpirySeconds(timeout time.Duration) uint32 {
	seconds := math.Ceil(timeout.Seconds())
	if seconds < 1 {
		return 1
	}
	if seconds > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(seconds)
}

func (t *MQTTTransport) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.subMu.Lock()
	t.closed = true
	t.subscribed = false
	t.subMu.Unlock()

	if t.removeObserver != nil {
		t.removeObserver()
	}
	if t.stopCancel != nil {
		t.stopCancel()
	}
	var err error
	if t.unregisterRoute != nil {
		err = t.unregisterRoute(ctx)
	}
	if t.manager != nil {
		disconnectErr := t.manager.Disconnect(ctx)
		if err == nil {
			err = disconnectErr
		}
	}
	t.subWG.Wait()
	return err
}

func (t *MQTTTransport) onReply(received paho.PublishReceived) (bool, error) {
	if !t.handleReplyPacket(received.Packet) {
		return false, nil
	}
	return true, nil
}

func (t *MQTTTransport) handleReplyPacket(pkt *paho.Publish) bool {
	if pkt == nil {
		return false
	}
	if gatewayID, ok := ParseReplyTopic(pkt.Topic); ok {
		traceID := t.lookupReplyTraceID(gatewayID, pkt.Payload)
		t.recordPacket("UPLINK", "CONCENTRATOR", gatewayID, pkt.Topic, traceID, pkt.Payload)
	}
	return t.DeliverReply(pkt.Topic, pkt.Payload)
}

func (t *MQTTTransport) lookupReplyTraceID(gatewayID string, payload []byte) string {
	if t == nil || t.packetHook == nil || len(payload) == 0 || len(payload) > maxReplyPayloadSize {
		return ""
	}
	var header struct {
		GatewayID string `json:"gatewayId"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return ""
	}
	if header.GatewayID != gatewayID || !uuidV7Pattern.MatchString(header.RequestID) {
		return ""
	}
	t.mu.Lock()
	traceID := t.waiterTraces[waiterKey{gatewayID: gatewayID, requestID: header.RequestID}]
	t.mu.Unlock()
	return traceID
}

func (t *MQTTTransport) DeliverReply(topic string, payload []byte) bool {
	gatewayID, ok := ParseReplyTopic(topic)
	if !ok || len(payload) == 0 || len(payload) > maxReplyPayloadSize {
		return false
	}
	var resp RPCResponse
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resp); err != nil {
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return false
	}
	if resp.SchemaVersion != rpcSchemaVersion || resp.GatewayID != gatewayID || !uuidV7Pattern.MatchString(resp.RequestID) {
		return false
	}
	key := waiterKey{gatewayID: gatewayID, requestID: resp.RequestID}
	t.mu.Lock()
	ch := t.waiters[key]
	t.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- resp:
	default:
	}
	return true
}

func validateTransportConfig(cfg TransportConfig) error {
	broker, err := url.Parse(strings.TrimSpace(cfg.BrokerURL))
	if err != nil || broker.Scheme != "tls" || broker.Host == "" || broker.User != nil {
		return errors.New("concentrator MQTT broker URL must be tls://")
	}
	for label, val := range map[string]string{
		"client id":   cfg.ClientID,
		"CA file":     cfg.CAFile,
		"cert file":   cfg.CertFile,
		"key file":    cfg.KeyFile,
		"server name": cfg.ServerName,
	} {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("concentrator MQTT %s is required", label)
		}
	}
	return nil
}

func buildTLSConfig(cfg TransportConfig) (*tls.Config, error) {
	return mqttclient.NewMTLSConfig(mqttclient.TLSConfig{
		CACertFile:     cfg.CAFile,
		ClientCertFile: cfg.CertFile,
		ClientKeyFile:  cfg.KeyFile,
		ServerName:     strings.TrimSpace(cfg.ServerName),
	})
}
