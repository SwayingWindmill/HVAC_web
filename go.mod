module github.com/quanlaihe/hvac-web

go 1.27.4

ignore (
	./apps
	./build
	./cd
	./contracts
	./deploy/acceptance
	./deploy/analytics
	./deploy/observability
	./deploy/platform/phase1/backup
	./deploy/platform/phase1/clickhouse
	./deploy/platform/phase1/environments
	./deploy/platform/phase1/images
	./deploy/platform/phase1/migrations
	./deploy/platform/phase1/nginx
	./deploy/platform/phase1/observability
	./deploy/platform/phase1/postgres
	./deploy/platform/phase1/recovery
	./deploy/platform/phase1/runtime
	./deploy/sites
	./dist
	./dist-frontend-review
	./docs
	./edge
	./hvac-backend
	./infra
	./node_modules
	./out
	./prototype
	./runtimes
	./scripts
	./semantic
	./services/operations-agent-service
	./tools/promo-video
	./worktrees
)

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.51.0
	github.com/eclipse/paho.golang v0.23.0
	github.com/go-jose/go-jose/v4 v4.1.6
	github.com/jackc/pgx/v5 v5.9.2
	github.com/pquerna/otp v1.4.0
	github.com/quanlaihe/hvac-web/libs/alarmexpr v0.0.0
	github.com/quanlaihe/hvac-web/libs/commandmodel v0.0.0
	github.com/quanlaihe/hvac-web/libs/edgecontrol v0.0.0
	github.com/quanlaihe/hvac-web/libs/grant v0.0.0
	github.com/quanlaihe/hvac-web/libs/identitycontext v0.0.0
	github.com/quanlaihe/hvac-web/libs/observability v0.0.0
	github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel v0.0.0
	github.com/redis/go-redis/v9 v9.21.0
	github.com/simonvetter/modbus v1.6.4
	golang.org/x/crypto v0.58.0
	golang.org/x/sync v0.24.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/ClickHouse/ch-go v0.75.0 // indirect
	github.com/andybalholm/brotli v1.2.7 // indirect
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.8.0 // indirect
	github.com/goburrow/serial v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20240701034025-d67f41855da4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/paulmach/orb v0.14.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.6.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/quanlaihe/hvac-web/libs/alarmexpr => ./libs/alarmexpr

replace github.com/quanlaihe/hvac-web/libs/commandmodel => ./libs/commandmodel

replace github.com/quanlaihe/hvac-web/libs/edgecontrol => ./libs/edgecontrol

replace github.com/quanlaihe/hvac-web/libs/grant => ./libs/grant

replace github.com/quanlaihe/hvac-web/libs/identitycontext => ./libs/identitycontext

replace github.com/quanlaihe/hvac-web/libs/observability => ./libs/observability

replace github.com/quanlaihe/hvac-web/libs/telemetryhistorymodel => ./libs/telemetryhistorymodel
