module github.com/quanlaihe/hvac-web/services/platform-gateway

go 1.22.5

require (
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/oidctest v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/ownershipregistry v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionevent v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionstore v0.0.0
	github.com/quanlaihe/hvac-web/libs/testpki v0.0.0
	github.com/quanlaihe/hvac-web/services/iam-service v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext
replace github.com/quanlaihe/hvac-web/libs/oidctest => ../../libs/oidctest
replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
replace github.com/quanlaihe/hvac-web/libs/ownershipregistry => ../../libs/ownershipregistry
replace github.com/quanlaihe/hvac-web/libs/sessionevent => ../../libs/sessionevent
replace github.com/quanlaihe/hvac-web/libs/sessionstore => ../../libs/sessionstore
replace github.com/quanlaihe/hvac-web/libs/testpki => ../../libs/testpki
replace github.com/quanlaihe/hvac-web/services/iam-service => ../iam-service
