package mqttguard

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/adapter"
)

const (
	defaultHandshakeTimeout       = 10 * time.Second
	defaultUpstreamDialTimeout    = 10 * time.Second
	defaultEphemeralCertTTL       = 15 * time.Minute
	defaultPerIPRate              = 20.0
	defaultPerIPBurst             = 40
	defaultPerGatewayRate         = 5.0
	defaultPerGatewayBurst        = 10
	metricGuardHandshakes         = "hvac_mqtt_guard_handshakes_total"
	metricGuardHandshakesHelp     = "MQTT guard mTLS gateway handshakes by outcome."
	metricGuardActiveSessions     = "hvac_mqtt_guard_active_sessions"
	metricGuardActiveSessionsHelp = "Active MQTT guard mTLS gateway sessions."
)

// CredentialVerifier validates that a verified client certificate maps to an active,
// non-revoked gateway credential and returns its canonical gateway ID.
type CredentialVerifier interface {
	VerifyActiveCredential(ctx context.Context, peer *x509.Certificate) (string, error)
}

// EphemeralCertIssuer mints short-lived client certificates under the Gateway CA so
// upstream broker sessions continue to identify the gateway by CN.
type EphemeralCertIssuer interface {
	IssueEphemeralGatewayTLS(gatewayID string, ttl time.Duration, now time.Time) (tls.Certificate, error)
}

// Config configures the TLS 1.3 terminating MQTT guard proxy.
type Config struct {
	ListenAddr            string
	ServerCertificate     tls.Certificate
	GatewayClientCAs      *x509.CertPool
	UpstreamAddr          string
	UpstreamRootCAs       *x509.CertPool
	UpstreamServerName    string
	Verifier              CredentialVerifier
	Issuer                EphemeralCertIssuer
	EphemeralCertTTL      time.Duration
	HandshakeTimeout      time.Duration
	UpstreamDialTimeout   time.Duration
	PerIPHandshakesPerSec float64
	PerIPBurst            int
	PerGatewayPerSec      float64
	PerGatewayBurst       int
	DisableRateLimits     bool
	Logger                *slog.Logger
	Metrics               *observability.Registry
	Now                   func() time.Time
	OnGatewayDisconnect   func(ctx context.Context, gatewayID string, disconnectedAt time.Time)
}

type activeSession struct {
	id                uint64
	gatewayID         string
	fingerprintSHA256 string
	downstream        net.Conn
	upstream          net.Conn
}

// Server terminates external gateway mTLS 1.3 MQTT connections, checks handshake rate
// limits and active/non-revoked credentials before dialing the internal Mosquitto broker
// with an ephemeral Gateway CA certificate, and supports instant revocation kicks.
type Server struct {
	cfg       Config
	tlsConfig *tls.Config
	ipLimiter *adapter.GatewayRateLimiter
	gwLimiter *adapter.GatewayRateLimiter
	now       func() time.Time
	logger    *slog.Logger
	metrics   *observability.Registry

	mu           sync.RWMutex
	onDisconnect func(context.Context, string, time.Time)
	closed       bool
	listener     net.Listener
	nextID       atomic.Uint64
	sessions     map[uint64]*activeSession
	byGateway    map[string]map[uint64]struct{}
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	wg           sync.WaitGroup
}

