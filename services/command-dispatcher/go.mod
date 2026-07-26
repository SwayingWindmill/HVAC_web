module github.com/quanlaihe/hvac-web/services/command-dispatcher

go 1.25.12

require (
	github.com/quanlaihe/hvac-web/libs/commandmodel v0.0.0
	github.com/quanlaihe/hvac-web/services/command-service v0.0.0
	github.com/quanlaihe/hvac-web/services/thingsboard-connector-control v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/commandmodel => ../../libs/commandmodel
replace github.com/quanlaihe/hvac-web/services/command-service => ../command-service
replace github.com/quanlaihe/hvac-web/services/thingsboard-connector-control => ../thingsboard-connector-control
