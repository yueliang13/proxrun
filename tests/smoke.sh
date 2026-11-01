#!/usr/bin/env bash
# 所有测试均使用临时 HOME、随机端口和假订阅。
set -euo pipefail
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec python3 "$REPO_DIR/tests/test_proxy.py" "$@"
