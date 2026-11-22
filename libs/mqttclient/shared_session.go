package mqttclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

const (
	defaultKeepAliveSeconds = 30
	defaultDialTimeout      = 10 * time.Second
	defaultRouteQoS         = byte(1)
)

var (
	// ErrSessionClosed indicates the SharedSession has been closed.
	ErrSessionClosed = errors.New("mqtt shared session is closed")
	// ErrSessionNotConnected indicates an operation requires an active MQTT connection.
	ErrSessionNotConnected = errors.New("mqtt shared session is not connected")
)

// SessionState captures the health and route count of a SharedSession.
type SessionState struct {
	Generation   uint64
	Connected    bool
	Subscribed   bool
	LastError    string
	LastSuccess  time.Time
	ActiveRoutes int
	Closed       bool
}

// Healthy reports whether the session is open, connected, subscribed, and error-free.
func (s SessionState) Healthy() bool {
	return !s.Closed && s.Connected && s.Subscribed && s.LastError == ""
}

// PublishEvent carries an incoming MQTT PUBLISH packet together with the physical
// connection close signal and manual ACK function for custody-before-ACK consumers.
type PublishEvent struct {
	Packet           *paho.Publish
	ConnectionClosed <-chan struct{}
	Ack              func(*paho.Publish) error
}

// SharedSessionConfig configures a multiplexed MQTT 5 session managed by SharedSession.
type SharedSessionConfig struct {
	BrokerURL                     string
	ClientID                      string
	TLS                           *TLSConfig
	TLSConfig                     *tls.Config
	KeepAlive                     uint16
	ReceiveMaximum                uint16
	CleanStartOnInitialConnection bool
	SessionExpiryInterval         uint32
	DialTimeout                   time.Duration
	ConnectRetryDelay             time.Duration
	ReconnectBackoff              func(int) time.Duration
	SubscribeAttemptTimeout       time.Duration
	SubscribeRetryDelays          []time.Duration
	ConnectUsername               string
	ConnectPassword               []byte
	WillMessage                   *paho.WillMessage
	ConnectPacketBuilder          func(*paho.Connect, *url.URL) (*paho.Connect, error)
	OnConnectSuccess              func()
	OnConnectError                func(error)
	OnSubscribeError              func(error)
	OnPublishAckError             func(error)
}

type sessionRoute struct {
	id      string
	filters []string
	qos     byte
	auto    func(*paho.Publish)
	managed func(PublishEvent) error
}

// SharedSession owns a single autopaho.ConnectionManager and multiplexes topic
// subscriptions and incoming PUBLISH routing across multiple in-process consumers.
type SharedSession struct {
	cfg     SharedSessionConfig
	tracker *ConnTracker

	sessionCtx    context.Context
	sessionCancel context.CancelFunc
	manager       *autopaho.ConnectionManager

	subMu                  sync.Mutex
	initialResubscribedGen uint64
	subCancelMu            sync.Mutex
	subCancel              context.CancelFunc
	subWG                  sync.WaitGroup

	routesMu sync.RWMutex
	routes   map[string]*sessionRoute

	closeMu   sync.Mutex
	closed    bool
	closeOnce sync.Once
}

