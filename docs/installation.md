# 安装与下载

## 一键安装

macOS/Linux，支持 amd64 和 arm64，无需 sudo 或 Go：

```sh
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh
```

安装到 `~/.local/bin/tdev`，自动为当前 shell 配置 PATH。打开新终端后执行：

```sh
tdev version
tdev setup
```

也可以执行安装器打印的 PATH 命令，立即在当前终端使用。脚本支持 bash、zsh、fish 和 POSIX sh；自动识别依据 `$SHELL`。
安装成功后会询问是否配置检测到的 bash/zsh 命令补全。管道安装从终端读取回答；无人值守环境使用显式参数。

```sh
# 指定 shell
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh -s -- --shell zsh

# 仅安装二进制
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh -s -- --no-modify-path --no-completion

# 一键安装并配置 bash 与 zsh 补全
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh -s -- --completion all

# 自定义目录，并显式配置 PATH
sh install.sh --destination "$HOME/tools/bin/tdev" --add-path

# 下载脚本后查看，再运行
curl -fL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh -o install.sh
sh install.sh
```

## 按版本下载二进制

发布产物见 [GitHub Releases](https://github.com/JourneyBean/tdev/releases)。每个版本包含以下平台，压缩包内的程序名为 tdev：

| 平台 | 压缩包 | 独立二进制 |
| --- | --- | --- |
| macOS · Apple Silicon | tdev-darwin-arm64.tar.gz | tdev-darwin-arm64 |
| macOS · Intel | tdev-darwin-amd64.tar.gz | tdev-darwin-amd64 |
| Linux · x86_64 | tdev-linux-amd64.tar.gz | tdev-linux-amd64 |
| Linux · ARM64 | tdev-linux-arm64.tar.gz | tdev-linux-arm64 |

手动安装示例（Linux x86_64）：

```sh
curl -fLO https://github.com/JourneyBean/tdev/releases/latest/download/tdev-linux-amd64.tar.gz
tar -xzf tdev-linux-amd64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 tdev "$HOME/.local/bin/tdev"
"$HOME/.local/bin/tdev" path --write
```

直接解包属于手动安装；需要签名验证、托管升级和回滚时，使用安装脚本。

## 单独设置 PATH

```sh
~/.local/bin/tdev path --write
~/.local/bin/tdev path --write --shell bash
~/.local/bin/tdev path --shell zsh   # 只显示配置命令
```

zsh 使用 `${ZDOTDIR:-$HOME}/.zshrc`；bash 使用 `.bashrc` 和当前登录配置文件；fish 使用 `${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/tdev.fish`；POSIX sh 使用 `.profile`。修改前保留备份，只维护 `tdev PATH` 标记中的内容。重复运行不会重复添加路径。

## 一键配置命令补全

```sh
tdev completion install --shell auto
tdev completion install --shell bash
tdev completion install --shell zsh
tdev completion install --shell all
```

配置写入对应 shell 的启动文件，修改前保留备份，保留文件权限和符号链接。`tdev completion` 区块与 PATH 独立，重复执行保持一份。打开新终端后启用；启动时从安装的二进制加载补全，升级后自动使用新版定义。

安装脚本支持 `--completion bash|zsh|all` 直接配置、`--install-completion` 配置检测到的 shell、`--no-completion` 跳过。默认 `auto` 在有终端时询问，无终端时输出后续配置命令。直接运行 `tdev install` 同样支持 `--completion auto|none|bash|zsh|all`；升级和回滚沿用已有配置。

## 校验与升级

每个 Release 附带 `manifest.json`、`manifest.sig`、`SHA256SUMS`、`SHA256SUMS.sig` 和 `release.pub`。正式安装脚本固定该版本的二进制摘要，在执行下载内容前校验 SHA-256；候选程序再验证 Ed25519 manifest 和版本。

发布公钥见[release.pub](release.pub)。自定义/离线发布源使用独立可信公钥，bootstrap 需要 OpenSSL 3+：

```sh
sh scripts/install.sh --release-dir ./verified-release --public-key /trusted/release.pub
```

通过默认在线安装入口安装后，可直接升级和回退：

```sh
tdev upgrade
tdev upgrade --rollback
```

升级复用安装时保存的发布公钥和下载源。安装与升级验证签名后比较二进制内容：相同内容保留原文件和已有回滚记录，内容变化时保存旧版本及其收据，再替换二进制。安装记录与备份统一存放在 `${XDG_STATE_HOME:-~/.local/state}/tdev/installations/<安装目标路径的 SHA256>/` 下的 `receipt.json` 和 `backups/`；XDG_STATE_HOME 使用绝对路径。新版安装或回滚时自动迁移旧记录及其引用的备份链，校验完成后清理旧路径。回退到仅支持旧收据路径的历史版本后，再次升级使用新版安装脚本。每个安装目标最多保留当前版本之前的两个版本，可连续回滚两次；成功安装或回滚后清理超出保留范围的备份。相同内容重复安装保留已有回滚记录。远端需要 Linux、SSH 和 tmux；文件挂载另需 SSHFS，macOS 需 macFUSE。

## v0.2.0 客户端与服务端

GitLab 的签名构建、Release 发布及专属安装地址见 [GitLab CI 发布](gitlab-release.md)。GitLab 生成的安装脚本内置当前项目的版本下载目录，后续 upgrade 使用该项目的 latest Release；在线下载需要匿名可读的 HTTPS 项目与包。

v0.2.0 增加本机诊断日志和 --debug，修复 zsh 登录 shell 下的目录补全。补全修复由客户端生成命令实现，更新客户端即可生效。机器协议、配置 schema 与 capability 要求沿用 v0.1.0，连接时继续按能力检查。

## v0.1.0 客户端与服务端

v0.1.0 使用 tdev api 机器接口。连接旧版服务端时，客户端显示版本并提示升级；已登记服务器使用 `tdev upgrade --remote dev`。远端通过上传已验证候选二进制完成升级，原 API 无需先支持新协议。

本机项目可直接创建，无需 setup。旧版自动生成名称的 tmux session 按项目 UUID 识别并复用；新项目 session 使用 `tdev-project-<name>`，该名称被无匹配 UUID 的会话占用时使用 `tdev-project-<UUID>`（UUID 去掉连字符）并提示 warning。scratch 使用 `tdev-scratch-<name>`，省略名称时使用自动生成的 UUID。
