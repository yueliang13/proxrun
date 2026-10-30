# simple-proxy 设计文档

> 一个"人控制开关"的极简 HTTP 代理工具：在共享 GPU 集群的 CPU 开发容器里，
> 需要访问被墙的境外服务时，用一条命令让某条命令走隧道，不开就直连。

| 项目 | 值 |
|---|---|
| 状态 | 已实现并投入使用 |
| 内核 | mihomo (MetaCubeX) v1.19.33，官方静态二进制 |
| 出口 | 单节点 VLESS+Reality，境外节点（具体地址见订阅，不在本文档记录）|
| 安装位置 | `~/.local/bin/mihomo`、`~/.local/share/mihomo/` |
| 命令 | `simple-proxy on/off/status/check/logs/update/install/uninstall`（+ shell 快捷命令 `proxy-on/off/status`）|
| 仓库 | `simple-proxy/`（`bin/ lib/ config/ scripts/ docs/ tests/`）|
| 安装 | `bash install.sh --subscription '<URL>'` |

---

## 目录

1. [设计动机](#1-设计动机)
2. [设计目标与非目标](#2-设计目标与非目标)
3. [设计原则](#3-设计原则)
4. [架构](#4-架构)
5. [命令语义](#5-命令语义)
6. [规则设计](#6-规则设计)
7. [实现要点](#7-实现要点)
8. [踩过的坑与教训](#8-踩过的坑与教训)
9. [已知限制](#9-已知限制)
10. [扩展方向](#10-扩展方向)

---

## 1. 设计动机

### 1.1 起点：一次"想用代理"的需求

在集群 CPU 容器里开发时，需要访问 `api.openai.com` 等被墙的服务。平台本身不提供出口，
而自己装一整套 Clash（`nelvko/clash-for-linux-install`）会带来一堆用不上的东西：
systemd 集成、TUI、Web 面板、订阅管理、shell 集成……

### 1.2 平台环境带来的约束

| 约束 | 来源 | 对设计的影响 |
|---|---|---|
| 容器 PID 1 是 `sleep infinity`，不是真 init | K8s 镜像 | 孤儿进程死后永久变僵尸，PID 1 不回收 |
| `/workspace`（GPFS）持久，`/` 是容器私有层 | 平台设计 | 工具必须装在 `~/.local`，不能装在 `/usr` |
| 镜像重建即失效 | Pod 生命周期 | 配置文件、规则、订阅必须落 `~/` |
| 用户只想要"能用" | 需求本身 | 反对重量级方案 |
| 出口 IP 是机房 IP | 订阅节点 | `chatgpt.com` 等站点返回 403（Cloudflare 风控），API 类可用 |

### 1.3 真正的痛点不是"没有代理"，而是"代理不好控"

初版实现（完整的 Clash 安装）带来一连串体验问题，最终促成这次重构：

1. **规则黑盒**：订阅自带 `MATCH,PROXY`（全量代理），用户无法直观判断"什么在走隧道"
2. **开关过多**：`on/off/status/stop/mode/run` 六个入口，且 `stop` 与 `off` 语义重叠
3. **输出嘈杂**：每次执行都刷作业控制提示 `[2] pid` / `+ done`，加上脚本自带的提示
4. **行为副作用**：`proxy-on` 会把用户的工作目录改成 mihomo 的家目录
5. **权限过松**：含节点凭据的 `config.yaml` 是 644，同机其他用户可读
6. **日志无节制**：`mihomo.log` 无限追加
7. **僵尸堆积**：每次停止留下一个 `Z` 条目（PID 1 不回收）

### 1.4 结论

> 代理应该像"手电筒"：**人决定什么时候开、开多久，照到哪里**。
> 工具只负责把"开/关"这件事做得**可预测、无噪音、无残留**。

---

## 2. 设计目标与非目标

### 目标

| # | 目标 | 验收标准 |
|---|---|---|
| G1 | **可预测** | 开 = 全部走隧道（除内网），关 = 全部直连；行为不随配置漂移 |
| G2 | **无噪音** | `proxy-on <cmd>` 的 stdout/stderr **只有被执行命令自己的输出** |
| G3 | **无副作用** | 不改用户的工作目录、不污染用户的 shell 环境变量 |
| G4 | **无残留** | 停止后不留后台进程；能回收的不留僵尸 |
| G5 | **持久** | 配置/规则/订阅都在 `~/`（GPFS），Pod 重建后仍可用 |
| G6 | **可自愈** | 重新拉订阅、重新生成运行时配置都是一条命令 |
| G7 | **信息可查** | `proxy-status` 一眼看清：进程、端口、当前用法 |

### 非目标

| ✗ | 不做 | 理由 |
|---|---|---|
| | TUI / Web 面板 | 平台已有 `hi tui`；这里只做命令行 |
| | systemd / 自启 / 保活 | 容器里没有 systemd；进程生命周期应跟 shell 走 |
| | 多节点负载均衡 / 多订阅 | 当前只有一个节点；规则可扩展但先不做 |
| | 全局透明代理（TUN）| 容器无 `/dev/net/tun`、无 `CAP_NET_ADMIN`，物理上做不到 |
| | 智能分流（国内直连、广告拦截等）| 用户明确要求"规则不重要，由人控制"；需要时一行开关即可 |
| | 节点健康检查 / 自动故障转移 | 只有一个节点，无从选择；`proxy-status` 足够 |

---

## 3. 设计原则

> 这些原则是本工具所有具体决定的来源。新增功能前先对照它们。

### P1 · 奥姆卡剃刀（Occam's Razor）

> "The entities must not be multiplied beyond necessity."

**体现**：16 个入口 → 3 个命令；6 个独立脚本 → 0 个；冗余的 `proxy-stop` 合并进 `proxy-off`。

**判断标准**：如果两个命令的语义在 99% 的场景下相同，就合并；差异只在"制造坏状态"时才保留。

### P2 · 人控制，工具不自作聪明

> 代理的开关与范围由人决定，工具不做"智能分流"。

**体现**：规则最终收敛为"内网直连 + 其余全走隧道"（`MATCH,PROXY`），
而不是"AI 域名走代理、其它直连"的智能版本 —— 后者曾短暂存在，但**用户看不懂它到底放行了什么**。

**推论**：任何"自动优化"（如自动加 GEOIP,CN）必须做成**显式开关**，不能是默认行为。

### P3 · 静默优先（Silence by default）

> 工具的成功标志是"什么都没发生"。

**体现**：
- 删除 `✅ mihomo 已启动/已停止` 提示 → 只在 `proxy-status` 里可见
- 启动 mihomo 时临时 `setopt no_monitor` → 消除 zsh 的 `[2] pid` / `+ done`
- 错误信息（❌）保留 → 失败必须可见

### P4 · 隔离与可回收（Reap what you start）

> 启动的进程必须能被停止，被停止的进程必须能被回收。

**体现**：mihomo 在**当前 shell** 里以 `&` 启动（保持为子进程）→ 停止时 `wait` 回收 → 不产生僵尸。
（对比 `&!` 虽然更安静，但失去 `wait` 能力 → 反而留僵尸 → 弃用）

### P5 · 持久优先（Persist everything meaningful）

> 能放在 `~/`（GPFS）的一律放 `~/`；`/usr`、`/etc` 是容器易失层。

**体现**：`mihomo` 二进制、`config.yaml`、`config.runtime.yaml`、`rules-custom.yaml`、
`GeoIP.dat`、`GeoSite.dat` 全部在 `~/.local/`；只有 apt 装的包在容器层（可接受，因为它们可重装）。

### P6 · 权限最小化

> 含凭据的文件必须 600。

**体现**：`config.yaml`（含节点 uuid / public-key / short-id）、`config.runtime.yaml` 均为 `600`；
重新拉订阅的脚本 `update-config.sh` 会自动 `chmod 600`。

### P7 · 有界增长（Bounded growth）

> 日志、缓存、僵尸这类会无限增长的东西必须有上界。

**体现**：日志按天轮转、只保留 3 天（`clean-logs.sh`）；僵尸靠 P4 避免新增。

### P8 · 分层配置，订阅不覆盖用户策略

> 订阅是"素材"，规则是"策略"，运行时配置是"产物"。

**体现**：`config.yaml`（订阅原样） → `build-config.py` 合并 → `config.runtime.yaml`（mihomo 实际加载）。
用户的策略写在独立的 `rules-custom.yaml` 里，**重新拉订阅不会冲掉它**。

### P9 · 先测量，再下结论

> 本项目最大的教训来自"假设"。

**体现**：见 [§8](#8-踩过的坑与教训)。所有性能/行为结论都来自实测数据，而非推理。

---

## 4. 架构

### 4.1 分层图

```
┌─ 用户 ─────────────────────────────────────────────────────────┐
│  proxy-on curl https://api.openai.com/...                      │
│  proxy-on --start codex        (隧道常驻)                      │
│  proxy-off / proxy-status                                       │
└───────────────────────────┬────────────────────────────────────┘
                            │ 注入 http_proxy/https_proxy（仅子进程）
                            ↓
┌─ mihomo (127.0.0.1:7890, 仅 HTTP) ────────────────────────────┐
│  规则引擎：内网 IP-CIDR → DIRECT；其余 MATCH → PROXY             │
│  fake-ip DNS：集群 DNS 优先，境内 DoH 兜底                       │
│  日志：mihomo.log（按天轮转，保留 3 天）                         │
└───────────────────────────┬────────────────────────────────────┘
                            ↓ VLESS+Reality 隧道
                    VPS (<node-ip>)
                            ↓
                      api.openai.com / github / 任何站点
```

### 4.2 仓库结构（可公开分发的形态）

```
simple-proxy/
├── README.md                 用户文档
├── LICENSE                   MIT
├── VERSION
├── install.sh                安装/初始化（--subscription / --no-shell / --port / --no-download）
├── uninstall.sh              卸载（--purge 连数据一起删）
├── bin/simple-proxy          CLI 入口（on/off/status/check/logs/update/install/uninstall）
├── lib/
│   ├── core.sh               核心：启动/停止/注入环境/状态/自检
│   ├── build-config.py       配置合并器（订阅 + 规则 + DNS 修正 + 端口收窄）
│   └── shell.sh              shell 集成片段（提供 proxy-on/off/status）
├── config/
│   ├── proxy.conf            配置模板（端口/日志天数/DNS/NO_PROXY/内核版本）
│   └── rules.yaml            规则模板（默认：内网直连 + 其余全走）
├── scripts/
│   ├── clean-logs.sh         日志轮转（按天，保留 N 天）
│   └── update-subscription.sh 拉取订阅（自动 chmod 600）
├── docs/
│   ├── DESIGN.md             本文
│   └── OPERATIONS.md         运维手册
└── tests/smoke.sh            冒烟测试（临时目录，不碰真实订阅/用户环境）
```

### 4.3 安装后的落盘布局（全部在持久区）

```
~/.local/bin/
├── simple-proxy              主命令
└── mihomo                    代理内核

~/.local/share/simple-proxy/  数据（GPFS，Pod 重建不丢）
├── config.yaml               订阅原样配置（600，含节点凭据）
├── config.runtime.yaml       运行时配置（600，mihomo 实际加载）
├── core.sh / build-config.py / shell.sh / *.sh
├── GeoIP.dat / GeoSite.dat   规则库
├── cache.db                  节点选择缓存
└── mihomo.log[.YYYY-MM-DD]   日志

~/.config/simple-proxy/
├── proxy.conf                用户配置
└── rules.yaml                用户规则
```

> **演进说明**：最初的单用户原型直接把文件放在 `~/.local/share/mihomo/`、`~/.local/bin/proxy-run`，
> 且 shell 集成是手工写的。产品化时把「实现」与「用户配置」分离：
> 数据目录只放机器生成物，用户可改的东西（`proxy.conf` / `rules.yaml`）放在 `~/.config/`，
> 并把散落的 shell 片段收敛成 `lib/shell.sh` 单一来源。

### 4.3 配置流水线

```
订阅 URL ──(update-config.sh)──► config.yaml        (600)
                                     │
                                     │ build-config.py
                                     ▼
   rules-custom.yaml ──────────► config.runtime.yaml (600)
   DNS 修正                        端口收窄
   端口收窄                          │
                                     ▼
                              mihomo -f config.runtime.yaml
```

**为什么分三层**：订阅会被覆盖/过期；用户的策略必须独立存活；运行时配置是二者的产物，
还包含平台相关的修正（DNS、端口），这些修正不该写进订阅。

---

## 5. 命令语义

### 5.1 命令表

| 命令 | 语义 | mihomo 生命周期 | 代理变量作用域 |
|---|---|---|---|
| `proxy-on <cmd> [args...]` | 让一条命令走代理 | **一次性**：命令跑完自动停止 | 仅该命令的子进程 |
| `proxy-on --start <cmd> [args...]` | 同上，但隧道常驻 | **常驻**：直到 `proxy-off` | 仅该命令的子进程 |
| `proxy-off` | 关闭代理 | 停止 mihomo | — |
| `proxy-status` | 查看状态 | 不变 | 只读 |

### 5.2 关键设计决策

**① 为什么 `proxy-on` 必须带命令？**

早期版本支持 `proxy-on`（无参数）= "给当前 shell 设全局代理变量"。
问题：
- 需要额外的 EXIT trap 来做"一次性"清理，语义反而更绕
- 全局变量会污染当前 shell 及其所有子进程（包括你随手跑的工具）
- 用户明确要求："`proxy-on` 不应该被单独使用，他只能 `proxy-on <cmd>` 使用"

现在无参数调用会打印用法并返回 1：

```
用法:
  proxy-on <cmd> [args...]         让一条命令走代理（一次性：跑完自动停 mihomo）
  proxy-on --start <cmd> [args...]  同上，但 mihomo 常驻（可继续复用）
```

**② 为什么 `--start` 是参数而不是独立命令？**

`--start` 修饰的是"mihomo 生命周期"这一个维度，与"代理什么命令"正交。
把它做成独立命令（`proxy-start`）会引入"两个命令如何配合、如何知道对方状态"的耦合。

**③ 为什么一次性是默认？**

一次性（跑完就停）符合"用完即走"的直觉，且不残留进程；
需要连续跑多个命令时才用 `--start` 显式声明常驻。

**④ 为什么代理变量只注入子进程？**

```zsh
( _mihomo_env; exec "$@" )
```
用户 shell 里**永远不会**残留 `http_proxy` → 不会出现"忘了关代理导致某个工具行为异常"。

---

## 6. 规则设计

### 6.1 当前规则（`rules-custom.yaml`）

```yaml
rules:
  # ── 内网 / 本地：必须直连 ────────────────────────────────
  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve
  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
  - IP-CIDR6,fc00::/7,DIRECT,no-resolve
  - IP-CIDR6,fe80::/10,DIRECT,no-resolve

  # ── 其余一律走隧道 ──────────────────────────────────────
  - MATCH,PROXY
```

**只有 7 行，且只有两个意图**：
1. 内网永远直连（否则平台 API、镜像仓库全部失联）
2. 其余全部走隧道（人控制，不搞智能）

### 6.2 为什么内网必须豁免

| 服务 | 地址 | 走隧道的后果 |
|---|---|---|
| 平台 API (hi) | `10.255.0.1:30080` | 平台调度命令全部失败 |
| K8s Service | `10.42.0.0/16`、`10.43.0.0/16` | 集群内调用中断 |
| 镜像仓库 | `registry.qz.local` | 拉/推镜像失败 |
| docker dind | `qz-dind-*.svc.cluster.local` | 容器操作失败 |

**这是唯一的"智能"，且是安全底线，不可关闭。**

### 6.3 客户端侧 NO_PROXY（与规则互补）

```
localhost,127.0.0.1,.svc,.svc.cluster.local,registry.qz.local,
qz-dind-*.svc.cluster.local,10.0.0.0/8,10.16.0.0/16,10.42.0.0/16,
10.43.0.0/16,10.254.0.0/16,10.255.0.0/16
```

**两层都覆盖内网**：客户端 NO_PROXY 让内网**根本不进 mihomo**（少一跳），
mihomo 规则再兜底一层（防止有工具忽略 NO_PROXY）。

> ⚠️ **踩坑记录**：本机 curl 是 **7.81**，`NO_PROXY` **不支持 CIDR**（7.86+ 才支持）。
> 所以必须**同时写主机名**（给 curl/git）和**网段**（给 Go/Java 等支持 CIDR 的库）。

### 6.4 DNS 设计

订阅默认用境外 DoH（`1.1.1.1` / `8.8.8.8`），在境内**直连不通** → 解析失败。
`build-config.py` 把它改成：

```yaml
default-nameserver:
  - 10.43.0.10          # 容器自己的集群 DNS（自动从 /etc/resolv.conf 探测）
nameserver:
  - 10.43.0.10          # ① 首选：集群 DNS —— 与容器直连行为一致
  - https://doh.pub/dns-query          # ② 兜底：腾讯 DoH
  - https://dns.alidns.com/dns-query  # ③ 兜底：阿里 DoH
```

**为什么首选集群 DNS**：
- 平台 DNS 对境外域名做分流（`github.com` → `10.16.0.40` 平台内部出口）→ 与容器直连**完全一致**
- 而公共 DoH 会给 GitHub 的真实公网 IP → 绕远、可能撞限速

**为什么 AI/代理域名不怕本地污染**：走隧道的域名由 **VPS 远端解析**（VLESS 传域名而非 IP），
本地 DNS 返回什么污染 IP 都不影响。

### 6.5 端口收窄

| 项 | 订阅默认 | 收窄后 | 理由 |
|---|---|---|---|
| 代理端口 | `mixed-port: 7890`（HTTP+SOCKS）| `port: 7890`（**仅 HTTP**）| 减少暴露面；工具链只用 HTTP |
| 控制 API | `external-controller: 127.0.0.1:9090` | **关闭** | 少一个监听端口（代价：不能交互式切节点）|

---

## 7. 实现要点

### 7.1 启动：三个"必须"

```zsh
# ① 在当前 shell 启动（保持为子进程）→ 停止时能 wait 回收
nohup "$MIHOMO_BIN" -d "$MIHOMO_HOME" -f "$MIHOMO_HOME/config.runtime.yaml" >>"$LOG" 2>&1 &
MIHOMO_PID=$!

# ② 还原工作目录（mihomo 需在自己的目录跑 geo 缓存，但不能改用户的目录）
local oldpwd="$PWD"; cd "$MIHOMO_HOME" || return 1
...启动...
cd "$oldpwd" || true

# ③ 关闭 zsh 作业通知（消除 [2] pid / + done 噪音）
setopt no_monitor
```

> ② 是一个真实 bug：早期版本直接 `cd "$MIHOMO_HOME"`，导致用户执行完命令后提示符
> 变成 `…/.local/share/mihomo`。**任何会改变 cwd 的后台启动都必须还原。**

### 7.2 停止：回收优先

```zsh
kill "$pid"; ... ; kill -9 "$pid"
if [[ "${MIHOMO_PID:-}" == "$pid" ]]; then
  wait "$pid"          # ← 回收，避免僵尸
  unset MIHOMO_PID
fi
rm -f "$pidfile"
setopt monitor          # 恢复作业通知
```

### 7.3 端口探测的跨 shell 问题

zsh **不支持** `/dev/tcp`（bash 专有）。第一版用 `(exec 3<>/dev/tcp/...)` 探测端口，
在 zsh 里**永远失败** → `proxy-on` 误报"启动超时"，而 mihomo 其实已经起来了。

正确写法：

```zsh
if bash -c "exec 3<>/dev/tcp/127.0.0.1/$MIHOMO_PORT" 2>/dev/null; then
```

### 7.4 订阅更新的权限保持

```bash
curl -fsSL -o "$CONFIG.tmp" "$SUB_URL" && mv "$CONFIG.tmp" "$CONFIG"
chmod 600 "$CONFIG"          # 含节点凭据，必须 600
```

### 7.5 日志轮转

`clean-logs.sh` 在**每次启动时**自动执行：
- `mihomo.log` → `mihomo.log.YYYY-MM-DD`（当天不重复切）
- 按**文件名里的日期**删除超过 3 天的（不是 mtime —— mtime 会被 touch 改掉）
- 兜底：> 20 MB 的旧日志也删

### 7.6 节点选择缓存的坑

mihomo 的 `PROXY` 组是 `select` 类型，`profile.store-selected: true` → 选择被记进 `cache.db`。
一旦某次把它切成 `DIRECT`，之后所有流量都会直连，而日志只显示
`match Match using DIRECT` —— **看起来像规则没生效，实际是组的当前选择变了**。

排查手法：
```bash
tail -20 ~/.local/share/simple-proxy/mihomo.log | grep '\[TCP\]'   # 看 "using X"
```
出现 `using DIRECT` 而规则明明是 `MATCH,PROXY` → 清 `cache.db` 重启即可。

> 注意：本工具**关闭了 9090 API**，所以没法用 API 切节点，只能清缓存。
> 这是"端口收窄"与"可运维性"的一次权衡，当前接受。

---

## 8. 踩过的坑与教训

### 8.1 最大的误判：把终端渲染问题当成字体/插件问题

**现象**：在终端敲 `ls` 显示成 `lsls`；敲 `exit` 显示成 `exitxit`；建议文本残留在输出区域。

**错误路径**（我走了很远）：
- 怀疑是 Nerd Font 缺失 → 查了 starship.toml 里 27 个私有区码位、查了本地字体
- 怀疑是 zsh-syntax-highlighting 与 zsh-autosuggestions 冲突 → 关掉插件、改加载顺序
- 怀疑是 `SHARE_HISTORY` + `INC_APPEND_HISTORY` 导致建议膨胀 → 改 history 选项

**真相**：容器 `/usr/share/terminfo` 是空的（`ncurses-base` 被裁剪过），
`TERM=xterm-ghostty` **查不到终端描述** → zsh 退化成"原始模式"，**没有"擦除到行尾"的能力**
→ 每次重绘都变成追加。

**决定性证据**（同一份配置，只换 TERM）：
```
TERM=xterm-256color  →  'mini> exit'      ✅
TERM=xterm-ghostty   →  'mini> exitxit'   ❌
```

**修复**：把 terminfo 拷到持久区 `~/.terminfo/`（ncurses 默认优先搜索该路径）+ 在 shell 里
对未知 TERM 做降级兜底。

> **教训（P9）**：在**测量**之前不要相信任何关于"渲染/字体/插件"的假设。
> 一次"换变量对照实验"胜过十次推理。

### 8.2 其它已修的坑

| 现象 | 根因 | 修复 |
|---|---|---|
| `proxy-on` 后提示符变成 mihomo 目录 | 启动时 `cd` 未还原 | 立即 `cd "$oldpwd"` |
| `proxy-on` 误报"启动超时" | zsh 不支持 `/dev/tcp` | 改用 `bash -c` |
| 改了 proxy.sh 但行为不变 | shell 已加载旧函数 | 重载（见 §10 建议）|
| 规则改了却不生效 | 旧 mihomo 进程未重启 | 重启后再验 |
| `proxy-stop` 报 command not found | 精简时删掉了该命令 | 加回；并修正测试脚本 |
| 建议文本未清除 | 订阅的 DoH 不可直连 | 换集群 DNS + 境内 DoH 兜底 |
| `config.yaml` 644 | 脚本未收紧权限 | 600 + 拉取时自动 chmod |

### 8.3 一次"删除过度"的教训

为了精简命令集，我删掉了 `proxy-stop`，结果自己的测试脚本随即报错
（`command not found`），却一度以为是"旧进程没重启"。
**教训**：删除一个公开接口前，先确认没有内部依赖它；测试脚本也算依赖方。

---

## 9. 已知限制

| # | 限制 | 影响 | 缓解/归属 |
|---|---|---|---|
| L1 | **单节点** | 节点挂了代理整体不可用 | 当前订阅只有一个节点；可加 `hi image`/多节点切换 |
| L2 | **Web UI 被 Cloudflare 403** | `chatgpt.com` / `claude.ai` 网页打不开 | 出口是机房 IP；**API 类完全可用**（`api.openai.com` 返回自身 JSON 错误 = 链路通）|
| L3 | **僵尸无法清除** | PID 表里留 `Z` 条目 | 根因是容器 PID1 = `sleep` 不回收；**根治需管理员改 Pod spec 用 tini/dumb-init**。当前同 shell 内 start+stop 不新增 |
| L4 | **无透明代理** | 不认 `http_proxy` 的工具无法走代理 | 容器无 `/dev/net/tun`、无 `CAP_NET_ADMIN`，物理不可能 |
| L5 | **docker 拉镜像不走代理** | CLI 侧环境变量影响不到远端 dind 守护进程 | 需在 dind 侧配置 |
| L6 | **规则无法交互切换** | 关闭了 9090 API | 改 `rules-custom.yaml` 后重启；或清 `cache.db` 重置节点选择 |
| L7 | **容器层依赖会丢** | `ncurses-term` 等 apt 包重建后消失 | 关键依赖已复制到 `~/.terminfo/`（持久）|

### 9.1 环境实测数据（供容量与调优参考）

```
GPFS 顺序读 2GB (direct I/O)   : 1.1 – 1.5 GB/s     ← 瓶颈在容器 10G 网卡
GPFS 顺序读 (页缓存命中)        : 2.8 GB/s
GPFS 写 2GB (direct)            : 219 MB/s
GPFS 小文件 stat ×1000          : ~1 ms/次
容器本地 overlay 读             : 261 MB/s
cgroup pids.max                 : 629145（当前 576，僵尸 290 → 无压力）
```

---

## 10. 扩展方向

### 10.1 已预留的旋钮

| 需求 | 做法 | 成本 |
|---|---|---|
| 国内站点直连 | 在 `rules-custom.yaml` 的 `MATCH` 前加一行 `- GEOIP,CN,DIRECT` | 一行 |
| 换节点/多节点 | 重新拉订阅（`update-config.sh`）+ 必要时清 `cache.db` | 一条命令 |
| 恢复交互式切节点 | 打开 `external-controller`（注意需同时设 `secret`） | 改 `build-config.py` |
| 换订阅 | `CLASH_SUB_URL=xxx bash update-config.sh` | 环境变量 |
| 调日志保留天数 | `MIHOMO_LOG_KEEP_DAYS=7` | 环境变量 |

### 10.2 状态

| 建议 | 状态 |
|---|---|
| **版本检查**：`proxy.sh` 顶部写 `PROXY_SH_VERSION`，`status` 比对磁盘版本，落后时提示重新 source | ⏳ 未做 |
| **自检命令**：`simple-proxy check` → 依次验证内核/订阅/规则/python3/terminfo/进程/端口 | ✅ 已做 |
| **安装脚本**：`install.sh` 一键完成"下载内核 → 拉订阅 → 生成配置 → 写 shell 集成" | ✅ 已做 |
| **冒烟测试**：`tests/smoke.sh`，临时目录内跑完整安装→运行→停止闭环 | ✅ 已做 |
| **跨平台**：Linux/macOS、x86_64/arm64 自动识别并下载对应内核 | ✅ 已做 |

### 10.3 明确不做

- 不做全局代理模式（`proxy-on` 无参数）—— 见 §5.2 ①
- 不做智能分流 —— 见 P2
- 不做 systemd/自启 —— 进程生命周期跟 shell 走更简单可靠

---

## 附录 A · 命令速查

```bash
proxy-on <cmd> [args...]         # 一次性：命令走代理，跑完自动停隧道
proxy-on --start <cmd> [args...]  # 常驻：隧道留着，可连续跑多个命令
proxy-off                         # 关闭（停 mihomo）
proxy-status                      # 状态

# 常用组合
proxy-on curl https://api.ipify.org          # 查出口 IP
proxy-on --start codex                       # codex 常驻走代理
proxy-on pip install xxx                     # 单条 pip 走代理
```

## 附录 B · 排障速查

| 症状 | 排查 |
|---|---|
| 流量没走隧道 | `tail -20 ~/.local/share/simple-proxy/mihomo.log \| grep '\[TCP\]'` 看 `using X`；若是 `DIRECT` 而规则是 `MATCH,PROXY` → 清 `cache.db` |
| `proxy-on` 报"启动超时" | 端口探测失败；确认 `ss -ltn \| grep 7890`；确认没有别的进程占 7890 |
| 命令改了没生效 | shell 加载的是旧函数 → `source ~/.local/share/simple-proxy/shell.sh` |
| 订阅过期/节点失效 | `bash simple-proxy update` 重拉 |
| 内部服务连不上 | 检查 `rules-custom.yaml` 的 10/8 直连规则是否被改 |
| 出口 IP 不对 | `proxy-on curl https://api.ipify.org` 看结果是否 = 节点 IP |

## 附录 C · 相关背景

- 平台存储约定：`/workspace`（GPFS，持久）、`/shared`（只读共享）、`/hi_local`（节点本地）
- 平台代理服务（官方 mihomo）：`hi service connect mihomo`（当前用户未挂载）
- 参见 `~/.codex/skills/hi-cli/SKILL.md`（平台工作流规范）
