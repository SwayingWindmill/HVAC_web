module github.com/quanlaihe/hvac-web/libs/commandauth

go 1.27.3

require (
	github.com/quanlaihe/hvac-web/libs/commandmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/commandmodel => ../commandmodel

replace github.com/quanlaihe/hvac-web/libs/grant => ../grant
