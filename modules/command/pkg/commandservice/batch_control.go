package commandservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

var (
	ErrCommandBatchNotFound = errors.New("command batch not found")
	ErrCommandBatchConflict = errors.New("command batch idempotency key was reused with a different payload")
)

type CommandBatchTargetInput struct {
	DeviceID   string                         `json:"deviceId"`
	PointID    string                         `json:"pointId"`
	Parameters commandmodel.CommandParameters `json:"parameters,omitempty"`
}

type SubmitCommandBatchInput struct {
	SiteID         string                         `json:"-"`
	Capability     commandmodel.Capability        `json:"capability"`
	Parameters     commandmodel.CommandParameters `json:"parameters,omitempty"`
	DeliveryMode   commandmodel.DeliveryMode      `json:"deliveryMode,omitempty"`
	TTLSeconds     *int                           `json:"ttlSeconds,omitempty"`
	IdempotencyKey string                         `json:"idempotencyKey"`
	Reason         string                         `json:"reason,omitempty"`
	TraceID        string                         `json:"traceId,omitempty"`
	Targets        []CommandBatchTargetInput      `json:"targets"`
}

type CommandBatchCancelInput struct {
	Reason string `json:"reason,omitempty"`
}

type CommandBatchItemView struct {
	ItemIndex         int     `json:"itemIndex"`
	DeviceID          string  `json:"deviceId"`
	PointID           string  `json:"pointId"`
	CommandID         *string `json:"commandId,omitempty"`
	Admitted          bool    `json:"admitted"`
	Status            string  `json:"status"`
	RejectionCode     *string `json:"rejectionCode,omitempty"`
	RejectionReason   *string `json:"rejectionReason,omitempty"`
	ExpiresAt         *string `json:"expiresAt,omitempty"`
	NextDispatchAfter *string `json:"nextDispatchAfter,omitempty"`
}

type CommandBatchView struct {
	ID                     string                     `json:"id"`
	TenantID               string                     `json:"tenantId"`
	SiteID                 string                     `json:"siteId"`
	IdempotencyKey         string                     `json:"idempotencyKey"`
	Capability             commandmodel.Capability    `json:"capability"`
	CommandSource          commandmodel.CommandSource `json:"commandSource"`
	DeliveryMode           commandmodel.DeliveryMode  `json:"deliveryMode"`
	TTLSeconds             *int                       `json:"ttlSeconds,omitempty"`
	ExpiresAt              *string                    `json:"expiresAt,omitempty"`
	TargetCount            int                        `json:"targetCount"`
	AcceptedCount          int                        `json:"acceptedCount"`
	RejectedCount          int                        `json:"rejectedCount"`
	Status                 string                     `json:"status"`
	InitiatedByPrincipalID string                     `json:"initiatedByPrincipalId"`
	TraceID                string                     `json:"traceId"`
	Reason                 string                     `json:"reason"`
	CreatedAt              string                     `json:"createdAt"`
	UpdatedAt              string                     `json:"updatedAt"`
	Replayed               bool                       `json:"replayed"`
	Items                  []CommandBatchItemView     `json:"items"`
}

type CommandBatchListView struct {
	Items []CommandBatchView `json:"items"`
}

type CommandBatchCancelResponse struct {
	Batch               CommandBatchView `json:"batch"`
	CancelledCommandIDs []string         `json:"cancelledCommandIds"`
}

