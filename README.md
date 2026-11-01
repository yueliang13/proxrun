# proxrun

> **Run a command through a proxy. Clean up when it finishes.**
>
> 一条命令走代理，用完自动关闭。

**proxrun** 取自 **proxy + run**，是用 Go 编写的轻量命令行 HTTP 代理工具。
在命令前加上 `po`，即可按需使用代理，适合下载文件、安装依赖、访问 API 等日常操作。

```bash
po curl https://api.ipify.org
po pip install requests
po git ls-remote https://github.com/yueliang13/proxrun.git
```

- **默认一次性**：启动代理，命令完成后自动关闭并回收进程。
- **环境隔离**：代理变量仅传给指定命令及其子进程。
- **输出干净**：保留命令自身的输出和退出码，不改变工作目录。
- **内网直连**：默认本地及内网目标直连，其余通过代理。
- **二进制运行**：配置处理、订阅下载和进程管理均在 `proxrun` 中完成，proxrun 自身无需 Python、PyYAML 或 curl。

## 构建与安装

支持 Linux / macOS，amd64 / arm64。**从源码构建需要 Go 1.24+**；
使用已构建的二进制不需要安装 Go。代理内核 mihomo 独立运行，由安装命令下载，也可以复用已有内核。

```bash
git clone https://github.com/yueliang13/proxrun.git
cd proxrun

# 仅构建，生成 dist/proxrun
make build
./dist/proxrun version

# 显式安装二进制、配置模板和 shell 集成
./dist/proxrun install --subscription '你的 Clash/mihomo YAML 订阅 URL'

# 当前终端立即启用快捷命令
source ~/.local/share/proxrun/shell.sh
```

也可以运行 `bash install.sh [安装选项]`，它会先构建，再执行安装。
已有 mihomo 时使用 `--no-download`；不需要 shell 集成时使用 `--no-shell`；`--port <N>` 指定 HTTP 端口。
安装默认根据 `SHELL` 选择集成，也可通过 `--shell bash|zsh|fish` 显式指定。
请确保 `~/.local/bin` 在 `PATH` 中。

**修改源码、构建和测试都不会更新已安装版本。** 安装时通过原子替换更新二进制，
已有进程继续运行旧版本，后续调用使用新版本。升级前后保留用户配置和订阅。

## 使用

`po` 是 `proxy-on` 的简写，二者参数相同。也可以直接使用主命令：

```bash
proxrun on curl https://api.ipify.org
proxrun on pip install requests
proxrun status
```

单独运行一次性命令时，命令结束后代理即关闭。多条命令并行执行时共享代理，最后一条完成后关闭；
首次启动代理的调用会等待共享使用者结束，以便回收进程。`proxy-off` 或 `proxrun off` 会显式关闭共享代理。

如需保持代理常驻，使用 `po --start <cmd>`，之后用 `proxy-off` 关闭；普通的一次性调用不会关闭已有的常驻代理。
安装后的新 Bash/zsh/Fish 终端会自动加载快捷命令。
`proxrun shell` 根据 `SHELL` 输出脚本，也可以用 `proxrun shell fish` 显式选择。

### Fish

```fish
# 使用新构建的二进制安装 Fish 集成，复用已有 mihomo
./dist/proxrun install --no-download --shell fish

# 当前 Fish 会话立即加载
source ~/.local/share/proxrun/shell.fish

po curl https://api.ipify.org
po --start your-command
proxy-off
```

安装会写入 `~/.config/fish/conf.d/proxrun.fish`，以后新开 Fish 自动生效；
设置 `XDG_CONFIG_HOME` 时使用对应目录下的 `fish/conf.d/proxrun.fish`。
用户的 `config.fish` 保持原样。卸载会清理 proxrun 生成的集成文件。

## 订阅与配置

订阅需要提供 **Clash/mihomo YAML 配置**；当前不转换裸节点链接或 Base64 节点列表。

```bash
proxrun update '你的订阅 URL'

# 修改配置后，关闭已有代理；下次运行命令时加载新配置
proxrun off
```

| 路径 | 内容 |
|---|---|
| `~/.local/bin/proxrun` | 编译后的主程序 |
| `~/.local/bin/mihomo` | 代理内核 |
| `~/.config/proxrun/proxy.conf` | 端口、日志保留天数、DNS 等设置 |
| `~/.config/proxrun/rules.yaml` | 用户路由规则 |
| `~/.local/share/proxrun/config.yaml` | 订阅配置，权限为 `600` |
| `~/.local/share/proxrun/config.runtime.yaml` | 自动生成的运行配置，权限为 `600` |

`proxy.conf` 是 `KEY=value` 数据文件，支持引号和注释，**不执行 shell 表达式**。
优先使用 `PROXRUN_*` 配置项和环境变量，旧的 `SP_*` 配置继续支持；环境变量优先于配置文件。
例如：

```bash
PROXRUN_HTTP_PORT=17890 proxrun on curl https://api.ipify.org
```

订阅地址也可以通过 `PROXRUN_SUBSCRIPTION` 环境变量传给 `install` 或 `update`。
用户规则与订阅独立保存，更新订阅不会覆盖规则。
真实订阅和运行数据保存在用户目录，源码仓库只包含公开配置模板。
`600` 表示只有文件所有者可读写，其他普通用户没有访问权限。

从旧版升级时，安装命令支持迁移 `simple-proxy` 的默认目录并保留旧命令兼容链接。
已有代理仍在运行时，通过目录链接共享原有状态，不重启代理，也不复制第二套运行状态。

## 排障与卸载

```bash
proxrun check              # 检查内核、配置、权限和运行状态
proxrun logs 50            # 最近的路由日志
proxrun uninstall         # 保留配置与数据
proxrun uninstall --purge # 同时删除配置与数据
```

默认 HTTP 代理地址为 `127.0.0.1:7890`。启动失败时检查端口占用和日志；
修改配置后，先执行 `proxrun off`，再重新运行命令。

## 开发

项目结构和开发约定见 [AGENTS.md](AGENTS.md)。

```bash
make build       # 只生成 dist/proxrun
make test        # Go 单元测试和隔离集成测试
make race        # 数据竞争检查
make vet         # 静态检查
make release     # 构建 Linux/macOS 的 amd64/arm64 二进制

# 可选：使用指定的真实 mihomo 验证配置与转发
SP_TEST_MIHOMO=/path/to/mihomo make test
```

测试使用临时用户目录和随机端口，不更新系统安装。构建产物位于 `dist/`，由 Git 忽略。

## License

MIT
