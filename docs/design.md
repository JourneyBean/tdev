# tdev 产品与技术设计

日期：2026-11-02

状态：开发交接基线

读者：负责实现、测试和发布 tdev 的开发者与开发代理

tdev 是通过 SSH 使用、以项目组织和恢复 tmux 工作环境的轻量工具。项目目录、窗口启动命令和保存的端口映射统一存放在远端；客户端管理连接、用户偏好和本地访问入口。远端无需数据库、Web API 或常驻 tdev 服务。

本文记录产品要求、接口行为与技术取舍。“实现默认决策”可根据实测调整，并同步更新设计。HTTP 域名映射留待后续阶段。

## 1 产品定位与范围

### 1.1 已确认的决定

- 自研，不依赖 smug、tmuxp 或 tmuxinator 作为运行后端。
- 使用 Go；分发预编译二进制，用户无需安装 Go。
- 产品名使用 tdev，原 devc-remote 是迁移来源；不限定开发容器。
- 目标是可通过 SSH 使用的兼容 Unix 环境，远端需要 tmux 和兼容 shell。
- 一个项目对应一个受 tdev 管理的 tmux session。
- 项目启动时创建声明的窗口、执行开发服务命令、恢复保存的转发；已有 session 时连接并复用。
- 支持临时 shell 窗口，也支持将其保存进项目启动配置。
- save 保存启动配置和工作环境结构；运行中的进程由 tmux 会话维持。
- 省略项目名时使用明确的 tmux 项目标记推断，并询问 yes/no；支持 -y 和标准输入确认。
- 转发方向、参数语义与 OpenSSH 保持一致，-L 和 -R 均支持保存。
- 反向映射明确监听位置和流量方向，说明多客户端可能争用远端监听。
- 终端重连次数可设为 0、正整数或 always，间隔可配置；重连期间 Ctrl-C 退出。
- 远端提供非交互 api 子命令，供客户端经 SSH 调用。
- 后续提供 http://demo.localhost:<固定代理端口> 形式的 HTTP 入口，其重连策略与终端独立。

### 1.2 用户价值

一次设置连接后，以项目名进入远端开发现场，不再记忆端口、目录和启动命令。关闭客户端或网络断开后，远端 tmux 中的工作继续运行。换一台客户端连接同一服务器，可以读取同一份项目定义。

### 1.3 第一版范围

交付 setup、ls、open、save、sh、forward、stop、push、pull、mount、unmount，以及对应的 api 接口、安装升级与必要诊断。优先支持 macOS/Linux 客户端及 Linux 远端，提供 amd64/arm64 产物；macOS 上的本机能力应纳入基础验证。Windows 初期通过 WSL 使用，不承诺原生 Windows 支持。

实现默认决策：第一版以窗口为主要编排单位。默认每个窗口一个 pane；遇到用户手工拆出的多 pane 窗口，save 必须明确报告尚不支持完整保存，不能静默丢失布局。可先拒绝该次保存并保留原配置。完整 pane 布局采集属于后续扩展。

不做：容器创建、集群/GPU 调度、浏览器 IDE、团队账号与计费、通用任务数据库、进程检查点、跨服务器迁移运行中进程、自动安装项目语言环境。

## 2 架构与职责

```text
本地 tdev
  ├─ setup、项目选择、用户确认
  ├─ 系统 OpenSSH → 远端 tdev api → 配置文件与 tmux
  ├─ 系统 OpenSSH -t → 远端 tmux attach
  ├─ 系统 OpenSSH -N → 端口转发
  ├─ 文件传输、SSHFS 挂载
  └─ 后续本地 HTTP 代理
```

### 2.1 三种信息的归属

| 信息 | 权威位置 | 示例 |
| --- | --- | --- |
| 项目定义 | 远端 | 项目根目录、窗口、启动命令、保存转发 |
| 连接与偏好 | 当前客户端 | SSH 别名、客户端 override、重连策略 |
| 临时运行状态 | 所在机器 | 客户端转发 PID、挂载点；远端 tmux session |

项目定义以远端版本为准。私钥保留在本地或 SSH agent 中，连接偏好及挂载、转发状态由客户端管理。

### 2.2 用户入口与机器接口

用户命令直接操作项目，包含 create/show/edit、ls、open/save/sh/stop、forward、push/pull 与 mount/unmount。连接与安装工具保留独立入口。local 是内置服务器名，表示运行当前 tdev 的机器，无需 setup。

`tdev api` 是客户端在目标机器上调用的机器接口，使用明确参数与结构化 stdin，操作当前机器，无业务确认。session attach 是要求终端和 attachment identity 的传输通道。API 按 project、session、transfer、binary 与 mount-probe 分组；窗口和转发定义通过 project get/set 更新。

`--ssh-config`、`--yes` 属于用户入口；`--config-dir`、`--timeout` 和 `--json` 两层共用。远端依赖 tdev、tmux、shell 与系统 SSH 服务；tdev 不监听控制端口。

## 3 项目标识与上下文

### 3.1 项目定位

