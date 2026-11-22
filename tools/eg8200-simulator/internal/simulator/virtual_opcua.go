package simulator

import (
	"crypto/rand"
	"net"
	"strings"
	"sync"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

type virtualOPCUANode struct {
	dv       edgecontrol.OPCUADataValue
	writable bool
}

// VirtualOPCUAServer simulates an IEC 62541 binary TCP OPC-UA endpoint supporting
// None and Basic256Sha256 Sign / SignAndEncrypt, Username/Password check, subscriptions,
// and disconnect/reject fault injection (SIM-004).
type VirtualOPCUAServer struct {
	ln               net.Listener
	endpointURL      string
	requiredUser     string
	requiredPassword string
	serverNonce      []byte

	mu            sync.Mutex
	closed        bool
	rejectConnect bool
	nodes         map[string]virtualOPCUANode
	conns         map[net.Conn]struct{}
	wg            sync.WaitGroup
}

// NewVirtualOPCUAServer listens on loopback TCP (or listenAddr) and serves opc.tcp:// clients.
func NewVirtualOPCUAServer(listenAddr, username, password string) (*VirtualOPCUAServer, error) {
	if strings.TrimSpace(listenAddr) == "" {
		listenAddr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	s := &VirtualOPCUAServer{
		ln:               ln,
		endpointURL:      "opc.tcp://" + ln.Addr().String(),
		requiredUser:     strings.TrimSpace(username),
		requiredPassword: password,
		serverNonce:      nonce,
		nodes:            make(map[string]virtualOPCUANode),
		conns:            make(map[net.Conn]struct{}),
	}
	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Endpoint returns the opc.tcp:// URL clients should dial.
func (s *VirtualOPCUAServer) Endpoint() string {
	return s.endpointURL
}

// SetNodeValue defines or updates a node's DataValue and writability.
func (s *VirtualOPCUAServer) SetNodeValue(nodeID string, dv edgecontrol.OPCUADataValue, writable bool) {
	s.mu.Lock()
	s.nodes[nodeID] = virtualOPCUANode{dv: dv, writable: writable}
	s.mu.Unlock()
}

// GetNodeValue retrieves a node's stored DataValue.
func (s *VirtualOPCUAServer) GetNodeValue(nodeID string) (edgecontrol.OPCUADataValue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[nodeID]
	return n.dv, ok
}

// SetRejectConnect forces new TCP sessions to be rejected cleanly with ERR frames.
func (s *VirtualOPCUAServer) SetRejectConnect(reject bool) {
	s.mu.Lock()
	s.rejectConnect = reject
	s.mu.Unlock()
}

// DropAllClients forcibly closes active TCP client connections to test reconnect & re-subscription.
func (s *VirtualOPCUAServer) DropAllClients() {
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
		delete(s.conns, c)
	}
	s.mu.Unlock()
}

// Close shuts down the listener, drops all active clients, and waits for goroutines.
func (s *VirtualOPCUAServer) Close() error {
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
	err := s.ln.Close()
	s.DropAllClients()
	s.wg.Wait()
	return err
}

func (s *VirtualOPCUAServer) acceptLoop() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		closed := s.closed
		if !closed {
			s.conns[c] = struct{}{}
			s.wg.Add(1)
		}
		s.mu.Unlock()
		if closed {
			_ = c.Close()
			return
		}
		go s.serveConn(c)
	}
}

