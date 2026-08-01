module github.com/quanlaihe/hvac-web/services/telemetry-query-service

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/analyticsmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/analyticsmodel => ../../libs/analyticsmodel

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel => ../../libs/telemetryhistorymodel
