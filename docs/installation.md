# 安装与下载

## 一键安装

macOS/Linux，支持 amd64 和 arm64，无需 sudo 或 Go：

```sh
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh
```

安装到 `~/.local/bin/tdev`，自动为当前 shell 配置 PATH。打开新终端后执行：

```sh
tdev version
tdev setup --help
```

也可以执行安装器打印的 PATH 命令，立即在当前终端使用。脚本支持 bash、zsh、fish 和 POSIX sh；自动识别依据 `$SHELL`。

```sh
# 指定 shell
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh -s -- --shell zsh

# 仅安装二进制
curl -fsSL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh | sh -s -- --no-modify-path

# 自定义目录，并显式配置 PATH
sh install.sh --destination "$HOME/tools/bin/tdev" --add-path

# 下载脚本后查看，再运行
curl -fL https://github.com/JourneyBean/tdev/releases/latest/download/install.sh -o install.sh
sh install.sh
```

## v0.0.2 二进制

| 平台 | 压缩包 | 独立二进制 |
|---|---|---|
| macOS · Apple Silicon | [darwin-arm64.tar.gz](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-darwin-arm64.tar.gz) | [darwin-arm64](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-darwin-arm64) |
| macOS · Intel | [darwin-amd64.tar.gz](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-darwin-amd64.tar.gz) | [darwin-amd64](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-darwin-amd64) |
| Linux · x86_64 | [linux-amd64.tar.gz](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-linux-amd64.tar.gz) | [linux-amd64](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-linux-amd64) |
| Linux · ARM64 | [linux-arm64.tar.gz](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-linux-arm64.tar.gz) | [linux-arm64](https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-linux-arm64) |

手动安装示例（Linux x86_64）：

```sh
curl -fLO https://github.com/JourneyBean/tdev/releases/download/v0.0.2/tdev-linux-amd64.tar.gz
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

升级复用安装时保存的发布公钥和下载源，验证签名后替换二进制，保留旧版本备份。远端需要 Linux、SSH 和 tmux；文件挂载另需 SSHFS，macOS 需 macFUSE。
