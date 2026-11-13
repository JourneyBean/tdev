# 终端标题与 tmux 提醒状态

## 客户端维护完整标题

交互附着期间，tdev 客户端生成 `tdev: server/project`；任一窗口有待查看提醒时，标题变为 `[!] tdev: server/project`。逐个查看窗口后清除相应提醒，最后一个提醒清除时恢复普通标题。本机、OpenSSH、scratch 与 sh 附着共用该规则。

连接名称只保存在客户端。服务端通过标题 OSC 传递项目或 scratch 的逻辑名称与提醒状态，客户端替换为完整标题。同一远端 session 的两个客户端可以使用各自的连接名称。托管 session 切换时，标题中的项目部分随服务端逻辑名称更新。

## 提醒状态协议

运行时标题模板输出 `tdev-alert-v1:<project>:<marks>`。每个有原生 bell、activity、silence 或 `@tdev_unfocused_bell` 标志的窗口贡献一个 `1`；同一窗口的多种标志合并计一次。客户端按标记数量取得待查看窗口数，界面继续用 `[!]` 表示有提醒。

原生提醒按 tmux 的窗口访问规则清除。当前窗口失焦时的 bell 使用现有补充标记，恢复焦点仅清除当前窗口的补充状态。重新附着读取当前状态快照。独立 BEL 继续透传给终端，承担即时振铃；连续 BEL 可能合并，已读清除与断线恢复依赖状态快照。

## 标题过滤

客户端拦截 OSC 0、1、2，丢弃普通程序或远端标题模板对外层标题的写入；有效 tdev 状态标记用于更新客户端标题。tmux 内部 `pane_title` 保留普通程序的标题语义。标题过滤支持 BEL、ST、C1 形式和跨读块序列，UTF-8 连续字节按文本处理。

对于 tmux DCS 透传和 screen 形式的透传包裹，过滤器检查内部序列并去掉标题写入。嵌套 tmux 包裹递归处理，保留转义和其他指令；包裹内的状态标记也按程序标题丢弃。OSC 8 超链接、OSC 52 剪贴板、普通图形 DCS 和字符串外的 BEL 保持透传。

待识别的标题或透传字符串缓冲上限为 1 MiB，透传递归上限为 8 层。超过上限的对应字符串丢弃至结束符，防止不完整控制字节进入终端。其他已识别的非标题字符串按流转发；异常中断通过取消序列恢复解析，重连时丢弃上次未结束的缓冲。

## 本机与 SSH 输出路径

本机 tmux 直接写附着 TTY，客户端为其分配独立 PTY，中继输入、输出和窗口大小。调用方终端在附着期间使用 raw 模式，Ctrl-C 发送给 pane；附着结束或失败后停止中继并恢复终端状态。API 附着继续提供原始终端流，由调用侧客户端负责过滤。

远端路径保留系统 OpenSSH，客户端在接收 SSH 输出时执行同一过滤。重新连接建立新的解析状态，附着快照恢复提醒。输出重定向时不生成客户端标题序列，仍移除服务端标题。

## 迁移与兼容

tmux runtime schema 为 2。创建、附着与 post-install 将旧模板迁移到状态协议，保留 session generation、pane、进程、未读标志和用户 hook。迁移已有附着会话时，报告 `reattach_required`：更新客户端后重新附着，加载客户端过滤器。高于支持值的运行配置保持原状并返回兼容错误。

该状态模板供 tdev 客户端消费。直接用原生 tmux 附着会显示状态标记；新版客户端连接旧服务端时保留本地标题，动态提醒需要将服务端更新到支持该模板的版本。

v0.6.0 只支持 runtime schema 1。回滚后，旧程序对已迁移会话的 post-install 返回部分成功，重新附着返回兼容错误，原会话和进程保留。恢复使用 v0.7.0 或支持 schema 2 的兼容版本，再执行 post-install 和 open。自动逆迁移尚未实现。

## 验证与边界

真实隔离 tmux/PTY 与 OpenSSH 集成覆盖标题覆盖拒绝、直接及嵌套 DCS、OSC 8/52 与 BEL 保留、焦点、已读清除、重新附着、窗口缩放、Ctrl-C、双连接名称和 tmux 内 scratch 切换。解析器测试覆盖分块、UTF-8/C1、异常序列、缓冲上限和连接重置；PTY 测试覆盖正常退出、取消、启动失败、输出失败与终端恢复。验证结果及平台边界见 [验收记录](acceptance.md)。

原生运行环境为 Linux；macOS 原生终端展示与完整嵌套 tmux 的最终表现仍需人工验证。DCS 字节过滤的测试与完整嵌套终端会话的验收分别记录。

## 依据

- [tmux 3.6 手册](https://github.com/tmux/tmux/blob/3.6/tmux.1)：提醒格式、标题、透传及 control mode。
- [server-client.c](https://github.com/tmux/tmux/blob/3.6/server-client.c)：标题模板展开与变化时更新。
- [client.c](https://github.com/tmux/tmux/blob/3.6/client.c) 与 [tty.c](https://github.com/tmux/tmux/blob/3.6/tty.c)：终端描述符与输出路径。
- [振铃透传记录](tmux-bell-forwarding.md)：BEL 条件、合并及断线语义。

早期可行性协议与原始观测保存在仓库本地 `.summary-web/evidence/`。control mode 订阅实验确认可取得变化状态，每秒最多一次更新；交互实现复用现有终端输出链路传递状态。