type CommandBatchItemRecord struct {
	BatchID         string
	ItemIndex       int
	TenantID        string
	SiteID          string
	DeviceID        string
	PointID         string
	CommandID       *string
	Admitted        bool
	RejectionCode   string
	RejectionReason string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CommandBatchRecord struct {
	ID                     string
	TenantID               string
	SiteID                 string
	IdempotencyKey         string
	RequestHash            string
	Capability             commandmodel.Capability
	CommandSource          commandmodel.CommandSource
	DeliveryMode           commandmodel.DeliveryMode
	TTLSeconds             *int
	ExpiresAt              *time.Time
	TargetCount            int
	AcceptedCount          int
	RejectedCount          int
	Status                 string
	InitiatedByPrincipalID string
	TraceID                string
	Reason                 string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Items                  []CommandBatchItemRecord
}

type CommandBatchStore interface {
	GetCommandBatchByIdempotencyKey(ctx context.Context, tenantID, siteID, key, requestHash string) (CommandBatchView, bool, error)
	CreateCommandBatch(ctx context.Context, record CommandBatchRecord) (CommandBatchView, bool, error)
	FinalizeCommandBatch(ctx context.Context, tenantID, batchID string, items []CommandBatchItemRecord, acceptedCount, rejectedCount int, status string) (CommandBatchView, error)
	GetCommandBatch(ctx context.Context, tenantID, siteID, batchID string) (CommandBatchView, error)
	ListCommandBatches(ctx context.Context, tenantID, siteID string, limit int) ([]CommandBatchView, error)
	UpdateCommandBatchItemsStatus(ctx context.Context, tenantID, batchID string, commandIDs []string, status string) error
}

func (a *ControlAuthority) SubmitOperatorCommandBatch(
	ctx context.Context,
	operator OperatorIdentity,
	input SubmitCommandBatchInput,
) (CommandBatchView, error) {
	if a.twin == nil || a.live == nil || a.iam == nil {
		return CommandBatchView{}, unavailableControlError("Command Service control ports are not configured.")
	}
	batchStore, ok := a.store.(CommandBatchStore)
	if !ok {
		return CommandBatchView{}, unavailableControlError("Command Service batch storage is not configured.")
	}
	siteID := strings.TrimSpace(input.SiteID)
	if !commandmodel.IsUUIDv7(siteID) {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "siteId must be a Site UUIDv7.",
		}
	}
	idemKey := strings.TrimSpace(input.IdempotencyKey)
	if !controlWriteKeyPattern.MatchString(idemKey) {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "A valid Idempotency-Key is required.",
		}
	}
	if len(input.Targets) < 1 || len(input.Targets) > commandmodel.MaxBatchCommandTargets {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Batch control targets must contain between 1 and 100 unique targets.",
		}
	}
	reason := strings.TrimSpace(input.Reason)
	if utf8.RuneCountInString(reason) > 1000 {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "reason must be at most 1000 characters.",
		}
	}
	profile, capSupported := commandmodel.CapabilityProfileFor(input.Capability)
	if !capSupported {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Unsupported batch control capability.",
		}
	}
	now := a.now().UTC()
	deliveryMode, ttlSeconds, expiresAt, deliveryErr := commandmodel.ValidateDeliveryAndTTL(input.Capability, input.DeliveryMode, input.TTLSeconds, nil, now)
	if deliveryErr != nil {
		return CommandBatchView{}, controlErrorFromStore(deliveryErr, deliveryErr.Error())
	}

	seenTargets := make(map[string]struct{}, len(input.Targets))
	normalizedTargets := make([]CommandBatchTargetInput, len(input.Targets))
	for i, target := range input.Targets {
		devID := strings.TrimSpace(target.DeviceID)
		ptID := strings.TrimSpace(target.PointID)
		if !commandmodel.IsUUIDv7(devID) || !commandmodel.IsUUIDv7(ptID) {
			return CommandBatchView{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
				Title: "Command request invalid", Detail: fmt.Sprintf("Target %d deviceId and pointId must be UUIDv7 values.", i),
			}
		}
		targetKey := devID + "\x00" + ptID
		if _, dup := seenTargets[targetKey]; dup {
			return CommandBatchView{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
				Title: "Command request invalid", Detail: "Duplicate (deviceId, pointId) target in batch request.",
			}
		}
		seenTargets[targetKey] = struct{}{}
		mergedParams := cloneParameters(target.Parameters)
		if len(mergedParams) == 0 && len(input.Parameters) > 0 {
			mergedParams = cloneParameters(input.Parameters)
		}
		if !commandmodel.ParametersValid(input.Capability, mergedParams) {
			return CommandBatchView{}, &ControlError{
				HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
				Title: "Command request invalid", Detail: fmt.Sprintf("Target %d parameters are invalid for capability %s.", i, input.Capability),
			}
		}
		normalizedTargets[i] = CommandBatchTargetInput{
			DeviceID:   devID,
			PointID:    ptID,
			Parameters: mergedParams,
		}
	}

	requestHash := hashCommandBatchRequest(operator.TenantID, siteID, input.Capability, deliveryMode, ttlSeconds, reason, normalizedTargets)
	existingView, found, err := batchStore.GetCommandBatchByIdempotencyKey(ctx, operator.TenantID, siteID, idemKey, requestHash)
	if err != nil {
		return CommandBatchView{}, controlErrorFromStore(err, "Command Service could not verify batch idempotency.")
	}
	if found {
		existingView.Replayed = true
		return existingView, nil
	}

	siteAuth, err := a.iam.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return CommandBatchView{}, err
	}
	if !siteAuth.Allowed || !containsString(siteAuth.AllowedSiteIDs, siteID) {
		return CommandBatchView{}, notFoundControlError("The requested Site was not found.")
	}

	model, err := a.twin.GetSiteAssetModel(ctx, operator, siteID)
	if err != nil {
		return CommandBatchView{}, err
	}
	controlState, err := a.twin.GetSiteControlState(ctx, operator, siteID)
	if err != nil {
		return CommandBatchView{}, err
	}
	locks, mw := toGateLocksAndWindow(controlState.AssetLocks, controlState.ActiveMaintenanceWindow)

	type preparedBatchTarget struct {
		itemIndex       int
		deviceID        string
		pointID         string
		resolved        resolvedCommandPointTarget
		params          commandmodel.CommandParameters
		targetObs       *commandmodel.LivePointObservation
		admitted        bool
		rejectionCode   string
		rejectionReason string
	}

	prepared := make([]preparedBatchTarget, len(normalizedTargets))
	anyStepUpRequired := false
	authSuccessCount := 0
	authDeniedCount := 0
	initiatedPrincipal := strings.TrimSpace(siteAuth.PrincipalID)
	if initiatedPrincipal == "" {
		initiatedPrincipal = strings.TrimSpace(operator.Subject)
	}

	for i, targetInput := range normalizedTargets {
		entry := preparedBatchTarget{
			itemIndex: i,
			deviceID:  targetInput.DeviceID,
			pointID:   targetInput.PointID,
			params:    targetInput.Parameters,
		}
		resolved, ctrlErr := resolveBatchPointTarget(model, targetInput.DeviceID, targetInput.PointID, input.Capability, profile, now)
		if ctrlErr != nil {
			entry.rejectionCode = ctrlErr.Code
			entry.rejectionReason = ctrlErr.Detail
			prepared[i] = entry
			continue
		}
		entry.resolved = resolved

		_, claims, authErr := a.iam.AuthorizeCommand(
			ctx, operator, commandmodel.AuthorizationCommandSubmit,
			siteID, resolved.device.ID, resolved.capability, resolved.profile.Revision,
		)
		if authErr != nil {
			var ce *ControlError
			if errors.As(authErr, &ce) && ce != nil && ce.HTTPStatus == http.StatusForbidden {
				authDeniedCount++
				entry.rejectionCode = ce.Code
				entry.rejectionReason = ce.Detail
				prepared[i] = entry
				continue
			}
			return CommandBatchView{}, authErr
		}
		authSuccessCount++
		if claims.PrincipalID != "" {
			initiatedPrincipal = claims.PrincipalID
		}

		targetObs, _, liveErr := a.live.ReadLivePoint(ctx, operator.TenantID, siteID, resolved.device.ID, resolved.feedbackPoint.PointCode)
		if liveErr != nil {
			if deliveryMode != commandmodel.DeliveryModeQueueIfOffline {
				var ce *ControlError
				if errors.As(liveErr, &ce) && ce != nil {
					entry.rejectionCode = ce.Code
					entry.rejectionReason = ce.Detail
					prepared[i] = entry
					continue
				}
				return CommandBatchView{}, liveErr
			}
			targetObs = nil
		}
		entry.targetObs = targetObs

		peerObs, peerErr := a.readGateInterlockPeerStates(ctx, operator.TenantID, siteID, resolved.interlocks)
		if peerErr != nil {
			return CommandBatchView{}, peerErr
		}

		_, hardProtected := edgecontrol.ClassifyHardRealtimeProtection(resolved.point.PointCode, string(resolved.capability), resolved.point.SourceMetadata)
		decision, rejection := commandmodel.EvaluateControlGate(commandmodel.GateInput{
			Phase:                   commandmodel.GatePhaseSubmit,
			AssetID:                 resolved.asset.ID,
			DeviceID:                resolved.device.ID,
			Capability:              resolved.capability,
			CapabilityRevision:      resolved.profile.Revision,
			ParameterKey:            resolved.profile.ParameterKey,
			ActuationMode:           resolved.actuationMode,
			AllowEmergencyDoRelease: false,
			Parameters:              targetInput.Parameters,
			SiteMode:                commandmodel.SiteControlMode(controlState.ControlMode),
			AssetLocks:              locks,
			ActiveMaintenanceWindow: mw,
			IdempotencyKey:          buildBatchItemIdempotencyKey(idemKey, targetInput.DeviceID, targetInput.PointID, i),
			PriorityTier:            commandmodel.PriorityManualOverride,
			CommandSource:           commandmodel.SourceOperator,
			DeliveryMode:            deliveryMode,
			TTLSeconds:              ttlSeconds,
			ExpiresAt:               expiresAt,
			Now:                     now,
			PreconditionMode:        commandmodel.PreconditionRequireCurrentState,
			HardRealtimeProtected:   hardProtected,
			TargetState:             targetObs,
			Interlocks:              resolved.interlocks,
			PeerStates:              peerObs,
			StepUpVerified:          operator.StepUpTOTPVerified,
		})
		if rejection != nil {
			entry.rejectionCode = rejection.Code
			entry.rejectionReason = rejection.Detail
			prepared[i] = entry
			continue
		}
		if guardErr := a.enforcePlantOperationalGuards(ctx, operator, model, resolved, targetObs); guardErr != nil {
			var ce *ControlError
			if errors.As(guardErr, &ce) && ce != nil {
				entry.rejectionCode = ce.Code
				entry.rejectionReason = ce.Detail
				prepared[i] = entry
				continue
			}
			return CommandBatchView{}, guardErr
		}
		if decision.StepUpRequired {
			anyStepUpRequired = true
		}
		entry.admitted = true
		prepared[i] = entry
	}

	if authSuccessCount == 0 && authDeniedCount > 0 {
		return CommandBatchView{}, &ControlError{
			HTTPStatus: http.StatusForbidden,
			Code:       "COMMAND_AUTHORIZATION_DENIED",
			Title:      "Command forbidden",
			Detail:     "The authenticated Principal is not allowed to submit Commands for the batch targets.",
		}
	}
	if anyStepUpRequired {
		return CommandBatchView{}, commandStepUpError("High-risk batch commands require a 6-digit verificationCode from your authenticator app.")
	}

	batchID, err := newUUID(now)
	if err != nil {
		return CommandBatchView{}, unavailableControlError("Unable to allocate Command batch identifier.")
	}
	traceID := commandmodel.NormalizeTraceID(input.TraceID)
	if traceID == "" {
		traceID = commandmodel.NormalizeTraceID(observability.TraceID(ctx))
	}

	createdView, replayed, err := batchStore.CreateCommandBatch(ctx, CommandBatchRecord{
		ID:                     batchID,
		TenantID:               operator.TenantID,
		SiteID:                 siteID,
		IdempotencyKey:         idemKey,
		RequestHash:            requestHash,
		Capability:             input.Capability,
		CommandSource:          commandmodel.SourceOperator,
		DeliveryMode:           deliveryMode,
		TTLSeconds:             cloneIntPtr(ttlSeconds),
		ExpiresAt:              cloneTimePtr(expiresAt),
		TargetCount:            len(normalizedTargets),
		Status:                 "PENDING",
		InitiatedByPrincipalID: initiatedPrincipal,
		TraceID:                traceID,
		Reason:                 reason,
		CreatedAt:              now,
		UpdatedAt:              now,
	})
	if err != nil {
		return CommandBatchView{}, controlErrorFromStore(err, "Command Service could not persist Command batch.")
	}
	if replayed {
		createdView.Replayed = true
		return createdView, nil
	}

	itemRecords := make([]CommandBatchItemRecord, len(prepared))
	acceptedCount := 0
	rejectedCount := 0
	for i, item := range prepared {
		rec := CommandBatchItemRecord{
			BatchID:         batchID,
			ItemIndex:       i,
			TenantID:        operator.TenantID,
			SiteID:          siteID,
			DeviceID:        item.deviceID,
			PointID:         item.pointID,
			Admitted:        false,
			RejectionCode:   item.rejectionCode,
			RejectionReason: item.rejectionReason,
			Status:          "REJECTED",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if !item.admitted {
			rejectedCount++
			itemRecords[i] = rec
			continue
		}

		idx := i
		childKey := buildBatchItemIdempotencyKey(idemKey, item.deviceID, item.pointID, i)
		submitted, submitErr := a.mintGrantAndSubmit(
			ctx, operator, siteID, item.resolved.device.ID, item.resolved.point.ID,
			childKey, traceID, item.resolved.capability, item.resolved.profile,
			item.params, item.resolved.feedbackPoint.PointCode,
			commandmodel.PreconditionRequireCurrentState, "", "", "",
			deliveryMode, ttlSeconds, expiresAt, &batchID, &idx, item.targetObs,
		)
		if submitErr != nil {
			rejectedCount++
			var ce *ControlError
			if errors.As(submitErr, &ce) && ce != nil {
				rec.RejectionCode = ce.Code
				rec.RejectionReason = ce.Detail
			} else {
				rec.RejectionCode = "COMMAND_SUBMIT_FAILED"
				rec.RejectionReason = submitErr.Error()
			}
			itemRecords[i] = rec
			continue
		}
		if !submitted.Replayed {
			if locker, hasLocker := a.store.(DevicePriorityLocker); hasLocker {
				lockedBy := strings.TrimSpace(operator.Subject)
				if lockedBy == "" {
					lockedBy = submitted.Intent.PrincipalID
				}
				if _, lockErr := locker.AcquireDevicePriorityLock(ctx, commandmodel.DevicePriorityLock{
					TenantID:       operator.TenantID,
					SiteID:         siteID,
					DeviceID:       item.resolved.device.ID,
					LockedPriority: commandmodel.PriorityManualOverride,
					CommandSource:  commandmodel.SourceOperator,
					LockedBy:       lockedBy,
					LockedUntil:    a.now().UTC().Add(2 * time.Hour),
					Reason:         "operator batch command cooldown",
				}); lockErr != nil && !errors.Is(lockErr, ErrDevicePriorityLocked) {
					slog.WarnContext(ctx, "operator batch manual cooldown lock skipped",
						"tenant_id", operator.TenantID,
						"device_id", item.resolved.device.ID,
						"error", "[REDACTED]",
					)
				}
			}
		}
		cmdID := submitted.Intent.ID
		rec.CommandID = &cmdID
		rec.Admitted = true
		rec.Status = string(submitted.Intent.Status)
		acceptedCount++
		itemRecords[i] = rec
	}

	viewItems := make([]CommandBatchItemView, len(itemRecords))
	for i, rec := range itemRecords {
		viewItems[i] = commandBatchItemViewFromRecord(rec, nil, nil)
	}
	_, _, finalStatus := aggregateBatchStatus(viewItems, acceptedCount, rejectedCount)
	finalView, err := batchStore.FinalizeCommandBatch(ctx, operator.TenantID, batchID, itemRecords, acceptedCount, rejectedCount, finalStatus)
	if err != nil {
		return CommandBatchView{}, controlErrorFromStore(err, "Command Service could not finalize Command batch.")
	}
	return finalView, nil
}

