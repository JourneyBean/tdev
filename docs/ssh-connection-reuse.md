# 本地 daemon 与 SSH 连接管理

`tdev daemon start|restart|stop|status` 管理当前配置根对应的本地监督进程。用户命令首次需要远端连接、托管转发或 SSHFS 时自动启动 daemon；远端继续使用系统 OpenSSH 和按次执行的 tdev API。

## 连接分工

| 功能 | 连接与生命周期 |
| --- | --- |
| API、项目发现、completion、远端 shell 查询 | 同一 SSH 别名及 profile 共享控制 master |
| open、scratch、sh 的交互终端 | 在控制 master 上打开独立 PTY channel；detach 只关闭本次附着 |
| 托管 forward | 每条映射使用独立 OpenSSH master；相同配置和映射复用监听 |
| SSHFS | daemon 启动独立 SSHFS/SFTP 连接，沿用 SSHFS reconnect |
| scp | 保留独立数据传输连接 |
| web | 保留功能名称，等待 HTTP 服务实现 |

转发、文件传输与 SSHFS 的流量独立于控制连接。服务端 MaxSessions 配额仍会限制共享控制连接中的终端与 API 并发。项目定义、revision 和 tmux 操作锁继续由目标机器管理。

## open 的功能与使用权

```sh
tdev open dev/demo                       # shell + forward
tdev open dev/demo --mount               # shell + forward + sshfs
tdev open dev/demo --only shell
tdev open dev/demo --only shell,forward
tdev open dev/demo --only forward
tdev open dev/demo --only sshfs
tdev open dev/demo --only forward,sshfs
```

选中 shell 时需要交互终端，并 ensure 项目会话。该次 open 为附属资源持有使用权；shell 退出时释放，最后一个使用者离开才清理临时映射和本次新建挂载。多个窗口共享相同映射或挂载，退出其中一个窗口保留其他窗口所需的资源。预先存在的挂载及显式后台打开的资源继续保留。安全卸载遇到忙碌或身份变化时保留挂载，并在 daemon status 中记录 warning。

只选择 forward/sshfs 时启动后返回，可用于非交互命令和 `--json`；资源后台保留。服务模式读取已有项目定义，不创建 tmux 会话或重跑项目命令。`--only` 接受逗号分隔及重复选项；空列表、未知功能、与 `--mount` 混用，以及尚未实现的 web 在操作前报错。scratch 使用 shell。

`daemon stop` 关闭控制连接和托管转发，清除这些资源的恢复登记；显式保留的 SSHFS 通过 `tdev unmount` 安全卸载。停止标记阻止 completion 和后台重连自动拉起 daemon，运行 start 或 restart 恢复使用。`daemon restart` 保留后台转发登记并恢复；仍活跃的 open 重新获取使用权和终端附着。显式 `tdev forward` 继续在前台监督本次独立转发，Ctrl-C 结束。

## 并发与故障恢复

每个配置根使用用户私有运行目录和权限为 0600 的 Unix socket。目录校验属主及权限，短路径适配 Unix socket 长度限制。start/restart/stop 用跨进程生命周期锁串行化；服务进程持有独占存续锁，只有持有者能移除旧 socket。

控制连接按 SSH 别名、绝对 profile 路径及 SSH 参数区分，每个目标单独串行化获取。首次交互认证在调用终端执行，然后将 detached master 交给 daemon；completion 和后台重连始终使用 BatchMode。控制 channel 禁止 socket 缺失时退回直接连接，避免隐式创建未经管理的传输。

OpenSSH keepalive 检测断线，daemon 每两秒检查已登记的控制 master，并对可恢复故障重建连接。认证及主机密钥错误保留诊断，后台停止自动尝试；显式操作可再次发起认证。终端保留原有重试预算，并检查原 session ID/generation；业务写操作保持现有结果核实和 revision 语义。

每次重建 master 使用新目录及 socket，旧代次清理只作用于旧目录。后台转发独立使用 forward 重连策略。daemon 正常关闭会清理自己拥有的传输；异常退出后再次 start 会清理私有目录内的旧 master，再恢复登记的连接和后台转发。远端 tmux 会话继续运行。

shell 附属资源的使用权由独立 Unix HTTP 请求维持。正常退出显式释放；进程异常退出时 socket 关闭也会触发释放。创建后尚未建立存续请求的使用权在十秒后回收。登记/最后释放期间串行化映射启动和取消，避免旧监听尚未关闭时创建新监听。

## 运维和后续扩展

运行目录保留连接参数引用和后台转发定义；文件权限为 0600，保存内容沿用客户端配置的隐私边界。重启系统或清理系统临时目录后需重新打开服务。SSHFS 的现有挂载记录继续存放在客户端配置根。

daemon 使用启动时的本机进程环境。安装新工具、切换 SSH agent 或修改 SSH 配置后，可运行 `tdev daemon restart` 使后续连接使用新环境及配置。已有控制连接沿用建立时的认证和路由。

本轮为后续 HTTP 服务提供本地监督进程和控制接口。web 监听、域名路由及 HTTP 故障恢复另行实现。

## 依据和验收

实现：`internal/daemon`、`internal/cli/daemon.go`、`internal/cli/open_features.go`。协议依据：[OpenSSH 控制操作](https://man.openbsd.org/ssh.1)、[连接复用与后台保留](https://man.openbsd.org/ssh_config.5)、[MaxSessions](https://man.openbsd.org/sshd_config.5)、[SSHFS reconnect](https://github.com/libfuse/sshfs/blob/master/sshfs.rst)。

真实连接计数、双窗口退出、重启及异常退出恢复由隔离 SSH/PTY 测试验证；挂载使用权与安全失败通过受控后端验证。完整命令、结果及真实 FUSE/macOS 等覆盖限制集中记录在 [验收记录](acceptance.md)。
