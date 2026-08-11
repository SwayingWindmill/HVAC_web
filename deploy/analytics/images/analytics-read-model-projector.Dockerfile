# syntax=docker/dockerfile:1.7
FROM golang:1.25.12-bookworm AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY libs ./libs
COPY services ./services
COPY tools ./tools
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/analytics-read-model-projector ./services/analytics-read-model-projector/cmd/analytics-read-model-projector

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build --chown=65532:65532 /out/analytics-read-model-projector /analytics-read-model-projector
USER 65532:65532
EXPOSE 19089
ENTRYPOINT ["/analytics-read-model-projector"]
