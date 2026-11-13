# tmux 振铃透传调研

## 结论

应用输出 BEL（`0x07`）后，tmux 原生支持向附着客户端的外层终端发送振铃。tdev 当前已对托管 session 设置 `bell-action any`，并对窗口设置 `monitor-bell on`；`visual-bell` 沿用用户配置。默认 `visual-bell off` 下，本机和 OpenSSH 附着路径均已实测收到独立 BEL。

实现沿用原生提醒机制与用户的 `visual-bell` 配置，标题 `[!]` 表示未读状态。`off` 发送振铃，`both` 同时显示 tmux 消息，`on` 仅显示消息。

v0.7.0 的[客户端标题过滤](tmux-client-title.md) 将标题合成移到客户端，服务端仅传递项目名称与提醒状态；BEL 继续承担即时振铃。连接名称保留在客户端。

这里的透传表示向客户端发送终端振铃信号。实际声音、标签页提示、闪烁或桌面通知由终端程序及其设置决定。BEL 只携带提醒信号；提醒来源仍需查看标题和窗口。

## 证据

2026-11-10 在 Linux、tmux 3.6、`TERM=xterm-256color` 下使用独立临时 socket、`/dev/null` 配置和真实 PTY。分析先移除 OSC 字符串，再统计独立 BEL，避免把标题序列的 BEL 结束符计为振铃。

| 场景 | 外层 PTY 观察 |
| --- | --- |
| 默认配置，后台窗口一次 BEL | 收到 1 次振铃 |
| 后台窗口已有未读标记，再次 BEL | 仍收到 1 次振铃 |
| 当前窗口聚焦或失焦时 BEL | 各收到 1 次振铃 |
| `visual-bell on` / `both` / `off` | 分别收到 0 / 1 / 1 次振铃 |
| `bell-action current` / `other` / `none` | 分别限当前窗口 / 后台窗口 / 全部抑制 |
| `monitor-bell off` | 收到 0 次振铃 |
| 同 session 两个普通附着客户端 | 各收到 1 次振铃 |
| 一次写入连续 10 个 BEL | 每个客户端收到 1 次振铃 |
| 全部客户端断开期间后台 BEL，再附着 | 保留窗口未读标记，收到 0 次补发振铃 |

探针共记录 18 个阶段，其中 17 项预设断言通过；连续 10 个 BEL 的合并行为作为观测记录。通过 Go overlay 为现有 `TestAlertTitlesThroughAttach` 临时增加独立 BEL 断言，本机和隔离 SSH 两个子测试通过，覆盖失焦当前窗口与后台窗口。overlay 保存在本地研究证据目录，产品及正式测试源码保持原状。

## 行为与设计边界

tmux 将 pane 输出中的 BEL 转换为窗口提醒，再依据 session 选项向附着客户端发送终端能力 `bel`。该路径适合提醒语义；连续事件可能合并，应用不能以收到的 BEL 数量作为精确计数。普通多客户端会广播到每个附着终端；tmux control mode 客户端由上游实现排除。

当前标题提醒与振铃互补：振铃通知即时事件，标题保存待查看状态。默认 `bell-action any` 会包含聚焦当前窗口；若希望只在应用失焦时振铃，需要额外设计按客户端焦点过滤的策略。`other` 仅区分 tmux 窗口，终端应用的焦点需另行处理。

SSH 附着链路直接转发终端输出，本轮实测通过。嵌套 tmux 的每一层都需要允许振铃。macOS 原生终端、真实 agent 输出、嵌套 tmux、其他 tmux 版本及不同终端的最终通知表现仍待实测。测试使用人工 `printf` 产生 BEL；应用需实际输出该信号。

## 来源与复现

- [tmux 3.6 官方手册源码](https://github.com/tmux/tmux/blob/3.6/tmux.1)：`bell-action`、`monitor-bell`、`visual-bell`。
- [tmux 3.6 提醒实现](https://github.com/tmux/tmux/blob/3.6/alerts.c)：提醒合并、重复振铃、客户端选择与 `TTYC_BEL` 输出。
- 项目源码：`internal/tmux/runtime.go`、`internal/tmux/alerts.go`、`internal/tmux/attach.go`、`internal/cli/terminal.go`。
- 本地证据：`.summary-web/evidence/tmux-bell-probe.py`、`tmux-bell-3.6-result.json`、`bell-overlay-probe.py`、`bell-overlay-result.log`。

```sh
python3 .summary-web/evidence/tmux-bell-probe.py
python3 .summary-web/evidence/bell-overlay-probe.py
TDEV_REQUIRE_INTEGRATION=1 go test -race -count=1 -timeout 90s \
  -overlay .summary-web/evidence/bell-overlay.json ./internal/cli \
  -run '^TestAlertTitlesThroughAttach$' -v
```
