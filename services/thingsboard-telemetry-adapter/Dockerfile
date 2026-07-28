# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src

COPY services/thingsboard-telemetry-adapter/go.mod ./services/thingsboard-telemetry-adapter/go.mod
COPY libs/testpki ./libs/testpki
COPY services/thingsboard-telemetry-adapter ./services/thingsboard-telemetry-adapter
WORKDIR /src/services/thingsboard-telemetry-adapter
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/thingsboard-telemetry-adapter ./cmd/thingsboard-telemetry-adapter

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/thingsboard-telemetry-adapter /thingsboard-telemetry-adapter
USER 65532:65532
EXPOSE 19093
ENTRYPOINT ["/thingsboard-telemetry-adapter"]
