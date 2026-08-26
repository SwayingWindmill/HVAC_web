module github.com/quanlaihe/hvac-web/cmd/telemetry-worker

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/modules/energy v0.0.0
	github.com/quanlaihe/hvac-web/modules/telemetry v0.0.0
)

replace github.com/quanlaihe/hvac-web/modules/energy => ../../modules/energy

replace github.com/quanlaihe/hvac-web/modules/telemetry => ../../modules/telemetry
