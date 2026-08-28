module github.com/quanlaihe/hvac-web/tools/oidc-test-provider

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/oidctest v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
replace github.com/quanlaihe/hvac-web/libs/oidctest => ../../libs/oidctest
