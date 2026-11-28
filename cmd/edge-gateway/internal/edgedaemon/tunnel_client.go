package edgedaemon

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgetunnel"
)

const (
	initialTunnelReconnectDelay = 1 * time.Second
	maxTunnelReconnectDelay     = 30 * time.Second
)

type bufferedNetConn struct {
	net.Conn
	r io.Reader
}

func (c bufferedNetConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// TunnelClientOptions configures the gateway outbound reverse tunnel client.
type TunnelClientOptions struct {
	PlatformBaseURL    string
	ServerName         string
	CAFile             string
	CertFile           string
	KeyFile            string
	AllowedTargetPorts []uint16
	PingInterval       time.Duration
	LocalDialTarget    func(context.Context, uint16) (net.Conn, error)
	OnConnected        func()
	OnDisconnected     func(error)
	Logger             *slog.Logger
}

// TunnelClient maintains an outbound mTLS WebSocket/Upgrade reverse tunnel session
// to /gateway/v1/tunnel and delegates stream multiplexing to edgetunnel.RunEdgeConnWithAllowedPorts.
type TunnelClient struct {
	opts          TunnelClientOptions
	dialAddr      string
	requestHost   string
	requestPath   string
	allowedPorts  []uint16
	allowedSet    map[uint16]struct{}
	pingInterval  time.Duration
	logger        *slog.Logger
	reloadCh      chan struct{}
	mu            sync.Mutex
	sessionCancel context.CancelFunc
	reloadPending bool
}

// NewTunnelClient validates options and constructs a TunnelClient.
func NewTunnelClient(opts TunnelClientOptions) (*TunnelClient, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(opts.PlatformBaseURL))
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || strings.TrimSpace(parsedURL.Host) == "" {
		return nil, fmt.Errorf("invalid tunnel platformBaseUrl %q", opts.PlatformBaseURL)
	}
	dialAddr := parsedURL.Host
	if _, _, splitErr := net.SplitHostPort(dialAddr); splitErr != nil {
		port := "443"
		if parsedURL.Scheme == "http" {
			port = "80"
		}
		dialAddr = net.JoinHostPort(dialAddr, port)
	}
	basePath := strings.TrimRight(parsedURL.Path, "/")
	requestPath := basePath + "/gateway/v1/tunnel"

	ports := slices.Clone(opts.AllowedTargetPorts)
	if len(ports) == 0 {
		ports = []uint16{edgetunnel.SSHTargetPort}
	}
	allowedSet := make(map[uint16]struct{}, len(ports))
	dedupPorts := make([]uint16, 0, len(ports))
	for _, port := range ports {
		if !edgetunnel.IsSupportedTargetPort(port) {
			return nil, fmt.Errorf("unsupported tunnel target port %d", port)
		}
		if _, dup := allowedSet[port]; dup {
			continue
		}
		allowedSet[port] = struct{}{}
		dedupPorts = append(dedupPorts, port)
	}
	slices.Sort(dedupPorts)

	pingInterval := opts.PingInterval
	if pingInterval <= 0 {
		pingInterval = 25 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TunnelClient{
		opts:         opts,
		dialAddr:     dialAddr,
		requestHost:  parsedURL.Host,
		requestPath:  requestPath,
		allowedPorts: dedupPorts,
		allowedSet:   allowedSet,
		pingInterval: pingInterval,
		logger:       logger,
		reloadCh:     make(chan struct{}, 1),
	}, nil
}

// ReloadCredentials closes the active tunnel connection (if any) so the loop
// immediately redials using the updated X.509 certificate files on disk.
func (c *TunnelClient) ReloadCredentials() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.reloadPending = true
	cancel := c.sessionCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case c.reloadCh <- struct{}{}:
	default:
	}
}

// Run maintains the reverse tunnel connection with exponential backoff until ctx is canceled.
func (c *TunnelClient) Run(ctx context.Context) error {
	backoff := initialTunnelReconnectDelay
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		startedAt := time.Now()
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.opts.OnDisconnected != nil {
			c.opts.OnDisconnected(err)
		}

		c.mu.Lock()
		immediate := c.reloadPending
		c.reloadPending = false
		c.mu.Unlock()
		if immediate {
			backoff = initialTunnelReconnectDelay
			continue
		}

		if err != nil {
			c.logger.Warn("gateway_tunnel_disconnected", "error", err.Error(), "retry_in", backoff.String())
		}
		if time.Since(startedAt) > maxTunnelReconnectDelay {
			backoff = initialTunnelReconnectDelay
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.reloadCh:
			timer.Stop()
			backoff = initialTunnelReconnectDelay
		case <-timer.C:
			backoff = min(backoff*2, maxTunnelReconnectDelay)
		}
	}
}

func (c *TunnelClient) connectAndServe(ctx context.Context) error {
	tlsCfg, id, err := newMTLSClientConfig(c.opts.CAFile, c.opts.CertFile, c.opts.KeyFile, c.opts.ServerName)
	if err != nil {
		return err
	}
	tlsCfg.NextProtos = []string{"http/1.1"}

	sessionCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.sessionCancel = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		c.sessionCancel = nil
		c.mu.Unlock()
	}()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    tlsCfg,
	}
	rawConn, err := dialer.DialContext(sessionCtx, "tcp", c.dialAddr)
	if err != nil {
		return fmt.Errorf("dial tunnel %s: %w", c.dialAddr, err)
	}
	defer func() { _ = rawConn.Close() }()

	req, err := http.NewRequestWithContext(sessionCtx, http.MethodGet, "https://"+c.requestHost+c.requestPath, nil)
	if err != nil {
		return err
	}
	req.Host = c.requestHost
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", edgetunnel.ProtocolName)
	req.Header.Set("User-Agent", "hvac-edge-gateway")
	if err := signHTTPPoPIfPresent(req, id, time.Now().UTC()); err != nil {
		return fmt.Errorf("sign tunnel PoP: %w", err)
	}
	if err := req.Write(rawConn); err != nil {
		return fmt.Errorf("write tunnel upgrade request: %w", err)
	}

	br := bufio.NewReader(rawConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return fmt.Errorf("read tunnel upgrade response: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Upgrade")), edgetunnel.ProtocolName) {
		return fmt.Errorf("tunnel upgrade rejected with HTTP %d", resp.StatusCode)
	}

	muxConn := rawConn
	if br.Buffered() > 0 {
		muxConn = bufferedNetConn{Conn: rawConn, r: br}
	}
	if c.opts.OnConnected != nil {
		c.opts.OnConnected()
	}

	return edgetunnel.RunEdgeConnWithAllowedPorts(sessionCtx, muxConn, c.dialLocalTarget, c.pingInterval, c.allowedPorts...)
}

func (c *TunnelClient) dialLocalTarget(ctx context.Context, port uint16) (net.Conn, error) {
	if _, ok := c.allowedSet[port]; !ok || !edgetunnel.IsSupportedTargetPort(port) {
		return nil, edgetunnel.ErrPortForbidden
	}
	if c.opts.LocalDialTarget != nil {
		return c.opts.LocalDialTarget(ctx, port)
	}
	targetAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", targetAddr)
}