func (a *ControlAuthority) GetOperatorCommandBatch(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	batchID string,
) (CommandBatchView, error) {
	if a.iam == nil {
		return CommandBatchView{}, unavailableControlError("IAM authorization is not configured.")
	}
	batchStore, ok := a.store.(CommandBatchStore)
	if !ok {
		return CommandBatchView{}, unavailableControlError("Command Service batch storage is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) || !lowerUUIDPattern.MatchString(batchID) {
		return CommandBatchView{}, notFoundControlError("The requested Command batch was not found.")
	}
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return CommandBatchView{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return CommandBatchView{}, notFoundControlError("The requested Site was not found.")
	}
	view, err := batchStore.GetCommandBatch(ctx, operator.TenantID, siteID, batchID)
	if err != nil {
		return CommandBatchView{}, controlErrorFromStore(err, "Command Service could not load the requested Command batch.")
	}
	return view, nil
}

func (a *ControlAuthority) ListOperatorCommandBatches(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	limit int,
) (CommandBatchListView, error) {
	if a.iam == nil {
		return CommandBatchListView{}, unavailableControlError("IAM authorization is not configured.")
	}
	batchStore, ok := a.store.(CommandBatchStore)
	if !ok {
		return CommandBatchListView{}, unavailableControlError("Command Service batch storage is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) || limit < 1 || limit > MaximumListedCommands {
		return CommandBatchListView{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_LIST_REQUEST_INVALID",
			Title: "Command batch list request invalid", Detail: "siteId must be a Site UUID and limit must be between 1 and 100.",
		}
	}
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return CommandBatchListView{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return CommandBatchListView{}, notFoundControlError("The requested Site was not found.")
	}
	items, err := batchStore.ListCommandBatches(ctx, operator.TenantID, siteID, limit)
	if err != nil {
		return CommandBatchListView{}, controlErrorFromStore(err, "Command Service could not list Command batches.")
	}
	return CommandBatchListView{Items: items}, nil
}

func (a *ControlAuthority) CancelOperatorCommandBatch(
	ctx context.Context,
	operator OperatorIdentity,
	siteID string,
	batchID string,
	reason string,
) (CommandBatchCancelResponse, error) {
	if a.iam == nil {
		return CommandBatchCancelResponse{}, unavailableControlError("IAM authorization is not configured.")
	}
	batchStore, ok := a.store.(CommandBatchStore)
	if !ok {
		return CommandBatchCancelResponse{}, unavailableControlError("Command Service batch storage is not configured.")
	}
	if !commandmodel.IsUUIDv7(siteID) || !lowerUUIDPattern.MatchString(batchID) {
		return CommandBatchCancelResponse{}, notFoundControlError("The requested Command batch was not found.")
	}
	decision, err := a.iam.AuthorizeRegistry(ctx, operator, ActionSiteRead)
	if err != nil {
		return CommandBatchCancelResponse{}, err
	}
	if !decision.Allowed || !containsString(decision.AllowedSiteIDs, siteID) {
		return CommandBatchCancelResponse{}, notFoundControlError("The requested Site was not found.")
	}
	batchView, err := batchStore.GetCommandBatch(ctx, operator.TenantID, siteID, batchID)
	if err != nil {
		return CommandBatchCancelResponse{}, controlErrorFromStore(err, "Command Service could not load the requested Command batch.")
	}

	cancellableIDs := make([]string, 0)
	for _, item := range batchView.Items {
		if !item.Admitted || item.CommandID == nil {
			continue
		}
		if item.Status == string(commandmodel.IntentAwaitingApproval) || item.Status == string(commandmodel.IntentQueued) {
			cancellableIDs = append(cancellableIDs, *item.CommandID)
		}
	}
	if len(cancellableIDs) == 0 {
		if batchView.Status == string(commandmodel.IntentCancelled) {
			return CommandBatchCancelResponse{
				Batch:               batchView,
				CancelledCommandIDs: []string{},
			}, nil
		}
		return CommandBatchCancelResponse{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "COMMAND_CANCEL_INVALID",
			Title: "Command batch cannot be cancelled", Detail: "No queued or awaiting-approval Commands remain in this batch.",
		}
	}

	cancelledIDs := make([]string, 0, len(cancellableIDs))
	for _, cmdID := range cancellableIDs {
		if _, cancelErr := a.CancelOperatorCommand(ctx, operator, cmdID, reason); cancelErr != nil {
			var ce *ControlError
			if errors.As(cancelErr, &ce) && ce != nil && ce.HTTPStatus == http.StatusForbidden {
				return CommandBatchCancelResponse{}, cancelErr
			}
			continue
		}
		cancelledIDs = append(cancelledIDs, cmdID)
	}
	if len(cancelledIDs) > 0 {
		if updateErr := batchStore.UpdateCommandBatchItemsStatus(ctx, operator.TenantID, batchID, cancelledIDs, string(commandmodel.IntentCancelled)); updateErr != nil {
			return CommandBatchCancelResponse{}, controlErrorFromStore(updateErr, "Command Service could not update cancelled batch items.")
		}
	}
	refreshed, err := batchStore.GetCommandBatch(ctx, operator.TenantID, siteID, batchID)
	if err != nil {
		return CommandBatchCancelResponse{}, controlErrorFromStore(err, "Command Service could not reload the Command batch.")
	}
	return CommandBatchCancelResponse{
		Batch:               refreshed,
		CancelledCommandIDs: cancelledIDs,
	}, nil
}

func resolveBatchPointTarget(
	model SiteAssetModelDTO,
	deviceID string,
	pointID string,
	expectedCapability commandmodel.Capability,
	expectedProfile commandmodel.CapabilityProfile,
	now time.Time,
) (resolvedCommandPointTarget, *ControlError) {
	var device DeviceDTO
	var deviceFound bool
	for _, item := range model.Devices {
		if item.ID == deviceID && item.TenantID == model.TenantID && item.SiteID == model.SiteID && strings.EqualFold(item.Status, "ACTIVE") {
			device = item
			deviceFound = true
			break
		}
	}
	if !deviceFound {
		return resolvedCommandPointTarget{}, notFoundControlError("The requested Device was not found on this Site.")
	}

	var point TelemetryPointDTO
	var pointFound bool
	for _, item := range model.TelemetryPoints {
		if item.ID == pointID && item.TenantID == model.TenantID && item.SiteID == model.SiteID &&
			item.ReportingDeviceID == device.ID && strings.EqualFold(item.Status, "ACTIVE") &&
			item.PointType == "COMMAND" && item.Writable {
			point = item
			pointFound = true
			break
		}
	}
	if !pointFound {
		return resolvedCommandPointTarget{}, notFoundControlError("The requested Command Point was not found for the target Device.")
	}

	meta, metaErr := commandmodel.ParseCommandPointMetadata(point.SourceMetadata)
	if metaErr != nil {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Command point capability metadata is invalid.",
		}
	}
	if meta.Capability != expectedCapability || meta.CapabilityRevision != expectedProfile.Revision || meta.FeedbackPointKey == "" {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "CAPABILITY_MISMATCH",
			Title: "Capability mismatch", Detail: fmt.Sprintf("Command point supports %s, not %s.", meta.Capability, expectedCapability),
		}
	}

	var asset AssetDTO
	var assetFound bool
	for _, rel := range model.Relationships {
		if !commandRelationshipCurrent(rel, now) {
			continue
		}
		if rel.FromType == "POINT" && rel.FromID == point.ID && rel.Role == "CONTROLS" {
			if rel.ToType == "ASSET" {
				for _, candidate := range model.Assets {
					if candidate.ID == rel.ToID && candidate.TenantID == model.TenantID && candidate.SiteID == model.SiteID && strings.EqualFold(candidate.Status, "ACTIVE") {
						asset = candidate
						assetFound = true
						break
					}
				}
			} else if rel.ToType == "SITE" && rel.ToID == model.SiteID {
				asset = AssetDTO{TenantID: model.TenantID, SiteID: model.SiteID, Status: "ACTIVE"}
				assetFound = true
			}
			if assetFound {
				break
			}
		}
	}
	if !assetFound {
		return resolvedCommandPointTarget{}, notFoundControlError("The Command Point is not bound to an active Asset on this Site.")
	}

	var feedbackPoint TelemetryPointDTO
	var feedbackFound bool
	for _, candidate := range model.TelemetryPoints {
		if candidate.ReportingDeviceID == device.ID && candidate.PointCode == meta.FeedbackPointKey && strings.EqualFold(candidate.Status, "ACTIVE") {
			feedbackPoint = candidate
			feedbackFound = true
			break
		}
	}
	if !feedbackFound {
		return resolvedCommandPointTarget{}, unavailableControlError("The Command feedback Point is unavailable.")
	}

	interlocks, interlockErr := validatePointInterlockChecks(meta.InterlockChecks, model)
	if interlockErr != nil {
		return resolvedCommandPointTarget{}, &ControlError{
			HTTPStatus: http.StatusBadRequest, Code: "COMMAND_REQUEST_INVALID",
			Title: "Command request invalid", Detail: "Command point interlock configuration is invalid.",
		}
	}

	return resolvedCommandPointTarget{
		asset:         asset,
		device:        device,
		point:         point,
		feedbackPoint: feedbackPoint,
		capability:    expectedCapability,
		profile:       expectedProfile,
		actuationMode: meta.ActuationMode,
		interlocks:    interlocks,
	}, nil
}

