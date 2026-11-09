# 同一远程目标的 tdev 操作能否共享常驻 SSH 连接？

## 当前答案

可以。已实现本地 daemon 管理连接，API、completion 与终端共享控制 master；forward、scp 与 SSHFS 保持独立传输。用户选择默认 open 的附属资源随 shell 退出释放，显式服务模式后台保留。

## 证据范围与限制

最初的源码评估已转为实现，行为与协议依据见 docs/ssh-connection-reuse.md。真实 SSH 连接计数、双窗口退出、重启和异常退出恢复均有专项测试；完整验证记录见 docs/acceptance.md。没有延迟或吞吐基准，真实 FUSE/macOS 新生命周期仍待环境验收。

## 评估与实现路径

```text
常驻连接是否可行？→ 已实现本地监督与分功能连接
  ├─ 初始源码审计：原实现按操作隔离 → docs/acceptance.md
  ├─ 官方协议核对：动态转发和后台保留可用 → docs/ssh-connection-reuse.md
  ├─ 实现与隔离 SSH 测试：共享控制连接和资源使用权 → docs/acceptance.md
  └─ 后续环境验收：真实 FUSE/macOS、规模与性能 → docs/acceptance.md
```

## 下一步

按验收边界验证真实挂载与大量并发终端；后续 HTTP 服务可接入已有 daemon，web 名称当前提前报错。

## 发布形式

现有 notebook 渲染器仅生成单页预览，详情保留在独立仓库文档中；预览中的本地文档引用显示为文本。未修改渲染器，未启动预览服务或外部发布。
