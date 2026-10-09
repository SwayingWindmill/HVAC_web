module github.com/quanlaihe/hvac-web/modules/energy

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/registryauth v0.0.0
)

replace (
	github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
	github.com/quanlaihe/hvac-web/libs/registryauth => ../../libs/registryauth
)
