package commandservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

const (
	ActionSiteRead       = "site.read"
	ActionAssetRead      = "asset.read"
	ActionAssetModelRead = "asset-model.read"
	ActionScheduleManage = "schedule.manage"

	StepUpActionTOTP             = "step-up:totp-code"
	StepUpActionSessionMFA       = "step-up:session-mfa"
	OperatorControlContextAction = "command:operator"

	SiteControlModeRemoteAuto       = "REMOTE_AUTO"
	SiteControlModeManualOverride   = "MANUAL_OVERRIDE"
	SiteControlModeSafeShutdownExit = "SAFE_SHUTDOWN_EXIT"

	AssetControlLockUnlocked    = "UNLOCKED"
	AssetControlLockManual      = "MANUAL_LOCK"
	AssetControlLockMaintenance = "MAINTENANCE_LOCK"

	ShutdownModePlantSafe          = "PLANT_SAFE_SHUTDOWN"
	ShutdownModeEmergencyDoRelease = "EMERGENCY_DO_RELEASE"
	ShutdownModeAbortUnitSequence  = "ABORT_UNIT_SEQUENCE"
)

// ControlError carries an explicit public HTTP ProblemDetails mapping across the control authority seam.
type ControlError struct {
	HTTPStatus int    `json:"status"`
	Code       string `json:"code"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Retryable  bool   `json:"retryable"`
}

func (e *ControlError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s (%d): %s", e.Code, e.HTTPStatus, e.Detail)
}

func problemFromGateRejection(rej *commandmodel.GateRejection) *ControlError {
	if rej == nil {
		return nil
	}
	return &ControlError{
		HTTPStatus: rej.HTTPStatus,
		Code:       rej.Code,
		Title:      rej.Title,
		Detail:     rej.Detail,
		Retryable:  rej.Retryable,
	}
}

func controlErrorFromStore(err error, defaultDetail string) error {
	var ce *ControlError
	if errors.As(err, &ce) {
		return ce
	}
	switch {
	case errors.Is(err, ErrCommandNotFound):
		return &ControlError{
			HTTPStatus: http.StatusNotFound,
			Code:       "RESOURCE_NOT_FOUND",
			Title:      "Resource not found",
			Detail:     "The requested Command was not found.",
		}
	case errors.Is(err, ErrAuthorizationDenied):
		return &ControlError{
			HTTPStatus: http.StatusForbidden,
			Code:       "COMMAND_AUTHORIZATION_DENIED",
			Title:      "Command forbidden",
			Detail:     "The authenticated Principal is not allowed to perform this Command operation.",
		}
	case errors.Is(err, ErrReconciliationInvalid):
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_RECONCILIATION_INVALID",
			Title:      "Command reconciliation invalid",
			Detail:     "Only OUTCOME_UNKNOWN Commands can be manually reconciled.",
		}
	case errors.Is(err, ErrCancelInvalid):
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_CANCEL_INVALID",
			Title:      "Command cannot be cancelled",
			Detail:     "Only a pending or queued Command can be cancelled before dispatch begins.",
		}
	case errors.Is(err, ErrApprovalInvalid), errors.Is(err, ErrApprovalRequired):
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_APPROVAL_INVALID",
			Title:      "Command approval invalid",
			Detail:     "The Command is not awaiting another approval.",
		}
	case errors.Is(err, ErrIdempotencyConflict):
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_IDEMPOTENCY_CONFLICT",
			Title:      "Idempotency conflict",
			Detail:     "Idempotency-Key was already used for a different Command payload.",
		}
	case errors.Is(err, ErrCurrentStateUnsafe):
		return &ControlError{
			HTTPStatus: http.StatusConflict,
			Code:       "COMMAND_CURRENT_STATE_UNSAFE",
			Title:      "Current state unsafe",
			Detail:     "The current Device state does not satisfy the control preconditions.",
		}
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrCapabilityDenied):
		detail := defaultDetail
		if detail == "" {
			detail = "The Command request is invalid."
		}
		return &ControlError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "COMMAND_REQUEST_INVALID",
			Title:      "Command request invalid",
			Detail:     detail,
		}
	default:
		detail := defaultDetail
		if detail == "" {
			detail = "Command Service is temporarily unavailable."
		}
		return &ControlError{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       "COMMAND_UNAVAILABLE",
			Title:      "Command unavailable",
			Detail:     detail,
			Retryable:  true,
		}
	}
}

type OperatorIdentity struct {
	TenantID                 string
	Subject                  string
	SubjectIssuer            string
	DisplayName              string
	Email                    string
	Roles                    []string
	SessionID                string
	TokenID                  string
	PolicyRevision           string
	IssuedAt                 time.Time
	ExpiresAt                time.Time
	StepUpTOTPVerified       bool
	StepUpSessionMFAVerified bool
}

type AssetDTO struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	AssetType   string `json:"assetType"`
	Status      string `json:"status"`
}

type DeviceDTO struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	SiteID      string `json:"siteId"`
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	DeviceType  string `json:"deviceType"`
	Status      string `json:"status"`
}

type TelemetryPointDTO struct {
	ID                string         `json:"id"`
	TenantID          string         `json:"tenantId"`
	SiteID            string         `json:"siteId"`
	ReportingDeviceID string         `json:"reportingDeviceId"`
	PointCode         string         `json:"pointCode"`
	SourceKey         string         `json:"sourceKey"`
	DisplayName       string         `json:"displayName"`
	PointType         string         `json:"pointType"`
	ValueType         string         `json:"valueType"`
	Writable          bool           `json:"writable"`
	SourceMetadata    map[string]any `json:"sourceMetadata"`
	Status            string         `json:"status"`
}

type AssetRelationshipDTO struct {
	ID        string  `json:"id"`
	TenantID  string  `json:"tenantId"`
	SiteID    string  `json:"siteId"`
	FromType  string  `json:"fromType"`
	FromID    string  `json:"fromId"`
	ToType    string  `json:"toType"`
	ToID      string  `json:"toId"`
	Role      string  `json:"role"`
	Status    string  `json:"status"`
	ValidFrom string  `json:"validFrom"`
	ValidTo   *string `json:"validTo"`
}

type SiteAssetModelDTO struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	TenantID        string                 `json:"tenantId"`
	SiteID          string                 `json:"siteId"`
	Assets          []AssetDTO             `json:"assets"`
	Devices         []DeviceDTO            `json:"devices"`
	TelemetryPoints []TelemetryPointDTO    `json:"telemetryPoints"`
	Relationships   []AssetRelationshipDTO `json:"relationships"`
}

type AssetControlLockView struct {
	ID             string   `json:"id,omitempty"`
	TenantID       string   `json:"tenantId,omitempty"`
	SiteID         string   `json:"siteId"`
	AssetID        string   `json:"assetId"`
	LockMode       string   `json:"lockMode"`
	Reason         string   `json:"reason"`
	UpdatedBy      *string  `json:"updatedBy"`
	UpdatedAt      *string  `json:"updatedAt"`
	Revision       int64    `json:"revision"`
	BoundDeviceIDs []string `json:"boundDeviceIds,omitempty"`
}

type ActiveMaintenanceWindowView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Reason    string `json:"reason"`
}

type SiteControlStateView struct {
	ID                      *string                      `json:"id,omitempty"`
	TenantID                string                       `json:"tenantId"`
	SiteID                  string                       `json:"siteId"`
	ControlMode             string                       `json:"controlMode"`
	Reason                  string                       `json:"reason"`
	UpdatedBy               *string                      `json:"updatedBy"`
	UpdatedAt               *string                      `json:"updatedAt"`
	Revision                int64                        `json:"revision"`
	AssetLocks              []AssetControlLockView       `json:"assetLocks"`
	ActiveMaintenanceWindow *ActiveMaintenanceWindowView `json:"activeMaintenanceWindow,omitempty"`
}

type ControlMutationMeta struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	IdempotencyKey   string `json:"idempotencyKey"`
	Reason           string `json:"reason"`
}

type OperatingScheduleSlotDTO struct {
	DayOfWeek   int `json:"dayOfWeek"`
	StartMinute int `json:"startMinute"`
	EndMinute   int `json:"endMinute"`
}

type OperatingScheduleDTO struct {
	Timezone string                     `json:"timezone"`
	Slots    []OperatingScheduleSlotDTO `json:"slots"`
}

type OperatingExceptionSlotDTO struct {
	StartMinute int `json:"startMinute"`
	EndMinute   int `json:"endMinute"`
}

type OperatingExceptionDTO struct {
	Kind             string                      `json:"kind"`
	StartDate        string                      `json:"startDate"`
	EndDate          string                      `json:"endDate"`
	Status           string                      `json:"status"`
	ReplacementSlots []OperatingExceptionSlotDTO `json:"replacementSlots"`
}

type SiteControlModeInput struct {
	ControlMode      string               `json:"controlMode"`
	Reason           string               `json:"reason,omitempty"`
	ExpectedRevision *int64               `json:"expectedRevision,omitempty"`
	Meta             *ControlMutationMeta `json:"meta,omitempty"`
	IdempotencyKey   string               `json:"-"`
}

type AssetControlLockInput struct {
	LockMode         string               `json:"lockMode"`
	Reason           string               `json:"reason,omitempty"`
	ExpectedRevision *int64               `json:"expectedRevision,omitempty"`
	Meta             *ControlMutationMeta `json:"meta,omitempty"`
	IdempotencyKey   string               `json:"-"`
}

type SiteControlModeResponse struct {
	SiteControlStateView
	CancelledCommandIDs []string `json:"cancelledCommandIds"`
}

type AssetControlLockResponse struct {
	AssetControlLockView
	CancelledCommandIDs []string `json:"cancelledCommandIds"`
}

type SiteControlShutdownInput struct {
	Mode                      string `json:"mode"`
	Reason                    string `json:"reason"`
	UnitCode                  string `json:"unitCode,omitempty"`
	ConfirmEmergencyDoRelease bool   `json:"confirmEmergencyDoRelease,omitempty"`
	ExpectedRevision          *int64 `json:"expectedRevision,omitempty"`
	IdempotencyKey            string `json:"-"`
}

type SiteControlShutdownResponse struct {
	SiteControlState        SiteControlStateView `json:"siteControlState"`
	Mode                    string               `json:"mode"`
	CancelledCommandIDs     []string             `json:"cancelledCommandIds"`
	SubmittedCommandIDs     []string             `json:"submittedCommandIds"`
	DeferredHoldingAssetIds []string             `json:"deferredHoldingAssetIds"`
}

type ControlDispatchContext struct {
	TenantID                string                        `json:"tenantId"`
	SiteID                  string                        `json:"siteId"`
	DeviceID                string                        `json:"deviceId"`
	PointID                 string                        `json:"pointId"`
	AssetID                 string                        `json:"assetId,omitempty"`
	ControlMode             string                        `json:"controlMode"`
	AssetLocks              []AssetControlLockView        `json:"assetLocks"`
	ActiveMaintenanceWindow *ActiveMaintenanceWindowView  `json:"activeMaintenanceWindow,omitempty"`
	ActuationMode           string                        `json:"actuationMode,omitempty"`
	FeedbackPointKey        string                        `json:"feedbackPointKey"`
	Interlocks              []commandmodel.InterlockCheck `json:"interlocks"`
}

type RegistryDecisionResult struct {
	Allowed        bool
	PrincipalID    string
	AllowedSiteIDs []string
	Grant          string
	ReasonCode     string
}

type SubmitOperatorCommandInput struct {
	AssetID        string                         `json:"assetId"`
	CommandPointID string                         `json:"commandPointId"`
	Parameters     commandmodel.CommandParameters `json:"parameters"`
	IdempotencyKey string                         `json:"idempotencyKey"`
}

type SubmitOperatorCommandOutput struct {
	View     CommandView
	Replayed bool
}

type PlantTwinPort interface {
	GetAsset(ctx context.Context, operator OperatorIdentity, assetID string) (AssetDTO, error)
	GetSiteAssetModel(ctx context.Context, operator OperatorIdentity, siteID string) (SiteAssetModelDTO, error)
	GetSiteControlState(ctx context.Context, operator OperatorIdentity, siteID string) (SiteControlStateView, error)
	GetSiteOperatingSchedule(ctx context.Context, operator OperatorIdentity, siteID string) (OperatingScheduleDTO, []OperatingExceptionDTO, error)
	SaveSiteControlMode(ctx context.Context, operator OperatorIdentity, siteID, controlMode, reason string, meta ControlMutationMeta) (SiteControlStateView, error)
	SaveAssetControlLock(ctx context.Context, operator OperatorIdentity, assetID, siteID, lockMode, reason string, meta ControlMutationMeta) (AssetControlLockView, error)
	GetDispatchContext(ctx context.Context, tenantID, siteID, deviceID, pointID string) (ControlDispatchContext, error)
}

type TelemetryLivePort interface {
	ReadLivePoint(ctx context.Context, tenantID, siteID, deviceID, pointKey string) (*commandmodel.LivePointObservation, string, error)
}

type IAMAuthorizerPort interface {
	AuthorizeRegistry(ctx context.Context, operator OperatorIdentity, action string) (RegistryDecisionResult, error)
	AuthorizeCommand(ctx context.Context, operator OperatorIdentity, purpose commandmodel.AuthorizationPurpose, siteID, deviceID string, capability commandmodel.Capability, capabilityRevision string) (string, commandauth.GrantClaims, error)
}
