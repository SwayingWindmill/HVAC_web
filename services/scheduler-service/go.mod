module github.com/quanlaihe/hvac-web/services/scheduler-service

go 1.25.12

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/observability => ../../libs/observability
