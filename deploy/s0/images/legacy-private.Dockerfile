# syntax=docker/dockerfile:1.7
FROM node:20.19.4-bookworm-slim AS build
WORKDIR /src
COPY hvac-backend/package.json hvac-backend/package-lock.json ./
RUN npm ci
COPY hvac-backend/ ./
RUN npm run build

FROM node:20.19.4-bookworm-slim AS production-dependencies
WORKDIR /app
COPY hvac-backend/package.json hvac-backend/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

FROM node:20.19.4-bookworm-slim
ENV NODE_ENV=production LEGACY_PRIVATE_MODE=true
WORKDIR /app
RUN groupadd --system --gid 10001 hvac && useradd --system --uid 10001 --gid hvac --home-dir /nonexistent hvac
COPY --from=production-dependencies --chown=10001:10001 /app/node_modules ./node_modules
COPY --from=build --chown=10001:10001 /src/dist ./dist
COPY --from=build --chown=10001:10001 /src/package.json ./package.json
USER 10001:10001
ENTRYPOINT ["node", "dist/main.js"]
