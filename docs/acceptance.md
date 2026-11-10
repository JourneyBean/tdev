# 开发验收记录

Last verified: 2026-11-10。源码版本：0.6.0。下表保留集成验收使用的开发版版本号。

本记录汇总第一版核心功能的自动化验证、环境实测和待验证项。

## v0.6.0 版本验收

2026-11-10：源码、Makefile、README 与发布说明统一为 0.6.0。`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s -json ./...` 全部通过，含子测试 269 项；CLI 211.029 秒、daemon 3.231 秒、真实隔离 tmux 13.224 秒、updater 7.555 秒。覆盖签名安装后处理及重试、活跃终端配置迁移、升级回滚、服务器临时 L/R 转发、后台恢复、前台退出清理和项目映射编辑。`make check`、Linux/macOS amd64/arm64 四平台 `make cross` 与 `make build` 通过；临时配置下构建产物 JSON version 返回 0.6.0、post-install-v1，协议及项目 schema 均为 1。

原生环境为 Linux amd64、Go 1.27.3、tmux 3.6、OpenSSH 10.2。测试使用临时配置和状态根、临时 SSH 身份、私有 tmux socket 与 `/dev/null` 配置；fish 未安装，对应子测试跳过。macOS 原生交互、OpenSSH 10.3+ 实际通道、真实历史发布包回滚及 FUSE 等覆盖边界见本记录对应章节。

公开文件、文件名与四平台二进制扫描未发现凭据或内部环境信息泄漏。五处模式命中经复核为既有产品迁移名称与通用测试路径；提交与标签使用公开 noreply 身份。

2026-11-10：首次标签 CI 在 tmux 3.3a 的服务退出用例失败，已退出 pane 的 pane_dead_status 为空。临时解包 Debian tmux 3.3a 复现，延长等待仍为空；改为验证 pane_dead、保留输出及启动计数后，专项连续 10 轮通过。完整 tmux 回归随后暴露分屏用例的同类断言，统一调整后通过。tmux 3.6 完整竞态回归通过，make check 与 actionlint v1.7.12 通过。补发使用 Go test overlay 读取兼容测试夹具，生产源码继续取原 v0.6.0 标签。CI 同时修复提交 SHA 读取时的目录归属检查和命令替换错误遗漏。

