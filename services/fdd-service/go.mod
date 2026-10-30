module github.com/quanlaihe/hvac-web/services/fdd-service

go 1.27.3

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/intelligencemodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/schedulerjob v0.0.0
	github.com/quanlaihe/hvac-web/libs/workloadtls v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/intelligencemodel => ../../libs/intelligencemodel

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/workloadtls => ../../libs/workloadtls

replace github.com/quanlaihe/hvac-web/libs/schedulerjob => ../../libs/schedulerjob

replace github.com/quanlaihe/hvac-web/libs/grant => ../../libs/grant
