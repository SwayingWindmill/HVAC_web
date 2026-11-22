package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBACnetBridgeTimeout = time.Second
	defaultBACnetProcessID     = uint32(1)
)

// BACnetTransport defines the southbound BACnet/IP protocol bridge operations
// decoupled from individual device adapters.
type BACnetTransport interface {
	DiscoverDevices(ctx context.Context, broadcastAddr string, lowLimit, highLimit *uint32) ([]BACnetIAMAnnouncement, error)
	ReadProperty(ctx context.Context, endpoint string, netNum uint16, objID BACnetObjectID, propID uint32) (BACnetAppValue, error)
	WriteProperty(ctx context.Context, endpoint string, netNum uint16, objID BACnetObjectID, propID uint32, val BACnetAppValue, priority *uint8) error
	SubscribeCOV(ctx context.Context, endpoint string, netNum uint16, procID uint32, objID BACnetObjectID, lifetimeSec uint32, onNotify func(BACnetCOVNotification)) error
	Close() error
}

// BACnetIPBridgeConfig configures a UDP Annex J BACnet/IP protocol bridge.
type BACnetIPBridgeConfig struct {
	LocalAddr string
	Timeout   time.Duration
	Retries   int
}

type bacnetPendingKey struct {
	remote   string
	invokeID uint8
}

type bacnetPendingReply struct {
	pkt BACnetPacket
	err error
}

type bacnetCOVKey struct {
	remote   string
	objType  BACnetObjectType
	instance uint32
}

// BACnetIPBridge implements a reusable UDP Annex J BACnet/IP bridge supporting
// Who-Is/I-Am discovery, confirmed ReadProperty/WriteProperty/SubscribeCOV, and COV push dispatch.
type BACnetIPBridge struct {
	conn    *net.UDPConn
	timeout time.Duration
	retries int

	nextInvoke atomic.Uint32

	mu          sync.Mutex
	closed      bool
	pending     map[bacnetPendingKey]chan bacnetPendingReply
	discovers   map[uint64]chan BACnetIAMAnnouncement
	discoverSeq uint64
	covHandlers map[bacnetCOVKey]func(BACnetCOVNotification)
	wg          sync.WaitGroup
}

// NewBACnetIPBridge opens a UDP socket for BACnet/IP traffic and starts its receive loop.
func NewBACnetIPBridge(cfg BACnetIPBridgeConfig) (*BACnetIPBridge, error) {
	localStr := strings.TrimSpace(cfg.LocalAddr)
	if localStr == "" {
		localStr = ":0"
	}
	laddr, err := net.ResolveUDPAddr("udp", localStr)
	if err != nil {
		return nil, fmt.Errorf("resolve BACnet local addr %q: %w", localStr, err)
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return nil, fmt.Errorf("listen BACnet/IP UDP %q: %w", localStr, err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultBACnetBridgeTimeout
	}
	retries := cfg.Retries
	if retries < 0 {
		retries = 0
	}

	b := &BACnetIPBridge{
		conn:        conn,
		timeout:     timeout,
		retries:     retries,
		pending:     make(map[bacnetPendingKey]chan bacnetPendingReply),
		discovers:   make(map[uint64]chan BACnetIAMAnnouncement),
		covHandlers: make(map[bacnetCOVKey]func(BACnetCOVNotification)),
	}
	b.wg.Add(1)
	go b.readLoop()
	return b, nil
}

// LocalAddr returns the bound local UDP address string.
func (b *BACnetIPBridge) LocalAddr() string {
	if b == nil || b.conn == nil {
		return ""
	}
	return b.conn.LocalAddr().String()
}

// DiscoverDevices sends an unconfirmed Who-Is and collects I-Am replies until the context/timeout expires.
func (b *BACnetIPBridge) DiscoverDevices(ctx context.Context, broadcastAddr string, lowLimit, highLimit *uint32) ([]BACnetIAMAnnouncement, error) {
	if b == nil {
		return nil, errors.New("BACnet/IP bridge is nil")
	}
	raddr, err := net.ResolveUDPAddr("udp", strings.TrimSpace(broadcastAddr))
	if err != nil {
		return nil, fmt.Errorf("resolve BACnet broadcast addr %q: %w", broadcastAddr, err)
	}
	frame, err := EncodeBACnetPacket(BACnetPacket{
		BVLCFunction: BVLCFuncOriginalBroadcastNPDU,
		PDUType:      BACnetAPDUTypeUnconfirmedRequest,
		Service:      BACnetServiceWhoIs,
		Payload:      EncodeBACnetWhoIsPayload(lowLimit, highLimit),
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan BACnetIAMAnnouncement, 32)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("BACnet/IP bridge is closed")
	}
	b.discoverSeq++
	id := b.discoverSeq
	b.discovers[id] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.discovers, id)
		b.mu.Unlock()
	}()

	if _, err := b.conn.WriteToUDP(frame, raddr); err != nil {
		return nil, err
	}

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, b.timeout)
		defer cancel()
	}

	var out []BACnetIAMAnnouncement
	seen := make(map[BACnetObjectID]struct{})
	for {
		select {
		case ann := <-ch:
			if _, dup := seen[ann.DeviceID]; !dup {
				seen[ann.DeviceID] = struct{}{}
				out = append(out, ann)
			}
		case <-waitCtx.Done():
			return out, nil
		}
	}
}

