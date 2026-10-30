module github.com/quanlaihe/hvac-web/modules/telemetry

go 1.27.3

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/analyticsmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/limitpolicy v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel v0.0.0
	github.com/redis/go-redis/v9 v9.21.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/analyticsmodel => ../../libs/analyticsmodel

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/limitpolicy => ../../libs/limitpolicy

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/telemetryauth => ../../libs/telemetryauth

replace github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel => ../../libs/telemetryhistorymodel

replace github.com/quanlaihe/hvac-web/libs/grant => ../../libs/grant
