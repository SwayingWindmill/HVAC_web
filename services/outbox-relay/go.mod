module github.com/quanlaihe/hvac-web/services/outbox-relay

go 1.22.5

require (
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionstore v0.0.0
	github.com/segmentio/kafka-go v0.4.51
)

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
replace github.com/quanlaihe/hvac-web/libs/sessionstore => ../../libs/sessionstore
