package adapter

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	// BrokerNoticeLogTopic is Mosquitto's native notice log topic carrying client
	// connect and disconnect events (non-shared subscription only).
	BrokerNoticeLogTopic = "$SYS/broker/log/N"

	defaultBrokerDisconnectDebounce = 2 * time.Second
)

var (
	mosquittoConnectNoticePattern  = regexp.MustCompile(`^New client connected from .+ as (\S+)(?: \(([^)]*)\))?\.?$`)
	mosquittoUsernameOptionPattern = regexp.MustCompile(`(?:^|,\s*)u'([^']+)'`)
)

type brokerDebounceTimer interface {
	Stop() bool
}

// BrokerLogWatcher tracks active Mosquitto client sessions from $SYS/broker/log/N
// notices and emits debounced gateway disconnect callbacks when the last active
// broker client for a gateway disconnects without an intervening reconnect.
type BrokerLogWatcher struct {
	ctx          context.Context
	cancel       context.CancelFunc
	onDisconnect func(context.Context, string, time.Time)
	debounce     time.Duration
	now          func() time.Time
	afterFunc    func(time.Duration, func()) brokerDebounceTimer
	logger       *slog.Logger
	metrics      *observability.Registry

	mu              sync.Mutex
	closed          bool
	clientToGateway map[string]string
	activeClients   map[string]map[string]struct{}
	timers          map[string]brokerDebounceTimer
	timerGen        map[string]uint64
	wg              sync.WaitGroup
}

// BrokerLogWatcherOption configures a BrokerLogWatcher.
type BrokerLogWatcherOption func(*BrokerLogWatcher)

// WithBrokerDisconnectDebounce overrides the grace window waited after a gateway's
// last broker client disconnects before emitting an offline transition.
func WithBrokerDisconnectDebounce(debounce time.Duration) BrokerLogWatcherOption {
	return func(w *BrokerLogWatcher) {
		if debounce >= 0 {
			w.debounce = debounce
		}
	}
}

// WithBrokerLogClock overrides the clock used to timestamp disconnect notices.
func WithBrokerLogClock(now func() time.Time) BrokerLogWatcherOption {
	return func(w *BrokerLogWatcher) {
		if now != nil {
			w.now = now
		}
	}
}

func withBrokerLogAfterFunc(afterFunc func(time.Duration, func()) brokerDebounceTimer) BrokerLogWatcherOption {
	return func(w *BrokerLogWatcher) {
		if afterFunc != nil {
			w.afterFunc = afterFunc
		}
	}
}

// WithBrokerLogObservability attaches structured logging and panic recovery metrics.
func WithBrokerLogObservability(logger *slog.Logger, metrics *observability.Registry) BrokerLogWatcherOption {
	return func(w *BrokerLogWatcher) {
		w.logger = logger
		w.metrics = metrics
	}
}

// NewBrokerLogWatcher constructs a watcher driving onDisconnect when a gateway loses all
// active Mosquitto client connections and does not reconnect within the debounce window.
func NewBrokerLogWatcher(
	ctx context.Context,
	onDisconnect func(context.Context, string, time.Time),
	opts ...BrokerLogWatcherOption,
) *BrokerLogWatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancel := context.WithCancel(ctx)
	w := &BrokerLogWatcher{
		ctx:          watchCtx,
		cancel:       cancel,
		onDisconnect: onDisconnect,
		debounce:     defaultBrokerDisconnectDebounce,
		now:          time.Now,
		afterFunc: func(d time.Duration, fn func()) brokerDebounceTimer {
			return time.AfterFunc(d, fn)
		},
		clientToGateway: make(map[string]string),
		activeClients:   make(map[string]map[string]struct{}),
		timers:          make(map[string]brokerDebounceTimer),
		timerGen:        make(map[string]uint64),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// HandlePublish parses an incoming $SYS/broker/log/N MQTT publish packet.
func (w *BrokerLogWatcher) HandlePublish(packet *paho.Publish) {
	if w == nil || packet == nil {
		return
	}
	if strings.TrimSpace(packet.Topic) != BrokerNoticeLogTopic {
		return
	}
	for _, line := range strings.Split(string(packet.Payload), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			w.HandleLine(trimmed)
		}
	}
}

// HandleLine parses a single Mosquitto notice log line and updates active gateway
// connection tracking.
func (w *BrokerLogWatcher) HandleLine(rawLine string) {
	if w == nil {
		return
	}
	body := stripMosquittoLogTimestamp(strings.TrimSpace(rawLine))
	if body == "" {
		return
	}

	if clientID, username, ok := parseBrokerConnectNotice(body); ok {
		gatewayID := resolveBrokerGatewayID(clientID, username)
		if gatewayID == "" {
			return
		}
		w.recordConnect(clientID, gatewayID)
		return
	}

	if clientID, ok := parseBrokerDisconnectNotice(body); ok {
		w.recordDisconnect(clientID)
	}
}

