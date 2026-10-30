# simple-proxy 运维手册

配套文档：[DESIGN.md](./DESIGN.md)（设计动机 / 方法 / 规则）

本手册面向"日常使用与出问题时怎么办"，不重复设计细节。

---

## 1. 安装

### 1.1 依赖

| 依赖 | 说明 |
|---|---|
| mihomo | 代理内核，由安装脚本自动下载 |
| Python 3 | 生成运行时配置 |
| curl | 下载内核与订阅 |
| bash 4+ | 运行脚本 |

### 1.2 一键安装

```bash
git clone <this-repo> && cd simple-proxy
bash install.sh --subscription 'https://your-subscription-url'
```

### 1.3 分步安装

```bash
bash install.sh                      # 内核 + 命令 + 配置模板 + shell 集成
simple-proxy update '<订阅URL>'      # 填订阅（自动 chmod 600）
simple-proxy check                   # 自检
```

选项：

| 选项 | 说明 |
|---|---|
| `--subscription <URL>` | 安装时直接填订阅（也可用 `SIMPLE_PROXY_SUBSCRIPTION` 环境变量）|
| `--no-shell` | 不修改 rc 文件（不提供 `proxy-on/off/status` 快捷命令）|
| `--port <N>` | 指定 HTTP 代理端口（默认 7890）|
| `--no-download` | 不下载内核（自行安装到 `~/.local/bin/mihomo`）|

### 1.4 安装内容

```
~/.local/bin/mihomo                 代理内核
~/.local/bin/simple-proxy           主命令
~/.local/share/simple-proxy/        数据（订阅/运行时配置/日志/GeoIP 库）
~/.config/simple-proxy/             配置（proxy.conf / rules.yaml）
~/.zshrc 或 ~/.bashrc               shell 集成（可选）
```

> ⚠️ `~/.local/bin` 与 `~/.local/share`、`~/.config` 建议放在**持久化**的磁盘上
> （容器环境里通常映射到 GPFS 的 `/workspace`），否则重建后需重装。

---

## 2. 日常使用

```bash
simple-proxy on <cmd> [args...]         # 一次性：命令走代理，跑完自动停隧道
simple-proxy on --start <cmd> [args...]  # 常驻：隧道留着，可继续复用
simple-proxy off                         # 关闭
simple-proxy status                      # 状态
simple-proxy check                       # 自检
simple-proxy logs [N]                    # 最近 N 行路由日志（默认 50）
```

### 2.1 典型场景

```bash
# 调 OpenAI 兼容接口
simple-proxy on curl -sS https://api.openai.com/v1/models -H "Authorization: Bearer $KEY"

# 跑 codex（想让它一直挂着）
simple-proxy on --start codex

# 一次性装包
simple-proxy on pip install somepkg

# 查出口 IP
simple-proxy on curl -s https://api.ipify.org
```

### 2.2 行为预期

| 事项 | 预期 |
|---|---|
| 执行完命令后，**当前 shell 是否有 `http_proxy`？** | **没有**。代理变量只存在于该命令的子进程里 |
| **当前工作目录会不会变？** | **不会** |
| **输出里会不会多出 `[2] pid` 之类的噪音？** | **不会** |
| 一次性模式下命令跑完，mihomo 会怎样？ | 自动停止 |
| `--start` 模式下 shell 退出后 mihomo 会怎样？ | 继续运行（常驻语义） |

---

## 3. 配置文件

| 文件 | 谁改 | 说明 |
|---|---|---|
| `~/.config/simple-proxy/rules.yaml` | **用户** | 规则策略（唯一需要手动改的） |
| `~/.config/simple-proxy/proxy.conf` | **用户** | 端口、日志天数、DNS、NO_PROXY、内核版本 |
| `~/.local/share/simple-proxy/config.yaml` | 自动 | 订阅原样（含凭据，600）|
| `~/.local/share/simple-proxy/config.runtime.yaml` | 自动 | mihomo 实际加载的配置（600，**不要手改**）|

### 3.1 改了规则怎么生效

```bash
simple-proxy off && simple-proxy on curl https://api.ipify.org
```
（每次 `on` 启动时都会重新生成 `config.runtime.yaml`）

### 3.2 常用规则变体

```yaml
# A) 当前默认：内网直连，其余走隧道
  - MATCH,PROXY

# B) 国内站点也直连（加在 MATCH 前一行）
  - GEOIP,CN,DIRECT
  - MATCH,PROXY

# C) 只让某些域名走隧道（其余直连）
  - DOMAIN-SUFFIX,openai.com,PROXY
  - DOMAIN-SUFFIX,anthropic.com,PROXY
  - MATCH,DIRECT
```

---

## 4. 维护

### 4.1 更新订阅

```bash
simple-proxy update '<URL>'
simple-proxy off && simple-proxy on --start true     # 重启使新订阅生效
```
脚本会自动 `chmod 600`（含节点凭据）。

### 4.2 更新内核

