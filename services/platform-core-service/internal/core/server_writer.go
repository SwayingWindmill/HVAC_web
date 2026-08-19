package core

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const maxRegistryMutationBytes = 2 << 20

func (server *server) handleAuthorizedWrite(writer http.ResponseWriter, request *http.Request, route registryRoute, claims registryauth.GrantClaims) int {
	if server.writer == nil {
		writeProblem(writer, request, http.StatusServiceUnavailable, "REGISTRY_WRITER_UNAVAILABLE", "The Registry writer is not configured.", true)
		return http.StatusServiceUnavailable
	}
	if request.URL.RawQuery != "" {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "Registry mutation routes do not accept query parameters.", false)
		return http.StatusBadRequest
	}

	switch route.resource {
	case "site-write":
		var input SiteMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.ID = route.id
		value, replayed, err := server.writer.SaveSite(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "space-write":
		var input SpaceMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID, input.ID = route.parentID, route.id
		value, replayed, err := server.writer.SaveSpace(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "asset-write":
		var input AssetMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID, input.ID = route.parentID, route.id
		value, replayed, err := server.writer.SaveAsset(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "device-write":
		var input DeviceMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID, input.ID = route.parentID, route.id
		value, replayed, err := server.writer.SaveDevice(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "sensor-write":
		var input SensorMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID, input.ID = route.parentID, route.id
		value, replayed, err := server.writer.SaveSensor(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "point-write":
		var input PointMutation
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID, input.ID = route.parentID, route.id
		value, replayed, err := server.writer.SavePoint(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, route.id == "", err)
	case "binding-write":
		var input RebindRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID = route.parentID
		value, err := server.writer.Rebind(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, value.Replayed, true, err)
	case "template-release":
		var input ReleaseTemplateRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		value, replayed, err := server.writer.ReleaseTemplate(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, true, err)
	case "template-assign":
		var input AssignTemplateRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		value, replayed, err := server.writer.AssignTemplate(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, replayed, true, err)
	case "import-plan":
		var input ImportPlanRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID = route.parentID
		value, err := server.writer.PlanImport(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, false, false, err)
	case "import-commit":
		var input ImportCommitRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		if input.Plan.SiteID != route.parentID {
			return server.writeMutationError(writer, request, ErrImportPlanInvalid)
		}
		value, err := server.writer.CommitImport(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, value.Replayed, true, err)
	case "retire":
		var input RetireRequest
		if !decodeRegistryMutation(writer, request, &input) {
			return http.StatusBadRequest
		}
		input.SiteID = route.parentID
		value, err := server.writer.Retire(request.Context(), claims, input)
		return server.writeMutationResult(writer, request, value, value.Replayed, true, err)
	default:
		writeProblem(writer, request, http.StatusNotFound, "CORE_ROUTE_NOT_FOUND", "The requested Core Registry route does not exist.", false)
		return http.StatusNotFound
	}
}

func decodeRegistryMutation(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRegistryMutationBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "The Registry mutation payload is invalid.", false)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "The Registry mutation payload must contain exactly one JSON value.", false)
		return false
	}
	return true
}

func (server *server) writeMutationResult(writer http.ResponseWriter, request *http.Request, value any, replayed bool, created bool, err error) int {
	if err != nil {
		return server.writeMutationError(writer, request, err)
	}
	if replayed {
		writer.Header().Set("X-Idempotent-Replay", "true")
		created = false
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, value)
	return status
}

func (server *server) writeMutationError(writer http.ResponseWriter, request *http.Request, err error) int {
	switch {
	case errors.Is(err, ErrInvalidMutation):
		writeProblem(writer, request, http.StatusBadRequest, "REGISTRY_MUTATION_INVALID", "The Registry mutation does not satisfy the canonical contract.", false)
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		writeProblem(writer, request, http.StatusNotFound, "RESOURCE_NOT_FOUND", "The requested Registry resource was not found in the authorized scope.", false)
		return http.StatusNotFound
	case errors.Is(err, ErrRevisionConflict):
		writeProblem(writer, request, http.StatusConflict, "REGISTRY_REVISION_CONFLICT", "The expected Registry revision is stale.", false)
		return http.StatusConflict
	case errors.Is(err, ErrIdempotencyConflict):
		writeProblem(writer, request, http.StatusConflict, "REGISTRY_IDEMPOTENCY_CONFLICT", "The idempotency key was already used for a different Registry mutation.", false)
		return http.StatusConflict
	case errors.Is(err, ErrBindingConflict):
		writeProblem(writer, request, http.StatusConflict, "REGISTRY_BINDING_CONFLICT", "The requested typed binding violates cardinality, interval, or scope constraints.", false)
		return http.StatusConflict
	case errors.Is(err, ErrTemplateImmutable):
		writeProblem(writer, request, http.StatusConflict, "TEMPLATE_REVISION_IMMUTABLE", "Released Template revisions are immutable; publish a new revision or assignment instead.", false)
		return http.StatusConflict
	case errors.Is(err, ErrImportPlanInvalid):
		writeProblem(writer, request, http.StatusConflict, "REGISTRY_IMPORT_PLAN_INVALID", "The Registry import plan is stale, inconsistent, or contains row errors.", false)
		return http.StatusConflict
	default:
		return server.writeStoreError(writer, request, err)
	}
}
