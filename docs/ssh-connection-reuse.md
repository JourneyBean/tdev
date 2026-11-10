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

`daemon stop` 关闭控制连接和托管转发，清除这些资源的恢复登记；显式保留的 SSHFS 通过 `tdev unmount` 安全卸载。停止标记阻止 completion 和后台重连自动拉起 daemon，运行 start 或 restart 恢复使用。`daemon restart` 保留后台转发登记并恢复；仍活跃的 open 重新获取使用权和终端附着。`tdev forward [server] -L/-R ...` 创建具名临时转发组，默认等监听就绪后返回并后台保留；`--foreground` 随命令退出释放。`ls --type forward` 查询当前客户端的临时转发，`stop server/forward-NAME` 停止整组。项目保存的规则统一使用 edit 修改，通过 open 运行。

## 并发与故障恢复

每个配置根使用用户私有运行目录和权限为 0600 的 Unix socket。目录校验属主及权限，短路径适配 Unix socket 长度限制。start/restart/stop 用跨进程生命周期锁串行化；服务进程持有独占存续锁，只有持有者能移除旧 socket。

控制连接按 SSH 别名、绝对 profile 路径及 SSH 参数区分，每个目标单独串行化获取。首次交互认证在调用终端执行，然后将 detached master 交给 daemon；completion 和后台重连始终使用 BatchMode。控制 channel 禁止 socket 缺失时退回直接连接，避免隐式创建未经管理的传输。

OpenSSH keepalive 检测断线，daemon 每两秒检查已登记的控制 master，并对可恢复故障重建连接。认证及主机密钥错误保留诊断，后台停止自动尝试；显式操作可再次发起认证。终端保留原有重试预算，并检查原 session ID/generation；业务写操作保持现有结果核实和 revision 语义。

R 映射断线后，旧远端连接的监听释放可能晚于客户端重连。曾经成功建立的 R 映射遇到远端绑定失败时，按转发重连预算等待恢复；首次创建时的端口冲突直接报错。长时间网络分区下，恢复取决于旧远端连接释放监听及配置的重试预算。

每次重建 master 使用新目录及 socket，旧代次清理只作用于旧目录。后台转发独立使用 forward 重连策略。daemon 正常关闭会清理自己拥有的传输；异常退出后再次 start 会清理私有目录内的旧 master，再恢复登记的连接和后台转发。远端 tmux 会话继续运行。

shell 附属资源的使用权由独立 Unix HTTP 请求维持。正常退出显式释放；进程异常退出时 socket 关闭也会触发释放。创建后尚未建立存续请求的使用权在十秒后回收。登记/最后释放期间串行化映射启动和取消，避免旧监听尚未关闭时创建新监听。

## 运维和后续扩展

`tdev ps` 按机器 → daemon → SSH master → 操作分配和实际通道展示当前用户的快照。默认查询本机与全部已登记服务器，`--server local` 或 `--server dev` 缩小范围，`--json` 输出协议数据。每台机器可包含多个配置根的 daemon；远端枚举当前 SSH 登录用户的 tdev daemon，服务器查询保持一跳。远端本来只运行按次 tdev API 时，daemon 列表为空。

本机通过私有 Unix socket 查询，远端通过独立的非交互 SSH 调用 `api ps`；查询保持 daemon 停止状态。部分机器不可达时返回 9，文本保留其他结果，JSON 的 error.details 包含完整快照。旧远端缺少 ps-v1 时显示 upgrade-required；旧 daemon 显示 running-legacy 和重启提示。首次升级后可分别在本机和需要查看明细的远端运行 `tdev daemon restart`。

连接显示用途、SSH 别名、master PID、资源状态、使用权数量和后台保留属性。实际 SSH 通道展示 ID、类型、开关状态与缓冲字节。tdev 的 allocation 列出 API、终端、安装引导的项目和客户端 PID；其 ID 属于本机登记，与 SSH 通道 ID 分开。SSHFS 显示登记的 helper PID，独立传输和通道未知；本机转发标记为 local。独立 scp、非 tdev 管理的 SSH 和系统 sshd 进程属于各自的管理范围。

OpenSSH 从 [10.3](https://www.openssh.org/txt/release-10.3) 起提供 `ssh -O channels`。旧版及未知输出将实际通道标为不可用，保留 tdev 的操作分配和资源状态。master-running 依据本地控制探测，网络故障可能仍在 keepalive 检测窗口内；快照跨多个进程，查询期间的短操作可能已经结束。已有旧客户端的操作可通过新版 OpenSSH 的实际通道观察，其 tdev 分配登记需使用支持该功能的客户端。原始通道描述、命令参数、环境与凭据均不写入快照。

安装、`tdev upgrade` 与 `tdev upgrade --rollback` 在提交二进制后调用目标程序的 post-install hooks，先迁移当前配置的托管 tmux，再刷新运行中的 daemon。daemon 按启动时程序摘要判断：相同内容保持原进程，变化时重启；后台转发自动恢复，活跃终端按重连策略重新附着原会话。已停止的 daemon 保持停止。处理范围由配置根确定，其他配置根分别运行 `tdev --config-dir <目录> post-install`。

`tdev upgrade --remote dev` 在远端执行目标程序的处理，当前附着进程继续运行原版本；后续请求和断线重连执行新远端程序。自动重连耗尽或认证失败后，运行 `tdev open dev/demo` 恢复原会话。协议不兼容时先安装兼容的本地与远端版本，再重新 open。

hooks 部分失败后用 `tdev post-install` 或 `tdev post-install --remote dev` 重试。旧客户端首次升级缺少调用新入口的逻辑，升级后用目标程序补跑 post-install；端口映射在 daemon 重启期间可能短暂中断。回滚目标缺少新入口时返回部分成功并保留已恢复的二进制。

运行目录保留连接参数引用和后台转发定义；文件权限为 0600，保存内容沿用客户端配置的隐私边界。重启系统或清理系统临时目录后需重新打开服务。SSHFS 的现有挂载记录继续存放在客户端配置根。

daemon 使用启动时的本机进程环境。安装新工具、切换 SSH agent 或修改 SSH 配置后，可运行 `tdev daemon restart` 使后续连接使用新环境及配置。已有控制连接沿用建立时的认证和路由。

本轮为后续 HTTP 服务提供本地监督进程和控制接口。web 监听、域名路由及 HTTP 故障恢复另行实现。

## 依据和验收

实现：`internal/daemon`、`internal/cli/daemon.go`、`internal/cli/open_features.go`。协议依据：[OpenSSH 控制操作](https://man.openbsd.org/ssh.1)、[连接复用与后台保留](https://man.openbsd.org/ssh_config.5)、[MaxSessions](https://man.openbsd.org/sshd_config.5)、[SSHFS reconnect](https://github.com/libfuse/sshfs/blob/master/sshfs.rst)。

真实连接计数、双窗口退出、重启及异常退出恢复由隔离 SSH/PTY 测试验证；挂载使用权与安全失败通过受控后端验证。完整命令、结果及真实 FUSE/macOS 等覆盖限制集中记录在 [验收记录](acceptance.md)。
