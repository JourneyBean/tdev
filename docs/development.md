# 开发记录与实现取舍

## 本地 daemon 与功能选择

本地 daemon 通过私有 Unix socket 管理控制连接、后台转发及 SSHFS 启动。API、completion 与交互终端共享控制 master；转发、scp 和 SSHFS 使用独立传输。daemon 生命周期锁、逐目标获取锁及独立代次 socket 协调多进程和重连；首次交互认证在调用终端完成，后台使用 BatchMode。

默认 open 的附属资源随 shell 退出释放；多个窗口共享使用权，最后一个退出才清理临时资源。显式服务模式 `open --only forward/sshfs` 后台保留。存续请求断开会回收异常退出客户端的使用权；挂载沿用身份核对与安全卸载。daemon restart 恢复后台转发和活跃 open 的使用权；stop 保持停止，start 恢复。后续 web 功能保留入口并在执行前报告尚未实现。行为、隐私边界及运维细节见 [本地 daemon 与 SSH 连接管理](ssh-connection-reuse.md)。

Last verified: 2026-11-09。版本：0.4.1。历史集成验收使用开发版版本号，见验收记录。

## GitHub CI 与发布

GitHub Actions 在 main 推送、PR 及版本标签事件中运行静态检查和真实 SSH/tmux 竞态测试。发布任务仅接受稳定版本标签，核对源码、构建版本和发布说明，并按测试记录的提交 SHA 构建。workflow_dispatch 可指定已有标签，发布逻辑保存在 workflow 中，支持补发早于流水线的版本提交。

测试任务使用与 GitLab 相同的 Go Bookworm 容器，在独立普通用户下执行，并显式使用 C.UTF-8 locale。隔离 Runner 的交互 shell 配置及 tmux 版本差异，保留完整真实 SSH、PTY、CJK 和连续断线测试。

终端文本断言忽略 ANSI 显示控制码，OSC 标题仍按原始字节验证。补发已有 v0.3.0 时，Go test overlay 使用 workflow 提交中的兼容终端夹具；生产源码及发布构建仍取原标签的提交。

发布公开后调用独立的只读验证 workflow，通过匿名版本下载地址校验全部附件及 Linux 在线安装。验证 workflow 支持手动输入已有标签，供网络问题恢复后单独重验。

签名使用 Actions Secret 中的既有 PKCS8 Ed25519 密钥，先核对仓库公钥，再生成四平台签名包；临时密钥按步骤清理。发布前验证签名、摘要及安装脚本，固定上传 14 个文件，全部上传成功后公开草稿。测试任务只读，发布任务单独授予 contents: write；同一标签的执行串行化。现有 Release 保持原样，失败草稿由维护者检查后处理。配置步骤见 [GitHub Actions 发布](github-release.md)。

## v0.4.1 版本

默认构建与源码版本统一为 0.4.1。本次补丁修复旧备份过期收据阻断升级与回滚的问题，pane 启动命令改为在交互 shell 初始化后执行，`ls` 文本将未运行项目显示为 `inactive`。机器协议、配置 schema 均保持为 1，沿用现有安装签名公钥。具体行为见下文对应章节。

## v0.4.0 版本

默认构建与源码版本统一为 0.4.0。安装状态迁入 XDG 状态目录，重复安装相同内容保留目标文件与回滚链，每个安装目标保留当前版本之前的两个版本。旧收据及完整备份链在安装或回滚时校验迁移；回退到仅支持旧收据路径的历史版本后，使用新版安装脚本再次升级。

新建项目会话使用 `tdev-project-<name>`，旧会话继续按 UUID 识别复用。打开本机项目时跳过目标为回环地址的自动转发；scratch 列表每个会话显示一行。机器协议、配置 schema 均保持为 1，安装签名公钥沿用现有版本。发布通过现有 GitHub Actions 完成真实 SSH/tmux 验收、四平台签名构建及公开下载验证。

## v0.3.0 版本

默认构建与源码版本统一为 0.3.0。本次次版本更新包含 pane 分屏保存与恢复、ls 六列表格及 doctor 自然语言诊断。机器协议和配置 schema 仍为 1；分屏配置通过 pane-layout-v1 协商兼容性，安装签名公钥沿用现有版本。

