# simple-proxy

> 极简 HTTP 代理工具 —— **人控制开关**，不做智能分流。
>
> 需要访问被墙的境外服务时，用一条命令让某条命令走隧道；不开就直连。

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
| 代理变量残留在 shell 里 | 只注入子进程，你的 shell 永远干净 |
| 停止后留下僵尸进程 | 同一 shell 内 start+stop → `wait` 回收 |
| 含密钥的配置文件 644 | 一律 600 |
| 日志无限增长 | 按天轮转，默认保留 3 天 |

设计动机、方法与规则的完整推导见 **[docs/DESIGN.md](docs/DESIGN.md)**。

---

## 安装

```bash
git clone <this-repo> && cd simple-proxy
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
- `curl`、`python3`（用于生成运行时配置）
- `bash`（4.x+）

---

## 使用

### 命令

| 命令 | 说明 |
|---|---|
| `simple-proxy on <cmd> [args...]` | 让一条命令走代理；**一次性**，命令跑完自动停隧道 |
| `simple-proxy on --start <cmd> [args...]` | 同上，但**隧道常驻**（之后可继续 `on` 其它命令） |
| `simple-proxy off` | 关闭代理（停止 mihomo） |
| `simple-proxy status` | 查看状态 |
| `simple-proxy check` | 自检 / 诊断 |
| `simple-proxy logs [N]` | 查看最近 N 行路由决策日志 |
| `simple-proxy update <URL>` | 更新订阅配置 |
| `simple-proxy install` | 安装 / 初始化（可重复执行） |
| `simple-proxy uninstall` | 卸载（保留数据与配置） |

### shell 快捷命令（安装时写入 rc）

```bash
proxy-on  [cmd...]    # = simple-proxy on
proxy-off             # = simple-proxy off
proxy-status          # = simple-proxy status
```

### 行为预期

| 事项 | 预期 |
|---|---|
| 执行完命令后，当前 shell 有 `http_proxy` 吗？ | **没有**（只存在于该命令的子进程） |
| 当前工作目录会变吗？ | **不会** |
| 输出里会有 `[2] pid` 之类噪音吗？ | **不会** |
| 一次性模式命令跑完后 | mihomo 自动停止 |
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
| `SP_DNS_FALLBACKS` | 境外 DoH | 备用 DNS；**中国大陆网络建议改境内 DoH**（见下） |
| `SP_NO_PROXY_EXTRA` | 内网网段 | 客户端不经过代理的目标 |

所有项都可用同名环境变量临时覆盖。

### DNS 说明

订阅通常使用境外 DoH（`1.1.1.1` / `8.8.8.8`），在部分网络环境下**直连不通** → 域名解析整体失败。
本工具的处理：**首选系统解析器**（`/etc/resolv.conf`），再叠加备用解析器。

中国大陆网络请把备用解析器改成境内 DoH：

```bash
SP_DNS_FALLBACKS="https://doh.pub/dns-query https://dns.alidns.com/dns-query"
```

> 走代理的域名由**远端解析**（VLESS 传域名而非 IP），本地 DNS 污染不影响它们。

### 规则

规则文件：`~/.config/simple-proxy/rules.yaml`（**唯一需要手动改的文件**）

默认策略：**内网直连 + 其余全走隧道**

```yaml
rules:
  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve
  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
  - IP-CIDR6,fc00::/7,DIRECT,no-resolve
  - IP-CIDR6,fe80::/10,DIRECT,no-resolve
  - MATCH,PROXY
```

常见变体（改完执行一次 `simple-proxy on ...` 即生效）：

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
| 流量没走隧道 | 看 `logs`：若是 `using DIRECT` 而规则是 `MATCH,PROXY` → 节点选择缓存被改，删 `config.runtime.yaml` 旁的 `cache.db` 后重试 |
| `on` 报"等待端口超时" | `ss -ltn \| grep 7890` 看端口是否被占；看 `mihomo.log` 尾部 |
| 改了配置没生效 | 每次 `on` 都会重新生成运行时配置；若仍异常，确认进程用的是新配置（`simple-proxy status`） |
| 内部服务连不上 | 检查规则里的 `10.0.0.0/8 → DIRECT` 是否还在 |
| 命令改了没生效 | shell 缓存了旧函数 → `source ~/.local/share/simple-proxy/shell.sh` |
| 终端输出错乱（如 `ls` 显示成 `lsls`）| 与本工具无关：系统缺少当前 `TERM` 的 terminfo，见 `simple-proxy check` 的提示 |

完整排障手册见 **[docs/OPERATIONS.md](docs/OPERATIONS.md)**。

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
bash tests/smoke.sh      # 冒烟测试（不碰真实订阅、不改用户环境）
```

目录：

```
bin/simple-proxy        CLI
lib/core.sh             核心实现（启动/停止/状态/自检）
lib/build-config.py     配置合并器
lib/shell.sh            shell 集成片段
config/proxy.conf       配置模板
config/rules.yaml       规则模板
scripts/                日志轮转、订阅更新
docs/DESIGN.md          设计文档（动机/方法/规则）
docs/OPERATIONS.md      运维手册
tests/smoke.sh          冒烟测试
```

---

## License

MIT