func buildBatchItemIdempotencyKey(batchIdempotencyKey, deviceID, pointID string, itemIndex int) string {
	digest := sha256.Sum256([]byte(batchIdempotencyKey + "|" + deviceID + "|" + pointID + "|" + strconv.Itoa(itemIndex)))
	return fmt.Sprintf("batch:%s:%d", hex.EncodeToString(digest[:12]), itemIndex)
}

func hashCommandBatchRequest(
	tenantID, siteID string,
	capability commandmodel.Capability,
	deliveryMode commandmodel.DeliveryMode,
	ttlSeconds *int,
	reason string,
	targets []CommandBatchTargetInput,
) string {
	profile, _ := commandmodel.CapabilityProfileFor(capability)
	ttlText := ""
	if ttlSeconds != nil {
		ttlText = strconv.Itoa(*ttlSeconds)
	}
	parts := []string{
		tenantID,
		siteID,
		string(capability),
		string(deliveryMode),
		ttlText,
		reason,
	}
	for _, t := range targets {
		paramText := ""
		if profile.ParameterKey != "" {
			if val, ok := commandmodel.ParameterValue(capability, t.Parameters); ok {
				paramText = profile.ParameterKey + "=" + strconv.FormatFloat(val, 'f', 3, 64)
			}
		} else if len(t.Parameters) > 0 {
			raw, _ := json.Marshal(t.Parameters)
			paramText = string(raw)
		}
		parts = append(parts, t.DeviceID+":"+t.PointID+":"+paramText)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func aggregateBatchStatus(items []CommandBatchItemView, acceptedCount, rejectedCount int) (int, int, string) {
	if len(items) == 0 {
		return acceptedCount, rejectedCount, "PENDING"
	}
	var activeCount, succeededCount, failedCount, cancelledCount, expiredCount int
	for _, item := range items {
		switch item.Status {
		case string(commandmodel.IntentSubmitted),
			string(commandmodel.IntentValidating),
			string(commandmodel.IntentAwaitingApproval),
			string(commandmodel.IntentApproved),
			string(commandmodel.IntentQueued),
			string(commandmodel.IntentDispatching),
			string(commandmodel.IntentOutcomeUnknown),
			"PENDING":
			activeCount++
		case string(commandmodel.IntentSucceeded):
			succeededCount++
		case string(commandmodel.IntentCancelled):
			cancelledCount++
		case string(commandmodel.IntentExpired):
			expiredCount++
		default:
			failedCount++
		}
	}
	switch {
	case activeCount > 0:
		return acceptedCount, rejectedCount, "PENDING"
	case succeededCount == len(items):
		return acceptedCount, rejectedCount, "SUCCEEDED"
	case succeededCount > 0:
		return acceptedCount, rejectedCount, "PARTIAL"
	case expiredCount > 0 && failedCount == 0 && cancelledCount == 0:
		return acceptedCount, rejectedCount, "EXPIRED"
	case cancelledCount > 0 && failedCount == 0 && expiredCount == 0:
		return acceptedCount, rejectedCount, "CANCELLED"
	default:
		return acceptedCount, rejectedCount, "FAILED"
	}
}

func commandBatchItemViewFromRecord(rec CommandBatchItemRecord, expiresAt, nextDispatchAfter *time.Time) CommandBatchItemView {
	var rejCode, rejReason *string
	if strings.TrimSpace(rec.RejectionCode) != "" {
		v := strings.TrimSpace(rec.RejectionCode)
		rejCode = &v
	}
	if strings.TrimSpace(rec.RejectionReason) != "" {
		v := strings.TrimSpace(rec.RejectionReason)
		rejReason = &v
	}
	return CommandBatchItemView{
		ItemIndex:         rec.ItemIndex,
		DeviceID:          rec.DeviceID,
		PointID:           rec.PointID,
		CommandID:         cloneStringPtr(rec.CommandID),
		Admitted:          rec.Admitted,
		Status:            rec.Status,
		RejectionCode:     rejCode,
		RejectionReason:   rejReason,
		ExpiresAt:         formatTimePtr(expiresAt),
		NextDispatchAfter: formatTimePtr(nextDispatchAfter),
	}
}

// In-memory Service CommandBatchStore implementation for fast unit testing.

func (s *Service) GetCommandBatchByIdempotencyKey(_ context.Context, tenantID, siteID, key, requestHash string) (CommandBatchView, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := strings.Join([]string{tenantID, siteID, key}, "\x00")
	batchID, ok := s.batchIdemp[scope]
	if !ok {
		return CommandBatchView{}, false, nil
	}
	rec := s.batches[batchID]
	if rec == nil {
		return CommandBatchView{}, false, nil
	}
	if rec.RequestHash != requestHash {
		return CommandBatchView{}, false, ErrCommandBatchConflict
	}
	return s.buildBatchViewLocked(rec), true, nil
}

func (s *Service) CreateCommandBatch(_ context.Context, record CommandBatchRecord) (CommandBatchView, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := strings.Join([]string{record.TenantID, record.SiteID, record.IdempotencyKey}, "\x00")
	if existingID, ok := s.batchIdemp[scope]; ok {
		existing := s.batches[existingID]
		if existing != nil {
			if existing.RequestHash != record.RequestHash {
				return CommandBatchView{}, false, ErrCommandBatchConflict
			}
			return s.buildBatchViewLocked(existing), true, nil
		}
	}
	if strings.TrimSpace(record.ID) == "" {
		s.nextBatchID++
		record.ID = fmt.Sprintf("018f3f00-0000-7000-8000-%012d", s.nextBatchID)
	}
	copied := record
	copied.TTLSeconds = cloneIntPtr(record.TTLSeconds)
	copied.ExpiresAt = cloneTimePtr(record.ExpiresAt)
	copied.Items = append([]CommandBatchItemRecord(nil), record.Items...)
	s.batches[copied.ID] = &copied
	s.batchIdemp[scope] = copied.ID
	return s.buildBatchViewLocked(&copied), false, nil
}

func (s *Service) FinalizeCommandBatch(_ context.Context, tenantID, batchID string, items []CommandBatchItemRecord, acceptedCount, rejectedCount int, status string) (CommandBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.batches[batchID]
	if !ok || rec.TenantID != tenantID {
		return CommandBatchView{}, ErrCommandBatchNotFound
	}
	rec.Items = append([]CommandBatchItemRecord(nil), items...)
	rec.AcceptedCount = acceptedCount
	rec.RejectedCount = rejectedCount
	rec.Status = status
	rec.UpdatedAt = s.now().UTC()
	return s.buildBatchViewLocked(rec), nil
}

func (s *Service) GetCommandBatch(_ context.Context, tenantID, siteID, batchID string) (CommandBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.batches[batchID]
	if !ok || rec.TenantID != tenantID || rec.SiteID != siteID {
		return CommandBatchView{}, ErrCommandBatchNotFound
	}
	return s.buildBatchViewLocked(rec), nil
}

func (s *Service) ListCommandBatches(_ context.Context, tenantID, siteID string, limit int) ([]CommandBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]*CommandBatchRecord, 0)
	for _, rec := range s.batches {
		if rec.TenantID == tenantID && rec.SiteID == siteID {
			matched = append(matched, rec)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	out := make([]CommandBatchView, 0, len(matched))
	for _, rec := range matched {
		out = append(out, s.buildBatchViewLocked(rec))
	}
	return out, nil
}

func (s *Service) UpdateCommandBatchItemsStatus(_ context.Context, tenantID, batchID string, commandIDs []string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.batches[batchID]
	if !ok || rec.TenantID != tenantID {
		return ErrCommandBatchNotFound
	}
	wanted := make(map[string]struct{}, len(commandIDs))
	for _, id := range commandIDs {
		wanted[id] = struct{}{}
	}
	now := s.now().UTC()
	for i := range rec.Items {
		if rec.Items[i].CommandID != nil {
			if _, hit := wanted[*rec.Items[i].CommandID]; hit {
				rec.Items[i].Status = status
				rec.Items[i].UpdatedAt = now
			}
		}
	}
	rec.UpdatedAt = now
	return nil
}

func (s *Service) buildBatchViewLocked(rec *CommandBatchRecord) CommandBatchView {
	items := make([]CommandBatchItemView, len(rec.Items))
	for i, item := range rec.Items {
		resolved := item
		var expAt, nextAt *time.Time
		if item.CommandID != nil {
			if intent, ok := s.intents[*item.CommandID]; ok {
				resolved.Status = string(intent.Status)
				expAt = intent.ExpiresAt
				nextAt = intent.NextDispatchAfter
			}
		}
		items[i] = commandBatchItemViewFromRecord(resolved, expAt, nextAt)
	}
	accepted, rejected, status := aggregateBatchStatus(items, rec.AcceptedCount, rec.RejectedCount)
	if len(items) == 0 && rec.Status != "" {
		status = rec.Status
	}
	return CommandBatchView{
		ID:                     rec.ID,
		TenantID:               rec.TenantID,
		SiteID:                 rec.SiteID,
		IdempotencyKey:         rec.IdempotencyKey,
		Capability:             rec.Capability,
		CommandSource:          rec.CommandSource,
		DeliveryMode:           commandmodel.NormalizeDeliveryMode(rec.DeliveryMode),
		TTLSeconds:             cloneIntPtr(rec.TTLSeconds),
		ExpiresAt:              formatTimePtr(rec.ExpiresAt),
		TargetCount:            rec.TargetCount,
		AcceptedCount:          accepted,
		RejectedCount:          rejected,
		Status:                 status,
		InitiatedByPrincipalID: rec.InitiatedByPrincipalID,
		TraceID:                rec.TraceID,
		Reason:                 rec.Reason,
		CreatedAt:              rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Items:                  items,
	}
}
