package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/modules/audit/pkg/auditserver"
	"github.com/quanlaihe/hvac-web/modules/command/pkg/commandservice"
)

type createSiteCommandBatchRequest struct {
	Capability       commandmodel.Capability                  `json:"capability"`
	Parameters       commandmodel.CommandParameters           `json:"parameters,omitempty"`
	DeliveryMode     commandmodel.DeliveryMode                `json:"deliveryMode,omitempty"`
	TTLSeconds       *int                                     `json:"ttlSeconds,omitempty"`
	Reason           string                                   `json:"reason,omitempty"`
	Targets          []commandservice.CommandBatchTargetInput `json:"targets"`
	VerificationCode string                                   `json:"verificationCode,omitempty"`
}

func (h *handler) createSiteCommandBatch(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "site:" + route.resourceID
	if len(request.URL.Query()) != 0 {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeRejected, map[string]any{"reasonCode": "COMMAND_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command batch request invalid", "Command batch submission does not accept query parameters.", false, nil)
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeRejected, map[string]any{"reasonCode": "COMMAND_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_REQUEST_INVALID", "Command batch request invalid", "The Command batch request must use application/json.", false, nil)
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if len(idempotencyKey) < 8 || !requestIDPattern.MatchString(idempotencyKey) {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeRejected, map[string]any{"reasonCode": "COMMAND_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command batch request invalid", "A valid Idempotency-Key is required.", false, nil)
		return
	}
	var input createSiteCommandBatchRequest
	if !decodeStrictControlJSON(writer, request, &input, maximumCommandRequestBody) {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeRejected, map[string]any{"reasonCode": "COMMAND_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command batch request invalid", "The Command batch request payload is invalid.", false, nil)
		return
	}

	var extraActions []string
	if strings.TrimSpace(input.VerificationCode) != "" {
		if stepUpFailure := h.verifySubmitStepUpTOTP(request, session, input.VerificationCode); stepUpFailure != nil {
			h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeDenied, map[string]any{
				"capability":     string(input.Capability),
				"targetCount":    len(input.Targets),
				"reasonCode":     stepUpFailure.code,
				"stepUpRequired": true,
			})
			h.writeCommandFailure(writer, request, *stepUpFailure)
			return
		}
		extraActions = append(extraActions, commandservice.StepUpActionTOTP)
	}

	payload := commandservice.SubmitCommandBatchInput{
		Capability:     input.Capability,
		Parameters:     input.Parameters,
		DeliveryMode:   input.DeliveryMode,
		TTLSeconds:     input.TTLSeconds,
		IdempotencyKey: idempotencyKey,
		Reason:         input.Reason,
		Targets:        input.Targets,
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost, route.internalPath, payload,
		map[string]string{"Idempotency-Key": idempotencyKey}, h.command.maxResponseBytes, extraActions...,
	)
	if failure != nil {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeForStatus(failure.status), map[string]any{
			"capability":  string(input.Capability),
			"targetCount": len(input.Targets),
			"reasonCode":  failure.code,
		})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeForStatus(status), map[string]any{
			"capability":  string(input.Capability),
			"targetCount": len(input.Targets),
			"reasonCode":  problemCodeFromRaw(rawBody),
		})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}

	var view commandservice.CommandBatchView
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || ensureCommandJSONEOF(decoder) != nil ||
		!isLowerUUID(view.ID) || view.TenantID != session.TenantID || view.SiteID != route.resourceID {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_SUBMITTED", auditOutcomeFailed, map[string]any{
			"capability":  string(input.Capability),
			"targetCount": len(input.Targets),
			"reasonCode":  "COMMAND_RESPONSE_INVALID",
		})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command batch projection."))
		return
	}
	if view.Items == nil {
		view.Items = []commandservice.CommandBatchItemView{}
	}

	h.emitBatchAudit(request, session, "command-batch:"+view.ID, "COMMAND_BATCH_SUBMITTED", auditOutcomeSucceeded, map[string]any{
		"siteId":        view.SiteID,
		"capability":    string(view.Capability),
		"deliveryMode":  string(view.DeliveryMode),
		"targetCount":   view.TargetCount,
		"acceptedCount": view.AcceptedCount,
		"rejectedCount": view.RejectedCount,
		"status":        view.Status,
	})
	writer.Header().Set("Location", "/api/v1/sites/"+url.PathEscape(route.resourceID)+"/command-batches/"+url.PathEscape(view.ID))
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusAccepted, view)
}

func (h *handler) listSiteCommandBatches(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_LIST_REQUEST_INVALID", "Command batch list request invalid", "Only a single limit query parameter is supported.", false, nil)
			return
		}
	}
	internalPath := route.internalPath
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maximumListedCommands {
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_LIST_REQUEST_INVALID", "Command batch list request invalid", "limit must be between 1 and 100.", false, nil)
			return
		}
		internalPath += "?limit=" + strconv.Itoa(limit)
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, internalPath, nil, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var listView commandservice.CommandBatchListView
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&listView) != nil || ensureCommandJSONEOF(decoder) != nil {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command batch list."))
		return
	}
	if listView.Items == nil {
		listView.Items = []commandservice.CommandBatchView{}
	}
	for i := range listView.Items {
		item := &listView.Items[i]
		if !isLowerUUID(item.ID) || item.TenantID != session.TenantID || item.SiteID != route.resourceID {
			h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command batch list."))
			return
		}
		if item.Items == nil {
			item.Items = []commandservice.CommandBatchItemView{}
		}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, listView)
}

