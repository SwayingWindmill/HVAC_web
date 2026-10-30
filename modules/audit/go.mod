module github.com/quanlaihe/hvac-web/modules/audit

go 1.27.3

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/operationsauditevent v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionevent v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/operationsauditevent => ../../libs/operationsauditevent

replace github.com/quanlaihe/hvac-web/libs/sessionevent => ../../libs/sessionevent

replace github.com/quanlaihe/hvac-web/libs/grant => ../../libs/grant
