package mqttclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/packets"
)

var closedSignalChan = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// ConnState captures a snapshot of a generation-fenced MQTT connection lifecycle.
type ConnState struct {
	Generation  uint64
	Connected   bool
	Subscribed  bool
	LastError   string
	LastSuccess time.Time
}

// Healthy reports whether the connection is up, subscribed, and error-free.
func (s ConnState) Healthy() bool {
	return s.Connected && s.Subscribed && s.LastError == ""
}

// ConnTracker tracks the active MQTT TCP/TLS connection and fences callbacks
// across reconnect generations so stale subscribe or disconnect goroutines cannot
// corrupt state belonging to a newer connection.
type ConnTracker struct {
	mu              sync.Mutex
	gen             uint64
	dialedGenActive bool
	connected       bool
	subscribed      bool
	lastError       string
	lastSuccess     time.Time

	curConn   *trackedConn
	connGen   uint64
	curClosed chan struct{}

	nextObsID uint64
	observers map[uint64]func(ConnState)
}

// NewConnTracker initializes a generation-fenced MQTT connection tracker.
func NewConnTracker() *ConnTracker {
	return &ConnTracker{}
}

// DialTLS establishes a tracked TCP or TLS connection (wrapped for thread-safe MQTT
// packet writes), replaces any prior tracked connection, and returns the wrapped
// connection, its generation, and a per-connection close signal channel.
func (t *ConnTracker) DialTLS(
	ctx context.Context,
	tlsConfig *tls.Config,
	address string,
	timeout time.Duration,
	onClose func(gen uint64),
) (net.Conn, uint64, <-chan struct{}, error) {
	if strings.TrimSpace(address) == "" {
		return nil, 0, nil, errors.New("mqtt broker address is required")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := &net.Dialer{Timeout: timeout}
	var (
		rawConn net.Conn
		err     error
	)
	if tlsConfig != nil {
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config:    tlsConfig,
		}
		rawConn, err = tlsDialer.DialContext(dialCtx, "tcp", address)
	} else {
		rawConn, err = dialer.DialContext(dialCtx, "tcp", address)
	}
	if err != nil {
		return nil, 0, nil, err
	}

	return t.trackConn(rawConn, onClose)
}

// DialBrokerURL dials a parsed MQTT broker URL using TLS for tls/mqtts/ssl schemes
// and plain TCP for tcp/mqtt schemes.
func (t *ConnTracker) DialBrokerURL(
	ctx context.Context,
	server *url.URL,
	timeout time.Duration,
	tlsConfig *tls.Config,
	onClose func(gen uint64),
) (net.Conn, uint64, <-chan struct{}, error) {
	if server == nil {
		return nil, 0, nil, errors.New("mqtt broker URL is required")
	}
	scheme := strings.ToLower(strings.TrimSpace(server.Scheme))
	host := server.Host
	if host == "" {
		return nil, 0, nil, errors.New("mqtt broker URL host is required")
	}

	var effectiveTLS *tls.Config
	switch scheme {
	case "tls", "mqtts", "ssl":
		if tlsConfig == nil {
			return nil, 0, nil, errors.New("mqtt tls configuration is required for TLS broker URL")
		}
		effectiveTLS = tlsConfig.Clone()
		if effectiveTLS.ServerName == "" {
			effectiveTLS.ServerName = server.Hostname()
		}
		if effectiveTLS.MinVersion == 0 {
			effectiveTLS.MinVersion = tls.VersionTLS13
		}
	case "tcp", "mqtt", "":
		effectiveTLS = nil
	default:
		if tlsConfig != nil {
			effectiveTLS = tlsConfig.Clone()
		}
	}

	return t.DialTLS(ctx, effectiveTLS, host, timeout, onClose)
}

// TrackConn wraps rawConn so closes are signaled on a per-connection channel,
// increments the tracker generation, and closes any previously tracked connection.
func (t *ConnTracker) TrackConn(rawConn net.Conn) net.Conn {
	if rawConn == nil {
		return nil
	}
	conn, _, _, _ := t.trackConn(rawConn, nil)
	return conn
}

func (t *ConnTracker) trackConn(rawConn net.Conn, onClose func(gen uint64)) (net.Conn, uint64, <-chan struct{}, error) {
	if rawConn == nil {
		return nil, 0, nil, ErrNilConn
	}
	closed := make(chan struct{})
	wrapped := &trackedConn{
		Conn:    rawConn,
		tracker: t,
		closed:  closed,
		onClose: onClose,
	}

	var prev *trackedConn
	t.mu.Lock()
	prev = t.curConn
	t.gen++
	gen := t.gen
	wrapped.connGen = gen
	t.connGen = gen
	t.dialedGenActive = true
	t.curConn = wrapped
	t.curClosed = closed
	t.connected = false
	t.subscribed = false
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}
	notifyObservers(observers, state)
	return packets.NewThreadSafeConn(wrapped), gen, closed, nil
}

