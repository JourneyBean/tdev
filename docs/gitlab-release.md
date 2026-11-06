# GitLab CI 发布

仓库根目录的 `.gitlab-ci.yml` 提供测试、签名构建和发布三个阶段。普通分支及合并请求运行测试；受保护的 `vMAJOR.MINOR.PATCH` tag 通过测试后自动发布，例如 `v0.2.0`。当前发布入口接受稳定版本 tag。

## 配置项目

1. 提供 Linux Docker Runner。流水线使用 `golang:1.27-bookworm`，需要访问镜像、Debian 软件源、Go 模块源和当前 GitLab 项目。测试容器安装 OpenSSH、tmux、zsh 和 OpenSSL，以独立用户运行真实 SSH/tmux 集成测试。
2. 在项目的 CI/CD Variables 添加 `TDEV_SIGNING_KEY`，类型选择 **File**，启用 **Protected**，关闭变量值展开。值为现有发布公钥 `docs/release.pub` 对应的未加密 PKCS8 Ed25519 私钥。构建会核对公钥，继续使用既有安装信任链。
3. 在 Protected tags 中保护 `v*`，限制发布 tag 的创建权限。构建任务仅运行在受保护的版本 tag 上。
4. 开启 Package Registry 和 Releases，确保触发流水线的用户具有上传包及创建 Release 的权限。发布使用 GitLab 自动提供的 `CI_JOB_TOKEN`。

签名任务将密钥复制到工作目录之外的临时目录，设置 0600 权限，结束时清理；构建产物只归档 `dist/`。签名构建要求关闭 `CI_DEBUG_TRACE`。提交和 tagger 元数据使用公开 noreply 邮箱。推送前检查待发布 diff、文件名、密钥及内部环境标识；发布产物和 CI 日志按公共内容处理。

## 触发发布

先提交准备发布的代码及流水线。确认 `Makefile` 中的默认版本、`internal/cli/cli.go` 的版本和 `docs/releases/v0.2.0.md` 一致，再创建并推送 tag：

```sh
git tag -a v0.2.0 -m 'tdev v0.2.0'
git push origin v0.2.0
```

`origin` 应指向运行该流水线的 GitLab 仓库。后续版本先同步版本号和发布说明，再推送对应 tag。也可以在 GitLab 的 Run pipeline 页面选择已存在的受保护版本 tag。

## 流水线与产物

`test` 安装集成依赖并运行 `make check` 和完整竞态测试。测试使用临时配置、生成的测试身份及私有 tmux socket。`build_release` 检查 tag、源码与构建版本、发布说明和签名公钥，再构建 Linux/macOS 的 amd64/arm64 二进制及压缩包，生成 Ed25519 签名清单、SHA-256 校验表、公钥和安装脚本。

`publish_release` 再次验证清单签名、公钥、二进制及压缩包摘要和校验表。14 个指定产物上传到当前项目的 Generic Package Registry：

```text
${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/packages/generic/tdev/0.2.0/<filename>
```

文件全部就绪后，任务读取对应版本的发布说明创建 GitLab Release，并为每个文件配置永久下载链接。发布任务通过 resource group 串行执行。任务 artifacts 保留 7 天；Release 使用 Package Registry 中的文件，包需要按发布保留策略长期保存。

上传中断时可重试发布任务，已有且摘要一致的文件复用。摘要不同的同版本文件会阻止发布；已存在的 Release 保持原样并报告错误。重新构建产生不同字节时使用新版本，清理或重写已有发布需单独处理。

实现使用 GitLab 官方的 [Generic Package Registry](https://docs.gitlab.com/user/packages/generic_packages/)、[Releases API](https://docs.gitlab.com/api/releases/) 和[永久下载链接](https://docs.gitlab.com/user/project/releases/release_fields/#permanent-links-to-release-assets)。

## 从 GitLab 安装和升级

将示例地址替换为自己的项目：

```sh
curl -fsSL https://gitlab.example.invalid/group/tdev/-/releases/permalink/latest/downloads/install.sh | sh
```

生成的安装脚本内置对应 tag 的 GitLab 下载地址、公钥和二进制摘要；安装凭据保存当前项目的 latest Release 下载目录。后续 `tdev upgrade` 从该 GitLab 项目获取新版本。显式 `--release-url` 继续覆盖下载及后续升级来源；普通本地 `make release` 保留 GitHub 默认来源。

在线安装及升级要求项目和包的 HTTPS 下载地址可匿名读取。私有项目可先通过外部认证工具下载整套 Release，再使用 `--release-dir` 和独立可信公钥进行离线安装或升级；客户端继续使用现有的无凭据下载协议。

手动构建 GitLab 安装包时，同时提供版本目录和后续升级目录：

```sh
TDEV_SIGNING_KEY=/secure/external/release-key.pem \
TDEV_RELEASE_URL=https://gitlab.example.invalid/group/tdev/-/releases/v0.2.0/downloads \
TDEV_UPGRADE_URL=https://gitlab.example.invalid/group/tdev/-/releases/permalink/latest/downloads \
make release VERSION=0.2.0
```

自建 GitLab 需要有效的 HTTPS 证书。流水线发布接口使用当前项目的标准 CI 变量，仓库中只保存通用配置。