// New validates cfg and creates an unstarted mqttguard Server.
func New(cfg Config) (*Server, error) {
	if len(cfg.ServerCertificate.Certificate) == 0 || cfg.ServerCertificate.PrivateKey == nil {
		return nil, errors.New("mqttguard server certificate is required")
	}
	if cfg.GatewayClientCAs == nil {
		return nil, errors.New("mqttguard gateway client CA pool is required")
	}
	if strings.TrimSpace(cfg.UpstreamAddr) == "" {
		return nil, errors.New("mqttguard upstream address is required")
	}
	if cfg.UpstreamRootCAs == nil {
		return nil, errors.New("mqttguard upstream root CA pool is required")
	}
	if cfg.Verifier == nil {
		return nil, errors.New("mqttguard credential verifier is required")
	}
	if cfg.Issuer == nil {
		return nil, errors.New("mqttguard ephemeral certificate issuer is required")
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	if cfg.UpstreamDialTimeout <= 0 {
		cfg.UpstreamDialTimeout = defaultUpstreamDialTimeout
	}
	if cfg.EphemeralCertTTL <= 0 {
		cfg.EphemeralCertTTL = defaultEphemeralCertTTL
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var ipLimiter, gwLimiter *adapter.GatewayRateLimiter
	if !cfg.DisableRateLimits {
		ipRate := cfg.PerIPHandshakesPerSec
		if ipRate == 0 {
			ipRate = defaultPerIPRate
		}
		ipBurst := cfg.PerIPBurst
		if ipBurst == 0 {
			ipBurst = defaultPerIPBurst
		}
		gwRate := cfg.PerGatewayPerSec
		if gwRate == 0 {
			gwRate = defaultPerGatewayRate
		}
		gwBurst := cfg.PerGatewayBurst
		if gwBurst == 0 {
			gwBurst = defaultPerGatewayBurst
		}
		ipLimiter = adapter.NewGatewayRateLimiter(ipRate, ipBurst)
		gwLimiter = adapter.NewGatewayRateLimiter(gwRate, gwBurst)
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cfg.ServerCertificate},
		ClientCAs:    cfg.GatewayClientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())

	s := &Server{
		cfg:          cfg,
		tlsConfig:    tlsCfg,
		ipLimiter:    ipLimiter,
		gwLimiter:    gwLimiter,
		now:          nowFn,
		logger:       logger,
		metrics:      cfg.Metrics,
		onDisconnect: cfg.OnGatewayDisconnect,
		sessions:     make(map[uint64]*activeSession),
		byGateway:    make(map[string]map[uint64]struct{}),
		rootCtx:      rootCtx,
		rootCancel:   rootCancel,
	}
	s.recordActiveSessionsGauge(0)
	return s, nil
}

// SetOnGatewayDisconnect updates the callback fired when a gateway's active guard
// sessions drop to zero.
func (s *Server) SetOnGatewayDisconnect(fn func(context.Context, string, time.Time)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onDisconnect = fn
	s.mu.Unlock()
}

// ListenAndServe binds cfg.ListenAddr over TCP and serves incoming mTLS 1.3 connections
// until Shutdown is called or an accept error occurs.
func (s *Server) ListenAndServe() error {
	addr := strings.TrimSpace(s.cfg.ListenAddr)
	if addr == "" {
		return errors.New("mqttguard listen address is required")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve accepts incoming gateway TCP connections on ln and terminates TLS 1.3 per session.
func (s *Server) Serve(ln net.Listener) error {
	if s == nil {
		return errors.New("mqttguard server is nil")
	}
	if ln == nil {
		return errors.New("mqttguard listener is nil")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return net.ErrClosed
	}
	s.listener = ln
	s.mu.Unlock()

	for {
		rawConn, err := ln.Accept()
		if err != nil {
			s.mu.RLock()
			closed := s.closed
			s.mu.RUnlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return net.ErrClosed
			}
			return err
		}
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			_ = rawConn.Close()
			return net.ErrClosed
		}
		s.wg.Add(1)
		s.mu.RUnlock()

		go func(c net.Conn) {
			defer s.wg.Done()
			defer observability.Recover(s.logger, s.metrics, "connectivity.mqtt_guard_session")
			s.handleConn(c)
		}(rawConn)
	}
}

// CloseGateway immediately closes all active proxied MQTT connections for gatewayID.
func (s *Server) CloseGateway(gatewayID string) int {
	normalized := strings.ToLower(strings.TrimSpace(gatewayID))
	if s == nil || normalized == "" {
		return 0
	}
	return s.closeMatchingSessions(func(sess *activeSession) bool {
		return sess.gatewayID == normalized
	})
}

// CloseGatewayByFingerprint immediately closes active proxied connections authenticated
// with the matching gateway certificate SHA-256 fingerprint.
func (s *Server) CloseGatewayByFingerprint(gatewayID, fingerprintSHA256 string) int {
	normalizedGW := strings.ToLower(strings.TrimSpace(gatewayID))
	normalizedFP := strings.ToLower(strings.TrimSpace(fingerprintSHA256))
	if s == nil || normalizedFP == "" {
		return 0
	}
	return s.closeMatchingSessions(func(sess *activeSession) bool {
		if normalizedGW != "" && sess.gatewayID != normalizedGW {
			return false
		}
		return sess.fingerprintSHA256 == normalizedFP
	})
}

// Shutdown closes the listener and all active sessions and waits for handlers to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	s.closed = true
	ln := s.listener
	s.listener = nil
	conns := make([]*activeSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		conns = append(conns, sess)
	}
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, sess := range conns {
		if sess.downstream != nil {
			_ = sess.downstream.Close()
		}
		if sess.upstream != nil {
			_ = sess.upstream.Close()
		}
	}
	if s.rootCancel != nil {
		s.rootCancel()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) closeMatchingSessions(match func(*activeSession) bool) int {
	s.mu.RLock()
	targets := make([]*activeSession, 0)
	for _, sess := range s.sessions {
		if match(sess) {
			targets = append(targets, sess)
		}
	}
	s.mu.RUnlock()

	for _, sess := range targets {
		if sess.downstream != nil {
			_ = sess.downstream.Close()
		}
		if sess.upstream != nil {
			_ = sess.upstream.Close()
		}
	}
	return len(targets)
}

func (s *Server) handleConn(rawConn net.Conn) {
	remoteIP := remoteHostKey(rawConn.RemoteAddr())
	if s.ipLimiter != nil && remoteIP != "" && !s.ipLimiter.Allow(remoteIP, s.now().UTC()) {
		s.recordHandshake("dropped")
		_ = rawConn.Close()
		return
	}

	tlsConn := tls.Server(rawConn, s.tlsConfig)
	hsCtx, hsCancel := context.WithTimeout(s.rootCtx, s.cfg.HandshakeTimeout)
	err := tlsConn.HandshakeContext(hsCtx)
	hsCancel()
	if err != nil {
		s.recordHandshake("failed")
		_ = tlsConn.Close()
		return
	}

	leaf := leafCertificate(tlsConn.ConnectionState())
	if leaf == nil {
		s.recordHandshake("rejected")
		_ = tlsConn.Close()
		return
	}
	sum := sha256.Sum256(leaf.Raw)
	fingerprint := hex.EncodeToString(sum[:])

	verifyCtx, verifyCancel := context.WithTimeout(s.rootCtx, s.cfg.HandshakeTimeout)
	gatewayID, verifyErr := s.cfg.Verifier.VerifyActiveCredential(verifyCtx, leaf)
	verifyCancel()
	if verifyErr != nil {
		s.recordHandshake("rejected")
		_ = tlsConn.Close()
		return
	}
	gatewayID = strings.ToLower(strings.TrimSpace(gatewayID))
	leafCN := strings.ToLower(strings.TrimSpace(leaf.Subject.CommonName))
	if gatewayID == "" || leafCN == "" || gatewayID != leafCN {
		s.recordHandshake("rejected")
		_ = tlsConn.Close()
		return
	}

	if s.gwLimiter != nil && !s.gwLimiter.Allow(gatewayID, s.now().UTC()) {
		s.recordHandshake("dropped")
		_ = tlsConn.Close()
		return
	}

	now := s.now().UTC()
	ephemeralCert, err := s.cfg.Issuer.IssueEphemeralGatewayTLS(gatewayID, s.cfg.EphemeralCertTTL, now)
	if err != nil {
		s.recordHandshake("failed")
		s.logger.Warn("mqttguard_ephemeral_cert_failed", "gateway_id", gatewayID, "error", err.Error())
		_ = tlsConn.Close()
		return
	}

	dialCtx, dialCancel := context.WithTimeout(s.rootCtx, s.cfg.UpstreamDialTimeout)
	upstreamTLS := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      s.cfg.UpstreamRootCAs,
		ServerName:   strings.TrimSpace(s.cfg.UpstreamServerName),
		Certificates: []tls.Certificate{ephemeralCert},
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: s.cfg.UpstreamDialTimeout},
		Config:    upstreamTLS,
	}
	upstreamConn, err := dialer.DialContext(dialCtx, "tcp", s.cfg.UpstreamAddr)
	dialCancel()
	if err != nil {
		s.recordHandshake("failed")
		s.logger.Warn("mqttguard_upstream_dial_failed", "gateway_id", gatewayID, "error", err.Error())
		_ = tlsConn.Close()
		return
	}

	sess := &activeSession{
		id:                s.nextID.Add(1),
		gatewayID:         gatewayID,
		fingerprintSHA256: fingerprint,
		downstream:        tlsConn,
		upstream:          upstreamConn,
	}
	if !s.registerSession(sess) {
		_ = tlsConn.Close()
		_ = upstreamConn.Close()
		return
	}
	s.recordHandshake("accepted")

	defer func() {
		_ = tlsConn.Close()
		_ = upstreamConn.Close()
		lastForGateway := s.unregisterSession(sess)
		if lastForGateway && s.rootCtx.Err() == nil {
			s.fireGatewayDisconnect(gatewayID, s.now().UTC())
		}
	}()

	spliceTLSConns(tlsConn, upstreamConn, s.logger, s.metrics)
}

