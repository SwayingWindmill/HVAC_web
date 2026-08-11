# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src/services/mqtt-telemetry-adapter
COPY services/mqtt-telemetry-adapter/go.mod services/mqtt-telemetry-adapter/go.sum ./
RUN go mod download
COPY services/mqtt-telemetry-adapter ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/mqtt-telemetry-adapter ./cmd/mqtt-telemetry-adapter

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/mqtt-telemetry-adapter /mqtt-telemetry-adapter
USER 65532:65532
EXPOSE 19094
ENTRYPOINT ["/mqtt-telemetry-adapter"]
