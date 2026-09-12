#!/bin/sh
set -eu

DATA_DIR="${XUI_DB_FOLDER:-/app/data}"
LOG_DIR="${XUI_LOG_FOLDER:-${DATA_DIR}/logs}"
BIN_DIR="${XUI_BIN_FOLDER:-/tmp/xui-bin}"

# Deplexo's runtime filesystem is read-only in the image layer. Keep only the
# files that must be writable under /tmp and symlink the large immutable geo/xray
# assets from the image to avoid exhausting the small writable filesystem.
if [ "$DATA_DIR" = "/app/data" ]; then
    mkdir -p /tmp/xui-data /tmp/xui-data/logs
else
    mkdir -p "$DATA_DIR" "$LOG_DIR"
fi
rm -rf "$BIN_DIR"
mkdir -p "$BIN_DIR"

for f in /app/x-ui/bin/*; do
    [ -e "$f" ] || continue
    ln -sf "$f" "$BIN_DIR/$(basename "$f")"
done

# 3x-ui can replace the Xray symlink with a newly downloaded regular file when
# the Xray Update button is used. Some releases create that file without +x on
# PaaS filesystems. Fix its mode immediately after create/move/close events.
(
    while :; do
        inotifywait -qq -e create -e close_write -e moved_to -e attrib "$BIN_DIR" 2>/dev/null || true
        for x in "$BIN_DIR"/xray*; do
            [ -e "$x" ] || continue
            chmod 0755 "$x" 2>/dev/null || true
        done
    done
) &

# Optional Cloudflare Tunnel. Set CLOUDFLARE_TUNNEL_TOKEN in Deplexo to enable.
# It is intentionally independent from the normal Deplexo/custom-domain path,
# so it can be used as a backup route.
if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ]; then
    echo "Cloudflare Tunnel enabled."
    (
        while :; do
            /usr/local/bin/cloudflared tunnel --no-autoupdate run \
                --token "$CLOUDFLARE_TUNNEL_TOKEN" || true
            echo "cloudflared stopped; retrying in 5 seconds..."
            sleep 5
        done
    ) &
else
    echo "Cloudflare Tunnel disabled (CLOUDFLARE_TUNNEL_TOKEN is empty)."
fi

exec /usr/local/bin/launcher