项目接受完整的 server/project 标识。local/demo 指当前机器，dev/demo 指登记的远端服务器。裸名称先查本机，再按远端名称排序搜索，自动匹配后显示完整目标。发现索引命中的目标不可达时报告该目标的连接错误。缓存未命中时逐台读取指定项目定义，PROJECT_NOT_FOUND 继续查找，其他错误保留；目标解析独立于 tmux 状态。create 的裸名称默认本机，空参数向导先选择位置。

ls 默认查询本机和全部已配置远端，--server 限定范围，--all 与默认行为一致。终端先显示系统临时目录中的私有缓存和 syncing，再并发更新为 synced；会话名称或归属 warning 随项目展示，刷新成功。失败保留缓存和时间；连接失败显示 unreachable，旧 API 显示 upgrade-required，服务器应用错误显示 error。成功空结果清空对应快照。非终端与 JSON 返回一次最终结果；部分失败返回 9，error.details 保留各服务器结果。缓存仅供展示和发现，实际操作读取目标机器最新定义。

项目名称建议约束为 `[A-Za-z0-9][A-Za-z0-9_-]*`，长度 1–64；禁止斜杠、点路径和控制字符。项目拥有独立稳定 UUID，显示名称可以与内部 session 名分离。tmux session 优先使用项目名称，稳定 UUID 存于 tmux 标记。状态查询按 UUID 查找，包括旧版或手工改名的会话；名称差异显示 warning。同名普通会话显示 unmanaged 和 warning，open 保留该会话并创建独立的 tdev-<UUID> 托管会话。多个会话具有同一项目 UUID 时，状态查询返回 warning，接管或停止操作需先确认归属。

### 3.2 tmux 标记

session 设置 tdev 专用 user options，例如 @tdev_project_id、@tdev_project_name、@tdev_schema_version。窗口记录 @tdev_window_id、@tdev_managed、@tdev_persist。以 tmux 查询得到的标记为依据，不从 session 名或当前目录猜测项目。

终端内无 project 参数的 save、sh、stop 从当前 session 推断本机项目，显示确认后执行本机逻辑，不回连服务器。没有标记则要求显式指定项目；明确指定的项目和当前 session 不一致时，save 必须报冲突，不能保存到错误项目。

```sh
tdev save
tdev save -y
yes | tdev save
```

正确管道方向是 yes 在左侧。-y 和 y/yes 代表接受明确展示的确认，不绕过 host key 校验、sudo 授权或 SSH 自身认证。

## 4 用户命令

### 4.1 命令总览

| 命令 | 行为 |
| --- | --- |
| tdev setup | 交互配置连接、密钥认证和重连偏好；检查或安装远端 tdev |
| tdev ls [--server NAME] [--json] | 遍历全部已配置服务器的项目和会话状态，可指定单台服务器 |
| tdev create [server/project] | 创建项目，配置目录、启动窗口和映射 |
| tdev show <project> | 显示定义、客户端 override 和生效配置 |
| tdev edit <project> | 复用创建向导，预填现有值并检查 revision 保存 |
| tdev open <name> [--mount] | 打开项目或恢复 scratch；项目恢复保存转发，--mount 用于项目挂载 |
| tdev save [project] [-y] | 从运行会话保存窗口结构、目录和已知启动定义 |
| tdev sh [project] [--name NAME] [--save] [-y] | 新建项目 shell 窗口，可同时保存定义 |
| tdev forward [project] [-L SPEC] [-R SPEC] | 启动保存的映射或本次临时映射 |
| tdev stop [project] [-y] | 停止远端项目 session，保留项目配置 |
| tdev push <project> <local_path> <remote_path> | 上传文件或目录 |
| tdev pull <project> <remote_path> <local_path> | 下载文件或目录 |
| tdev forward add/remove <project> | 管理保存的映射定义 |
| tdev mount <project> [local_path] | 将项目远端根目录挂载到本地 |
| tdev unmount <project> [<local_path>] | 安全卸载本客户端对应挂载 |
| tdev doctor [project] [--json] | 检查二进制、SSH、tmux、配置和所需功能依赖 |
| tdev version [--json] | 输出程序、协议和配置格式版本 |
| tdev upgrade [--version <version>] [--remote <server>] | 更新本机或指定远端 |

doctor、version、unmount、upgrade 是实现默认补充接口。第一版不添加复杂全屏项目管理界面；setup 的交互可用逐项问答。

### 4.2 setup

流程：自动读取已登记连接与 SSH Host/Include → 选择或搜索服务器，或填写 `user@host:port` → 验证 SSH → 检测已有远端 tdev 或确认安装 → 读取目标设备已有项目 → 按需调整本机端口 → 保存连接。

重复设置已登记服务器时，保留现有远端配置目录、私有 SSH profile、重连偏好和本机 override。连接、依赖和配置读取成功后保存；失败时可重试，Ctrl-C 退出。SSH 配置发现只读取显示所需字段，`Match exec` 交给真正发起连接时的 OpenSSH 处理。`--json` 或无终端时，使用显式 `--host`/`--ssh-alias` 参数。

