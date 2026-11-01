# proxrun

proxrun（proxy + run）是用 Go 编写的命令行 HTTP 代理工具，基于独立的 mihomo 内核。
默认让一条命令走代理，完成后关闭并回收；`--start` 显式常驻。
`po`、`proxy-on` 支持相同参数。主命令为 `proxrun`，配置和数据分别在
`~/.config/proxrun/`、`~/.local/share/proxrun/`。

## 代码结构

- `cmd/proxrun/`：二进制入口。
- `internal/app/`：CLI、配置、订阅、安装、共享运行状态和进程回收。
- `internal/app/process_linux.go`、`process_darwin.go`：平台相关进程身份检查。
- `assets.go`、`config/`：编译时嵌入的默认配置和规则。
- `lib/shell.sh`、`lib/shell.fish`：Bash/zsh/Fish 快捷命令及常驻父进程管理，不包含核心配置逻辑。
- `Makefile`：构建、测试和跨平台产物；`dist/` 不提交。
- `install.sh`、`uninstall.sh`：显式调用构建产物的薄包装。

## 开发约定

- 修改源码、构建、测试均不得自动更新系统安装；只有显式 install 才安装或升级。
- 升级可执行文件使用原子替换，不截断正在运行的文件。
- 代理变量仅注入目标命令及其子进程，不改变用户工作目录。
- 成功的 on/off 保持静默，保留目标命令的输出、退出码和信号处理。
- 默认仅监听本机 HTTP 端口，内网直连，其余走代理。
- 并发命令共享隧道；一个使用者退出不得关闭其他使用者或常驻隧道。
- 启动的子进程必须 Wait 回收；错误和信号路径也要清理。
- 含凭据的临时文件写入前即为 600，使用原子替换；不提交真实订阅或运行数据。
- `docs/` 为本地文档，保持忽略；公开说明维护在 `README.md`。

## 验证

需要 Go 1.24+。运行 `make test`、`make race`、`make vet`。
设置 `SP_TEST_MIHOMO=/path/to/mihomo` 可额外运行真实内核验证。
修改平台相关代码后运行 `make release`，确认 Linux/macOS、amd64/arm64 都能编译。
测试仅使用临时 HOME、随机端口和假订阅，不停止或覆盖用户正在使用的代理。
