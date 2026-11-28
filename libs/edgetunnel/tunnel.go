package edgetunnel

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	AuditSessionOpened  = "SESSION_OPENED"
	AuditSessionClosed  = "SESSION_CLOSED"
	AuditStreamOpened   = "STREAM_OPENED"
	AuditStreamRejected = "STREAM_REJECTED"
)

// AuditEvent captures one tunnel lifecycle or stream event for durable audit.
type AuditEvent struct {
	GatewayID  string
	SessionID  string
	EventType  string
	StreamID   *int64
	TargetPort *uint16
	RemoteAddr string
	Reason     string
	BytesTX    int64
	BytesRX    int64
	OccurredAt time.Time
}

// AuditSink persists tunnel session and stream audit events.
type AuditSink interface {
	RecordTunnelAudit(context.Context, AuditEvent) error
}

// WithAuditSink attaches a durable audit sink to a tunnel Hub.
func WithAuditSink(sink AuditSink) Option {
	return func(h *Hub) {
		h.auditSink = sink
	}
}

func newTunnelUUIDv7(now time.Time) string {
	return uuidv7.MustNewAt(now)
}

func truncateAuditUTF8(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := 0
	for cut < len(value) {
		_, size := utf8.DecodeRuneInString(value[cut:])
		if cut+size > maxBytes {
			break
		}
		cut += size
	}
	return value[:cut]
}

func truncateAuditReason(reason string) string {
	return truncateAuditUTF8(reason, 512)
}

const (
	ProtocolName               = "hvac-gateway-tunnel/v1"
	AllowedTargetPort   uint16 = 1880
	SSHTargetPort       uint16 = 22
	MaxFramePayload            = 64 * 1024
	maxStreamBuffer            = 4 * 1024 * 1024
	defaultWriteTimeout        = 15 * time.Second
)

// IsSupportedTargetPort reports whether port is in the hard-bounded gateway tunnel allowlist.
func IsSupportedTargetPort(port uint16) bool {
	return port == AllowedTargetPort || port == SSHTargetPort
}

func buildAllowedPortSet(ports ...uint16) map[uint16]struct{} {
	allowed := make(map[uint16]struct{}, len(ports)+1)
	for _, port := range ports {
		if IsSupportedTargetPort(port) {
			allowed[port] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		allowed[AllowedTargetPort] = struct{}{}
	}
	return allowed
}

const (
	FrameOpen    uint8 = 0x01
	FrameOpenOK  uint8 = 0x02
	FrameOpenErr uint8 = 0x03
	FrameData    uint8 = 0x04
	FrameEOF     uint8 = 0x05
	FrameRST     uint8 = 0x06
	FramePing    uint8 = 0x07
	FramePong    uint8 = 0x08
)

var (
	ErrNonLoopbackAddress = errors.New("tunnel address must use a loopback IP")
	ErrPortForbidden      = errors.New("tunnel target port forbidden")
	ErrNoActiveGateway    = errors.New("no active gateway tunnel session")
	ErrAmbiguousGateway   = errors.New("multiple active gateways require explicit gateway ID")
	ErrFrameTooLarge      = errors.New("tunnel frame payload exceeds maximum size")
	ErrInvalidUpgrade     = errors.New("invalid tunnel upgrade headers")
)

type Frame struct {
	Type     uint8
	StreamID uint32
	Payload  []byte
}

func WriteFrame(w io.Writer, frame Frame) error {
	if len(frame.Payload) > MaxFramePayload {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 9+len(frame.Payload))
	buf[0] = frame.Type
	binary.BigEndian.PutUint32(buf[1:5], frame.StreamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(frame.Payload)))
	copy(buf[9:], frame.Payload)
	for len(buf) > 0 {
		n, err := w.Write(buf)
		buf = buf[n:]
		if err != nil {
			return err
		}
	}
	return nil
}

func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [9]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(hdr[5:9])
	if length > MaxFramePayload {
		return Frame{}, ErrFrameTooLarge
	}
	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{
		Type:     hdr[0],
		StreamID: binary.BigEndian.Uint32(hdr[1:5]),
		Payload:  payload,
	}, nil
}

// ValidateLoopbackAddr enforces fail-closed binding and dialing to literal loopback IPs only.
func ValidateLoopbackAddr(addr string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNonLoopbackAddress, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: invalid port", ErrNonLoopbackAddress)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: %q", ErrNonLoopbackAddress, addr)
	}
	return nil
}

type Option func(*Hub)

func WithPingInterval(interval time.Duration) Option {
	return func(h *Hub) {
		if interval > 0 {
			h.pingInterval = interval
		}
	}
}

func WithReadTimeout(timeout time.Duration) Option {
	return func(h *Hub) {
		if timeout > 0 {
			h.readTimeout = timeout
		}
	}
}

