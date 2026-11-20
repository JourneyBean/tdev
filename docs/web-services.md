# Web 服务转发

项目的 `http` 声明将域名映射到项目所在机器的 HTTP 或 HTTPS 服务。本机代理分别在
两个端口接收 HTTP 和 HTTPS 请求。远端服务通过共享 OpenSSH 控制连接上的 TCP 隧道访问。
本机项目直接访问目标服务。

## 使用向导

```sh
tdev web setup           # 选择客户端代理或项目 Web 路由
tdev web setup dev/demo  # 直接选择项目，添加、修改或删除路由
```

客户端向导设置默认域名后缀和 HTTP、HTTPS 监听端口。项目向导设置路由名称、Web
域名、上游协议、地址、端口和自动启动选项。本机或远端项目的路由保存在项目所在机器，
客户端设置保存在本机。向导需要交互终端，并在保存前显示修改预览。

删除时选择「删除已有 Web 路由」及具体路由。向导会列出项目、路由名、域名和上游地址，
输入 `y` 才会从项目配置删除。

客户端设置保存后，通过 `tdev daemon restart` 应用。项目路由配置保存后，若项目已有活动
Web 路由，先执行 `tdev web stop dev/demo`。用 `tdev open dev/demo --only web` 启动配置中
保留的路由。

## 在配置文件中声明

在项目所在机器的 `config.yaml` 或项目片段中声明 Web 服务。

```yaml
demo:
  dir: /home/developer/projects/demo
  http:
    - name: web
      hostname: demo
      target_host: localhost
      target_port: 5173
      autostart: true
    - name: api
      hostname: api-demo
      target_scheme: https
      target_host: localhost
      target_port: 8443
      autostart: false
```

`name` 使用项目名的字符规则，在项目内唯一。`hostname` 可填写小写 DNS 标签（例如
`demo`），由客户端追加默认后缀 `tdev.localhost`。也可填写以 `.localhost` 结尾的
完整域名（例如 `api.demo.localhost`），客户端按原值使用。各标签使用小写字母、
数字和连字符。展开后的完整域名在当前客户端 daemon 中唯一。`target_host` 和
`target_port` 为必填项，目标端口范围为 1–65535。`target_scheme` 选择代理连接上游时
使用 `http` 或 `https`，省略时为 `http`。HTTPS 上游接受自签证书，代理跳过上游证书
校验。使用域名作为 `target_host` 时，TLS SNI 使用这个域名。
`autostart` 为可选布尔值，省略时默认为 `true`；`false` 保留手动打开入口。
应用的启动由项目窗口命令或其他现有方式管理。远端项目包含 `http` 声明时，
本机和远端都需要支持所用声明格式的 tdev 版本。升级后运行 `tdev daemon restart` 采用新版本。

客户端可在自己的 `config.yaml` 中配置两个代理端口和默认域名后缀。

```yaml
client:
  http:
    port: 24880
    https_port: 24881
    domain_suffix: tdev.localhost
  reconnect:
    http: {attempts: always, interval: 5s}
```

HTTP 默认端口为 24880，HTTPS 默认端口为 24881，两者均监听 `127.0.0.1`。
端口必须不同。如果把 HTTP 设为 24881，省略 `https_port` 时 HTTPS 使用 24880。
`domain_suffix` 使用小写 DNS 名称，
首尾省略点号。设置为 `dev.example.invalid` 时，`hostname: demo` 对应
`demo.dev.example.invalid`，需在客户端将该域名解析到 `127.0.0.1`。
支持 `client.include`，HTTP 重连策略沿用服务器覆盖优先于全局配置的规则。
代理活动期间修改任一端口，需要先停止当前所有 Web 路由再重新打开。
如果任一端口被占用，启动返回 `INVALID_CONFIG`。修改后缀后，可对项目执行 `web stop` 和 `open --only web`，
或用 `daemon restart` 按新后缀重新启动自动服务。

```sh
tdev open dev/demo --only web
# 浏览器访问 http://demo.tdev.localhost:24880
# 或访问 https://demo.tdev.localhost:24881
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
返回的每条路由包含 HTTP 地址 `url`、HTTPS 地址 `https_url`、
`connecting` / `ready` / `waiting` / `failed` 状态及连接错误。
`ready` 表示隧道已建立；应用自身的可用性由 HTTP 响应反映。本机路由直接报告 ready。

Web 入口独立于终端使用权，终端退出和 detach 后持续运行。`web stop server/project`
移除该项目全部路由、隧道和恢复登记，关闭其活动连接，包括 WebSocket；其他项目保留。
手动停止也抑制本轮尚未完成的发现任务；下次 daemon 启动重新按项目配置判断。
最后一个路由停止时释放两个代理监听端口。目标地址、域名、连接偏好或重连策略变化后，先停止旧
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

代理支持 WebSocket upgrade、SSE 和流式响应，并向上游传递请求取消。浏览器可用
`ws://` 或 `wss://`，上游的 WebSocket 连接沿用该路由的 `target_scheme`。上游收到原始
Host、路径、查询、Origin 和 Cookie；代理重新生成 `X-Forwarded-*`，清除客户端伪造的
转发头。未注册的 `*.localhost` 域名返回 404。其他未注册的 Host、非法 Host、绝对请求目标
与 CONNECT 返回 400。
代理保留响应正文和应用跳转地址，应用可能需要配置 allowed hosts、Origin、外部 base
URL 和 OAuth callback。浏览器须将 Web 域名解析到 IPv4 回环。HTTPS 入口按访问域名
提供 tdev 自签名证书。证书和私钥保存在 daemon 内存中，路由重新注册时生成新证书。
浏览器首次连接通常会提示证书不受信任。可用以下命令测试本机 HTTPS 入口。

```sh
curl --insecure --resolve demo.tdev.localhost:24881:127.0.0.1 https://demo.tdev.localhost:24881/
```

手动打开的路由和连接参数以 0600 权限保存于现有 daemon 私有运行目录。自动服务
在 daemon 启动时从项目定义重建，采用当前的 `autostart` 设置。代理关闭请求访问日志，
状态检查只展示声明和传输状态。
