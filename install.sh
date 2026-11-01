#!/usr/bin/env bash
# 显式构建并安装；日常修改源码或 make build 不会更新已安装版本。
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_DIR"
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -o dist/proxrun ./cmd/proxrun
exec "$REPO_DIR/dist/proxrun" install "$@"