// Close cancels pending disconnect timers and waits for in-flight disconnect callbacks.
func (w *BrokerLogWatcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		for _, timer := range w.timers {
			if timer != nil {
				timer.Stop()
			}
		}
		clear(w.timers)
		clear(w.timerGen)
		clear(w.clientToGateway)
		clear(w.activeClients)
	}
	w.mu.Unlock()

	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

func (w *BrokerLogWatcher) recordConnect(clientID, gatewayID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if prevGateway, had := w.clientToGateway[clientID]; had && prevGateway != gatewayID {
		w.removeClientLocked(prevGateway, clientID)
	}
	w.clientToGateway[clientID] = gatewayID
	clients := w.activeClients[gatewayID]
	if clients == nil {
		clients = make(map[string]struct{}, 1)
		w.activeClients[gatewayID] = clients
	}
	clients[clientID] = struct{}{}

	if timer := w.timers[gatewayID]; timer != nil {
		timer.Stop()
		delete(w.timers, gatewayID)
	}
	w.timerGen[gatewayID]++
}

func (w *BrokerLogWatcher) recordDisconnect(clientID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	gatewayID := w.clientToGateway[clientID]
	delete(w.clientToGateway, clientID)
	if gatewayID == "" {
		gatewayID = normalizeLowerUUIDv7(clientID)
	}
	if gatewayID == "" {
		return
	}

	w.removeClientLocked(gatewayID, clientID)
	if len(w.activeClients[gatewayID]) > 0 {
		return
	}

	if timer := w.timers[gatewayID]; timer != nil {
		timer.Stop()
	}
	gen := w.timerGen[gatewayID] + 1
	w.timerGen[gatewayID] = gen
	disconnectedAt := w.now().UTC()
	w.timers[gatewayID] = w.afterFunc(w.debounce, func() {
		w.fireDisconnect(gatewayID, gen, disconnectedAt)
	})
}

func (w *BrokerLogWatcher) removeClientLocked(gatewayID, clientID string) {
	clients := w.activeClients[gatewayID]
	if clients == nil {
		return
	}
	delete(clients, clientID)
	if len(clients) == 0 {
		delete(w.activeClients, gatewayID)
	}
}

func (w *BrokerLogWatcher) fireDisconnect(gatewayID string, gen uint64, disconnectedAt time.Time) {
	w.mu.Lock()
	if w.closed || w.watchStopped() || w.timerGen[gatewayID] != gen || len(w.activeClients[gatewayID]) > 0 {
		w.mu.Unlock()
		return
	}
	delete(w.timers, gatewayID)
	delete(w.timerGen, gatewayID)
	w.wg.Add(1)
	w.mu.Unlock()

	defer w.wg.Done()
	defer observability.Recover(w.logger, w.metrics, "connectivity.broker_log_disconnect")
	if w.onDisconnect != nil {
		w.onDisconnect(w.ctx, gatewayID, disconnectedAt)
	}
}

func (w *BrokerLogWatcher) watchStopped() bool {
	return w.ctx == nil || w.ctx.Err() != nil
}

func stripMosquittoLogTimestamp(line string) string {
	idx := strings.Index(line, ": ")
	if idx <= 0 {
		return line
	}
	for i := 0; i < idx; i++ {
		if line[i] < '0' || line[i] > '9' {
			return line
		}
	}
	return strings.TrimSpace(line[idx+2:])
}

func parseBrokerConnectNotice(body string) (clientID, username string, ok bool) {
	matches := mosquittoConnectNoticePattern.FindStringSubmatch(body)
	if len(matches) == 0 {
		return "", "", false
	}
	clientID = strings.TrimSpace(matches[1])
	if clientID == "" || clientID == "<unknown>" {
		return "", "", false
	}
	if len(matches) >= 3 && matches[2] != "" {
		if userMatches := mosquittoUsernameOptionPattern.FindStringSubmatch(matches[2]); len(userMatches) >= 2 {
			username = strings.TrimSpace(userMatches[1])
		}
	}
	return clientID, username, true
}

func parseBrokerDisconnectNotice(body string) (string, bool) {
	var clientID string
	switch {
	case strings.HasPrefix(body, "Client "):
		rest := strings.TrimPrefix(body, "Client ")
		for _, marker := range []string{
			" disconnected",
			" closed its connection",
			" has exceeded timeout",
		} {
			if idx := strings.Index(rest, marker); idx > 0 {
				clientID = strings.TrimSpace(rest[:idx])
				break
			}
		}
	case strings.HasPrefix(body, "Socket error on client "):
		rest := strings.TrimPrefix(body, "Socket error on client ")
		if idx := strings.IndexAny(rest, ", "); idx > 0 {
			clientID = strings.TrimSpace(rest[:idx])
		}
	}
	if clientID == "" || clientID == "<unknown>" || strings.ContainsAny(clientID, " \t") {
		return "", false
	}
	return clientID, true
}

func resolveBrokerGatewayID(clientID, username string) string {
	if username != "" {
		return normalizeLowerUUIDv7(username)
	}
	return normalizeLowerUUIDv7(clientID)
}

func normalizeLowerUUIDv7(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !uuidv7.IsValidAnyCase(trimmed) {
		return ""
	}
	return strings.ToLower(trimmed)
}
