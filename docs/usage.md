# 使用指南

项目配置与客户端偏好见[配置指南](configuration.md)，安装、补全与 AI skill 见[安装指南](installation.md)。

## 创建、查看和编辑

```sh
tdev create                      # 选择位置，再配置项目
tdev create --server dev
tdev create dev/demo --directory /home/developer/projects/demo
tdev show dev/demo
tdev edit dev/demo
tdev edit dev/demo --directory /home/developer/projects/new-demo
tdev override dev/demo --forward FORWARD_ID --local-port 18000
```

create 和 edit 复用逐项向导，edit 预填已有值：项目目录、启动窗口、端口映射及保存确认。目录支持绝对路径、`~/路径` 与 Tab 补全；目录不存在时询问是否创建，默认不创建。确认后在项目所在机器创建目录及缺失的父目录，随后取消向导仍保留目录。非交互 `--directory` 要求目录已存在。初始窗口默认为 `main`。输入支持方向键、Home/End 和删除，兼容 UTF-8、空格与引号。

添加映射时选择 L/R，直接输入 `localhost:8000`、`service.example.invalid:3000` 或 `[::1]:8000`。监听端口默认取服务端口。远端 L 映射的默认监听端口保存在项目定义，当前客户端的端口调整单独保存为 override。可连续添加或跳过。项目转发的增、删、改统一使用 `edit`；保存后通过 `open` 或 `open --only forward` 建立监听。

项目文件使用具名项目、`dir`、`windows` 和 `ports` 的简洁 YAML，支持单文件及 include 拆分，见[项目配置](configuration.md)。UUID 与 revision 保存在独立内部记录。通过 CLI 修改会检查 revision 并备份源文件。机器接口完整替换使用 `edit --stdin --expected-revision N`，输入为 show 的 `definition` 对象。show 同时展示原始定义、override 与最终生效配置。窗口启动命令在新建会话时执行。

## 会话和窗口

托管会话会把窗口提醒汇总到终端标题：有待查看提醒时显示 `[!]`，切换到对应 tmux 窗口清除该窗口的提醒，全部清除后恢复普通标题。终端支持焦点上报时，失焦期间当前窗口收到 bell 也保留标记，重新聚焦时清除当前窗口的标记。新会话自动配置，已有会话在下次通过 tdev 附着时补齐；终端需允许应用修改标题。

独立临时会话使用 `scratch`，支持本机和已登记服务器：

```sh
tdev scratch dev --name debug          # 新建并进入远端临时 session
tdev scratch local                    # 本机创建，自动生成唯一名称
tdev scratch dev --dir '~/projects'    # 在目标机器上展开 home 路径
tdev open dev/debug                   # 恢复已有临时 session
tdev open debug                       # 按名称查找项目或 scratch
tdev ls --type scratch --server dev   # 筛选临时会话
```

省略服务器且仅有本机可用时直接使用本机；配置了远端服务器时显示选择器，JSON 调用需显式指定服务器。默认从目标用户的 home 启动，`--dir` 接受绝对路径或 `~/` 前缀；远端 home 路径应加引号。名称重复时返回冲突。恢复统一使用 `tdev open <名称>` 或 `tdev open <服务器>/<名称>`；`scratch` 仅用于新建。`--json scratch ...` 创建会话并返回身份信息，终端附着使用交互命令。

`open` 的裸名称查找先使用本机，再使用已登记服务器的缓存索引和实时查询；显式服务器前缀可固定目标。同一服务器的项目与 scratch 同名时返回冲突，需要先调整名称。scratch 的 open 直接恢复会话，`--mount` 仅适用于项目。缓存中已知的 scratch 结束后返回会话缺失；完全未知的名称沿用项目创建向导。

scratch 通过 tmux 标记发现，独立于项目定义和 `save`。detach 或 SSH 断线后进程继续运行，最后一个窗口退出后会话结束。自动重连校验原 session ID 和 generation；detach 后可按名称恢复。`ls` 默认同时展示项目和 scratch，并标明类型；`--type project` 仅显示项目。远端不可达时保留带时间的缓存；已结束的 scratch 在成功刷新后消失。远端需支持 `scratch-v1`；旧版服务器的项目列表继续展示，并提示升级以发现 scratch。

