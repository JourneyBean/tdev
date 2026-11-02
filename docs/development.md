# 开发记录与实现取舍

Last verified: 2026-11-02。版本：0.0.1。历史集成验收使用开发版版本号，见验收记录。

源码发布于 [GitHub](https://github.com/JourneyBean/tdev) 和
[GitLab](https://git.mewwoof.cn/JourneyBean/tdev)。

## 已实现的第一版范围

项目配置、窗口与保存转发定义、tmux 会话、setup、ls、open、save、sh、stop、
forward、push/pull、mount/unmount、doctor、install/upgrade/rollback 已提供实际实现。
远端通过按需启动的 CLI/attach 进程管理配置与会话。
HTTP 域名代理仍属于后续版本。

配置使用严格 YAML/JSON 解码，拒绝重复 key、未知字段和不支持的版本。
修改持有内核文件锁，比较 revision，保留历史版本后原子写入；配置默认 0600。
local 接口无确认交互，--json 返回单一协议响应；客户端按 error.code 区分远端错误和连接错误。
协议握手同时检查 schema 和所需 capability，不以程序补丁版本完全相同作为前提。

## 转发所有权与就绪

每条 L/R 映射单独建立 SSH master，控制 socket 位于用户私有临时目录。
建立 master 后，通过 `ssh -O forward` 获取监听建立的确认；控制请求使用独立的空 SSH
config，避免把用户 config 中保存的其他转发也提交给本次 master。
保留 SSH alias 的连接、认证和 ProxyJump 等能力；不接管用户已有 ControlMaster。

映射分别监督并使用 forward 重连策略。冲突不会杀死其他监听，保存定义与实际连接状态
分别报告。退出时取消自己的映射并关闭自己的连接，随后删除控制 socket。
open 期间的事件写私有 JSONL 日志，交互终端不接收后台转发提示。
ready 表示 SSH 监听已建立；应用健康由应用自身检查。

## 终端重连

全局、服务器覆盖和 CLI 覆盖依次生效。attempts 支持 0、非负整数及 always；N 表示
初始尝试之外追加 N 次。等待和连接尝试受取消 context 控制，Ctrl-C 返回 130。
交互 SSH 直接继承终端文件描述符，键盘 Ctrl-C 在 raw 模式下作为远端输入传递。
退出或断线后恢复终端状态。

重连始终查询原会话的 session ID 与随机 generation；缺失或被替换时结束，不再次 ensure。
attach 的就绪检查依据远端 tmux 实际 client PID，避免仅因探测成功就重置预算。
连接稳定至少 max(重试间隔, 1s) 后才重置本次失联计数，防止快速连接/断开空转。
重连使用批模式，认证和 host key 问题不会持续弹交互提示。

连接故障结合连接阶段和 SSH 诊断分类；首次失败使用诊断白名单识别可重试故障，未知
原因保守退出。控制连接使用 INFO 日志级别，保留故障诊断和用户 locale。

## 传输一致性

采用系统 scp/SFTP，暂存文件以编号命名，避免把用户文件名交给远端 shell 或 glob 解释。
目录内容合并进精确目标目录，保留空目录，不删除无关文件，不随目标存在与否改变层级。
覆盖前展示目标；提交前检查快照和 SHA-256，再逐文件原子替换。
目录按文件提交，部分失败时保留已提交文件并报告进度。

符号链接和特殊文件在第一版明确拒绝。UTF-8 字节保持原样，不自动重命名或统一 Unicode
形式。Mac 目标在写入前保守拒绝大小写和规范化等价名称，以免发生别名覆盖。
断线可能使远端暂存目录未能清理，错误会给出 transfer ID；可通过 local transfer abort 清理
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

setup 支持既有 alias，或使用现有 identity/agent 创建私有 SSH profile；配置保存在客户端，
Include 既有 SSH config。新建密钥、公钥分发和交互 MFA 引导没有封装为自动流程。
安装/传输需可批模式认证的身份；缺少依赖时明确失败。当前 Release 提供源码归档，
签名二进制安装包待配置发布密钥后提供。

## 验证与回退

实际结果和待验证项见[验收记录](acceptance.md)。构建使用 Go 1.27.3，依赖记录于
go.mod/go.sum。运行产物无需 Go。

配置回退使用对应 `.yaml.rev-N.bak`，读取当前 revision 后显式导入。
二进制回退使用 receipt 中带哈希的备份及 upgrade --rollback，外部修改会被拒绝覆盖。
