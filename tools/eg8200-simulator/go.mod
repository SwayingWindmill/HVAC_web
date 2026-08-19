module github.com/quanlaihe/hvac-web/tools/eg8200-simulator

go 1.25.12

require (
	github.com/eclipse/paho.golang v0.23.0
	github.com/quanlaihe/hvac-web/libs/edgecontrol v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/edgecontrol => ../../libs/edgecontrol

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/net v0.43.0 // indirect
)
