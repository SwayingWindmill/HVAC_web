# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY cmd ./cmd
COPY libs ./libs
COPY services ./services
COPY tools ./tools
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/telemetry-runtime ./cmd/telemetry-worker

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/telemetry-runtime /telemetry-runtime
USER 65532:65532
EXPOSE 8443 19086
ENTRYPOINT ["/telemetry-runtime"]
