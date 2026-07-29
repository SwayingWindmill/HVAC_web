# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY libs ./libs
COPY services ./services
COPY tools ./tools
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/telemetry-history-projector ./services/telemetry-runtime-service/cmd/telemetry-history-projector

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/telemetry-history-projector /telemetry-history-projector
USER 65532:65532
EXPOSE 19087
ENTRYPOINT ["/telemetry-history-projector"]