## doctor 自然语言诊断

`doctor` 的默认输出先概括本次检查是否有提示，再以自然语言解释依赖缺失、服务器刷新失败、会话状态及客户端覆盖配置错误，并给出影响和下一步操作。会话 missing 表示尚未启动；sshfs 缺失提示挂载功能所需条件。同名未托管会话、重复项目标记和会话改名分别解释，未知 warning 保留原始原因。离线项目只说明缓存保留情况，当前状态由成功刷新的结果解释。指定项目的建议命令使用完整服务器及项目名称。

诊断使用现有检查数据进行规则归纳；`--json` 和 `api doctor` 继续输出机器数据。依赖路径、配置根和项目原始字段供 JSON 调用方读取。

## ls 表格展示

`ls` 文本输出将项目会话的 `missing` 状态显示为 `inactive`，表示项目尚未启动或已停止。JSON、服务端状态与缓存继续使用 `missing`，兼容现有机器接口和旧服务端。

`ls` 的文本输出使用 SERVER、NAME、TYPE、STATUS、DIR、FORWARDS 六列总表，服务器与项目名称分别展示。列宽使用终端显示单元计算，支持中文名称对齐；FORWARDS 显示 `监听地址:端口 -> 目标地址:端口`，R 方向使用 `<-`；IPv6 地址保留方括号，省略配置中的映射 ID。第一条转发与项目同行，后续转发在 FORWARDS 列下方排列，无映射显示 —；scratch 每个会话显示一行。空服务器保留创建提示，服务器错误和缓存时间放在表格下方。缓存刷新与最终输出共用渲染器，管道保留完整路径，JSON 协议沿用现有结构。

## 本机项目自动转发

`open local/<project>` 按生效配置中的服务目标地址筛选自动转发，跳过 `localhost`（大小写不敏感）、`127.0.0.1` 和 `::1`，L/R 方向均适用。其他地址保留自动转发；全部跳过时直接附着会话，省去监听器与转发日志。筛选仅作用于 `open`，保存的项目定义、客户端覆盖和显式 `forward` 命令保持原有语义；远端项目继续启动全部生效转发。

## pane 分屏布局

项目窗口新增可选 `layout` 树。`horizontal` 按左右排列，`vertical` 按上下排列，可嵌套；叶节点保存稳定 pane ID、目录及显式启动命令，`weight` 保存同级空间比例。原单 pane 窗口的 directory/command 格式继续有效。layout 窗口的启动命令放在叶节点；目录相对项目根目录解析。布局深度限制为 16，叶节点 ID 在窗口内唯一。

ensure 先检查全部目录，再按树建立分屏并标记 pane 身份。pane 使用 tmux 的默认 shell 和启动配置，通过 `send-keys -l` 输入已声明命令，再单独发送 Enter；交互 shell 初始化后执行命令，命令结束后继续保留 shell。显式 `exit` 结束 shell 时保留 pane 输出与退出状态。命令只在新建会话时执行；初始化失败保留 incomplete 会话，重复 open 沿用已有会话。

save 读取 tmux window_layout 与 pane 当前目录，两次快照比较后按 revision 写入。保存方向、嵌套结构、比例和手动新增 pane；启动命令按 pane ID 从原定义保留，新增 pane 保存为空 shell。持久标记继续按窗口生效。普通单 pane 窗口保存为原格式。pane 标记在配置提交后更新，失败返回 PARTIAL_SUCCESS。旧版单 pane 会话首次保存时补齐标记；旧版服务窗口已被手动分屏且缺少标记时，需先为原服务 pane 设置 @tdev_pane_id 为窗口 ID，再保存。

API 增加 `pane-layout-v1`，携带 layout 的远端写入先协商该能力。配置 schema 仍为 1；使用分屏定义的机器与编辑配置的客户端需使用支持该能力的版本。交互窗口编辑保留已有布局，pane 配置通过 `edit --stdin` 修改；也可在 tmux 手动分屏后 save。分屏尺寸受 tmux 窗口可用空间限制，比例按整数单元格近似恢复。

## v0.2.1 发布

默认构建版本与源码版本更新为 0.2.1，使用新 tag 发布已合入 main 的 GitLab CI 与连续 SSH 重连测试修复。连续断线测试为附着查询与稳定计时留出时间，检查两次恢复后的输出与重试计数。机器协议、配置 schema 和安装信任公钥沿用现有版本。