// NextGen increments the connection generation and resets connected/subscribed flags.
func (t *ConnTracker) NextGen() uint64 {
	t.mu.Lock()
	t.gen++
	gen := t.gen
	t.dialedGenActive = false
	t.connected = false
	t.subscribed = false
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
	return gen
}

// CurrentGen returns the current monotonic connection generation.
func (t *ConnTracker) CurrentGen() uint64 {
	t.mu.Lock()
	gen := t.gen
	t.mu.Unlock()
	return gen
}

// CurrentClosed returns a channel closed when the currently tracked connection closes,
// or nil if no socket connection is currently tracked.
func (t *ConnTracker) CurrentClosed() <-chan struct{} {
	t.mu.Lock()
	closed := t.curClosed
	t.mu.Unlock()
	return closed
}

// ClosedChanForGen returns a closed channel immediately if gen has been superseded,
// or the underlying tracked connection's close channel while gen remains current.
func (t *ConnTracker) ClosedChanForGen(gen uint64) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen == 0 || t.gen != gen {
		return closedSignalChan
	}
	return t.curClosed
}

// OnConnectUp records an active connection for a connection generation and resets
// Subscribed to false until subscribe verification completes.
// If DialTLS/TrackConn already opened a generation for this handshake, that generation
// is preserved so callers inspecting the dial generation stay aligned.
func (t *ConnTracker) OnConnectUp() uint64 {
	t.mu.Lock()
	if t.dialedGenActive {
		t.dialedGenActive = false
	} else {
		t.gen++
	}
	gen := t.gen
	t.connected = true
	t.subscribed = false
	t.lastError = ""
	if t.curConn != nil {
		t.connGen = gen
		t.curConn.setGen(gen)
	}
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
	return gen
}

// OnConnectionDown marks the connection down, advances the generation so in-flight
// subscribe work for earlier generations cannot commit, and closes the current socket.
func (t *ConnTracker) OnConnectionDown(err error) uint64 {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	t.mu.Lock()
	prev, state, observers := t.resetConnectionLocked(true, errMsg)
	gen := state.Generation
	t.mu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}
	notifyObservers(observers, state)
	return gen
}

// MarkConnectError records a connection establishment error, advances generation,
// clears connected/subscribed flags, and closes any tracked socket.
func (t *ConnTracker) MarkConnectError(err error) uint64 {
	errMsg := "mqtt connect failed"
	if err != nil {
		errMsg = err.Error()
	}
	t.mu.Lock()
	prev, state, observers := t.resetConnectionLocked(true, errMsg)
	gen := state.Generation
	t.mu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}
	notifyObservers(observers, state)
	return gen
}

// MarkSubscribed marks the connection subscribed only if gen still matches the current
// live generation and the connection is currently up.
func (t *ConnTracker) MarkSubscribed(gen uint64) bool {
	if gen == 0 {
		return false
	}
	t.mu.Lock()
	if t.gen != gen || !t.connected {
		t.mu.Unlock()
		return false
	}
	t.subscribed = true
	t.lastError = ""
	t.lastSuccess = time.Now()
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
	return true
}

// MarkSubscribeError records a subscription failure only if gen still matches the
// current generation.
func (t *ConnTracker) MarkSubscribeError(gen uint64, err error) bool {
	if gen == 0 {
		return false
	}
	t.mu.Lock()
	if t.gen != gen {
		t.mu.Unlock()
		return false
	}
	t.subscribed = false
	if err != nil {
		t.lastError = err.Error()
	} else {
		t.lastError = "mqtt subscribe failed"
	}
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
	return true
}

// RecordSuccess clears LastError, updates LastSuccess, and notifies observers.
func (t *ConnTracker) RecordSuccess() {
	t.mu.Lock()
	t.lastError = ""
	t.lastSuccess = time.Now()
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
}

// RecordError updates LastError outside of a subscribe transition.
func (t *ConnTracker) RecordError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	t.lastError = err.Error()
	state := t.stateLocked()
	observers := t.snapshotObserversLocked()
	t.mu.Unlock()

	notifyObservers(observers, state)
}

func (t *ConnTracker) resetConnectionLocked(advanceGen bool, errMsg string) (*trackedConn, ConnState, []func(ConnState)) {
	if advanceGen {
		t.gen++
	}
	t.dialedGenActive = false
	t.connected = false
	t.subscribed = false
	if errMsg != "" {
		t.lastError = errMsg
	}
	prev := t.curConn
	t.curConn = nil
	return prev, t.stateLocked(), t.snapshotObserversLocked()
}

