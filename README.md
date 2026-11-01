# proxrun

> **Run commands through a proxy, on demand.**
>
> 按需让命令走代理，用完自动收回。

**proxrun** 取自 **proxy + run**，是基于 mihomo 的轻量命令行 HTTP 代理工具。
由用户决定哪些命令走代理，默认执行完自动关闭隧道，也支持显式常驻和多 shell 共享。
内网直连，代理变量只传给指定命令及其子进程。

项目名和仓库目录为 `proxrun`；当前 CLI 名称及安装目录沿用 `simple-proxy`，快捷命令为 `proxy-on`、`po`、`proxy-off` 和 `proxy-status`。

```
simple-proxy on curl https://api.openai.com/v1/models   # 一次性：跑完自动收隧道
simple-proxy on --start codex                            # 常驻：隧道留着可连续用
simple-proxy off                                         # 关闭
simple-proxy status                                      # 状态
```

---

## 为什么做这个

在共享 GPU 集群的 CPU 容器里开发时，经常需要访问 `api.openai.com` 这类被墙的服务。
市面上的方案（完整 Clash / clash-for-linux-install）通常带来一堆用不上的东西：
systemd 集成、TUI、Web 面板、订阅管理、shell 集成脚本……而且**不好控**：

| 常见问题 | 本工具的做法 |
|---|---|
| 不知道什么流量在走隧道 | 规则只有一条意图：**内网直连 + 其余全走** |
| 一堆开关，语义重叠 | 只有 3 个命令 |
| 每次执行都刷一屏提示 | **静默**：输出里只有被执行命令自己的内容 |
| 执行完工作目录被改掉 | 启动后立刻还原 cwd |
| 代理变量残留在 shell 里 | 只注入指定命令及其子进程 |
| 停止后留下僵尸进程 | 一次性启动者回收；shell 常驻模式保留可回收的父进程 |
| 含密钥的配置文件 644 | 一律 600 |
| 日志无限增长 | 按天轮转，默认保留 3 天 |

开发者可参阅 [AGENTS.md](AGENTS.md) 了解代码结构和开发约定。

---

## 安装

```bash
git clone <this-repo> proxrun
cd proxrun
bash install.sh --subscription 'https://your-subscription-url'
```

或分步：

```bash
bash install.sh                          # 安装内核与命令
simple-proxy update '<订阅URL>'          # 填订阅
simple-proxy check                       # 自检
```

安装内容：

```
~/.local/bin/mihomo                 代理内核（mihomo）
~/.local/bin/simple-proxy           主命令
~/.local/share/simple-proxy/        数据：订阅配置、运行时配置、日志、GeoIP 库
~/.config/simple-proxy/             配置：proxy.conf、rules.yaml
~/.zshrc 或 ~/.bashrc               shell 集成（可选，提供 proxy-on/off/status）
```

> 想不修改 rc 文件：`bash install.sh --no-shell`

### 依赖

- Linux / macOS，x86_64 或 arm64
- `curl`、`python3`（3.9+，用于生成配置和管理进程）
- `PyYAML`（安装脚本在缺失时通过 pip 安装到持久数据目录；离线安装需预先准备）
- `bash`（4.x+）

---

## 使用

### 命令

| 命令 | 说明 |
|---|---|
| `simple-proxy on <cmd> [args...]` | 让一条命令走代理；**一次性**，命令跑完自动停隧道 |
| `simple-proxy on --start <cmd> [args...]` | 同上，但**隧道常驻**（之后的普通 `on` 不会关闭它） |
| `simple-proxy off` | 关闭代理（停止 mihomo） |
| `simple-proxy status` | 查看状态 |
| `simple-proxy check` | 自检 / 诊断 |
| `simple-proxy logs [N]` | 查看最近 N 行路由决策日志 |
| `simple-proxy update <URL>` | 更新订阅配置 |
| `simple-proxy install` | 安装 / 初始化（可重复执行） |
| `simple-proxy uninstall` | 卸载（保留数据与配置） |


并行的普通 `on` 共享一个隧道，直到最后一个命令完成才关闭。启动内核的 CLI 会等待共享使用者结束并回收内核，因此它可能稍晚于自己的命令返回。显式 `off` 会关闭所有使用者正在共享的隧道。

在 PID 1 不回收孤儿进程的容器中，常驻模式建议使用 `proxy-on --start <cmd>`，并在同一 shell 中用 `proxy-off` 关闭；shell 会保留并回收内核的父进程。直接运行独立 CLI 的 `on --start`，或常驻后退出原 shell，仍依赖系统 init 回收孤儿进程；彻底解决需要容器启用 tini/dumb-init。

升级旧安装前，先用旧版 `proxy-off` 停止进程。新版不会凭旧格式 PID 文件杀进程，避免误杀重用该 PID 的其他程序。

### shell 快捷命令（安装时写入 rc）

```bash
proxy-on  [cmd...]    # = simple-proxy on
po        [cmd...]    # = proxy-on，支持相同参数（包括 --start）
proxy-off             # = simple-proxy off
proxy-status          # = simple-proxy status
```

### 行为预期

| 事项 | 预期 |
|---|---|
| 执行完命令后，当前 shell 有 `http_proxy` 吗？ | **没有**（只存在于该命令的子进程） |
| 当前工作目录会变吗？ | **不会** |
| 输出里会有 `[2] pid` 之类噪音吗？ | **不会** |
| 一次性模式命令跑完后 | 最后一个共享命令结束后，mihomo 自动停止 |
| `--start` 模式下 shell 退出后 | mihomo 继续运行（常驻语义） |

---

## 配置

配置文件：`~/.config/simple-proxy/proxy.conf`