## scratch 临时会话

恢复入口统一为 open，scratch 用户命令仅保留创建。open 的解析在现有项目解析器中按需纳入 scratch；其他项目操作保持项目范围。裸名称先查本机，再查包含项目和 scratch 的服务器缓存，最后查询远端实际定义与会话。缓存仅用于定位，进入前重新读取项目与 scratch；同一目标同名返回冲突，已知缓存 scratch 结束后返回缺失，未知名称沿用项目创建向导。scratch 附着跳过项目转发与挂载，--mount 返回参数错误。open 的名称补全包含 scratch，ls 每个 scratch 会话显示一行。

`scratch` 使用独立 `@tdev_scratch_name`、`@tdev_scratch_directory` 和 generation 标记，创建与标记在同一 tmux 命令队列中完成，标记显式指定新会话目标，保留调用方的 tmux 上下文。逻辑名称独立于项目名称，实际 tmux 名称为 `tdev-scratch-<name>`，未指定名称时直接生成 UUID 作为逻辑名称；同名普通会话保持原状，实际名称占用时报冲突。配置根内的 scratch-session 锁串行化同名创建，tmux 的名称唯一约束处理跨配置根竞争。会话不携带项目 UUID，项目配置和 save 继续只管理项目会话。

临时会话复用现有 OpenSSH 终端附着、客户端就绪检测及重连预算，按 session ID 与 generation 校验原会话。本机 tmux 内使用 switch-client 进入临时会话。机器 API 保持非交互，通过新增 scratch-v1 能力检查；JSON scratch 用户入口创建后返回，终端入口执行附着。

ls 缓存新增 scratches 字段，保留 projects 的既有结构。展示阶段应用类型筛选，缓存保留完整发现结果；离线保留上次成功刷新数据。旧服务器缺少 scratch-v1 时仍刷新项目，并通过 scratch_warning 提示升级；其 scratch 列表为空。

## v0.2.0 版本与兼容性

GitLab CI 按 test/build/release 分阶段执行，受保护的稳定版本 tag 触发签名及发布。签名密钥通过 Protected File 变量提供，临时复制至仓库之外并核对 docs/release.pub。构建检查 tag、Makefile、源码版本和发布说明一致；发布前再次验证签名及产物摘要。Generic Package Registry 保存固定清单的 14 个文件，文件就绪后通过 CI_JOB_TOKEN 创建 Release 和永久下载链接。已有相同包文件可复用，内容冲突和已有 Release 保留原样；resource group 串行化发布。API 错误只报告操作和状态，下载重定向移除 job token。

安装包生成器接受成对的版本下载目录和升级目录，通过模板引用写入脚本；默认值保留 GitHub 来源。GitLab 构建内置当前项目的 tag 下载目录及 latest Release 升级目录，安装后凭据记录后者。显式 release-url 保留覆盖行为；URL 限制沿用 HTTPS 且无凭据、查询和 fragment。在线 GitLab 安装要求匿名可读，私有项目使用外部认证下载后的离线安装路径。

默认构建版本与源码运行版本统一为 0.2.0。协议版本、配置 schema 和 capability 要求沿用现有定义；兼容性继续按能力检查。历史发布说明与升级回滚测试中的固定版本保留原值。当前发布说明汇总诊断日志与 zsh 目录补全修复。

## v0.1.0 项目入口与 API

远端目录补全将完整枚举脚本引用为单个参数，通过 sh -c 执行。OpenSSH 仍使用账户登录 shell 解析外层命令，内部通配符和循环遵循 POSIX sh 语义；zsh 的 NOMATCH 设置不会中断没有隐藏目录或空目录的查询。路径中的引号、空格和 shell 元字符继续通过两层参数引用保持原值，HOME 在远端 sh 中展开。该修复位于客户端生成的命令中，更新客户端即可生效。

