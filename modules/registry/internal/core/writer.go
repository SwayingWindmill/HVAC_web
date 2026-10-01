package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

var (
	ErrInvalidMutation      = errors.New("registry mutation is invalid")
	ErrRevisionConflict     = errors.New("registry expected revision conflict")
	ErrIdempotencyConflict  = errors.New("registry idempotency conflict")
	ErrBindingConflict      = errors.New("registry binding conflict")
	ErrTemplateImmutable    = errors.New("released TemplateRevision is immutable")
	ErrImportPlanInvalid    = errors.New("registry import plan is invalid")
	ErrResourceDependencies = errors.New("registry resource has active dependencies")
)

var (
	writeKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}\z`)
	codePattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}\z`)
	pointCodePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}\z`)
	sourceKeyPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}\z`)
	templateKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}\z`)
)

type MutationMeta struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	IdempotencyKey   string `json:"idempotencyKey"`
	Reason           string `json:"reason"`
}

func (meta MutationMeta) validate(create bool) error {
	if !writeKeyPattern.MatchString(meta.IdempotencyKey) || strings.TrimSpace(meta.Reason) == "" || len(meta.Reason) > 1000 {
		return ErrInvalidMutation
	}
	if create {
		if meta.ExpectedRevision != 0 {
			return ErrInvalidMutation
		}
	} else if meta.ExpectedRevision < 1 {
		return ErrInvalidMutation
	}
	return nil
}

type SiteMutation struct {
	ID          string       `json:"id,omitempty"`
	Code        string       `json:"code"`
	DisplayName string       `json:"displayName"`
	Timezone    string       `json:"timezone"`
	Status      string       `json:"status"`
	Meta        MutationMeta `json:"meta"`
}

type SpaceMutation struct {
	ID            string       `json:"id,omitempty"`
	SiteID        string       `json:"siteId"`
	ParentSpaceID *string      `json:"parentSpaceId,omitempty"`
	Code          string       `json:"code"`
	DisplayName   string       `json:"displayName"`
	SpaceType     string       `json:"spaceType"`
	Status        string       `json:"status"`
	Meta          MutationMeta `json:"meta"`
}

type AssetMutation struct {
	ID          string       `json:"id,omitempty"`
	SiteID      string       `json:"siteId"`
	Code        string       `json:"code"`
	DisplayName string       `json:"displayName"`
	AssetType   string       `json:"assetType"`
	Status      string       `json:"status"`
	Meta        MutationMeta `json:"meta"`
}

type DeviceMutation struct {
	ID          string       `json:"id,omitempty"`
	SiteID      string       `json:"siteId"`
	Code        string       `json:"code"`
	DisplayName string       `json:"displayName"`
	DeviceType  string       `json:"deviceType"`
	Status      string       `json:"status"`
	Meta        MutationMeta `json:"meta"`
}

type SensorMutation struct {
	ID               string         `json:"id,omitempty"`
	SiteID           string         `json:"siteId"`
	Code             string         `json:"code"`
	DisplayName      string         `json:"displayName"`
	SensorType       string         `json:"sensorType"`
	Manufacturer     string         `json:"manufacturer,omitempty"`
	Model            string         `json:"model,omitempty"`
	SerialNumber     string         `json:"serialNumber,omitempty"`
	CalibrationDueAt *string        `json:"calibrationDueAt,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	Status           string         `json:"status"`
	Meta             MutationMeta   `json:"meta"`
}

type PointMutation struct {
	ID                     string         `json:"id,omitempty"`
	SiteID                 string         `json:"siteId"`
	ReportingDeviceID      string         `json:"reportingDeviceId"`
	SensorID               *string        `json:"sensorId,omitempty"`
	PointCode              string         `json:"pointCode"`
	SourceKey              string         `json:"sourceKey"`
	DisplayName            string         `json:"displayName"`
	PointType              string         `json:"pointType"`
	ValueType              string         `json:"valueType"`
	Unit                   string         `json:"unit,omitempty"`
	Writable               bool           `json:"writable"`
	SampleIntervalMS       int64          `json:"sampleIntervalMs"`
	PublishIntervalMS      int64          `json:"publishIntervalMs"`
	StaleAfterMS           int64          `json:"staleAfterMs"`
	CounterDecreaseMode    *string        `json:"counterDecreaseMode,omitempty"`
	CounterRolloverModulus *float64       `json:"counterRolloverModulus,omitempty"`
	SourceMetadata         map[string]any `json:"sourceMetadata"`
	Status                 string         `json:"status"`
	Meta                   MutationMeta   `json:"meta"`
}

