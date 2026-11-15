package concentrator

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const (
	rpcSchemaVersion    = "1.0"
	defaultRPCTimeout   = 18 * time.Second
	ReplyTopicFilter    = "hvac/v1/+/up/concentrator"
	maxReplyPayloadSize = 256 << 10
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

type MQTTTransport struct {
	rootContext context.Context
	timeout     time.Duration
	logger      *slog.Logger
	manager     *autopaho.ConnectionManager

	readyMu   sync.Mutex
	readyOnce bool
	readyCh   chan struct{}

	mu      sync.Mutex
	waiters map[waiterKey]chan RPCResponse
}

func NewMQTTTransport(ctx context.Context, cfg TransportConfig) (*MQTTTransport, error) {
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
	timeout := cfg.RPCTimeout
	if timeout <= 0 {
		timeout = defaultRPCTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	transport := &MQTTTransport{
		rootContext: ctx,
		timeout:     timeout,
		logger:      logger,
		readyCh:     make(chan struct{}),
		waiters:     map[waiterKey]chan RPCResponse{},
	}

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		OnConnectError: func(connectErr error) {
			logger.Warn("concentrator_mqtt_connect_failed", "error", connectErr.Error())
		},
		ClientConfig: paho.ClientConfig{
			ClientID: strings.TrimSpace(cfg.ClientID),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				transport.onReply,
			},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, subErr := manager.Subscribe(subCtx, &paho.Subscribe{
			Subscriptions: []paho.SubscribeOptions{{Topic: ReplyTopicFilter, QoS: 1}},
		}); subErr != nil {
			logger.Warn("concentrator_mqtt_subscribe_failed", "error", subErr.Error())
			return
		}
		transport.markReady()
	}

	manager, err := autopaho.NewConnection(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("create concentrator MQTT connection: %w", err)
	}
	transport.manager = manager
	return transport, nil
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

func (t *MQTTTransport) Call(ctx context.Context, req RPCRequest) (RPCResponse, error) {
	if t == nil || t.manager == nil {
		return RPCResponse{}, ErrTransportUnavailable
	}
	timeout := t.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	req.ExpireAt = time.Now().UTC().Add(timeout).UnixMilli()
	payload, err := json.Marshal(req)
	if err != nil {
		return RPCResponse{}, fmt.Errorf("encode concentrator RPC request: %w", err)
	}

	key := waiterKey{gatewayID: req.GatewayID, requestID: req.RequestID}
	ch := make(chan RPCResponse, 1)
	t.mu.Lock()
	t.waiters[key] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.waiters, key)
		t.mu.Unlock()
	}()

	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	_, pubErr := t.manager.Publish(pubCtx, &paho.Publish{
		QoS:     1,
		Retain:  false,
		Topic:   DownlinkTopic(req.GatewayID),
		Payload: payload,
	})
	pubCancel()
	if pubErr != nil {
		return RPCResponse{}, fmt.Errorf("%w: %v", ErrTransportUnavailable, pubErr)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp, nil
	case <-timer.C:
		return RPCResponse{}, ErrRPCTimeout
	case <-ctx.Done():
		return RPCResponse{}, ErrRPCTimeout
	}
}

func (t *MQTTTransport) Close(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	return t.manager.Disconnect(ctx)
}

func (t *MQTTTransport) onReply(received paho.PublishReceived) (bool, error) {
	if received.Packet == nil {
		return false, nil
	}
	if !t.DeliverReply(received.Packet.Topic, received.Packet.Payload) {
		return false, nil
	}
	return true, nil
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
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load concentrator MQTT client cert: %w", err)
	}
	caBytes, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read concentrator MQTT CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("invalid concentrator MQTT CA")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{cert},
		ServerName:   strings.TrimSpace(cfg.ServerName),
	}, nil
}
