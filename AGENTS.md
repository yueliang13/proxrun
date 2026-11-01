# proxrun

proxrun 取自 proxy + run，是基于 mihomo 的轻量命令行 HTTP 代理工具。
用户显式选择哪些命令走代理：按需让命令走代理，用完自动收回。
默认一次性使用，命令结束后释放代理；`--start` 保持常驻，直到显式关闭。
`proxy-on` 和简写 `po` 支持相同参数。
当前 CLI 名称及安装目录沿用 `simple-proxy`，文档中的命令和路径应与实际实现一致。

## 代码结构

- `bin/simple-proxy`：CLI 入口。
- `lib/core.sh`：配置加载和自检。
- `lib/runtime.py`：进程身份验证、并发共享、启停和回收。
- `lib/build-config.py`：使用 PyYAML 合并订阅和本地策略。
- `lib/shell.sh`：Bash/zsh 快捷命令及常驻模式回收。
- `config/`：可公开提交的默认配置和规则模板。
- `install.sh`、`uninstall.sh`、`scripts/`：安装、卸载和维护。

## 开发约定

- 代理变量仅注入目标命令及其子进程，不改变用户的工作目录。
- 成功的 on/off 保持静默，错误必须可见，保留目标命令的退出码。
- 默认仅监听本机 HTTP 端口，内网直连，其余走代理。
- 并发命令共享隧道；普通命令结束不得关闭其他使用者或常驻隧道。
- 含凭据的文件写入前即须限制为 600，不提交真实订阅、节点凭据或运行数据。
- `docs/` 为本地文档，保持 Git 忽略；公开使用说明维护在 `README.md`。

## 验证

开发依赖见 `requirements.txt`。运行 `bash tests/smoke.sh`，测试使用临时目录和假订阅。
设置 `SP_TEST_MIHOMO=/path/to/mihomo` 可额外验证真实内核。
测试时不要停止或覆盖用户正在使用的代理和订阅。