func WithWriteTimeout(timeout time.Duration) Option {
	return func(h *Hub) {
		if timeout > 0 {
			h.writeTimeout = timeout
		}
	}
}

func WithOpenTimeout(timeout time.Duration) Option {
	return func(h *Hub) {
		if timeout > 0 {
			h.openTimeout = timeout
		}
	}
}

func WithDefaultGateway(gatewayID string) Option {
	return func(h *Hub) {
		h.defaultGatewayID = strings.TrimSpace(gatewayID)
	}
}

// WithAllowedTargetPorts restricts Hub to an explicit subset of IsSupportedTargetPort ports.
func WithAllowedTargetPorts(ports ...uint16) Option {
	allowed := buildAllowedPortSet(ports...)
	return func(h *Hub) {
		h.allowedPorts = allowed
	}
}

// WithObservability attaches a logger and metrics registry to a tunnel Hub for panic recovery.
func WithObservability(logger *slog.Logger, metrics *observability.Registry) Option {
	return func(h *Hub) {
		h.logger = logger
		h.metrics = metrics
	}
}

type Hub struct {
	mu               sync.RWMutex
	sessions         map[string]*Session
	listeners        []net.Listener
	allowedPorts     map[uint16]struct{}
	defaultGatewayID string
	pingInterval     time.Duration
	readTimeout      time.Duration
	writeTimeout     time.Duration
	openTimeout      time.Duration
	auditSink        AuditSink
	logger           *slog.Logger
	metrics          *observability.Registry
	closed           atomic.Bool
}

func (h *Hub) isPortAllowed(port uint16) bool {
	if h == nil || len(h.allowedPorts) == 0 {
		return port == AllowedTargetPort
	}
	_, ok := h.allowedPorts[port]
	return ok
}

func (h *Hub) log() *slog.Logger {
	if h != nil && h.logger != nil {
		return h.logger
	}
	return slog.Default()
}

func (h *Hub) metricRegistry() *observability.Registry {
	if h == nil {
		return nil
	}
	return h.metrics
}

func (h *Hub) recordActiveTunnelsLocked() {
	if h == nil || h.metrics == nil {
		return
	}
	_ = h.metrics.SetGauge("hvac_mqtt_active_tunnels", "Active gateway reverse tunnel sessions.", nil, float64(len(h.sessions)))
}

func NewHub(options ...Option) *Hub {
	h := &Hub{
		sessions:     make(map[string]*Session),
		allowedPorts: buildAllowedPortSet(AllowedTargetPort),
		pingInterval: 25 * time.Second,
		readTimeout:  75 * time.Second,
		writeTimeout: defaultWriteTimeout,
		openTimeout:  10 * time.Second,
	}
	for _, option := range options {
		option(h)
	}
	h.mu.Lock()
	h.recordActiveTunnelsLocked()
	h.mu.Unlock()
	return h
}

func (h *Hub) recordAudit(ctx context.Context, event AuditEvent) {
	if h == nil || h.auditSink == nil {
		return
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	event.Reason = truncateAuditReason(event.Reason)
	event.RemoteAddr = truncateAuditUTF8(event.RemoteAddr, 256)
	auditCtx := ctx
	if auditCtx == nil {
		auditCtx = context.Background()
	}
	var cancel context.CancelFunc
	if _, hasDeadline := auditCtx.Deadline(); !hasDeadline {
		auditCtx, cancel = context.WithTimeout(auditCtx, 2*time.Second)
		defer cancel()
	}
	if err := h.auditSink.RecordTunnelAudit(auditCtx, event); err != nil {
		slog.Warn("tunnel audit record failed",
			"event_type", event.EventType,
			"gateway_id", event.GatewayID,
			"error_code", "TUNNEL_AUDIT_RECORD_FAILED",
		)
	}
}

func (h *Hub) ActiveGateways() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		out = append(out, id)
	}
	return out
}

func (h *Hub) CloseGateway(gatewayID string) {
	h.mu.Lock()
	session := h.sessions[gatewayID]
	if session != nil {
		delete(h.sessions, gatewayID)
		h.recordActiveTunnelsLocked()
	}
	h.mu.Unlock()
	if session != nil {
		session.closeWithReason("gateway_closed")
	}
}

// CloseGatewayByFingerprint closes the active tunnel session for gatewayID only if its
// presenting client certificate matches fingerprint, preserving newer rotated sessions.
func (h *Hub) CloseGatewayByFingerprint(gatewayID, fingerprint string) bool {
	target := strings.ToLower(strings.TrimSpace(fingerprint))
	if target == "" {
		return false
	}
	h.mu.Lock()
	session := h.sessions[gatewayID]
	if session == nil || session.peerFingerprint != target {
		h.mu.Unlock()
		return false
	}
	delete(h.sessions, gatewayID)
	h.recordActiveTunnelsLocked()
	h.mu.Unlock()
	session.closeWithReason("certificate_revoked")
	return true
}

