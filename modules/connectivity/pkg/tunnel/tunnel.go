package tunnel

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ProtocolName             = "hvac-gateway-tunnel/v1"
	AllowedTargetPort uint16 = 1880
	MaxFramePayload          = 64 * 1024
	maxStreamBuffer          = 4 * 1024 * 1024
)

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

type Hub struct {
	mu               sync.RWMutex
	sessions         map[string]*Session
	listeners        []net.Listener
	defaultGatewayID string
	pingInterval     time.Duration
	readTimeout      time.Duration
	openTimeout      time.Duration
	closed           atomic.Bool
}

func NewHub(options ...Option) *Hub {
	h := &Hub{
		sessions:     make(map[string]*Session),
		pingInterval: 25 * time.Second,
		readTimeout:  75 * time.Second,
		openTimeout:  10 * time.Second,
	}
	for _, option := range options {
		option(h)
	}
	return h
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
	}
	h.mu.Unlock()
	if session != nil {
		session.Close()
	}
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
	h.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, s := range sessions {
		s.Close()
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
	if targetPort != AllowedTargetPort {
		return nil, fmt.Errorf("%w: port %d not allowed", ErrPortForbidden, targetPort)
	}
	session, err := h.resolveSession(gatewayID)
	if err != nil {
		return nil, err
	}
	return session.openStream(ctx, targetPort, h.openTimeout)
}

func (h *Hub) registerSession(gatewayID string, conn net.Conn) *Session {
	s := newSession(h, gatewayID, conn)
	h.mu.Lock()
	if h.closed.Load() {
		h.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	previous := h.sessions[gatewayID]
	h.sessions[gatewayID] = s
	h.mu.Unlock()
	if previous != nil && previous != s {
		previous.Close()
	}
	return s
}

func (h *Hub) unregisterSession(s *Session) {
	h.mu.Lock()
	if h.sessions[s.gatewayID] == s {
		delete(h.sessions, s.gatewayID)
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
		// Clear http.Server read/write deadlines immediately so long-lived idle tunnels survive past 10s.
		if err := conn.SetDeadline(time.Time{}); err != nil {
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
		session := hub.registerSession(gatewayID, tunnelConn)
		if session == nil {
			return
		}
		// Re-check revocation after registration so a concurrent Revoke cannot slip in between pre-hijack auth and registerSession.
		if verifiedID, recheckErr := verifyPeer(r.Context(), peer); recheckErr != nil || verifiedID != gatewayID {
			hub.unregisterSession(session)
			session.Close()
			return
		}
		session.run(r.Context())
	})
}

func (h *Hub) StartLoopbackListener(addr string) (net.Listener, error) {
	if err := ValidateLoopbackAddr(addr); err != nil {
		return nil, err
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
	go h.acceptLoop(ln)
	return ln, nil
}

func (h *Hub) acceptLoop(ln net.Listener) {
	for {
		localConn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serveLoopbackConn(localConn)
	}
}

func (h *Hub) serveLoopbackConn(localConn net.Conn) {
	defer func() { _ = localConn.Close() }()
	openCtx, cancel := context.WithTimeout(context.Background(), h.openTimeout)
	stream, err := h.OpenStream(openCtx, "", AllowedTargetPort)
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
		_, copyErr := io.Copy(conn, in)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = conn.Close()
		}
		inDone <- copyErr
	}()
	go func() {
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
		_, _ = io.Copy(b, a)
		_ = b.CloseWrite()
	}()
	go func() {
		defer wg.Done()
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
	hub        *Hub
	gatewayID  string
	conn       net.Conn
	writeMu    sync.Mutex
	mu         sync.Mutex
	streams    map[uint32]*Stream
	nextStream uint32
	closed     atomic.Bool
	done       chan struct{}
}

func newSession(hub *Hub, gatewayID string, conn net.Conn) *Session {
	return &Session{
		hub:        hub,
		gatewayID:  gatewayID,
		conn:       conn,
		streams:    make(map[uint32]*Stream),
		nextStream: 1,
		done:       make(chan struct{}),
	}
}

func (s *Session) sendFrame(frame Frame) error {
	if s.closed.Load() {
		return net.ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return net.ErrClosed
	}
	return WriteFrame(s.conn, frame)
}

func (s *Session) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	close(s.done)
	_ = s.conn.Close()
	s.mu.Lock()
	streams := make([]*Stream, 0, len(s.streams))
	for id, st := range s.streams {
		delete(s.streams, id)
		streams = append(streams, st)
	}
	s.mu.Unlock()
	for _, st := range streams {
		st.fail(io.EOF)
	}
	s.hub.unregisterSession(s)
}

func (s *Session) openStream(ctx context.Context, targetPort uint16, timeout time.Duration) (*Stream, error) {
	if s.closed.Load() {
		return nil, net.ErrClosed
	}
	s.mu.Lock()
	id := s.nextStream
	s.nextStream += 2
	st := newStream(s, id)
	s.streams[id] = st
	s.mu.Unlock()

	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], targetPort)
	if err := s.sendFrame(Frame{Type: FrameOpen, StreamID: id, Payload: portBuf[:]}); err != nil {
		s.removeStream(id)
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-st.openCh:
		if err != nil {
			s.removeStream(id)
			return nil, err
		}
		return st, nil
	case <-ctx.Done():
		_ = s.sendFrame(Frame{Type: FrameRST, StreamID: id})
		s.removeStream(id)
		return nil, ctx.Err()
	case <-s.done:
		s.removeStream(id)
		return nil, io.EOF
	case <-timer.C:
		_ = s.sendFrame(Frame{Type: FrameRST, StreamID: id})
		s.removeStream(id)
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
	defer s.Close()
	go func() {
		ticker := time.NewTicker(s.hub.pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := s.sendFrame(Frame{Type: FramePing}); err != nil {
					s.Close()
					return
				}
			case <-ctx.Done():
				s.Close()
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
			st.fail(err)
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
	var writeMu sync.Mutex
	send := func(frame Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return WriteFrame(conn, frame)
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
			if port != AllowedTargetPort {
				_ = send(Frame{Type: FrameOpenErr, StreamID: frame.StreamID, Payload: []byte(ErrPortForbidden.Error())})
				continue
			}
			streamID := frame.StreamID
			go func() {
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
