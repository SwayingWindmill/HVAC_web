module github.com/quanlaihe/hvac-web/services/command-service

go 1.25.12

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/commandauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/commandmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/workloadtls v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/commandauth => ../../libs/commandauth

replace github.com/quanlaihe/hvac-web/libs/commandmodel => ../../libs/commandmodel

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/workloadtls => ../../libs/workloadtls
