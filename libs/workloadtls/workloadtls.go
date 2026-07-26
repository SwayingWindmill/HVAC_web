package workloadtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultConnectionLifetime = 10 * time.Minute

// CertificateFiles loads the current certificate and private key from projected
// workload files. The Get methods deliberately reload on each new TLS handshake
// so cert-manager CSI rotations do not require a process restart.
type CertificateFiles struct {
	CertificatePath string
	PrivateKeyPath  string
}

func (files CertificateFiles) Validate() error {
	_, err := files.load()
	return err
}

func (files CertificateFiles) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certificate, err := files.load()
	if err != nil {
		return nil, err
	}
	return &certificate, nil
}

func (files CertificateFiles) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	certificate, err := files.load()
	if err != nil {
		return nil, err
	}
	return &certificate, nil
}

func (files CertificateFiles) load() (tls.Certificate, error) {
	certificatePath := strings.TrimSpace(files.CertificatePath)
	privateKeyPath := strings.TrimSpace(files.PrivateKeyPath)
	if certificatePath == "" || privateKeyPath == "" {
		return tls.Certificate{}, errors.New("workload certificate and private key paths are required")
	}
	certificate, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	return certificate, nil
}

func LoadCertPool(path string) (*x509.CertPool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("certificate authority path is required")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(body) {
		return nil, errors.New("certificate authority bundle is invalid")
	}
	return pool, nil
}

type ServerConfig struct {
	CertificateFiles CertificateFiles
	ClientCAPath     string
}

func NewServerTLSConfig(config ServerConfig) (*tls.Config, error) {
	if err := config.CertificateFiles.Validate(); err != nil {
		return nil, err
	}
	clientCAs, err := LoadCertPool(config.ClientCAPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: config.CertificateFiles.GetCertificate,
		ClientCAs:      clientCAs,
		ClientAuth:     tls.RequireAndVerifyClientCert,
	}, nil
}

type ClientConfig struct {
	CertificateFiles   *CertificateFiles
	ServerCAPath       string
	ServerName         string
	Timeout            time.Duration
	ConnectionLifetime time.Duration
}

func NewHTTPClient(config ClientConfig) (*http.Client, error) {
	serverName := strings.TrimSpace(config.ServerName)
	if serverName == "" {
		return nil, errors.New("TLS server name is required")
	}
	if config.CertificateFiles != nil {
		if err := config.CertificateFiles.Validate(); err != nil {
			return nil, err
		}
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	connectionLifetime := config.ConnectionLifetime
	if connectionLifetime <= 0 {
		connectionLifetime = defaultConnectionLifetime
	}
	factory := func() (idleClosingRoundTripper, error) {
		roots, err := LoadCertPool(config.ServerCAPath)
		if err != nil {
			return nil, err
		}
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    roots,
			ServerName: serverName,
		}
		if config.CertificateFiles != nil {
			tlsConfig.GetClientCertificate = config.CertificateFiles.GetClientCertificate
		}
		return &http.Transport{
			TLSClientConfig:       tlsConfig,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
		}, nil
	}
	initialTransport, err := factory()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &http.Client{
		Transport: &rotatingTransport{
			current:   initialTransport,
			factory:   factory,
			now:       time.Now,
			expiresAt: now.Add(connectionLifetime),
			lifetime:  connectionLifetime,
		},
		Timeout: timeout,
	}, nil
}

type idleClosingRoundTripper interface {
	http.RoundTripper
	CloseIdleConnections()
}

type rotatingTransport struct {
	mu        sync.Mutex
	current   idleClosingRoundTripper
	factory   func() (idleClosingRoundTripper, error)
	now       func() time.Time
	expiresAt time.Time
	lifetime  time.Duration
}

func (transport *rotatingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	current, replaced, err := transport.transportForRequest()
	if err != nil {
		return nil, err
	}
	if replaced != nil {
		replaced.CloseIdleConnections()
	}
	return current.RoundTrip(request)
}

func (transport *rotatingTransport) CloseIdleConnections() {
	if transport == nil {
		return
	}
	transport.mu.Lock()
	current := transport.current
	transport.mu.Unlock()
	if current != nil {
		current.CloseIdleConnections()
	}
}

func (transport *rotatingTransport) transportForRequest() (idleClosingRoundTripper, idleClosingRoundTripper, error) {
	if transport == nil || transport.factory == nil || transport.now == nil || transport.lifetime <= 0 {
		return nil, nil, errors.New("rotating TLS transport is invalid")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	now := transport.now()
	if transport.current != nil && now.Before(transport.expiresAt) {
		return transport.current, nil, nil
	}
	next, err := transport.factory()
	if err != nil {
		return nil, nil, err
	}
	replaced := transport.current
	transport.current = next
	transport.expiresAt = now.Add(transport.lifetime)
	return next, replaced, nil
}