- 优先复用已有 SSH config 和认证方式。只添加 tdev 管理的配置块或独立 Include 文件，不重写整个 SSH config。
- 复制公钥必须有一次可用的引导认证；交给系统 SSH 处理密码、MFA、host key 提示，不捕获密码。
- 新建密钥、写入 authorized_keys 等变更必须在 setup 交互中明确列出并确认；重复运行不添加重复公钥。
- 不将私钥复制到服务器，不将秘密保存到项目 YAML。
- 远端无需公网访问：本机可下载目标 OS/架构产物，再经 SSH 上传、校验并原子安装。
- 缺少 tmux 时报告并给出安装指导；第一版不自动执行远端 sudo。
- mount 的依赖按需检测，不阻止其他功能完成设置。

### 4.3 open

1. 解析服务器与项目，检查远端协议兼容性，实时获取项目定义并合并本机 override。
2. 调用 session ensure：存在则返回实际会话；不存在则按配置创建。
3. 在本地建立保存的转发；冲突只影响对应入口，明确显示失败。
4. 指定 --mount 时挂载项目目录，复用本机 override 中的路径；未配置时询问。独立 mount/unmount 保留，open 退出后挂载保留。
5. 恢复终端状态，本机直接连接 tmux，远端交给系统 ssh -t。
6. 正常 detach 则结束该次 open；异常断线进入重连状态机。

交互终端中，open 遇到尚未配置的项目时询问是否创建，填写项目所在机器的已有目录、按需配置窗口与映射后继续打开，新项目默认窗口 main。拒绝保留 PROJECT_NOT_FOUND，Ctrl-C 取消；连接或配置错误按原错误返回。也可从客户端运行 `tdev create`。setup 发现零项目时提供立即创建入口。`ls/show/edit` 和 `forward add/remove` 管理项目定义，复用原子写入、revision 和备份机制。本机端口偏好继续使用 override。

ensure 必须在并发客户端调用下幂等：不重复创建窗口，不重复执行启动命令。发现未完成初始化的 session 时返回可诊断状态，不盲目重新运行全部命令。可使用项目操作锁和初始化标记，成功启动每个窗口后记录完成状态；崩溃恢复采取显式修复，不追求未经确认的自动重放。

已经存在但服务命令退出的窗口保留输出与退出状态；再次 open 不自动重启该服务。只对 tdev 管理的窗口设置 remain-on-exit 等必要选项，避免改变用户所有 tmux 会话。

### 4.4 save

保存窗口顺序、稳定窗口 ID、名称、目录以及已经声明的启动命令。窗口当前目录来自 tmux；命令来自受管理的启动配置，不使用 ps 或 shell history 猜测完整命令。

- 已保存的服务窗口：保留声明命令，允许采集名称、顺序、目录变化。
- 普通新增 shell 窗口：保存成 shell 定义，不把当前运行程序误认为启动命令。
- sh 创建且未带 --save 的窗口标记为临时，通用 save 默认排除；sh --save 创建持久窗口。
- 用户自行创建的、无临时标记的单 pane 窗口可纳入保存，并展示变更摘要。
- 将手工运行的程序变成启动命令，通过 edit 向导显式填写启动命令。
- 保留原有保存转发；不扫描客户端临时转发并自动收录。
- 先生成变更并校验，再写入；冲突时不覆盖并发修改。

save 不保存终端历史全文、环境秘密、程序内存、网络连接状态，也不承诺恢复上次的 AI 对话。

### 4.5 sh

创建一个新的 shell 窗口，工作目录默认项目根目录，不重复执行项目服务启动命令。远端 tmux 内执行时切换到新窗口；客户端执行时创建后连接并选择新窗口。session 不存在时返回错误，提示先 open。临时 shell 窗口关闭 remain-on-exit，退出 shell 即删除运行窗口；持久 shell 退出后保留启动定义。

--save 将新窗口定义写入远端配置。需要同时考虑“窗口创建成功、配置保存失败”的部分成功：保留窗口并明确报告未持久化，不误报完整成功。

### 4.6 stop

停止指定项目 session 及其 tmux 管理的前台程序，保留配置。不保证清理程序自行 daemonize 后脱离 session 的进程；配置中的服务应以前台方式运行。显式 stop 显示项目和影响范围并确认，-y 跳过；推断项目时合并为一次确认。

同一项目的其他客户端会被断开，重连检测到 session 已停止后结束。stop 只关闭当前项目的 session，保留其他项目。

## 5 转发语义与生命周期

### 5.1 参数语法

遵循 OpenSSH：-L 在客户端监听，-R 在远端监听。两者均采用“监听端口:目标主机:目标端口”。

```sh
# 本地 3000 → 远端 localhost:8000
tdev forward demo -L 3000:localhost:8000
tdev forward demo -L 3000:8000

# 远端 9000 → 本地 localhost:3000
tdev forward demo -R 9000:localhost:3000
tdev forward demo -R 9000:3000

# 同端口简写
tdev forward demo -L 8000
tdev forward demo -R 9000

# 连续保存定义；两种方向均支持
tdev forward add demo -L 8000 -L 5173 -R 9000:3000
tdev forward demo
```