// ReadProperty reads one property value from a remote BACnet/IP endpoint.
func (b *BACnetIPBridge) ReadProperty(ctx context.Context, endpoint string, netNum uint16, objID BACnetObjectID, propID uint32) (BACnetAppValue, error) {
	payload, err := EncodeBACnetReadPropertyRequest(objID, propID)
	if err != nil {
		return BACnetAppValue{}, err
	}
	reply, err := b.doConfirmed(ctx, endpoint, netNum, BACnetServiceReadProperty, payload)
	if err != nil {
		return BACnetAppValue{}, err
	}
	_, _, val, err := DecodeBACnetReadPropertyACK(reply.Payload)
	return val, err
}

// WriteProperty writes a property value to a remote BACnet/IP endpoint at optional priority [1..16].
func (b *BACnetIPBridge) WriteProperty(ctx context.Context, endpoint string, netNum uint16, objID BACnetObjectID, propID uint32, val BACnetAppValue, priority *uint8) error {
	payload, err := EncodeBACnetWritePropertyRequest(objID, propID, val, priority)
	if err != nil {
		return err
	}
	_, err = b.doConfirmed(ctx, endpoint, netNum, BACnetServiceWriteProperty, payload)
	return err
}

// SubscribeCOV registers an unconfirmed COV subscription and callback on the bridge.
func (b *BACnetIPBridge) SubscribeCOV(ctx context.Context, endpoint string, netNum uint16, procID uint32, objID BACnetObjectID, lifetimeSec uint32, onNotify func(BACnetCOVNotification)) error {
	raddr, err := net.ResolveUDPAddr("udp", strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("resolve BACnet endpoint %q: %w", endpoint, err)
	}
	if onNotify != nil {
		b.mu.Lock()
		b.covHandlers[bacnetCOVKey{remote: raddr.String(), objType: objID.Type, instance: objID.Instance}] = onNotify
		b.mu.Unlock()
	}
	if procID == 0 {
		procID = defaultBACnetProcessID
	}
	payload, err := EncodeBACnetSubscribeCOVRequest(procID, objID, false, lifetimeSec)
	if err != nil {
		return err
	}
	_, err = b.doConfirmed(ctx, endpoint, netNum, BACnetServiceSubscribeCOV, payload)
	return err
}

func (b *BACnetIPBridge) doConfirmed(ctx context.Context, endpoint string, netNum uint16, service uint8, payload []byte) (BACnetPacket, error) {
	if b == nil {
		return BACnetPacket{}, errors.New("BACnet/IP bridge is nil")
	}
	raddr, err := net.ResolveUDPAddr("udp", strings.TrimSpace(endpoint))
	if err != nil {
		return BACnetPacket{}, fmt.Errorf("resolve BACnet endpoint %q: %w", endpoint, err)
	}

	var lastErr error
	attempts := 1 + b.retries
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return BACnetPacket{}, ctx.Err()
		}
		reply, err := b.doConfirmedOnce(ctx, raddr, netNum, service, payload)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		var bacErr *BACnetProtocolError
		if errors.As(err, &bacErr) {
			return BACnetPacket{}, err
		}
	}
	return BACnetPacket{}, lastErr
}

// BACnetProtocolError captures an explicit device-returned ASHRAE 135 Error APDU.
type BACnetProtocolError struct {
	Service    uint8
	ErrorClass uint32
	ErrorCode  uint32
}

