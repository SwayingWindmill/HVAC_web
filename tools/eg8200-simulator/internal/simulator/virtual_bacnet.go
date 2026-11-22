package simulator

import (
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type bacnetCOVSub struct {
	addr   *net.UDPAddr
	procID uint32
}

// VirtualBACnetServer simulates an ASHRAE 135 BACnet/IP device over a real UDP socket,
// supporting Who-Is/I-Am, ReadProperty, WriteProperty, SubscribeCOV + async COV push,
// and fault injection for protocol-level verification (SIM-004).
type VirtualBACnetServer struct {
	conn           *net.UDPConn
	deviceInstance uint32
	vendorID       uint32

	mu           sync.Mutex
	closed       bool
	dropRequests bool
	objects      map[edgecontrol.BACnetObjectID]edgecontrol.BACnetAppValue
	covSubs      map[edgecontrol.BACnetObjectID][]bacnetCOVSub
	wg           sync.WaitGroup
}

// NewVirtualBACnetServer binds an ephemeral loopback UDP port (or listenAddr) and starts serving.
func NewVirtualBACnetServer(listenAddr string, deviceInstance uint32) (*VirtualBACnetServer, error) {
	if strings.TrimSpace(listenAddr) == "" {
		listenAddr = "127.0.0.1:0"
	}
	laddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return nil, err
	}
	s := &VirtualBACnetServer{
		conn:           conn,
		deviceInstance: deviceInstance,
		vendorID:       999,
		objects:        make(map[edgecontrol.BACnetObjectID]edgecontrol.BACnetAppValue),
		covSubs:        make(map[edgecontrol.BACnetObjectID][]bacnetCOVSub),
	}
	s.wg.Add(1)
	go s.serveLoop()
	return s, nil
}

// Endpoint returns host:port bound by the virtual BACnet/IP device.
func (s *VirtualBACnetServer) Endpoint() string {
	return s.conn.LocalAddr().String()
}

// SetDropRequests enables or disables simulated UDP packet loss.
func (s *VirtualBACnetServer) SetDropRequests(drop bool) {
	s.mu.Lock()
	s.dropRequests = drop
	s.mu.Unlock()
}

// SetObjectValue sets an object's Present_Value and emits UnconfirmedCOVNotification to active subscribers.
func (s *VirtualBACnetServer) SetObjectValue(objID edgecontrol.BACnetObjectID, val edgecontrol.BACnetAppValue) {
	s.mu.Lock()
	s.objects[objID] = val
	subs := append([]bacnetCOVSub(nil), s.covSubs[objID]...)
	devInst := s.deviceInstance
	closed := s.closed
	s.mu.Unlock()

	if closed || len(subs) == 0 {
		return
	}
	for _, sub := range subs {
		payload, err := edgecontrol.EncodeBACnetUnconfirmedCOVNotification(edgecontrol.BACnetCOVNotification{
			SubscriberProcessID: sub.procID,
			InitiatingDeviceID:  edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjDevice, Instance: devInst},
			MonitoredObjectID:   objID,
			TimeRemainingSec:    120,
			Properties: []edgecontrol.BACnetPropertyValue{
				{PropertyID: edgecontrol.BACnetPropPresentValue, Value: val},
			},
		})
		if err != nil {
			continue
		}
		frame, err := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
			PDUType: edgecontrol.BACnetAPDUTypeUnconfirmedRequest,
			Service: edgecontrol.BACnetServiceUnconfirmedCOVNotification,
			Payload: payload,
		})
		if err == nil {
			_, _ = s.conn.WriteToUDP(frame, sub.addr)
		}
	}
}

// GetObjectValue retrieves the simulated object value safely.
func (s *VirtualBACnetServer) GetObjectValue(objID edgecontrol.BACnetObjectID) (edgecontrol.BACnetAppValue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.objects[objID]
	return v, ok
}

// Close shuts down the UDP socket and waits cleanly for the serve goroutine.
func (s *VirtualBACnetServer) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.conn.Close()
	s.wg.Wait()
	return err
}

func (s *VirtualBACnetServer) serveLoop() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, raddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		pkt, err := edgecontrol.DecodeBACnetPacket(buf[:n])
		if err != nil {
			continue
		}
		s.mu.Lock()
		drop := s.dropRequests
		s.mu.Unlock()
		if drop {
			continue
		}
		s.handlePacket(raddr, pkt)
	}
}