诊断日志统一写入当前配置根下的 logs/diagnostics.jsonl，默认只记录失败，--debug 增加成功命令与 SSH/API 批次耗时。记录包含 UTC 时间、PID、固定命令路径或操作名、服务器名称、耗时、协议错误码、已知原因类别和可用的进程退出码。分类从内存中的错误文本提取固定标签，原始错误、命令参数、stdin、环境变量及远端输出保持在日志之外；未知原因记为 operation_failed。shell 请求在上下文到期时优先保留 deadline_exceeded，避免进程被终止后只剩 signal 错误。目录补全在当前查询失败时显示类别与日志位置，过期结果继续丢弃。

日志使用配置根内独立内核锁串行追加和轮转，每次写入的锁等待预算为 250 ms，独立于操作取消上下文。目录权限 0700、文件 0600，拒绝符号链接日志目标及开放权限的现有日志文件；单文件 1 MiB，保留三个备份。写入错误保持原命令退出码与 JSON 协议，交互向导及人类可读命令输出提示日志不可用。已有 forward 进程专用诊断文件继续使用原生命周期。

ls 依据标准输出是否为终端启用原地刷新，先显示本机与远端缓存，再启动并发查询；标准输入与标准错误的重定向独立于此判断。刷新阶段与最终输出共用列表格式，warning 合并进项目状态列，末尾统一提示 doctor。JSON 保留完整 warning 与 override_error；doctor 汇总项目诊断，指定项目时展示对应诊断与依赖信息。管道与 JSON 输出一次最终结果。

setup 的客户端配置锁仅覆盖重新读取、冲突检查和连接配置写入；保存后立即释放，再执行缓存更新与首个项目向导。多个终端可同时停留在项目输入界面，后续 setup 可独立保存连接。

会话状态通过一次 `list-sessions` 读取 UUID、名称和初始化标记，按 UUID 确认归属。旧版命名及手工改名的托管会话直接复用，名称差异返回 `session.warning`；`tdev-project-<name>` 被无匹配 UUID 的会话占用时返回 `unmanaged` 和 warning，`open` 创建独立的 `tdev-project-<UUID>` 托管会话（UUID 去掉连字符），原会话保留。普通会话使用项目逻辑名称时与项目独立。重复 UUID 返回 warning，接管和停止操作返回 `SESSION_UNMANAGED`。`ls` 保持各项目与服务器的刷新结果，warning 展示在项目状态标签中；应用错误状态为 `error`，连接失败为 `unreachable`，协议不兼容为 `upgrade-required`。

setup 的初次项目发现与首个项目创建后的刷新仅读取项目定义，返回及缓存的会话状态为 unknown，由 ls 刷新实际状态。sh 创建窗口后，仅在接下来需要 attach 时额外读取会话状态；JSON 输出与本机 tmux 内窗口切换直接使用创建结果。显式指定项目的配置、转发、传输、挂载与补全操作独立于 tmux 状态；省略项目名时读取当前会话的项目标记用于定位目标。doctor 和安装前检查保留 tmux 程序依赖检查。

`open` 完成会话、可选挂载和转发准备后，在进入 tmux 前向终端标准输出发送 OSC 2，将标题设为 `tdev: server/project`，本机服务器名为 local。标准输出重定向时跳过标题序列；标题使用已验证的服务器名和项目名。

默认服务器字段已从运行配置和 setup 选择逻辑移除；已有文件的 default_server 仅在解码时丢弃，setup 重写配置时移除该字段，其他未知字段继续严格拒绝。服务器顺序统一按名称排列，本机项目优先规则保持一致。窗口 ID 的旧补全分支已删除。forward 只运行映射，保存由 add/remove 和 override 负责；旧保存参数与把数字参数解释为端口的简写已移除，数字项目名按普通项目解析。

用户命令直接操作项目：create/show/edit、ls、open/save/sh/stop、forward、push/pull、mount/unmount。原 project 与 window 用户分组移除；机器接口由 local 改为 api。`api project list/create/get/set` 统一读写完整定义，session 管理实际会话，transfer/binary/mount-probe 管理对应机器操作。

local 是保留的内置服务器名，无需连接配置。完整项目标识为 server/project；裸名称先检查本机，再按远端名称排序搜索。临时索引命中后，实际操作读取目标机器的最新定义；命中离线目标保留原目标并报告失败。缓存未命中时逐台读取指定项目定义，PROJECT_NOT_FOUND 继续查找，其他错误保留；解析目标独立于 tmux 状态，支持从旧版远端拉取更新。create 的裸名称默认本机，空参数向导提供本机和远端选择。

