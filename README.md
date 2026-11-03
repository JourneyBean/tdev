# tdev

通过 SSH 使用、以项目组织和恢复 tmux 工作环境的轻量工具。运行 `tdev setup`
选择已有连接或填写服务器地址，读取目标设备的项目配置，随后打开项目继续开发。
复用系统 OpenSSH 和 tmux，远端无需数据库、HTTP API 或常驻 tdev 服务。

当前版本为 `0.0.3`，已实现连接向导、项目会话、本机端口覆盖、独立转发与重连、文件传输、SSHFS
挂载、签名安装升级和回滚。设计基线见[产品与技术设计](docs/design.md)，实现取舍见
[开发记录](docs/development.md)，实际通过与尚未实测的场景见[第一版验收记录](docs/acceptance.md)。
版本变更见[发布说明](docs/releases/v0.0.3.md)。二进制 Release 位于
[GitHub](https://github.com/JourneyBean/tdev/releases/tag/v0.0.3)。

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
支持子命令、选项、`--server` / `--remote` 服务器名、SSH 别名、窗口与转发 ID。
`tdev open <Tab>` 查询默认服务器的项目，`tdev open dev/<Tab>` 查询指定服务器；
`tdev create <Tab>` 补全服务器前缀，供继续填写新项目名。远端候选每次只读查询，
使用非交互 SSH 和 1.5 秒等待上限，连接不可用时返回空候选。脚本生成过程只读取命令结构。

## 开始使用

```sh
tdev setup                       # 选择已有连接，或填写服务器地址
tdev ls                          # 发现远端项目和当前状态
tdev open my-awsome-project       # 打开工作区
```

向导会读取目标设备的已有配置，按需安装远端 tdev，并保留现有项目。远端没有项目时，可立即进入创建向导，填写项目名和远端已有目录。

也可以直接运行 `tdev open my-awsome-project`。项目尚未配置时，确认创建并填写远端目录，随后直接进入工作区。

## 从客户端管理项目

连接保存后，随时在本机运行项目向导：

```sh
tdev create                                 # 选择服务器，再添加项目
tdev create --server dev                     # 指定已保存的服务器
tdev create dev/demo --directory /home/developer/projects/demo
tdev project list --server dev
tdev project show dev/demo
tdev project update dev/demo --directory /home/developer/projects/new-demo
tdev project window add dev/demo --id web --name 前端 --command 'npm run dev'
tdev project window add dev/demo --id api --name 后端 --command 'uv run python manage.py runserver'
tdev project forward add dev/demo -L 3000:localhost:3000 -L 8000:localhost:8000
tdev open dev/demo
```

项目创建时带有一个 shell 窗口。目录应在远端已存在，向导支持绝对路径和 `~/路径`。
向导输入支持左右方向键、Home/End 和删除键。远端目录输入按 Tab 补全；多个候选时，再按 Tab 进入选择，用方向键切换、Enter 确认。
未指定服务器时，创建向导先列出已保存的服务器；可输入序号或名称，回车选择列表中标注的默认项。`--server dev` 或 `dev/demo` 可直接指定服务器。脚本调用需要显式指定服务器和远端目录。
管理命令通过 SSH 读写远端项目定义，修改带 revision 检查并保留备份；窗口启动命令在新建会话时执行。运行中的会话继续使用当前窗口。

```sh
tdev project window remove dev/demo --id api
tdev project forward remove dev/demo --id FORWARD_ID
tdev override dev/demo --forward FORWARD_ID --local-port 8080
```

`project forward` 管理共享转发定义，本机端口偏好使用 `override`。稳定 ID 可从 `project show` 查看。
完整项目替换使用 `project update --stdin --expected-revision N`，输入为 `project show` 中的原始项目 YAML/JSON；窗口替换使用 `project window update --id ID --stdin`。

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

## 在远端定义项目

下面的命令在有 tmux 的远端执行。目录必须已存在。

```sh
./bin/tdev local project create demo --directory /home/developer/projects/demo
./bin/tdev local window add demo --id api --name api --command 'uv run python -m app'
./bin/tdev local forward add demo -L 8000 -R 9000:3000
./bin/tdev local session ensure demo --json
./bin/tdev local session status demo --json
```

`window add/update/remove` 只编辑启动定义，不改变运行中的窗口。
`session ensure` 并发幂等，已存在的会话不会再次执行命令。未完成初始化的会话
会明确报错，需检查后显式 stop，再 ensure。服务窗口退出后保留输出与退出状态。

`local` 接口不询问确认，适合经 SSH 调用。将 `--json` 放在参数前可确保参数错误
也采用 JSON 协议响应，stdout 不混入诊断。

```sh
./bin/tdev --json local config get demo
./bin/tdev --json local config set demo --stdin --expected-revision 3 < demo.yaml
./bin/tdev --json local window update demo --id api --stdin --expected-revision 3 < api.yaml
./bin/tdev --json local session save demo --expected-revision 3
```

配置严格拒绝重复 key、未知字段、不支持的 schema 和无效端口。修改持有文件锁，
校验 revision，保留旧版本后原子写入；冲突返回 `CONFIG_CONFLICT` 和退出码 4。

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

`api` 是远端映射的稳定 ID，可从 `tdev ls` 查看。项目目录、窗口和命令实时读取，本机只保存端口差异及附加转发。

```sh
tdev forward demo -L 3000:localhost:8000
tdev forward demo -L 8080:localhost:8000 --save
tdev forward demo -R 9000:localhost:3000 --save-remote
```

与 OpenSSH 一致，`-L` 本机监听、`-R` 远端监听，均为
`[bind_address:]port:host:hostport`。支持 `port:port`、`port` 及方括号 IPv6。
默认绑定回环地址，不接管已有连接。保存 reverse forward 时确认多客户端端口
竞争风险；`local forward add` 是无交互管理接口。

每条映射拥有独立 SSH master 和私有控制 socket；通过 OpenSSH 控制请求确认监听
建立后才报告 ready。端口冲突不接管其他连接，失败和重连互相隔离。
`--save` 将 `-L` 映射保存为本机 override；匹配已有远端目标时只覆盖本机监听地址和端口，否则保存为本机附加转发。`--save-remote` 显式保存共享项目定义，反向转发使用此选项。
保存定义与监听建立分别报告，保存成功而监听失败时保留定义并返回部分失败。

前台 forward 和 open 的隧道归当前进程所有，退出时取消自己的映射和连接。
open 期间的后台诊断写入私有 `runtime/logs/open-*.jsonl`，不会插入交互终端。
ready 表示 SSH 监听已建立；应用健康由应用自身检查。

## 传输与挂载

```sh
tdev push demo ./source ./destination
tdev pull demo ./remote-source ./local-destination
tdev mount demo ./empty-mountpoint
tdev unmount demo ./empty-mountpoint
```

远端相对路径以项目根目录为基准；本地相对路径以 cwd 为基准。目录传输将源目录
内容合并进指定目标目录，不因目标是否存在而增加一层目录，不删除目标的其他文件。
覆盖已有文件需确认或 `-y`。传输使用系统 scp/SFTP、编号暂存文件、SHA-256 校验和
逐文件原子替换；提交前检查目标是否变化。目录按文件提交，部分失败时报告已提交进度。
第一版拒绝符号链接和设备文件；Mac 目标会在写入前拒绝大小写或 Unicode 规范化重名。

挂载检查目录内容、实际来源和文件系统 ID；复用正确来源，不覆盖其他挂载。
外部已有挂载可以复用，但不会被自动接管。unmount 使用本地记录，断网时也可操作；
身份不符或 busy 时拒绝，不强制卸载，也不按单独 PID 杀进程。

SSHFS 以独立前台进程运行，避免当前 macFUSE 的后台 fork 问题；mount 命令退出后
文件系统继续运行，诊断位于私有日志。Mac 使用 noappledouble/noapplexattr 和 iconv
文件名转换，并通过远端独立检查验证 CJK 文件名及元数据防护；验证需要项目根目录可写。
Mac 挂载按 NFC 远端文件名工作，混合 NFC/NFD 名称仍受 SSHFS/macFUSE 限制，不会自动
重命名远端文件。详见[验收边界](docs/acceptance.md#尚未完成的环境或人工验收)。

## 安装、升级和回滚

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
