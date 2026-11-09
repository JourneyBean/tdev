# tmux 原生提醒与终端标题：验证记录

## 问题和协议

验证多个后台窗口的提醒是否分别保留、逐个切换清除，并通过 OSC 标题序列汇总到外层终端。当前窗口提醒允许忽略；agent hook 不在本轮范围。

执行 `python3 .summary-web/evidence/tmux-title-probe.py > .summary-web/evidence/tmux-3.6-result.json`。脚本用 Python 标准库创建真实 PTY 和控制终端，启动 tmux 3.6，独立临时 socket、临时 HOME、`/dev/null` 配置，三个窗口运行 `/bin/sh`。仅为测试合成一个最小子进程环境，不采集调用者环境。清理仅终止该临时 server。

设置 session 的 set-titles=on，set-titles-string=`#{?session_alerts,[!] ,}#S:#W`，status-interval=60，visual-bell=off，bell-action=any；开启 monitor-bell，初始关闭 monitor-activity。focus-events 在 attach 前开启。每个阶段采集 0.5 秒 PTY 输出，初次 attach 采集 1 秒；提取 OSC 0/1/2 标题，查询 session_alerts、窗口标记、客户端 flags。脚本 pass 字段检查 7 项核心状态与标题断言，其余阶段为观测记录。

## 观测

| 阶段 | session_alerts | 新发出的标题 |
| --- | --- | --- |
| 初始，选中 observer | 空 | demo:observer |
| 后台 A 输出 BEL | 0! | [!] demo:observer |
| 后台 B 输出 BEL | 0!,1! | 无，标题未改变 |
| 切到 A | 1! | [!] demo:agent-a |
| 切到 B | 空 | demo:agent-b |
| 当前 B 输出 BEL | 空 | 无 |
| 注入焦点离开 | 空 | 无；focused 标志清除 |
| 失焦时当前 B 输出 BEL | 空 | 无 |
| 注入焦点进入 | 空 | 无；focused 标志恢复 |
| 开启 A 的 activity 监控，切到 observer，A 普通输出 | 0# | demo:observer，然后 [!] demo:observer |
| 切到 A | 空 | demo:agent-a |

核心断言 pass=true。11 个阶段来自一次运行；采集窗口说明本次更新在窗口内发生，未测量精确延迟。

## 解释与候选集成

session_alerts 满足逐窗口清除需求，无需 tdev 自建待查看状态。标题在提醒/窗口变化后发送；内容相同时省略重复发送。索引 0 的提醒实际表示为 `0!`，条件模板能正确识别。

候选方案作用于托管项目和 scratch session，覆盖新建、复用和重连。internal/cli/terminal.go 直接把 SSH 标准输入输出连接到终端，internal/tmux/attach.go 同样直接连接 tmux 子进程；这是传输可行性的代码依据，本轮没有测试远程链路。

session_alerts 包含 bell/activity/silence，语义是原生窗口提醒。开启 monitor-activity 后，普通流式输出也可能触发，无法单独证明 agent 完成。需要在实际提醒出现时检查 window_bell_flag/activity_flag，确定用户环境的信号。仅同步 bell 的方案需要选定兼容版本及聚合格式。

tmux 3.6 的 alerts.c 对后台窗口或无附着客户端的 session 保存 bell 标记，focused 状态不参与该分支。当前窗口失焦 bell 可考虑用 tmux 的 alert-bell hook 配合额外状态和焦点清除规则扩展；这是尚未验证的未来方案，无需 agent hook。

## 来源

- [tmux 3.6 alerts.c](https://github.com/tmux/tmux/blob/3.6/alerts.c)：alerts_check_bell、alerts_check_activity。
- [tmux 3.6 server-client.c](https://github.com/tmux/tmux/blob/3.6/server-client.c)：server_client_check_redraw、server_client_set_title。
- [tmux 3.6 手册](https://github.com/tmux/tmux/blob/3.6/tmux.1)：set-titles、set-titles-string、session_alerts、focus-events、alert-bell。
- 本仓库 internal/cli/terminal.go、internal/tmux/attach.go。

## 覆盖限制和后续

未测试真实 Codex/Claude 输出、外层终端实际渲染、SSH 全链路、嵌套 tmux、多客户端、macOS 或其他 tmux 版本。焦点通过 PTY 注入事件，仅验证 tmux 接收与状态更新。下一步在用户终端确认原生提醒类型，并决定同步全部提醒还是仅 bell。实现后补 tdev 本机/SSH attach 与 scratch 回归。

发布工具仅支持单页 Markdown 预览。本次保持短概览与独立证据文件，预览的细节页导航尚不可用，未改动渲染器。

## 后续工程验证

原生调研之后，tdev 已实现 session 标题配置与 tmux hook：失焦当前窗口 bell 保存窗口补充标记，聚焦或切换到窗口时清除对应标记，重新附着保留补充标记。原生提醒与其他窗口补充标记继续汇总。agent hook 与外部脚本未引入。

新增 internal/tmux/alerts_test.go 与 internal/cli/alerts_integration_test.go，覆盖真实 tmux、旧会话本机/SSH 附着、输入焦点序列与输出 OSC 标题。完整竞态测试通过，最终复验 CLI 122.227 秒、tmux 8.382 秒；静态检查与四平台交叉构建通过。开发中发现空 hook 数组的序列化与测试缓冲区方法提升问题，已修正并通过完整竞态测试。原始 11 阶段记录继续保存原生 tmux 的基线行为。

真实 macOS 焦点上报/标题显示、真实 agent bell、嵌套 tmux、多客户端焦点竞争和其他版本待实测。focus-events 是 server 级设置，其余标题和 hook 配置限定在托管 session。详情见 docs/development.md 和 docs/acceptance.md。
