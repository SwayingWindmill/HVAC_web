package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type healthEnvelope struct {
	Code int        `json:"code"`
	Data healthData `json:"data"`
}

type healthData struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Version   string `json:"version"`
}

func main() {
	addr := envOr("LEGACY_FIXTURE_ADDR", "127.0.0.1:18081")
	certPath := required("LEGACY_TLS_CERT")
	keyPath := required("LEGACY_TLS_KEY")
	clientCAPath := required("LEGACY_CLIENT_CA")
	allowedSPIFFE := envOr("LEGACY_ALLOWED_WORKLOAD_SPIFFE", "spiffe://hvac.local/platform-gateway")
	audience := envOr("LEGACY_AUDIENCE", "legacy-hvac-backend")

	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatal(err)
	}
	caPEM, err := os.ReadFile(clientCAPath)
	if err != nil {
		log.Fatal(err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		log.Fatal("Legacy fixture client CA is invalid")
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           legacyHandler(allowedSPIFFE, audience, time.Now),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    clientCAs,
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	log.Printf("Legacy compatibility fixture listening on %s", addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func legacyHandler(allowedSPIFFE, audience string, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/health" {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for _, header := range []string{"X-Principal", "X-Roles", "X-Organization-ID", "X-Site-ID", "X-Admin"} {
			if request.Header.Get(header) != "" {
				http.Error(writer, "forged identity header", http.StatusBadRequest)
				return
			}
		}
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
			http.Error(writer, "workload identity required", http.StatusUnauthorized)
			return
		}
		peerCertificate := request.TLS.PeerCertificates[0]
		if len(peerCertificate.URIs) != 1 || peerCertificate.URIs[0] == nil || peerCertificate.URIs[0].String() != allowedSPIFFE {
			http.Error(writer, "workload identity rejected", http.StatusUnauthorized)
			return
		}
		claims, err := identitycontext.VerifyDelegation(peerCertificate.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			http.Error(writer, "delegation invalid", http.StatusUnauthorized)
			return
		}
		scope := "organization:" + claims.ActingOrganizationID
		if err := identitycontext.ValidateDelegation(claims, now(), allowedSPIFFE, audience, "legacy:platform-status:read", scope); err != nil {
			http.Error(writer, "delegation rejected", http.StatusForbidden)
			return
		}
		revision, err := strconv.ParseInt(request.Header.Get("X-Route-Policy-Revision"), 10, 64)
		if err != nil || revision <= 0 || strings.TrimSpace(request.Header.Get("X-Request-ID")) == "" {
			http.Error(writer, "routing context invalid", http.StatusBadRequest)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(healthEnvelope{
			Code: http.StatusOK,
			Data: healthData{
				Status:    "UP",
				Timestamp: now().UTC().Format(time.RFC3339Nano),
				Version:   "legacy-fixture-v1",
			},
		})
	})
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func required(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
