# syntax=docker/dockerfile:1.7
ARG NODE_IMAGE=node:22.22.0-bookworm-slim
FROM ${NODE_IMAGE} AS build
WORKDIR /src
COPY services/operations-agent-service/package.json services/operations-agent-service/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY services/operations-agent-service/tsconfig.json services/operations-agent-service/tsconfig.build.json ./
COPY services/operations-agent-service/src ./src
RUN npm run build && npm prune --omit=dev --no-audit --no-fund

FROM ${NODE_IMAGE}
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build --chown=node:node /src/package.json ./package.json
COPY --from=build --chown=node:node /src/node_modules ./node_modules
COPY --from=build --chown=node:node /src/dist ./dist
USER node
ENTRYPOINT ["node", "dist/bootstrap/main.js"]