func (h *handler) getSiteCommandBatch(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	if len(request.URL.Query()) != 0 {
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_REQUEST_INVALID", "Command batch request invalid", "Command batch detail does not accept query parameters.", false, nil)
		return
	}
	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodGet, route.internalPath, nil, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var view commandservice.CommandBatchView
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || ensureCommandJSONEOF(decoder) != nil ||
		view.ID != route.secondaryID || view.TenantID != session.TenantID || view.SiteID != route.resourceID {
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command batch projection."))
		return
	}
	if view.Items == nil {
		view.Items = []commandservice.CommandBatchItemView{}
	}
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, view)
}

func (h *handler) cancelSiteCommandBatch(writer http.ResponseWriter, request *http.Request, session bffSession, route publicControlExitRoute) {
	targetID := "command-batch:" + route.secondaryID
	if len(request.URL.Query()) != 0 {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeRejected, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_CANCEL_REQUEST_INVALID"})
		writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command batch cancel invalid", "Command batch cancel does not accept query parameters.", false, nil)
		return
	}
	var body commandservice.CommandBatchCancelInput
	if request.Body != nil && request.ContentLength != 0 {
		if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != "" && mediaType != "application/json" {
			h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeRejected, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_CANCEL_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusUnsupportedMediaType, "COMMAND_CANCEL_REQUEST_INVALID", "Command batch cancel invalid", "The Command batch cancel request body must use application/json.", false, nil)
			return
		}
		if !decodeStrictControlJSON(writer, request, &body, maximumControlExitRequestBody) {
			h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeRejected, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_CANCEL_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command batch cancel invalid", "The Command batch cancel request payload is invalid.", false, nil)
			return
		}
		body.Reason = strings.TrimSpace(body.Reason)
		if utf8.RuneCountInString(body.Reason) > 256 {
			h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeRejected, map[string]any{"siteId": route.resourceID, "reasonCode": "COMMAND_CANCEL_REQUEST_INVALID"})
			writeProblem(writer, request, http.StatusBadRequest, "COMMAND_CANCEL_REQUEST_INVALID", "Command batch cancel invalid", "reason must be at most 256 characters.", false, nil)
			return
		}
	}

	status, rawBody, failure := h.callOperatorCommandBackend(
		request, session, http.MethodPost, route.internalPath, body, nil, h.command.maxResponseBytes,
	)
	if failure != nil {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeForStatus(failure.status), map[string]any{
			"siteId":     route.resourceID,
			"reasonCode": failure.code,
		})
		h.writeCommandFailure(writer, request, *failure)
		return
	}
	if status != http.StatusOK {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeForStatus(status), map[string]any{
			"siteId":     route.resourceID,
			"reasonCode": problemCodeFromRaw(rawBody),
		})
		h.writeOperatorProblem(writer, request, status, rawBody, "")
		return
	}
	var resp commandservice.CommandBatchCancelResponse
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&resp) != nil || ensureCommandJSONEOF(decoder) != nil ||
		resp.Batch.ID != route.secondaryID || resp.Batch.TenantID != session.TenantID || resp.Batch.SiteID != route.resourceID {
		h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeFailed, map[string]any{
			"siteId":     route.resourceID,
			"reasonCode": "COMMAND_RESPONSE_INVALID",
		})
		h.writeCommandFailure(writer, request, commandUnavailable("Command Service returned an invalid Command batch cancel response."))
		return
	}
	if resp.Batch.Items == nil {
		resp.Batch.Items = []commandservice.CommandBatchItemView{}
	}
	if resp.CancelledCommandIDs == nil {
		resp.CancelledCommandIDs = []string{}
	}

	h.emitBatchAudit(request, session, targetID, "COMMAND_BATCH_CANCELLED", auditOutcomeSucceeded, map[string]any{
		"siteId":              route.resourceID,
		"cancelledCommandIds": resp.CancelledCommandIDs,
		"status":              resp.Batch.Status,
	})
	writer.Header().Set("Cache-Control", "private, no-store")
	writeJSON(writer, http.StatusOK, resp)
}

func (h *handler) emitBatchAudit(
	request *http.Request,
	session bffSession,
	targetID, action, outcome string,
	summary map[string]any,
) {
	h.emitDomainAudit(request.Context(), request, session, domainAuditInput{
		Category:      auditserver.AggregateTypeRemoteCommand,
		TargetID:      targetID,
		TargetLabel:   "批量群控指令",
		Action:        action,
		Outcome:       outcome,
		ChangeSummary: summary,
	})
}
