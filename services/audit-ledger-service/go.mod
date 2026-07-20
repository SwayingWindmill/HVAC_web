module github.com/quanlaihe/hvac-web/services/audit-ledger-service

go 1.22.5

require (
	github.com/jackc/pgx/v5 v5.7.6
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionevent v0.0.0
	github.com/segmentio/kafka-go v0.4.51
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../../libs/identitycontext
replace github.com/quanlaihe/hvac-web/libs/sessionevent => ../../libs/sessionevent
