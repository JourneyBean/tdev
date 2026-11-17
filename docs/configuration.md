# 项目与客户端配置

项目定义保存在项目所在机器的 `${XDG_CONFIG_HOME:-$HOME/.config}/tdev` 下，`--config-dir` 可指定配置根目录。顶层名称是项目名；文件名用于组织配置，一个文件可以声明多个项目。项目名使用字母、数字、下划线或连字符，以字母或数字开头，最多 64 字符；`projects` 和 `client` 是保留名称。

## 单文件与多文件

存在 `config.yaml` 时，以该文件为入口。可以直接声明全部项目：

```yaml
demo:
  dir: ~/projects/demo
  windows:
    - name: editor
      command: nvim .
    - name: web
      dir: frontend
      command: npm run dev
  ports:
    - "3000:127.0.0.1:3000"
    - "3001:other-host:3001"

tools:
  dir: ~/projects/tools
```

也可以在主配置中包含其他文件，并同时声明项目：

```yaml
projects:
  include:
    - ./projects/*.yaml
    - ./shared.yaml

scratch:
  dir: ~/projects/scratch
```

被包含文件沿用相同的项目声明格式，例如 `projects/work.yaml`：

```yaml
demo:
  dir: ~/projects/demo

tools:
  dir: ~/projects/tools
```

没有主配置时，自动读取配置根下的 `projects/*.yaml`。存在主配置后，其他文件通过 include 加载。相对 include 路径以主配置所在目录为基准，glob 匹配按路径排序；空匹配允许，显式文件缺失报错。文件重复包含、自包含和项目重名均报错；重复项目错误指出两个源文件及位置。同名项目的字段须写在同一处。include 仅允许出现在主配置的 `projects` 块中。

新建项目时，有主配置则追加到主配置；没有主配置则写入 `projects/<项目名>.yaml`。已有文件包含其他项目时追加声明。编辑和 save 写回项目所在的源文件，保留其他项目、注释、顺序及未修改的简写；YAML 缩进和排版可能被规范化。符号链接包含的文件写回实际目标。

## 目录、窗口与命令

最简项目只需要 `dir`。省略 windows 时生成一个名为 main 的 shell：

```yaml
demo:
  dir: ~/projects/demo
```

项目 dir 使用绝对路径或 `~/`，由项目所在机器展开。窗口和 pane 的 dir 支持绝对路径、`~/` 或相对项目根目录的路径；窗口省略 dir 时使用项目根目录，pane 省略时继承窗口目录。嵌套分屏的相对目录始终相对项目根目录。

windows 按列表顺序创建，name 必填且项目内唯一，可使用中文。省略 command 时打开交互 shell。启动命令只在新建会话时执行，由项目机器上的 shell 解释；配置文件应来自可信用户。修改文件后，已有会话继续运行，新建会话使用最新配置。

## 分屏

layout 使用 row 表示左右排列，column 表示上下排列。一个分屏至少有两个子节点；weight 表示同级比例，省略时等分。叶节点可用 name 指定窗口内唯一的稳定名称，名称使用与项目名相同的字符规则；省略时按布局位置生成内部标识。

```yaml
demo:
  dir: ~/projects/demo
  windows:
    - name: dev
      layout:
        row:
          - name: editor
            command: nvim .
            weight: 2
          - column:
              - name: web
                dir: frontend
                command: npm run dev
              - name: api
                dir: backend
                command: uv run server.py
            weight: 1
```

使用 layout 时，command 写在叶节点。分屏节点仅包含 row 或 column 以及可选 weight。布局按终端可用单元格近似恢复。项目机器和编辑客户端需支持 `pane-layout-v1`。

## 端口

ports 使用列表，简写默认建立本机监听到项目机器目标的转发：

```yaml
demo:
  dir: ~/projects/demo
  ports:
    - "3000:127.0.0.1:3000"
    - "3001:other-host:3001"
    - "[::1]:8080:[::1]:8000"
```

简写遵循 OpenSSH 顺序 `[监听地址:]本机端口:目标主机:目标端口`，监听地址默认为 127.0.0.1。也支持 `"3000"` 同号转发、`"8080:8000"` 不同端口转发，省略的目标主机为 127.0.0.1。IPv6 地址加方括号。建议为端口简写加引号。

