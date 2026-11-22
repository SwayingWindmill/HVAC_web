package edgecontrol

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// OPCUATransport defines the pooled OPC-UA client session and node operations.
type OPCUATransport interface {
	ReadNodes(ctx context.Context, nodes []OPCUANodeID) ([]OPCUADataValue, error)
	WriteNodes(ctx context.Context, writes []OPCUAWriteValue) ([]uint32, error)
	EnsureSubscriptions(ctx context.Context, intervalMS int, nodes []OPCUANodeID) error
	PollPublish(ctx context.Context) ([]OPCUAMonitoredNotification, error)
	Close() error
}

// OPCUABridgeConfig configures a binary TCP IEC 62541 OPC-UA protocol bridge.
type OPCUABridgeConfig struct {
	Endpoint         string
	SecurityPolicy   string
	SecurityMode     string
	AuthMode         string
	Username         string
	CredentialRef    string
	CredentialLookup func(credentialRef string) (string, error)
	Timeout          time.Duration
	Retries          int
}

// OPCUABridge manages a persistent binary TCP SecureChannel + Session to an OPC-UA server,
// automatically reconnecting and restoring subscriptions after disconnect.
type OPCUABridge struct {
	cfg     OPCUABridgeConfig
	tcpAddr string

	mu             sync.Mutex
	closed         bool
	conn           net.Conn
	channelID      uint32
	tokenID        uint32
	sessionToken   uint32
	clientKeys     OPCUACryptoKeys
	serverKeys     OPCUACryptoKeys
	hasKeys        bool
	seqNum         atomic.Uint32
	reqID          atomic.Uint32
	subID          uint32
	subIntervalMS  int
	subNodes       []OPCUANodeID
	subEstablished bool
}

// NewOPCUABridge validates cfg and returns an OPC-UA client transport (lazy or eager dial on first op).
func NewOPCUABridge(cfg OPCUABridgeConfig) (*OPCUABridge, error) {
	if err := validateOPCUAEndpoint(cfg.Endpoint); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(cfg.Endpoint)
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Second
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	if cfg.SecurityPolicy == "" {
		cfg.SecurityPolicy = OPCUASecurityPolicyNone
	}
	if cfg.SecurityMode == "" {
		cfg.SecurityMode = OPCUASecurityModeNone
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = OPCUAAuthAnonymous
	}
	return &OPCUABridge{
		cfg:     cfg,
		tcpAddr: parsed.Host,
	}, nil
}

// ReadNodes reads the Current Values of nodes cleanly over the active SecureChannel.
func (b *OPCUABridge) ReadNodes(ctx context.Context, nodes []OPCUANodeID) ([]OPCUADataValue, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	respBody, err := b.roundTripWithRetry(ctx, EncodeOPCUAReadRequest(nodes))
	if err != nil {
		return nil, err
	}
	status, vals, err := DecodeOPCUAReadResponse(respBody)
	if err != nil {
		return nil, err
	}
	if status != OPCUAStatusGood {
		return nil, fmt.Errorf("OPC-UA Read failed status=0x%08x", status)
	}
	return vals, nil
}

// WriteNodes writes node values and returns per-node OPC-UA status codes.
func (b *OPCUABridge) WriteNodes(ctx context.Context, writes []OPCUAWriteValue) ([]uint32, error) {
	if len(writes) == 0 {
		return nil, nil
	}
	respBody, err := b.roundTripWithRetry(ctx, EncodeOPCUAWriteRequest(writes))
	if err != nil {
		return nil, err
	}
	status, codes, err := DecodeOPCUAWriteResponse(respBody)
	if err != nil {
		return nil, err
	}
	if status != OPCUAStatusGood {
		return nil, fmt.Errorf("OPC-UA Write failed status=0x%08x", status)
	}
	return codes, nil
}

// EnsureSubscriptions establishes or restores an OPC-UA Subscription + MonitoredItems for nodes.
func (b *OPCUABridge) EnsureSubscriptions(ctx context.Context, intervalMS int, nodes []OPCUANodeID) error {
	if len(nodes) == 0 {
		return nil
	}
	b.mu.Lock()
	b.subIntervalMS = intervalMS
	b.subNodes = append([]OPCUANodeID(nil), nodes...)
	needSetup := !b.subEstablished || b.conn == nil
	b.mu.Unlock()
	if !needSetup {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureConnectedLocked(ctx); err != nil {
		return err
	}
	return b.setupSubscriptionLocked(ctx)
}

// PollPublish retrieves queued DataChange notifications from the active subscription.
func (b *OPCUABridge) PollPublish(ctx context.Context) ([]OPCUAMonitoredNotification, error) {
	b.mu.Lock()
	subID := b.subID
	hasSub := b.subEstablished
	b.mu.Unlock()
	if !hasSub || subID == 0 {
		return nil, nil
	}
	respBody, err := b.roundTripWithRetry(ctx, EncodeOPCUAPublishRequest(subID))
	if err != nil {
		return nil, err
	}
	status, _, notifs, err := DecodeOPCUAPublishResponse(respBody)
	if err != nil {
		return nil, err
	}
	if status != OPCUAStatusGood {
		return nil, fmt.Errorf("OPC-UA Publish failed status=0x%08x", status)
	}
	return notifs, nil
}

// Close closes the underlying TCP session gracefully.
func (b *OPCUABridge) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	return b.resetConnLocked()
}

