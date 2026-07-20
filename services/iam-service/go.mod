module github.com/quanlaihe/hvac-web/services/iam-service

go 1.25.0

require (
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext
replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