func (e *BACnetProtocolError) Error() string {
	return fmt.Sprintf("BACnet error PDU (service=%d class=%d code=%d)", e.Service, e.ErrorClass, e.ErrorCode)
}

func (b *BACnetIPBridge) doConfirmedOnce(ctx context.Context, raddr *net.UDPAddr, netNum uint16, service uint8, payload []byte) (BACnetPacket, error) {
	invokeID := uint8(b.nextInvoke.Add(1) & 0xFF)
	key := bacnetPendingKey{remote: raddr.String(), invokeID: invokeID}
	ch := make(chan bacnetPendingReply, 1)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return BACnetPacket{}, errors.New("BACnet/IP bridge is closed")
	}
	b.pending[key] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
	}()

	frame, err := EncodeBACnetPacket(BACnetPacket{
		BVLCFunction: BVLCFuncOriginalUnicastNPDU,
		NetworkNum:   netNum,
		PDUType:      BACnetAPDUTypeConfirmedRequest,
		InvokeID:     invokeID,
		Service:      service,
		Payload:      payload,
	})
	if err != nil {
		return BACnetPacket{}, err
	}
	if _, err := b.conn.WriteToUDP(frame, raddr); err != nil {
		return BACnetPacket{}, err
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, b.timeout)
		defer cancel()
	}

	select {
	case rep := <-ch:
		if rep.err != nil {
			return BACnetPacket{}, rep.err
		}
		if rep.pkt.PDUType == BACnetAPDUTypeError {
			ec, ecode, decErr := DecodeBACnetErrorPayload(rep.pkt.Payload)
			if decErr != nil {
				return BACnetPacket{}, fmt.Errorf("BACnet error PDU (malformed payload): %w", decErr)
			}
			return BACnetPacket{}, &BACnetProtocolError{Service: rep.pkt.Service, ErrorClass: ec, ErrorCode: ecode}
		}
		return rep.pkt, nil
	case <-reqCtx.Done():
		return BACnetPacket{}, reqCtx.Err()
	}
}

func (b *BACnetIPBridge) readLoop() {
	defer b.wg.Done()
	buf := make([]byte, maxBACnetPacketBytes)
	for {
		n, remote, err := b.conn.ReadFromUDP(buf)
		if err != nil {
			b.mu.Lock()
			closed := b.closed
			b.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		pkt, err := DecodeBACnetPacket(buf[:n])
		if err != nil {
			continue
		}
		remoteKey := remote.String()
		switch pkt.PDUType {
		case BACnetAPDUTypeSimpleACK, BACnetAPDUTypeComplexACK, BACnetAPDUTypeError:
			b.mu.Lock()
			ch := b.pending[bacnetPendingKey{remote: remoteKey, invokeID: pkt.InvokeID}]
			b.mu.Unlock()
			if ch != nil {
				select {
				case ch <- bacnetPendingReply{pkt: pkt}:
				default:
				}
			}
		case BACnetAPDUTypeUnconfirmedRequest:
			switch pkt.Service {
			case BACnetServiceIAm:
				ann, decErr := DecodeBACnetIAmPayload(pkt.Payload)
				if decErr != nil {
					continue
				}
				b.mu.Lock()
				for _, ch := range b.discovers {
					select {
					case ch <- ann:
					default:
					}
				}
				b.mu.Unlock()
			case BACnetServiceUnconfirmedCOVNotification:
				msg, decErr := DecodeBACnetUnconfirmedCOVNotification(pkt.Payload)
				if decErr != nil {
					continue
				}
				k := bacnetCOVKey{remote: remoteKey, objType: msg.MonitoredObjectID.Type, instance: msg.MonitoredObjectID.Instance}
				b.mu.Lock()
				handler := b.covHandlers[k]
				b.mu.Unlock()
				if handler != nil {
					handler(msg)
				}
			}
		}
	}
}

// Close closes the underlying UDP socket and waits for the receive loop to terminate.
func (b *BACnetIPBridge) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for k, ch := range b.pending {
		delete(b.pending, k)
		select {
		case ch <- bacnetPendingReply{err: errors.New("BACnet/IP bridge closed")}:
		default:
		}
	}
	b.mu.Unlock()

	var closeErr error
	if b.conn != nil {
		closeErr = b.conn.Close()
	}
	b.wg.Wait()
	return closeErr
}
