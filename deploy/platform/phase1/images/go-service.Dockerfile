# syntax=docker/dockerfile:1.7
ARG GO_BUILD_IMAGE=golang:1.25.12-bookworm
ARG GO_RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot
FROM ${GO_BUILD_IMAGE} AS build
ARG GO_PROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GO_PROXY}
WORKDIR /src

COPY go.work go.work.sum ./
COPY cmd ./cmd
COPY libs ./libs
COPY modules ./modules
COPY services ./services
COPY tools ./tools
COPY contracts ./contracts

ARG SERVICE_PACKAGE
RUN test -n "$SERVICE_PACKAGE"
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/service "$SERVICE_PACKAGE"
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/healthcheck ./tools/container-healthcheck/cmd/container-healthcheck/main.go

FROM ${GO_RUNTIME_IMAGE}
WORKDIR /
COPY --from=build --chown=65532:65532 /out/service /service
COPY --from=build --chown=65532:65532 /out/healthcheck /healthcheck
USER 65532:65532
ENTRYPOINT ["/service"]
