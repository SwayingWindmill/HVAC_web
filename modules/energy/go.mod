module github.com/quanlaihe/hvac-web/modules/energy

go 1.27.3

require (
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/registryauth v0.0.0
	github.com/quanlaihe/hvac-web/libs/tariffmodel v0.0.0
)

replace (
	github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
	github.com/quanlaihe/hvac-web/libs/registryauth => ../../libs/registryauth
	github.com/quanlaihe/hvac-web/libs/tariffmodel => ../../libs/tariffmodel
)

replace github.com/quanlaihe/hvac-web/libs/grant => ../../libs/grant
