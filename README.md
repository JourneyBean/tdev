# tdev

**一条命令，回到你的开发现场。**

终端、分屏、服务和端口映射，跟着项目一起打开。断开 SSH 后，tmux 中的任务继续运行；
回来时，`tdev open dev/demo` 接着做。

tdev 用 Go 编写，基于系统 OpenSSH 和 tmux，将本机与远端的开发工作按项目组织起来。
适合经常在多个项目、终端和开发服务器之间切换的人。

- **按项目恢复工作**：保存窗口、分屏布局、目录和启动命令，新建会话时恢复；已有会话直接接着用。
- **打开项目，也连上服务**：启动项目预设的端口转发，按需挂载文件；已声明的 Web 服务默认可通过 `*.tdev.localhost` 访问。
- **换台客户端，沿用项目配置**：项目定义保存在项目所在机器，客户端单独保存端口和挂载偏好。
- **沿用熟悉的工具**：复用现有 SSH 配置和身份，远端只需 tdev、tmux 与兼容 shell。
- **人和 AI 都能操作**：交互向导和命令补全方便日常使用，JSON 接口与内置 skill 供脚本和 AI agent 调用。

[快速开始](#快速开始) · [使用指南](docs/usage.md) · [配置指南](docs/configuration.md) · [下载版本](https://github.com/JourneyBean/tdev/releases)

## 安装

支持 macOS / Linux，提供 amd64 / arm64 二进制，运行无需 Go。

```sh
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh
```

安装器校验下载内容，默认安装到 `~/.local/bin/tdev`，配置 PATH，并询问是否启用
bash/zsh 补全。打开新终端，或执行安装器输出的 PATH 命令后即可使用。
本机管理会话需要 tmux；纯远端客户端使用系统 SSH，远端需要 tdev 和 tmux。

手动安装、签名校验与离线安装见[安装指南](docs/installation.md)。

## 快速开始

先连接开发服务器，再创建并打开项目：

```sh
tdev setup                       # 选择已有 SSH 连接，或填写服务器地址
# 以下假设将连接命名为 dev
tdev create dev/demo              # 配置项目目录、启动窗口和端口
tdev open dev/demo                # 进入项目，启动已配置的转发和 Web 入口
```

`setup` 会检查连接和远端依赖；远端缺少 tdev 时，可以确认安装官方签名版本。
项目目录不存在时，创建向导会询问是否创建。

也可以先在本机试用，`local` 无需配置连接：

```sh
mkdir -p ~/projects/demo
tdev create local/demo --directory ~/projects/demo
tdev open local/demo
```

在 tmux 中按 `Ctrl-b d` 分离会话，任务继续运行；再次 `tdev open dev/demo` 即可返回。
`tdev stop dev/demo` 会结束项目会话及其中的进程，启动配置保留。

## 日常使用

```sh
tdev ls                          # 查看已启动工作项及 TCP/Web 映射
tdev session ls                  # 查看项目与 scratch 会话及客户端数
tdev project ls                  # 查看全部项目配置
tdev connection ls               # 查看连接、PID 和使用资源（也可用 ps）
tdev forward ls                  # 查看 TCP 映射
tdev web ls                      # 查看 Web 映射和状态
tdev show dev/demo               # 查看项目配置及本机覆盖结果
tdev edit dev/demo               # 修改目录、窗口与端口映射
tdev sh dev/demo --name logs     # 新开一个项目窗口
tdev save dev/demo               # 保存当前窗口与分屏布局
tdev scratch dev --name debug    # 创建临时会话，之后用 open dev/debug 返回
tdev doctor dev/demo             # 检查依赖、会话与配置
```

项目目录、启动窗口和端口也可以直接用 YAML 声明：

```yaml
demo:
  dir: ~/projects/demo
  windows:
    - name: editor
      command: nvim .
    - name: web
      command: npm run dev
  ports:
    - "3000:127.0.0.1:3000"
```

配置保存在项目所在机器的 `~/.config/tdev`，支持单文件、多项目及 include 拆分。
手动分屏后运行 `save` 保存布局；更新后的启动配置在新建会话时生效。
完整格式与客户端覆盖设置见[配置指南](docs/configuration.md)。

## 把服务和文件带到本机

临时访问远端服务：

```sh
tdev forward dev -L 8080:localhost:8000  # 本机 8080 → 远端 8000，后台保留
tdev forward ls                        # 查看项目映射、临时转发及其名称
tdev stop dev/forward-NAME              # 使用列表中的名称停止
```

项目的固定转发通过 `edit` 保存，`open` 时启动。只需要服务时，可以使用
`tdev open dev/demo --only forward`。转发默认监听回环地址。

运行 `tdev web setup` 配置客户端代理或项目 Web 路由。`hostname: demo` 默认可通过 `http://demo.tdev.localhost:24880` 或 `https://demo.tdev.localhost:24881` 访问，可在客户端修改域名后缀。
Web 入口独立于终端持续运行，可用 `tdev web ls` 查看、`tdev web stop dev/demo` 停止。
配置方式与 WebSocket 支持见[Web 服务转发](docs/web-services.md)。

```sh
tdev push dev/demo ./source ./destination  # 上传，相对远端路径以项目目录为基准
tdev pull dev/demo ./result ./result       # 下载
tdev open dev/demo --mount                 # 打开项目并挂载文件
tdev unmount dev/demo                      # 卸载项目挂载
```

覆盖已有文件时需要确认。挂载需要客户端安装 SSHFS，macOS 另需 macFUSE。
传输规则、资源释放及平台限制见[使用指南](docs/usage.md#传输与挂载)。

## 补全、AI 与升级

```sh
tdev completion install           # 配置当前 shell 的补全
tdev skill install                # 检测并逐项确认安装 AI skill
tdev upgrade                      # 验证签名并升级
tdev upgrade --remote dev         # 升级已登记的远端
tdev upgrade --rollback           # 回退到保留的旧版本
```

内置 skill 支持 Codex、Claude Code 和 Pi，安装前逐项确认。
自动化可使用 `--json` 获取结构化响应；目标机器接口见[机器接口](docs/api.md)。
安装、升级及回滚的兼容要求见[安装指南](docs/installation.md)。

## 文档

| 想了解什么 | 文档 |
| --- | --- |
| 项目、会话、转发、文件与诊断命令 | [使用指南](docs/usage.md) |
| YAML、分屏、客户端覆盖与重连偏好 | [配置指南](docs/configuration.md) |
| 安装、补全、AI skill、升级与回滚 | [安装指南](docs/installation.md) |
| 本地域名与远端 Web 服务 | [Web 服务转发](docs/web-services.md) |
| 后台连接、资源生命周期与恢复 | [连接管理](docs/ssh-connection-reuse.md) |
| 终端标题与未读提醒 | [终端提醒](docs/tmux-client-title.md) |
| 设计、实现取舍与验证范围 | [设计](docs/design.md) · [开发记录](docs/development.md) · [验收记录](docs/acceptance.md) |
| 版本变更与签名发布 | [发布说明](docs/releases/) · [GitHub](docs/github-release.md) · [GitLab](docs/gitlab-release.md) |

## 参与开发

需要 Go 1.27；真实集成测试需要 tmux、OpenSSH 等依赖，测试使用隔离配置与临时身份。

```sh
make build                              # 生成 bin/tdev
TDEV_REQUIRE_INTEGRATION=1 make test     # 竞态测试，要求集成依赖齐全
make check                              # 静态检查和格式检查
make cross                              # 构建四平台开发二进制
```

开发构建自动包含 Git revision 和工作区状态；签名发布使用指定版本。
开发约束见 [AGENTS.md](AGENTS.md)，实现与验证记录见 [docs/](docs/development.md)。
