#!/usr/bin/env bash
# 测试仅使用临时 HOME、随机端口和假订阅。
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"
exec go test ./... "$@"
