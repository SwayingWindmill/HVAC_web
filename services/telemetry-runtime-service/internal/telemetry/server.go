package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/telemetryauth"
	"github.com/quanlaihe/hvac-web/services/telemetry-runtime-service/pkg/telemetryapi"
)

const (
	InternalDeviceSnapshotPrefix = "/internal/v1/devices/"
	InternalBatchSnapshotPath    = "/internal/v1/telemetry/observation-snapshots:batchGet"
	maximumSnapshotRequestSize   = 256 << 10
)

var (
	safeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	traceIDPattern       = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type ServerConfig struct {
	Store                SnapshotStore
	Authorizer           GrantAuthorizer
	AllowedGatewaySPIFFE string
	Now                  func() time.Time
}

type handler struct {
	store                SnapshotStore
	authorizer           GrantAuthorizer
	allowedGatewaySPIFFE string
	now                  func() time.Time
}

func NewHandler(config ServerConfig) http.Handler {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &handler{
		store: config.Store, authorizer: config.Authorizer,
		allowedGatewaySPIFFE: strings.TrimSpace(config.AllowedGatewaySPIFFE), now: now,
	}
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == InternalBatchSnapshotPath {
		h.handleBatch(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, InternalDeviceSnapshotPrefix) && strings.HasSuffix(request.URL.Path, "/observation-snapshot") {
		h.handleSingle(writer, request)
		return
	}
	writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
}

func (h *handler) handleSingle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports GET.", false)
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	deviceID, ok := parseSinglePath(request.URL.Path)
	if !ok {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	for name := range request.URL.Query() {
		if name != "key" {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry selection is invalid.", false)
			return
		}
	}
	keys := append([]string(nil), request.URL.Query()["key"]...)
	target := telemetryauth.Target{DeviceID: deviceID, Keys: keys}
	if _, err := telemetryauth.CanonicalTargets([]telemetryauth.Target{target}); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry selection is invalid.", false)
		return
	}
	if !h.authorize(writer, request, peer, grant, telemetryauth.ActionSnapshotRead, []telemetryauth.Target{target}) {
		return
	}
	commit, err := h.store.EvaluateAndRead(request.Context(), target, h.now().UTC())
	if errors.Is(err, ErrDeviceNotFound) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
		return
	}
	writeJSON(writer, http.StatusOK, commit.Snapshot)
}

func (h *handler) handleBatch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, request, http.StatusMethodNotAllowed, "TELEMETRY_METHOD_NOT_ALLOWED", "This telemetry route only supports POST.", false)
		return
	}
	peer, grant, ok := h.authenticate(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumSnapshotRequestSize)
	var input telemetryapi.BatchGetObservationSnapshotsRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil || len(input.Requests) == 0 || len(input.Requests) > telemetryauth.MaximumTargets {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
		return
	}
	targets := make([]telemetryauth.Target, len(input.Requests))
	seenRequestIDs := make(map[string]struct{}, len(input.Requests))
	for index, item := range input.Requests {
		if strings.TrimSpace(item.RequestId) == "" {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
			return
		}
		if _, duplicate := seenRequestIDs[item.RequestId]; duplicate {
			writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
			return
		}
		seenRequestIDs[item.RequestId] = struct{}{}
		keys := make([]string, len(item.Keys))
		for keyIndex, key := range item.Keys {
			keys[keyIndex] = string(key)
		}
		targets[index] = telemetryauth.Target{DeviceID: string(item.DeviceId), Keys: keys}
	}
	if _, err := telemetryauth.CanonicalTargets(targets); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_REQUEST_INVALID", "The telemetry batch request is invalid.", false)
		return
	}
	if !h.authorize(writer, request, peer, grant, telemetryauth.ActionBatchRead, targets) {
		return
	}
	evaluatedAt := h.now().UTC()
	response := telemetryapi.BatchGetObservationSnapshotsResponse{SchemaVersion: 1, Items: make([]telemetryapi.BatchObservationResult, 0, len(input.Requests))}
	for index, item := range input.Requests {
		commit, err := h.store.EvaluateAndRead(request.Context(), targets[index], evaluatedAt)
		if errors.Is(err, ErrDeviceNotFound) {
			response.Items = append(response.Items, telemetryapi.BatchObservationResult{Failure: &telemetryapi.BatchObservationFailure{
				RequestId: item.RequestId, DeviceId: item.DeviceId, Status: "ERROR",
				Problem: problemDetails(request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false),
			}})
			continue
		}
		if err != nil {
			writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
			return
		}
		response.Items = append(response.Items, telemetryapi.BatchObservationResult{Success: &telemetryapi.BatchObservationSuccess{
			RequestId: item.RequestId, DeviceId: item.DeviceId, Status: "OK", Snapshot: commit.Snapshot,
		}})
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *handler) authenticate(writer http.ResponseWriter, request *http.Request) (string, string, bool) {
	if hasForgedIdentityHeader(request.Header) {
		writeProblem(writer, request, http.StatusBadRequest, "TELEMETRY_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.", false)
		return "", "", false
	}
	peer, ok := verifiedPeerSPIFFE(request)
	if !ok || h.allowedGatewaySPIFFE == "" || peer != h.allowedGatewaySPIFFE {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.", false)
		return "", "", false
	}
	grant, ok := bearerGrant(request.Header.Get("Authorization"))
	if !ok {
		writeProblem(writer, request, http.StatusUnauthorized, "TELEMETRY_GRANT_REQUIRED", "A Telemetry delegation grant is required.", false)
		return "", "", false
	}
	if h.authorizer == nil || h.store == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_RUNTIME_UNAVAILABLE", "The authoritative telemetry runtime is temporarily unavailable.", true)
		return "", "", false
	}
	return peer, grant, true
}

