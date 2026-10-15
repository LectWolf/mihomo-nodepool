# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/frontend/dist/ ./internal/web/dist/
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo.Version=$VERSION -X github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo.Commit=$COMMIT -X github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo.BuildTime=$BUILD_TIME" \
    -o /out/nodepool ./cmd/nodepool

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -g 1000 nodepool \
    && adduser -D -u 1000 -G nodepool nodepool \
    && mkdir -p /app/data \
    && chown -R nodepool:nodepool /app
WORKDIR /app
COPY --from=backend --chown=nodepool:nodepool /out/nodepool /app/nodepool
COPY deploy/docker-entrypoint.sh /usr/local/bin/nodepool-entrypoint
RUN chmod 0755 /usr/local/bin/nodepool-entrypoint
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="Mihomo NodePool" \
      org.opencontainers.image.source="https://github.com/LectWolf/mihomo-nodepool" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT
ENV HTTP_ADDR=:8080 DATA_DIR=/app/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/nodepool", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/nodepool-entrypoint"]
CMD ["/app/nodepool"]
