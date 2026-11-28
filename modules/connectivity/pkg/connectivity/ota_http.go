package connectivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	maxOTACampaignRequestBodyBytes = 64 << 10
	maxOTAReportRequestBodyBytes   = 32 << 10
	maxOTAMultipartFormOverhead    = 256 << 10
	otaUploadDeadlineDuration      = 2 * time.Minute
	otaDownloadDeadlineDuration    = 5 * time.Minute
)

// OTAHandlerOptions configures runtime callbacks and limits for NewOTAHandler.
type OTAHandlerOptions struct {
	PublicBaseURL   string
	MaxPackageBytes int64
	Now             func() time.Time
	OnDispatch      func([]OTAStageDispatch)
	OnGatewaySeen   func(gatewayID string)
	IsGatewayOnline func(gatewayID string) bool
}

// NewOTAHandler constructs the internal operator and gateway mTLS HTTP endpoints for OTA management.
func NewOTAHandler(
	store OTAStore,
	storage *OTAArtifactStorage,
	signer *OTASignerVerifier,
	credentials GatewayPeerVerifier,
	authorize CredentialAuthorizer,
	opts OTAHandlerOptions,
) http.Handler {
	mux := http.NewServeMux()
	maxPkgBytes := opts.MaxPackageBytes
	if maxPkgBytes <= 0 {
		maxPkgBytes = DefaultMaxOTAPackageBytes
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	dispatch := func(items []OTAStageDispatch) {
		if len(items) == 0 || opts.OnDispatch == nil {
			return
		}
		if strings.TrimSpace(opts.PublicBaseURL) != "" {
			for i := range items {
				items[i].Stage.DownloadURL = BuildOTAStageDownloadURL(opts.PublicBaseURL, items[i].Stage.PackageID)
			}
		}
		opts.OnDispatch(items)
	}

	enrichDetailOnline := func(detail *OTACampaignDetail) {
		if detail == nil || opts.IsGatewayOnline == nil {
			return
		}
		for i := range detail.Tasks {
			detail.Tasks[i].GatewayOnline = opts.IsGatewayOnline(detail.Tasks[i].GatewayID)
		}
	}

	mux.HandleFunc("GET /internal/v1/ota/packages", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		limit := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "limit must be an integer between 1 and 100")
				return
			}
			limit = parsed
		}
		items, err := store.ListOTAPackages(r.Context(), claims, ListOTAPackagesFilter{
			Component:  r.URL.Query().Get("component"),
			TargetArch: r.URL.Query().Get("targetArch"),
			Status:     r.URL.Query().Get("status"),
			Limit:      limit,
		})
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if items == nil {
			items = []OTAPackage{}
		}
		writeOTAJSON(w, http.StatusOK, map[string]any{"items": items})
	})

	mux.HandleFunc("POST /internal/v1/ota/packages", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || storage == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA package storage unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		deadline := nowFn().Add(otaUploadDeadlineDuration)
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline)

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || strings.TrimSpace(params["boundary"]) == "" {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "Content-Type must be multipart/form-data")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxPkgBytes+maxOTAMultipartFormOverhead)
		mr := multipart.NewReader(r.Body, params["boundary"])

		fileKeyID, err := uuidv7.NewAt(nowFn().UTC())
		if err != nil {
			writeOTAErrorJSON(w, http.StatusInternalServerError, "OTA_SERVICE_UNAVAILABLE", "unable to allocate OTA package identifier")
			return
		}

		var (
			relPath      string
			fileName     string
			fileSize     int64
			actualSHA256 string
			fields       = make(map[string]string, 12)
		)
		cleanupFile := func() {
			if relPath != "" {
				_ = storage.Delete(relPath)
			}
		}

		for {
			part, partErr := mr.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				cleanupFile()
				var maxBytesErr *http.MaxBytesError
				if errors.As(partErr, &maxBytesErr) {
					writeOTAError(w, ErrOTAPackageTooLarge)
					return
				}
				writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "malformed multipart upload payload")
				return
			}
			formName := strings.TrimSpace(part.FormName())
			if formName == "file" {
				if relPath != "" {
					_ = part.Close()
					cleanupFile()
					writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "duplicate file part in multipart upload")
					return
				}
				fileName = filepath.Base(strings.TrimSpace(part.FileName()))
				if fileName == "." || fileName == "" {
					fileName = "edge-gateway.bin"
				}
				savedRel, savedSize, savedSHA, saveErr := storage.SaveStream(claims.TenantID, fileKeyID, part, maxPkgBytes)
				_ = part.Close()
				if saveErr != nil {
					writeOTAError(w, saveErr)
					return
				}
				relPath = savedRel
				fileSize = savedSize
				actualSHA256 = savedSHA
				continue
			}

			valBytes, readErr := io.ReadAll(io.LimitReader(part, 8<<10))
			_ = part.Close()
			if readErr != nil {
				cleanupFile()
				writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "failed reading multipart metadata field")
				return
			}
			if formName != "" {
				fields[formName] = strings.TrimSpace(string(valBytes))
			}
		}

		if relPath == "" {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "multipart file field is required")
			return
		}
		if expectedSHA := strings.ToLower(fields["expectedSha256"]); expectedSHA != "" && expectedSHA != actualSHA256 {
			cleanupFile()
			writeOTAErrorJSON(w, http.StatusUnprocessableEntity, "OTA_CHECKSUM_MISMATCH", fmt.Sprintf("uploaded binary sha256 %s does not match expectedSha256 %s", actualSHA256, expectedSHA))
			return
		}

		hwModels, err := parseHardwareModelsField(fields["hardwareModels"])
		if err != nil {
			cleanupFile()
			writeOTAError(w, err)
			return
		}
		var minCompat *string
		if rawMin := fields["minCompatibleVersion"]; rawMin != "" {
			minCompat = &rawMin
		}
		pkgID := strings.ToLower(fields["packageId"])
		if pkgID == "" {
			pkgID = fileKeyID
		} else if !uuidv7.IsValid(pkgID) {
			cleanupFile()
			writeOTAErrorJSON(w, http.StatusUnprocessableEntity, "OTA_INVALID", "packageId must be UUIDv7")
			return
		}

		input := CreateOTAPackageInput{
			PackageID:            pkgID,
			Component:            firstNonEmptyOTA(fields["component"], edgecontrol.OTAComponentEdgeGateway),
			Version:              fields["version"],
			TargetOS:             firstNonEmptyOTA(fields["targetOs"], "linux"),
			TargetArch:           fields["targetArch"],
			HardwareModels:       hwModels,
			MinCompatibleVersion: minCompat,
			FileName:             fileName,
			FileSizeBytes:        fileSize,
			SHA256:               actualSHA256,
			SigningKeyID:         fields["signingKeyId"],
			SignatureB64:         fields["signatureB64"],
			StorageRelPath:       relPath,
			ReleaseNotes:         fields["releaseNotes"],
		}
		// Normalize metadata first so Ed25519 signature binds exact canonical fields.
		normInput, normErr := func() (CreateOTAPackageInput, error) {
			temp := input
			if temp.SigningKeyID == "" {
				temp.SigningKeyID = "pending"
			}
			if temp.SignatureB64 == "" {
				temp.SignatureB64 = "pending"
			}
			return NormalizeOTAPackageInput(temp)
		}()
		if normErr != nil {
			cleanupFile()
			writeOTAError(w, normErr)
			return
		}

		keyID, sigB64, sigErr := signer.ResolveSignature(
			normInput.PackageID,
			normInput.Version,
			normInput.Component,
			normInput.TargetOS,
			normInput.TargetArch,
			normInput.SHA256,
			normInput.FileSizeBytes,
			fields["signingKeyId"],
			fields["signatureB64"],
		)
		if sigErr != nil {
			cleanupFile()
			writeOTAError(w, sigErr)
			return
		}
		input.Component = normInput.Component
		input.TargetOS = normInput.TargetOS
		input.TargetArch = normInput.TargetArch
		input.HardwareModels = normInput.HardwareModels
		input.MinCompatibleVersion = normInput.MinCompatibleVersion
		input.SigningKeyID = keyID
		input.SignatureB64 = sigB64

		pkg, err := store.CreateOTAPackage(r.Context(), claims, input)
		if err != nil {
			cleanupFile()
			writeOTAError(w, err)
			return
		}
		writeOTAJSON(w, http.StatusCreated, pkg)
	})

	mux.HandleFunc("GET /internal/v1/ota/campaigns", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		limit := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "limit must be an integer between 1 and 100")
				return
			}
			limit = parsed
		}
		items, err := store.ListOTACampaigns(r.Context(), claims, ListOTACampaignsFilter{
			Status: r.URL.Query().Get("status"),
			Limit:  limit,
		})
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if items == nil {
			items = []OTACampaignSummary{}
		}
		writeOTAJSON(w, http.StatusOK, map[string]any{"items": items})
	})

	mux.HandleFunc("POST /internal/v1/ota/campaigns", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input CreateOTACampaignInput
		if err := decodeOTAStrictJSON(w, r, &input, maxOTACampaignRequestBodyBytes, false); err != nil {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "invalid OTA campaign create request")
			return
		}
		detail, dispatches, err := store.CreateOTACampaign(r.Context(), claims, input)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		enrichDetailOnline(&detail)
		dispatch(dispatches)
		writeOTAJSON(w, http.StatusCreated, detail)
	})

	mux.HandleFunc("GET /internal/v1/ota/campaigns/{campaignId}", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceRead)
		if !ok {
			return
		}
		detail, err := store.GetOTACampaign(r.Context(), claims, r.PathValue("campaignId"))
		if err != nil {
			writeOTAError(w, err)
			return
		}
		enrichDetailOnline(&detail)
		writeOTAJSON(w, http.StatusOK, detail)
	})

	mux.HandleFunc("POST /internal/v1/ota/campaigns/{campaignId}/pause", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input OTACampaignActionInput
		if err := decodeOTAStrictJSON(w, r, &input, maxOTACampaignRequestBodyBytes, true); err != nil {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "invalid OTA campaign pause request")
			return
		}
		detail, err := store.PauseOTACampaign(r.Context(), claims, r.PathValue("campaignId"), input.Reason)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		enrichDetailOnline(&detail)
		writeOTAJSON(w, http.StatusOK, detail)
	})

	mux.HandleFunc("POST /internal/v1/ota/campaigns/{campaignId}/resume", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input OTACampaignActionInput
		if err := decodeOTAStrictJSON(w, r, &input, maxOTACampaignRequestBodyBytes, true); err != nil {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "invalid OTA campaign resume request")
			return
		}
		detail, dispatches, err := store.ResumeOTACampaign(r.Context(), claims, r.PathValue("campaignId"), input.Reason)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		enrichDetailOnline(&detail)
		dispatch(dispatches)
		writeOTAJSON(w, http.StatusOK, detail)
	})

	mux.HandleFunc("POST /internal/v1/ota/campaigns/{campaignId}/rollback", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA store unavailable")
			return
		}
		claims, ok := authorizeOTAHTTP(w, r, authorize, registryauth.ActionDeviceWrite)
		if !ok {
			return
		}
		var input OTACampaignActionInput
		if err := decodeOTAStrictJSON(w, r, &input, maxOTACampaignRequestBodyBytes, true); err != nil {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "invalid OTA campaign rollback request")
			return
		}
		detail, dispatches, err := store.RollbackOTACampaign(r.Context(), claims, r.PathValue("campaignId"), input.Reason)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		enrichDetailOnline(&detail)
		dispatch(dispatches)
		writeOTAJSON(w, http.StatusOK, detail)
	})

	mux.HandleFunc("GET /gateway/v1/ota/packages/{packageId}/download", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || storage == nil || credentials == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA download service unavailable")
			return
		}
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		gatewayID, err := credentials.VerifyActiveCredential(r.Context(), peer)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if opts.OnGatewaySeen != nil {
			opts.OnGatewaySeen(gatewayID)
		}
		_ = http.NewResponseController(w).SetWriteDeadline(nowFn().Add(otaDownloadDeadlineDuration))

		pkg, err := store.GetOTAPackageForGateway(r.Context(), gatewayID, r.PathValue("packageId"))
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if pkg.Status != OTAPackageStatusActive {
			writeOTAErrorJSON(w, http.StatusConflict, "OTA_CONFLICT", "OTA package is retired")
			return
		}
		file, info, err := storage.Open(pkg.StorageRelPath)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		defer file.Close()

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": pkg.FileName}))
		w.Header().Set("ETag", `"`+pkg.SHA256+`"`)
		w.Header().Set("X-OTA-Package-Id", pkg.ID)
		w.Header().Set("X-OTA-Version", pkg.Version)
		w.Header().Set("X-OTA-SHA256", pkg.SHA256)
		w.Header().Set("X-OTA-Signature-Alg", pkg.SignatureAlg)
		w.Header().Set("X-OTA-Signing-Key-Id", pkg.SigningKeyID)
		w.Header().Set("X-OTA-Signature", pkg.SignatureB64)
		modTime := pkg.UpdatedAt
		if modTime.IsZero() {
			modTime = info.ModTime()
		}
		http.ServeContent(w, r, pkg.FileName, modTime, file)
	})

	mux.HandleFunc("GET /gateway/v1/ota/desired", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || credentials == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA service unavailable")
			return
		}
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		gatewayID, err := credentials.VerifyActiveCredential(r.Context(), peer)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if opts.OnGatewaySeen != nil {
			opts.OnGatewaySeen(gatewayID)
		}
		stage, err := store.GetGatewayDesiredOTAStage(r.Context(), gatewayID)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if stage != nil {
			stage.DownloadURL = resolveGatewayDownloadURL(r, opts.PublicBaseURL, stage.PackageID)
		}
		writeOTAJSON(w, http.StatusOK, map[string]any{
			"gatewayId": gatewayID,
			"stage":     stage,
		})
	})

	mux.HandleFunc("POST /gateway/v1/ota/tasks/{taskId}/report", func(w http.ResponseWriter, r *http.Request) {
		if store == nil || credentials == nil {
			writeOTAErrorJSON(w, http.StatusServiceUnavailable, "OTA_UNAVAILABLE", "OTA service unavailable")
			return
		}
		peer, err := ParseGatewayPeerCert(r)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		gatewayID, err := credentials.VerifyActiveCredential(r.Context(), peer)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if opts.OnGatewaySeen != nil {
			opts.OnGatewaySeen(gatewayID)
		}
		var result edgecontrol.OTAResultPayload
		if err := decodeOTAStrictJSON(w, r, &result, maxOTAReportRequestBodyBytes, false); err != nil {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "invalid OTA result report payload")
			return
		}
		if !strings.EqualFold(strings.TrimSpace(r.PathValue("taskId")), strings.TrimSpace(result.TaskID)) {
			writeOTAErrorJSON(w, http.StatusBadRequest, "OTA_REQUEST_INVALID", "taskId path parameter does not match payload taskId")
			return
		}
		task, dispatches, err := store.ApplyGatewayOTAResult(r.Context(), gatewayID, result)
		if err != nil {
			writeOTAError(w, err)
			return
		}
		if len(dispatches) > 0 && opts.OnDispatch != nil {
			baseURL := strings.TrimSpace(opts.PublicBaseURL)
			if baseURL == "" && r.Host != "" {
				baseURL = "https://" + r.Host
			}
			for i := range dispatches {
				dispatches[i].Stage.DownloadURL = BuildOTAStageDownloadURL(baseURL, dispatches[i].Stage.PackageID)
			}
			opts.OnDispatch(dispatches)
		}
		writeOTAJSON(w, http.StatusOK, task)
	})

	return mux
}

