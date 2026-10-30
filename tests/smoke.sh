#!/usr/bin/env bash
# simple-proxy · 冒烟测试（不触碰真实订阅、不改动用户环境）
#
#   bash tests/smoke.sh
#
# 用临时目录模拟一个完整的安装 → 运行 → 停止流程。
set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
cleanup() {
  # 结束假内核（它会 sleep 20s，占着端口）
  if [[ -f "$SP_DATA_DIR/mihomo.pid" ]]; then
    pkill -P "$(cat "$SP_DATA_DIR/mihomo.pid" 2>/dev/null)" 2>/dev/null
    kill "$(cat "$SP_DATA_DIR/mihomo.pid" 2>/dev/null)" 2>/dev/null
  fi
  [[ -f "$SP_DATA_DIR/fake-kernel.pid" ]] && kill "$(cat "$SP_DATA_DIR/fake-kernel.pid")" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

export SP_BIN_DIR="$TMP/bin"
export SP_DATA_DIR="$TMP/data"
export SP_CONFIG_DIR="$TMP/config"

PASS=0 FAIL=0
ok()   { printf '  ✓ %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  ✗ %s\n' "$*"; FAIL=$((FAIL+1)); }

echo "── simple-proxy 冒烟测试 ──"

# 1. 安装（不下载内核、不写 shell 集成）
bash "$REPO_DIR/install.sh" --no-shell --no-download >/dev/null 2>&1
[[ -x "$SP_BIN_DIR/simple-proxy" ]] && ok "安装：主命令" || bad "安装：主命令"
[[ -f "$SP_CONFIG_DIR/proxy.conf" ]] && ok "安装：配置文件" || bad "安装：配置文件"
[[ -f "$SP_CONFIG_DIR/rules.yaml" ]] && ok "安装：规则文件" || bad "安装：规则文件"
[[ -f "$SP_DATA_DIR/build-config.py" ]] && ok "安装：配置生成器" || bad "安装：配置生成器"

# 2. 造一份最小可用订阅配置（含一个假节点，不需要真实密钥）
cat > "$SP_DATA_DIR/config.yaml" <<'YAML'
mixed-port: 7890
external-controller: 127.0.0.1:9090
allow-lan: false
mode: rule
log-level: info
dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  default-nameserver:
    - 223.5.5.5
    - 1.1.1.1
  nameserver:
    - https://1.1.1.1/dns-query
    - https://8.8.8.8/dns-query
proxies:
  - name: "TEST-NODE"
    type: socks5
    server: 127.0.0.1
    port: 1080
proxy-groups:
  - name: "PROXY"
    type: select
    proxies:
      - "TEST-NODE"
      - DIRECT
rules:
  - MATCH,DIRECT
YAML
chmod 600 "$SP_DATA_DIR/config.yaml"

# 3. 配置生成：订阅 + 规则 → 运行时配置
python3 "$SP_DATA_DIR/build-config.py" \
  --subscription-config "$SP_DATA_DIR/config.yaml" \
  --rules "$SP_CONFIG_DIR/rules.yaml" \
  --output "$SP_DATA_DIR/config.runtime.yaml" \
  --dns-fallbacks "https://1.1.1.1/dns-query" \
  --http-port 7890 --enable-api 0 --data-dir "$SP_DATA_DIR" >/dev/null 2>&1
RT="$SP_DATA_DIR/config.runtime.yaml"
[[ -f "$RT" ]] && ok "生成：运行时配置" || bad "生成：运行时配置"
grep -q '^port: 7890$' "$RT"                    && ok "收窄：mixed-port → port" || bad "收窄：mixed-port → port"
grep -q '^# external-controller' "$RT"          && ok "收窄：控制 API 已关闭" || bad "收窄：控制 API 已关闭"
grep -q 'MATCH,PROXY' "$RT"                     && ok "规则：用户规则已注入" || bad "规则：用户规则已注入"
grep -qE '^[[:space:]]*-[[:space:]]*MATCH,DIRECT[[:space:]]*$' "$RT" && bad "规则：订阅旧规则未清除" || ok "规则：订阅旧规则已清除"
[[ "$(stat -c '%a' "$RT")" == "600" ]]           && ok "权限：运行时配置 600" || bad "权限：运行时配置 600"
grep -q 'doh.pub\|10\.43\.0\.10\|1\.1\.1\.1' "$RT" && ok "DNS：已重写" || bad "DNS：已重写"

# 4. 命令行为
"$SP_BIN_DIR/simple-proxy" status  >/dev/null 2>&1 && ok "命令：status" || bad "命令：status"
check_out="$("$SP_BIN_DIR/simple-proxy" check 2>&1)"
echo "$check_out" | grep -q '自检' && ok "命令：check" || bad "命令：check"
out="$("$SP_BIN_DIR/simple-proxy" on 2>&1)"; rc=$?
[[ $rc -ne 0 ]] && ok "命令：on 无参数 → 报用法" || bad "命令：on 无参数 → 报用法"
echo "$out" | grep -q '用法' && ok "命令：用法提示" || bad "命令：用法提示"

# 5. 端到端（用假内核验证 start/stop 流程）
cat > "$SP_BIN_DIR/mihomo" <<'SH'
#!/usr/bin/env bash
# 假内核：只监听端口，不做代理
if [[ "$1" == "-v" || "$1" == "--version" ]]; then echo "fake-mihomo v0"; exit 0; fi
# -d <data-dir>
for ((a=1; a<=$#; a++)); do
  if [[ "${!a}" == "-d" ]]; then
    n=$((a+1)); echo $$ > "${!n}/fake-kernel.pid"
  fi
done
python3 -c "
import socket, time
# SP_FAKE_KERNEL_MARKER
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', 7890)); s.listen(8); time.sleep(20)
"
SH
chmod +x "$SP_BIN_DIR/mihomo"
# 用 --start 让隧道常驻，便于检查状态（一次性模式会在命令结束时自动收）
"$SP_BIN_DIR/simple-proxy" on --start sleep 2 >/dev/null 2>&1
sleep 1
"$SP_BIN_DIR/simple-proxy" status 2>/dev/null | grep -q '运行中' && ok "端到端：启动成功" || bad "端到端：启动成功"
"$SP_BIN_DIR/simple-proxy" off >/dev/null 2>&1
sleep 1
"$SP_BIN_DIR/simple-proxy" status 2>/dev/null | grep -q '未运行' && ok "端到端：停止成功" || bad "端到端：停止成功"

echo ""
printf '结果: %d 通过 / %d 失败\n' "$PASS" "$FAIL"
[[ $FAIL -eq 0 ]]
