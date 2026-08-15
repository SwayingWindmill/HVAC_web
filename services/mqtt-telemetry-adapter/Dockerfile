# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src

COPY go.work go.work.sum ./
COPY libs ./libs
COPY services ./services
COPY tools ./tools
COPY contracts ./contracts

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/iot-service ./services/mqtt-telemetry-adapter/cmd/mqtt-telemetry-adapter

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/iot-service /iot-service
USER 65532:65532
EXPOSE 19094
ENTRYPOINT ["/iot-service"]
