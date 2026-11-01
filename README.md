# proxrun

> **Run a command through a proxy. Clean up when it finishes.**
>
> 一条命令走代理，用完自动关闭。

**proxrun** 取自 **proxy + run**，是基于 mihomo 的轻量命令行 HTTP 代理工具。
在命令前加上 `po`，即可按需使用代理，适合下载文件、安装依赖、访问 API 等日常操作。

```bash
po curl https://api.ipify.org
po pip install requests
po git ls-remote https://github.com/yueliang13/proxrun.git
```

- **默认一次性**：自动启动代理，命令完成后自动关闭并回收进程。
- **环境隔离**：代理变量仅传给指定命令及其子进程。
- **输出干净**：成功时不添加提示，保留命令自身的输出和退出码。
- **目录不变**：执行前后保持当前工作目录。
- **内网直连**：默认本地及内网目标直连，其余通过代理。

## 安装

支持 Linux / macOS，x86_64 / arm64。需要 Bash 4+、Python 3.9+ 和 curl。
安装脚本自动下载 mihomo；缺少 PyYAML 时，通过 pip 安装到用户数据目录。

```bash
git clone https://github.com/yueliang13/proxrun.git
cd proxrun
bash install.sh --subscription '你的 Clash/mihomo YAML 订阅 URL'

# 当前终端立即启用快捷命令
source ~/.local/share/simple-proxy/shell.sh
```

安装后，新开的 Bash/zsh 终端会自动加载快捷命令。请确保 `~/.local/bin` 在 `PATH` 中。

已有 mihomo 时，可使用 `--no-download`；不需要 shell 集成时，可使用 `--no-shell`。

## 使用

`po` 是 `proxy-on` 的简写，二者参数相同。项目名为 proxrun，当前主命令及安装目录沿用 `simple-proxy`。

```bash
# 一次性下载文件
po curl -fLO https://example.com/file.zip

# 一次性安装依赖
po pip install requests

# 查看状态
proxy-status
```

不加载 shell 集成，也可以直接使用主命令：

```bash
simple-proxy on curl https://api.ipify.org
simple-proxy on pip install requests
simple-proxy status
```

单独运行一次性命令时，命令结束后代理即关闭。多条命令并行执行时共享代理，最后一条完成后关闭；
首次启动代理的调用会等待共享使用者结束，以便回收进程。`proxy-off` 会显式关闭共享代理。

如需保持代理常驻，可使用 `po --start <cmd>`，之后用 `proxy-off` 关闭；普通的一次性调用不会关闭已有的常驻代理。

## 订阅与配置

订阅需要提供 **Clash/mihomo YAML 配置**；当前不转换裸节点链接或 Base64 节点列表。

```bash
# 添加或更换订阅
simple-proxy update '你的订阅 URL'

# 修改配置后，关闭已有代理；下次 po 时加载新配置
proxy-off
```

| 路径 | 内容 |
|---|---|
| `~/.config/simple-proxy/proxy.conf` | 端口、日志保留天数、DNS 等设置 |
| `~/.config/simple-proxy/rules.yaml` | 用户路由规则 |
| `~/.local/share/simple-proxy/config.yaml` | 订阅配置，权限为 `600` |
| `~/.local/share/simple-proxy/config.runtime.yaml` | 自动生成的运行配置，权限为 `600` |

用户规则与订阅独立保存，更新订阅不会覆盖规则。配置支持同名环境变量覆盖。
真实订阅和运行数据保存在用户目录，源码仓库只包含公开配置模板。

## 排障

```bash
simple-proxy check      # 检查依赖、配置、进程和端口
simple-proxy logs 50    # 查看最近的路由日志
```

默认 HTTP 代理地址为 `127.0.0.1:7890`。启动失败时检查端口占用和日志；
修改配置后，先执行 `proxy-off`，再重新运行命令。

## 卸载

```bash
bash uninstall.sh          # 保留配置与数据
bash uninstall.sh --purge  # 同时删除配置与数据
```

## 开发

项目结构和开发约定见 [AGENTS.md](AGENTS.md)。

```bash
python3 -m pip install -r requirements.txt
bash tests/smoke.sh

# 可选：使用真实 mihomo 验证
SP_TEST_MIHOMO=/path/to/mihomo bash tests/smoke.sh
```

## License

MIT
