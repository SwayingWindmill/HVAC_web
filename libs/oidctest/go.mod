module github.com/quanlaihe/hvac-web/libs/oidctest

go 1.27.3

require (
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0 // indirect
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../identitycontext

replace github.com/quanlaihe/hvac-web/libs/grant => ../grant