支持标准 TCP 形式 `[bind_address:]port:host:hostport`，IPv6 地址使用方括号。第一版不支持 -D、Unix socket forwarding 和远端动态分配监听端口 0；明确拒绝，不错误解析。端口范围 1–65535。默认监听回环地址，不自动开放到公网或修改服务端 GatewayPorts。

项目参数始终解释为项目名称，纯数字名称同样适用。临时映射通过 -L/-R 显式传入；省略方向参数时使用项目保存的映射及当前客户端 override。

当前项目自动匹配需要 tmux 标记。forward 的“本地”永远指运行 forward 的机器：在远端 tmux 中运行不会反向控制原来的笔记本。若当前机器没有连接目标信息，应要求显式 server/project，不能根据 SSH_CLIENT 猜测或尝试反连笔记本。

### 5.2 保存与客户端覆盖

`forward add/remove` 管理项目所在机器的共享映射，保存前验证完整定义并检查 revision。`override --forward ID` 按稳定 ID 修改当前客户端的监听参数。create/edit 向导也可配置映射。

`forward <项目>` 只负责建立和维持监听；显式 -L/-R 为临时映射。定义保存与监听运行分别操作。反向映射将流量送到成功建立监听的客户端，多客户端可能争用同一个远端端口。

### 5.3 第一版连接所有权

实现默认决策：显式 forward 在前台运行，Ctrl-C 终止本次转发。open 创建的转发由本次 open 进程管理，正常退出或取消重连时清理自己创建的连接。保存定义继续保留。暂不引入全局后台转发 daemon。

重复建立同一监听的连接不得杀死现有进程。如果无法证明监听属于本次操作，报告占用；对外部 SSH 或其他客户端的隧道不做接管。第一版可以将每条映射分为独立 SSH 进程，以准确隔离与报告单项失败，后续再优化连接复用。

SSH 进程设置合理的 keepalive、ConnectTimeout 和 ExitOnForwardFailure。后者只确认转发监听建立，不表示目标应用健康。保存 -R 不改变 sshd 允许的绑定范围。

## 6 重连行为

### 6.1 配置

客户端全局设置默认值，允许每个服务器覆盖，也允许本次命令覆盖。不要把某台笔记本的重连偏好写进项目定义。

```yaml
reconnect:
  terminal:
    attempts: 5
    interval: 2s
  forward:
    attempts: 5
    interval: 2s
  http:
    attempts: always
    interval: 5s
ssh:
  connect_timeout: 10s
  server_alive_interval: 15s
  server_alive_count_max: 3
```

数值是实现默认决策。attempts 接受非负整数或字符串 always；interval 必须为正 duration。forward 是为显式前台 TCP 转发补充的独立策略；HTTP 是后续专用策略。CLI 可使用 --reconnect-attempts 与 --reconnect-interval 覆盖当前命令策略。

### 6.2 状态转换

```text
connecting → attached → 正常 detach → exit
                 │
                 └─ 异常断线 → waiting → reconnecting → attached
                                  │           │
                                  └─ Ctrl-C ──┴─→ cancelled → exit
```

- 0：连接失败/断线后不自动重试。
- N：初始连接之外最多追加 N 次尝试；每次失联事件独立计数。
- always：持续重试可恢复的连接故障，直到成功或用户取消。
- interval：每次失败结束到下次尝试开始的固定等待时间；第一版不隐式增加指数退避。
- 重连成功清零计数；这里的成功指远端会话确认可连接并进入 attach，不只是 TCP 握手成功。
- 正常 detach、用户停止项目、session 明确不存在：结束，不自动创建。
- 网络不可达时 session 为 unknown，不能视为 missing。
- 等待和发起重连期间 Ctrl-C 必须立即取消计时器、探测与连接尝试，清理本次拥有的子进程，恢复终端并以 130 退出。
- 已连接交互期间 Ctrl-C 继续交给远端应用。不要用全局处理器将远端程序的 Ctrl-C 变成退出 tdev。

示例提示：`Connection lost. Retrying in 2s… [2/5]. Press Ctrl-C to exit.`

### 6.3 失败分类与检测限制

认证失败、主机密钥变化、无效配置、协议不兼容、明确的项目停止不自动重试。DNS 临时失败、连接中断和超时可以重试。

OpenSSH 的退出码 255 不能独立区分所有原因。实现不能把所有非零退出一概当作断网，也不能仅根据一次 has-session 调用失败宣布 session 已结束。应结合独立结构化探测、连接阶段和有限的 SSH 诊断信息分类；无法确认原因时展示原始诊断，保留 unknown，避免自动修改 host key 或强制重建 session。分类逻辑须有集成测试，诊断文本解析是兼容性风险，不能作为唯一真相。

重连阶段优先非交互认证，避免后台重试不断弹密码/MFA；发现需要交互认证时停止，提示用户重新 open。首次 open 的正常认证交互仍由 OpenSSH 处理。成功连接后立即反复断开的情况需要避免快速空转，始终保留配置的重试间隔；不能只因探测成功就清零失败次数。

