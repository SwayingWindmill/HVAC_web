# syntax=docker/dockerfile:1.7
# Local and source-deploy builds. Production apply-on-server.sh does not use this file:
# it copies the already-built binary onto the local alertmanager image so the server does not pull a Go toolchain.
ARG GO_BUILD_IMAGE=golang:1.27.3-bookworm
ARG GO_RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot
FROM ${GO_BUILD_IMAGE} AS build
ARG GO_PROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GO_PROXY}
ENV GOWORK=off
WORKDIR /src
COPY cmd/wecom-alert-adapter/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/wecom-alert-adapter .

FROM ${GO_RUNTIME_IMAGE}
COPY --from=build --chown=65532:65532 /out/wecom-alert-adapter /wecom-alert-adapter
USER 65532:65532
ENTRYPOINT ["/wecom-alert-adapter"]