ls 并发刷新，每台服务器的版本与项目/会话查询合并为一次 SSH。终端先显示缓存和 syncing，逐项更新后输出最终列表；管道与 JSON 仅输出一次最终结果。缓存位于系统临时目录的用户私有目录，按客户端配置根和连接信息的哈希隔离，0600 文件原子替换。成功空列表替换旧快照；失败保留更新时间与缓存。旧版 API 标记 upgrade-required；部分失败返回 9，完整列表在 error.details。

每次机器请求通过 shell 顺序执行顶层 version、`api version --require` 与实际子命令。用每次生成的随机标记记录各段输出和退出码，本地严格解析。API gate 成功后才执行读写，不依赖 jq 或新增请求封装。多项独立查询使用同一 SSH 批次；stdin 保留给实际写操作。旧服务端显示已读取版本和 upgrade --remote 指引，升级通过上传已验证候选程序引导，原 API 不可用时仍可执行。

create、open 缺失项目和 edit 使用同一份草稿向导。目录、窗口和映射全部收集并验证，最终确认后再写入；完整创建通过 api project create --stdin 原子完成。edit 预填原值并携带原 revision。L 映射的服务地址一次输入 host:port，客户端监听差异按 UUID 和 forward ID 保存在 overrides.yaml。保存定义与保存 override 的部分失败分别报告。

新建项目 tmux session 使用 `tdev-project-<name>`，UUID 标记验证归属并支持复用旧名称的会话，保留原 session ID 和连接。新项目窗口 main；sh --name 指定新窗口名称，默认 shell。open 默认只建立会话、转发和终端；--mount 显式挂载，独立 mount/unmount 保留。挂载路径按项目 UUID 保存在本机 override；带 shell 的 open 退出时释放本次新建挂载；预先存在及其他使用者所需挂载保留。本机项目直接进入 tmux，转发用本地 TCP listener，文件传输复用暂存与哈希校验。

## v0.0.3 连接向导与配置发现

`setup` 从本机已有 tdev 连接和 SSH Host/Include 发现候选，支持搜索、序号选择和直接填写地址。读取 SSH 配置时仅提取展示字段，避免为了展示列表执行 Match 命令。向导本身不受单次 SSH 超时限制，每次远端请求独立计时；Ctrl-C 或 EOF 退出。参数形式和 JSON 机器接口保留。

验证连接后探测已有远端程序，读取远端项目定义，按需提示安装。新地址使用私有 SSH profile，既有服务器保留原配置目录与偏好。提交本机连接前重新读取并检查同一服务器配置，冲突时停止，其他服务器的并发改动保留。

`client.yaml` 继续保存连接及重连偏好；本机监听端口差异和附加 -L 单独保存在 `overrides.yaml`。`ls` 和 `open` 每次发现远端定义后合并，覆盖按项目 UUID 和稳定 forward ID 绑定；过期身份、已删除映射、重复监听和非法端口在执行前报错。`get` 保持读取远端原始定义，避免 `save` 把客户端差异写回远端。共享定义通过 `forward add/remove` 或 edit 修改。

## v0.0.2 安装与分发

提供四平台的二进制和压缩包。发布时从已签名 manifest 生成版本专属安装脚本，内置发布公钥与各平台的 SHA-256。bootstrap 用固定摘要校验二进制后执行候选程序，由候选程序再次验证 Ed25519 manifest、版本和协议。普通安装复用系统 SHA-256 工具；自定义可信公钥模式保留 OpenSSL 3+ 签名校验路径。

默认在线安装记录 `releases/latest/download`，后续 `tdev upgrade` 复用已保存的公钥获取最新签名包。交叉构建使用 `-trimpath -buildvcs=false`，避免产物携带工作目录或构建时工作树状态。

`tdev path` 默认输出当前 shell 的配置命令；`--write` 为用户启动文件写入独立标记块。写入持有配置锁，备份已有内容并保留权限和符号链接；修改前再次检查文件内容，重复执行保持幂等。安装器默认配置 PATH，自定义安装目录需 `--add-path`，或使用 `--no-modify-path` 跳过。当前父 shell 由用户执行打印的命令或新开终端后生效。

