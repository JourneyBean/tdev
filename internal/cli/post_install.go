package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/update"
)

type installHookResult struct {
	Name   string          `json:"name"`
	Status string          `json:"status"`
	Data   any             `json:"data,omitempty"`
	Error  *protocol.Error `json:"error,omitempty"`
}
type postInstallReport struct {
	Version string              `json:"version"`
	Hooks   []installHookResult `json:"hooks"`
}

func (a *app) postInstallCommand(parent *cobra.Command) {
	c := a.command(parent, "post-install", "Apply or retry this version's post-install hooks on this host", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		adopt, _ := c.Flags().GetBool("adopt-legacy-scratch")
		if c.Flags().Lookup("remote") != nil {
			remote, _ := c.Flags().GetString("remote")
			if remote != "" && remote != "local" {
				prefs, err := a.prefs()
				if err != nil {
					return nil, err
				}
				t, err := a.serverTarget(prefs, remote, "")
				if err != nil {
					return nil, err
				}
				args := []string{"binary", "post-install"}
				if adopt {
					args = append(args, "--adopt-legacy-scratch")
				}
				return a.call(ctx, t, nil, args...)
			}
		}
		return a.postInstall(ctx, adopt)
	})
	c.Flags().Bool("adopt-legacy-scratch", false, "claim legacy scratch sessions with no configuration owner")
	if parent == a.root {
		c.Flags().String("remote", "", "apply hooks on a registered server")
	}
}

func (a *app) postInstall(ctx context.Context, adopt bool) (postInstallReport, error) {
	report := postInstallReport{Version: Version, Hooks: []installHookResult{}}
	// Registration order is explicit. Each hook gets its own timeout and runs
	// even if a preceding hook failed.
	hooks := []struct {
		name string
		run  func(context.Context) (any, error)
	}{
		{"tmux-runtime", func(ctx context.Context) (any, error) { return a.manager().ReconcileRuntime(ctx, adopt) }},
		{"daemon-refresh", func(ctx context.Context) (any, error) {
			client, err := daemon.New(a.store.Root)
			if err != nil {
				return nil, err
			}
			exe, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return client.Manage(ctx, "refresh", exe, a.store.Root)
		}},
	}
	failed := false
	for _, hook := range hooks {
		hookCtx, cancel := context.WithTimeout(ctx, a.timeout)
		data, err := hook.run(hookCtx)
		cancel()
		row := installHookResult{Name: hook.name, Status: "succeeded", Data: data}
		if err != nil {
			row.Status = "failed"
			row.Error = protocol.Normalize(err)
			failed = true
		}
		report.Hooks = append(report.Hooks, row)
	}
	if failed {
		return report, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "post-install hooks need attention; retry with tdev post-install", Details: report}
	}
	return report, nil
}

func (a *app) targetPostInstall(ctx context.Context, destination string) (json.RawMessage, error) {
	// The installer keeps its per-destination lock through this subprocess.
	// Always execute the installed image, including identical-content installs.
	cmd := sshx.Command(ctx, destination, "--config-dir", a.store.Root, "--timeout", a.timeout.String(), "--json", "api", "binary", "post-install")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	execErr := cmd.Run()
	data, err := decodeAPI(stdout.Bytes())
	if err != nil {
		if execErr != nil && stdout.Len() == 0 {
			return nil, execErr
		}
		return nil, err
	}
	if execErr != nil {
		return nil, execErr
	}
	var report postInstallReport
	if json.Unmarshal(data, &report) != nil || report.Version == "" || len(report.Hooks) == 0 {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "installed program has no post-install hook support; invoke a compatible target's post-install command")
	}
	return data, nil
}

// Keep installation fields stable for existing API consumers; add hook results.
func installationReport(result any, post json.RawMessage, hookErr error) any {
	encoded, _ := json.Marshal(result)
	var fields map[string]any
	if json.Unmarshal(encoded, &fields) != nil || fields == nil {
		fields = map[string]any{"installation": result}
	}
	if len(post) > 0 {
		fields["post_install"] = post
	} else if hookErr != nil {
		e := protocol.Normalize(hookErr)
		if e.Details != nil {
			fields["post_install"] = e.Details
		} else {
			fields["post_install"] = map[string]any{"error": e}
		}
	}
	return fields
}

func (a *app) installBinary(ctx context.Context, b update.Bundle, destination string) (any, error) {
	var post json.RawMessage
	var hookErr error
	result, err := update.Install(ctx, a.store, b, destination, func(ctx context.Context, path string) error {
		post, hookErr = a.targetPostInstall(ctx, path)
		return hookErr
	})
	return finishInstallation(result, post, hookErr, err)
}
func (a *app) rollbackBinary(ctx context.Context, destination string) (any, error) {
	var post json.RawMessage
	var hookErr error
	result, err := update.Rollback(ctx, a.store, destination, func(ctx context.Context, path string) error {
		post, hookErr = a.targetPostInstall(ctx, path)
		return hookErr
	})
	return finishInstallation(result, post, hookErr, err)
}
func finishInstallation(result any, post json.RawMessage, hookErr, err error) (any, error) {
	if len(post) == 0 && hookErr == nil {
		return result, err
	}
	combined := installationReport(result, post, hookErr)
	if err != nil {
		e := *protocol.Normalize(err)
		e.Details = combined
		return combined, &e
	}
	return combined, nil
}