func validateCommonMutation(id, siteID, code, displayName, status string, meta MutationMeta) error {
	create := id == ""
	if err := meta.validate(create); err != nil {
		return err
	}
	if !create && !validUUIDv7(id) {
		return ErrInvalidMutation
	}
	if siteID != "" && !validUUIDv7(siteID) {
		return ErrInvalidMutation
	}
	if !codePattern.MatchString(code) || strings.TrimSpace(displayName) == "" || len(displayName) > 256 {
		return ErrInvalidMutation
	}
	if status != "ACTIVE" && status != "INACTIVE" && status != "RETIRED" {
		return ErrInvalidMutation
	}
	return nil
}

func (input SiteMutation) Validate() error {
	if err := validateCommonMutation(input.ID, "", input.Code, input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if strings.TrimSpace(input.Timezone) == "" || len(input.Timezone) > 128 {
		return ErrInvalidMutation
	}
	return nil
}

func (input SpaceMutation) Validate() error {
	if err := validateCommonMutation(input.ID, input.SiteID, input.Code, input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if input.ParentSpaceID != nil && !validUUIDv7(*input.ParentSpaceID) {
		return ErrInvalidMutation
	}
	switch input.SpaceType {
	case "CAMPUS", "BUILDING", "FLOOR", "ZONE", "ROOM", "PLANT_ROOM", "ROOFTOP", "OUTDOOR", "TENANT_SPACE", "OTHER":
		return nil
	default:
		return ErrInvalidMutation
	}
}

func (input AssetMutation) Validate() error {
	if err := validateCommonMutation(input.ID, input.SiteID, input.Code, input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if strings.TrimSpace(input.AssetType) == "" || len(input.AssetType) > 64 {
		return ErrInvalidMutation
	}
	return nil
}

func (input DeviceMutation) Validate() error {
	if err := validateCommonMutation(input.ID, input.SiteID, input.Code, input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if strings.TrimSpace(input.DeviceType) == "" || len(input.DeviceType) > 64 {
		return ErrInvalidMutation
	}
	return nil
}

func (input SensorMutation) Validate() error {
	if err := validateCommonMutation(input.ID, input.SiteID, input.Code, input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if strings.TrimSpace(input.SensorType) == "" || len(input.SensorType) > 64 || input.Metadata == nil {
		return ErrInvalidMutation
	}
	if input.CalibrationDueAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *input.CalibrationDueAt); err != nil {
			return ErrInvalidMutation
		}
	}
	return nil
}

func (input PointMutation) Validate() error {
	if err := validateCommonMutation(input.ID, input.SiteID, "point", input.DisplayName, input.Status, input.Meta); err != nil {
		return err
	}
	if !validUUIDv7(input.ReportingDeviceID) || input.SensorID != nil && !validUUIDv7(*input.SensorID) ||
		!pointCodePattern.MatchString(input.PointCode) || !sourceKeyPattern.MatchString(input.SourceKey) || input.SourceMetadata == nil {
		return ErrInvalidMutation
	}
	switch input.PointType {
	case "TELEMETRY", "COUNTER", "STATE", "SETTING", "COMMAND":
	default:
		return ErrInvalidMutation
	}
	switch input.ValueType {
	case "BOOLEAN", "NUMBER", "STRING", "JSON":
	default:
		return ErrInvalidMutation
	}
	if input.SampleIntervalMS < 100 || input.SampleIntervalMS > 86400000 ||
		input.PublishIntervalMS < 100 || input.PublishIntervalMS > 86400000 ||
		input.StaleAfterMS < 100 || input.StaleAfterMS > 604800000 ||
		input.PublishIntervalMS < input.SampleIntervalMS || input.StaleAfterMS < input.PublishIntervalMS {
		return ErrInvalidMutation
	}
	if input.PointType == "COMMAND" && !input.Writable || input.PointType != "COMMAND" && input.PointType != "SETTING" && input.Writable {
		return ErrInvalidMutation
	}
	if input.PointType != "COUNTER" {
		if input.CounterDecreaseMode != nil || input.CounterRolloverModulus != nil {
			return ErrInvalidMutation
		}
		return nil
	}
	if input.ValueType != "NUMBER" || input.CounterDecreaseMode == nil {
		return ErrInvalidMutation
	}
	switch *input.CounterDecreaseMode {
	case "RESET_TO_ZERO", "INVALID":
		if input.CounterRolloverModulus != nil {
			return ErrInvalidMutation
		}
	case "ROLLOVER":
		if input.CounterRolloverModulus == nil || *input.CounterRolloverModulus <= 0 {
			return ErrInvalidMutation
		}
	default:
		return ErrInvalidMutation
	}
	return nil
}

type BindingKind string

const (
	BindingDeviceAsset  BindingKind = "DEVICE_ASSET"
	BindingAssetSpace   BindingKind = "ASSET_SPACE"
	BindingDeviceSpace  BindingKind = "DEVICE_SPACE"
	BindingSensorDevice BindingKind = "SENSOR_DEVICE"
	BindingSensorSpace  BindingKind = "SENSOR_SPACE"
	BindingPointSubject BindingKind = "POINT_SUBJECT"
)

type RebindRequest struct {
	SiteID      string       `json:"siteId"`
	Kind        BindingKind  `json:"kind"`
	SourceID    string       `json:"sourceId"`
	TargetID    string       `json:"targetId"`
	TargetType  string       `json:"targetType,omitempty"`
	Role        string       `json:"role"`
	EffectiveAt string       `json:"effectiveAt"`
	Meta        MutationMeta `json:"meta"`
}

type BindingMutationResult struct {
	BindingID string `json:"bindingId"`
	SiteID    string `json:"siteId"`
	Kind      string `json:"kind"`
	SourceID  string `json:"sourceId"`
	TargetID  string `json:"targetId"`
	Role      string `json:"role"`
	Revision  int64  `json:"revision"`
	ValidFrom string `json:"validFrom"`
	Replayed  bool   `json:"replayed"`
}

func (request RebindRequest) Validate() error {
	if !validUUIDv7(request.SiteID) || !validUUIDv7(request.SourceID) || !validUUIDv7(request.TargetID) || request.Meta.ExpectedRevision != 0 {
		return ErrInvalidMutation
	}
	if err := request.Meta.validate(true); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, request.EffectiveAt); err != nil {
		return ErrInvalidMutation
	}
	allowed := false
	switch request.Kind {
	case BindingDeviceAsset:
		allowed = request.Role == "CONTROLLER" || request.Role == "METER" || request.Role == "SENSOR" || request.Role == "GATEWAY" || request.Role == "SUPERVISORY_CONTROLLER"
	case BindingAssetSpace:
		allowed = request.Role == "INSTALLED_IN" || request.Role == "SERVES"
	case BindingDeviceSpace:
		allowed = request.Role == "INSTALLED_IN" || request.Role == "SERVES" || request.Role == "GATEWAY_FOR" || request.Role == "SUPERVISES"
	case BindingSensorDevice:
		allowed = request.Role == "REPORTS_THROUGH"
	case BindingSensorSpace:
		allowed = request.Role == "MOUNTED_IN"
	case BindingPointSubject:
		allowed = (request.TargetType == "SITE" || request.TargetType == "SPACE" || request.TargetType == "ASSET") && (request.Role == "DESCRIBES" || request.Role == "CONTROLS")
		if request.TargetType == "SITE" && request.TargetID != request.SiteID {
			allowed = false
		}
	}
	if !allowed {
		return ErrInvalidMutation
	}
	return nil
}

type TemplateKind string

const (
	TemplateAsset  TemplateKind = "ASSET"
	TemplateDevice TemplateKind = "DEVICE"
	TemplatePoint  TemplateKind = "POINT"
)

type TemplateRevision struct {
	ID                string            `json:"id"`
	TenantID          string            `json:"tenantId"`
	TemplateID        string            `json:"templateId"`
	TemplateKey       string            `json:"templateKey"`
	TemplateKind      TemplateKind      `json:"templateKind"`
	RevisionNumber    int64             `json:"revisionNumber"`
	Status            string            `json:"status"`
	Payload           map[string]any    `json:"payload"`
	ReleaseReferences map[string]string `json:"releaseReferences"`
	ReleasedAt        string            `json:"releasedAt"`
}

type ReleaseTemplateRequest struct {
	TemplateKey       string            `json:"templateKey"`
	TemplateKind      TemplateKind      `json:"templateKind"`
	Payload           map[string]any    `json:"payload"`
	ReleaseReferences map[string]string `json:"releaseReferences"`
	Meta              MutationMeta      `json:"meta"`
}

func (request ReleaseTemplateRequest) Validate() error {
	if err := request.Meta.validate(true); err != nil || !templateKeyPattern.MatchString(request.TemplateKey) || request.Payload == nil || request.ReleaseReferences == nil {
		return ErrInvalidMutation
	}
	if request.TemplateKind != TemplateAsset && request.TemplateKind != TemplateDevice && request.TemplateKind != TemplatePoint {
		return ErrInvalidMutation
	}
	if len(request.ReleaseReferences) == 0 {
		return ErrInvalidMutation
	}
	for key, value := range request.ReleaseReferences {
		if strings.TrimSpace(key) == "" || len(key) > 128 || strings.TrimSpace(value) == "" || len(value) > 256 {
			return ErrInvalidMutation
		}
	}
	return nil
}

type TemplateAssignment struct {
	ID                 string       `json:"id"`
	TenantID           string       `json:"tenantId"`
	SiteID             string       `json:"siteId"`
	TargetType         TemplateKind `json:"targetType"`
	TargetID           string       `json:"targetId"`
	TemplateRevisionID string       `json:"templateRevisionId"`
	ValidFrom          string       `json:"validFrom"`
	ValidTo            *string      `json:"validTo,omitempty"`
	Revision           int64        `json:"revision"`
}

type AssignTemplateRequest struct {
	SiteID             string       `json:"siteId"`
	TargetType         TemplateKind `json:"targetType"`
	TargetID           string       `json:"targetId"`
	TemplateRevisionID string       `json:"templateRevisionId"`
	EffectiveAt        string       `json:"effectiveAt"`
	Meta               MutationMeta `json:"meta"`
}

func (request AssignTemplateRequest) Validate() error {
	if err := request.Meta.validate(true); err != nil || !validUUIDv7(request.SiteID) || !validUUIDv7(request.TargetID) || !validUUIDv7(request.TemplateRevisionID) {
		return ErrInvalidMutation
	}
	if request.TargetType != TemplateAsset && request.TargetType != TemplateDevice && request.TargetType != TemplatePoint {
		return ErrInvalidMutation
	}
	if _, err := time.Parse(time.RFC3339Nano, request.EffectiveAt); err != nil {
		return ErrInvalidMutation
	}
	return nil
}

type ResourceType string

const (
	ResourceSite   ResourceType = "SITE"
	ResourceSpace  ResourceType = "SPACE"
	ResourceAsset  ResourceType = "ASSET"
	ResourceDevice ResourceType = "DEVICE"
	ResourceSensor ResourceType = "SENSOR"
	ResourcePoint  ResourceType = "POINT"
)

type RetireRequest struct {
	SiteID       string       `json:"siteId"`
	ResourceType ResourceType `json:"resourceType"`
	ResourceID   string       `json:"resourceId"`
	Meta         MutationMeta `json:"meta"`
}

type RetirementResult struct {
	SagaID          string       `json:"sagaId"`
	ResourceType    ResourceType `json:"resourceType"`
	ResourceID      string       `json:"resourceId"`
	Status          string       `json:"status"`
	DependencyCount int          `json:"dependencyCount"`
	Revision        int64        `json:"revision"`
	Replayed        bool         `json:"replayed"`
}

func (request RetireRequest) Validate() error {
	if err := request.Meta.validate(false); err != nil || !validUUIDv7(request.SiteID) || !validUUIDv7(request.ResourceID) {
		return ErrInvalidMutation
	}
	switch request.ResourceType {
	case ResourceSite:
		if request.ResourceID != request.SiteID {
			return ErrInvalidMutation
		}
		return nil
	case ResourceSpace, ResourceAsset, ResourceDevice, ResourceSensor, ResourcePoint:
		return nil
	default:
		return ErrInvalidMutation
	}
}

type ImportRow struct {
	RowNumber        int             `json:"rowNumber"`
	ResourceType     ResourceType    `json:"resourceType"`
	ExternalID       string          `json:"externalId"`
	TargetID         string          `json:"targetId,omitempty"`
	ExpectedRevision int64           `json:"expectedRevision"`
	Payload          json.RawMessage `json:"payload"`
}

type ImportRowResult struct {
	RowNumber        int          `json:"rowNumber"`
	ResourceType     ResourceType `json:"resourceType"`
	ExternalID       string       `json:"externalId"`
	TargetID         string       `json:"targetId,omitempty"`
	ExpectedRevision int64        `json:"expectedRevision"`
	Status           string       `json:"status"`
	ErrorCode        string       `json:"errorCode,omitempty"`
	Message          string       `json:"message,omitempty"`
}

type ImportPlan struct {
	SchemaVersion int               `json:"schemaVersion"`
	PlanID        string            `json:"planId"`
	TenantID      string            `json:"tenantId"`
	SiteID        string            `json:"siteId"`
	Namespace     string            `json:"namespace"`
	Rows          []ImportRow       `json:"rows"`
	Results       []ImportRowResult `json:"results"`
	Digest        string            `json:"digest"`
}

type ImportPlanRequest struct {
	SiteID    string      `json:"siteId"`
	Namespace string      `json:"namespace"`
	Rows      []ImportRow `json:"rows"`
}

type ImportCommitRequest struct {
	Plan ImportPlan   `json:"plan"`
	Meta MutationMeta `json:"meta"`
}

type ImportCommitResult struct {
	PlanDigest string            `json:"planDigest"`
	Results    []ImportRowResult `json:"results"`
	Replayed   bool              `json:"replayed"`
}

func (request ImportPlanRequest) Validate() error {
	if !validUUIDv7(request.SiteID) || strings.TrimSpace(request.Namespace) == "" || len(request.Namespace) > 128 || len(request.Rows) == 0 || len(request.Rows) > 1000 {
		return ErrInvalidMutation
	}
	seen := map[int]struct{}{}
	for _, row := range request.Rows {
		if row.RowNumber < 1 || strings.TrimSpace(row.ExternalID) == "" || len(row.ExternalID) > 256 || len(row.Payload) == 0 {
			return ErrInvalidMutation
		}
		if _, duplicate := seen[row.RowNumber]; duplicate {
			return ErrInvalidMutation
		}
		seen[row.RowNumber] = struct{}{}
		switch row.ResourceType {
		case ResourceSpace, ResourceAsset, ResourceDevice, ResourceSensor, ResourcePoint:
		default:
			return ErrInvalidMutation
		}
	}
	return nil
}

func (request ImportCommitRequest) Validate() error {
	if err := request.Meta.validate(true); err != nil || request.Plan.SchemaVersion != 1 || !validUUIDv7(request.Plan.PlanID) || !validUUIDv7(request.Plan.TenantID) || !validUUIDv7(request.Plan.SiteID) || request.Plan.Digest == "" {
		return ErrInvalidMutation
	}
	if len(request.Plan.Rows) == 0 || len(request.Plan.Results) != len(request.Plan.Rows) {
		return ErrInvalidMutation
	}
	return nil
}

func ensureSiteScope(claims registryauth.GrantClaims, siteID string) error {
	if !registryauth.ScopeAllows(claims, siteID) {
		return ErrNotFound
	}
	return nil
}

func importPayloadError(row ImportRow, siteID string) error {
	switch row.ResourceType {
	case ResourceSpace:
		var input SpaceMutation
		if json.Unmarshal(row.Payload, &input) != nil {
			return ErrInvalidMutation
		}
		input.ID, input.SiteID, input.Meta = importMutationID(row), siteID, MutationMeta{ExpectedRevision: row.ExpectedRevision, IdempotencyKey: "import-row-placeholder", Reason: "import plan"}
		return input.Validate()
	case ResourceAsset:
		var input AssetMutation
		if json.Unmarshal(row.Payload, &input) != nil {
			return ErrInvalidMutation
		}
		input.ID, input.SiteID, input.Meta = importMutationID(row), siteID, MutationMeta{ExpectedRevision: row.ExpectedRevision, IdempotencyKey: "import-row-placeholder", Reason: "import plan"}
		return input.Validate()
	case ResourceDevice:
		var input DeviceMutation
		if json.Unmarshal(row.Payload, &input) != nil {
			return ErrInvalidMutation
		}
		input.ID, input.SiteID, input.Meta = importMutationID(row), siteID, MutationMeta{ExpectedRevision: row.ExpectedRevision, IdempotencyKey: "import-row-placeholder", Reason: "import plan"}
		return input.Validate()
	case ResourceSensor:
		var input SensorMutation
		if json.Unmarshal(row.Payload, &input) != nil {
			return ErrInvalidMutation
		}
		input.ID, input.SiteID, input.Meta = importMutationID(row), siteID, MutationMeta{ExpectedRevision: row.ExpectedRevision, IdempotencyKey: "import-row-placeholder", Reason: "import plan"}
		return input.Validate()
	case ResourcePoint:
		var input PointMutation
		if json.Unmarshal(row.Payload, &input) != nil {
			return ErrInvalidMutation
		}
		input.ID, input.SiteID, input.Meta = importMutationID(row), siteID, MutationMeta{ExpectedRevision: row.ExpectedRevision, IdempotencyKey: "import-row-placeholder", Reason: "import plan"}
		return input.Validate()
	default:
		return fmt.Errorf("%w: unsupported import resource type", ErrInvalidMutation)
	}
}

func importMutationID(row ImportRow) string {
	if row.ExpectedRevision == 0 {
		return ""
	}
	return row.TargetID
}

type RegistryWriter interface {
	SaveSite(context.Context, registryauth.GrantClaims, SiteMutation) (Site, bool, error)
	SaveSpace(context.Context, registryauth.GrantClaims, SpaceMutation) (Space, bool, error)
	SaveAsset(context.Context, registryauth.GrantClaims, AssetMutation) (Asset, bool, error)
	SaveDevice(context.Context, registryauth.GrantClaims, DeviceMutation) (Device, bool, error)
	SaveSensor(context.Context, registryauth.GrantClaims, SensorMutation) (Sensor, bool, error)
	SavePoint(context.Context, registryauth.GrantClaims, PointMutation) (TelemetryPoint, bool, error)
	Rebind(context.Context, registryauth.GrantClaims, RebindRequest) (BindingMutationResult, error)
	ReleaseTemplate(context.Context, registryauth.GrantClaims, ReleaseTemplateRequest) (TemplateRevision, bool, error)
	AssignTemplate(context.Context, registryauth.GrantClaims, AssignTemplateRequest) (TemplateAssignment, bool, error)
	PlanImport(context.Context, registryauth.GrantClaims, ImportPlanRequest) (ImportPlan, error)
	CommitImport(context.Context, registryauth.GrantClaims, ImportCommitRequest) (ImportCommitResult, error)
	Retire(context.Context, registryauth.GrantClaims, RetireRequest) (RetirementResult, error)
}