```sh
tdev open dev/demo
tdev sh --name debug              # 当前项目中新建临时窗口
tdev sh dev/demo --name logs --save
tdev save
tdev stop dev/demo -y
```

新建项目 tmux session 使用 `tdev-project-<name>`，如 `tdev-project-demo`，归属按项目 UUID 标记识别。旧版或手工改名的托管 session 可直接复用，名称差异显示 warning。普通会话使用项目逻辑名称时与项目独立。`tdev-project-<name>` 被无匹配 UUID 的会话占用时，`ls` 显示 `unmanaged` 和 warning，`open` 保留原会话并创建独立的 `tdev-project-<UUID>` 托管会话（UUID 去掉连字符）。scratch 使用 `tdev-scratch-<name>`，省略名称时使用自动生成的 UUID。warning 保持列表刷新成功；多个会话使用同一项目 UUID 时，接管或停止操作需先确认归属。

open 遇到项目不存在时，确认后进入创建向导，保存后继续打开。新项目默认窗口为 `main`，已有项目沿用保存的名称。sh 的新窗口默认叫 `shell`，`--name` 可指定名称。

## 从客户端连接

运行连接向导，自动读取已登记的服务器、SSH `Host` 和 `Include` 文件；可以选择、搜索，或直接填写 `user@host:port`。向导验证连接后列出远端已有项目，保留已有配置，并可调整这些项目在本机使用的端口。远端缺少 tdev 时可确认安装官方签名版本；缺少 tmux 时提示先准备依赖。

```sh
tdev setup
tdev ls
tdev open dev/demo
```

已有服务器再次运行向导时，保留其连接信息、远端配置目录及本机偏好。新增主机使用私有 SSH profile，引用现有身份和 SSH 配置。参数形式继续供脚本使用：

```sh
tdev setup --name research --ssh-alias my-existing-alias --remote-binary .local/bin/tdev
# 或提供主机信息，使用现有 identity/SSH agent
tdev setup --name research --host server.example.invalid --port 22 --user developer \
  --identity-file ~/.ssh/existing-key
tdev ls
tdev open demo
tdev open research/demo --reconnect-attempts 5 --reconnect-interval 2s
tdev sh demo --save
tdev save demo
tdev stop demo -y
```

没有项目参数的 `save`、`sh`、`stop` 在本机 tmux 中依据项目标记推断，询问确认
后操作本机会话，不回连服务器。支持 `-y` 和 `yes | tdev save`。显式 stop 会提示
其他客户端断开；项目启动定义继续保留。

远端 `open` 使用系统 OpenSSH 附着，客户端过滤终端标题。正常 detach 后退出；
重连检查 session ID 和 generation，原会话消失后结束，不进入替代会话。
`--reconnect-attempts` 支持 `0`、非负整数和 `always`；数值是初次连接之外的重试次数。
等待和连接期间 Ctrl-C 退出；已连接时键盘 Ctrl-C 仍交给远端应用。

主配置的 `client` 块支持连接、override 和全局 reconnect/SSH 偏好；`client.include` 可拆分文件。旧客户端配置在首次保存时备份迁移，完整格式见[配置指南](configuration.md)。支持每台服务器的 reconnect 覆盖。
优先级为命令参数、服务器覆盖、全局设置。setup 也提供
`--terminal-reconnect-attempts`、`--terminal-reconnect-interval` 及对应 forward 参数。

```yaml
client:
  reconnect:
    terminal: {attempts: 5, interval: 2s}
    forward: {attempts: 5, interval: 2s}
    http: {attempts: always, interval: 5s}
  ssh:
    connect_timeout: 10s
    server_alive_interval: 15s
    server_alive_count_max: 3
```

项目标识仍使用设计中的 ASCII slug；目录、窗口显示名、命令和文件名支持 UTF-8/CJK。
诊断沿用用户 locale；tmux 查询使用 `-u`，保持 CJK 与机器接口分隔符的 UTF-8 输出。无效 UTF-8 和路径中的控制字符会明确报错。

## 转发

`ls`、`open` 每次读取远端最新配置，再合并本机 `client.overrides`。调整本机端口可在向导中完成，也可以使用：

```sh
tdev override demo                       # 查看远端与合并后的配置
tdev override demo --forward api --local-port 8080
tdev override demo --reset               # 恢复远端默认值
```

