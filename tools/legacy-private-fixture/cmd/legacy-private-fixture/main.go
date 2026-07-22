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

const (
	fixtureOrganizationID = "018f1e00-1000-7000-8000-000000000001"
	fixtureSiteID         = "018f1e00-1000-7000-8000-000000000002"
	fixtureEquipmentID    = "018f1e00-1000-7000-8000-000000000003"
	fixtureDeviceID       = "018f1e00-1000-7000-8000-000000000004"
)

type legacyRoute struct {
	action           string
	acceptableScopes []string
	payload          any
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
		route, found := resolveLegacyRoute(request.URL.Path, now())
		if !found {
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
		acceptableScopes := route.acceptableScopes
		if len(acceptableScopes) == 0 {
			acceptableScopes = []string{"organization:" + claims.ActingOrganizationID}
		}
		if err := identitycontext.ValidateDelegationAnyScope(claims, now(), allowedSPIFFE, audience, route.action, acceptableScopes); err != nil {
			http.Error(writer, "delegation rejected", http.StatusForbidden)
			return
		}
		revision, err := strconv.ParseInt(request.Header.Get("X-Route-Policy-Revision"), 10, 64)
		if err != nil || revision <= 0 || strings.TrimSpace(request.Header.Get("X-Request-ID")) == "" {
			http.Error(writer, "routing context invalid", http.StatusBadRequest)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(route.payload)
	})
}

func resolveLegacyRoute(path string, now time.Time) (legacyRoute, bool) {
	timestamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	organization := map[string]any{
		"id": fixtureOrganizationID, "code": "north-campus", "displayName": "North Campus",
		"status": "ACTIVE", "revision": 1, "createdAt": timestamp, "updatedAt": timestamp,
	}
	site := map[string]any{
		"id": fixtureSiteID, "owningOrganizationId": fixtureOrganizationID, "code": "main-site",
		"displayName": "Main Site", "timezone": "UTC", "status": "ACTIVE", "revision": 1,
		"createdAt": timestamp, "updatedAt": timestamp,
	}
	equipment := map[string]any{
		"id": fixtureEquipmentID, "owningOrganizationId": fixtureOrganizationID, "siteId": fixtureSiteID,
		"code": "ahu-01", "displayName": "AHU 01", "equipmentType": "AHU", "status": "ACTIVE",
		"revision": 1, "createdAt": timestamp, "updatedAt": timestamp,
	}
	device := map[string]any{
		"id": fixtureDeviceID, "owningOrganizationId": fixtureOrganizationID, "siteId": fixtureSiteID,
		"code": "controller-01", "displayName": "Controller 01", "deviceType": "CONTROLLER", "status": "ACTIVE",
		"revision": 1, "createdAt": timestamp, "updatedAt": timestamp,
	}
	collection := func(item any) map[string]any {
		return map[string]any{"items": []any{item}, "nextCursor": nil, "hasMore": false}
	}
	organizationScopes := []string{"organization:" + fixtureOrganizationID}
	siteScopes := []string{"organization:" + fixtureOrganizationID, "site:" + fixtureSiteID}

	switch path {
	case "/api/v1/health":
		return legacyRoute{action: "legacy:platform-status:read", payload: healthEnvelope{
			Code: http.StatusOK,
			Data: healthData{Status: "UP", Timestamp: now.UTC().Format(time.RFC3339Nano), Version: "legacy-fixture-v1"},
		}}, true
	case "/api/v1/organizations":
		return legacyRoute{action: "legacy:registry:organization.list", acceptableScopes: organizationScopes, payload: collection(organization)}, true
	case "/api/v1/organizations/" + fixtureOrganizationID:
		return legacyRoute{action: "legacy:registry:organization.read", acceptableScopes: organizationScopes, payload: organization}, true
	case "/api/v1/organizations/" + fixtureOrganizationID + "/sites":
		return legacyRoute{action: "legacy:registry:site.list", acceptableScopes: siteScopes, payload: collection(site)}, true
	case "/api/v1/sites/" + fixtureSiteID:
		return legacyRoute{action: "legacy:registry:site.read", acceptableScopes: siteScopes, payload: site}, true
	case "/api/v1/sites/" + fixtureSiteID + "/equipment":
		return legacyRoute{action: "legacy:registry:equipment.list", acceptableScopes: siteScopes, payload: collection(equipment)}, true
	case "/api/v1/equipment/" + fixtureEquipmentID:
		return legacyRoute{action: "legacy:registry:equipment.read", acceptableScopes: siteScopes, payload: equipment}, true
	case "/api/v1/sites/" + fixtureSiteID + "/devices":
		return legacyRoute{action: "legacy:registry:device.list", acceptableScopes: siteScopes, payload: collection(device)}, true
	case "/api/v1/devices/" + fixtureDeviceID:
		return legacyRoute{action: "legacy:registry:device.read", acceptableScopes: siteScopes, payload: device}, true
	default:
		return legacyRoute{}, false
	}
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
