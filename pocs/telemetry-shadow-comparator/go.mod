module github.com/quanlaihe/hvac-web/pocs/telemetry-shadow-comparator

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/ownershipregistry v0.0.0
	github.com/quanlaihe/hvac-web/modules/telemetry v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/ownershipregistry => ../../libs/ownershipregistry

replace github.com/quanlaihe/hvac-web/modules/telemetry => ../../modules/telemetry
