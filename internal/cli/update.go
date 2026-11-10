package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
	"tdev/internal/update"
)

func releaseFlags(c *cobra.Command) {
	c.Flags().String("release-dir", "", "local directory containing signed release artifacts")
	c.Flags().String("release-url", os.Getenv("TDEV_RELEASE_URL"), "HTTPS release directory")
	c.Flags().String("public-key", "", "independently trusted PEM Ed25519 release public key")
	c.Flags().String("version", "", "require this signed release version")
}
func (a *app) bundle(ctx context.Context, c *cobra.Command, goos, arch string, receipt *update.Receipt) (update.Bundle, error) {
	dir, _ := c.Flags().GetString("release-dir")
	source, _ := c.Flags().GetString("release-url")
	key, _ := c.Flags().GetString("public-key")
	version, _ := c.Flags().GetString("version")
	if dir == "" && source == "" && receipt != nil {
		source = receipt.SourceURL
	}
	if key == "" {
		if receipt == nil && dir != "" {
			return update.Bundle{}, config.Invalid("--public-key is required for a custom local release")
		}
		publicKey := update.OfficialPublicKey
		if receipt != nil {
			publicKey = receipt.PublicKey
		}
		f, err := os.CreateTemp("", "tdev-trust-*.pem")
		if err != nil {
			return update.Bundle{}, err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.WriteString(publicKey); err != nil {
			f.Close()
			return update.Bundle{}, err
		}
		if err = f.Close(); err != nil {
			return update.Bundle{}, err
		}
		key = name
	}
	if dir == "" && source == "" {
		source = update.OfficialReleaseURL
	}
	return update.Load(ctx, dir, source, key, version, goos, arch)
}
func (a *app) apiUpdate(local *cobra.Command) {
	g := group(local, "binary", "Inspect/install/rollback a verified local binary")
	a.postInstallCommand(g)
	a.command(g, "platform", "Report runtime platform without requiring tmux", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}, nil
	})
	r := a.command(g, "receipt", "Read a managed installation receipt", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		d, _ := c.Flags().GetString("destination")
		d, err := update.Destination(d)
		if err != nil {
			return nil, err
		}
		return update.ReadReceipt(d)
	})
	r.Flags().String("destination", "", "binary destination")
	install := a.command(g, "install", "Install a signature-verified release with backup", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		b, err := a.bundle(ctx, c, runtime.GOOS, runtime.GOARCH, nil)
		if err != nil {
			return nil, err
		}
		defer b.Close()
		sourceURL, _ := c.Flags().GetString("source-url")
		if sourceURL != "" {
			b.SourceURL = sourceURL
		}
		destination, _ := c.Flags().GetString("destination")
		return a.installBinary(ctx, b, destination)
	})
	releaseFlags(install)
	install.Flags().String("destination", "", "absolute or home-relative binary destination")
	install.Flags().String("source-url", "", "verified source URL to remember for future upgrades")
	rollback := a.command(g, "rollback", "Restore the previous identity-checked binary backup", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		d, _ := c.Flags().GetString("destination")
		return a.rollbackBinary(ctx, d)
	})
	rollback.Flags().String("destination", "", "managed destination")
	a.apiMountProbe(local)
}
func (a *app) updateCommands() {
	a.postInstallCommand(a.root)
	for _, op := range []string{"install", "upgrade"} {
		op := op
		c := &cobra.Command{Use: op, Short: op + " a signed local or remote binary without restarting sessions", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			remote, _ := c.Flags().GetString("remote")
			if remote == "local" {
				remote = ""
			}
			completionMode := "none"
			if op == "install" {
				completionMode, _ = c.Flags().GetString("completion")
				if !validCompletionMode(completionMode) {
					return config.Invalid("completion must be auto, none, bash, zsh or all")
				}
				if remote != "" && c.Flags().Changed("completion") {
					return config.Invalid("--completion configures local shells; run completion install on the target device")
				}
			}
			rollback, _ := c.Flags().GetBool("rollback")
			destination, _ := c.Flags().GetString("destination")
			var receipt *update.Receipt
			if remote != "" {
				p, err := a.prefs()
				if err != nil {
					return err
				}
				t, err := a.serverTarget(p, remote, "")
				if err != nil {
					return err
				}
				if destination != "" {
					t.Server.RemoteBinary = destination
				}
				if op == "upgrade" {
					a.upgradeNotice(remote)
				}
				if rollback {
					b, err := a.call(c.Context(), t, nil, "binary", "rollback", "--destination", t.Server.RemoteBinary)
					return a.result(b, err)
				}
				result, err := a.installRemote(c.Context(), t, c)
				return a.result(result, err)
			}
			if destination == "" && op == "upgrade" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				destination = exe
			}
			var err error
			destination, err = update.Destination(destination)
			if err != nil {
				return err
			}
			if op == "upgrade" {
				a.upgradeNotice("")
			}
			if rollback {
				result, err := a.rollbackBinary(c.Context(), destination)
				return a.result(result, err)
			}
			if old, err := update.ReadReceipt(destination); err == nil {
				receipt = &old
			} else if op == "upgrade" {
				return config.Invalid("binary is not managed by tdev; use its package manager or explicit tdev install --destination")
			}
			b, err := a.bundle(c.Context(), c, runtime.GOOS, runtime.GOARCH, receipt)
			if err != nil {
				return err
			}
			defer b.Close()
			if _, err = os.Lstat(destination); err == nil {
				if err = a.confirm(fmt.Sprintf("Replace %s with signed version %s, retaining a backup?", destination, b.Manifest.Version)); err != nil {
					return err
				}
			}
			result, err := a.installBinary(c.Context(), b, destination)
			if err == nil && op == "install" {
				completion, completionErr := a.configureCompletionAfterInstall(c.Context(), completionMode, destination)
				if completionErr != nil {
					return protocol.Fail("PARTIAL_SUCCESS", 9, "binary installed; completion configuration failed: %v", completionErr)
				}
				if completion != nil {
					return a.result(map[string]any{"installation": result, "completion": completion}, nil)
				}
			}
			return a.result(result, err)
		}}
		a.root.AddCommand(c)
		releaseFlags(c)
		c.Flags().String("destination", "", "install destination (defaults to ~/.local/bin/tdev; upgrade defaults to current managed binary)")
		c.Flags().String("remote", "", "install/upgrade on a registered server")
		c.Flags().Bool("rollback", false, "restore previous binary instead of downloading a release")
		if op == "install" {
			c.Flags().String("completion", "auto", "configure shell completion: auto (ask), none, bash, zsh or all")
		}
	}
}

