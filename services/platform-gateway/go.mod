module github.com/quanlaihe/hvac-web/services/platform-gateway

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/alarmauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/alarmmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/analyticsmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/commandauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/commandmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/oidctest v0.0.0
	github.com/quanlaihe/hvac-web/libs/ownershipregistry v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionevent v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionstore v0.0.0
	github.com/quanlaihe/hvac-web/libs/testpki v0.0.0
	github.com/quanlaihe/hvac-web/libs/workorderauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/workordermodel v0.0.0
	github.com/quanlaihe/hvac-web/services/iam-service v0.0.0
)

require (
	github.com/quanlaihe/hvac-web/libs/registryauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel v0.0.0
)

require (
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/logto-io/go/v2 v2.2.0
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/alarmauth => ../../libs/alarmauth

replace github.com/quanlaihe/hvac-web/libs/alarmmodel => ../../libs/alarmmodel

replace github.com/quanlaihe/hvac-web/libs/analyticsmodel => ../../libs/analyticsmodel

replace github.com/quanlaihe/hvac-web/libs/commandauth => ../../libs/commandauth

replace github.com/quanlaihe/hvac-web/libs/commandmodel => ../../libs/commandmodel

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/oidctest => ../../libs/oidctest

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/ownershipregistry => ../../libs/ownershipregistry

replace github.com/quanlaihe/hvac-web/libs/sessionevent => ../../libs/sessionevent

replace github.com/quanlaihe/hvac-web/libs/sessionstore => ../../libs/sessionstore

replace github.com/quanlaihe/hvac-web/libs/testpki => ../../libs/testpki

replace github.com/quanlaihe/hvac-web/libs/workorderauth => ../../libs/workorderauth

replace github.com/quanlaihe/hvac-web/libs/workordermodel => ../../libs/workordermodel

replace github.com/quanlaihe/hvac-web/services/iam-service => ../iam-service

replace github.com/quanlaihe/hvac-web/libs/registryauth => ../../libs/registryauth

replace github.com/quanlaihe/hvac-web/libs/telemetryauth => ../../libs/telemetryauth

replace github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel => ../../libs/telemetryhistorymodel
