#!/bin/bash
set -e

echo "==> Installing dependencies..."

apt-get update
apt-get install -y curl wget git ca-certificates

echo "==> Installing Go dependencies..."
go mod download

echo "==> Installation completed."