func resolveGatewayDownloadURL(r *http.Request, configuredBase, packageID string) string {
	base := strings.TrimSpace(configuredBase)
	if base == "" && r != nil && strings.TrimSpace(r.Host) != "" {
		base = "https://" + strings.TrimSpace(r.Host)
	}
	return BuildOTAStageDownloadURL(base, packageID)
}

func parseHardwareModelsField(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []string
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, fmt.Errorf("%w: hardwareModels JSON array is invalid", ErrOTAInvalid)
		}
		return list, nil
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if item := strings.TrimSpace(p); item != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func firstNonEmptyOTA(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func authorizeOTAHTTP(w http.ResponseWriter, r *http.Request, authorize CredentialAuthorizer, action registryauth.Action) (registryauth.GrantClaims, bool) {
	if authorize == nil {
		writeOTAErrorJSON(w, http.StatusForbidden, "CREDENTIAL_FORBIDDEN", "OTA authorizer unavailable")
		return registryauth.GrantClaims{}, false
	}
	claims, err := authorize(r, action)
	if err != nil {
		writeOTAError(w, err)
		return registryauth.GrantClaims{}, false
	}
	return claims, true
}

func decodeOTAStrictJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64, optional bool) error {
	if r.Body == nil {
		if optional {
			return nil
		}
		return errors.New("request body required")
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		if optional {
			return nil
		}
		return errors.New("empty JSON body")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	return nil
}

func writeOTAJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOTAError(w http.ResponseWriter, err error) {
	status, code := mapOTAError(err)
	message := err.Error()
	if status >= http.StatusInternalServerError {
		message = "OTA service unavailable"
	}
	writeOTAErrorJSON(w, status, code, message)
}

func writeOTAErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

func mapOTAError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrOTAPackageTooLarge):
		return http.StatusRequestEntityTooLarge, "OTA_PACKAGE_TOO_LARGE"
	case errors.Is(err, ErrOTANoTargets):
		return http.StatusUnprocessableEntity, "OTA_NO_TARGETS"
	case errors.Is(err, ErrOTAInvalid):
		return http.StatusUnprocessableEntity, "OTA_INVALID"
	case errors.Is(err, ErrOTAConflict):
		return http.StatusConflict, "OTA_CONFLICT"
	case errors.Is(err, ErrEnrollmentRejected):
		return http.StatusBadRequest, "ENROLLMENT_REJECTED"
	case errors.Is(err, ErrCredentialForbidden):
		return http.StatusForbidden, "CREDENTIAL_FORBIDDEN"
	case errors.Is(err, ErrCredentialRevoked):
		return http.StatusConflict, "GATEWAY_REVOKED"
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "OTA_NOT_FOUND"
	default:
		return http.StatusInternalServerError, "OTA_SERVICE_UNAVAILABLE"
	}
}
