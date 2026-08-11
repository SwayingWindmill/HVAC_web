package core

type Area struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	ParentAreaID         *string `json:"parentAreaId"`
	Code                 string  `json:"code"`
	DisplayName          string  `json:"displayName"`
	AreaType             string  `json:"areaType"`
	Status               string  `json:"status"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type EquipmentAreaBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	EquipmentID          string  `json:"equipmentId"`
	AreaID               string  `json:"areaId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type DeviceAreaBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	DeviceID             string  `json:"deviceId"`
	AreaID               string  `json:"areaId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type Sensor struct {
	ID                   string         `json:"id"`
	TenantID             string         `json:"tenantId"`
	OwningOrganizationID string         `json:"owningOrganizationId"`
	SiteID               string         `json:"siteId"`
	Code                 string         `json:"code"`
	DisplayName          string         `json:"displayName"`
	SensorType           string         `json:"sensorType"`
	Manufacturer         *string        `json:"manufacturer"`
	Model                *string        `json:"model"`
	SerialNumber         *string        `json:"serialNumber"`
	CalibrationDueAt     *string        `json:"calibrationDueAt"`
	Metadata             map[string]any `json:"metadata"`
	Status               string         `json:"status"`
	Revision             int64          `json:"revision"`
	CreatedAt            string         `json:"createdAt"`
	UpdatedAt            string         `json:"updatedAt"`
}

type SensorDeviceBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	SensorID             string  `json:"sensorId"`
	DeviceID             string  `json:"deviceId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type SensorAreaBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	SensorID             string  `json:"sensorId"`
	AreaID               string  `json:"areaId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type SensorSubjectBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	SensorID             string  `json:"sensorId"`
	SubjectType          string  `json:"subjectType"`
	AreaID               *string `json:"areaId"`
	EquipmentID          *string `json:"equipmentId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type TelemetryPoint struct {
	ID                   string         `json:"id"`
	TenantID             string         `json:"tenantId"`
	OwningOrganizationID string         `json:"owningOrganizationId"`
	SiteID               string         `json:"siteId"`
	ReportingDeviceID    string         `json:"reportingDeviceId"`
	SensorID             *string        `json:"sensorId"`
	PointKey             string         `json:"pointKey"`
	SourceKey            string         `json:"sourceKey"`
	DisplayName          string         `json:"displayName"`
	PointKind            string         `json:"pointKind"`
	ValueType            string         `json:"valueType"`
	Unit                 *string        `json:"unit"`
	Writable             bool           `json:"writable"`
	SampleIntervalMS     int            `json:"sampleIntervalMs"`
	PublishIntervalMS    int            `json:"publishIntervalMs"`
	StaleAfterMS         int            `json:"staleAfterMs"`
	FormulaRevision      *string        `json:"formulaRevision"`
	SourceMetadata       map[string]any `json:"sourceMetadata"`
	Status               string         `json:"status"`
	Revision             int64          `json:"revision"`
	CreatedAt            string         `json:"createdAt"`
	UpdatedAt            string         `json:"updatedAt"`
}

type PointSubjectBinding struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	PointID              string  `json:"pointId"`
	SubjectType          string  `json:"subjectType"`
	AreaID               *string `json:"areaId"`
	EquipmentID          *string `json:"equipmentId"`
	BindingRole          string  `json:"bindingRole"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type AssetRelationship struct {
	ID                   string  `json:"id"`
	TenantID             string  `json:"tenantId"`
	OwningOrganizationID string  `json:"owningOrganizationId"`
	SiteID               string  `json:"siteId"`
	FromType             string  `json:"fromType"`
	FromID               string  `json:"fromId"`
	ToType               string  `json:"toType"`
	ToID                 string  `json:"toId"`
	Role                 string  `json:"role"`
	Status               string  `json:"status"`
	ValidFrom            string  `json:"validFrom"`
	ValidTo              *string `json:"validTo"`
	Revision             int64   `json:"revision"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type CalculatedPointInput struct {
	TenantID             string `json:"tenantId"`
	OwningOrganizationID string `json:"owningOrganizationId"`
	SiteID               string `json:"siteId"`
	CalculatedPointID    string `json:"calculatedPointId"`
	InputPointID         string `json:"inputPointId"`
	InputRole            string `json:"inputRole"`
	Ordinal              int    `json:"ordinal"`
	FormulaRevision      string `json:"formulaRevision"`
}

type AssetModelCounts struct {
	Areas                    int `json:"areas"`
	Equipment                int `json:"equipment"`
	DeviceEndpoints          int `json:"deviceEndpoints"`
	Sensors                  int `json:"sensors"`
	TelemetryPoints          int `json:"telemetryPoints"`
	CalculatedPoints         int `json:"calculatedPoints"`
	IndependentSensorDevices int `json:"independentSensorDevices"`
}

type SiteAssetModel struct {
	SchemaVersion         int                    `json:"schemaVersion"`
	TenantID              string                 `json:"tenantId"`
	SiteID                string                 `json:"siteId"`
	Areas                 []Area                 `json:"areas"`
	Equipment             []Equipment            `json:"equipment"`
	Devices               []Device               `json:"devices"`
	Sensors               []Sensor               `json:"sensors"`
	TelemetryPoints       []TelemetryPoint       `json:"telemetryPoints"`
	Relationships         []AssetRelationship    `json:"relationships"`
	CalculatedPointInputs []CalculatedPointInput `json:"calculatedPointInputs"`
	Counts                AssetModelCounts       `json:"counts"`

	DeviceBindings        []DeviceBinding        `json:"-"`
	EquipmentAreaBindings []EquipmentAreaBinding `json:"-"`
	DeviceAreaBindings    []DeviceAreaBinding    `json:"-"`
	SensorDeviceBindings  []SensorDeviceBinding  `json:"-"`
	SensorAreaBindings    []SensorAreaBinding    `json:"-"`
	SensorSubjectBindings []SensorSubjectBinding `json:"-"`
	PointSubjectBindings  []PointSubjectBinding  `json:"-"`
}