keepalive 决定多久发现连接失效，interval 只决定失效后多久再试。两者不能混为同一参数。open 所属转发可在该次 open 存续期间使用 forward 策略独立恢复，但 open 退出时统一取消；后台日志不得打断交互终端。

### 6.4 HTTP 后续独立运行

HTTP 代理及其隧道由本地独立管理进程持有，使用 http 重连策略。终端 detach 或取消终端重连后，HTTP 入口继续运行。隧道中断时返回暂不可用响应；应用 5xx 原样转发，重连仅由 SSH 连接故障触发。

## 7 配置与文件结构

### 7.1 远端项目配置

默认目录 `${XDG_CONFIG_HOME:-$HOME/.config}/tdev/projects/<project>.yaml`。配置格式带 schema_version；项目带稳定 id 和单调递增 revision。

```yaml
schema_version: 1
revision: 1
id: "c42ced90-50c4-4cf9-bcff-f1658484d177"
name: demo
directory: /home/developer/projects/demo

windows:
  - id: api
    name: api
    directory: .
    command: "uv run python -m app"
  - id: web
    name: web
    directory: web
    command: "pnpm dev"
  - id: shell
    name: shell
    directory: .

forwards:
  - id: web
    direction: L
    bind_address: "127.0.0.1"
    listen_port: 5173
    target_host: localhost
    target_port: 5173
  - id: local-service
    direction: R
    bind_address: "127.0.0.1"
    listen_port: 9000
    target_host: localhost
    target_port: 3000
```

窗口 directory 为绝对路径或相对项目根目录的路径；项目根目录创建配置时规范化为远端绝对路径。缺少 command 表示交互 shell。command 由远端 shell 执行，项目配置应来自可信用户。

第一版禁止重复 YAML key、未知字段和不支持的 schema_version，避免拼写错误被忽略；后续字段必须通过格式版本和能力协商引入。不保存完整环境变量快照，不自动读取 .env 写入配置。配置权限默认用户私有。

### 7.2 本地配置

```yaml
schema_version: 1
servers:
  research:
    ssh_alias: research-host
    remote_binary: .local/bin/tdev
  development:
    ssh_alias: dev-host
    remote_binary: .local/bin/tdev
reconnect:
  terminal:
    attempts: 5
    interval: 2s
  forward:
    attempts: 5
    interval: 2s
  http:
    attempts: always
    interval: 5s
```

本地默认使用 XDG 风格目录。runtime/state 保存本机挂载和操作所有权等临时信息。进程归属通过 PID、启动标识与进程身份共同核验。

本机端口偏好单独写入 `overrides.yaml`，例如：

```yaml
schema_version: 1
servers:
  development:
    demo:
      project_id: 22222222-2222-4222-8222-222222222222
      forwards:
        api:
          listen_port: 8080
```

向导自动记录项目 UUID 和稳定 forward ID。`ls`、`open` 每次从服务器读取最新项目，再覆盖本机监听端口；目录、窗口、命令和转发目标继续使用本次发现的值。远端移除映射或同名项目身份变化时，提示重置过期 override。保存窗口与项目使用远端原始定义。

### 7.3 写入与并发

- api 修改接口取得项目锁，读取并校验 expected_revision，再生成新配置。
- 写同目录临时文件、校验、刷新后原子 rename；revision 增加。
- revision 不匹配返回 CONFIG_CONFLICT 和当前 revision，不默认 last-write-wins。
- 修改前保留受限权限的上一版本备份；配置迁移也需要备份和可解释的回退。
- 不在持有短时配置锁期间等待交互输入或长期运行 SSH。
- 多个字段的更新作为一个请求处理；错误不得产生半份配置。
- save 应再次核对 session/window ID；布局在采集期间发生变化时可重试有限次数或报告冲突。

## 8 机器 API 与 shell 批次

```sh
tdev --json version
tdev --json api version --require api-v1
tdev --json api project list --status
tdev --json api project create demo --directory /home/developer/projects/demo
tdev --json api project create demo --stdin < draft.json
tdev --json api project get demo
tdev --json api project set demo --stdin --expected-revision 3 < definition.json
tdev --json api session ensure demo
tdev --json api session status demo
tdev --json api session save demo --expected-revision 3
tdev --json api session stop demo
tdev --json api session add-window demo --name debug --temporary
tdev --json api session context
```

完整项目草稿收集并验证后，通过 create --stdin 一次写入。project set 更新已保存定义，修改检查 revision。session add-window 修改运行中的窗口，sh --save 组合运行和定义变更并报告部分成功。新 shell 默认叫 shell，支持 --name。

客户端用一次 SSH shell 调用顺序执行顶层 version、api version 能力检查与所需操作。每段输出以请求专属的随机标记和退出码分隔，客户端严格解析；诊断走 stderr，stdin 保留给实际写操作。API gate 失败即停止后续操作并显示服务端版本与升级指引。独立查询合并进同一批次，需要用户确认或依赖前一步结果的流程分阶段执行。API 保持普通子命令，不增加请求封装，不依赖 jq。旧 local 入口移除，旧服务端需要升级。