func (s *VirtualBACnetServer) handlePacket(raddr *net.UDPAddr, pkt edgecontrol.BACnetPacket) {
	switch pkt.PDUType {
	case edgecontrol.BACnetAPDUTypeUnconfirmedRequest:
		if pkt.Service == edgecontrol.BACnetServiceWhoIs {
			low, high, _ := edgecontrol.DecodeBACnetWhoIsPayload(pkt.Payload)
			if low != nil && s.deviceInstance < *low {
				return
			}
			if high != nil && s.deviceInstance > *high {
				return
			}
			iam, _ := edgecontrol.EncodeBACnetIAmPayload(edgecontrol.BACnetIAMAnnouncement{
				DeviceID:     edgecontrol.BACnetObjectID{Type: edgecontrol.BACnetObjDevice, Instance: s.deviceInstance},
				MaxAPDU:      1476,
				Segmentation: 3,
				VendorID:     s.vendorID,
			})
			resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
				PDUType: edgecontrol.BACnetAPDUTypeUnconfirmedRequest,
				Service: edgecontrol.BACnetServiceIAm,
				Payload: iam,
			})
			_, _ = s.conn.WriteToUDP(resp, raddr)
		}
	case edgecontrol.BACnetAPDUTypeConfirmedRequest:
		switch pkt.Service {
		case edgecontrol.BACnetServiceReadProperty:
			objID, propID, err := edgecontrol.DecodeBACnetReadPropertyRequest(pkt.Payload)
			if err != nil {
				s.sendError(raddr, pkt, 5, 0)
				return
			}
			s.mu.Lock()
			val, ok := s.objects[objID]
			s.mu.Unlock()
			if !ok {
				s.sendError(raddr, pkt, 1, 31)
				return
			}
			ack, _ := edgecontrol.EncodeBACnetReadPropertyACK(objID, propID, val)
			resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
				PDUType:  edgecontrol.BACnetAPDUTypeComplexACK,
				InvokeID: pkt.InvokeID,
				Service:  pkt.Service,
				Payload:  ack,
			})
			_, _ = s.conn.WriteToUDP(resp, raddr)
		case edgecontrol.BACnetServiceWriteProperty:
			objID, _, val, _, err := edgecontrol.DecodeBACnetWritePropertyRequest(pkt.Payload)
			if err != nil {
				s.sendError(raddr, pkt, 5, 0)
				return
			}
			if objID.Type.IsInputOnly() {
				s.sendError(raddr, pkt, 2, 40) // Write access denied
				return
			}
			s.SetObjectValue(objID, val)
			resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
				PDUType:  edgecontrol.BACnetAPDUTypeSimpleACK,
				InvokeID: pkt.InvokeID,
				Service:  pkt.Service,
			})
			_, _ = s.conn.WriteToUDP(resp, raddr)
		case edgecontrol.BACnetServiceSubscribeCOV:
			procID, objID, _, _, err := edgecontrol.DecodeBACnetSubscribeCOVRequest(pkt.Payload)
			if err != nil {
				s.sendError(raddr, pkt, 5, 0)
				return
			}
			s.mu.Lock()
			existing := s.covSubs[objID]
			found := false
			for _, sub := range existing {
				if sub.addr.String() == raddr.String() && sub.procID == procID {
					found = true
					break
				}
			}
			if !found {
				s.covSubs[objID] = append(existing, bacnetCOVSub{addr: raddr, procID: procID})
			}
			s.mu.Unlock()
			resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
				PDUType:  edgecontrol.BACnetAPDUTypeSimpleACK,
				InvokeID: pkt.InvokeID,
				Service:  pkt.Service,
			})
			_, _ = s.conn.WriteToUDP(resp, raddr)
		}
	}
}

func (s *VirtualBACnetServer) sendError(raddr *net.UDPAddr, req edgecontrol.BACnetPacket, errClass, errCode uint32) {
	resp, _ := edgecontrol.EncodeBACnetPacket(edgecontrol.BACnetPacket{
		PDUType:  edgecontrol.BACnetAPDUTypeError,
		InvokeID: req.InvokeID,
		Service:  req.Service,
		Payload:  edgecontrol.EncodeBACnetErrorPayload(errClass, errCode),
	})
	_, _ = s.conn.WriteToUDP(resp, raddr)
}
