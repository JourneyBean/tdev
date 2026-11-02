package cli

import (
	"context"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"tdev/internal/config"
)

func (a *app) localExtras(local, session *cobra.Command) {
	a.command(local, "version", "Machine protocol and capability handshake", cobra.NoArgs, func(context.Context, *cobra.Command, []string) (any, error) { return versionData(), nil })
	attach := &cobra.Command{Use: "attach <project>", Short: "Attach an existing session using an identity-checked terminal", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if a.json || !terminalAvailable(a.in) {
			return config.Invalid("local session attach requires a terminal without --json")
		}
		p, err := a.store.Get(args[0])
		if err != nil {
			return err
		}
		sid, _ := c.Flags().GetString("session-id")
		generation, _ := c.Flags().GetString("generation")
		window, _ := c.Flags().GetString("window")
		token, _ := c.Flags().GetString("token")
		return a.manager().Attach(c.Context(), p, sid, generation, window, token, a.in, a.out, a.errOut)
	}}
	session.AddCommand(attach)
	for _, flag := range []string{"session-id", "generation", "token"} {
		attach.Flags().String(flag, "", "expected attachment identity")
		_ = attach.MarkFlagRequired(flag)
	}
	attach.Flags().String("window", "", "optional window ID")
	status := a.command(session, "attachment <project>", "Check whether a specific tmux client actually attached", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		p, err := a.store.Get(args[0])
		if err != nil {
			return nil, err
		}
		token, _ := c.Flags().GetString("token")
		return a.manager().Attachment(ctx, p, token)
	})
	status.Flags().String("token", "", "attachment identity")
	_ = status.MarkFlagRequired("token")
	a.localTransfer(local)
	a.localUpdate(local)
}
func versionData() map[string]any {
	return map[string]any{"version": Version, "protocol_version": 1, "schema_version": 1, "capabilities": []string{"project-v1", "config-revision-v1", "window-v1", "forward-config-v1", "session-v1", "attachment-v1", "transfer-v1", "binary-install-v1", "mount-probe-nfc-v1"}}
}
func (a *app) clientDoctor() any {
	paths := map[string]any{}
	for _, name := range []string{"ssh", "scp", "sshfs", "umount"} {
		p, err := exec.LookPath(name)
		paths[name] = map[string]any{"available": err == nil, "path": p}
	}
	return map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": Version, "protocol_version": 1, "dependencies": paths, "config_root": a.store.Root}
}