func (b *OPCUABridge) resetConnLocked() error {
	var err error
	if b.conn != nil {
		err = b.conn.Close()
		b.conn = nil
	}
	b.channelID = 0
	b.tokenID = 0
	b.sessionToken = 0
	b.hasKeys = false
	b.subEstablished = false
	b.subID = 0
	return err
}

func (b *OPCUABridge) roundTripWithRetry(ctx context.Context, reqBody []byte) ([]byte, error) {
	if b == nil {
		return nil, errors.New("OPC-UA bridge is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	var lastErr error
	attempts := 1 + b.cfg.Retries
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if b.closed {
			return nil, errors.New("OPC-UA bridge is closed")
		}
		if err := b.ensureConnectedLocked(ctx); err != nil {
			lastErr = err
			_ = b.resetConnLocked()
			continue
		}
		if !b.subEstablished && len(b.subNodes) > 0 {
			if subErr := b.setupSubscriptionLocked(ctx); subErr != nil {
				lastErr = subErr
				_ = b.resetConnLocked()
				continue
			}
		}
		resp, err := b.sendAndReceiveMSGLocked(ctx, reqBody)
		if err != nil {
			lastErr = err
			_ = b.resetConnLocked()
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func (b *OPCUABridge) deadlineForCtx(ctx context.Context) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return time.Now().Add(b.cfg.Timeout)
}

func (b *OPCUABridge) ensureConnectedLocked(ctx context.Context) error {
	if b.conn != nil {
		return nil
	}
	dialCtx, cancel := context.WithDeadline(ctx, b.deadlineForCtx(ctx))
	defer cancel()
	conn, err := (&net.Dialer{Timeout: b.cfg.Timeout}).DialContext(dialCtx, "tcp", b.tcpAddr)
	if err != nil {
		return fmt.Errorf("dial OPC-UA %s: %w", b.tcpAddr, err)
	}
	b.conn = conn
	_ = conn.SetDeadline(b.deadlineForCtx(ctx))

	// 1. HEL / ACK
	if _, err := conn.Write(EncodeOPCUAHello(b.cfg.Endpoint)); err != nil {
		_ = b.resetConnLocked()
		return err
	}
	mType, frame, err := ReadOPCUAFrame(conn)
	if err != nil {
		_ = b.resetConnLocked()
		return err
	}
	if mType == OPCUAMsgError {
		code, reason, _ := DecodeOPCUAErrorFrame(frame)
		_ = b.resetConnLocked()
		return fmt.Errorf("OPC-UA HEL rejected (0x%08x): %s", code, reason)
	}
	if err := DecodeOPCUAAcknowledge(frame); err != nil {
		_ = b.resetConnLocked()
		return err
	}

	// 2. OPN SecureChannel + Key Derivation
	var clientNonce []byte
	if b.cfg.SecurityPolicy == OPCUASecurityPolicyBasic256Sha256 {
		clientNonce = make([]byte, 32)
		if _, err := rand.Read(clientNonce); err != nil {
			_ = b.resetConnLocked()
			return err
		}
	}
	seq := b.seqNum.Add(1)
	req := b.reqID.Add(1)
	opnReq := EncodeOPCUAOpenRequest(OPCUAOpenChannelFrame{
		SecurityPolicy: b.cfg.SecurityPolicy,
		SecurityMode:   OPCUASecurityModeCode(b.cfg.SecurityMode),
		SeqNum:         seq,
		RequestID:      req,
		ClientNonce:    clientNonce,
	})
	if _, err := conn.Write(opnReq); err != nil {
		_ = b.resetConnLocked()
		return err
	}
	mType, frame, err = ReadOPCUAFrame(conn)
	if err != nil {
		_ = b.resetConnLocked()
		return err
	}
	if mType == OPCUAMsgError {
		code, reason, _ := DecodeOPCUAErrorFrame(frame)
		_ = b.resetConnLocked()
		return fmt.Errorf("OPC-UA OPN rejected (0x%08x): %s", code, reason)
	}
	opnResp, err := DecodeOPCUAOpenResponse(frame)
	if err != nil {
		_ = b.resetConnLocked()
		return err
	}
	b.channelID = opnResp.TokenChannelID
	b.tokenID = opnResp.TokenID
	if b.cfg.SecurityPolicy == OPCUASecurityPolicyBasic256Sha256 {
		if len(opnResp.ServerNonce) < 32 {
			_ = b.resetConnLocked()
			return errors.New("OPC-UA server nonce too short for Basic256Sha256")
		}
		b.clientKeys, b.serverKeys = DeriveOPCUABasic256Sha256Keys(clientNonce, opnResp.ServerNonce)
		b.hasKeys = true
	}

	// 3. CreateSession
	csRespBody, err := b.sendAndReceiveMSGLocked(ctx, EncodeOPCUACreateSessionRequest(0, "hvac-edge-"+strings.TrimSpace(b.tcpAddr)))
	if err != nil {
		_ = b.resetConnLocked()
		return err
	}
	status, sessionID, _, err := DecodeOPCUACreateSessionResponse(csRespBody)
	if err != nil || status != OPCUAStatusGood {
		_ = b.resetConnLocked()
		return fmt.Errorf("OPC-UA CreateSession failed (status=0x%08x): %w", status, err)
	}
	b.sessionToken = sessionID

	// 4. ActivateSession
	var password string
	if b.cfg.AuthMode == OPCUAAuthUsername && b.cfg.CredentialLookup != nil {
		password, err = b.cfg.CredentialLookup(b.cfg.CredentialRef)
		if err != nil {
			_ = b.resetConnLocked()
			return fmt.Errorf("resolve OPC-UA credentialRef %q: %w", b.cfg.CredentialRef, err)
		}
	}
	asRespBody, err := b.sendAndReceiveMSGLocked(ctx, EncodeOPCUAActivateSessionRequest(b.sessionToken, b.cfg.AuthMode, b.cfg.Username, password))
	if err != nil {
		_ = b.resetConnLocked()
		return err
	}
	asStatus, err := DecodeOPCUAActivateSessionResponse(asRespBody)
	if err != nil || asStatus != OPCUAStatusGood {
		_ = b.resetConnLocked()
		return fmt.Errorf("OPC-UA ActivateSession failed (status=0x%08x): %w", asStatus, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return nil
}

func (b *OPCUABridge) setupSubscriptionLocked(ctx context.Context) error {
	if len(b.subNodes) == 0 {
		return nil
	}
	interval := uint32(b.subIntervalMS)
	if interval == 0 {
		interval = defaultOPCUASubscriptionIntervalMS
	}
	subResp, err := b.sendAndReceiveMSGLocked(ctx, EncodeOPCUACreateSubscriptionRequest(interval))
	if err != nil {
		return err
	}
	status, subID, _, err := DecodeOPCUACreateSubscriptionResponse(subResp)
	if err != nil || status != OPCUAStatusGood {
		return fmt.Errorf("OPC-UA CreateSubscription failed (status=0x%08x): %w", status, err)
	}
	items := make([]OPCUAMonitoredItemSpec, len(b.subNodes))
	for i, nid := range b.subNodes {
		items[i] = OPCUAMonitoredItemSpec{ClientHandle: uint32(i + 1), NodeID: nid}
	}
	monResp, err := b.sendAndReceiveMSGLocked(ctx, EncodeOPCUACreateMonitoredItemsRequest(subID, items))
	if err != nil {
		return err
	}
	monStatus, _, err := DecodeOPCUACreateMonitoredItemsResponse(monResp)
	if err != nil || monStatus != OPCUAStatusGood {
		return fmt.Errorf("OPC-UA CreateMonitoredItems failed (status=0x%08x): %w", monStatus, err)
	}
	b.subID = subID
	b.subEstablished = true
	return nil
}

func (b *OPCUABridge) sendAndReceiveMSGLocked(ctx context.Context, body []byte) ([]byte, error) {
	if b.conn == nil {
		return nil, errors.New("OPC-UA TCP connection not open")
	}
	seq := b.seqNum.Add(1)
	req := b.reqID.Add(1)
	var keysPtr *OPCUACryptoKeys
	if b.hasKeys {
		keysPtr = &b.clientKeys
	}
	wire, err := EncodeOPCUASecureMessage(OPCUAMsgMessage, b.channelID, b.tokenID, seq, req, body, b.cfg.SecurityMode, keysPtr)
	if err != nil {
		return nil, err
	}
	_ = b.conn.SetDeadline(b.deadlineForCtx(ctx))
	defer func() {
		if b.conn != nil {
			_ = b.conn.SetDeadline(time.Time{})
		}
	}()
	if _, err := b.conn.Write(wire); err != nil {
		return nil, err
	}
	mType, respFrame, err := ReadOPCUAFrame(b.conn)
	if err != nil {
		return nil, err
	}
	if mType == OPCUAMsgError {
		code, reason, _ := DecodeOPCUAErrorFrame(respFrame)
		return nil, fmt.Errorf("OPC-UA service error (0x%08x): %s", code, reason)
	}
	if mType != OPCUAMsgMessage {
		return nil, fmt.Errorf("unexpected OPC-UA response chunk %q", mType)
	}
	var srvKeysPtr *OPCUACryptoKeys
	if b.hasKeys {
		srvKeysPtr = &b.serverKeys
	}
	_, _, _, respReqID, respBody, err := DecodeOPCUASecureMessage(respFrame, b.cfg.SecurityMode, srvKeysPtr)
	if err != nil {
		return nil, err
	}
	if respReqID != req {
		return nil, fmt.Errorf("OPC-UA requestId mismatch: got %d want %d", respReqID, req)
	}
	return respBody, nil
}
