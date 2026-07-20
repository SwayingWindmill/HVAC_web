module github.com/quanlaihe/hvac-web/libs/sessionstore

go 1.22.5

require (
	github.com/jackc/pgx/v5 v5.7.6
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/sessionevent v0.0.0
)

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ../identitycontext
replace github.com/quanlaihe/hvac-web/libs/observability => ../observability
replace github.com/quanlaihe/hvac-web/libs/sessionevent => ../sessionevent
