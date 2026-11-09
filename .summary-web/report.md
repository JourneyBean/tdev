# tmux 原生提醒能否汇总到外层终端标题？

可以。Linux tmux 3.6 的隔离真实 PTY 验证表明，session_alerts 能保留多个窗口提醒，并在逐个切换时分别清除；最后一个提醒清除后，终端收到去掉 `[!]` 的标题序列。原生能力已接入 tdev，并增加当前窗口失焦 bell 的补充标记；产品验证记录见 `docs/acceptance.md`。

## 原生方案与已实现扩展

对托管 session 设置：

```sh
tmux set-option set-titles on
tmux set-option set-titles-string '#{?session_alerts,[!] ,}#S:#W'
```

标记代表整个 session 的原生窗口提醒，包括 bell、activity 和 silence。当前窗口失焦时收到 bell，由 tmux hook 记录补充标记；重新聚焦仅清除当前窗口标记，其他窗口提醒保留。agent hook 不在 MVP 范围。

## 调研步骤与结论

```text
问题：逐窗口清除能否只靠 tmux？
  ├─ 原生聚合与 OSC 验证 → A/B 分别保留，切换逐个清除：通过
  ├─ 焦点事件验证 → 可更新 focused 标志；当前窗口失焦 bell 仍不留标记
  └─ activity 对照 → 普通输出也会提醒，需明确标记语义
```

三个步骤的协议、观测和来源见[验证记录](evidence/findings.md)。原始记录为[11 阶段结果](evidence/tmux-3.6-result.json)，复现脚本为[隔离 PTY 协议](evidence/tmux-title-probe.py)。当前发布工具仅提供单页预览；证据文件独立保存，预览不提供细节页导航。

## 证据边界

本次捕获真实标题控制序列。刷新间隔设置为 60 秒，各后续阶段在 0.5 秒采集窗口内得到事件更新；7 项核心断言通过。后续工程测试已通过本机和真实隔离 SSH/PTY 全链路；真实终端渲染、真实 agent 信号、嵌套 tmux、多客户端焦点竞争和其他版本仍待验证。

实现选择同步全部原生提醒，并用 tmux 自身 hook 补充失焦 bell。下一步在实际 macOS 终端验证应用切换、标签页切换和标题渲染。

## 相关研究

[本地 daemon 与 SSH 连接管理](studies/ssh-connections/report.md) 的原始 journal、快照和独立 HTML 预览保存在 studies/ssh-connections/。档案中的旧源路径保留原记录含义。产品行为及验收见 docs/ssh-connection-reuse.md 和 docs/acceptance.md。