func (s *VirtualOPCUAServer) serveConn(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
		s.wg.Done()
	}()

	secMode := edgecontrol.OPCUASecurityModeNone
	var cKeys, sKeys edgecontrol.OPCUACryptoKeys
	hasKeys := false
	subItems := map[uint32]edgecontrol.OPCUANodeID{}
	lastPublished := map[string]edgecontrol.OPCUADataValue{}

	for {
		mType, frame, err := edgecontrol.ReadOPCUAFrame(conn)
		if err != nil {
			return
		}
		switch mType {
		case edgecontrol.OPCUAMsgHello:
			s.mu.Lock()
			reject := s.rejectConnect
			s.mu.Unlock()
			if reject {
				_, _ = conn.Write(edgecontrol.EncodeOPCUAErrorFrame(edgecontrol.OPCUAStatusBadSec, "simulated disconnect/reject"))
				return
			}
			_, _ = conn.Write(edgecontrol.EncodeOPCUAAcknowledge())
		case edgecontrol.OPCUAMsgOpenChannel:
			opn, oErr := edgecontrol.DecodeOPCUAOpenRequest(frame)
			if oErr != nil {
				return
			}
			switch opn.SecurityMode {
			case 3:
				secMode = edgecontrol.OPCUASecurityModeSignAndEncrypt
			case 2:
				secMode = edgecontrol.OPCUASecurityModeSign
			default:
				secMode = edgecontrol.OPCUASecurityModeNone
			}
			if opn.SecurityPolicy == edgecontrol.OPCUASecurityPolicyBasic256Sha256 {
				cKeys, sKeys = edgecontrol.DeriveOPCUABasic256Sha256Keys(opn.ClientNonce, s.serverNonce)
				hasKeys = true
			} else {
				hasKeys = false
			}
			_, _ = conn.Write(edgecontrol.EncodeOPCUAOpenResponse(edgecontrol.OPCUAOpenChannelFrame{
				SecurityPolicy:  opn.SecurityPolicy,
				SeqNum:          opn.SeqNum,
				RequestID:       opn.RequestID,
				TokenChannelID:  101,
				TokenID:         1,
				RevisedLifetime: 3600000,
				ServerNonce:     s.serverNonce,
			}))
		case edgecontrol.OPCUAMsgMessage:
			var ckPtr, skPtr *edgecontrol.OPCUACryptoKeys
			if hasKeys {
				ckPtr, skPtr = &cKeys, &sKeys
			}
			chID, tokID, seq, reqID, body, dErr := edgecontrol.DecodeOPCUASecureMessage(frame, secMode, ckPtr)
			if dErr != nil {
				return
			}
			svcID, _ := edgecontrol.PeekOPCUAServiceID(body)
			var respBody []byte
			switch svcID {
			case edgecontrol.OPCUAServiceCreateSessionReq:
				respBody = edgecontrol.EncodeOPCUACreateSessionResponse(edgecontrol.OPCUAStatusGood, 202, s.serverNonce)
			case edgecontrol.OPCUAServiceActivateSessionReq:
				_, authMode, user, pass, _ := edgecontrol.DecodeOPCUAActivateSessionRequest(body)
				status := edgecontrol.OPCUAStatusGood
				if s.requiredUser != "" && (authMode != edgecontrol.OPCUAAuthUsername || user != s.requiredUser || pass != s.requiredPassword) {
					status = edgecontrol.OPCUAStatusBadSec
				}
				respBody = edgecontrol.EncodeOPCUAActivateSessionResponse(status)
			case edgecontrol.OPCUAServiceReadReq:
				nids, _ := edgecontrol.DecodeOPCUAReadRequest(body)
				dvs := make([]edgecontrol.OPCUADataValue, len(nids))
				s.mu.Lock()
				for i, nid := range nids {
					if n, ok := s.nodes[nid.String()]; ok {
						dvs[i] = n.dv
					} else {
						dvs[i] = edgecontrol.OPCUADataValue{StatusCode: edgecontrol.OPCUAStatusBadNodeID}
					}
				}
				s.mu.Unlock()
				respBody = edgecontrol.EncodeOPCUAReadResponse(edgecontrol.OPCUAStatusGood, dvs)
			case edgecontrol.OPCUAServiceWriteReq:
				wrs, _ := edgecontrol.DecodeOPCUAWriteRequest(body)
				rcs := make([]uint32, len(wrs))
				s.mu.Lock()
				for i, w := range wrs {
					k := w.NodeID.String()
					node, ok := s.nodes[k]
					if !ok {
						rcs[i] = edgecontrol.OPCUAStatusBadNodeID
						continue
					}
					if !node.writable {
						rcs[i] = edgecontrol.OPCUAStatusBadAccess
						continue
					}
					node.dv = edgecontrol.OPCUADataValue{HasValue: true, Value: w.Value, StatusCode: edgecontrol.OPCUAStatusGood}
					s.nodes[k] = node
					rcs[i] = edgecontrol.OPCUAStatusGood
				}
				s.mu.Unlock()
				respBody = edgecontrol.EncodeOPCUAWriteResponse(edgecontrol.OPCUAStatusGood, rcs)
			case edgecontrol.OPCUAServiceCreateSubscriptionReq:
				respBody = edgecontrol.EncodeOPCUACreateSubscriptionResponse(edgecontrol.OPCUAStatusGood, 701, 200)
			case edgecontrol.OPCUAServiceCreateMonitoredItemsReq:
				_, items, _ := edgecontrol.DecodeOPCUACreateMonitoredItemsRequest(body)
				rcs := make([]uint32, len(items))
				for i, it := range items {
					subItems[it.ClientHandle] = it.NodeID
					rcs[i] = edgecontrol.OPCUAStatusGood
				}
				respBody = edgecontrol.EncodeOPCUACreateMonitoredItemsResponse(edgecontrol.OPCUAStatusGood, rcs)
			case edgecontrol.OPCUAServicePublishReq:
				var notifs []edgecontrol.OPCUAMonitoredNotification
				s.mu.Lock()
				for handle, nid := range subItems {
					k := nid.String()
					if node, ok := s.nodes[k]; ok {
						prev, seen := lastPublished[k]
						if !seen || prev.Value != node.dv.Value {
							notifs = append(notifs, edgecontrol.OPCUAMonitoredNotification{ClientHandle: handle, Value: node.dv})
							lastPublished[k] = node.dv
						}
					}
				}
				s.mu.Unlock()
				respBody = edgecontrol.EncodeOPCUAPublishResponse(edgecontrol.OPCUAStatusGood, 701, notifs)
			}
			outWire, _ := edgecontrol.EncodeOPCUASecureMessage(edgecontrol.OPCUAMsgMessage, chID, tokID, seq, reqID, respBody, secMode, skPtr)
			_, _ = conn.Write(outWire)
		}
	}
}
