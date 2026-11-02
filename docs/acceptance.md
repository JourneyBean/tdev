# 第一版开发验收记录

Last verified: 2026-11-02。发布目标版本：0.0.1。下表保留集成验收使用的开发版版本号。

本记录汇总第一版核心功能的自动化验证、环境实测和待验证项。

## v0.0.2 安装验收

- Linux：完整 `go test -race ./...`，包含真实 SSH/tmux 和签名 bootstrap、升级、回滚；`go vet`、格式检查、shell 语法检查和四平台交叉构建通过。
- 正式安装脚本生成后，内置 SHA-256 校验值、公钥和默认发布地址；篡改二进制会在执行前被拒绝。
- Linux amd64、macOS arm64：隔离 HOME 中安装成功，覆盖中文和带空格目录；设置不存在的 OpenSSL 路径仍可使用固定校验值的安装器。
- PATH：bash/zsh 启动文件可找到安装后的命令，重复设置没有重复标记或路径；已有内容、权限、符号链接和备份均有测试。shell 引号测试覆盖空格、UTF-8、引号、美元符和反引号。
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
| save | 保留声明命令与转发；排除临时窗口，纳入手工单 pane 窗口，多 pane 拒绝落盘 |
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

## 尚未完成的环境或人工验收

- Linux FUSE 挂载待实测；状态机和失败路径已有测试，真实挂载已在 Mac 验证。
- Mac 本机 tmux 会话编排待实测；local 配置、锁、revision、依赖诊断和连接 Linux tmux 已通过。
- OSC 52 对实际剪贴板的影响、鼠标滚轮与拖选、输入法组合输入、Vim 等 GUI 终端体验
  待人工验收；控制序列直通和 PTY 行为已有自动化测试。
- IPv6 转发完成语法/规范化单元验证，真实数据转发实测使用 IPv4 回环地址。
- SSH 诊断未知或本地化场景仍保守失败；未穷举所有 OpenSSH 版本、代理链和 MFA 方式。
- HTTPS 下载已在隔离 TLS 源验证。HTTP 域名映射留待后续版本。