func (h *Hub) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.mu.Lock()
	listeners := append([]net.Listener(nil), h.listeners...)
	h.listeners = nil
	sessions := make([]*Session, 0, len(h.sessions))
	for id, s := range h.sessions {
		delete(h.sessions, id)
		sessions = append(sessions, s)
	}
	h.recordActiveTunnelsLocked()
	h.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, s := range sessions {
		s.closeTransport()
	}
	if h.auditSink == nil || len(sessions) == 0 {
		return
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range sessions {
		if !s.opened.Load() {
			continue
		}
		wg.Add(1)
		go func(sess *Session) {
			defer wg.Done()
			defer observability.Recover(h.log(), h.metricRegistry(), "connectivity.tunnel_close_audit")
			sess.emitCloseAudit(auditCtx, "hub_closed")
		}(s)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer observability.Recover(h.log(), h.metricRegistry(), "connectivity.tunnel_close_wait")
		wg.Wait()
	}()
	select {
	case <-done:
	case <-auditCtx.Done():
	}
}

func (h *Hub) resolveSession(gatewayID string) (*Session, error) {
	id := strings.TrimSpace(gatewayID)
	if id == "" {
		id = h.defaultGatewayID
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if id != "" {
		session := h.sessions[id]
		if session == nil {
			return nil, ErrNoActiveGateway
		}
		return session, nil
	}
	if len(h.sessions) == 0 {
		return nil, ErrNoActiveGateway
	}
	if len(h.sessions) > 1 {
		return nil, ErrAmbiguousGateway
	}
	for _, session := range h.sessions {
		return session, nil
	}
	return nil, ErrNoActiveGateway
}

func (h *Hub) OpenStream(ctx context.Context, gatewayID string, targetPort uint16) (*Stream, error) {
	if !h.isPortAllowed(targetPort) {
		port := targetPort
		resolvedID := strings.TrimSpace(gatewayID)
		if resolvedID == "" {
			resolvedID = h.defaultGatewayID
		}
		var sessionID, remoteAddr string
		if session, _ := h.resolveSession(gatewayID); session != nil {
			resolvedID = session.gatewayID
			sessionID = session.sessionID
			remoteAddr = session.remoteAddr
		} else {
			sessionID = newTunnelUUIDv7(time.Now().UTC())
		}
		if resolvedID != "" {
			h.recordAudit(ctx, AuditEvent{
				GatewayID:  resolvedID,
				SessionID:  sessionID,
				EventType:  AuditStreamRejected,
				TargetPort: &port,
				RemoteAddr: remoteAddr,
				Reason:     "port_forbidden",
				OccurredAt: time.Now().UTC(),
			})
		}
		return nil, fmt.Errorf("%w: port %d not allowed", ErrPortForbidden, targetPort)
	}
	session, err := h.resolveSession(gatewayID)
	if err != nil {
		resolvedID := strings.TrimSpace(gatewayID)
		if resolvedID == "" {
			resolvedID = h.defaultGatewayID
		}
		if resolvedID != "" {
			port := targetPort
			h.recordAudit(ctx, AuditEvent{
				GatewayID:  resolvedID,
				SessionID:  newTunnelUUIDv7(time.Now().UTC()),
				EventType:  AuditStreamRejected,
				TargetPort: &port,
				Reason:     err.Error(),
				OccurredAt: time.Now().UTC(),
			})
		}
		return nil, err
	}
	return session.openStream(ctx, targetPort, h.openTimeout)
}

func (h *Hub) registerSession(gatewayID string, conn net.Conn) *Session {
	return h.registerSessionWithAddr(gatewayID, conn, "")
}

func (h *Hub) registerSessionWithAddr(gatewayID string, conn net.Conn, remoteAddr string) *Session {
	return h.registerSessionWithFingerprint(gatewayID, conn, remoteAddr, "")
}

func (h *Hub) registerSessionWithFingerprint(gatewayID string, conn net.Conn, remoteAddr, peerFingerprint string) *Session {
	if conn == nil {
		return nil
	}
	s := newSessionWithFingerprint(h, gatewayID, conn, remoteAddr, peerFingerprint)
	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	previous := h.sessions[gatewayID]
	h.sessions[gatewayID] = s
	h.recordActiveTunnelsLocked()
	h.mu.Unlock()
	if previous != nil && previous != s {
		previous.closeWithReason("session_replaced")
	}
	return s
}

func peerCertificateFingerprint(cert *x509.Certificate) string {
	if cert == nil || len(cert.Raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func (h *Hub) unregisterSession(s *Session) {
	h.mu.Lock()
	if h.sessions[s.gatewayID] == s {
		delete(h.sessions, s.gatewayID)
		h.recordActiveTunnelsLocked()
	}
	h.mu.Unlock()
}

type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func hasUpgradeToken(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), ProtocolName) {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

type PeerParser func(*http.Request) (*x509.Certificate, error)
type PeerVerifier func(context.Context, *x509.Certificate) (string, error)

func NewUpgradeHandler(hub *Hub, parsePeer PeerParser, verifyPeer PeerVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !hasUpgradeToken(r) {
			http.Error(w, ErrInvalidUpgrade.Error(), http.StatusBadRequest)
			return
		}
		peer, err := parsePeer(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		gatewayID, err := verifyPeer(r.Context(), peer)
		if err != nil || gatewayID == "" {
			http.Error(w, "gateway credential rejected", http.StatusUnauthorized)
			return
		}
		controller := http.NewResponseController(w)
		conn, brw, err := controller.Hijack()
		if err != nil {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		// Clear http.Server read deadline across Hijack() so idle tunnels outlive server timeouts.
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			_ = conn.Close()
			return
		}
		if hub.writeTimeout > 0 {
			if err := conn.SetWriteDeadline(time.Now().Add(hub.writeTimeout)); err != nil {
				_ = conn.Close()
				return
			}
		} else if err := conn.SetWriteDeadline(time.Time{}); err != nil {
			_ = conn.Close()
			return
		}
		if _, err := brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + ProtocolName + "\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			_ = conn.Close()
			return
		}
		if err := brw.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		tunnelConn := conn
		if brw.Reader.Buffered() > 0 {
			tunnelConn = &bufferedConn{Conn: conn, r: brw.Reader}
		}
		// Re-check revocation after 101 flush and before exposing the session in hub.sessions.
		if verifiedID, recheckErr := verifyPeer(r.Context(), peer); recheckErr != nil || verifiedID != gatewayID {
			_ = tunnelConn.Close()
			return
		}
		fingerprint := peerCertificateFingerprint(peer)
		session := hub.registerSessionWithFingerprint(gatewayID, tunnelConn, r.RemoteAddr, fingerprint)
		if session == nil {
			return
		}
		// Re-check after registering in hub.sessions to close any revocation race window.
		if verifiedID, recheckErr := verifyPeer(r.Context(), peer); recheckErr != nil || verifiedID != gatewayID {
			if fingerprint != "" {
				hub.CloseGatewayByFingerprint(gatewayID, fingerprint)
			} else {
				session.Close()
			}
			return
		}
		session.markOpened(context.Background())
		session.run(r.Context())
	})
}

func (h *Hub) StartLoopbackListener(addr string) (net.Listener, error) {
	return h.StartLoopbackListenerForPort(addr, AllowedTargetPort)
}

// StartLoopbackListenerForPort binds a literal loopback address and forwards accepted connections
// to targetPort through the active gateway tunnel, failing closed if addr or targetPort is disallowed.
func (h *Hub) StartLoopbackListenerForPort(addr string, targetPort uint16) (net.Listener, error) {
	if err := ValidateLoopbackAddr(addr); err != nil {
		return nil, err
	}
	if !h.isPortAllowed(targetPort) {
		return nil, fmt.Errorf("%w: port %d not allowed", ErrPortForbidden, targetPort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		_ = ln.Close()
		return nil, net.ErrClosed
	}
	h.listeners = append(h.listeners, ln)
	h.mu.Unlock()
	go h.acceptLoopForPort(ln, targetPort)
	return ln, nil
}

func (h *Hub) acceptLoopForPort(ln net.Listener, targetPort uint16) {
	defer observability.Recover(h.log(), h.metricRegistry(), "connectivity.tunnel_loopback_accept")
	for {
		localConn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serveLoopbackConnForPort(localConn, targetPort)
	}
}

func (h *Hub) serveLoopbackConnForPort(localConn net.Conn, targetPort uint16) {
	defer func() { _ = localConn.Close() }()
	defer observability.Recover(h.log(), h.metricRegistry(), "connectivity.tunnel_loopback_conn")
	openCtx, cancel := context.WithTimeout(context.Background(), h.openTimeout)
	stream, err := h.OpenStream(openCtx, "", targetPort)
	cancel()
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()
	spliceNetConn(localConn, stream)
}

func RunStdioBridge(ctx context.Context, targetAddr string, in io.Reader, out io.Writer) error {
	if err := ValidateLoopbackAddr(targetAddr); err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	inDone := make(chan error, 1)
	outDone := make(chan error, 1)
	go func() {
		defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_stdio_in", func(any) {
			_ = conn.Close()
			inDone <- errors.New("tunnel stdio input bridge panicked")
		})
		_, copyErr := io.Copy(conn, in)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = conn.Close()
		}
		inDone <- copyErr
	}()
	go func() {
		defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_stdio_out", func(any) {
			_ = conn.Close()
			outDone <- errors.New("tunnel stdio output bridge panicked")
		})
		_, copyErr := io.Copy(out, conn)
		outDone <- copyErr
	}()
	for {
		select {
		case copyErr := <-outDone:
			_ = conn.Close()
			return copyErr
		case copyErr := <-inDone:
			if copyErr != nil {
				_ = conn.Close()
				<-outDone
				return copyErr
			}
			inDone = nil
		case <-ctx.Done():
			_ = conn.Close()
			<-outDone
			return ctx.Err()
		}
	}
}

