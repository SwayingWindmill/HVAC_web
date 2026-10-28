module github.com/quanlaihe/hvac-web/libs/energyseries

go 1.27.3

require (
	github.com/quanlaihe/hvac-web/libs/analyticsmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/workloadtls v0.0.0
)

replace (
	github.com/quanlaihe/hvac-web/libs/analyticsmodel => ../analyticsmodel
	github.com/quanlaihe/hvac-web/libs/workloadtls => ../workloadtls
)
