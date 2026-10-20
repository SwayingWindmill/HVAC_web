package adapter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
)

// fakeBroker is a minimal MQTT 5 server over mutual TLS. The test decides what each
// connection receives and when it drops, the way Mosquitto restarting would.
type fakeBroker struct {
	listener net.Listener
	conns    chan *brokerConn
	mqtt     MQTTConfig
}

type brokerConn struct {
	conn       net.Conn
	writeMu    sync.Mutex
	subscribed chan struct{}
	acks       chan uint16
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	mqtt, serverTLS := writeTestPKI(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	mqtt.BrokerURL = "tls://" + listener.Addr().String()
	mqtt.ClientID = "connectivity-test"
	broker := &fakeBroker{listener: listener, conns: make(chan *brokerConn, 16), mqtt: mqtt}
	t.Cleanup(func() { _ = listener.Close() })
	go broker.accept()
	return broker
}

func (broker *fakeBroker) accept() {
	for {
		conn, err := broker.listener.Accept()
		if err != nil {
			return
		}
		go broker.serve(conn)
	}
}

func (broker *fakeBroker) serve(conn net.Conn) {
	connect, err := packets.ReadPacket(conn)
	if err != nil || connect.Type != packets.CONNECT {
		_ = conn.Close()
		return
	}
	client := &brokerConn{conn: conn, subscribed: make(chan struct{}), acks: make(chan uint16, 64)}
	connack := packets.NewControlPacket(packets.CONNACK)
	connack.Content.(*packets.Connack).SessionPresent = true
	if err := client.write(connack); err != nil {
		return
	}
	broker.conns <- client
	for {
		received, err := packets.ReadPacket(conn)
		if err != nil {
			return
		}
		switch received.Type {
		case packets.SUBSCRIBE:
			subscribe := received.Content.(*packets.Subscribe)
			suback := packets.NewControlPacket(packets.SUBACK)
			suback.Content.(*packets.Suback).PacketID = subscribe.PacketID
			for range subscribe.Subscriptions {
				suback.Content.(*packets.Suback).Reasons = append(suback.Content.(*packets.Suback).Reasons, 1)
			}
			_ = client.write(suback)
			close(client.subscribed)
		case packets.PINGREQ:
			_ = client.write(packets.NewControlPacket(packets.PINGRESP))
		case packets.PUBACK:
			client.acks <- received.Content.(*packets.Puback).PacketID
		}
	}
}

// awaitConnection returns the next connection Connectivity makes once it has subscribed.
func (broker *fakeBroker) awaitConnection(t *testing.T) *brokerConn {
	t.Helper()
	select {
	case client := <-broker.conns:
		select {
		case <-client.subscribed:
			return client
		case <-time.After(3 * time.Second):
			t.Fatal("Connectivity connected but never subscribed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Connectivity never reconnected to the broker")
	}
	return nil
}

func (client *brokerConn) write(packet *packets.ControlPacket) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_, err := packet.WriteTo(client.conn)
	return err
}

// deliver sends a QoS 1 telemetry message; redelivered marks a copy the broker sends
// again on a new connection because the first one was never acknowledged.
func (client *brokerConn) deliver(t *testing.T, packetID uint16, index int, redelivered bool) {
	t.Helper()
	packet := packets.NewControlPacket(packets.PUBLISH)
	publish := packet.Content.(*packets.Publish)
	publish.Topic = telemetryTopic(testGatewayA)
	publish.Payload = telemetryPayload(testGatewayA, messageID(index), index, "METER-01", "active_power")
	publish.QoS = 1
	publish.PacketID = packetID
	publish.Duplicate = redelivered
	if err := client.write(packet); err != nil {
		t.Fatal(err)
	}
}

// awaitAcks waits until the broker has received a PUBACK for every packet id.
func (client *brokerConn) awaitAcks(t *testing.T, packetIDs ...uint16) {
	t.Helper()
	pending := map[uint16]bool{}
	for _, id := range packetIDs {
		pending[id] = true
	}
	deadline := time.After(3 * time.Second)
	for len(pending) > 0 {
		select {
		case id := <-client.acks:
			delete(pending, id)
		case <-deadline:
			t.Fatalf("broker never received PUBACK for packets %v", pending)
		}
	}
}

func (client *brokerConn) drop() { _ = client.conn.Close() }

// writeTestPKI writes a CA, a broker certificate and a Connectivity client certificate,
// returning Connectivity's MQTT file settings and the broker's TLS config.
func writeTestPKI(t *testing.T) (MQTTConfig, *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	writePEM := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	client := issue(2, "connectivity", x509.ExtKeyUsageClientAuth)
	clientKey, _ := x509.MarshalECPrivateKey(client.PrivateKey.(*ecdsa.PrivateKey))
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return MQTTConfig{
			CAFile:     writePEM("ca.pem", "CERTIFICATE", caDER),
			CertFile:   writePEM("client.pem", "CERTIFICATE", client.Certificate[0]),
			KeyFile:    writePEM("client.key", "EC PRIVATE KEY", clientKey),
			ServerName: "mqtt.local",
		}, &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{issue(3, "mqtt.local", x509.ExtKeyUsageServerAuth)},
			ClientCAs:    roots,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		}
}