`api` 可由项目文件中端口的 `name` 指定，也是机器定义中的稳定 ID，可从 `tdev show` 查看。项目目录、窗口和命令实时读取，本机只保存端口差异及附加转发。

```sh
tdev forward dev -L 3000:localhost:8000          # 等监听就绪后返回，后台保留
tdev forward dev -R 9000:localhost:3000
tdev forward dev -L 8000 --foreground          # 前台运行，Ctrl-C 释放
tdev forward local -L 8080:localhost:8000       # 本机 TCP 转发
tdev forward ls
tdev stop dev/forward-NAME -y                  # 使用 forward ls 中的名称
tdev edit dev/demo                            # 修改项目保存的转发
tdev open dev/demo --only forward              # 后台运行项目预设
```

与 OpenSSH 一致，`-L` 本机监听、`-R` 远端监听，均为
`[bind_address:]port:host:hostport`。支持 `port:port`、`port` 及方括号 IPv6。
默认绑定回环地址，不接管已有连接。反向映射将流量送到建立连接的客户端，多客户端可能争用远端监听。

每条映射拥有独立 SSH master 和私有控制 socket；通过 OpenSSH 控制请求确认监听
建立后才报告 ready。端口冲突不接管其他连接，失败和重连互相隔离。
`forward [server]` 要求显式提供至少一条 `-L/-R`，按 server 解析参数。省略 server 时，配置了远端的交互终端提供选择；非交互调用需显式指定。只有本机时默认 local。每次运行生成 `forward-…` 名称，多条规则组成一组，在 `forward ls` 中以 temporary 类型列出，可通过 `stop server/name` 整组停止。临时规则保存在当前客户端的 daemon 恢复登记中。项目共享规则由 `edit` 修改，`override` 调整当前客户端的监听偏好。

默认 forward 等监听建立成功后返回，由 daemon 后台维持；`--foreground` 持有使用权直到命令退出，Ctrl-C 释放本次转发。启动批次中任何规则失败时清理整组。`daemon restart` 恢复后台转发，`daemon stop` 清除恢复登记。临时转发在当前客户端的 `forward ls` 和 `daemon status` 中可见。open 带 shell 时按使用权释放转发，只选 forward 时后台保留。
ready 表示 SSH 监听已建立；应用健康由应用自身检查。

## 会话与转发列表

`tdev ls` 是工作项概览，每项为一个项目、scratch 或临时 forward 组。
默认展示已启动的工作项，健康状态统一显示 `running`，重连与失败保留
`connecting`、`waiting`、`failed`。项目只启动 Web、TCP 或挂载时也进入概览。
`FORWARD`、`WEB` 展示完整映射，多条映射在对应列续行，目录省略。
Web 入口优先使用已登记路由的访问端口，未登记服务使用客户端配置端口。

```sh
tdev ls
tdev ls --wide                         # 展开资源对应的 CONNECTION、PID
tdev ls --include-inactive             # 包含未启动配置
tdev project ls                       # 查看全部项目定义及映射
tdev session ls                       # 每行一个项目或 scratch 会话
tdev forward ls                       # 每行一条已启动 TCP 映射
tdev web ls                           # 每行一条已登记 Web 路由
tdev forward ls --include-inactive     # 同时显示配置中的 TCP 映射
```

`session ls` 展示会话状态、真实 tmux 客户端数和会话名称。
旧服务器缺少客户端数时显示 `—`。`forward ls` 展示归属、运行状态、完整映射和错误，
未建立且被显式包含的配置标为 `configured`。`web ls` 的未登记配置标为 `stopped`。
列表支持 `--server`、`--all` 和 `--json`；`--all` 沿用查询全部服务器的含义。
发现失败保留带时间的缓存，缓存工作项状态显示 `unknown`。
JSON 的每台服务器 `items` 对应当前列表视图，保留 `projects`、`scratches`、`forwards`、
`web` 等完整快照兼容原有调用。`--wide` 的 `connections` 补充资源与连接的关联。
旧 `ls --type project|scratch|forward` 和 `web status` 继续可用。