结构化输出示例：

```json
{"protocol_version":1,"ok":true,"data":{"project":"demo","revision":4,"session_id":"$2","status":"running"}}
```

```json
{"protocol_version":1,"ok":false,"error":{"code":"CONFIG_CONFLICT","message":"Project configuration changed","details":{"expected_revision":3,"actual_revision":4}}}
```

--json 时 stdout 只输出一份完整 JSON，诊断放 stderr，禁止颜色和进度提示。错误也保持结构化。具体 SSH 远端执行使用安全参数编码或固定命令加 JSON stdin，不把路径、项目名直接插入 shell 字符串。

建议退出码：0 成功；1 普通运行错误；2 参数/配置无效；3 项目不存在；4 配置冲突；5 会话不存在；6 连接或认证失败；7 功能依赖缺失；8 协议不兼容；9 部分成功；130 用户取消。机器消费者以 error.code 判断具体原因，外层不要误把远端命令失败当作网络失败。

版本区分程序版本、protocol_version 与 schema_version；两端程序补丁版本不同不应直接拒绝。握手返回能力列表，客户端依据协议与所需能力判断兼容性。

## 9 终端交互设计

使用系统 OpenSSH 交互连接，stdin/stdout/stderr 直接继承当前终端文件描述符，不经 Scanner、行缓冲或 UI 重绘层。不需要自己实现 SSH 协议或终端模拟器。

setup 的交互结束后恢复本地终端模式，再连接。交互期间不向终端打印后台状态，日志写文件或在 SSH 退出后展示。SSH 负责本地终端模式、远端 PTY 与窗口大小变化，tdev 负责正确的前台进程与信号生命周期。

能力目标是与手工 `ssh -t … tmux attach` 等价：OSC 52 剪贴板、CSI 鼠标上报、tmux 鼠标滚动、Vim 等全屏程序、中文输入输出、bracketed paste、窗口缩放、Ctrl-C/Ctrl-Z、detach 均不因封装退化。

这些能力仍受终端、tmux 版本与配置限制。不能宣称任意终端支持所有序列；不全局硬编码 TERM，不全局开启所有控制序列透传。可以提供 doctor 和可选 tmux 配置建议，但不要覆盖用户 tmux.conf。若本地还嵌套在 tmux 中，额外的转义与剪贴板配置限制应明确提示。

交互 open 要求可用终端。yes 管道适用于 save/stop 等确认，不将其内容作为交互 SSH 的键盘输入。控制面 JSON 调用不得分配 PTY，以免混入终端控制字符或换行转换。

## 10 文件传输与挂载

push/pull 先采用系统 OpenSSH 配套的 scp/SFTP 能力，不额外要求 rsync。明确区分文件和目录，禁止隐式删除目标多余文件。实现默认决策：remote_path 相对路径以项目根目录为基准，绝对路径按用户输入处理；local_path 相对本地 cwd。禁止通过 shell eval 展开用户路径。

单文件复制到已有目录时，先将源文件名追加到目录路径，得到精确文件目标；显式目标文件名支持重命名。目录上传表示将源目录内容复制到指定目标目录；不得随着目标是否已存在而产生不同层级。底层工具不天然符合该语义时需封装或显式报错。已有目标文件覆盖需确认，提供 -y；中断/部分上传必须非零退出并指出可能残留，不能承诺整个目录传输原子性。

mount 使用项目远端根目录作为来源。挂载前检查 SSHFS 依赖、挂载记录与目录内容；正确来源可复用，其他来源或非空未挂载目录拒绝覆盖。记录实际挂载来源，unmount 有多个候选时要求指定 local_path。busy 时报告占用，不默认强制卸载。

macOS SSHFS 使用 noappledouble/noapplexattr 等选项，防止项目产生 AppleDouble、.DS_Store 和 Apple 扩展属性附带文件，并核验后端的实际防护效果。断连后的 I/O 错误应如实反馈，写入结果需独立确认。

本项目开发部署仍受工作入口 AGENTS.md 约束：业务源码通过 SSHFS 编辑，测试构建在远端执行；提供 push/pull 功能不意味着开发代理可绕过该仓库的编辑规定。

## 11 后续 HTTP 域名映射

目标：`http://demo.localhost:<固定代理端口>`，可扩展 `api.demo.localhost`。固定代理端口由客户端偏好设置；项目保存域名到远端服务端口的声明，不保存客户端临时隧道端口。

```yaml
# 后续配置版本才启用，第一版不得静默忽略
http:
  - name: web
    hostname: demo.localhost
    target_host: localhost
    target_port: 5173
```

本地代理监听回环地址，根据 Host 路由到动态分配的本地 SSH 隧道端口，再访问远端服务。需处理 WebSocket、SSE/流式响应、请求取消、Host 校验和同名冲突。不同服务器存在同名项目时，提示冲突或明确使用带服务器名的域名，不能无提示覆盖路由。

第一阶段不要求监听 80/443，不自动修改证书信任或 hosts，不默认改写响应正文。应用可能需要配置 allowed hosts、Origin、外部 base URL 和 OAuth callback，代理不能承诺自动解决所有应用兼容问题。