需要稳定名称或反向转发时使用完整写法：

```yaml
demo:
  dir: ~/projects/demo
  ports:
    - name: web
      local: 8080
      remote: 8000
    - name: database
      local: 5433
      remote: 5432
      host: database.example.invalid
    - name: callback
      direction: remote
      remote: 9000
      local: 3000
```

local 始终指客户端端口，remote 指项目机器端口。direction 默认 local：本机监听、项目机器一侧连接目标；remote 表示项目机器监听、客户端一侧连接目标。host 是目标地址，bind 是监听地址，均默认 127.0.0.1。

name 可选，使用与项目名相同的字符规则，在项目内唯一。具名端口以该名称供 `tdev override demo --forward web --local-port 18080` 引用。匿名端口按完整转发规则识别，规则变化后重新识别；长期使用客户端覆盖的端口应显式命名。调整 ports 列表顺序保持匿名规则身份。

## 内部记录、并发与兼容

手写项目声明仅包含用户设置，UUID、revision 和 schema_version 由目标机器的 tdev 管理。内部项目快照和声明摘要位于配置根的 `state/projects/<项目名>.json`，默认权限 0600。保持该目录可保留现有 tmux 会话与客户端 override 的关联；迁移整个配置根时同时保留内部记录。

同一配置根内，项目按名称定位，文件移动、拆分与合并保持身份。窗口按名称关联，手工重命名按新窗口识别；pane 可通过 name 保持布局调整时的关联。手工修改后，下次读取会校验并更新内部 revision；纯排版、注释和等价默认值保持 revision。机器 API 的 definition 继续提供内部字段，`edit --stdin --expected-revision N` 接受 show 返回的 definition 对象，与磁盘上的简洁 YAML 分属两种格式。

写操作取得项目锁与配置根共享锁，比较 revision 并检查源文件是否被外部编辑，冲突返回 CONFIG_CONFLICT。修改前保存整个源文件为 `<源文件>.<项目名>.rev-N.bak`，备份默认 0600；向已有文件追加新项目时，备份 revision 为 0。恢复时从备份取出所需项目声明，合并回当前源文件，保留共享文件中其他项目的后续修改。配置已提交而内部记录保存失败时返回 PARTIAL_SUCCESS。外部编辑器不参与 tdev 的文件锁；应避免在 tdev 提交配置的同时保存同一文件。

现有带 schema_version 的项目文件仍可读取，首次通过 tdev 修改时转换为简洁声明，保留项目 UUID、窗口标识和 revision 连续性；原文件备份使用 `<源文件>.rev-N.bak`。新版本转换后，旧版本程序需恢复旧格式备份才能读取。

解析拒绝未知字段、重复 key、多个 YAML 文档、YAML anchor/alias 和 merge key。项目配置只保存显式目录、布局、命令和端口，不采集环境变量、历史或凭据。

## 同时作为项目主机与客户端

同一台机器可以提供本机项目，也可以连接其他机器。主配置的顶层项目描述本机管理的项目；client 描述当前机器发起连接时使用的设置：

```yaml
demo:
  dir: ~/projects/demo
  ports:
    - name: web
      local: 3000
      remote: 3000

client:
  servers:
    dev:
      ssh_alias: dev-host
  overrides:
    dev/demo:
      ports:
        web:
          local: 18080
      mount:
        path: ~/mnt/demo
    local/demo:
      ports:
        web:
          local: 13000
```

servers 中的名称是当前客户端的连接名称；local 保留给本机。ssh_alias 引用系统 OpenSSH 配置；remote_binary 默认 .local/bin/tdev，remote_config_dir 可指定目标机器的配置根，ssh_config 可指定当前客户端使用的 SSH 配置文件。每个服务器也可配置 reconnect 覆盖。

overrides 的键必须是完整的 server/project，支持 local/project。dev/demo 的设置只影响本机访问 dev 的 demo；local/demo 的设置只影响本机打开本机项目。目标机器的项目 API 返回原始定义，发起连接的一侧合并自己的 override。其他机器连接此处的 demo 时获取项目默认端口 3000。

## 客户端片段与写回

client.include 相对主配置所在目录解析，可与项目 include 同时使用：

