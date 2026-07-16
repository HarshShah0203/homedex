# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:22-bookworm-slim
ARG GO_IMAGE=golang:1.23-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${NODE_IMAGE} AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM ${GO_IMAGE} AS go-base
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
COPY --from=web-build /src/internal/server/static/ ./internal/server/static/

FROM go-base AS app-build
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${TARGETVARIANT#v}" \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/homedex ./cmd/homedex && \
    mkdir -p /out/data

FROM go-base AS seed-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${TARGETVARIANT#v}" \
    go build -trimpath -buildvcs=false -ldflags="-s -w" \
      -o /out/homedex-seed ./demo/seed && \
    mkdir -p /out/data

FROM ${RUNTIME_IMAGE} AS demo-seed
COPY --from=seed-build --chown=nonroot:nonroot /out/homedex-seed /homedex-seed
COPY --from=seed-build --chown=nonroot:nonroot /out/data /data
USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["/homedex-seed"]

FROM ${RUNTIME_IMAGE} AS runtime
ARG VERSION=dev
LABEL org.opencontainers.image.title="Homedex" \
      org.opencontainers.image.description="Read-only homelab inventory" \
      org.opencontainers.image.source="https://github.com/HarshShah0203/homedex" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=app-build --chown=nonroot:nonroot /out/homedex /homedex
COPY --from=app-build --chown=nonroot:nonroot /out/data /data
USER 65532:65532
WORKDIR /data
ENV HOMEDEX_DATA_DIR=/data \
    HOMEDEX_LISTEN=:7377
EXPOSE 7377
VOLUME ["/data"]
ENTRYPOINT ["/homedex"]
