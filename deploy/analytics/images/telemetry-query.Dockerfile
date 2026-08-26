# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY libs ./libs
COPY modules ./modules
COPY services ./services
COPY tools ./tools
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/telemetry-query ./modules/telemetry/cmd/telemetry-query-owner

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/telemetry-query /telemetry-query
USER 65532:65532
EXPOSE 18447 19087
ENTRYPOINT ["/telemetry-query"]