HTTP 阶段需要本地长驻代理进程，但不会改变“远端无常驻 tdev 服务”的原则。代理由同一二进制子命令承载，具备显式启停与路由清理能力；具体命令在该阶段单独设计。

## 12 技术选型与取舍

### 12.1 Go

选择 Go 主要考虑配置、进程编排、连接管理和网络代理的实现与维护成本。纯 Go 依赖便于单二进制分发和跨平台构建，HTTP 与并发设施适合后续扩展。Shell 用于薄安装器，业务状态机由 Go 实现。

### 12.2 建议实现组成

- 标准库优先：os/exec、encoding/json、net/http、context、日志与文件 API。
- CLI 参数与 YAML 使用维护状态良好的纯 Go 库，具体版本在建仓时核验并固定。
- CGO_ENABLED=0 为默认构建目标，避免无意引入系统 C 库依赖；实际产物验证不能只依赖构建参数。
- 通过 argv 启动本地进程；只有用户声明的远端 command 才交给 shell 解释。
- 命令启动使用远端用户 shell 的明确约定，首次实现建议用非交互 login shell 执行命令；交互 shell 窗口使用正常交互模式。需要文档说明 rc 文件差异，不随意 source 用户全部配置。
- 配置锁、信号、PTY 与进程处理按平台拆分，避免假定所有 Unix 行为完全一致。

建议目录：cmd/tdev、internal/cli、internal/config、internal/protocol、internal/ssh、internal/tmux、internal/reconnect、internal/forward、internal/transfer、internal/mount、internal/update。不要为了抽象创建庞大的通用工作流框架。

### 12.3 为什么不直接依赖现成 tmux 管理器

smug/tmuxp 的项目启动和布局能力值得借鉴，但 tdev 还需要统一配置修改接口、远端项目归属、SSH 转发和版本协议。引入它们会增加配置转换与版本依赖。第一版控制在窗口级别，直接调用 tmux 的范围可控。

## 13 安装升级与迁移

- CI 为 darwin/linux 的 amd64/arm64 发布压缩包、校验清单与可验证签名，版本号采用 SemVer。
- 默认安装 ~/.local/bin/tdev，不默认 sudo，不要求用户安装 Go、Node、Python、jq/yq。
- 安装脚本检测平台、下载指定版本、验证后原子安装；明确显示 PATH 调整建议，修改 shell 配置需明确授权。
- SHA-256 只能验证文件一致性；真实性验证需要受信任的签名/发布渠道，不能把从同一未受信任来源下载的校验文件当作独立信任保证。
- tdev upgrade 使用与安装一致的校验规则，保留上一产物供回退；包管理器安装版本提示使用对应包管理器升级，避免混用所有权。
- 远端升级上传到用户临时路径，校验后原子替换；不重启 tmux，不退出运行中的项目，不修改 authorized_keys。
- 配置迁移与二进制升级分开处理：旧版无法读取新格式时明确报告；不可逆迁移必须显式确认并备份。
- 安装新 tdev 不覆盖现有 devc-remote。兼容入口可以后续显式安装，不在首轮静默替换用户脚本。
- 已有任意 tmux session 不自动接管；只有明确注册或通过 tdev 创建的 session 才写入项目标记。

GoReleaser 可负责产物组织和发布；安装器、签名验证、远端部署与回退的最终行为仍需项目自己的测试。

## 14 测试与验收

测试围绕行为和故障，不为简单封装机械地逐行写镜像测试。

| 场景 | 验收要求 |
| --- | --- |
| 干净机器安装 | 无 Go 环境可安装；无 tmux 的客户端可连接合格远端 |
| 首次 open | 创建声明窗口、正确目录和命令，建立转发 |
| 重复/并发 open | 不重复执行服务启动命令、不重复创建窗口 |
| 正常 detach | 项目继续运行，本次客户端正常退出 |
| 临时窗口 | sh 不启动其他服务；退出 shell 关闭该窗口；未保存临时窗口不进入 save |
| save | 保留已声明命令与转发，正确保存目录和顺序，不采集秘密或猜测进程命令 |
| 多 pane | 不支持时明确拒绝完整保存，不破坏原配置 |
| 自动匹配 | tmux 标记明确且确认有效；普通 session 不猜测；yes 管道和 -y 可用 |
| 并发修改 | revision 冲突可诊断，不覆盖其他客户端更新 |
| -L/-R | 与原生 SSH 参数方向一致；IPv6、空格路径等正确处理 |
| 保存 -R | 有冲突提示；保存后跨客户端恢复不抢占已有远端监听 |
| 端口占用 | 转发失败明确，其他项目/连接不被停止；open 仍能进入终端 |
| 网络断开 | 0/N/always 和间隔行为准确；未知网络状态不当成 session 消失 |
| 重连 Ctrl-C | 等待与连接尝试都及时退出，不残留本次子进程，终端回显恢复 |
| 远端 Ctrl-C | 中断远端程序，不能意外退出 tdev |
| 远端停止/重启 | session 消失后重连退出；下一次显式 open 才重建配置中的工作环境 |
| 终端等价性 | OSC 52、中文、鼠标拖选、滚轮、Vim、粘贴、缩放与手动 SSH 基线一致 |
| 文件传输 | 文件/目录及已有目标语义固定，中断与覆盖可见，不隐式删除 |
| SSHFS | 来源验证、非空目录保护、busy 卸载和断连 I/O 错误正确处理 |
| 升级回退 | 无语言运行时可升级；不终止 session；协议不兼容有明确错误 |

