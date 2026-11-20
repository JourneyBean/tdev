# Warp 远程文件树与 tdev 目录同步

2026-11-16 调研结论：当前 tdev 的附着通道没有建立 Warp 的远程文件树上下文。可先使用独立 Warp SSH 会话，或通过 tdev SSHFS 挂载浏览文件。当前窗口自动跟随 tmux 活动 pane 的目录仍需 Warp 客户端验证。

## 原因与证据

[Warp SSH 文档](https://docs.warp.dev/terminal/warpify/ssh/)说明，远程文件树由用户确认安装的 SSH extension 提供；它在远端读取文件并通过 SSH 与客户端通信。检测针对交互式 `ssh` 命令；脚本内 SSH 和 SSH alias 不触发该检测。

tdev 的 `internal/cli/terminal.go` 在 Go 内部通过 `sshx.Command` 启动 OpenSSH，远端命令直接执行 `tdev api session attach`，随后由 `internal/tmux/attach.go` 附着 tmux。`internal/sshx/ssh.go` 使用受管理的 ControlMaster 通道。因此，基于官方检测规则与源码，tdev 当前路径缺少 Warp 识别普通远程交互 shell 的入口。这是源码与文档推断，尚未在 Warp GUI 复现。

`internal/terminal/output.go` 重写 OSC 0/1/2 标题，保留其他 OSC。现有标题所有权逻辑没有普遍屏蔽目录消息。即使目录消息可透传，远程文件树仍需要远程文件访问后端和对应会话上下文；仅补标题或 OSC 7 不能据此认定问题已解决。

## 可用路径

### 独立 Warp SSH 会话

在 Warp 新建标签页，直接运行以下命令，按 Warp 提示启用 SSH extension，再在远端 shell 切换项目目录：

```sh
ssh dev-host
cd /home/developer/projects/demo
```

原 tdev 窗口继续管理 tmux 会话。此路径利用 Warp 官方支持的 SSH 文件树；两个窗口的目录独立，需要手动选择相同项目。进入 tmux 后文件树是否继续跟随活动 pane，应单独验收。

当前官方要求包括 macOS/Linux 客户端及远端、远端 bash/zsh、amd64/arm64，Linux 远端需要 glibc 2.31+。Windows 客户端当前没有该 extension。安装由用户在 Warp 中操作，tdev 不自动部署 Warp 后端。

### tdev SSHFS 挂载与本地 Warp shell

具备 SSHFS 运行环境时，在本地 shell 执行：

```sh
tdev mount dev/demo ./demo-mount
cd ./demo-mount
```

Warp 此时通过本地挂载路径访问远端文件。另一个标签页运行 `tdev open dev/demo`。独立 `mount` 的生命周期由显式 `unmount` 管理；路径和身份检查沿用 tdev 既有行为。这个方案具备现有命令基础，本轮未执行实际挂载或 Warp 文件树验收。

需要一键体验时，可设计本地 shell 函数，先挂载再由父 shell 执行 `cd`。独立 tdev 子进程无法修改调用 shell 的 cwd。任何在挂载目录运行的工具都使用本地执行环境，构建与测试仍应放在远端运行。SSHFS 上文件监视、索引和 Git 扫描的体验需实测。

## 适配候选与边界

| 候选 | 证据与判断 |
| --- | --- |
| 将 `tdev open` 加入 Warpify Subshells 的 Added commands | [官方支持自定义命令和正则](https://docs.warp.dev/terminal/warpify/subshells)。可作为低成本人工探针；文档未保证它能穿过 tdev/tmux 建立 SSH extension 文件树。 |
| 发 OSC 7 或透传 Warp shell integration | 可研究目录信号，但远端身份、后端握手、pane 切换、多客户端与断线恢复均需验证。本轮未测 Warp 对 OSC 7 的处理。 |
| `warp://` 定位目录 | [官方 URI 文档](https://docs.warp.dev/terminal/more-features/uri-scheme/)支持为新窗口/标签页传 path，明确无法控制已有 tab/pane，也没有列出远端主机与目录绑定接口。适合打开挂载目录的新标签页。 |
| 复用旧 Warp tmux wrapper | 官方说明旧 tmux-based Warpification 已移除。该旧机制与普通 tmux 支持是不同问题；移除本身不能证明普通 tmux 全部不兼容。 |

建议先采用独立 SSH 或挂载方案。自动同步适配的下一步是在真实 Warp 中对照直接 SSH、SSH 后进入 tmux、tdev open、自定义 subshell 命令四种路径，记录文件树所属主机、根目录与 pane 切换行为；通过后再决定是否增加产品功能。

## 验证范围

真实 tmux 集成测试的结果及 GUI、SSHFS 验收范围见 [验收记录](acceptance.md)。
