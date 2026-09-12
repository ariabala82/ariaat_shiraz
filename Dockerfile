FROM golang:1.22-alpine AS launcher-builder

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/launcher .

FROM alpine:3.22

RUN apk add --no-cache \
    ca-certificates \
    curl \
    tar \
    gzip \
    bash \
    tzdata \
    openssl \
    inotify-tools

WORKDIR /app

# Version files deliberately participate in Docker's cache key. Updating either
# file forces the corresponding upstream binary layer to rebuild.
COPY XUI_VERSION /tmp/XUI_VERSION
COPY CLOUDFLARED_VERSION /tmp/CLOUDFLARED_VERSION

# Install a stable 3x-ui release during IMAGE BUILD, not at container startup.
# This avoids the old /app/x-ui/x-ui runtime-download failure on Deplexo.
RUN set -eux; \
    version="$(tr -d '\r\n ' < /tmp/XUI_VERSION)"; \
    asset="x-ui-linux-amd64.tar.gz"; \
    pinned="https://github.com/MHSanaei/3x-ui/releases/download/${version}/${asset}"; \
    latest="https://github.com/MHSanaei/3x-ui/releases/latest/download/${asset}"; \
    if ! curl -fL --retry 8 --retry-all-errors --connect-timeout 20 --max-time 300 "$pinned" -o /tmp/xui.tar.gz; then \
      echo "Pinned 3x-ui download failed; falling back to latest stable release."; \
      curl -fL --retry 8 --retry-all-errors --connect-timeout 20 --max-time 300 "$latest" -o /tmp/xui.tar.gz; \
    fi; \
    tar -tzf /tmp/xui.tar.gz | grep -q '^x-ui/x-ui$'; \
    tar -xzf /tmp/xui.tar.gz -C /app; \
    test -f /app/x-ui/x-ui; \
    chmod 0755 /app/x-ui/x-ui; \
    if [ -d /app/x-ui/bin ]; then find /app/x-ui/bin -type f -exec chmod 0755 {} \;; fi; \
    rm -f /tmp/xui.tar.gz

# Optional Cloudflare Tunnel client, pinned for reproducible builds. If a pinned
# release ever disappears, fall back to Cloudflare's latest official binary.
RUN set -eux; \
    version="$(tr -d '\r\n ' < /tmp/CLOUDFLARED_VERSION)"; \
    pinned="https://github.com/cloudflare/cloudflared/releases/download/${version}/cloudflared-linux-amd64"; \
    latest="https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"; \
    if ! curl -fL --retry 8 --retry-all-errors --connect-timeout 20 --max-time 300 "$pinned" -o /usr/local/bin/cloudflared; then \
      echo "Pinned cloudflared download failed; falling back to latest release."; \
      curl -fL --retry 8 --retry-all-errors --connect-timeout 20 --max-time 300 "$latest" -o /usr/local/bin/cloudflared; \
    fi; \
    chmod 0755 /usr/local/bin/cloudflared

# 3x-ui expects these writable paths. /app/data is redirected to /tmp for the
# default SQLite mode. For real persistence across redeploys use PostgreSQL via
# XUI_DB_TYPE=postgres + XUI_DB_DSN, or point XUI_DB_FOLDER at a persistent mount.
RUN rm -rf /app/data && ln -s /tmp/xui-data /app/data

COPY --from=launcher-builder /out/launcher /usr/local/bin/launcher
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/launcher /usr/local/bin/entrypoint.sh

ENV XUI_BIN_FOLDER=/tmp/xui-bin \
    XUI_ENABLE_FAIL2BAN=false \
    XUI_SKIP_HSTS=true \
    XUI_LOG_LEVEL=warning

EXPOSE 2053

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
