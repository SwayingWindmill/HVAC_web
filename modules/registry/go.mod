module github.com/quanlaihe/hvac-web/modules/registry

go 1.27.3

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/edgecontrol v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/registryauth v0.0.0
)

require (
	github.com/goburrow/serial v0.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	github.com/simonvetter/modbus v1.6.4 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/edgecontrol => ../../libs/edgecontrol

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

replace github.com/quanlaihe/hvac-web/libs/registryauth => ../../libs/registryauth

replace github.com/quanlaihe/hvac-web/libs/grant => ../../libs/grant
