package core

type Space struct {
	ID            string  `json:"id"`
	TenantID      string  `json:"tenantId"`
	SiteID        string  `json:"siteId"`
	ParentSpaceID *string `json:"parentSpaceId"`
	Code          string  `json:"code"`
	DisplayName   string  `json:"displayName"`
	SpaceType     string  `json:"spaceType"`
	Status        string  `json:"status"`
	Revision      int64   `json:"revision"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

type AssetSpaceBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	AssetID     string  `json:"assetId"`
	SpaceID     string  `json:"spaceId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type DeviceSpaceBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	DeviceID    string  `json:"deviceId"`
	SpaceID     string  `json:"spaceId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

// Sensor is an optional physical probe with an independent traceability lifecycle.
// Point remains the canonical data identity whether Sensor exists or not.
type Sensor struct {
	ID               string         `json:"id"`
	TenantID         string         `json:"tenantId"`
	SiteID           string         `json:"siteId"`
	Code             string         `json:"code"`
	DisplayName      string         `json:"displayName"`
	SensorType       string         `json:"sensorType"`
	Manufacturer     *string        `json:"manufacturer"`
	Model            *string        `json:"model"`
	SerialNumber     *string        `json:"serialNumber"`
	CalibrationDueAt *string        `json:"calibrationDueAt"`
	Metadata         map[string]any `json:"metadata"`
	Status           string         `json:"status"`
	Revision         int64          `json:"revision"`
	CreatedAt        string         `json:"createdAt"`
	UpdatedAt        string         `json:"updatedAt"`
}

type SensorDeviceBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	SensorID    string  `json:"sensorId"`
	DeviceID    string  `json:"deviceId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type SensorSpaceBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	SensorID    string  `json:"sensorId"`
	SpaceID     string  `json:"spaceId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type TelemetryPoint struct {
	ID                string         `json:"id"`
	TenantID          string         `json:"tenantId"`
	SiteID            string         `json:"siteId"`
	ReportingDeviceID string         `json:"reportingDeviceId"`
	SensorID          *string        `json:"sensorId"`
	PointCode         string         `json:"pointCode"`
	SourceKey         string         `json:"sourceKey"`
	DisplayName       string         `json:"displayName"`
	PointType         string         `json:"pointType"`
	ValueType         string         `json:"valueType"`
	Unit              *string        `json:"unit"`
	Writable          bool           `json:"writable"`
	SampleIntervalMS  int            `json:"sampleIntervalMs"`
	PublishIntervalMS int            `json:"publishIntervalMs"`
	StaleAfterMS      int            `json:"staleAfterMs"`
	SourceMetadata    map[string]any `json:"sourceMetadata"`
	Status            string         `json:"status"`
	Revision          int64          `json:"revision"`
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
}

type PointSubjectBinding struct {
	ID          string  `json:"id"`
	TenantID    string  `json:"tenantId"`
	SiteID      string  `json:"siteId"`
	PointID     string  `json:"pointId"`
	SubjectType string  `json:"subjectType"`
	SpaceID     *string `json:"spaceId"`
	AssetID     *string `json:"assetId"`
	BindingRole string  `json:"bindingRole"`
	Status      string  `json:"status"`
	ValidFrom   string  `json:"validFrom"`
	ValidTo     *string `json:"validTo"`
	Revision    int64   `json:"revision"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type AssetRelationship struct {
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
	Revision  int64   `json:"revision"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type AssetModelCounts struct {
	Spaces          int `json:"spaces"`
	Assets          int `json:"assets"`
	DeviceEndpoints int `json:"deviceEndpoints"`
	PhysicalSensors int `json:"physicalSensors"`
	Points           int `json:"points"`
}

type SiteAssetModel struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	TenantID        string              `json:"tenantId"`
	SiteID          string              `json:"siteId"`
	Spaces          []Space             `json:"spaces"`
	Assets          []Asset             `json:"assets"`
	Devices         []Device            `json:"devices"`
	Sensors         []Sensor            `json:"sensors"`
	TelemetryPoints []TelemetryPoint    `json:"telemetryPoints"`
	Relationships   []AssetRelationship `json:"relationships"`
	Counts          AssetModelCounts    `json:"counts"`

	DeviceBindings       []DeviceBinding       `json:"-"`
	AssetSpaceBindings   []AssetSpaceBinding   `json:"-"`
	DeviceSpaceBindings  []DeviceSpaceBinding  `json:"-"`
	SensorDeviceBindings []SensorDeviceBinding `json:"-"`
	SensorSpaceBindings  []SensorSpaceBinding  `json:"-"`
	PointSubjectBindings []PointSubjectBinding `json:"-"`
}