func spliceConns(a, b interface {
	io.ReadWriteCloser
	CloseWrite() error
}) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_splice", func(any) {
			_ = a.Close()
			_ = b.Close()
		})
		_, _ = io.Copy(b, a)
		_ = b.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_splice", func(any) {
			_ = a.Close()
			_ = b.Close()
		})
		_, _ = io.Copy(a, b)
		_ = a.CloseWrite()
	}()
	wg.Wait()
}

type closeWriteConn struct {
	net.Conn
}

func (c closeWriteConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

func spliceNetConn(localConn net.Conn, stream *Stream) {
	spliceConns(closeWriteConn{localConn}, stream)
}

type Session struct {
	hub             *Hub
	gatewayID       string
	sessionID       string
	peerFingerprint string
	remoteAddr      string
	conn            net.Conn
	writeMu         sync.Mutex
	mu              sync.Mutex
	streams         map[uint32]*Stream
	nextStream      uint32
	bytesTX         atomic.Int64
	bytesRX         atomic.Int64
	opened          atomic.Bool
	closeAuditOnce  sync.Once
	closed          atomic.Bool
	done            chan struct{}
}

func newSessionWithFingerprint(hub *Hub, gatewayID string, conn net.Conn, remoteAddr, peerFingerprint string) *Session {
	addr := strings.TrimSpace(remoteAddr)
	if addr == "" && conn != nil && conn.RemoteAddr() != nil {
		addr = conn.RemoteAddr().String()
	}
	return &Session{
		hub:             hub,
		gatewayID:       gatewayID,
		sessionID:       newTunnelUUIDv7(time.Now().UTC()),
		peerFingerprint: strings.ToLower(strings.TrimSpace(peerFingerprint)),
		remoteAddr:      addr,
		conn:            conn,
		streams:         make(map[uint32]*Stream),
		nextStream:      1,
		done:            make(chan struct{}),
	}
}

func (s *Session) markOpened(ctx context.Context) {
	s.mu.Lock()
	if s.closed.Load() || !s.opened.CompareAndSwap(false, true) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if s.gatewayID == "" {
		return
	}
	s.hub.recordAudit(ctx, AuditEvent{
		GatewayID:  s.gatewayID,
		SessionID:  s.sessionID,
		EventType:  AuditSessionOpened,
		RemoteAddr: s.remoteAddr,
		OccurredAt: time.Now().UTC(),
	})
}

func (s *Session) sendFrame(frame Frame) error {
	if s.closed.Load() || s.conn == nil {
		return net.ErrClosed
	}
	s.writeMu.Lock()
	if s.closed.Load() || s.conn == nil {
		s.writeMu.Unlock()
		return net.ErrClosed
	}
	var err error
	reasonPrefix := "write_error: "
	if s.hub != nil && s.hub.writeTimeout > 0 {
		if deadlineErr := s.conn.SetWriteDeadline(time.Now().Add(s.hub.writeTimeout)); deadlineErr != nil {
			err = deadlineErr
			reasonPrefix = "write_deadline_error: "
		}
	}
	if err == nil {
		err = WriteFrame(s.conn, frame)
	}
	if err != nil {
		closedNow := s.closeTransport()
		s.writeMu.Unlock()
		if closedNow {
			s.emitCloseAudit(context.Background(), reasonPrefix+err.Error())
		}
		return err
	}
	if frame.Type == FrameData {
		s.bytesTX.Add(int64(len(frame.Payload)))
	}
	s.writeMu.Unlock()
	return nil
}

func (s *Session) Close() {
	s.closeWithReason("closed")
}

func (s *Session) closeTransport() bool {
	s.mu.Lock()
	if !s.closed.CompareAndSwap(false, true) {
		s.mu.Unlock()
		return false
	}
	streams := make([]*Stream, 0, len(s.streams))
	for id, st := range s.streams {
		delete(s.streams, id)
		streams = append(streams, st)
	}
	s.mu.Unlock()

	close(s.done)
	if s.conn != nil {
		_ = s.conn.Close()
	}
	for _, st := range streams {
		st.fail(io.EOF)
	}
	s.hub.unregisterSession(s)
	return true
}

func (s *Session) emitCloseAudit(ctx context.Context, reason string) {
	if !s.opened.Load() || s.gatewayID == "" {
		return
	}
	s.closeAuditOnce.Do(func() {
		// Synchronize with sendFrame so BytesTX reflects frames written before closeTransport closed the socket.
		s.writeMu.Lock()
		bytesTX := s.bytesTX.Load()
		s.writeMu.Unlock()
		s.hub.recordAudit(ctx, AuditEvent{
			GatewayID:  s.gatewayID,
			SessionID:  s.sessionID,
			EventType:  AuditSessionClosed,
			RemoteAddr: s.remoteAddr,
			Reason:     reason,
			BytesTX:    bytesTX,
			BytesRX:    s.bytesRX.Load(),
			OccurredAt: time.Now().UTC(),
		})
	})
}

func (s *Session) closeWithReason(reason string) {
	s.closeTransport()
	s.emitCloseAudit(context.Background(), reason)
}

func (s *Session) openStream(ctx context.Context, targetPort uint16, timeout time.Duration) (*Stream, error) {
	streamID := int64(0)
	port := targetPort
	recordRejected := func(err error) {
		if s.gatewayID == "" {
			return
		}
		var sidPtr *int64
		if streamID > 0 {
			sid := streamID
			sidPtr = &sid
		}
		s.hub.recordAudit(ctx, AuditEvent{
			GatewayID:  s.gatewayID,
			SessionID:  s.sessionID,
			EventType:  AuditStreamRejected,
			StreamID:   sidPtr,
			TargetPort: &port,
			RemoteAddr: s.remoteAddr,
			Reason:     err.Error(),
			OccurredAt: time.Now().UTC(),
		})
	}
	if s.closed.Load() {
		recordRejected(net.ErrClosed)
		return nil, net.ErrClosed
	}
	s.mu.Lock()
	id := s.nextStream
	s.nextStream += 2
	streamID = int64(id)
	st := newStream(s, id)
	s.streams[id] = st
	s.mu.Unlock()

	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], targetPort)
	if err := s.sendFrame(Frame{Type: FrameOpen, StreamID: id, Payload: portBuf[:]}); err != nil {
		s.removeStream(id)
		recordRejected(err)
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-st.openCh:
		if err != nil {
			s.removeStream(id)
			recordRejected(err)
			return nil, err
		}
		sid := streamID
		s.hub.recordAudit(ctx, AuditEvent{
			GatewayID:  s.gatewayID,
			SessionID:  s.sessionID,
			EventType:  AuditStreamOpened,
			StreamID:   &sid,
			TargetPort: &port,
			RemoteAddr: s.remoteAddr,
			OccurredAt: time.Now().UTC(),
		})
		return st, nil
	case <-ctx.Done():
		_ = s.sendFrame(Frame{Type: FrameRST, StreamID: id})
		s.removeStream(id)
		recordRejected(ctx.Err())
		return nil, ctx.Err()
	case <-s.done:
		s.removeStream(id)
		recordRejected(io.EOF)
		return nil, io.EOF
	case <-timer.C:
		_ = s.sendFrame(Frame{Type: FrameRST, StreamID: id})
		s.removeStream(id)
		recordRejected(context.DeadlineExceeded)
		return nil, context.DeadlineExceeded
	}
}

