FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run typecheck && npm run build

FROM caddy:2.10.2-alpine
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/Daniel-Cpz/FlowForge" org.opencontainers.image.revision=$REVISION
COPY --from=frontend /src/web/dist /srv
COPY deploy/gateway/Caddyfile.private /etc/caddy/Caddyfile
# Caddy requires writable /data and /config, but serves no host sources.
EXPOSE 8080
