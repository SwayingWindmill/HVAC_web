# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src

COPY tools/eg8200-simulator/go.mod ./tools/eg8200-simulator/go.mod
COPY tools/eg8200-simulator ./tools/eg8200-simulator
WORKDIR /src/tools/eg8200-simulator
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/eg8200-simulator ./cmd/eg8200-simulator

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/eg8200-simulator /eg8200-simulator
USER 65532:65532
EXPOSE 19092
ENTRYPOINT ["/eg8200-simulator"]