2026-11-10：GitHub 的 [v0.6.0 发布流水线](https://github.com/JourneyBean/tdev/actions/runs/42823868357) 全部通过，CLI 完整竞态集成 193.966 秒、真实 tmux 17.942 秒、daemon 4.520 秒、updater 11.335 秒。测试检出原标签提交，构建明确检出同一提交 52b5e5b；测试覆盖文件仅调整 tmux 断言。Linux/macOS amd64/arm64 四平台签名包共 14 个附件已公开，Release 设为 latest。匿名下载验证通过可信公钥、Ed25519 manifest 与 SHA256SUMS 签名、全部摘要及压缩包内容检查；临时在线安装返回 0.6.0，post-install 两项处理均成功。公开发布与 CI 日志扫描未发现凭据或内部环境标识。

并行 main 校验曾在 R 转发恢复用例耗尽 20 次重试；发布任务的该用例及全部包随后完整通过。该次失败记录作为恢复时序的验收观察保留。

## v0.5.0 版本验收

2026-11-10：源码、Makefile、README 与发布说明统一为 0.5.0。`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，含子测试共 248 项；CLI 173.324 秒、daemon 2.945 秒、真实隔离 tmux 9.192 秒。覆盖本地和远端签名升级/回滚、活跃终端重连、L/R 映射恢复、ps 汇总与操作分配、停止状态保持及标题提醒。`make check`、四平台 `make cross` 与 `make build` 通过；构建产物 JSON version 返回 0.5.0、ps-v1，协议及 schema 均为 1。

原生环境为 Linux amd64、Go 1.27.3、tmux 3.6、OpenSSH 10.2。fish 未安装，对应子测试跳过；OpenSSH 10.3+ 的实际通道、macOS 原生交互与真实 FUSE 等限制见“尚未完成的环境或人工验收”。公开文件及待推送历史扫描未发现凭据、内部环境标识或私人页面引用；两处路径命中为通用测试示例。提交与标签使用公开 noreply 身份。

2026-11-10：GitHub 的 [v0.5.0 发布流水线](https://github.com/JourneyBean/tdev/actions/runs/42668096233) 全部通过，云端 CLI 完整竞态集成 167.747 秒、真实 tmux 13.741 秒，CI 使用 tmux 3.3a。Linux/macOS amd64/arm64 四平台签名包共 14 个附件已公开，Release 设为 latest。匿名下载验证通过可信公钥、Ed25519 manifest 与 SHA256SUMS 签名、全部摘要及压缩包内容检查；临时在线安装的 Linux amd64 程序返回 0.5.0。公开发布与 CI 日志扫描未发现凭据或内部环境标识。

2026-11-09：审计开始时工作区、暂存区及未跟踪文件均为空，验证对象为 `5546ef3`。重点复查本地 daemon、open 功能选择与 tmux 标题提醒，未发现确定的阻断问题。`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，含子测试共 238 项通过；CLI 115.301 秒、daemon 2.205 秒、真实隔离 tmux 8.557 秒。`make check`、四平台 `make cross` 和 `make build` 通过，构建产物 JSON version 返回 0.4.1、协议及 schema 均为 1。原生环境为 Linux amd64、Go 1.27.3、tmux 3.6，真实 SSH/PTY 与 tmux 使用临时配置及私有 socket。fish 未安装，对应 shell 路径子测试跳过；真实 FUSE、macOS 原生交互、终端焦点渲染及多客户端限制沿用下文验收边界。

2026-11-09：标题提醒已接入托管项目与 scratch 的创建及附着路径。真实隔离 tmux/PTY 回归验证失焦当前窗口 bell、焦点恢复只清除当前窗口补充标记、其他窗口原生及补充提醒继续保留、逐窗口切换清除、聚焦时当前窗口 bell 沿用原生已读逻辑，以及 60 秒周期刷新下事件立即更新 OSC 标题。配置回归验证继承的全局 hook 保留、重复设置幂等、普通 session 与全局标题/hook 保持原状、scratch 自动设置。真实本机与隔离 SSH 的 CLI 附着回归验证旧托管 session 自动补齐、重新附着保留补充标记、焦点事件输入、bell 到 OSC 标题输出和聚焦后的多窗口提醒保留。

`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，CLI 集成 122.227 秒、真实 tmux 测试 8.382 秒。`go vet ./...`、`gofmt -l cmd internal`、`git diff --check` 与四平台 `make cross` 通过。原生执行环境为 Linux、tmux 3.6；焦点事件由 PTY 注入，真实终端和多客户端的覆盖限制见“尚未完成的环境或人工验收”。

2026-11-09：标题提醒调研使用 Linux、tmux 3.6、独立临时 socket、`/dev/null` 配置及真实 PTY，记录 11 个阶段，7 项核心断言通过。A、B 后台窗口依次输出 BEL 后，session 提醒为 `0!`、`0!,1!`；切到 A 后保留 `1!` 和带 `[!]` 的 OSC 标题，切到 B 后提醒为空并发送普通标题。刷新间隔设为 60 秒，各后续阶段在 0.5 秒采集窗口内捕获事件更新；标题内容相同时省略重复发送。当前窗口在聚焦和失焦时输出 BEL 均未留下提醒；注入焦点进入/离开事件可改变 focused 标志。activity 监控下普通输出产生 `0#`，切换至该窗口清除。候选方案尚未接入 tdev；真实 agent 信号、外层终端渲染、SSH 全链路、嵌套 tmux、多客户端和其他版本待验证。本轮未修改 Go 产品代码，未重跑产品构建与 Go 测试。协议和原始结果保存在 `.summary-web/evidence/`。

2026-11-08：`ls` 文本输出将尚未启动或已停止的项目状态显示为 `inactive`，JSON 与缓存沿用 `missing`。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成用时 128.087 秒、真实隔离 tmux 测试用时 3.998 秒；跨服务器列表集成验证新文案。`make check` 与 `make build` 通过。执行环境为 Linux，本次仅调整列表展示。

## v0.4.1 版本验收

2026-11-08：默认构建、源码版本与发布说明统一为 0.4.1。`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，真实隔离 SSH/PTY 的 CLI 集成用时 134.853 秒、tmux 用时 6.573 秒、安装升级测试用时 8.102 秒，覆盖交互 shell 启动与继续输入、分屏、过期收据迁移和回滚、跨服务器列表文案及连续断线重连。`make check`、`make cross` 和 `make build` 通过；Linux amd64 构建的 JSON version 返回 0.4.1，协议及 schema 均为 1。公开文件与文件名扫描未发现凭据或内部环境标识。

2026-11-08：GitHub 的 [v0.4.1 发布流水线](https://github.com/JourneyBean/tdev/actions/runs/42511212633) 全部通过，云端 CLI 完整竞态集成用时 168.360 秒、真实 tmux 用时 8.052 秒、安装升级测试用时 11.406 秒。四平台签名包共 14 个附件已公开，Release 设为 latest。匿名下载验证完成可信公钥、Ed25519 签名、全部摘要与压缩包内容校验，并在临时目录在线安装 Linux amd64，JSON version 返回 0.4.1。公开流水线日志扫描未发现凭据或内部环境标识。

原生执行环境为 Linux amd64、Go 1.27.3、tmux 3.6；Linux/macOS amd64/arm64 四平台交叉构建通过。macOS 原生 shell、实际 nvm 配置、真实跨文件系统迁移与中断恢复等人工验收限制继续适用，见“尚未完成的环境或人工验收”。

## GitHub CI 与发布验收

2026-11-08：GitHub workflow 通过 actionlint v1.7.12 校验。临时夹具的 14 个用例覆盖稳定标签格式、前导零及注入字符串拒绝、源码版本一致性、缺失及错误签名密钥、调试日志限制、临时密钥清理、GitHub 下载及升级来源，以及上传失败时保持草稿状态；测试密钥均在临时目录生成并清理。

完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 123 秒，包含真实隔离 SSH/tmux；`make check` 通过。原生执行环境为 Linux。上传步骤的本地夹具使用模拟 gh。

首次 GitHub Ubuntu Runner 执行在终端标记断言失败，日志包含长提示符、非 UTF-8 显示和 tmux 重绘控制序列。测试任务改为 Go Bookworm 容器中的独立普通用户，使用 C.UTF-8 与简短提示符；完整真实终端测试继续执行。调整后的 workflow 通过 actionlint v1.7.12，Linux `make check` 与完整竞态集成回归通过，CLI 集成约 122 秒。云端测试、签名构建及公开附件需在目标 workflow 运行中核验。

Bookworm 云端运行通过 SSH、CJK 与连续断线测试后，本机 scratch 用例仍因标记与换行之间的 ANSI 清行序列失败。文本匹配增加 ANSI 规范化，回归覆盖清行序列、Unicode、命令回显拒绝，以及 OSC 标题原始字节保留与缺失拒绝。v0.3.0 补发使用仅替换该测试文件的 Go overlay，原标签生产源码保持不变。

2026-11-08：修复后的 main CI 与 v0.3.0 补发均在 GitHub 成功，补发 CLI 完整竞态集成约 156 秒。v0.3.0 已公开并成为 latest。匿名资产 API 下载的 14 个附件通过独立公钥、Ed25519 签名、manifest、SHA256SUMS 和四平台压缩包内容校验；macOS arm64 与 Linux amd64 的隔离签名安装均返回 0.3.0。常规下载地址验收遇到 GitHub 边缘节点 504，新增公开下载验证 workflow 用于云端在线重验。Linux arm64 与 macOS amd64 产物完成交叉构建及文件校验。

## v0.4.0 版本验收

2026-11-08：默认构建、源码版本与发布说明统一为 0.4.0。版本更新后的完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 全部通过，CLI 集成用时 125.169 秒、真实 tmux 用时 4.891 秒、安装升级测试用时 5.401 秒，覆盖连续断线重连、会话命名兼容、分屏、本机自动转发、状态迁移与两版回滚。`make check`、`make cross`、`make build` 通过；构建产物 JSON version 返回 0.4.0，协议及 schema 均为 1。公开文件与文件名扫描未发现凭据或内部环境标识。

原生执行环境为 Linux amd64，四平台完成交叉构建。macOS 原生交互、跨文件系统迁移、中断恢复等覆盖限制见“尚未完成的环境或人工验收”。

2026-11-08：GitHub 的 [v0.4.0 发布流水线](https://github.com/JourneyBean/tdev/actions/runs/42503800547) 全部通过，云端 CLI 完整竞态集成用时 133.562 秒、真实 tmux 用时 6.046 秒、安装升级测试用时 5.626 秒。四平台签名包共 14 个附件已公开，Release 设为 latest。公开下载验证通过匿名版本下载地址校验可信公钥、Ed25519 签名、全部摘要及压缩包内容，并在临时目录在线安装 Linux amd64，JSON version 返回 0.4.0。公开流水线日志扫描未发现凭据或内部环境标识。

同一提交的首次 main CI 在 `TestConcurrentEnsureSaveAndStop` 中报告 `tmux list-sessions: server exited unexpectedly`；[main CI 的完整重跑](https://github.com/JourneyBean/tdev/actions/runs/42503800626) 通过，发布标签的独立完整测试也通过，保留该偶发失败记录。

## v0.3.0 版本验收

2026-11-08：默认构建、源码及发布说明统一为 0.3.0。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 179 秒，包含真实隔离 SSH/tmux 分屏、保存恢复及诊断回归。`make check`、`make cross`、`make build` 通过；最终二进制 JSON version 返回 0.3.0、pane-layout-v1，协议及 schema 均为 1。四平台二进制格式及架构校验通过，新增内容、文件名与构建路径扫描未发现凭据或内部环境标识。原生执行环境为 Linux；macOS 原生交互、实际 FUSE 挂载及极小终端尺寸下布局恢复待对应环境验证。

## doctor 自然语言诊断验收

2026-11-08：普通 `doctor` 与指定项目输出自然语言结论、影响和处理建议；`--json` 保留结构化数据。单元测试覆盖未启动会话、初始化未完成、可选依赖缺失、离线缓存、客户端覆盖错误、配置读取失败及 JSON 输出分流。真实隔离 SSH/tmux 验证全局及指定项目解释同名未托管会话，JSON 保留原始 warning。列表终端回归检查最终刷新内容及 syncing 提示消失，适配健康服务器省略 synced 尾注的展示。

完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 125 秒；最终定向 doctor、跨服务器列表与离线刷新回归通过。`make check`（go vet、gofmt、diff 与脚本语法）和 `make build` 通过。构建产物以临时配置及私有 tmux socket 验证普通文本和 JSON。原生执行环境为 Linux；本轮验证输出解释，实际 FUSE 挂载及 macOS 原生显示待对应环境验证。

## ls 表格验收

2026-11-08：scratch 列表每个会话只显示一行，移除目录下方的恢复命令提示。表格对齐测试通过；临时配置和独立 tmux socket 的构建产物冒烟验证表头、分隔线及单行 scratch 输出。12 项真实 tmux 测试以 `go test -race -count=1 ./internal/tmux` 通过。`go test -race ./...`、`go vet ./...`、`gofmt -l cmd internal` 和 `git diff --check` 通过，CLI 集成约 125 秒。原生验证环境为 Linux，macOS 显示待实测。

2026-11-06：列表增加独立 FORWARDS 列，以 `监听地址:端口 -> 目标地址:端口` 展示 L 方向，R 使用 `<-`，隐藏映射 ID；多条映射按列向下排列。单元测试验证中文对齐、首条映射同行、后续映射列对齐、双向箭头、IPv6 方括号及 ID 隐藏。真实隔离 SSH/PTY 定向回归通过，覆盖表头、空服务器行、中文名称列宽、缓存先显示与远端放行后刷新、离线缓存及 warning 状态行。构建产物使用临时配置创建本机项目后，实际 ls 输出 SERVER、NAME、TYPE、STATUS、DIR、FORWARDS 六列。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 122 秒，包含真实隔离 SSH/tmux；`make check`、`make build` 与四平台 `make cross` 通过。原生执行环境为 Linux；macOS 原生显示及窄终端下长路径换行待实测，管道保留完整路径。

## pane 分屏验收

2026-11-06：真实私有 tmux socket 测试覆盖横向与纵向嵌套、不同目录（含空格及中文）、不等比例布局、启动命令只执行一次、服务输出保留、手动新增 pane、保存后停止与重建，以及合并为单 pane 后再次保存仍保留命令。缺失 pane 目录时会话保持 missing。旧版服务窗口缺少 pane 身份时拒绝猜测命令归属，补齐原 pane 标记后保存成功。真实隔离 SSH 验证分屏定义读写、ensure/save，以及缺少 pane-layout-v1 的服务端在写入前被能力检查拒绝。

11 份配置使用最终构建产物完成独立隔离冒烟，共 38 个窗口、42 个 pane。核对窗口名称、目录、pane 数量和 save；启动服务替换为无害标记命令，测试前后既有 tmux pane 身份一致。迁移保留原配置及备份，实际服务命令的应用行为未在冒烟中运行。

完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`、`make check`、`make build` 和四平台 `make cross` 通过。原生执行环境为 Linux；macOS 分屏原生交互及极小终端尺寸下的布局恢复待对应环境实测。布局按可用单元格近似恢复；空间不足时保留 incomplete 会话供检查。

## v0.2.1 版本验收

2026-11-06：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 120 秒，包含真实隔离 SSH/tmux 与连续断线重连。`make check`（go vet、gofmt、diff 与脚本语法）、四平台 `make cross`、`make build` 通过；构建产物 JSON version 返回 0.2.1，协议与 schema 均为 1。发布改动与文件名已检查，敏感信息及内部环境标识扫描未发现问题。原生执行环境为 Linux，macOS 仅完成交叉构建。

## GitLab CI 发布验收

连续断线重连用例在 Linux 上启用竞态检测连续通过 5 次，使用真实隔离 SSH/tmux。两次断线都从第 1 次重试开始，并各自验证恢复后的多语言终端输出。基于发布源码的修复通过 `make check` 与完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`，CLI 测试用时约 118 秒。

2026-11-06：固定源码快照完成 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`、`go vet ./...`、四平台 `make cross` 和 `make build`，CLI 集成约 123 秒，包含真实隔离 SSH/tmux。工作区的 `make check`、新增脚本语法与格式检查、diff 检查通过；GitLab YAML 已用仓库现有 YAML 解析器检查，官方 Go CI 镜像标签可解析。

新增 TLS 模拟 GitLab API 验证 14 个产物的上传与 Release 链接、部分上传失败后的续传、同版本摘要冲突保护、已有 Release 保留、鉴权失败、错误信息中的 token 排除及下载重定向移除 job token。篡改二进制、压缩包、清单签名和校验表均在网络请求前被拒绝。签名构建脚本测试覆盖受保护 tag、版本一致性、公钥匹配、调试追踪拒绝、临时密钥清理和原密钥保留。实际生成的安装脚本验证 GitHub 默认来源、GitLab 版本下载及 latest 升级来源、显式覆盖和 URL 引用；不安全来源被拒绝。

尚未在实际 GitLab 项目执行流水线、实例 CI Lint、包上传及 Release 创建；Runner 权限、File 变量、受保护 tag、包存储和匿名下载地址需在目标项目完成首发验收。本轮原生验证环境为 Linux，macOS 产物完成交叉构建。

## scratch 临时会话验收

统一 open 入口（2026-11-06）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成约 123 秒；`make check`、四平台 `make cross` 和 `make build` 通过。帮助参数更新后补测命令边界与 scratch 补全通过。真实本机和隔离 SSH/PTY 验证 `open debug` 与 `open local/debug`、`open dev/debug` 恢复 scratch，远端缓存未收录的新名称通过实时查询找到。远端 scratch 在实际 SSH 断线后恢复，detach 后再次 open 正常；缓存中的已结束 scratch 返回 SESSION_NOT_FOUND。本机裸名称与带服务器前缀的同名项目/scratch 均返回 SESSION_CONFLICT，原 scratch 保持存在，项目会话保持 missing；scratch 配合 --mount 返回参数错误。旧 scratch --attach 参数返回未知参数错误；裸名称和带服务器前缀的 scratch 补全均通过。机器接口的 attach 保留为 SSH 终端传输入口。

完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...` 通过，CLI 集成用时约 117 秒；`make check`（含 go vet、gofmt 与 diff 检查）、四平台 `make cross` 和 `make build` 通过。

2026-11-06：本机和隔离 SSH/PTY 的真实 tmux 用例通过，覆盖指定名称和目录创建、JSON 返回、默认唯一名称、64 字符名称、重名冲突、普通会话名称占用保护、项目归属隔离、按名称恢复、detach 后保留、最后一个 shell 退出后消失，以及失效 generation 拒绝附着。ls 覆盖全部、project/scratch 类型筛选、服务器筛选和远端离线时保留 scratch 缓存及时间戳。

新增真实 PTY 回归确认 tmux 内创建 scratch 后 switch-client 进入新会话，原会话的 session ID、generation 和逻辑名称保持不变；标记命令显式指定新会话目标。远端附着经历实际 SSH 断线后恢复原会话，再 detach、按名称恢复及退出，均通过。原生执行环境为 Linux；macOS 仅交叉构建，原生 tmux 交互待对应环境实测。旧服务端缺少 scratch-v1 时沿用现有能力门禁，scratch_warning 升级提示分支尚无独立旧版本集成用例。

## v0.2.0 版本验收

2026-11-05：默认源码运行与 make build 产物的 JSON version 均返回 0.2.0，协议与 schema 继续为 1。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、`make cross` 和 `make build` 通过；CLI 集成约 109 秒，包含真实隔离 SSH/tmux 和 zsh 目录补全回归。源码、构建默认值、README、安装说明及发布说明已复查。原生执行于 Linux，macOS 的 amd64/arm64 产物完成交叉构建。

## v0.1.0 项目入口与 API 验收

zsh 目录补全回归（2026-11-05）：诊断日志中的 shell_glob_no_match 定位到登录 shell 对未匹配目录通配符的处理。真实隔离 SSH 使用临时测试身份的强制命令将收到的命令交给 zsh，先复现原始 glob 错误，再验证通过 sh -c 执行的补全脚本。空目录、无匹配、~/proje、隐藏目录显式前缀，以及含中文、空格、引号和 shell 元字符的目录均通过；HOME 使用隔离测试目录。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`、`make check`、四平台 `make cross` 与 `make build` 通过，CLI 集成用时约 109 秒，真实 tmux 测试重新执行。已复查命令引用与相邻日志处理。原生执行于 Linux；macOS 完成交叉构建，用户设备上的最终复验待更新客户端后进行。

诊断日志回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...` 通过，CLI 集成用时约 111 秒，包含真实隔离 SSH/tmux。新增真实 SSH/PTY 用例在目录编辑器就绪后关闭连接，验证 Tab 补全显示失败类别与日志位置、日志记录 CONNECTION_FAILED，Ctrl-C 正常退出，输入路径未进入日志。单元测试覆盖默认仅记录失败、--debug 成功记录、JSON 单一响应、0600/0700 权限、1 MiB 轮转及三个备份、并发追加、符号链接拒绝、日志失败保留操作结果、超时原因及进程退出码、原始错误和 stderr 内容排除。改动区域复查后调整命令错误归一化顺序，定向竞态回归确认日志错误码与最终 JSON 响应一致；`make check`、四平台 `make cross` 和 `make build` 通过。本轮原生执行于 Linux；macOS 日志文件与终端交互仅完成交叉构建。实际用户环境中的补全失败原因仍待新日志采集，未知原因按 operation_failed 记录。

ls 缓存展示与 warning 回归（2026-11-05）：真实隔离 SSH/PTY 测试让远端请求等待文件门控，验证标准输入为 /dev/null、标准错误重定向到文件、仅标准输出为终端时，远端完成前即显示缓存及 syncing，放行后显示新增项目及 synced。本机项目刷新后同样保存缓存。真实 tmux 用例验证同名普通会话显示 [unmanaged,warning]，列表只保留一条 doctor 提示，JSON 与全局及指定项目 doctor 保留完整 warning；离线缓存及错误状态分类继续通过。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`、`make check`、`make build` 与四平台 `make cross` 通过，真实 tmux 集成测试已重新执行。原生终端验证执行于 Linux，macOS 原生显示待对应环境实测。

setup 并发向导回归（2026-11-05）：真实隔离 SSH/PTY 验证客户端与服务端共用配置目录时，读取两个无转发项目后完成连接保存；首个项目提示等待输入时，配置锁可由另一调用取得，第二个终端的 setup 同样完成保存。完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、`make build` 与四平台 `make cross` 通过。本轮原生执行于 Linux。覆盖配置锁在连接提交后释放的边界；实际用户环境的远端目录补全失败尚未复现。

tmux 查询边界回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、四平台 `make cross` 和 `make build` 通过。真实隔离 SSH/PTY 验证拒绝会话状态 API 的远端仍可完成 setup、向导创建首个项目及随后的项目刷新；setup 的会话状态为 unknown。同一环境验证 show/create/edit、override、forward add/remove、项目补全和 pull/push；sh --json 完成真实 tmux 窗口创建，省去额外 session status 请求。代码检查确认 ls 与显式 project list --status 保留状态查询，open/sh attach、save/stop 及 attachment 保留对应会话校验；省略项目名时保留当前 tmux 项目标记读取。doctor 与安装检查保留 tmux 程序依赖检查。此次查询边界用例原生执行于 Linux，macOS 原生交互范围见环境验证记录。

传输目标解析回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、四平台 `make cross` 和 `make build` 通过。真实隔离 SSH/SFTP 验证旧版远端的会话状态查询继续返回 SESSION_CONFLICT 时，客户端在无索引缓存的情况下，以裸项目名和完整 server/project 标识完成 pull/push，文件内容一致。裸名称查找跳过前序服务器的 PROJECT_NOT_FOUND，并定位正确目标；解析与传输全程使用项目定义，独立于 tmux 状态。此用例用包装程序模拟旧版状态错误，其余 API 和 SFTP 均实际执行。

会话 warning 回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、四平台 `make cross` 和 `make build` 通过。真实隔离 SSH/tmux 验证本机与远端同名普通会话显示 unmanaged 和 warning，列表返回 0、服务器保持 synced，其他项目继续展示；远端应用错误标记 error 并保留缓存。真实 PTY 验证 open 创建独立托管会话后可操作和 detach，原同名会话保持存在。tmux 回归验证 UUID 识别旧版命名、复用时保留 generation、停止仅影响托管会话；重复 UUID 查询返回 warning，停止操作保留所有会话。本轮原生执行环境为 Linux，macOS 完成交叉构建，原生 tmux 交互待对应环境实测。

终端标题回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`go vet ./...`、格式与 diff 检查、`make cross` 和 `make build` 通过。本机与远端 `open` 的真实 PTY/tmux 用例检查 OSC 2 序列，标题分别为 `tdev: local/demo` 和 `tdev: fixture/demo`。终端模拟器的标题展示及用户 shell/tmux 后续修改标题的交互待人工验证。

旧入口清理回归（2026-11-05）：完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、四平台 `make cross` 和 `make build` 通过。已有 default_server 字段可读取但被丢弃，序列化不再输出；旧字段重复和其他未知字段继续拒绝。真实 SSH/PTY 项目管理验证 setup 新配置无默认服务器字段，补全只注册转发 ID。forward 的旧保存参数返回 INVALID_ARGUMENT，数字项目名按项目解析。最终 bin/tdev 对数字项目运行保存映射与 override，真实 TCP echo 成功，Ctrl-C 后清理，项目定义和覆盖文件内容保持不变。

单文件目录目标回归：真实隔离 SSH 验证 pull 将带中文、空格和引号的远端文件下载到当前目录 ./，保留文件名与内容；已有文件拒绝覆盖返回 130，-y 确认覆盖成功。push 到已有远端目录同样保留文件名。传输单元测试覆盖显式文件名、覆盖计数、并发目标变化、非法 basename 和符号链接拒绝；原目录内容合并行为继续通过。重建后的 bin/tdev 使用实际 darwin-arm64 产物完成同形式 pull 到 ./ 的独立冒烟，文件名、SHA-256 与确认覆盖均通过；完整竞态回归、make check 和四平台构建复验通过。

最终构建产物独立冒烟（2026-11-04）：直接运行 bin/tdev，使用临时配置、临时 SSH 身份、隔离 sshd 和两个私有 tmux socket，全部通过。覆盖本机无需 setup 创建、main 默认窗口、session 项目名、sh --name --save、真实 PTY open/detach、save、show/edit、forward add/remove、UTF-8 文件 push/pull；远端覆盖 setup、create、API、真实 PTY open/detach、命名窗口和文件往返。本机与远端同名项目同时存在时，ls 正确区分位置；stop 后会话 missing。关闭隔离 sshd 后，ls 返回部分失败并保留带时间的远端缓存，本机结果正常。旧 local/project/window 用户入口返回参数错误。测试配置、身份、缓存与会话已清理；本轮未执行真实 FUSE 挂载。

2026-11-04：最终 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check`、`make cross` 与 `make build` 全部通过。CLI 竞态回归用时约 89 秒；四平台为 Linux/macOS 的 amd64/arm64。构建程序 version 返回 0.1.0。

- 本机项目无需 setup：创建 main 窗口、同名 session、sh --name debug --save、列表位置标识、push/pull 内容校验与 stop 通过。真实 PTY 验证本机 open、Ctrl-C 中断前台程序和 detach。
- 真实隔离 SSH 验证 shell 分段解析、写入 stdin 和 shell 特殊字符保持原值、revision 错误码。两项查询合并后实际只接受一次 SSH TCP 连接；单项写入也只接受一次。
- 模拟旧版服务端返回 0.0.3，API gate 拒绝后显示版本及升级指引，后续 project set 未被调用。旧 API 不可用时通过 shell 读取安装凭据，保留原可信公钥并升级到签名候选版本。
- ls 覆盖本机、空远端、有项目远端、--all、--server 与参数错误。真实 PTY 显示缓存和 syncing，随后输出 synced；新增项目刷新出现，离线保留两项目快照和原更新时间，恢复后再次同步。缓存文件权限为 0600。
- 裸名称优先本机；远端名称可从缓存定位，目标离线时实际配置读取失败，保持目标身份。缓存仅用于展示和发现。
- 真实 PTY 创建向导输入完整 localhost:8000，项目保存默认监听 8000，客户端保存 18000 override。edit 复用向导修改窗口名称和启动命令，revision 增加；原共享转发定义保持。
- 普通 open 未调用 SSHFS；--mount 和独立 mount 调用私有模拟后端，验证显式触发和失败路径。此用例未执行真实 FUSE 挂载。
- 本机 TCP 转发完成真实 echo 往返、端口冲突保留原监听、取消后清理。真实 tmux 验证同名未托管会话 warning，原会话保持存在。
- 原有真实 SSH/tmux、连续断线重连、签名安装/升级/回滚、Bash/zsh 补全和 UTF-8 路径编辑回归保留。客户端参数与 API 边界、移除旧用户命令的行为均有验证。

本轮原生执行环境为 Linux。macOS 新版 CLI、原生 tmux 与 SSHFS 挂载交互待对应环境实测；历史 Mac 挂载验证记录见下文。

## v0.0.3 向导与配置验收

- 发布前复验：Go 1.27.3 下完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...`、`make check` 与 `make cross VERSION=0.0.3` 通过，包含真实 SSH/tmux 及连续两次断线重连。此前该断线用例曾出现一次重试耗尽，单独复测与本轮全量复测均通过；保留其时序敏感性记录。

- 补全一键配置：签名安装脚本的 `--completion all`、`curl | sh` 形式下的真实终端确认/拒绝、本机 install 的显式与交互模式；原配置、PATH 区块、权限、符号链接和备份保留，重复运行不重复。含中文、空格、引号和 shell 元字符的二进制路径可加载，bash/zsh 启动验证通过，共享 `.profile` 保持 POSIX 语法。

- Shell 补全：Bash / zsh 脚本语法与加载验证，Bash 实际调用补全函数获得远端项目。覆盖子命令、服务器名、指定服务器的项目、SSH 别名、窗口/转发 ID；新增远端项目即时出现，离线时限时返回空结果。

- 创建服务器选择：未指定目标时先提示，支持序号、名称、非法输入重试和取消；双服务器隔离目录验证配置写入所选目标，原默认服务器保持不变。显式指定服务器跳过选择，JSON 缺少服务器时返回参数错误。
- 补全渲染：中文与 emoji 长名称、五十个候选、窄窗口与窗口缩放保持宽高边界，选中项可见，展示截断保留真实路径；过期查询结果丢弃。

- 向导输入真实 PTY：左右方向键、Home/End、Delete、中文路径编辑；远端目录唯一匹配补全、多候选与方向键选择、隐藏目录显式前缀、普通文件排除。空格、引号和 shell 元字符保持原值。Ctrl-C、外部 SIGTERM 取消后终端设置恢复，open 的创建与 tmux 接管继续通过。

- `tdev create` 顶层入口通过真实 SSH/PTY 验证，旧入口仍可用；`open` 缺失项目时确认并创建后直接进入 tmux，拒绝与 Ctrl-C 取消保留远端原状，JSON 调用拒绝交互。

- SSH 配置发现：Host 多别名、Include 通配文件、引号和等号语法、继承字段、否定模式、循环 Include；确认发现期间不执行 Match exec。
- 真实终端和隔离 SSH/tmux：已登记服务器选择、远端配置目录保留、已有项目读取、本机端口调整、直接地址连接及私有 profile 持久化。
- 首次远端缺少 tdev：向导确认后安装签名包，再读取已有项目，配置文件内容保持不变。
- 实时发现：远端窗口名称改变和新增项目立即出现在下一次 ls；open 的真实 TCP 连接使用本机 override，save 保留远端原监听端口。
- 安全边界：同名新项目 UUID 不匹配、已删除 forward ID、重复监听和非法端口被拒绝；非终端/JSON setup 使用显式参数，避免等待交互输入。
- macOS arm64：正式签名包在隔离 HOME 安装成功，真实终端自动发现测试 SSH 连接；q 退出和 SIGINT 取消均返回 130。其余四平台完成交叉构建，完整竞态测试与静态检查通过。

## v0.0.2 安装验收

- Linux：完整 `go test -race ./...`，包含真实 SSH/tmux 和签名 bootstrap、升级、回滚；`go vet`、格式检查、shell 语法检查和四平台交叉构建通过。
- 正式安装脚本生成后，内置 SHA-256 校验值、公钥和默认发布地址；篡改二进制会在执行前被拒绝。
- Linux amd64、macOS arm64：隔离 HOME 中安装成功，覆盖中文和带空格目录；设置不存在的 OpenSSL 路径仍可使用固定校验值的安装器。
- PATH：bash/zsh 启动文件可找到安装后的命令，重复设置没有重复标记或路径；已有内容、权限、符号链接和备份均有测试。shell 引号测试覆盖空格、UTF-8、引号、美元符和反引号。
- 公开 v0.0.2 分发链路：四个平台压缩包下载均返回 HTTP 200；Linux amd64 与 macOS arm64 使用 `releases/latest/download/install.sh` 完成在线一键安装及 PATH 验证，macOS 另通过在线签名升级和回滚。
- Linux arm64、macOS amd64 完成交叉构建，原生安装仍待对应硬件验证。fish 路径生成已有测试，原生 shell 执行待安装 fish 的环境验证。

## 环境与检查

Linux 构建与测试环境使用 Go 1.27.3、tmux 3.6、OpenSSH 10.2p1。
Mac 客户端为 arm64，SSHFS 3.7.5、macFUSE 5.4.0，bootstrap 使用 OpenSSL 3.6.4。

实际执行并通过：

```sh
TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 180s ./...
go vet ./...
make check
make cross
```

另补充执行 `go test -race -timeout 90s ./internal/update`，HTTPS 下载、下载暂存清理和
禁止 HTTPS 向 HTTP 重定向的新增测试通过。格式、diff 检查及源代码秘密模式扫描无异常。
四平台构建均设置 CGO_ENABLED=0；file 实际确认 Linux amd64/arm64 为静态 ELF，
darwin amd64/arm64 为对应架构 Mach-O。Linux amd64 与 Mac arm64 产物已运行，
另外两种架构为构建与格式验证，未在对应硬件执行。

## 行为验收

| 场景 | 实际结果与范围 |
| --- | --- |
| 配置解码 | 重复 key、未知字段、无效 schema、项目路径穿越、无效 UTF-8 明确拒绝 |
| 并发配置修改 | 12 个相同 revision 的更新只有一个成功；其他返回冲突，旧备份和 0600 权限正确 |
| 并发 ensure | 8 个并发调用只启动一次服务，不重复窗口或启动命令 |
| 未完成初始化 | 保留现场并报错，不重放已启动的命令 |
| 服务退出 | 窗口保留输出与退出状态，再次 ensure 不重启 |
| save | 保留声明命令与转发；排除临时窗口，纳入手工窗口及多 pane 布局 |
| shell | 临时和持久窗口行为、配置保存、会话复用通过真实 tmux 验证 |
| L/R 监听与数据 | 独立 sshd 上两种方向真实 ready 和 echo 数据往返通过 |
| 端口竞争 | 第二个连接失败，原连接继续工作，不抢占或停止已有监听 |
| 转发断线 | TCP 故障代理断开连接后重新建立，数据再次往返；取消后监听清理 |
| 初始重试次数 | attempts=0 仅一次连接；attempts=2 实际建立三次连接尝试 |
| 两次独立失联 | 真实 PTY 两次断线后恢复，健康连接重置预算；快速断线不重置预算另有单元测试 |
| 等待/连接时 Ctrl-C | always 等待和 SSH 握手挂起期间均可取消，退出码 130 |
| 认证与 host key | 身份错误和主机键变化在 always 策略下也明确退出，不无限重试 |
| 会话停止 | 断线期间 stop，恢复后退出码 5，原项目不被自动重建 |
| 终端控制 | Linux PTY 与真实 Mac 客户端 Ctrl-C 中断远端 sleep 后仍可操作，正常 detach 返回 0；PTY resize 后远端尺寸更新 |
| 传输路径与层级 | 真实 SSH push/pull，文件/目录、空目录、空格、引号、方括号和 CJK 文件名往返正确 |
| 覆盖与并发 | 用户拒绝覆盖不写入；无关文件保留；提交前检测目标变化与篡改暂存内容 |
| Mac 别名保护 | NFC/NFD 等价名称、韩文组合形式和大小写别名在传输前检测，避免覆盖 |
| SSHFS 防护 | 单元测试覆盖非空目录、其他来源、FSID 变化、多个挂载歧义和 busy；不会强制卸载 |
| Mac 真实挂载 | 命令返回后 SSHFS 独立进程继续运行；重复 mount 复用同一挂载，释放占用后正常卸载 |
| Mac busy 卸载 | 真实进程 cwd 位于挂载内时拒绝卸载，并报告 PID；拒绝后仍能正常读取文件 |
| Mac 元数据 | .DS_Store/AppleDouble 创建被阻止，com.apple.* 属性被拒绝，远端独立检查未生成附带文件 |
| 发布真实性 | 错误信任公钥、篡改 manifest、篡改二进制、版本不匹配、非法源被拒绝；bootstrap 不执行未验证候选 |
| 安装/升级/回滚 | Linux 原生二进制从 0.1.0-dev 升到 0.1.1-dev 并回退；远端更新不改变运行会话的 generation |
| Mac 无 Go 安装 | PATH 仅包含系统目录，使用显式 OpenSSL 路径完成签名 bootstrap、原地 upgrade 和 rollback |
| 回退保护 | 备份哈希、receipt 和恢复内容正确；拒绝覆盖被外部修改的安装目标 |
| HTTPS | 临时可信 CA 的隔离 TLS 发布源下载成功；降级重定向被拒绝，暂存目录清理 |

真实 SSH 测试使用隔离的临时主机键、身份、sshd 配置和故障代理。
签名测试使用临时 Ed25519 密钥和四平台构建产物。测试密钥、会话、挂载及临时数据均已清理。

## UTF-8 与 CJK

客户端项目管理已通过隔离 sshd 与真实 PTY 验证：零项目 setup 接续创建、独立创建向导、指定服务器列表、重复创建冲突、目录更新、完整定义替换、窗口和双向转发增改移除、过期 revision 拒绝。中文、emoji、空格、引号及 shell 元字符目录可正确处理；本机端口覆盖保持独立，管理过程未启动 tmux 会话。

已实际覆盖中文、日文、韩文和 emoji，包括项目目录、窗口显示名、命令输出、文件名、
文件内容、正常连接和重连后的终端输入输出。项目 ID/name 仍遵循设计的 ASCII slug schema，
不将 Unicode 显示文本混同为内部标识。

测试发现并修复两类问题：

- 无 UTF-8 locale 时 tmux 会把 CJK 和 tab 替换为下划线。机器查询显式使用 tmux -u，SSH
  子进程保留用户 locale，不再通过 LC_ALL=C 影响远端程序。
- Mac libfuse3 的文件名规范化可能造成“能列出但打不开”。当前后端不接受旧版
  norm_insensitive 选项，改用可用的 iconv UTF-8/UTF-8-MAC 模块。Mac 的 CJK 文件读取、
  新建保存和传输往返实测通过；挂载还用独立远端探测设置 utf8_verified=true。

传输不自动改写文件名的 Unicode 字节。Mac 目标对规范化等价或大小写别名保守拒绝。
混合 NFC/NFD 目录及等价重名受 Mac/SSHFS 命名规则限制，需要用户显式处理名称冲突。

## 安装状态目录迁移

安装记录与备份迁入 XDG 状态目录的验证已通过：

- 单元测试覆盖默认状态目录、自定义 XDG_STATE_HOME、相对路径回退、安装目标隔离、重复安装保持文件身份与修改时间。
- 从二进制相邻路径及旧配置备份目录迁移，覆盖安装触发、回滚触发、完整历史收据链及连续两次回滚；损坏备份使迁移停止并保留旧文件。
- 真实 SSH 与签名发布包验证远端独立 XDG 目录、旧路径收据、旧二进制缺少 API 时复用信任公钥、升级及回滚；升级前后 tmux 会话 generation 保持一致。
- `go test -race ./...`、`go vet ./...`、`gofmt -l cmd internal`、`git diff --check`、`make cross` 通过。真实 tmux 的 12 项测试使用独立 socket 和 `/dev/null` 配置通过，覆盖并发、持久 shell、布局、服务退出和会话归属。

## 安装幂等与两版回滚保留

2026-11-08：最终组合实现通过 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 240s ./...`，CLI 集成约 124 秒，包含真实隔离 SSH/tmux；`make check` 和四平台 `make cross` 通过。安装路径保持原值，收据与回滚副本使用 XDG 状态目录。

回归覆盖连续安装五个版本后仅保留前两版、相同内容重复安装保持文件身份及修改时间、下载源更新保留回滚链、缺少收据时登记现有二进制和恢复执行权限、旧路径四份历史备份迁移后裁剪为两版、连续回滚两次及边界拒绝。回滚后清理已恢复版本的备份副本；其他安装目标、无关文件和符号链接保持完整。原生执行环境为 Linux，macOS 完成交叉构建。

## 本机项目回环地址自动转发（2026-11-08）

`open local/<project>` 回归通过：`localhost`、`127.0.0.1`、`::1` 在监听端口已占用时跳过自动转发，保持无转发诊断和运行日志。筛选测试覆盖 localhost 大小写、L/R 方向、混合目标、远端项目保留全部转发以及原始定义保持完整。

`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，CLI 约 127 秒，真实 tmux 3.6 测试约 4 秒，使用独立 socket 和 `/dev/null` 配置。`make check` 与四平台 `make cross` 通过。原生执行环境为 Linux，IPv6 回环跳过规则已验证，实际 IPv6 数据转发与 macOS 原生执行仍沿用下列验收边界。

## 尚未完成的环境或人工验收

- `ps` 已在 Linux、OpenSSH 10.2 下验证真实 master PID、操作分配、独立转发归属和远端汇总。OpenSSH 10.3+ 的实际通道报告目前覆盖格式解析与隐私过滤夹具，原生新版本、macOS、多跳代理、大量并发连接及长时间运行的快照负载待环境实测。SSHFS 仅展示已登记 helper PID，独立传输状态与通道未知。

- 标题提醒已在 Linux tmux 3.6 及 CI 的 tmux 3.3a 验证本机和 SSH/PTY 链路；macOS 切换应用/终端标签页的真实焦点上报、实际标题渲染、真实 agent bell、嵌套 tmux及其他 tmux 版本待实测。同 session 多客户端的焦点竞争未验证，补充状态按最近一次 session 焦点事件记录。已附着客户端在首次开启 server 级 `focus-events` 后需重新附着。

- 状态迁移已在 Linux 本机与隔离 SSH 环境实测；macOS 已完成交叉构建，真实跨文件系统迁移及中断恢复待实测。历史发行版回退后通过新版安装脚本重新升级的完整流程待实测。

- Linux FUSE 挂载待实测；状态机和失败路径已有测试，真实挂载已在 Mac 验证。
- Mac 本机 tmux 会话编排待实测；local 配置、锁、revision、依赖诊断和连接 Linux tmux 已通过。
- OSC 52 对实际剪贴板的影响、鼠标滚轮与拖选、输入法组合输入、Vim 等 GUI 终端体验
  待人工验收；控制序列直通和 PTY 行为已有自动化测试。
- IPv6 转发完成语法/规范化单元验证，真实数据转发实测使用 IPv4 回环地址。
- SSH 诊断未知或本地化场景仍保守失败；未穷举所有 OpenSSH 版本、代理链和 MFA 方式。
- HTTPS 下载已在隔离 TLS 源验证。HTTP 域名映射留待后续版本。

## tmux session 命名（2026-11-08）

新建项目会话使用 `tdev-project-<name>`，名称被无匹配 UUID 的会话占用时回退到 `tdev-project-<UUID>`（UUID 去掉连字符）。scratch 使用 `tdev-scratch-<name>`，省略名称时直接生成 UUID。真实隔离 tmux 回归通过，覆盖普通同名会话共存、占用名称保护与 UUID 回退、旧项目名和旧 UUID 名称及手工改名会话的复用、重复 UUID 保护、scratch 归属与自动命名。

`go vet ./...`、`gofmt -l cmd internal`、`git diff --check`、四平台 `make cross`、`make build` 和 `go test -race ./internal/tmux` 通过。隔离 SSH/PTY 专项重跑中，项目列表与命名冲突、远端 scratch、本机 scratch、项目与 scratch 同名冲突、无需额外 session 状态查询的项目操作通过；本机项目与命名 shell 用例首次在 detach 后等待进程退出超时，单独重跑通过。

此前完整 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 240s ./...` 曾因断线重连用例报告稳定连接未重置重试预算、后续 SSH 连接重置而超时；断线重连专项重跑通过。后续完整复验结果见下节。

## 当前修改发布前复验（2026-11-08）

Go 1.27.3、Linux amd64、tmux 3.6 环境下，`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，CLI 集成用时 123.485 秒，真实 tmux 测试用时 4.149 秒，安装升级测试用时 4.816 秒。测试使用临时配置、独立 tmux socket 和隔离系统 OpenSSH，覆盖连续两次断线重连、本机项目与命名 shell、项目及 scratch 命名、分屏布局、回环地址自动转发、签名升级、安装状态迁移、重复安装及两版回滚保留。

`make check` 通过，包含 `go vet ./...`、`gofmt -l cmd internal`、`git diff --check` 和安装及 GitLab 构建脚本语法检查。`make cross` 完成 Linux/macOS 的 amd64/arm64 四平台构建，Go 构建信息与目标平台一致；Linux amd64 产物的 JSON version 返回 0.3.0，协议及配置 schema 均为 1。本轮保留现有源码版本，发布时需同步版本与发布说明。

本轮完整测试通过，此前断线重连与 detach 退出的偶发失败仍保留为时序敏感性记录。macOS 原生交互、真实跨文件系统状态迁移及中断恢复、历史发行版回退后使用新版安装脚本重新升级等覆盖限制，见“尚未完成的环境或人工验收”。

## 旧备份过期收据修复（2026-11-09）

回归测试覆盖旧相邻目录和配置目录的过期 receipt，分别执行重复安装、新版升级和直接回滚。校验通过的备份仍可恢复，过期 receipt 的链在边界截断，原始备份、receipt 和更早历史字节保持。负向用例覆盖格式错误、错误安装目标、备份哈希损坏及新状态目录中不匹配的 receipt，均拒绝操作并保留原文件。另行验证外部替换二进制后升级，新备份仅保存实际字节，回滚可恢复该快照。

Go 1.27.3、Linux amd64 下 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，真实隔离 SSH/PTY 集成用时 122.604 秒，tmux 用时 4.207 秒，update 用时 7.847 秒。`make check` 和 Linux/macOS amd64/arm64 四平台 `make cross` 通过。测试使用临时安装目标和配置；现有环境与人工验收边界继续适用。

## 交互 shell 启动命令（2026-11-09）

pane 启动普通 tmux shell，通过 literal `send-keys` 和独立 Enter 执行声明命令。隔离 tmux 回归覆盖单窗口与分屏、交互初始化中定义的工具、延迟初始化、引号和 shell 元字符原样传递、命令结束后继续使用 shell。布局测试同时核对实际文件写入及 pane 退出状态，避免把输入回显当作命令完成。

Go 1.27.3、Linux amd64、tmux 3.6 下最终 `TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 300s ./...` 全部通过，隔离 SSH/PTY 集成用时 125.013 秒，tmux 用时 6.856 秒。`make check`、四平台 `make cross` 和 `make build` 通过。验证使用私有配置与 socket；macOS 原生 shell 和用户实际 nvm 配置的人工体验仍待验证。

## SSH 连接复用评估（2026-11-09）

本轮仅增加架构评估和本地 Answer Book，运行时行为保持当前实现。`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过：CLI 120.146 秒、forward 1.919 秒、tmux 5.677 秒。真实 SSH/PTY 与 tmux 使用临时配置及私有 socket；覆盖现有 L/R 转发、监听冲突保护、断线恢复、取消清理及终端重连。`go vet ./...`、`gofmt -l cmd internal`、`git diff --check` 通过。仅文档修改，无平台相关代码变更，未重复交叉构建。

共享单传输尚未实现；并发冷启动的连接计数、共享映射释放、崩溃租约回收、旧代次清理、MaxSessions 耗尽、共享 SSHFS 恢复和大流量交互延迟仍待后续验收。评估依据与方案见 [SSH 连接复用评估](ssh-connection-reuse.md)。

## 本地 daemon 与 open 功能选择（2026-11-09）

`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过：CLI 110.863 秒、daemon 2.316 秒、forward 1.993 秒、tmux 6.458 秒。测试使用临时配置、生成的 SSH 身份、隔离 OpenSSH 和私有 tmux socket。随后修改的逗号列表补全通过专项 race 测试，CLI 1.064 秒。

真实 SSH 专项核对十六个并发获取返回同一控制 socket、仅新增一条 TCP 连接；多个命令 channel 继续复用；转发另外建立连接，重复请求共享原监听。故障注入覆盖控制及转发恢复、后台转发登记的重启恢复。真实 CLI/PTY 覆盖并发冷启动幂等、热连接 completion 零新增 TCP、双窗口使用权、最后一个 shell 退出清理、活跃 shell 与转发跨 daemon restart 恢复、仅 shell 模式、显式后台转发保留、daemon 强制结束后的旧 master 清理与恢复，以及 stop 后 completion 保持 daemon 停止。

挂载受控后端验证多窗口最后释放、已有挂载保留、busy 安全失败及 warning、异常退出留下的临时挂载标记恢复。服务模式支持非交互及 JSON，已有项目仅打开 forward 时不创建 tmux session；未知功能、空列表、参数冲突及 web 在操作前失败。

首轮完整回归发现交互冷连接在网络失败后额外执行认证尝试，已改为先查询热 master、冷连接在调用终端认证一次后交给 daemon。SSHFS 依赖测试原先在 daemon 启动后改变 PATH，现显式 restart 后验证新工具路径。恢复登记表的 race 已通过复制恢复快照修复。以上场景的专项及最终完整 race 回归均通过。

`go vet ./...`、`gofmt -l cmd internal`、`git diff --check` 和 Linux/macOS amd64/arm64 四平台 `make cross` 通过。原生环境为 Linux；真实 FUSE/macOS 下的新 daemon 挂载生命周期、密码/MFA 和 GUI askpass、多跳代理、大量并发终端的 MaxSessions 边界、长时间休眠及传输性能仍待环境验收。SSHFS 在 daemon 与客户端同时异常结束后可能保留挂载，需按现有身份记录安全 unmount；忙碌时保留。web/HTTP 服务尚未实现，当前选择 web 会报告配置错误。

## daemon 与 tmux 标题提醒组合验收（2026-11-09）

合并本地连接监督、open 功能选择与 tmux 标题提醒后，`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过：CLI 112.672 秒、daemon 2.325 秒、真实隔离 tmux 8.397 秒。`make check` 和 Linux/macOS amd64/arm64 四平台 `make cross` 通过。标题提醒研究保留原入口；SSH 连接研究的 journal、快照及预览迁入 `.summary-web/studies/ssh-connections/`，原始记录字节核对一致。各功能的环境与人工验收边界继续适用。

## 升级期间的 SSH 与映射生命周期（2026-11-09）

新增真实签名升级专项，在活跃 SSH/PTY、临时 L 映射和后台 R 映射共存时执行本地与远端升级、回滚，并注入断线。核对原 session ID/generation 保留、已有远端附着继续使用原进程、重连后实际远端进程与安装二进制身份一致、本地 daemon 切换到安装后的可执行文件，以及两种方向的 echo 数据往返。最后使用权释放后，临时映射清理、后台映射继续恢复。提示先于确认，`-y` 和 JSON 模式保留 stderr 提示，stdout 为单个协议响应。

失败路径覆盖签名拒绝、取消、重复相同内容升级、已停止 daemon 保持原状，以及二进制已更新而 daemon 重启失败时返回带安装结果的 PARTIAL_SUCCESS。模拟远端协议不兼容后，终端退出码为 8，提示更新本地客户端及重新 open，能力门禁阻止后续操作，原 tmux 会话与后台转发保留。

双向回归发现断线后旧远端监听占用导致 R 映射永久失败，修复为曾成功建立的 R 映射按原重连预算重试绑定；首次端口冲突的保护继续由真实 L/R 转发测试验证。测试收尾等待远端附着进程完成取消诊断后退出，解决临时目录删除与日志写入的竞争。最终 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 180s ./internal/cli -run '^TestUpgrade' -v` 通过，升级流程 55.46 秒、CLI 包 56.521 秒。

最终 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，含子测试共 240 项，CLI 169.421 秒，双向升级用例 53.95 秒。`make check`、Linux/macOS amd64/arm64 四平台 `make cross` 和 `make build` 通过。fish 未安装，对应 shell 路径子测试跳过。

版本夹具由当前源码分别构建为 0.4.1 和 0.4.2；协议不兼容使用受控程序注入。原生执行环境为 Linux amd64、Go 1.27.3、tmux 3.6，使用临时签名密钥、隔离 OpenSSH 和私有 tmux socket。真实历史发行版迁移、真实 FUSE/macOS 挂载与终端体验仍待环境验收。从缺少自动刷新逻辑的旧客户端首次升级后，需要用新客户端执行一次 `tdev daemon restart`。

## daemon、SSH 与通道快照（2026-11-09）

`tdev ps` 支持本机、已登记远端、`--server` 筛选及 JSON。真实隔离 SSH/PTY 用例验证两个配置根的 daemon、master PID、远端分配记录、活跃终端的项目与客户端 PID；停止本机 daemon 后查询远端保持停止状态，远端离线时返回 9 并保留本机结果。快照只做本地控制探测，测试确认未新增 SSH TCP 连接；CLI 的远端观测使用独立、非交互连接。

daemon 回归验证操作取消后移除登记、跨目录 socket 和任意用途拒绝、旧 daemon 的 PID 与能力提示，以及私有目录枚举、符号链接和开放权限目录跳过。格式夹具验证实际通道类型、数字状态和缓冲字节解析，排除 mux 控制与原始命令描述；旧 OpenSSH 统计不可用保持显式状态。文本渲染用例验证终端控制字符转义。使用短临时运行目录覆盖 Unix socket 路径长度边界，全部会话、身份与配置使用私有临时资源。

最终 `TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过，含子测试 248 项：CLI 172.516 秒、daemon 2.802 秒、真实 tmux 8.927 秒。升级、daemon restart、双向转发与连续断线重连回归一并通过。`make check`、Linux/macOS amd64/arm64 四平台 `make cross` 与 `make build` 通过。构建产物使用临时配置运行 JSON `ps --server local`，无 daemon 时返回空数组、退出 0，配置目录保持为空。fish 未安装，对应 shell 路径子测试跳过。

原生环境为 Linux amd64、Go 1.27.3、tmux 3.6、OpenSSH 10.2；实际通道查询在该 OpenSSH 版本不可用，API、终端及安装引导的 tdev 分配登记已实测。其他环境与实际通道原生验证边界见“尚未完成的环境或人工验收”。

## SSH 扩展通道类型解析（2026-11-10）

回归夹具复现旧解析器将 `session:command`、`session:shell`、`session:subsystem:sftp` 判为 `unrecognized`。修复后混合会话、mux 控制和 TCP 通道的报告可解析，保留基础类型、状态及缓冲字节，过滤 mux 控制、原始描述和自定义子系统参数。

`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过：CLI 176.051 秒、真实隔离 tmux 9.183 秒。`make check` 和 Linux/macOS amd64/arm64 四平台 `make cross` 通过。测试使用临时配置、生成的 SSH 身份和私有 tmux socket。原生 OpenSSH 为 10.2，扩展通道报告使用格式夹具验证；OpenSSH 10.3+ 与 macOS 原生通道查询仍待环境实测。

## 安装后处理与 tmux 配置迁移（2026-11-10）

`TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 300s ./...` 全部通过：CLI 207.250 秒、daemon 3.148 秒、真实 tmux 14.635 秒、updater 8.608 秒。`go vet ./...`、`gofmt -l cmd internal`、`git diff --check`、安装脚本语法检查、四平台 `make cross` 与 `make build` 通过。构建产物返回版本 0.5.0、协议及项目 schema 1，并声明 post-install-v1。

新增签名安装回归验证全新目标路径安装时迁移已有活跃 PTY；报告版本来自目标程序，实时 OSC 标题更新，pane ID、PID 与 session generation 保留。相同内容的 API 重装保持二进制 inode 并补跑迁移。未来 runtime schema 返回 PARTIAL_SUCCESS，目标二进制保留，后续 daemon hook 仍执行；显式 post-install 可重试。现有真实 SSH 升级/回滚回归增加目标 hooks 报告及远端活跃 OSC 标题断言，继续验证旧附着进程、原会话、端口映射恢复、同内容升级与 daemon 停止状态。

真实隔离 tmux 回归验证：旧版重复 hook 去重，用户 hook 和继承动作保留，索引冲突避让，迁移保留焦点及未读 bell 标记；构造 hook 已写入而版本未提交的现场后重试保持幂等，用户占用已登记索引时保留配置并报错。并发重试、两个配置根并发认领同一旧 scratch、其他配置根保护、显式旧 scratch 认领、普通 session 保留及未来 schema 保护通过。无 tmux server 时处理保持无 server 状态，损坏项目配置单独报告失败。updater 回归验证目标提交及安装锁持有期间执行处理，hook 失败后的同内容重试、回滚目标处理及回滚备份保留。

测试使用临时配置和状态根、唯一 tmux socket 与 `/dev/null` 配置；SSH 身份位于临时目录。当前工作区包含既有 SSH 通道解析修改。实现前的版本门控调研协议与原始记录保存在仓库本地 `.summary-web/evidence/runtime-upgrade-*`。

覆盖边界：签名升级夹具的不同程序版本由当前源码注入版本号构建，未来 schema 用较高标记模拟；真实历史发布包缺少 post-install 入口的回滚、后续实际 schema 逆迁移、迁移中进程强杀及不同用户状态根共享同一 tmux server 待进一步验证。macOS 原生终端、其他 tmux 版本的旧 hook 规范化输出、多客户端焦点竞争及首次开启焦点上报后的真实终端交互继续沿用前文边界。旧客户端首次升级通过新版 post-install 补跑；缺少归属的旧 scratch 需显式附着或认领。

## 服务器临时转发（2026-11-10）

`TDEV_REQUIRE_INTEGRATION=1 go test -race -timeout 300s ./...` 全部通过：CLI 209.234 秒、daemon 3.462 秒、真实隔离 tmux 14.051 秒。`make check`、Linux/macOS amd64/arm64 四平台 `make cross` 与 `make build` 通过。最终构建产物的 forward、ls、stop 帮助输出使用临时配置核对，包含 server 参数、foreground 开关和临时转发管理入口。

真实隔离 OpenSSH 与本机 TCP 用例覆盖：无项目时创建临时 L/R 转发、命令返回后持续 echo、普通 ls 与 ls --type forward 展示、批次端口冲突回收已成功的监听且保留原组、daemon restart 后恢复、按名称 stop 及随后 restart 保持已删除状态。前台用例覆盖本机与远端 SSH、超过普通非交互 timeout 后继续运行、Ctrl-C 返回 130、进程强杀后使用权回收与监听关闭；强杀清理以实际端口关闭作为完成条件。专项重复两轮通过。daemon 用例验证恢复时监听失败仍保留后台登记，显式停止后清除登记。

项目配置回归使用 edit --stdin 验证添加、修改目标并保持映射 ID、删除映射及对应客户端 override 清理；真实 SSH 项目管理和拒绝 session status 的远端夹具同步使用 edit 修改映射。参数用例拒绝缺少规则、重复监听、project 路径充当 server 及旧 add/remove 入口。服务器名称补全与既有 override ID 补全回归通过。

覆盖边界：原生执行于 Linux，测试配置、SSH 身份、状态根与 tmux socket 均隔离。macOS 验证为交叉构建，原生网络与终端行为沿用前文待验收范围。临时转发状态来自当前客户端 daemon；前台组随命令或 daemon 结束释放，后台组支持 daemon restart 恢复。
