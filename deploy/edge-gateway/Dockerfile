# syntax=docker/dockerfile:1.7
FROM golang:1.27.4-bookworm AS build
WORKDIR /src

COPY go.mod go.sum go.work go.work.sum ./
COPY cmd ./cmd
COPY libs ./libs
COPY modules ./modules
COPY services ./services
COPY tools ./tools
COPY contracts ./contracts

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/edge-gateway ./cmd/edge-gateway

FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=build /out/edge-gateway /edge-gateway
ENTRYPOINT ["/edge-gateway"]
