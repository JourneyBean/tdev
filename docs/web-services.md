# Web 服务转发

项目的 `http` 声明将域名映射到项目所在机器的 HTTP 服务。本机代理使用固定端口，
远端服务通过系统 OpenSSH 的独立 TCP 隧道访问。本机项目直接访问目标服务。

在项目所在机器的 `config.yaml` 或项目片段中声明：

```yaml
demo:
  dir: /home/developer/projects/demo
  http:
    - name: web
      hostname: demo.localhost
      target_host: localhost
      target_port: 5173
      autostart: true
    - name: api
      hostname: api.demo.localhost
      target_host: localhost
      target_port: 8080
      autostart: false
```

`name` 使用项目名的字符规则，在项目内唯一。`hostname` 为小写 DNS 域名，必须以
`.localhost` 结尾，各标签使用字母、数字和连字符。每个域名在当前客户端 daemon
中唯一。`target_host` 和 `target_port` 为必填项，目标端口范围为 1–65535。
`autostart` 为可选布尔值，省略时默认为 `true`；`false` 保留手动打开入口。
应用的启动由项目窗口命令或其他现有方式管理。远端项目包含 `http` 声明时，
本机和远端都需要支持该字段的 tdev 版本；已有旧 daemon 需运行 `tdev daemon restart`。

客户端可以在自己的 `config.yaml` 中选择代理端口：

```yaml
client:
  http:
    port: 17890
  reconnect:
    http: {attempts: always, interval: 5s}
```

默认端口为 17890，监听地址为 `127.0.0.1`。支持 `client.include`，HTTP 重连策略
沿用服务器覆盖优先于全局配置的规则。代理活动期间修改端口，需要先停止当前所有 Web
路由再重新打开。端口冲突返回 `INVALID_CONFIG`，保留原监听。

```sh
tdev open dev/demo --only web
# 浏览器访问 http://demo.localhost:17890
tdev web ls
tdev web stop dev/demo
```

daemon 每次启动时，在后台读取本机以及客户端 `servers` 中所有已配置服务器的项目，
自动启用 `autostart: true` 或省略该字段的 Web 服务，包含尚未 open 的项目。
服务器分别发现，慢网络与离线服务器保持其他服务可用；临时传输失败按该服务器的
HTTP 重连策略重试，认证、主机密钥和配置错误通过 `daemon status` 的 warnings 报告。
发现成功后清除该服务器的临时故障提示。项目发现使用非交互的共享 OpenSSH 控制连接。

自动发现每次 daemon 启动执行一次，成功后由隧道自身负责断线重连。新增项目、修改
`autostart` 或改变服务器列表后，运行 `tdev daemon restart` 重新读取配置。
显式 `daemon start`、`restart` 以及其他命令按需拉起 daemon 都会触发自动启动；
电脑登录时自动拉起 daemon 由操作系统启动配置单独管理。

默认 `open` 启动 shell、TCP 转发和项目已声明的 Web 服务；`--only shell` 可只打开
终端。`--only web` 启动后返回，支持 `--json`，保持现有 tmux 会话状态。
这些选项控制本次 open 请求；daemon 启动时的自动策略独立执行。
返回的每条路由包含 URL、`connecting` / `ready` / `waiting` / `failed` 状态及连接错误。
`ready` 表示隧道已建立；应用自身的可用性由 HTTP 响应反映。本机路由直接报告 ready。

Web 入口独立于终端使用权，终端退出和 detach 后持续运行。`web stop server/project`
移除该项目全部路由、隧道和恢复登记，关闭其活动连接，包括 WebSocket；其他项目保留。
手动停止也抑制本轮尚未完成的发现任务；下次 daemon 启动重新按项目配置判断。
最后一个路由停止时释放代理监听端口。目标地址、域名、连接偏好或重连策略变化后，先停止旧
路由，再 open 以使用新定义。相同定义的重复 open 复用路由，手动 open 可向自动启动
的项目补齐 `autostart: false` 的服务。同名域名跨服务器冲突会
报告已有归属；可为不同服务器声明不同域名，例如 `demo.dev.localhost`。

`daemon restart` 按最新项目定义重新启用自动服务；`autostart: false` 的手动服务在
重启后需要再次 open。`daemon stop` 关闭服务并清除登记，保持停止状态；下一次显式
start/restart 才恢复自动发现。系统重启或临时目录清理后，daemon 再启动时仍从配置
自动发现服务。启动、恢复和 SSH 断线重连期间，入口返回
503；应用返回的 5xx 保留原响应。只有 SSH 传输故障触发隧道重连，HTTP 请求失败保持
既定 SSH 重连预算。预算耗尽后可使用 stop/open 重新建立路由。
`tdev connection ls`（或 `tdev ps`）将远端隧道显示为 `web`，本机路由合并到承载它的 daemon 进程行；`--verbose` 保留逐路由传输详情。

代理支持 WebSocket upgrade、SSE 和流式响应，并向上游传递请求取消。上游收到原始
Host、路径、查询、Origin 和 Cookie；代理重新生成 `X-Forwarded-*`，清除客户端伪造的
转发头。未知域名返回 404，非法 Host、绝对请求目标与 CONNECT 返回 400。
代理保留响应正文和应用跳转地址，应用可能需要配置 allowed hosts、Origin、外部 base
URL 和 OAuth callback。第一版提供明文 HTTP 上游和本机 HTTP 入口；HTTPS、证书信任、
系统 hosts 修改与 80/443 监听属于后续范围。浏览器须将 `*.localhost` 解析到 IPv4 回环；
可用 `curl --resolve demo.localhost:17890:127.0.0.1 http://demo.localhost:17890/` 验证入口。

手动打开的路由和连接参数以 0600 权限保存于现有 daemon 私有运行目录。自动服务
在 daemon 启动时从项目定义重建，采用当前的 `autostart` 设置。代理关闭请求访问日志，
状态检查只展示声明和传输状态。