```bash
# 改 ~/.config/simple-proxy/proxy.conf 里的 SP_MIHOMO_VERSION，然后：
simple-proxy off
bash install.sh --no-shell --subscription "$(cat ~/.local/share/simple-proxy/.sub)" 2>/dev/null || true
```
更直接的方式：
```bash
V=1.19.33
curl -fsSL -o /tmp/m.gz \
  "https://github.com/MetaCubeX/mihomo/releases/download/v$V/mihomo-linux-amd64-compatible-v$V.gz"
gunzip -f /tmp/m.gz && install -m 755 /tmp/m.gz ~/.local/bin/mihomo && rm -f /tmp/m.gz
simple-proxy off && simple-proxy on --start true
```

### 4.3 日志

```bash
# 实时
tail -f ~/.local/share/simple-proxy/mihomo.log

# 路由决策（排障主力）
simple-proxy logs 50
#   [TCP] 127.0.0.1:xxx --> api.openai.com:443 match Match using PROXY[<node>]    ← 走隧道
#   [TCP] 127.0.0.1:xxx --> registry.local:443 match IPCIDR(10.0.0.0/8) using DIRECT ← 内网直连

# 轮转策略：按天切分，保留 N 天（SP_LOG_KEEP_DAYS，默认 3），启动时自动执行
ls -1 ~/.local/share/simple-proxy/mihomo.log*
```

### 4.4 磁盘占用

```
~/.local/share/simple-proxy/  ≈ 25 MB
  GeoIP.dat   16 MB
  GeoSite.dat  4 MB
  日志         < 1 MB/天（3 天上限）
~/.local/bin/mihomo           62 MB
```

---

## 5. 排障

### 5.1 决策树

```
命令没走代理？
├─ status 显示未运行 → on 会自动拉起；单独跑 on <cmd> 观察输出
├─ 在跑，但流量直连？
│   ├─ logs 显示 using DIRECT 而规则是 MATCH,PROXY → 节点选择缓存被改 → 删 cache.db 重启
│   └─ logs 显示 using PROXY 但出口 IP 仍是国内 → 节点失效 → update 重拉订阅
└─ 命令报 connection refused → 7890 没监听 → off && on 重启
```

### 5.2 症状对照表

| 症状 | 根因 | 处理 |
|---|---|---|
| `on` 报"等待端口超时" | 7890 被占 / 内核启动失败 | `ss -ltn \| grep 7890`；看 `mihomo.log` 尾部 |
| 改了配置没生效 | 进程用的是旧配置 | `off` 后重新 `on` |
| 改了 shell 集成没生效 | shell 缓存了旧函数 | `source ~/.local/share/simple-proxy/shell.sh` |
| 内部服务（集群 API / 镜像仓库）连不上 | 内网直连规则被改 | 恢复 `10.0.0.0/8 → DIRECT` |
| 出口 IP 不对 | 节点被换 / 失效 | 看 `logs` 的 `using X`；重拉订阅 |
| 网页能打开但 API 403 | 出口 IP 是机房 IP，被 Cloudflare 风控 | **API 类通常正常**；网页需换节点 |
| 提示符目录莫名改变 | 旧版实现的 `cd` 副作用 | 升级到当前版本 |
| 命令行出现 `[2] 12345` | 旧版（作业通知未关）| 升级到当前版本 |
| 终端输出错乱（`ls` 显示成 `lsls`）| 系统缺少当前 `TERM` 的 terminfo | 与本工具无关，见 `check` 的提示 |

### 5.3 常用检查命令

```bash
simple-proxy status                                    # 进程与端口
simple-proxy check                                     # 综合自检
simple-proxy logs 50                                   # 路由决策
ls -l ~/.local/share/simple-proxy/config.yaml          # 权限应为 600
cat ~/.config/simple-proxy/rules.yaml                  # 当前规则
ss -ltn | grep 7890                                    # 端口监听
ps -eo stat,pid,ppid,comm | awk '$1 ~ /^Z/'            # 僵尸进程（容器 PID1 不回收，与本工具无关）
```

---

## 6. 部署后自检清单

- [ ] `simple-proxy check` 全绿
- [ ] `simple-proxy on curl https://api.ipify.org` 返回**预期的出口 IP**
- [ ] `simple-proxy on curl https://api.openai.com/v1/models` 返回 **401**（到达 OpenAI，而不是 000）
- [ ] 内网 API 仍可达：`simple-proxy on curl http://<内网API>` 有响应
- [ ] `simple-proxy on --start curl -s https://ipinfo.io/ip` 后 `status` 显示"运行中"
- [ ] `simple-proxy off` 后端口释放、进程消失
- [ ] 重开 shell 后 `proxy-status` 仍可用（shell 集成已加载）

---

## 7. 与平台其它组件的关系

| 组件 | 关系 |
|---|---|
| 集群 API（`10.255.0.0/16`）| 必须直连，规则里的 `10.0.0.0/8 → DIRECT` 保障 |
| 镜像仓库 `registry.qz.local` | 必须直连 |
| K8s Service（`10.42/10.43.0.0/16`）| 必须直连 |
| 远端 dind（docker）| 守护进程在别的节点，CLI 侧环境变量不影响它 |
| 平台官方代理（若已挂载）| 优先用它：节点更多、有托管与日志 |

---

## 8. 卸载

```bash
bash uninstall.sh            # 移除命令与内核，保留数据与配置
bash uninstall.sh --purge    # 连数据与配置一起删除
```
（会同时清理 rc 文件里的 shell 集成片段）