| 项 | 默认 | 说明 |
|---|---|---|
| `SP_HTTP_PORT` | `7890` | HTTP 代理端口（只开 HTTP，不开 SOCKS） |
| `SP_LOG_KEEP_DAYS` | `3` | 日志保留天数 |
| `SP_MIHOMO_VERSION` | `1.19.33` | 安装时下载的内核版本 |
| `SP_ENABLE_API` | `0` | 是否开启 mihomo 控制 API（127.0.0.1:9090） |
| `SP_DNS_FALLBACKS` | 腾讯 / 阿里 DoH | 系统解析器之外的备用 DNS |
| `SP_NO_PROXY_EXTRA` | 内网网段 | 客户端不经过代理的目标 |

所有项都可用同名环境变量临时覆盖，优先级为：环境变量 > 配置文件 > 默认值。

### DNS 说明

订阅通常使用境外 DoH（`1.1.1.1` / `8.8.8.8`），在部分网络环境下**直连不通** → 域名解析整体失败。
本工具的处理：**首选系统解析器**（`/etc/resolv.conf`），再叠加备用解析器。

默认备用解析器为境内 DoH，可在配置中修改：

```bash
SP_DNS_FALLBACKS="https://doh.pub/dns-query https://dns.alidns.com/dns-query"
```

> 内网 IP 规则会先解析域名以判断是否需要直连；命中代理后，VLESS 可将域名交给远端解析。因此本地 DNS 仍会影响路由判断。

### 规则

规则文件：`~/.config/simple-proxy/rules.yaml`（**唯一需要手动改的文件**）

默认策略：**内网直连 + 其余全走隧道**

```yaml
rules:
  - IP-CIDR,127.0.0.0/8,DIRECT
  - IP-CIDR,10.0.0.0/8,DIRECT
  - IP-CIDR,172.16.0.0/12,DIRECT
  - IP-CIDR,192.168.0.0/16,DIRECT
  - IP-CIDR6,fc00::/7,DIRECT
  - IP-CIDR6,fe80::/10,DIRECT
  - MATCH,PROXY
```

常见变体（改完先 `off`，下次 `on` 启动时生效）：

```yaml
# A) 国内站点直连
  - GEOIP,CN,DIRECT
  - MATCH,PROXY

# B) 只让指定域名走隧道
  - DOMAIN-SUFFIX,openai.com,PROXY
  - DOMAIN-SUFFIX,anthropic.com,PROXY
  - MATCH,DIRECT
```

> ⚠️ **内网直连规则是安全底线**。把内网流量送进隧道会导致集群内 API、镜像仓库、K8s 服务全部失联。

---

## 排障

```bash
simple-proxy check                  # 自检
simple-proxy logs 50                # 最近的路由决策
```

`logs` 的输出形如：

```
[TCP] 127.0.0.1:xxx --> api.openai.com:443 match Match using PROXY[<node>]     ← 走隧道
[TCP] 127.0.0.1:xxx --> registry.local:443 match IPCIDR(10.0.0.0/8) using DIRECT ← 内网直连
```

| 症状 | 排查 |
|---|---|
| 流量没走隧道 | 看 `logs` 的匹配规则；运行配置会重建 PROXY 组并关闭节点选择缓存，订阅不能把默认 PROXY 设为 DIRECT |
| `on` 报"等待端口超时" | `ss -ltn \| grep 7890` 看端口是否被占；看 `mihomo.log` 尾部 |
| 改了配置没生效 | 仅启动新隧道时生成配置；复用进程不会重载，需先 `off` 再 `on` |
| 内部服务连不上 | 检查规则里的 `10.0.0.0/8 → DIRECT` 是否还在 |
| 命令改了没生效 | shell 缓存了旧函数 → `source ~/.local/share/simple-proxy/shell.sh` |
| 终端输出错乱（如 `ls` 显示成 `lsls`）| 与本工具无关：系统缺少当前 `TERM` 的 terminfo，见 `simple-proxy check` 的提示 |

排障时优先结合 `simple-proxy check` 和 `simple-proxy logs` 检查进程、端口及路由决策。

---

## 架构

```
用户命令
   │ 注入 http_proxy/https_proxy（仅子进程）
   ↓
mihomo (127.0.0.1:7890, 仅 HTTP)
   │ 规则：内网 IP-CIDR → DIRECT；其余 MATCH → PROXY
   │ DNS：系统解析器优先 + 备用 DoH
   ↓ VLESS/Reality 隧道
境外目标
```

配置文件流水线：

```
订阅 URL ──(update-subscription.sh)──► config.yaml          (600, 可被覆盖)
                                          │
rules.yaml ───────────────────────────────┤
                                          │ build-config.py
                                          ▼
                              config.runtime.yaml            (600, mihomo 实际加载)
                              = 订阅 + 你的规则 + DNS 修正 + 端口收窄
```

---

## 开发

```bash
python3 -m pip install -r requirements.txt  # 本地开发依赖
bash tests/smoke.sh                         # 隔离回归测试
SP_TEST_MIHOMO=/path/to/mihomo bash tests/smoke.sh  # 加跑真实内核验证
```

目录：

```
bin/simple-proxy        CLI
lib/core.sh             配置加载 / 自检
lib/runtime.py          进程身份 / 使用者登记 / 启停与信号清理
lib/build-config.py     配置合并器
lib/shell.sh            shell 集成片段
config/proxy.conf       配置模板
config/rules.yaml       规则模板
scripts/                日志轮转、订阅更新
AGENTS.md              项目结构与开发约定
tests/smoke.sh          冒烟测试
```

---

## License

MIT