```yaml
projects:
  include:
    - ./projects/*.yaml
client:
  include:
    - ./client.d/*.yaml
```

客户端片段直接写 client 块内部的内容：

```yaml
# client.d/work.yaml
servers:
  dev:
    ssh_alias: dev-host
overrides:
  dev/demo:
    ports:
      web:
        local: 18080
```

include 的显式文件缺失报错，glob 空匹配允许；同一文件重复包含、自包含与片段中的嵌套 include 报错。同名服务器、同一 override 目标重复定义报错并指出来源。ssh 与 reconnect 设置各自集中声明一次。

setup 和 override 写回条目原来的文件，保留注释、其他条目和未修改的默认值。新增服务器和 override 写入主配置的 client 块；仅创建客户端主配置时自动加入 projects.include: [./projects/*.yaml]，保留已有项目发现规则。项目和客户端写操作共用配置根文件锁，源文件被外部编辑时返回 CONFIG_CONFLICT。写前保存完整源文件到 `<源文件>.client-<内容摘要>.bak`，权限为 0600；涉及多个文件时逐文件原子提交，中途失败返回 PARTIAL_SUCCESS。

## override 字段与身份

```yaml
client:
  overrides:
    dev/demo:
      ports:
        web:
          local: 18080
          bind: 127.0.0.1
        database:
          enabled: false
      extra_ports:
        - "19090:127.0.0.1:9090"
      mount:
        path: ~/mnt/demo
```

ports 按项目端口名称设置当前客户端的监听端口、监听地址或 enabled 状态，仅适用于 local 方向的转发。extra_ports 添加当前客户端专用的 local 转发，使用与项目 ports 相同的简写或完整对象。mount.path 是当前客户端挂载位置，支持绝对路径和 ~/，由当前客户端展开。本机项目直接返回项目目录。需要长期 override 的项目端口应显式命名。

项目目录、窗口、启动命令和共享转发目标由项目定义管理。项目定义先应用当前客户端 override，再应用对应命令支持的显式参数。不存在的端口名称、反向监听覆盖、非法端口和重复监听在使用前报错。

手写 override 使用目标名称和端口名称，不填写 project_id 或 schema_version。首次成功合并后，项目 UUID 绑定保存在配置根的 state/client-bindings.json，默认权限 0600。绑定变化时拒绝应用，并提示 `tdev override server/project --reset`。reset 同时移除当前客户端的目标声明和内部绑定；需要保留原偏好时，重新添加声明后再绑定新项目。

## SSH 与重连偏好

```yaml
client:
  reconnect:
    terminal: {attempts: 5, interval: 2s}
    forward: {attempts: 5, interval: 2s}
    http: {attempts: always, interval: 5s}
  ssh:
    connect_timeout: 10s
    server_alive_interval: 15s
    server_alive_count_max: 3
```

重连设置的优先级为命令参数、服务器覆盖、client 全局设置和内置默认值。attempts 为初次连接之外的重试次数；0 表示停止重试，always 表示持续重试。HTTP 设置用于 Web 隧道独立重连；代理端口通过 `client.http.port` 配置，默认为 17890。项目 `http` 声明见 [Web 服务转发](web-services.md)。向导省略与内置默认值相同的全局配置。

## 旧客户端配置迁移

主配置没有 client 块时，继续读取 client.yaml 和 overrides.yaml。首次通过 setup、override 或挂载偏好保存客户端设置时，把完整客户端设置迁入主配置；旧文件以 .legacy.bak 保存私有备份，并保留原文件供旧程序回退。存在 client 块后，新块及其 include 是完整客户端配置来源，旧文件保留作回退数据。client: {} 表示使用内置默认值和空服务器、override 列表。

迁移保留所有服务器、重连/SSH 偏好、端口规则和挂载位置，项目 UUID 转入内部绑定文件。仅手动迁入 YAML 时，可在首次使用时建立身份绑定；迁移整个配置根应同时保留 state 下的记录。


项目 `http` 服务支持 `autostart: true|false`，默认为 `true`。daemon 启动时读取本机和
已配置服务器的全部项目，仅自动启用允许自动启动的服务。`false` 服务通过 open
手动启用，daemon 重启后再次手动打开。配置示例与启停语义见 [Web 服务转发](web-services.md)。