func (s *Session) removeStream(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

func (s *Session) getStream(id uint32) *Stream {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	return st
}

func (s *Session) run(ctx context.Context) {
	closeReason := "session_ended"
	defer func() {
		s.closeWithReason(closeReason)
	}()
	go func() {
		defer observability.Recover(s.hub.log(), s.hub.metricRegistry(), "connectivity.tunnel_session_ping", func(any) {
			s.closeWithReason("ping_panicked")
		})
		ticker := time.NewTicker(s.hub.pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := s.sendFrame(Frame{Type: FramePing}); err != nil {
					s.closeWithReason("ping_failed: " + err.Error())
					return
				}
			case <-ctx.Done():
				s.closeWithReason("context_done")
				return
			case <-s.done:
				return
			}
		}
	}()

	for {
		if s.hub.readTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.hub.readTimeout))
		}
		frame, err := ReadFrame(s.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				closeReason = "eof"
			} else {
				closeReason = "read_error: " + err.Error()
			}
			return
		}
		switch frame.Type {
		case FrameOpenOK:
			if st := s.getStream(frame.StreamID); st != nil {
				st.resolveOpen(nil)
			}
		case FrameOpenErr:
			if st := s.getStream(frame.StreamID); st != nil {
				msg := strings.TrimSpace(string(frame.Payload))
				if msg == "" {
					msg = "open failed"
				}
				st.resolveOpen(errors.New(msg))
			}
		case FrameData:
			s.bytesRX.Add(int64(len(frame.Payload)))
			if st := s.getStream(frame.StreamID); st != nil {
				if err := st.pushData(frame.Payload); err != nil {
					_ = s.sendFrame(Frame{Type: FrameRST, StreamID: frame.StreamID})
					s.removeStream(frame.StreamID)
				}
			}
		case FrameEOF:
			if st := s.getStream(frame.StreamID); st != nil {
				st.remoteEOF()
			}
		case FrameRST:
			if st := s.getStream(frame.StreamID); st != nil {
				st.fail(io.EOF)
				s.removeStream(frame.StreamID)
			}
		case FramePing:
			_ = s.sendFrame(Frame{Type: FramePong, Payload: frame.Payload})
		case FramePong:
		}
	}
}

