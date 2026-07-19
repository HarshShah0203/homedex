# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:22-bookworm-slim
ARG GO_IMAGE=golang:1.23-alpine
ARG SSH_IMAGE=debian:bookworm-slim
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM ${SSH_IMAGE} AS ssh-client
RUN apt-get update && \
    apt-get install -y --no-install-recommends openssh-client && \
    rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    root=/out/ssh; \
    mkdir -p "$root/usr/bin" "$root/etc/ssh/ssh_config.d" "$root/home/nonroot/.ssh" "$root/usr/share/doc"; \
    cp /usr/bin/ssh /usr/bin/ssh-keyscan "$root/usr/bin/"; \
    cp /etc/ssh/ssh_config "$root/etc/ssh/"; \
    printf 'passwd: files\ngroup: files\nshadow: files\nhosts: files dns\nnetworks: files\nprotocols: files\nservices: files\n' > "$root/etc/nsswitch.conf"; \
    find /etc/ssh/ssh_config.d -mindepth 1 -maxdepth 1 -exec cp -a -t "$root/etc/ssh/ssh_config.d" {} +; \
    { ldd /usr/bin/ssh; ldd /usr/bin/ssh-keyscan; } | \
      awk '/=> \// { print $3 } $1 ~ /^\// { print $1 }' | sort -u | \
      while read -r library; do cp -L --parents "$library" "$root"; done; \
    for library in /lib/*/libnss_files.so.2 /lib/*/libnss_dns.so.2; do \
      cp -L --parents "$library" "$root"; \
    done; \
    find /usr/share/doc -mindepth 2 -maxdepth 2 -name copyright -exec cp --parents {} "$root" \;; \
    chown -R 65532:65532 "$root/home/nonroot"; \
    chmod 0700 "$root/home/nonroot/.ssh"; \
    /usr/bin/ssh -V

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-base
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
COPY --from=ssh-client /out/ssh/ /
COPY --from=app-build --chown=nonroot:nonroot /out/homedex /homedex
COPY --from=app-build --chown=nonroot:nonroot /out/data /data
USER 65532:65532
WORKDIR /data
ENV HOMEDEX_DATA_DIR=/data \
    HOMEDEX_LISTEN=:7377 \
    HOME=/home/nonroot
EXPOSE 7377
VOLUME ["/data"]
ENTRYPOINT ["/homedex"]
