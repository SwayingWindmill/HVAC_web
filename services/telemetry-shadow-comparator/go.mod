module github.com/quanlaihe/hvac-web/services/telemetry-shadow-comparator

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/ownershipregistry v0.0.0
	github.com/quanlaihe/hvac-web/services/telemetry-runtime-service v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/ownershipregistry => ../../libs/ownershipregistry

replace github.com/quanlaihe/hvac-web/services/telemetry-runtime-service => ../telemetry-runtime-service
