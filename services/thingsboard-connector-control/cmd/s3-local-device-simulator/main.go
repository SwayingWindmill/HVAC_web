package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const maxBodyBytes = int64(64 << 10)

type deviceState struct {
	mu        sync.RWMutex
	setpointC float64
	revision  uint64
	updatedAt time.Time
}

type simulator struct {
	state              *deviceState
	organizationID     string
	siteID             string
	deviceID           string
	externalDeviceID   string
	providerMethod     string
	reportedStateKey   string
	providerCredential string
	verifierSPIFFE     string
	now                func() time.Time
}

type rpcRequest struct {
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
	Timeout int64     `json:"timeout"`
}

type rpcParams struct {
	SetpointC float64 `json:"setpointC"`
}

type reportedStateResponse struct {
	SchemaVersion          int       `json:"schemaVersion"`
	EvidenceID             string    `json:"evidenceId"`
	OrganizationID         string    `json:"organizationId"`
	SiteID                 string    `json:"siteId"`
	DeviceID               string    `json:"deviceId"`
	EvaluationAvailability string    `json:"evaluationAvailability"`
	Presence               string    `json:"presence"`
	Readiness              string    `json:"readiness"`
	Freshness              string    `json:"freshness"`
	Quality                string    `json:"quality"`
	BusinessRevision       uint64                   `json:"businessRevision"`
	ReportedValue          commandmodel.ScalarValue `json:"reportedValue"`
	ObservedAt             time.Time                `json:"observedAt"`
	ReportedStateKey       string    `json:"reportedStateKey"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	tlsCertificate, err := tls.LoadX509KeyPair(requiredEnv("S3_LOCAL_SIMULATOR_TLS_CERT"), requiredEnv("S3_LOCAL_SIMULATOR_TLS_KEY"))
	if err != nil {
		logger.Error("simulator_tls_keypair_invalid")
		os.Exit(1)
	}
	verifierCABody, err := os.ReadFile(requiredEnv("S3_LOCAL_SIMULATOR_VERIFIER_CA"))
	if err != nil {
		logger.Error("simulator_verifier_ca_unreadable")
		os.Exit(1)
	}
	verifierCAs := x509.NewCertPool()
	if !verifierCAs.AppendCertsFromPEM(verifierCABody) {
		logger.Error("simulator_verifier_ca_invalid")
		os.Exit(1)
	}
	providerCredential, err := loadSingleLineFile(requiredEnv("S3_LOCAL_PROVIDER_CREDENTIAL_FILE"), maxBodyBytes)
	if err != nil {
		logger.Error("simulator_provider_credential_invalid")
		os.Exit(1)
	}

	sim := &simulator{
		state:              &deviceState{setpointC: 23, revision: 21, updatedAt: time.Now().UTC()},
		organizationID:     requiredEnv("S3_LOCAL_ORGANIZATION_ID"),
		siteID:             requiredEnv("S3_LOCAL_SITE_ID"),
		deviceID:           requiredEnv("S3_LOCAL_DEVICE_ID"),
		externalDeviceID:   requiredEnv("S3_LOCAL_EXTERNAL_DEVICE_ID"),
		providerMethod:     requiredEnv("S3_LOCAL_PROVIDER_METHOD"),
		reportedStateKey:   requiredEnv("S3_LOCAL_REPORTED_STATE_KEY"),
		providerCredential: providerCredential,
		verifierSPIFFE:     envOr("S3_LOCAL_VERIFIER_SPIFFE", "spiffe://hvac.local/command-verifier"),
		now:                time.Now,
	}

	providerServer := &http.Server{
		Addr:              envOr("S3_LOCAL_PROVIDER_ADDR", ":8448"),
		Handler:           sim.providerHandler(),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{tlsCertificate}},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	reportedStateServer := &http.Server{
		Addr:    envOr("S3_LOCAL_REPORTED_STATE_ADDR", ":8449"),
		Handler: sim.reportedStateHandler(),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{tlsCertificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    verifierCAs,
		},
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	diagnosticsServer := &http.Server{
		Addr: envOr("S3_LOCAL_SIMULATOR_DIAGNOSTICS_ADDR", ":19090"),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/health/ready" && request.URL.Path != "/health/live" {
				http.NotFound(writer, request)
				return
			}
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok\n"))
		}),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
	}

	errCh := make(chan error, 3)
	go func() { errCh <- providerServer.ListenAndServeTLS("", "") }()
	go func() { errCh <- reportedStateServer.ListenAndServeTLS("", "") }()
	go func() { errCh <- diagnosticsServer.ListenAndServe() }()
	logger.Info("s3_local_device_simulator_started", "external_device_id", sim.externalDeviceID)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("s3_local_device_simulator_failed", "error", serveErr.Error())
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = providerServer.Shutdown(shutdownContext)
	_ = reportedStateServer.Shutdown(shutdownContext)
	_ = diagnosticsServer.Shutdown(shutdownContext)
}

func (sim *simulator) providerHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		expectedPath := "/api/rpc/twoway/" + sim.externalDeviceID
		if request.Method != http.MethodPost || request.URL.Path != expectedPath {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("X-Authorization") != "Bearer "+sim.providerCredential {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"success": false})
			return
		}
		body, err := readBounded(request.Body, maxBodyBytes)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false})
			return
		}
		var input rpcRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || ensureEOF(decoder) != nil || input.Method != sim.providerMethod || input.Timeout < 1000 || input.Timeout > 30000 || input.Params.SetpointC < 16 || input.Params.SetpointC > 30 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false})
			return
		}
		now := sim.now().UTC()
		sim.state.mu.Lock()
		sim.state.setpointC = input.Params.SetpointC
		sim.state.revision++
		sim.state.updatedAt = now
		sim.state.mu.Unlock()
		writeJSON(writer, http.StatusOK, map[string]any{"success": true, "appliedSetpointC": input.Params.SetpointC})
	})
}

func (sim *simulator) reportedStateHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/internal/v1/commands/reported-state" {
			http.NotFound(writer, request)
			return
		}
		if peerSPIFFE(request) != sim.verifierSPIFFE {
			writeJSON(writer, http.StatusForbidden, map[string]any{"code": "VERIFIER_WORKLOAD_FORBIDDEN"})
			return
		}
		if request.URL.Query().Get("key") != sim.reportedStateKey || len(request.URL.Query()) != 1 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"code": "REPORTED_STATE_KEY_INVALID"})
			return
		}
		sim.state.mu.RLock()
		setpointC := sim.state.setpointC
		revision := sim.state.revision
		sim.state.mu.RUnlock()
		observedAt := sim.now().UTC()
		evidenceInput := fmt.Sprintf("%s|%s|%s|%s|%d|%.3f|%s", sim.organizationID, sim.siteID, sim.deviceID, sim.reportedStateKey, revision, setpointC, observedAt.Format(time.RFC3339Nano))
		digest := sha256.Sum256([]byte(evidenceInput))
		writeJSON(writer, http.StatusOK, reportedStateResponse{
			SchemaVersion:          1,
			EvidenceID:             "s2:sha256:" + hex.EncodeToString(digest[:]),
			OrganizationID:         sim.organizationID,
			SiteID:                 sim.siteID,
			DeviceID:               sim.deviceID,
			EvaluationAvailability: "AVAILABLE",
			Presence:               "ONLINE",
			Readiness:              "CURRENT",
			Freshness:              "FRESH",
			Quality:                "GOOD",
			BusinessRevision:       revision,
			ReportedValue:          commandmodel.NumberScalar(setpointC),
			ObservedAt:             observedAt,
			ReportedStateKey:       sim.reportedStateKey,
		})
	})
}

func peerSPIFFE(request *http.Request) string {
	if request == nil || request.TLS == nil || len(request.TLS.PeerCertificates) != 1 || request.TLS.PeerCertificates[0] == nil {
		return ""
	}
	leaf := request.TLS.PeerCertificates[0]
	if len(leaf.URIs) != 1 || leaf.URIs[0] == nil {
		return ""
	}
	return leaf.URIs[0].String()
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("body exceeds limit")
	}
	return body, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func loadSingleLineFile(path string, maximum int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	body, err := readBounded(file, maximum)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("value file is invalid")
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		_, _ = os.Stderr.WriteString(name + " is required\n")
		os.Exit(1)
	}
	return value
}
