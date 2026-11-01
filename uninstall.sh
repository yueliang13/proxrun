#!/usr/bin/env bash
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${PROXRUN_BIN_DIR:-${SP_BIN_DIR:-$HOME/.local/bin}}/proxrun"
if [[ ! -x "$BINARY" ]]; then BINARY="$REPO_DIR/dist/proxrun"; fi
if [[ ! -x "$BINARY" ]]; then
  echo 'proxrun: 找不到已安装或已构建的 proxrun' >&2
  exit 1
fi
exec "$BINARY" uninstall "$@"