// NewSharedSession connects a single autopaho.ConnectionManager and prepares route
// multiplexing across in-process consumers.
func NewSharedSession(ctx context.Context, cfg SharedSessionConfig) (*SharedSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	brokerRaw := strings.TrimSpace(cfg.BrokerURL)
	if brokerRaw == "" {
		return nil, errors.New("mqtt broker URL is required")
	}
	brokerURL, err := url.Parse(brokerRaw)
	if err != nil {
		return nil, fmt.Errorf("parse mqtt broker URL: %w", err)
	}
	if strings.TrimSpace(brokerURL.Host) == "" {
		return nil, errors.New("mqtt broker URL host is required")
	}

	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		return nil, errors.New("mqtt client ID is required")
	}

	var tlsCfg *tls.Config
	switch {
	case cfg.TLSConfig != nil:
		tlsCfg = cfg.TLSConfig.Clone()
		if tlsCfg.MinVersion == 0 {
			tlsCfg.MinVersion = tls.VersionTLS13
		}
	case cfg.TLS != nil:
		tlsCfg, err = NewMTLSConfig(*cfg.TLS)
		if err != nil {
			return nil, err
		}
	}

	keepAlive := cfg.KeepAlive
	if keepAlive == 0 {
		keepAlive = defaultKeepAliveSeconds
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}
	reconnectBackoff := cfg.ReconnectBackoff
	if reconnectBackoff == nil {
		if cfg.ConnectRetryDelay > 0 {
			delay := cfg.ConnectRetryDelay
			reconnectBackoff = func(int) time.Duration { return delay }
		} else {
			reconnectBackoff = autopaho.DefaultExponentialBackoff()
		}
	}
	subAttemptTimeout := cfg.SubscribeAttemptTimeout
	if subAttemptTimeout <= 0 {
		subAttemptTimeout = DefaultSubscribeAttemptTimeout
	}
	subRetryDelays := cfg.SubscribeRetryDelays
	if subRetryDelays == nil {
		subRetryDelays = slices.Clone(DefaultSubscribeRetryDelays)
	} else {
		subRetryDelays = slices.Clone(subRetryDelays)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	s := &SharedSession{
		cfg:           cfg,
		tracker:       NewConnTracker(),
		sessionCtx:    sessionCtx,
		sessionCancel: sessionCancel,
		routes:        make(map[string]*sessionRoute),
	}
	s.cfg.KeepAlive = keepAlive
	s.cfg.DialTimeout = dialTimeout
	s.cfg.ReconnectBackoff = reconnectBackoff
	s.cfg.SubscribeAttemptTimeout = subAttemptTimeout
	s.cfg.SubscribeRetryDelays = subRetryDelays

	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     keepAlive,
		CleanStartOnInitialConnection: cfg.CleanStartOnInitialConnection,
		SessionExpiryInterval:         cfg.SessionExpiryInterval,
		ConnectTimeout:                dialTimeout,
		ReconnectBackoff:              reconnectBackoff,
		ConnectUsername:               cfg.ConnectUsername,
		ConnectPassword:               bytesClone(cfg.ConnectPassword),
		WillMessage:                   cfg.WillMessage,
		ConnectPacketBuilder: func(cp *paho.Connect, u *url.URL) (*paho.Connect, error) {
			if cfg.ReceiveMaximum > 0 {
				if cp.Properties == nil {
					cp.Properties = &paho.ConnectProperties{}
				}
				rm := cfg.ReceiveMaximum
				cp.Properties.ReceiveMaximum = &rm
			}
			if cfg.ConnectPacketBuilder != nil {
				return cfg.ConnectPacketBuilder(cp, u)
			}
			return cp, nil
		},
		AttemptConnection: func(attemptCtx context.Context, _ autopaho.ClientConfig, server *url.URL) (net.Conn, error) {
			conn, _, _, err := s.tracker.DialBrokerURL(attemptCtx, server, dialTimeout, tlsCfg, nil)
			return conn, err
		},
		OnConnectError: func(connectErr error) {
			s.tracker.MarkConnectError(connectErr)
			if s.cfg.OnConnectError != nil && connectErr != nil {
				s.cfg.OnConnectError(connectErr)
			}
		},
		OnConnectionDown: func() bool {
			if s.isClosed() || s.sessionCtx.Err() != nil {
				s.tracker.OnConnectionDown(nil)
				return false
			}
			s.tracker.OnConnectionDown(errors.New("mqtt connection lost"))
			return true
		},
		OnConnectionUp: func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
			s.handleConnectionUp(manager)
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   clientID,
			EnableManualAcknowledgment: true,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				s.onPublishReceived,
			},
			OnClientError: func(clientErr error) {
				if !s.isClosed() {
					s.tracker.RecordError(clientErr)
				}
			},
		},
	}

	manager, err := autopaho.NewConnection(sessionCtx, clientConfig)
	if err != nil {
		sessionCancel()
		return nil, fmt.Errorf("create shared MQTT connection: %w", err)
	}
	s.manager = manager
	return s, nil
}