源码发布于 [GitHub](https://github.com/JourneyBean/tdev)。

## 已实现的第一版范围

项目配置、窗口与保存转发定义、tmux 会话、setup、ls、open、save、sh、stop、
forward、push/pull、mount/unmount、doctor、install/upgrade/rollback 已提供实际实现。
远端通过按需启动的 CLI/attach 进程管理配置与会话。
HTTP 域名代理仍属于后续版本。

配置使用严格 YAML/JSON 解码，拒绝重复 key、未知字段和不支持的版本。
修改持有内核文件锁，比较 revision，保留历史版本后原子写入；配置默认 0600。
api 接口无确认交互，--json 返回单一协议响应；客户端按 error.code 区分远端错误和连接错误。
协议握手同时检查 schema 和所需 capability，不以程序补丁版本完全相同作为前提。

## 转发所有权与就绪

每条 L/R 映射单独建立 SSH master，控制 socket 位于用户私有临时目录。
建立 master 后，通过 `ssh -O forward` 获取监听建立的确认；控制请求使用独立的空 SSH
config，避免把用户 config 中保存的其他转发也提交给本次 master。
保留 SSH alias 的连接、认证和 ProxyJump 等能力；不接管用户已有 ControlMaster。

映射分别监督并使用 forward 重连策略。冲突不会杀死其他监听，保存定义与实际连接状态
分别报告。前台 forward 退出时取消自己的映射并关闭连接，随后删除控制 socket。open 的映射由 daemon 按使用权管理，后台状态通过 daemon status 查询。
ready 表示 SSH 监听已建立；应用健康由应用自身检查。

## 终端重连

全局、服务器覆盖和 CLI 覆盖依次生效。attempts 支持 0、非负整数及 always；N 表示
初始尝试之外追加 N 次。等待和连接尝试受取消 context 控制，Ctrl-C 返回 130。
交互 SSH 直接继承终端文件描述符，键盘 Ctrl-C 在 raw 模式下作为远端输入传递。
退出或断线后恢复终端状态。

重连始终查询原会话的 session ID 与随机 generation；缺失或被替换时结束，不再次 ensure。
attach 的就绪检查依据远端 tmux 实际 client PID，避免仅因探测成功就重置预算。
连接稳定至少 max(重试间隔, 1s) 后才重置本次失联计数，防止快速连接/断开空转。
连续断线集成测试在第一次恢复后留出附着查询和稳定计时的时间，并分别验证两次恢复的终端输出与重试计数。
重连使用批模式，认证和 host key 问题不会持续弹交互提示。

连接故障结合连接阶段和 SSH 诊断分类；首次失败使用诊断白名单识别可重试故障，未知
原因保守退出。控制连接使用 INFO 日志级别，保留故障诊断和用户 locale。

## 传输一致性

单文件 push/pull 到已有目录时保留源文件名，包括 pull 的 ./ 目标；目标为显式文件名时支持重命名。CLI 在目标机器上先展开目录，再复用精确路径计划、覆盖确认和并发快照校验，目录源继续按内容合并。push 通过 api transfer plan --source-name 传递 basename，并协商 transfer-file-directory-v1 能力；pull 在客户端解析目标，兼容原有远端传输响应。basename 校验拒绝路径穿越，符号链接目标继续拒绝。

采用系统 scp/SFTP，暂存文件以编号命名，避免把用户文件名交给远端 shell 或 glob 解释。
目录内容合并进精确目标目录，保留空目录，不删除无关文件，不随目标存在与否改变层级。
覆盖前展示目标；提交前检查快照和 SHA-256，再逐文件原子替换。
目录按文件提交，部分失败时保留已提交文件并报告进度。

符号链接和特殊文件在第一版明确拒绝。UTF-8 字节保持原样，不自动重命名或统一 Unicode
形式。Mac 目标在写入前保守拒绝大小写和规范化等价名称，以免发生别名覆盖。
断线可能使远端暂存目录未能清理，错误会给出 transfer ID；可通过 api transfer abort 清理
经 receipt 验证的本次暂存目录，不扫描删除其他目录。

## SSHFS 生命周期与 Mac 兼容

挂载前核对目录内容和实际 mount inventory。记录实际来源、文件系统 ID、项目 ID 和 token；
卸载先核对当前挂载身份。若有 lsof，则先查询占用 PID，busy 时拒绝；不强制卸载或按 PID
单独判定归属并杀进程。unmount 只依赖本地连接配置和挂载记录，不需要服务器在线。

当前 SSHFS 3.7.5/macFUSE 5.4 组合的默认后台 fork 路径实测失败。因此采用独立 session
中的 `sshfs -f`，stdin 为 /dev/null、stdout/stderr 为私有日志。mount 返回后文件系统进程继续
运行；卸载后自然退出。

macFUSE 可以覆盖 fsname，不能只凭期望的 fsname 验证归属，因此保存实际 source 和 FSID。
Mac 添加 noappledouble/noapplexattr，并通过远端独立探测检查 CJK/NFC 文件名、普通文件保存、
AppleDouble 和 com.apple.* 属性，成功后才设置 utf8_verified/metadata_verified。
这些探测需要项目根目录可写。探测仅删除带本次随机 token 的专属文件。

当前 libfuse3 不接受旧版 norm_insensitive 选项，改用实际可用的 iconv 模块：
`modules=iconv,from_code=UTF-8,to_code=UTF-8-MAC`。Mac 挂载按 NFC 远端名称工作；
混合 NFC/NFD 名称及等价重名仍是原生 SSHFS/macFUSE 的兼容边界，不自动改写远端名称。
参考 [macFUSE 文件名规范](https://github.com/macfuse/macfuse/wiki/File-Names-%28Unicode-Normalization-Forms%29)
和[挂载选项](https://github.com/macfuse/macfuse/wiki/Mount-Options)。

## 安装与信任

发布工具生成四平台裸二进制、tar.gz、SHA256SUMS、manifest 及 Ed25519 分离签名。
签名密钥由外部提供，工具不生成或保存生产私钥。首次安装显式提供独立可信的 PEM 公钥；
同源下载的 release.pub 不会自动获得信任。后续使用已安装 receipt 中的公钥和 HTTPS 源。

bootstrap 先通过 OpenSSL 3+ 校验签名校验表和二进制哈希，再执行候选程序；候选程序再次
校验 manifest、版本和协议，保留备份并原子安装。Go updater 直接验证签名 manifest，限制
下载大小、HTTPS 和安全重定向。远端不必访问发布网站：客户端验证后上传，远端校验上传
内容，再执行签名候选安装。更新不重启 tmux，不修改 authorized_keys，不执行 sudo。

二进制安装在验证候选后比较实际内容。相同内容保留目标文件，按需恢复 0755 权限；匹配当前哈希的 receipt 沿用已有回滚记录，下载源与信任信息更新为本次验证结果，内容相同的 receipt 跳过写入。缺少 receipt 的相同二进制直接登记；哈希过期的 receipt 清除旧回滚引用，损坏的 receipt 报错。只有二进制内容变化时创建新备份。安装状态根目录为 `${XDG_STATE_HOME:-~/.local/state}/tdev/`，相对的 XDG_STATE_HOME 按未设置处理。`installations/<规范化安装目标绝对路径的 SHA256>/receipt.json` 保存安装记录，`backups/` 保存该目标的二进制与历史 receipt；安装锁放在状态根目录，跨配置根目录的同一安装目标共享锁。

读取优先使用状态目录记录，缺失时兼容二进制旁的 `.tdev-install.json`。安装与回滚持锁迁移记录及其引用的完整备份链，也兼容配置根目录下曾使用的 `binary-backups/`。迁移逐级校验目标、哈希和记录对应关系，复制并原子写入新记录后清理旧文件，支持跨文件系统迁移。迁移失败保留旧数据；未被记录引用的文件保留原位。每次成功安装或回滚后，将回滚链限制为当前版本之前的两个版本，先原子更新最旧保留版本的 receipt，截断更早的引用，再清理当前目标备份目录中已失去引用的安装器备份。回滚后清理已恢复版本的备份副本；无关文件、目录和符号链接保留。远端 bootstrap 在目标机器解析 HOME、XDG_STATE_HOME 和安装目标路径，优先读取新记录，兼容旧记录，复用已信任的发布公钥与下载源。

旧安装器在二进制被外部替换后升级，可能把过期 receipt 复制给备份。迁移旧相邻目录或配置目录备份时，若备份哈希与父记录一致、随附 receipt 的安装目标正确而哈希过期，仅迁移已校验的二进制，并在此处截断 receipt 链。原始备份、过期 receipt 和其更早历史保留原位，供人工检查。回滚可恢复该二进制，恢复后结束自动回滚链。备份哈希损坏、receipt 格式错误、安装目标不符和新状态目录中的收据不一致继续报错。新安装创建备份时，仅关联与实际备份哈希一致的 receipt。

setup 自动读取已有连接，也支持使用现有 identity/agent 创建私有 SSH profile，Include 既有 SSH config。认证交给系统 OpenSSH，安装与传输需要可批模式认证的身份。缺少 tmux 等依赖时提示先准备环境。四平台签名二进制与安装脚本由公开 Release 分发。

## tmux 标题提醒

项目与 scratch 创建时设置运行中的 tmux 标题及 hook，已有会话在下次附着前补齐。本机和远端使用同一逻辑，状态与 hook 随 tmux 运行时保存。标题为 `session:window`，任一窗口有原生 bell、activity、silence 提醒或失焦 bell 标记时加 `[!]`。开启 `monitor-activity` 时普通输出也属于提醒。agent 完成信号沿用应用发出的 bell。

`client-focus-out` 记录 session 失焦；此时当前窗口的 `alert-bell` 设置窗口选项 `@tdev_unfocused_bell`。重新聚焦清除当前窗口的补充标记；切换窗口清除进入窗口的补充标记。重新附着保留补充标记，附着或切换 session 后默认聚焦，等待终端后续焦点上报。其他窗口标记继续汇总，原生提醒由 tmux 清除。应用仅使用 tmux 命令与格式，无需 agent hook、后台进程或轮询。session 的配置锁与版本标记保证重复附着沿用同一组 hook。

标题、bell-action 和 hook 作用于托管 session，monitor-bell 作用于其窗口，包括后续新建窗口。首次设置 session hook 时复制当时继承的同名全局动作，再追加 tdev 动作；已有本地动作保留。用户配置文件及全局标题、hook 保持原状。`focus-events` 是 server 级选项，需在首次附着前运行时开启，并保持到该 server 结束；同 server 的其他客户端也受此焦点上报设置影响。已附着的客户端需重新附着以请求终端焦点上报。终端缺少焦点上报时仍使用原生窗口提醒。早期隔离调研协议与结果保存在仓库本地 `.summary-web/`。

## 验证与回退

Shell 补全复用 Cobra 的 Bash V2 / zsh 脚本生成和隐藏补全协议。注册服务器、项目、SSH 别名及定义 ID 的候选函数；远端项目来自 `api project list` 原始定义，批模式 SSH 查询限时 1.5 秒，避免终端 Tab 等待交互认证或输出诊断。Bash 脚本提供私有帮助函数，兼容未安装 bash-completion 的环境。文件传输的本机路径继续使用 shell 文件补全。
安装器成功安装后可配置补全：管道脚本通过 `/dev/tty` 询问，支持显式选择或跳过；本机二进制 install 也提供对应选项。`completion install` 复用 shellpath 的备份、权限、符号链接及并发保护，在独立 completion 管理区块中加载绝对路径的二进制。共享 `.profile` 保持 POSIX sh 语法，并仅在匹配的交互 shell 中加载补全。

Shell 补全提供 local 与远端前缀，裸项目前缀并发查询全部目标，远端查询保持 1.5 秒上限。客户端项目管理通过新 API 读写，旧版服务端需要升级。向导终端输入使用 Bubble Tea/Bubbles，远端目录只读枚举，候选使用 NUL 分隔，过期查询结果丢弃。输入历史关闭，取消和外部信号恢复终端设置。

实际结果和待验证项见[验收记录](acceptance.md)。构建使用 Go 1.27.3，依赖记录于
go.mod/go.sum。运行产物无需 Go。

配置回退使用对应 `.yaml.rev-N.bak`，读取当前 revision 后显式导入。
二进制回退使用 receipt 中带哈希的备份及 upgrade --rollback，外部修改会被拒绝覆盖。