func (a *app) upgradeNotice(remote string) {
	if remote != "" {
		fmt.Fprintln(a.errOut, "升级期间端口映射可能短暂中断。远端目标程序执行 post-install hooks 并保留 tmux 会话；现有附着进程继续运行，后续请求和断线重连使用升级后的远端程序。")
		fmt.Fprintf(a.errOut, "连接中断时，当前终端按重连策略恢复原会话；自动重连失败后可运行 tdev open %s/<项目名>。协议不兼容时请先升级本地客户端。\n", remote)
		return
	}
	fmt.Fprintln(a.errOut, "升级期间端口映射可能短暂中断。安装后的目标程序执行 post-install hooks，迁移当前配置的托管 tmux 会话，并在程序内容变化时刷新运行中的 daemon；活跃终端按重连策略恢复，后台映射重新建立。已停止的 daemon 保持停止。")
	fmt.Fprintln(a.errOut, "现有客户端进程继续运行原版本；自动重连失败后请重新运行 tdev open <服务器>/<项目名>。")
}

func (a *app) shell(ctx context.Context, t target, command string) (data []byte, resultErr error) {
	started := time.Now()
	var exitCode *int
	defer func() { _, _ = a.recordDiagnostic("shell", t.Name, started, resultErr, exitCode) }()
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	var args []string
	var err error
	release := func() {}
	if !t.Local {
		args, release, err = a.remoteArgs(ctx, t, false, command, "bootstrap")
		if err != nil {
			return nil, err
		}
	}
	defer release()
	cmd := sshx.Command(ctx, "ssh", args...)
	if t.Local {
		cmd = sshx.Command(ctx, "sh", "-c", command)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		exitCode = &code
	}
	if ctx.Err() != nil {
		return nil, sshx.Error(ctx.Err(), stderr.String())
	}
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 7 {
		return nil, protocol.Fail("DEPENDENCY_MISSING", 7, "remote bootstrap dependency missing: %s", stderr.String())
	}
	return out, sshx.Error(err, stderr.String())
}
func (a *app) installRemote(ctx context.Context, t target, c *cobra.Command) (any, error) {
	marker := "TDEV_INSTALL_" + strings.ReplaceAll(config.NewID(), "-", "")
	receiptScript := remoteReceiptScript(t.Server.RemoteBinary)
	script := "uname -s; uname -m; command -v tmux >/dev/null || { printf 'tmux is required; install it before setup\\n' >&2; exit 7; }; printf '\\n" + marker + "\\n'; " + receiptScript
	platform, err := a.shell(ctx, t, script)
	if err != nil {
		return nil, err
	}
	metadataParts := bytes.SplitN(platform, []byte("\n"+marker+"\n"), 2)
	if len(metadataParts) != 2 {
		return nil, config.Invalid("invalid remote platform/receipt response")
	}
	fields := strings.Fields(string(metadataParts[0]))
	if len(fields) != 2 {
		return nil, config.Invalid("invalid remote platform response")
	}
	goos, arch := strings.ToLower(fields[0]), fields[1]
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	var receipt *update.Receipt
	if len(bytes.TrimSpace(metadataParts[1])) > 0 {
		var r update.Receipt
		if err = json.Unmarshal(metadataParts[1], &r); err != nil {
			return nil, config.Invalid("invalid remote installation receipt: %v", err)
		}
		receipt = &r
	}
	b, err := a.bundle(ctx, c, goos, arch, receipt)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	if !t.InstallApproved {
		if err = a.confirm(fmt.Sprintf("Install signed %s on %s at %s, retaining any prior binary?", b.Manifest.Version, t.Server.SSHAlias, t.Server.RemoteBinary)); err != nil {
			return nil, err
		}
	}
	root, err := a.shell(ctx, t, `umask 077; mktemp -d "${TMPDIR:-/tmp}/tdev-install.XXXXXX"`)
	if err != nil {
		return nil, err
	}
	stage := strings.TrimSpace(string(root))
	if !filepath.IsAbs(stage) || !strings.HasPrefix(filepath.Base(stage), "tdev-install.") {
		return nil, config.Invalid("invalid bootstrap staging path")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		batch := t
		batch.Batch = true
		if _, err := a.shell(cleanup, batch, "rm -rf -- "+tmux.Quote(stage)); err != nil {
			fmt.Fprintf(a.errOut, "Remote installation staging may remain at %s: %v\n", stage, err)
		}
	}()
	local, err := os.MkdirTemp("", "tdev-install-client-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(local)
	files := map[string][]byte{"manifest.json": b.ManifestBytes, "manifest.sig": b.Signature, "release.pub": b.PublicKey}
	for name, data := range files {
		path := filepath.Join(local, name)
		if err = os.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		if err = a.scp(ctx, t, path, t.Server.SSHAlias+":"+filepath.Join(stage, name)); err != nil {
			return nil, err
		}
	}
	remoteBinary := filepath.Join(stage, b.Artifact.File)
	if err = a.scp(ctx, t, b.Path, t.Server.SSHAlias+":"+remoteBinary); err != nil {
		return nil, err
	}
	quoted := tmux.Quote(remoteBinary)
	checksum, err := a.shell(ctx, t, "if command -v sha256sum >/dev/null 2>&1; then sha256sum -- "+quoted+"; elif command -v shasum >/dev/null 2>&1; then shasum -a 256 -- "+quoted+"; else printf 'sha256sum or shasum is required for bootstrap\\n' >&2; exit 7; fi")
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(string(checksum))
	if len(parts) < 1 || parts[0] != b.Artifact.SHA256 {
		return nil, update.VerifyError("remote uploaded binary checksum mismatch")
	}
	if _, err = a.shell(ctx, t, "chmod 700 -- "+quoted); err != nil {
		return nil, err
	}
	bootstrap := t
	bootstrap.Server.RemoteBinary = remoteBinary
	args := []string{"binary", "install", "--release-dir", stage, "--public-key", filepath.Join(stage, "release.pub"), "--version", b.Manifest.Version, "--destination", t.Server.RemoteBinary}
	if b.SourceURL != "" {
		args = append(args, "--source-url", b.SourceURL)
	}
	result, err := a.call(ctx, bootstrap, nil, args...)
	if err != nil {
		return nil, err
	}
	if _, err = a.call(ctx, t, nil, "binary", "platform"); err != nil {
		return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "installed binary did not pass post-install control verification: " + err.Error(), Details: result}
	}
	return result, nil
}

// Resolve state paths on the target: its home, XDG setting and canonical binary
// parent can differ from the client's, even when the destination string matches.
func remoteReceiptScript(destination string) string {
	return `destination=` + tmux.Quote(destination) + `
case "$destination" in '~/'*) destination="$HOME/${destination#'~/'}";; esac
legacy="$destination.tdev-install.json"
if parent=$(CDPATH= cd -- "$(dirname -- "$destination")" 2>/dev/null && pwd -P); then
 destination="${parent%/}/$(basename -- "$destination")"
 if command -v sha256sum >/dev/null 2>&1; then
  digest=$(printf '%s' "$destination" | sha256sum)
 elif command -v shasum >/dev/null 2>&1; then
  digest=$(printf '%s' "$destination" | shasum -a 256)
 else
  printf 'sha256sum or shasum is required for bootstrap\n' >&2; exit 7
 fi
 digest=${digest%% *}
 case "${XDG_STATE_HOME:-}" in /*) state="$XDG_STATE_HOME";; *) state="$HOME/.local/state";; esac
 receipt="$state/tdev/installations/$digest/receipt.json"
 if test -f "$receipt"; then cat -- "$receipt"; exit; fi
fi
if test -f "$legacy"; then cat -- "$legacy"; fi`
}
