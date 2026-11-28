package edgecontrol

import "time"

// PointCopyTarget mirrors a mapped primary point onto an additional target device/point.
type PointCopyTarget struct {
	DeviceKey string `json:"deviceKey"`
	PointCode string `json:"pointCode"`
}

// GatewayPointMapping maps a gateway source channel to a canonical device point.
type GatewayPointMapping struct {
	SourceChannel    string            `json:"sourceChannel"`
	PrimaryDeviceKey string            `json:"primaryDeviceKey"`
	PrimaryPointCode string            `json:"primaryPointCode"`
	CopyTargets      []PointCopyTarget `json:"copyTargets,omitempty"`
}

// GatewaySamplingPolicy configures poll/publish intervals for a device or point.
type GatewaySamplingPolicy struct {
	DeviceKey         string `json:"deviceKey"`
	PointCode         string `json:"pointCode,omitempty"`
	SampleIntervalMS  int    `json:"sampleIntervalMs"`
	PublishIntervalMS int    `json:"publishIntervalMs"`
}

// ConcentratorShadowParams mirrors serial/LoRa concentrator parameters.
type ConcentratorShadowParams struct {
	ModbusID        int `json:"modbusId"`
	BaudRateCode    int `json:"baudRateCode"`
	OffLineTime     int `json:"offLineTime"`
	DataUplinkCycle int `json:"dataUplinkCycle"`
	LocalNet        int `json:"localNet"`
	TimeMark        int `json:"timeMark"`
	RfTxPower       int `json:"rfTxPower"`
	RfFrequency     int `json:"rfFrequency"`
	RfBandWidth     int `json:"rfBandWidth"`
	RfSf            int `json:"rfSf"`
	RfLeadCode      int `json:"rfLeadCode"`
	TxTimeout       int `json:"txTimeout"`
	RxTimeout       int `json:"rxTimeout"`
	RssiOut         int `json:"rssiOut"`
}

// ConcentratorShadowEIDSlot assigns one EID slot on an RS-485 concentrator.
type ConcentratorShadowEIDSlot struct {
	Slot int    `json:"slot"`
	EID  uint32 `json:"eid"`
}

// ConcentratorUnitShadow describes one concentrator's params and EID table.
type ConcentratorUnitShadow struct {
	Params *ConcentratorShadowParams   `json:"params,omitempty"`
	EIDs   []ConcentratorShadowEIDSlot `json:"eids,omitempty"`
}

// ShadowDriftDetail reports section-level shadow rejection/drift facts.
type ShadowDriftDetail struct {
	Section string `json:"section"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

// GatewayDesiredConfig matches GET /gateway/v1/config-shadow desired payload on edge clients.
type GatewayDesiredConfig struct {
	DesiredRevision     int64                             `json:"desiredRevision"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []ModbusDriverInput               `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *AutonomyPolicyConfig             `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *EdgeRuleBundle                   `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy   *EdgeAggregationPolicy            `json:"aggregationPolicy,omitempty"`
	ClusterPolicy       *ClusterControlPolicy             `json:"clusterPolicy,omitempty"`
	EnergySchedule      *EdgeEnergySchedule               `json:"energySchedule,omitempty"`
	EdgeModels          []EdgeModelBundle                 `json:"edgeModels,omitempty"`
	UpdatedBy           string                            `json:"updatedBy,omitempty"`
	Reason              string                            `json:"reason,omitempty"`
	CreatedAt           *time.Time                        `json:"createdAt,omitempty"`
	UpdatedAt           *time.Time                        `json:"updatedAt,omitempty"`
}

// GatewayShadowPullResponse matches GET /gateway/v1/config-shadow response on edge clients.
type GatewayShadowPullResponse struct {
	GatewayID       string                `json:"gatewayId"`
	DesiredRevision int64                 `json:"desiredRevision"`
	HasDelta        bool                  `json:"hasDelta"`
	Desired         *GatewayDesiredConfig `json:"desired,omitempty"`
}

// GatewayShadowReportRequest matches POST /gateway/v1/config-shadow/report body on edge clients.
type GatewayShadowReportRequest struct {
	ReportedRevision    int64                             `json:"reportedRevision"`
	AppliedStatus       string                            `json:"appliedStatus"`
	PointMappings       []GatewayPointMapping             `json:"pointMappings"`
	SamplingPolicies    []GatewaySamplingPolicy           `json:"samplingPolicies"`
	ConcentratorConfigs map[string]ConcentratorUnitShadow `json:"concentratorConfigs"`
	DriverConfigs       []ModbusDriverInput               `json:"driverConfigs,omitempty"`
	AutonomyPolicy      *AutonomyPolicyConfig             `json:"autonomyPolicy,omitempty"`
	EdgeRuleBundle      *EdgeRuleBundle                   `json:"edgeRuleBundle,omitempty"`
	AggregationPolicy   *EdgeAggregationPolicy            `json:"aggregationPolicy,omitempty"`
	ClusterPolicy       *ClusterControlPolicy             `json:"clusterPolicy,omitempty"`
	EnergySchedule      *EdgeEnergySchedule               `json:"energySchedule,omitempty"`
	EdgeModels          []EdgeModelBundle                 `json:"edgeModels,omitempty"`
	EdgeModelStatuses   []EdgeModelRuntimeStatus          `json:"edgeModelStatuses,omitempty"`
	DriftDetails        []ShadowDriftDetail               `json:"driftDetails,omitempty"`
	ErrorMessage        *string                           `json:"errorMessage,omitempty"`
	ReportedAt          *time.Time                        `json:"reportedAt,omitempty"`
}
