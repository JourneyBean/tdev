# 机器接口

`tdev api` 供客户端在目标机器上执行，使用明确参数和结构化 stdin，无业务确认。`--json` 输出单一协议响应；session attach 要求终端和明确的会话身份。

```sh
./bin/tdev --json version
./bin/tdev --json api version --require api-v1
./bin/tdev --json api project list --status
./bin/tdev --json api project create demo --directory /home/developer/projects/demo
./bin/tdev --json api project get demo
./bin/tdev --json api project set demo --stdin --expected-revision 3 < demo.json
./bin/tdev --json api session ensure demo
./bin/tdev --json api session save demo --expected-revision 3
```

项目定义统一通过 project get/set 读写。其他接口按 session、transfer、binary 和 mount-probe 分组。客户端每次 API 调用将版本查询、能力检查与操作合并为一次 SSH shell 调用，用随机分隔标记和独立退出码解析结果；能力检查通过后才执行操作。setup 的依赖与项目查询共享一次连接。旧服务端提示升级：

```sh
tdev upgrade --remote dev
```

`--ssh-config` 与 `--yes` 属于用户命令；`--config-dir`、`--timeout` 和 `--json` 两层共用。API 参数和配置错误与 SSH 连接错误分别报告。

项目定义见[配置指南](configuration.md)，协议与 SSH 批次设计见[设计文档](design.md#8-机器-api-与-shell-批次)。
