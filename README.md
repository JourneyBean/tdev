# tdev

通过 SSH 使用、以项目组织和恢复 tmux 工作环境的轻量工具。运行 `tdev setup`
选择已有连接或填写服务器地址，读取目标设备的项目配置，随后打开项目继续开发。
复用系统 OpenSSH 和 tmux，远端无需数据库、HTTP API 或常驻 tdev 服务。

当前源码版本为 `0.3.0`，已实现连接向导、项目会话、本机端口覆盖、独立转发与重连、文件传输、SSHFS
挂载、签名安装升级和回滚。设计基线见[产品与技术设计](docs/design.md)，实现取舍见
[开发记录](docs/development.md)，实际通过与尚未实测的场景见[第一版验收记录](docs/acceptance.md)。
版本变更见[发布说明](docs/releases/v0.3.0.md)。二进制 Release 位于
[GitHub](https://github.com/JourneyBean/tdev/releases)。

## 安装

```sh
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh
```

自动识别 macOS/Linux 及 amd64/arm64，校验后安装到 `~/.local/bin/tdev`，并配置当前 shell 的 PATH。
安装完成后询问是否配置 bash/zsh 命令补全，支持 `curl | sh`；使用 `--completion all` 可直接配置两种 shell，`--no-completion` 跳过。
打开新终端，或执行安装器输出的 PATH 命令即可使用。支持 `--no-modify-path`，也可以单独运行 `tdev path --write`。

[四平台二进制下载、手动安装和签名校验](docs/installation.md)

## Shell 命令补全

一键配置启动文件，打开新终端生效：

```sh
tdev completion install                     # 根据 SHELL 选择
tdev completion install --shell all         # 同时配置 bash 和 zsh
```

重复执行只更新管理区块，保留原配置、权限和符号链接，并在修改前备份。补全与 PATH 使用独立区块。

使用新版 `tdev`，在当前终端加载补全：

```sh
# zsh：放在已有 compinit 初始化之后
autoload -Uz compinit && compinit
source <(tdev completion zsh)

# bash
source <(tdev completion bash)
```

将对应的 `source` 行加入 `~/.zshrc` 或 Bash 的交互启动文件即可持久启用。
支持子命令、选项、`--server` / `--remote` 服务器名、SSH 别名、转发 ID。
`tdev open <Tab>` 查询本机和已配置服务器的项目，`tdev open dev/<Tab>` 查询指定服务器；
`tdev create <Tab>` 补全服务器前缀，供继续填写新项目名。远端候选每次只读查询，
使用非交互 SSH 和 1.5 秒等待上限，连接不可用时返回空候选。脚本生成过程只读取命令结构。

## 开始使用

项目是默认操作对象。`local` 是内置服务器，无需 setup；远端通过系统 SSH 连接。

```sh
tdev create demo --directory /home/developer/projects/demo
tdev open local/demo
tdev setup                       # 添加远端服务器
tdev ls                          # 本机与全部远端
tdev ls --server dev              # 指定服务器
tdev create dev/demo              # 交互配置远端项目
tdev open dev/demo
```

完整标识为 `server/project`。裸名称先查本机，再按服务器名称排序查找远端；自动匹配时显示完整目标。缓存命中的目标离线时报告错误。裸名称 create 默认在本机创建；不带项目名的 create 向导先选择位置，本机为默认项。

`ls` 默认以 `SERVER、NAME、TYPE、STATUS、DIR、FORWARDS` 六列表格展示，中文按终端显示宽度对齐。端口映射显示在 `FORWARDS` 列，多条映射向下排列；列内以 `监听地址:端口 -> 目标地址:端口` 表示本地转发（`-L`），反向转发（`-R`）使用 `<-`，省略映射 ID。标准输出为终端时，先展示本机与远端缓存，表格下方显示服务器 `syncing`；随后并发查询每台服务器，更新条目并移除已完成的刷新提示。项目 warning 合并在状态列中，如 `running,warning`，列表末尾统一提示 `tdev doctor`；该命令展示完整诊断，也可用 `tdev doctor server/project` 单独检查项目。连接失败保留带时间的缓存并标记 `unreachable`，协议不兼容标记 `upgrade-required`，服务器应用错误标记 `error`。管道输出只展示最终列表；`--json` 返回一次结构化结果，包含位置、状态、更新时间和项目。部分失败时退出码为 9，结果保留在错误的 details 中。

缓存位于系统临时目录的用户私有目录，供列表展示和项目发现。实际操作始终读取目标机器的最新项目定义，再应用当前客户端 override。项目配置保存在项目所在机器。

## 创建、查看和编辑

```sh
tdev create                      # 选择位置，再配置项目
tdev create --server dev
tdev create dev/demo --directory /home/developer/projects/demo
tdev show dev/demo
tdev edit dev/demo
tdev edit dev/demo --directory /home/developer/projects/new-demo
tdev forward add dev/demo -L 3000:localhost:3000 -L 8000:localhost:8000
tdev forward remove dev/demo --id FORWARD_ID
tdev override dev/demo --forward FORWARD_ID --local-port 18000
```

create 和 edit 复用逐项向导，edit 预填已有值：项目目录、启动窗口、端口映射及保存确认。目录应已存在，支持绝对路径、`~/路径` 与 Tab 补全。初始窗口默认为 `main`。输入支持方向键、Home/End 和删除，兼容 UTF-8、空格与引号。

添加映射时选择 L/R，直接输入 `localhost:8000`、`service.internal:3000` 或 `[::1]:8000`。监听端口默认取服务端口。远端 L 映射的默认监听端口保存在项目定义，当前客户端的端口调整单独保存为 override。可连续添加或跳过。保存配置后，open 或 forward 建立实际监听。

配置修改检查 revision 并保留备份。完整替换使用 `edit --stdin --expected-revision N`，输入为 show 的 `definition` 对象。show 同时展示原始定义、override 与最终生效配置。窗口启动命令在新建会话时执行。

## 会话和窗口

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

tmux session 优先使用项目名称，如 `demo`，归属按项目 UUID 标记识别。旧版或手工改名的托管 session 可直接复用，名称差异显示 warning。同名普通会话保留，`ls` 显示 `unmanaged` 和 warning，`open` 创建独立的 `tdev-<UUID>` 托管会话。warning 保持列表刷新成功；多个会话使用同一项目 UUID 时，接管或停止操作需先确认归属。

open 遇到项目不存在时，确认后进入创建向导，保存后继续打开。新项目默认窗口为 `main`，已有项目沿用保存的名称。sh 的新窗口默认叫 `shell`，`--name` 可指定名称。

## 分屏布局

在项目的 tmux 窗口中手动分屏后运行 `tdev save`，即可保存布局。新建会话时恢复分屏方向、嵌套结构、比例和各 pane 的目录。已声明的 pane 启动命令继续保留，手动新增 pane 保存为空 shell。

也可通过 `tdev edit <项目> --stdin --expected-revision N` 设置窗口的 `layout`；完整输入使用 `tdev show` 返回的 `definition`。例如窗口定义：

```yaml
id: web
name: web
directory: .
layout:
  split: vertical
  panes:
    - id: frontend
      directory: frontend
      command: npm run dev
    - id: backend
      directory: backend
      command: uv run server.py
```

`horizontal` 表示左右排列，`vertical` 表示上下排列；子节点可以继续分屏。叶节点的 `id` 在窗口内唯一，目录相对项目根目录解析，`weight` 可指定同级空间比例。单 pane 窗口继续使用原来的 `directory` 和 `command` 字段。

使用分屏配置时，项目所在机器及编辑配置的客户端需支持 `pane-layout-v1`；远端写入会检查该能力。布局按可用单元格近似恢复。已有会话继续运行原布局，新建会话使用保存的配置。

## 诊断日志

失败操作自动写入当前配置根下的 `logs/diagnostics.jsonl`。默认路径为 `~/.config/tdev/logs/diagnostics.jsonl`；`XDG_CONFIG_HOME` 和 `--config-dir` 同时影响配置与日志位置。目录补全失败时，向导显示错误类别和日志路径。命令结束时，人类可读输出显示本次日志位置；`--json` 保持单一协议响应。

```sh
tdev --debug setup
tail -n 30 ~/.config/tdev/logs/diagnostics.jsonl
```

`--debug` 额外记录成功的命令和 SSH/API 请求。每条 JSON 记录包含 UTC 时间、进程号、操作、服务器名称、耗时，以及失败时的错误码、原因类别和可用的进程退出码。日志仅保存已知错误类别，不保存命令参数、目录输入、环境变量、凭据或原始远端输出；未识别的原因记为 `operation_failed`，可结合操作和退出码定位。补全查询的独立时限为 3 秒，超时记为 `deadline_exceeded`。

日志目录权限为 0700，文件为 0600。单文件上限为 1 MiB，保留三个轮转备份。日志写入失败保留原操作结果，人类可读输出提示日志不可用。

## 构建与验证

开发需要 Go 1.27，运行产物无需 Go。会话管理需要 tmux；纯客户端仅需 SSH。

```sh
make build
TDEV_REQUIRE_INTEGRATION=1 make test
make check
make cross
```

构建生成 `bin/tdev`；跨平台二进制位于 `dist/`。这些是未签名开发产物。
默认配置目录为 `${XDG_CONFIG_HOME:-$HOME/.config}/tdev`；`--config-dir` 可指定
本机隔离目录。配置和历史版本备份使用 0600 权限。

## 机器接口

`tdev api` 供客户端在目标机器上执行，使用明确参数和结构化 stdin，无业务确认。`--json` 输出单一协议响应；session attach 要求终端和明确的会话身份。

```sh
./bin/tdev --json version
./bin/tdev --json api version --require api-v1
./bin/tdev --json api project list --status
./bin/tdev --json api project create demo --directory /home/developer/projects/demo
./bin/tdev --json api project get demo
./bin/tdev --json api project set demo --stdin --expected-revision 3 < demo.json
./bin/tdev --json api session ensure demo
./bin/tdev --json api session save demo --expected-revision 3
```

项目定义统一通过 project get/set 读写。其他接口按 session、transfer、binary 和 mount-probe 分组。客户端每次 API 调用将版本查询、能力检查与操作合并为一次 SSH shell 调用，用随机分隔标记和独立退出码解析结果；能力检查通过后才执行操作。setup 的依赖与项目查询共享一次连接。旧服务端提示升级：

```sh
tdev upgrade --remote dev
```

`--ssh-config` 与 `--yes` 属于用户命令；`--config-dir`、`--timeout` 和 `--json` 两层共用。API 参数和配置错误与 SSH 连接错误分别报告。

## 从客户端连接

运行连接向导，自动读取已登记的服务器、SSH `Host` 和 `Include` 文件；可以选择、搜索，或直接填写 `user@host:port`。向导验证连接后列出远端已有项目，保留已有配置，并可调整这些项目在本机使用的端口。远端缺少 tdev 时可确认安装官方签名版本；缺少 tmux 时提示先准备依赖。

```sh
tdev setup
tdev ls
tdev open my-awsome-project
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

`open` 将终端文件描述符直接交给系统 SSH。正常 detach 后退出，不重建会话；
重连检查 session ID 和 generation，原会话消失后结束，不进入替代会话。
`--reconnect-attempts` 支持 `0`、非负整数和 `always`；数值是初次连接之外的重试次数。
等待和连接期间 Ctrl-C 退出；已连接时键盘 Ctrl-C 仍交给远端应用。

本地 `client.yaml` 支持全局 reconnect/SSH 偏好，以及每台服务器的 reconnect 覆盖。
优先级为命令参数、服务器覆盖、全局设置。setup 也提供
`--terminal-reconnect-attempts`、`--terminal-reconnect-interval` 及对应 forward 参数。

```yaml
reconnect:
  terminal: {attempts: 5, interval: 2s}
  forward: {attempts: 5, interval: 2s}
  http: {attempts: always, interval: 5s} # 为后续 HTTP 功能保留
ssh:
  connect_timeout: 10s
  server_alive_interval: 15s
  server_alive_count_max: 3
```

项目标识仍使用设计中的 ASCII slug；目录、窗口显示名、命令和文件名支持 UTF-8/CJK。
不会为了诊断而将用户 locale 改成 C。tmux 查询使用 `-u`，避免 C locale 下将 CJK 或
机器接口分隔符替换成下划线。无效 UTF-8 和路径中的控制字符会明确报错。

## 转发

`ls`、`open` 每次读取远端最新配置，再合并本机 `overrides.yaml`。调整本机端口可在向导中完成，也可以使用：

```sh
tdev override demo                       # 查看远端与合并后的配置
tdev override demo --forward api --local-port 8080
tdev override demo --reset               # 恢复远端默认值
```

`api` 是远端映射的稳定 ID，可从 `tdev show` 查看。项目目录、窗口和命令实时读取，本机只保存端口差异及附加转发。

```sh
tdev forward demo -L 3000:localhost:8000
tdev forward add demo -L 8000:localhost:8000
tdev override demo --forward FORWARD_ID --local-port 8080
tdev forward add demo -R 9000:localhost:3000
tdev forward demo
```

与 OpenSSH 一致，`-L` 本机监听、`-R` 远端监听，均为
`[bind_address:]port:host:hostport`。支持 `port:port`、`port` 及方括号 IPv6。
默认绑定回环地址，不接管已有连接。反向映射将流量送到建立连接的客户端，多客户端可能争用远端监听。

每条映射拥有独立 SSH master 和私有控制 socket；通过 OpenSSH 控制请求确认监听
建立后才报告 ready。端口冲突不接管其他连接，失败和重连互相隔离。
`forward add/remove` 保存或移除共享定义，`override` 调整当前客户端的监听参数。`forward <项目>` 使用保存的映射；显式 -L/-R 只为本次运行建立监听。项目参数始终按项目名解析，包括纯数字名称。

前台 forward 和 open 的隧道归当前进程所有，退出时取消自己的映射和连接。
open 期间的后台诊断写入私有 `runtime/logs/open-*.jsonl`，不会插入交互终端。
ready 表示 SSH 监听已建立；应用健康由应用自身检查。

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
第一版拒绝符号链接和设备文件；Mac 目标会在写入前拒绝大小写或 Unicode 规范化重名。

open 默认只进入终端并建立转发。显式 `--mount` 或独立 mount 才挂载，路径保存为本机 override，可通过 `override --mount-path` 配置。再次 mount 可省略路径。挂载在退出 open 后保留，通过 unmount 显式释放。本机项目的 mount 返回已有项目目录。

挂载检查目录内容、实际来源和文件系统 ID；复用正确来源，不覆盖其他挂载。
外部已有挂载可以复用，但不会被自动接管。unmount 使用本地记录，断网时也可操作；
身份不符或 busy 时拒绝，不强制卸载，也不按单独 PID 杀进程。

SSHFS 以独立前台进程运行，避免当前 macFUSE 的后台 fork 问题；mount 命令退出后
文件系统继续运行，诊断位于私有日志。Mac 使用 noappledouble/noapplexattr 和 iconv
文件名转换，并通过远端独立检查验证 CJK 文件名及元数据防护；验证需要项目根目录可写。
Mac 挂载按 NFC 远端文件名工作，混合 NFC/NFD 名称仍受 SSHFS/macFUSE 限制，不会自动
重命名远端文件。详见[验收边界](docs/acceptance.md#尚未完成的环境或人工验收)。

## 安装、升级和回滚

GitHub 自动发布见 [GitHub Actions 发布配置](docs/github-release.md)，支持版本标签触发和补发已有标签。

GitLab 自动发布见 [GitLab CI 发布配置](docs/gitlab-release.md)。配置签名文件变量和受保护的 `v*` tag 后，推送版本 tag 即可运行测试、构建四平台签名包并创建 GitLab Release。GitLab 安装脚本会保存当前项目作为后续升级来源。

构建机器可以用既有外部 PKCS8 Ed25519 密钥生成四平台二进制、压缩包、签名清单和校验表：

```sh
TDEV_SIGNING_KEY=/secure/external/release-key.pem make release VERSION=0.0.1
```

首次安装应从独立可信渠道确认公钥，再通过 `--public-key` 指定。
离线安装示例：

```sh
sh scripts/install.sh --release-dir ./verified-release --public-key /trusted/release.pem
tdev upgrade --release-dir ./verified-next-release
tdev upgrade --rollback

# 从客户端安装远端，无需远端访问发布网站
tdev setup --name research --ssh-alias my-existing-alias --install \
  --release-dir ./verified-release --public-key /trusted/release.pem
tdev upgrade --remote research --release-dir ./verified-next-release
tdev upgrade --remote research --rollback
```

也可使用指向特定版本目录的 `--release-url https://…`，以及 `--version` 校验期望版本。
HTTPS 源地址和公钥会保存在安装 receipt 中，后续升级可复用。离线源需再次显式指定目录。
bootstrap 脚本需要 OpenSSL 3+（可通过 `OPENSSL` 指定路径），在线源另需 curl；运行二进制
无需 Go/Node/Python。远端 bootstrap 校验使用 sha256sum 或 shasum，安装及传输需可批模式
认证的 SSH 身份。缺少 tmux 时给出错误，不自动 sudo 安装。

升级先验证签名、SHA-256、候选版本与协议，再备份并原子替换，不停止 tmux。
备份和安装 receipt 保留在二进制旁；rollback 校验当前文件和备份身份后恢复。
包管理器等非 tdev 管理的二进制不会被默认 upgrade 覆盖。

## 后续范围

HTTP `*.localhost` 代理属于后续版本。