使用真实 tmux 与测试 sshd 做关键集成测试，避免只 mock 命令。终端类能力在实际终端上做人工/可重复测试，至少覆盖用户常用终端和 macOS/Linux 组合；纯 CI 输出比较不能证明剪贴板、鼠标和终端恢复全部正常。

进程取消和重连测试使用可控时钟、测试 SSH 目标或故障注入，不通过反复等待长时间网络超时完成。引号、控制字符、项目名、端口解析、重复请求和文件写入冲突是重点。

## 15 开发顺序与交接要求

1. 建立 Go 项目、CLI 骨架、版本协议、配置 schema、原子保存与 revision。
2. 实现 api 项目配置与 tmux session 操作，验证并发 ensure 和 save 规则。
3. 实现 setup、ls、open、sh、save、stop 与原生终端接管。
4. 实现重连状态机、退出分类与 Ctrl-C 行为，先验证终端恢复。
5. 实现 -L/-R 转发、保存及多客户端冲突处理。
6. 实现 push/pull、mount/unmount、doctor。
7. 完成跨平台发布、安装、升级和回退，验证无语言运行时安装。
8. 第一版稳定后独立实现 HTTP 域名入口与其后台连接管理。

开发前读取 AGENTS.md 和 README，核验工作树并保护已有修改。实测引起的设计调整应在 PR 中说明原因、行为变化和验证结果，待验证项统一记录在验收文档中。

## 16 参考资料

- [smug](https://github.com/ivaaaan/smug)：轻量项目与窗口编排参考。
- [tmuxp freeze](https://tmuxp.git-pull.com/cli/freeze/)：运行会话导出配置参考。
- [tmuxinator](https://github.com/tmuxinator/tmuxinator)：项目启动定义与生命周期参考。
- [Tmux Workbench](https://github.com/LeON-Nie-code/tmux-workbench)：远端工作区发现和恢复参考；其本地索引归属与本设计不同。
- [Tailmux 转发](https://www.tailmux.dev/docs/forwarding/)：转发状态与重连体验参考。
- [Portless](https://github.com/vercel-labs/portless)：后续 HTTP 域名入口参考。
- [OpenSSH ssh](https://man.openbsd.org/ssh)、[ssh_config](https://man.openbsd.org/ssh_config)：PTY、认证、转发、超时和 keepalive 语义。
- [tmux 剪贴板](https://github.com/tmux/tmux/wiki/Clipboard)、[Xterm 控制序列](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html)：OSC 52、鼠标与终端能力边界。
- [Go os/exec](https://pkg.go.dev/os/exec)、[Go Modules](https://go.dev/ref/mod)：进程调用与依赖管理。
- [GoReleaser](https://goreleaser.com/getting-started/intro/)：跨平台发布与校验产物。

参考资料来自设计阶段文档调研。开发时应固定依赖版本并核验当前官方接口。

## 独立临时会话

`tdev scratch [server]` 创建并进入独立 tmux session。`local` 为本机，其他目标取自客户端登记的服务器。省略目标且仅有本机可用时直接使用本机，否则显示服务器选择器。`--name` 设置逻辑名称，默认生成唯一名称；`--dir` 设置目标机器上的绝对路径或 home 相对路径，默认 home。重名创建返回 SESSION_CONFLICT。JSON 调用创建会话并返回身份，不附着终端。恢复统一使用 `tdev open <name>` 或 `tdev open <server>/<name>`。open 按本机、缓存索引、实时远端查询定位目标，选择目标后重新验证实际会话。项目与 scratch 在同一服务器同名时返回 SESSION_CONFLICT；缓存中已结束的 scratch 返回 SESSION_NOT_FOUND；完全未知的名称沿用项目创建向导。scratch 的 open 直接附着，`--mount` 仅适用于项目。

临时会话使用独立 tmux 归属标记与 `tdev-scratch-` 名称前缀，独立于项目定义、启动配置、转发及 save。detach 和传输断线保留运行进程，最后一个窗口退出时结束。重连只进入匹配原 session ID 与 generation 的会话。

`ls` 默认展示项目和 scratch，支持 `--type project|scratch` 与现有 `--server` 筛选。scratch 从实际 tmux 标记发现，跟随列表缓存和离线状态展示；成功刷新后移除已结束的会话。JSON 保留 projects 数组，并增加 scratches 数组。机器接口 `api scratch create|list|status|attach|attachment` 使用 `scratch-v1` 能力门禁，旧服务器保留项目列表并给出 scratch 升级提示。