type Stream struct {
	session    *Session
	id         uint32
	openCh     chan error
	openOnce   sync.Once
	mu         sync.Mutex
	chunks     [][]byte
	queued     int
	eof        bool
	closed     bool
	writeEOF   bool
	readNotify chan struct{}
	readErr    error
}

func newStream(session *Session, id uint32) *Stream {
	return &Stream{
		session:    session,
		id:         id,
		openCh:     make(chan error, 1),
		readNotify: make(chan struct{}, 1),
	}
}

func (st *Stream) signalRead() {
	select {
	case st.readNotify <- struct{}{}:
	default:
	}
}

func (st *Stream) resolveOpen(err error) {
	st.openOnce.Do(func() {
		if err != nil {
			st.markFailed(err)
		}
		st.openCh <- err
	})
}

func (st *Stream) pushData(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	st.mu.Lock()
	if st.closed || st.eof {
		st.mu.Unlock()
		return nil
	}
	if st.queued+len(data) > maxStreamBuffer {
		st.mu.Unlock()
		st.fail(errors.New("stream receive buffer overflow"))
		return errors.New("stream receive buffer overflow")
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	st.chunks = append(st.chunks, cp)
	st.queued += len(cp)
	st.mu.Unlock()
	st.signalRead()
	return nil
}

func (st *Stream) remoteEOF() {
	st.mu.Lock()
	st.eof = true
	st.mu.Unlock()
	st.signalRead()
}

func (st *Stream) fail(err error) {
	st.openOnce.Do(func() {
		st.openCh <- err
	})
	st.markFailed(err)
}

func (st *Stream) markFailed(err error) {
	st.mu.Lock()
	if st.readErr == nil {
		st.readErr = err
	}
	st.eof = true
	st.closed = true
	st.mu.Unlock()
	st.signalRead()
}

func (st *Stream) Read(p []byte) (int, error) {
	for {
		st.mu.Lock()
		if len(st.chunks) > 0 {
			head := st.chunks[0]
			n := copy(p, head)
			if n == len(head) {
				st.chunks = st.chunks[1:]
			} else {
				st.chunks[0] = head[n:]
			}
			st.queued -= n
			st.mu.Unlock()
			return n, nil
		}
		if st.eof {
			err := st.readErr
			if err == nil {
				err = io.EOF
			}
			st.mu.Unlock()
			return 0, err
		}
		st.mu.Unlock()
		<-st.readNotify
	}
}

func (st *Stream) Write(p []byte) (int, error) {
	st.mu.Lock()
	if st.closed || st.writeEOF {
		st.mu.Unlock()
		return 0, net.ErrClosed
	}
	st.mu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > MaxFramePayload {
			chunk = p[:MaxFramePayload]
		}
		if err := st.session.sendFrame(Frame{Type: FrameData, StreamID: st.id, Payload: chunk}); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (st *Stream) CloseWrite() error {
	st.mu.Lock()
	already := st.writeEOF || st.closed
	st.writeEOF = true
	st.mu.Unlock()
	if already {
		return nil
	}
	return st.session.sendFrame(Frame{Type: FrameEOF, StreamID: st.id})
}

func (st *Stream) Close() error {
	st.mu.Lock()
	already := st.closed
	st.closed = true
	st.eof = true
	if st.readErr == nil {
		st.readErr = net.ErrClosed
	}
	st.mu.Unlock()
	st.signalRead()
	if !already {
		_ = st.session.sendFrame(Frame{Type: FrameRST, StreamID: st.id})
		st.session.removeStream(st.id)
	}
	return nil
}

// RunEdgeConn executes the edge side of the multiplexed tunnel against dialTarget,
// strictly rejecting any port != AllowedTargetPort with OPEN_ERR without dialing.
func RunEdgeConn(ctx context.Context, conn net.Conn, dialTarget func(context.Context, uint16) (net.Conn, error), pingInterval time.Duration) error {
	return runEdgeConn(ctx, conn, dialTarget, pingInterval, defaultWriteTimeout)
}

// RunEdgeConnWithAllowedPorts executes the edge side of the multiplexed tunnel while
// restricting OPEN requests to an explicit subset of IsSupportedTargetPort.
func RunEdgeConnWithAllowedPorts(
	ctx context.Context,
	conn net.Conn,
	dialTarget func(context.Context, uint16) (net.Conn, error),
	pingInterval time.Duration,
	allowedPorts ...uint16,
) error {
	return runEdgeConnWithPorts(ctx, conn, dialTarget, pingInterval, defaultWriteTimeout, buildAllowedPortSet(allowedPorts...))
}

func runEdgeConn(ctx context.Context, conn net.Conn, dialTarget func(context.Context, uint16) (net.Conn, error), pingInterval, writeTimeout time.Duration) error {
	return runEdgeConnWithPorts(ctx, conn, dialTarget, pingInterval, writeTimeout, buildAllowedPortSet(AllowedTargetPort))
}

func runEdgeConnWithPorts(ctx context.Context, conn net.Conn, dialTarget func(context.Context, uint16) (net.Conn, error), pingInterval, writeTimeout time.Duration, allowedPorts map[uint16]struct{}) error {
	var writeMu sync.Mutex
	send := func(frame Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeTimeout > 0 {
			if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				_ = conn.Close()
				return err
			}
		}
		err := WriteFrame(conn, frame)
		if err != nil {
			_ = conn.Close()
		}
		return err
	}
	var streamsMu sync.Mutex
	streams := make(map[uint32]net.Conn)
	closeAll := func() {
		streamsMu.Lock()
		conns := make([]net.Conn, 0, len(streams))
		for id, c := range streams {
			delete(streams, id)
			conns = append(conns, c)
		}
		streamsMu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
		_ = conn.Close()
	}
	defer closeAll()

	if pingInterval > 0 {
		go func() {
			defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_edge_ping", func(any) {
				_ = conn.Close()
			})
			ticker := time.NewTicker(pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := send(Frame{Type: FramePing}); err != nil {
						_ = conn.Close()
						return
					}
				case <-ctx.Done():
					_ = conn.Close()
					return
				}
			}
		}()
	}

	for {
		frame, err := ReadFrame(conn)
		if err != nil {
			return err
		}
		switch frame.Type {
		case FrameOpen:
			if len(frame.Payload) != 2 {
				_ = send(Frame{Type: FrameOpenErr, StreamID: frame.StreamID, Payload: []byte("invalid open payload")})
				continue
			}
			port := binary.BigEndian.Uint16(frame.Payload)
			if _, ok := allowedPorts[port]; !ok {
				_ = send(Frame{Type: FrameOpenErr, StreamID: frame.StreamID, Payload: []byte(ErrPortForbidden.Error())})
				continue
			}
			streamID := frame.StreamID
			go func() {
				defer observability.Recover(slog.Default(), nil, "connectivity.tunnel_edge_stream", func(any) {
					_ = send(Frame{Type: FrameRST, StreamID: streamID})
				})
				dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				targetConn, dialErr := dialTarget(dialCtx, port)
				cancel()
				if dialErr != nil {
					_ = send(Frame{Type: FrameOpenErr, StreamID: streamID, Payload: []byte(dialErr.Error())})
					return
				}
				streamsMu.Lock()
				streams[streamID] = targetConn
				streamsMu.Unlock()
				if err := send(Frame{Type: FrameOpenOK, StreamID: streamID}); err != nil {
					_ = targetConn.Close()
					streamsMu.Lock()
					delete(streams, streamID)
					streamsMu.Unlock()
					return
				}
				buf := make([]byte, 32*1024)
				for {
					n, readErr := targetConn.Read(buf)
					if n > 0 {
						if err := send(Frame{Type: FrameData, StreamID: streamID, Payload: buf[:n]}); err != nil {
							break
						}
					}
					if readErr != nil {
						if errors.Is(readErr, io.EOF) {
							_ = send(Frame{Type: FrameEOF, StreamID: streamID})
						} else {
							_ = send(Frame{Type: FrameRST, StreamID: streamID})
						}
						break
					}
				}
			}()
		case FrameData:
			streamsMu.Lock()
			target := streams[frame.StreamID]
			streamsMu.Unlock()
			if target != nil {
				if _, err := target.Write(frame.Payload); err != nil {
					_ = send(Frame{Type: FrameRST, StreamID: frame.StreamID})
				}
			}
		case FrameEOF:
			streamsMu.Lock()
			target := streams[frame.StreamID]
			streamsMu.Unlock()
			if target != nil {
				if cw, ok := target.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				} else {
					_ = target.Close()
				}
			}
		case FrameRST:
			streamsMu.Lock()
			target := streams[frame.StreamID]
			delete(streams, frame.StreamID)
			streamsMu.Unlock()
			if target != nil {
				_ = target.Close()
			}
		case FramePing:
			_ = send(Frame{Type: FramePong, Payload: frame.Payload})
		case FramePong:
		}
	}
}
