# GitHub Actions 发布

`.github/workflows/release.yml` 在 main 推送及针对 main 的 PR 中运行静态检查和真实 SSH/tmux 竞态测试。推送稳定版本标签后，通过测试的同一提交构建 Linux/macOS 的 amd64/arm64 签名包并创建 GitHub Release。

测试在 Go Bookworm 容器中以独立普通用户运行，使用 C.UTF-8 locale；构建及发布在 Ubuntu Runner 上执行。

## 配置签名

在仓库 Settings → Secrets and variables → Actions 中添加 `TDEV_SIGNING_KEY`，内容为 `docs/release.pub` 对应的现有、未加密 PKCS8 Ed25519 私钥。GitHub Secret 保存 PEM 内容；GitLab File 变量保存密钥文件，两种配置沿用同一安装信任链。通过可信的本地文件设置该 secret；密钥保留在仓库之外。

```sh
gh secret set TDEV_SIGNING_KEY --repo JourneyBean/tdev < /secure/external/release-key.pem
```

发布任务核对私钥导出的公钥，临时密钥目录位于 Runner 临时目录中，构建结束时清理。签名步骤要求关闭 Actions 调试日志。测试任务只有 contents: read；发布任务使用 GitHub 自动提供的 GITHUB_TOKEN 和 contents: write 权限，无需另配发布 token。建议通过仓库 ruleset 限制 v* 标签的创建、更新和删除权限。

## 自动发布

同步 `Makefile`、`internal/cli/cli.go` 和 `docs/releases/vX.Y.Z.md` 后，将提交及版本标签推送到 GitHub：

```sh
git push github main
git push github v0.4.0
```

main 的测试通过后，版本标签流水线独立执行完整验收并构建发布。标签必须是无前导零的 `vMAJOR.MINOR.PATCH`，源码及构建版本需与标签一致。预发布标签在版本检查阶段拒绝。

## 补发已有标签

已有标签的提交可以早于 workflow。先将 workflow 推送到 GitHub 默认分支，再在 Actions → GitHub CI and Release → Run workflow 中选择 main，填写已有标签，如 v0.3.0；也可以运行：

```sh
gh workflow run release.yml --repo JourneyBean/tdev --ref main -f tag=v0.3.0
```

手动运行从已有标签检出源码、执行测试，并按测试记录的提交 SHA 构建。目标标签无需移动。若通过 GITHUB_TOKEN 推送标签，使用 workflow_dispatch 显式触发发布；该 token 产生的普通 push 事件受 [GitHub 防递归规则](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow)限制。

v0.3.0 的终端测试夹具通过 Go test overlay 读取 workflow 提交中的 ANSI 兼容断言；原标签文件与生产构建源码保持原样。

## 产物与重试

发布前验证 manifest 和 SHA256SUMS 的 Ed25519 签名、四平台二进制及压缩包摘要、可信公钥和安装脚本语法。固定上传 14 个文件：四个平台各一个二进制和 tar.gz，加上 manifest.json、manifest.sig、SHA256SUMS、SHA256SUMS.sig、release.pub、install.sh。临时签名文件不在上传名单内。

先创建草稿并上传全部文件，成功后才公开 Release 并更新 latest。已存在的 Release 保持原样；同一标签的并发运行串行执行。上传失败可能留下草稿，需要先检查草稿及产物，处理后再运行；流水线不自动覆盖发布文件或删除 Release。

公开后调用 `verify-release.yml`，从匿名版本下载地址读取全部 14 个附件，验证公钥、签名、摘要、压缩包内容，并在临时目录在线安装 Linux amd64 版本。已有 Release 可单独手动运行 Verify public release，输入标签后验证下载，无需重新构建或覆盖产物。

安装脚本内置当前 GitHub 仓库的版本下载地址和 latest 升级地址，继续支持 README 中的 GitHub 安装方式。GitLab 发布流程独立保留。