`tdev connection ls` 与 `tdev ps` 使用相同的连接表：
`CONNECTION PID SERVER TYPE STATUS RESOURCES`。每个 SSH master 一行，
共用连接的资源合并列出；同一 daemon 承载的本机 TCP/Web 合并为一个进程行。
`SERVER` 表示进程所在的观测机器，PID 属于该机器；`RESOURCES` 使用带目标服务器的
工作项名称，例如 `dev-host/api/session`、`dev-host/scratch/debug`。
SSHFS 展示独立挂载进程 PID。`--verbose` 展开 daemon、socket、分配记录和通道。
JSON 的 `connections` 是合并后的连接表，`hosts` 保留原始逐机器快照。

连接 ID 由实际 socket 和 PID 生成，资源列表与连接列表使用同一标识；
连接替换后标识变化。已存在但没有客户端附着的 tmux 会话继续显示 `running`，
宽表中的连接和 PID 显示 `—`。共享项目 TCP 转发记录所有使用它的资源，
前台使用权释放时移除对应归属，后台归属随恢复登记保留。
列表使用已有 daemon 查询，服务启动通过 `open` 或 `daemon start` 完成。

## 传输与挂载

```sh
tdev push demo ./source ./destination
tdev pull demo ./remote-source ./local-destination
tdev open dev/demo --mount        # 打开时显式挂载
tdev mount dev/demo ./empty-mountpoint
tdev unmount dev/demo ./empty-mountpoint
```

远端相对路径以项目根目录为基准；本地相对路径以 cwd 为基准。单文件目标为已有目录（例如 `./`）时，保存为该目录下的源文件名；指定目标文件名时按该名称保存。目录传输将源目录
内容合并进指定目标目录，不因目标是否存在而增加一层目录，不删除目标的其他文件。
覆盖已有文件需确认或 `-y`。传输使用系统 scp/SFTP、编号暂存文件、SHA-256 校验和
逐文件原子替换；提交前检查目标是否变化。目录按文件提交，部分失败时报告已提交进度。
传输拒绝符号链接和设备文件；Mac 目标会在写入前拒绝大小写或 Unicode 规范化重名。

open 默认进入终端并建立 TCP 转发及项目已声明的 Web 服务。显式 `--mount` 或独立 mount 才挂载，路径保存为本机 override，可通过 `override --mount-path` 配置。再次 mount 可省略路径。带 shell 的 open 在退出时释放本次新建挂载；已有挂载、其他窗口所需挂载和独立 mount 继续保留，通过 unmount 显式释放。本机项目的 mount 返回已有项目目录。

挂载检查目录内容、实际来源和文件系统 ID；复用正确来源，不覆盖其他挂载。
外部已有挂载可以复用，但不会被自动接管。unmount 使用本地记录，断网时也可操作；
身份不符或 busy 时拒绝，不强制卸载，也不按单独 PID 杀进程。

SSHFS 以独立前台进程运行，避免当前 macFUSE 的后台 fork 问题；mount 命令退出后
文件系统继续运行，诊断位于私有日志。Mac 使用 noappledouble/noapplexattr 和 iconv
文件名转换，并通过远端独立检查验证 CJK 文件名及元数据防护；验证需要项目根目录可写。
Mac 挂载按 NFC 远端文件名工作，混合 NFC/NFD 名称仍受 SSHFS/macFUSE 限制，不会自动
重命名远端文件。详见[验收边界](acceptance.md#尚未完成的环境或人工验收)。

## 诊断日志

失败操作自动写入当前配置根下的 `logs/diagnostics.jsonl`。默认路径为 `~/.config/tdev/logs/diagnostics.jsonl`；`XDG_CONFIG_HOME` 和 `--config-dir` 同时影响配置与日志位置。目录补全失败时，向导显示错误类别和日志路径。命令结束时，人类可读输出显示本次日志位置；`--json` 保持单一协议响应。

```sh
tdev --debug setup
tail -n 30 ~/.config/tdev/logs/diagnostics.jsonl
```

`--debug` 额外记录成功的命令和 SSH/API 请求。每条 JSON 记录包含 UTC 时间、进程号、操作、服务器名称、耗时，以及失败时的错误码、原因类别和可用的进程退出码。日志仅保存已知错误类别，不保存命令参数、目录输入、环境变量、凭据或原始远端输出；未识别的原因记为 `operation_failed`，可结合操作和退出码定位。补全查询的独立时限为 3 秒，超时记为 `deadline_exceeded`。

日志目录权限为 0700，文件为 0600。单文件上限为 1 MiB，保留三个轮转备份。日志写入失败保留原操作结果，人类可读输出提示日志不可用。