// AwaitConnection blocks until the session establishes an active MQTT connection,
// ctx expires, or the session closes.
func (s *SharedSession) AwaitConnection(ctx context.Context) error {
	if s == nil || s.manager == nil {
		return ErrSessionClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.manager.AwaitConnection(ctx); err != nil {
		if s.isClosed() && ctx.Err() == nil {
			return ErrSessionClosed
		}
		return err
	}
	return nil
}

// AwaitSubscribed blocks until the session is connected, all active routes are
// subscribed, and LastError is empty, or until ctx expires or session closes.
func (s *SharedSession) AwaitSubscribed(ctx context.Context) error {
	if s == nil {
		return ErrSessionClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.State().Healthy() {
		return nil
	}

	ready := make(chan struct{})
	var once sync.Once
	remove := s.AddObserver(func(state SessionState) {
		if state.Closed || state.Healthy() {
			once.Do(func() { close(ready) })
		}
	})
	defer remove()

	if s.State().Healthy() {
		return nil
	}

	select {
	case <-ready:
		if s.isClosed() {
			return ErrSessionClosed
		}
		return nil
	case <-s.Done():
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RegisterRoute subscribes to filters (if not already covered) and routes matching
// incoming packets to handler with automatic QoS 1 acknowledgment after delivery.
func (s *SharedSession) RegisterRoute(
	id string,
	filters []string,
	handler func(*paho.Publish),
) (func(context.Context) error, error) {
	if handler == nil {
		return nil, errors.New("mqtt route handler is required")
	}
	return s.registerRoute(id, filters, handler, nil)
}

// RegisterManagedRoute subscribes to filters and delegates QoS 1 manual
// acknowledgment to handler. Returning a non-nil error closes the active connection
// without acknowledging so the broker redelivers the message.
func (s *SharedSession) RegisterManagedRoute(
	id string,
	filters []string,
	handler func(PublishEvent) error,
) (func(context.Context) error, error) {
	if handler == nil {
		return nil, errors.New("mqtt managed route handler is required")
	}
	return s.registerRoute(id, filters, nil, handler)
}

func (s *SharedSession) registerRoute(
	id string,
	filters []string,
	auto func(*paho.Publish),
	managed func(PublishEvent) error,
) (func(context.Context) error, error) {
	if s == nil {
		return nil, ErrSessionClosed
	}
	routeID := strings.TrimSpace(id)
	if routeID == "" {
		return nil, errors.New("mqtt route id is required")
	}
	cleanFilters, err := normalizeRouteFilters(filters)
	if err != nil {
		return nil, err
	}

	s.subMu.Lock()
	defer s.subMu.Unlock()

	if s.isClosed() || s.sessionCtx.Err() != nil {
		return nil, ErrSessionClosed
	}

	s.routesMu.Lock()
	if _, exists := s.routes[routeID]; exists {
		s.routesMu.Unlock()
		return nil, fmt.Errorf("mqtt shared session route %q is already registered", routeID)
	}
	route := &sessionRoute{
		id:      routeID,
		filters: cleanFilters,
		qos:     defaultRouteQoS,
		auto:    auto,
		managed: managed,
	}
	s.routes[routeID] = route
	s.routesMu.Unlock()

	state := s.tracker.State()
	if s.manager != nil && state.Connected {
		gen := state.Generation
		sub := buildSubscribePacket(cleanFilters, defaultRouteQoS)
		closedFn := func() bool {
			return IsClosedSignal(s.tracker.ClosedChanForGen(gen))
		}
		if err := SubscribeWithRetry(
			s.sessionCtx,
			s.manager,
			sub,
			s.cfg.SubscribeAttemptTimeout,
			s.cfg.SubscribeRetryDelays,
			closedFn,
		); err != nil {
			s.routesMu.Lock()
			delete(s.routes, routeID)
			s.routesMu.Unlock()

			if s.tracker.MarkSubscribeError(gen, err) {
				if s.cfg.OnSubscribeError != nil {
					s.cfg.OnSubscribeError(err)
				}
			}
			return nil, err
		}
		if s.initialResubscribedGen == gen {
			s.tracker.MarkSubscribed(gen)
		}
	}

	var unregOnce sync.Once
	var unregErr error
	unregister := func(ctx context.Context) error {
		unregOnce.Do(func() {
			unregErr = s.unregisterRoute(ctx, routeID)
		})
		return unregErr
	}
	return unregister, nil
}

func (s *SharedSession) unregisterRoute(ctx context.Context, routeID string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.subMu.Lock()
	defer s.subMu.Unlock()

	s.routesMu.Lock()
	removed, exists := s.routes[routeID]
	if !exists {
		s.routesMu.Unlock()
		return nil
	}
	delete(s.routes, routeID)

	stillUsed := make(map[string]struct{})
	for _, r := range s.routes {
		for _, f := range r.filters {
			stillUsed[f] = struct{}{}
		}
	}
	var orphans []string
	for _, f := range removed.filters {
		if _, ok := stillUsed[f]; !ok {
			orphans = append(orphans, f)
		}
	}
	s.routesMu.Unlock()

	if len(orphans) == 0 || s.manager == nil || s.isClosed() || s.sessionCtx.Err() != nil {
		return nil
	}
	if !s.tracker.State().Connected {
		return nil
	}
	if _, err := s.manager.Unsubscribe(ctx, &paho.Unsubscribe{Topics: orphans}); err != nil {
		if !s.tracker.State().Connected || s.isClosed() || s.sessionCtx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// Publish sends an MQTT PUBLISH packet over the shared session connection.
func (s *SharedSession) Publish(ctx context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	if s == nil || s.manager == nil || s.isClosed() {
		return nil, ErrSessionClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := s.manager.Publish(ctx, pub)
	if err != nil {
		return resp, err
	}
	s.tracker.RecordSuccess()
	return resp, nil
}

// State returns a snapshot of the session health and registered route count.
func (s *SharedSession) State() SessionState {
	if s == nil {
		return SessionState{Closed: true}
	}
	return s.toSessionState(s.tracker.State())
}

// AddObserver registers a state observer invoked immediately and on each transition.
func (s *SharedSession) AddObserver(fn func(SessionState)) func() {
	if s == nil || fn == nil {
		return func() {}
	}
	return s.tracker.AddObserver(func(cs ConnState) {
		fn(s.toSessionState(cs))
	})
}

// Done returns the underlying autopaho connection manager Done channel.
func (s *SharedSession) Done() <-chan struct{} {
	if s == nil || s.manager == nil {
		return closedSignalChan
	}
	return s.manager.Done()
}

// CurrentClosed returns the close signal channel of the currently active socket.
func (s *SharedSession) CurrentClosed() <-chan struct{} {
	if s == nil || s.tracker == nil {
		return nil
	}
	return s.tracker.CurrentClosed()
}

// CloseCurrentConnection tears down the active socket so the broker redelivers
// any unacknowledged QoS 1 messages on reconnect.
func (s *SharedSession) CloseCurrentConnection() error {
	if s == nil || s.tracker == nil {
		return nil
	}
	return s.tracker.CloseCurrent()
}

// Close unsubscribes/disconnects the shared session and waits for background subscribe
// goroutines to finish. Close is safe to call multiple times.
func (s *SharedSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.closeMu.Lock()
		s.closed = true
		s.closeMu.Unlock()

		s.subCancelMu.Lock()
		if s.subCancel != nil {
			s.subCancel()
		}
		s.subCancelMu.Unlock()

		if s.manager != nil {
			// Disconnect before canceling sessionCtx so Disconnect can transmit DISCONNECT.
			closeErr = s.manager.Disconnect(ctx)
		}
		s.sessionCancel()
		_ = s.tracker.CloseCurrent()
		s.subWG.Wait()
	})
	return closeErr
}

func (s *SharedSession) isClosed() bool {
	if s == nil {
		return true
	}
	s.closeMu.Lock()
	closed := s.closed
	s.closeMu.Unlock()
	if closed {
		return true
	}
	return s.sessionCtx != nil && s.sessionCtx.Err() != nil
}

func (s *SharedSession) toSessionState(cs ConnState) SessionState {
	s.routesMu.RLock()
	activeRoutes := len(s.routes)
	s.routesMu.RUnlock()

	return SessionState{
		Generation:   cs.Generation,
		Connected:    cs.Connected,
		Subscribed:   cs.Subscribed,
		LastError:    cs.LastError,
		LastSuccess:  cs.LastSuccess,
		ActiveRoutes: activeRoutes,
		Closed:       s.isClosed(),
	}
}

func (s *SharedSession) handleConnectionUp(manager *autopaho.ConnectionManager) {
	if s.isClosed() {
		return
	}
	gen := s.tracker.OnConnectUp()
	if s.cfg.OnConnectSuccess != nil {
		s.cfg.OnConnectSuccess()
	}

	s.subCancelMu.Lock()
	if s.subCancel != nil {
		s.subCancel()
	}
	subCtx, subCancel := context.WithCancel(s.sessionCtx)
	s.subCancel = subCancel
	s.subCancelMu.Unlock()

	s.closeMu.Lock()
	if s.closed || (s.sessionCtx != nil && s.sessionCtx.Err() != nil) {
		s.closeMu.Unlock()
		subCancel()
		return
	}
	s.subWG.Add(1)
	s.closeMu.Unlock()

	go func() {
		defer s.subWG.Done()
		defer subCancel()
		defer observability.Recover(slog.Default(), nil, "mqtt_shared_session_resubscribe")
		s.resubscribeAll(subCtx, manager, gen)
	}()
}

func (s *SharedSession) resubscribeAll(ctx context.Context, manager *autopaho.ConnectionManager, gen uint64) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	if ctx.Err() != nil || s.isClosed() || s.tracker.CurrentGen() != gen {
		return
	}

	filters, qosByFilter := s.snapshotAllFilters()
	if len(filters) > 0 {
		subs := make([]paho.SubscribeOptions, 0, len(filters))
		for _, filter := range filters {
			subs = append(subs, paho.SubscribeOptions{
				Topic: filter,
				QoS:   qosByFilter[filter],
			})
		}
		closedFn := func() bool {
			return IsClosedSignal(s.tracker.ClosedChanForGen(gen))
		}
		if err := SubscribeWithRetry(
			ctx,
			manager,
			&paho.Subscribe{Subscriptions: subs},
			s.cfg.SubscribeAttemptTimeout,
			s.cfg.SubscribeRetryDelays,
			closedFn,
		); err != nil {
			if ctx.Err() == nil && !s.isClosed() {
				if s.tracker.MarkSubscribeError(gen, err) && s.cfg.OnSubscribeError != nil {
					s.cfg.OnSubscribeError(err)
				}
				_ = s.tracker.CloseIfGen(gen)
			}
			return
		}
	}

	if s.tracker.MarkSubscribed(gen) {
		s.initialResubscribedGen = gen
	}
}

func (s *SharedSession) snapshotAllFilters() ([]string, map[string]byte) {
	s.routesMu.RLock()
	defer s.routesMu.RUnlock()

	if len(s.routes) == 0 {
		return nil, nil
	}
	qosByFilter := make(map[string]byte)
	filters := make([]string, 0, len(s.routes)*2)
	for _, r := range s.routes {
		for _, f := range r.filters {
			existingQoS, seen := qosByFilter[f]
			if !seen {
				filters = append(filters, f)
				qosByFilter[f] = r.qos
			} else if r.qos > existingQoS {
				qosByFilter[f] = r.qos
			}
		}
	}
	return filters, qosByFilter
}

func (s *SharedSession) onPublishReceived(received paho.PublishReceived) (bool, error) {
	if received.Packet == nil || received.Client == nil {
		return false, errors.New("mqtt publish callback is incomplete")
	}

	s.routesMu.RLock()
	var matchedAuto []func(*paho.Publish)
	var matchedManaged []func(PublishEvent) error
	for _, r := range s.routes {
		if routeMatches(r.filters, received.Packet.Topic) {
			if r.managed != nil {
				matchedManaged = append(matchedManaged, r.managed)
			}
			if r.auto != nil {
				matchedAuto = append(matchedAuto, r.auto)
			}
		}
	}
	s.routesMu.RUnlock()

	totalMatched := len(matchedAuto) + len(matchedManaged)
	if totalMatched == 0 {
		s.ackBestEffort(received.Client, received.Packet)
		return true, nil
	}

	closedCh := s.tracker.CurrentClosed()
	for _, handler := range matchedManaged {
		pkt := received.Packet
		if totalMatched > 1 {
			pkt = clonePublishPacket(pkt)
		}
		if err := handler(PublishEvent{
			Packet:           pkt,
			ConnectionClosed: closedCh,
			Ack:              received.Client.Ack,
		}); err != nil {
			_ = s.tracker.CloseIfClosed(closedCh)
			return true, err
		}
	}

	for _, handler := range matchedAuto {
		pkt := received.Packet
		if totalMatched > 1 {
			pkt = clonePublishPacket(pkt)
		}
		handler(pkt)
	}

	if len(matchedManaged) == 0 {
		s.ackBestEffort(received.Client, received.Packet)
	}
	return true, nil
}

func (s *SharedSession) ackBestEffort(client *paho.Client, pkt *paho.Publish) {
	if client == nil || pkt == nil || pkt.QoS == 0 {
		return
	}
	if err := client.Ack(pkt); err != nil && !errors.Is(err, paho.ErrManualAcknowledgmentDisabled) {
		if s.cfg.OnPublishAckError != nil {
			s.cfg.OnPublishAckError(err)
		}
	}
}

func normalizeRouteFilters(filters []string) ([]string, error) {
	if len(filters) == 0 {
		return nil, errors.New("mqtt route requires at least one topic filter")
	}
	seen := make(map[string]struct{}, len(filters))
	out := make([]string, 0, len(filters))
	for _, raw := range filters {
		f := strings.TrimSpace(raw)
		if f == "" {
			return nil, errors.New("mqtt route topic filter must not be empty")
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out, nil
}

func buildSubscribePacket(filters []string, qos byte) *paho.Subscribe {
	subs := make([]paho.SubscribeOptions, 0, len(filters))
	for _, f := range filters {
		subs = append(subs, paho.SubscribeOptions{
			Topic: f,
			QoS:   qos,
		})
	}
	return &paho.Subscribe{Subscriptions: subs}
}

func routeMatches(filters []string, topic string) bool {
	for _, f := range filters {
		if MatchTopicFilter(f, topic) {
			return true
		}
	}
	return false
}

func clonePublishPacket(pkt *paho.Publish) *paho.Publish {
	if pkt == nil {
		return nil
	}
	copied := *pkt
	if pkt.Payload != nil {
		copied.Payload = slices.Clone(pkt.Payload)
	}
	return &copied
}

func bytesClone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return slices.Clone(b)
}