func (h *handler) authorize(writer http.ResponseWriter, request *http.Request, peer, grant string, action telemetryauth.Action, targets []telemetryauth.Target) bool {
	_, err := h.authorizer.Authorize(request.Context(), peer, grant, action, targets)
	if errors.Is(err, ErrGrantRejected) {
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested telemetry resource was not found.", false)
		return false
	}
	if err != nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "TELEMETRY_AUTHORIZATION_UNAVAILABLE", "Telemetry authorization is temporarily unavailable.", true)
		return false
	}
	return true
}

func parseSinglePath(path string) (string, bool) {
	trimmed := strings.TrimPrefix(path, InternalDeviceSnapshotPrefix)
	if trimmed == path || !strings.HasSuffix(trimmed, "/observation-snapshot") {
		return "", false
	}
	deviceID := strings.TrimSuffix(trimmed, "/observation-snapshot")
	if deviceID == "" || strings.Contains(deviceID, "/") {
		return "", false
	}
	return deviceID, true
}

func verifiedPeerSPIFFE(request *http.Request) (string, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].Scheme != "spiffe" {
		return "", false
	}
	return certificate.URIs[0].String(), true
}

func hasForgedIdentityHeader(header http.Header) bool {
	for name, values := range header {
		nonEmpty := false
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			continue
		}
		lowerName := strings.ToLower(name)
		switch lowerName {
		case "x-principal", "x-roles", "x-role", "x-admin", "x-scope", "x-organization-id", "x-site-id", "x-delegation-grant":
			return true
		}
		if strings.HasPrefix(lowerName, "x-principal-") || strings.HasPrefix(lowerName, "x-organization-") || strings.HasPrefix(lowerName, "x-site-") {
			return true
		}
	}
	return false
}

func bearerGrant(value string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("additional JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProblem(writer http.ResponseWriter, request *http.Request, status int, code, detail string, retryable bool) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problemDetails(request, status, code, detail, retryable))
}

func problemDetails(request *http.Request, status int, code, detail string, retryable bool) telemetryapi.ProblemDetails {
	return telemetryapi.ProblemDetails{
		Type:  "https://api.quanlaihe.com/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: http.StatusText(status), Status: status, Detail: detail, Instance: request.URL.Path,
		Code: code, TraceId: problemTraceID(request), Retryable: retryable,
	}
}

func problemTraceID(request *http.Request) string {
	parts := strings.Split(strings.TrimSpace(request.Header.Get("Traceparent")), "-")
	if len(parts) == 4 {
		traceID := strings.ToLower(parts[1])
		if len(parts[0]) == 2 && traceIDPattern.MatchString(traceID) && traceID != strings.Repeat("0", 32) {
			return traceID
		}
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if traceIDPattern.MatchString(strings.ToLower(requestID)) {
		return strings.ToLower(requestID)
	}
	seed := request.Method + "|" + request.URL.RequestURI() + "|" + request.RemoteAddr
	if safeRequestIDPattern.MatchString(requestID) {
		seed = requestID
	}
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:16])
}