// CloseCurrent closes the currently tracked socket if present.
func (t *ConnTracker) CloseCurrent() error {
	t.mu.Lock()
	prev, state, observers := t.resetConnectionLocked(false, "")
	t.mu.Unlock()

	var closeErr error
	if prev != nil {
		closeErr = prev.Close()
	}
	notifyObservers(observers, state)
	return closeErr
}

// CloseIfGen closes the tracked socket only if gen matches either the active generation
// or the generation bound to the current socket.
func (t *ConnTracker) CloseIfGen(gen uint64) error {
	if gen == 0 {
		return nil
	}
	t.mu.Lock()
	prev := t.curConn
	matchesConn := prev != nil && (t.connGen == gen || t.gen == gen)
	if !matchesConn {
		t.mu.Unlock()
		return nil
	}
	var state ConnState
	var observers []func(ConnState)
	if t.gen == gen {
		prev, state, observers = t.resetConnectionLocked(true, "")
	} else {
		t.curConn = nil
		state = t.stateLocked()
		observers = t.snapshotObserversLocked()
	}
	t.mu.Unlock()

	closeErr := prev.Close()
	notifyObservers(observers, state)
	return closeErr
}

// CloseIfClosed closes the active connection when expected is nil or still matches
// the currently tracked connection's close channel, preventing stale error paths from
// tearing down a newer connection.
func (t *ConnTracker) CloseIfClosed(expected <-chan struct{}) error {
	t.mu.Lock()
	if t.curConn == nil || (expected != nil && t.curClosed != expected) {
		t.mu.Unlock()
		return nil
	}
	prev, state, observers := t.resetConnectionLocked(false, "")
	t.mu.Unlock()

	closeErr := prev.Close()
	notifyObservers(observers, state)
	return closeErr
}

// State returns a consistent snapshot of the tracker's connection state.
func (t *ConnTracker) State() ConnState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stateLocked()
}

// AddObserver registers a callback notified on state changes and immediately invokes it
// once with the current snapshot. The returned function removes the observer.
func (t *ConnTracker) AddObserver(fn func(ConnState)) func() {
	if fn == nil {
		return func() {}
	}
	t.mu.Lock()
	if t.observers == nil {
		t.observers = make(map[uint64]func(ConnState))
	}
	t.nextObsID++
	id := t.nextObsID
	t.observers[id] = fn
	state := t.stateLocked()
	t.mu.Unlock()

	fn(state)

	return func() {
		t.mu.Lock()
		delete(t.observers, id)
		t.mu.Unlock()
	}
}

func (t *ConnTracker) stateLocked() ConnState {
	return ConnState{
		Generation:  t.gen,
		Connected:   t.connected,
		Subscribed:  t.subscribed,
		LastError:   t.lastError,
		LastSuccess: t.lastSuccess,
	}
}

func (t *ConnTracker) snapshotObserversLocked() []func(ConnState) {
	if len(t.observers) == 0 {
		return nil
	}
	out := make([]func(ConnState), 0, len(t.observers))
	for _, fn := range t.observers {
		out = append(out, fn)
	}
	return out
}

func notifyObservers(observers []func(ConnState), state ConnState) {
	for _, fn := range observers {
		fn(state)
	}
}

func (t *ConnTracker) detachClosedConn(tc *trackedConn) {
	t.mu.Lock()
	if t.curConn == tc {
		t.curConn = nil
	}
	t.mu.Unlock()
}

type trackedConn struct {
	net.Conn
	tracker   *ConnTracker
	closed    chan struct{}
	closeOnce sync.Once
	genMu     sync.Mutex
	connGen   uint64
	onClose   func(gen uint64)
}

func (tc *trackedConn) setGen(gen uint64) {
	tc.genMu.Lock()
	tc.connGen = gen
	tc.genMu.Unlock()
}

func (tc *trackedConn) getGen() uint64 {
	tc.genMu.Lock()
	gen := tc.connGen
	tc.genMu.Unlock()
	return gen
}

func (tc *trackedConn) Close() error {
	var err error
	tc.closeOnce.Do(func() {
		close(tc.closed)
		if tc.tracker != nil {
			tc.tracker.detachClosedConn(tc)
		}
		if tc.Conn != nil {
			err = tc.Conn.Close()
		}
		if tc.onClose != nil {
			tc.onClose(tc.getGen())
		}
	})
	return err
}

// ErrNilConn indicates a nil net.Conn was passed to connection tracking.
var ErrNilConn = errors.New("mqtt connection is nil")