func (s *Server) registerSession(sess *activeSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.sessions[sess.id] = sess
	gwSessions := s.byGateway[sess.gatewayID]
	if gwSessions == nil {
		gwSessions = make(map[uint64]struct{}, 1)
		s.byGateway[sess.gatewayID] = gwSessions
	}
	gwSessions[sess.id] = struct{}{}
	s.recordActiveSessionsGauge(len(s.sessions))
	return true
}

func (s *Server) unregisterSession(sess *activeSession) bool {
	s.mu.Lock()
	delete(s.sessions, sess.id)
	lastForGateway := false
	if gwSessions := s.byGateway[sess.gatewayID]; gwSessions != nil {
		delete(gwSessions, sess.id)
		if len(gwSessions) == 0 {
			delete(s.byGateway, sess.gatewayID)
			lastForGateway = true
		}
	}
	remainingTotal := len(s.sessions)
	closed := s.closed
	s.mu.Unlock()

	s.recordActiveSessionsGauge(remainingTotal)
	return lastForGateway && !closed
}

func (s *Server) fireGatewayDisconnect(gatewayID string, disconnectedAt time.Time) {
	s.mu.RLock()
	fn := s.onDisconnect
	s.mu.RUnlock()
	if fn != nil {
		fn(s.rootCtx, gatewayID, disconnectedAt)
	}
}

func (s *Server) recordHandshake(outcome string) {
	if s.metrics == nil {
		return
	}
	_ = s.metrics.AddCounter(metricGuardHandshakes, metricGuardHandshakesHelp, map[string]string{"outcome": outcome}, 1)
}

func (s *Server) recordActiveSessionsGauge(count int) {
	if s.metrics == nil {
		return
	}
	_ = s.metrics.SetGauge(metricGuardActiveSessions, metricGuardActiveSessionsHelp, nil, float64(count))
}

func leafCertificate(state tls.ConnectionState) *x509.Certificate {
	if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
		return state.VerifiedChains[0][0]
	}
	if len(state.PeerCertificates) > 0 {
		return state.PeerCertificates[0]
	}
	return nil
}

func remoteHostKey(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func spliceTLSConns(a, b net.Conn, logger *slog.Logger, metrics *observability.Registry) {
	var wg sync.WaitGroup
	wg.Add(2)
	copySide := func(dst, src net.Conn) {
		defer wg.Done()
		defer observability.Recover(logger, metrics, "connectivity.mqtt_guard_splice", func(any) {
			_ = a.Close()
			_ = b.Close()
		})
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go copySide(b, a)
	go copySide(a, b)
	wg.Wait()
}
