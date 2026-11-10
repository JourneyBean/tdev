package cli

import (
	"context"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
)

func (a *app) apiExtras(local, session *cobra.Command) {
	a.command(local, "ps", "Inspect this user's existing daemon processes without starting services", cobra.NoArgs, func(ctx context.Context, _ *cobra.Command, _ []string) (any, error) {
		return daemon.InventoryForUser(ctx)
	})
	version := a.command(local, "version", "Machine protocol and capability handshake", cobra.NoArgs, func(_ context.Context, c *cobra.Command, _ []string) (any, error) {
		required, _ := c.Flags().GetStringArray("require")
		available := map[string]bool{}
		for _, capability := range versionData()["capabilities"].([]string) {
			available[capability] = true
		}
		for _, capability := range required {
			if !available[capability] {
				return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "missing capability %s; upgrade the server", capability)
			}
		}
		return versionData(), nil
	})
	version.Flags().StringArray("require", nil, "required API capability")
	attach := &cobra.Command{Use: "attach <project>", Short: "Attach an existing session using an identity-checked terminal", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if a.json || !terminalAvailable(a.in) {
			return config.Invalid("api session attach requires a terminal without --json")
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
	a.apiScratch(local)
	a.apiTransfer(local)
	a.apiUpdate(local)
}
func versionData() map[string]any {
	return map[string]any{"version": Version, "protocol_version": 1, "schema_version": 1, "capabilities": []string{"api-v1", "scratch-v1", "project-v1", "pane-layout-v1", "config-revision-v1", "session-v1", "attachment-v1", "transfer-v1", "transfer-file-directory-v1", "binary-install-v1", "post-install-v1", "mount-probe-nfc-v1", "ps-v1"}}
}
func (a *app) clientDoctor() doctorClient {
	paths := map[string]doctorDependency{}
	for _, name := range []string{"ssh", "scp", "sshfs", "umount"} {
		p, err := exec.LookPath(name)
		paths[name] = doctorDependency{Available: err == nil, Path: p}
	}
	return doctorClient{OS: runtime.GOOS, Arch: runtime.GOARCH, Version: Version, ProtocolVersion: 1, Dependencies: paths, ConfigRoot: a.store.Root}
}
