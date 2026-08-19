# syntax=docker/dockerfile:1.7
FROM node:22.22.0-bookworm-slim AS web-build
WORKDIR /src
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY apps/hvac-web ./apps/hvac-web
ARG HVAC_WEB_BUILD_ID=phase1-local
ARG HVAC_WEB_GATEWAY_BASE_PATH=
ARG HVAC_WEB_REALTIME_PROTOCOL=centrifugo-v1
ENV HVAC_WEB_BUILD_ID=${HVAC_WEB_BUILD_ID}
ENV HVAC_WEB_GATEWAY_BASE_PATH=${HVAC_WEB_GATEWAY_BASE_PATH}
ENV HVAC_WEB_REALTIME_PROTOCOL=${HVAC_WEB_REALTIME_PROTOCOL}
RUN npm run build:real

FROM nginx:1.27.4-alpine
COPY deploy/platform/phase1/nginx/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/apps/hvac-web/dist/real /usr/share/nginx/html
EXPOSE 443
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget --no-check-certificate -qO- https://127.0.0.1/healthz || exit 1
